package main

// build_cache_git.go — git-state cache layer for the Hermes bundle build.
//
// Goal: when the mobile app calls POST /dev/build-native and nothing
// that the Hermes bundle would actually pick up has changed since the
// last successful build, skip Metro + hermesc and re-serve the cached
// .yaver-build/main.jsbundle. A typical day churns through commits
// that touch only docs / Go agent / CI / web — all of which are
// irrelevant to the JS bundle that the phone runs. Rebuilding for any
// of those is 8–15 s of wasted Metro + hermesc work.
//
// Decision data:
//   1. HEAD SHA at last build vs HEAD SHA now (recorded in status.json)
//   2. If HEAD moved: which files changed (`git diff --name-only A B`)
//   3. dirty bundle-relevant files at last build vs now
//
// The classifier `isBundleRelevant(path)` is intentionally conservative:
// when in doubt, treat as relevant and rebuild. The cost of an extra
// rebuild is 8–15 s; the cost of serving a stale bundle is "the user's
// edits don't show up", which is way worse for trust.

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// gitBuildCacheDecision captures the result of comparing the recorded
// build state (status.json fields) to the current working tree.
type gitBuildCacheDecision struct {
	// Valid means the cached bundle is still good — all bundle-
	// relevant inputs match what was on disk last time.
	Valid bool
	// Reason is a one-line explanation we surface to the dev server
	// progress stream so the user can see why their build was (or
	// wasn't) reused.
	Reason string
	// HeadSHAChanged is set when HEAD has moved since the last
	// successful build. The cache may still be Valid if the diff
	// touched only non-bundle files (docs, CI, etc.).
	HeadSHAChanged bool
	// DirtyChanged is set when the bundle-relevant dirty file set
	// has different content from what we hashed at the last build.
	DirtyChanged bool
	// CurrentHeadSHA is the HEAD SHA we just observed (may be empty
	// when the workdir isn't a git repo).
	CurrentHeadSHA string
	// CurrentDirtySHA is the hash we just computed over the current
	// bundle-relevant dirty files (empty when tree is clean).
	CurrentDirtySHA string
	// CurrentDirty is true when `git status --porcelain` returned
	// any output. Recorded back into status.json so the next build
	// has the full snapshot.
	CurrentDirty bool
}

// checkGitStateBuildCache compares the cached snapshot in `status` with
// the current state of `workDir` and returns whether the cached bundle
// can be re-served. Caller should AND this with consumer-cache validity
// (mobile app version etc.) before short-circuiting Metro + hermesc.
//
// Always succeeds with a sensible decision — git failures degrade to
// "rebuild to be safe" rather than blocking the request.
func checkGitStateBuildCache(workDir string, status nativeBuildStatus) gitBuildCacheDecision {
	// 1. Need a git repo to diff anything.
	if _, err := runGit(workDir, "rev-parse", "--is-inside-work-tree"); err != nil {
		return gitBuildCacheDecision{
			Valid:  false,
			Reason: "not a git repo; cannot diff source — rebuilding",
		}
	}

	currentHead, err := runGit(workDir, "rev-parse", "HEAD")
	if err != nil {
		return gitBuildCacheDecision{
			Valid:  false,
			Reason: "git rev-parse HEAD failed; rebuilding to be safe",
		}
	}

	// Always compute current dirty snapshot so we can cache it back
	// even when we end up rebuilding anyway.
	porcelain, _ := runGit(workDir, "status", "--porcelain")
	currentDirty := strings.TrimSpace(porcelain) != ""
	currentDirtySHA := ""
	if currentDirty {
		currentDirtySHA = hashDirtyBundleFiles(workDir, porcelain)
	}

	decision := gitBuildCacheDecision{
		CurrentHeadSHA:  currentHead,
		CurrentDirtySHA: currentDirtySHA,
		CurrentDirty:    currentDirty,
	}

	// 2. First build (no recorded SHA) — must build.
	if status.LastBuiltGitSHA == "" {
		decision.Reason = "no prior build snapshot recorded — first build"
		return decision
	}

	// 3. HEAD moved? Walk the diff and check whether anything bundle-
	//    relevant landed.
	if status.LastBuiltGitSHA != currentHead {
		decision.HeadSHAChanged = true
		relevant, err := bundleRelevantPathsBetween(workDir, status.LastBuiltGitSHA, currentHead)
		if err != nil {
			// Diff failed (force-pushed upstream we no longer have, etc.).
			// Conservative: rebuild.
			decision.Reason = fmt.Sprintf("git diff %s..HEAD failed (%v); rebuilding to be safe",
				shortSHA(status.LastBuiltGitSHA), err)
			return decision
		}
		if len(relevant) > 0 {
			decision.Reason = fmt.Sprintf("HEAD moved %s → %s; bundle-relevant files: %s",
				shortSHA(status.LastBuiltGitSHA), shortSHA(currentHead),
				summarizePaths(relevant))
			return decision
		}
		// HEAD moved but only docs/Go/CI/web/etc. → keep cache.
		// Fall through to dirty check.
	}

	// 4. Dirty file changes? If the cached build was from a clean tree
	//    and the tree is now dirty (or vice-versa), the source has
	//    changed. If both are dirty, the file content hash must match.
	if status.LastBuiltGitHasDirty != currentDirty {
		decision.DirtyChanged = true
		if currentDirty {
			decision.Reason = "dirty files appeared since last build (was clean) — rebuilding"
		} else {
			decision.Reason = "dirty files cleared since last build (was dirty) — rebuilding"
		}
		return decision
	}
	if currentDirty && status.LastBuiltSourceTreeSHA != "" &&
		status.LastBuiltSourceTreeSHA != currentDirtySHA {
		decision.DirtyChanged = true
		decision.Reason = "dirty bundle-relevant files changed since last build — rebuilding"
		return decision
	}

	// 5. Everything matches.
	suffix := "clean"
	if currentDirty {
		suffix = "dirty (unchanged)"
	}
	decision.Valid = true
	if decision.HeadSHAChanged {
		decision.Reason = fmt.Sprintf("source unchanged for the bundle (HEAD moved %s → %s but only non-bundle files); %s",
			shortSHA(status.LastBuiltGitSHA), shortSHA(currentHead), suffix)
	} else {
		decision.Reason = fmt.Sprintf("source unchanged since last build (HEAD %s, %s)",
			shortSHA(currentHead), suffix)
	}
	return decision
}

// bundleRelevantPathsBetween returns the bundle-relevant files in the
// `git diff --name-only fromSHA..toSHA` set. Empty list means the diff
// touched nothing the Hermes bundle would pick up.
func bundleRelevantPathsBetween(workDir, fromSHA, toSHA string) ([]string, error) {
	out, err := runGit(workDir, "diff", "--name-only", fromSHA, toSHA)
	if err != nil {
		return nil, err
	}
	var hits []string
	for _, line := range strings.Split(out, "\n") {
		path := strings.TrimSpace(line)
		if path == "" {
			continue
		}
		if isBundleRelevant(path) {
			hits = append(hits, path)
		}
	}
	return hits, nil
}

// hashDirtyBundleFiles produces a stable sha256 over the bundle-relevant
// subset of `git status --porcelain`'s output, including the file
// contents themselves so silent edits don't slip through. Files that
// aren't bundle-relevant (a README edit, a CHANGELOG bump) are ignored
// — they shouldn't invalidate the cache.
//
// O(sum of dirty bundle-relevant file sizes). For typical RN projects
// where dirt is a few JSX files, this is sub-100ms. For projects with
// large untracked binary blobs, isBundleRelevant filters most of them
// out before we ever read bytes.
func hashDirtyBundleFiles(workDir, porcelain string) string {
	type entry struct {
		path   string
		status string
	}
	var entries []entry
	for _, line := range strings.Split(porcelain, "\n") {
		if len(line) < 4 {
			continue
		}
		// porcelain format: "XY path" with renames as "XY path -> newpath"
		statusBytes := line[:2]
		rest := strings.TrimSpace(line[3:])
		// For renames, the right-hand-side is the path that exists now.
		if idx := strings.Index(rest, " -> "); idx >= 0 {
			rest = strings.TrimSpace(rest[idx+4:])
		}
		// Strip surrounding quotes that git adds for paths with whitespace.
		rest = strings.Trim(rest, `"`)
		if rest == "" {
			continue
		}
		if !isBundleRelevant(rest) {
			continue
		}
		entries = append(entries, entry{path: rest, status: statusBytes})
	}
	if len(entries) == 0 {
		// All dirt was outside the bundle — treat as clean for cache
		// purposes. Return a stable sentinel so the caller can detect
		// "nothing bundle-relevant" without needing a second flag.
		return "no-bundle-dirt"
	}
	// Stable order so the hash is deterministic regardless of porcelain
	// line ordering across git versions / locales.
	sort.SliceStable(entries, func(i, j int) bool {
		return entries[i].path < entries[j].path
	})

	h := sha256.New()
	for _, e := range entries {
		fmt.Fprintf(h, "status:%s\npath:%s\n", e.status, e.path)
		full := filepath.Join(workDir, e.path)
		// Deleted files in porcelain (status starts with " D" or "D ")
		// won't exist on disk; just include the status marker.
		fi, err := os.Stat(full)
		if err != nil || fi.IsDir() {
			fmt.Fprintf(h, "missing-or-dir\n")
			continue
		}
		fmt.Fprintf(h, "size:%d\n", fi.Size())
		// Stream the file in chunks so we don't allocate giant buffers
		// for the occasional asset.
		f, err := os.Open(full)
		if err != nil {
			fmt.Fprintf(h, "read-error\n")
			continue
		}
		br := bufio.NewReaderSize(f, 64*1024)
		_, _ = br.WriteTo(h)
		f.Close()
		fmt.Fprintf(h, "\n")
	}
	return hex.EncodeToString(h.Sum(nil))
}

// isBundleRelevant returns true when the path *might* affect the JS
// bundle that the phone runs. The whitelist is "anything not on the
// drop list" — when in doubt, return true and rebuild.
//
// Drop list (paths we know are irrelevant to the bundle):
//   - Markdown / docs
//   - The agent's own dotfiles (.gitignore, .gitattributes, LICENSE,
//     CHANGELOG, AUTHORS)
//   - .github / .vscode / .idea
//   - Other monorepo surfaces that don't ship in the mobile bundle:
//     desktop/agent/, cli/, web/, relay/, backend/, sdk/, scripts/,
//     e2e/, ci/, docs/, demo/
//   - Tests: anything under __tests__/ or *_test.go / *.test.ts(x) /
//     *.spec.ts(x)
//   - Release coordination: versions.json, CHANGELOG.md
//   - Go source files (*.go) — never enter the JS bundle
//
// Everything else is bundle-relevant by default.
func isBundleRelevant(path string) bool {
	if path == "" {
		return false
	}
	lower := strings.ToLower(path)

	// Doc / repo metadata files anywhere in the tree.
	base := filepath.Base(lower)
	switch base {
	case "readme.md", "license", "license.md", "license.txt",
		"changelog.md", "changelog", "authors", "contributing.md",
		".gitignore", ".gitattributes", ".editorconfig", ".npmrc",
		"versions.json":
		return false
	}
	if strings.HasSuffix(lower, ".md") {
		return false
	}

	// Go sources never enter the JS bundle.
	if strings.HasSuffix(lower, ".go") {
		return false
	}

	// Test files don't ship in the bundle.
	if strings.HasSuffix(lower, "_test.go") ||
		strings.HasSuffix(lower, ".test.ts") ||
		strings.HasSuffix(lower, ".test.tsx") ||
		strings.HasSuffix(lower, ".test.js") ||
		strings.HasSuffix(lower, ".test.jsx") ||
		strings.HasSuffix(lower, ".spec.ts") ||
		strings.HasSuffix(lower, ".spec.tsx") ||
		strings.HasSuffix(lower, ".spec.js") ||
		strings.HasSuffix(lower, ".spec.jsx") {
		return false
	}
	if strings.Contains(lower, "/__tests__/") || strings.HasPrefix(lower, "__tests__/") {
		return false
	}

	// Top-level dirs that don't ship in the mobile bundle. These match
	// the yaver.io monorepo layout — for projects that aren't this
	// monorepo, none of these prefixes will match and we'll fall
	// through to "bundle-relevant" (correct).
	dropPrefixes := []string{
		".github/", ".vscode/", ".idea/", ".cache/",
		"desktop/agent/", "desktop/electron/",
		"cli/", "web/", "relay/", "backend/", "sdk/",
		"scripts/", "e2e/", "ci/", "docs/", "demo/",
		"node_modules/", ".yaver-build/", ".expo/", "build/",
		"ios/build/", "android/build/", "android/.gradle/",
	}
	for _, p := range dropPrefixes {
		if strings.HasPrefix(lower, p) {
			return false
		}
	}

	// Everything else: assume the bundle picks it up.
	//   - *.{js,jsx,ts,tsx,cjs,mjs}                  (source)
	//   - *.json                                      (config + data)
	//   - package-lock.json / yarn.lock / pnpm-lock   (dep graph)
	//   - app.json / app.config.js / metro.config.js  (bundler config)
	//   - assets / images / fonts                     (referenced from JS)
	//   - ios/* and android/* native overlays         (codegen affects bundle)
	return true
}

// shortSHA already exists in morning_cmd.go.

func summarizePaths(paths []string) string {
	const maxList = 5
	if len(paths) <= maxList {
		return strings.Join(paths, ", ")
	}
	return fmt.Sprintf("%s, +%d more", strings.Join(paths[:maxList], ", "), len(paths)-maxList)
}
