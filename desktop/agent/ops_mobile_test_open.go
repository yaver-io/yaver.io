package main

// ops_mobile_test_open.go — the "open the Yaver mobile app for testing" verb.
//
// WHY (2026-08-09, user call): an agent (Claude/Codex/opencode via MCP) that
// wants to test a React Native app needs a one-call verb, not a four-step recipe
// ("cd mobile && npm run web, then launch e2e/open-mobile-app.mjs with a
// profile, then…"). The repo already has two PROVEN scripts for the two
// legitimately different jobs:
//
//   - e2e/mobile-ui-overview.mjs   — HEADLESS route inventory + screenshots at
//     a REAL mobile device context. This is the default: a remote Mac must not
//     require somebody to touch its keyboard, approve a dialog, or sign in.
//   - e2e/open-mobile-app.mjs      — explicitly-requested HEADED Chromium at a REAL mobile viewport
//     (iPhone 13, touch enabled, persistent profile) so a HUMAN signs in by
//     hand and tests. Never substitute a narrowed desktop Chrome window for
//     this (AGENTS.md viewport rule).
//   - e2e/verify_live_console.mjs  — HEADLESS assertion: opens the task detail
//     and checks the LiveConsoleSection rendered the streamed opencode console.
//
// The verb picks by `mode`: "overview" (default, fully headless), "open"
// (explicit human-in-the-loop headed window), or "verify" (headless closed-loop
// assertion). It resolves the repo root
// (work-dir of the agent), ensures Metro is up, and execs the right script.
// This is the MCP/ops twin of the documented manual procedure in
// AGENTS.md/CLAUDE.md ("Opening the mobile app for a HUMAN to test").

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

//go:embed mobile_ui_overview.mjs
var mobileUIOverviewScript []byte

func init() {
	registerOpsVerb(opsVerbSpec{
		Name: "mobile-test-open",
		Description: "Inspect a React Native/Expo app at a REAL mobile viewport without physical access to the host. " +
			"mode=overview (default) discovers Expo Router screens, renders them headlessly in an iPhone device context, " +
			"and returns screenshots, visible text, and browser errors. mode=open is an explicit headed human session; " +
			"mode=verify runs the Yaver-specific headless closed-loop assertion that the " +
			"task-detail LiveConsoleSection rendered the streamed opencode console. Never substitute a narrowed " +
			"desktop Chrome window for the mobile app — RN-web renders a different component tree without the device " +
			"context. Overview never falls back to a physical phone or asks for interaction on the remote host.",
		Schema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"mode": map[string]interface{}{
					"type": "string", "enum": []string{"overview", "open", "verify"},
					"description": "overview = fully headless screen inventory (default). open = explicit headed human session. verify = Yaver headless closed-loop assertion.",
				},
				"workDir": map[string]interface{}{
					"type": "string", "description": "React Native/Expo project root. Defaults to the agent work-dir; for the Yaver monorepo, mobile/ is selected automatically.",
				},
				"profile": map[string]interface{}{
					"type":        "string",
					"description": "Persistent profile name/path for mode=open. Default ~/.yaver-e2e-profile; use a fresh one when the default is locked by another Chromium instance.",
				},
				"url": map[string]interface{}{
					"type":        "string",
					"description": "Mobile web URL. Default http://localhost:8081 (Metro).",
				},
				"timeout_sec": map[string]interface{}{
					"type":        "integer",
					"description": "Max seconds to wait (verify mode, and Metro start in open mode). Default 180.",
				},
			},
			"additionalProperties": false,
		},
		Handler:        opsMobileTestOpenHandler,
		Streaming:      false,
		AllowCompanion: false,
	})
}

type mobileTestOpenRequest struct {
	Mode       string `json:"mode"`
	WorkDir    string `json:"workDir"`
	Profile    string `json:"profile"`
	URL        string `json:"url"`
	TimeoutSec int    `json:"timeout_sec"`
}

func opsMobileTestOpenHandler(c OpsContext, payload json.RawMessage) OpsResult {
	var req mobileTestOpenRequest
	if len(payload) > 0 {
		if err := json.Unmarshal(payload, &req); err != nil {
			return OpsResult{OK: false, Code: "bad_payload", Error: "mobile-test-open payload: " + err.Error()}
		}
	}
	mode := req.Mode
	if mode == "" {
		mode = "overview"
	}
	if mode != "overview" && mode != "open" && mode != "verify" {
		return OpsResult{OK: false, Code: "bad_payload", Error: fmt.Sprintf("mode must be 'overview', 'open', or 'verify', got %q", mode)}
	}
	timeout := time.Duration(req.TimeoutSec) * time.Second
	if req.TimeoutSec <= 0 {
		timeout = 180 * time.Second
		if mode == "overview" {
			timeout = 10 * time.Minute
		}
	}

	// Repo root = the agent's work dir. The e2e scripts live in <repo>/e2e.
	repoRoot := ""
	if c.Server != nil && c.Server.taskMgr != nil {
		repoRoot = c.Server.taskMgr.workDir
	}
	if repoRoot == "" {
		// Fall back to the process CWD if the server context lacks a work dir.
		if wd, err := os.Getwd(); err == nil {
			repoRoot = wd
		}
	}
	projectRoot := strings.TrimSpace(req.WorkDir)
	if projectRoot == "" {
		projectRoot = repoRoot
		if isFile(filepath.Join(repoRoot, "mobile", "package.json")) {
			projectRoot = filepath.Join(repoRoot, "mobile")
		}
	}
	projectRoot, _ = filepath.Abs(projectRoot)
	e2eDir := filepath.Join(repoRoot, "e2e")
	if mode != "overview" {
		needed := "open-mobile-app.mjs"
		if mode == "verify" {
			needed = "verify_live_console.mjs"
		}
		if info, err := os.Stat(filepath.Join(e2eDir, needed)); err != nil || info.IsDir() {
			return OpsResult{OK: false, Code: "not_found",
				Error: fmt.Sprintf("e2e/%s not found under %q — this explicit mode needs a Yaver source checkout", needed, repoRoot)}
		}
	}

	// nodePath: prefer the agent runtime node, then system node. The
	// managed runtime lives at ~/.yaver/runtimes/node/bin/node; the
	// testkit resolves it the same way (detectManagedOrSystemNode).
	node := ""
	if n, err := exec.LookPath("node"); err == nil {
		node = n
	} else if home, herr := os.UserHomeDir(); herr == nil {
		candidate := filepath.Join(home, ".yaver", "runtimes", "node", "bin", "node")
		if info, serr := os.Stat(candidate); serr == nil && !info.IsDir() {
			node = candidate
		}
	}
	if node == "" {
		return OpsResult{OK: false, Code: "unavailable", Error: "node not found on PATH or ~/.yaver/runtimes/node/bin"}
	}

	url := req.URL
	if url == "" {
		url = "http://localhost:8081"
	}
	var overviewResources *ResourcePressure
	if mode == "overview" {
		fresh := sampleResourcePressure()
		overviewResources = &fresh
		if fresh.Level == resourceLevelCritical {
			return OpsResult{OK: false, Code: ReasonBoxResourcePressure,
				Error: "headless mobile overview cannot start while the remote host is under critical resource pressure",
				Initial: map[string]interface{}{
					"resources": fresh, "physicalAccessRequired": false,
					"remedy": "wait for Yaver's resource warden to shed optional load, then retry this same headless overview",
				}}
		}
	}

	// Metro must be up before every browser mode. Probe the requested URL; if it
	// is not answering, start the PROJECT's web script and wait. This stays
	// project-relative: no username or workstation path is baked into the lane.
	if !httpReady(url, 2*time.Second) {
		started, err := ensureMetroWeb(projectRoot, url, timeout)
		if err != nil {
			return OpsResult{OK: false, Code: "metro_start_failed",
				Error: fmt.Sprintf("Metro not serving %s and could not be started: %v", url, err)}
		}
		if started {
			// Give the first bundle a moment after the server answers.
			time.Sleep(3 * time.Second)
		}
	}

	switch mode {
	case "overview":
		artifactDir, err := newMobileOverviewArtifactDir()
		if err != nil {
			return OpsResult{OK: false, Code: "artifact_failed", Error: err.Error()}
		}
		runCtx := c.Ctx
		if runCtx == nil {
			runCtx = context.Background()
		}
		runCtx, cancel := context.WithTimeout(runCtx, timeout)
		defer cancel()
		scriptPath := filepath.Join(artifactDir, "mobile-ui-overview.mjs")
		if err := os.WriteFile(scriptPath, mobileUIOverviewScript, 0o700); err != nil {
			return OpsResult{OK: false, Code: "artifact_failed", Error: "write embedded overview runner: " + err.Error()}
		}
		cmd := exec.CommandContext(runCtx, node, scriptPath)
		cmd.Dir = projectRoot
		cmd.Env = append(os.Environ(),
			"MOBILE_WEB_URL="+url,
			"MOBILE_OVERVIEW_WORK_DIR="+projectRoot,
			"MOBILE_OVERVIEW_ARTIFACT_DIR="+artifactDir,
			"YAVER_PLAYWRIGHT_ROOT="+e2eDir,
		)
		if cfg, loadErr := LoadConfig(); loadErr == nil && cfg != nil && strings.TrimSpace(cfg.AuthToken) != "" {
			cmd.Env = append(cmd.Env, "YAVER_AGENT_TOKEN="+cfg.AuthToken)
		}
		out, runErr := cmd.CombinedOutput()
		manifest := filepath.Join(artifactDir, "manifest.json")
		if runErr != nil {
			tail := tailStr(string(out), 6000)
			return OpsResult{OK: false, Code: "overview_failed", Error: fmt.Sprintf("headless mobile overview failed: %v", runErr), Initial: map[string]interface{}{
				"lane": "browser", "artifactDir": artifactDir, "manifest": manifest, "output": tail,
				"physicalAccessRequired": false, "resources": overviewResources,
			}}
		}
		return OpsResult{OK: true, Initial: map[string]interface{}{
			"mode": "overview", "lane": "browser", "artifactDir": artifactDir, "manifest": manifest,
			"projectRoot": projectRoot, "url": url, "output": tailStr(string(out), 6000),
			"physicalAccessRequired": false, "resources": overviewResources,
		}}
	case "open":
		profile := req.Profile
		if profile == "" {
			profile = filepath.Join(os.Getenv("HOME"), ".yaver-e2e-profile")
		}
		cmd := exec.Command(node, filepath.Join(e2eDir, "open-mobile-app.mjs"))
		cmd.Env = append(os.Environ(), "MOBILE_WEB_URL="+url, "E2E_PROFILE="+profile)
		if err := cmd.Start(); err != nil {
			return OpsResult{OK: false, Code: "spawn_failed", Error: fmt.Sprintf("launch open-mobile-app.mjs: %v", err)}
		}
		return OpsResult{OK: true, Initial: map[string]interface{}{
			"mode":    "open",
			"pid":     cmd.Process.Pid,
			"profile": profile,
			"url":     url,
			"note":    "Headed Chromium at iPhone 13 viewport (touch enabled, persistent profile). Sign in by hand; the session persists for later runs. Leave it open and run mobile-test-open mode=verify to assert the LiveConsoleSection.",
		}}
	default: // verify
		cmd := exec.Command(node, filepath.Join(e2eDir, "verify_live_console.mjs"))
		cmd.Env = append(os.Environ(), "MOBILE_WEB_URL="+url)
		ctx, cancel := context.WithTimeout(context.Background(), timeout+30*time.Second)
		defer cancel()
		cmd = exec.CommandContext(ctx, node, filepath.Join(e2eDir, "verify_live_console.mjs"))
		cmd.Env = append(os.Environ(), "MOBILE_WEB_URL="+url)
		out, err := cmd.CombinedOutput()
		tail := string(out)
		if len(tail) > 4000 {
			tail = tail[len(tail)-4000:]
		}
		if err != nil {
			return OpsResult{OK: false, Code: "verify_failed",
				Error:   fmt.Sprintf("verify_live_console.mjs failed: %v", err),
				Initial: map[string]interface{}{"output": tail}}
		}
		return OpsResult{OK: true, Initial: map[string]interface{}{
			"mode":   "verify",
			"result": "ALL PASS — task detail rendered the LiveConsoleSection with the streamed opencode console",
			"output": tail,
		}}
	}
}

// httpReady probes a URL until it answers or the timeout elapses.
func httpReady(u string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	client := &http.Client{Timeout: 2 * time.Second}
	for time.Now().Before(deadline) {
		resp, err := client.Get(u)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode < 500 {
				return true
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	return false
}

// ensureMetroWeb starts `npm run web` (expo start --web) in <repo>/mobile and
// waits until the URL answers. Returns true when it had to start Metro.
func ensureMetroWeb(projectRoot, readyURL string, wait time.Duration) (bool, error) {
	if info, err := os.Stat(filepath.Join(projectRoot, "package.json")); err != nil || info.IsDir() {
		return false, fmt.Errorf("package.json not found under %q", projectRoot)
	}
	cmd := exec.Command("npm", "run", "web")
	cmd.Dir = projectRoot
	cmd.Env = append(os.Environ(), "CI=1")
	if err := cmd.Start(); err != nil {
		return false, err
	}
	// Let expo boot; poll the URL up to `wait`.
	deadline := time.Now().Add(wait)
	client := &http.Client{Timeout: 2 * time.Second}
	for time.Now().Before(deadline) {
		resp, err := client.Get(readyURL)
		if err == nil {
			resp.Body.Close()
			return true, nil
		}
		time.Sleep(1 * time.Second)
	}
	// Process may still be warming up; don't kill it — report and let the
	// caller's retry or the human decide.
	return true, fmt.Errorf("Metro did not answer :8081 within %v (still starting?)", wait)
}

func isFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// newMobileOverviewArtifactDir owns one exact namespace. Old runs from this
// feature are the only artifacts it may reap; source trees, simulator data and
// unrelated test output are never candidates. A successful run remains for
// inspection and naturally ages out after seven days.
func newMobileOverviewArtifactDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	root := filepath.Join(home, ".yaver", "artifacts", "mobile-overview")
	if err := os.MkdirAll(root, 0o700); err != nil {
		return "", err
	}
	entries, _ := os.ReadDir(root)
	cutoff := time.Now().Add(-7 * 24 * time.Hour)
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		info, statErr := entry.Info()
		if statErr == nil && info.ModTime().Before(cutoff) {
			_ = os.RemoveAll(filepath.Join(root, entry.Name()))
		}
	}
	return os.MkdirTemp(root, "run-")
}

// Keep strings imported for the trim helper even if unused later.
var _ = strings.TrimSpace
