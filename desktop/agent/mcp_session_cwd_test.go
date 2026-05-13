package main

import (
	"os"
	"testing"
)

// Resets package state between subtests so order doesn't matter.
func resetMCPSessionCwdForTest(t *testing.T) {
	t.Helper()
	mcpSessionCwdMu.Lock()
	mcpSessionCwd = ""
	mcpSessionCwdMu.Unlock()
	t.Setenv("YAVER_MCP_CWD", "")
}

func TestResolveMCPCwd_FallsBackToOsGetwd(t *testing.T) {
	resetMCPSessionCwdForTest(t)
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if got := ResolveMCPCwd(); got != wd {
		t.Fatalf("ResolveMCPCwd() = %q, want %q (os.Getwd fallback)", got, wd)
	}
}

func TestResolveMCPCwd_PrefersSessionCwd(t *testing.T) {
	resetMCPSessionCwdForTest(t)
	SetMCPSessionCwd("/tmp/session-pinned")
	if got := ResolveMCPCwd(); got != "/tmp/session-pinned" {
		t.Fatalf("ResolveMCPCwd() = %q, want pinned session value", got)
	}
}

func TestResolveMCPCwd_EnvOverridesSession(t *testing.T) {
	resetMCPSessionCwdForTest(t)
	SetMCPSessionCwd("/tmp/session-pinned")
	t.Setenv("YAVER_MCP_CWD", "/tmp/env-override")
	if got := ResolveMCPCwd(); got != "/tmp/env-override" {
		t.Fatalf("ResolveMCPCwd() = %q, want env override", got)
	}
}

func TestSetMCPSessionCwd_IgnoresEmpty(t *testing.T) {
	resetMCPSessionCwdForTest(t)
	SetMCPSessionCwd("/tmp/first")
	SetMCPSessionCwd("   ")
	SetMCPSessionCwd("")
	if got := GetMCPSessionCwd(); got != "/tmp/first" {
		t.Fatalf("GetMCPSessionCwd() = %q, want first non-empty value to survive", got)
	}
}

func TestSetMCPSessionCwd_TrimsWhitespace(t *testing.T) {
	resetMCPSessionCwdForTest(t)
	SetMCPSessionCwd("   /tmp/with-space  \n")
	if got := GetMCPSessionCwd(); got != "/tmp/with-space" {
		t.Fatalf("GetMCPSessionCwd() = %q, want trimmed value", got)
	}
}
