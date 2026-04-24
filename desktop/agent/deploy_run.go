package main

// deploy_run.go — POST /deploy/ship: execute the vault-aware deploy
// script for an (app, target) on the host, streaming stdout + stderr
// to the caller via SSE. Named "ship" to disambiguate from the older
// /deploy/run release-pipeline endpoint in deploy_pipeline.go. Designed for shared-machine flows where a
// trusted guest (with a matching allowedProjects grant) triggers a
// TestFlight / Play Store / Cloudflare deploy from their own laptop
// against someone else's Mac mini.
//
// Security envelope:
//
//  - Script body is generated server-side from vetted templates.
//    A guest's JSON body cannot inject shell; they only pick which
//    (app, target) to run.
//  - Vault values are injected into the subprocess env; they never
//    appear in the script source or in any response to the guest.
//    Only stdout/stderr stream back, and the templates reference
//    vault secrets via $VAR expansions — their plaintext doesn't
//    land in a log line unless a user explicitly echoes it.
//  - Guests are subject to the existing `allowedProjects` filter.
//    A guest with `allowedProjects=["web"]` cannot deploy "mobile"
//    no matter what they POST.
//  - Guests cannot override `stack` or `path` — those come from the
//    workspace manifest only. Owners may override explicitly.
//  - Subprocess env is a whitelist for guests: only safe-to-inherit
//    system vars (PATH/HOME/SHELL/USER/LOGNAME/LANG/LC_*/TMPDIR) plus
//    the vault-supplied project env. This stops a malicious host env
//    var (e.g. a stray GITHUB_TOKEN from the owner's shell) from
//    being visible to whatever command the template runs.
//  - Runs are time-bounded; default 20 min, configurable via the
//    body's `timeout_sec` (capped at 60 min).

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// deployShipDefaultTimeoutSec is how long a single /deploy/run can live
// without an explicit override.
const (
	deployShipDefaultTimeoutSec = 20 * 60
	deployShipMaxTimeoutSec     = 60 * 60
)

// safeSystemEnvKeys is the whitelist of parent-process env vars we
// propagate into a deploy subprocess. Keep it tight — anything that
// looks remotely secret-ish goes through the vault.
var safeSystemEnvKeys = map[string]bool{
	"PATH":    true,
	"HOME":    true,
	"USER":    true,
	"LOGNAME": true,
	"SHELL":   true,
	"LANG":    true,
	"TMPDIR":  true,
	"PWD":     true,
	"TERM":    true,
}

type deployShipRequest struct {
	App        string `json:"app"`
	Target     string `json:"target"`
	Stack      string `json:"stack,omitempty"` // owner-only
	Path       string `json:"path,omitempty"`  // owner-only
	TimeoutSec int    `json:"timeout_sec,omitempty"`
}

// handleDeployShip streams a /deploy/ship run as SSE.
//
// Body: {app, target, stack?, path?, timeout_sec?}
// Response stream events:
//
//	event: meta    — { app, target, stack, path, started_at }
//	event: line    — { stream: "stdout"|"stderr", text: "..." }
//	event: exit    — { code, duration_ms, ok }
//	event: error   — { error: "..." }
func (s *HTTPServer) handleDeployShip(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonReply(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		jsonReply(w, http.StatusInternalServerError, map[string]string{"error": "streaming unsupported"})
		return
	}

	var body deployShipRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonReply(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
		return
	}
	body.App = strings.TrimSpace(body.App)
	body.Target = strings.TrimSpace(body.Target)
	if body.App == "" || body.Target == "" {
		jsonReply(w, http.StatusBadRequest, map[string]string{"error": "app and target are required"})
		return
	}

	// Guest vs. owner gating.
	isGuest := r.Header.Get("X-Yaver-Guest") == "true"
	guestUID := r.Header.Get("X-Yaver-GuestUserID")
	if isGuest {
		// Guests cannot override stack/path — only the workspace manifest
		// decides where the code lives.
		if body.Stack != "" || body.Path != "" {
			jsonReply(w, http.StatusForbidden, map[string]string{
				"error": "guests cannot override stack or path — values come from the workspace manifest",
			})
			return
		}
		if s.guestConfigMgr != nil && !s.guestConfigMgr.GuestCanAccessProject(guestUID, body.App) {
			jsonReply(w, http.StatusForbidden, map[string]string{
				"error": "guest is not authorised for this project",
			})
			return
		}
	}

	// Resolve stack + path. Owner may override; guest always uses manifest.
	stack := body.Stack
	path := body.Path
	var workspaceRoot string
	if stack == "" || path == "" {
		ms, mp, root := resolveAppFromWorkspaceFull(body.App)
		if stack == "" {
			stack = ms
		}
		if path == "" {
			path = mp
		}
		workspaceRoot = root
	}
	if stack == "" || path == "" {
		jsonReply(w, http.StatusBadRequest, map[string]string{
			"error": "could not resolve stack and path from workspace manifest — declare the app in yaver.workspace.yaml or pass --stack --path (owner only)",
		})
		return
	}
	// Manifest paths are relative to the workspace root; everything
	// else is relative to cwd. Anchor the path to an absolute one so
	// the subprocess's Dir is unambiguous.
	if !filepath.IsAbs(path) {
		base := workspaceRoot
		if base == "" {
			if cwd, err := os.Getwd(); err == nil {
				base = cwd
			}
		}
		path = filepath.Join(base, path)
	}
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}

	// Generate the script.
	script, err := GenerateDeployScript(DeployScriptSpec{
		App:    body.App,
		Stack:  stack,
		Target: body.Target,
		Path:   path,
	})
	if err != nil {
		jsonReply(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	// Preflight — refuse to spawn if the toolchain is broken. This
	// mirrors the gate embedded in the script itself but surfaces as
	// a nice 409 with structured details before we open the stream.
	preflight, err := RunBuildDoctor(body.Target, body.App, s.vaultStore)
	if err == nil && !preflight.OK {
		jsonReply(w, http.StatusConflict, map[string]interface{}{
			"error":    "preflight failed — install missing tools / secrets first",
			"doctor":   preflight,
		})
		return
	}

	// Persist the generated script to a short-lived temp file so bash
	// can read it via a filename (also plays nicely with `set -x`).
	f, err := os.CreateTemp("", "yaver-deploy-*.sh")
	if err != nil {
		jsonReply(w, http.StatusInternalServerError, map[string]string{"error": "tempfile: " + err.Error()})
		return
	}
	scriptPath := f.Name()
	defer os.Remove(scriptPath)
	if _, err := f.WriteString(script); err != nil {
		f.Close()
		jsonReply(w, http.StatusInternalServerError, map[string]string{"error": "write tempfile: " + err.Error()})
		return
	}
	f.Close()
	_ = os.Chmod(scriptPath, 0700)

	// Build the subprocess env. Guests get a sanitised whitelist +
	// vault values; owners inherit their full env but still see vault
	// values layered on top.
	env := buildDeployShipEnv(s.vaultStore, body.App, isGuest)

	// Lock in the timeout.
	timeoutSec := body.TimeoutSec
	if timeoutSec <= 0 {
		timeoutSec = deployShipDefaultTimeoutSec
	}
	if timeoutSec > deployShipMaxTimeoutSec {
		timeoutSec = deployShipMaxTimeoutSec
	}
	ctx, cancel := context.WithTimeout(r.Context(), time.Duration(timeoutSec)*time.Second)
	defer cancel()

	// Open the SSE stream.
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)

	writeEvent := func(event string, payload interface{}) {
		b, _ := json.Marshal(payload)
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, b)
		flusher.Flush()
	}

	startedAt := time.Now()
	writeEvent("meta", map[string]interface{}{
		"app":        body.App,
		"target":     body.Target,
		"stack":      stack,
		"path":       path,
		"started_at": startedAt.UnixMilli(),
		"timeout_s":  timeoutSec,
	})

	cmd := exec.CommandContext(ctx, "bash", scriptPath)
	cmd.Env = env
	cmd.Dir = path
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		writeEvent("error", map[string]string{"error": "stdout pipe: " + err.Error()})
		return
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		writeEvent("error", map[string]string{"error": "stderr pipe: " + err.Error()})
		return
	}
	if err := cmd.Start(); err != nil {
		writeEvent("error", map[string]string{"error": "spawn: " + err.Error()})
		return
	}

	var wg sync.WaitGroup
	streamPipe := func(label string, rd io.Reader) {
		defer wg.Done()
		scanner := bufio.NewScanner(rd)
		// Allow up to 1 MB lines (default 64 KB was hitting limits on
		// noisy xcodebuild output).
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for scanner.Scan() {
			writeEvent("line", map[string]string{
				"stream": label,
				"text":   scanner.Text(),
			})
		}
	}
	wg.Add(2)
	go streamPipe("stdout", stdout)
	go streamPipe("stderr", stderr)

	waitErr := cmd.Wait()
	wg.Wait()

	exitCode := 0
	if waitErr != nil {
		if ee, ok := waitErr.(*exec.ExitError); ok {
			exitCode = ee.ExitCode()
		} else {
			exitCode = -1
			writeEvent("error", map[string]string{"error": waitErr.Error()})
		}
	}
	duration := time.Since(startedAt)
	writeEvent("exit", map[string]interface{}{
		"code":        exitCode,
		"duration_ms": duration.Milliseconds(),
		"ok":          exitCode == 0,
	})
}

// buildDeployShipEnv composes the subprocess env: sanitised system vars
// (always), plus the project's vault env (project-scoped + globals,
// project-wins-on-collision). Guest callers get only the whitelist;
// owners inherit their full parent env plus vault values.
func buildDeployShipEnv(vs *VaultStore, project string, isGuest bool) []string {
	var base []string
	if isGuest {
		// Whitelist parent env for guests.
		for _, kv := range os.Environ() {
			if eq := strings.IndexByte(kv, '='); eq > 0 {
				key := kv[:eq]
				if safeSystemEnvKeys[key] || strings.HasPrefix(key, "LC_") {
					base = append(base, kv)
				}
			}
		}
	} else {
		base = append(base, os.Environ()...)
	}

	if vs == nil {
		return base
	}
	seen := map[string]int{}
	for i, kv := range base {
		if eq := strings.IndexByte(kv, '='); eq > 0 {
			seen[kv[:eq]] = i
		}
	}
	setEnv := func(k, v string) {
		kv := k + "=" + v
		if idx, ok := seen[k]; ok {
			base[idx] = kv
		} else {
			seen[k] = len(base)
			base = append(base, kv)
		}
	}
	// Globals first so the project values can override.
	for _, sum := range vs.List("") {
		entry, err := vs.Get("", sum.Name)
		if err == nil && entry != nil {
			setEnv(entry.Name, entry.Value)
		}
	}
	for _, sum := range vs.List(project) {
		entry, err := vs.Get(project, sum.Name)
		if err == nil && entry != nil {
			setEnv(entry.Name, entry.Value)
		}
	}
	return base
}
