package main

// Find an existing clone of a given remote URL anywhere under the
// agent's known project-discovery roots. Used by the Feedback SDK's
// git-setup wizard so a user who already cloned the repo manually
// (often months ago) doesn't end up with a duplicate clone in a
// fresh location after they Save & verify on a new web setup.
//
// The walk is bounded by the same roots scanMobileProjects uses
// (`projectDiscoveryRoots`) — `~/`, `~/Workspace`, `~/Projects`,
// `~/Code`, `~/src`, `~/work`, `~/dev`, plus WSL counterparts.
// We don't want to scan the whole disk, and we don't want to invent
// new roots; reusing this list keeps `git find-repo`'s answer
// consistent with what `/projects` already lists.
//
// URL normalisation is the trickiest bit. The same logical repo can
// be remoted as:
//
//	git@github.com:owner/repo.git
//	https://github.com/owner/repo.git
//	https://github.com/owner/repo
//	https://user:token@github.com/owner/repo.git
//	ssh://git@github.com/owner/repo
//
// We normalise all of those down to `host/owner/repo` (lowercase,
// trailing `.git` stripped, embedded creds stripped) before comparing.

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// normalizeGitRemoteURL extracts a `host/owner/repo` triple from any
// of the four common remote URL shapes. Empty string when the input
// can't be parsed (then comparison fails closed — we'd rather
// re-clone than risk merging into an unrelated dir).
func normalizeGitRemoteURL(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return ""
	}
	// SCP-like ssh form: `git@host:owner/repo(.git)?` — convert to
	// `ssh://git@host/owner/repo(.git)?` so the URL parser accepts it.
	if !strings.Contains(s, "://") && strings.Contains(s, "@") && strings.Contains(s, ":") {
		// Only do this when the part before `:` looks like user@host
		// (no path separators), not when the colon is from a port spec.
		if at := strings.Index(s, "@"); at >= 0 {
			rest := s[at+1:]
			if colon := strings.Index(rest, ":"); colon >= 0 {
				host := rest[:colon]
				path := rest[colon+1:]
				if !strings.ContainsAny(host, "/\\") {
					s = "ssh://" + s[:at+1] + host + "/" + path
				}
			}
		}
	}
	u, err := url.Parse(s)
	if err != nil || u.Host == "" {
		return ""
	}
	host := strings.ToLower(u.Host)
	// Strip user-info — credentials in the URL must not factor into
	// equality (`user:token@github.com/...` and `github.com/...` are
	// the same repo).
	if i := strings.Index(host, "@"); i >= 0 {
		host = host[i+1:]
	}
	// Strip an explicit port (`:443`, `:22`, etc).
	if i := strings.Index(host, ":"); i >= 0 {
		host = host[:i]
	}
	path := strings.TrimSuffix(strings.Trim(u.Path, "/"), ".git")
	if path == "" {
		return ""
	}
	return host + "/" + strings.ToLower(path)
}

// readGitConfigRemoteURLs returns every remote URL declared in the
// given `.git/config` file. Most repos have just `origin` but we
// honour any name — a user may have cloned via `upstream` and pushes
// to a fork named `origin`, etc.
func readGitConfigRemoteURLs(gitConfigPath string) []string {
	f, err := os.Open(gitConfigPath)
	if err != nil {
		return nil
	}
	defer f.Close()
	var urls []string
	inRemoteSection := false
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 256*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "[") {
			inRemoteSection = strings.HasPrefix(line, "[remote ")
			continue
		}
		if !inRemoteSection {
			continue
		}
		if strings.HasPrefix(line, "url") {
			eq := strings.Index(line, "=")
			if eq < 0 {
				continue
			}
			urls = append(urls, strings.TrimSpace(line[eq+1:]))
		}
	}
	return urls
}

type existingCloneMatch struct {
	Path       string `json:"path"`
	RemoteURL  string `json:"remoteUrl"`
	RemoteName string `json:"remoteName,omitempty"` // best-effort, we don't track which name matched
}

// findExistingClone walks the discovery roots looking for a directory
// whose `.git/config` has any remote URL that normalises to the same
// triple as `target`. Returns the first match (deepest-found wins is
// not necessary — duplicate clones of the same repo on the same
// machine are rare enough that any match is the right answer).
//
// Bounded by:
//   - the existing `projectDiscoveryRoots` (no whole-disk walk).
//   - `maxWalkDuration` so a deeply nested home directory can't
//     wedge the request.
//   - the same skip-dir list scanMobileProjects uses (no
//     node_modules, no Library, etc.).
func findExistingClone(targetRemoteURL string) (*existingCloneMatch, error) {
	want := normalizeGitRemoteURL(targetRemoteURL)
	if want == "" {
		return nil, fmt.Errorf("target remote URL %q could not be normalised", targetRemoteURL)
	}
	roots := projectDiscoveryRoots()
	if len(roots) == 0 {
		return nil, nil
	}

	// Same skip lists scanMobileProjects uses, narrowed to the dirs
	// that genuinely never contain a checkout (node_modules,
	// Library/, .Trash, etc.). Keeping the lists in sync with that
	// scanner means a future "ignore this folder" tweak in one place
	// is mirrored in find-repo without code drift.
	skipDirs := map[string]bool{
		"node_modules": true, "build": true, "dist": true,
		".cache": true, ".local": true, ".cargo": true, ".rustup": true,
		"Library": true, "Applications": true, "Music": true, "Movies": true,
		"Pictures": true, "Documents": true, "Public": true, "Downloads": true,
		"Desktop": true, ".Trash": true, "Pods": true, ".cocoapods": true,
		".gradle": true, ".pub-cache": true,
		".dart_tool": true, ".expo": true, ".next": true, "vendor": true,
		"homebrew": true, "Cellar": true, "Caskroom": true,
	}

	deadline := time.Now().Add(maxFindRepoWalkDuration)
	seenRoot := map[string]bool{}

	for _, root := range roots {
		if root == "" || seenRoot[root] {
			continue
		}
		seenRoot[root] = true

		var match *existingCloneMatch
		var stopErr = errStopFindRepo

		err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return filepath.SkipDir
			}
			if time.Now().After(deadline) {
				return stopErr
			}
			if !info.IsDir() {
				return nil
			}
			name := info.Name()
			// Skip hidden dirs except .git itself (which contains config).
			if strings.HasPrefix(name, ".") && name != "." && name != ".git" && name != ".config" {
				return filepath.SkipDir
			}
			if skipDirs[name] {
				return filepath.SkipDir
			}
			// Limit depth — same 7-level cap scanMobileProjects uses.
			rel, _ := filepath.Rel(root, path)
			if strings.Count(rel, string(os.PathSeparator)) > 7 {
				return filepath.SkipDir
			}

			// .git/config is what we read. .git can also be a file
			// for git submodules / worktrees; we ignore those — the
			// canonical repo lives at the gitdir target which is
			// almost always inside a discovery root anyway.
			gitDir := filepath.Join(path, ".git")
			st, err := os.Stat(gitDir)
			if err != nil || !st.IsDir() {
				return nil
			}
			urls := readGitConfigRemoteURLs(filepath.Join(gitDir, "config"))
			for _, u := range urls {
				if normalizeGitRemoteURL(u) == want {
					match = &existingCloneMatch{Path: path, RemoteURL: u}
					return stopErr
				}
			}
			// Once we've inspected a `.git`-bearing dir, don't descend
			// further into its working tree — nested git repos are
			// rare and the walk gets expensive.
			return filepath.SkipDir
		})

		if match != nil {
			return match, nil
		}
		if err != nil && err != errStopFindRepo {
			// Non-fatal: the root may have a permission issue. Try the
			// next root rather than failing the whole call.
			continue
		}
	}
	return nil, nil
}

const maxFindRepoWalkDuration = 5 * time.Second

var errStopFindRepo = fmt.Errorf("find-repo: walk stopped early")

// handleGitFindRepo answers POST /git/find-repo with the location of
// an existing clone of the given remote URL, or null if none exists
// under the discovery roots. Owner-only.
//
// Body: `{"remoteUrl": "https://github.com/owner/repo.git"}`
// Reply: `{"ok": true, "match": {"path": ..., "remoteUrl": ...}}`
//        or `{"ok": true, "match": null}` when nothing was found.
func (s *HTTPServer) handleGitFindRepo(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}
	var req struct {
		RemoteURL string `json:"remoteUrl"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if strings.TrimSpace(req.RemoteURL) == "" {
		jsonError(w, http.StatusBadRequest, "remoteUrl required")
		return
	}
	match, err := findExistingClone(req.RemoteURL)
	if err != nil {
		jsonReply(w, http.StatusOK, map[string]interface{}{
			"ok":    true,
			"match": nil,
			"error": err.Error(),
		})
		return
	}
	jsonReply(w, http.StatusOK, map[string]interface{}{
		"ok":    true,
		"match": match,
	})
}

// findExistingCloneCached wraps findExistingClone with a small
// short-TTL cache so repeated calls during a wizard's Save & verify
// → clone path don't re-walk the home directory back-to-back.
var (
	findRepoCache   = map[string]findRepoCacheEntry{}
	findRepoCacheMu sync.RWMutex
)

type findRepoCacheEntry struct {
	match     *existingCloneMatch
	expiresAt time.Time
}

func findExistingCloneCached(targetRemoteURL string) (*existingCloneMatch, error) {
	key := normalizeGitRemoteURL(targetRemoteURL)
	if key == "" {
		return findExistingClone(targetRemoteURL)
	}
	findRepoCacheMu.RLock()
	if entry, ok := findRepoCache[key]; ok && time.Now().Before(entry.expiresAt) {
		findRepoCacheMu.RUnlock()
		return entry.match, nil
	}
	findRepoCacheMu.RUnlock()

	match, err := findExistingClone(targetRemoteURL)
	if err == nil {
		findRepoCacheMu.Lock()
		findRepoCache[key] = findRepoCacheEntry{
			match:     match,
			expiresAt: time.Now().Add(30 * time.Second),
		}
		findRepoCacheMu.Unlock()
	}
	return match, err
}
