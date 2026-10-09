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
	"time"
)

// mcpInitializeResult is shared by the HTTP and stdio transports. The stdio
// lane is what Codex, Claude Code, and OpenCode normally launch; it previously
// omitted instructions while HTTP included them, so a clean local client never
// received the very discovery guidance the server claimed to provide.
func mcpInitializeResult() map[string]interface{} {
	return map[string]interface{}{
		"protocolVersion": "2024-11-05",
		"capabilities": map[string]interface{}{
			"tools": map[string]interface{}{},
		},
		"serverInfo": map[string]interface{}{
			"name":    "yaver",
			"version": version,
		},
		"instructions": mcpInstructions(),
	}
}

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
	instructions := mcpStaleDocsWarning + `

Vision (screenshots, crash logs, UI failures, video frames):

  If you are a TEXT-ONLY model (no vision), you cannot see the image
  blocks some tools return. When a screenshot, pasted image, crash-log
  capture, or UI-failure image is involved:

    1. vision_analyze_image {source} — analyze an image and get TEXT
       back: on-device OCR (free) plus a semantic verdict when a vision
       provider is configured. source = file path, base64:..., or URL.
    2. ui_inspect {surface: browser|selenium|droid|simulator|mac,
       session_id?} — one-call capture + text analysis of a surface.
    3. testkit_visual_check {session_id, question} — PASS/WARN/FAIL
       pixel judgment for automated browser tests (needs a vision
       provider: MISTRAL/OPENAI/ANTHROPIC key).
    4. vision_pdf_extract {source} — pull text out of PDFs (crash logs,
       reports). vision_diff {source_a, source_b} — changed-region stats.
    5. mac_ui_snapshot — macOS Accessibility element tree of the
       frontmost app (the Mac equivalent of droid_ui_texts / DOM).

  Free-first: OCR and the accessibility/DOM trees cost nothing. The
  vision LLM is only used when you ask for a semantic verdict, and the
  server auto-rewrites image results into this text analysis for
  text-only clients. If no vision provider is configured, the tools say
  so and still return free OCR.

Dogfood mode (Yaver rendering Yaver):

  When the user says reload, re-render, or refresh Yaver itself, call
  dogfood_status first. If it reports active, call dogfood_rerender on that
  renderer device. Never substitute mobile_hermes_reload or reload whichever
  unrelated project happens to be active. If Dogfood is inactive, keep the
  normal Production/project reload semantics.

Third-party app Dogfood reload:

  When an app developer asks to reload the app they are dogfooding, use
  dogfood_reload with the selected render box, lane, and exact project_path.
  Render that checkout's CURRENT working tree — commonly a solo developer's
  main branch — without committing, checking out, rebasing, pulling, or pushing
  Git. Keep coding in the user's chosen control plane; Reload Only mode does not
  imply or require SDK chat.

Existing app integration:

  When the user asks to add Yaver to an existing Expo app, call
  yaver_sdk_integrate with the explicit app directory. Do not hand-edit the
  root, install a separate web SDK, invent bundle identifiers, or claim setup
  from package presence alone. The integration tool installs compatible
  dependencies, mounts the FeedbackModal, wires the Expo plugin, and verifies
  the actual project. Use verify=web when a browser bundle is part of the
  requested surface.

Remote React Native UI review:

  When asked to inspect or overview a mobile app on a remote machine, call
  mobile-test-open with mode=overview (or omit mode). It drives the browser
  lane headlessly with a real iPhone device context, inventories static Expo
  Router screens, and returns a manifest plus screenshots. Never choose
  mode=open, a physical phone, QR scanning, or a local sign-in prompt unless
  the user explicitly requests a manual headed session. An auth-blocked route
  is a named overview result, not a reason to ask for physical access.

Fleet coding:

  For substantial coding work, prefer agent_fleet_run. It makes the master
  produce architecture, roadmap, risks, and a testing strategy; runs an
  OpenCode worker by default; then returns to the master for independent
  validation of the worker report and available diff evidence. Master and
  worker may be separate runner processes on the same computer. Poll with
  agent_graph_show. The workflow never authorizes push or deploy.

OpenRouter inside an app:

  For a generated Expo + Convex starter, call yaver_openrouter_integrate at
  the explicit monorepo root before asking the coding runner to invent proxy
  code. It keeps the API key in Convex env, requires a starter session token,
  rate-limits per user, passes OpenRouter SSE directly to the client, and can
  write the React Native stream helper. It refuses apps without an auth
  boundary. Use Vibing afterward for domain UI, image inputs, and final-answer
  history.`
	if runtime := currentDogfoodRuntime(time.Now()); runtime.Active {
		instructions += "\n\nCURRENT RUNTIME: DOGFOOD ACTIVE on " + runtime.WorkDir + ". Route Yaver re-render intent through dogfood_rerender."
	} else {
		instructions += "\n\nCURRENT RUNTIME: PRODUCTION (no local Dogfood attach session). Use dogfood_status with device_id when another owned machine may be the renderer."
	}
	return instructions
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
