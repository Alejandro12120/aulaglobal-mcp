package aulaglobal

import (
	"context"
	"crypto/md5"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeClock replaces the time source and the sleeper of the polite layer. Sleeping
// advances the clock instantly (or, with release set, waits for the test to let it
// go), so rate limiting and pauses can be asserted exactly without real delays.
type fakeClock struct {
	mu       sync.Mutex
	t        time.Time
	slept    []time.Duration
	release  chan struct{}      // when non-nil, sleep blocks until closed (or ctx done)
	sleeping chan time.Duration // when non-nil, receives each sleep as it starts
}

func (f *fakeClock) now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.t
}
func (f *fakeClock) advance(d time.Duration) {
	f.mu.Lock()
	f.t = f.t.Add(d)
	f.mu.Unlock()
}
func (f *fakeClock) sleeps() []time.Duration {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]time.Duration(nil), f.slept...)
}
func (f *fakeClock) sleep(ctx context.Context, d time.Duration) error {
	f.mu.Lock()
	f.slept = append(f.slept, d)
	end := f.t.Add(d)
	f.mu.Unlock()
	if f.sleeping != nil {
		f.sleeping <- d
	}
	if f.release != nil {
		select {
		case <-f.release:
		case <-ctx.Done():
			return ctx.Err()
		}
	} else if err := ctx.Err(); err != nil {
		return err
	}
	f.mu.Lock()
	if end.After(f.t) {
		f.t = end
	}
	f.mu.Unlock()
	return nil
}

func newFakeClient(base string) (*Client, *fakeClock) {
	c := New(base, StaticToken{Value: "secret"})
	fc := &fakeClock{t: time.Date(2030, 1, 1, 12, 0, 0, 0, time.UTC)}
	p := c.polite()
	p.now, p.sleep, p.jitter = fc.now, fc.sleep, func() float64 { return 0.5 } // jitter factor exactly 1
	p.last, p.tokens = fc.now(), p.burst
	return c, fc
}

func TestRateLimiterSpacing(t *testing.T) {
	c, fc := newFakeClient("http://unused")
	if e := c.SetMaxRPS(4); e != nil { // burst 8
		t.Fatal(e)
	}
	p := c.polite()
	p.tokens = p.burst
	for i := 0; i < 20; i++ {
		if e := p.wait(context.Background()); e != nil {
			t.Fatal(e)
		}
	}
	got := fc.sleeps()
	if len(got) != 12 {
		t.Fatalf("the burst of 8 must pass at once and the other 12 be spaced: %d sleeps %v", len(got), got)
	}
	for _, d := range got {
		if d != 250*time.Millisecond {
			t.Fatalf("spacing %v, want 250ms at 4 req/s: %v", d, got)
		}
	}
}

func TestRateLimiterRealTimeTolerant(t *testing.T) {
	c := New("http://unused", nil)
	if e := c.SetMaxRPS(50); e != nil {
		t.Fatal(e)
	}
	p := c.polite()
	p.mu.Lock()
	p.burst, p.tokens = 1, 1 // no burst: 6 requests need >= 5 intervals of 20 ms
	p.mu.Unlock()
	start := time.Now()
	for i := 0; i < 6; i++ {
		if e := p.wait(context.Background()); e != nil {
			t.Fatal(e)
		}
	}
	if d := time.Since(start); d < 70*time.Millisecond || d > 3*time.Second {
		t.Fatalf("6 requests at 50 req/s took %v, expected about 100ms", d)
	}
}

func TestRateLimiterContextCancel(t *testing.T) {
	c := New("http://unused", nil)
	p := c.polite()
	p.pause(time.Hour)
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(20 * time.Millisecond); cancel() }()
	start := time.Now()
	if e := p.wait(ctx); !errors.Is(e, context.Canceled) {
		t.Fatalf("got %v", e)
	}
	if time.Since(start) > 5*time.Second {
		t.Fatal("cancellation not prompt")
	}
	// Cancelled before a slot is free: the reservation is returned.
	c2 := New("http://unused", nil)
	p2 := c2.polite()
	p2.mu.Lock()
	p2.rate, p2.burst, p2.tokens = 0.1, 1, 0
	p2.mu.Unlock()
	ctx2, cancel2 := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel2()
	if e := p2.wait(ctx2); !errors.Is(e, context.DeadlineExceeded) {
		t.Fatalf("got %v", e)
	}
	p2.mu.Lock()
	tk := p2.tokens
	p2.mu.Unlock()
	if tk < -0.01 || tk > 0.5 {
		t.Fatalf("reservation not returned: tokens %v", tk)
	}
}

func TestMaxRPSValidation(t *testing.T) {
	for _, s := range []string{"", "abc", "0", "-1", "0.05", "101", "NaN", "Inf"} {
		if _, e := ParseMaxRPS(s); e == nil {
			t.Errorf("%q accepted", s)
		}
	}
	for s, want := range map[string]float64{"4": 4, " 0.5 ": 0.5, "100": 100} {
		if got, e := ParseMaxRPS(s); e != nil || got != want {
			t.Errorf("%q: %v %v", s, got, e)
		}
	}
	c := New("http://unused", nil)
	if c.SetMaxRPS(0) == nil || c.SetMaxRPS(1000) == nil {
		t.Fatal("out-of-range rate accepted")
	}
	if p := c.polite(); p.rate != DefaultMaxRPS || p.burst != 8 {
		t.Fatalf("defaults changed: %v/%v", p.rate, p.burst)
	}
}

// throttleServer answers the first n requests with status (and header), then a valid JSON body.
func throttleServer(n int32, status int, retryAfter func() string, hits *atomic.Int32, times *[]time.Time, fc *fakeClock) *httptest.Server {
	var mu sync.Mutex
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := hits.Add(1)
		if times != nil {
			mu.Lock()
			*times = append(*times, fc.now())
			mu.Unlock()
		}
		if h <= n {
			if retryAfter != nil {
				if v := retryAfter(); v != "" {
					w.Header().Set("Retry-After", v)
				}
			}
			w.WriteHeader(status)
			return
		}
		fmt.Fprint(w, `{"sitename":"x"}`)
	}))
}

func TestRetryAfterSeconds(t *testing.T) {
	for _, status := range []int{429, 503} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			var hits atomic.Int32
			var times []time.Time
			c, fc := newFakeClient("")
			ts := throttleServer(1, status, func() string { return "3" }, &hits, &times, fc)
			defer ts.Close()
			c.Base = ts.URL
			var out struct{ Sitename string }
			if e := c.WS(context.Background(), siteInfoFn, nil, &out); e != nil || out.Sitename != "x" {
				t.Fatal(e)
			}
			if hits.Load() != 2 {
				t.Fatalf("hits %d", hits.Load())
			}
			if got := fc.sleeps(); len(got) != 1 || got[0] != 3*time.Second {
				t.Fatalf("slept %v, want [3s]", got)
			}
			if gap := times[1].Sub(times[0]); gap < 3*time.Second {
				t.Fatalf("retry arrived %v after the 429", gap)
			}
		})
	}
}

func TestRetryAfterHTTPDate(t *testing.T) {
	var hits atomic.Int32
	c, fc := newFakeClient("")
	ts := throttleServer(1, 429, func() string { return fc.now().Add(10 * time.Second).UTC().Format(http.TimeFormat) }, &hits, nil, fc)
	defer ts.Close()
	c.Base = ts.URL
	if e := c.WS(context.Background(), siteInfoFn, nil, &struct{}{}); e != nil {
		t.Fatal(e)
	}
	if got := fc.sleeps(); len(got) != 1 || got[0] != 10*time.Second {
		t.Fatalf("slept %v, want [10s]", got)
	}
}

func TestRetryAfterParsing(t *testing.T) {
	now := time.Date(2030, 1, 1, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		in   string
		want time.Duration
		ok   bool
	}{
		{"7", 7 * time.Second, true}, {" 0 ", 0, true}, {"-3", 0, false}, {"", 0, false}, {"soon", 0, false},
		{now.Add(30 * time.Second).Format(http.TimeFormat), 30 * time.Second, true},
		{now.Add(-time.Minute).Format(http.TimeFormat), -time.Minute, true},
		{"99999999999999999999", 0, false},
	} {
		got, ok := parseRetryAfter(tc.in, now)
		if got != tc.want || ok != tc.ok {
			t.Errorf("%q: %v %v, want %v %v", tc.in, got, ok, tc.want, tc.ok)
		}
	}
	p := newPolite()
	p.now, p.jitter = func() time.Time { return now }, func() float64 { return 0.5 }
	if d := p.backoff("99999", 0); d != maxRetryAfter {
		t.Fatalf("not capped: %v", d)
	}
	if d := p.backoff("0", 0); d != time.Second {
		t.Fatalf("zero not floored: %v", d)
	}
	if d := p.backoff("", 0); d != 5*time.Second {
		t.Fatalf("default %v", d)
	}
	if d := p.backoff("", 1); d != 10*time.Second {
		t.Fatalf("exponential %v", d)
	}
	if d := p.backoff("", 9); d != maxRetryAfter {
		t.Fatalf("default not capped: %v", d)
	}
	p.jitter = func() float64 { return 0 }
	if d := p.backoff("", 0); d != 3750*time.Millisecond {
		t.Fatalf("jitter low %v", d)
	}
}

func TestRetriesAreBounded(t *testing.T) {
	var hits atomic.Int32
	c, fc := newFakeClient("")
	ts := throttleServer(1000, 503, nil, &hits, nil, fc)
	defer ts.Close()
	c.Base = ts.URL
	e := c.WS(context.Background(), siteInfoFn, nil, &struct{}{})
	var rl *RateLimitError
	if !errors.Is(e, ErrRateLimited) || !errors.As(e, &rl) {
		t.Fatalf("got %v", e)
	}
	if hits.Load() != 3 {
		t.Fatalf("1 attempt + 2 retries expected, got %d requests", hits.Load())
	}
	if got := fc.sleeps(); len(got) != 2 || got[0] != 5*time.Second || got[1] != 10*time.Second {
		t.Fatalf("backoff %v, want [5s 10s]", got)
	}
	if rl.RetryAfter != 20*time.Second || !strings.Contains(e.Error(), "AulaGlobal está limitando las peticiones; vuelve a intentarlo en 20 s") {
		t.Fatalf("message: %v", e)
	}
	if strings.Contains(e.Error(), "secret") {
		t.Fatal("token in error")
	}
}

func TestGlobalPauseAffectsParallelRequests(t *testing.T) {
	var hits atomic.Int32
	c, fc := newFakeClient("")
	fc.release = make(chan struct{})
	fc.sleeping = make(chan time.Duration, 8)
	ts := throttleServer(1, 429, func() string { return "7" }, &hits, nil, fc)
	defer ts.Close()
	c.Base = ts.URL
	errs := make(chan error, 2)
	go func() { errs <- c.WS(context.Background(), siteInfoFn, nil, &struct{}{}) }()
	if d := <-fc.sleeping; d != 7*time.Second {
		t.Fatalf("first request paused %v", d)
	}
	// A different call, started while the pause is active, must not reach the server.
	go func() { errs <- c.WS(context.Background(), "core_course_get_contents", nil, &struct{}{}) }()
	select {
	case d := <-fc.sleeping:
		if d != 7*time.Second {
			t.Fatalf("second request paused %v", d)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("second request did not wait for the global pause")
	}
	if hits.Load() != 1 {
		t.Fatalf("a request was sent during the pause: %d hits", hits.Load())
	}
	close(fc.release)
	for i := 0; i < 2; i++ {
		if e := <-errs; e != nil {
			t.Fatal(e)
		}
	}
	if hits.Load() != 3 {
		t.Fatalf("hits %d, want 3", hits.Load())
	}
}

func TestContextHandlingDuringPause(t *testing.T) {
	var hits atomic.Int32
	c, fc := newFakeClient("")
	ts := throttleServer(1000, 429, func() string { return "100" }, &hits, nil, fc)
	defer ts.Close()
	c.Base = ts.URL

	// Pause longer than the caller's remaining time: fail at once with the clear error.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	e := c.WS(ctx, siteInfoFn, nil, &struct{}{})
	var rl *RateLimitError
	if !errors.As(e, &rl) || rl.RetryAfter != 100*time.Second || !strings.Contains(e.Error(), "en 100 s") {
		t.Fatalf("got %v", e)
	}
	if len(fc.sleeps()) != 0 || hits.Load() != 1 {
		t.Fatalf("waited or retried despite insufficient context: %v hits=%d", fc.sleeps(), hits.Load())
	}

	// No deadline: cancelling while paused returns promptly with the context error.
	fc.release = make(chan struct{})
	fc.sleeping = make(chan time.Duration, 4)
	ctx2, cancel2 := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- c.WS(ctx2, "core_course_get_contents", nil, &struct{}{}) }()
	<-fc.sleeping
	cancel2()
	select {
	case e := <-done:
		if !errors.Is(e, context.Canceled) {
			t.Fatalf("got %v", e)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancellation ignored during pause")
	}
}

func TestMoodleJSONErrorIsNotARateLimit(t *testing.T) {
	var hits atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		fmt.Fprint(w, `{"exception":"moodle_exception","errorcode":"toomanyrequests","message":"slow down"}`)
	}))
	defer ts.Close()
	c, fc := newFakeClient(ts.URL)
	e := c.WS(context.Background(), "core_course_get_contents", nil, &struct{}{})
	var api *APIError
	if !errors.As(e, &api) || errors.Is(e, ErrRateLimited) {
		t.Fatalf("got %v", e)
	}
	if hits.Load() != 1 || len(fc.sleeps()) != 0 {
		t.Fatal("HTTP 200 JSON error caused a retry or pause")
	}
}

func TestOtherStatusesAreNotRateLimits(t *testing.T) {
	var hits atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(500)
	}))
	defer ts.Close()
	c, fc := newFakeClient(ts.URL)
	e := c.WS(context.Background(), siteInfoFn, nil, &struct{}{})
	if e == nil || errors.Is(e, ErrRateLimited) || hits.Load() != 1 || len(fc.sleeps()) != 0 {
		t.Fatalf("%v hits=%d", e, hits.Load())
	}
}

func TestUnknownFunctionIsNeverRetriedOrShared(t *testing.T) {
	var hits atomic.Int32
	c, fc := newFakeClient("")
	ts := throttleServer(1000, 429, func() string { return "1" }, &hits, nil, fc)
	defer ts.Close()
	c.Base = ts.URL
	e := c.WS(context.Background(), "core_message_send_instant_messages", nil, &struct{}{})
	if !errors.Is(e, ErrRateLimited) || hits.Load() != 1 {
		t.Fatalf("non read-only function retried: %v hits=%d", e, hits.Load())
	}
	// ...but the pause still applies to what comes next.
	if _, ok := p429Pending(c); !ok {
		t.Fatal("429 did not pause the process")
	}
}

func p429Pending(c *Client) (time.Duration, bool) {
	p := c.polite()
	p.mu.Lock()
	defer p.mu.Unlock()
	d := p.until.Sub(p.now())
	return d, d > 0
}

func TestSingleflight(t *testing.T) {
	var hits atomic.Int32
	gate := make(chan struct{})
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		<-gate
		fmt.Fprint(w, `{"sitename":"shared"}`)
	}))
	defer ts.Close()
	c, _ := newFakeClient(ts.URL)
	const n = 10
	var wg sync.WaitGroup
	results := make([]string, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var out struct{ Sitename string }
			if e := c.WS(context.Background(), siteInfoFn, nil, &out); e != nil {
				t.Error(e)
			}
			results[i] = out.Sitename
		}()
	}
	key := cacheKey("secret", siteInfoFn, nil)
	deadline := time.Now().Add(5 * time.Second)
	for {
		p := c.polite()
		p.cmu.Lock()
		f := p.flights[key]
		w := 0
		if f != nil {
			w = f.waiters
		}
		p.cmu.Unlock()
		if w == n {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("only %d callers joined the in-flight call", w)
		}
		time.Sleep(time.Millisecond)
	}
	close(gate)
	wg.Wait()
	if hits.Load() != 1 {
		t.Fatalf("%d concurrent identical calls caused %d upstream requests", n, hits.Load())
	}
	for _, r := range results {
		if r != "shared" {
			t.Fatalf("results %v", results)
		}
	}
	// site_info is deduplicated while in flight but never cached.
	if e := c.WS(context.Background(), siteInfoFn, nil, &struct{}{}); e != nil || hits.Load() != 2 {
		t.Fatalf("site_info must not be cached: %v hits=%d", e, hits.Load())
	}
}

func TestSingleflightSurvivesOneCallerLeaving(t *testing.T) {
	var hits atomic.Int32
	gate := make(chan struct{})
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		<-gate
		fmt.Fprint(w, `{"sitename":"shared"}`)
	}))
	defer ts.Close()
	c, _ := newFakeClient(ts.URL)
	ctx1, cancel1 := context.WithCancel(context.Background())
	e1 := make(chan error, 1)
	go func() { e1 <- c.WS(ctx1, siteInfoFn, nil, &struct{}{}) }()
	for hits.Load() == 0 {
		time.Sleep(time.Millisecond)
	}
	e2 := make(chan error, 1)
	go func() { e2 <- c.WS(context.Background(), siteInfoFn, nil, &struct{}{}) }()
	key := cacheKey("secret", siteInfoFn, nil)
	for {
		p := c.polite()
		p.cmu.Lock()
		w := p.flights[key].waiters
		p.cmu.Unlock()
		if w == 2 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	cancel1()
	if e := <-e1; !errors.Is(e, context.Canceled) {
		t.Fatalf("first caller: %v", e)
	}
	close(gate)
	if e := <-e2; e != nil {
		t.Fatalf("the caller that stayed must not inherit the other's cancellation: %v", e)
	}
	if hits.Load() != 1 {
		t.Fatalf("hits %d", hits.Load())
	}
}

func TestCacheTTLAndErrorsNotCached(t *testing.T) {
	var hits atomic.Int32
	var fail atomic.Bool
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if fail.Load() {
			fmt.Fprint(w, `{"exception":"x","errorcode":"invalidtoken"}`)
			return
		}
		fmt.Fprint(w, `[{"id":1}]`)
	}))
	defer ts.Close()
	c, fc := newFakeClient(ts.URL)
	get := func() error {
		var out []struct{ ID int }
		e := c.WS(context.Background(), "core_course_get_contents", nil, &out)
		if e == nil && (len(out) != 1 || out[0].ID != 1) {
			return errors.New("bad body")
		}
		return e
	}
	fail.Store(true)
	if e := get(); !errors.Is(e, ErrTokenExpired) {
		t.Fatalf("got %v", e)
	}
	fail.Store(false)
	if e := get(); e != nil || hits.Load() != 2 {
		t.Fatalf("the error was cached: %v hits=%d", e, hits.Load())
	}
	if e := get(); e != nil || hits.Load() != 2 {
		t.Fatalf("second call must come from the cache: %v hits=%d", e, hits.Load())
	}
	fc.advance(cacheTTL - time.Second)
	if e := get(); e != nil || hits.Load() != 2 {
		t.Fatalf("entry expired early: %v hits=%d", e, hits.Load())
	}
	fc.advance(2 * time.Second)
	if e := get(); e != nil || hits.Load() != 3 {
		t.Fatalf("entry did not expire: %v hits=%d", e, hits.Load())
	}
	// A dead token is noticed by the very next uncached call.
	fail.Store(true)
	if e := c.WS(context.Background(), siteInfoFn, nil, &struct{}{}); !errors.Is(e, ErrTokenExpired) {
		t.Fatalf("got %v", e)
	}
}

func TestCacheKeyAndSeparation(t *testing.T) {
	a := cacheKey("tok", "fn", map[string][]string{"b": {"2"}, "a": {"1"}})
	b := cacheKey("tok", "fn", map[string][]string{"a": {"1"}, "b": {"2"}})
	if a != b {
		t.Fatal("key depends on map order")
	}
	if a == cacheKey("tok2", "fn", map[string][]string{"a": {"1"}, "b": {"2"}}) || a == cacheKey("tok", "fn", map[string][]string{"a": {"1"}, "b": {"3"}}) || a == cacheKey("tok", "fn2", map[string][]string{"a": {"1"}, "b": {"2"}}) {
		t.Fatal("different token/params/function share a key")
	}
	if strings.Contains(cacheKey("hunter2-token", "fn", nil), "hunter2") {
		t.Fatal("token visible in key")
	}

	// Same call, different token or parameters: separate upstream requests.
	var hits atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		fmt.Fprint(w, `{}`)
	}))
	defer ts.Close()
	c, _ := newFakeClient(ts.URL)
	ctx := context.Background()
	for _, id := range []string{"1", "2", "1", "2"} {
		_ = c.WS(ctx, "core_course_get_contents", map[string][]string{"courseid": {id}}, &struct{}{})
	}
	if hits.Load() != 2 {
		t.Fatalf("params not separated: %d hits", hits.Load())
	}
	c.Tokens = StaticToken{Value: "other-token"}
	_ = c.WS(ctx, "core_course_get_contents", map[string][]string{"courseid": {"1"}}, &struct{}{})
	if hits.Load() != 3 {
		t.Fatalf("token not part of the key: %d hits", hits.Load())
	}
}

func TestCacheIsBounded(t *testing.T) {
	p := newPolite()
	var tick int64
	p.now = func() time.Time {
		tick++
		return time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC).Add(time.Duration(tick) * time.Millisecond)
	}
	for i := 0; i < 3*cacheMaxEntries; i++ {
		p.store(fmt.Sprint(i), []byte("{}"))
	}
	if len(p.cache) > cacheMaxEntries {
		t.Fatalf("%d entries", len(p.cache))
	}
	p.store("huge", make([]byte, cacheMaxEntry+1))
	if _, ok := p.cached("huge"); ok {
		t.Fatal("oversized response cached")
	}
	big := make([]byte, cacheMaxEntry)
	for i := 0; i < 8; i++ {
		p.store(fmt.Sprint("big", i), big)
	}
	total := 0
	for _, e := range p.cache {
		total += len(e.body)
	}
	if total > cacheMaxBytes {
		t.Fatalf("%d bytes cached", total)
	}
	if _, ok := p.cached("big7"); !ok {
		t.Fatal("newest entry evicted")
	}
}

func TestDownloadHonoursRetryAfter(t *testing.T) {
	var hits atomic.Int32
	c, fc := newFakeClient("")
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hits.Add(1) == 1 {
			w.Header().Set("Retry-After", "2")
			w.WriteHeader(429)
			return
		}
		io.WriteString(w, "file body")
	}))
	defer ts.Close()
	c.Base = ts.URL
	resp, e := c.Download(context.Background(), File{URL: ts.URL + "/pluginfile.php/1/x.pdf"})
	if e != nil {
		t.Fatal(e)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(b) != "file body" || hits.Load() != 2 {
		t.Fatalf("%q hits=%d", b, hits.Load())
	}
	if got := fc.sleeps(); len(got) != 1 || got[0] != 2*time.Second {
		t.Fatalf("slept %v", got)
	}
	// Persistent throttling gives the user-facing error and stops after the bound.
	hits.Store(0)
	ts2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Retry-After", "1")
		w.WriteHeader(503)
	}))
	defer ts2.Close()
	c.Base = ts2.URL
	if _, e = c.Download(context.Background(), File{URL: ts2.URL + "/pluginfile.php/1/x.pdf"}); !errors.Is(e, ErrRateLimited) || hits.Load() != 3 {
		t.Fatalf("%v hits=%d", e, hits.Load())
	}
}

func TestLaunchHonoursRetryAfterAndLimiter(t *testing.T) {
	var hits atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hits.Add(1) == 1 {
			w.Header().Set("Retry-After", "4")
			w.WriteHeader(429)
			return
		}
		h := md5.Sum([]byte("http://" + r.Host + r.URL.Query().Get("passport")))
		w.Header().Set("Location", "moodlemobile://token="+base64.StdEncoding.EncodeToString([]byte(fmt.Sprintf("%x:::token:::private", h))))
		w.WriteHeader(303)
	}))
	defer ts.Close()
	c, fc := newFakeClient(ts.URL)
	token, _, e := c.TokenFromCookie(context.Background(), "fake")
	if e != nil || token != "token" || hits.Load() != 2 {
		t.Fatalf("%q %v hits=%d", token, e, hits.Load())
	}
	if got := fc.sleeps(); len(got) != 1 || got[0] != 4*time.Second {
		t.Fatalf("slept %v", got)
	}
}

func TestAssignmentStatusStopsWhenThrottled(t *testing.T) {
	var statusHits atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		switch r.Form.Get("wsfunction") {
		case "mod_assign_get_assignments":
			fmt.Fprint(w, `{"courses":[{"id":1,"fullname":"A","assignments":[{"id":1,"cmid":1,"name":"a"},{"id":2,"cmid":2,"name":"b"},{"id":3,"cmid":3,"name":"c"},{"id":4,"cmid":4,"name":"d"},{"id":5,"cmid":5,"name":"e"},{"id":6,"cmid":6,"name":"f"},{"id":7,"cmid":7,"name":"g"},{"id":8,"cmid":8,"name":"h"},{"id":9,"cmid":9,"name":"i"},{"id":10,"cmid":10,"name":"j"}]}]}`)
		case "core_course_get_courses_by_field":
			fmt.Fprint(w, `{"courses":[{"id":1,"fullname":"A"}]}`)
		default:
			statusHits.Add(1)
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(429)
		}
	}))
	defer ts.Close()
	c, _ := newFakeClient(ts.URL)
	_, e := c.Assignments(context.Background(), AssignmentInput{CourseIDs: []int64{1}, Status: "all", IncludeSubmissionStatus: true}, func(s string) string { return s })
	if !errors.Is(e, ErrRateLimited) {
		t.Fatalf("got %v", e)
	}
	// 4 workers x 3 attempts at most; the batch must not keep going after the first failure.
	if statusHits.Load() > 12 {
		t.Fatalf("%d status requests", statusHits.Load())
	}
}

func TestSingleflightFollowerOutlivesLeaderDeadline(t *testing.T) {
	var hits atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hits.Add(1) == 1 {
			select { // first request is slow: the leader gives up first
			case <-time.After(400 * time.Millisecond):
			case <-r.Context().Done():
			}
		}
		fmt.Fprint(w, `{"sitename":"ok"}`)
	}))
	defer ts.Close()
	c, _ := newFakeClient(ts.URL)
	lctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	leader := make(chan error, 1)
	go func() { leader <- c.WS(lctx, siteInfoFn, nil, &struct{}{}) }()
	for hits.Load() == 0 {
		time.Sleep(time.Millisecond)
	}
	follower := make(chan error, 1)
	go func() {
		var out struct{ Sitename string }
		e := c.WS(context.Background(), siteInfoFn, nil, &out)
		if e == nil && out.Sitename != "ok" {
			e = errors.New("bad body")
		}
		follower <- e
	}()
	if e := <-leader; !errors.Is(e, context.DeadlineExceeded) {
		t.Fatalf("leader: %v", e)
	}
	if e := <-follower; e != nil {
		t.Fatalf("follower inherited the leader's deadline: %v", e)
	}
}
