package main

// git_pr.go — POST /git/pull-request and POST /git/identity.
//
// Completes the normie collaboration loop: a non-technical collaborator
// codes on their own branch, then opens a pull request instead of pushing
// to main. The owner reviews + merges. We also let the caller set the
// per-repo commit author so the collaborator's name lands on the history
// rather than the box owner's.
//
// Tokens never leave the box: the PR call uses the locally-stored provider
// token (~/.yaver/git-providers.json), exactly like /git/provider/repo/create.

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// parsedRemote describes a git remote decomposed into provider coordinates.
type parsedRemote struct {
	Provider string // "github" | "gitlab"
	Host     string // github.com | gitlab.com | self-hosted
	Owner    string // owner / group (may contain subgroups for gitlab)
	Repo     string // repository name (no .git)
}

// parseGitRemote turns `git remote get-url origin` into provider coordinates.
// Handles git@host:owner/repo.git, https://host/owner/repo(.git), and
// embedded-cred https://user:tok@host/owner/repo forms.
func parseGitRemote(raw string) (parsedRemote, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return parsedRemote{}, fmt.Errorf("empty remote")
	}
	var host, path string
	if m := strings.SplitN(s, "@", 2); strings.HasPrefix(s, "git@") || (len(m) == 2 && strings.Contains(m[1], ":") && !strings.Contains(m[0], "/")) {
		// scp-like: [user@]host:owner/repo
		rest := s
		if at := strings.Index(rest, "@"); at >= 0 {
			rest = rest[at+1:]
		}
		parts := strings.SplitN(rest, ":", 2)
		if len(parts) != 2 {
			return parsedRemote{}, fmt.Errorf("unparseable ssh remote: %s", raw)
		}
		host = parts[0]
		path = parts[1]
	} else {
		t := s
		for _, scheme := range []string{"https://", "http://", "ssh://", "git://"} {
			t = strings.TrimPrefix(t, scheme)
		}
		if at := strings.Index(t, "@"); at >= 0 {
			t = t[at+1:] // strip embedded creds
		}
		slash := strings.Index(t, "/")
		if slash < 0 {
			return parsedRemote{}, fmt.Errorf("unparseable remote: %s", raw)
		}
		host = t[:slash]
		path = t[slash+1:]
	}
	host = strings.TrimSpace(host)
	path = strings.TrimSuffix(strings.Trim(path, "/"), ".git")
	if host == "" || path == "" || !strings.Contains(path, "/") {
		return parsedRemote{}, fmt.Errorf("unparseable remote: %s", raw)
	}
	lastSlash := strings.LastIndex(path, "/")
	owner := path[:lastSlash]
	repo := path[lastSlash+1:]

	provider := ""
	switch {
	case strings.Contains(host, "github"):
		provider = "github"
	case strings.Contains(host, "gitlab"):
		provider = "gitlab"
	}
	return parsedRemote{Provider: provider, Host: host, Owner: owner, Repo: repo}, nil
}

// providerForHost resolves a self-hosted host to its provider ("github" /
// "gitlab") using the local providers store, where setup recorded the pairing.
func providerForHost(host string) string {
	providers, _ := loadGitProviders()
	for _, p := range providers {
		if p.Host == host {
			return p.Provider
		}
	}
	return ""
}

// tokenForProviderHost returns the stored token for a provider+host, with a
// soft fallback that matches on provider alone (covers default-host setups).
func tokenForProviderHost(provider, host string) string {
	providers, _ := loadGitProviders()
	for _, p := range providers {
		if p.Provider == provider && p.Host == host {
			return p.Token
		}
	}
	for _, p := range providers {
		if p.Provider == provider {
			return p.Token
		}
	}
	return ""
}

type pullRequestRequest struct {
	WorkDir string `json:"workDir,omitempty"`
	Title   string `json:"title,omitempty"`
	Body    string `json:"body,omitempty"`
	Head    string `json:"head,omitempty"` // source branch; default = current branch
	Base    string `json:"base,omitempty"` // target branch; default = origin default / main
}

type pullRequestResponse struct {
	OK       bool   `json:"ok"`
	URL      string `json:"url,omitempty"`
	Number   int    `json:"number,omitempty"`
	Provider string `json:"provider,omitempty"`
	Head     string `json:"head,omitempty"`
	Base     string `json:"base,omitempty"`
	Error    string `json:"error,omitempty"`
}

func (s *HTTPServer) handleGitPullRequest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}
	var req pullRequestRequest
	if r.ContentLength > 0 {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
			return
		}
	}
	workDir := strings.TrimSpace(req.WorkDir)
	if workDir == "" {
		workDir = getGitWorkDir(r, s.taskMgr)
	}
	if workDir == "" {
		jsonError(w, http.StatusBadRequest, "missing workDir")
		return
	}

	remoteURL, err := runGit(workDir, "remote", "get-url", "origin")
	if err != nil {
		jsonError(w, http.StatusBadRequest, "no origin remote: "+remoteURL)
		return
	}
	pr, err := parseGitRemote(remoteURL)
	if err != nil {
		jsonError(w, http.StatusBadRequest, err.Error())
		return
	}
	if pr.Provider == "" {
		// Self-hosted host the hostname couldn't classify — resolve it from
		// the providers store (which records provider↔host at setup time).
		pr.Provider = providerForHost(pr.Host)
	}
	if pr.Provider == "" {
		jsonError(w, http.StatusBadRequest, "unsupported git host (need github or gitlab): "+pr.Host)
		return
	}

	head := strings.TrimSpace(req.Head)
	if head == "" {
		head, _ = runGit(workDir, "rev-parse", "--abbrev-ref", "HEAD")
		head = strings.TrimSpace(head)
	}
	base := strings.TrimSpace(req.Base)
	if base == "" {
		if def, derr := runGit(workDir, "symbolic-ref", "--short", "refs/remotes/origin/HEAD"); derr == nil {
			base = strings.TrimPrefix(strings.TrimSpace(def), "origin/")
		}
		if base == "" {
			base = "main"
		}
	}
	if head == "" || head == base {
		jsonError(w, http.StatusBadRequest, fmt.Sprintf("head branch (%q) must differ from base (%q)", head, base))
		return
	}
	title := strings.TrimSpace(req.Title)
	if title == "" {
		title = fmt.Sprintf("%s → %s", head, base)
	}

	token := tokenForProviderHost(pr.Provider, pr.Host)
	if token == "" {
		jsonError(w, http.StatusPreconditionFailed,
			"no "+pr.Provider+" token configured on this machine — connect a git provider first")
		return
	}

	var prURL string
	var number int
	switch pr.Provider {
	case "github":
		prURL, number, err = createPullRequestGitHub(pr.Host, token, pr.Owner, pr.Repo, head, base, title, req.Body)
	case "gitlab":
		prURL, number, err = createMergeRequestGitLab(pr.Host, token, pr.Owner, pr.Repo, head, base, title, req.Body)
	}
	if err != nil {
		jsonError(w, http.StatusBadGateway, pr.Provider+" pull request failed: "+err.Error())
		return
	}
	jsonReply(w, http.StatusOK, pullRequestResponse{
		OK: true, URL: prURL, Number: number, Provider: pr.Provider, Head: head, Base: base,
	})
}

func githubAPIBase(host string) string {
	if host == "github.com" || host == "" {
		return "https://api.github.com"
	}
	return "https://" + host + "/api/v3" // GitHub Enterprise
}

func createPullRequestGitHub(host, token, owner, repo, head, base, title, body string) (string, int, error) {
	payload, _ := json.Marshal(map[string]interface{}{
		"title": title, "head": head, "base": base, "body": body,
	})
	endpoint := fmt.Sprintf("%s/repos/%s/%s/pulls", githubAPIBase(host), owner, repo)
	req, _ := http.NewRequest("POST", endpoint, strings.NewReader(string(payload)))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", 0, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", 0, fmt.Errorf("github %d: %s", resp.StatusCode, string(raw))
	}
	var data struct {
		HTMLURL string `json:"html_url"`
		Number  int    `json:"number"`
	}
	if err := json.Unmarshal(raw, &data); err != nil {
		return "", 0, err
	}
	return data.HTMLURL, data.Number, nil
}

func createIssueGitHub(host, token, owner, repo, title, body string) (string, int, error) {
	payload, _ := json.Marshal(map[string]interface{}{
		"title": title,
		"body":  body,
	})
	endpoint := fmt.Sprintf("%s/repos/%s/%s/issues", githubAPIBase(host), owner, repo)
	req, _ := http.NewRequest("POST", endpoint, strings.NewReader(string(payload)))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", 0, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", 0, fmt.Errorf("github %d: %s", resp.StatusCode, string(raw))
	}
	var data struct {
		HTMLURL string `json:"html_url"`
		Number  int    `json:"number"`
	}
	if err := json.Unmarshal(raw, &data); err != nil {
		return "", 0, err
	}
	return data.HTMLURL, data.Number, nil
}

func createMergeRequestGitLab(host, token, owner, repo, head, base, title, body string) (string, int, error) {
	if host == "" {
		host = "gitlab.com"
	}
	projectPath := url.PathEscape(owner + "/" + repo) // owner/repo → owner%2Frepo
	payload, _ := json.Marshal(map[string]interface{}{
		"source_branch": head, "target_branch": base, "title": title, "description": body,
	})
	endpoint := fmt.Sprintf("https://%s/api/v4/projects/%s/merge_requests", host, projectPath)
	req, _ := http.NewRequest("POST", endpoint, strings.NewReader(string(payload)))
	req.Header.Set("PRIVATE-TOKEN", token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", 0, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", 0, fmt.Errorf("gitlab %d: %s", resp.StatusCode, string(raw))
	}
	var data struct {
		WebURL string `json:"web_url"`
		IID    int    `json:"iid"`
	}
	if err := json.Unmarshal(raw, &data); err != nil {
		return "", 0, err
	}
	return data.WebURL, data.IID, nil
}

func createIssueGitLab(host, token, owner, repo, title, body string) (string, int, error) {
	if host == "" {
		host = "gitlab.com"
	}
	projectPath := url.PathEscape(owner + "/" + repo)
	payload, _ := json.Marshal(map[string]interface{}{
		"title":       title,
		"description": body,
	})
	endpoint := fmt.Sprintf("https://%s/api/v4/projects/%s/issues", host, projectPath)
	req, _ := http.NewRequest("POST", endpoint, strings.NewReader(string(payload)))
	req.Header.Set("PRIVATE-TOKEN", token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", 0, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", 0, fmt.Errorf("gitlab %d: %s", resp.StatusCode, string(raw))
	}
	var data struct {
		WebURL string `json:"web_url"`
		IID    int    `json:"iid"`
	}
	if err := json.Unmarshal(raw, &data); err != nil {
		return "", 0, err
	}
	return data.WebURL, data.IID, nil
}

// ── Per-repo commit identity ─────────────────────────────────────────

type gitIdentityRequest struct {
	WorkDir string `json:"workDir,omitempty"`
	Name    string `json:"name"`
	Email   string `json:"email"`
}

// handleGitIdentity sets repo-local git user.name / user.email so a
// collaborator's commits are authored under their own identity rather than
// the box owner's. Scoped to the working tree (git config --local), never
// global.
func (s *HTTPServer) handleGitIdentity(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}
	var req gitIdentityRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	workDir := strings.TrimSpace(req.WorkDir)
	if workDir == "" {
		workDir = getGitWorkDir(r, s.taskMgr)
	}
	if workDir == "" {
		jsonError(w, http.StatusBadRequest, "missing workDir")
		return
	}
	name := strings.TrimSpace(req.Name)
	email := strings.TrimSpace(req.Email)
	if name == "" || email == "" {
		jsonError(w, http.StatusBadRequest, "name and email are required")
		return
	}
	if out, err := runGit(workDir, "config", "--local", "user.name", name); err != nil {
		jsonError(w, http.StatusInternalServerError, "git config user.name failed: "+out)
		return
	}
	if out, err := runGit(workDir, "config", "--local", "user.email", email); err != nil {
		jsonError(w, http.StatusInternalServerError, "git config user.email failed: "+out)
		return
	}
	jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true, "name": name, "email": email})
}
