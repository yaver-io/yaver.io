package main

// mcp_instructions.go — content shown to LLM clients on every MCP
// initialize handshake, plus the `project_context` tool that packs
// CLAUDE.md / AGENTS.md / init.md into one call.
//
// Core rule: .md files go stale. We can't stop drift at write time,
// but we can at least tell every LLM-client that picks up our MCP
// "don't trust these, verify by grepping code." Both the initialize
// instructions field and the project_context tool repeat this — the
// init instructions covers clients that read capabilities, the tool
// covers clients that only look at tool outputs.

import (
	"os"
	"path/filepath"
)

const mcpStaleDocsWarning = `IMPORTANT — Yaver project guidance rule.

Every *.md file in this repo (CLAUDE.md, AGENTS.md, AI_ARCH.md,
REMOTE_WORKER.md, init.md, docs/*.md) was accurate on the day it
was written and drifts every time a handler moves, a route
renames, or a field is added. Treat them as starting hints, not
authoritative answers.

Before acting on any claim a .md file makes:

  1. Grep the code for the thing the doc names. If CLAUDE.md says
     "the agent has POST /foo/bar", run:
         grep -n 'HandleFunc.*"/foo/bar"' desktop/agent/*.go
     CLI 1.99.33 shipped with /diagnose handlers compiled in but
     the mux.HandleFunc line missing; the doc claimed it worked
     and the route 404'd in production.

  2. Re-read files on disk, not from memory. A function signature
     or a JSON schema may have changed since your training data
     or since another thread last committed.

  3. Cross-check versions: yaver --version (PATH), /info.version
     (running process), git log --oneline -- <file> (HEAD). If
     the three disagree the doc almost certainly describes a
     different slice of time than the one you're operating on.

  4. When the doc and the code disagree, the code wins AND fix
     the doc as part of the same change.

Full guide: CLAUDE.md. Agent-tool convention variant: AGENTS.md.`

// mcpInstructions returns the string shown to LLM clients on every
// MCP session start. Kept short by design — long strings here burn
// input tokens on every reconnect.
func mcpInstructions() string {
	return mcpStaleDocsWarning
}

// projectContextFiles reads the repo's agent-guidance files + an
// optional per-project init.md and packages them into one payload
// for the `project_context` MCP tool. Every file is prefixed with
// the stale-docs warning so even an LLM that skips the MCP
// initialize instructions sees it.
func projectContextFiles(workDir string) map[string]interface{} {
	out := map[string]interface{}{
		"warning": mcpStaleDocsWarning,
	}
	// Repo-level files live next to the agent binary's repo root;
	// we best-effort look in both $CWD and the agent's taskMgr workDir.
	roots := []string{}
	if cwd, err := os.Getwd(); err == nil {
		roots = append(roots, cwd)
	}
	if workDir != "" && !mcpContainsString(roots, workDir) {
		roots = append(roots, workDir)
	}
	// Also walk up from workDir looking for the repo root (has .git).
	for _, r := range roots {
		if root := findRepoRoot(r); root != "" && !mcpContainsString(roots, root) {
			roots = append(roots, root)
		}
	}

	addFile := func(key, relPath string) {
		for _, root := range roots {
			p := filepath.Join(root, relPath)
			if data, err := os.ReadFile(p); err == nil {
				// Cap each at 16 KB so a bloated CLAUDE.md doesn't
				// explode the MCP response. Clients that need more
				// can read the file directly with Read.
				if len(data) > 16*1024 {
					data = append(data[:16*1024], []byte("\n\n… (truncated — read the full file directly)")...)
				}
				out[key] = string(data)
				out[key+"_path"] = p
				return
			}
		}
	}

	addFile("CLAUDE_md", "CLAUDE.md")
	addFile("AGENTS_md", "AGENTS.md")
	addFile("AI_ARCH_md", "AI_ARCH.md")
	addFile("REMOTE_WORKER_md", "REMOTE_WORKER.md")

	// Per-project init.md lives at the agent's active workdir root.
	if workDir != "" {
		if data, err := os.ReadFile(filepath.Join(workDir, "init.md")); err == nil {
			if len(data) > 16*1024 {
				data = append(data[:16*1024], []byte("\n\n… (truncated)")...)
			}
			out["init_md"] = string(data)
			out["init_md_path"] = filepath.Join(workDir, "init.md")
		}
	}

	return out
}

func mcpContainsString(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

func findRepoRoot(start string) string {
	cur := start
	for i := 0; i < 10; i++ {
		if _, err := os.Stat(filepath.Join(cur, ".git")); err == nil {
			return cur
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			break
		}
		cur = parent
	}
	return ""
}
