package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// MessageIntent represents the classified intent of a user message.
type MessageIntent string

const (
	// IntentTodo means the message describes a bug or issue to record for later.
	IntentTodo MessageIntent = "todo"
	// IntentAction means the message is an immediate command/request to execute.
	IntentAction MessageIntent = "action"
	// IntentContinuation means the message continues/clarifies a previous todo item.
	IntentContinuation MessageIntent = "continuation"
)

// ClassifyResult holds the classification of a user message.
type ClassifyResult struct {
	Intent      MessageIntent `json:"intent"`
	TodoID      string        `json:"todoId,omitempty"` // for continuation: which item it continues
	Description string        `json:"description"`      // cleaned description
	IsImmediate bool          `json:"isImmediate"`      // true if should be executed now
	Confidence  float64       `json:"confidence"`       // 0.0-1.0
}

// ClassifyMessage determines if a user's chat message is a todo item (bug/issue to record),
// a continuation of an existing item, or an immediate action to execute.
// Uses keyword heuristics — no LLM call needed, keeps it fast and P2P.
func ClassifyMessage(message string, existingItems []*TodoItem) ClassifyResult {
	msg := strings.ToLower(strings.TrimSpace(message))

	// Check for explicit queue/todo signals
	if hasTodoSignal(msg) {
		return ClassifyResult{
			Intent:      IntentTodo,
			Description: message,
			IsImmediate: false,
			Confidence:  0.9,
		}
	}

	// Check for explicit action signals
	if hasActionSignal(msg) {
		return ClassifyResult{
			Intent:      IntentAction,
			Description: message,
			IsImmediate: true,
			Confidence:  0.9,
		}
	}

	// Check for continuation signals (references to existing items)
	if todoID := findContinuation(msg, existingItems); todoID != "" {
		return ClassifyResult{
			Intent:      IntentContinuation,
			TodoID:      todoID,
			Description: message,
			IsImmediate: false,
			Confidence:  0.8,
		}
	}

	// Bug/issue patterns → todo
	if hasBugPattern(msg) {
		return ClassifyResult{
			Intent:      IntentTodo,
			Description: message,
			IsImmediate: false,
			Confidence:  0.7,
		}
	}

	// Default: treat as immediate action (solo founder wants things done)
	return ClassifyResult{
		Intent:      IntentAction,
		Description: message,
		IsImmediate: true,
		Confidence:  0.5,
	}
}

// hasTodoSignal checks for explicit "add to list" signals.
func hasTodoSignal(msg string) bool {
	signals := []string{
		"add to queue", "queue this", "add to todo", "add to list",
		"record this", "note this", "save this", "remember this",
		"not now", "later", "do this later", "fix later",
		"add it", "put it in", "log this",
	}
	for _, s := range signals {
		if strings.Contains(msg, s) {
			return true
		}
	}
	return false
}

// hasActionSignal checks for explicit "do it now" signals.
func hasActionSignal(msg string) bool {
	signals := []string{
		"fix this now", "do it now", "implement", "implement all",
		"run", "execute", "deploy", "build", "hot reload", "reload",
		"start", "stop", "restart", "push", "commit",
		"show me", "explain", "what is", "how do",
	}
	for _, s := range signals {
		if strings.Contains(msg, s) {
			return true
		}
	}
	return false
}

// hasBugPattern checks for common bug/issue description patterns.
func hasBugPattern(msg string) bool {
	patterns := []string{
		"is broken", "doesn't work", "not working", "is not working",
		"bug", "crash", "error", "issue", "problem",
		"wrong", "incorrect", "missing", "can't", "cannot",
		"fails", "failing", "broken", "stuck",
		"should be", "supposed to", "expected",
		"this page", "this screen", "this button", "the button",
		"overlaps", "misaligned", "cut off", "truncated",
	}
	for _, p := range patterns {
		if strings.Contains(msg, p) {
			return true
		}
	}
	return false
}

// findContinuation checks if the message references an existing todo item.
func findContinuation(msg string, items []*TodoItem) string {
	// Check for "also" or "and" at the start (continuation of last reported bug)
	if (strings.HasPrefix(msg, "also ") || strings.HasPrefix(msg, "and ") ||
		strings.HasPrefix(msg, "another thing") || strings.HasPrefix(msg, "same thing")) &&
		len(items) > 0 {
		// Find the most recent pending item
		var latest *TodoItem
		for _, item := range items {
			if item.Status == TodoStatusPending || item.Status == TodoStatusImplementing {
				if latest == nil || item.CreatedAt > latest.CreatedAt {
					latest = item
				}
			}
		}
		if latest != nil {
			return latest.ID
		}
	}
	return ""
}

// ProjectInfo contains metadata about the current project/workspace.
type ProjectInfo struct {
	Name      string    `json:"name"`                // directory name
	Path      string    `json:"path"`                // full path
	GitBranch string    `json:"gitBranch,omitempty"` // current git branch
	GitRemote string    `json:"gitRemote,omitempty"` // origin URL (sanitized)
	Framework string    `json:"framework,omitempty"` // detected framework
	Stack     RepoStack `json:"stack,omitempty"`     // full stack detection
}

// DetectProjectInfo extracts project metadata from a working directory.
func DetectProjectInfo(workDir string) ProjectInfo {
	info := ProjectInfo{
		Name: filepath.Base(workDir),
		Path: workDir,
	}

	// Git branch
	if out, err := exec.Command("git", "-C", workDir, "rev-parse", "--abbrev-ref", "HEAD").Output(); err == nil {
		info.GitBranch = strings.TrimSpace(string(out))
	}

	// Git remote (sanitize — no tokens/credentials)
	if out, err := exec.Command("git", "-C", workDir, "remote", "get-url", "origin").Output(); err == nil {
		remote := strings.TrimSpace(string(out))
		// Strip credentials from URL
		if idx := strings.Index(remote, "@"); idx > 0 {
			if strings.HasPrefix(remote, "https://") || strings.HasPrefix(remote, "http://") {
				// https://token@github.com/... → https://github.com/...
				proto := remote[:strings.Index(remote, "://")+3]
				remote = proto + remote[idx+1:]
			}
		}
		info.GitRemote = remote
	}

	// Framework detection
	info.Framework = detectFramework(workDir)

	// Full stack detection (lightweight — only checks marker files)
	info.Stack = detectStack(workDir)

	return info
}

// DetectProjectTags returns tags/chips for a project (e.g. "mobile", "supabase", "firebase").
func DetectProjectTags(dir string) []string {
	var tags []string

	// Mobile
	if fileExists(filepath.Join(dir, "ios")) || fileExists(filepath.Join(dir, "android")) {
		tags = append(tags, "mobile")
	}
	if fileExists(filepath.Join(dir, "pubspec.yaml")) {
		tags = append(tags, "mobile")
	}

	// Backend/services
	if fileExists(filepath.Join(dir, "supabase")) || fileExists(filepath.Join(dir, "supabase.json")) {
		tags = append(tags, "supabase")
	}
	if fileExists(filepath.Join(dir, "firebase.json")) || fileExists(filepath.Join(dir, ".firebaserc")) {
		tags = append(tags, "firebase")
	}
	if fileExists(filepath.Join(dir, "docker-compose.yml")) || fileExists(filepath.Join(dir, "docker-compose.yaml")) || fileExists(filepath.Join(dir, "Dockerfile")) {
		tags = append(tags, "docker")
	}
	if fileExists(filepath.Join(dir, "convex")) {
		tags = append(tags, "convex")
	}
	if fileExists(filepath.Join(dir, "prisma")) {
		tags = append(tags, "prisma")
	}
	if fileExists(filepath.Join(dir, "drizzle.config.ts")) || fileExists(filepath.Join(dir, "drizzle.config.js")) {
		tags = append(tags, "drizzle")
	}

	// Language indicators
	if fileExists(filepath.Join(dir, "go.mod")) {
		tags = append(tags, "go")
	}
	if fileExists(filepath.Join(dir, "Cargo.toml")) {
		tags = append(tags, "rust")
	}
	if fileExists(filepath.Join(dir, "requirements.txt")) || fileExists(filepath.Join(dir, "pyproject.toml")) {
		tags = append(tags, "python")
	}
	if fileExists(filepath.Join(dir, "tsconfig.json")) {
		tags = append(tags, "typescript")
	}

	// Web
	if fileExists(filepath.Join(dir, "tailwind.config.js")) || fileExists(filepath.Join(dir, "tailwind.config.ts")) {
		tags = append(tags, "tailwind")
	}
	if hasFeedbackSDK(dir) {
		tags = append(tags, "feedback-sdk")
	}

	return tags
}

func hasFeedbackSDK(dir string) bool {
	candidates := []string{
		filepath.Join(dir, "package.json"),
		filepath.Join(dir, "app.json"),
		filepath.Join(dir, "app.config.js"),
		filepath.Join(dir, "app.config.ts"),
		filepath.Join(dir, "yaver.json"),
	}
	for _, candidate := range candidates {
		data, err := readSmallFile(candidate)
		if err != nil {
			continue
		}
		content := string(data)
		if strings.Contains(content, "yaver-feedback-react-native") ||
			strings.Contains(content, "yaver-feedback") ||
			strings.Contains(content, `"sdkVersion"`) {
			return true
		}
	}
	return false
}

// detectFramework checks common framework indicators.
func detectFramework(dir string) string {
	// Flutter first, but read pubspec.yaml content — a bare pubspec.yaml can be
	// a plain Dart package (CLI/server), not a Flutter app. A Flutter project
	// declares the flutter SDK (`sdk: flutter` / a `flutter:` section), and has
	// a lib/ with dart sources. Detecting Flutter from its dart project markers
	// rather than mere file presence stops a Dart backend being labelled
	// "flutter" and routed to the mobile preview lanes.
	if data, err := readSmallFile(filepath.Join(dir, "pubspec.yaml")); err == nil {
		body := string(data)
		if strings.Contains(body, "sdk: flutter") ||
			strings.Contains(body, "flutter:") ||
			strings.Contains(body, "cupertino_icons") ||
			fileExists(filepath.Join(dir, "lib", "main.dart")) {
			return "flutter"
		}
		// pubspec present but no Flutter markers → a plain Dart package. Fall
		// through to the other checks rather than mislabelling it flutter.
	}

	checks := []struct {
		file      string
		framework string
	}{
		{"next.config.ts", "nextjs"},
		{"next.config.js", "nextjs"},
		{"vite.config.ts", "vite"},
		{"vite.config.js", "vite"},
		{"package.json", ""}, // check for expo below
	}

	for _, c := range checks {
		if fileExists(filepath.Join(dir, c.file)) {
			if c.framework != "" {
				return c.framework
			}
			// Check package.json for expo
			if data, err := readSmallFile(filepath.Join(dir, c.file)); err == nil {
				if strings.Contains(string(data), `"expo"`) {
					return "expo"
				}
				if strings.Contains(string(data), `"react-native"`) {
					return "react-native"
				}
				if strings.Contains(string(data), `"react"`) {
					return "react"
				}
			}
		}
	}

	// Native mobile — only detect after JS/Flutter checks above fall through,
	// so a React Native repo (which has ios/*.xcodeproj) is never labelled as
	// swift, and a Kotlin Spring Boot / JVM backend is never labelled as a
	// Kotlin *mobile* project.
	if fileExists(filepath.Join(dir, "Package.swift")) || hasExtInDir(dir, ".xcodeproj") {
		return "swift"
	}
	if isKotlinAndroidProject(dir) {
		return "kotlin"
	}
	return ""
}

func hasExtInDir(dir, ext string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ext) {
			return true
		}
	}
	return false
}

// isKotlinAndroidProject returns true only when the directory looks like an
// Android app — a Gradle project that targets Android. A pure Kotlin/JVM
// backend (Spring Boot, Ktor) will also have build.gradle.kts but must NOT be
// classified as a mobile project, since we'd then offer Play Store deploy
// buttons that make no sense.
func isKotlinAndroidProject(dir string) bool {
	hasGradle := fileExists(filepath.Join(dir, "build.gradle.kts")) ||
		fileExists(filepath.Join(dir, "settings.gradle.kts")) ||
		fileExists(filepath.Join(dir, "build.gradle")) ||
		fileExists(filepath.Join(dir, "settings.gradle"))
	if !hasGradle {
		return false
	}
	// Strongest signal: AndroidManifest.xml anywhere in typical locations.
	manifestCandidates := []string{
		filepath.Join(dir, "AndroidManifest.xml"),
		filepath.Join(dir, "app", "src", "main", "AndroidManifest.xml"),
		filepath.Join(dir, "src", "main", "AndroidManifest.xml"),
	}
	for _, p := range manifestCandidates {
		if fileExists(p) {
			return true
		}
	}
	// Fall back: the `android/` directory is the conventional Android module root.
	if info, err := os.Stat(filepath.Join(dir, "android")); err == nil && info.IsDir() {
		// But exclude React Native / Flutter cases — those are caught above by
		// detectFramework, so reaching here means plain Android.
		return true
	}
	// Last resort: look for an Android plugin line in build.gradle.kts so a
	// plain Android-app-as-root repo without `android/` still classifies.
	for _, name := range []string{"build.gradle.kts", "build.gradle", "app/build.gradle.kts", "app/build.gradle"} {
		if data, err := readSmallFile(filepath.Join(dir, name)); err == nil {
			s := string(data)
			if strings.Contains(s, "com.android.application") || strings.Contains(s, "com.android.library") {
				return true
			}
		}
	}
	return false
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func readSmallFile(path string) ([]byte, error) {
	return os.ReadFile(path)
}
