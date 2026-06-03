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

func TestMcpToolMatchesPatterns(t *testing.T) {
	cases := []struct {
		tool     string
		patterns []string
		want     bool
	}{
		{"code_build", []string{"*"}, true},
		{"code_build", []string{"code_*"}, true},
		{"code_build", []string{"talos_*", "code_*"}, true},
		{"web_preview_start", []string{"code_*", "talos_*"}, false},
		{"talos_robot_status", []string{"talos_robot_status"}, true}, // exact
		{"talos_robot_start", []string{"talos_robot_status"}, false},
		{"anything", []string{}, false}, // empty list matches nothing
		{"anything", []string{""}, false},
	}
	for _, c := range cases {
		if got := mcpToolMatchesPatterns(c.tool, c.patterns); got != c.want {
			t.Errorf("mcpToolMatchesPatterns(%q, %v) = %v, want %v", c.tool, c.patterns, got, c.want)
		}
	}
}

func TestMcpToolDeniedByScope(t *testing.T) {
	// No header → owner / unconstrained → allowed.
	if d := mcpToolDeniedByScope(reqWithHeaders(nil), "vault_get"); d != nil {
		t.Fatalf("absent header should impose no constraint, got %q", d.Reason)
	}
	// "(none)" → viewer role → deny every tool.
	if d := mcpToolDeniedByScope(reqWithHeaders(map[string]string{"X-Yaver-AllowedTools": "(none)"}), "git_info"); d == nil {
		t.Fatalf("(none) must deny all tools")
	}
	// Pattern allowlist.
	r := reqWithHeaders(map[string]string{"X-Yaver-AllowedTools": "code_*,talos_*"})
	if d := mcpToolDeniedByScope(r, "code_build"); d != nil {
		t.Fatalf("code_build should be allowed by code_*, got %q", d.Reason)
	}
	if d := mcpToolDeniedByScope(r, "exec_command"); d == nil {
		t.Fatalf("exec_command must be denied when allowlist is code_*/talos_*")
	}
}

func TestStampMcpToolScope(t *testing.T) {
	// tools: scope overwrites any inbound spoof.
	r := reqWithHeaders(map[string]string{"X-Yaver-AllowedTools": "*"})
	stampMcpToolScope(r, []string{"runners:opencode", "tools:code_*,talos_*"})
	if got := r.Header.Get("X-Yaver-AllowedTools"); got != "code_*,talos_*" {
		t.Fatalf("tools scope should overwrite spoof, got %q", got)
	}
	// No tools scope → inbound spoof stripped to empty.
	r2 := reqWithHeaders(map[string]string{"X-Yaver-AllowedTools": "*"})
	stampMcpToolScope(r2, []string{"runners:opencode"})
	if got := r2.Header.Get("X-Yaver-AllowedTools"); got != "" {
		t.Fatalf("no tools scope should strip header, got %q", got)
	}
}
