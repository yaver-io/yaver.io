package main

// develop_for.go — P2 orchestration verb.
//
// One MCP call turns "launch Talos for Android Watch" into the whole
// loop: resolve the machine → verify an authenticated runner lives
// there → resolve the mechanism per (framework, surface, platform) →
// create+boot a remote-runtime session on the resolved target →
// launch the app → return a session handle + first frame.
//
// This is not a new transport. It composes the P0 target fan-out and
// the P1 `runtime_*` verbs. Every step is idempotent so retries land
// on the same session.

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"

	"github.com/yaver-io/agent/testkit"
)

// DevelopForRequest is the MCP verb payload.
type DevelopForRequest struct {
	Project   string `json:"project"`
	Framework string `json:"framework,omitempty"`
	Surface   string `json:"surface"`
	Platform  string `json:"platform,omitempty"`
	Machine   string `json:"machine,omitempty"`
	// RenderOn (Axis 3) lets a client on surface A ask surface B to
	// render the resulting stream. In P2 we only surface the field on
	// the response so a sibling can attach; full cast routing lands in P5.
	RenderOn string `json:"renderOn,omitempty"`
	// BundleID is optional — when set, launch-app runs after boot. If
	// empty, the caller can fire runtime_command {launch-app,bundleId}
	// separately (useful for RN Hermes flows where the bundle id is
	// io.yaver.mobile).
	BundleID string `json:"bundleId,omitempty"`
	WorkDir  string `json:"workDir,omitempty"`
}

// DevelopForResult is what we return to MCP. Fields chosen to unblock
// the immediate next chat turn: `sessionId` for further runtime_*
// verbs, `firstFrame` (base64 JPEG) so a client can render without
// another round-trip, `runnerSessionHint` so the runner knows which
// session to attach to.
type DevelopForResult struct {
	SessionID         string `json:"sessionId"`
	Mechanism         string `json:"mechanism"`
	TargetID          string `json:"targetId"`
	RunnerSessionHint string `json:"runnerSessionHint"`
	RenderOn          string `json:"renderOn,omitempty"`
	FirstFrameJPEG    string `json:"firstFrameJpegBase64,omitempty"`
	Note              string `json:"note,omitempty"`
}

// developForRunnerAuthGate is the gate seam — swappable by tests so
// we don't have to install a real runner on the CI host.
var developForRunnerAuthGate = runnerAuthGateProbe

// developForRuntimeCall lets tests intercept the HTTP proxy so a
// pure-Go test can exercise the whole verb without spinning a real
// agent on 127.0.0.1:18080.
var developForRuntimeCall = remoteRuntimeHTTPMCP

// developForFrameCall lets tests supply a canned JPEG for the first
// frame instead of hitting the real /frame handler (which needs a
// booted sim).
var developForFrameCall = remoteRuntimeFrameJPEG

// runnerAuthGateProbe returns nil when the target machine has at least one
// installed + authenticated runner. Empty deviceID = local mini. This
// is the hard gate the plan mandates before we boot anything.
func runnerAuthGateProbe(deviceID string) error {
	rowsAny := mcpRunnerAuthStatus(deviceID)
	m, ok := rowsAny.(map[string]any)
	if !ok {
		return fmt.Errorf("runner-auth probe returned unexpected shape")
	}
	if errMsg, hasErr := m["error"].(string); hasErr && errMsg != "" {
		return fmt.Errorf("runner-auth probe failed: %s", errMsg)
	}
	rows, ok := m["runners"].([]runnerAuthStatusRow)
	if !ok {
		return fmt.Errorf("runner-auth probe returned no runners field")
	}
	for _, row := range rows {
		// An authed runner is: installed on disk AND the runner's own
		// auth-config is present (AuthConfigured is the field the local
		// probe sets from ~/.claude/... / codex config / opencode
		// config). AuthVerified is a stronger signal when the runner
		// itself confirms sign-in, but we don't require it because
		// older agents don't populate it (see runnerAuthStatusRow doc).
		if row.Installed && (row.AuthConfigured || row.AuthVerified) {
			return nil
		}
	}
	target := deviceID
	if target == "" {
		target = "this machine"
	}
	return fmt.Errorf("no authed runner on %s — run `yaver runner auth` on %s and retry", target, target)
}

// RunDevelopFor composes the whole loop. Errors return early with a
// clean message so a runner can decide whether to reprompt the user.
func RunDevelopFor(ctx context.Context, req DevelopForRequest) (DevelopForResult, error) {
	if strings.TrimSpace(req.Framework) == "" {
		req.Framework = frameworkForProject(req.Project, req.WorkDir)
	}
	if req.Framework == "" {
		return DevelopForResult{}, fmt.Errorf("framework required (no auto-detect for project %q)", req.Project)
	}
	if strings.TrimSpace(req.Surface) == "" {
		return DevelopForResult{}, fmt.Errorf("surface required (phone/tablet/watch/tv/vision/car/web)")
	}
	if err := developForRunnerAuthGate(strings.TrimSpace(req.Machine)); err != nil {
		return DevelopForResult{}, err
	}

	host := currentHostCaps(ctx)
	mechanism, targetID, err := ResolveMechanism(req.Framework, req.Surface, req.Platform, host)
	if err != nil {
		return DevelopForResult{}, err
	}

	workDir := strings.TrimSpace(req.WorkDir)
	if workDir == "" {
		workDir = strings.TrimSpace(req.Project)
	}

	// Create the session via the local HTTP handler so the returned
	// payload matches exactly what runtime_create would give a runner.
	createPayload := map[string]any{
		"framework": req.Framework,
		"workDir":   workDir,
		"targetId":  targetID,
	}
	body, status, err := developForRuntimeCall("POST", "/remote-runtime/sessions", createPayload)
	if err != nil {
		return DevelopForResult{}, err
	}
	if status >= 400 {
		return DevelopForResult{}, fmt.Errorf("session create failed: HTTP %d — %s", status, string(body))
	}
	var session RemoteRuntimeSession
	if err := json.Unmarshal(body, &session); err != nil {
		return DevelopForResult{}, fmt.Errorf("decode session: %w", err)
	}

	// Launch the app if we know the bundle id. Missing bundle id is
	// fine for a "just show me the sim" call — the caller can hit
	// runtime_command later.
	if strings.TrimSpace(req.BundleID) != "" {
		launchPayload := map[string]any{
			"command":  "launch-app",
			"bundleId": req.BundleID,
		}
		if _, status, err := developForRuntimeCall("POST",
			"/remote-runtime/sessions/"+session.ID+"/command", launchPayload); err != nil {
			return DevelopForResult{}, fmt.Errorf("launch-app: %w", err)
		} else if status >= 400 {
			return DevelopForResult{}, fmt.Errorf("launch-app: HTTP %d", status)
		}
	}

	// Pull the first frame so a caller can render without another
	// round-trip. Best-effort — if the frame isn't ready yet (some
	// sims need a beat after boot), we return the session anyway.
	frame, _, _ := developForFrameCall(session.ID)

	result := DevelopForResult{
		SessionID:         session.ID,
		Mechanism:         string(mechanism),
		TargetID:          targetID,
		RunnerSessionHint: session.ID,
		RenderOn:          strings.TrimSpace(req.RenderOn),
		Note:              fmt.Sprintf("mechanism=%s target=%s surface=%s", mechanism, targetID, req.Surface),
	}
	if len(frame) > 0 {
		result.FirstFrameJPEG = base64Encode(frame)
	}
	return result, nil
}

func currentHostCaps(ctx context.Context) HostCaps {
	fams, _ := testkit.InstalledRuntimeFamilies(ctx)
	adbAvail := false
	if _, err := exec.LookPath("adb"); err == nil {
		if _, err := exec.LookPath("emulator"); err == nil {
			adbAvail = true
		}
	}
	return HostCaps{AppleRuntimeFamilies: fams, AndroidEmulatorAvailable: adbAvail}
}

// frameworkForProject is a tiny best-effort heuristic. Real detection
// lives in the workspace scanner; this is intentionally cheap — the
// verb accepts an explicit `framework` field so callers can bypass.
func frameworkForProject(project, workDir string) string {
	switch strings.ToLower(strings.TrimSpace(project)) {
	case "talos", "yaver", "mobile":
		return "expo"
	case "sfmg":
		return "expo"
	}
	// Peek at workDir extension — if the caller passed one, we can
	// map common ones without shelling out.
	wd := strings.ToLower(strings.TrimSpace(workDir))
	switch {
	case strings.HasSuffix(wd, ".xcodeproj"):
		return "swift"
	case strings.HasSuffix(wd, ".gradle") || strings.HasSuffix(wd, "build.gradle"):
		return "kotlin"
	}
	return ""
}

// base64Encode is a tiny wrapper to avoid pulling encoding/base64 into
// callers' import list.
func base64Encode(buf []byte) string {
	return base64.StdEncoding.EncodeToString(buf)
}
