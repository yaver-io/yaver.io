package main

// Publish-job worker — agent side. Pairs with backend/convex/
// publishJobs.ts + the /publish-jobs/* http.ts routes.
//
// This is the "tap Publish, close the app, come back to a green
// check" half: the CLI/mobile enqueue a job into Convex; this Mac is
// the farm node, so its heartbeat loop calls claimNextPublishJob,
// runs the build, keeps the job alive while a 15-20 min archive
// grinds, and reports the terminal per-target outcome.
//
// Deliberate reuse, no new engine: the build itself is run by POSTing
// to THIS agent's own local /deploy/ship — the already-tested,
// vault-aware, preflighted composite path (deploy_run.go). The worker
// only translates its SSE composite stream into Convex job state.
//
// Privacy: the claim carries app NAME + targets + stack only — never
// a path. /deploy/ship resolves the project path locally from the app
// name (resolveDeployStackPath's workspace fallback). Build logs
// stream over the local SSE and are NEVER forwarded to Convex; only
// per-target metadata (ok / exitCode / errorClass / ms) is reported.
//
// Mirrors rescue.go's structure 1:1 (claim → execute → report,
// single-flight per heartbeat tick) because that pattern is proven.

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"runtime"
	"strings"
	"sync"
	"time"
)

// publishJobClaim is the work unit returned by /publish-jobs/claim.
// Intentionally has no Path — the farm node resolves it locally.
type publishJobClaim struct {
	JobID   string   `json:"jobId"`
	App     string   `json:"app"`
	Stack   string   `json:"stack"`
	Targets []string `json:"targets"`
}

type publishJobClaimResponse struct {
	OK    bool             `json:"ok"`
	Job   *publishJobClaim `json:"job"` // null when idle
	Error string           `json:"error"`
}

// publishTargetResult is the per-target metadata we report back. Same
// shape as /deploy/ship's composite summary and the Convex
// publishJobs.result object — NO logs.
type publishTargetResult struct {
	Target     string `json:"target"`
	OK         bool   `json:"ok"`
	ExitCode   int    `json:"exitCode"`
	ErrorClass string `json:"errorClass,omitempty"`
	DurationMs int64  `json:"durationMs,omitempty"`
}

// ── single-flight per heartbeat tick (mirrors rescue.go) ─────────────

var publishClaimMu sync.Mutex

func claimAndExecutePublishJobSingleFlight(baseURL, token, deviceID string) {
	if !publishClaimMu.TryLock() {
		return
	}
	defer publishClaimMu.Unlock()
	claimAndExecutePublishJob(baseURL, token, deviceID)
}

// claimAndExecutePublishJob pulls one queued job for this device and
// runs it. Best-effort: errors are logged, never propagated — a
// publish-queue hiccup must never wedge the heartbeat loop. Called
// after a successful heartbeat (Convex reachable, token valid).
func claimAndExecutePublishJob(baseURL, token, deviceID string) {
	if baseURL == "" || token == "" || deviceID == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	claim, err := claimNextPublishJob(ctx, baseURL, token, deviceID)
	cancel()
	if err != nil {
		if !isQuietRescueError(err) { // reuse rescue.go's noise filter
			log.Printf("[publish-worker] claim error: %v", err)
		}
		return
	}
	if claim == nil {
		return
	}
	log.Printf("[publish-worker] claimed job %s: %s → %s",
		claim.JobID, claim.App, strings.Join(claim.Targets, "+"))

	results, overallOK, phase := executePublishBuild(baseURL, token, claim)

	status := "done"
	if !overallOK {
		status = "failed"
	}
	rctx, rcancel := context.WithTimeout(context.Background(), 20*time.Second)
	if err := reportPublishJobResult(rctx, baseURL, token, claim.JobID, status, results, phase); err != nil {
		log.Printf("[publish-worker] report error for %s: %v", claim.JobID, err)
	}
	rcancel()
}

// executePublishBuild runs the claimed targets via this agent's own
// local /deploy/ship and returns per-target metadata. A background
// ticker keeps lastProgressAt fresh so the Convex reaper doesn't kill
// a healthy long build. Returns (results, overallOK, lastPhase).
func executePublishBuild(convexBase, token string, claim *publishJobClaim) ([]publishTargetResult, bool, string) {
	// Heartbeat the job while the build runs (mirrors the running-grace
	// the Convex side expects). Short phase message only — never log.
	phase := &atomicString{}
	phase.Set("starting build")
	stopProgress := make(chan struct{})
	var progressWG sync.WaitGroup
	progressWG.Add(1)
	go func() {
		defer progressWG.Done()
		t := time.NewTicker(60 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-stopProgress:
				return
			case <-t.C:
				pctx, pcancel := context.WithTimeout(context.Background(), 15*time.Second)
				_ = reportPublishJobProgress(pctx, convexBase, token, claim.JobID, phase.Get())
				pcancel()
			}
		}
	}()
	defer func() {
		close(stopProgress)
		progressWG.Wait()
	}()

	// First progress ping immediately so the UI flips queued → running
	// without waiting a full minute.
	pctx, pcancel := context.WithTimeout(context.Background(), 15*time.Second)
	_ = reportPublishJobProgress(pctx, convexBase, token, claim.JobID, phase.Get())
	pcancel()

	results, overallOK := runLocalShipForJob(claim, phase, token)
	return results, overallOK, phase.Get()
}

// shotsJobTargets are pseudo-targets handled locally by Engine 1 (sim +
// Maestro screenshot capture) rather than by /deploy/ship.
//
//	shots        → capture + upload screenshots only
//	shots-submit → capture + upload + metadata + attempt submit-for-review
var shotsJobTargets = map[string]bool{"shots": true, "shots-submit": true}

// platformJobTargets are deployable platform surfaces that do not go
// through /deploy/ship yet. They use the mobile_platform_deploy spine
// directly so mobile/web can enqueue TV deploys without a human shell.
var platformJobTargets = map[string]bool{
	"tv": true, "android-tv": true, "tvos": true,
	"wear-os": true, "watchos": true,
	"carplay":  true,
	"visionos": true, "android-xr": true,
}

// runLocalShipForJob dispatches the claim's targets: store binaries go
// through the existing /deploy/ship spine; `shots*` pseudo-targets run the
// local screenshot pipeline (ShotsPlan). Results are merged.
func runLocalShipForJob(claim *publishJobClaim, phase *atomicString, token string) ([]publishTargetResult, bool) {
	var shipTargets, shotsTargets, platformTargets []string
	for _, t := range claim.Targets {
		t = normalizePublishJobTarget(t)
		if shotsJobTargets[t] {
			shotsTargets = append(shotsTargets, t)
		} else if platformJobTargets[t] {
			platformTargets = append(platformTargets, t)
		} else {
			shipTargets = append(shipTargets, t)
		}
	}

	var results []publishTargetResult
	overall := true
	if len(shipTargets) > 0 {
		r, ok := runShipTargetsForJob(claim, shipTargets, phase, token)
		results = append(results, r...)
		overall = overall && ok
	}
	for _, t := range shotsTargets {
		r := runShotsTargetForJob(claim, t, phase)
		results = append(results, r)
		if !r.OK {
			overall = false
		}
	}
	for _, t := range platformTargets {
		r := runPlatformTargetForJob(t, phase)
		results = append(results, r)
		if !r.OK {
			overall = false
		}
	}
	return results, overall
}

func normalizePublishJobTarget(target string) string {
	switch strings.ToLower(strings.TrimSpace(target)) {
	case "androidtv", "leanback", "google-tv", "googletv":
		return "android-tv"
	case "apple-tv", "appletv":
		return "tvos"
	case "wear", "wearos", "android-wear", "android-watch":
		return "wear-os"
	case "watch-os", "apple-watch", "applewatch":
		return "watchos"
	case "apple-car", "apple-carplay":
		return "carplay"
	case "vision-os", "apple-vision", "apple-vision-pro", "vision-pro":
		return "visionos"
	case "androidxr", "android-vr", "quest", "meta-quest", "vr-android", "xr-android":
		return "android-xr"
	case "television":
		return "tv"
	default:
		return strings.ToLower(strings.TrimSpace(target))
	}
}

func runPlatformTargetForJob(target string, phase *atomicString) publishTargetResult {
	phase.Set("building " + target)
	start := time.Now()
	out := mcpMobilePlatformDeploy("", target, true, false, 7200, platformSubmitValidation(target))
	ok, _ := out["ok"].(bool)
	result := publishTargetResult{
		Target:     target,
		OK:         ok,
		DurationMs: time.Since(start).Milliseconds(),
	}
	if ok {
		result.ExitCode = 0
		phase.Set("finished " + target)
		return result
	}
	result.ExitCode = -1
	if code, ok := out["exit_code"].(int); ok {
		result.ExitCode = code
	}
	if out["timed_out"] == true {
		result.ErrorClass = "timeout"
	} else if errText, _ := out["error"].(string); strings.TrimSpace(errText) != "" {
		result.ErrorClass = "platform_dispatch"
	} else {
		result.ErrorClass = "platform_deploy"
	}
	phase.Set("failed " + target)
	return result
}

// runShotsTargetForJob runs Engine 1 locally for a shots* pseudo-target.
// Resolves the project path from the app name (privacy: path never on the
// wire), then drives the ShotsPlan. Submit only for shots-submit.
func runShotsTargetForJob(claim *publishJobClaim, target string, phase *atomicString) publishTargetResult {
	phase.Set("capturing screenshots")
	start := time.Now()
	fail := func(class string) publishTargetResult {
		return publishTargetResult{Target: target, OK: false, ExitCode: -1,
			ErrorClass: class, DurationMs: time.Since(start).Milliseconds()}
	}

	_, path, err := resolveDeployStackPath(claim.App, claim.Stack, "")
	if err != nil {
		log.Printf("[publish-worker] shots: resolve path for %s: %v", claim.App, err)
		return fail("resolve_path")
	}
	bundleID := readBundleIDFromAppJSON(path)
	if bundleID == "" {
		log.Printf("[publish-worker] shots: no bundle id at %s", path)
		return fail("no_bundle_id")
	}

	plan := ShotsPlan{
		App:      claim.App,
		Path:     path,
		Stack:    claim.Stack,
		BundleID: bundleID,
		Locale:   "en-US",
		Submit:   target == "shots-submit",
	}
	code := plan.Run()
	return publishTargetResult{
		Target:     target,
		OK:         code == 0,
		ExitCode:   code,
		DurationMs: time.Since(start).Milliseconds(),
	}
}

// runShipTargetsForJob POSTs the given store targets to the local
// /deploy/ship composite path and parses its SSE stream into per-target
// results. We do NOT pass a path — the server resolves it locally from the
// app name, keeping filesystem paths off the wire and out of Convex.
func runShipTargetsForJob(claim *publishJobClaim, targets []string, phase *atomicString, token string) ([]publishTargetResult, bool) {
	body := map[string]interface{}{
		"app":     claim.App,
		"stack":   claim.Stack,
		"targets": targets,
	}
	raw, _ := json.Marshal(body)

	// Builds are long (cold iOS archive ~20 min). No client timeout;
	// the Convex running-grace governs liveness instead.
	req, err := http.NewRequest("POST", localAgentBaseURL()+"/deploy/ship", bytes.NewReader(raw))
	if err != nil {
		return shipFailAll(claim.Targets, "spawn: "+err.Error()), false
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")

	resp, err := (&http.Client{Timeout: 0}).Do(req)
	if err != nil {
		return shipFailAll(targets, "ship request: "+err.Error()), false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return shipFailAll(targets, fmt.Sprintf("ship HTTP %d: %s",
			resp.StatusCode, capSnippet(b, 256))), false
	}

	// Parse the SSE composite stream. We care about `exit` (per-target
	// outcome) and `composite` (authoritative summary). `line` events
	// are build log — read and discarded here, NEVER sent to Convex.
	perTarget := map[string]publishTargetResult{}
	var compositeSummary []map[string]interface{}

	reader := bufio.NewReaderSize(resp.Body, 64*1024)
	var event, dataBuf string
	for {
		line, rerr := reader.ReadString('\n')
		if line == "" && rerr != nil {
			break
		}
		line = strings.TrimRight(line, "\r\n")
		switch {
		case strings.HasPrefix(line, "event: "):
			event = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			dataBuf = strings.TrimPrefix(line, "data: ")
		case line == "":
			if event == "" {
				continue
			}
			var p map[string]interface{}
			_ = json.Unmarshal([]byte(dataBuf), &p)
			switch event {
			case "meta":
				if t, _ := p["target"].(string); t != "" {
					phase.Set("building " + t)
				}
			case "exit":
				tgt, _ := p["target"].(string)
				if tgt == "" && len(targets) == 1 {
					tgt = targets[0]
				}
				code, _ := p["code"].(float64)
				ok, _ := p["ok"].(bool)
				ec, _ := p["error_class"].(string)
				dur, _ := p["duration_ms"].(float64)
				perTarget[tgt] = publishTargetResult{
					Target: tgt, OK: ok, ExitCode: int(code),
					ErrorClass: ec, DurationMs: int64(dur),
				}
				phase.Set("finished " + tgt)
			case "composite":
				if s, ok := p["summary"].([]interface{}); ok {
					for _, it := range s {
						if m, ok := it.(map[string]interface{}); ok {
							compositeSummary = append(compositeSummary, m)
						}
					}
				}
			}
			event, dataBuf = "", ""
		}
	}

	// Composite summary is authoritative when present (multi-target).
	if len(compositeSummary) > 0 {
		out := make([]publishTargetResult, 0, len(compositeSummary))
		all := true
		for _, m := range compositeSummary {
			tgt, _ := m["target"].(string)
			ok, _ := m["ok"].(bool)
			code, _ := m["code"].(float64)
			ec, _ := m["error_class"].(string)
			dur, _ := m["duration_ms"].(float64)
			if !ok {
				all = false
			}
			out = append(out, publishTargetResult{
				Target: tgt, OK: ok, ExitCode: int(code),
				ErrorClass: ec, DurationMs: int64(dur),
			})
		}
		return out, all
	}

	// Single-target / no composite event: fall back to exit events.
	out := make([]publishTargetResult, 0, len(targets))
	all := len(targets) > 0
	for _, t := range targets {
		r, seen := perTarget[t]
		if !seen {
			r = publishTargetResult{Target: t, OK: false, ExitCode: -1,
				ErrorClass: "no_exit_event"}
		}
		if !r.OK {
			all = false
		}
		out = append(out, r)
	}
	return out, all
}

func shipFailAll(targets []string, reason string) []publishTargetResult {
	log.Printf("[publish-worker] ship failed: %s", reason)
	out := make([]publishTargetResult, 0, len(targets))
	for _, t := range targets {
		out = append(out, publishTargetResult{
			Target: t, OK: false, ExitCode: -1, ErrorClass: "ship_dispatch",
		})
	}
	return out
}

// ── Convex transport (mirrors rescue.go's claim/report helpers) ──────

func claimNextPublishJob(ctx context.Context, baseURL, token, deviceID string) (*publishJobClaim, error) {
	body, _ := json.Marshal(map[string]string{"deviceId": deviceID})
	req, err := http.NewRequestWithContext(ctx, "POST", baseURL+"/publish-jobs/claim", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == 401 {
		return nil, ErrAuthExpired
	}
	respBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("claim status %d: %s", resp.StatusCode, capSnippet(respBytes, 256))
	}
	var parsed publishJobClaimResponse
	if err := json.Unmarshal(respBytes, &parsed); err != nil {
		return nil, fmt.Errorf("parse claim response: %w", err)
	}
	if !parsed.OK {
		if parsed.Error != "" {
			return nil, fmt.Errorf("%s", parsed.Error)
		}
		return nil, fmt.Errorf("claim returned ok=false")
	}
	return parsed.Job, nil
}

func reportPublishJobProgress(ctx context.Context, baseURL, token, jobID, message string) error {
	if len(message) > 200 {
		message = message[:200]
	}
	body, _ := json.Marshal(map[string]string{"jobId": jobID, "message": message})
	req, err := http.NewRequestWithContext(ctx, "POST", baseURL+"/publish-jobs/progress", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return fmt.Errorf("progress status %d: %s", resp.StatusCode, capSnippet(b, 256))
	}
	return nil
}

func reportPublishJobResult(ctx context.Context, baseURL, token, jobID, status string, results []publishTargetResult, message string) error {
	if status != "done" && status != "failed" {
		return fmt.Errorf("invalid status %q", status)
	}
	if len(message) > 500 {
		message = message[:500]
	}
	payload := map[string]interface{}{
		"jobId":   jobID,
		"status":  status,
		"result":  results,
		"message": message,
	}
	body, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, "POST", baseURL+"/publish-jobs/report", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("report status %d: %s", resp.StatusCode, capSnippet(b, 256))
	}
	return nil
}

// atomicString — tiny race-free holder for the current build phase
// message shared between the SSE reader and the progress ticker.
type atomicString struct {
	mu sync.RWMutex
	s  string
}

func (a *atomicString) Set(v string) { a.mu.Lock(); a.s = v; a.mu.Unlock() }
func (a *atomicString) Get() string  { a.mu.RLock(); defer a.mu.RUnlock(); return a.s }

// computePublishCapabilities reports which app stores this host can
// build+publish to, advertised in the heartbeat so the UI can show
// only publish-capable devices. macOS = both (Xcode does iOS,
// Gradle/Java does Android); Linux = Android only; iOS is Mac-only,
// always. Static — no toolchain probing here (the per-target `yaver
// doctor build` preflight inside /deploy/ship is the real gate).
func computePublishCapabilities() []string {
	switch runtime.GOOS {
	case "darwin":
		return []string{"ios", "android", "android-tv", "tvos", "tv", "wear-os", "watchos", "carplay", "visionos", "android-xr"}
	case "linux":
		return []string{"android", "android-tv", "wear-os", "android-xr"}
	default:
		return []string{}
	}
}
