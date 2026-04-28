package main

// runner_sandbox.go — long-lived sandbox sessions on top of
// container_runner.go (RUNNER_DEV.md Phase 2). Replaces e2b/Modal
// sandbox-as-a-service: a Yaver client opens a sandbox, runs zero or
// more `exec` calls and `files` operations against it, then stops it
// — all on the user's own machine.
//
// Implementation: each session is a long-running Docker container
// spawned with `tail -f /dev/null` as the entrypoint, then driven
// via `docker exec`. Same security envelope as ContainerRunner —
// vault env via composeRunnerEnv, optional read-only root, optional
// project workdir mount.
//
// Idle TTL: a session that has not been touched for sandboxIdleTTL
// (default 30 min) is reaped on the next housekeeping tick. Clients
// can extend the TTL by hitting any endpoint; the manager refreshes
// LastActivity on every Exec/ReadFile/WriteFile.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// sandboxIdleTTL is how long a sandbox can sit idle before the
// housekeeping goroutine reaps it. 30 min is the e2b free-tier
// default; long enough for an LLM to think between calls, short
// enough that abandoned sessions don't pin Docker resources.
const sandboxIdleTTL = 30 * time.Minute

// sandboxHousekeepInterval is how often the manager scans for idle
// sessions to reap.
const sandboxHousekeepInterval = 60 * time.Second

// sandboxFileMaxBytes is the read/write size cap. 2 MB matches the
// e2b API limit and protects the agent's RAM from a runaway client
// shovelling a giant log into memory.
const sandboxFileMaxBytes = 2 * 1024 * 1024

// sandboxExecOutputCap is the most stdout+stderr a single Exec call
// will buffer in memory before truncating. Mirrors deployOutputTailCap
// in shape, sized larger because a sandbox call can produce real
// build output.
const sandboxExecOutputCap = 256 * 1024

// SandboxSession is one live sandbox on disk. Mirrors the flat-JSON
// shape RunnerRun/DeployRun use so the mobile + web UIs render it
// with the same components.
type SandboxSession struct {
	ID           string `json:"id"`
	Label        string `json:"label,omitempty"`
	Image        string `json:"image"`
	Project      string `json:"project,omitempty"`
	WorkspaceDir string `json:"workspaceDir,omitempty"` // host path mounted into /workspace; empty = scratch sandbox
	NetworkMode  string `json:"networkMode,omitempty"`  // host | bridge | none
	ReadOnly     bool   `json:"readOnly,omitempty"`
	CPULimit     string `json:"cpuLimit,omitempty"`
	MemoryLimit  string `json:"memoryLimit,omitempty"`
	CreatedAt    int64  `json:"createdAt"`
	LastActivity int64  `json:"lastActivity"`
	TTLSec       int    `json:"ttlSec,omitempty"` // override default; 0 = use sandboxIdleTTL
	OwnerUserID  string `json:"ownerUserID,omitempty"`

	containerName string
}

// SandboxStartOpts is the sandbox-creation request.
type SandboxStartOpts struct {
	Label        string `json:"label,omitempty"`
	Image        string `json:"image,omitempty"`
	Project      string `json:"project,omitempty"`
	WorkspaceDir string `json:"workspaceDir,omitempty"`
	NetworkMode  string `json:"networkMode,omitempty"`
	ReadOnly     bool   `json:"readOnly,omitempty"`
	CPULimit     string `json:"cpuLimit,omitempty"`
	MemoryLimit  string `json:"memoryLimit,omitempty"`
	TTLSec       int    `json:"ttlSec,omitempty"`
}

// SandboxExecOpts feeds Exec.
type SandboxExecOpts struct {
	Command  string            `json:"command"`
	Env      map[string]string `json:"env,omitempty"`
	WorkDir  string            `json:"workDir,omitempty"`
	TimeoutS int               `json:"timeoutSec,omitempty"`
}

// SandboxExecResult is what Exec returns.
type SandboxExecResult struct {
	ExitCode int    `json:"exitCode"`
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
	OK       bool   `json:"ok"`
	TimedOut bool   `json:"timedOut,omitempty"`
}

// SandboxManager owns the live sandbox set and the housekeeping
// goroutine. Created lazily on first /runner/sandboxes request.
type SandboxManager struct {
	mu        sync.Mutex
	sessions  map[string]*SandboxSession
	runner    *ContainerRunner
	stopCh    chan struct{}
	stopped   bool
	dockerCmd string
}

// NewSandboxManager wires the manager to the agent's existing
// ContainerRunner. Returns nil when Docker is unavailable so callers
// can render a friendly "install Docker" error instead of a panic.
func NewSandboxManager(runner *ContainerRunner) *SandboxManager {
	if runner == nil {
		return nil
	}
	dockerCmd := runner.dockerPath
	if dockerCmd == "" {
		return nil
	}
	m := &SandboxManager{
		sessions:  map[string]*SandboxSession{},
		runner:    runner,
		stopCh:    make(chan struct{}),
		dockerCmd: dockerCmd,
	}
	go m.housekeepLoop()
	return m
}

// Stop shuts down housekeeping and tears down every live sandbox.
// Safe to call multiple times; subsequent calls no-op.
func (m *SandboxManager) Stop() {
	if m == nil {
		return
	}
	m.mu.Lock()
	if m.stopped {
		m.mu.Unlock()
		return
	}
	m.stopped = true
	close(m.stopCh)
	ids := make([]string, 0, len(m.sessions))
	for id := range m.sessions {
		ids = append(ids, id)
	}
	m.mu.Unlock()
	for _, id := range ids {
		_ = m.StopSandbox(id)
	}
}

// Start launches a new sandbox container and registers the session.
// Returns the populated SandboxSession (with ID + container name).
func (m *SandboxManager) Start(ctx context.Context, opts SandboxStartOpts, ownerUserID string) (*SandboxSession, error) {
	if m == nil {
		return nil, errors.New("sandbox manager unavailable — Docker not detected")
	}
	if !m.runner.IsAvailable() {
		return nil, errors.New("docker not running")
	}
	image := opts.Image
	if strings.TrimSpace(image) == "" {
		image = sandboxImage
	}
	if image == sandboxImage {
		// The default image must be built before a sandbox can start.
		if !m.runner.IsImageReady() {
			return nil, fmt.Errorf("sandbox image %q is not built — run 'yaver sandbox build' first", sandboxImage)
		}
	}
	id := NewRunnerRunID()
	containerName := "yaver-sandbox-" + id
	now := time.Now().UnixMilli()

	args := []string{"run", "-d", "--rm", "--name", containerName}
	if opts.CPULimit != "" {
		args = append(args, "--cpus", opts.CPULimit)
	}
	if opts.MemoryLimit != "" {
		args = append(args, "--memory", opts.MemoryLimit)
	}
	if opts.ReadOnly {
		args = append(args, "--read-only")
		args = append(args, "--tmpfs", "/tmp:rw,noexec,nosuid,size=512m")
		args = append(args, "--tmpfs", "/root:rw,noexec,nosuid,size=256m")
	}
	if opts.WorkspaceDir != "" {
		// Validate via the existing helper so a sandbox can't be
		// pointed at the Docker socket / $HOME / /etc.
		if err := validateContainerMount(opts.WorkspaceDir + ":/workspace"); err != nil {
			return nil, fmt.Errorf("workspace: %w", err)
		}
		args = append(args, "-v", opts.WorkspaceDir+":/workspace")
	}
	args = append(args, "-w", "/workspace")
	netMode := strings.TrimSpace(opts.NetworkMode)
	if netMode == "" {
		netMode = "bridge"
	}
	args = append(args, "--network", netMode)
	args = append(args, image, "tail", "-f", "/dev/null")

	cmd := exec.CommandContext(ctx, m.dockerCmd, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("docker run: %w (%s)", err, strings.TrimSpace(string(out)))
	}

	sess := &SandboxSession{
		ID:            id,
		Label:         opts.Label,
		Image:         image,
		Project:       opts.Project,
		WorkspaceDir:  opts.WorkspaceDir,
		NetworkMode:   netMode,
		ReadOnly:      opts.ReadOnly,
		CPULimit:      opts.CPULimit,
		MemoryLimit:   opts.MemoryLimit,
		CreatedAt:     now,
		LastActivity:  now,
		TTLSec:        opts.TTLSec,
		OwnerUserID:   ownerUserID,
		containerName: containerName,
	}
	m.mu.Lock()
	m.sessions[id] = sess
	m.mu.Unlock()
	log.Printf("[sandbox=%s] started container %s image=%s", id, containerName, image)
	return sess, nil
}

// Get returns one session by id. The bool is false when the id is
// unknown or when the caller-scoped owner does not match (used by
// the HTTP layer to hide other users' sandboxes — the same trick
// DeployHistory.Get uses to make "not yours" indistinguishable from
// "doesn't exist").
func (m *SandboxManager) Get(id, requireOwner string) (*SandboxSession, bool) {
	if m == nil {
		return nil, false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[id]
	if !ok {
		return nil, false
	}
	if requireOwner != "" && s.OwnerUserID != requireOwner {
		return nil, false
	}
	cp := *s
	return &cp, true
}

// List returns every session sorted by CreatedAt descending. Owner
// filter mirrors Get — empty = no filter.
func (m *SandboxManager) List(requireOwner string) []SandboxSession {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]SandboxSession, 0, len(m.sessions))
	for _, s := range m.sessions {
		if requireOwner != "" && s.OwnerUserID != requireOwner {
			continue
		}
		out = append(out, *s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt > out[j].CreatedAt })
	return out
}

// touch updates LastActivity for a session. Caller holds m.mu.
func (m *SandboxManager) touchLocked(id string) {
	if s, ok := m.sessions[id]; ok {
		s.LastActivity = time.Now().UnixMilli()
	}
}

// Exec runs a command inside the sandbox. Stdout + stderr captured
// into memory up to sandboxExecOutputCap, then truncated. The
// session's LastActivity is bumped. Returns SandboxExecResult.
func (m *SandboxManager) Exec(ctx context.Context, id string, opts SandboxExecOpts, requireOwner string) (*SandboxExecResult, error) {
	if m == nil {
		return nil, errors.New("sandbox manager unavailable")
	}
	m.mu.Lock()
	sess, ok := m.sessions[id]
	if !ok {
		m.mu.Unlock()
		return nil, errors.New("sandbox not found")
	}
	if requireOwner != "" && sess.OwnerUserID != requireOwner {
		m.mu.Unlock()
		return nil, errors.New("sandbox not found")
	}
	containerName := sess.containerName
	m.touchLocked(id)
	m.mu.Unlock()

	if strings.TrimSpace(opts.Command) == "" {
		return nil, errors.New("command is required")
	}

	args := []string{"exec"}
	if opts.WorkDir != "" {
		args = append(args, "-w", opts.WorkDir)
	}
	for k, v := range opts.Env {
		if k == "" {
			continue
		}
		args = append(args, "-e", k+"="+v)
	}
	args = append(args, containerName, "sh", "-c", opts.Command)

	cctx := ctx
	var cancel context.CancelFunc
	if opts.TimeoutS > 0 {
		cctx, cancel = context.WithTimeout(ctx, time.Duration(opts.TimeoutS)*time.Second)
		defer cancel()
	}
	cmd := exec.CommandContext(cctx, m.dockerCmd, args...)
	var stdoutBuf, stderrBuf bytes.Buffer
	cmd.Stdout = &capWriter{w: &stdoutBuf, max: sandboxExecOutputCap}
	cmd.Stderr = &capWriter{w: &stderrBuf, max: sandboxExecOutputCap}

	runErr := cmd.Run()
	exitCode := 0
	timedOut := false
	if runErr != nil {
		if ee, ok := runErr.(*exec.ExitError); ok {
			exitCode = ee.ExitCode()
		} else {
			exitCode = -1
		}
		if cctx.Err() == context.DeadlineExceeded {
			timedOut = true
			exitCode = -1
		}
	}
	return &SandboxExecResult{
		ExitCode: exitCode,
		Stdout:   stdoutBuf.String(),
		Stderr:   stderrBuf.String(),
		OK:       exitCode == 0 && !timedOut,
		TimedOut: timedOut,
	}, nil
}

// WriteFile copies content into the sandbox at the given path.
// Implementation: `docker exec -i <name> sh -c 'cat > "<path>"'`
// fed via stdin. Caps at sandboxFileMaxBytes — bigger payloads are
// rejected to keep the agent honest about its memory.
func (m *SandboxManager) WriteFile(ctx context.Context, id, path string, content []byte, requireOwner string) error {
	if m == nil {
		return errors.New("sandbox manager unavailable")
	}
	if path == "" {
		return errors.New("path is required")
	}
	if len(content) > sandboxFileMaxBytes {
		return fmt.Errorf("content exceeds %d bytes", sandboxFileMaxBytes)
	}
	m.mu.Lock()
	sess, ok := m.sessions[id]
	if !ok || (requireOwner != "" && sess.OwnerUserID != requireOwner) {
		m.mu.Unlock()
		return errors.New("sandbox not found")
	}
	containerName := sess.containerName
	m.touchLocked(id)
	m.mu.Unlock()

	// `cat > path` writes via stdin; quoted path stops shell metas.
	quoted := shellEscape(path)
	cmd := exec.CommandContext(ctx, m.dockerCmd, "exec", "-i", containerName, "sh", "-c", "cat > "+quoted)
	cmd.Stdin = bytes.NewReader(content)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("write %s: %w (%s)", path, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// ReadFile pulls a file from the sandbox. Same size cap as Write.
func (m *SandboxManager) ReadFile(ctx context.Context, id, path string, requireOwner string) ([]byte, error) {
	if m == nil {
		return nil, errors.New("sandbox manager unavailable")
	}
	if path == "" {
		return nil, errors.New("path is required")
	}
	m.mu.Lock()
	sess, ok := m.sessions[id]
	if !ok || (requireOwner != "" && sess.OwnerUserID != requireOwner) {
		m.mu.Unlock()
		return nil, errors.New("sandbox not found")
	}
	containerName := sess.containerName
	m.touchLocked(id)
	m.mu.Unlock()

	quoted := shellEscape(path)
	cmd := exec.CommandContext(ctx, m.dockerCmd, "exec", containerName, "sh", "-c", "cat "+quoted)
	var out bytes.Buffer
	cmd.Stdout = &capWriter{w: &out, max: sandboxFileMaxBytes}
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	if out.Len() >= sandboxFileMaxBytes {
		return nil, fmt.Errorf("file exceeds %d bytes", sandboxFileMaxBytes)
	}
	return out.Bytes(), nil
}

// StopSandbox tears down the container and removes the session.
// Idempotent — already-stopped sessions are a no-op.
func (m *SandboxManager) StopSandbox(id string) error {
	if m == nil {
		return errors.New("sandbox manager unavailable")
	}
	m.mu.Lock()
	sess, ok := m.sessions[id]
	if !ok {
		m.mu.Unlock()
		return errors.New("sandbox not found")
	}
	delete(m.sessions, id)
	containerName := sess.containerName
	m.mu.Unlock()

	cmd := exec.Command(m.dockerCmd, "stop", "-t", "5", containerName)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	_ = cmd.Run() // best-effort; --rm cleans up the container itself
	log.Printf("[sandbox=%s] stopped container %s", id, containerName)
	return nil
}

// housekeepLoop periodically reaps idle sandboxes. Runs until Stop()
// closes m.stopCh.
func (m *SandboxManager) housekeepLoop() {
	ticker := time.NewTicker(sandboxHousekeepInterval)
	defer ticker.Stop()
	for {
		select {
		case <-m.stopCh:
			return
		case <-ticker.C:
			m.reapIdle()
		}
	}
}

func (m *SandboxManager) reapIdle() {
	if m == nil {
		return
	}
	now := time.Now().UnixMilli()
	m.mu.Lock()
	var expired []string
	for id, s := range m.sessions {
		ttl := time.Duration(s.TTLSec) * time.Second
		if ttl <= 0 {
			ttl = sandboxIdleTTL
		}
		if time.Duration(now-s.LastActivity)*time.Millisecond > ttl {
			expired = append(expired, id)
		}
	}
	m.mu.Unlock()
	for _, id := range expired {
		log.Printf("[sandbox=%s] idle TTL expired — reaping", id)
		_ = m.StopSandbox(id)
	}
}

// capWriter wraps an io.Writer with a hard byte cap. Once the cap is
// reached, subsequent writes silently no-op — the upstream
// subprocess keeps producing, but we stop accumulating in memory.
type capWriter struct {
	w   io.Writer
	max int
	n   int
}

func (c *capWriter) Write(p []byte) (int, error) {
	if c.max > 0 && c.n >= c.max {
		return len(p), nil
	}
	remain := c.max - c.n
	if c.max > 0 && len(p) > remain {
		_, _ = c.w.Write(p[:remain])
		c.n = c.max
		return len(p), nil
	}
	n, err := c.w.Write(p)
	c.n += n
	return n, err
}

// shellEscape produces a single-quoted string safe to drop into an
// `sh -c` invocation. Single quotes inside the source are encoded by
// closing-quote → escaped-quote → reopening-quote, the canonical
// POSIX trick.
func shellEscape(s string) string {
	if s == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// SandboxStatusReport mirrors the e2b /sandboxes shape: a snapshot
// of the live set plus aggregate counters. JSON-friendly for the
// HTTP/MCP/CLI surfaces.
type SandboxStatusReport struct {
	Available bool             `json:"available"`
	Image     string           `json:"image"`
	Sessions  []SandboxSession `json:"sessions"`
	Count     int              `json:"count"`
}

// Snapshot returns the current state. Owner filter trims per-caller.
func (m *SandboxManager) Snapshot(requireOwner string) SandboxStatusReport {
	rep := SandboxStatusReport{Image: sandboxImage}
	if m == nil {
		return rep
	}
	rep.Available = m.runner.IsAvailable() && m.runner.IsImageReady()
	rep.Sessions = m.List(requireOwner)
	rep.Count = len(rep.Sessions)
	return rep
}

// PersistDir is the on-disk location where sandbox metadata could be
// snapshotted in a future phase. Phase 2 keeps everything in memory
// — sandbox lifetime is bounded to the agent process anyway, so a
// restart-survival guarantee buys nothing. The function is here so
// the HTTP handler can refer to a stable path when documenting the
// behaviour.
func SandboxPersistDir() string {
	if dir, err := ConfigDir(); err == nil {
		return filepath.Join(dir, "runner", "sandboxes")
	}
	return ""
}

// _ keeps the json + os imports tied to a real reference even if a
// future trim drops their direct uses elsewhere in the file.
var (
	_ = json.Marshal
	_ = os.Remove
)
