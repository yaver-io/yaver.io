package main

import (
	"fmt"
	"log"
	"path/filepath"
	"strings"
)

// autoSwitchProject detects project references in a task prompt and switches
// the workDir to that project. This enables "start AcmeStore" from Yaver mobile
// when the agent is serving from ~.
func (tm *TaskManager) autoSwitchProject(task *Task, prompt string) {
	lower := strings.ToLower(prompt)

	// Extract potential project names from common patterns
	// "start AcmeStore", "load acmestore", "open my-app", "hot reload acmestore"
	patterns := []string{
		"start ", "load ", "open ", "hot reload ", "reload ",
		"run ", "test ", "deploy ", "build ",
		"switch to ", "go to ", "work on ",
	}

	for _, p := range patterns {
		idx := strings.Index(lower, p)
		if idx < 0 {
			continue
		}
		after := strings.TrimSpace(prompt[idx+len(p):])
		// Take the first word/phrase as the project name
		words := strings.Fields(after)
		if len(words) == 0 {
			continue
		}

		// Try first word, then first two words
		candidates := []string{words[0]}
		if len(words) > 1 {
			candidates = append(candidates, words[0]+" "+words[1])
		}

		for _, candidate := range candidates {
			// Clean up: remove trailing punctuation, "on my phone", "app"
			candidate = strings.TrimRight(candidate, ".,!?;:")
			candidate = strings.TrimSuffix(candidate, " on")
			candidate = strings.TrimSuffix(candidate, " app")

			if len(candidate) < 2 {
				continue
			}

			// Skip common words that aren't project names
			skip := map[string]bool{
				"the": true, "my": true, "this": true, "a": true, "an": true,
				"it": true, "all": true, "dev": true, "server": true,
				"now": true, "here": true, "phone": true, "app": true,
			}
			if skip[strings.ToLower(candidate)] {
				continue
			}

			// Try to find the project
			projectPath, err := findProject(strings.ToLower(candidate))
			if err != nil {
				continue
			}

			// Found a match — switch workDir
			tm.mu.Lock()
			oldDir := tm.workDir
			tm.workDir = projectPath
			tm.mu.Unlock()

			log.Printf("[task %s] Auto-switched project: %s → %s (matched %q)",
				task.ID, filepath.Base(oldDir), filepath.Base(projectPath), candidate)
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
