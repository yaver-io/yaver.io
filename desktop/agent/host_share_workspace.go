package main

import (
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type HostShareWorkspace struct {
	SessionID     string `json:"sessionId"`
	RootDir       string `json:"rootDir"`
	RepoDir       string `json:"repoDir"`
	State         string `json:"state"`
	SourceDir     string `json:"sourceDir,omitempty"`
	GuestDeviceID string `json:"guestDeviceId,omitempty"`
	GuestRootID   string `json:"guestRootId,omitempty"`
	GuestRootPath string `json:"guestRootPath,omitempty"`
	CreatedAt     string `json:"createdAt"`
	UpdatedAt     string `json:"updatedAt"`
	FileCount     int    `json:"fileCount,omitempty"`
	DirCount      int    `json:"dirCount,omitempty"`
	LastError     string `json:"lastError,omitempty"`
}

type HostShareWorkspaceManager struct {
	baseDir string
	mu      sync.Mutex
}

func NewHostShareWorkspaceManager() (*HostShareWorkspaceManager, error) {
	cfgDir, err := ConfigDir()
	if err != nil {
		return nil, err
	}
	baseDir := filepath.Join(cfgDir, "host-share", "workspaces")
	if err := os.MkdirAll(baseDir, 0700); err != nil {
		return nil, fmt.Errorf("create host-share workspaces dir: %w", err)
	}
	return &HostShareWorkspaceManager{baseDir: baseDir}, nil
}

func sanitizeHostShareSessionID(sessionID string) string {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return ""
	}
	replacer := strings.NewReplacer("/", "_", "\\", "_", "..", "_", " ", "_", ":", "_")
	return replacer.Replace(sessionID)
}

func (m *HostShareWorkspaceManager) workspacePaths(sessionID string) (rootDir, repoDir, metaPath string, err error) {
	if m == nil {
		return "", "", "", fmt.Errorf("workspace manager unavailable")
	}
	safe := sanitizeHostShareSessionID(sessionID)
	if safe == "" {
		return "", "", "", fmt.Errorf("session id required")
	}
	rootDir = filepath.Join(m.baseDir, safe)
	repoDir = filepath.Join(rootDir, "repo")
	metaPath = filepath.Join(rootDir, "workspace.json")
	return rootDir, repoDir, metaPath, nil
}

func (m *HostShareWorkspaceManager) loadLocked(sessionID string) (*HostShareWorkspace, string, string, string, error) {
	rootDir, repoDir, metaPath, err := m.workspacePaths(sessionID)
	if err != nil {
		return nil, "", "", "", err
	}
	data, err := os.ReadFile(metaPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, rootDir, repoDir, metaPath, nil
		}
		return nil, "", "", "", fmt.Errorf("read workspace metadata: %w", err)
	}
	var ws HostShareWorkspace
	if err := json.Unmarshal(data, &ws); err != nil {
		return nil, "", "", "", fmt.Errorf("parse workspace metadata: %w", err)
	}
	return &ws, rootDir, repoDir, metaPath, nil
}

func (m *HostShareWorkspaceManager) saveLocked(ws *HostShareWorkspace, metaPath string) error {
	if ws == nil {
		return fmt.Errorf("workspace metadata required")
	}
	data, err := json.MarshalIndent(ws, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal workspace metadata: %w", err)
	}
	if err := os.WriteFile(metaPath, data, 0600); err != nil {
		return fmt.Errorf("write workspace metadata: %w", err)
	}
	return nil
}

func (m *HostShareWorkspaceManager) EnsureWorkspace(sessionID string) (*HostShareWorkspace, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	ws, rootDir, repoDir, metaPath, err := m.loadLocked(sessionID)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	if ws == nil {
		if err := os.MkdirAll(repoDir, 0700); err != nil {
			return nil, fmt.Errorf("create workspace repo dir: %w", err)
		}
		ws = &HostShareWorkspace{
			SessionID: sessionID,
			RootDir:   rootDir,
			RepoDir:   repoDir,
			State:     "empty",
			CreatedAt: now,
			UpdatedAt: now,
		}
		if err := m.saveLocked(ws, metaPath); err != nil {
			return nil, err
		}
		return ws, nil
	}
	if err := os.MkdirAll(ws.RepoDir, 0700); err != nil {
		return nil, fmt.Errorf("ensure workspace repo dir: %w", err)
	}
	return ws, nil
}

// DeleteWorkspace removes a tenant's entire workspace tree (repo + metadata)
// for the given session. Idempotent: a missing dir is success. This is the
// "removable allocation" wipe — on session end/revoke/expiry the tenant's
// code and data must leave the operator's disk, both for the tenant's
// privacy and to prevent cross-tenant residue when the box is reused.
func (m *HostShareWorkspaceManager) DeleteWorkspace(sessionID string) error {
	if m == nil {
		return fmt.Errorf("workspace manager unavailable")
	}
	rootDir, _, _, err := m.workspacePaths(sessionID)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := os.RemoveAll(rootDir); err != nil {
		return fmt.Errorf("remove workspace %s: %w", sessionID, err)
	}
	return nil
}

// listSessionDirsLocked returns the sanitized session-dir names that
// currently exist under the workspaces base dir. Caller holds m.mu.
func (m *HostShareWorkspaceManager) listSessionDirsLocked() ([]string, error) {
	entries, err := os.ReadDir(m.baseDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	return names, nil
}

// ReapExcept deletes every on-disk workspace whose sanitized session id is
// NOT in keepSanitized. Returns the sanitized dir names removed. Used by the
// host-share reaper to scrub workspaces of sessions that have ended/been
// revoked while the agent wasn't holding a live terminal for them.
func (m *HostShareWorkspaceManager) ReapExcept(keepSanitized map[string]bool) ([]string, error) {
	if m == nil {
		return nil, nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	names, err := m.listSessionDirsLocked()
	if err != nil {
		return nil, err
	}
	var removed []string
	for _, name := range names {
		if keepSanitized[name] {
			continue
		}
		if err := os.RemoveAll(filepath.Join(m.baseDir, name)); err != nil {
			return removed, fmt.Errorf("reap workspace %s: %w", name, err)
		}
		removed = append(removed, name)
	}
	return removed, nil
}

// SanitizeSessionID exposes the same dir-name normalization used internally,
// so callers (the reaper) can build a keep-set keyed by dir name.
func (m *HostShareWorkspaceManager) SanitizeSessionID(sessionID string) string {
	return sanitizeHostShareSessionID(sessionID)
}

func (m *HostShareWorkspaceManager) GetWorkspace(sessionID string) (*HostShareWorkspace, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	ws, _, _, _, err := m.loadLocked(sessionID)
	if err != nil {
		return nil, err
	}
	if ws == nil {
		return nil, nil
	}
	return ws, nil
}

func copyFileContents(src, dst string, mode fs.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Close()
}

func cleanWorkspaceRepoDir(repoDir string) error {
	entries, err := os.ReadDir(repoDir)
	if err != nil {
		if os.IsNotExist(err) {
			return os.MkdirAll(repoDir, 0700)
		}
		return err
	}
	for _, entry := range entries {
		if err := os.RemoveAll(filepath.Join(repoDir, entry.Name())); err != nil {
			return err
		}
	}
	return nil
}

func countWorkspaceEntries(root string) (files, dirs int, err error) {
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == root {
			return nil
		}
		if d.IsDir() {
			dirs++
			return nil
		}
		files++
		return nil
	})
	return
}

func (m *HostShareWorkspaceManager) BootstrapFromDir(sessionID, sourceDir string) (*HostShareWorkspace, error) {
	sourceDir = strings.TrimSpace(sourceDir)
	if sourceDir == "" {
		return nil, fmt.Errorf("source directory required")
	}
	absSource, err := filepath.Abs(sourceDir)
	if err != nil {
		return nil, fmt.Errorf("resolve source directory: %w", err)
	}
	info, err := os.Stat(absSource)
	if err != nil {
		return nil, fmt.Errorf("stat source directory: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("source path is not a directory")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	ws, rootDir, repoDir, metaPath, err := m.loadLocked(sessionID)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	if ws == nil {
		if err := os.MkdirAll(repoDir, 0700); err != nil {
			return nil, fmt.Errorf("create workspace repo dir: %w", err)
		}
		ws = &HostShareWorkspace{
			SessionID: sessionID,
			RootDir:   rootDir,
			RepoDir:   repoDir,
			CreatedAt: now,
		}
	}
	if err := cleanWorkspaceRepoDir(repoDir); err != nil {
		return nil, fmt.Errorf("clean workspace repo dir: %w", err)
	}
	files := 0
	dirs := 0
	err = filepath.WalkDir(absSource, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(absSource, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		if strings.HasPrefix(rel, ".yaver") {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		target := filepath.Join(repoDir, rel)
		cleanTarget := filepath.Clean(target)
		if cleanTarget != target && !strings.HasPrefix(cleanTarget, repoDir) {
			return fmt.Errorf("unsafe target path: %s", rel)
		}
		if d.IsDir() {
			dirs++
			return os.MkdirAll(target, 0700)
		}
		if d.Type()&os.ModeSymlink != 0 {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
			return err
		}
		files++
		mode := info.Mode().Perm()
		if mode == 0 {
			mode = 0600
		}
		return copyFileContents(path, target, mode)
	})
	if err != nil {
		ws.State = "error"
		ws.LastError = err.Error()
		ws.UpdatedAt = now
		_ = m.saveLocked(ws, metaPath)
		return nil, err
	}
	ws.State = "ready"
	ws.SourceDir = absSource
	ws.FileCount = files
	ws.DirCount = dirs
	ws.LastError = ""
	ws.UpdatedAt = now
	if ws.CreatedAt == "" {
		ws.CreatedAt = now
	}
	if err := m.saveLocked(ws, metaPath); err != nil {
		return nil, err
	}
	return ws, nil
}

func (m *HostShareWorkspaceManager) RefreshCounts(sessionID string) (*HostShareWorkspace, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	ws, _, _, metaPath, err := m.loadLocked(sessionID)
	if err != nil {
		return nil, err
	}
	if ws == nil {
		return nil, nil
	}
	files, dirs, err := countWorkspaceEntries(ws.RepoDir)
	if err != nil {
		return nil, err
	}
	ws.FileCount = files
	ws.DirCount = dirs
	ws.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	if err := m.saveLocked(ws, metaPath); err != nil {
		return nil, err
	}
	return ws, nil
}

func (m *HostShareWorkspaceManager) BindGuestRoot(sessionID, guestDeviceID, guestRootID, guestRootPath string) (*HostShareWorkspace, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	ws, _, _, metaPath, err := m.loadLocked(sessionID)
	if err != nil {
		return nil, err
	}
	if ws == nil {
		return nil, fmt.Errorf("workspace not found")
	}
	ws.GuestDeviceID = strings.TrimSpace(guestDeviceID)
	ws.GuestRootID = strings.TrimSpace(guestRootID)
	ws.GuestRootPath = strings.TrimSpace(guestRootPath)
	ws.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	if err := m.saveLocked(ws, metaPath); err != nil {
		return nil, err
	}
	return ws, nil
}
