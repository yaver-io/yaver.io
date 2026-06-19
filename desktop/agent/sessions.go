package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	sessionsDirName  = "sessions"
	sessionRetention = 7 * 24 * time.Hour // keep last 7 days
	maxSessionCtxLen = 8000               // max chars to inject into prompt
)

// sessionsDir returns ~/.yaver/sessions/, creating it if necessary.
func sessionsDir() (string, error) {
	base, err := yaverDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(base, sessionsDirName)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	return dir, nil
}

// saveSessionFile writes a completed task as a session file under ~/.yaver/sessions/.
// Filename: YYYY-MM-DD_HHMMSS_{taskId}.md
// Called after a task finishes (completed or failed).
func saveSessionFile(task *Task, runnerName, workDir string) {
	dir, err := sessionsDir()
	if err != nil {
		log.Printf("[sessions] Cannot create sessions dir: %v", err)
		return
	}

	ts := time.Now()
	if task.FinishedAt != nil {
		ts = *task.FinishedAt
	}

	filename := fmt.Sprintf("%s_%s.md", ts.Format("2006-01-02_150405"), task.ID)
	path := filepath.Join(dir, filename)

	var b strings.Builder
	b.WriteString(fmt.Sprintf("# Session %s\n\n", task.ID))
	b.WriteString(fmt.Sprintf("- **Date**: %s\n", ts.Format("2006-01-02 15:04")))
	b.WriteString(fmt.Sprintf("- **Status**: %s\n", task.Status))
	b.WriteString(fmt.Sprintf("- **Runner**: %s\n", runnerName))
	b.WriteString(fmt.Sprintf("- **Directory**: %s\n", workDir))
	if task.CostUSD > 0 {
		b.WriteString(fmt.Sprintf("- **Cost**: $%.4f\n", task.CostUSD))
	}
	b.WriteString("\n")

	// Write conversation turns
	for _, turn := range task.Turns {
		if turn.Role == "user" {
			b.WriteString(fmt.Sprintf("## User\n\n%s\n\n", turn.Content))
		} else {
			// Truncate long assistant responses to keep files reasonable
			content := turn.Content
			if len(content) > 3000 {
				content = content[:3000] + "\n\n...(truncated)"
			}
			b.WriteString(fmt.Sprintf("## Assistant\n\n%s\n\n", content))
		}
	}

	// Always persist the final runner result when it was not already
	// appended as the latest assistant turn. Raw-mode runners can finish
	// with pre-seeded turns, and hiding ResultText there leaves the saved
	// session looking like the prompt only.
	lastAssistant := ""
	for i := len(task.Turns) - 1; i >= 0; i-- {
		if task.Turns[i].Role == "assistant" {
			lastAssistant = task.Turns[i].Content
			break
		}
	}
	if task.ResultText != "" && strings.TrimSpace(lastAssistant) != strings.TrimSpace(task.ResultText) {
		content := task.ResultText
		if len(content) > 3000 {
			content = content[:3000] + "\n\n...(truncated)"
		}
		b.WriteString(fmt.Sprintf("## Result\n\n%s\n\n", content))
	}

	if err := os.WriteFile(path, []byte(b.String()), 0600); err != nil {
		log.Printf("[sessions] Failed to write session file: %v", err)
		return
	}

	log.Printf("[sessions] Saved session %s to %s", task.ID, filename)
}

// cleanOldSessions removes session files older than sessionRetention.
func cleanOldSessions() {
	dir, err := sessionsDir()
	if err != nil {
		return
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}

	cutoff := time.Now().Add(-sessionRetention)
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if info.ModTime().Before(cutoff) {
			os.Remove(filepath.Join(dir, e.Name()))
			log.Printf("[sessions] Cleaned old session: %s", e.Name())
		}
	}
}

// getRecentSessionsContext reads recent session files and returns a summary
// suitable for injecting into prompts, so the AI agent has context about
// what the user has been working on recently.
func getRecentSessionsContext() string {
	dir, err := sessionsDir()
	if err != nil {
		return ""
	}

	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) == 0 {
		return ""
	}

	// Filter to .md files and sort by name (which is date-prefixed, so newest last)
	var files []os.DirEntry
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".md") {
			files = append(files, e)
		}
	}
	if len(files) == 0 {
		return ""
	}

	sort.Slice(files, func(i, j int) bool {
		return files[i].Name() < files[j].Name()
	})

	// Read files newest-first, up to maxSessionCtxLen
	var parts []string
	totalLen := 0
	for i := len(files) - 1; i >= 0; i-- {
		path := filepath.Join(dir, files[i].Name())
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		content := string(data)
		if totalLen+len(content) > maxSessionCtxLen {
			// Include a truncated version if we have room
			remaining := maxSessionCtxLen - totalLen
			if remaining > 200 {
				// Just include the header/metadata lines
				lines := strings.SplitN(content, "\n## ", 2)
				if len(lines[0]) < remaining {
					parts = append(parts, lines[0]+"\n\n(details omitted for brevity)")
				}
			}
			break
		}
		parts = append(parts, content)
		totalLen += len(content)
	}

	if len(parts) == 0 {
		return ""
	}

	// Reverse so they're in chronological order
	for i, j := 0, len(parts)-1; i < j; i, j = i+1, j-1 {
		parts[i], parts[j] = parts[j], parts[i]
	}

	return "# Recent Sessions (last 7 days)\n\nThese are the user's recent AI coding sessions on this machine. Use this context to understand ongoing work.\n\n---\n\n" + strings.Join(parts, "\n---\n\n")
}
