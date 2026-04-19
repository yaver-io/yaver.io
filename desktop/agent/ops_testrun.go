package main

// ops_testrun.go — verb "test": run the project's test suite.
// Sibling of `build` — same detection heuristic, different command
// per toolchain. Uses streaming execution so an agent can follow
// per-test output + failure frames live.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type opsTestPayload struct {
	WorkDir    string            `json:"workDir,omitempty"`
	Pattern    string            `json:"pattern,omitempty"`
	Coverage   bool              `json:"coverage,omitempty"`
	Env        map[string]string `json:"env,omitempty"`
	TimeoutSec int               `json:"timeoutSec,omitempty"`
}

func init() {
	registerOpsVerb(opsVerbSpec{
		Name:        "test",
		Description: "Run the project's test suite in workDir. Same detection as build (go / node / rust / flutter / python / gradle / xcode / make). Optional `pattern` filter when the tool supports it.",
		Schema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"workDir":    map[string]interface{}{"type": "string"},
				"pattern":    map[string]interface{}{"type": "string"},
				"coverage":   map[string]interface{}{"type": "boolean"},
				"env":        map[string]interface{}{"type": "object"},
				"timeoutSec": map[string]interface{}{"type": "integer"},
			},
			"additionalProperties": false,
		},
		Handler:    opsTestHandler,
		Streaming:  true,
		AllowGuest: false,
	})
}

func opsTestHandler(c OpsContext, payload json.RawMessage) OpsResult {
	var p opsTestPayload
	if len(payload) > 0 {
		if err := json.Unmarshal(payload, &p); err != nil {
			return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
		}
	}
	workDir := p.WorkDir
	if workDir == "" {
		workDir = "."
	}

	cmd, tool := detectOpsTestCommand(workDir, p.Pattern, p.Coverage)
	if cmd == "" {
		return OpsResult{
			OK:    false,
			Code:  "unsupported",
			Error: fmt.Sprintf("no recognised test manifest in %q", workDir),
		}
	}
	if c.Server == nil || c.Server.execMgr == nil {
		return OpsResult{OK: false, Code: "unavailable", Error: "exec manager not initialised"}
	}
	sess, err := c.Server.execMgr.StartExec(cmd, workDir, "", p.Env, p.TimeoutSec)
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
		},
	}
}

func detectOpsTestCommand(workDir, pattern string, coverage bool) (string, string) {
	exists := func(name string) bool {
		_, err := os.Stat(filepath.Join(workDir, name))
		return err == nil
	}
	filter := ""
	if pattern != "" {
		filter = opsShellQuote(pattern)
	}
	if exists("go.mod") {
		cmd := "go test ./..."
		if filter != "" {
			cmd += " -run " + filter
		}
		if coverage {
			cmd += " -cover"
		}
		return cmd, "go"
	}
	if exists("Cargo.toml") {
		cmd := "cargo test"
		if filter != "" {
			cmd += " " + filter
		}
		return cmd, "cargo"
	}
	if exists("package.json") {
		data, _ := os.ReadFile(filepath.Join(workDir, "package.json"))
		if strings.Contains(string(data), `"test"`) {
			cmd := "npm test"
			if filter != "" {
				cmd += " -- " + filter
			}
			return cmd, "npm"
		}
		return "echo 'no test script in package.json'", "npm-noop"
	}
	if exists("pubspec.yaml") {
		return "flutter test", "flutter"
	}
	if exists("build.gradle") || exists("build.gradle.kts") {
		return "./gradlew test", "gradle"
	}
	if matches, _ := filepath.Glob(filepath.Join(workDir, "*.xcodeproj")); len(matches) > 0 {
		return "xcodebuild test", "xcode"
	}
	if exists("pyproject.toml") || exists("setup.py") || exists("tox.ini") {
		if filter != "" {
			return "pytest -k " + filter, "pytest"
		}
		return "pytest", "pytest"
	}
	if exists("Makefile") || exists("makefile") {
		return "make test", "make"
	}
	return "", ""
}
