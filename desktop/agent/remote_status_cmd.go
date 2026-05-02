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
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	renderRemoteAgentStatus(report, asJSON)
}
