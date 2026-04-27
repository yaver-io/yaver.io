package main

// Monorepo / multi-project detection — walks a single root directory and
// classifies every sub-project by framework. Extends what scanMobileProjects
// already does (flutter / expo / react-native / unity / next / vite) to also
// catch native iOS Swift (.xcodeproj / project.yml / Project.swift) and native
// Android Kotlin (root settings.gradle{,.kts}) so the friendly native build
// pipeline (yaver iosNative / androidNative / flutter) has something concrete
// to point at when users run `yaver code`, the mobile app, or the web UI
// against a real monorepo.
//
// The detector is pure — no side effects, no caching — so it's safe to call
// repeatedly from HTTP / MCP / CLI without worrying about staleness.

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// DetectedProject is one project found inside a monorepo (or a standalone
// project, in which case the Monorepo wraps a single entry).
type DetectedProject struct {
	Name      string   `json:"name"`               // app name (from package.json / pubspec / xcodeproj base name) or directory name
	Path      string   `json:"path"`               // absolute path
	RelPath   string   `json:"relPath"`            // path relative to monorepo root, "." for the root itself
	Framework string   `json:"framework"`          // flutter | expo | react-native | next | vite | unity | iosNative | androidNative | swift-package | gradle-jvm
	Tags      []string `json:"tags,omitempty"`     // mobile, web, backend, ios, android, kotlin, swift, dart, kotlin-jvm, ...
	HasTests  bool     `json:"hasTests"`           // common test dirs present
	HasGit    bool     `json:"hasGit"`             // has its own .git (rare for monorepo apps but possible for git submodules)
	Manifest  string   `json:"manifest,omitempty"` // path to the marker file that classified it (debugging)
}

// Monorepo wraps a root directory's classification result.
type Monorepo struct {
	Root        string            `json:"root"` // absolute path passed in
	GitBranch   string            `json:"gitBranch,omitempty"`
	GitRemote   string            `json:"gitRemote,omitempty"`
	Projects    []DetectedProject `json:"projects"`    // sorted by RelPath
	IsMonorepo  bool              `json:"isMonorepo"`  // true when ≥ 2 projects (or a workspace manifest is present)
	HasManifest bool              `json:"hasManifest"` // true when yaver.workspace.yaml exists at root
	Frameworks  []string          `json:"frameworks"`  // distinct frameworks found, sorted (UX shorthand)
}

// DetectOpts controls the walk. Zero-value is sane for most callers.
type DetectOpts struct {
	MaxDepth int      // 0 → 6 (deep enough for apps/<name>/<package>/ trees, shallow enough to skip vendor caches)
	Skip     []string // additional dir names to skip (always merged with the built-in skip list)
}

// Built-in skip list: matches scanMobileProjects' set so we don't walk
// node_modules / build / .git / etc. Top-level directory names only.
var monorepoSkipDirs = map[string]bool{
	"node_modules": true, ".git": true, ".idea": true, ".vscode": true,
	"build": true, "dist": true, "out": true, ".dart_tool": true,
	".gradle": true, ".kotlin": true, ".cxx": true,
	".cache": true, ".local": true, ".cargo": true, ".rustup": true,
	".pub-cache": true, ".expo": true, ".next": true, ".turbo": true,
	"DerivedData": true, "Pods": true, ".cocoapods": true,
	"vendor": true, "target": true, "Library": true,
	".yaver-build": true, ".yaver": true,
}

// Per-RN-app inner platform dirs we should NOT recurse into (an RN project's
// android/ and ios/ subtrees are part of the parent, not standalone projects).
// We make the call only AFTER classifying the parent — see walkAndClassify.
var rnInnerDirs = map[string]bool{
	"android": true, "ios": true,
}

// DetectMonorepo classifies every project inside rootDir. Returns a Monorepo
// describing the root and an ordered list of DetectedProject children.
func DetectMonorepo(rootDir string, opts DetectOpts) (*Monorepo, error) {
	if rootDir == "" {
		return nil, fmt.Errorf("rootDir is required")
	}
	abs, err := filepath.Abs(rootDir)
	if err != nil {
		return nil, fmt.Errorf("abs path: %w", err)
	}
	st, err := os.Stat(abs)
	if err != nil {
		return nil, fmt.Errorf("stat %s: %w", abs, err)
	}
	if !st.IsDir() {
		return nil, fmt.Errorf("%s is not a directory", abs)
	}

	maxDepth := opts.MaxDepth
	if maxDepth <= 0 {
		maxDepth = 6
	}
	skip := make(map[string]bool, len(monorepoSkipDirs)+len(opts.Skip))
	for k, v := range monorepoSkipDirs {
		skip[k] = v
	}
	for _, s := range opts.Skip {
		skip[s] = true
	}

	mr := &Monorepo{Root: abs}

	// Workspace manifest at root → strong signal this is a managed monorepo.
	for _, name := range []string{"yaver.workspace.yaml", "yaver.workspace.yml"} {
		if _, err := os.Stat(filepath.Join(abs, name)); err == nil {
			mr.HasManifest = true
			break
		}
	}

	// Git metadata (sanitized — strip embedded credentials).
	if out, err := exec.Command("git", "-C", abs, "rev-parse", "--abbrev-ref", "HEAD").Output(); err == nil {
		mr.GitBranch = strings.TrimSpace(string(out))
	}
	if out, err := exec.Command("git", "-C", abs, "remote", "get-url", "origin").Output(); err == nil {
		remote := strings.TrimSpace(string(out))
		if idx := strings.Index(remote, "@"); idx > 0 && (strings.HasPrefix(remote, "https://") || strings.HasPrefix(remote, "http://")) {
			proto := remote[:strings.Index(remote, "://")+3]
			remote = proto + remote[idx+1:]
		}
		mr.GitRemote = remote
	}

	// Walk and classify.
	mr.Projects = walkAndClassify(abs, abs, 0, maxDepth, skip)

	// Distinct framework summary.
	fwSet := map[string]bool{}
	for _, p := range mr.Projects {
		if p.Framework != "" {
			fwSet[p.Framework] = true
		}
	}
	for fw := range fwSet {
		mr.Frameworks = append(mr.Frameworks, fw)
	}
	sort.Strings(mr.Frameworks)

	// Monorepo if manifest exists OR we found ≥ 2 distinct projects.
	mr.IsMonorepo = mr.HasManifest || len(mr.Projects) >= 2

	return mr, nil
}

// walkAndClassify recursively descends from dir, classifying each level. Each
// classified project's RN inner platform subdirs (android/, ios/) are NOT
// recursed into so we don't double-count them.
func walkAndClassify(root, dir string, depth, maxDepth int, skip map[string]bool) []DetectedProject {
	if depth > maxDepth {
		return nil
	}

	var results []DetectedProject

	// Classify this directory.
	if proj := classifyDir(root, dir); proj != nil {
		results = append(results, *proj)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return results
	}

	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasPrefix(name, ".") && name != "." {
			continue
		}
		if skip[name] {
			continue
		}
		// If the parent we just classified is RN/Expo/Flutter, don't walk
		// into its inner platform dirs.
		if len(results) > 0 {
			parent := results[len(results)-1]
			if parent.Path == dir {
				switch parent.Framework {
				case "react-native", "expo", "flutter":
					if rnInnerDirs[name] {
						continue
					}
				}
			}
		}
		child := filepath.Join(dir, name)
		results = append(results, walkAndClassify(root, child, depth+1, maxDepth, skip)...)
	}

	return results
}

// classifyDir looks at marker files in `dir` and returns a DetectedProject if
// it recognizes a framework. Returns nil otherwise.
func classifyDir(root, dir string) *DetectedProject {
	relPath, _ := filepath.Rel(root, dir)
	if relPath == "" {
		relPath = "."
	}

	exists := func(parts ...string) bool {
		_, err := os.Stat(filepath.Join(dir, filepath.Join(parts...)))
		return err == nil
	}
	readSmall := func(name string) string {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil || len(data) > 1<<20 { // skip if missing or > 1 MB
			return ""
		}
		return string(data)
	}

	// 1. Flutter
	if exists("pubspec.yaml") {
		name := parseAppName(dir, "flutter")
		if name == "" {
			name = filepath.Base(dir)
		}
		return &DetectedProject{
			Name: name, Path: dir, RelPath: relPath,
			Framework: "flutter", Manifest: "pubspec.yaml",
			Tags:     []string{"mobile", "dart"},
			HasTests: exists("test") || exists("integration_test"),
			HasGit:   exists(".git"),
		}
	}

	// 2. Expo / React Native / Next.js / Vite (package.json content sniff)
	if pkg := readSmall("package.json"); pkg != "" {
		name := parseAppName(dir, "expo")
		if name == "" {
			name = filepath.Base(dir)
		}
		switch {
		case strings.Contains(pkg, `"expo"`):
			return &DetectedProject{
				Name: name, Path: dir, RelPath: relPath,
				Framework: "expo", Manifest: "package.json",
				Tags:     append([]string{"mobile", "typescript"}, ifContains(pkg, `"react-native-web"`, "web")...),
				HasTests: exists("__tests__") || exists("test") || exists("e2e"),
				HasGit:   exists(".git"),
			}
		case strings.Contains(pkg, `"react-native":`) || strings.Contains(pkg, `"react-native" :`):
			return &DetectedProject{
				Name: name, Path: dir, RelPath: relPath,
				Framework: "react-native", Manifest: "package.json",
				Tags:     append([]string{"mobile", "typescript"}, ifContains(pkg, `"react-native-web"`, "web")...),
				HasTests: exists("__tests__") || exists("test") || exists("e2e"),
				HasGit:   exists(".git"),
			}
		case strings.Contains(pkg, `"next":`) || strings.Contains(pkg, `"next" :`):
			return &DetectedProject{
				Name: name, Path: dir, RelPath: relPath,
				Framework: "next", Manifest: "package.json",
				Tags:     []string{"web", "typescript"},
				HasTests: exists("__tests__") || exists("test") || exists("e2e"),
				HasGit:   exists(".git"),
			}
		case strings.Contains(pkg, `"vite":`) || strings.Contains(pkg, `"vite" :`):
			return &DetectedProject{
				Name: name, Path: dir, RelPath: relPath,
				Framework: "vite", Manifest: "package.json",
				Tags:     []string{"web", "typescript"},
				HasTests: exists("__tests__") || exists("test") || exists("e2e"),
				HasGit:   exists(".git"),
			}
		}
	}

	// 3. Unity (ProjectSettings/ProjectVersion.txt + Packages/manifest.json)
	if exists("ProjectSettings", "ProjectVersion.txt") && exists("ProjectSettings", "ProjectSettings.asset") && exists("Packages", "manifest.json") {
		return &DetectedProject{
			Name: filepath.Base(dir), Path: dir, RelPath: relPath,
			Framework: "unity", Manifest: "ProjectSettings/ProjectVersion.txt",
			Tags:   []string{"mobile", "csharp"},
			HasGit: exists(".git"),
		}
	}

	// 4. Native iOS Swift — .xcodeproj at top level OR xcodegen project.yml
	//    OR Tuist Project.swift, OR Swift Package Manager Package.swift.
	if hasXcodeProj(dir) || exists("project.yml") || exists("Project.swift") {
		return &DetectedProject{
			Name: filepath.Base(dir), Path: dir, RelPath: relPath,
			Framework: "iosNative", Manifest: firstExisting(dir, "project.yml", "Project.swift", "*.xcodeproj"),
			Tags:     []string{"mobile", "ios", "swift"},
			HasTests: exists("Tests") || strings.Contains(strings.ToLower(filepath.Base(dir)), "tests"),
			HasGit:   exists(".git"),
		}
	}
	if exists("Package.swift") {
		return &DetectedProject{
			Name: filepath.Base(dir), Path: dir, RelPath: relPath,
			Framework: "swift-package", Manifest: "Package.swift",
			Tags:     []string{"swift", "spm"},
			HasTests: exists("Tests"),
			HasGit:   exists(".git"),
		}
	}

	// 5. Native Android Kotlin — root Gradle project (settings.gradle{,.kts})
	//    with a build.gradle{,.kts}. Distinguish from RN/Flutter inner android/
	//    by requiring it to NOT be named exactly "android" inside an RN parent
	//    (the walker already prevents that case via rnInnerDirs).
	hasSettings := exists("settings.gradle") || exists("settings.gradle.kts")
	hasBuild := exists("build.gradle") || exists("build.gradle.kts")
	if hasSettings && hasBuild {
		// If there's an Android Manifest at app/src/main/, it's Android-targeted.
		if exists("app", "src", "main", "AndroidManifest.xml") || exists("src", "main", "AndroidManifest.xml") {
			return &DetectedProject{
				Name: filepath.Base(dir), Path: dir, RelPath: relPath,
				Framework: "androidNative", Manifest: firstExisting(dir, "settings.gradle.kts", "settings.gradle"),
				Tags:     []string{"mobile", "android", "kotlin"},
				HasTests: exists("app", "src", "test") || exists("app", "src", "androidTest"),
				HasGit:   exists(".git"),
			}
		}
		// Plain JVM Gradle project (Spring, KMP server, etc.) — still a real project.
		return &DetectedProject{
			Name: filepath.Base(dir), Path: dir, RelPath: relPath,
			Framework: "gradle-jvm", Manifest: firstExisting(dir, "settings.gradle.kts", "settings.gradle"),
			Tags:     []string{"jvm", "kotlin"},
			HasTests: exists("src", "test") || exists("src", "test", "kotlin"),
			HasGit:   exists(".git"),
		}
	}

	return nil
}

// hasXcodeProj reports whether dir contains a *.xcodeproj entry.
func hasXcodeProj(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if e.IsDir() && strings.HasSuffix(e.Name(), ".xcodeproj") {
			return true
		}
	}
	return false
}

// firstExisting returns the first marker file/glob that exists in dir, for the
// Manifest debugging field.
func firstExisting(dir string, candidates ...string) string {
	for _, c := range candidates {
		if strings.HasSuffix(c, ".xcodeproj") {
			if hasXcodeProj(dir) {
				entries, _ := os.ReadDir(dir)
				for _, e := range entries {
					if e.IsDir() && strings.HasSuffix(e.Name(), ".xcodeproj") {
						return e.Name()
					}
				}
			}
			continue
		}
		if _, err := os.Stat(filepath.Join(dir, c)); err == nil {
			return c
		}
	}
	return ""
}

func ifContains(s, needle, tag string) []string {
	if strings.Contains(s, needle) {
		return []string{tag}
	}
	return nil
}
