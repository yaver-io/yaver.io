package main

// remote_status_cmd.go — `yaver primary status` and `yaver runner <hint>
// status`. Both share one read path: resolve the target device, fetch
// /info + /agent/runners over the same direct-or-relay transport stack
// the rest of the CLI uses, then pretty-print to stdout. JSON is
// available for scripting via --json.
//
// `yaver primary status`     — runs against userSettings.primaryDeviceId
//                              (Convex /settings).
// `yaver runner <hint> status` — runs against any device the caller can
//                              reach by deviceId/alias/name prefix.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"
)

type remoteRunnerSummary struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Installed      bool   `json:"installed"`
	Ready          bool   `json:"ready"`
	AuthConfigured bool   `json:"authConfigured,omitempty"`
	AuthSource     string `json:"authSource,omitempty"`
	Warning        string `json:"warning,omitempty"`
	Error          string `json:"error,omitempty"`
	IsDefault      bool   `json:"isDefault"`
}

// remoteAgentStatusReport is the merged shape we render at the CLI. We
// intentionally keep it small — anything the user wants raw is a
// `--json` flag away from the underlying /info + /agent/runners
// payloads.
type remoteAgentStatusReport struct {
	DeviceID         string                 `json:"deviceId"`
	Name             string                 `json:"name"`
	Alias            string                 `json:"alias,omitempty"`
	Platform         string                 `json:"platform,omitempty"`
	Hostname         string                 `json:"hostname,omitempty"`
	Version          string                 `json:"version,omitempty"`
	WorkDir          string                 `json:"workDir,omitempty"`
	LifecycleState   string                 `json:"lifecycleState,omitempty"`
	NeedsAuth        bool                   `json:"needsAuth,omitempty"`
	IsOnline         bool                   `json:"isOnline"`
	Transport        string                 `json:"transport,omitempty"`
	BaseURL          string                 `json:"baseUrl,omitempty"`
	DefaultRunner    string                 `json:"defaultRunner,omitempty"`
	Runners          []remoteRunnerSummary  `json:"runners,omitempty"`
	DevServer        map[string]interface{} `json:"devServer,omitempty"`
	Sandbox          map[string]interface{} `json:"sandbox,omitempty"`
	Project          map[string]interface{} `json:"project,omitempty"`
	TaskStats        map[string]interface{} `json:"taskStats,omitempty"`
	TodoCount        interface{}            `json:"todoCount,omitempty"`
	TodoTotal        interface{}            `json:"todoTotal,omitempty"`
	Info             map[string]interface{} `json:"info,omitempty"`
	HTTPStatusInfo   int                    `json:"httpStatusInfo,omitempty"`
	HTTPStatusRunner int                    `json:"httpStatusRunners,omitempty"`
}

func fetchRemoteAgentStatusByHint(ctx context.Context, deviceHint string) (*remoteAgentStatusReport, error) {
	candidates, token, err := resolveRemoteAgentCandidates(deviceHint)
	if err != nil {
		return nil, err
	}
	cfg, err := LoadConfig()
	if err != nil {
		return nil, err
	}
	devices, derr := listDevices(cfg.ConvexSiteURL, cfg.AuthToken)
	if derr != nil {
		return nil, fmt.Errorf("list devices: %w", derr)
	}
	hint := normalizeDeviceHint(deviceHint)
	var target *DeviceInfo
	for i := range devices {
		d := &devices[i]
		if strings.HasPrefix(d.DeviceID, hint) ||
			strings.EqualFold(d.Name, hint) ||
			strings.HasPrefix(strings.ToLower(d.Name), strings.ToLower(hint)) ||
			(strings.TrimSpace(d.Alias) != "" && strings.EqualFold(d.Alias, hint)) ||
			(strings.TrimSpace(d.Alias) != "" && strings.HasPrefix(strings.ToLower(d.Alias), strings.ToLower(hint))) {
			target = d
			break
		}
	}
	if target == nil {
		return nil, fmt.Errorf("device %q not found", deviceHint)
	}
	return fetchRemoteAgentStatus(ctx, candidates, token, target)
}

func fetchRemoteAgentStatusByDeviceID(ctx context.Context, deviceID string) (*remoteAgentStatusReport, error) {
	cfg, err := LoadConfig()
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(cfg.AuthToken) == "" {
		return nil, fmt.Errorf("not signed in — run 'yaver auth' first")
	}
	if strings.TrimSpace(cfg.ConvexSiteURL) == "" {
		cfg.ConvexSiteURL = defaultConvexSiteURL
	}
	devices, err := listDevices(cfg.ConvexSiteURL, cfg.AuthToken)
	if err != nil {
		return nil, fmt.Errorf("list devices: %w", err)
	}
	var target *DeviceInfo
	for i := range devices {
		d := &devices[i]
		if d.DeviceID == deviceID {
			target = d
			break
		}
	}
	if target == nil {
		return nil, fmt.Errorf("primary device %q is no longer in your registered devices — run 'yaver primary clear' to reset", deviceID)
	}
	if !target.IsOnline {
		report := &remoteAgentStatusReport{
			DeviceID: target.DeviceID,
			Name:     target.Name,
			Alias:    strings.TrimSpace(target.Alias),
			Platform: target.Platform,
			IsOnline: false,
		}
		return report, nil
	}
	candidates, err := buildRemoteAgentCandidates(cfg, target)
	if err != nil {
		return nil, err
	}
	if len(candidates) == 0 {
		return nil, fmt.Errorf("device %q has no reachable transport candidates", target.Name)
	}
	return fetchRemoteAgentStatus(ctx, candidates, cfg.AuthToken, target)
}

// fetchRemoteAgentStatus pulls /info + /agent/runners from the chosen
// transport, distilling the result into a small render-friendly struct.
// Failures of /agent/runners are non-fatal — the caller still sees the
// device version, lifecycle, and dev-server state from /info.
func fetchRemoteAgentStatus(ctx context.Context, candidates []RemoteAgentCandidate, token string, target *DeviceInfo) (*remoteAgentStatusReport, error) {
	report := &remoteAgentStatusReport{
		DeviceID: target.DeviceID,
		Name:     target.Name,
		Alias:    strings.TrimSpace(target.Alias),
		Platform: target.Platform,
		IsOnline: target.IsOnline,
	}
	infoCtx, infoCancel := context.WithTimeout(ctx, 10*time.Second)
	chosen, status, raw, err := doRemoteAgentRequest(infoCtx, candidates, token, http.MethodGet, "/info", nil, 8*time.Second)
	infoCancel()
	if err != nil {
		return nil, fmt.Errorf("/info: %w", err)
	}
	report.HTTPStatusInfo = status
	report.Transport = firstNonEmpty(strings.TrimSpace(chosen.Label), chosen.Kind)
	report.BaseURL = chosen.BaseURL
	// Auth-expired detection fallback: /info is auth'd, so when the
	// remote box's own Convex token is dead it cannot validate caller
	// tokens and returns 401/403 — the very signal we need is absent
	// from the response. /health is unauth and returns authExpired
	// even in this state, so probe it as a side-channel.
	if status == 401 || status == 403 {
		healthCtx, healthCancel := context.WithTimeout(ctx, 5*time.Second)
		_, hstatus, hraw, herr := doRemoteAgentRequest(healthCtx, candidates, "", http.MethodGet, "/health", nil, 4*time.Second)
		healthCancel()
		if herr == nil && hstatus >= 200 && hstatus < 300 && len(hraw) > 0 {
			var hinfo map[string]interface{}
			if json.Unmarshal(hraw, &hinfo) == nil {
				if exp, ok := hinfo["authExpired"].(bool); ok && exp {
					report.NeedsAuth = true
				}
				if v, ok := hinfo["lifecycleState"].(string); ok {
					report.LifecycleState = v
				}
				if v, ok := hinfo["version"].(string); ok && report.Version == "" {
					report.Version = v
				}
				if v, ok := hinfo["hostname"].(string); ok && report.Hostname == "" {
					report.Hostname = v
				}
			}
		}
	}
	if status >= 200 && status < 300 && len(raw) > 0 {
		var info map[string]interface{}
		if err := json.Unmarshal(raw, &info); err == nil {
			report.Info = info
			if v, ok := info["hostname"].(string); ok {
				report.Hostname = v
			}
			if v, ok := info["version"].(string); ok {
				report.Version = v
			}
			if v, ok := info["workDir"].(string); ok {
				report.WorkDir = v
			}
			if v, ok := info["lifecycleState"].(string); ok {
				report.LifecycleState = v
			}
			if needs, ok := info["needsAuth"].(bool); ok && needs {
				report.NeedsAuth = true
			}
			if authExpired, ok := info["authExpired"].(bool); ok && authExpired {
				report.NeedsAuth = true
			}
			if r, ok := info["runner"].(map[string]interface{}); ok {
				if id, ok := r["id"].(string); ok {
					report.DefaultRunner = id
				}
			}
			if dev, ok := info["devServer"].(map[string]interface{}); ok {
				report.DevServer = dev
			}
			if sb, ok := info["sandbox"].(map[string]interface{}); ok {
				report.Sandbox = sb
			}
			if proj, ok := info["project"].(map[string]interface{}); ok {
				report.Project = proj
			}
			if ts, ok := info["taskStats"].(map[string]interface{}); ok {
				report.TaskStats = ts
			}
			if v, ok := info["todoCount"]; ok {
				report.TodoCount = v
			}
			if v, ok := info["todoTotal"]; ok {
				report.TodoTotal = v
			}
		}
	}

	runnerCtx, runnerCancel := context.WithTimeout(ctx, 10*time.Second)
	_, rstatus, rraw, rerr := doRemoteAgentRequest(runnerCtx, candidates, token, http.MethodGet, "/agent/runners", nil, 8*time.Second)
	runnerCancel()
	report.HTTPStatusRunner = rstatus
	if rerr == nil && rstatus >= 200 && rstatus < 300 && len(rraw) > 0 {
		var resp struct {
			Runners []remoteRunnerSummary `json:"runners"`
		}
		if err := json.Unmarshal(rraw, &resp); err == nil {
			sort.Slice(resp.Runners, func(i, j int) bool {
				if resp.Runners[i].IsDefault != resp.Runners[j].IsDefault {
					return resp.Runners[i].IsDefault
				}
				return resp.Runners[i].ID < resp.Runners[j].ID
			})
			report.Runners = resp.Runners
		}
	}
	return report, nil
}

func renderRemoteAgentStatus(report *remoteAgentStatusReport, asJSON bool) {
	if asJSON {
		out, _ := json.MarshalIndent(report, "", "  ")
		fmt.Println(string(out))
		return
	}
	if report == nil {
		fmt.Println("(no status — no report returned)")
		return
	}
	header := report.Name
	if report.Alias != "" {
		header += " (@" + report.Alias + ")"
	}
	if dID := strings.TrimSpace(report.DeviceID); dID != "" {
		header += " [" + dID[:min(8, len(dID))] + "]"
	}
	fmt.Println(header)
	if !report.IsOnline {
		fmt.Println("  status: offline")
		return
	}
	if report.Hostname != "" || report.Platform != "" {
		platformLabel := report.Platform
		if report.Hostname != "" && report.Hostname != report.Name {
			platformLabel = strings.TrimSpace(report.Hostname + " · " + report.Platform)
		} else if report.Hostname != "" {
			platformLabel = report.Hostname
		}
		fmt.Printf("  host:           %s\n", strings.TrimSpace(platformLabel))
	}
	if report.Version != "" {
		fmt.Printf("  agent version:  %s\n", report.Version)
	}
	if report.LifecycleState != "" {
		marker := report.LifecycleState
		if report.NeedsAuth {
			marker += " (needs reauth)"
		}
		fmt.Printf("  lifecycle:      %s\n", marker)
	}
	if report.WorkDir != "" {
		fmt.Printf("  workdir:        %s\n", report.WorkDir)
	}
	if report.Transport != "" {
		fmt.Printf("  transport:      %s\n", report.Transport)
	}
	// Multi-layer auth summary: yaver-level (Convex token alive on the
	// remote box) + per-runner (claude-code, codex, etc. logged in on
	// that box). Pre-fix this was scattered across the lifecycle line +
	// the runners table; surfacing it as one block makes the "is this
	// machine ready to receive work" question answerable at a glance.
	renderAuthSummary(report)
	if report.DefaultRunner != "" {
		fmt.Printf("  default runner: %s\n", report.DefaultRunner)
	}
	if report.Project != nil {
		if name, _ := report.Project["name"].(string); strings.TrimSpace(name) != "" {
			path, _ := report.Project["path"].(string)
			line := name
			if strings.TrimSpace(path) != "" && path != name {
				line += " (" + path + ")"
			}
			fmt.Printf("  project:        %s\n", line)
		}
	}
	if report.DevServer != nil {
		fw, _ := report.DevServer["framework"].(string)
		running, _ := report.DevServer["running"].(bool)
		port, _ := report.DevServer["port"].(float64)
		state := "stopped"
		if running {
			state = "running"
		}
		line := fmt.Sprintf("%s — %s", strings.TrimSpace(fw), state)
		if port > 0 {
			line += fmt.Sprintf(" (port %d)", int(port))
		}
		fmt.Printf("  dev server:     %s\n", line)
	}
	if report.TaskStats != nil {
		if total, ok := report.TaskStats["total"].(float64); ok {
			running := numFromAny(report.TaskStats["running"])
			done := numFromAny(report.TaskStats["done"])
			failed := numFromAny(report.TaskStats["failed"])
			fmt.Printf("  tasks:          total=%d running=%d done=%d failed=%d\n",
				int(total), running, done, failed)
		}
	}
	if report.TodoTotal != nil {
		fmt.Printf("  todo:           pending=%v total=%v\n", report.TodoCount, report.TodoTotal)
	}
	if len(report.Runners) > 0 {
		fmt.Println("  runners:")
		fmt.Printf("    %-12s %-9s %-9s %-6s %s\n", "RUNNER", "INSTALLED", "READY", "AUTH", "NOTES")
		for _, r := range report.Runners {
			notes := ""
			if r.Warning != "" {
				notes = r.Warning
			} else if r.Error != "" {
				notes = r.Error
			}
			star := " "
			if r.IsDefault {
				star = "★"
			}
			fmt.Printf("    %s %-10s %-9s %-9s %-6s %s\n",
				star,
				runnerTrunc(r.ID, 10),
				yesNo(r.Installed),
				yesNo(r.Ready),
				yesNo(r.AuthConfigured),
				notes,
			)
		}
	} else if report.HTTPStatusRunner != 0 {
		fmt.Printf("  runners:        (could not fetch — HTTP %d on /agent/runners)\n", report.HTTPStatusRunner)
	}
}

// renderAuthSummary prints a "auth:" block summarising the auth state
// of the remote box across all relevant layers:
//
//   - yaver  — is the box's own Convex session token still alive?
//     Comes from /info.authExpired (and lifecycleState).
//   - claude — is claude-code installed AND logged in?  From
//     /agent/runners[id=claude-code].AuthConfigured.
//   - codex  — same shape as claude.
//
// When /agent/runners returns 403 (typical when the remote box's
// yaver auth is expired — it can't validate caller tokens), we fall
// back to "(unknown — fix yaver auth first)" rather than silently
// hiding the layers.
func renderAuthSummary(report *remoteAgentStatusReport) {
	// Yaver-level auth.
	yaverState := "✓ active"
	if report.NeedsAuth || report.LifecycleState == "yaver-auth-expired" {
		yaverState = "✗ expired (run: yaver primary auth)"
	}
	fmt.Printf("  auth:\n")
	fmt.Printf("    yaver:        %s\n", yaverState)

	runnersByID := map[string]*remoteRunnerSummary{}
	for i := range report.Runners {
		runnersByID[strings.ToLower(report.Runners[i].ID)] = &report.Runners[i]
	}
	// We surface the two coding runners that have first-class quick
	// flows (`yaver primary auth claude` / `yaver primary auth codex`).
	// Other runners (aider / opencode / ollama) are visible in the
	// detailed "runners:" table below.
	for _, ridLabel := range [][2]string{{"claude-code", "claude"}, {"codex", "codex"}} {
		runner := runnersByID[ridLabel[0]]
		label := ridLabel[1]
		state := authStateStringForRunner(runner, report)
		fmt.Printf("    %-13s %s\n", label+":", state)
	}
}

// authStateStringForRunner translates a single runner's installed/auth
// shape into a one-line state string.
func authStateStringForRunner(runner *remoteRunnerSummary, report *remoteAgentStatusReport) string {
	if runner == nil {
		// /agent/runners didn't return data. Distinguish "auth-expired
		// blocked the call" from "runner genuinely not present" using
		// the HTTP status — 403/401 → permissions, anything else →
		// likely missing.
		if report.HTTPStatusRunner == 401 || report.HTTPStatusRunner == 403 {
			return "(unknown — fix yaver auth first)"
		}
		if report.HTTPStatusRunner == 0 {
			return "(unknown — runners endpoint unreachable)"
		}
		return "✗ not installed"
	}
	if !runner.Installed {
		return "✗ not installed"
	}
	if runner.AuthConfigured {
		src := strings.TrimSpace(runner.AuthSource)
		if src != "" {
			return "✓ active (" + src + ")"
		}
		return "✓ active"
	}
	suffix := ""
	switch strings.ToLower(runner.ID) {
	case "claude-code":
		suffix = " (run: yaver primary auth claude)"
	case "codex":
		suffix = " (run: yaver primary auth codex)"
	}
	return "✗ not configured" + suffix
}

func numFromAny(v interface{}) int {
	switch x := v.(type) {
	case float64:
		return int(x)
	case int:
		return x
	case int64:
		return int(x)
	}
	return 0
}

// runRemoteAgentStatusByHint is the entry point for `yaver runner
// <hint> status`. It resolves the target by alias/deviceId/name and
// renders the status report.
func runRemoteAgentStatusByHint(deviceHint string, asJSON bool) {
	if strings.TrimSpace(deviceHint) == "" {
		fmt.Fprintln(os.Stderr, "Usage: yaver runner <deviceId|name|alias> status [--json]")
		os.Exit(1)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	report, err := fetchRemoteAgentStatusByHint(ctx, deviceHint)
	if err != nil {
		// Resolve the deviceID for the friendly renderer; if we can't,
		// fall through to the raw error.
		cfg, _ := LoadConfig()
		if cfg != nil {
			if devices, derr := listDevices(cfg.ConvexSiteURL, cfg.AuthToken); derr == nil {
				hint := normalizeDeviceHint(deviceHint)
				for i := range devices {
					d := &devices[i]
					if strings.HasPrefix(d.DeviceID, hint) ||
						strings.EqualFold(d.Name, hint) ||
						strings.HasPrefix(strings.ToLower(d.Name), strings.ToLower(hint)) ||
						(strings.TrimSpace(d.Alias) != "" && strings.EqualFold(d.Alias, hint)) {
						renderRemoteAgentStatusError(ctx, d.DeviceID, err, asJSON)
						os.Exit(1)
					}
				}
			}
		}
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	renderRemoteAgentStatus(report, asJSON)
}

// runPrimaryStatus is the entry point for `yaver primary status`.
func runPrimaryStatus(ctx context.Context, asJSON bool) {
	token, convex, err := primaryLoadAuth()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	current, err := primaryGetCurrent(ctx, token, convex)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to read userSettings: %v\n", err)
		os.Exit(1)
	}
	current = strings.TrimSpace(current)
	if current == "" {
		// Single-device users have no primary set but the CLI should
		// still answer — fall back to the only registered owner device.
		devices, listErr := listDevices(convex, token)
		if listErr != nil {
			fmt.Fprintf(os.Stderr, "No primary device set and listing devices failed: %v\n", listErr)
			os.Exit(1)
		}
		var owned []DeviceInfo
		for _, d := range devices {
			if !d.IsGuest {
				owned = append(owned, d)
			}
		}
		if len(owned) == 0 {
			fmt.Fprintln(os.Stderr, "No registered owner devices yet. Run `yaver serve` on a machine to register it.")
			os.Exit(1)
		}
		if len(owned) > 1 {
			fmt.Fprintln(os.Stderr, "No primary device set and you have multiple registered devices.")
			fmt.Fprintln(os.Stderr, "Pick one with `yaver primary set <deviceId|name|alias>` and try again, or run")
			fmt.Fprintln(os.Stderr, "`yaver runner <hint> status` for a specific device.")
			os.Exit(1)
		}
		current = owned[0].DeviceID
	}
	report, err := fetchRemoteAgentStatusByDeviceID(ctx, current)
	if err != nil {
		renderRemoteAgentStatusError(ctx, current, err, asJSON)
		os.Exit(1)
	}
	renderRemoteAgentStatus(report, asJSON)
}

// renderRemoteAgentStatusError prints a clean "primary unreachable" summary
// when fetchRemoteAgentStatus failed across every transport candidate. The
// raw error from doRemoteAgentRequest is a `|`-joined wall of attempt URLs +
// reasons (Docker bridge IPs the box reported as local IPs, relay 502 because
// the box's tunnel is down, etc.) — useful for debugging but unreadable as
// the default output. We replace it with: device label + a one-line cause
// classification + the most likely recovery command. Falls back to the raw
// error for --json or when device metadata can't be loaded.
func renderRemoteAgentStatusError(ctx context.Context, deviceID string, err error, asJSON bool) {
	if asJSON {
		jsonErr := map[string]interface{}{
			"deviceId": deviceID,
			"error":    err.Error(),
		}
		out, _ := json.MarshalIndent(jsonErr, "", "  ")
		fmt.Fprintln(os.Stderr, string(out))
		return
	}
	cfg, _ := LoadConfig()
	var target *DeviceInfo
	if cfg != nil && strings.TrimSpace(cfg.AuthToken) != "" {
		if devices, derr := listDevices(cfg.ConvexSiteURL, cfg.AuthToken); derr == nil {
			for i := range devices {
				if devices[i].DeviceID == deviceID {
					target = &devices[i]
					break
				}
			}
		}
	}
	label := deviceID
	if len(label) > 8 {
		label = label[:8]
	}
	hostLabel := ""
	if target != nil {
		alias := strings.TrimSpace(target.Alias)
		if alias != "" {
			label = fmt.Sprintf("%s (@%s) [%s]", target.Name, alias, label)
		} else {
			label = fmt.Sprintf("%s [%s]", target.Name, label)
		}
		hostLabel = strings.TrimSpace(target.Platform)
	}
	cause, hint := classifyRemoteStatusError(err, target)
	fmt.Println(label)
	if hostLabel != "" {
		fmt.Printf("  host:           %s\n", hostLabel)
	}
	if target != nil && !target.IsOnline {
		fmt.Println("  status:         offline (no recent heartbeat)")
	} else {
		fmt.Println("  status:         online per Convex but unreachable from here")
	}
	fmt.Printf("  cause:          %s\n", cause)
	if hint != "" {
		fmt.Printf("  next step:      %s\n", hint)
	}
}

// classifyRemoteStatusError boils a verbose multi-candidate transport failure
// down to one short cause + a recovery hint. The match patterns are intentionally
// loose — false positives just mean the user gets the wrong hint, not a hidden
// error (the raw error is still in --json output).
func classifyRemoteStatusError(err error, target *DeviceInfo) (cause, hint string) {
	msg := strings.ToLower(err.Error())
	switch {
	case target != nil && !target.IsOnline:
		return "device offline (last heartbeat too old)", "ssh into the box and run `yaver serve`, or check `systemctl --user status yaver`"
	case strings.Contains(msg, "device not connected to relay"):
		return "relay tunnel down (typically Yaver auth expired on the box)", "run `yaver primary auth` to re-sign in on the primary device"
	case strings.Contains(msg, "i/o timeout") && strings.Contains(msg, "172.") || strings.Contains(msg, "i/o timeout") && strings.Contains(msg, "10."):
		return "no LAN path + relay tunnel unavailable", "run `yaver primary auth` (re-establishes the relay tunnel) or join the same LAN as the primary"
	case strings.Contains(msg, "i/o timeout"):
		return "every transport timed out", "run `yaver primary auth` to refresh the relay tunnel; if you're on cellular, the LAN candidates were never going to work anyway"
	case strings.Contains(msg, "connection refused"):
		return "agent process not listening on its HTTP port", "ssh in and check `pgrep -af 'yaver serve'` and `journalctl --user -u yaver -n 50`"
	case strings.Contains(msg, "401") || strings.Contains(msg, "403"):
		return "agent rejected our auth token", "your local CLI's session may be stale — run `yaver auth` here, or `yaver primary auth` if the box is the one with bad auth"
	default:
		return "every transport candidate failed", "see `yaver primary status --json` for the raw error list"
	}
}
