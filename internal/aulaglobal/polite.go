package aulaglobal

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math/rand/v2"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Polite access to AulaGlobal. Every outgoing request (web service calls,
// pluginfile downloads, launch.php) goes through one per-process gate that
//   - spaces requests with a token bucket (DefaultMaxRPS, burst 2x),
//   - pauses ALL callers when the server answers 429/503 (honouring Retry-After),
//   - retries read-only requests a bounded number of times.
// Concurrent identical read-only web service calls share one upstream request and
// listing calls are kept in a small, short-lived in-memory cache.

const (
	// DefaultMaxRPS is the steady request rate. Moodle's own web client issues a few
	// requests per second per user and the mobile app bursts higher; 4 rps (240 per
	// minute) with a burst of 8 lets interactive tool calls run without delay while
	// keeping batch downloads and per-assignment lookups well below anything that
	// could be mistaken for abuse.
	DefaultMaxRPS = 4.0
	// MinMaxRPS and MaxMaxRPS bound AULAGLOBAL_MAX_RPS.
	MinMaxRPS = 0.1
	MaxMaxRPS = 100.0

	maxRateRetries = 2                 // retries after the first attempt on 429/503
	maxRetryAfter  = 120 * time.Second // cap for any server-requested or default pause
	defaultBackoff = 5 * time.Second   // 5 s, 10 s, ... when Retry-After is absent

	cacheTTL        = 45 * time.Second
	cacheMaxEntries = 32
	cacheMaxBytes   = 32 << 20
	cacheMaxEntry   = 8 << 20
)

// ErrRateLimited marks AulaGlobal asking us to slow down (HTTP 429 or 503). Test with errors.Is.
var ErrRateLimited = errors.New("AulaGlobal rate limited")

// RateLimitError is returned when AulaGlobal throttles us and waiting is not
// possible (retries used up, or the pause is longer than the caller can wait).
type RateLimitError struct{ RetryAfter time.Duration }

func (e *RateLimitError) Is(target error) bool { return target == ErrRateLimited }
func (e *RateLimitError) Error() string {
	secs := int64((e.RetryAfter + time.Second - 1) / time.Second)
	return fmt.Sprintf("AulaGlobal está limitando las peticiones; vuelve a intentarlo en %d s.", secs)
}

// ParseMaxRPS validates the value of AULAGLOBAL_MAX_RPS.
func ParseMaxRPS(s string) (float64, error) {
	v, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil || v != v || v < MinMaxRPS || v > MaxMaxRPS {
		return 0, fmt.Errorf("AULAGLOBAL_MAX_RPS must be a number between %g and %g", MinMaxRPS, MaxMaxRPS)
	}
	return v, nil
}

// Readonly web service functions: safe to retry, deduplicate and (for the subset
// in cacheable) cache. Anything not listed gets the rate limiter only, so a future
// function that writes to Moodle can never be replayed by accident. Today the
// client calls no function that writes.
var readOnlyFns = map[string]bool{
	siteInfoFn: true,
	"core_course_get_enrolled_courses_by_timeline_classification": true,
	"core_enrol_get_users_courses":                                true,
	"core_course_get_contents":                                    true,
	"core_course_get_courses_by_field":                            true,
	"mod_assign_get_assignments":                                  true,
	"mod_assign_get_submission_status":                            true,
}

// cacheable: listings. site_info is never cached (auth_status and setup must see a
// dead token at once) nor is the submission status (it changes when the student submits).
var cacheable = map[string]bool{
	"core_course_get_enrolled_courses_by_timeline_classification": true,
	"core_enrol_get_users_courses":                                true,
	"core_course_get_contents":                                    true,
	"core_course_get_courses_by_field":                            true,
	"mod_assign_get_assignments":                                  true,
}

type cacheEntry struct {
	body []byte
	at   time.Time
}

type flight struct {
	done    chan struct{}
	body    []byte
	err     error
	waiters int
	cancel  context.CancelFunc
}

type polite struct {
	mu                  sync.Mutex // gate state
	rate, burst, tokens float64
	last, until         time.Time
	now                 func() time.Time
	sleep               func(context.Context, time.Duration) error
	jitter              func() float64 // [0,1)

	cmu     sync.Mutex // cache and in-flight calls
	cache   map[string]cacheEntry
	flights map[string]*flight
}

func newPolite() *polite {
	p := &polite{now: time.Now, sleep: realSleep, jitter: rand.Float64, cache: map[string]cacheEntry{}, flights: map[string]*flight{}}
	p.rate, p.burst = DefaultMaxRPS, burstFor(DefaultMaxRPS)
	p.tokens, p.last = p.burst, p.now()
	return p
}

func burstFor(rps float64) float64 { return max(1, float64(int(rps*2+0.5))) }

func realSleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (c *Client) polite() *polite {
	c.politeOnce.Do(func() { c.pol = newPolite() })
	return c.pol
}

// SetMaxRPS sets the steady request rate (burst is twice the rate, at least 1).
// Call it before the first request; values outside MinMaxRPS..MaxMaxRPS are rejected.
func (c *Client) SetMaxRPS(rps float64) error {
	if rps != rps || rps < MinMaxRPS || rps > MaxMaxRPS {
		return fmt.Errorf("max requests per second must be between %g and %g", MinMaxRPS, MaxMaxRPS)
	}
	p := c.polite()
	p.mu.Lock()
	defer p.mu.Unlock()
	p.rate, p.burst = rps, burstFor(rps)
	p.tokens = min(p.tokens, p.burst)
	return nil
}

// wait blocks until one request may be sent: it sits out any global pause, then
// takes a token (reserving a future slot when the bucket is empty, so waiters are
// served in order). It returns early on context cancellation, and with a
// RateLimitError when the global pause is longer than the caller can wait.
func (p *polite) wait(ctx context.Context) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		p.mu.Lock()
		now := p.now()
		if d := p.until.Sub(now); d > 0 {
			p.mu.Unlock()
			if dl, ok := ctx.Deadline(); ok && time.Until(dl) < d {
				return &RateLimitError{RetryAfter: d}
			}
			if err := p.sleep(ctx, d); err != nil {
				return err
			}
			continue
		}
		if el := now.Sub(p.last).Seconds(); el > 0 {
			p.tokens = min(p.burst, p.tokens+el*p.rate)
		}
		p.last = now
		p.tokens--
		var delay time.Duration
		if p.tokens < 0 {
			delay = time.Duration(-p.tokens / p.rate * float64(time.Second))
		}
		p.mu.Unlock()
		if delay <= 0 {
			return nil
		}
		err := p.sleep(ctx, delay)
		p.mu.Lock()
		paused := p.until.After(p.now())
		if err != nil || paused {
			p.tokens = min(p.burst, p.tokens+1) // give the reservation back
		}
		p.mu.Unlock()
		if err != nil {
			return err
		}
		if !paused {
			return nil
		}
	}
}

// pause delays every request, from every goroutine, until now+d (never shortens an existing pause).
func (p *polite) pause(d time.Duration) {
	p.mu.Lock()
	if u := p.now().Add(d); u.After(p.until) {
		p.until = u
	}
	p.mu.Unlock()
}

// backoff turns a Retry-After header (seconds or HTTP-date) into a pause. Without
// a usable header it is 5 s doubling per attempt with +-25% jitter. Always <= maxRetryAfter.
func (p *polite) backoff(retryAfter string, attempt int) time.Duration {
	if d, ok := parseRetryAfter(retryAfter, p.now()); ok {
		return min(max(d, time.Second), maxRetryAfter)
	}
	d := min(defaultBackoff<<min(attempt, 5), maxRetryAfter)
	return time.Duration(float64(d) * (0.75 + 0.5*p.jitter()))
}

func parseRetryAfter(v string, now time.Time) (time.Duration, bool) {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0, false
	}
	if n, err := strconv.ParseInt(v, 10, 64); err == nil {
		if n < 0 {
			return 0, false
		}
		return time.Duration(min(n, 86400)) * time.Second, true
	}
	if t, err := http.ParseTime(v); err == nil {
		return t.Sub(now), true
	}
	return 0, false
}

type throttle struct {
	status     int
	retryAfter string
}

func throttled(status int) bool {
	return status == http.StatusTooManyRequests || status == http.StatusServiceUnavailable
}

func throttleOf(resp *http.Response) *throttle {
	if resp == nil || !throttled(resp.StatusCode) {
		return nil
	}
	return &throttle{resp.StatusCode, resp.Header.Get("Retry-After")}
}

// politely runs once through the shared gate. once performs one request and reports
// a non-nil throttle when the server answered 429/503; any other outcome is final
// (once keeps its own results). A throttle pauses everybody; with retry set the
// request is repeated up to maxRateRetries times, otherwise (or when retries are
// used up, or the pause is longer than ctx allows) a RateLimitError is returned.
func (c *Client) politely(ctx context.Context, retry bool, once func() (*throttle, error)) error {
	p := c.polite()
	for n := 0; ; n++ {
		if err := p.wait(ctx); err != nil {
			return err
		}
		th, err := once()
		if err != nil || th == nil {
			return err
		}
		d := p.backoff(th.retryAfter, n)
		p.pause(d)
		if !retry || n >= maxRateRetries {
			return &RateLimitError{RetryAfter: d}
		}
	}
}

// cacheKey identifies a call without ever holding the token in the clear: the
// token only enters a SHA-256 digest. The key is never logged.
func cacheKey(token, fn string, args url.Values) string {
	h := sha256.New()
	h.Write([]byte(token))
	h.Write([]byte{0})
	h.Write([]byte(fn))
	h.Write([]byte{0})
	h.Write([]byte(args.Encode())) // Encode sorts by key
	return hex.EncodeToString(h.Sum(nil))
}

func (p *polite) cached(key string) ([]byte, bool) {
	p.cmu.Lock()
	defer p.cmu.Unlock()
	e, ok := p.cache[key]
	if !ok {
		return nil, false
	}
	if !p.now().Before(e.at.Add(cacheTTL)) {
		delete(p.cache, key)
		return nil, false
	}
	return e.body, true
}

func (p *polite) store(key string, body []byte) {
	if len(body) > cacheMaxEntry {
		return
	}
	p.cmu.Lock()
	defer p.cmu.Unlock()
	now := p.now()
	p.cache[key] = cacheEntry{body, now}
	for {
		total := 0
		oldest := ""
		for k, e := range p.cache {
			if !now.Before(e.at.Add(cacheTTL)) {
				delete(p.cache, k) // expired entries go first
				continue
			}
			total += len(e.body)
			if oldest == "" || e.at.Before(p.cache[oldest].at) {
				oldest = k
			}
		}
		if (len(p.cache) <= cacheMaxEntries && total <= cacheMaxBytes) || oldest == "" {
			return
		}
		delete(p.cache, oldest)
	}
}

// shared runs fetch once for all concurrent callers with the same key. The fetch
// is detached from any single caller's cancellation (it inherits only the first
// caller's deadline) and is cancelled when every caller has gone away. A failed
// fetch is never remembered. A caller whose own context is still alive rejoins a
// fresh fetch when the shared one died of somebody else's deadline.
func (p *polite) shared(ctx context.Context, key string, fetch func(context.Context) ([]byte, error)) ([]byte, error) {
	for rejoin := 0; ; rejoin++ {
		p.cmu.Lock()
		f, ok := p.flights[key]
		if !ok {
			base := context.WithoutCancel(ctx)
			var fctx context.Context
			var cancel context.CancelFunc
			if dl, has := ctx.Deadline(); has {
				fctx, cancel = context.WithDeadline(base, dl)
			} else {
				fctx, cancel = context.WithCancel(base)
			}
			f = &flight{done: make(chan struct{}), cancel: cancel}
			p.flights[key] = f
			go func() {
				f.body, f.err = fetch(fctx)
				p.cmu.Lock()
				if p.flights[key] == f {
					delete(p.flights, key)
				}
				p.cmu.Unlock()
				close(f.done)
				cancel()
			}()
		}
		f.waiters++
		p.cmu.Unlock()
		select {
		case <-f.done:
			if (errors.Is(f.err, context.DeadlineExceeded) || errors.Is(f.err, context.Canceled)) && ctx.Err() == nil && rejoin < 2 {
				continue
			}
			return f.body, f.err
		case <-ctx.Done():
			p.cmu.Lock()
			f.waiters--
			if f.waiters == 0 {
				f.cancel()
				if p.flights[key] == f {
					delete(p.flights, key) // do not let new callers join a cancelled fetch
				}
			}
			p.cmu.Unlock()
			return nil, ctx.Err()
		}
	}
}
