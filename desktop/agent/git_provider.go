package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	osexec "os/exec"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

// GitProvider represents a configured git hosting provider (GitHub, GitLab).
// Stored locally in ~/.yaver/git-providers.json — never sent to Convex.
type GitProvider struct {
	Host       string `json:"host"`                 // "github.com" or "gitlab.com" or custom
	Provider   string `json:"provider"`              // "github" or "gitlab"
	Username   string `json:"username"`              // verified username from API
	Token      string `json:"token"`                 // Personal Access Token
	AvatarURL  string `json:"avatarUrl,omitempty"`   // profile avatar
	SSHKeyPath string `json:"sshKeyPath,omitempty"`  // path to generated SSH private key
	SSHKeyName string `json:"sshKeyName,omitempty"`  // name used when adding to provider
	SetupAt    string `json:"setupAt"`               // ISO 8601
}

// RemoteRepo represents a repository from a git provider's API.
type RemoteRepo struct {
	Name        string `json:"name"`
	FullName    string `json:"fullName"`    // "owner/repo"
	Description string `json:"description"`
	CloneURL    string `json:"cloneUrl"`    // HTTPS URL
	SSHURL      string `json:"sshUrl"`      // SSH URL
	Private     bool   `json:"private"`
	Fork        bool   `json:"fork"`
	Language    string `json:"language"`
	Stars       int    `json:"stars"`
	UpdatedAt   string `json:"updatedAt"`
}

// ---------------------------------------------------------------------------
// Provider file helpers
// ---------------------------------------------------------------------------

func gitProvidersPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".yaver", "git-providers.json")
}

func loadGitProviders() ([]GitProvider, error) {
	data, err := os.ReadFile(gitProvidersPath())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var providers []GitProvider
	if err := json.Unmarshal(data, &providers); err != nil {
		return nil, err
	}
	return providers, nil
}

func saveGitProviders(providers []GitProvider) error {
	path := gitProvidersPath()
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(providers, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0600)
}

func findProvider(host string) *GitProvider {
	providers, err := loadGitProviders()
	if err != nil || len(providers) == 0 {
		return nil
	}
	for _, p := range providers {
		if strings.EqualFold(p.Host, host) {
			return &p
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// GitHub / GitLab API helpers
// ---------------------------------------------------------------------------

func verifyGitHubToken(token string) (username, avatarURL string, err error) {
	req, _ := http.NewRequest("GET", "https://api.github.com/user", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return "", "", fmt.Errorf("GitHub API returned %d — check your token", resp.StatusCode)
	}

	var user struct {
		Login     string `json:"login"`
		AvatarURL string `json:"avatar_url"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&user); err != nil {
		return "", "", fmt.Errorf("failed to parse response: %w", err)
	}
	return user.Login, user.AvatarURL, nil
}

func verifyGitLabToken(host, token string) (username, avatarURL string, err error) {
	apiURL := fmt.Sprintf("https://%s/api/v4/user", host)
	req, _ := http.NewRequest("GET", apiURL, nil)
	req.Header.Set("PRIVATE-TOKEN", token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return "", "", fmt.Errorf("GitLab API returned %d — check your token", resp.StatusCode)
	}

	var user struct {
		Username  string `json:"username"`
		AvatarURL string `json:"avatar_url"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&user); err != nil {
		return "", "", fmt.Errorf("failed to parse response: %w", err)
	}
	return user.Username, user.AvatarURL, nil
}

func listGitHubRepos(token string, page, perPage int) ([]RemoteRepo, error) {
	url := fmt.Sprintf("https://api.github.com/user/repos?sort=updated&direction=desc&per_page=%d&page=%d&type=all", perPage, page)
	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("GitHub API returned %d: %s", resp.StatusCode, string(body))
	}

	var ghRepos []struct {
		Name        string `json:"name"`
		FullName    string `json:"full_name"`
		Description string `json:"description"`
		CloneURL    string `json:"clone_url"`
		SSHURL      string `json:"ssh_url"`
		Private     bool   `json:"private"`
		Fork        bool   `json:"fork"`
		Language    string `json:"language"`
		Stars       int    `json:"stargazers_count"`
		UpdatedAt   string `json:"updated_at"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&ghRepos); err != nil {
		return nil, err
	}

	repos := make([]RemoteRepo, len(ghRepos))
	for i, r := range ghRepos {
		repos[i] = RemoteRepo{
			Name:        r.Name,
			FullName:    r.FullName,
			Description: r.Description,
			CloneURL:    r.CloneURL,
			SSHURL:      r.SSHURL,
			Private:     r.Private,
			Fork:        r.Fork,
			Language:    r.Language,
			Stars:       r.Stars,
			UpdatedAt:   r.UpdatedAt,
		}
	}
	return repos, nil
}

func listGitLabRepos(host, token string, page, perPage int) ([]RemoteRepo, error) {
	url := fmt.Sprintf("https://%s/api/v4/projects?membership=true&order_by=updated_at&sort=desc&per_page=%d&page=%d", host, perPage, page)
	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("PRIVATE-TOKEN", token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("GitLab API returned %d: %s", resp.StatusCode, string(body))
	}

	var glRepos []struct {
		Name            string `json:"name"`
		PathWithNS      string `json:"path_with_namespace"`
		Description     string `json:"description"`
		HTTPCloneURL    string `json:"http_url_to_repo"`
		SSHCloneURL     string `json:"ssh_url_to_repo"`
		Visibility      string `json:"visibility"`
		ForkedFrom      *struct{} `json:"forked_from_project"`
		Star            int    `json:"star_count"`
		LastActivityAt  string `json:"last_activity_at"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&glRepos); err != nil {
		return nil, err
	}

	repos := make([]RemoteRepo, len(glRepos))
	for i, r := range glRepos {
		repos[i] = RemoteRepo{
			Name:        r.Name,
			FullName:    r.PathWithNS,
			Description: r.Description,
			CloneURL:    r.HTTPCloneURL,
			SSHURL:      r.SSHCloneURL,
			Private:     r.Visibility != "public",
			Fork:        r.ForkedFrom != nil,
			Stars:       r.Star,
			UpdatedAt:   r.LastActivityAt,
		}
	}
	return repos, nil
}

// ---------------------------------------------------------------------------
// SSH key generation
// ---------------------------------------------------------------------------

func yaverSSHKeyDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".yaver", "ssh")
}

// generateYaverSSHKey creates an ed25519 SSH keypair for Yaver.
// Returns (privatePath, publicKeyString, error).
func generateYaverSSHKey(label string) (string, string, error) {
	dir := yaverSSHKeyDir()
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", "", err
	}

	privPath := filepath.Join(dir, "yaver_ed25519")
	pubPath := privPath + ".pub"

	// Don't overwrite existing key
	if _, err := os.Stat(privPath); err == nil {
		pubData, err := os.ReadFile(pubPath)
		if err != nil {
			return "", "", fmt.Errorf("key exists but can't read public key: %w", err)
		}
		return privPath, strings.TrimSpace(string(pubData)), nil
	}

	// Generate ed25519 key
	pubKey, privKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return "", "", fmt.Errorf("keygen failed: %w", err)
	}

	// Marshal private key to OpenSSH format
	sshPriv, err := ssh.MarshalPrivateKey(privKey, label)
	if err != nil {
		return "", "", fmt.Errorf("marshal private key: %w", err)
	}

	if err := os.WriteFile(privPath, pem.EncodeToMemory(sshPriv), 0600); err != nil {
		return "", "", err
	}

	// Marshal public key
	sshPub, err := ssh.NewPublicKey(pubKey)
	if err != nil {
		return "", "", fmt.Errorf("marshal public key: %w", err)
	}
	pubStr := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(sshPub))) + " " + label

	if err := os.WriteFile(pubPath, []byte(pubStr+"\n"), 0644); err != nil {
		return "", "", err
	}

	log.Printf("[git-provider] Generated SSH key: %s", pubPath)
	return privPath, pubStr, nil
}

// addSSHKeyToGitHub adds a public SSH key to the user's GitHub account.
func addSSHKeyToGitHub(token, title, pubKey string) error {
	body := fmt.Sprintf(`{"title":%q,"key":%q}`, title, pubKey)
	req, _ := http.NewRequest("POST", "https://api.github.com/user/keys", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode == 422 {
		// Key already exists — that's fine
		return nil
	}
	if resp.StatusCode != 201 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("GitHub API returned %d: %s", resp.StatusCode, string(respBody))
	}
	return nil
}

// addSSHKeyToGitLab adds a public SSH key to the user's GitLab account.
func addSSHKeyToGitLab(host, token, title, pubKey string) error {
	body := fmt.Sprintf(`{"title":%q,"key":%q}`, title, pubKey)
	apiURL := fmt.Sprintf("https://%s/api/v4/user/keys", host)
	req, _ := http.NewRequest("POST", apiURL, strings.NewReader(body))
	req.Header.Set("PRIVATE-TOKEN", token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode == 400 {
		// Key already exists
		return nil
	}
	if resp.StatusCode != 201 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("GitLab API returned %d: %s", resp.StatusCode, string(respBody))
	}
	return nil
}

// configureSSHForProvider ensures ~/.ssh/config has an entry for the provider
// using the Yaver SSH key.
func configureSSHForProvider(host, keyPath string) error {
	home, _ := os.UserHomeDir()
	sshDir := filepath.Join(home, ".ssh")
	configPath := filepath.Join(sshDir, "config")

	if err := os.MkdirAll(sshDir, 0700); err != nil {
		return err
	}

	// Read existing config
	existing, _ := os.ReadFile(configPath)
	content := string(existing)

	// Check if entry already exists
	marker := fmt.Sprintf("# Yaver — %s", host)
	if strings.Contains(content, marker) {
		return nil // already configured
	}

	// Append entry
	entry := fmt.Sprintf("\n%s\nHost %s\n  IdentityFile %s\n  IdentitiesOnly yes\n\n", marker, host, keyPath)
	f, err := os.OpenFile(configPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString(entry)
	return err
}

// ---------------------------------------------------------------------------
// Post-clone metadata generation
// ---------------------------------------------------------------------------

// generateRepoMetadata runs after a successful clone to gather project info.
func generateRepoMetadata(repoPath string) map[string]interface{} {
	meta := map[string]interface{}{
		"path":      repoPath,
		"name":      filepath.Base(repoPath),
		"clonedAt":  time.Now().UTC().Format(time.RFC3339),
	}

	// Branch
	if branch, err := runGit(repoPath, "rev-parse", "--abbrev-ref", "HEAD"); err == nil {
		meta["branch"] = branch
	}

	// Remote
	if remote, err := runGit(repoPath, "config", "--get", "remote.origin.url"); err == nil {
		meta["remote"] = remote
	}

	// Last commit
	if commit, err := runGit(repoPath, "log", "-1", "--format=%H %s"); err == nil {
		meta["lastCommit"] = commit
	}

	// Detect framework
	info := DetectProjectInfo(repoPath)
	if info.Framework != "" {
		meta["framework"] = info.Framework
	}

	// Count files
	if out, err := runGit(repoPath, "ls-files"); err == nil {
		lines := strings.Split(strings.TrimSpace(out), "\n")
		meta["fileCount"] = len(lines)
	}

	// Detect languages from file extensions
	langs := detectLanguages(repoPath)
	if len(langs) > 0 {
		meta["languages"] = langs
	}

	// Check for common config files
	configs := []string{}
	for _, f := range []string{"package.json", "go.mod", "Cargo.toml", "pubspec.yaml", "pyproject.toml", "Dockerfile", "docker-compose.yml"} {
		if _, err := os.Stat(filepath.Join(repoPath, f)); err == nil {
			configs = append(configs, f)
		}
	}
	meta["buildFiles"] = configs

	return meta
}

// detectLanguages is defined in discovery.go — reused here for metadata generation.

// ---------------------------------------------------------------------------
// HTTP handlers
// ---------------------------------------------------------------------------

// handleGitProviderSetup handles POST /git/provider/setup.
// Verifies token, saves provider config, optionally generates SSH key.
func (s *HTTPServer) handleGitProviderSetup(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}

	var req struct {
		Provider    string `json:"provider"`    // "github" or "gitlab"
		Host        string `json:"host"`        // optional, defaults to github.com/gitlab.com
		Token       string `json:"token"`       // Personal Access Token
		GenerateSSH bool   `json:"generateSsh"` // generate + upload SSH key
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	if req.Provider == "" || req.Token == "" {
		jsonError(w, http.StatusBadRequest, "provider and token are required")
		return
	}

	// Default hosts
	host := req.Host
	if host == "" {
		switch req.Provider {
		case "github":
			host = "github.com"
		case "gitlab":
			host = "gitlab.com"
		default:
			jsonError(w, http.StatusBadRequest, "unknown provider — use 'github' or 'gitlab'")
			return
		}
	}

	// Verify token with provider API
	var username, avatarURL string
	var err error

	switch req.Provider {
	case "github":
		username, avatarURL, err = verifyGitHubToken(req.Token)
	case "gitlab":
		username, avatarURL, err = verifyGitLabToken(host, req.Token)
	}

	if err != nil {
		jsonError(w, http.StatusUnauthorized, "token verification failed: "+err.Error())
		return
	}

	provider := GitProvider{
		Host:      host,
		Provider:  req.Provider,
		Username:  username,
		Token:     req.Token,
		AvatarURL: avatarURL,
		SetupAt:   time.Now().UTC().Format(time.RFC3339),
	}

	// Generate SSH key if requested
	if req.GenerateSSH {
		keyLabel := fmt.Sprintf("yaver-agent@%s", hostname())
		privPath, pubKey, err := generateYaverSSHKey(keyLabel)
		if err != nil {
			log.Printf("[git-provider] SSH keygen failed: %v", err)
			// Non-fatal — continue without SSH
		} else {
			// Add public key to provider
			title := fmt.Sprintf("Yaver Agent (%s)", hostname())
			switch req.Provider {
			case "github":
				err = addSSHKeyToGitHub(req.Token, title, pubKey)
			case "gitlab":
				err = addSSHKeyToGitLab(host, req.Token, title, pubKey)
			}
			if err != nil {
				log.Printf("[git-provider] Failed to add SSH key to %s: %v", req.Provider, err)
			} else {
				provider.SSHKeyPath = privPath
				provider.SSHKeyName = title
				// Configure ~/.ssh/config
				if err := configureSSHForProvider(host, privPath); err != nil {
					log.Printf("[git-provider] Failed to configure SSH: %v", err)
				}
			}
		}
	}

	// Also save as git credential (for HTTPS clone fallback)
	creds, _ := loadGitCredentials()
	found := false
	for i := range creds {
		if strings.EqualFold(creds[i].Host, host) {
			creds[i].Token = req.Token
			creds[i].Username = username
			found = true
			break
		}
	}
	if !found {
		creds = append(creds, GitCredential{Host: host, Username: username, Token: req.Token})
	}
	_ = saveGitCredentials(creds)

	// Save provider
	providers, _ := loadGitProviders()
	updated := false
	for i := range providers {
		if strings.EqualFold(providers[i].Host, host) {
			providers[i] = provider
			updated = true
			break
		}
	}
	if !updated {
		providers = append(providers, provider)
	}
	if err := saveGitProviders(providers); err != nil {
		jsonError(w, http.StatusInternalServerError, "failed to save provider: "+err.Error())
		return
	}

	log.Printf("[git-provider] Setup %s: user=%s ssh=%v", req.Provider, username, provider.SSHKeyPath != "")

	jsonReply(w, http.StatusOK, map[string]interface{}{
		"ok":       true,
		"username": username,
		"avatar":   avatarURL,
		"host":     host,
		"provider": req.Provider,
		"sshKey":   provider.SSHKeyPath != "",
	})
}

// handleGitProviderStatus handles GET /git/provider/status.
func (s *HTTPServer) handleGitProviderStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, http.StatusMethodNotAllowed, "use GET")
		return
	}

	providers, err := loadGitProviders()
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "failed to load providers: "+err.Error())
		return
	}

	// Return without tokens
	type safeProvider struct {
		Host      string `json:"host"`
		Provider  string `json:"provider"`
		Username  string `json:"username"`
		AvatarURL string `json:"avatarUrl,omitempty"`
		HasSSH    bool   `json:"hasSsh"`
		SetupAt   string `json:"setupAt"`
	}

	result := make([]safeProvider, len(providers))
	for i, p := range providers {
		result[i] = safeProvider{
			Host:      p.Host,
			Provider:  p.Provider,
			Username:  p.Username,
			AvatarURL: p.AvatarURL,
			HasSSH:    p.SSHKeyPath != "",
			SetupAt:   p.SetupAt,
		}
	}

	jsonReply(w, http.StatusOK, map[string]interface{}{
		"ok":        true,
		"providers": result,
	})
}

// handleGitProviderRepos handles GET /git/provider/repos?host=github.com&page=1.
func (s *HTTPServer) handleGitProviderRepos(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, http.StatusMethodNotAllowed, "use GET")
		return
	}

	host := r.URL.Query().Get("host")
	if host == "" {
		host = "github.com"
	}

	provider := findProvider(host)
	if provider == nil {
		jsonError(w, http.StatusNotFound, fmt.Sprintf("no provider configured for %s — set up in Settings first", host))
		return
	}

	page := 1
	if p := r.URL.Query().Get("page"); p != "" {
		fmt.Sscanf(p, "%d", &page)
	}
	perPage := 30
	if pp := r.URL.Query().Get("per_page"); pp != "" {
		fmt.Sscanf(pp, "%d", &perPage)
	}
	search := r.URL.Query().Get("search")

	var repos []RemoteRepo
	var err error

	switch provider.Provider {
	case "github":
		repos, err = listGitHubRepos(provider.Token, page, perPage)
	case "gitlab":
		repos, err = listGitLabRepos(host, provider.Token, page, perPage)
	}

	if err != nil {
		jsonError(w, http.StatusBadGateway, "failed to list repos: "+err.Error())
		return
	}

	// Client-side search filter
	if search != "" {
		search = strings.ToLower(search)
		var filtered []RemoteRepo
		for _, r := range repos {
			if strings.Contains(strings.ToLower(r.Name), search) ||
				strings.Contains(strings.ToLower(r.FullName), search) ||
				strings.Contains(strings.ToLower(r.Description), search) {
				filtered = append(filtered, r)
			}
		}
		repos = filtered
	}

	if repos == nil {
		repos = []RemoteRepo{}
	}

	jsonReply(w, http.StatusOK, map[string]interface{}{
		"ok":       true,
		"repos":    repos,
		"page":     page,
		"provider": provider.Provider,
		"username": provider.Username,
	})
}

// handleGitProviderRemove handles DELETE /git/provider/{host}.
func (s *HTTPServer) handleGitProviderRemove(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		jsonError(w, http.StatusMethodNotAllowed, "use DELETE")
		return
	}

	host := strings.TrimPrefix(r.URL.Path, "/git/provider/")
	if host == "" || host == "setup" || host == "status" || host == "repos" {
		jsonError(w, http.StatusBadRequest, "host is required")
		return
	}

	providers, err := loadGitProviders()
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}

	var filtered []GitProvider
	found := false
	for _, p := range providers {
		if strings.EqualFold(p.Host, host) {
			found = true
			continue
		}
		filtered = append(filtered, p)
	}

	if !found {
		jsonError(w, http.StatusNotFound, fmt.Sprintf("no provider for %q", host))
		return
	}

	if err := saveGitProviders(filtered); err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Also remove from git credentials
	creds, _ := loadGitCredentials()
	var filteredCreds []GitCredential
	for _, c := range creds {
		if !strings.EqualFold(c.Host, host) {
			filteredCreds = append(filteredCreds, c)
		}
	}
	_ = saveGitCredentials(filteredCreds)

	log.Printf("[git-provider] Removed provider: %s", host)
	jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true})
}

// handleRepoCloneEnhanced wraps handleRepoClone with post-clone metadata.
// Called from POST /repos/clone — extends existing handler.
func (s *HTTPServer) handleRepoCloneWithMetadata(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}

	var req struct {
		URL    string `json:"url"`
		Dir    string `json:"dir"`
		Branch string `json:"branch"`
	}

	// Read body for both clone and metadata
	bodyBytes, _ := io.ReadAll(r.Body)
	if err := json.Unmarshal(bodyBytes, &req); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if req.URL == "" {
		jsonError(w, http.StatusBadRequest, "url is required")
		return
	}

	repoName := repoNameFromURL(req.URL)
	targetDir := req.Dir
	if targetDir == "" {
		home, _ := os.UserHomeDir()
		targetDir = filepath.Join(home, "Projects")
	}
	if err := os.MkdirAll(targetDir, 0755); err != nil {
		jsonError(w, http.StatusInternalServerError, "cannot create directory: "+err.Error())
		return
	}

	clonePath := filepath.Join(targetDir, repoName)

	// Check if already cloned
	if _, err := os.Stat(filepath.Join(clonePath, ".git")); err == nil {
		meta := generateRepoMetadata(clonePath)
		jsonReply(w, http.StatusOK, map[string]interface{}{
			"ok":              true,
			"path":            clonePath,
			"alreadyExisted":  true,
			"metadata":        meta,
		})
		return
	}

	// Determine clone method: SSH (if key exists) or HTTPS (with token)
	cloneURL := req.URL
	host := hostFromURL(req.URL)
	provider := findProvider(host)

	if provider != nil && provider.SSHKeyPath != "" {
		// Convert HTTPS URL to SSH if we have an SSH key
		if strings.HasPrefix(cloneURL, "https://") {
			// https://github.com/user/repo.git → git@github.com:user/repo.git
			parsed := strings.TrimPrefix(cloneURL, "https://")
			parsed = strings.TrimPrefix(parsed, host+"/")
			cloneURL = fmt.Sprintf("git@%s:%s", host, parsed)
			if !strings.HasSuffix(cloneURL, ".git") {
				cloneURL += ".git"
			}
		}
	} else {
		// HTTPS with token
		cloneURL = injectCredentials(req.URL)
	}

	args := []string{"clone"}
	if req.Branch != "" {
		args = append(args, "-b", req.Branch)
	}
	args = append(args, cloneURL, clonePath)

	cmd := osexec.Command("git", args...)
	// If using SSH key, set GIT_SSH_COMMAND
	if provider != nil && provider.SSHKeyPath != "" {
		cmd.Env = append(os.Environ(), fmt.Sprintf("GIT_SSH_COMMAND=ssh -i %s -o IdentitiesOnly=yes -o StrictHostKeyChecking=accept-new", provider.SSHKeyPath))
	}

	out, err := cmd.CombinedOutput()
	output := strings.TrimSpace(string(out))

	if err != nil {
		sanitised := strings.ReplaceAll(output, cloneURL, req.URL)
		jsonError(w, http.StatusInternalServerError, "git clone failed: "+sanitised)
		return
	}

	// Generate metadata
	meta := generateRepoMetadata(clonePath)

	// Trigger project discovery refresh
	go func() {
		if cmd, err := osexec.Command("touch", filepath.Join(clonePath, ".git", "HEAD")).CombinedOutput(); err != nil {
			log.Printf("[git-provider] touch .git/HEAD: %s %v", string(cmd), err)
		}
	}()

	log.Printf("[git-provider] Cloned %s → %s", req.URL, clonePath)
	jsonReply(w, http.StatusOK, map[string]interface{}{
		"ok":       true,
		"path":     clonePath,
		"output":   strings.ReplaceAll(output, cloneURL, req.URL),
		"metadata": meta,
	})
}

// hostname returns the machine hostname for key labels.
func hostname() string {
	h, err := os.Hostname()
	if err != nil {
		return "yaver-agent"
	}
	return h
}
