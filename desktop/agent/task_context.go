package main

import (
	"fmt"
	"log"
	"path/filepath"
	"strings"
)

// autoSwitchProject detects project references in a task prompt and switches
// the workDir for THIS TASK to that project. The global workDir is unchanged.
// This enables "fix login in talos" from mobile when serving from ~/yaver.io.
func (tm *TaskManager) autoSwitchProject(task *Task, prompt string) {
	// Strategy 1: Pattern-based extraction (verbs + project name)
	lower := strings.ToLower(prompt)
	patterns := []string{
		"start ", "load ", "open ", "hot reload ", "reload ",
		"run ", "test ", "deploy ", "build ", "fix ", "add ", "update ",
		"switch to ", "go to ", "work on ", "clone ", "pull ",
		"in ", "for ", "on ", "of ",
	}

	for _, p := range patterns {
		idx := strings.Index(lower, p)
		if idx < 0 {
			continue
		}
		after := strings.TrimSpace(prompt[idx+len(p):])
		words := strings.Fields(after)
		if len(words) == 0 {
			continue
		}

		// Try first word, then first two words, then first three
		candidates := []string{words[0]}
		if len(words) > 1 {
			candidates = append(candidates, words[0]+" "+words[1])
		}
		if len(words) > 2 {
			candidates = append(candidates, words[0]+" "+words[1]+" "+words[2])
		}

		for _, candidate := range candidates {
			candidate = strings.TrimRight(candidate, ".,!?;:'\"")
			candidate = strings.TrimSuffix(candidate, " on")
			candidate = strings.TrimSuffix(candidate, " app")
			candidate = strings.TrimSuffix(candidate, " repo")
			candidate = strings.TrimSuffix(candidate, " project")

			if len(candidate) < 2 {
				continue
			}

			skip := map[string]bool{
				"the": true, "my": true, "this": true, "a": true, "an": true,
				"it": true, "all": true, "dev": true, "server": true,
				"now": true, "here": true, "phone": true, "app": true,
				"code": true, "bug": true, "feature": true, "error": true,
				"new": true, "some": true, "that": true, "login": true,
				"page": true, "screen": true, "button": true, "ui": true,
			}
			if skip[strings.ToLower(candidate)] {
				continue
			}

			projectPath, err := findProject(strings.ToLower(candidate))
			if err != nil {
				continue
			}

			// Found a match — set workDir for THIS task only
			task.WorkDir = projectPath
			log.Printf("[task %s] Auto-detected project: %s (matched %q in prompt)",
				task.ID, filepath.Base(projectPath), candidate)
			return
		}
	}

	// Strategy 2: Brute-force — check every word in the prompt against known projects
	projects := listDiscoveredProjects()
	if len(projects) == 0 {
		return
	}
	projectNames := map[string]string{} // lowercase name → path
	for _, p := range projects {
		name := strings.ToLower(filepath.Base(p.Path))
		projectNames[name] = p.Path
		// Also add without common suffixes/prefixes
		for _, suffix := range []string{"-app", "_app", "-mobile", "_mobile", "-web", "_web"} {
			trimmed := strings.TrimSuffix(name, suffix)
			if trimmed != name && len(trimmed) > 2 {
				projectNames[trimmed] = p.Path
			}
		}
	}

	words := strings.Fields(lower)
	for _, word := range words {
		word = strings.TrimRight(word, ".,!?;:'\"")
		if len(word) < 3 {
			continue
		}
		if path, ok := projectNames[word]; ok {
			task.WorkDir = path
			log.Printf("[task %s] Auto-detected project: %s (word %q found in prompt)",
				task.ID, filepath.Base(path), word)
			return
		}
	}
}

// yaverDevServerContext returns the Yaver dev server proxy instructions
// that are injected into every task prompt. This is hardcoded into the agent
// binary — not dependent on any CLAUDE.md file in any directory.
func yaverDevServerContext(workDir string) string {
	project := DetectProjectInfo(workDir)

	var sb strings.Builder
	sb.WriteString("\n\n[Yaver Agent Context]\n")
	sb.WriteString(fmt.Sprintf("Working directory: %s\n", workDir))

	if project.Name != "" {
		sb.WriteString(fmt.Sprintf("Project: %s", project.Name))
		if project.GitBranch != "" {
			sb.WriteString(fmt.Sprintf(" (branch: %s)", project.GitBranch))
		}
		if project.Framework != "" {
			sb.WriteString(fmt.Sprintf(" [%s]", project.Framework))
		}
		sb.WriteString("\n")
	}

	sb.WriteString(`
IMPORTANT — Dev Server Proxy Rules:
The user is connecting from a mobile phone through the Yaver P2P channel.
When they ask to "start", "load", "run", or "hot reload" an app on their phone:

1. Use the Yaver dev server proxy — run this curl command:
   curl -s -X POST http://localhost:18080/dev/start \
     -H "Content-Type: application/json" \
     -d '{"framework":"<auto>","workDir":"<project-path>"}'
   (No auth header needed — you're running on the same machine as the agent.)

   Optional: if the user wants a secondary phone as the real-device preview
   target, include:
   "targetDeviceId", "targetDeviceName", and "targetDeviceClass":"edge-mobile"
   in the JSON body. If omitted, the current phone remains the default target.

2. The phone automatically detects the dev server and shows a green "Open App" banner.
   For Expo / React Native, the user taps it and Yaver loads the Hermes bundle
   natively inside the app. Browser/WebView preview is only for actual web projects.

3. NEVER output exp:// URLs, QR codes, or tell the user to open Expo Go.
   NEVER tell the user to run terminal commands on their phone.
   Everything flows through the Yaver P2P channel automatically.

4. To trigger hot reload after fixing code:
   curl -s -X POST http://localhost:18080/dev/reload

5. To check dev server status:
   curl -s http://localhost:18080/dev/status

6. To stop the dev server:
   curl -s -X POST http://localhost:18080/dev/stop

7. Before starting the dev server, ensure dependencies are installed:
   cd <project-path> && npm install (if node_modules missing)

8. The dev server proxy supports: Expo, React Native, Flutter, Vite, Next.js.
   It auto-detects the framework from the project files.

9. After calling /dev/start, ALWAYS verify the server is running:
   curl -s http://localhost:18080/dev/status
   Wait for "running":true in the response. If not ready, wait 10s and retry up to 5 times.
   Only tell the user "app is ready" when /dev/status shows running:true.

10. If /dev/start fails or times out, check if another process is using the port:
    lsof -i:8081
    Kill any stale expo/metro processes before retrying.
`)

	return sb.String()
}

// yaverWrapperCapabilityContext teaches terminal/MCP runners that Yaver is not
// just a generic shell: it can drive dev servers, Hermes reload, and web
// previews on the current machine.
func yaverWrapperCapabilityContext(workDir, source string) string {
	workspaceLocation := "this machine"
	switch source {
	case terminalRemoteTaskSource, "connect":
		workspaceLocation = "the attached remote machine"
	}

	return fmt.Sprintf(`

[Yaver wrapper capabilities]
You are running inside Yaver, not a generic terminal. Prefer Yaver-aware app/dev flows when the user asks to run, reload, preview, or inspect an app on %s.
Working directory for these flows: %s

If Yaver MCP tools are available inside the current runner, prefer them.
Otherwise call the local Yaver agent over HTTP on this machine:
  http://localhost:18080

Mobile / Hermes rules:
- For React Native / Expo app serving, use Yaver's dev flow (mobile_project_status, mobile_project_build, ops reload, or /dev/start + /dev/status).
- Never tell the user to open Expo Go, scan a QR code, or use an exp:// URL.
- After Yaver is serving the Hermes bundle successfully, tell the user plainly to open the Yaver app or tap Open App in Yaver.
- For a normal hot reload, use ops reload with mode=dev or POST /dev/reload.
- For a fresh Hermes rebundle + push, use POST /dev/reload-app with the work dir and bundle mode when needed.

Web / WebView preview rules:
- For browser-style preview, use web_preview_start or POST /dev/web-preview/start.
- When the preview starts, surface the returned iframeUrl or webUrl explicitly to the user. Do not just say "server is running" — tell them the webview/browser preview URL.
- Use web_preview_reload or the web-preview reload action when the user asks for a refresh.
- Use web_preview_stop or POST /dev/web-preview/stop to shut the preview down.

Remote visual feedback:
- If the user wants visual confirmation of what is rendering, use vibe_preview_start, vibe_preview_status, vibe_preview_snapshot, or related Yaver preview tools instead of asking them to guess.
`, workspaceLocation, workDir)
}

// noQuestionsPreamble is the standing instruction we splice into every task
// prompt (unless task.AskFreely is set) telling the runner to *act* on
// reasonable defaults instead of stopping mid-task with "should I…?" prose.
//
// Two escape hatches the agent still has:
//
//  1. Call the MCP tool `yaver_ask_user` if a decision genuinely needs a
//     human. The tool blocks the agent's turn until the human answers via
//     mobile/web/CLI, then returns the answer as the tool result. Use only
//     for irreversible or value-judgement decisions.
//
//  2. Look up secrets in the vault before asking. The vault names available
//     to this task are listed inline. The agent should `yaver vault get
//     <name>` (or the env-style `yaver vault env --project <p>`) instead
//     of asking the user to paste a credential.
//
// vaultHints is the rendered vault-names block (may be empty when the vault
// is locked or empty). It's a string rather than a slice so the caller can
// pass "" when vault is unavailable.
func noQuestionsPreamble(vaultHints string) string {
	var sb strings.Builder
	sb.WriteString("\n\n[Yaver — decision policy]\n")
	sb.WriteString("Operate autonomously. Do not stop the run to ask the user clarifying questions in prose. The user is on a mobile / web / CLI surface and may have walked away.\n\n")
	sb.WriteString("When a choice is ambiguous, pick the most reasonable default and proceed:\n")
	sb.WriteString("- Package manager: pnpm > npm > yarn > bun, unless a lockfile pins one.\n")
	sb.WriteString("- Framework / language: detect from the project files. If still ambiguous, prefer the option that minimizes new dependencies.\n")
	sb.WriteString("- API / library version: prefer the version already in the project; if absent, prefer the latest stable release.\n")
	sb.WriteString("- File location: follow the existing convention in the same directory.\n")
	sb.WriteString("- Naming: follow the closest existing identifier in the file or module.\n")
	sb.WriteString("- Greenfield app: use the Yaver stack unless the user explicitly asks otherwise — Yaver Git as the repo home, a Yaver monorepo layout, Yaver Serverless for backend/data, and a self-hosted Yaver mesh box or Yaver Managed Cloud as the development machine.\n")
	sb.WriteString("- Greenfield UI target: if the user asks for mobile, use React Native / Expo with Hermes reload through Yaver; if they ask for web, use the repo's standard web stack and Yaver web preview. If they ask for both, create one monorepo with shared backend/types.\n")
	sb.WriteString("- Cross-surface loop: the user may be driving from phone, web, watch, car, TV, or AR/VR with STT/TTS. Keep responses concise and preview-focused; use Yaver reload/preview tools so they can see the app while continuing by voice.\n")
	sb.WriteString("- Feedback loop: for apps Yaver creates or touches, include/wire the Yaver Feedback SDK when appropriate so shake, screenshot, voice notes, crashes, and black-box context can create follow-up tasks against the same remote repo.\n")
	sb.WriteString("- Remote box policy: do not start app development in a phone/local/browser sandbox. If no usable remote box is available, report that the task needs a self-hosted Yaver box or Yaver Managed Cloud and stop with that concrete next step.\n")
	sb.WriteString("- Git default: initialize/commit locally on the selected remote box and use Yaver Git for the remote unless the user selected GitHub, GitLab, or another provider.\n")
	sb.WriteString("- Reversibility: if the change is reversible (single-file edit, can be reverted with one git command), just do it. State the assumption briefly in the result.\n\n")
	sb.WriteString("Two real escape hatches if a decision *cannot* be defaulted:\n")
	sb.WriteString("1. Call the MCP tool `yaver_ask_user` ({prompt, header?: \"short tag\", kind?: \"text\"|\"choice\"|\"secret\", choices?: [...], multi?: bool, vault_hint?: \"name\"}). Prefer a `choice` with a short `header` and 2-4 options, recommended one first — the surface always adds a free-text 'Other…', so never spell one out. The user answers from their phone or laptop and the tool returns their answer string. This is the LAST resort, not a convenience: use ONLY when the decision is irreversible, a value-judgement, or affects production / billing / customer-visible state, AND you have already checked files / git / vault for the answer. If it returns {cancelled:true}, take the safest default and continue — do not re-ask.\n")
	sb.WriteString("2. If you need a secret (API token, signing key, deploy credential), DO NOT ask the user to paste it. Look in the vault first.")

	if strings.TrimSpace(vaultHints) != "" {
		sb.WriteString("\n\n[Vault — secrets you can read directly, do not ask the user for these]\n")
		sb.WriteString(vaultHints)
		sb.WriteString("\nRead one with `yaver vault get <name> [--project <p>]`. Load all of a project's into env with `eval \"$(yaver vault env --project <p>)\"`. The names above are non-secret; the values stay on disk encrypted until you read them.")
	}

	// gh + glab CLI hint — when present + authenticated, runners
	// should reach for them directly instead of asking for tokens.
	// Empty when neither is usable, so the preamble stays clean on
	// boxes that haven't installed them.
	if hint := gitProviderCLIPreambleHint(); hint != "" {
		sb.WriteString("\n\n[Git providers — use these CLIs directly]\n")
		sb.WriteString(hint)
	}

	sb.WriteString("\n\nDo not write the strings 'Should I', 'Would you like me to', 'Do you want me to', 'Please confirm', or 'Let me know if' anywhere in your output unless you are quoting documentation. Either act, or call yaver_ask_user.\n")
	return sb.String()
}

// schedulingPreamble is the runner-agnostic "future work" contract spliced in
// alongside noQuestionsPreamble. It makes every runner (claude / codex /
// opencode / glm) treat recurring or deferred work the same way: don't loop
// in-process or busy-wait — confirm the cadence with the human once, then hand
// the work to the scheduler via schedule_self. This is what gives non-Claude
// runners the "should I run this periodically / at a time?" behaviour that the
// Claude Code harness has natively; here it lives in Yaver's prompt assembly
// so it is portable across runners.
func schedulingPreamble() string {
	var sb strings.Builder
	sb.WriteString("\n\n[Yaver — recurring / future work]\n")
	sb.WriteString("If the request implies recurring or deferred work — monitoring, a daily/periodic report, \"keep an eye on\", \"remind me\", \"every N\", \"when X happens\", or anything that should run again later — do NOT loop in-process, busy-wait, or sleep. You are a short-lived process; the right tool is the scheduler.\n")
	sb.WriteString("1. If the cadence is unclear, confirm it ONCE via `yaver_ask_user` (a `choice` with options like \"Run once now\", \"Every day\", \"Every hour\", \"At a specific time\"). Mention the rough cost of recurring runs if it's frequent.\n")
	sb.WriteString("2. Then call `schedule_self` to register the continuation: pick one cadence (when / interval_minutes / cron), put everything the next run needs in `prompt`, and carry any state forward in `memo` (the next run is a fresh process with no memory of this turn). It runs on the same runner unless you pass `runner`.\n")
	sb.WriteString("Do the immediate part now if there is one; schedule only the future part. Don't schedule work the user didn't ask to repeat.\n")
	return sb.String()
}

// askModePreamble is spliced into a task prompt when the task is created in
// "ask mode" (yaver ask / yaver_ask MCP / an Ask toggle on the web/mobile
// console). It reframes the run from "do work" to "deeply answer the user's
// question against THIS repo", with three properties the user asked for:
//
//  1. Grounded — read the actual code, never guess from training data. Every
//     non-trivial claim cites a file:line so the answer is checkable.
//  2. Auto-escalating — a cheap shallow scan first; if the question turns out
//     to be broad / architectural / cross-cutting, the agent widens the read
//     and cross-checks its own answer before responding, instead of stopping
//     at the first file it finds.
//  3. Explain-first, may act — the default deliverable is an explanation, not
//     a mutation. The agent MAY offer to do the thing (run the test, write the
//     snippet), but must confirm via yaver_ask_user BEFORE any change to the
//     working tree, deploys, or git. Read-only investigation needs no
//     confirmation.
//
// Ask mode deliberately replaces noQuestionsPreamble: that preamble tells the
// runner "never ask, just act on defaults", which is exactly wrong for a
// question — here we WANT explain-first with a confirmation gate before acting.
func askModePreamble() string {
	var sb strings.Builder
	sb.WriteString("\n\n[Yaver — ask mode]\n")
	sb.WriteString("The user asked a QUESTION. Your job is to ANSWER it deeply and correctly against THIS repository — not to change anything by default.\n\n")
	sb.WriteString("Grounding (required):\n")
	sb.WriteString("- Read the actual code before answering. Grep, open files, follow the wiring. Never answer from memory or generic knowledge when the repo can tell you.\n")
	sb.WriteString("- Cite your evidence as file:line for every concrete claim, so the user can verify it. If a doc/comment disagrees with the code, the code wins — say so.\n\n")
	sb.WriteString("Depth — shallow first, then escalate automatically:\n")
	sb.WriteString("1. Do a quick scan to locate the relevant files and form a first answer.\n")
	sb.WriteString("2. Judge the question's breadth. If it is narrow (one file / one command), answer now.\n")
	sb.WriteString("3. If it is broad, architectural, or cross-cutting (touches multiple subsystems, has subtle edge cases, or your first answer feels thin), ESCALATE: read the adjacent code, trace the full path end to end, and adversarially re-check your own answer for what you missed — THEN respond. Do not stop at the first plausible file.\n\n")
	sb.WriteString("Deliverable:\n")
	sb.WriteString("- Lead with the direct answer in plain language, then the grounded detail (steps, the exact command, the file:line map). Be concrete enough to act on.\n")
	sb.WriteString("- If there is a clear next action (run the test, scaffold the snippet, fix the bug you found), OFFER it — but you MUST get a yes via the yaver_ask_user MCP tool BEFORE you modify the working tree, run a deploy, or touch git. Pure read-only investigation needs no permission. If the user declines or the ask times out, return the explanation alone — that is a complete, successful result.\n")
	return sb.String()
}

// renderVaultHintsForTask returns a multi-line list of vault entry names
// (global + project-scoped) the running task should know it can read. Values
// are NEVER included. Returns "" when the vault is unavailable, empty, or
// the caller passed nil. The project parameter scopes which project's
// entries to surface; "" surfaces only global; "*" surfaces all.
//
// Output shape (one entry per line):
//
//	github-token            (global, category=git-credential)
//	APP_STORE_KEY_ISSUER    (project=yaver, category=signing-key)
func renderVaultHintsForTask(vs *VaultStore, project string) string {
	if vs == nil {
		return ""
	}
	wanted := project
	if wanted == "" {
		wanted = "*"
	}
	entries := vs.List(wanted)
	if len(entries) == 0 {
		return ""
	}
	const maxEntries = 60
	var sb strings.Builder
	for i, e := range entries {
		if i >= maxEntries {
			fmt.Fprintf(&sb, "  …%d more (run `yaver vault list` to see all)\n", len(entries)-maxEntries)
			break
		}
		scope := "global"
		if e.Project != "" {
			scope = "project=" + e.Project
		}
		category := strings.TrimSpace(e.Category)
		if category == "" {
			category = "custom"
		}
		fmt.Fprintf(&sb, "  %s\t(%s, category=%s)\n", e.Name, scope, category)
	}
	return strings.TrimRight(sb.String(), "\n")
}

// autopilotContext returns instructions injected into task prompts when autopilot is on.
func autopilotContext() string {
	return `

[AUTOPILOT MODE — Auto-Driving]
You are in autopilot mode. The user has queued multiple tasks and gone away.
- Complete ALL items without stopping to ask for confirmation.
- Do NOT ask "should I continue?", "shall I proceed?", or similar questions.
- After finishing one item, immediately move to the next.
- If something fails, note the error briefly and move on to the next item.
- When all items are done, state clearly: "All items completed."
- The autopilot supervisor will resume this session with remaining items if you stop early.
  Each follow-up will list what's COMPLETED and what's REMAINING — pick up where you left off.
`
}
