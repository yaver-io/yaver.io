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

2. The phone automatically detects the dev server and shows a green "Open App" banner.
   The user taps it and the app loads in a WebView through the P2P channel.

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
