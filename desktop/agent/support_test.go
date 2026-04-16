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
	s1 := StartSupportSession("first", time.Minute)
	s2 := StartSupportSession("second", time.Minute)
	if s1.Code == s2.Code {
		t.Fatal("expected new code when start is called twice")
	}
	if !supportTokenValidFor(s2.Token, "/exec") {
		t.Fatal("second session token should validate")
	}
	if supportTokenValidFor(s1.Token, "/exec") {
		t.Fatal("first session token should no longer validate after replacement")
	}
}

func TestSupportTokenAllowlist(t *testing.T) {
	resetSupport(t)
	sess := StartSupportSession("", time.Minute)
	defer StopSupportSession()

	// Paths inside the default allowlist.
	ok := []string{"/exec", "/exec/abc", "/ws/terminal", "/files/list", "/info", "/agent/status"}
	for _, p := range ok {
		if !supportTokenValidFor(sess.Token, p) {
			t.Errorf("expected support token to pass %s", p)
		}
	}
	// Paths deliberately NOT granted: vault, shutdown, tasks, session.
	blocked := []string{"/vault/read", "/agent/shutdown", "/tasks", "/session/export", "/autodev/start"}
	for _, p := range blocked {
		if supportTokenValidFor(sess.Token, p) {
			t.Errorf("expected support token to be blocked on %s", p)
		}
	}
}

func TestSupportTokenRejectsUnknown(t *testing.T) {
	resetSupport(t)
	StartSupportSession("", time.Minute)
	defer StopSupportSession()
	if supportTokenValidFor("yv_supp_totally-fake", "/exec") {
		t.Fatal("arbitrary yv_supp_-prefixed token must not validate")
	}
	if supportTokenValidFor("", "/exec") {
		t.Fatal("empty token must not validate")
	}
}

func TestSupportExpiryClearsSession(t *testing.T) {
	resetSupport(t)
	// Tiny TTL — the snapshot function should nil out an expired
	// session so a later start doesn't stack on a ghost.
	sess := StartSupportSession("", 20*time.Millisecond)
	time.Sleep(50 * time.Millisecond)
	if activeSupportSnapshot() != nil {
		t.Fatal("snapshot must return nil after TTL")
	}
	if supportTokenValidFor(sess.Token, "/exec") {
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
	sess := StartSupportSession("", time.Minute)
	if !StopSupportSession() {
		t.Fatal("stop on active session should return true")
	}
	if supportTokenValidFor(sess.Token, "/exec") {
		t.Fatal("stopped session token must not validate")
	}
	if StopSupportSession() {
		t.Fatal("second stop should return false — nothing active")
	}
}

func TestSupportRedeemCode(t *testing.T) {
	resetSupport(t)
	sess := StartSupportSession("", time.Minute)
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
