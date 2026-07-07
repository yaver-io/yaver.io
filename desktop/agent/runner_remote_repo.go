package main

// runner_remote_repo.go — CWD-aware remote coding. When `yaver <runner> remote`
// (or `yaver remote …`) runs from inside a local git checkout, we mirror that
// project onto the target box before opening the runner TUI: detect the repo
// from the CWD, ensure the box has the same repo+branch checked out (clone if
// absent, pull if present — both idempotent server primitives), then cd the
// runner into the matching subdirectory. Sync semantics are pull-from-origin:
// the box works on the PUSHED branch state, and we WARN when the local tree has
// uncommitted or unpushed work that won't travel.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// localRepoContext is what we detect from the CLI's CWD to mirror onto a box.
type localRepoContext struct {
	root      string // git toplevel (absolute, local)
	remoteURL string // the branch's remote, normalized to https for token auth
	branch    string
	relSubdir string // CWD relative to root, slash-separated; "" when at root
	dirty     bool   // uncommitted changes present
	ahead     int    // commits ahead of upstream (unpushed)
}

// detectLocalRepoContext inspects the current working directory and returns the
// git project to mirror, or (nil, nil) when CWD isn't inside a git repo (the
// caller then runs in the box's default work dir, the pre-CWD-aware behavior).
func detectLocalRepoContext() (*localRepoContext, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	root := gitLine(cwd, "rev-parse", "--show-toplevel")
	if root == "" {
		return nil, nil // not a git repo
	}

	branch := gitLine(root, "rev-parse", "--abbrev-ref", "HEAD")
	if branch == "" || branch == "HEAD" {
		branch = gitLine(root, "branch", "--show-current")
	}

	// Resolve the branch's ACTUAL remote — not hardcoded "origin". This repo's
	// remote is named "github"; assuming origin returns nothing. Fall back to
	// origin, then the first configured remote.
	remoteName := gitLine(root, "config", "--get", "branch."+branch+".remote")
	if remoteName == "" {
		if _, e := runGit(root, "remote", "get-url", "origin"); e == nil {
			remoteName = "origin"
		} else if remotes := strings.Fields(gitLine(root, "remote")); len(remotes) > 0 {
			remoteName = remotes[0]
		}
	}
	remoteURL := ""
	if remoteName != "" {
		// Keep the ORIGINAL url (often SSH). prepareRemoteRepo decides HTTPS-vs-
		// SSH per box: HTTPS only when the box holds a token for the host, else
		// the SSH url so the box clones with its own key.
		remoteURL = gitLine(root, "remote", "get-url", remoteName)
	}

	rel := ""
	if r, e := filepath.Rel(root, cwd); e == nil && r != "." && !strings.HasPrefix(r, "..") {
		rel = filepath.ToSlash(r)
	}

	dirty := gitLine(root, "status", "--porcelain") != ""
	ahead := 0
	if out := gitLine(root, "rev-list", "--count", "@{u}..HEAD"); out != "" {
		fmt.Sscanf(out, "%d", &ahead)
	}

	return &localRepoContext{
		root: root, remoteURL: remoteURL, branch: branch,
		relSubdir: rel, dirty: dirty, ahead: ahead,
	}, nil
}

// gitLine runs git in dir and returns the trimmed output, or "" on any error
// (a non-zero exit — e.g. an unset config key — is a normal "not set" signal).
func gitLine(dir string, args ...string) string {
	out, err := runGit(dir, args...)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// normalizeGitURLToHTTPS rewrites SSH remotes to their HTTPS form so the box can
// clone with an HTTPS token from its credential store (the box may not carry an
// SSH key for your git host). scp-form `git@host:owner/repo.git` and
// `ssh://git@host/owner/repo.git` both become `https://host/owner/repo.git`.
// HTTPS / git:// URLs pass through unchanged.
func normalizeGitURLToHTTPS(raw string) string {
	raw = strings.TrimSpace(raw)
	switch {
	case raw == "":
		return ""
	case strings.HasPrefix(raw, "git@"):
		rest := strings.TrimPrefix(raw, "git@")
		if i := strings.Index(rest, ":"); i > 0 {
			return "https://" + rest[:i] + "/" + strings.TrimPrefix(rest[i+1:], "/")
		}
		return raw
	case strings.HasPrefix(raw, "ssh://"):
		rest := strings.TrimPrefix(raw, "ssh://")
		rest = strings.TrimPrefix(rest, "git@")
		return "https://" + rest
	default:
		return raw
	}
}

// warnLocalRepoState prints the pull-from-origin caveat: the box works on the
// pushed branch, so local WIP won't be there. One terse line, always shown
// (even zero-chrome) because it prevents a confusing "why didn't it see my fix".
func warnLocalRepoState(repo *localRepoContext, machine string) {
	var bits []string
	if repo.dirty {
		bits = append(bits, "uncommitted changes")
	}
	if repo.ahead > 0 {
		bits = append(bits, fmt.Sprintf("%d unpushed commit(s)", repo.ahead))
	}
	if len(bits) == 0 {
		return
	}
	fmt.Fprintf(os.Stderr, "⚠ %s has %s — %s works on the pushed %q branch, so those won't be there. Commit + push first if the task needs them.\r\n",
		filepath.Base(repo.root), strings.Join(bits, " and "), machine, repo.branch)
}

// prepareRemoteRepo ensures the box has repo checked out and returns the remote
// working directory (checkout path + the same subdir the user is in locally).
// Uses the idempotent /repos/clone (clones or reports the existing path) then
// /repos/pull to freshen an existing checkout. On a git-auth failure it makes
// one best-effort attempt to push local git credentials to the box, then
// retries the clone.
func prepareRemoteRepo(base, token string, headers http.Header, repo *localRepoContext, deviceID string, quiet bool) (string, error) {
	if repo == nil || strings.TrimSpace(repo.remoteURL) == "" {
		return "", fmt.Errorf("can't tell which git remote this project uses — set one (e.g. `git remote add origin <url>`) or pass --yaver-cwd=<path-on-box>")
	}
	client := &http.Client{Timeout: 3 * time.Minute}
	doJSON := func(path string, body any) (map[string]any, int, error) {
		buf, _ := json.Marshal(body)
		req, err := http.NewRequest(http.MethodPost, strings.TrimRight(base, "/")+path, bytes.NewReader(buf))
		if err != nil {
			return nil, 0, err
		}
		for k := range headers {
			req.Header.Set(k, headers.Get(k))
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(req)
		if err != nil {
			return nil, 0, err
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		var out map[string]any
		_ = json.Unmarshal(raw, &out)
		return out, resp.StatusCode, nil
	}
	errText := func(out map[string]any) string {
		if out == nil {
			return "no response"
		}
		if e, ok := out["error"].(string); ok && e != "" {
			return e
		}
		return "unexpected response"
	}

	if !quiet {
		fmt.Fprintf(os.Stderr, "→ preparing %s (%s) on the box…\r\n", filepath.Base(repo.root), repo.branch)
	}
	// Pick the clone URL form per box: HTTPS (token auth) when the box already
	// holds a credential for this host, else the original SSH url so the box
	// clones with its own key. Falls back to HTTPS on any lookup failure — the
	// auth-repair + hint path below covers a miss.
	cloneURL := repo.remoteURL
	host := hostFromURL(repo.remoteURL)
	if boxHasGitCredForHost(base, token, headers, host) {
		cloneURL = normalizeGitURLToHTTPS(repo.remoteURL)
	} else if strings.HasPrefix(repo.remoteURL, "http") {
		cloneURL = repo.remoteURL // already HTTPS; leave as-is
	}
	cloneBody := map[string]string{"url": cloneURL, "branch": repo.branch}
	out, status, err := doJSON("/repos/clone", cloneBody)
	if err != nil {
		return "", fmt.Errorf("clone request to the box failed: %v", err)
	}
	if status != http.StatusOK && looksLikeGitAuthError(errText(out)) {
		if tryPushGitCredsToDevice(deviceID, quiet) {
			out, status, err = doJSON("/repos/clone", cloneBody)
			if err != nil {
				return "", fmt.Errorf("clone request to the box failed: %v", err)
			}
		}
	}
	if status != http.StatusOK {
		hint := fmt.Sprintf("give it git access — `yaver git push-creds --device %s` (forwards an HTTPS token), or add the box's SSH key to %s", shortDeviceID(deviceID), host)
		if strings.HasPrefix(cloneURL, "git@") || strings.HasPrefix(cloneURL, "ssh://") {
			// SSH form was used → the box's own key isn't authorized on the host.
			hint = fmt.Sprintf("the box tried its SSH key and %s rejected it — add the box's public key (`yaver ssh %s -- cat ~/.ssh/id_*.pub`) to %s, or set an HTTPS token with `yaver git push-creds --device %s`",
				host, deviceID, host, shortDeviceID(deviceID))
		}
		return "", fmt.Errorf("the box couldn't clone %s: %s\n  %s — then retry", cloneURL, errText(out), hint)
	}

	path, _ := out["path"].(string)
	if strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("the box did not report a checkout path")
	}
	already, _ := out["alreadyExisted"].(bool)
	if already {
		if _, st, _ := doJSON("/repos/pull", map[string]string{"workDir": path}); st == http.StatusOK && !quiet {
			fmt.Fprintf(os.Stderr, "→ pulled latest into %s on the box\r\n", filepath.Base(path))
		}
	} else if !quiet {
		fmt.Fprintf(os.Stderr, "→ cloned %s onto the box\r\n", filepath.Base(path))
	}

	remoteCwd := path
	if repo.relSubdir != "" {
		remoteCwd = strings.TrimRight(path, "/") + "/" + repo.relSubdir
	}
	return remoteCwd, nil
}

// boxHasGitCredForHost asks the box (GET /repos/credentials) whether it holds a
// token for host. Best-effort: any error → false (caller then prefers the SSH
// url). Never returns the token itself — the list endpoint reports hasToken only.
func boxHasGitCredForHost(base, token string, headers http.Header, host string) bool {
	if strings.TrimSpace(host) == "" {
		return false
	}
	req, err := http.NewRequest(http.MethodGet, strings.TrimRight(base, "/")+"/repos/credentials", nil)
	if err != nil {
		return false
	}
	for k := range headers {
		req.Header.Set(k, headers.Get(k))
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := (&http.Client{Timeout: 8 * time.Second}).Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false
	}
	var rows []struct {
		Host     string `json:"host"`
		HasToken bool   `json:"hasToken"`
	}
	if json.NewDecoder(resp.Body).Decode(&rows) != nil {
		return false
	}
	for _, r := range rows {
		if strings.EqualFold(strings.TrimSpace(r.Host), host) && r.HasToken {
			return true
		}
	}
	return false
}

// looksLikeGitAuthError sniffs a git clone failure for the credential-missing
// signatures worth auto-repairing (vs a genuine bad-URL / disk error).
func looksLikeGitAuthError(msg string) bool {
	m := strings.ToLower(msg)
	return strings.Contains(m, "authentication failed") ||
		strings.Contains(m, "could not read username") ||
		strings.Contains(m, "permission denied") ||
		strings.Contains(m, "terminal prompts disabled") ||
		strings.Contains(m, "403") ||
		strings.Contains(m, "access denied") ||
		strings.Contains(m, "invalid credentials")
}

// tryPushGitCredsToDevice forwards this machine's detected git tokens to the
// target box (same path as `yaver git push-creds`), returning true when the
// push reported no error. Best-effort: no local token → nothing to push → false.
func tryPushGitCredsToDevice(deviceID string, quiet bool) bool {
	if strings.TrimSpace(deviceID) == "" {
		return false
	}
	if !quiet {
		fmt.Fprintf(os.Stderr, "→ clone hit an auth wall — forwarding your git credentials to the box…\r\n")
	}
	res := mcpGitPushCreds(gitPushCredsMCPArgs{DeviceID: deviceID})
	if m, ok := res.(map[string]any); ok {
		if e, has := m["error"].(string); has && e != "" {
			return false
		}
	}
	return true
}
