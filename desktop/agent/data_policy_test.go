package main

import (
	"strings"
	"testing"
	"time"
)

func TestRedactPII_HighConfidenceSpans(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string // substring that must appear
		gone string // substring that must NOT appear
	}{
		{"email", "ping me at jane.doe@acme.co please", "[redacted-email]", "jane.doe@acme.co"},
		{"openai key", "key sk-abcdef0123456789ABCDEF here", "[redacted-secret]", "sk-abcdef0123456789ABCDEF"},
		{"github token", "token ghp_ABCDEFGHIJKLMNOPQRSTUVWX0123 set", "[redacted-secret]", "ghp_ABCDEFGHIJKLMNOPQRSTUVWX0123"},
		{"bearer", "Authorization: Bearer abcDEF123456ghiJKL", "Bearer [redacted-token]", "abcDEF123456ghiJKL"},
		{"ipv4", "connect to 192.168.10.42:443", "[redacted-ip]", "192.168.10.42"},
		{"phone", "call +1 415-555-0132 today", "[redacted-phone]", "+1 415-555-0132"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out, n := RedactPII(c.in)
			if n == 0 {
				t.Fatalf("%s: expected a redaction, got none (out=%q)", c.name, out)
			}
			if !strings.Contains(out, c.want) {
				t.Fatalf("%s: want %q in %q", c.name, c.want, out)
			}
			if c.gone != "" && strings.Contains(out, c.gone) {
				t.Fatalf("%s: %q should be gone from %q", c.name, c.gone, out)
			}
		})
	}
}

func TestRedactPII_Card_LuhnOnly(t *testing.T) {
	// Valid Visa test number (Luhn-valid) → redacted.
	good, n := RedactPII("card 4111 1111 1111 1111 on file")
	if n == 0 || !strings.Contains(good, "[redacted-card]") {
		t.Fatalf("expected card redaction, got %q", good)
	}
	// A long non-Luhn integer (e.g. a code constant) → untouched.
	bad, n2 := RedactPII("const MAX = 1234567890123456")
	if n2 != 0 || !strings.Contains(bad, "1234567890123456") {
		t.Fatalf("non-Luhn integer must not be redacted, got %q (n=%d)", bad, n2)
	}
}

func TestRedactPII_DoesNotEatOrdinaryCode(t *testing.T) {
	code := "for (let i = 0; i < 100000; i++) { total += i; } // semver 1.2.3"
	out, n := RedactPII(code)
	if n != 0 || out != code {
		t.Fatalf("ordinary code was altered: n=%d out=%q", n, out)
	}
}

func TestDataPolicy_ApplyToPrompt(t *testing.T) {
	p := DataPolicy{RedactPII: true}
	got := p.ApplyToPrompt("email admin@x.io")
	if strings.Contains(got, "admin@x.io") {
		t.Fatalf("redaction not applied: %q", got)
	}
	off := DataPolicy{RedactPII: false}
	if off.ApplyToPrompt("email admin@x.io") != "email admin@x.io" {
		t.Fatalf("redaction must be a no-op when disabled")
	}
}

func TestTasksToPrune_RetentionWindow(t *testing.T) {
	now := time.Date(2026, 6, 3, 12, 0, 0, 0, time.UTC)
	old := now.Add(-40 * 24 * time.Hour)
	recent := now.Add(-5 * 24 * time.Hour)
	tasks := []*Task{
		{ID: "old-done", Status: TaskStatusFinished, FinishedAt: &old},
		{ID: "recent-done", Status: TaskStatusFinished, FinishedAt: &recent},
		{ID: "running", Status: TaskStatusRunning},         // no FinishedAt → never pruned
		{ID: "old-but-running", Status: TaskStatusRunning}, // never pruned even if old
	}
	got := tasksToPrune(tasks, 30, now)
	if len(got) != 1 || got[0] != 0 {
		t.Fatalf("expected only index 0 (old-done) pruned, got %v", got)
	}
	// retentionDays <= 0 disables pruning.
	if tasksToPrune(tasks, 0, now) != nil {
		t.Fatalf("retentionDays=0 must disable pruning")
	}
}
