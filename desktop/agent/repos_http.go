package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	osexec "os/exec"
	"path/filepath"
	"strings"
	"sync"
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
	Frameworks []string `json:"frameworks,omitempty"` // e.g. ["expo", "react-native", "next.js"]
	Services   []string `json:"services,omitempty"`   // e.g. ["convex", "supabase", "firebase"]
	Actions    []string `json:"actions,omitempty"`    // e.g. ["hot-reload", "convex-deploy", "cloudflare-deploy"]
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

// stripURLCredentials returns the URL with any embedded userinfo (user:token@)
// removed. Non-URL inputs (e.g. SSH scp-style git@host:path) are returned
// unchanged. Used to keep a cloned repo's origin token-free.
func stripURLCredentials(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.User == nil {
		return raw
	}
	// Only rewrite http(s) — SSH scp-style URLs (git@host:path) have no
	// embedded token and can confuse url.Parse; leave them untouched.
	if parsed.Scheme != "https" && parsed.Scheme != "http" {
		return raw
	}
	parsed.User = nil
	return parsed.String()
}

// resetOriginToCleanURL rewrites a freshly-cloned repo's origin remote to a
// token-free URL. `git clone https://user:token@host/…` persists the token in
// .git/config remote.origin.url; leaving it there lets anything that can read
// the working tree — a tester/guest task, a shared container — lift the owner's
// git PAT. Fetch/pull re-inject credentials per-operation (handleRepoPull), so
// origin never needs to carry the token. Non-fatal: the clone already
// succeeded, so a failure here is logged, not surfaced.
func resetOriginToCleanURL(ctx context.Context, clonePath, cleanURL string) {
	clean := stripURLCredentials(cleanURL)
	cmd := osexec.CommandContext(ctx, "git", "-C", clonePath, "remote", "set-url", "origin", clean)
	if out, err := cmd.CombinedOutput(); err != nil {
		log.Printf("[repos] warning: could not strip credentials from %s origin: %v (%s)",
			clonePath, err, strings.TrimSpace(string(out)))
	}
}

// gitRemoteURL returns the configured URL for a remote (e.g. "origin"), or ""
// if the command fails. Used by pull to re-inject credentials without ever
// persisting the token back into .git/config.
func gitRemoteURL(workDir, remote string) string {
	out, err := osexec.Command("git", "-C", workDir, "remote", "get-url", remote).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// errNothingToCommit signals a no-op save (the tester ran vibe but left no
// change). Callers surface it as a friendly "nothing to save", not an error.
var errNothingToCommit = fmt.Errorf("nothing to commit")

// commitAndPushGuestVibe stages everything in a tester's project, commits it to
// the current branch (straight onto the branch — no PR), attributed to the
// friend, and best-effort pushes to origin (re-injecting creds per-op, never
// persisting them). Returns the new commit sha + whether the push landed.
// errNothingToCommit when the working tree is clean. Uses the shared runGit
// (git_http.go), which runs in `dir` with its own timeout.
func commitAndPushGuestVibe(dir, authorName, authorEmail, message string) (sha string, pushed bool, err error) {
	if strings.TrimSpace(dir) == "" {
		return "", false, fmt.Errorf("no project directory")
	}
	if out, e := runGit(dir, "add", "-A"); e != nil {
		return "", false, fmt.Errorf("git add: %v (%s)", e, out)
	}
	if status, _ := runGit(dir, "status", "--porcelain"); strings.TrimSpace(status) == "" {
		return "", false, errNothingToCommit
	}
	author := fmt.Sprintf("%s <%s>", authorName, authorEmail)
	if out, e := runGit(dir,
		"-c", "user.name="+authorName, "-c", "user.email="+authorEmail,
		"commit", "-m", message, "--author", author); e != nil {
		return "", false, fmt.Errorf("git commit: %v (%s)", e, out)
	}
	sha, _ = runGit(dir, "rev-parse", "HEAD")
	// Best-effort push — a self-hosted box may have no remote configured, and
	// a failed push shouldn't lose the local commit. Re-inject creds per-op.
	if origin := gitRemoteURL(dir, "origin"); origin != "" {
		pushArgs := []string{"push", "origin", "HEAD"}
		if inj := injectCredentials(origin); inj != origin {
			pushArgs = []string{"push", inj, "HEAD"}
		}
		if _, e := runGit(dir, pushArgs...); e == nil {
			pushed = true
		}
	}
	return sha, pushed, nil
}

func allowedRepoRootsForTask(taskWorkDir string) []string {
	home, _ := os.UserHomeDir()
	roots := []string{
		filepath.Join(home, "Projects"),
		filepath.Join(home, "Workspace"),
		filepath.Join(home, "repos"),
		filepath.Join(home, "code"),
		filepath.Join(home, "src"),
		filepath.Join(home, "dev"),
	}
	if strings.TrimSpace(taskWorkDir) != "" {
		roots = append(roots, taskWorkDir)
	}
	return roots
}

func isPathWithinRoot(path, root string) bool {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(absRoot, absPath)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

func repoInfoForPath(repoPath string) (RepoInfo, error) {
	info, err := os.Stat(repoPath)
	if err != nil {
		return RepoInfo{}, err
	}
	if !info.IsDir() {
		return RepoInfo{}, fmt.Errorf("path is not a directory")
	}
	if _, err := os.Stat(filepath.Join(repoPath, ".git")); err != nil {
		return RepoInfo{}, fmt.Errorf("path is not a git repository root")
	}
	return getRepoInfo(repoPath, filepath.Base(repoPath)), nil
}

func guestCanAccessRepoPath(s *HTTPServer, guestUID, repoPath string) bool {
	if strings.TrimSpace(guestUID) == "" || s.guestConfigMgr == nil {
		return true
	}
	repo, err := repoInfoForPath(repoPath)
	if err != nil {
		return false
	}
	if s.guestConfigMgr.GuestCanAccessProject(guestUID, repo.Name) {
		return true
	}
	base := filepath.Base(repo.Path)
	return s.guestConfigMgr.GuestCanAccessProject(guestUID, base)
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
	if has("wrangler.toml") || has("wrangler.jsonc") {
		s.Services = append(s.Services, "cloudflare")
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
	if containsAny(s.Services, "cloudflare") {
		s.Actions = append(s.Actions, "cloudflare-deploy")
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
	guestUID := r.Header.Get("X-Yaver-GuestUserID")
	if guestUID != "" && s.guestConfigMgr != nil {
		repoName := repoNameFromURL(req.URL)
		if !s.guestConfigMgr.GuestCanAccessProject(guestUID, repoName) {
			jsonError(w, http.StatusForbidden, fmt.Sprintf("repo %q is not accessible for this guest", repoName))
			return
		}
	}

	// Default directory: $HOME/Workspace/{repoName} — matches
	// kivanc's macOS layout (~/Workspace/talos) + the existing
	// project-discovery scanner. ResolveWorkspaceParent handles
	// managed-cloud cases (/root/Workspace, /home/yaver/Workspace)
	// and falls back to cwd if HOME resolution dies.
	repoName := repoNameFromURL(req.URL)
	targetDir := ResolveWorkspaceParent(req.Dir)

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

	// Credential hygiene: when a token was injected into the clone URL, git
	// persisted it in .git/config. Reset origin to the token-free URL so a
	// tester/guest running in this workdir (or a shared container) can't lift
	// the owner's git PAT out of .git/config. Fetch/pull re-inject per-op.
	if cloneURL != req.URL {
		resetOriginToCleanURL(ctx, clonePath, req.URL)
	}

	// Invalidate project/repo caches so the mobile Hot Reload list
	// reflects the just-cloned project on its next poll (within 2.5s
	// while scanning, vs. a 10-minute wait otherwise).
	invalidateProjectCaches()

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
	guestUID := r.Header.Get("X-Yaver-GuestUserID")
	if guestUID != "" && !guestCanAccessRepoPath(s, guestUID, workDir) {
		jsonError(w, http.StatusForbidden, "repo not accessible for this guest")
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	// Origin is stored token-free (see resetOriginToCleanURL), so re-inject
	// credentials for this operation only — pass the tokenised URL as an arg
	// so it never persists back into .git/config. Public repos / SSH origins
	// return an unchanged URL and pull from origin as before.
	pullArgs := []string{"pull"}
	pullURL := ""
	if originURL := gitRemoteURL(workDir, "origin"); originURL != "" {
		if injected := injectCredentials(originURL); injected != originURL {
			pullURL = injected
			pullArgs = []string{"pull", injected}
		}
	}
	cmd := osexec.CommandContext(ctx, "git", pullArgs...)
	cmd.Dir = workDir
	out, err := cmd.CombinedOutput()
	output := strings.TrimSpace(string(out))
	if pullURL != "" {
		// Never leak the injected token in the response.
		output = strings.ReplaceAll(output, pullURL, stripURLCredentials(pullURL))
	}

	if err != nil {
		jsonError(w, http.StatusInternalServerError, "git pull failed: "+output)
		return
	}

	// Get current branch
	branch := ""
	if b, err := runGit(workDir, "rev-parse", "--abbrev-ref", "HEAD"); err == nil {
		branch = b
	}

	// A pull can introduce a new package.json / pubspec.yaml that
	// flips a repo into a mobile-project category, so refresh caches.
	invalidateProjectCaches()

	jsonReply(w, http.StatusOK, map[string]interface{}{
		"ok":     true,
		"output": output,
		"branch": branch,
	})
}

// handleRepoDelete handles POST /repos/delete and deletes the checked-out
// source tree from this machine. This is actual remote source deletion, not
// a metadata detach.
func (s *HTTPServer) handleRepoDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}

	var req struct {
		Path string `json:"path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if strings.TrimSpace(req.Path) == "" {
		jsonError(w, http.StatusBadRequest, "path is required")
		return
	}

	repoPath, err := filepath.Abs(req.Path)
	if err != nil {
		jsonError(w, http.StatusBadRequest, "invalid path: "+err.Error())
		return
	}
	if _, err := repoInfoForPath(repoPath); err != nil {
		jsonError(w, http.StatusBadRequest, err.Error())
		return
	}

	taskWorkDir := ""
	if s.taskMgr != nil {
		taskWorkDir = s.taskMgr.workDir
	}
	allowed := false
	for _, root := range allowedRepoRootsForTask(taskWorkDir) {
		if root != "" && isPathWithinRoot(repoPath, root) {
			allowed = true
			break
		}
	}
	if !allowed {
		jsonError(w, http.StatusForbidden, "refusing to delete repo outside allowed roots")
		return
	}

	guestUID := r.Header.Get("X-Yaver-GuestUserID")
	if guestUID != "" && !guestCanAccessRepoPath(s, guestUID, repoPath) {
		jsonError(w, http.StatusForbidden, "repo not accessible for this guest")
		return
	}

	if err := os.RemoveAll(repoPath); err != nil {
		jsonError(w, http.StatusInternalServerError, "delete repo: "+err.Error())
		return
	}
	invalidateProjectCaches()
	jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true, "path": repoPath})
}

// Repo list cache — avoid re-scanning directories every request.
// Cache is invalidated when any scanned directory has a newer mod time.
var repoCache struct {
	mu       sync.Mutex
	repos    []RepoInfo
	dirTimes map[string]time.Time // dir path → last mod time at scan
	cachedAt time.Time
}

// invalidateProjectCaches flips every project/repo cache to stale and
// kicks off a background mobile-project rescan. Called after git
// mutations (clone / pull) so the mobile Hot Reload tab's next poll
// gets fresh data — the 15s / 2.5s-while-scanning cadence then picks
// up the new project within a few seconds instead of waiting out the
// 10-minute scan TTL.
func invalidateProjectCaches() {
	repoCache.mu.Lock()
	repoCache.repos = nil
	repoCache.cachedAt = time.Time{}
	repoCache.dirTimes = nil
	repoCache.mu.Unlock()

	// Mark the mobile cache as scanning so the mobile client switches
	// to its fast-poll cadence immediately; the goroutine refreshes
	// the results in place.
	mobileProjectCache.mu.Lock()
	mobileProjectCache.scannedAt = time.Time{}
	mobileProjectCache.scanning = true
	mobileProjectCache.mu.Unlock()

	go func() {
		projects := scanMobileProjects()
		mobileProjectCache.mu.Lock()
		mobileProjectCache.projects = projects
		mobileProjectCache.scannedAt = time.Now()
		mobileProjectCache.scanning = false
		mobileProjectCache.mu.Unlock()
	}()
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
	if s.taskMgr != nil && s.taskMgr.workDir != "" {
		scanDirs = append(scanDirs, s.taskMgr.workDir)
	}

	// Check cache — if all directory mod times match, return cached result
	repoCache.mu.Lock()
	if repoCache.repos != nil && time.Since(repoCache.cachedAt) < 60*time.Second {
		cacheValid := true
		for dir, cachedTime := range repoCache.dirTimes {
			if info, err := os.Stat(dir); err == nil {
				if info.ModTime().After(cachedTime) {
					cacheValid = false
					break
				}
			}
		}
		if cacheValid {
			result := repoCache.repos
			repoCache.mu.Unlock()
			jsonReply(w, http.StatusOK, result)
			return
		}
	}
	repoCache.mu.Unlock()

	// Cache miss — scan directories
	seen := make(map[string]bool)
	var repos []RepoInfo
	dirTimes := make(map[string]time.Time)

	for _, dir := range scanDirs {
		if info, err := os.Stat(dir); err == nil {
			dirTimes[dir] = info.ModTime()
		}
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
	guestUID := r.Header.Get("X-Yaver-GuestUserID")
	if guestUID != "" && s.guestConfigMgr != nil {
		filtered := make([]RepoInfo, 0, len(repos))
		for _, repo := range repos {
			if s.guestConfigMgr.GuestCanAccessProject(guestUID, repo.Name) ||
				s.guestConfigMgr.GuestCanAccessProject(guestUID, filepath.Base(repo.Path)) {
				filtered = append(filtered, repo)
			}
		}
		repos = filtered
	}

	// Update cache
	repoCache.mu.Lock()
	repoCache.repos = repos
	repoCache.dirTimes = dirTimes
	repoCache.cachedAt = time.Now()
	repoCache.mu.Unlock()

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
