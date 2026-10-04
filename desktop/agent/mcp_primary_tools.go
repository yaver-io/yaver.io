package main

// mcp_primary_tools.go — MCP sugar for the user's "primary" device.
//
// Mirrors the `yaver primary …` CLI verbs so AI agents (Claude Code,
// Codex, OpenCode, etc.) can drive the user's primary remote box
// without having to look up a deviceId first. Each tool resolves the
// primary deviceId from Convex (`userSettings.primaryDeviceId`) once,
// then delegates to the existing per-device MCP plumbing.
//
//   primary_auth   → device_reauth_start (Yaver token) OR
//                    native runner PTY (claude / codex / opencode)
//   primary_status → fetchRemoteAgentStatusByDeviceID
//   primary_ping   → reachability + agent identity
//   primary_projects (with mobile_only flag) → /projects [/mobile]

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type mobileWorkspaceOnboardingSelectArgs struct {
	Device   string `json:"device"`
	Runner   string `json:"runner"`
	Model    string `json:"model,omitempty"`
	Mode     string `json:"mode,omitempty"`
	Provider string `json:"provider,omitempty"`
}

func remoteRunnerReadyForMobileWorkspace(option remoteRunnerSummary) bool {
	return option.Installed && option.Ready && option.AuthConfigured && option.AuthVerified
}

// mcpMobileWorkspaceOnboardingStatus is the machine-readable counterpart of the
// Mobile Workspace target + git steps. It intentionally returns capability
// probes from the selected box (runner auth and git clone/CI readiness), not
// PATH inventory, and never includes provider tokens.
func mcpMobileWorkspaceOnboardingStatus() map[string]interface{} {
	cfg, err := LoadConfig()
	if err != nil || cfg == nil || strings.TrimSpace(cfg.AuthToken) == "" {
		return map[string]interface{}{"ok": false, "error": "not signed in — run 'yaver auth' first"}
	}
	convex := strings.TrimSpace(cfg.ConvexSiteURL)
	if convex == "" {
		convex = defaultConvexSiteURL
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	settings, err := fetchUserSettings(ctx, nil)
	if err != nil {
		return map[string]interface{}{"ok": false, "error": "read onboarding settings: " + err.Error()}
	}
	devices, err := primaryListDevices(ctx, cfg.AuthToken, convex)
	if err != nil {
		return map[string]interface{}{"ok": false, "error": "list devices: " + err.Error()}
	}
	primaryID := strings.TrimSpace(settings.PrimaryDeviceID)
	if primaryID == "" && len(devices) == 1 {
		primaryID = devices[0].DeviceID
	}
	options := make([]map[string]interface{}, 0, len(devices))
	for _, device := range devices {
		options = append(options, map[string]interface{}{
			"deviceId": device.DeviceID,
			"name":     device.Name,
			"platform": device.Platform,
			"online":   device.IsOnline,
			"primary":  device.DeviceID == primaryID,
		})
	}
	out := map[string]interface{}{
		"ok": true,
		"backend": map[string]interface{}{
			"id":        "yaver-serverless",
			"engine":    "sqlite",
			"portable":  true,
			"migration": "schema.yaml + yaver.serverless.yaml",
		},
		"devices":         options,
		"primaryDeviceId": primaryID,
		"gitProviders": []map[string]interface{}{
			{"id": "yaver-git", "configured": true, "ready": true, "builtIn": true},
			{"id": "github", "configured": false, "ready": false},
			{"id": "gitlab", "configured": false, "ready": false},
		},
	}
	if primaryID == "" {
		return out
	}
	report, err := fetchRemoteAgentStatusByDeviceID(ctx, primaryID)
	if err != nil {
		out["targetError"] = err.Error()
		return out
	}
	out["primaryDevice"] = report
	if report.MobileWorkspace != nil {
		out["workspace"] = report.MobileWorkspace
	} else {
		out["workspace"] = map[string]interface{}{
			"ok":     false,
			"code":   "mobile_workspace.agent_upgrade_required",
			"detail": "The selected remote agent does not expose Mobile Workspace readiness yet",
			"action": map[string]interface{}{"label": "Update Yaver agent", "method": "POST", "path": "/agent/update", "stream": "/streams/agent-update"},
		}
	}
	for _, pref := range settings.PrimaryRunnerByDevice {
		if pref.DeviceID == primaryID {
			out["primaryRunner"] = map[string]interface{}{
				"runner": pref.RunnerID, "model": pref.Model, "mode": pref.Mode, "provider": pref.Provider,
			}
			break
		}
	}
	if report.Git != nil {
		states := []map[string]interface{}{{"id": "yaver-git", "configured": true, "ready": true, "builtIn": true}}
		for _, provider := range report.Git.Providers {
			states = append(states, map[string]interface{}{
				"id":         provider.ID,
				"configured": provider.Configured,
				"ready":      provider.Ready,
				"cloneReady": provider.CloneReady,
				"ciReady":    provider.CIReady,
				"username":   provider.Username,
				"detail":     provider.Detail,
				"warning":    provider.Warning,
			})
		}
		out["gitProviders"] = states
	}
	return out
}

// mcpMobileWorkspaceOnboardingSelect persists the exact target highlighted by the
// onboarding picker. The device + runner preference is written in one Convex
// /settings request so "Next" cannot save half of the pair.
func mcpMobileWorkspaceOnboardingSelect(args mobileWorkspaceOnboardingSelectArgs) map[string]interface{} {
	cfg, err := LoadConfig()
	if err != nil || cfg == nil || strings.TrimSpace(cfg.AuthToken) == "" {
		return map[string]interface{}{"ok": false, "error": "not signed in — run 'yaver auth' first"}
	}
	convex := strings.TrimSpace(cfg.ConvexSiteURL)
	if convex == "" {
		convex = defaultConvexSiteURL
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	devices, err := primaryListDevices(ctx, cfg.AuthToken, convex)
	if err != nil {
		return map[string]interface{}{"ok": false, "error": "list devices: " + err.Error()}
	}
	device, err := resolveMachineRoleDevice(strings.TrimSpace(args.Device), devices)
	if err != nil {
		return map[string]interface{}{"ok": false, "error": err.Error()}
	}
	runner := normalizeRunnerAuthName(args.Runner)
	if runner == "" && strings.EqualFold(strings.TrimSpace(args.Runner), "opencode") {
		runner = "opencode"
	}
	if runner != "claude" && runner != "codex" && runner != "opencode" {
		return map[string]interface{}{"ok": false, "error": "runner must be claude-code, codex, or opencode"}
	}
	report, err := fetchRemoteAgentStatusByDeviceID(ctx, device.DeviceID)
	if err != nil {
		return map[string]interface{}{"ok": false, "error": "probe selected device: " + err.Error()}
	}
	ready := false
	for _, option := range report.Runners {
		if normalizeRunnerAuthName(option.ID) == runner || strings.EqualFold(option.ID, runner) {
			ready = remoteRunnerReadyForMobileWorkspace(option)
			break
		}
	}
	if !ready {
		return map[string]interface{}{"ok": false, "error": fmt.Sprintf("%s is not installed, authenticated, and operational on %s", runner, device.Name)}
	}
	preference := map[string]interface{}{
		"deviceId": device.DeviceID,
		"runnerId": runner,
	}
	if model := strings.TrimSpace(args.Model); model != "" {
		preference["model"] = model
	}
	if runner == "opencode" {
		if mode := strings.TrimSpace(args.Mode); mode != "" {
			preference["mode"] = mode
		}
		if provider := strings.TrimSpace(args.Provider); provider != "" {
			preference["provider"] = provider
		}
	} else {
		// A Claude/Codex selection must not inherit stale OpenCode metadata.
		preference["mode"] = nil
		preference["provider"] = nil
	}
	payload := map[string]interface{}{
		"primaryDeviceId":        device.DeviceID,
		"primaryRunnerForDevice": preference,
	}
	body, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(convex, "/")+"/settings", bytes.NewReader(body))
	if err != nil {
		return map[string]interface{}{"ok": false, "error": err.Error()}
	}
	req.Header.Set("Authorization", "Bearer "+cfg.AuthToken)
	req.Header.Set("Content-Type", "application/json")
	resp, err := httpClient.Do(req)
	if err != nil {
		return map[string]interface{}{"ok": false, "error": err.Error()}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		return map[string]interface{}{"ok": false, "error": fmt.Sprintf("save onboarding selection: HTTP %d %s", resp.StatusCode, strings.TrimSpace(string(raw)))}
	}
	userSettingsCacheMu.Lock()
	userSettingsCacheVal = nil
	userSettingsCacheExpiry = time.Time{}
	userSettingsCacheMu.Unlock()
	return map[string]interface{}{
		"ok":                true,
		"primaryDeviceId":   device.DeviceID,
		"primaryDeviceName": device.Name,
		"primaryRunner":     runner,
		"model":             strings.TrimSpace(args.Model),
		"mode":              strings.TrimSpace(args.Mode),
		"provider":          strings.TrimSpace(args.Provider),
		"backend":           "yaver-serverless",
	}
}

// resolvePrimaryDeviceIDForMCP looks up the caller's
// userSettings.primaryDeviceId. If unset, falls back to the only
// registered owner device (single-device users never set a primary
// explicitly — every CLI surface treats their lone box as primary).
// Returns ("", error) when the caller is signed out, has no devices,
// or has multiple devices and no primary chosen.
func resolvePrimaryDeviceIDForMCP() (string, error) {
	cfg, err := LoadConfig()
	if err != nil || cfg == nil || strings.TrimSpace(cfg.AuthToken) == "" {
		return "", fmt.Errorf("not signed in — run 'yaver auth' first")
	}
	convex := strings.TrimSpace(cfg.ConvexSiteURL)
	if convex == "" {
		convex = defaultConvexSiteURL
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	current, err := primaryGetCurrent(ctx, cfg.AuthToken, convex)
	if err != nil {
		return "", fmt.Errorf("read userSettings: %w", err)
	}
	current = strings.TrimSpace(current)
	if current != "" {
		return current, nil
	}
	devices, err := listDevices(convex, cfg.AuthToken)
	if err != nil {
		return "", fmt.Errorf("list devices: %w", err)
	}
	owned := devices
	switch len(owned) {
	case 0:
		return "", fmt.Errorf("no registered owner devices yet — run 'yaver serve' on a machine to register it")
	case 1:
		return owned[0].DeviceID, nil
	default:
		return "", fmt.Errorf("no primary device set and multiple devices registered — run 'yaver primary set <deviceId|name|alias>' or call device_primary_set first")
	}
}

// mcpPrimaryAuth re-auths the user's primary device. With runner=="",
// this is Yaver-level reauth (refreshes the Yaver session token via
// the same /auth/recover path device_reauth_start uses). With
// runner=="claude" or "codex", this kicks off the runner's browser /
// device-code login flow on the primary box; the response carries the
// URL/code the user (or another agent) opens to finish.
func mcpPrimaryAuth(runner string, confirm bool) map[string]interface{} {
	deviceID, err := resolvePrimaryDeviceIDForMCP()
	if err != nil {
		return map[string]interface{}{"ok": false, "error": err.Error()}
	}
	runner = normalizeRunnerAuthName(runner)
	if runner == "" {
		out := mcpDeviceReauthStart(deviceID, "auto", "")
		if out == nil {
			out = map[string]interface{}{}
		}
		out["primaryDeviceId"] = deviceID
		out["scope"] = "yaver"
		return out
	}
	if runner != "claude" && runner != "codex" {
		return map[string]interface{}{
			"ok":              false,
			"error":           fmt.Sprintf("unsupported runner %q — pass claude / claude-code / codex, or omit runner for Yaver-level reauth", runner),
			"primaryDeviceId": deviceID,
		}
	}
	out := mcpRunnerBrowserAuthStart(deviceID, runner, confirm)
	if out == nil {
		out = map[string]interface{}{}
	}
	out["primaryDeviceId"] = deviceID
	out["scope"] = "runner"
	out["runner"] = runner
	return out
}

// mcpPrimaryStatus returns the same merged /info + /agent/runners
// snapshot `yaver primary status` prints, but as a JSON-shaped map so
// MCP callers can branch on lifecycleState / needsAuth / runners
// without parsing a TUI surface.
func mcpPrimaryStatus() map[string]interface{} {
	deviceID, err := resolvePrimaryDeviceIDForMCP()
	if err != nil {
		return map[string]interface{}{"ok": false, "error": err.Error()}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	report, err := fetchRemoteAgentStatusByDeviceID(ctx, deviceID)
	if err != nil {
		return map[string]interface{}{
			"ok":              false,
			"error":           err.Error(),
			"primaryDeviceId": deviceID,
		}
	}
	return map[string]interface{}{
		"ok":              true,
		"primaryDeviceId": deviceID,
		"report":          report,
	}
}

// mcpPrimaryPing is the short reachability + auth check matching
// `yaver primary ping`. Distilled output is enough for an AI agent to
// decide whether to proceed (state==healthy / ready-to-connect) or
// recommend re-auth (state==yaver-auth-expired / bootstrap).
func mcpPrimaryPing() map[string]interface{} {
	deviceID, err := resolvePrimaryDeviceIDForMCP()
	if err != nil {
		return map[string]interface{}{"ok": false, "error": err.Error()}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	report, err := fetchRemoteAgentStatusByDeviceID(ctx, deviceID)
	if err != nil {
		var target *DeviceInfo
		if cfg, lerr := LoadConfig(); lerr == nil && cfg != nil {
			if devices, derr := listDevices(cfg.ConvexSiteURL, cfg.AuthToken); derr == nil {
				for i := range devices {
					if devices[i].DeviceID == deviceID {
						target = &devices[i]
						break
					}
				}
			}
		}
		cause, hint := classifyRemoteStatusError(err, target)
		return map[string]interface{}{
			"ok":              false,
			"reachable":       false,
			"primaryDeviceId": deviceID,
			"cause":           cause,
			"hint":            hint,
			"error":           err.Error(),
		}
	}
	out := map[string]interface{}{
		"ok":              true,
		"reachable":       report.HTTPStatusInfo > 0,
		"primaryDeviceId": deviceID,
		"name":            report.Name,
		"transport":       report.Transport,
		"baseUrl":         report.BaseURL,
		"agentVersion":    report.Version,
		"lifecycleState":  report.LifecycleState,
		"needsAuth":       report.NeedsAuth,
		"isOnline":        report.IsOnline,
	}
	if report.Info != nil {
		if email, ok := report.Info["ownerEmail"].(string); ok && strings.TrimSpace(email) != "" {
			out["ownerEmail"] = email
		}
		if owner, ok := report.Info["ownerUserId"].(string); ok && strings.TrimSpace(owner) != "" {
			if cfg, lerr := LoadConfig(); lerr == nil {
				if me := callerUserID(cfg); me != "" {
					out["ownerIsCaller"] = (owner == me)
				}
			}
		}
	}
	return out
}

// mcpPrimaryProjects fetches the agent's /projects (or /projects/mobile
// when mobileOnly is true) over whichever transport works. Same data
// `yaver primary projects` / `yaver primary mobiles` print, but as
// JSON. Discovery works without any coding-agent installed on the box.
func mcpPrimaryProjects(mobileOnly bool) map[string]interface{} {
	deviceID, err := resolvePrimaryDeviceIDForMCP()
	if err != nil {
		return map[string]interface{}{"ok": false, "error": err.Error()}
	}
	cfg, err := LoadConfig()
	if err != nil || cfg == nil || strings.TrimSpace(cfg.AuthToken) == "" {
		return map[string]interface{}{
			"ok":              false,
			"error":           "not signed in — run 'yaver auth' first",
			"primaryDeviceId": deviceID,
		}
	}
	if strings.TrimSpace(cfg.ConvexSiteURL) == "" {
		cfg.ConvexSiteURL = defaultConvexSiteURL
	}
	devices, err := listDevices(cfg.ConvexSiteURL, cfg.AuthToken)
	if err != nil {
		return map[string]interface{}{
			"ok":              false,
			"error":           fmt.Sprintf("list devices: %v", err),
			"primaryDeviceId": deviceID,
		}
	}
	var target *DeviceInfo
	for i := range devices {
		if devices[i].DeviceID == deviceID {
			target = &devices[i]
			break
		}
	}
	if target == nil {
		return map[string]interface{}{
			"ok":              false,
			"error":           "primary device is no longer in your registered devices — run 'yaver primary clear' to reset",
			"primaryDeviceId": deviceID,
		}
	}
	if !target.IsOnline {
		return map[string]interface{}{
			"ok":              false,
			"error":           "primary device has no recent heartbeat — try `yaver primary status` or re-auth via primary_auth",
			"primaryDeviceId": deviceID,
			"isOnline":        false,
		}
	}
	candidates, err := buildRemoteAgentCandidates(cfg, target)
	if err != nil {
		return map[string]interface{}{
			"ok":              false,
			"error":           err.Error(),
			"primaryDeviceId": deviceID,
		}
	}
	if len(candidates) == 0 {
		return map[string]interface{}{
			"ok":              false,
			"error":           "primary device has no reachable transport candidates",
			"primaryDeviceId": deviceID,
		}
	}
	path := "/projects"
	if mobileOnly {
		path = "/projects/mobile"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	raw, status, chosenURL, ferr := primaryFetchWithFallthrough(ctx, candidates, cfg.AuthToken, path, 20*time.Second)
	if ferr != nil {
		return map[string]interface{}{
			"ok":              false,
			"error":           ferr.Error(),
			"primaryDeviceId": deviceID,
			"httpStatus":      status,
		}
	}
	if status < 200 || status >= 300 {
		return map[string]interface{}{
			"ok":              false,
			"error":           fmt.Sprintf("%s returned HTTP %d: %s", chosenURL, status, strings.TrimSpace(string(raw))),
			"primaryDeviceId": deviceID,
			"httpStatus":      status,
		}
	}
	return map[string]interface{}{
		"ok":              true,
		"primaryDeviceId": deviceID,
		"name":            target.Name,
		"scope":           map[bool]string{true: "mobile-capable", false: "all"}[mobileOnly],
		"baseUrl":         chosenURL,
		"raw":             rawJSONBlob(raw),
	}
}

// rawJSONBlob marshals the agent's /projects response into a value the
// outer JSON encoder will pass through verbatim. We could decode +
// re-encode, but the agent already validates the shape and the MCP
// client wants the original keys (scannedAt, scanning, projects[]).
func rawJSONBlob(raw []byte) interface{} {
	if len(raw) == 0 {
		return nil
	}
	// json.RawMessage round-trips correctly through encoding/json so the
	// caller sees the agent's exact payload (not a re-flattened map).
	return jsonRaw(raw)
}

// jsonRaw is a tiny alias so we can keep the import surface in this
// file minimal. Equivalent to json.RawMessage.
type jsonRaw []byte

func (j jsonRaw) MarshalJSON() ([]byte, error) {
	if len(j) == 0 {
		return []byte("null"), nil
	}
	return j, nil
}
