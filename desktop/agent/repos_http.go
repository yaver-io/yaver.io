package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	osexec "os/exec"
	"path/filepath"
	"strings"
	"time"
)

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

// GitCredential holds a PAT for a git host. Stored locally in
// ~/.yaver/git-credentials.json with 0600 permissions — never sent to Convex.
type GitCredential struct {
	Host     string `json:"host"`
	Username string `json:"username"`
	Token    string `json:"token"`
}

// RepoInfo describes a git repository discovered on the dev machine.
type RepoInfo struct {
	Name       string    `json:"name"`
	Path       string    `json:"path"`
	Branch     string    `json:"branch"`
	Remote     string    `json:"remote"`
	LastCommit string    `json:"lastCommit"`
	Dirty      bool      `json:"dirty"`
	Stack      RepoStack `json:"stack,omitempty"`
}

// RepoStack describes the detected software stack of a repository.
// Lightweight detection: only checks for marker files, never reads file contents.
type RepoStack struct {
	Type       string   `json:"type"`                 // "mobile", "web", "backend", "monorepo", "library", "cli"
	Frameworks []string `json:"frameworks,omitempty"`  // e.g. ["expo", "react-native", "next.js"]
	Services   []string `json:"services,omitempty"`    // e.g. ["convex", "supabase", "firebase"]
	Actions    []string `json:"actions,omitempty"`     // e.g. ["hot-reload", "convex-deploy", "vercel-deploy"]
}

// ---------------------------------------------------------------------------
// Credentials file helpers
// ---------------------------------------------------------------------------

func gitCredsPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".yaver", "git-credentials.json")
}

func loadGitCredentials() ([]GitCredential, error) {
	data, err := os.ReadFile(gitCredsPath())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var creds []GitCredential
	if err := json.Unmarshal(data, &creds); err != nil {
		return nil, err
	}
	return creds, nil
}

func saveGitCredentials(creds []GitCredential) error {
	path := gitCredsPath()
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(creds, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0600)
}

func findCredentialForHost(host string) *GitCredential {
	creds, err := loadGitCredentials()
	if err != nil || len(creds) == 0 {
		return nil
	}
	for _, c := range creds {
		if strings.EqualFold(c.Host, host) {
			return &c
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// repoNameFromURL extracts the repository name from a git clone URL.
// Handles both HTTPS and SSH URLs, stripping the .git suffix.
func repoNameFromURL(rawURL string) string {
	// Strip trailing .git
	rawURL = strings.TrimSuffix(rawURL, ".git")

	// SSH: git@github.com:user/repo
	if strings.Contains(rawURL, ":") && !strings.Contains(rawURL, "://") {
		parts := strings.Split(rawURL, "/")
		return parts[len(parts)-1]
	}

	// HTTPS
	parsed, err := url.Parse(rawURL)
	if err != nil {
		// Fallback: last path segment
		parts := strings.Split(rawURL, "/")
		return parts[len(parts)-1]
	}
	parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	if len(parts) == 0 {
		return "repo"
	}
	return parts[len(parts)-1]
}

// hostFromURL extracts the hostname from a git clone URL.
func hostFromURL(rawURL string) string {
	// SSH: git@github.com:user/repo
	if strings.Contains(rawURL, "@") && strings.Contains(rawURL, ":") && !strings.Contains(rawURL, "://") {
		at := strings.Index(rawURL, "@")
		colon := strings.Index(rawURL[at:], ":")
		if colon > 0 {
			return rawURL[at+1 : at+colon]
		}
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	return parsed.Hostname()
}

// injectCredentials returns a clone URL with embedded credentials for HTTPS URLs.
// For SSH URLs or when no credential is found, returns the URL unchanged.
func injectCredentials(cloneURL string) string {
	host := hostFromURL(cloneURL)
	if host == "" {
		return cloneURL
	}
	cred := findCredentialForHost(host)
	if cred == nil || cred.Token == "" {
		return cloneURL
	}
	// Only inject into HTTPS URLs
	parsed, err := url.Parse(cloneURL)
	if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") {
		return cloneURL
	}
	username := cred.Username
	if username == "" {
		username = "x-access-token"
	}
	parsed.User = url.UserPassword(username, cred.Token)
	return parsed.String()
}

// scanDirForRepos finds git repositories one level deep in dir.
func scanDirForRepos(dir string) []RepoInfo {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var repos []RepoInfo
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		repoPath := filepath.Join(dir, e.Name())
		gitDir := filepath.Join(repoPath, ".git")
		if _, err := os.Stat(gitDir); err != nil {
			continue
		}
		info := getRepoInfo(repoPath, e.Name())
		repos = append(repos, info)
	}
	return repos
}

func getRepoInfo(repoPath, name string) RepoInfo {
	info := RepoInfo{
		Name: name,
		Path: repoPath,
	}
	// Lightweight: only get branch (fast, single git call). Skip log/status/remote for speed.
	if branch, err := runGit(repoPath, "rev-parse", "--abbrev-ref", "HEAD"); err == nil {
		info.Branch = branch
	}
	// Stack detection is cheap — just os.Stat calls, no file reads except package.json
	info.Stack = detectStack(repoPath)
	return info
}

// detectStack does lightweight project stack detection by checking marker files.
// Never reads file contents — only checks for file/directory existence.
func detectStack(dir string) RepoStack {
	s := RepoStack{}
	has := func(rel string) bool {
		_, err := os.Stat(filepath.Join(dir, rel))
		return err == nil
	}

	// Frameworks
	if has("app.json") && has("node_modules/expo") {
		s.Frameworks = append(s.Frameworks, "expo")
	} else if has("app.json") && has("package.json") {
		// Could be Expo without node_modules yet
		if has("babel.config.js") || has("metro.config.js") {
			s.Frameworks = append(s.Frameworks, "expo")
		}
	}
	if has("android/build.gradle") || has("ios/Podfile") {
		s.Frameworks = append(s.Frameworks, "react-native")
	}
	if has("pubspec.yaml") {
		s.Frameworks = append(s.Frameworks, "flutter")
	}
	if has("next.config.js") || has("next.config.ts") || has("next.config.mjs") {
		s.Frameworks = append(s.Frameworks, "next.js")
	}
	if has("vite.config.ts") || has("vite.config.js") || has("vite.config.mjs") {
		s.Frameworks = append(s.Frameworks, "vite")
	}
	if has("nuxt.config.ts") || has("nuxt.config.js") {
		s.Frameworks = append(s.Frameworks, "nuxt")
	}
	if has("svelte.config.js") || has("svelte.config.ts") {
		s.Frameworks = append(s.Frameworks, "svelte")
	}
	if has("go.mod") {
		s.Frameworks = append(s.Frameworks, "go")
	}
	if has("Cargo.toml") {
		s.Frameworks = append(s.Frameworks, "rust")
	}
	if has("requirements.txt") || has("pyproject.toml") || has("setup.py") {
		s.Frameworks = append(s.Frameworks, "python")
	}

	// Services / backends
	if has("convex/") || has("convex.json") {
		s.Services = append(s.Services, "convex")
	}
	if has("supabase/") || has("supabase/config.toml") {
		s.Services = append(s.Services, "supabase")
	}
	if has("firebase.json") || has(".firebaserc") {
		s.Services = append(s.Services, "firebase")
	}
	if has("vercel.json") || has(".vercel/") {
		s.Services = append(s.Services, "vercel")
	}
	if has("netlify.toml") {
		s.Services = append(s.Services, "netlify")
	}
	if has("docker-compose.yml") || has("docker-compose.yaml") || has("Dockerfile") {
		s.Services = append(s.Services, "docker")
	}
	if has("prisma/") || has("prisma/schema.prisma") {
		s.Services = append(s.Services, "prisma")
	}
	if has("drizzle.config.ts") || has("drizzle/") {
		s.Services = append(s.Services, "drizzle")
	}

	// Determine type
	isMonorepo := has("pnpm-workspace.yaml") || has("lerna.json") || has("turbo.json") ||
		(has("packages/") && has("package.json"))
	hasMobile := containsAny(s.Frameworks, "expo", "react-native", "flutter")
	hasWeb := containsAny(s.Frameworks, "next.js", "vite", "nuxt", "svelte")

	switch {
	case isMonorepo:
		s.Type = "monorepo"
	case hasMobile && hasWeb:
		s.Type = "monorepo"
	case hasMobile:
		s.Type = "mobile"
	case hasWeb:
		s.Type = "web"
	case containsAny(s.Frameworks, "go", "rust", "python"):
		s.Type = "backend"
	default:
		s.Type = "unknown"
	}

	// Derive available actions from detected stack
	// Hot reload only for React Native/Expo (runs inside Yaver app with native access)
	if containsAny(s.Frameworks, "expo", "react-native") {
		s.Actions = append(s.Actions, "hot-reload")
	}
	if containsAny(s.Services, "convex") {
		s.Actions = append(s.Actions, "convex-deploy")
	}
	if containsAny(s.Services, "supabase") {
		s.Actions = append(s.Actions, "supabase-deploy")
	}
	if containsAny(s.Services, "firebase") {
		s.Actions = append(s.Actions, "firebase-deploy")
	}
	if containsAny(s.Services, "vercel") {
		s.Actions = append(s.Actions, "vercel-deploy")
	}
	if containsAny(s.Services, "netlify") {
		s.Actions = append(s.Actions, "netlify-deploy")
	}
	if containsAny(s.Services, "docker") {
		s.Actions = append(s.Actions, "docker-build")
	}
	if hasWeb {
		s.Actions = append(s.Actions, "dev-server")
	}

	return s
}

func containsAny(slice []string, items ...string) bool {
	for _, s := range slice {
		for _, item := range items {
			if s == item {
				return true
			}
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// HTTP handlers
// ---------------------------------------------------------------------------

// handleRepoClone handles POST /repos/clone.
func (s *HTTPServer) handleRepoClone(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}

	var req struct {
		URL    string `json:"url"`
		Dir    string `json:"dir"`
		Branch string `json:"branch"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if req.URL == "" {
		jsonError(w, http.StatusBadRequest, "url is required")
		return
	}

	// Default directory: ~/Projects/{repoName}
	repoName := repoNameFromURL(req.URL)
	targetDir := req.Dir
	if targetDir == "" {
		home, _ := os.UserHomeDir()
		targetDir = filepath.Join(home, "Projects")
	}

	// Ensure parent directory exists
	if err := os.MkdirAll(targetDir, 0755); err != nil {
		jsonError(w, http.StatusInternalServerError, "cannot create directory: "+err.Error())
		return
	}

	clonePath := filepath.Join(targetDir, repoName)

	// Build git clone command
	args := []string{"clone"}
	if req.Branch != "" {
		args = append(args, "-b", req.Branch)
	}
	cloneURL := injectCredentials(req.URL)
	args = append(args, cloneURL, clonePath)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	cmd := osexec.CommandContext(ctx, "git", args...)
	out, err := cmd.CombinedOutput()
	output := strings.TrimSpace(string(out))

	if err != nil {
		// Sanitise output — never leak credentials in error messages
		sanitised := strings.ReplaceAll(output, cloneURL, req.URL)
		jsonError(w, http.StatusInternalServerError, "git clone failed: "+sanitised)
		return
	}

	jsonReply(w, http.StatusOK, map[string]interface{}{
		"ok":     true,
		"path":   clonePath,
		"output": strings.ReplaceAll(output, cloneURL, req.URL),
	})
}

// handleRepoPull handles POST /repos/pull.
func (s *HTTPServer) handleRepoPull(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}

	var req struct {
		WorkDir string `json:"workDir"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	workDir := req.WorkDir
	if workDir == "" && s.taskMgr != nil {
		workDir = s.taskMgr.workDir
	}
	if workDir == "" {
		jsonError(w, http.StatusBadRequest, "workDir is required")
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	cmd := osexec.CommandContext(ctx, "git", "pull")
	cmd.Dir = workDir
	out, err := cmd.CombinedOutput()
	output := strings.TrimSpace(string(out))

	if err != nil {
		jsonError(w, http.StatusInternalServerError, "git pull failed: "+output)
		return
	}

	// Get current branch
	branch := ""
	if b, err := runGit(workDir, "rev-parse", "--abbrev-ref", "HEAD"); err == nil {
		branch = b
	}

	jsonReply(w, http.StatusOK, map[string]interface{}{
		"ok":     true,
		"output": output,
		"branch": branch,
	})
}

// handleRepoList handles GET /repos/list.
func (s *HTTPServer) handleRepoList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, http.StatusMethodNotAllowed, "use GET")
		return
	}

	home, _ := os.UserHomeDir()
	scanDirs := []string{
		filepath.Join(home, "Projects"),
		filepath.Join(home, "Workspace"),
		filepath.Join(home, "repos"),
		filepath.Join(home, "code"),
		filepath.Join(home, "src"),
		filepath.Join(home, "dev"),
	}
	// Add agent workDir if set
	if s.taskMgr != nil && s.taskMgr.workDir != "" {
		scanDirs = append(scanDirs, s.taskMgr.workDir)
	}

	seen := make(map[string]bool)
	var repos []RepoInfo

	for _, dir := range scanDirs {
		for _, repo := range scanDirForRepos(dir) {
			if !seen[repo.Path] {
				seen[repo.Path] = true
				repos = append(repos, repo)
			}
		}
	}

	// Also check if the workDir itself is a git repo
	if s.taskMgr != nil && s.taskMgr.workDir != "" {
		wd := s.taskMgr.workDir
		gitDir := filepath.Join(wd, ".git")
		if _, err := os.Stat(gitDir); err == nil && !seen[wd] {
			repos = append(repos, getRepoInfo(wd, filepath.Base(wd)))
		}
	}

	if repos == nil {
		repos = []RepoInfo{}
	}

	jsonReply(w, http.StatusOK, repos)
}

// handleRepoCredentials handles POST and GET on /repos/credentials.
func (s *HTTPServer) handleRepoCredentials(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.handleRepoCredentialsList(w, r)
	case http.MethodPost:
		s.handleRepoCredentialsSet(w, r)
	default:
		jsonError(w, http.StatusMethodNotAllowed, "use GET or POST")
	}
}

func (s *HTTPServer) handleRepoCredentialsList(w http.ResponseWriter, _ *http.Request) {
	creds, err := loadGitCredentials()
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "failed to load credentials: "+err.Error())
		return
	}

	// Return without tokens
	type safeEntry struct {
		Host     string `json:"host"`
		Username string `json:"username"`
		HasToken bool   `json:"hasToken"`
	}
	result := make([]safeEntry, 0, len(creds))
	for _, c := range creds {
		result = append(result, safeEntry{
			Host:     c.Host,
			Username: c.Username,
			HasToken: c.Token != "",
		})
	}

	jsonReply(w, http.StatusOK, result)
}

func (s *HTTPServer) handleRepoCredentialsSet(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Host     string `json:"host"`
		Token    string `json:"token"`
		Username string `json:"username"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if req.Host == "" || req.Token == "" {
		jsonError(w, http.StatusBadRequest, "host and token are required")
		return
	}

	creds, err := loadGitCredentials()
	if err != nil {
		creds = nil
	}

	// Update existing or append
	found := false
	for i := range creds {
		if strings.EqualFold(creds[i].Host, req.Host) {
			creds[i].Token = req.Token
			if req.Username != "" {
				creds[i].Username = req.Username
			}
			found = true
			break
		}
	}
	if !found {
		creds = append(creds, GitCredential{
			Host:     req.Host,
			Username: req.Username,
			Token:    req.Token,
		})
	}

	if err := saveGitCredentials(creds); err != nil {
		jsonError(w, http.StatusInternalServerError, "failed to save credentials: "+err.Error())
		return
	}

	jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true})
}

// handleRepoCredentialByHost handles DELETE /repos/credentials/{host}.
func (s *HTTPServer) handleRepoCredentialByHost(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		jsonError(w, http.StatusMethodNotAllowed, "use DELETE")
		return
	}

	host := strings.TrimPrefix(r.URL.Path, "/repos/credentials/")
	host, _ = url.PathUnescape(host)
	if host == "" {
		jsonError(w, http.StatusBadRequest, "host is required")
		return
	}

	creds, err := loadGitCredentials()
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "failed to load credentials: "+err.Error())
		return
	}

	filtered := make([]GitCredential, 0, len(creds))
	found := false
	for _, c := range creds {
		if strings.EqualFold(c.Host, host) {
			found = true
			continue
		}
		filtered = append(filtered, c)
	}

	if !found {
		jsonError(w, http.StatusNotFound, fmt.Sprintf("no credential for host %q", host))
		return
	}

	if err := saveGitCredentials(filtered); err != nil {
		jsonError(w, http.StatusInternalServerError, "failed to save credentials: "+err.Error())
		return
	}

	jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true})
}
