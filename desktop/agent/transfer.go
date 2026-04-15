package main

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// TransferBundle is the portable session format transferred between machines.
type TransferBundle struct {
	Version      int               `json:"version"`
	ExportedAt   string            `json:"exportedAt"`
	SourceDevice string            `json:"sourceDevice"`
	SourceOS     string            `json:"sourceOS"`
	AgentType    string            `json:"agentType"`    // "claude", "codex", "aider", "ollama", "goose", "amp", "opencode", "custom"
	AgentVersion string            `json:"agentVersion,omitempty"`
	SessionID    string            `json:"sessionId,omitempty"`

	// Task state
	Task TransferTask `json:"task"`

	// Agent-specific session files (path -> base64 content)
	AgentFiles map[string]string `json:"agentFiles,omitempty"`

	// Workspace info
	Workspace *TransferWorkspace `json:"workspace,omitempty"`
}

type TransferTask struct {
	Title       string            `json:"title"`
	Description string            `json:"description,omitempty"`
	Turns       []ConversationTurn `json:"turns,omitempty"`
	ResultText  string            `json:"resultText,omitempty"`
	CostUSD     float64           `json:"costUsd,omitempty"`
	RunnerID    string            `json:"runnerId"`
	Model       string            `json:"model,omitempty"`
	WorkDir     string            `json:"workDir"`
}

type TransferWorkspace struct {
	GitRemote   string            `json:"gitRemote,omitempty"`
	GitBranch   string            `json:"gitBranch,omitempty"`
	GitCommit   string            `json:"gitCommit,omitempty"`
	GitPatch    string            `json:"gitPatch,omitempty"`    // base64-encoded git diff
	ConfigFiles map[string]string `json:"configFiles,omitempty"` // filename -> content
	TarGz       string            `json:"tarGz,omitempty"`       // base64-encoded tar.gz (small workspaces only)
}

type ExportOptions struct {
	IncludeWorkspace bool   `json:"includeWorkspace"`
	WorkspaceMode    string `json:"workspaceMode"` // "none", "git", "tar"
}

type ImportOptions struct {
	WorkDir       string `json:"workDir,omitempty"`       // override work directory
	ResumeOnImport bool  `json:"resumeOnImport"`
	GitClone      bool   `json:"gitClone"`                // clone from git remote if available

	// HandoffEngine, when non-empty, makes ImportSession's caller hand the
	// imported task to a fresh autodev loop. Values: "claude" (default,
	// claude-code end-to-end), "hybrid" (planner+local implementer),
	// "runner" (single arbitrary runner via HandoffRunner).
	HandoffEngine  string `json:"handoffEngine,omitempty"`
	HandoffRunner  string `json:"handoffRunner,omitempty"`  // when HandoffEngine="runner"
	HandoffMaxKicks int   `json:"handoffMaxKicks,omitempty"`
	HandoffDeadlineSec int `json:"handoffDeadlineSec,omitempty"`
}

// TransferableSession describes a session that can be exported.
type TransferableSession struct {
	TaskID        string `json:"taskId"`
	AgentType     string `json:"agentType"`
	SessionID     string `json:"sessionId,omitempty"`
	Title         string `json:"title"`
	WorkDir       string `json:"workDir"`
	Status        string `json:"status"`
	Turns         int    `json:"turns"`
	LastActive    string `json:"lastActive"`
	BundleSize    int64  `json:"estimatedBundleSize"`
	GitRemote     string `json:"gitRemote,omitempty"`
	GitBranch     string `json:"gitBranch,omitempty"`
	Resumable     bool   `json:"resumable"`
}

// ListTransferableSessions returns sessions that can be exported.
func ListTransferableSessions(tm *TaskManager) []TransferableSession {
	tasks := tm.ListTasks()
	var sessions []TransferableSession

	for _, t := range tasks {
		if t.Status != "completed" && t.Status != "failed" && t.Status != "stopped" && t.Status != "running" {
			continue
		}
		if len(t.Turns) == 0 && t.ResultText == "" {
			continue
		}

		ts := TransferableSession{
			TaskID:    t.ID,
			AgentType: t.RunnerID,
			SessionID: t.SessionID,
			Title:     t.Title,
			WorkDir:   tm.workDir,
			Status:    string(t.Status),
			Turns:     len(t.Turns),
			Resumable: t.SessionID != "" && (t.RunnerID == "claude" || t.RunnerID == "aider"),
		}

		if t.FinishedAt != nil {
			ts.LastActive = t.FinishedAt.Format(time.RFC3339)
		} else if t.StartedAt != nil {
			ts.LastActive = t.StartedAt.Format(time.RFC3339)
		}

		// Estimate bundle size
		ts.BundleSize = int64(len(t.ResultText))
		for _, turn := range t.Turns {
			ts.BundleSize += int64(len(turn.Content))
		}

		// Git info
		if remote, branch, _ := getGitInfo(tm.workDir); remote != "" {
			ts.GitRemote = remote
			ts.GitBranch = branch
		}

		sessions = append(sessions, ts)
	}

	// Also scan for native Claude Code sessions not tracked by Yaver
	seenSessionIDs := make(map[string]bool)
	for _, s := range sessions {
		if s.SessionID != "" {
			seenSessionIDs[s.SessionID] = true
		}
	}

	claudeSessions := listClaudeSessions(tm.workDir)
	for _, cs := range claudeSessions {
		if seenSessionIDs[cs.SessionID] {
			continue // already listed via Yaver task
		}
		title := cs.Summary
		if title == "" {
			title = "Claude Code session"
		}
		sessions = append(sessions, TransferableSession{
			TaskID:     cs.SessionID, // use session ID as task ID for native sessions
			AgentType:  "claude",
			SessionID:  cs.SessionID,
			Title:      title,
			WorkDir:    tm.workDir,
			Status:     "completed",
			BundleSize: cs.FileSize,
			LastActive: cs.ModTime.Format(time.RFC3339),
			Resumable:  true,
		})
	}

	return sessions
}

// ExportSession packages a task into a TransferBundle.
// taskID can be a Yaver task ID or a native Claude Code session UUID.
func ExportSession(tm *TaskManager, taskID string, opts ExportOptions) (*TransferBundle, error) {
	hostname, _ := os.Hostname()

	task, ok := tm.GetTask(taskID)
	if !ok {
		// Try as a native Claude Code session UUID
		claudeSessions := listClaudeSessions(tm.workDir)
		for _, cs := range claudeSessions {
			if cs.SessionID == taskID {
				// Build bundle directly from Claude session file
				bundle := &TransferBundle{
					Version:      1,
					ExportedAt:   time.Now().UTC().Format(time.RFC3339),
					SourceDevice: hostname,
					SourceOS:     runtime.GOOS + "/" + runtime.GOARCH,
					AgentType:    "claude",
					SessionID:    cs.SessionID,
					Task: TransferTask{
						Title:    cs.Summary,
						RunnerID: "claude",
						WorkDir:  tm.workDir,
					},
					AgentFiles: make(map[string]string),
				}
				if bundle.Task.Title == "" {
					bundle.Task.Title = "Claude Code session " + cs.SessionID[:8]
				}
				// Read the session file
				if data, err := os.ReadFile(cs.FilePath); err == nil {
					bundle.AgentFiles["claude/session.jsonl"] = base64.StdEncoding.EncodeToString(data)
				}
				// Also collect subagents directory if present
				subagentsDir := filepath.Join(filepath.Dir(cs.FilePath), cs.SessionID, "subagents")
				if entries, err := os.ReadDir(subagentsDir); err == nil {
					for _, e := range entries {
						if !e.IsDir() && strings.HasSuffix(e.Name(), ".jsonl") {
							data, _ := os.ReadFile(filepath.Join(subagentsDir, e.Name()))
							if data != nil {
								bundle.AgentFiles["claude/subagents/"+e.Name()] = base64.StdEncoding.EncodeToString(data)
							}
						}
					}
				}
				// Get agent version
				if out, err := exec.Command("claude", "--version").CombinedOutput(); err == nil {
					bundle.AgentVersion = strings.TrimSpace(strings.Split(string(out), "\n")[0])
				}
				// Workspace
				collectWorkspaceForBundle(bundle, tm.workDir, opts)
				return bundle, nil
			}
		}
		return nil, fmt.Errorf("task or session not found: %s", taskID)
	}

	bundle := &TransferBundle{
		Version:      1,
		ExportedAt:   time.Now().UTC().Format(time.RFC3339),
		SourceDevice: hostname,
		SourceOS:     runtime.GOOS + "/" + runtime.GOARCH,
		AgentType:    task.RunnerID,
		SessionID:    task.SessionID,
		Task: TransferTask{
			Title:       task.Title,
			Description: task.Description,
			ResultText:  task.ResultText,
			CostUSD:     task.CostUSD,
			RunnerID:    task.RunnerID,
			WorkDir:     tm.workDir,
		},
	}

	// Copy turns
	tm.mu.RLock()
	bundle.Task.Turns = make([]ConversationTurn, len(task.Turns))
	copy(bundle.Task.Turns, task.Turns)
	tm.mu.RUnlock()

	// Get agent version
	if runner, ok := builtinRunners[task.RunnerID]; ok {
		if out, err := exec.Command(runner.Command, "--version").CombinedOutput(); err == nil {
			bundle.AgentVersion = strings.TrimSpace(strings.Split(string(out), "\n")[0])
		}
	}

	// Collect agent-specific session files
	bundle.AgentFiles = collectAgentFiles(task.RunnerID, task.SessionID, tm.workDir)

	// Workspace
	collectWorkspaceForBundle(bundle, tm.workDir, opts)

	return bundle, nil
}

// collectWorkspaceForBundle adds workspace info to a transfer bundle.
func collectWorkspaceForBundle(bundle *TransferBundle, workDir string, opts ExportOptions) {
	if !opts.IncludeWorkspace && opts.WorkspaceMode != "git" && opts.WorkspaceMode != "tar" {
		return
	}
	ws := &TransferWorkspace{
		ConfigFiles: collectConfigFiles(workDir),
	}

	remote, branch, commit := getGitInfo(workDir)
	if remote != "" {
		ws.GitRemote = remote
		ws.GitBranch = branch
		ws.GitCommit = commit
	}

	mode := opts.WorkspaceMode
	if mode == "" && remote != "" {
		mode = "git"
	} else if mode == "" {
		mode = "none"
	}

	if mode == "git" && remote != "" {
		patch, _ := exec.Command("git", "-C", workDir, "diff", "HEAD").CombinedOutput()
		if len(patch) > 0 {
			ws.GitPatch = base64.StdEncoding.EncodeToString(patch)
		}
	} else if mode == "tar" {
		tarData, err := tarWorkspace(workDir)
		if err != nil {
			log.Printf("[transfer] Warning: tar workspace failed: %v", err)
		} else if len(tarData) <= 50*1024*1024 {
			ws.TarGz = base64.StdEncoding.EncodeToString(tarData)
		}
	}

	bundle.Workspace = ws
}

// ImportSession unpacks a TransferBundle and creates a task.
func ImportSession(tm *TaskManager, bundle *TransferBundle, opts ImportOptions) (taskID string, warnings []string, err error) {
	if bundle.Version > 1 {
		return "", nil, fmt.Errorf("unsupported bundle version %d (upgrade your agent)", bundle.Version)
	}

	workDir := opts.WorkDir
	if workDir == "" {
		workDir = tm.workDir
	}

	// Workspace setup
	if bundle.Workspace != nil {
		if opts.GitClone && bundle.Workspace.GitRemote != "" {
			// Clone the repo
			if _, err := os.Stat(filepath.Join(workDir, ".git")); os.IsNotExist(err) {
				log.Printf("[transfer] Cloning %s into %s", bundle.Workspace.GitRemote, workDir)
				out, err := exec.Command("git", "clone", bundle.Workspace.GitRemote, workDir).CombinedOutput()
				if err != nil {
					warnings = append(warnings, fmt.Sprintf("git clone failed: %s", strings.TrimSpace(string(out))))
				}
			}
			// Checkout branch
			if bundle.Workspace.GitBranch != "" {
				exec.Command("git", "-C", workDir, "checkout", bundle.Workspace.GitBranch).Run()
			}
			// Apply patch
			if bundle.Workspace.GitPatch != "" {
				patch, _ := base64.StdEncoding.DecodeString(bundle.Workspace.GitPatch)
				if len(patch) > 0 {
					cmd := exec.Command("git", "-C", workDir, "apply", "--allow-empty")
					cmd.Stdin = bytes.NewReader(patch)
					if out, err := cmd.CombinedOutput(); err != nil {
						warnings = append(warnings, fmt.Sprintf("git patch apply failed: %s", strings.TrimSpace(string(out))))
					}
				}
			}
		}

		// Extract tar if present
		if bundle.Workspace.TarGz != "" {
			tarData, _ := base64.StdEncoding.DecodeString(bundle.Workspace.TarGz)
			if len(tarData) > 0 {
				if err := extractTarGz(tarData, workDir); err != nil {
					warnings = append(warnings, fmt.Sprintf("tar extraction failed: %v", err))
				}
			}
		}

		// Write config files
		if bundle.Workspace.ConfigFiles != nil {
			for name, content := range bundle.Workspace.ConfigFiles {
				// Sanitize path
				if strings.Contains(name, "..") {
					continue
				}
				fpath := filepath.Join(workDir, name)
				os.MkdirAll(filepath.Dir(fpath), 0755)
				os.WriteFile(fpath, []byte(content), 0644)
			}
		}
	}

	// Write agent-specific session files
	if len(bundle.AgentFiles) > 0 {
		writeAgentFiles(bundle.AgentType, bundle.SessionID, bundle.AgentFiles, workDir, &warnings)
	}

	// Check target has the runner installed
	if runner, ok := builtinRunners[bundle.AgentType]; ok {
		if _, err := exec.LookPath(runner.Command); err != nil {
			warnings = append(warnings, fmt.Sprintf("%s (%s) is not installed on this machine", runner.Name, runner.Command))
		} else {
			// Check version compatibility
			if bundle.AgentVersion != "" {
				if out, err := exec.Command(runner.Command, "--version").CombinedOutput(); err == nil {
					localVer := strings.TrimSpace(strings.Split(string(out), "\n")[0])
					if localVer != bundle.AgentVersion {
						warnings = append(warnings, fmt.Sprintf("Agent version mismatch: source=%s, local=%s", bundle.AgentVersion, localVer))
					}
				}
			}
		}
	}

	// Create the task
	task := &Task{
		ID:          generateTaskID(),
		Title:       "[Transferred] " + bundle.Task.Title,
		Description: fmt.Sprintf("Transferred from %s (%s) at %s", bundle.SourceDevice, bundle.SourceOS, bundle.ExportedAt),
		Status:      TaskStatusFinished,
		RunnerID:    bundle.Task.RunnerID,
		SessionID:   bundle.SessionID,
		Turns:       bundle.Task.Turns,
		ResultText:  bundle.Task.ResultText,
		CostUSD:     bundle.Task.CostUSD,
		Source:       "transfer",
		CreatedAt:   time.Now(),
	}

	tm.mu.Lock()
	tm.tasks[task.ID] = task
	tm.persist()
	tm.mu.Unlock()

	log.Printf("[transfer] Imported session %s from %s (agent=%s, turns=%d, sessionId=%s)",
		task.ID, bundle.SourceDevice, bundle.AgentType, len(bundle.Task.Turns), bundle.SessionID)

	return task.ID, warnings, nil
}

// collectAgentFiles collects agent-specific session files for export.
func collectAgentFiles(agentType, sessionID, workDir string) map[string]string {
	files := make(map[string]string)

	switch agentType {
	case "claude":
		collectClaudeFiles(sessionID, workDir, files)
	case "aider":
		collectAiderFiles(workDir, files)
	case "codex":
		collectCodexFiles(workDir, files)
	case "goose":
		collectGooseFiles(workDir, files)
	case "amp":
		collectAmpFiles(workDir, files)
	case "opencode":
		collectOpenCodeFiles(workDir, files)
	}

	return files
}

// Claude Code: session data in ~/.claude/projects/{hash}/
func collectClaudeFiles(sessionID, workDir string, files map[string]string) {
	if sessionID == "" {
		return
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return
	}

	// Claude Code stores sessions in ~/.claude/projects/{project-hash}/
	// The project hash is derived from the canonical path
	projectHash := claudeProjectDir(workDir)
	sessionDir := filepath.Join(home, ".claude", "projects", projectHash)

	if _, err := os.Stat(sessionDir); os.IsNotExist(err) {
		// Try to find by scanning ~/.claude/projects/
		projectsDir := filepath.Join(home, ".claude", "projects")
		entries, _ := os.ReadDir(projectsDir)
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			// Check if this directory contains our session
			sessionFile := filepath.Join(projectsDir, e.Name(), sessionID+".jsonl")
			if _, err := os.Stat(sessionFile); err == nil {
				sessionDir = filepath.Join(projectsDir, e.Name())
				break
			}
		}
	}

	// Collect session file
	sessionFile := filepath.Join(sessionDir, sessionID+".jsonl")
	if data, err := os.ReadFile(sessionFile); err == nil {
		files["claude/session.jsonl"] = base64.StdEncoding.EncodeToString(data)
	}

	// Collect subagent files if present
	subagentsDir := filepath.Join(sessionDir, sessionID, "subagents")
	if entries, err := os.ReadDir(subagentsDir); err == nil {
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(e.Name(), ".jsonl") {
				if data, err := os.ReadFile(filepath.Join(subagentsDir, e.Name())); err == nil {
					files["claude/subagents/"+e.Name()] = base64.StdEncoding.EncodeToString(data)
				}
			}
		}
	}

	// Also collect CLAUDE.md if present
	claudeMD := filepath.Join(workDir, "CLAUDE.md")
	if data, err := os.ReadFile(claudeMD); err == nil {
		files["CLAUDE.md"] = string(data)
	}

	// Collect .claude/settings.json if present
	settingsFile := filepath.Join(home, ".claude", "settings.json")
	if data, err := os.ReadFile(settingsFile); err == nil {
		files["claude/settings.json"] = string(data)
	}
}

// Aider: chat history in project directory
func collectAiderFiles(workDir string, files map[string]string) {
	aiderFiles := []string{
		".aider.chat.history.md",
		".aider.input.history",
		".aider.conf.yml",
		".aider.model.settings.yml",
	}
	for _, name := range aiderFiles {
		fpath := filepath.Join(workDir, name)
		if data, err := os.ReadFile(fpath); err == nil {
			files["aider/"+name] = string(data)
		}
	}
}

// Codex: session state
func collectCodexFiles(workDir string, files map[string]string) {
	// Codex stores state in .codex/ in the project directory
	codexDir := filepath.Join(workDir, ".codex")
	if _, err := os.Stat(codexDir); err == nil {
		filepath.Walk(codexDir, func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() || info.Size() > 1<<20 {
				return nil
			}
			rel, _ := filepath.Rel(workDir, path)
			data, _ := os.ReadFile(path)
			if data != nil {
				files["codex/"+rel] = base64.StdEncoding.EncodeToString(data)
			}
			return nil
		})
	}
}

// Goose: session state
func collectGooseFiles(workDir string, files map[string]string) {
	home, _ := os.UserHomeDir()
	gooseDir := filepath.Join(home, ".config", "goose", "sessions")
	if entries, err := os.ReadDir(gooseDir); err == nil {
		// Get most recent session file
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(e.Name(), ".json") {
				data, _ := os.ReadFile(filepath.Join(gooseDir, e.Name()))
				if data != nil {
					files["goose/"+e.Name()] = base64.StdEncoding.EncodeToString(data)
				}
			}
		}
	}
}

// Amp: session state
func collectAmpFiles(workDir string, files map[string]string) {
	ampDir := filepath.Join(workDir, ".amp")
	if _, err := os.Stat(ampDir); err == nil {
		filepath.Walk(ampDir, func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() || info.Size() > 1<<20 {
				return nil
			}
			rel, _ := filepath.Rel(workDir, path)
			data, _ := os.ReadFile(path)
			if data != nil {
				files["amp/"+rel] = base64.StdEncoding.EncodeToString(data)
			}
			return nil
		})
	}
}

// OpenCode: session state
func collectOpenCodeFiles(workDir string, files map[string]string) {
	ocDir := filepath.Join(workDir, ".opencode")
	if _, err := os.Stat(ocDir); err == nil {
		filepath.Walk(ocDir, func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() || info.Size() > 1<<20 {
				return nil
			}
			rel, _ := filepath.Rel(workDir, path)
			data, _ := os.ReadFile(path)
			if data != nil {
				files["opencode/"+rel] = base64.StdEncoding.EncodeToString(data)
			}
			return nil
		})
	}
}

// writeAgentFiles writes agent-specific session files on import.
func writeAgentFiles(agentType, sessionID string, files map[string]string, workDir string, warnings *[]string) {
	home, _ := os.UserHomeDir()

	for key, content := range files {
		// Sanitize
		if strings.Contains(key, "..") {
			continue
		}

		switch {
		case agentType == "claude" && key == "claude/session.jsonl":
			if sessionID == "" {
				continue
			}
			projectHash := claudeProjectDir(workDir)
			targetDir := filepath.Join(home, ".claude", "projects", projectHash)
			os.MkdirAll(targetDir, 0700)
			data, err := base64.StdEncoding.DecodeString(content)
			if err != nil {
				*warnings = append(*warnings, "Failed to decode Claude session file")
				continue
			}
			targetFile := filepath.Join(targetDir, sessionID+".jsonl")
			if err := os.WriteFile(targetFile, data, 0600); err != nil {
				*warnings = append(*warnings, fmt.Sprintf("Failed to write Claude session: %v", err))
			} else {
				log.Printf("[transfer] Wrote Claude session to %s", targetFile)
			}

		case agentType == "claude" && strings.HasPrefix(key, "claude/subagents/"):
			if sessionID == "" {
				continue
			}
			projectDir := claudeProjectDir(workDir)
			subagentName := strings.TrimPrefix(key, "claude/subagents/")
			targetDir := filepath.Join(home, ".claude", "projects", projectDir, sessionID, "subagents")
			os.MkdirAll(targetDir, 0700)
			data, _ := base64.StdEncoding.DecodeString(content)
			if data != nil {
				os.WriteFile(filepath.Join(targetDir, subagentName), data, 0600)
			}

		case key == "CLAUDE.md":
			fpath := filepath.Join(workDir, "CLAUDE.md")
			if _, err := os.Stat(fpath); os.IsNotExist(err) {
				os.WriteFile(fpath, []byte(content), 0644)
			}

		case strings.HasPrefix(key, "aider/"):
			name := strings.TrimPrefix(key, "aider/")
			fpath := filepath.Join(workDir, name)
			os.WriteFile(fpath, []byte(content), 0644)

		case strings.HasPrefix(key, "codex/"):
			rel := strings.TrimPrefix(key, "codex/")
			fpath := filepath.Join(workDir, rel)
			os.MkdirAll(filepath.Dir(fpath), 0755)
			data, _ := base64.StdEncoding.DecodeString(content)
			if data != nil {
				os.WriteFile(fpath, data, 0644)
			}

		case strings.HasPrefix(key, "goose/"):
			gooseDir := filepath.Join(home, ".config", "goose", "sessions")
			os.MkdirAll(gooseDir, 0755)
			name := strings.TrimPrefix(key, "goose/")
			data, _ := base64.StdEncoding.DecodeString(content)
			if data != nil {
				os.WriteFile(filepath.Join(gooseDir, name), data, 0644)
			}

		case strings.HasPrefix(key, "amp/"):
			rel := strings.TrimPrefix(key, "amp/")
			fpath := filepath.Join(workDir, rel)
			os.MkdirAll(filepath.Dir(fpath), 0755)
			data, _ := base64.StdEncoding.DecodeString(content)
			if data != nil {
				os.WriteFile(fpath, data, 0644)
			}

		case strings.HasPrefix(key, "opencode/"):
			rel := strings.TrimPrefix(key, "opencode/")
			fpath := filepath.Join(workDir, rel)
			os.MkdirAll(filepath.Dir(fpath), 0755)
			data, _ := base64.StdEncoding.DecodeString(content)
			if data != nil {
				os.WriteFile(fpath, data, 0644)
			}
		}
	}
}

// claudeProjectDir returns the directory name Claude Code uses for session storage.
// Claude Code stores sessions in ~/.claude/projects/{path-with-slashes-replaced-by-dashes}/
// e.g., /Users/alice/project → -Users-alice-project
func claudeProjectDir(dir string) string {
	absDir, err := filepath.Abs(dir)
	if err != nil {
		absDir = dir
	}
	return strings.ReplaceAll(absDir, "/", "-")
}

// listClaudeSessions scans ~/.claude/projects/{projectDir}/ for session JSONL files.
// Returns session UUIDs sorted by modification time (newest first).
func listClaudeSessions(workDir string) []ClaudeSessionInfo {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}

	projectDir := claudeProjectDir(workDir)
	sessionPath := filepath.Join(home, ".claude", "projects", projectDir)

	entries, err := os.ReadDir(sessionPath)
	if err != nil {
		return nil
	}

	var sessions []ClaudeSessionInfo
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		sessionID := strings.TrimSuffix(e.Name(), ".jsonl")
		info, _ := e.Info()
		size := int64(0)
		modTime := time.Time{}
		if info != nil {
			size = info.Size()
			modTime = info.ModTime()
		}

		// Read first and last lines to get summary
		fpath := filepath.Join(sessionPath, e.Name())
		summary := readClaudeSessionSummary(fpath)

		sessions = append(sessions, ClaudeSessionInfo{
			SessionID: sessionID,
			FilePath:  fpath,
			FileSize:  size,
			ModTime:   modTime,
			Summary:   summary,
		})
	}

	// Sort by mod time, newest first
	for i := 0; i < len(sessions); i++ {
		for j := i + 1; j < len(sessions); j++ {
			if sessions[j].ModTime.After(sessions[i].ModTime) {
				sessions[i], sessions[j] = sessions[j], sessions[i]
			}
		}
	}

	return sessions
}

// ClaudeSessionInfo describes a Claude Code session file.
type ClaudeSessionInfo struct {
	SessionID string    `json:"sessionId"`
	FilePath  string    `json:"-"`
	FileSize  int64     `json:"fileSize"`
	ModTime   time.Time `json:"modTime"`
	Summary   string    `json:"summary,omitempty"`
}

// readClaudeSessionSummary reads first user message from a Claude session JSONL.
func readClaudeSessionSummary(fpath string) string {
	f, err := os.Open(fpath)
	if err != nil {
		return ""
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 256*1024), 256*1024) // 256KB line buffer
	for scanner.Scan() {
		var entry struct {
			Type    string `json:"type"`
			Message struct {
				Role    string `json:"role"`
				Content interface{} `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal(scanner.Bytes(), &entry) == nil {
			if entry.Type == "user" && entry.Message.Role == "user" {
				switch c := entry.Message.Content.(type) {
				case string:
					if len(c) > 100 {
						return c[:100] + "..."
					}
					return c
				}
			}
		}
	}
	return ""
}

// getGitInfo returns remote URL, branch, and commit hash for a git directory.
func getGitInfo(dir string) (remote, branch, commit string) {
	if out, err := exec.Command("git", "-C", dir, "remote", "get-url", "origin").CombinedOutput(); err == nil {
		remote = strings.TrimSpace(string(out))
	}
	if out, err := exec.Command("git", "-C", dir, "branch", "--show-current").CombinedOutput(); err == nil {
		branch = strings.TrimSpace(string(out))
	}
	if out, err := exec.Command("git", "-C", dir, "rev-parse", "HEAD").CombinedOutput(); err == nil {
		commit = strings.TrimSpace(string(out))
	}
	return
}

// collectConfigFiles collects project config files (CLAUDE.md, .aider.conf.yml, etc.)
func collectConfigFiles(dir string) map[string]string {
	configs := make(map[string]string)
	configNames := []string{
		"CLAUDE.md", ".claude/settings.local.json",
		".aider.conf.yml", ".aider.model.settings.yml",
		".env.example", "Makefile", "Taskfile.yml",
		".cursorrules", ".cursorignore",
	}
	for _, name := range configNames {
		fpath := filepath.Join(dir, name)
		if data, err := os.ReadFile(fpath); err == nil {
			configs[name] = string(data)
		}
	}
	return configs
}

// tarWorkspace creates a tar.gz of the workspace, excluding large/build directories.
func tarWorkspace(dir string) ([]byte, error) {
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)

	excludes := map[string]bool{
		".git": true, "node_modules": true, ".next": true,
		"__pycache__": true, "vendor": true, "Pods": true,
		"build": true, "dist": true, ".yaver": true,
		"target": true, ".cache": true, ".venv": true,
		"venv": true, "env": true,
	}

	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // skip errors
		}
		rel, _ := filepath.Rel(dir, path)
		if rel == "." {
			return nil
		}

		// Check excludes
		parts := strings.Split(rel, string(filepath.Separator))
		for _, p := range parts {
			if excludes[p] {
				if info.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
		}

		// Skip large files (> 5MB)
		if !info.IsDir() && info.Size() > 5*1024*1024 {
			return nil
		}

		header, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return nil
		}
		header.Name = rel

		if err := tw.WriteHeader(header); err != nil {
			return err
		}
		if !info.IsDir() {
			f, err := os.Open(path)
			if err != nil {
				return nil
			}
			defer f.Close()
			io.Copy(tw, f)
		}
		return nil
	})

	tw.Close()
	gw.Close()

	if err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// extractTarGz extracts a tar.gz archive into the target directory.
func extractTarGz(data []byte, targetDir string) error {
	gr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return err
	}
	defer gr.Close()

	tr := tar.NewReader(gr)
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}

		// Sanitize path
		if strings.Contains(header.Name, "..") {
			continue
		}

		target := filepath.Join(targetDir, header.Name)

		switch header.Typeflag {
		case tar.TypeDir:
			os.MkdirAll(target, 0755)
		case tar.TypeReg:
			os.MkdirAll(filepath.Dir(target), 0755)
			f, err := os.Create(target)
			if err != nil {
				continue
			}
			io.Copy(f, tr)
			f.Close()
			os.Chmod(target, os.FileMode(header.Mode))
		}
	}
	return nil
}

// generateTaskID creates a short unique task ID.
func generateTaskID() string {
	h := sha256.Sum256([]byte(fmt.Sprintf("%d-%d", time.Now().UnixNano(), os.Getpid())))
	return hex.EncodeToString(h[:4])
}

// Ensure json import is used (referenced in other files that import this package's types).
var _ = json.Marshal
