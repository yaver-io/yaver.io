package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/google/uuid"
)

const projectSessionCommandTimeout = 2 * time.Minute

// GitRepository is the path-free repository descriptor returned to clients.
// Repository paths are resolved only inside the trusted runner.
type GitRepository struct {
	RepositoryID string `json:"repositoryId"`
	Name         string `json:"name"`
	DefaultRef   string `json:"defaultRef,omitempty"`
}

// ProjectSession owns one isolated checkout and review branch.
type ProjectSession struct {
	ProjectSessionID string    `json:"projectSessionId"`
	RepositoryID     string    `json:"repositoryId"`
	RepositoryName   string    `json:"repositoryName"`
	BaseRef          string    `json:"baseRef"`
	ReviewBranch     string    `json:"reviewBranch"`
	Status           string    `json:"status"`
	CreatedAt        time.Time `json:"createdAt"`
	UpdatedAt        time.Time `json:"updatedAt"`
	WorkDir          string    `json:"-"`
	RepositoryDir    string    `json:"-"`
}

type persistedProjectSession struct {
	ProjectSession
	WorkDir       string `json:"workDir"`
	RepositoryDir string `json:"repositoryDir,omitempty"`
}

type ProjectSessionManager struct {
	mu           sync.RWMutex
	stateDir     string
	worktreeRoot string
	registry     string
	sessions     map[string]*ProjectSession
}

func NewProjectSessionManager() (*ProjectSessionManager, error) {
	configDir, err := ConfigDir()
	if err != nil {
		return nil, err
	}
	stateDir := filepath.Join(configDir, "project-sessions")
	if err := os.MkdirAll(stateDir, 0700); err != nil {
		return nil, fmt.Errorf("create project sessions directory: %w", err)
	}
	managedWorktrees, err := DefaultWorkspaceWorktreesDir()
	if err != nil {
		return nil, err
	}
	worktreeRoot := filepath.Join(managedWorktrees, "project-sessions")
	if err := os.MkdirAll(worktreeRoot, 0700); err != nil {
		return nil, fmt.Errorf("create project worktree directory: %w", err)
	}
	m := &ProjectSessionManager{
		stateDir:     stateDir,
		worktreeRoot: worktreeRoot,
		registry:     filepath.Join(stateDir, "sessions.json"),
		sessions:     make(map[string]*ProjectSession),
	}
	if err := m.load(); err != nil {
		return nil, err
	}
	return m, nil
}

func repositoryID(path string) string {
	digest := sha256.Sum256([]byte("yaver-repository-v1\x00" + filepath.Clean(path)))
	return "repo_" + hex.EncodeToString(digest[:12])
}

func repositoryCatalog() (map[string]projectInfo, error) {
	projects := listDiscoveredProjects()
	catalog := make(map[string]projectInfo, len(projects))
	for _, project := range projects {
		path := filepath.Clean(project.Path)
		if _, statErr := os.Stat(filepath.Join(path, ".git")); statErr != nil {
			continue
		}
		project.Path = path
		catalog[repositoryID(path)] = project
	}
	return catalog, nil
}

func ListGitRepositories(refresh bool) ([]GitRepository, error) {
	if refresh {
		discoverProjects()
	}
	catalog, err := repositoryCatalog()
	if err != nil {
		return nil, err
	}
	repositories := make([]GitRepository, 0, len(catalog))
	for id, project := range catalog {
		repositories = append(repositories, GitRepository{
			RepositoryID: id,
			Name:         filepath.Base(project.Path),
			DefaultRef:   project.Branch,
		})
	}
	sort.Slice(repositories, func(i, j int) bool {
		if repositories[i].Name == repositories[j].Name {
			return repositories[i].RepositoryID < repositories[j].RepositoryID
		}
		return repositories[i].Name < repositories[j].Name
	})
	return repositories, nil
}

func validGitRef(ref string) bool {
	if ref == "" || strings.HasPrefix(ref, "-") || strings.HasSuffix(ref, ".") || strings.HasSuffix(ref, "/") {
		return false
	}
	if strings.Contains(ref, "..") || strings.Contains(ref, "//") || strings.Contains(ref, "@{") || strings.ContainsAny(ref, "~^:?*[\\") {
		return false
	}
	for _, r := range ref {
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			return false
		}
	}
	return true
}

func runProjectSessionGit(ctx context.Context, workDir string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = workDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		message := strings.TrimSpace(string(out))
		if message == "" {
			message = err.Error()
		}
		return nil, fmt.Errorf("git %s: %s", args[0], message)
	}
	return out, nil
}

// convergeManagedRepository keeps only Yaver-chosen repository destinations
// pristine and current. A repo in a user-selected custom hierarchy is never
// pulled or switched implicitly.
func convergeManagedRepository(ctx context.Context, projectPath string) error {
	reposRoot, err := DefaultWorkspaceReposDir()
	if err != nil || !isPathWithinRoot(projectPath, reposRoot) {
		return err
	}
	status, err := runProjectSessionGit(ctx, projectPath, "status", "--porcelain")
	if err != nil {
		return err
	}
	if strings.TrimSpace(string(status)) != "" {
		return fmt.Errorf("managed repository is not pristine; move development work into Workspace/worktrees before opening a new session")
	}
	branchOut, err := runProjectSessionGit(ctx, projectPath, "branch", "--show-current")
	if err != nil {
		return err
	}
	branch := strings.TrimSpace(string(branchOut))
	if branch == "" {
		return fmt.Errorf("managed repository is detached; restore its default branch before opening a development worktree")
	}
	if _, err := runProjectSessionGit(ctx, projectPath, "fetch", "--prune", "origin"); err != nil {
		return fmt.Errorf("refresh managed repository: %w", err)
	}
	remoteRef := "origin/" + branch
	if _, mainErr := runProjectSessionGit(ctx, projectPath, "rev-parse", "--verify", "refs/remotes/origin/main"); mainErr == nil {
		if branch != "main" {
			return fmt.Errorf("managed repository is on %s; keep Workspace/repos on main and open that branch under Workspace/worktrees", branch)
		}
		remoteRef = "origin/main"
	}
	counts, err := runProjectSessionGit(ctx, projectPath, "rev-list", "--left-right", "--count", "HEAD..."+remoteRef)
	if err != nil {
		return fmt.Errorf("compare managed repository with %s: %w", remoteRef, err)
	}
	fields := strings.Fields(string(counts))
	if len(fields) != 2 {
		return fmt.Errorf("compare managed repository with %s: unexpected result", remoteRef)
	}
	if fields[0] != "0" {
		return fmt.Errorf("managed repository has %s local commit(s) not on %s; land or preserve them in a development worktree first", fields[0], remoteRef)
	}
	if fields[1] != "0" {
		if _, err := runProjectSessionGit(ctx, projectPath, "merge", "--ff-only", remoteRef); err != nil {
			return fmt.Errorf("fast-forward managed repository to %s: %w", remoteRef, err)
		}
	}
	return nil
}

func (m *ProjectSessionManager) Create(repositoryIDValue, baseRef string) (*ProjectSession, error) {
	catalog, err := repositoryCatalog()
	if err != nil {
		return nil, err
	}
	project, ok := catalog[repositoryIDValue]
	if !ok {
		return nil, fmt.Errorf("repository is not available to this runner")
	}
	baseRef = strings.TrimSpace(baseRef)
	if baseRef == "" {
		baseRef = strings.TrimSpace(project.Branch)
	}
	if baseRef == "" {
		baseRef = "HEAD"
	}
	if !validGitRef(baseRef) {
		return nil, fmt.Errorf("invalid base ref")
	}
	ctx, cancel := context.WithTimeout(context.Background(), projectSessionCommandTimeout)
	defer cancel()
	if err := convergeManagedRepository(ctx, project.Path); err != nil {
		return nil, err
	}

	id := "ps_" + strings.ReplaceAll(uuid.New().String(), "-", "")[:20]
	reviewBranch := "yaver/cloud-" + strings.TrimPrefix(id, "ps_")
	checkoutDir := filepath.Join(m.worktreeRoot, sanitizeBranchName(filepath.Base(project.Path))+"-"+strings.TrimPrefix(id, "ps_"))
	cleanup := true
	defer func() {
		if cleanup {
			_, _ = runProjectSessionGit(context.Background(), project.Path, "worktree", "remove", "--force", checkoutDir)
			if isPathWithinRoot(checkoutDir, m.worktreeRoot) {
				_ = os.RemoveAll(checkoutDir)
			}
		}
	}()

	if _, err := runProjectSessionGit(ctx, project.Path, "worktree", "add", "-b", reviewBranch, checkoutDir, baseRef); err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	session := &ProjectSession{
		ProjectSessionID: id,
		RepositoryID:     repositoryIDValue,
		RepositoryName:   filepath.Base(project.Path),
		BaseRef:          baseRef,
		ReviewBranch:     reviewBranch,
		Status:           "ready",
		CreatedAt:        now,
		UpdatedAt:        now,
		WorkDir:          checkoutDir,
		RepositoryDir:    project.Path,
	}
	m.mu.Lock()
	m.sessions[id] = session
	if err := m.persistLocked(); err != nil {
		delete(m.sessions, id)
		m.mu.Unlock()
		return nil, err
	}
	m.mu.Unlock()
	cleanup = false
	copy := *session
	return &copy, nil
}

func (m *ProjectSessionManager) List() []ProjectSession {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make([]ProjectSession, 0, len(m.sessions))
	for _, session := range m.sessions {
		result = append(result, *session)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].CreatedAt.After(result[j].CreatedAt) })
	return result
}

func (m *ProjectSessionManager) Get(id string) (*ProjectSession, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	session, ok := m.sessions[id]
	if !ok {
		return nil, false
	}
	copy := *session
	return &copy, true
}

func (m *ProjectSessionManager) Stop(id string) (*ProjectSession, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	session, ok := m.sessions[id]
	if !ok {
		return nil, fmt.Errorf("project session not found")
	}
	session.Status = "stopped"
	session.UpdatedAt = time.Now().UTC()
	if err := m.persistLocked(); err != nil {
		return nil, err
	}
	copy := *session
	return &copy, nil
}

func (m *ProjectSessionManager) Delete(id string) (*ProjectSession, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	session, ok := m.sessions[id]
	if !ok {
		return nil, fmt.Errorf("project session not found")
	}
	copy := *session
	copy.Status = "stopped"
	copy.UpdatedAt = time.Now().UTC()
	isManagedTree := isPathWithinRoot(session.WorkDir, m.worktreeRoot)
	isLegacySession := isPathWithinRoot(session.WorkDir, m.stateDir)
	if !isManagedTree && !isLegacySession {
		return nil, fmt.Errorf("project session cleanup boundary mismatch")
	}
	ctx, cancel := context.WithTimeout(context.Background(), projectSessionCommandTimeout)
	defer cancel()
	if isManagedTree && session.RepositoryDir != "" {
		status, err := runProjectSessionGit(ctx, session.WorkDir, "status", "--porcelain")
		if err != nil {
			return nil, err
		}
		if strings.TrimSpace(string(status)) != "" {
			return nil, fmt.Errorf("project session worktree has uncommitted changes; commit them before deleting the session")
		}
		if _, err := runProjectSessionGit(ctx, session.RepositoryDir, "worktree", "remove", session.WorkDir); err != nil {
			return nil, fmt.Errorf("remove project session worktree: %w", err)
		}
	} else {
		legacyDir := filepath.Dir(session.WorkDir)
		if filepath.Clean(legacyDir) != filepath.Join(m.stateDir, id) {
			return nil, fmt.Errorf("legacy project session cleanup boundary mismatch")
		}
		if err := os.RemoveAll(legacyDir); err != nil {
			return nil, fmt.Errorf("remove legacy project session checkout: %w", err)
		}
	}
	delete(m.sessions, id)
	if err := m.persistLocked(); err != nil {
		return nil, err
	}
	return &copy, nil
}

func (m *ProjectSessionManager) GitStatus(id string) (map[string]string, error) {
	session, ok := m.Get(id)
	if !ok {
		return nil, fmt.Errorf("project session not found")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	status, err := runProjectSessionGit(ctx, session.WorkDir, "status", "--short", "--branch")
	if err != nil {
		return nil, err
	}
	return map[string]string{"branch": session.ReviewBranch, "status": string(status)}, nil
}

func (m *ProjectSessionManager) GitDiff(id string) (string, error) {
	session, ok := m.Get(id)
	if !ok {
		return "", fmt.Errorf("project session not found")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	out, err := runProjectSessionGit(ctx, session.WorkDir, "diff", "--no-ext-diff", "--no-color")
	if err != nil {
		return "", err
	}
	const maxDiffBytes = 1024 * 1024
	if len(out) > maxDiffBytes {
		return string(out[:maxDiffBytes]) + "\n\n[diff truncated]", nil
	}
	return string(out), nil
}

func (m *ProjectSessionManager) GitCommit(id, message string) (string, error) {
	session, ok := m.Get(id)
	if !ok {
		return "", fmt.Errorf("project session not found")
	}
	message = strings.TrimSpace(message)
	if message == "" || len(message) > 500 {
		return "", fmt.Errorf("commit message must be between 1 and 500 characters")
	}
	ctx, cancel := context.WithTimeout(context.Background(), projectSessionCommandTimeout)
	defer cancel()
	if _, err := runProjectSessionGit(ctx, session.WorkDir, "add", "--all"); err != nil {
		return "", err
	}
	if _, err := runProjectSessionGit(ctx, session.WorkDir, "commit", "-m", message); err != nil {
		return "", err
	}
	out, err := runProjectSessionGit(ctx, session.WorkDir, "rev-parse", "HEAD")
	return strings.TrimSpace(string(out)), err
}

func (m *ProjectSessionManager) PushReview(id string) (string, error) {
	session, ok := m.Get(id)
	if !ok {
		return "", fmt.Errorf("project session not found")
	}
	ctx, cancel := context.WithTimeout(context.Background(), projectSessionCommandTimeout)
	defer cancel()
	branchOut, err := runProjectSessionGit(ctx, session.WorkDir, "branch", "--show-current")
	if err != nil || strings.TrimSpace(string(branchOut)) != session.ReviewBranch {
		return "", fmt.Errorf("review branch policy violation")
	}
	remoteOut, err := runProjectSessionGit(ctx, session.WorkDir, "remote", "get-url", "origin")
	if err != nil {
		return "", err
	}
	remote := strings.TrimSpace(string(remoteOut))
	if !isNetworkGitRemote(remote) {
		return "", fmt.Errorf("review push requires an HTTPS or SSH Git remote")
	}
	if _, err := runProjectSessionGit(ctx, session.WorkDir, "push", "--set-upstream", "origin", session.ReviewBranch); err != nil {
		return "", err
	}
	return session.ReviewBranch, nil
}

func isNetworkGitRemote(remote string) bool {
	return strings.HasPrefix(remote, "https://") || strings.HasPrefix(remote, "ssh://") || strings.HasPrefix(remote, "git@")
}

func (m *ProjectSessionManager) load() error {
	data, err := os.ReadFile(m.registry)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read project session registry: %w", err)
	}
	var stored []persistedProjectSession
	if err := json.Unmarshal(data, &stored); err != nil {
		return fmt.Errorf("parse project session registry: %w", err)
	}
	for _, item := range stored {
		workDir := filepath.Clean(item.WorkDir)
		if !isPathWithinRoot(workDir, m.worktreeRoot) && !isPathWithinRoot(workDir, m.stateDir) {
			continue
		}
		copy := item.ProjectSession
		copy.WorkDir = workDir
		copy.RepositoryDir = filepath.Clean(item.RepositoryDir)
		if item.RepositoryDir == "" {
			copy.RepositoryDir = ""
		}
		if _, statErr := os.Stat(workDir); statErr != nil && copy.Status == "ready" {
			copy.Status = "error"
		}
		m.sessions[copy.ProjectSessionID] = &copy
	}
	return nil
}

func (m *ProjectSessionManager) persistLocked() error {
	stored := make([]persistedProjectSession, 0, len(m.sessions))
	for _, session := range m.sessions {
		stored = append(stored, persistedProjectSession{ProjectSession: *session, WorkDir: session.WorkDir, RepositoryDir: session.RepositoryDir})
	}
	sort.Slice(stored, func(i, j int) bool { return stored[i].CreatedAt.Before(stored[j].CreatedAt) })
	data, err := json.MarshalIndent(stored, "", "  ")
	if err != nil {
		return fmt.Errorf("encode project session registry: %w", err)
	}
	temp := m.registry + ".tmp"
	if err := os.WriteFile(temp, data, 0600); err != nil {
		return fmt.Errorf("write project session registry: %w", err)
	}
	if err := os.Rename(temp, m.registry); err != nil {
		return fmt.Errorf("replace project session registry: %w", err)
	}
	return nil
}
