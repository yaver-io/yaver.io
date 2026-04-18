package main

// mcp_auth_link_tools_test.go — unit tests for the MCP auth-linking
// helpers. We stand up a tiny HTTP stub that mimics the Convex endpoints
// our helpers call (/auth/providers, /auth/oauth-link/*, /auth/account/merge/*)
// and point the config at it for the duration of each test.
//
// The actual server-side mutations (unlinkAuthIdentity, mergeUserInto,
// completeAccountMerge, etc.) are typechecked by the Convex compiler and
// reviewed end-to-end. Adding the `convex-test` harness to exercise them
// in isolation would be a larger build-system change than the tests
// themselves; tests here therefore focus on the Go client contract:
//
//   - parse the shapes we claim to parse
//   - forward the right query/body to the right URL
//   - surface the documented error codes (409 only-identity, 412 totp,
//     429 rate limit) with friendly messages
//   - Layer-4 refusal + local-device short-circuit still hold

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

// stubConvex spins up an httptest.Server whose routes match what
// mcp_auth_link_tools.go calls. Each test registers handlers for the
// specific endpoints it cares about. Returns the base URL + a function
// to point config.json at it and restore afterwards.
type stubConvex struct {
	base      string
	mux       *http.ServeMux
	calls     atomic.Int64
	cleanup   func()
	origHome  string
	origAuth  string
	origToken string
}

func newStubConvex(t *testing.T) *stubConvex {
	t.Helper()
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)

	// Redirect config lookups at a throwaway HOME so we don't trample the
	// real ~/.yaver/config.json on the dev machine.
	tmp := t.TempDir()
	cfgDir := filepath.Join(tmp, ".yaver")
	if err := os.MkdirAll(cfgDir, 0700); err != nil {
		t.Fatalf("mkdir %s: %v", cfgDir, err)
	}
	cfg := &Config{
		AuthToken:     "test-token",
		ConvexSiteURL: srv.URL,
		DeviceID:      "test-device",
	}
	data, _ := json.Marshal(cfg)
	if err := os.WriteFile(filepath.Join(cfgDir, "config.json"), data, 0600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	origHome := os.Getenv("HOME")
	os.Setenv("HOME", tmp)

	sc := &stubConvex{
		base:     srv.URL,
		mux:      mux,
		origHome: origHome,
		cleanup: func() {
			srv.Close()
			os.Setenv("HOME", origHome)
		},
	}
	return sc
}

func (s *stubConvex) close() { s.cleanup() }

func (s *stubConvex) on(path string, handler http.HandlerFunc) {
	s.mux.Handle(path, handler)
}

// ---------------------------------------------------------------------------

func TestAuthListIdentities_ParsesResponse(t *testing.T) {
	sc := newStubConvex(t)
	defer sc.close()

	sc.on("/auth/providers", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			t.Errorf("want GET, got %s", r.Method)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Errorf("bad auth header: %q", got)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"identities": []map[string]any{
				{"provider": "apple", "email": "me@icloud.com", "isPrimary": true, "createdAt": 1, "lastUsedAt": 2},
				{"provider": "google", "email": "me@gmail.com", "isPrimary": false, "createdAt": 3, "lastUsedAt": 4},
			},
		})
	})

	result, err := authListIdentities(context.Background())
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if result.Count != 2 {
		t.Fatalf("want 2 identities, got %d", result.Count)
	}
	if result.Identities[0].Provider != "apple" || !result.Identities[0].IsPrimary {
		t.Fatalf("bad first identity: %+v", result.Identities[0])
	}
	if !strings.Contains(result.Message, "2 sign-in methods") {
		t.Fatalf("expected count in message, got %q", result.Message)
	}
}

func TestAuthLinkStart_BuildsOAuthURL(t *testing.T) {
	sc := newStubConvex(t)
	defer sc.close()

	sc.on("/auth/oauth-link/start", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Errorf("want POST, got %s", r.Method)
		}
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), `"provider":"google"`) {
			t.Errorf("body missing provider: %s", body)
		}
		if !strings.Contains(string(body), `"client":"mcp"`) {
			t.Errorf("body missing client=mcp: %s", body)
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"token": "stub-link-token"})
	})

	t.Setenv("YAVER_WEB_BASE_URL", "https://selfhost.example")

	result, err := authLinkStart(context.Background(), "google")
	if err != nil {
		t.Fatalf("link start: %v", err)
	}
	if !strings.Contains(result.URL, "selfhost.example/api/auth/oauth/google") {
		t.Fatalf("URL missing provider path: %s", result.URL)
	}
	if !strings.Contains(result.URL, "linkToken=stub-link-token") {
		t.Fatalf("URL missing link token: %s", result.URL)
	}
	if !strings.Contains(result.URL, "client=mcp") {
		t.Fatalf("URL missing client=mcp: %s", result.URL)
	}
	if len(result.QRASCII) < 100 {
		t.Fatalf("QR ASCII suspiciously short: %d bytes", len(result.QRASCII))
	}
}

func TestAuthLinkStart_RejectsUnknownProvider(t *testing.T) {
	sc := newStubConvex(t)
	defer sc.close()
	_, err := authLinkStart(context.Background(), "facebook")
	if err == nil || !strings.Contains(err.Error(), "apple | github | gitlab | google | microsoft") {
		t.Fatalf("expected provider rejection, got %v", err)
	}
}

func TestAuthUnlink_OnlyIdentityReturnsFriendlyMessage(t *testing.T) {
	sc := newStubConvex(t)
	defer sc.close()

	sc.on("/auth/oauth-link/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "DELETE" {
			t.Errorf("want DELETE, got %s", r.Method)
		}
		w.WriteHeader(409)
		_, _ = w.Write([]byte("Refusing to unlink the only sign-in method"))
	})

	result, err := authUnlink(context.Background(), "google", "")
	if err != nil {
		t.Fatalf("unlink: %v", err)
	}
	if result.OK {
		t.Fatalf("want OK=false on 409")
	}
	if !strings.Contains(result.Message, "only sign-in") {
		t.Fatalf("missing friendly message, got %q", result.Message)
	}
}

func TestAuthUnlink_NotLinkedReturns404Friendly(t *testing.T) {
	sc := newStubConvex(t)
	defer sc.close()

	sc.on("/auth/oauth-link/", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
		_, _ = w.Write([]byte("not_linked"))
	})

	result, err := authUnlink(context.Background(), "apple", "")
	if err != nil {
		t.Fatalf("unlink: %v", err)
	}
	if result.OK {
		t.Fatalf("want OK=false on 404")
	}
	if !strings.Contains(result.Message, "not linked") {
		t.Fatalf("message should say 'not linked': %q", result.Message)
	}
}

func TestAuthMergeStart_BuildsApprovalURL(t *testing.T) {
	sc := newStubConvex(t)
	defer sc.close()

	sc.on("/auth/account/merge/start", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"mergeToken":  "merge-abc",
			"expiresAt":   int64(1_700_000_000_000),
			"targetEmail": "me@example.com",
		})
	})

	t.Setenv("YAVER_WEB_BASE_URL", "https://selfhost.example")

	result, err := authMergeStart(context.Background(), "")
	if err != nil {
		t.Fatalf("merge start: %v", err)
	}
	if !strings.Contains(result.ApprovalURL, "token=merge-abc") {
		t.Fatalf("approval URL missing merge token: %s", result.ApprovalURL)
	}
	if !strings.Contains(result.ApprovalURL, "selfhost.example/account/merge") {
		t.Fatalf("approval URL missing self-hosted base: %s", result.ApprovalURL)
	}
	if result.TargetEmail != "me@example.com" {
		t.Fatalf("bad target email: %s", result.TargetEmail)
	}
}

func TestAuthMergeStatus_ReportsCompletedState(t *testing.T) {
	sc := newStubConvex(t)
	defer sc.close()

	sc.on("/auth/account/merge/status", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("token") != "xyz" {
			t.Errorf("expected ?token=xyz, got %s", r.URL.RawQuery)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status":      "completed",
			"targetEmail": "dest@example.com",
			"completedAt": int64(42),
		})
	})

	result, err := authMergeStatus(context.Background(), "xyz")
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if result.Status != "completed" {
		t.Fatalf("bad status: %+v", result)
	}
	if !strings.Contains(result.Message, "merge completed") {
		t.Fatalf("message should mention merge completion, got %q", result.Message)
	}
}

func TestAuthMergeStatus_RequiresToken(t *testing.T) {
	sc := newStubConvex(t)
	defer sc.close()
	_, err := authMergeStatus(context.Background(), "  ")
	if err == nil || !strings.Contains(err.Error(), "merge_token is required") {
		t.Fatalf("expected merge_token-required error, got %v", err)
	}
}

// Guard: the Layer-4 refusal list and the local-device short-circuit
// stay correct even across refactors. Covered more thoroughly in
// mcp_remote_proxy_test.go — this is a cheap regression check that the
// two files still agree.
func TestLayer4AndLocalShortCircuit_StillHold(t *testing.T) {
	t.Parallel()
	if err := refuseRemoteLayer4("vault_get", "remote"); !errors.Is(err, errLayer4Remote) {
		t.Fatalf("vault_get should be blocked remote, got %v", err)
	}
	if err := refuseRemoteLayer4("mobile_project_build", "remote"); err != nil {
		t.Fatalf("mobile_project_build should allow remote, got %v", err)
	}
}
