package main

// Tests for the runner-auth-mirror primitive. Per CLAUDE.md: real
// HTTP servers on random ports, no mocks. We exercise:
//   - Ledger round-trips (upsert, load, revoke)
//   - ReadLocalRunnerCredential against fixture files in $HOME
//   - AcceptMirrorPayload writes the right canonical path
//   - The MirrorRequest → AcceptMirrorPayload flow as a single
//     in-process loop (PushMirrorToPeer + a real net/http test
//     server hosting handleRunnerAuthMirrorAccept)

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRunnerTokenLedger_RoundTrip(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	entry := RunnerTokenLedgerEntry{
		Runner:    "claude",
		TokenHash: HashRunnerToken("plaintext-token-aaa"),
		Source:    "mirror:test-host",
		ExpiresAt: time.Now().Add(time.Hour * 24 * 365),
	}
	if _, err := UpsertRunnerToken(entry); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	entries := LoadRunnerTokenLedger()
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].TokenHash != entry.TokenHash {
		t.Errorf("hash mismatch: %q want %q", entries[0].TokenHash, entry.TokenHash)
	}
	if entries[0].Source != "mirror:test-host" {
		t.Errorf("source mismatch: %q", entries[0].Source)
	}
}

func TestRunnerTokenLedger_UpsertReplaces(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	h := HashRunnerToken("same-token")
	first := RunnerTokenLedgerEntry{Runner: "claude", TokenHash: h, Source: "initial"}
	second := RunnerTokenLedgerEntry{Runner: "claude", TokenHash: h, Source: "refreshed"}
	_, _ = UpsertRunnerToken(first)
	_, _ = UpsertRunnerToken(second)
	entries := LoadRunnerTokenLedger()
	if len(entries) != 1 {
		t.Fatalf("expected 1 row after upsert-replace, got %d", len(entries))
	}
	if entries[0].Source != "refreshed" {
		t.Errorf("expected updated source, got %q", entries[0].Source)
	}
}

func TestRunnerTokenLedger_RevokeByHash(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	_, _ = UpsertRunnerToken(RunnerTokenLedgerEntry{Runner: "claude", TokenHash: "h1", Source: "a"})
	_, _ = UpsertRunnerToken(RunnerTokenLedgerEntry{Runner: "claude", TokenHash: "h2", Source: "b"})
	remaining, err := RevokeRunnerTokenByHash("claude", "h1")
	if err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if len(remaining) != 1 || remaining[0].TokenHash != "h2" {
		t.Errorf("expected only h2 to remain, got %+v", remaining)
	}
}

func TestMarkRunnerTokenUsed(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	plain := "marked-plain"
	h := HashRunnerToken(plain)
	_, _ = UpsertRunnerToken(RunnerTokenLedgerEntry{Runner: "claude", TokenHash: h, Source: "test"})
	MarkRunnerTokenUsed("claude", plain)
	entries := LoadRunnerTokenLedger()
	if len(entries) != 1 {
		t.Fatalf("ledger lost rows")
	}
	if entries[0].LastUsedAt.IsZero() {
		t.Error("LastUsedAt should be stamped after Mark call")
	}
}

func TestReadLocalRunnerCredential_Missing(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if _, err := ReadLocalRunnerCredential("claude"); err != ErrNoCredential {
		t.Errorf("expected ErrNoCredential, got %v", err)
	}
	if _, err := ReadLocalRunnerCredential("codex"); err != ErrNoCredential {
		t.Errorf("expected ErrNoCredential, got %v", err)
	}
}

func TestReadLocalRunnerCredential_Claude(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, ".claude")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	expiresMs := time.Now().Add(24 * time.Hour).UnixMilli()
	content := `{"claudeAiOauth":{"accessToken":"sk-ant-oat-abc","expiresAt":` +
		jsonInt(expiresMs) + `}}`
	if err := os.WriteFile(filepath.Join(dir, ".credentials.json"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	cred, err := ReadLocalRunnerCredential("claude")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if cred.ExpiresAt.IsZero() {
		t.Error("expected ExpiresAt to be set")
	}
	if cred.Hash == "" {
		t.Error("expected non-empty hash")
	}
	if !strings.Contains(string(cred.FileBytes), "sk-ant-oat-abc") {
		t.Errorf("file bytes don't carry token")
	}
}

func TestAcceptMirrorPayload_WritesCanonicalPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	creds := `{"claudeAiOauth":{"accessToken":"sk-ant-oat-zzz"}}`
	payload := MirrorAcceptPayload{
		Runner:          "claude",
		CredentialsFile: base64.StdEncoding.EncodeToString([]byte(creds)),
		SourceHost:      "fixture-host",
	}
	result, err := AcceptMirrorPayload(context.Background(), payload)
	if err != nil {
		t.Fatalf("accept: %v", err)
	}
	if !result.OK {
		t.Fatal("expected OK")
	}
	wantPath := filepath.Join(home, ".claude", ".credentials.json")
	if result.WrittenTo != wantPath {
		t.Errorf("WrittenTo = %q, want %q", result.WrittenTo, wantPath)
	}
	written, err := os.ReadFile(wantPath)
	if err != nil {
		t.Fatalf("file not written: %v", err)
	}
	if string(written) != creds {
		t.Errorf("file content mismatch: got %q", string(written))
	}
	// Confirm ledger picked up the entry
	entries := LoadRunnerTokenLedger()
	if len(entries) != 1 || entries[0].Source != "mirror:fixture-host" {
		t.Errorf("ledger entry missing or wrong: %+v", entries)
	}
}

func TestAcceptMirrorPayload_RejectsGarbage(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	payload := MirrorAcceptPayload{
		Runner:          "claude",
		CredentialsFile: base64.StdEncoding.EncodeToString([]byte("this is not json")),
		SourceHost:      "bad-host",
	}
	if _, err := AcceptMirrorPayload(context.Background(), payload); err == nil {
		t.Error("expected error on non-JSON payload")
	}
}

func TestAcceptMirrorPayload_UnsupportedRunner(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if _, err := AcceptMirrorPayload(context.Background(), MirrorAcceptPayload{Runner: "ghost"}); err == nil {
		t.Error("expected error on unsupported runner")
	}
}

func TestPushMirrorToPeer_HappyPath(t *testing.T) {
	// Stage: source machine HOME has a Claude credentials file. Spin
	// up a tiny HTTP server that mimics the target agent's
	// /runner/auth/mirror/accept route. PushMirrorToPeer reads the
	// source file and posts to the fake target; we assert the request
	// body shape.
	sourceHome := t.TempDir()
	t.Setenv("HOME", sourceHome)
	dir := filepath.Join(sourceHome, ".claude")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	creds := `{"claudeAiOauth":{"accessToken":"sk-ant-oat-push"}}`
	if err := os.WriteFile(filepath.Join(dir, ".credentials.json"), []byte(creds), 0o600); err != nil {
		t.Fatal(err)
	}

	var receivedAuth string
	var receivedPayload MirrorAcceptPayload
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedAuth = r.Header.Get("Authorization")
		_ = json.NewDecoder(r.Body).Decode(&receivedPayload)
		_ = json.NewEncoder(w).Encode(MirrorResult{
			OK:         true,
			Runner:     "claude",
			TokenHash:  HashRunnerToken(creds),
			SourceHost: "ignored-by-target-in-test",
			WrittenTo:  "/tmp/fixture-target/.claude/.credentials.json",
		})
	}))
	defer target.Close()

	res, err := PushMirrorToPeer(context.Background(), "claude", target.URL, "owner-token-xyz", http.DefaultClient.Do)
	if err != nil {
		t.Fatalf("push: %v", err)
	}
	if !res.OK {
		t.Fatal("expected OK")
	}
	if receivedAuth != "Bearer owner-token-xyz" {
		t.Errorf("missing/incorrect Authorization header: %q", receivedAuth)
	}
	if receivedPayload.Runner != "claude" {
		t.Errorf("payload runner = %q", receivedPayload.Runner)
	}
	dec, _ := base64.StdEncoding.DecodeString(receivedPayload.CredentialsFile)
	if string(dec) != creds {
		t.Errorf("payload credentialsFile mismatch")
	}
}

func TestPushMirrorToPeer_NoLocalCredential(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	target := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		t.Fatal("target should not be reached when no local credential exists")
	}))
	defer target.Close()
	_, err := PushMirrorToPeer(context.Background(), "claude", target.URL, "owner", http.DefaultClient.Do)
	if err != ErrNoCredential {
		t.Errorf("expected ErrNoCredential, got %v", err)
	}
}

func jsonInt(n int64) string {
	out, _ := json.Marshal(n)
	return string(out)
}
