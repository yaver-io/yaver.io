package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// UserSession represents an isolated user environment on a shared machine.
// Each user who authenticates against the shared agent gets their own:
// - Workspace directory (for code)
// - Task manager (their own task queue)
// - Feedback manager (their own reports)
// - Black box manager (their own device streams)
// - Tmux session namespace (isolated terminal sessions)
type UserSession struct {
	UserID       string    `json:"userId"`
	Email        string    `json:"email"`
	FullName     string    `json:"fullName,omitempty"`
	Provider     string    `json:"provider,omitempty"` // apple, google, microsoft
	WorkspaceDir string    `json:"workspaceDir"`       // /home/yaver-{short}/workspace
	HomeDir      string    `json:"homeDir"`            // /home/yaver-{short}
	CreatedAt    time.Time `json:"createdAt"`
	LastActiveAt time.Time `json:"lastActiveAt"`

	// Per-user managers (not serialized)
	taskMgr     *TaskManager
	feedbackMgr *FeedbackManager
	blackboxMgr *BlackBoxManager
	// devServerMgr is allocated lazily on the first /dev/start so an
	// idle user doesn't spawn a manager. Each user's manager owns its
	// own port slot from the shared DevPortAllocator.
	devServerMgr *DevServerManager
	devPorts     DevPortPair
}

// MultiUserManager orchestrates multiple user sessions on a shared machine.
// It replaces the single-owner model with a registry of authenticated users,
// each getting isolated workspace and session resources.
//
// Auth flow for team members:
//  1. Team admin purchases a GPU/CPU machine on yaver.io
//  2. Machine provisions with `yaver serve --multi-user --team <teamId>`
//  3. Team member opens Yaver app → sees the shared machine in their device list
//  4. Member taps "Connect" → their token is validated against Convex
//  5. Convex confirms: this user belongs to teamId → access granted
//  6. MultiUserManager creates an isolated UserSession for this user
//  7. All subsequent requests from this user route to their own session
type MultiUserManager struct {
	mu       sync.RWMutex
	users    map[string]*UserSession // userId → session
	baseDir  string                  // /var/yaver/users/ or ~/.yaver/users/
	teamID   string                  // Team ID for access control (empty = any authenticated user)
	maxUsers int                     // Max concurrent users (0 = unlimited)

	// Shared resources (GPU services accessible by all users)
	sharedOllamaURL       string // http://localhost:11434
	sharedPersonaPlexURL  string // http://localhost:8765

	// portAlloc hands out unique (Metro, ExpoWeb) port pairs to user
	// sessions so they don't collide on the canonical 8081 / 19006.
	portAlloc *DevPortAllocator
}

// MultiUserConfig holds configuration for multi-user mode.
type MultiUserConfig struct {
	BaseDir  string // Where user home dirs are created
	TeamID   string // Restrict to team members (empty = open)
	MaxUsers int    // Max concurrent users
}

// NewMultiUserManager creates a new multi-user manager.
func NewMultiUserManager(cfg MultiUserConfig) (*MultiUserManager, error) {
	baseDir := cfg.BaseDir
	if baseDir == "" {
		baseDir = "/var/yaver/users"
		// Fallback to home dir if /var/yaver doesn't exist
		if _, err := os.Stat("/var/yaver"); os.IsNotExist(err) {
			home, _ := os.UserHomeDir()
			baseDir = filepath.Join(home, ".yaver", "users")
		}
	}

	if err := os.MkdirAll(baseDir, 0755); err != nil {
		return nil, fmt.Errorf("create users dir: %w", err)
	}

	mgr := &MultiUserManager{
		users:     make(map[string]*UserSession),
		baseDir:   baseDir,
		teamID:    cfg.TeamID,
		maxUsers:  cfg.MaxUsers,
		portAlloc: NewDevPortAllocator(),
	}

	// Load existing user sessions from disk
	mgr.loadExisting()

	return mgr, nil
}

// loadExisting scans the users directory for existing sessions.
func (m *MultiUserManager) loadExisting() {
	entries, err := os.ReadDir(m.baseDir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		metaPath := filepath.Join(m.baseDir, e.Name(), ".yaver-user.json")
		data, err := os.ReadFile(metaPath)
		if err != nil {
			continue
		}
		var session UserSession
		if err := json.Unmarshal(data, &session); err != nil {
			continue
		}
		m.users[session.UserID] = &session
		log.Printf("[multiuser] Loaded existing session for %s (%s)", session.Email, session.UserID[:8])
	}
}

// GetOrCreateSession returns (or creates) an isolated session for a user.
// This is called after auth validation confirms the user is allowed.
func (m *MultiUserManager) GetOrCreateSession(userID, email, fullName, provider string) (*UserSession, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Return existing session
	if session, ok := m.users[userID]; ok {
		session.LastActiveAt = time.Now()
		return session, nil
	}

	// Check max users
	if m.maxUsers > 0 && len(m.users) >= m.maxUsers {
		return nil, fmt.Errorf("max users reached (%d)", m.maxUsers)
	}

	// Create new user session
	shortID := userID
	if len(shortID) > 8 {
		shortID = shortID[:8]
	}

	homeDir := filepath.Join(m.baseDir, "yaver-"+shortID)
	workspaceDir := filepath.Join(homeDir, "workspace")

	// Create directory structure
	for _, dir := range []string{
		homeDir,
		workspaceDir,
		filepath.Join(homeDir, ".yaver"),
		filepath.Join(homeDir, ".yaver", "tasks"),
		filepath.Join(homeDir, ".yaver", "feedback"),
		filepath.Join(homeDir, ".yaver", "sessions"),
		filepath.Join(homeDir, ".yaver", "blackbox"),
	} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			return nil, fmt.Errorf("create dir %s: %w", dir, err)
		}
	}

	session := &UserSession{
		UserID:       userID,
		Email:        email,
		FullName:     fullName,
		Provider:     provider,
		WorkspaceDir: workspaceDir,
		HomeDir:      homeDir,
		CreatedAt:    time.Now(),
		LastActiveAt: time.Now(),
	}

	// Persist user metadata
	meta, _ := json.MarshalIndent(session, "", "  ")
	os.WriteFile(filepath.Join(homeDir, ".yaver-user.json"), meta, 0600)

	m.users[userID] = session
	log.Printf("[multiuser] Created new session for %s (%s) at %s", email, shortID, homeDir)

	return session, nil
}

// GetSession returns an existing session, or nil.
func (m *MultiUserManager) GetSession(userID string) *UserSession {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.users[userID]
}

// EnsureDevServerMgr lazily attaches a DevServerManager + a reserved
// port pair to the user's session. Idempotent — returns the existing
// manager on repeat calls.
func (m *MultiUserManager) EnsureDevServerMgr(userID string) (*DevServerManager, DevPortPair, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	session, ok := m.users[userID]
	if !ok {
		return nil, DevPortPair{}, fmt.Errorf("no session for user %s", userID)
	}
	if session.devServerMgr != nil {
		return session.devServerMgr, session.devPorts, nil
	}
	pair, err := m.portAlloc.Reserve(userID)
	if err != nil {
		return nil, DevPortPair{}, err
	}
	session.devServerMgr = NewDevServerManager()
	session.devPorts = pair
	log.Printf("[multiuser] DevServerManager allocated for %s (slot=%d, metro=%d, web=%d)",
		userID[:min(8, len(userID))], pair.Slot, pair.MetroPort, pair.WebPort)
	return session.devServerMgr, pair, nil
}

// ReleaseDevServerMgr stops + drops a user's dev server. Called on
// session removal so port slots get freed for the next user.
func (m *MultiUserManager) ReleaseDevServerMgr(userID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	session, ok := m.users[userID]
	if !ok || session.devServerMgr == nil {
		return
	}
	_ = session.devServerMgr.Stop()
	session.devServerMgr = nil
	m.portAlloc.Release(userID)
}

// ListUsers returns info about all active user sessions.
func (m *MultiUserManager) ListUsers() []map[string]interface{} {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make([]map[string]interface{}, 0, len(m.users))
	for _, s := range m.users {
		result = append(result, map[string]interface{}{
			"userId":       s.UserID,
			"email":        s.Email,
			"fullName":     s.FullName,
			"provider":     s.Provider,
			"workspaceDir": s.WorkspaceDir,
			"createdAt":    s.CreatedAt.Format(time.RFC3339),
			"lastActiveAt": s.LastActiveAt.Format(time.RFC3339),
		})
	}
	return result
}

// RemoveUser removes a user session and optionally their data.
func (m *MultiUserManager) RemoveUser(userID string, deleteData bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	session, ok := m.users[userID]
	if !ok {
		return fmt.Errorf("user %s not found", userID)
	}

	delete(m.users, userID)

	if deleteData {
		log.Printf("[multiuser] Removing data for %s at %s", session.Email, session.HomeDir)
		return os.RemoveAll(session.HomeDir)
	}

	return nil
}

// IsTeamMember checks if a user belongs to the configured team.
// If no team is configured, all authenticated users are allowed.
func (m *MultiUserManager) IsTeamMember(userID string) bool {
	if m.teamID == "" {
		return true // No team restriction
	}
	// Team membership is checked via Convex during auth validation.
	// The auth middleware passes teamId from the token validation response.
	// This method is a local cache check after the initial Convex validation.
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, exists := m.users[userID]
	return exists
}
