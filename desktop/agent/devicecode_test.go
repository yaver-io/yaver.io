package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

// TestIsHeadless pins the env-var matrix for headless detection. The
// important non-obvious case is WSL without DISPLAY — a cousin driving
// his WSL from a phone at a cafe should get device-code flow, not a
// browser opening on the unreachable Windows host.
func TestIsHeadless(t *testing.T) {
	cases := []struct {
		name string
		env  map[string]string
		// wslOverride lets us stub detectWSLRuntime for the cases where
		// we want to assert WSL-specific behaviour without requiring the
		// test host to actually be WSL.
		wslOverride *wslRuntimeInfo
		want        bool
	}{
		{
			name: "GUI desktop — not headless",
			env:  map[string]string{"DISPLAY": ":0"},
			want: false,
		},
		{
			name: "plain SSH with DISPLAY forwarded — not headless",
			env:  map[string]string{"SSH_CONNECTION": "1.2.3.4 22 5.6.7.8 22", "DISPLAY": "localhost:10.0"},
			want: false,
		},
		{
			name: "SSH without DISPLAY — headless",
			env:  map[string]string{"SSH_CONNECTION": "1.2.3.4 22 5.6.7.8 22"},
			want: true,
		},
		{
			name: "SSH_TTY variant — headless",
			env:  map[string]string{"SSH_TTY": "/dev/pts/0"},
			want: true,
		},
		{
			name:        "WSL without DISPLAY — headless (cousin-at-cafe case)",
			env:         map[string]string{"WSL_DISTRO_NAME": "Ubuntu"},
			wslOverride: &wslRuntimeInfo{IsWSL: true, Version: 2},
			want:        true,
		},
		{
			name:        "WSL with X forwarding — not headless",
			env:         map[string]string{"WSL_DISTRO_NAME": "Ubuntu", "DISPLAY": ":0"},
			wslOverride: &wslRuntimeInfo{IsWSL: true, Version: 2},
			want:        false,
		},
		{
			name: "YAVER_HEADLESS=1 — always headless",
			env:  map[string]string{"YAVER_HEADLESS": "1", "DISPLAY": ":0"},
			want: true,
		},
		{
			name: "YAVER_HEADLESS=true — always headless",
			env:  map[string]string{"YAVER_HEADLESS": "true"},
			want: true,
		},
		{
			name: "no signals — not headless",
			env:  map[string]string{},
			want: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Clear every relevant env var so a test inherits nothing from
			// the shell. t.Setenv restores the previous value on cleanup.
			for _, k := range []string{
				"YAVER_HEADLESS", "SSH_TTY", "SSH_CONNECTION", "DISPLAY",
				"WAYLAND_DISPLAY", "WSL_DISTRO_NAME", "WSL_INTEROP",
			} {
				t.Setenv(k, "")
			}
			for k, v := range tc.env {
				t.Setenv(k, v)
			}

			if tc.wslOverride != nil {
				prev := wslRuntimeProbe
				wslRuntimeProbe = func() wslRuntimeInfo { return *tc.wslOverride }
				defer func() { wslRuntimeProbe = prev }()
			}

			got := isHeadless()
			if got != tc.want {
				t.Errorf("isHeadless() = %v, want %v (env=%v)", got, tc.want, tc.env)
			}
		})
	}
}

func TestEnvTruthy(t *testing.T) {
	truthy := []string{"1", "true", "yes", "on", "TRUE", " Yes "}
	falsy := []string{"", "0", "false", "no", "off", "banana"}
	for _, v := range truthy {
		if !envTruthy(v) {
			t.Errorf("envTruthy(%q) = false, want true", v)
		}
	}
	for _, v := range falsy {
		if envTruthy(v) {
			t.Errorf("envTruthy(%q) = true, want false", v)
		}
	}
}

// withTempHome redirects the user's home dir to a scratch path so tests
// can exercise ~/.yaver/pending-auth.json without clobbering the real one.
func withTempHome(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("YAVER_VAULT_SKIP_KEYCHAIN", "1")
	// On darwin/linux `ConfigDir` resolves via os.UserHomeDir which
	// consults $HOME first, so setting it is enough.
	yaverDir := filepath.Join(dir, ".yaver")
	if err := os.MkdirAll(yaverDir, 0o700); err != nil {
		t.Fatalf("mkdir %s: %v", yaverDir, err)
	}
	return yaverDir
}

func TestPendingAuthRoundTrip(t *testing.T) {
	yaverDir := withTempHome(t)

	got, err := loadPendingAuth()
	if err != nil || got != nil {
		t.Fatalf("empty state: want nil, got %v / %v", got, err)
	}

	orig := &pendingAuth{
		DeviceCode: "abc123",
		UserCode:   "ABCD-1234",
		URL:        "https://yaver.io/auth/device?code=ABCD-1234",
		ConvexURL:  "https://convex.example",
		ExpiresAt:  time.Now().Add(10 * time.Minute).UnixMilli(),
		CreatedAt:  time.Now().UnixMilli(),
	}
	if err := savePendingAuth(orig); err != nil {
		t.Fatalf("savePendingAuth: %v", err)
	}

	// File should exist and be 0o600 — it contains a secret-ish opaque id.
	info, err := os.Stat(filepath.Join(yaverDir, "pending-auth.json"))
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("permission = %v, want 0o600", info.Mode().Perm())
	}

	loaded, err := loadPendingAuth()
	if err != nil || loaded == nil {
		t.Fatalf("loadPendingAuth: %v / %v", loaded, err)
	}
	if loaded.DeviceCode != orig.DeviceCode || loaded.UserCode != orig.UserCode {
		t.Errorf("loaded = %+v, want %+v", loaded, orig)
	}

	clearPendingAuth()
	again, err := loadPendingAuth()
	if err != nil || again != nil {
		t.Fatalf("after clear: want nil, got %v / %v", again, err)
	}
}

func TestObtainOrResumeDeviceCode_ResumesValid(t *testing.T) {
	withTempHome(t)
	_ = savePendingAuth(&pendingAuth{
		DeviceCode: "preserved-code",
		UserCode:   "XYZQ-9988",
		URL:        "https://yaver.io/auth/device?code=XYZQ-9988",
		ConvexURL:  "https://original.example",
		ExpiresAt:  time.Now().Add(8 * time.Minute).UnixMilli(),
		CreatedAt:  time.Now().Add(-2 * time.Minute).UnixMilli(),
	})

	// Server intentionally panics on POST — resume path must not hit it.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
	}))
	defer srv.Close()

	dc, resumed, err := obtainOrResumeDeviceCode(srv.URL)
	if err != nil {
		t.Fatalf("obtainOrResumeDeviceCode: %v", err)
	}
	if !resumed {
		t.Errorf("resumed = false, want true")
	}
	if dc.DeviceCode != "preserved-code" || dc.UserCode != "XYZQ-9988" {
		t.Errorf("got %+v", dc)
	}
}

func TestObtainOrResumeDeviceCode_DiscardsExpired(t *testing.T) {
	withTempHome(t)
	_ = savePendingAuth(&pendingAuth{
		DeviceCode: "expired-code",
		UserCode:   "DEAD-0000",
		URL:        "https://yaver.io/auth/device?code=DEAD-0000",
		ConvexURL:  "https://original.example",
		ExpiresAt:  time.Now().Add(-1 * time.Minute).UnixMilli(),
		CreatedAt:  time.Now().Add(-20 * time.Minute).UnixMilli(),
	})

	hit := int32(0)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/auth/device-code" && r.Method == http.MethodPost {
			atomic.AddInt32(&hit, 1)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(deviceCodeResponse{
				UserCode:   "FRSH-1111",
				DeviceCode: "fresh-code",
				ExpiresAt:  time.Now().Add(15 * time.Minute).UnixMilli(),
			})
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	dc, resumed, err := obtainOrResumeDeviceCode(srv.URL)
	if err != nil {
		t.Fatalf("obtainOrResumeDeviceCode: %v", err)
	}
	if resumed {
		t.Errorf("resumed = true, want false — expired record should be discarded")
	}
	if dc.DeviceCode != "fresh-code" {
		t.Errorf("dc.DeviceCode = %q, want fresh-code", dc.DeviceCode)
	}
	if atomic.LoadInt32(&hit) != 1 {
		t.Errorf("backend hits = %d, want 1", hit)
	}

	// The fresh code should have been persisted.
	saved, _ := loadPendingAuth()
	if saved == nil || saved.DeviceCode != "fresh-code" {
		t.Errorf("saved = %+v, want fresh-code", saved)
	}
}

func TestPollUntilAuthorized_BudgetTimeoutReturnsErrResumable(t *testing.T) {
	withTempHome(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Always pending — never finishes within the test budget.
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(pollResponse{Status: "pending"})
	}))
	defer srv.Close()

	// Preserve a pending-auth record so we can assert it stays on disk.
	_ = savePendingAuth(&pendingAuth{
		DeviceCode: "pending-code",
		UserCode:   "RESU-4242",
		ExpiresAt:  time.Now().Add(15 * time.Minute).UnixMilli(),
		CreatedAt:  time.Now().UnixMilli(),
	})

	_, err := pollUntilAuthorized(srv.URL, "pending-code",
		time.Now().Add(15*time.Minute), 50*time.Millisecond)
	if !errors.Is(err, errResumable) {
		t.Fatalf("err = %v, want errResumable", err)
	}

	// pending-auth must still be on disk — this is the whole point.
	saved, _ := loadPendingAuth()
	if saved == nil || saved.DeviceCode != "pending-code" {
		t.Errorf("pending-auth was cleared; saved = %+v", saved)
	}
}

func TestPollUntilAuthorized_SuccessClearsPending(t *testing.T) {
	withTempHome(t)
	_ = savePendingAuth(&pendingAuth{
		DeviceCode: "good-code",
		UserCode:   "GOOD-8888",
		ExpiresAt:  time.Now().Add(15 * time.Minute).UnixMilli(),
		CreatedAt:  time.Now().UnixMilli(),
	})

	calls := int32(0)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		w.Header().Set("Content-Type", "application/json")
		if n == 1 {
			_ = json.NewEncoder(w).Encode(pollResponse{Status: "authorized", Token: "tok-success"})
			return
		}
		t.Fatalf("unexpected second call")
	}))
	defer srv.Close()

	token, err := pollUntilAuthorized(srv.URL, "good-code",
		time.Now().Add(15*time.Minute), 10*time.Second)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if token != "tok-success" {
		t.Errorf("token = %q, want tok-success", token)
	}
	saved, _ := loadPendingAuth()
	if saved != nil {
		t.Errorf("pending-auth not cleared after success: %+v", saved)
	}
}

// TestAuthStatusSnapshot_ExposesPendingAuth guarantees the MCP contract:
// when a sign-in is in flight (pending-auth.json on disk), the auth_status
// snapshot surfaces the URL + code so the agent can show them to the human
// without fishing around on the filesystem.
func TestAuthStatusSnapshot_ExposesPendingAuth(t *testing.T) {
	withTempHome(t)

	_ = savePendingAuth(&pendingAuth{
		DeviceCode: "dev-abc",
		UserCode:   "WAIT-9090",
		URL:        "https://yaver.io/auth/device?code=WAIT-9090",
		ConvexURL:  "https://example.invalid",
		ExpiresAt:  time.Now().Add(10 * time.Minute).UnixMilli(),
		CreatedAt:  time.Now().UnixMilli(),
	})

	snap := authStatusSnapshot()
	if snap.SignedIn {
		t.Fatalf("snap.SignedIn = true, want false (no token on disk)")
	}
	if !snap.NeedsAuth {
		t.Fatalf("snap.NeedsAuth = false, want true")
	}
	if snap.PendingAuth == nil {
		t.Fatalf("snap.PendingAuth is nil — should surface in-flight code")
	}
	if snap.PendingAuth.UserCode != "WAIT-9090" ||
		snap.PendingAuth.DeviceCode != "dev-abc" ||
		snap.PendingAuth.URL != "https://yaver.io/auth/device?code=WAIT-9090" {
		t.Errorf("PendingAuth mismatch: %+v", snap.PendingAuth)
	}
	if snap.PendingAuth.ExpiresInSeconds <= 0 {
		t.Errorf("ExpiresInSeconds = %d, want > 0", snap.PendingAuth.ExpiresInSeconds)
	}
	// Message should point the agent at the right next action.
	if snap.Message == "" {
		t.Errorf("Message is empty")
	}
}

func TestAuthStatusSnapshot_NoPendingWhenExpired(t *testing.T) {
	withTempHome(t)
	_ = savePendingAuth(&pendingAuth{
		DeviceCode: "dev-expired",
		UserCode:   "OLD-0000",
		URL:        "https://yaver.io/auth/device?code=OLD-0000",
		ExpiresAt:  time.Now().Add(-1 * time.Minute).UnixMilli(),
		CreatedAt:  time.Now().Add(-20 * time.Minute).UnixMilli(),
	})
	snap := authStatusSnapshot()
	if snap.PendingAuth != nil {
		t.Errorf("expired pending-auth should not surface; got %+v", snap.PendingAuth)
	}
}

func TestHumanRoundDuration(t *testing.T) {
	cases := []struct {
		in   time.Duration
		want string
	}{
		{-1 * time.Second, "0s"},
		{0, "0s"},
		{30 * time.Second, "30s"},
		{90 * time.Second, "1m30s"},
		{2 * time.Minute, "2m"},
		{2*time.Minute + 30*time.Second, "2m30s"},
	}
	for _, tc := range cases {
		if got := humanRoundDuration(tc.in); got != tc.want {
			t.Errorf("humanRoundDuration(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// confirm the compile-time sentinel matches errors.Is semantics.
var _ error = errResumable

func init() {
	// Silence unused-import warnings if a future refactor drops one.
	_ = fmt.Sprintf
}
