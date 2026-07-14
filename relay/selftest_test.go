package main

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// signedSelftestRequest builds a request the way the watchdog script does:
// canonical JSON body, Ed25519 signature over the raw bytes in a header.
func signedSelftestRequest(t *testing.T, priv ed25519.PrivateKey, nonce string, issuedAtMs int64) *http.Request {
	t.Helper()
	body, _ := json.Marshal(selftestRequest{Nonce: nonce, IssuedAtMs: issuedAtMs})
	sig := ed25519.Sign(priv, body)
	req := httptest.NewRequest(http.MethodPost, "/admin/selftest", bytes.NewReader(body))
	req.Header.Set("X-Yaver-Watchdog-Sig", base64.StdEncoding.EncodeToString(sig))
	return req
}

// withWatchdogKey installs a single authorised key for the duration of a test,
// resetting the package-level once/state so each case is independent.
func withWatchdogKey(t *testing.T, pub ed25519.PublicKey) {
	t.Helper()
	watchdogKeysOnce.Do(func() {}) // consume the once so our assignment sticks
	watchdogKeys = []ed25519.PublicKey{pub}
	selftestNonces = &nonceGuard{seen: make(map[string]time.Time)}
	t.Cleanup(func() { watchdogKeys = nil })
}

func newRelayForSelftest() *RelayServer {
	return &RelayServer{tunnels: make(map[string]*agentTunnel)}
}

func TestSelftestRejectsForgedSignature(t *testing.T) {
	pub, _, _ := ed25519.GenerateKey(nil)
	withWatchdogKey(t, pub)

	// Sign with a DIFFERENT key than the one the relay trusts.
	_, attacker, _ := ed25519.GenerateKey(nil)
	req := signedSelftestRequest(t, attacker, "nonce-1", time.Now().UnixMilli())
	rec := httptest.NewRecorder()
	newRelayForSelftest().handleAdminSelftest(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("forged signature: got %d, want 403 — an attacker could probe the fleet", rec.Code)
	}
}

func TestSelftestAcceptsAuthorisedSignature(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(nil)
	withWatchdogKey(t, pub)

	req := signedSelftestRequest(t, priv, "nonce-ok", time.Now().UnixMilli())
	rec := httptest.NewRecorder()
	newRelayForSelftest().handleAdminSelftest(rec, req) // zero tunnels registered

	if rec.Code != http.StatusOK {
		t.Fatalf("authorised request rejected: got %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out["ok"] != true || out["zombies"] != float64(0) {
		t.Fatalf("unexpected payload: %v", out)
	}
}

func TestSelftestRejectsReplay(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(nil)
	withWatchdogKey(t, pub)
	s := newRelayForSelftest()

	first := signedSelftestRequest(t, priv, "replay-me", time.Now().UnixMilli())
	rec1 := httptest.NewRecorder()
	s.handleAdminSelftest(rec1, first)
	if rec1.Code != http.StatusOK {
		t.Fatalf("first use should succeed, got %d", rec1.Code)
	}

	// EXACT same body+sig again — a captured request replayed.
	replay := signedSelftestRequest(t, priv, "replay-me", time.Now().UnixMilli())
	rec2 := httptest.NewRecorder()
	s.handleAdminSelftest(rec2, replay)
	if rec2.Code != http.StatusUnauthorized {
		t.Fatalf("replayed nonce: got %d, want 401", rec2.Code)
	}
}

func TestSelftestRejectsStaleTimestamp(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(nil)
	withWatchdogKey(t, pub)

	old := time.Now().Add(-5 * time.Minute).UnixMilli()
	req := signedSelftestRequest(t, priv, "old-nonce", old)
	rec := httptest.NewRecorder()
	newRelayForSelftest().handleAdminSelftest(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("stale timestamp: got %d, want 401 (replay window must be bounded)", rec.Code)
	}
}

func TestSelftestIsInvisibleWithoutKeys(t *testing.T) {
	// No authorised keys => the endpoint must not exist. An un-provisioned relay
	// exposes no introspection surface at all.
	watchdogKeysOnce.Do(func() {})
	watchdogKeys = nil

	_, priv, _ := ed25519.GenerateKey(nil)
	req := signedSelftestRequest(t, priv, "n", time.Now().UnixMilli())
	rec := httptest.NewRecorder()
	newRelayForSelftest().handleAdminSelftest(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("with no keys configured: got %d, want 404", rec.Code)
	}
}

func TestSelftestRejectsOversizeBody(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(nil)
	withWatchdogKey(t, pub)

	// A valid signature over a huge body must still be refused at the length gate
	// (before any per-tunnel work), so it can't be used as an amplification lever.
	big := make([]byte, selftestMaxBody+100)
	for i := range big {
		big[i] = 'a'
	}
	sig := ed25519.Sign(priv, big)
	req := httptest.NewRequest(http.MethodPost, "/admin/selftest", bytes.NewReader(big))
	req.Header.Set("X-Yaver-Watchdog-Sig", base64.StdEncoding.EncodeToString(sig))
	rec := httptest.NewRecorder()
	newRelayForSelftest().handleAdminSelftest(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("oversize body: got %d, want 400", rec.Code)
	}
}

// Guard the base64-pubkey parser: a malformed entry is dropped, valid ones survive.
func TestWatchdogPubKeyParsing(t *testing.T) {
	pub, _, _ := ed25519.GenerateKey(nil)
	good := base64.StdEncoding.EncodeToString(pub)
	keys := parseWatchdogPubKeys(fmt.Sprintf("not-base64!!, %s , AAAA", good))
	if len(keys) != 1 {
		t.Fatalf("expected exactly the one valid key to survive, got %d", len(keys))
	}
}
