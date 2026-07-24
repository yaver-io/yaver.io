package main

// discovery_gitwalk.go — find git repos under the discovery roots, in-process.
//
// Replaces a `find(1)` shell-out that lost every result it had already
// collected whenever its timeout fired. See writeProjects in discovery.go for
// the incident: on a box whose home directory holds 30+ monorepo clones, the
// external find took minutes, the 30s context killed it, and its block-buffered
// pipe stdout was discarded with the process — so a machine full of projects
// reported "_No projects found._" while the sibling in-process scanner found
// 213 on the same disk.
//
// The invariant this file exists to hold: a scan that runs out of time returns
// LESS, never NOTHING. Partial results are appended as they are discovered, so
// hitting the deadline can only truncate the tail.

import (
	"os"
	"path/filepath"
	"strings"
	"time"
)

// discoveryGitWalkSkipDirs are directories that never contain a user's project
// checkout. Kept deliberately in step with the lists in scanMobileProjects and
// findExistingClone — a folder worth ignoring in one walk is worth ignoring in
// all of them, and drift between them is how one surface finds a project the
// others cannot.
var discoveryGitWalkSkipDirs = map[string]bool{
	"node_modules": true, "build": true, "dist": true,
	".cache": true, ".local": true, ".cargo": true, ".rustup": true,
	"Library": true, "Applications": true, "Music": true, "Movies": true,
	"Pictures": true, "Documents": true, "Public": true, "Downloads": true,
	"Desktop": true, ".Trash": true, "Pods": true, ".cocoapods": true,
	".gradle": true, ".pub-cache": true, ".dart_tool": true,
	".expo": true, ".next": true, "vendor": true,
	"homebrew": true, "Cellar": true, "Caskroom": true,
	"AppData": true,
}

// discoveryGitWalkMaxDepth bounds how deep below a root a repo is looked for.
// Matches the old `find -maxdepth 6` so behaviour is unchanged for anyone whose
// scan was already completing.
const discoveryGitWalkMaxDepth = 6

// findGitRepoDirsForDiscovery walks projectDiscoveryRoots and returns the
// directories that contain a `.git` entry, newest-found last.
//
// budget bounds the whole sweep. On expiry it returns what it has — never an
// empty slice it "would have" filled, which is precisely the failure the
// find(1) version had.
func findGitRepoDirsForDiscovery(budget time.Duration) []string {
	roots := projectDiscoveryRoots()
	if len(roots) == 0 {
		return nil
	}
	if budget <= 0 {
		budget = 30 * time.Second
	}
	deadline := time.Now().Add(budget)

	var repos []string
	seen := map[string]bool{}
	seenRoot := map[string]bool{}

	for _, root := range roots {
		if root == "" || seenRoot[root] {
			continue
		}
		seenRoot[root] = true
		if time.Now().After(deadline) {
			break
		}

		_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
			if time.Now().After(deadline) {
				// Stop this root's walk; whatever is in `repos` is kept.
				return filepath.SkipAll
			}
			if err != nil {
				// Unreadable dir (permissions/TCC, a dead symlink, a stalled
				// network mount) must skip that subtree, never abort the sweep.
				if info != nil && info.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			if !info.IsDir() {
				return nil
			}

			name := info.Name()
			if name == ".git" {
				repoDir := filepath.Dir(path)
				if !seen[repoDir] {
					seen[repoDir] = true
					repos = append(repos, repoDir)
				}
				// Never descend into .git itself.
				return filepath.SkipDir
			}
			if discoveryGitWalkSkipDirs[name] {
				return filepath.SkipDir
			}
			// Hidden dirs hold configs and caches, not checkouts. `.git` is
			// handled above, so this cannot skip a repo marker.
			if strings.HasPrefix(name, ".") && path != root {
				return filepath.SkipDir
			}
			if rel, relErr := filepath.Rel(root, path); relErr == nil {
				if strings.Count(rel, string(os.PathSeparator)) >= discoveryGitWalkMaxDepth {
					return filepath.SkipDir
				}
			}
			return nil
		})
	}
	return repos
}
