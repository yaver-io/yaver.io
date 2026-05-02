package main

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

func resetGlobalRateLimiterForTest() {
	rateLimitMgr = nil
	rateLimitOnce = sync.Once{}
}

func TestGlobalRateLimiterEnabledByDefault(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	resetGlobalRateLimiterForTest()
	t.Cleanup(resetGlobalRateLimiterForTest)

	mgr := globalRateLimiter()
	if !mgr.cfg.enabledOrDefault() {
		t.Fatal("expected global rate limiter to default to enabled")
	}
}

func TestGlobalRateLimiterHonorsExplicitDisable(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if err := SaveConfig(&Config{
		RateLimit: &RateLimitConfig{
			Enabled:           boolPtr(false),
			RequestsPerMinute: 5,
			BurstSize:         1,
		},
	}); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}
	resetGlobalRateLimiterForTest()
	t.Cleanup(resetGlobalRateLimiterForTest)

	mgr := globalRateLimiter()
	if mgr.cfg.enabledOrDefault() {
		t.Fatal("expected explicit rate_limit.enabled=false to disable the limiter")
	}
}

func TestRateLimitMiddlewareRejectsBurstWhenEnabled(t *testing.T) {
	resetGlobalRateLimiterForTest()
	t.Cleanup(resetGlobalRateLimiterForTest)
	rateLimitMgr = &rateLimitManager{
		buckets: make(map[string]*bucket),
		cfg: RateLimitConfig{
			Enabled:           boolPtr(true),
			RequestsPerMinute: 60,
			BurstSize:         1,
		},
	}
	rateLimitOnce = sync.Once{}
	rateLimitOnce.Do(func() {})

	s := &HTTPServer{}
	handler := s.rateLimit(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})

	mkReq := func() *http.Request {
		req := httptest.NewRequest(http.MethodGet, "/support/info", nil)
		req.RemoteAddr = "198.51.100.40:12345"
		return req
	}

	first := httptest.NewRecorder()
	handler(first, mkReq())
	if first.Code != http.StatusNoContent {
		t.Fatalf("expected first request 204, got %d", first.Code)
	}

	second := httptest.NewRecorder()
	handler(second, mkReq())
	if second.Code != http.StatusTooManyRequests {
		t.Fatalf("expected second request 429, got %d", second.Code)
	}
	if second.Header().Get("Retry-After") == "" {
		t.Fatal("expected Retry-After header on rate-limited response")
	}
}
