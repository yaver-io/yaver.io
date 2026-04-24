package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// capturedWebhook records what the test server saw. Using
// sync.Mutex + slice rather than a channel so the test's attempt
// counter is easy to assert against at various points.
type capturedWebhook struct {
	mu      sync.Mutex
	calls   []DeployWebhookPayload
	codes   []int
	headers []http.Header
}

func (c *capturedWebhook) hit(payload DeployWebhookPayload, header http.Header, code int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls = append(c.calls, payload)
	c.codes = append(c.codes, code)
	c.headers = append(c.headers, header)
}

func (c *capturedWebhook) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.calls)
}

// setWebhookInConfig writes a minimal config file pointing
// DeployWebhookURL at the test server. Returns the cleanup.
func setWebhookInConfig(t *testing.T, url, filter string) {
	t.Helper()
	setWebhookInConfigWithSecret(t, url, filter, "")
}

func setWebhookInConfigWithSecret(t *testing.T, url, filter, secret string) {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	if err := os.MkdirAll(filepath.Join(tmp, ".yaver"), 0700); err != nil {
		t.Fatalf("mkdir .yaver: %v", err)
	}
	cfg := &Config{DeployWebhookURL: url, DeployWebhookOn: filter, DeployWebhookSecret: secret}
	if err := SaveConfig(cfg); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}
}

func TestShouldFireDeployWebhookFilter(t *testing.T) {
	cases := []struct {
		filter string
		ok     bool
		want   bool
	}{
		{"", true, true},
		{"", false, true},
		{"all", true, true},
		{"all", false, true},
		{"success", true, true},
		{"success", false, false},
		{"failure", true, false},
		{"failure", false, true},
		{"fail", false, true},
		{"failures", false, true},
		{"ALL", true, true},
		{"bogus", false, true}, // unknown values default to "all"
	}
	for _, c := range cases {
		if got := shouldFireDeployWebhook(c.ok, c.filter); got != c.want {
			t.Errorf("filter=%q ok=%v: got %v want %v", c.filter, c.ok, got, c.want)
		}
	}
}

func TestFireDeployWebhookSuccess(t *testing.T) {
	cap := &capturedWebhook{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body DeployWebhookPayload
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		cap.hit(body, r.Header, http.StatusOK)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	setWebhookInConfig(t, srv.URL, "all")

	FireDeployWebhook(DeployRun{
		ID:         "abc123",
		App:        "mobile",
		Target:     "testflight",
		ExitCode:   0,
		OK:         true,
		DurationMs: 123,
		StartedAt:  1714065600000,
	})

	// Wait briefly — the webhook fires in a goroutine.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && cap.count() == 0 {
		time.Sleep(20 * time.Millisecond)
	}
	if cap.count() != 1 {
		t.Fatalf("expected 1 POST, got %d", cap.count())
	}
	got := cap.calls[0]
	if got.ID != "abc123" || got.App != "mobile" || got.Target != "testflight" || !got.OK {
		t.Fatalf("wrong payload: %+v", got)
	}
	if ct := cap.headers[0].Get("Content-Type"); ct != "application/json" {
		t.Errorf("wrong Content-Type: %q", ct)
	}
}

func TestFireDeployWebhookFilterExcludesSuccess(t *testing.T) {
	cap := &capturedWebhook{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cap.hit(DeployWebhookPayload{}, r.Header, 200)
		w.WriteHeader(200)
	}))
	defer srv.Close()
	setWebhookInConfig(t, srv.URL, "failure")

	FireDeployWebhook(DeployRun{ID: "x", OK: true})

	// Give it a beat. It should never fire.
	time.Sleep(300 * time.Millisecond)
	if cap.count() != 0 {
		t.Fatalf("expected 0 calls with filter=failure on success, got %d", cap.count())
	}
}

func TestFireDeployWebhookRetriesOnFailure(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := hits.Add(1)
		if n == 1 {
			// First hit: 500. Retry must happen.
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	setWebhookInConfig(t, srv.URL, "all")

	FireDeployWebhook(DeployRun{ID: "r1", App: "w", Target: "cloudflare", OK: false})

	// Retry is +2s, so give us 4s headroom.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && hits.Load() < 2 {
		time.Sleep(50 * time.Millisecond)
	}
	if n := hits.Load(); n != 2 {
		t.Fatalf("expected exactly 2 hits (one failure + one retry), got %d", n)
	}
}

func TestFireDeployWebhookNoURLNoPost(t *testing.T) {
	// Config with no DeployWebhookURL → no call. If we wrongly issued
	// a request to "" the test would panic at http.NewRequest.
	setWebhookInConfig(t, "", "all")
	FireDeployWebhook(DeployRun{ID: "n", OK: true})
	time.Sleep(150 * time.Millisecond) // let the (non-)goroutine settle
}

func TestDeployWebhookPayloadOmitsHostWhenEmpty(t *testing.T) {
	// Swap the hostname stub; the webhook payload must omit Host
	// rather than serialising an empty string.
	orig := hostnameForWebhook
	hostnameForWebhook = func() string { return "" }
	defer func() { hostnameForWebhook = orig }()

	var captured []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured, _ = io.ReadAll(r.Body)
		w.WriteHeader(200)
	}))
	defer srv.Close()
	setWebhookInConfig(t, srv.URL, "all")
	FireDeployWebhook(DeployRun{ID: "h", OK: true})
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && len(captured) == 0 {
		time.Sleep(20 * time.Millisecond)
	}
	if len(captured) == 0 {
		t.Fatal("webhook never arrived")
	}
	if contains := string(captured); stringHas(contains, `"host":""`) {
		t.Errorf("payload must omit empty host, got: %s", contains)
	}
}

func TestSignDeployWebhookDeterministic(t *testing.T) {
	// Stable inputs → stable signature. Matters because both retry
	// attempts reuse the same (timestamp, signature) — if the
	// computation drifted, the retry would silently ship a second
	// signature valid for the same body, defeating replay guards.
	sig1 := signDeployWebhook("s3cr3t", "1714065600", []byte(`{"x":1}`))
	sig2 := signDeployWebhook("s3cr3t", "1714065600", []byte(`{"x":1}`))
	if sig1 != sig2 {
		t.Fatalf("signature not deterministic: %q vs %q", sig1, sig2)
	}
	if !stringHas(sig1, "sha256=") {
		t.Errorf("signature missing sha256= prefix: %q", sig1)
	}
	// Different secret → different signature.
	if signDeployWebhook("other", "1714065600", []byte(`{"x":1}`)) == sig1 {
		t.Error("different secret must yield different signature")
	}
	// Different timestamp → different signature (replay guard).
	if signDeployWebhook("s3cr3t", "1714065601", []byte(`{"x":1}`)) == sig1 {
		t.Error("different timestamp must yield different signature")
	}
	// Different body → different signature.
	if signDeployWebhook("s3cr3t", "1714065600", []byte(`{"x":2}`)) == sig1 {
		t.Error("different body must yield different signature")
	}
}

func TestFireDeployWebhookSignsWhenSecretSet(t *testing.T) {
	var rawBody []byte
	var sig, ts string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rawBody, _ = io.ReadAll(r.Body)
		sig = r.Header.Get(WebhookSignatureHeader)
		ts = r.Header.Get(WebhookTimestampHeader)
		w.WriteHeader(200)
	}))
	defer srv.Close()
	setWebhookInConfigWithSecret(t, srv.URL, "all", "super-secret")

	FireDeployWebhook(DeployRun{ID: "signed", App: "mobile", Target: "testflight", OK: true})

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && (sig == "" || len(rawBody) == 0) {
		time.Sleep(20 * time.Millisecond)
	}
	if sig == "" || ts == "" {
		t.Fatalf("expected signature + timestamp headers; sig=%q ts=%q", sig, ts)
	}

	// The signature must verify against the exact bytes the server saw.
	if err := VerifyDeployWebhookSignature("super-secret", ts, sig, rawBody, 5*time.Minute); err != nil {
		t.Errorf("verify failed: %v", err)
	}
	// Wrong secret must not verify.
	if err := VerifyDeployWebhookSignature("wrong", ts, sig, rawBody, 5*time.Minute); err == nil {
		t.Error("wrong secret must not verify")
	}
	// Tampered body must not verify.
	tampered := append([]byte{}, rawBody...)
	if len(tampered) > 5 {
		tampered[5] ^= 0xff
	}
	if err := VerifyDeployWebhookSignature("super-secret", ts, sig, tampered, 5*time.Minute); err == nil {
		t.Error("tampered body must not verify")
	}
}

func TestFireDeployWebhookNoSignatureWhenSecretEmpty(t *testing.T) {
	var sigSeen, tsSeen string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sigSeen = r.Header.Get(WebhookSignatureHeader)
		tsSeen = r.Header.Get(WebhookTimestampHeader)
		w.WriteHeader(200)
	}))
	defer srv.Close()
	setWebhookInConfigWithSecret(t, srv.URL, "all", "")

	FireDeployWebhook(DeployRun{ID: "unsigned", OK: true})

	deadline := time.Now().Add(1 * time.Second)
	for time.Now().Before(deadline) && sigSeen == "" && tsSeen == "" {
		time.Sleep(20 * time.Millisecond)
	}
	// Wait a little extra so we're sure the POST actually went.
	time.Sleep(200 * time.Millisecond)
	if sigSeen != "" {
		t.Errorf("should not emit signature when secret is empty, got %q", sigSeen)
	}
	if tsSeen != "" {
		t.Errorf("should not emit timestamp when secret is empty, got %q", tsSeen)
	}
}

func TestVerifyDeployWebhookSignatureSkewRejects(t *testing.T) {
	secret := "s"
	body := []byte(`{}`)
	// Timestamp in the distant past — skew guard should refuse.
	staleTs := fmt.Sprintf("%d", time.Now().Add(-2*time.Hour).Unix())
	sig := signDeployWebhook(secret, staleTs, body)
	err := VerifyDeployWebhookSignature(secret, staleTs, sig, body, 5*time.Minute)
	if err == nil {
		t.Fatal("expected skew rejection")
	}
	if !stringHas(err.Error(), "skew") {
		t.Errorf("wrong error: %v", err)
	}
	// maxSkew=0 disables the check — same stale signature verifies.
	if err := VerifyDeployWebhookSignature(secret, staleTs, sig, body, 0); err != nil {
		t.Errorf("maxSkew=0 should disable skew guard: %v", err)
	}
}

func TestVerifyDeployWebhookSignatureMissingHeaders(t *testing.T) {
	body := []byte(`{}`)
	if err := VerifyDeployWebhookSignature("", "1", "sha256=x", body, time.Minute); err == nil {
		t.Error("empty secret must error")
	}
	if err := VerifyDeployWebhookSignature("s", "", "sha256=x", body, time.Minute); err == nil {
		t.Error("empty timestamp must error")
	}
	if err := VerifyDeployWebhookSignature("s", "1", "", body, time.Minute); err == nil {
		t.Error("empty signature must error")
	}
}

func stringHas(s, needle string) bool { return len(s) >= len(needle) && indexOf(s, needle) >= 0 }

func indexOf(s, needle string) int {
	n := len(needle)
	if n == 0 {
		return 0
	}
	for i := 0; i+n <= len(s); i++ {
		if s[i:i+n] == needle {
			return i
		}
	}
	return -1
}
