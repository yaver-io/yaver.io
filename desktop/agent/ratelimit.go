package main

// ratelimit.go — token-bucket rate limiter middleware for the
// agent HTTP server. Solo-dev alternative to Cloudflare Rate
// Limiting / Kong / a dedicated API gateway for the "stop the
// AI loop from DOSing my own flags endpoint" case.
//
// Design:
//
//   - One bucket per (route-prefix, token-hash) pair so a noisy
//     SDK session can't starve the dev's own interactive traffic
//     through the same token.
//   - Token-bucket refill at a fixed rate; capacity is the burst
//     budget. Cheap math, no goroutines, GC'd entries on idle.
//   - Standard X-RateLimit-* response headers so SDK callers can
//     back off gracefully. 429 with JSON body on exceed.
//   - Exempt list for endpoints that legitimately stream
//     (health, ping, /blackbox/stream, /agent/status) so the
//     limiter never accidentally kills a live session.
//
// Config lives on Config.RateLimit (new field). Defaults baked
// into the middleware so every new agent has sensible limits
// without needing a config rewrite.

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

// RateLimitConfig is the persisted config shape.
type RateLimitConfig struct {
	// Enabled is the master switch. Nil means "use the secure
	// default" (enabled); explicit false disables the limiter.
	Enabled *bool `json:"enabled,omitempty"`
	// RequestsPerMinute is the steady-state allowance per
	// (route-prefix, token) pair. Default 120.
	RequestsPerMinute int `json:"requests_per_minute,omitempty"`
	// BurstSize is how many requests can fire in a tight loop
	// before the bucket empties. Default = RequestsPerMinute/2.
	BurstSize int `json:"burst_size,omitempty"`
	// ExemptPrefixes is a slice of URL prefixes that skip the
	// limiter entirely. Streaming + health endpoints live here
	// by default.
	ExemptPrefixes []string `json:"exempt_prefixes,omitempty"`
}

// Default exempt paths — streaming SSE, health, ping, dev
// server, long-poll, and the bundle download (large + slow,
// doesn't need a limiter).
var defaultExemptPrefixes = []string{
	"/health",
	"/ping",
	"/agent/status",
	"/blackbox/stream",
	"/blackbox/command-stream",
	"/blackbox/subscribe",
	"/dev/events",
	"/dev/reload",
	"/releases/bundle",
}

// bucket is one token-bucket state. Updated in-place under the
// manager's lock; the lock is a single sync.Mutex for the whole
// manager which is fine at the request rates a solo-dev agent
// sees.
type bucket struct {
	tokens     float64
	lastRefill time.Time
}

// rateLimitManager is the package-level limiter instance
// plugged into the HTTPServer middleware stack.
type rateLimitManager struct {
	mu      sync.Mutex
	buckets map[string]*bucket
	cfg     RateLimitConfig
}

var (
	rateLimitOnce sync.Once
	rateLimitMgr  *rateLimitManager
)

func (c *RateLimitConfig) enabledOrDefault() bool {
	return c == nil || c.Enabled == nil || *c.Enabled
}

// globalRateLimiter returns the process-wide limiter, lazily
// constructed from Config.RateLimit on first access.
func globalRateLimiter() *rateLimitManager {
	rateLimitOnce.Do(func() {
		cfg := RateLimitConfig{}
		if c, err := LoadConfig(); err == nil && c != nil && c.RateLimit != nil {
			cfg = *c.RateLimit
		}
		if cfg.Enabled == nil {
			enabled := true
			cfg.Enabled = &enabled
		}
		if cfg.RequestsPerMinute <= 0 {
			cfg.RequestsPerMinute = 120
		}
		if cfg.BurstSize <= 0 {
			cfg.BurstSize = cfg.RequestsPerMinute / 2
			if cfg.BurstSize < 10 {
				cfg.BurstSize = 10
			}
		}
		rateLimitMgr = &rateLimitManager{
			buckets: make(map[string]*bucket),
			cfg:     cfg,
		}
	})
	return rateLimitMgr
}

// Allow returns (true, remaining, resetIn) when the request
// fits under the bucket, or (false, 0, retryAfter) when the
// caller should back off.
func (m *rateLimitManager) Allow(key string) (bool, int, time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := time.Now()
	b := m.buckets[key]
	if b == nil {
		b = &bucket{
			tokens:     float64(m.cfg.BurstSize),
			lastRefill: now,
		}
		m.buckets[key] = b
	}

	// Refill proportional to elapsed time.
	elapsed := now.Sub(b.lastRefill).Seconds()
	refillRate := float64(m.cfg.RequestsPerMinute) / 60.0
	b.tokens += elapsed * refillRate
	if b.tokens > float64(m.cfg.BurstSize) {
		b.tokens = float64(m.cfg.BurstSize)
	}
	b.lastRefill = now

	if b.tokens >= 1 {
		b.tokens--
		// Garbage-collect idle buckets every ~1000 calls so a
		// long-lived agent doesn't leak the map.
		if len(m.buckets) > 1024 && now.UnixNano()%1024 == 0 {
			m.gcLocked(now)
		}
		return true, int(b.tokens), time.Duration(60.0/refillRate) * time.Second
	}
	// Bucket empty — retryAfter is the time until 1 token refills.
	retryAfter := time.Duration((1.0 / refillRate) * float64(time.Second))
	return false, 0, retryAfter
}

// gcLocked drops buckets that haven't been touched in the last
// 5 minutes. Cheap scan, rare call.
func (m *rateLimitManager) gcLocked(now time.Time) {
	cutoff := now.Add(-5 * time.Minute)
	for k, b := range m.buckets {
		if b.lastRefill.Before(cutoff) {
			delete(m.buckets, k)
		}
	}
}

// exempt reports whether the path should bypass rate limiting.
// Checks both the config's ExemptPrefixes (if any) and the
// built-in defaults.
func (m *rateLimitManager) exempt(path string) bool {
	for _, p := range defaultExemptPrefixes {
		if strings.HasPrefix(path, p) {
			return true
		}
	}
	for _, p := range m.cfg.ExemptPrefixes {
		if strings.HasPrefix(path, p) {
			return true
		}
	}
	return false
}

// rateLimit wraps an http.HandlerFunc with bucket enforcement.
// Keyed on (route-prefix, token-hash) so different callers on
// the same token don't share a bucket if they hit different
// paths. The route-prefix is the first two path segments,
// which matches the HTTP server's own route grouping (e.g.
// /flags/eval and /flags/override share a bucket).
//
// When the limiter is disabled, this decorator is a pass-
// through — exactly one map lookup per request, no allocation.
func (s *HTTPServer) rateLimit(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		mgr := globalRateLimiter()
		if !mgr.cfg.enabledOrDefault() || mgr.exempt(r.URL.Path) {
			next(w, r)
			return
		}
		key := rateLimitKey(r)
		allowed, remaining, resetIn := mgr.Allow(key)
		w.Header().Set("X-RateLimit-Limit", fmt.Sprintf("%d", mgr.cfg.RequestsPerMinute))
		w.Header().Set("X-RateLimit-Remaining", fmt.Sprintf("%d", remaining))
		w.Header().Set("X-RateLimit-Reset", fmt.Sprintf("%d", int(resetIn.Seconds())))
		if !allowed {
			w.Header().Set("Retry-After", fmt.Sprintf("%d", int(resetIn.Seconds())))
			jsonError(w, http.StatusTooManyRequests, "rate limit exceeded")
			return
		}
		next(w, r)
	}
}

// rateLimitKey composes a bucket key from the request path's
// first two segments plus a hash of the auth token. Token is
// never stored in plaintext — we hash it so `yaver sync dump`
// or a stray log can't leak credentials.
func rateLimitKey(r *http.Request) string {
	path := r.URL.Path
	// Group by first two path segments: /flags/eval and
	// /flags/override share a bucket, /errors and /errors/detail
	// share another. Different features get different buckets.
	prefix := "/"
	if strings.HasPrefix(path, "/") {
		segs := strings.SplitN(path[1:], "/", 3)
		if len(segs) >= 2 {
			prefix = "/" + segs[0] + "/" + segs[1]
		} else if len(segs) == 1 {
			prefix = "/" + segs[0]
		}
	}
	auth := r.Header.Get("Authorization")
	if auth == "" {
		auth = r.RemoteAddr
	}
	sum := sha256.Sum256([]byte(auth))
	return prefix + "|" + hex.EncodeToString(sum[:8])
}
