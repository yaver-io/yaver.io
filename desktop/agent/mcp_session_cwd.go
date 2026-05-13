package main

// mcp_session_cwd.go — session-scoped working directory for MCP tools.
//
// Why this exists
// ---------------
// Claude Code / Codex / opencode spawn `yaver mcp` as a stdio
// subprocess each time the user starts a session. That subprocess
// inherits $PWD from the AI client, so on its own `os.Getwd()`
// already points at the AI session's working directory — e.g. when
// the user runs `cd /Users/kivanc/Workspace/sfmg && claude`, the
// spawned yaver-mcp process sees `/Users/.../sfmg` as cwd.
//
// In practice that works today, but it is fragile:
//
//   - any code path that calls `os.Chdir` later (legitimate or
//     accidental) silently re-aims every subsequent `os.Getwd()`
//     fallback at the new directory
//   - HTTP MCP mode (`yaver mcp --mode http`) runs as a long-lived
//     daemon that may be started from somewhere else entirely
//   - some MCP clients launch their stdio servers from a config
//     home rather than the AI's project root
//
// Pinning the working directory once at MCP startup, in a single
// package-level variable, removes that fragility — tools that
// previously fell back to `os.Getwd()` now read the pinned value
// first. The env-var override (`YAVER_MCP_CWD`) gives callers a
// way to inject the AI's cwd even when they cannot control the
// spawned process's $PWD (e.g. when patching an existing MCP
// registration).
//
// Scoped to the push / build / dev tools that need a *project*
// path — analysis / search / compiler tools keep their existing
// behavior so the diff stays narrow.

import (
	"os"
	"strings"
	"sync"
)

var (
	mcpSessionCwdMu sync.RWMutex
	mcpSessionCwd   string
)

// SetMCPSessionCwd records the working directory that every
// path-defaulting MCP tool should treat as "the project root the
// caller is currently in". Called once from runMCP after parsing
// the --work-dir flag. Empty input is ignored so callers can pass
// a flag value verbatim without guarding.
func SetMCPSessionCwd(p string) {
	p = strings.TrimSpace(p)
	if p == "" {
		return
	}
	mcpSessionCwdMu.Lock()
	mcpSessionCwd = p
	mcpSessionCwdMu.Unlock()
}

// GetMCPSessionCwd returns the pinned value (or "" if unset).
// Exposed for tests and for tools that want to distinguish "no
// session cwd known" from "session cwd is the agent's cwd".
func GetMCPSessionCwd() string {
	mcpSessionCwdMu.RLock()
	defer mcpSessionCwdMu.RUnlock()
	return mcpSessionCwd
}

// ResolveMCPCwd is the single resolver every push/build/dev MCP
// tool calls when its `path` / `work_dir` argument is empty.
// Order of precedence:
//
//  1. YAVER_MCP_CWD env var — set per-call by wrappers that
//     can't control the spawned process's $PWD (e.g. a shell
//     alias that exports it before invoking `yaver mcp`).
//  2. mcpSessionCwd pinned by runMCP at startup.
//  3. os.Getwd() — same behavior as before; preserves correctness
//     when neither of the first two is set (CLI invocations, unit
//     tests, ad-hoc tooling).
func ResolveMCPCwd() string {
	if v := strings.TrimSpace(os.Getenv("YAVER_MCP_CWD")); v != "" {
		return v
	}
	if v := GetMCPSessionCwd(); v != "" {
		return v
	}
	wd, _ := os.Getwd()
	return wd
}
