package main

import (
	"strings"
	"testing"
	"time"
)

// resetSupport unwires any in-flight session so tests don't leak
// state into each other.
func resetSupport(t *testing.T) {
	t.Helper()
	supportMu.Lock()
	activeSupport = nil
	supportMu.Unlock()
}

func TestSupportCodeFormat(t *testing.T) {
	// Safe alphabet check — no 0/O/1/I, uppercase, length 6.
	for i := 0; i < 100; i++ {
		code := generateSupportCode()
		if len(code) != 6 {
			t.Fatalf("code %q length = %d, want 6", code, len(code))
		}
		for _, ch := range code {
			if !strings.ContainsRune(string(supportCodeAlphabet), ch) {
				t.Fatalf("code %q contains %q outside safe alphabet", code, ch)
			}
		}
	}
}

func TestSupportTokenPrefix(t *testing.T) {
	// Callers key off this prefix in the auth fast path; changing it
	// requires updating httpserver.go too.
	tok := generateSupportToken()
	if !strings.HasPrefix(tok, "yv_supp_") {
		t.Fatalf("token %q missing yv_supp_ prefix", tok)
	}
	if len(tok) < 32 {
		t.Fatalf("token %q shorter than expected (len=%d)", tok, len(tok))
	}
}

func TestStartSessionReplaces(t *testing.T) {
	resetSupport(t)
	s1 := StartSupportSession(SupportStartOptions{Label: "first", TTL: time.Minute})
	s2 := StartSupportSession(SupportStartOptions{Label: "second", TTL: time.Minute})
	if s1.Code == s2.Code {
		t.Fatal("expected new code when start is called twice")
	}
	if !supportTokenValidFor(s2.Token, "/info") {
		t.Fatal("second session token should validate")
	}
	if supportTokenValidFor(s1.Token, "/info") {
		t.Fatal("first session token should no longer validate after replacement")
	}
}

func TestSupportTokenAllowlist(t *testing.T) {
	resetSupport(t)
	sess := StartSupportSession(SupportStartOptions{TTL: time.Minute})
	defer StopSupportSession()

	// Paths inside the default allowlist (read-only / status only).
	ok := []string{"/files/list", "/info", "/agent/status", "/health", "/streams", "/streams/foo"}
	for _, p := range ok {
		if !supportTokenValidFor(sess.Token, p) {
			t.Errorf("expected support token to pass %s", p)
		}
	}
	// Paths deliberately NOT granted: anything that grants RCE / SSRF /
	// owner-only state. The whole point of dropping /exec /ws/terminal
	// /browser/ is that a 6-char redeem code is not enough authority for
	// arbitrary command execution.
	blocked := []string{
		"/vault/read", "/agent/shutdown", "/tasks", "/session/export", "/autodev/start",
		"/exec", "/exec/foo", "/ws/terminal", "/browser/sessions", "/browser/foo",
	}
	for _, p := range blocked {
		if supportTokenValidFor(sess.Token, p) {
			t.Errorf("expected support token to be blocked on %s", p)
		}
	}
}

func TestSupportNoRCEPathsInAllowlist(t *testing.T) {
	// Tripwire: the support session bearer is delivered to anyone who
	// brute-forces or guesses a 6-char code. /exec, /exec/, /ws/terminal,
	// /browser/, and any other path that ends up running a subprocess /
	// PTY / arbitrary URL navigation must NEVER be in this allowlist.
	forbidden := []string{
		"/exec",
		"/exec/",
		"/ws/terminal",
		"/browser/",
		"/browser",
		"/vault/",
		"/vault",
		"/agent/shutdown",
		"/agent/clean",
		"/tasks",
		"/session/",
		"/tmux/",
		"/git/",
		"/repos/",
		"/autodev/",
		"/dev/start",
		"/dev/stop",
		"/deploy/",
		"/hybrid/",
		"/notifications/",
		"/schedules",
		"/sandbox/build",
	}
	for _, bad := range forbidden {
		for _, allowed := range supportAllowedPrefixes {
			normalized := strings.TrimSuffix(allowed, "/")
			badNorm := strings.TrimSuffix(bad, "/")
			if normalized == badNorm {
				t.Errorf("forbidden path %q is in supportAllowedPrefixes (entry %q)", bad, allowed)
			}
		}
	}
}

func TestSupportSegmentAwareMatch(t *testing.T) {
	resetSupport(t)
	sess := StartSupportSession(SupportStartOptions{TTL: time.Minute})
	defer StopSupportSession()

	// /agent/status must NOT match /agent/status-extra (prefix-collision class).
	if supportTokenValidFor(sess.Token, "/agent/status-extra") {
		t.Error("prefix collision: /agent/status-extra leaked through")
	}
	if supportTokenValidFor(sess.Token, "/agent/runners-debug") {
		t.Error("prefix collision: /agent/runners-debug leaked through")
	}
	if supportTokenValidFor(sess.Token, "/healthcheck") {
		t.Error("prefix collision: /healthcheck leaked through")
	}
	// But the legitimate sub-path must still match.
	if !supportTokenValidFor(sess.Token, "/agent/status") {
		t.Error("/agent/status must match exact entry")
	}
	if !supportTokenValidFor(sess.Token, "/files/raw") {
		t.Error("/files/raw must match exact entry")
	}
	// Trailing-slash entries (like /streams/) match sub-paths.
	if !supportTokenValidFor(sess.Token, "/streams/foo") {
		t.Error("/streams/foo must match /streams/ entry")
	}
}

func TestSupportTokenRejectsUnknown(t *testing.T) {
	resetSupport(t)
	StartSupportSession(SupportStartOptions{TTL: time.Minute})
	defer StopSupportSession()
	if supportTokenValidFor("yv_supp_totally-fake", "/info") {
		t.Fatal("arbitrary yv_supp_-prefixed token must not validate")
	}
	if supportTokenValidFor("", "/info") {
		t.Fatal("empty token must not validate")
	}
}

func TestSupportExpiryClearsSession(t *testing.T) {
	resetSupport(t)
	// Tiny TTL — the snapshot function should nil out an expired
	// session so a later start doesn't stack on a ghost.
	sess := StartSupportSession(SupportStartOptions{TTL: 20 * time.Millisecond})
	time.Sleep(50 * time.Millisecond)
	if activeSupportSnapshot() != nil {
		t.Fatal("snapshot must return nil after TTL")
	}
	if supportTokenValidFor(sess.Token, "/info") {
		t.Fatal("expired token must not validate")
	}
	// The snapshot call above should have cleared the pointer.
	supportMu.RLock()
	current := activeSupport
	supportMu.RUnlock()
	if current != nil {
		t.Fatal("activeSupport must be cleared after expiry snapshot")
	}
}

func TestSupportStopRevokes(t *testing.T) {
	resetSupport(t)
	sess := StartSupportSession(SupportStartOptions{TTL: time.Minute})
	if !StopSupportSession() {
		t.Fatal("stop on active session should return true")
	}
	if supportTokenValidFor(sess.Token, "/info") {
		t.Fatal("stopped session token must not validate")
	}
	if StopSupportSession() {
		t.Fatal("second stop should return false — nothing active")
	}
}

func TestSupportShellOptInGrantsExec(t *testing.T) {
	// Hosts that explicitly want the TeamViewer-style remote-help UX
	// can pass --shell. That re-enables /exec, /ws/terminal, /browser/*
	// on top of the read-only default. The opt-in is per-session and
	// embedded in the session struct, so revocation kills shell access.
	resetSupport(t)
	sess := StartSupportSession(SupportStartOptions{TTL: time.Minute, Shell: true})
	defer StopSupportSession()

	if !sess.Shell {
		t.Fatal("Shell flag must round-trip from options to session")
	}
	for _, p := range []string{"/exec", "/exec/abc", "/ws/terminal", "/browser/", "/browser/sessions"} {
		if !supportTokenValidFor(sess.Token, p) {
			t.Errorf("--shell session expected to allow %s but did not", p)
		}
	}
	// vault / shutdown / tasks remain blocked even with --shell — those
	// are owner-only and not part of "remote help".
	for _, p := range []string{"/vault/read", "/agent/shutdown", "/tasks", "/session/export"} {
		if supportTokenValidFor(sess.Token, p) {
			t.Errorf("--shell session must still block %s", p)
		}
	}
}

func TestSupportRedeemCode(t *testing.T) {
	resetSupport(t)
	sess := StartSupportSession(SupportStartOptions{TTL: time.Minute})
	defer StopSupportSession()

	got := supportSessionRedeem(sess.Code)
	if got == nil || got.Token != sess.Token {
		t.Fatal("redeem with exact code should return the session")
	}
	if supportSessionRedeem(strings.ToLower(sess.Code)) == nil {
		t.Fatal("redeem should be case-insensitive")
	}
	if supportSessionRedeem("NOTACODE") != nil {
		t.Fatal("redeem with wrong code must return nil")
	}
}
