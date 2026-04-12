package main

import (
	"bytes"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// ─── Types ────────────────────────────────────────────────────────────────────

// PreviewEnv represents an active branch preview deployment.
type PreviewEnv struct {
	ID        string    `json:"id"`
	Branch    string    `json:"branch"`
	Port      int       `json:"port"`
	URL       string    `json:"url"`       // local URL, e.g. http://localhost:4001
	TunnelURL string    `json:"tunnelUrl"` // public URL if exposed, empty otherwise
	WorkDir   string    `json:"workDir"`   // git worktree path
	Status    string    `json:"status"`    // building / running / stopped / failed
	CreatedAt time.Time `json:"createdAt"`
	BuildLog  string    `json:"buildLog"`
	PID       int       `json:"pid"`
}

// PreviewManager manages branch preview deployments backed by git worktrees.
type PreviewManager struct {
	mu       sync.Mutex
	workDir  string
	previews map[string]*PreviewEnv
	basePort int
	cmds     map[string]*exec.Cmd
}

// NewPreviewManager creates a PreviewManager rooted at workDir.
// basePort is the starting port for preview allocations (default 4001).
func NewPreviewManager(workDir string) *PreviewManager {
	return &PreviewManager{
		workDir:  workDir,
		previews: make(map[string]*PreviewEnv),
		basePort: 4001,
		cmds:     make(map[string]*exec.Cmd),
	}
}

// ─── Public API ───────────────────────────────────────────────────────────────

// Create builds and serves a preview for the given branch.
// If port is 0 the next free port starting from basePort is used.
// If expose is true a public tunnel is opened via ExposeManager.
func (m *PreviewManager) Create(branch string, port int, expose bool) (*PreviewEnv, error) {
	id := sanitizeBranchName(branch)
	worktreePath := filepath.Join("/tmp", "yaver-preview-"+id)

	m.mu.Lock()
	if existing, ok := m.previews[id]; ok {
		cp := *existing
		m.mu.Unlock()
		return &cp, fmt.Errorf("preview for branch %q already exists (status: %s)", branch, existing.Status)
	}
	if port == 0 {
		port = m.findFreePort(m.basePort)
	}
	env := &PreviewEnv{
		ID:        id,
		Branch:    branch,
		Port:      port,
		URL:       fmt.Sprintf("http://localhost:%d", port),
		WorkDir:   worktreePath,
		Status:    "building",
		CreatedAt: time.Now(),
	}
	m.previews[id] = env
	m.mu.Unlock()

	// appendLog appends a line to BuildLog under the manager lock.
	appendLog := func(text string) {
		m.mu.Lock()
		env.BuildLog += text
		m.mu.Unlock()
	}

	// All I/O runs outside the lock so other operations aren't blocked.
	if err := m.setupWorktree(branch, worktreePath, appendLog); err != nil {
		m.setStatus(id, "failed")
		return snapshotEnv(m, id), err
	}

	installOut, err := installDeps(worktreePath)
	appendLog(installOut)
	if err != nil {
		m.setStatus(id, "failed")
		return snapshotEnv(m, id), fmt.Errorf("install deps: %w", err)
	}

	buildOut, err := buildProject(worktreePath)
	appendLog(buildOut)
	if err != nil {
		m.setStatus(id, "failed")
		return snapshotEnv(m, id), fmt.Errorf("build: %w", err)
	}

	cmd, err := detectFrameworkAndServe(worktreePath, port)
	if err != nil {
		m.setStatus(id, "failed")
		return snapshotEnv(m, id), fmt.Errorf("serve: %w", err)
	}

	if err := cmd.Start(); err != nil {
		m.setStatus(id, "failed")
		return snapshotEnv(m, id), fmt.Errorf("start serve process: %w", err)
	}

	m.mu.Lock()
	env.PID = cmd.Process.Pid
	env.Status = "running"
	m.cmds[id] = cmd
	m.mu.Unlock()

	// Reap the process asynchronously; update status when it exits.
	go func() {
		_ = cmd.Wait()
		m.mu.Lock()
		if e, ok := m.previews[id]; ok && e.Status == "running" {
			e.Status = "stopped"
		}
		m.mu.Unlock()
	}()

	if expose {
		em := NewExposeManager()
		tunnel, tunnelErr := em.Start(port, id)
		if tunnelErr == nil {
			m.mu.Lock()
			env.TunnelURL = tunnel.PublicURL
			m.mu.Unlock()
		}
		// Non-fatal: preview is still accessible locally without a tunnel.
	}

	return snapshotEnv(m, id), nil
}

// Stop terminates the preview identified by branch name or ID, removes the
// git worktree, and cleans up the temporary directory.
func (m *PreviewManager) Stop(branchOrID string) (string, error) {
	id := sanitizeBranchName(branchOrID)

	m.mu.Lock()
	env, ok := m.previews[id]
	cmd := m.cmds[id]
	if ok {
		env.Status = "stopped"
		delete(m.previews, id)
		delete(m.cmds, id)
	}
	m.mu.Unlock()

	if !ok {
		return "", fmt.Errorf("no preview found for %q", branchOrID)
	}

	// Kill the serve process.
	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
		// Reap so we don't leave a zombie.
		go func() { _ = cmd.Wait() }()
	}

	// Remove git worktree.
	if err := m.removeWorktree(env.WorkDir); err != nil {
		return fmt.Sprintf("stopped preview %s (worktree removal warning: %v)", id, err), nil
	}

	return fmt.Sprintf("stopped preview %s (branch: %s, was on port %d)", id, env.Branch, env.Port), nil
}

// List returns a snapshot of all active preview environments.
func (m *PreviewManager) List() ([]*PreviewEnv, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	out := make([]*PreviewEnv, 0, len(m.previews))
	for _, env := range m.previews {
		cp := *env
		out = append(out, &cp)
	}
	return out, nil
}

// Logs returns the captured build/serve log for a preview.
func (m *PreviewManager) Logs(branchOrID string) (string, error) {
	id := sanitizeBranchName(branchOrID)

	m.mu.Lock()
	defer m.mu.Unlock()

	env, ok := m.previews[id]
	if !ok {
		return "", fmt.Errorf("no preview found for %q", branchOrID)
	}
	return env.BuildLog, nil
}

// StopAll stops every active preview and cleans up all worktrees.
func (m *PreviewManager) StopAll() (string, error) {
	m.mu.Lock()
	ids := make([]string, 0, len(m.previews))
	for id := range m.previews {
		ids = append(ids, id)
	}
	m.mu.Unlock()

	count := len(ids)
	var msgs []string
	var firstErr error
	for _, id := range ids {
		msg, err := m.Stop(id)
		if err != nil && firstErr == nil {
			firstErr = err
		}
		if msg != "" {
			msgs = append(msgs, msg)
		}
	}

	summary := fmt.Sprintf("stopped %d preview(s)", count)
	if len(msgs) > 0 {
		summary += "\n" + strings.Join(msgs, "\n")
	}
	return summary, firstErr
}

// ─── Internal helpers ─────────────────────────────────────────────────────────

// findFreePort returns the next available TCP port at or above base.
// Caller must hold m.mu.
func (m *PreviewManager) findFreePort(base int) int {
	for port := base; port < base+1000; port++ {
		// Skip ports already claimed by active previews.
		inUse := false
		for _, env := range m.previews {
			if env.Port == port {
				inUse = true
				break
			}
		}
		if inUse {
			continue
		}

		ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
		if err == nil {
			_ = ln.Close()
			return port
		}
	}
	// Fallback: let the OS pick an ephemeral port.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return base
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	return port
}

// setupWorktree creates the git worktree for branch at dest.
// appendLog is called with progress text as each step completes.
func (m *PreviewManager) setupWorktree(branch, dest string, appendLog func(string)) error {
	// Remove stale directory from a crashed previous run.
	if _, err := os.Stat(dest); err == nil {
		_ = os.RemoveAll(dest)
	}

	cmd := exec.Command("git", "worktree", "add", dest, branch)
	cmd.Dir = m.workDir
	out, err := cmd.CombinedOutput()
	appendLog(fmt.Sprintf("$ git worktree add %s %s\n%s\n", dest, branch, string(out)))

	if err != nil {
		return fmt.Errorf("git worktree add %s %s: %w\n%s", dest, branch, err, string(out))
	}
	return nil
}

// removeWorktree removes the git worktree registration and the temp directory.
func (m *PreviewManager) removeWorktree(worktreePath string) error {
	cmd := exec.Command("git", "worktree", "remove", "--force", worktreePath)
	cmd.Dir = m.workDir
	out, err := cmd.CombinedOutput()
	// Always attempt a plain rm in case git left the directory behind.
	_ = os.RemoveAll(worktreePath)
	if err != nil {
		return fmt.Errorf("git worktree remove: %w\n%s", err, string(out))
	}
	return nil
}

// setStatus updates the status field of an existing preview under the manager lock.
func (m *PreviewManager) setStatus(id, status string) {
	m.mu.Lock()
	if env, ok := m.previews[id]; ok {
		env.Status = status
	}
	m.mu.Unlock()
}

// snapshotEnv returns a copy of the PreviewEnv with the given id under the lock.
// Returns an empty pointer if the id is not found.
func snapshotEnv(m *PreviewManager, id string) *PreviewEnv {
	m.mu.Lock()
	defer m.mu.Unlock()
	if env, ok := m.previews[id]; ok {
		cp := *env
		return &cp
	}
	return &PreviewEnv{}
}

// ─── Framework detection ──────────────────────────────────────────────────────

// detectFrameworkAndServe auto-detects the project framework and returns an
// *exec.Cmd (not yet started) that will serve the built project on port.
func detectFrameworkAndServe(workDir string, port int) (*exec.Cmd, error) {
	portStr := fmt.Sprintf("%d", port)

	// Next.js: has next.config.{js,ts,mjs}
	for _, name := range []string{"next.config.js", "next.config.ts", "next.config.mjs"} {
		if hasFile(workDir, name) {
			cmd := exec.Command("npx", "next", "start", "-p", portStr)
			cmd.Dir = workDir
			return cmd, nil
		}
	}

	// Astro: has astro.config.{js,ts,mjs}
	for _, name := range []string{"astro.config.js", "astro.config.ts", "astro.config.mjs"} {
		if hasFile(workDir, name) {
			cmd := exec.Command("npx", "astro", "preview", "--port", portStr)
			cmd.Dir = workDir
			return cmd, nil
		}
	}

	// Vite: has vite.config.{js,ts}
	for _, name := range []string{"vite.config.js", "vite.config.ts"} {
		if hasFile(workDir, name) {
			var cmd *exec.Cmd
			if hasDir(workDir, "dist") {
				cmd = exec.Command("npx", "vite", "preview", "--port", portStr)
			} else {
				cmd = exec.Command("npx", "serve", "dist", "-l", portStr)
			}
			cmd.Dir = workDir
			return cmd, nil
		}
	}

	// Go project: has go.mod
	if hasFile(workDir, "go.mod") {
		binName := filepath.Base(workDir)
		binPath := filepath.Join(workDir, binName)
		var cmd *exec.Cmd
		if _, err := os.Stat(binPath); err == nil {
			cmd = exec.Command(binPath, "--port", portStr)
		} else {
			cmd = exec.Command("go", "run", ".", "--port", portStr)
		}
		cmd.Dir = workDir
		return cmd, nil
	}

	// React / generic JS with build output.
	if hasFile(workDir, "package.json") {
		var cmd *exec.Cmd
		switch {
		case hasDir(workDir, "build"):
			cmd = exec.Command("npx", "serve", "build", "-l", portStr)
		case hasDir(workDir, "dist"):
			cmd = exec.Command("npx", "serve", "dist", "-l", portStr)
		default:
			cmd = exec.Command("npx", "serve", ".", "-l", portStr)
		}
		cmd.Dir = workDir
		return cmd, nil
	}

	// Static fallback: serve the project root.
	cmd := exec.Command("npx", "serve", ".", "-l", portStr)
	cmd.Dir = workDir
	return cmd, nil
}

// ─── Package manager / build helpers ─────────────────────────────────────────

// installDeps detects the package manager and runs install.
// Returns combined stdout+stderr output and any error.
func installDeps(workDir string) (string, error) {
	if !hasFile(workDir, "package.json") {
		return "", nil
	}

	var pm string
	var args []string
	switch {
	case hasFile(workDir, "bun.lockb"):
		pm, args = "bun", []string{"install", "--frozen-lockfile"}
	case hasFile(workDir, "pnpm-lock.yaml"):
		pm, args = "pnpm", []string{"install", "--frozen-lockfile"}
	case hasFile(workDir, "yarn.lock"):
		pm, args = "yarn", []string{"install", "--frozen-lockfile"}
	default:
		pm, args = "npm", []string{"ci"}
	}

	cmd := exec.Command(pm, args...)
	cmd.Dir = workDir
	out, err := cmd.CombinedOutput()
	logLine := fmt.Sprintf("$ %s %s\n%s\n", pm, strings.Join(args, " "), string(out))

	if err != nil {
		return logLine, fmt.Errorf("%s %s: %w", pm, strings.Join(args, " "), err)
	}
	return logLine, nil
}

// buildProject runs the appropriate build command and returns the combined
// stdout+stderr output and any error.
func buildProject(workDir string) (string, error) {
	var buf bytes.Buffer

	if hasFile(workDir, "package.json") {
		script := detectBuildScript(workDir)
		if script == "" {
			// No build script configured; nothing to do.
			return "", nil
		}

		var cmd *exec.Cmd
		switch {
		case hasFile(workDir, "bun.lockb"):
			cmd = exec.Command("bun", "run", script)
		case hasFile(workDir, "pnpm-lock.yaml"):
			cmd = exec.Command("pnpm", "run", script)
		case hasFile(workDir, "yarn.lock"):
			cmd = exec.Command("yarn", script)
		default:
			cmd = exec.Command("npm", "run", script)
		}
		cmd.Dir = workDir
		cmd.Stdout = &buf
		cmd.Stderr = &buf
		fmt.Fprintf(&buf, "$ build script: %s\n", script)
		if err := cmd.Run(); err != nil {
			return buf.String(), fmt.Errorf("build script %q: %w", script, err)
		}
		return buf.String(), nil
	}

	if hasFile(workDir, "go.mod") {
		cmd := exec.Command("go", "build", "-o", filepath.Base(workDir), ".")
		cmd.Dir = workDir
		cmd.Stdout = &buf
		cmd.Stderr = &buf
		fmt.Fprintf(&buf, "$ go build\n")
		if err := cmd.Run(); err != nil {
			return buf.String(), fmt.Errorf("go build: %w", err)
		}
		return buf.String(), nil
	}

	// Nothing to build.
	return "", nil
}

// detectBuildScript reads package.json and returns the first recognised build
// script name found in the "scripts" section.
func detectBuildScript(workDir string) string {
	data, err := os.ReadFile(filepath.Join(workDir, "package.json"))
	if err != nil {
		return ""
	}
	for _, candidate := range []string{"build", "build:prod", "build:production", "export"} {
		key := fmt.Sprintf(`"%s"`, candidate)
		if bytes.Contains(data, []byte(key)) {
			return candidate
		}
	}
	return ""
}

// ─── Branch name helper ───────────────────────────────────────────────────────

// sanitizeBranchName converts a git branch name to a safe identifier for use
// as a directory suffix and URL segment.
func sanitizeBranchName(branch string) string {
	r := strings.NewReplacer(
		"/", "-",
		"\\", "-",
		" ", "-",
		".", "-",
		"_", "-",
		":", "-",
		"@", "-",
		"#", "",
		"?", "",
		"&", "",
		"=", "",
		"%", "",
	)
	s := r.Replace(strings.ToLower(branch))
	// Collapse consecutive dashes.
	for strings.Contains(s, "--") {
		s = strings.ReplaceAll(s, "--", "-")
	}
	s = strings.Trim(s, "-")
	if s == "" {
		s = "preview"
	}
	return s
}
// hasFile and hasDir are defined in project_actions.go and shared across the package.
