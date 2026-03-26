package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// VibingSuggestion is a structured suggestion from the AI for the Vibing widget.
type VibingSuggestion struct {
	ID       string `json:"id"`
	Icon     string `json:"icon"`
	Label    string `json:"label"`
	Desc     string `json:"desc"`
	Category string `json:"category"` // "feature", "bugfix", "test", "deploy", "refactor", "docs"
	Prompt   string `json:"prompt"`   // full prompt to send to the agent
	Priority int    `json:"priority"` // 1=high, 2=medium, 3=low
}

// VibingState holds the state of a vibing session for a project.
type VibingState struct {
	Project     string             `json:"project"`
	Path        string             `json:"path"`
	Framework   string             `json:"framework,omitempty"`
	Suggestions []VibingSuggestion `json:"suggestions"`
	QuickActions []VibingSuggestion `json:"quickActions"` // always-available actions
	History     []string           `json:"history"`       // recent task titles
	GeneratedAt string             `json:"generatedAt"`
}

// generateQuickActions returns always-available actions for any project.
func generateQuickActions(projectPath, projectName, framework string) []VibingSuggestion {
	actions := []VibingSuggestion{
		{ID: "tests", Icon: "\U0001F9EA", Label: "Run Tests", Desc: "Run test suite", Category: "test",
			Prompt: fmt.Sprintf("Run all tests for %s. Report which pass and which fail. Fix any failures.", projectName), Priority: 1},
		{ID: "bugfix", Icon: "\U0001F41B", Label: "Bug Analysis", Desc: "Scan for bugs", Category: "bugfix",
			Prompt: fmt.Sprintf("Analyze %s for common bugs: null checks, error handling, race conditions, security issues. List what you find and fix the critical ones.", projectName), Priority: 2},
		{ID: "refactor", Icon: "\u2728", Label: "Clean Up", Desc: "Refactor code", Category: "refactor",
			Prompt: fmt.Sprintf("Do a code quality pass on %s: remove dead code, simplify complex functions, improve naming. Don't change behavior.", projectName), Priority: 3},
		{ID: "docs", Icon: "\U0001F4DD", Label: "Write Docs", Desc: "Update README", Category: "docs",
			Prompt: fmt.Sprintf("Update the README.md for %s. Add setup instructions, usage examples, and architecture overview based on the actual code.", projectName), Priority: 3},
		{ID: "add-tests", Icon: "\u2705", Label: "Add Tests", Desc: "Create unit tests", Category: "test",
			Prompt: fmt.Sprintf("Add unit tests for %s. Create a test file for each major module. Use the project's testing framework.", projectName), Priority: 3},
		{ID: "deps", Icon: "\U0001F4E6", Label: "Update Deps", Desc: "Check outdated packages", Category: "refactor",
			Prompt: fmt.Sprintf("Check %s for outdated dependencies. Update safe ones (patch/minor). Don't update major versions without checking.", projectName), Priority: 3},
	}

	// Add deploy actions based on framework
	if framework == "expo" || framework == "flutter" || framework == "react-native" {
		actions = append([]VibingSuggestion{
			{ID: "testflight", Icon: "\U0001F34E", Label: "Ship to TestFlight", Desc: "Build + upload to TestFlight", Category: "deploy",
				Prompt: fmt.Sprintf("Build %s for iOS, archive, and upload to TestFlight. Auto-increment build number. Report progress.", projectName), Priority: 1},
			{ID: "playstore", Icon: "\U0001F916", Label: "Ship to Play Store", Desc: "Build AAB + upload to internal testing", Category: "deploy",
				Prompt: fmt.Sprintf("Build %s for Android (release AAB) and upload to Google Play internal testing. Auto-increment versionCode.", projectName), Priority: 1},
		}, actions...)
	}
	if framework == "nextjs" || framework == "vite" {
		actions = append([]VibingSuggestion{
			{ID: "vercel", Icon: "\U0001F680", Label: "Deploy to Vercel", Desc: "Build and deploy to production", Category: "deploy",
				Prompt: fmt.Sprintf("Build %s and deploy to Vercel. Report the deploy URL.", projectName), Priority: 1},
		}, actions...)
	}

	return actions
}

// getRecentGitActivity returns recent commit messages and active files for smart suggestions.
func getRecentGitActivity(projectPath string) (commits []string, activeFiles []string) {
	// Recent commit messages
	if out, err := exec.Command("git", "-C", projectPath, "log", "--oneline", "-15", "--no-merges").Output(); err == nil {
		for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			if line != "" {
				// Strip hash prefix
				parts := strings.SplitN(line, " ", 2)
				if len(parts) == 2 {
					commits = append(commits, parts[1])
				}
			}
		}
	}
	// Most recently changed files
	if out, err := exec.Command("git", "-C", projectPath, "diff", "--name-only", "HEAD~5", "HEAD").Output(); err == nil {
		for _, f := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			if f != "" && !strings.Contains(f, "node_modules") && !strings.Contains(f, ".lock") {
				activeFiles = append(activeFiles, f)
			}
		}
	}
	return
}

// generateAISuggestions creates project-specific suggestions by reading the codebase.
// Reads README, package.json, git log, TODOs to propose smart ideas.
func generateAISuggestions(projectPath, projectName string) []VibingSuggestion {
	var suggestions []VibingSuggestion

	// Read README for context
	readmeContent := ""
	for _, name := range []string{"README.md", "readme.md", "README.txt"} {
		data, err := os.ReadFile(filepath.Join(projectPath, name))
		if err == nil {
			readmeContent = string(data)
			break
		}
	}

	// Git activity — what's been worked on recently
	commits, activeFiles := getRecentGitActivity(projectPath)

	// Smart "What's Next" based on git activity
	if len(commits) > 0 {
		// Build a context string from recent commits
		recentWork := strings.Join(commits, "; ")
		if len(recentWork) > 300 {
			recentWork = recentWork[:300]
		}
		suggestions = append(suggestions, VibingSuggestion{
			ID: "whats-next", Icon: "\U0001F52E", Label: "What's Next?",
			Desc: fmt.Sprintf("Based on recent work: %s", commits[0]),
			Category: "feature", Priority: 1,
			Prompt: fmt.Sprintf("I've been working on %s. Recent commits: %s\n\nBased on this momentum, what should I build next? Suggest 3 concrete features or improvements that naturally follow from what I've been doing. For each one, give a one-liner and ask which one to start.", projectName, recentWork),
		})
	}

	// Active area suggestion
	if len(activeFiles) > 3 {
		area := filepath.Dir(activeFiles[0])
		suggestions = append(suggestions, VibingSuggestion{
			ID: "active-area", Icon: "\U0001F525", Label: fmt.Sprintf("Hot area: %s", area),
			Desc: fmt.Sprintf("%d files changed recently", len(activeFiles)),
			Category: "feature", Priority: 2,
			Prompt: fmt.Sprintf("The most active area in %s is around %s (%d files changed recently). Look at this area, understand what's being built, and suggest improvements or the next logical step.", projectName, area, len(activeFiles)),
		})
	}

	// Feature ideas based on git momentum + README context
	if len(commits) > 2 {
		// Recent work pattern → next logical feature
		recentWork := strings.Join(commits[:min(5, len(commits))], "; ")
		suggestions = append(suggestions, VibingSuggestion{
			ID: "next-feature", Icon: "\U0001F4A1", Label: "Continue momentum",
			Desc: fmt.Sprintf("You've been working on: %s", commits[0]),
			Category: "feature", Priority: 1,
			Prompt: fmt.Sprintf("I've been working on %s. Recent commits: %s\n\nWhat's the most impactful thing to build next? Give me ONE concrete feature that follows from this work. Describe it in 2 sentences and start implementing it.", projectName, recentWork),
		})
	}

	// README-based feature idea
	if readmeContent != "" && len(readmeContent) > 100 {
		truncatedReadme := readmeContent
		if len(truncatedReadme) > 1500 {
			truncatedReadme = truncatedReadme[:1500]
		}
		suggestions = append(suggestions, VibingSuggestion{
			ID: "readme-feature", Icon: "\U0001F52E", Label: "What's missing?",
			Desc: "AI analyzes your project and finds gaps",
			Category: "feature", Priority: 1,
			Prompt: fmt.Sprintf("Read the codebase and README of %s:\n\n%s\n\nWhat's the most obvious missing feature that users would expect? Describe it and implement it.", projectName, truncatedReadme),
		})
	}

	// Active area — where the action is
	if len(activeFiles) > 2 {
		area := filepath.Dir(activeFiles[0])
		suggestions = append(suggestions, VibingSuggestion{
			ID: "hot-area", Icon: "\U0001F525", Label: fmt.Sprintf("Improve %s", area),
			Desc: fmt.Sprintf("%d files changed recently in this area", len(activeFiles)),
			Category: "feature", Priority: 2,
			Prompt: fmt.Sprintf("The hottest area in %s is %s (%d files changed recently: %s). Look at what's being built there and make it better — add error handling, improve the UX, or add a missing feature.", projectName, area, len(activeFiles), strings.Join(activeFiles[:min(5, len(activeFiles))], ", ")),
		})
	}

	return suggestions
}

// countPatternInDir counts occurrences of a pattern in source files.
func countPatternInDir(dir, pattern string) int {
	count := 0
	filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		// Skip non-source files and large files
		ext := filepath.Ext(path)
		sourceExts := map[string]bool{".ts": true, ".tsx": true, ".js": true, ".jsx": true, ".go": true, ".py": true, ".rs": true, ".dart": true, ".swift": true, ".kt": true}
		if !sourceExts[ext] {
			return nil
		}
		if info.Size() > 100000 { // skip files > 100KB
			return nil
		}
		// Skip node_modules, .git, etc
		if strings.Contains(path, "node_modules") || strings.Contains(path, ".git/") || strings.Contains(path, "vendor/") {
			return filepath.SkipDir
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		count += strings.Count(string(data), pattern)
		return nil
	})
	return count
}

// handleVibing returns vibing state for a project — suggestions, quick actions, history.
func (s *HTTPServer) handleVibing(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, http.StatusMethodNotAllowed, "use GET")
		return
	}

	query := r.URL.Query().Get("query")
	path := r.URL.Query().Get("path")

	if path == "" && query != "" {
		found, err := findProject(query)
		if err != nil {
			jsonError(w, http.StatusNotFound, "project not found: "+err.Error())
			return
		}
		path = found
	}
	if path == "" {
		path = s.taskMgr.workDir
	}

	projectName := filepath.Base(path)
	info := DetectProjectInfo(path)

	// Generate suggestions
	quickActions := generateQuickActions(path, projectName, info.Framework)
	suggestions := generateAISuggestions(path, projectName)

	// Get recent task history for this project
	var history []string
	tasks := s.taskMgr.ListTasks()
	for _, t := range tasks {
		if len(history) >= 10 {
			break
		}
		history = append(history, t.Title)
	}

	state := VibingState{
		Project:      projectName,
		Path:         path,
		Framework:    info.Framework,
		Suggestions:  suggestions,
		QuickActions: quickActions,
		History:      history,
		GeneratedAt:  time.Now().UTC().Format(time.RFC3339),
	}

	log.Printf("[vibing] Generated %d suggestions + %d quick actions for %s", len(suggestions), len(quickActions), projectName)
	jsonReply(w, http.StatusOK, state)
}

// handleVibingSurprise asks the AI to analyze the project and suggest features.
// Returns structured suggestions as JSON (not a task — stays in vibing mode).
func (s *HTTPServer) handleVibingSurprise(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}

	var req struct {
		ProjectPath string `json:"projectPath"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	projectName := filepath.Base(req.ProjectPath)
	commits, _ := getRecentGitActivity(req.ProjectPath)
	info := DetectProjectInfo(req.ProjectPath)

	// Read README
	readme := ""
	for _, name := range []string{"README.md", "readme.md"} {
		if data, err := os.ReadFile(filepath.Join(req.ProjectPath, name)); err == nil {
			readme = string(data)
			if len(readme) > 2000 {
				readme = readme[:2000]
			}
			break
		}
	}

	recentWork := ""
	if len(commits) > 0 {
		recentWork = strings.Join(commits, "\n")
	}

	// Build prompt for the AI
	prompt := fmt.Sprintf(`Analyze this project and suggest 5 concrete features to build next.

Project: %s
Framework: %s
Git branch: %s

Recent commits:
%s

README excerpt:
%s

Return ONLY a JSON array, no markdown, no explanation. Each item must have these exact fields:
[{"icon":"emoji","label":"short title","desc":"one line description","category":"feature|bugfix|refactor|test","prompt":"detailed instruction for implementing this"}]`, projectName, info.Framework, info.GitBranch, recentWork, readme)

	// Run as a task but capture the output
	task, err := s.taskMgr.CreateTask(prompt, "", "", "vibing-surprise", "", "", nil)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}

	jsonReply(w, http.StatusOK, map[string]interface{}{
		"ok":     true,
		"taskId": task.ID,
		"message": "AI is analyzing your project. Check task output for suggestions.",
	})
}

// handleVibingExecute runs a vibing suggestion as a task.
func (s *HTTPServer) handleVibingExecute(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}

	var req struct {
		Prompt    string `json:"prompt"`
		ProjectPath string `json:"projectPath"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	if req.ProjectPath != "" {
		s.taskMgr.mu.Lock()
		s.taskMgr.workDir = req.ProjectPath
		s.taskMgr.mu.Unlock()
	}

	task, err := s.taskMgr.CreateTask(req.Prompt, "", "", "vibing", "", "", nil)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}

	jsonReply(w, http.StatusOK, map[string]interface{}{
		"ok":     true,
		"taskId": task.ID,
	})
}
