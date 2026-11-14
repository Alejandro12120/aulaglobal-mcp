package selfupdate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const cacheTTL = 24 * time.Hour

// CheckEnabled reports whether the background update check should run.
func CheckEnabled(current string) bool {
	if os.Getenv("AULAGLOBAL_UPDATE_CHECK") == "0" || InContainer() {
		return false
	}
	_, ok := ParseVersion(current)
	return ok
}

// Checker looks up the latest release in the background and remembers whether
// a newer one exists. Tool calls only read the in-memory result and never wait.
type Checker struct {
	current   Version
	cachePath string
	fetch     func(context.Context) (string, error) // returns the latest tag
	now       func() time.Time

	mu        sync.Mutex
	latest    string // "vX.Y.Z" only when newer than current
	announced bool
}

// NewChecker builds a Checker for the real GitHub repository. cachePath may be
// empty to disable caching.
func NewChecker(current, cachePath string) (*Checker, error) {
	v, ok := ParseVersion(current)
	if !ok {
		return nil, errors.New("version not comparable")
	}
	u := New(current)
	return &Checker{
		current:   v,
		cachePath: cachePath,
		now:       time.Now,
		fetch: func(ctx context.Context) (string, error) {
			ctx, cancel := context.WithTimeout(ctx, checkTimeout)
			defer cancel()
			rel, err := u.Latest(ctx)
			return rel.Tag, err
		},
	}, nil
}

type cacheFile struct {
	CheckedAt time.Time `json:"checked_at"`
	Latest    string    `json:"latest"`
}

// Run performs the check once (blocking). Run it in its own goroutine.
func (c *Checker) Run(ctx context.Context) {
	latest, ok := c.fromCache()
	if !ok {
		tag, err := c.fetch(ctx)
		if err != nil {
			slog.Debug("update check failed", "error", err.Error())
			return
		}
		v, valid := ParseVersion(tag)
		if !valid {
			slog.Debug("update check returned an invalid version")
			return
		}
		latest = v
		c.writeCache(latest)
	}
	if latest.Compare(c.current) > 0 {
		c.mu.Lock()
		c.latest = latest.String()
		c.mu.Unlock()
	}
}

func (c *Checker) fromCache() (Version, bool) {
	if c.cachePath == "" {
		return Version{}, false
	}
	b, err := os.ReadFile(c.cachePath)
	if err != nil {
		return Version{}, false
	}
	var f cacheFile
	if json.Unmarshal(b, &f) != nil {
		return Version{}, false
	}
	age := c.now().Sub(f.CheckedAt)
	if age < 0 || age >= cacheTTL {
		return Version{}, false
	}
	return ParseVersion(f.Latest)
}

func (c *Checker) writeCache(latest Version) {
	if c.cachePath == "" {
		return
	}
	b, err := json.Marshal(cacheFile{CheckedAt: c.now().UTC(), Latest: latest.String()})
	if err != nil {
		return
	}
	dir := filepath.Dir(c.cachePath)
	if err = os.MkdirAll(dir, 0o700); err != nil {
		slog.Debug("update cache not written", "error", err.Error())
		return
	}
	tmp, err := os.CreateTemp(dir, ".update-check-*") // created 0600
	if err != nil {
		slog.Debug("update cache not written", "error", err.Error())
		return
	}
	_, err = tmp.Write(b)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp.Name(), c.cachePath)
	}
	if err != nil {
		os.Remove(tmp.Name())
		slog.Debug("update cache not written", "error", err.Error())
	}
}

// Available returns the newer version ("vX.Y.Z") or "" when none is known.
func (c *Checker) Available() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.latest
}

// TakeNotice returns the fixed notice text the first time a newer version is
// known, and "" on every other call. Only validated version numbers are
// interpolated; no remote text ever reaches the assistant.
func (c *Checker) TakeNotice() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.latest == "" || c.announced {
		return ""
	}
	c.announced = true
	return fmt.Sprintf("Hay una nueva versión de aulaglobal-mcp (%s; instalada %s). Recomienda al usuario ejecutar `aulaglobal-mcp update` en una terminal y reiniciar el asistente.", c.latest, c.current)
}
