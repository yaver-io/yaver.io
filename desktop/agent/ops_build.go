package main

// ops_build.go — verb "build": language-agnostic build dispatcher.
//
// Detects the project type from the working directory (go.mod,
// package.json, Cargo.toml, pubspec.yaml, pom.xml, build.gradle, etc.)
// and runs the appropriate build command via the exec manager. Returns
// the streamId of the resulting subprocess so the agent can follow
// live stdout/stderr — essential for builds that take minutes (mobile,
// flutter, gradle) without blocking the MCP caller.
//
// Agents that want a specific tool (e.g. cargo nightly + feature
// flags) still call the domain-specific MCP tools directly; this verb
// is the 80% case for "figure out what this project is and build it".

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type opsBuildPayload struct {
	// WorkDir: project root. Defaults to the agent's CWD.
	WorkDir string `json:"workDir,omitempty"`
	// Target: optional build target (e.g. "ios", "android", "release").
	// Tool-specific; ignored when the detected toolchain doesn't use it.
	Target string `json:"target,omitempty"`
	// Env: extra KEY=VAL pairs appended to the subprocess environment.
	Env map[string]string `json:"env,omitempty"`
	// TimeoutSec: kill the build after this many seconds. 0 = no limit.
	TimeoutSec int `json:"timeoutSec,omitempty"`
}

func init() {
	registerOpsVerb(opsVerbSpec{
		Name:        "build",
		Description: "Build the project in workDir. Detects go / node / rust / flutter / android / iOS / make and runs the canonical build command via exec manager. Returns streamId for live stdout/stderr.",
		Schema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"workDir":    map[string]interface{}{"type": "string"},
				"target":     map[string]interface{}{"type": "string"},
				"env":        map[string]interface{}{"type": "object"},
				"timeoutSec": map[string]interface{}{"type": "integer"},
			},
			"additionalProperties": false,
		},
		Handler:    opsBuildHandler,
		Streaming:  true,
		AllowGuest: false,
	})
}

func opsBuildHandler(c OpsContext, payload json.RawMessage) OpsResult {
	var p opsBuildPayload
	if len(payload) > 0 {
		if err := json.Unmarshal(payload, &p); err != nil {
			return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
		}
	}
	workDir := p.WorkDir
	if workDir == "" {
		workDir = "."
	}

	// ExecManager takes the env map verbatim.
	envMap := p.Env

	cmd, tool := detectBuildCommand(workDir, p.Target)
	if cmd == "" {
		return OpsResult{
			OK:   false,
			Code: "unsupported",
			Error: fmt.Sprintf("no recognised build manifest in %q — expected one of: go.mod, package.json, Cargo.toml, pubspec.yaml, build.gradle, *.xcodeproj, Makefile", workDir),
		}
	}
	if c.Server == nil || c.Server.execMgr == nil {
		return OpsResult{OK: false, Code: "unavailable", Error: "exec manager not initialised"}
	}
	sess, err := c.Server.execMgr.StartExec(cmd, workDir, "", envMap, p.TimeoutSec)
	if err != nil {
		return OpsResult{OK: false, Code: "exec_failed", Error: err.Error()}
	}
	return OpsResult{
		OK:       true,
		StreamID: sess.ID,
		Initial: map[string]interface{}{
			"sessionId": sess.ID,
			"tool":      tool,
			"command":   cmd,
			"workDir":   workDir,
			"sseHint":   fmt.Sprintf("/exec/%s/stream for live output, /exec/%s for snapshot", sess.ID, sess.ID),
		},
	}
}

// detectBuildCommand inspects workDir and returns (command, tool-name).
// Order matters — more specific / more authoritative manifests first
// so a hybrid repo (e.g. Expo + Go helper) builds the right thing.
func detectBuildCommand(workDir, target string) (string, string) {
	exists := func(name string) bool {
		_, err := os.Stat(filepath.Join(workDir, name))
		return err == nil
	}
	tgt := strings.ToLower(strings.TrimSpace(target))

	// Mobile app.json / eas.json first — Expo projects are sometimes
	// wrapped inside a package.json monorepo and we don't want to run
	// `npm run build` for them.
	if exists("app.json") || exists("app.config.js") || exists("app.config.ts") || exists("eas.json") {
		if tgt == "ios" {
			return "npx expo prebuild --platform ios && cd ios && xcodebuild -workspace *.xcworkspace -scheme $(basename $(ls *.xcworkspace) .xcworkspace) -configuration Release archive", "expo-ios"
		}
		if tgt == "android" {
			return "npx expo prebuild --platform android && cd android && ./gradlew bundleRelease", "expo-android"
		}
		return "npx expo build --platform all", "expo"
	}
	if exists("pubspec.yaml") {
		if tgt != "" {
			return fmt.Sprintf("flutter build %s", tgt), "flutter"
		}
		return "flutter build apk", "flutter"
	}
	if exists("Cargo.toml") {
		if tgt == "release" {
			return "cargo build --release", "cargo"
		}
		return "cargo build", "cargo"
	}
	if exists("go.mod") {
		return "go build ./...", "go"
	}
	if exists("package.json") {
		// Prefer `npm run build` if a build script is present; fall
		// back to `npm install` (noop build for lib-only packages).
		data, _ := os.ReadFile(filepath.Join(workDir, "package.json"))
		if strings.Contains(string(data), `"build"`) {
			return "npm run build", "npm"
		}
		return "npm install", "npm"
	}
	if exists("build.gradle") || exists("build.gradle.kts") || exists("settings.gradle") {
		return "./gradlew build", "gradle"
	}
	if matches, _ := filepath.Glob(filepath.Join(workDir, "*.xcodeproj")); len(matches) > 0 {
		return "xcodebuild -configuration Release", "xcode"
	}
	if exists("CMakeLists.txt") {
		return "cmake -B build && cmake --build build", "cmake"
	}
	if exists("Makefile") || exists("makefile") {
		return "make", "make"
	}
	if exists("pyproject.toml") || exists("setup.py") {
		return "python -m build", "python"
	}
	return "", ""
}
