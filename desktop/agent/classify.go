package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// MessageIntent represents the classified intent of a user message.
type MessageIntent string

const (
	// IntentTodo means the message describes a bug or issue to record for later.
	IntentTodo MessageIntent = "todo"
	// IntentAction means the message is an immediate command/request to execute.
	IntentAction MessageIntent = "action"
	// IntentContinuation means the message continues/clarifies a previous todo item.
	IntentContinuation MessageIntent = "continuation"
)

// ClassifyResult holds the classification of a user message.
type ClassifyResult struct {
	Intent       MessageIntent `json:"intent"`
	TodoID       string        `json:"todoId,omitempty"`       // for continuation: which item it continues
	Description  string        `json:"description"`            // cleaned description
	IsImmediate  bool          `json:"isImmediate"`            // true if should be executed now
	Confidence   float64       `json:"confidence"`             // 0.0-1.0
}

// ClassifyMessage determines if a user's chat message is a todo item (bug/issue to record),
// a continuation of an existing item, or an immediate action to execute.
// Uses keyword heuristics — no LLM call needed, keeps it fast and P2P.
func ClassifyMessage(message string, existingItems []*TodoItem) ClassifyResult {
	msg := strings.ToLower(strings.TrimSpace(message))

	// Check for explicit queue/todo signals
	if hasTodoSignal(msg) {
		return ClassifyResult{
			Intent:      IntentTodo,
			Description: message,
			IsImmediate: false,
			Confidence:  0.9,
		}
	}

	// Check for explicit action signals
	if hasActionSignal(msg) {
		return ClassifyResult{
			Intent:      IntentAction,
			Description: message,
			IsImmediate: true,
			Confidence:  0.9,
		}
	}

	// Check for continuation signals (references to existing items)
	if todoID := findContinuation(msg, existingItems); todoID != "" {
		return ClassifyResult{
			Intent:      IntentContinuation,
			TodoID:      todoID,
			Description: message,
			IsImmediate: false,
			Confidence:  0.8,
		}
	}

	// Bug/issue patterns → todo
	if hasBugPattern(msg) {
		return ClassifyResult{
			Intent:      IntentTodo,
			Description: message,
			IsImmediate: false,
			Confidence:  0.7,
		}
	}

	// Default: treat as immediate action (solo founder wants things done)
	return ClassifyResult{
		Intent:      IntentAction,
		Description: message,
		IsImmediate: true,
		Confidence:  0.5,
	}
}

// hasTodoSignal checks for explicit "add to list" signals.
func hasTodoSignal(msg string) bool {
	signals := []string{
		"add to queue", "queue this", "add to todo", "add to list",
		"record this", "note this", "save this", "remember this",
		"not now", "later", "do this later", "fix later",
		"add it", "put it in", "log this",
	}
	for _, s := range signals {
		if strings.Contains(msg, s) {
			return true
		}
	}
	return false
}

// hasActionSignal checks for explicit "do it now" signals.
func hasActionSignal(msg string) bool {
	signals := []string{
		"fix this now", "do it now", "implement", "implement all",
		"run", "execute", "deploy", "build", "hot reload", "reload",
		"start", "stop", "restart", "push", "commit",
		"show me", "explain", "what is", "how do",
	}
	for _, s := range signals {
		if strings.Contains(msg, s) {
			return true
		}
	}
	return false
}

// hasBugPattern checks for common bug/issue description patterns.
func hasBugPattern(msg string) bool {
	patterns := []string{
		"is broken", "doesn't work", "not working", "is not working",
		"bug", "crash", "error", "issue", "problem",
		"wrong", "incorrect", "missing", "can't", "cannot",
		"fails", "failing", "broken", "stuck",
		"should be", "supposed to", "expected",
		"this page", "this screen", "this button", "the button",
		"overlaps", "misaligned", "cut off", "truncated",
	}
	for _, p := range patterns {
		if strings.Contains(msg, p) {
			return true
		}
	}
	return false
}

// findContinuation checks if the message references an existing todo item.
func findContinuation(msg string, items []*TodoItem) string {
	// Check for "also" or "and" at the start (continuation of last reported bug)
	if (strings.HasPrefix(msg, "also ") || strings.HasPrefix(msg, "and ") ||
		strings.HasPrefix(msg, "another thing") || strings.HasPrefix(msg, "same thing")) &&
		len(items) > 0 {
		// Find the most recent pending item
		var latest *TodoItem
		for _, item := range items {
			if item.Status == TodoStatusPending || item.Status == TodoStatusImplementing {
				if latest == nil || item.CreatedAt > latest.CreatedAt {
					latest = item
				}
			}
		}
		if latest != nil {
			return latest.ID
		}
	}
	return ""
}

// ProjectInfo contains metadata about the current project/workspace.
type ProjectInfo struct {
	Name      string `json:"name"`                // directory name
	Path      string `json:"path"`                // full path
	GitBranch string `json:"gitBranch,omitempty"`  // current git branch
	GitRemote string `json:"gitRemote,omitempty"`  // origin URL (sanitized)
	Framework string `json:"framework,omitempty"`  // detected framework
}

// DetectProjectInfo extracts project metadata from a working directory.
func DetectProjectInfo(workDir string) ProjectInfo {
	info := ProjectInfo{
		Name: filepath.Base(workDir),
		Path: workDir,
	}

	// Git branch
	if out, err := exec.Command("git", "-C", workDir, "rev-parse", "--abbrev-ref", "HEAD").Output(); err == nil {
		info.GitBranch = strings.TrimSpace(string(out))
	}

	// Git remote (sanitize — no tokens/credentials)
	if out, err := exec.Command("git", "-C", workDir, "remote", "get-url", "origin").Output(); err == nil {
		remote := strings.TrimSpace(string(out))
		// Strip credentials from URL
		if idx := strings.Index(remote, "@"); idx > 0 {
			if strings.HasPrefix(remote, "https://") || strings.HasPrefix(remote, "http://") {
				// https://token@github.com/... → https://github.com/...
				proto := remote[:strings.Index(remote, "://")+3]
				remote = proto + remote[idx+1:]
			}
		}
		info.GitRemote = remote
	}

	// Framework detection
	info.Framework = detectFramework(workDir)

	return info
}

// detectFramework checks common framework indicators.
func detectFramework(dir string) string {
	checks := []struct {
		file      string
		framework string
	}{
		{"pubspec.yaml", "flutter"},
		{"next.config.ts", "nextjs"},
		{"next.config.js", "nextjs"},
		{"vite.config.ts", "vite"},
		{"vite.config.js", "vite"},
		{"package.json", ""}, // check for expo below
	}

	for _, c := range checks {
		if fileExists(filepath.Join(dir, c.file)) {
			if c.framework != "" {
				return c.framework
			}
			// Check package.json for expo
			if data, err := readSmallFile(filepath.Join(dir, c.file)); err == nil {
				if strings.Contains(string(data), `"expo"`) {
					return "expo"
				}
				if strings.Contains(string(data), `"react-native"`) {
					return "react-native"
				}
				if strings.Contains(string(data), `"react"`) {
					return "react"
				}
			}
		}
	}
	return ""
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func readSmallFile(path string) ([]byte, error) {
	return os.ReadFile(path)
}
