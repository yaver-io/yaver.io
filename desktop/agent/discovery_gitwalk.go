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
	"log"
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
func findGitRepoDirsWalk(budget time.Duration, emit func(string)) {
	roots := projectDiscoveryRoots()
	if len(roots) == 0 {
		return
	}
	if budget <= 0 {
		budget = 30 * time.Second
	}
	deadline := time.Now().Add(budget)

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
					emit(repoDir)
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
}

// findGitRepoDirsForDiscovery bounds the walk from OUTSIDE it.
//
// The in-callback deadline below is necessary but NOT sufficient, and that
// distinction cost a real outage (mac mini, 2026-07-24). filepath.Walk blocks
// inside readDirNames -> os.Open BEFORE it ever calls the callback, so a single
// hung path — a stale network mount, a dead automount, an unresponsive FUSE
// volume — wedges the walk in a syscall where no callback-based deadline can
// ever fire. Goroutine stack from the box, captured via SIGQUIT:
//
//	syscall.Open -> os.OpenFile -> filepath.readDirNames -> filepath.walk
//	  -> findGitRepoDirsForDiscovery -> writeProjects -> discoverProjects
//
// discoverProjects never returned, PROJECTS.md was never written, /projects
// stayed empty forever, and the phone said "No projects yet" on a machine with
// 30+ repos.
//
// So the walk runs in a goroutine that streams each repo through a channel, and
// the CALLER holds the wall clock. If the walk hangs we return what arrived and
// move on; the orphaned goroutine is blocked in the kernel and will exit if the
// mount ever answers. Leaking one goroutine is strictly better than never
// discovering projects again.
//
// General rule this encodes for any filesystem sweep in the agent: a depth
// limit is not a bound, and neither is a deadline you can only check between
// callbacks. Only an out-of-band timeout bounds wall-clock.
func findGitRepoDirsForDiscovery(budget time.Duration) []string {
	if budget <= 0 {
		budget = 30 * time.Second
	}
	out := make(chan string, 512)
	go func() {
		defer close(out)
		findGitRepoDirsWalk(budget, func(dir string) {
			select {
			case out <- dir:
			default: // caller gave up; never block a walk on a dead reader
			}
		})
	}()

	var repos []string
	timer := time.NewTimer(budget)
	defer timer.Stop()
	for {
		select {
		case dir, ok := <-out:
			if !ok {
				return repos
			}
			repos = append(repos, dir)
		case <-timer.C:
			log.Printf("[discovery] git-repo walk exceeded %s (likely a stalled mount) — continuing with %d repos found",
				budget, len(repos))
			return repos
		}
	}
}
