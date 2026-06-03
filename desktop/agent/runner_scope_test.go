package main

import (
	"net/http"
	"testing"
)

func reqWithHeaders(h map[string]string) *http.Request {
	r, _ := http.NewRequest("POST", "http://x/tasks", nil)
	for k, v := range h {
		r.Header.Set(k, v)
	}
	return r
}

func TestParseRunnerAllowCSV(t *testing.T) {
	if got := parseRunnerAllowCSV(""); got != nil {
		t.Fatalf("empty CSV should be nil (no constraint), got %v", got)
	}
	if got := parseRunnerAllowCSV("  ,  "); got != nil {
		t.Fatalf("blank-only CSV should be nil, got %v", got)
	}
	got := parseRunnerAllowCSV("opencode, codex , claude-code")
	if !got["opencode"] || !got["codex"] || !got["claude"] {
		t.Fatalf("expected opencode/codex/claude (normalized), got %v", got)
	}
	if got["claude-code"] {
		t.Fatalf("claude-code should normalize to claude, not stay raw")
	}
}

func TestRunnerDeniedByScopeHeaders_NoConstraint(t *testing.T) {
	// No scope headers → any runner allowed.
	if d := runnerDeniedByScopeHeaders(reqWithHeaders(nil), "codex", "opencode"); d != nil {
		t.Fatalf("no headers should impose no constraint, got denial %q", d.Reason)
	}
}

func TestRunnerDeniedByScopeHeaders_SdkScope(t *testing.T) {
	r := reqWithHeaders(map[string]string{"X-Yaver-SdkAllowedRunners": "opencode"})
	if d := runnerDeniedByScopeHeaders(r, "codex", "opencode"); d == nil {
		t.Fatalf("codex should be denied when sdk scope allows only opencode")
	}
	if d := runnerDeniedByScopeHeaders(r, "opencode", "opencode"); d != nil {
		t.Fatalf("opencode should be allowed, got denial %q", d.Reason)
	}
}

func TestRunnerDeniedByScopeHeaders_JointlyInclusive(t *testing.T) {
	// Both layers present: requested runner must satisfy EVERY layer (intersection).
	r := reqWithHeaders(map[string]string{
		"X-Yaver-HostShareAllowedRunners": "opencode",          // host-share: only opencode
		"X-Yaver-SdkAllowedRunners":       "opencode,codex",    // sdk: opencode or codex
	})
	if d := runnerDeniedByScopeHeaders(r, "codex", "opencode"); d == nil {
		t.Fatalf("codex must be denied because host-share excludes it, even though sdk allows it")
	}
	if d := runnerDeniedByScopeHeaders(r, "opencode", "opencode"); d != nil {
		t.Fatalf("opencode is in both layers and must be allowed, got %q", d.Reason)
	}
}

func TestRunnerDeniedByScopeHeaders_DefaultFallback(t *testing.T) {
	// Empty requested runner falls back to the default runner for the check.
	r := reqWithHeaders(map[string]string{"X-Yaver-SdkAllowedRunners": "opencode"})
	if d := runnerDeniedByScopeHeaders(r, "", "codex"); d == nil {
		t.Fatalf("default runner codex should be denied by an opencode-only scope")
	}
	if d := runnerDeniedByScopeHeaders(r, "", "opencode"); d != nil {
		t.Fatalf("default runner opencode should be allowed, got %q", d.Reason)
	}
}

func TestStampSdkRunnerScope(t *testing.T) {
	// Inbound spoof must be deleted, then re-stamped only from a real scope.
	r := reqWithHeaders(map[string]string{"X-Yaver-SdkAllowedRunners": "claude,codex,opencode"})
	stampSdkRunnerScope(r, []string{"workKind:app-code", "runners:opencode"})
	if got := r.Header.Get("X-Yaver-SdkAllowedRunners"); got != "opencode" {
		t.Fatalf("scope should overwrite spoofed header with the token value, got %q", got)
	}

	// No runner scope → header cleared (inbound spoof removed), never trusted.
	r2 := reqWithHeaders(map[string]string{"X-Yaver-SdkAllowedRunners": "claude,codex"})
	stampSdkRunnerScope(r2, []string{"feedback", "blackbox"})
	if got := r2.Header.Get("X-Yaver-SdkAllowedRunners"); got != "" {
		t.Fatalf("no runner scope should leave header empty (spoof stripped), got %q", got)
	}
}
