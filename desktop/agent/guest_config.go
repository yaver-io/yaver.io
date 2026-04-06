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

// GuestConfigManager caches guest configs from Convex and manages
// local project access (P2P-only). It provides CheckAccess for
// the auth middleware to enforce limits before executing guest requests.
type GuestConfigManager struct {
	mu       sync.RWMutex
	configs  map[string]*GuestConfig // guestUserId -> config
	projects map[string][]string     // guestUserId -> allowed project paths

	// Daily usage tracked locally (flushed to Convex periodically)
	usageMu    sync.Mutex
	dailyUsage map[string]float64 // "guestUserId:YYYY-MM-DD" -> seconds
	dirty      map[string]bool    // keys with unflushed usage

	configDir string // path to store project access config
}

// NewGuestConfigManager creates a new guest config manager.
func NewGuestConfigManager(dataDir string) *GuestConfigManager {
	configDir := filepath.Join(dataDir, "guest-config")
	os.MkdirAll(configDir, 0700)
	mgr := &GuestConfigManager{
		configs:    make(map[string]*GuestConfig),
		projects:   make(map[string][]string),
		dailyUsage: make(map[string]float64),
		dirty:      make(map[string]bool),
		configDir:  configDir,
	}
	mgr.loadProjectAccess()
	return mgr
}

// AccessDeniedReason describes why a guest was denied access.
type AccessDeniedReason struct {
	Denied  bool   `json:"denied"`
	Reason  string `json:"reason,omitempty"`
}

// CheckAccess verifies whether a guest can execute a request right now.
// Returns nil if allowed, or an AccessDeniedReason if denied.
func (m *GuestConfigManager) CheckAccess(guestUserID string) *AccessDeniedReason {
	m.mu.RLock()
	cfg, ok := m.configs[guestUserID]
	m.mu.RUnlock()

	if !ok {
		// No config = use defaults (allowed)
		return nil
	}

	// Check usage mode
	mode := cfg.UsageMode
	if mode == "" {
		mode = "always" // default: always allowed
	}

	switch mode {
	case "always":
		// Always allowed
	case "idle-only":
		// TODO: check if any runner is active; for now allow
	case "scheduled":
		if cfg.Schedule != nil {
			now := time.Now()
			tz := cfg.Schedule.Timezone
			if tz != "" {
				if loc, err := time.LoadLocation(tz); err == nil {
					now = now.In(loc)
				}
			}
			hour := now.Hour()
			start := cfg.Schedule.StartHour
			end := cfg.Schedule.EndHour

			var allowed bool
			if start <= end {
				allowed = hour >= start && hour < end
			} else {
				// Wraps midnight: e.g. 22-06 means 22,23,0,1,2,3,4,5
				allowed = hour >= start || hour < end
			}
			if !allowed {
				return &AccessDeniedReason{
					Denied: true,
					Reason: fmt.Sprintf("guest access scheduled %02d:00-%02d:00 %s only", start, end, tz),
				}
			}
		}
	}

	// Check daily token limit
	if cfg.DailyTokenLimit != nil && *cfg.DailyTokenLimit > 0 {
		today := time.Now().Format("2006-01-02")
		key := guestUserID + ":" + today
		m.usageMu.Lock()
		used := m.dailyUsage[key]
		m.usageMu.Unlock()
		if used >= float64(*cfg.DailyTokenLimit) {
			return &AccessDeniedReason{
				Denied: true,
				Reason: fmt.Sprintf("daily limit reached (%.0f/%d seconds)", used, *cfg.DailyTokenLimit),
			}
		}
	}

	return nil
}

// CheckRunner verifies whether a guest can use a specific runner.
func (m *GuestConfigManager) CheckRunner(guestUserID, runnerID string) *AccessDeniedReason {
	m.mu.RLock()
	cfg, ok := m.configs[guestUserID]
	m.mu.RUnlock()

	if !ok || len(cfg.AllowedRunners) == 0 {
		return nil // no restriction
	}

	for _, r := range cfg.AllowedRunners {
		if r == runnerID {
			return nil
		}
	}

	return &AccessDeniedReason{
		Denied: true,
		Reason: fmt.Sprintf("runner %q not allowed for this guest", runnerID),
	}
}

// CheckProject verifies whether a guest can access a specific project path.
func (m *GuestConfigManager) CheckProject(guestUserID, projectPath string) *AccessDeniedReason {
	m.mu.RLock()
	paths, ok := m.projects[guestUserID]
	m.mu.RUnlock()

	if !ok || len(paths) == 0 {
		return nil // no restriction = all projects
	}

	for _, p := range paths {
		if p == projectPath {
			return nil
		}
	}

	return &AccessDeniedReason{
		Denied: true,
		Reason: "project not accessible for this guest",
	}
}

// RecordUsage records task-seconds consumed by a guest.
func (m *GuestConfigManager) RecordUsage(guestUserID string, seconds float64) {
	today := time.Now().Format("2006-01-02")
	key := guestUserID + ":" + today
	m.usageMu.Lock()
	m.dailyUsage[key] += seconds
	m.dirty[key] = true
	m.usageMu.Unlock()
}

// FlushUsage sends accumulated usage to Convex and clears the dirty set.
func (m *GuestConfigManager) FlushUsage(convexURL, token string) {
	m.usageMu.Lock()
	toFlush := make(map[string]float64)
	for k := range m.dirty {
		toFlush[k] = m.dailyUsage[k]
	}
	m.dirty = make(map[string]bool)
	m.usageMu.Unlock()

	for key, seconds := range toFlush {
		// key = "guestUserId:YYYY-MM-DD"
		parts := splitKeyDate(key)
		if parts == nil {
			continue
		}
		if err := RecordGuestUsage(convexURL, token, parts[0], seconds, parts[1]); err != nil {
			log.Printf("[GUEST-CONFIG] Failed to flush usage for %s: %v", key, err)
			// Put it back as dirty
			m.usageMu.Lock()
			m.dirty[key] = true
			m.usageMu.Unlock()
		}
	}
}

func splitKeyDate(key string) []string {
	// Find the last ':' before the date portion (YYYY-MM-DD has colons in the key)
	// Format: "userId:2026-04-06" — split on first ':'
	for i := 0; i < len(key); i++ {
		if key[i] == ':' {
			return []string{key[:i], key[i+1:]}
		}
	}
	return nil
}

// UpdateConfigs replaces the cached configs with fresh data from Convex.
func (m *GuestConfigManager) UpdateConfigs(configs []GuestConfig) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.configs = make(map[string]*GuestConfig, len(configs))
	for i := range configs {
		m.configs[configs[i].GuestUserID] = &configs[i]
	}
}

// GetConfig returns the config for a specific guest.
func (m *GuestConfigManager) GetConfig(guestUserID string) *GuestConfig {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.configs[guestUserID]
}

// GetAllConfigs returns all cached guest configs.
func (m *GuestConfigManager) GetAllConfigs() []GuestConfig {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make([]GuestConfig, 0, len(m.configs))
	for _, c := range m.configs {
		result = append(result, *c)
	}
	return result
}

// ─── Project Access (P2P local) ─────────────────────────────────────

// SetProjectAccess sets the allowed projects for a guest (P2P — stored locally).
func (m *GuestConfigManager) SetProjectAccess(guestUserID string, projects []string) {
	m.mu.Lock()
	m.projects[guestUserID] = projects
	m.mu.Unlock()
	m.saveProjectAccess()
}

// GetProjectAccess returns the allowed projects for a guest.
func (m *GuestConfigManager) GetProjectAccess(guestUserID string) []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.projects[guestUserID]
}

func (m *GuestConfigManager) saveProjectAccess() {
	data, err := json.MarshalIndent(m.projects, "", "  ")
	if err != nil {
		log.Printf("[GUEST-CONFIG] Failed to marshal project access: %v", err)
		return
	}
	path := filepath.Join(m.configDir, "project-access.json")
	if err := os.WriteFile(path, data, 0600); err != nil {
		log.Printf("[GUEST-CONFIG] Failed to save project access: %v", err)
	}
}

// guestPromptPrefix returns a security preamble prepended to guest task prompts.
// This instructs the AI agent to stay within the project directory and avoid
// accessing sensitive files. Combined with workdir restriction, this provides
// defense-in-depth for guest tasks.
func guestPromptPrefix(workDir string) string {
	return fmt.Sprintf(`[SECURITY CONTEXT — GUEST SESSION]
You are running as a GUEST user with restricted access. You MUST follow these rules:
1. ONLY read/write files within the project directory: %s
2. NEVER access, read, or modify files outside the project directory
3. NEVER access ~/.ssh, ~/.env, ~/.aws, ~/.config, ~/.gnupg, /etc, or any dotfiles in home directory
4. NEVER run commands that modify system configuration, install global packages, or access other users' files
5. NEVER use curl/wget to upload or exfiltrate file contents to external URLs
6. NEVER modify git credentials, SSH keys, or authentication tokens
7. Focus only on the coding task requested by the user
[END SECURITY CONTEXT]

`, workDir)
}

func (m *GuestConfigManager) loadProjectAccess() {
	path := filepath.Join(m.configDir, "project-access.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return // not found = no project restrictions
	}
	var projects map[string][]string
	if err := json.Unmarshal(data, &projects); err != nil {
		log.Printf("[GUEST-CONFIG] Failed to parse project access: %v", err)
		return
	}
	m.projects = projects
}
