package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

// signLikeAgent mirrors desktop/agent/device_sign_key.go::SignRelayRequest so
// the test proves the two halves of the wire contract interoperate.
func signLikeAgent(priv ed25519.PrivateKey, deviceID, method, path string, body []byte, tsMs int64, nonce string) *http.Request {
	ts := strconv.FormatInt(tsMs, 10)
	sum := sha256.Sum256(body)
	canonical := strings.Join([]string{method, path, deviceID, ts, nonce, hex.EncodeToString(sum[:])}, "\n")
	sig := base64.StdEncoding.EncodeToString(ed25519.Sign(priv, []byte(canonical)))
	req := httptest.NewRequest(method, path, nil)
	req.Header.Set("X-Yaver-Sig", "v1")
	req.Header.Set("X-Yaver-Device", deviceID)
	req.Header.Set("X-Yaver-Timestamp", ts)
	req.Header.Set("X-Yaver-Nonce", nonce)
	req.Header.Set("X-Yaver-Signature", sig)
	return req
}

func TestVerifyDeviceSig(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	body := []byte(`{"x":1}`)
	now := time.Now().UnixMilli()
	nc := newSigNonceCache()

	if id, ok := verifyDeviceSig(signLikeAgent(priv, "dev1", "POST", "/d/dev1/ops", body, now, "n1"), body, pub, nc); !ok || id != "dev1" {
		t.Fatalf("valid signature rejected: id=%q ok=%v", id, ok)
	}

	// Replay of the same (device, nonce) → reject.
	if _, ok := verifyDeviceSig(signLikeAgent(priv, "dev1", "POST", "/d/dev1/ops", body, now, "n1"), body, pub, nc); ok {
		t.Fatal("SECURITY: replayed nonce accepted")
	}

	// Body tampered after signing → reject.
	if _, ok := verifyDeviceSig(signLikeAgent(priv, "dev1", "POST", "/d/dev1/ops", body, now, "n2"), []byte(`{"x":2}`), pub, nc); ok {
		t.Fatal("SECURITY: tampered body accepted")
	}

	// Verified against the wrong key → reject.
	otherPub, _, _ := ed25519.GenerateKey(rand.Reader)
	if _, ok := verifyDeviceSig(signLikeAgent(priv, "dev1", "POST", "/d/dev1/ops", body, time.Now().UnixMilli(), "n3"), body, otherPub, nc); ok {
		t.Fatal("SECURITY: signature verified against wrong key")
	}

	// Expired timestamp → reject.
	old := time.Now().Add(-5 * time.Minute).UnixMilli()
	if _, ok := verifyDeviceSig(signLikeAgent(priv, "dev1", "POST", "/d/dev1/ops", body, old, "n4"), body, pub, newSigNonceCache()); ok {
		t.Fatal("SECURITY: expired signature accepted")
	}

	// Future timestamp beyond skew → reject.
	future := time.Now().Add(5 * time.Minute).UnixMilli()
	if _, ok := verifyDeviceSig(signLikeAgent(priv, "dev1", "POST", "/d/dev1/ops", body, future, "n5"), body, pub, newSigNonceCache()); ok {
		t.Fatal("SECURITY: far-future signature accepted")
	}

	// Unsigned request → not attempted.
	if _, ok := verifyDeviceSig(httptest.NewRequest("GET", "/d/dev1/health", nil), nil, pub, nc); ok {
		t.Fatal("unsigned request should not verify")
	}
}

func TestSigDeviceMatches(t *testing.T) {
	if !sigDeviceMatches("dev-abc", "dev-abc") {
		t.Fatal("equal ids should match")
	}
	if sigDeviceMatches("dev-abc", "dev-abd") {
		t.Fatal("SECURITY: mismatched ids matched")
	}
}

// TestCanonicalGolden pins the exact signed bytes so relay + agent can't drift.
func TestCanonicalGolden(t *testing.T) {
	got := canonicalSigString("POST", "/d/dev1/ops", "dev1", "1700000000000", "n1", []byte("hi"))
	sum := sha256.Sum256([]byte("hi"))
	want := "POST\n/d/dev1/ops\ndev1\n1700000000000\nn1\n" + hex.EncodeToString(sum[:])
	if got != want {
		t.Fatalf("canonical drift:\n got=%q\nwant=%q", got, want)
	}
}

func TestDecodeSignPubKey(t *testing.T) {
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	if got := decodeSignPubKey(base64.StdEncoding.EncodeToString(pub)); len(got) != ed25519.PublicKeySize {
		t.Fatal("valid pubkey failed to decode")
	}
	if decodeSignPubKey("not-base64!!") != nil {
		t.Fatal("malformed pubkey must decode to nil (fail closed)")
	}
	if decodeSignPubKey(base64.StdEncoding.EncodeToString([]byte("short"))) != nil {
		t.Fatal("wrong-length pubkey must decode to nil")
	}
}

func TestAuthorizeProxyViaSig_OversizedChunkedBodyIsNotTruncatedIntoFallback(t *testing.T) {
	s := NewRelayServer(0, 0, "pw", "", "")
	cfg := defaultAbuseGuardConfig()
	cfg.MaxRequestBodyBytes = 5
	s.abuseGuard = newAbuseGuard(cfg)

	req := httptest.NewRequest(http.MethodPost, "/d/device1234/ops", io.NopCloser(strings.NewReader("abcdef")))
	req.ContentLength = -1 // chunked/unknown length: must detect by reading limit+1.
	req.Header.Set("X-Yaver-Sig", "v1")
	req.Header.Set("X-Yaver-Device", "device1234")

	if _, ok, _ := s.authorizeProxyViaSig(req, "device1234"); ok {
		t.Fatal("oversized body must not authenticate via signature")
	}

	w := httptest.NewRecorder()
	body, ok := readCappedBody(w, req, cfg.MaxRequestBodyBytes)
	if ok {
		t.Fatalf("fallback proxy body read must fail with 413, got ok body=%q", string(body))
	}
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("fallback body read status = %d, want 413 body=%s", w.Code, w.Body.String())
	}
}
