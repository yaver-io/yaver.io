package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestAuthAllowsHostShareInfo(t *testing.T) {
	srv := &HTTPServer{
		token:       "owner-token",
		ownerUserID: "owner-user",
	}
	srv.tokenCache.Store("guest-token", &cachedTokenInfo{
		userID: "guest-user",
		hostShare: &HostShareAccessInfo{
			SessionID:     "hsess_123",
			GuestDeviceID: "guest-device-1",
			Policy: HostSharePolicy{
				AllowTerminal: true,
			},
		},
		storedAt: time.Now(),
	})

	req := httptest.NewRequest(http.MethodGet, "/info", nil)
	req.Header.Set("Authorization", "Bearer guest-token")
	rec := httptest.NewRecorder()
	called := false

	srv.auth(func(w http.ResponseWriter, r *http.Request) {
		called = true
		if got := r.Header.Get("X-Yaver-HostShare"); got != "true" {
			t.Fatalf("missing host-share marker header, got %q", got)
		}
		if got := r.Header.Get("X-Yaver-HostShareSessionID"); got != "hsess_123" {
			t.Fatalf("session header = %q, want hsess_123", got)
		}
		if got := r.Header.Get("X-Yaver-HostShareGuestDeviceID"); got != "guest-device-1" {
			t.Fatalf("guest device header = %q, want guest-device-1", got)
		}
		w.WriteHeader(http.StatusOK)
	})(rec, req)

	if !called {
		t.Fatalf("expected wrapped handler to be called")
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
}

func TestAuthRejectsHostShareExec(t *testing.T) {
	srv := &HTTPServer{
		token:       "owner-token",
		ownerUserID: "owner-user",
	}
	srv.tokenCache.Store("guest-token", &cachedTokenInfo{
		userID: "guest-user",
		hostShare: &HostShareAccessInfo{
			SessionID: "hsess_123",
			Policy: HostSharePolicy{
				AllowTerminal: true,
			},
		},
		storedAt: time.Now(),
	})

	req := httptest.NewRequest(http.MethodPost, "/exec", nil)
	req.Header.Set("Authorization", "Bearer guest-token")
	rec := httptest.NewRecorder()

	srv.auth(func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("wrapped handler should not be called")
	})(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusForbidden)
	}
}

func TestAuthDoesNotCacheNegativeHostShareLookup(t *testing.T) {
	var hostShareEnabled atomic.Bool
	convex := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/auth/validate":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"user": map[string]any{
					"userId": "guest-user",
				},
			})
		case "/host-share/access":
			access := any(nil)
			if hostShareEnabled.Load() {
				access = map[string]any{
					"sessionId":     "hsess_456",
					"guestUserId":   "guest-user",
					"guestDeviceId": "guest-device-2",
					"policy": map[string]any{
						"allowTerminal": true,
					},
				}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"access": access})
		case "/host-share/peer-access":
			_ = json.NewEncoder(w).Encode(map[string]any{"access": nil})
		default:
			http.NotFound(w, r)
		}
	}))
	defer convex.Close()

	srv := &HTTPServer{
		token:       "owner-token",
		ownerUserID: "owner-user",
		convexURL:   convex.URL,
		deviceID:    "host-device-1",
	}

	req := httptest.NewRequest(http.MethodGet, "/agent/runners", nil)
	req.Header.Set("Authorization", "Bearer guest-token")

	rec := httptest.NewRecorder()
	srv.auth(func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("wrapped handler should not be called before host-share access exists")
	})(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("first status = %d, want %d", rec.Code, http.StatusForbidden)
	}
	if _, ok := srv.tokenCache.Load("guest-token"); ok {
		t.Fatalf("negative non-owner auth result should not be cached")
	}

	hostShareEnabled.Store(true)
	rec = httptest.NewRecorder()
	called := false
	srv.auth(func(w http.ResponseWriter, r *http.Request) {
		called = true
		if got := r.Header.Get("X-Yaver-HostShareSessionID"); got != "hsess_456" {
			t.Fatalf("session header = %q, want hsess_456", got)
		}
		w.WriteHeader(http.StatusOK)
	})(rec, req)
	if !called {
		t.Fatalf("expected wrapped handler to be called after host-share access exists")
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("second status = %d, want %d", rec.Code, http.StatusOK)
	}
}
