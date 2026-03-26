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
		{ID: "tests", Icon: "\U0001F9EA", Label: "Run Tests", Desc: "Run the test suite and report results", Category: "test",
			Prompt: fmt.Sprintf("Run all tests for %s. Report which pass and which fail. Fix any failures.", projectName), Priority: 1},
		{ID: "bugfix", Icon: "\U0001F41B", Label: "Bug Analysis", Desc: "Scan for common bugs and issues", Category: "bugfix",
			Prompt: fmt.Sprintf("Analyze %s for common bugs: null checks, error handling, race conditions, security issues. List what you find and fix the critical ones.", projectName), Priority: 2},
		{ID: "refactor", Icon: "\u2728", Label: "Clean Up Code", Desc: "Refactor for readability and maintainability", Category: "refactor",
			Prompt: fmt.Sprintf("Do a code quality pass on %s: remove dead code, simplify complex functions, improve naming. Don't change behavior.", projectName), Priority: 3},
		{ID: "docs", Icon: "\U0001F4DD", Label: "Write Docs", Desc: "Generate or update documentation", Category: "docs",
			Prompt: fmt.Sprintf("Update the README.md for %s. Add setup instructions, usage examples, and architecture overview based on the actual code.", projectName), Priority: 3},
		{ID: "custom", Icon: "\U0001F4AC", Label: "Custom Task", Desc: "Tell the agent what to do", Category: "feature",
			Prompt: "", Priority: 3}, // empty prompt = user types their own
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

	// Read package.json for deps
	pkgDeps := ""
	if data, err := os.ReadFile(filepath.Join(projectPath, "package.json")); err == nil {
		pkgDeps = string(data)
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

	// Check for TODO/FIXME comments
	todoCount := countPatternInDir(projectPath, "TODO")
	fixmeCount := countPatternInDir(projectPath, "FIXME")
	_ = countPatternInDir(projectPath, "HACK")

	if todoCount > 0 {
		suggestions = append(suggestions, VibingSuggestion{
			ID: "todos", Icon: "\U0001F4CB", Label: fmt.Sprintf("Fix %d TODOs", todoCount),
			Desc: "Address TODO comments in the codebase", Category: "bugfix", Priority: 1,
			Prompt: fmt.Sprintf("Find all TODO comments in %s, list them, and implement/fix as many as you can. Remove the TODO comment after fixing each one.", projectName),
		})
	}
	if fixmeCount > 0 {
		suggestions = append(suggestions, VibingSuggestion{
			ID: "fixmes", Icon: "\U0001F6A8", Label: fmt.Sprintf("Fix %d FIXMEs", fixmeCount),
			Desc: "Critical fixes flagged in code", Category: "bugfix", Priority: 1,
			Prompt: fmt.Sprintf("Find all FIXME comments in %s and fix them. These are critical issues flagged by the developer.", projectName),
		})
	}

	// Check for missing tests
	hasTests := hasFile(projectPath, "__tests__") || hasFile(projectPath, "test") || hasFile(projectPath, "tests") ||
		hasFile(projectPath, "spec") || hasFile(projectPath, "_test.go")
	if !hasTests {
		suggestions = append(suggestions, VibingSuggestion{
			ID: "add-tests", Icon: "\U0001F9EA", Label: "Add Tests",
			Desc: "No test directory found — add unit tests", Category: "test", Priority: 2,
			Prompt: fmt.Sprintf("Add unit tests for %s. Create a test file for each major module. Use the project's testing framework or set up one if missing.", projectName),
		})
	}

	// Check for missing .gitignore
	if !hasFile(projectPath, ".gitignore") {
		suggestions = append(suggestions, VibingSuggestion{
			ID: "gitignore", Icon: "\U0001F6AB", Label: "Add .gitignore",
			Desc: "No .gitignore found", Category: "docs", Priority: 2,
			Prompt: fmt.Sprintf("Create a .gitignore for %s based on the project's stack.", projectName),
		})
	}

	// Check for outdated deps
	if strings.Contains(pkgDeps, "dependencies") {
		suggestions = append(suggestions, VibingSuggestion{
			ID: "deps", Icon: "\U0001F4E6", Label: "Update Dependencies",
			Desc: "Check for outdated packages", Category: "refactor", Priority: 3,
			Prompt: fmt.Sprintf("Check %s for outdated npm dependencies. List what's outdated and update the safe ones (patch/minor). Don't update major versions without checking for breaking changes.", projectName),
		})
	}

	// Feature suggestion based on README
	if readmeContent != "" && len(readmeContent) > 100 {
		suggestions = append(suggestions, VibingSuggestion{
			ID: "feature", Icon: "\U0001F4A1", Label: "Suggest Features",
			Desc: "AI reads your README and proposes what to build next", Category: "feature", Priority: 2,
			Prompt: fmt.Sprintf("Read the README and codebase of %s. Based on what the project does and what's already built, suggest 3-5 features that would make it better. For each feature, explain what it does and roughly how to implement it. Ask which one I want to build.", projectName),
		})
	}

	// Accessibility check for mobile/web
	info := DetectProjectInfo(projectPath)
	if info.Framework == "expo" || info.Framework == "react-native" || info.Framework == "flutter" ||
		info.Framework == "nextjs" || info.Framework == "vite" || info.Framework == "react" {
		suggestions = append(suggestions, VibingSuggestion{
			ID: "a11y", Icon: "\u267F", Label: "Accessibility Check",
			Desc: "Check for a11y issues in the UI", Category: "bugfix", Priority: 3,
			Prompt: fmt.Sprintf("Scan %s for accessibility issues: missing alt text, missing labels, color contrast, touch target sizes, screen reader support. Fix what you find.", projectName),
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
