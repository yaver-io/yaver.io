package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// detectMonorepoLineage walks up from `dir` looking for a parent that
// holds either a `.git` directory or a `yaver.workspace.yaml`. If found
// AND the path between dir and the root passes through a well-known
// monorepo subdir (`apps`, `packages`, `mobile`, `web`), returns the
// root and the human-friendly in-monorepo app name. Otherwise returns
// empty strings (project is standalone).
func detectMonorepoLineage(dir string) (root, app string) {
	cur := dir
	for i := 0; i < 6; i++ { // bounded walk — never cross 6 ancestors
		parent := filepath.Dir(cur)
		if parent == cur || parent == "/" {
			return "", ""
		}
		// Identify a monorepo root: has .git (file or dir) OR yaver.workspace.yaml.
		// `.git` can be a directory in normal repos OR a file (a single
		// line `gitdir: ...`) in worktrees and submodules — `os.Stat`
		// covers both, while `projectFileExists` rejects directories.
		hasGit := false
		if _, err := os.Stat(filepath.Join(parent, ".git")); err == nil {
			hasGit = true
		}
		hasManifest := projectFileExists(filepath.Join(parent, "yaver.workspace.yaml"))
		if hasGit || hasManifest {
			rel, err := filepath.Rel(parent, dir)
			if err != nil || rel == "." {
				return "", ""
			}
			parts := strings.Split(rel, string(filepath.Separator))
			if len(parts) == 0 {
				return "", ""
			}
			// Recognise common monorepo subdir layouts.
			if len(parts) >= 2 && (parts[0] == "apps" || parts[0] == "packages") {
				return parent, parts[0] + "/" + parts[1]
			}
			if len(parts) == 1 && (parts[0] == "mobile" || parts[0] == "web" || parts[0] == "client" || parts[0] == "server") {
				return parent, parts[0]
			}
			// `dir` is in a monorepo root but not under a recognised
			// subdir layout — still return the root so the dashboard
			// can group by repo, but use the leaf as the app name.
			return parent, filepath.Base(dir)
		}
		cur = parent
	}
	return "", ""
}

func hasProjectGitContext(dir string) bool {
	if strings.TrimSpace(dir) == "" {
		return false
	}
	if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
		return true
	}
	if root, _ := detectMonorepoLineage(dir); root != "" {
		return true
	}
	cur := dir
	for i := 0; i < 6; i++ {
		parent := filepath.Dir(cur)
		if parent == cur || parent == "/" {
			break
		}
		if _, err := os.Stat(filepath.Join(parent, ".git")); err == nil {
			return true
		}
		cur = parent
	}
	// Trust projects placed directly in a known workspace root
	// (~/Workspace/<proj>, ~/Projects/<proj>, ...) even without .git —
	// rsync'd test boxes and remote slaves often arrive without a
	// .git tree. SDK packages live under node_modules, which is
	// already excluded upstream by the walker's skipDirs.
	parent := filepath.Dir(dir)
	for _, root := range projectDiscoveryRoots() {
		if parent == root {
			return true
		}
	}
	return false
}

// MobileProject represents a discovered mobile project on the dev machine.
type MobileProject struct {
	Name        string `json:"name"`
	Path        string `json:"path"`
	Framework   string `json:"framework"`            // "flutter", "expo", "react-native", "unity", "next", "vite"
	SDKVersion  string `json:"sdkVersion,omitempty"` // e.g. "52.0.0", "55.0.6"
	HasDevBuild bool   `json:"hasDevBuild"`          // true if ios/ or android/ prebuild exists
	Branch      string `json:"branch,omitempty"`
	Remote      string `json:"remote,omitempty"`
	SizeHuman   string `json:"size,omitempty"`
	// Capabilities (Yaver Protocol v1 — surface the dashboard's mode
	// toggle correctly per project). One project can be both web and
	// mobile capable (RN+RN-Web) — that's why these are independent flags.
	WebCapable    bool `json:"webCapable"`    // can be served as web (has react-native-web, expo with --web, next, vite, etc.)
	MobileCapable bool `json:"mobileCapable"` // can run on phone (has react-native, expo, flutter, native iOS/Android)
	// Monorepo lineage. Set when the project lives inside a yaver.workspace.yaml-
	// declared monorepo or under a `apps/*`, `packages/*`, `mobile/` subdir.
	// Lets the dashboard show "carrotbet → apps/web" + "carrotbet → mobile"
	// as separate selectable rows instead of one root-level entry.
	MonorepoRoot   string `json:"monorepoRoot,omitempty"`   // absolute path to monorepo root, or "" if standalone
	MonorepoApp    string `json:"monorepoApp,omitempty"`    // app name within the monorepo (e.g. "web", "mobile")
	ExecutionMode  string `json:"executionMode,omitempty"`  // rn-hermes | web-webview | native-webrtc
	PrimarySurface string `json:"primarySurface,omitempty"` // hermes | webview | webrtc
}

func projectKindLabel(framework string, mobileCapable, webCapable bool) string {
	fw := strings.TrimSpace(strings.ToLower(framework))
	switch fw {
	case "next", "vite":
		return "web"
	case "expo", "react-native", "flutter", "swift", "kotlin":
		return "mobile"
	}
	if mobileCapable && !webCapable {
		return "mobile"
	}
	if webCapable && !mobileCapable {
		return "web"
	}
	if mobileCapable && webCapable {
		return "mobile"
	}
	if fw != "" {
		return fw
	}
	return "project"
}

func repoDisplayName(repoRoot string) string {
	repoName := strings.TrimSpace(filepath.Base(strings.TrimSpace(repoRoot)))
	if repoName == "." || repoName == string(filepath.Separator) {
		return ""
	}
	repoName = strings.TrimSuffix(repoName, ".git")
	if dot := strings.Index(repoName, "."); dot > 0 {
		repoName = repoName[:dot]
	}
	return strings.TrimSpace(repoName)
}

func displayProjectName(repoRoot, monorepoApp, appName, framework string, mobileCapable, webCapable bool) string {
	repoName := repoDisplayName(repoRoot)
	appName = strings.TrimSpace(appName)
	kind := projectKindLabel(framework, mobileCapable, webCapable)
	leaf := strings.TrimSpace(monorepoApp)
	if leaf != "" {
		leaf = strings.TrimSpace(strings.Split(leaf, "/")[len(strings.Split(leaf, "/"))-1])
	}
	subproject := ""
	genericAppName := func(v string) bool {
		switch strings.TrimSpace(strings.ToLower(v)) {
		case "", "workspace", "workspaces", "project", "projects", "app", "apps",
			"code", "src", "dev", "work", "repos", "repo", "mobile app", "web app":
			return true
		default:
			return false
		}
	}
	switch {
	case leaf != "" && !strings.EqualFold(leaf, kind):
		subproject = leaf
	case appName != "" && !genericAppName(appName) && !strings.EqualFold(appName, repoName) && !strings.EqualFold(appName, kind):
		subproject = appName
	}
	if repoName == "" {
		repoName = appName
	}
	if strings.EqualFold(subproject, repoName) {
		subproject = ""
	}
	if repoName == "" {
		repoName = "project"
	}
	// If repoName is itself a generic container directory like "Workspace"
	// or "Projects" (a yaver.workspace.yaml manifest at /home/$USER/Workspace
	// pulls $HOME/Workspace in as the "repo root"), promote the subproject
	// to the primary name so the user sees `sfmg / mobile` instead of
	// `Workspace (sfmg) / mobile`. The container directory carries no
	// product meaning — the leaf is what the user named their project.
	if subproject != "" && genericAppName(repoName) {
		return fmt.Sprintf("%s / %s", subproject, kind)
	}
	if subproject != "" {
		return fmt.Sprintf("%s (%s) / %s", repoName, subproject, kind)
	}
	return fmt.Sprintf("%s / %s", repoName, kind)
}

// demoShowcaseProject identifies projects living in the well-known
// `<repo>/demo/{mobile,web}/<app>/...` showcase layout. These apps
// exist purely to demo the Yaver Feedback SDK and shouldn't carry
// the host repo name in the Hot Reload list — `Bento / mobile` and
// `Todo RN / mobile` read as discoverable showcases, while
// `yaver (todo-rn) / mobile` reads like a yaver internal subproject
// the user might break by tapping. Returns ("", "") for non-demo
// paths so the caller falls through to the standard repo+monorepo
// naming. The check matches anywhere in the path, not only at the
// repo root, so a vendored demo at `<repo>/vendor/yaver/demo/mobile/x`
// still gets the friendly name.
func demoShowcaseProject(dir string) (surface, leaf string) {
	parts := strings.Split(filepath.Clean(dir), string(filepath.Separator))
	for i := 0; i < len(parts)-2; i++ {
		if parts[i] != "demo" {
			continue
		}
		next := parts[i+1]
		if next != "mobile" && next != "web" {
			continue
		}
		return next, parts[i+2]
	}
	return "", ""
}

func repoRootForProject(dir string) string {
	if root, _ := detectMonorepoLineage(dir); strings.TrimSpace(root) != "" {
		return root
	}
	cur := strings.TrimSpace(dir)
	for i := 0; i < 6 && cur != "" && cur != "/" && cur != "."; i++ {
		if _, err := os.Stat(filepath.Join(cur, ".git")); err == nil {
			return cur
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			break
		}
		cur = parent
	}
	return ""
}

// Known Expo SDK versions and their compatibility
var knownExpoSDKs = map[string]string{
	"52": "React Native 0.76",
	"53": "React Native 0.77",
	"54": "React Native 0.78",
	"55": "React Native 0.79",
}

// ── Mobile project cache ──────────────────────────────────────────────

var mobileProjectCache struct {
	mu        sync.RWMutex
	projects  []MobileProject
	scannedAt time.Time
	scanning  bool
	cancel    bool
	// stats captures WHY a scan ended — so the mobile diagnostic can tell
	// "scan found nothing" from "scan couldn't read your files (grant Full
	// Disk Access)" from "scan ran out of time". Without this, a
	// permission-blocked or runaway walk was indistinguishable from a
	// genuinely empty machine.
	stats mobileScanStats
}

// mobileScanStats summarises the last scan for diagnostics.
type mobileScanStats struct {
	PermDenied int           `json:"permDenied"` // dirs skipped due to EACCES/EPERM (macOS TCC)
	TimedOut   bool          `json:"timedOut"`   // hit mobileScanTimeout before finishing
	Elapsed    time.Duration `json:"-"`
	ElapsedMs  int64         `json:"elapsedMs"`
	Err        string        `json:"error,omitempty"`
}

const mobileProjectsCacheFileName = "mobile-projects.json"

type persistedMobileProjectsCache struct {
	Projects  []MobileProject `json:"projects"`
	ScannedAt string          `json:"scannedAt"`
	Stats     mobileScanStats `json:"stats"`
}

func mobileProjectsCachePath() (string, error) {
	dir, err := yaverDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, mobileProjectsCacheFileName), nil
}

func loadPersistedMobileProjectsCache() ([]MobileProject, time.Time, mobileScanStats, bool) {
	path, err := mobileProjectsCachePath()
	if err != nil {
		return nil, time.Time{}, mobileScanStats{}, false
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, time.Time{}, mobileScanStats{}, false
	}
	var cached persistedMobileProjectsCache
	if err := json.Unmarshal(raw, &cached); err != nil {
		return nil, time.Time{}, mobileScanStats{}, false
	}
	scannedAt, err := time.Parse(time.RFC3339, strings.TrimSpace(cached.ScannedAt))
	if err != nil {
		return nil, time.Time{}, mobileScanStats{}, false
	}
	return cached.Projects, scannedAt, cached.Stats, true
}

func savePersistedMobileProjectsCache(projects []MobileProject, scannedAt time.Time, stats mobileScanStats) {
	path, err := mobileProjectsCachePath()
	if err != nil {
		log.Printf("[mobile-scan] cache path unavailable: %v", err)
		return
	}
	payload, err := json.MarshalIndent(persistedMobileProjectsCache{
		Projects:  projects,
		ScannedAt: scannedAt.UTC().Format(time.RFC3339),
		Stats:     stats,
	}, "", "  ")
	if err != nil {
		log.Printf("[mobile-scan] marshal cache: %v", err)
		return
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, payload, 0o600); err != nil {
		log.Printf("[mobile-scan] write cache: %v", err)
		return
	}
	if err := os.Rename(tmp, path); err != nil {
		log.Printf("[mobile-scan] rename cache: %v", err)
		_ = os.Remove(tmp)
	}
}

func hydrateMobileProjectCacheFromDisk() bool {
	projects, scannedAt, stats, ok := loadPersistedMobileProjectsCache()
	if !ok {
		return false
	}
	mobileProjectCache.mu.Lock()
	if mobileProjectCache.projects == nil || len(mobileProjectCache.projects) == 0 || mobileProjectCache.scannedAt.Before(scannedAt) {
		mobileProjectCache.projects = projects
		mobileProjectCache.scannedAt = scannedAt
		mobileProjectCache.stats = stats
	}
	mobileProjectCache.mu.Unlock()
	return true
}

// mobileScanTimeout bounds a single scan. The walk checks the deadline on
// every directory so `scanning` ALWAYS resolves to false — a slow or
// permission-blocked home-directory walk can no longer leave the mobile
// "Discovering apps…" spinner running forever. 45s is generous for a
// healthy machine (a normal scan settles in 1-3s) but caps the worst case.
const mobileScanTimeout = 45 * time.Second

var errStopMobileScan = errors.New("mobile project scan cancelled")
var errMobileScanDeadline = errors.New("mobile project scan deadline exceeded")

// scanMobileProjects walks workspace roots looking for mobile projects.
// Detects: pubspec.yaml (Flutter), package.json with expo/react-native,
// and Unity projects via ProjectSettings/ProjectVersion.txt.
// Skips: node_modules, .git, build artifacts, system dirs, caches.
// scanMobileProjects runs a scan with the default timeout, discarding
// stats. Kept as the back-compat entry point for the many callers
// (PrewarmMobileProjects, diagnose, repos_http, etc.) that just want the
// project list.
func scanMobileProjects() []MobileProject {
	projects, _ := scanMobileProjectsWithDeadline(time.Now().Add(mobileScanTimeout))
	return projects
}

// scanMobileProjectsWithDeadline is the real scanner. It stops early when
// `deadline` passes (returning whatever it found so far + TimedOut=true)
// and counts permission-denied directories instead of silently skipping
// them, so callers can surface "grant Full Disk Access" on macOS.
func scanMobileProjectsWithDeadline(deadline time.Time) ([]MobileProject, mobileScanStats) {
	var stats mobileScanStats
	roots := projectDiscoveryRoots()
	if len(roots) == 0 {
		return nil, stats
	}

	skipDirs := map[string]bool{
		"node_modules": true, ".git": true, "build": true, "dist": true,
		".cache": true, ".local": true, ".cargo": true, ".rustup": true,
		"Library": true, "Applications": true, "Music": true, "Movies": true,
		"Pictures": true, "Documents": true, "Public": true, "Downloads": true,
		"Desktop": true, ".Trash": true, "Pods": true, ".cocoapods": true,
		".gradle": true, ".android": true, ".pub-cache": true,
		"android": true, "ios": true, ".dart_tool": true,
		".expo": true, ".next": true, "vendor": true,
		"homebrew": true, "Cellar": true, "Caskroom": true,
	}

	// Skip entire directories that are SDKs/tools (not user projects)
	skipPaths := []string{
		"/development/flutter/", // Flutter SDK
		"/flutter/bin/",
		"/.pub-cache/",
		"/sdk/",
	}

	var projects []MobileProject
	seen := map[string]bool{}
	seenRoot := map[string]bool{}

	for _, root := range roots {
		if root == "" || seenRoot[root] {
			continue
		}
		seenRoot[root] = true

		err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
			mobileProjectCache.mu.RLock()
			cancelled := mobileProjectCache.cancel
			mobileProjectCache.mu.RUnlock()
			if cancelled {
				return errStopMobileScan
			}
			// Hard deadline — guarantees `scanning` resolves even if a
			// huge home dir or a hung network mount would otherwise walk
			// forever. Cheap enough to check on every entry.
			if !deadline.IsZero() && time.Now().After(deadline) {
				return errMobileScanDeadline
			}
			if err != nil {
				// Permission denied (macOS TCC / Full Disk Access not
				// granted) is the single most common cause of an empty or
				// stuck scan. Count it so the diagnostic can say so out
				// loud instead of the walk silently swallowing it.
				if os.IsPermission(err) {
					stats.PermDenied++
				}
				return filepath.SkipDir
			}

			// Skip hidden dirs and known non-project dirs
			name := info.Name()
			if info.IsDir() {
				if strings.HasPrefix(name, ".") && name != "." {
					if name != ".config" {
						return filepath.SkipDir
					}
				}
				if skipDirs[name] {
					return filepath.SkipDir
				}
				// Skip SDK/tool paths
				for _, sp := range skipPaths {
					if strings.Contains(path, sp) {
						return filepath.SkipDir
					}
				}
				// Limit depth per root to catch monorepos without walking the whole disk.
				rel, _ := filepath.Rel(root, path)
				if strings.Count(rel, string(os.PathSeparator)) > 7 {
					return filepath.SkipDir
				}
				return nil
			}

			dir := filepath.Dir(path)

			// Already found a project in this dir
			if seen[dir] {
				return nil
			}

			var framework string
			// Capability flags computed on detection so the dashboard
			// can route Web App tab vs Mobile App tab without a second
			// pass over the project's package.json.
			webCapable := false
			mobileCapable := false

			switch name {
			case "pubspec.yaml":
				framework = "flutter"
				mobileCapable = true
			case "package.json":
				data, err := os.ReadFile(path)
				if err != nil {
					return nil
				}
				content := string(data)
				switch {
				case strings.Contains(content, `"expo"`):
					framework = "expo"
					mobileCapable = true
					// Expo with react-native-web → also web-capable
					// (`expo --web` / `expo export -p web` work).
					if strings.Contains(content, `"react-native-web"`) {
						webCapable = true
					}
				case strings.Contains(content, `"react-native"`):
					// Verify it's an actual RN app (has dependencies, not just a keyword match)
					if strings.Contains(content, `"dependencies"`) &&
						(strings.Contains(content, `"react-native":`) || strings.Contains(content, `"react-native" :`)) {
						framework = "react-native"
						mobileCapable = true
						if strings.Contains(content, `"react-native-web"`) {
							webCapable = true
						}
					}
				case strings.Contains(content, `"next":`) || strings.Contains(content, `"next" :`):
					framework = "next"
					webCapable = true
				case strings.Contains(content, `"vite":`) || strings.Contains(content, `"vite" :`):
					framework = "vite"
					webCapable = true
				}
			case "ProjectVersion.txt":
				if filepath.Base(dir) == "ProjectSettings" &&
					projectFileExists(filepath.Join(dir, "ProjectSettings.asset")) &&
					projectFileExists(filepath.Join(filepath.Dir(dir), "Packages", "manifest.json")) {
					dir = filepath.Dir(dir)
					framework = "unity"
					mobileCapable = true
				}
			case "Package.swift":
				framework = "swift"
				mobileCapable = true
			case "project.yml":
				// xcodegen project descriptor. Treat as Swift only when
				// it explicitly declares an iOS platform — otherwise
				// project.yml could be a Linux Swift package or any
				// other xcodegen target type.
				if isXcodegenIOSProject(dir) {
					framework = "swift"
					mobileCapable = true
				}
			case "build.gradle.kts", "build.gradle", "settings.gradle.kts", "settings.gradle":
				if isKotlinAndroidProject(dir) {
					framework = "kotlin"
					mobileCapable = true
					// Walk visits children alphabetically, so
					// `todo-kt/app/build.gradle.kts` fires before
					// `todo-kt/settings.gradle.kts`. Peek at the
					// parent now: if it carries a Gradle root that
					// includes the current dir as a sub-module, the
					// real project is the parent — return nil here
					// and let the parent's settings file register
					// when the walker reaches it.
					if isKotlinSubmoduleOfParent(dir) {
						return nil
					}
				}
			default:
				return nil
			}

			if framework == "" {
				return nil
			}

			// Suppress modules nested inside an already-detected project
			// of the same framework — most often Android Gradle modules
			// (`<root>/app/build.gradle.kts`) showing up after the
			// project root (`<root>/settings.gradle.kts`) was already
			// added. The :app sub-module is not a separate Hot Reload
			// target; the user wants the project root.
			nestedDup := false
			for _, existing := range projects {
				if existing.Framework != framework {
					continue
				}
				if dir == existing.Path {
					continue
				}
				if strings.HasPrefix(dir+string(filepath.Separator), existing.Path+string(filepath.Separator)) {
					nestedDup = true
					break
				}
			}
			if nestedDup {
				return nil
			}

			seen[dir] = true

			// Skip if this is inside another project's subdirectory or an SDK/library
			parentName := filepath.Base(dir)
			if parentName == "example" || parentName == "test" || parentName == "e2e" {
				return nil
			}
			// Skip if it's a library/SDK (not a real app) by requiring some
			// nearby git context. Walk more than two ancestors so fixture apps
			// under tests/fixtures/ inside a larger repo still show up in Hot
			// Reload and remote-runtime testing.
			if !hasProjectGitContext(dir) {
				return nil
			}

			// Parse real app name from framework config files
			appName := parseAppName(dir, framework)
			if appName == "" {
				appName = filepath.Base(dir)
			}
			repoRoot := repoRootForProject(dir)
			monorepoApp := ""

			// Detect SDK version and dev build status
			sdkVersion := detectExpoSDK(dir, framework)
			hasDevBuild := fileExists(filepath.Join(dir, "ios", "Podfile")) ||
				fileExists(filepath.Join(dir, "android", "build.gradle")) ||
				framework == "unity"

			proj := MobileProject{
				Name:           displayProjectName(repoRoot, monorepoApp, appName, framework, mobileCapable, webCapable),
				Path:           dir,
				Framework:      framework,
				SDKVersion:     sdkVersion,
				HasDevBuild:    hasDevBuild,
				WebCapable:     webCapable,
				MobileCapable:  mobileCapable,
				ExecutionMode:  string(executionModeForFramework(framework)),
				PrimarySurface: primarySurfaceForFramework(framework),
			}
			// Monorepo lineage detection. If `dir` is N levels under a
			// directory that has its own .git AND is also one of the
			// well-known monorepo subdirs (`apps/<app>`, `packages/<pkg>`,
			// `mobile`, `web`), record the root + the in-monorepo app
			// name so the dashboard can show "carrotbet → mobile" and
			// "carrotbet → apps/web" as separate rows under the same
			// repo. Standalone repos leave both fields empty.
			if root, app := detectMonorepoLineage(dir); root != "" {
				monorepoApp = app
				proj.MonorepoRoot = root
				proj.MonorepoApp = app
				proj.Name = displayProjectName(root, app, appName, framework, mobileCapable, webCapable)
			} else if repoRoot != "" {
				proj.Name = displayProjectName(repoRoot, "", appName, framework, mobileCapable, webCapable)
			}

			// Demo-showcase override. Apps under `demo/{mobile,web}/<app>/`
			// are video-demo showcases (Todo RN, Bento, etc.) — always
			// display them as `<app or app.json name> / <surface>`,
			// dropping the host repo prefix. Without this, every demo
			// inside yaver.io renders as `yaver (todo-rn) / mobile`,
			// which makes them look like yaver internals rather than
			// safe demo targets. Always prefer appName when present
			// (preserves the user's casing — `app.json name:"Bento"`
			// must render as `Bento`, not `bento` from the dir leaf
			// even though the two match case-insensitively).
			if surf, leaf := demoShowcaseProject(dir); surf != "" {
				display := strings.TrimSpace(appName)
				if display == "" {
					display = leaf
				}
				proj.Name = fmt.Sprintf("%s / %s", display, surf)
			}

			// Get git info (fast — just reads local files)
			if branch, err := runGit(dir, "rev-parse", "--abbrev-ref", "HEAD"); err == nil {
				proj.Branch = branch
			}
			if remote, err := runGit(dir, "config", "--get", "remote.origin.url"); err == nil {
				// Sanitize credentials from URL
				if idx := strings.Index(remote, "@"); idx > 0 && strings.Contains(remote[:idx], "://") {
					remote = remote[:strings.Index(remote, "://")+3] + remote[idx+1:]
				}
				proj.Remote = remote
			}

			// Quick size estimate (du -sh, first line)
			proj.SizeHuman = dirSizeHuman(dir)

			projects = append(projects, proj)
			return nil
		})
		if errors.Is(err, errStopMobileScan) {
			log.Printf("[mobile-scan] Scan cancelled while walking %s", root)
			return projects, stats
		}
		if errors.Is(err, errMobileScanDeadline) {
			stats.TimedOut = true
			log.Printf("[mobile-scan] Scan hit deadline while walking %s (found %d so far, permDenied=%d)",
				root, len(projects), stats.PermDenied)
			return projects, stats
		}
	}

	return projects, stats
}

// runMobileScan executes a single bounded scan and writes the result +
// stats back into the cache. It is guarded so only one scan runs at a
// time (concurrent callers no-op), and it ALWAYS clears `scanning` —
// even on panic — so the mobile UI can never get stuck on the spinner.
// Run it in a goroutine; HTTP handlers must never block on it.
func runMobileScan(reason string) {
	mobileProjectCache.mu.Lock()
	if mobileProjectCache.scanning {
		mobileProjectCache.mu.Unlock()
		return
	}
	mobileProjectCache.cancel = false
	mobileProjectCache.scanning = true
	mobileProjectCache.mu.Unlock()

	start := time.Now()
	var projects []MobileProject
	var stats mobileScanStats
	func() {
		defer func() {
			if rec := recover(); rec != nil {
				stats.Err = fmt.Sprintf("scan panic: %v", rec)
				log.Printf("[mobile-scan] panic during %s: %v", reason, rec)
			}
		}()
		projects, stats = scanMobileProjectsWithDeadline(start.Add(mobileScanTimeout))
	}()
	stats.Elapsed = time.Since(start)
	stats.ElapsedMs = stats.Elapsed.Milliseconds()

	mobileProjectCache.mu.Lock()
	mobileProjectCache.projects = projects
	scannedAt := time.Now()
	mobileProjectCache.scannedAt = scannedAt
	mobileProjectCache.scanning = false
	mobileProjectCache.stats = stats
	mobileProjectCache.mu.Unlock()
	savePersistedMobileProjectsCache(projects, scannedAt, stats)

	log.Printf("[mobile-scan] %s: %d projects in %dms (permDenied=%d timedOut=%v)",
		reason, len(projects), stats.ElapsedMs, stats.PermDenied, stats.TimedOut)
}

func requestForcedMobileScan(reason string) {
	mobileProjectCache.mu.Lock()
	wasScanning := mobileProjectCache.scanning
	if wasScanning {
		mobileProjectCache.cancel = true
	}
	mobileProjectCache.mu.Unlock()

	go func() {
		if wasScanning {
			for i := 0; i < 100; i++ {
				time.Sleep(50 * time.Millisecond)
				mobileProjectCache.mu.RLock()
				scanning := mobileProjectCache.scanning
				mobileProjectCache.mu.RUnlock()
				if !scanning {
					break
				}
			}
		}
		runMobileScan(reason)
	}()
}

// dirSizeHuman returns a human-readable size of a directory (e.g. "42M").
// Skips heavy dirs (node_modules, .git, build) for speed.
func dirSizeHuman(dir string) string {
	var total int64
	count := 0
	filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return filepath.SkipDir
		}
		if info.IsDir() {
			n := info.Name()
			if n == "node_modules" || n == ".git" || n == "build" || n == "Pods" || n == ".dart_tool" || n == ".gradle" {
				return filepath.SkipDir
			}
		} else {
			total += info.Size()
		}
		count++
		if count > 5000 {
			return filepath.SkipAll
		}
		return nil
	})

	switch {
	case total < 1024:
		return "<1K"
	case total < 1024*1024:
		return fmt.Sprintf("%dK", total/1024)
	case total < 1024*1024*1024:
		return fmt.Sprintf("%dM", total/(1024*1024))
	default:
		return fmt.Sprintf("%.1fG", float64(total)/(1024*1024*1024))
	}
}

// ── SDK detection ─────────────────────────────────────────────────────

// detectExpoSDK reads the Expo SDK version from package.json.
func detectExpoSDK(dir, framework string) string {
	if framework == "unity" {
		return detectUnityVersion(dir)
	}
	if framework != "expo" && framework != "react-native" {
		return ""
	}
	data, err := os.ReadFile(filepath.Join(dir, "package.json"))
	if err != nil {
		return ""
	}
	var pkg map[string]interface{}
	if err := json.Unmarshal(data, &pkg); err != nil {
		return ""
	}
	deps, _ := pkg["dependencies"].(map[string]interface{})
	if deps == nil {
		return ""
	}
	expoVer, _ := deps["expo"].(string)
	// Strip semver prefix: "~52.0.0" → "52.0.0", "^55.0.6" → "55.0.6"
	expoVer = strings.TrimLeft(expoVer, "~^>=<")
	return expoVer
}

func detectUnityVersion(dir string) string {
	data, err := os.ReadFile(filepath.Join(dir, "ProjectSettings", "ProjectVersion.txt"))
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "m_EditorVersion:") {
			return strings.TrimSpace(strings.TrimPrefix(line, "m_EditorVersion:"))
		}
	}
	return ""
}

// ── Startup prebuild ──────────────────────────────────────────────────

// PrewarmMobileProjects runs on `yaver serve` startup.
// findMobileProjectByName returns the cached MobileProject whose Name
// matches the supplied identifier (case-insensitive, trimmed). Used by
// /dev/build-native + /dev/reload-app to resolve which project to
// build when the Feedback SDK sends a `projectName` hint — needed
// because TestFlight-installed apps never run `yaver dev start` so
// the agent has no "active" dev server to fall back on. Returns nil
// if the cache is empty (cold start) or no match.
func findMobileProjectByName(name string) *MobileProject {
	if name == "" {
		return nil
	}
	target := strings.ToLower(strings.TrimSpace(name))
	mobileProjectCache.mu.RLock()
	defer mobileProjectCache.mu.RUnlock()
	for i := range mobileProjectCache.projects {
		if strings.EqualFold(strings.TrimSpace(mobileProjectCache.projects[i].Name), target) {
			return &mobileProjectCache.projects[i]
		}
	}
	return nil
}

func findMobileProjectByPath(path string) *MobileProject {
	target := strings.TrimSpace(path)
	if target == "" {
		return nil
	}
	mobileProjectCache.mu.RLock()
	defer mobileProjectCache.mu.RUnlock()
	for i := range mobileProjectCache.projects {
		if strings.TrimSpace(mobileProjectCache.projects[i].Path) == target {
			return &mobileProjectCache.projects[i]
		}
	}
	return nil
}

// findMobileProjectByBundleID resolves a mobile project by iOS bundle
// identifier. Reads each cached project's ios/*/Info.plist lazily (no
// upfront indexing) since bundle IDs aren't in the cache schema yet —
// one Info.plist read per project at worst, and the cache is typically
// < 20 entries. Returns the first match.
//
// Used by /vibing/execute and /feedback/.../fix so the SDK can route
// a "vibe on THIS app" request to the right repo instead of relying
// on the legacy prompt-substring matcher, which picked the wrong
// project when a common word (e.g. "in") matched an unrelated repo.
func findMobileProjectByBundleID(bundleID string) *MobileProject {
	target := strings.TrimSpace(bundleID)
	if target == "" {
		return nil
	}
	mobileProjectCache.mu.RLock()
	projects := make([]MobileProject, len(mobileProjectCache.projects))
	copy(projects, mobileProjectCache.projects)
	mobileProjectCache.mu.RUnlock()

	for i := range projects {
		if projectBundleIDMatches(projects[i].Path, target) {
			return &projects[i]
		}
	}
	return nil
}

// projectBundleIDMatches checks a project's iOS and Android manifests
// for the given identifier. Both platforms are first-class — an
// Android-only project resolves just as well as an iOS-only one, and
// a cross-platform project resolves from whichever manifest loads
// first. Best-effort: any read error is treated as "not a match" so
// a single broken project can't poison lookups for the rest.
//
// iOS identifier surfaces checked:
//   - ios/<App>/Info.plist literal CFBundleIdentifier
//   - ios/<App>.xcodeproj/project.pbxproj PRODUCT_BUNDLE_IDENTIFIER
//   - app.json / app.config.json expo.ios.bundleIdentifier
//
// Android identifier surfaces checked:
//   - android/app/build.gradle applicationId (Groovy + KTS syntax)
//   - android/app/src/main/AndroidManifest.xml package= attribute
//     (AGP 7 legacy) and namespace in build.gradle (AGP 8+)
//   - app.json / app.config.json expo.android.package
//
// RN / Expo projects share a bundle id between platforms by
// convention, so a match on EITHER side identifies the project
// correctly.
func projectBundleIDMatches(projectPath, bundleID string) bool {
	if projectPath == "" || bundleID == "" {
		return false
	}
	if iosProjectHasBundleID(projectPath, bundleID) {
		return true
	}
	if androidProjectHasBundleID(projectPath, bundleID) {
		return true
	}
	return expoConfigHasBundleID(projectPath, bundleID)
}

func iosProjectHasBundleID(projectPath, bundleID string) bool {
	iosDir := filepath.Join(projectPath, "ios")
	entries, err := os.ReadDir(iosDir)
	if err != nil {
		return false
	}
	quoted := `PRODUCT_BUNDLE_IDENTIFIER = "` + bundleID + `"`
	unquoted := `PRODUCT_BUNDLE_IDENTIFIER = ` + bundleID + `;`
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		// Two directory layouts to handle:
		//   ios/<AppName>/Info.plist           — an app target source folder
		//   ios/<AppName>.xcodeproj/project.pbxproj — the Xcode project
		// The earlier revision of this function always appended
		// ".xcodeproj" which produced the double-suffix path
		// "MyApp.xcodeproj.xcodeproj" and missed every real project.
		if strings.HasSuffix(name, ".xcodeproj") {
			pbx := filepath.Join(iosDir, name, "project.pbxproj")
			if data, err := os.ReadFile(pbx); err == nil {
				s := string(data)
				if strings.Contains(s, quoted) || strings.Contains(s, unquoted) {
					return true
				}
			}
			continue
		}
		plist := filepath.Join(iosDir, name, "Info.plist")
		if data, err := os.ReadFile(plist); err == nil {
			if strings.Contains(string(data), bundleID) {
				return true
			}
		}
	}
	return false
}

func androidProjectHasBundleID(projectPath, bundleID string) bool {
	// 1. app/build.gradle — Groovy: `applicationId "x"` / `applicationId 'x'`
	//    Kotlin DSL: `applicationId = "x"`
	//    Plus AGP 8+ `namespace = "x"` (used in newer RN templates)
	for _, gradleName := range []string{"build.gradle", "build.gradle.kts"} {
		gradle := filepath.Join(projectPath, "android", "app", gradleName)
		if data, err := os.ReadFile(gradle); err == nil {
			s := string(data)
			if strings.Contains(s, `applicationId "`+bundleID+`"`) ||
				strings.Contains(s, `applicationId '`+bundleID+`'`) ||
				strings.Contains(s, `applicationId = "`+bundleID+`"`) ||
				strings.Contains(s, `namespace "`+bundleID+`"`) ||
				strings.Contains(s, `namespace = "`+bundleID+`"`) {
				return true
			}
		}
	}
	// 2. AndroidManifest.xml — older templates carry `package="x"`
	manifest := filepath.Join(projectPath, "android", "app", "src", "main", "AndroidManifest.xml")
	if data, err := os.ReadFile(manifest); err == nil {
		if strings.Contains(string(data), `package="`+bundleID+`"`) {
			return true
		}
	}
	return false
}

// expoConfigHasBundleID checks Expo's declarative config. Expo sets
// BOTH ios.bundleIdentifier and android.package — checking both here
// so an Android-only app.json still matches even when the iOS section
// is absent, and vice versa.
// expoConfigHasBundleID reports whether an Expo manifest declares bundleID
// for either platform.
//
// The manifest is parsed rather than substring-matched. The previous
// implementation looked for the literal `"bundleIdentifier": "<id>"`, which
// silently depended on the file being pretty-printed with exactly one space
// after the colon — reformat app.json (or emit it from a tool) and the match
// evaporated. That matters more now that feedback→fix routing keys off the
// bundle id: for an Expo app that hasn't run prebuild there is no ios/ or
// android/ directory, so the manifest is the *only* surface identifying the
// project, and a miss sends the fix task to the agent's own working
// directory instead. Falls back to the substring check if the manifest isn't
// valid JSON (e.g. hand-edited with a trailing comma) so a broken file
// degrades to the old behaviour rather than to "no match".
func expoConfigHasBundleID(projectPath, bundleID string) bool {
	if bundleID == "" {
		return false
	}
	for _, f := range []string{"app.json", "app.config.json"} {
		data, err := os.ReadFile(filepath.Join(projectPath, f))
		if err != nil {
			continue
		}
		var manifest struct {
			Expo struct {
				IOS struct {
					BundleIdentifier string `json:"bundleIdentifier"`
				} `json:"ios"`
				Android struct {
					Package string `json:"package"`
				} `json:"android"`
			} `json:"expo"`
		}
		if err := json.Unmarshal(data, &manifest); err != nil {
			s := string(data)
			if strings.Contains(s, `"bundleIdentifier": "`+bundleID+`"`) ||
				strings.Contains(s, `"package": "`+bundleID+`"`) {
				return true
			}
			continue
		}
		if manifest.Expo.IOS.BundleIdentifier == bundleID ||
			manifest.Expo.Android.Package == bundleID {
			return true
		}
	}
	return false
}

// Scans all mobile projects, checks dev builds, and pre-builds missing ones in background.
func PrewarmMobileProjects() {
	log.Println("[mobile-scan] Scanning for mobile projects...")
	projects := scanMobileProjects()
	stats := mobileScanStats{}

	mobileProjectCache.mu.Lock()
	mobileProjectCache.projects = projects
	scannedAt := time.Now()
	mobileProjectCache.scannedAt = scannedAt
	mobileProjectCache.stats = stats
	mobileProjectCache.mu.Unlock()
	savePersistedMobileProjectsCache(projects, scannedAt, stats)

	log.Printf("[mobile-scan] Found %d mobile projects", len(projects))

	// Log summary
	for _, p := range projects {
		status := "ready"
		if !p.HasDevBuild {
			status = "needs prebuild"
		}
		sdk := p.SDKVersion
		if sdk == "" {
			sdk = "n/a"
		}
		log.Printf("[mobile-scan]   %s (%s, SDK %s) — %s [%s]", p.Name, p.Framework, sdk, status, p.Path)
	}

	// Pre-build dev clients for Expo/RN projects that don't have one
	// This runs in background — user can start hot reload immediately for projects that already have builds
	for _, p := range projects {
		if (p.Framework == "expo" || p.Framework == "react-native") && !p.HasDevBuild {
			go prebuildExpoProject(p)
		}
	}
}

// prebuildExpoProject runs `npx expo prebuild` + `pod install` for a project.
func prebuildExpoProject(p MobileProject) {
	log.Printf("[mobile-prebuild] Pre-building %s (%s SDK %s)...", p.Name, p.Framework, p.SDKVersion)

	// Check if node_modules exist, install if not
	if !fileExists(filepath.Join(p.Path, "node_modules")) {
		log.Printf("[mobile-prebuild] Installing deps for %s...", p.Name)
		install := exec.Command("npm", "install", "--legacy-peer-deps")
		install.Dir = p.Path
		logW := &devLogWriter{prefix: fmt.Sprintf("[prebuild:%s:npm]", p.Name)}
		install.Stdout = logW
		install.Stderr = logW
		if err := install.Run(); err != nil {
			log.Printf("[mobile-prebuild] npm install failed for %s: %v", p.Name, err)
			return
		}
	}

	// Run expo prebuild
	prebuild := exec.Command("npx", "expo", "prebuild", "--no-install")
	prebuild.Dir = p.Path
	logW := &devLogWriter{prefix: fmt.Sprintf("[prebuild:%s]", p.Name)}
	prebuild.Stdout = logW
	prebuild.Stderr = logW
	if err := prebuild.Run(); err != nil {
		log.Printf("[mobile-prebuild] Prebuild failed for %s: %v", p.Name, err)
		return
	}

	// Install CocoaPods for iOS
	if fileExists(filepath.Join(p.Path, "ios", "Podfile")) {
		log.Printf("[mobile-prebuild] Installing pods for %s...", p.Name)
		pods := exec.Command("pod", "install")
		pods.Dir = filepath.Join(p.Path, "ios")
		podsLog := &devLogWriter{prefix: fmt.Sprintf("[prebuild:%s:pods]", p.Name)}
		pods.Stdout = podsLog
		pods.Stderr = podsLog
		pods.Run() // best-effort
	}

	// Update cache
	mobileProjectCache.mu.Lock()
	for i := range mobileProjectCache.projects {
		if mobileProjectCache.projects[i].Path == p.Path {
			mobileProjectCache.projects[i].HasDevBuild = true
			break
		}
	}
	mobileProjectCache.mu.Unlock()

	log.Printf("[mobile-prebuild] %s ready for hot reload", p.Name)
}

// ── App name parsing ──────────────────────────────────────────────────

// parseAppName reads the real app name from framework config files.
// Priority order matches each platform's display surface — what the
// home-screen launcher renders, not the package id — so the Hot
// Reload list shows "Todo Kt" instead of "yaver-fixture-native-android".
//   - Expo/RN: app.json → expo.name → top-level name → package.json
//   - Flutter: AndroidManifest android:label (literal) → iOS Info.plist
//     CFBundleDisplayName → pubspec.yaml `name:`
//   - Unity:   ProjectSettings/ProjectSettings.asset productName
//   - Kotlin:  res/values/strings.xml `app_name` → settings.gradle
//     rootProject.name
//   - Swift:   <App>/Info.plist CFBundleDisplayName → CFBundleName →
//     project.yml INFOPLIST_KEY_CFBundleDisplayName → project.yml `name:`
func parseAppName(dir, framework string) string {
	switch framework {
	case "expo", "react-native":
		return parseExpoAppName(dir)
	case "flutter":
		return parseFlutterAppName(dir)
	case "unity":
		return parseUnityAppName(dir)
	case "kotlin":
		return parseKotlinAppName(dir)
	case "swift":
		return parseSwiftAppName(dir)
	}
	return ""
}

func parseExpoAppName(dir string) string {
	// Try app.json first
	for _, fname := range []string{"app.json", "app.config.json"} {
		data, err := os.ReadFile(filepath.Join(dir, fname))
		if err != nil {
			continue
		}
		// Parse as JSON — could be { "expo": { "name": "X" } } or { "name": "X" }
		var raw map[string]json.RawMessage
		if err := json.Unmarshal(data, &raw); err != nil {
			continue
		}
		// Check expo.name first
		if expoRaw, ok := raw["expo"]; ok {
			var expo map[string]interface{}
			if err := json.Unmarshal(expoRaw, &expo); err == nil {
				if name, ok := expo["name"].(string); ok && name != "" {
					return name
				}
			}
		}
		// Fallback: top-level name
		if nameRaw, ok := raw["name"]; ok {
			var name string
			if err := json.Unmarshal(nameRaw, &name); err == nil && name != "" {
				return name
			}
		}
	}

	// Fallback: package.json name
	data, err := os.ReadFile(filepath.Join(dir, "package.json"))
	if err != nil {
		return ""
	}
	var pkg map[string]interface{}
	if err := json.Unmarshal(data, &pkg); err != nil {
		return ""
	}
	if name, ok := pkg["name"].(string); ok {
		return name
	}
	return ""
}

func parseFlutterAppName(dir string) string {
	// 1. AndroidManifest android:label="..." (literal). Skip @string
	//    refs — they require a strings.xml round-trip and the pubspec
	//    name is a fine fallback if the developer hasn't set a literal.
	manifest := filepath.Join(dir, "android", "app", "src", "main", "AndroidManifest.xml")
	if name := readAndroidLabel(manifest); name != "" {
		return name
	}
	// 2. iOS Info.plist CFBundleDisplayName, then CFBundleName.
	for _, plist := range []string{
		filepath.Join(dir, "ios", "Runner", "Info.plist"),
		filepath.Join(dir, "ios", "Info.plist"),
	} {
		if name := readPlistString(plist, "CFBundleDisplayName"); name != "" {
			return name
		}
		if name := readPlistString(plist, "CFBundleName"); name != "" {
			return name
		}
	}
	// 3. pubspec.yaml `name:` (snake_case package id, ugly but valid).
	data, err := os.ReadFile(filepath.Join(dir, "pubspec.yaml"))
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "name:") {
			name := strings.TrimSpace(strings.TrimPrefix(line, "name:"))
			name = strings.Trim(name, `"'`)
			if name != "" {
				return name
			}
		}
	}
	return ""
}

// parseKotlinAppName resolves the Android launcher label. Priority:
//
//  1. `app/src/main/res/values/strings.xml` `<string name="app_name">…`.
//     This is what the launcher renders, so it's the highest-quality
//     display name for a real Android app.
//  2. `settings.gradle(.kts)` `rootProject.name = "…"`. Usually a
//     package id like "todo-kt" — workable but uglier.
//
// Returns "" when neither is present so the caller falls back to the
// directory leaf.
func parseKotlinAppName(dir string) string {
	if name := readStringsXMLAppName(filepath.Join(dir, "app", "src", "main", "res", "values", "strings.xml")); name != "" {
		return name
	}
	for _, settings := range []string{"settings.gradle.kts", "settings.gradle"} {
		data, err := os.ReadFile(filepath.Join(dir, settings))
		if err != nil {
			continue
		}
		// Match both `rootProject.name = "x"` (KTS) and `rootProject.name = 'x'`
		// (Groovy single-quote). One regex over both keeps the parser
		// honest — substring matching would mis-fire on
		// `rootProject.name.toLowerCase()` or comments.
		re := regexp.MustCompile(`(?m)^\s*rootProject\.name\s*=\s*["']([^"']+)["']`)
		if m := re.FindSubmatch(data); m != nil {
			return string(m[1])
		}
	}
	return ""
}

// parseSwiftAppName resolves the iOS launcher label. Priority:
//
//  1. `<App>/Info.plist` `CFBundleDisplayName` (preferred — what
//     SpringBoard renders), falling back to `CFBundleName`.
//  2. `project.yml` (xcodegen) settings.base.INFOPLIST_KEY_CFBundleDisplayName
//     when GENERATE_INFOPLIST_FILE: YES is in use and there's no
//     literal Info.plist on disk.
//  3. `project.yml` top-level `name:` — the xcodegen project name,
//     workable as a final fallback.
func parseSwiftAppName(dir string) string {
	entries, err := os.ReadDir(dir)
	if err == nil {
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			plist := filepath.Join(dir, e.Name(), "Info.plist")
			if name := readPlistString(plist, "CFBundleDisplayName"); name != "" {
				return name
			}
			if name := readPlistString(plist, "CFBundleName"); name != "" {
				return name
			}
		}
	}
	if data, err := os.ReadFile(filepath.Join(dir, "project.yml")); err == nil {
		// xcodegen settings overrides — match exact key, not a
		// substring, so a comment like `# CFBundleDisplayName: …`
		// doesn't poison the result.
		re := regexp.MustCompile(`(?m)^\s*INFOPLIST_KEY_CFBundleDisplayName\s*:\s*["']?([^"'\n]+?)["']?\s*$`)
		if m := re.FindSubmatch(data); m != nil {
			return strings.TrimSpace(string(m[1]))
		}
		nameRe := regexp.MustCompile(`(?m)^name\s*:\s*["']?([^"'\n]+?)["']?\s*$`)
		if m := nameRe.FindSubmatch(data); m != nil {
			return strings.TrimSpace(string(m[1]))
		}
	}
	return ""
}

// isKotlinSubmoduleOfParent returns true when `dir` is a child Gradle
// sub-module included from a parent project root — the canonical case
// being `<root>/app/build.gradle.kts` referenced by `<root>/settings.
// gradle.kts` `include(":app")`. Without this check, the Hot Reload
// list double-counts every Android Gradle project: once as the root,
// once as the `:app` sub-module that holds the actual AndroidManifest.
func isKotlinSubmoduleOfParent(dir string) bool {
	parent := filepath.Dir(dir)
	leaf := filepath.Base(dir)
	if parent == "" || parent == dir || leaf == "" || leaf == "." {
		return false
	}
	include := `":` + leaf + `"`
	includeAlt := `':` + leaf + `'`
	for _, settings := range []string{"settings.gradle.kts", "settings.gradle"} {
		data, err := os.ReadFile(filepath.Join(parent, settings))
		if err != nil {
			continue
		}
		s := string(data)
		if strings.Contains(s, include) || strings.Contains(s, includeAlt) {
			return true
		}
	}
	return false
}

// isXcodegenIOSProject returns true when `dir/project.yml` declares
// an iOS app target. Without this check, xcodegen-driven Swift apps
// (no `Package.swift` on disk) wouldn't appear in the Hot Reload
// list at all — `demo/mobile/todo-swift/` is the canonical example.
// We accept any of the common xcodegen iOS markers because the schema
// is loose: top-level `bundleIdPrefix:`, a `platform: iOS` line under
// any target, or a `deploymentTarget.iOS:` block.
func isXcodegenIOSProject(dir string) bool {
	data, err := os.ReadFile(filepath.Join(dir, "project.yml"))
	if err != nil {
		return false
	}
	s := string(data)
	if strings.Contains(s, "platform: iOS") || strings.Contains(s, "platform: ios") {
		return true
	}
	if strings.Contains(s, "iOS:") && strings.Contains(s, "deploymentTarget") {
		return true
	}
	return false
}

// readStringsXMLAppName reads `<string name="app_name">VALUE</string>`
// from an Android values resources file. Returns "" when the value is
// itself a `@string/...` reference (no point chasing the ref — the
// ref target is in another file and we'd recurse).
func readStringsXMLAppName(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	re := regexp.MustCompile(`<string\s+name="app_name"\s*>\s*([^<]+?)\s*</string>`)
	m := re.FindSubmatch(data)
	if m == nil {
		return ""
	}
	val := strings.TrimSpace(string(m[1]))
	if strings.HasPrefix(val, "@") {
		return ""
	}
	return val
}

// readAndroidLabel reads `android:label="LITERAL"` from the
// `<application>` element of an AndroidManifest.xml. Returns "" when
// the value is a `@string/foo` reference — the caller can resolve the
// ref via res/values/strings.xml on its own if needed.
func readAndroidLabel(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	re := regexp.MustCompile(`<application\b[^>]*\bandroid:label\s*=\s*"([^"]+)"`)
	m := re.FindSubmatch(data)
	if m == nil {
		return ""
	}
	val := strings.TrimSpace(string(m[1]))
	if strings.HasPrefix(val, "@") {
		// @string/app_name — resolve via strings.xml.
		base := filepath.Dir(path)
		if name := readStringsXMLAppName(filepath.Join(base, "res", "values", "strings.xml")); name != "" {
			return name
		}
		return ""
	}
	return val
}

// readPlistString reads a top-level `<key>NAME</key><string>VALUE</string>`
// pair out of an Info.plist (XML format). Returns "" when the key is
// absent, the value is empty, or the value starts with `$(` (a build
// setting placeholder like `$(PRODUCT_NAME)` — those resolve only at
// build time and aren't useful as a display name).
func readPlistString(path, key string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	// `<key>NAME</key>\s*<string>VALUE</string>` — be tolerant of
	// arbitrary whitespace/newlines between the two tags. We do NOT
	// support the binary plist format here (Xcode writes XML by default).
	re := regexp.MustCompile(`<key>` + regexp.QuoteMeta(key) + `</key>\s*<string>([^<]*)</string>`)
	m := re.FindSubmatch(data)
	if m == nil {
		return ""
	}
	val := strings.TrimSpace(string(m[1]))
	if val == "" || strings.HasPrefix(val, "$(") {
		return ""
	}
	return val
}

func parseUnityAppName(dir string) string {
	data, err := os.ReadFile(filepath.Join(dir, "ProjectSettings", "ProjectSettings.asset"))
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "productName:") {
			name := strings.TrimSpace(strings.TrimPrefix(line, "productName:"))
			name = strings.Trim(name, `"'`)
			if name != "" {
				return name
			}
		}
	}
	return ""
}

// ── HTTP handler ──────────────────────────────────────────────────────

// handleProjectsByCapability is a capability-aware filter on top of the
// shared discovery cache. Same payload as /projects/mobile but filtered
// by the URL path:
//
//	/projects/mobile  → MobileCapable=true (Flutter, RN, Expo, Unity)
//	/projects/web     → WebCapable=true (Next, Vite, Expo with RN-Web,
//	                                    bare RN with RN-Web)
//	/projects/all     → no filter — every detected project with its
//	                                capability flags
//
// Used by the dashboard to populate the Web App tab and Mobile App tab
// independently. A single project (e.g. an Expo app with `react-native-web`
// in deps) appears in both /projects/mobile and /projects/web because
// its MobileCapable + WebCapable flags are both true.
func (s *HTTPServer) handleProjectsByCapability(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, http.StatusMethodNotAllowed, "use GET")
		return
	}
	wantWeb := strings.HasSuffix(r.URL.Path, "/web")
	wantAll := strings.HasSuffix(r.URL.Path, "/all")
	mobileProjectCache.mu.RLock()
	projects := mobileProjectCache.projects
	scannedAt := mobileProjectCache.scannedAt
	scanning := mobileProjectCache.scanning
	mobileProjectCache.mu.RUnlock()
	if (projects == nil || len(projects) == 0) && hydrateMobileProjectCacheFromDisk() {
		mobileProjectCache.mu.RLock()
		projects = mobileProjectCache.projects
		scannedAt = mobileProjectCache.scannedAt
		scanning = mobileProjectCache.scanning
		mobileProjectCache.mu.RUnlock()
	}
	// Honest empty-state for first-call: kick a scan and tell the
	// caller it's still running so the UI can render a spinner.
	if (projects == nil || len(projects) == 0) && time.Since(scannedAt) > 10*time.Minute {
		go runMobileScan("by-capability cold-cache")
	}
	out := make([]MobileProject, 0, len(projects))
	for _, p := range projects {
		switch {
		case wantAll:
			out = append(out, p)
		case wantWeb:
			if p.WebCapable {
				out = append(out, p)
			}
		default:
			if p.MobileCapable {
				out = append(out, p)
			}
		}
	}
	jsonReply(w, http.StatusOK, map[string]interface{}{
		"ok":         true,
		"projects":   out,
		"scannedAt":  scannedAt.UTC().Format(time.RFC3339),
		"scanning":   scanning,
		"capability": map[bool]string{true: "web", false: "mobile"}[wantWeb],
	})
}

// mobileCapableProjects filters the shared discovery cache down to
// projects that actually run on a phone. The scanner caches every
// detected project (web + mobile) so /projects/all and /projects/web
// can reuse the same walk, but /projects/mobile must drop pure-web
// frameworks (Next, Vite) before they reach the Hot Reload tab —
// otherwise tapping a Next.js project tries to start a Hermes bundle
// for a folder with no React Native runtime. Returns a non-nil slice
// even when nothing matches so jsonReply emits `[]` instead of `null`.
func mobileCapableProjects(in []MobileProject) []MobileProject {
	out := make([]MobileProject, 0, len(in))
	for _, p := range in {
		if p.MobileCapable {
			out = append(out, p)
		}
	}
	return out
}

// handleMobileProjects returns mobile-capable projects found on the
// machine. GET /projects/mobile — scans home directory for Flutter,
// Expo, React Native, native iOS (Swift) and native Android (Kotlin)
// projects. Results are filtered to MobileCapable=true; pure-web
// frameworks (Next, Vite) live in /projects/web instead. Cached for
// 10 minutes; POST forces a re-scan.
func (s *HTTPServer) handleMobileProjects(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		// Force a fresh background re-scan. If startup discovery is already
		// walking an older filesystem snapshot, cancel it first so newly cloned
		// or generated projects are picked up by the explicit refresh.
		requestForcedMobileScan("forced re-scan")
		jsonReply(w, http.StatusOK, map[string]interface{}{
			"ok":      true,
			"message": "scan started",
		})
		return
	}

	if r.Method == http.MethodDelete {
		mobileProjectCache.mu.Lock()
		wasScanning := mobileProjectCache.scanning
		mobileProjectCache.cancel = true
		mobileProjectCache.scanning = false
		mobileProjectCache.mu.Unlock()
		jsonReply(w, http.StatusOK, map[string]interface{}{
			"ok":      true,
			"message": map[bool]string{true: "scan stop requested", false: "no scan was running"}[wasScanning],
		})
		return
	}

	if r.Method != http.MethodGet {
		jsonError(w, http.StatusMethodNotAllowed, "use GET, POST, or DELETE")
		return
	}

	// Check cache (10 min TTL). Empty caches are treated as stale so a failed
	// startup scan doesn't leave mobile showing "no projects" for minutes.
	mobileProjectCache.mu.RLock()
	projects := mobileProjectCache.projects
	scannedAt := mobileProjectCache.scannedAt
	scanning := mobileProjectCache.scanning
	stats := mobileProjectCache.stats
	mobileProjectCache.mu.RUnlock()
	if (projects == nil || len(projects) == 0) && hydrateMobileProjectCacheFromDisk() {
		mobileProjectCache.mu.RLock()
		projects = mobileProjectCache.projects
		scannedAt = mobileProjectCache.scannedAt
		scanning = mobileProjectCache.scanning
		stats = mobileProjectCache.stats
		mobileProjectCache.mu.RUnlock()
	}

	if projects != nil && len(projects) > 0 && time.Since(scannedAt) < 10*time.Minute {
		jsonReply(w, http.StatusOK, mobileProjectsReply(mobileCapableProjects(projects), scannedAt, scanning, stats))
		return
	}

	// No cache or stale — kick a BACKGROUND scan and return immediately with
	// scanning=true. The HTTP request must never block on the filesystem
	// walk (that was the request-level hang: a slow macOS home-dir scan with
	// no timeout held the response open forever). The mobile UI polls
	// /projects/mobile and renders results as soon as the scan settles.
	if !scanning {
		reason := "initial scan"
		if len(projects) > 0 {
			reason = "background refresh"
		}
		go runMobileScan(reason)
		scanning = true
	}

	jsonReply(w, http.StatusOK, mobileProjectsReply(mobileCapableProjects(projects), scannedAt, scanning, stats))
}

// mobileProjectsReply builds the GET /projects/mobile body, including the
// scan diagnostics the mobile preflight reads to distinguish "empty
// machine" from "permission-blocked" from "timed out".
func mobileProjectsReply(projects []MobileProject, scannedAt time.Time, scanning bool, stats mobileScanStats) map[string]interface{} {
	reply := map[string]interface{}{
		"ok":         true,
		"projects":   projects,
		"scannedAt":  scannedAt.UTC().Format(time.RFC3339),
		"scanning":   scanning,
		"permDenied": stats.PermDenied,
		"timedOut":   stats.TimedOut,
		"scanMs":     stats.ElapsedMs,
	}
	if stats.Err != "" {
		reply["scanError"] = stats.Err
	}
	return reply
}
