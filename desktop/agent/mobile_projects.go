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
	WebCapable    bool `json:"webCapable"`              // can be served as web (has react-native-web, expo with --web, next, vite, etc.)
	MobileCapable bool `json:"mobileCapable"`           // can run on phone (has react-native, expo, flutter)
	// Monorepo lineage. Set when the project lives inside a yaver.workspace.yaml-
	// declared monorepo or under a `apps/*`, `packages/*`, `mobile/` subdir.
	// Lets the dashboard show "carrotbet → apps/web" + "carrotbet → mobile"
	// as separate selectable rows instead of one root-level entry.
	MonorepoRoot string `json:"monorepoRoot,omitempty"`  // absolute path to monorepo root, or "" if standalone
	MonorepoApp  string `json:"monorepoApp,omitempty"`   // app name within the monorepo (e.g. "web", "mobile")
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
}

var errStopMobileScan = errors.New("mobile project scan cancelled")

// scanMobileProjects walks workspace roots looking for mobile projects.
// Detects: pubspec.yaml (Flutter), package.json with expo/react-native,
// and Unity projects via ProjectSettings/ProjectVersion.txt.
// Skips: node_modules, .git, build artifacts, system dirs, caches.
func scanMobileProjects() []MobileProject {
	roots := projectDiscoveryRoots()
	if len(roots) == 0 {
		return nil
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
			if err != nil {
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
			default:
				return nil
			}

			if framework == "" {
				return nil
			}

			seen[dir] = true

			// Skip if this is inside another project's subdirectory or an SDK/library
			parentName := filepath.Base(dir)
			if parentName == "example" || parentName == "test" || parentName == "e2e" {
				return nil
			}
			// Skip if it's a library/SDK (not a real app) — check for .git to confirm it's a standalone project
			// or if the parent has a .git (it's a subdir of a larger project, which is fine — like monorepo/mobile/)
			if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
				// No .git — check if parent or grandparent has one (monorepo case)
				parent := filepath.Dir(dir)
				grandparent := filepath.Dir(parent)
				hasParentGit := false
				if _, err := os.Stat(filepath.Join(parent, ".git")); err == nil {
					hasParentGit = true
				}
				if _, err := os.Stat(filepath.Join(grandparent, ".git")); err == nil {
					hasParentGit = true
				}
				if !hasParentGit {
					return nil // orphan package.json/pubspec.yaml without any git context — skip
				}
			}

			// Parse real app name from framework config files
			appName := parseAppName(dir, framework)
			if appName == "" {
				appName = filepath.Base(dir)
			}

			// Detect SDK version and dev build status
			sdkVersion := detectExpoSDK(dir, framework)
			hasDevBuild := fileExists(filepath.Join(dir, "ios", "Podfile")) ||
				fileExists(filepath.Join(dir, "android", "build.gradle")) ||
				framework == "unity"

			proj := MobileProject{
				Name:          appName,
				Path:          dir,
				Framework:     framework,
				SDKVersion:    sdkVersion,
				HasDevBuild:   hasDevBuild,
				WebCapable:    webCapable,
				MobileCapable: mobileCapable,
			}
			// Monorepo lineage detection. If `dir` is N levels under a
			// directory that has its own .git AND is also one of the
			// well-known monorepo subdirs (`apps/<app>`, `packages/<pkg>`,
			// `mobile`, `web`), record the root + the in-monorepo app
			// name so the dashboard can show "carrotbet → mobile" and
			// "carrotbet → apps/web" as separate rows under the same
			// repo. Standalone repos leave both fields empty.
			if root, app := detectMonorepoLineage(dir); root != "" {
				proj.MonorepoRoot = root
				proj.MonorepoApp = app
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
			return projects
		}
	}

	return projects
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
func expoConfigHasBundleID(projectPath, bundleID string) bool {
	for _, f := range []string{"app.json", "app.config.json"} {
		data, err := os.ReadFile(filepath.Join(projectPath, f))
		if err != nil {
			continue
		}
		s := string(data)
		if strings.Contains(s, `"bundleIdentifier": "`+bundleID+`"`) ||
			strings.Contains(s, `"package": "`+bundleID+`"`) {
			return true
		}
	}
	return false
}

// Scans all mobile projects, checks dev builds, and pre-builds missing ones in background.
func PrewarmMobileProjects() {
	log.Println("[mobile-scan] Scanning for mobile projects...")
	projects := scanMobileProjects()

	mobileProjectCache.mu.Lock()
	mobileProjectCache.projects = projects
	mobileProjectCache.scannedAt = time.Now()
	mobileProjectCache.mu.Unlock()

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
// - Expo/RN: app.json → expo.name or name; app.config.js not parsed (JS)
// - Flutter: pubspec.yaml → name: field
// - Unity: ProjectSettings/ProjectSettings.asset productName
func parseAppName(dir, framework string) string {
	switch framework {
	case "expo", "react-native":
		return parseExpoAppName(dir)
	case "flutter":
		return parseFlutterAppName(dir)
	case "unity":
		return parseUnityAppName(dir)
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
	data, err := os.ReadFile(filepath.Join(dir, "pubspec.yaml"))
	if err != nil {
		return ""
	}
	// Simple YAML parsing — just find "name: X" at the top level (no indentation)
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
	// Honest empty-state for first-call: kick a scan and tell the
	// caller it's still running so the UI can render a spinner.
	if (projects == nil || len(projects) == 0) && time.Since(scannedAt) > 10*time.Minute {
		go func() {
			mobileProjectCache.mu.Lock()
			mobileProjectCache.cancel = false
			mobileProjectCache.scanning = true
			mobileProjectCache.mu.Unlock()
			scanned := scanMobileProjects()
			mobileProjectCache.mu.Lock()
			mobileProjectCache.projects = scanned
			mobileProjectCache.scannedAt = time.Now()
			mobileProjectCache.scanning = false
			mobileProjectCache.mu.Unlock()
		}()
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

// handleMobileProjects returns all mobile projects found on the machine.
// GET /projects/mobile — scans home directory for Flutter, Expo, React Native projects.
// Results are cached for 10 minutes; POST forces a re-scan.
func (s *HTTPServer) handleMobileProjects(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		// Force re-scan
		go func() {
			mobileProjectCache.mu.Lock()
			mobileProjectCache.cancel = false
			mobileProjectCache.scanning = true
			mobileProjectCache.mu.Unlock()

			projects := scanMobileProjects()

			mobileProjectCache.mu.Lock()
			mobileProjectCache.projects = projects
			mobileProjectCache.scannedAt = time.Now()
			mobileProjectCache.scanning = false
			mobileProjectCache.mu.Unlock()

			log.Printf("[mobile-scan] Found %d mobile projects", len(projects))
		}()

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
	mobileProjectCache.mu.RUnlock()

	if projects != nil && len(projects) > 0 && time.Since(scannedAt) < 10*time.Minute {
		jsonReply(w, http.StatusOK, map[string]interface{}{
			"ok":        true,
			"projects":  projects,
			"scannedAt": scannedAt.UTC().Format(time.RFC3339),
			"scanning":  scanning,
		})
		return
	}

	// No cache or stale — scan synchronously (first time), then cache
	if projects == nil || len(projects) == 0 {
		mobileProjectCache.mu.Lock()
		mobileProjectCache.cancel = false
		mobileProjectCache.scanning = true
		mobileProjectCache.mu.Unlock()

		scanned := scanMobileProjects()

		mobileProjectCache.mu.Lock()
		mobileProjectCache.projects = scanned
		mobileProjectCache.scannedAt = time.Now()
		mobileProjectCache.scanning = false
		mobileProjectCache.mu.Unlock()

		projects = scanned
		scannedAt = time.Now()
		log.Printf("[mobile-scan] Initial scan: found %d mobile projects", len(projects))
	} else {
		// Stale cache — return stale data but trigger background refresh
		go func() {
			mobileProjectCache.mu.Lock()
			mobileProjectCache.cancel = false
			mobileProjectCache.scanning = true
			mobileProjectCache.mu.Unlock()

			scanned := scanMobileProjects()

			mobileProjectCache.mu.Lock()
			mobileProjectCache.projects = scanned
			mobileProjectCache.scannedAt = time.Now()
			mobileProjectCache.scanning = false
			mobileProjectCache.mu.Unlock()

			log.Printf("[mobile-scan] Background refresh: found %d mobile projects", len(scanned))
		}()
	}

	if projects == nil {
		projects = []MobileProject{}
	}

	jsonReply(w, http.StatusOK, map[string]interface{}{
		"ok":        true,
		"projects":  projects,
		"scannedAt": scannedAt.UTC().Format(time.RFC3339),
		"scanning":  scanning,
	})
}
