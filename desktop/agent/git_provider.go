package main

import (
	"context"
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
	Provider   string `json:"provider"`             // "github" or "gitlab"
	Username   string `json:"username"`             // verified username from API
	Token      string `json:"token"`                // Personal Access Token
	AvatarURL  string `json:"avatarUrl,omitempty"`  // profile avatar
	SSHKeyPath string `json:"sshKeyPath,omitempty"` // path to generated SSH private key
	SSHKeyName string `json:"sshKeyName,omitempty"` // name used when adding to provider
	SetupAt    string `json:"setupAt"`              // ISO 8601
}

// RemoteRepo represents a repository from a git provider's API.
type RemoteRepo struct {
	Name        string `json:"name"`
	FullName    string `json:"fullName"` // "owner/repo"
	Description string `json:"description"`
	CloneURL    string `json:"cloneUrl"` // HTTPS URL
	SSHURL      string `json:"sshUrl"`   // SSH URL
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
// Auto-detect tokens from dev machine's existing tooling
// ---------------------------------------------------------------------------

// gitProviderExternalAuth describes auth state detected outside Yaver's
// own stores: gh/glab CLIs, env vars, ssh keys configured for the host.
// Used by `yaver status` so the Git block doesn't show "not configured"
// when the user has perfectly working git auth via standard tooling.
type gitProviderExternalAuth struct {
	Configured bool
	Sources    []string // e.g. ["gh", "ssh", "env:GITHUB_TOKEN"]
	Username   string
}

// detectGitHubExternalAuth probes gh CLI, env vars, and ~/.ssh for a
// working GitHub identity that lives outside Yaver's own stores.
func detectGitHubExternalAuth() gitProviderExternalAuth {
	var out gitProviderExternalAuth
	if user, ok := readGhAuthStatus("github.com"); ok {
		out.Configured = true
		out.Sources = append(out.Sources, "gh")
		if user != "" {
			out.Username = user
		}
	}
	for _, env := range []string{"GITHUB_TOKEN", "GH_TOKEN"} {
		if strings.TrimSpace(os.Getenv(env)) != "" {
			out.Configured = true
			out.Sources = append(out.Sources, "env:"+env)
			break
		}
	}
	if sshConfiguredForHost("github.com") {
		out.Configured = true
		out.Sources = append(out.Sources, "ssh")
	}
	return out
}

// detectGitLabExternalAuth is the GitLab counterpart of
// detectGitHubExternalAuth: glab CLI, env vars, ssh.
func detectGitLabExternalAuth(host string) gitProviderExternalAuth {
	if strings.TrimSpace(host) == "" {
		host = "gitlab.com"
	}
	var out gitProviderExternalAuth
	if user, ok := readGlabAuthStatus(host); ok {
		out.Configured = true
		out.Sources = append(out.Sources, "glab")
		if user != "" {
			out.Username = user
		}
	}
	for _, env := range []string{"GITLAB_TOKEN", "GITLAB_PRIVATE_TOKEN"} {
		if strings.TrimSpace(os.Getenv(env)) != "" {
			out.Configured = true
			out.Sources = append(out.Sources, "env:"+env)
			break
		}
	}
	if sshConfiguredForHost(host) {
		out.Configured = true
		out.Sources = append(out.Sources, "ssh")
	}
	return out
}

// readGhAuthStatus parses `gh auth status` and returns (username, ok)
// for the given host. No network call — gh reads its own config file.
func readGhAuthStatus(host string) (string, bool) {
	out, err := osexec.Command("gh", "auth", "status").CombinedOutput()
	if err != nil && len(out) == 0 {
		return "", false
	}
	return parseLoggedInUsername(string(out), host)
}

// readGlabAuthStatus is the glab equivalent of readGhAuthStatus.
func readGlabAuthStatus(host string) (string, bool) {
	out, err := osexec.Command("glab", "auth", "status").CombinedOutput()
	if err != nil && len(out) == 0 {
		return "", false
	}
	return parseLoggedInUsername(string(out), host)
}

// parseLoggedInUsername scans `gh auth status` / `glab auth status`
// output for "Logged in to <host>" and extracts the username. gh prints
// "account <username>"; glab prints "as <username>" — handle both.
func parseLoggedInUsername(s, host string) (string, bool) {
	host = strings.ToLower(strings.TrimSpace(host))
	for _, line := range strings.Split(s, "\n") {
		lower := strings.ToLower(line)
		if !strings.Contains(lower, host) || !strings.Contains(lower, "logged in") {
			continue
		}
		for _, marker := range []string{" account ", " as "} {
			if idx := strings.Index(line, marker); idx >= 0 {
				rest := strings.TrimSpace(line[idx+len(marker):])
				end := len(rest)
				for i, r := range rest {
					if r == ' ' || r == '\t' || r == '(' {
						end = i
						break
					}
				}
				name := strings.TrimSpace(rest[:end])
				if name != "" {
					return name, true
				}
			}
		}
		return "", true
	}
	return "", false
}

// sshConfiguredForHost is a local-only heuristic: returns true when
// ~/.ssh has at least one common private key file AND the host appears
// either in ~/.ssh/config (Host/HostName line) or ~/.ssh/known_hosts.
// We can't actually verify the key is registered with the provider
// without a network call; this just reflects "looks set up locally".
func sshConfiguredForHost(host string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "" {
		return false
	}
	home, _ := os.UserHomeDir()
	if home == "" {
		return false
	}
	sshDir := filepath.Join(home, ".ssh")

	haveKey := false
	for _, name := range []string{"id_ed25519", "id_rsa", "id_ecdsa", "id_dsa"} {
		if _, err := os.Stat(filepath.Join(sshDir, name)); err == nil {
			haveKey = true
			break
		}
	}
	if !haveKey {
		// also accept any *_<host> key (e.g. id_ed25519_github)
		entries, _ := os.ReadDir(sshDir)
		for _, e := range entries {
			n := e.Name()
			if strings.HasPrefix(n, "id_") && !strings.HasSuffix(n, ".pub") {
				haveKey = true
				break
			}
		}
	}
	if !haveKey {
		return false
	}

	if data, err := os.ReadFile(filepath.Join(sshDir, "config")); err == nil {
		for _, raw := range strings.Split(string(data), "\n") {
			line := strings.TrimSpace(strings.ToLower(raw))
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			if (strings.HasPrefix(line, "host ") || strings.HasPrefix(line, "hostname ")) && strings.Contains(line, host) {
				return true
			}
		}
	}

	if data, err := os.ReadFile(filepath.Join(sshDir, "known_hosts")); err == nil {
		if strings.Contains(strings.ToLower(string(data)), host) {
			return true
		}
	}

	return false
}

// detectGitHubToken tries to find a GitHub token from the dev machine.
// Checks: gh CLI, git credential helpers, env vars, git-credentials file.
func detectGitHubToken() string {
	// 1. gh CLI (most common for devs)
	if out, err := osexec.Command("gh", "auth", "token").Output(); err == nil {
		token := strings.TrimSpace(string(out))
		if token != "" {
			return token
		}
	}

	// 2. Environment variables
	for _, env := range []string{"GITHUB_TOKEN", "GH_TOKEN"} {
		if token := os.Getenv(env); token != "" {
			return token
		}
	}

	// 3. git credential fill (queries osxkeychain, credential-manager, etc.)
	if token := gitCredentialFill("github.com"); token != "" {
		return token
	}

	// 4. Yaver's own git-credentials.json
	if cred := findCredentialForHost("github.com"); cred != nil && cred.Token != "" {
		return cred.Token
	}

	// 5. Yaver's git-providers.json
	if p := findProvider("github.com"); p != nil && p.Token != "" {
		return p.Token
	}

	return ""
}

// detectGitLabToken tries to find a GitLab token from the dev machine.
func detectGitLabToken(host string) string {
	// 1. GITLAB_TOKEN env var
	for _, env := range []string{"GITLAB_TOKEN", "GITLAB_PRIVATE_TOKEN"} {
		if token := os.Getenv(env); token != "" {
			return token
		}
	}

	// 2. glab CLI
	if out, err := osexec.Command("glab", "config", "get", "token", "--host", host).Output(); err == nil {
		token := strings.TrimSpace(string(out))
		if token != "" {
			return token
		}
	}

	// 3. git credential fill (queries osxkeychain, credential-manager, etc.)
	if token := gitCredentialFill(host); token != "" {
		return token
	}

	// 4. Yaver's own stores
	if cred := findCredentialForHost(host); cred != nil && cred.Token != "" {
		return cred.Token
	}
	if p := findProvider(host); p != nil && p.Token != "" {
		return p.Token
	}

	return ""
}

// gitCredentialFill queries git's native credential helpers (osxkeychain,
// credential-manager, etc.) for a stored password/token for the given host.
func gitCredentialFill(host string) string {
	cmd := osexec.Command("git", "credential", "fill")
	cmd.Stdin = strings.NewReader("protocol=https\nhost=" + host + "\n\n")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(line, "password=") {
			token := strings.TrimPrefix(line, "password=")
			token = strings.TrimSpace(token)
			if token != "" {
				return token
			}
		}
	}
	return ""
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
	req.Header.Set("Authorization", "Bearer "+token)

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

// listGitHubReposPaged fetches up to maxPages pages of /user/repos so a
// user with > perPage repos still sees later ones in the "browse all"
// view. Caps total at maxPages*perPage to avoid silly fan-out for users
// with thousands of repos — they should use the search box.
func listGitHubReposPaged(token string, perPage, maxPages int) ([]RemoteRepo, error) {
	if perPage <= 0 {
		perPage = 100
	}
	if perPage > 100 {
		perPage = 100
	}
	if maxPages <= 0 {
		maxPages = 1
	}
	var all []RemoteRepo
	for page := 1; page <= maxPages; page++ {
		batch, err := listGitHubRepos(token, page, perPage)
		if err != nil {
			if page == 1 {
				return nil, err
			}
			break
		}
		all = append(all, batch...)
		if len(batch) < perPage {
			break
		}
	}
	return all, nil
}

// listGitLabReposPaged is the GitLab analogue of listGitHubReposPaged:
// fetch up to maxPages pages of /api/v4/projects (membership=true so
// private + collab repos appear) and return the union.
func listGitLabReposPaged(host, token string, perPage, maxPages int) ([]RemoteRepo, error) {
	if perPage <= 0 {
		perPage = 100
	}
	if perPage > 100 {
		perPage = 100
	}
	if maxPages <= 0 {
		maxPages = 1
	}
	var all []RemoteRepo
	for page := 1; page <= maxPages; page++ {
		batch, err := listGitLabRepos(host, token, page, perPage)
		if err != nil {
			if page == 1 {
				return nil, err
			}
			break
		}
		all = append(all, batch...)
		if len(batch) < perPage {
			break
		}
	}
	return all, nil
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
	req.Header.Set("Authorization", "Bearer "+token)

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
		Name           string    `json:"name"`
		PathWithNS     string    `json:"path_with_namespace"`
		Description    string    `json:"description"`
		HTTPCloneURL   string    `json:"http_url_to_repo"`
		SSHCloneURL    string    `json:"ssh_url_to_repo"`
		Visibility     string    `json:"visibility"`
		ForkedFrom     *struct{} `json:"forked_from_project"`
		Star           int       `json:"star_count"`
		LastActivityAt string    `json:"last_activity_at"`
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
	req.Header.Set("Authorization", "Bearer "+token)
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
		"path":     repoPath,
		"name":     filepath.Base(repoPath),
		"clonedAt": time.Now().UTC().Format(time.RFC3339),
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
	if info.Stack.Type != "" {
		meta["stackType"] = info.Stack.Type
	}
	meta["stack"] = info.Stack

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
	if ci := detectRepoCIProviders(repoPath); len(ci) > 0 {
		meta["ciProviders"] = ci
	}
	if integrations := detectRepoIntegrations(repoPath, info); len(integrations) > 0 {
		meta["integrations"] = integrations
	}
	meta["topology"] = detectRepoTopology(repoPath, info)
	meta["autoinit"] = computeAutoInitStatus(repoPath)

	return meta
}

func detectRepoCIProviders(repoPath string) []string {
	var out []string
	if _, err := os.Stat(filepath.Join(repoPath, ".github", "workflows")); err == nil {
		out = append(out, "github-actions")
	}
	for _, name := range []string{".gitlab-ci.yml", ".gitlab-ci.yaml"} {
		if _, err := os.Stat(filepath.Join(repoPath, name)); err == nil {
			out = append(out, "gitlab-ci")
			break
		}
	}
	return out
}

func detectRepoIntegrations(repoPath string, info ProjectInfo) []string {
	seen := map[string]bool{}
	add := func(name string) {
		if name != "" && !seen[name] {
			seen[name] = true
		}
	}

	if strings.Contains(info.GitRemote, "github.com") {
		add("github")
	}
	if strings.Contains(info.GitRemote, "gitlab.com") {
		add("gitlab")
	}
	for _, ci := range detectRepoCIProviders(repoPath) {
		switch ci {
		case "github-actions":
			add("github")
		case "gitlab-ci":
			add("gitlab")
		}
	}
	if hasOpenAIIntegration(repoPath) {
		add("openai")
	}

	if hasYaverBackendIntegration(repoPath) {
		add("yaver-backend")
	}

	out := make([]string, 0, len(seen))
	for _, name := range []string{"yaver-backend", "github", "gitlab", "openai"} {
		if seen[name] {
			out = append(out, name)
		}
	}
	return out
}

func hasOpenAIIntegration(repoPath string) bool {
	candidates := []string{
		".env",
		".env.example",
		"package.json",
		"requirements.txt",
		"pyproject.toml",
		"go.mod",
	}
	for _, name := range candidates {
		path := filepath.Join(repoPath, name)
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		body := strings.ToLower(string(data))
		if strings.Contains(body, "openai") || strings.Contains(body, "openai_api_key") {
			return true
		}
	}
	return false
}

func hasYaverBackendIntegration(repoPath string) bool {
	if _, err := os.Stat(filepath.Join(repoPath, ".yaver", "config.yaml")); err == nil {
		return true
	}
	if _, err := os.Stat(filepath.Join(repoPath, ".yaver", "services.yaml")); err == nil {
		return true
	}
	for _, name := range []string{"schema.yaml", "auth.yaml", "seed.json"} {
		if _, err := os.Stat(filepath.Join(repoPath, name)); err == nil {
			return true
		}
	}
	return false
}

func detectRepoTopology(repoPath string, info ProjectInfo) map[string]interface{} {
	topology := map[string]interface{}{
		"projectStartsFrom": []string{"phone", "dev-machine"},
		"codingRunsOn":      []string{"phone", "dev-machine", "yaver-cloud"},
		"codingDefault":     "user-choice",
	}

	runners := detectCodingRunners(repoPath)
	topology["codingRunners"] = runners
	topology["supportsPhoneCoding"] = true
	topology["supportsRemoteCoding"] = true
	if len(runners) > 0 {
		topology["preferredCodingTargets"] = []string{"dev-machine", "yaver-cloud", "phone"}
	} else {
		topology["preferredCodingTargets"] = []string{"phone", "dev-machine", "yaver-cloud"}
	}

	if hasYaverBackendIntegration(repoPath) {
		topology["backendRunsOn"] = []string{"phone", "dev-machine", "yaver-cloud"}
		topology["backendDefault"] = "yaver-backend"
	} else {
		topology["backendRunsOn"] = []string{"project-defined"}
	}

	if info.Stack.Type == "monorepo" {
		topology["monorepo"] = true
	}
	return topology
}

func detectCodingRunners(workDir string) []map[string]interface{} {
	var out []map[string]interface{}
	for _, id := range []string{"claude", "codex", "opencode", "aider", "aider-ollama", "ollama"} {
		cfg := GetRunnerConfig(id)
		if cfg.RunnerID == "" || cfg.Command == "" {
			continue
		}
		if err := CheckRunnerBinary(cfg.Command); err != nil {
			continue
		}
		status := DetectRunnerRuntimeStatus(cfg, workDir)
		out = append(out, map[string]interface{}{
			"id":             cfg.RunnerID,
			"name":           cfg.Name,
			"ready":          status.Ready,
			"authConfigured": status.AuthConfigured,
			"authSource":     status.AuthSource,
			"warning":        status.Warning,
			"error":          status.Error,
		})
	}
	return out
}

// detectLanguages is defined in discovery.go — reused here for metadata generation.

// ---------------------------------------------------------------------------
// HTTP handlers
// ---------------------------------------------------------------------------

// handleGitProviderAutoDetect handles GET /git/provider/detect.
// Auto-detects GitHub/GitLab tokens from the dev machine's existing tooling (gh CLI, env vars, etc.)
// No input from mobile needed — the dev machine already has credentials.
func (s *HTTPServer) handleGitProviderAutoDetect(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "use GET or POST")
		return
	}

	type detectedProvider struct {
		Provider  string `json:"provider"`
		Host      string `json:"host"`
		Username  string `json:"username"`
		AvatarURL string `json:"avatarUrl,omitempty"`
		HasToken  bool   `json:"hasToken"`
	}

	var detected []detectedProvider

	// Try GitHub
	if token := detectGitHubToken(); token != "" {
		if username, avatar, err := verifyGitHubToken(token); err == nil {
			detected = append(detected, detectedProvider{
				Provider: "github", Host: "github.com",
				Username: username, AvatarURL: avatar, HasToken: true,
			})
			// Auto-save to providers if not already there
			saveDetectedProvider("github", "github.com", username, avatar, token)
		}
	}

	// Try GitLab
	if token := detectGitLabToken("gitlab.com"); token != "" {
		if username, avatar, err := verifyGitLabToken("gitlab.com", token); err == nil {
			detected = append(detected, detectedProvider{
				Provider: "gitlab", Host: "gitlab.com",
				Username: username, AvatarURL: avatar, HasToken: true,
			})
			saveDetectedProvider("gitlab", "gitlab.com", username, avatar, token)
		}
	}

	jsonReply(w, http.StatusOK, map[string]interface{}{
		"ok":        true,
		"providers": detected,
	})
}

// saveDetectedProvider saves an auto-detected provider (idempotent).
func saveDetectedProvider(provider, host, username, avatarURL, token string) {
	providers, _ := loadGitProviders()
	for _, p := range providers {
		if strings.EqualFold(p.Host, host) {
			return // already saved
		}
	}
	providers = append(providers, GitProvider{
		Host:      host,
		Provider:  provider,
		Username:  username,
		Token:     token,
		AvatarURL: avatarURL,
		SetupAt:   time.Now().UTC().Format(time.RFC3339),
	})
	saveGitProviders(providers)

	// Also save as git credential for clone
	creds, _ := loadGitCredentials()
	for _, c := range creds {
		if strings.EqualFold(c.Host, host) {
			return
		}
	}
	creds = append(creds, GitCredential{Host: host, Username: username, Token: token})
	saveGitCredentials(creds)

	log.Printf("[git-provider] Auto-detected %s: user=%s", provider, username)
}

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

	// Load every repo the user can see in one shot — paginating page
	// by page on the phone hides private repos that fall onto page 2
	// and confuses the search box (it could only filter what was
	// already loaded). Cap at 1000 (10 pages × 100) which covers
	// >99 % of users; anyone beyond that gets the search-box server
	// fallback.
	switch provider.Provider {
	case "github":
		repos, err = listGitHubReposPaged(provider.Token, 100, 10)
	case "gitlab":
		repos, err = listGitLabReposPaged(host, provider.Token, 100, 10)
	}

	if err != nil {
		jsonError(w, http.StatusBadGateway, "failed to list repos: "+err.Error())
		return
	}

	// Optional client-style filter on the server (kept for callers
	// that want to pre-narrow the response). Mobile filters
	// client-side over the full list now that we return everything.
	if search != "" {
		needle := strings.ToLower(search)
		var filtered []RemoteRepo
		for _, r := range repos {
			if strings.Contains(strings.ToLower(r.Name), needle) ||
				strings.Contains(strings.ToLower(r.FullName), needle) ||
				strings.Contains(strings.ToLower(r.Description), needle) {
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
		URL            string `json:"url"`
		Dir            string `json:"dir"`
		Branch         string `json:"branch"`
		AutoInit       bool   `json:"autoInit"`
		AutoInitRunner string `json:"autoInitRunner"`
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
	// Default to $HOME/Workspace (capital W) — matches the user's
	// macOS layout (~/Workspace/talos) + the existing project-
	// discovery scanner. On managed-cloud boxes lands at
	// /root/Workspace or /home/yaver/Workspace.
	targetDir := ResolveWorkspaceParent(req.Dir)
	if err := os.MkdirAll(targetDir, 0755); err != nil {
		jsonError(w, http.StatusInternalServerError, "cannot create directory: "+err.Error())
		return
	}

	clonePath := filepath.Join(targetDir, repoName)

	// Check if already cloned
	if _, err := os.Stat(filepath.Join(clonePath, ".git")); err == nil {
		meta := generateRepoMetadata(clonePath)
		var autoinitResp map[string]interface{}
		if req.AutoInit {
			resp, err := startAutoInitBackground(AutoInitStart{
				Project: filepath.Base(clonePath),
				WorkDir: clonePath,
				Runner:  req.AutoInitRunner,
			})
			if err != nil {
				autoinitResp = map[string]interface{}{
					"ok":      false,
					"started": false,
					"error":   err.Error(),
				}
			} else {
				autoinitResp = resp
			}
		}
		jsonReply(w, http.StatusOK, map[string]interface{}{
			"ok":             true,
			"path":           clonePath,
			"alreadyExisted": true,
			"metadata":       meta,
			"autoinit":       autoinitResp,
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

	// Credential hygiene: the HTTPS branch injected a token into the clone URL,
	// which git persists in .git/config. Reset origin to the token-free URL so a
	// tester/guest in this workdir can't read the owner's PAT. SSH clones carry
	// no token, so they're left untouched. See resetOriginToCleanURL.
	usedTokenURL := (provider == nil || provider.SSHKeyPath == "") && cloneURL != req.URL
	if usedTokenURL {
		resetOriginToCleanURL(context.Background(), clonePath, req.URL)
	}

	// Generate metadata
	meta := generateRepoMetadata(clonePath)
	var autoinitResp map[string]interface{}
	if req.AutoInit {
		resp, err := startAutoInitBackground(AutoInitStart{
			Project: filepath.Base(clonePath),
			WorkDir: clonePath,
			Runner:  req.AutoInitRunner,
		})
		if err != nil {
			autoinitResp = map[string]interface{}{
				"ok":      false,
				"started": false,
				"error":   err.Error(),
			}
		} else {
			autoinitResp = resp
		}
	}

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
		"autoinit": autoinitResp,
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

// ---------------------------------------------------------------------------
// Repo creation (POST /git-providers/repo/create)
//
// Mobile sandbox wizard's "Configure now" git path needs to actually
// create a repo on GitHub or GitLab when the user submits the new-app
// flow. Until this endpoint existed, gitMode was a preference we
// recorded but never acted on. Now we look up the matching provider
// (already authed via /git-providers/setup), call the provider's
// repo-create API, write a starter `yaver.workspace.yaml` so future
// Yaver tooling recognises the repo as a sandbox-aware workspace,
// commit + push that yaml, and return the clone URL the mobile flow
// can display + use for the project's gitRemote field.
//
// We deliberately DO NOT clone the repo here — the desktop agent
// already has /repos/clone for that, and the mobile sandbox might be
// for a phone-only project that never needs a local clone. Callers
// who want both should chain create + clone themselves.
// ---------------------------------------------------------------------------

func (s *HTTPServer) handleGitProviderRepoCreate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}

	var req struct {
		Provider     string `json:"provider"`     // "github" | "gitlab"
		Host         string `json:"host"`         // optional; defaults github.com / gitlab.com
		Name         string `json:"name"`         // repo name (slugified)
		Visibility   string `json:"visibility"`   // "private" | "public"
		Description  string `json:"description"`  // optional one-liner
		WriteSandbox bool   `json:"writeSandbox"` // commit yaver.workspace.yaml
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	req.Provider = strings.ToLower(strings.TrimSpace(req.Provider))
	req.Name = strings.TrimSpace(req.Name)
	req.Visibility = strings.ToLower(strings.TrimSpace(req.Visibility))
	if req.Provider != "github" && req.Provider != "gitlab" {
		jsonError(w, http.StatusBadRequest, "provider must be 'github' or 'gitlab'")
		return
	}
	if req.Name == "" {
		jsonError(w, http.StatusBadRequest, "name is required")
		return
	}
	if req.Visibility != "private" && req.Visibility != "public" {
		req.Visibility = "private"
	}
	host := req.Host
	if host == "" {
		host = req.Provider + ".com"
	}

	// Look up the stored token for this provider+host. The user
	// must have run /git-providers/setup first.
	providers, err := loadGitProviders()
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "git-providers store: "+err.Error())
		return
	}
	var token, username string
	for _, p := range providers {
		if p.Provider == req.Provider && p.Host == host {
			token = p.Token
			username = p.Username
			break
		}
	}
	if token == "" {
		jsonError(w, http.StatusPreconditionFailed,
			"no "+req.Provider+" token configured on this machine — run /git-providers/setup first")
		return
	}

	private := req.Visibility == "private"
	var cloneURL, sshURL, fullName string
	switch req.Provider {
	case "github":
		cloneURL, sshURL, fullName, err = createRepoOnGitHub(token, req.Name, req.Description, private)
	case "gitlab":
		cloneURL, sshURL, fullName, err = createRepoOnGitLab(host, token, req.Name, req.Description, private)
	}
	if err != nil {
		jsonError(w, http.StatusBadGateway, req.Provider+" repo create failed: "+err.Error())
		return
	}

	// Best-effort: commit + push a starter yaver.workspace.yaml so
	// the repo is born sandbox-aware. We do this via a temp clone
	// because the GitHub/GitLab APIs don't expose a single-file
	// commit endpoint that's symmetrical across providers, and a
	// shallow clone is < 1 MB for a brand-new repo.
	var sandboxWritten bool
	if req.WriteSandbox {
		if err := seedSandboxWorkspaceYaml(req.Provider, host, username, token, fullName, req.Name); err != nil {
			log.Printf("[git-provider] yaver.workspace.yaml seed failed for %s: %v", fullName, err)
		} else {
			sandboxWritten = true
		}
	}

	jsonReply(w, http.StatusOK, map[string]interface{}{
		"ok":             true,
		"cloneUrl":       cloneURL,
		"sshUrl":         sshURL,
		"fullName":       fullName,
		"provider":       req.Provider,
		"host":           host,
		"private":        private,
		"sandboxWritten": sandboxWritten,
	})
}

func createRepoOnGitHub(token, name, description string, private bool) (cloneURL, sshURL, fullName string, err error) {
	body := map[string]interface{}{
		"name":        name,
		"description": description,
		"private":     private,
		"auto_init":   true,
	}
	buf, _ := json.Marshal(body)
	req, _ := http.NewRequest("POST", "https://api.github.com/user/repos", strings.NewReader(string(buf)))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", "", "", err
	}
	defer resp.Body.Close()
	bodyBytes, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", "", "", fmt.Errorf("github %d: %s", resp.StatusCode, string(bodyBytes))
	}
	var data struct {
		FullName string `json:"full_name"`
		CloneURL string `json:"clone_url"`
		SSHURL   string `json:"ssh_url"`
	}
	if err := json.Unmarshal(bodyBytes, &data); err != nil {
		return "", "", "", err
	}
	return data.CloneURL, data.SSHURL, data.FullName, nil
}

func createRepoOnGitLab(host, token, name, description string, private bool) (cloneURL, sshURL, fullName string, err error) {
	visibility := "public"
	if private {
		visibility = "private"
	}
	body := map[string]interface{}{
		"name":                   name,
		"description":            description,
		"visibility":             visibility,
		"initialize_with_readme": true,
	}
	buf, _ := json.Marshal(body)
	req, _ := http.NewRequest("POST", "https://"+host+"/api/v4/projects", strings.NewReader(string(buf)))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", "", "", err
	}
	defer resp.Body.Close()
	bodyBytes, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", "", "", fmt.Errorf("gitlab %d: %s", resp.StatusCode, string(bodyBytes))
	}
	var data struct {
		PathWithNamespace string `json:"path_with_namespace"`
		HTTPURLToRepo     string `json:"http_url_to_repo"`
		SSHURLToRepo      string `json:"ssh_url_to_repo"`
	}
	if err := json.Unmarshal(bodyBytes, &data); err != nil {
		return "", "", "", err
	}
	return data.HTTPURLToRepo, data.SSHURLToRepo, data.PathWithNamespace, nil
}

// seedSandboxWorkspaceYaml clones the freshly-created repo into a
// temp dir, writes yaver.workspace.yaml + a one-line README addition
// flagging the project as Yaver-sandbox-aware, commits + pushes,
// then removes the temp dir. Best-effort: if any step fails we just
// log and let the caller decide. The user can always edit the repo
// directly to add the file later.
func seedSandboxWorkspaceYaml(provider, host, username, token, fullName, projectName string) error {
	tmp, err := os.MkdirTemp("", "yaver-repo-seed-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)

	// HTTPS clone with the PAT inline. We strip it again before
	// running git push so the token never ends up in `git config
	// remote.origin.url`.
	var cloneURL string
	switch provider {
	case "github":
		cloneURL = fmt.Sprintf("https://%s:%s@%s/%s.git", username, token, host, fullName)
	case "gitlab":
		cloneURL = fmt.Sprintf("https://oauth2:%s@%s/%s.git", token, host, fullName)
	}

	cloneCmd := osexec.Command("git", "clone", "--depth=1", cloneURL, tmp)
	if out, err := cloneCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("clone: %s — %v", string(out), err)
	}

	yamlPath := filepath.Join(tmp, "yaver.workspace.yaml")
	yamlBody := buildSandboxWorkspaceYaml(projectName)
	if err := os.WriteFile(yamlPath, []byte(yamlBody), 0644); err != nil {
		return err
	}

	addCmd := osexec.Command("git", "-C", tmp, "add", "yaver.workspace.yaml")
	if out, err := addCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git add: %s — %v", string(out), err)
	}

	cfgUserName := osexec.Command("git", "-C", tmp, "config", "user.name", "Yaver Agent")
	_ = cfgUserName.Run()
	cfgUserEmail := osexec.Command("git", "-C", tmp, "config", "user.email", "agent@yaver.io")
	_ = cfgUserEmail.Run()

	commitCmd := osexec.Command("git", "-C", tmp, "commit", "-m", "yaver: mark workspace as sandbox-aware")
	if out, err := commitCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git commit: %s — %v", string(out), err)
	}

	pushCmd := osexec.Command("git", "-C", tmp, "push", "origin", "HEAD")
	if out, err := pushCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git push: %s — %v", string(out), err)
	}

	return nil
}

// buildSandboxWorkspaceYaml returns the yaml body that marks a repo
// as Yaver-mobile-sandboxable. The schema lines up with
// yaver.workspace.yaml's existing manifest format (the agent's
// workspace.go parses these), with an extra mobile_sandbox section
// the mobile wizard reads when it lists "existing sandbox projects"
// across the user's repos. Single app for now (the project itself);
// extra apps can be added later by the user or by Yaver's auto-
// detection on next workspace init.
func buildSandboxWorkspaceYaml(projectName string) string {
	return fmt.Sprintf(`# yaver.workspace.yaml — generated by Yaver mobile sandbox wizard.
# This repo is registered as a Yaver mobile-sandboxable project.
# Edit freely; the only field Yaver reads to recognise it is
# mobile_sandbox.enabled.
mobile_sandbox:
  enabled: true
  origin: yaver-mobile-wizard
apps:
  - name: %s
    path: .
    stack: auto-detect
`, projectName)
}
