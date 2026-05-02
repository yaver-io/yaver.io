package main

// primary_cmd.go — `yaver primary` CLI subcommand.
//
// Convex stores a per-user `userSettings.primaryDeviceId` that mobile,
// web, and (eventually) the desktop app use as the auto-connect target
// when the user has more than one machine registered. This CLI gives
// the user a terminal-side knob for the same preference so they can
// script it or set it without opening the phone.
//
// Shape:
//
//   yaver primary               # print current primary + device list
//   yaver primary show          # alias for bare invocation
//   yaver primary set <devId>   # mark a device primary (partial match OK)
//   yaver primary clear         # unset the preference
//
// All commands read ~/.yaver/config.json for the auth token. Partial
// match on `set` accepts any unique prefix of deviceId OR the exact
// device name — same ergonomics as `yaver guests remove <email>`.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
)

func runPrimary(args []string) {
	ctx := context.Background()
	if len(args) == 0 {
		runPrimaryShow(ctx)
		return
	}
	// Reserved verbs come first so a future runner whose name collides
	// with a verb (e.g. "auth") never silently re-routes to the runner
	// quick flow.
	switch strings.ToLower(strings.TrimSpace(args[0])) {
	case "status":
		runPrimaryStatus(ctx, primaryHasFlag(args[1:], "--json"))
		return
	case "auth":
		runPrimaryAuth(ctx, args[1:])
		return
	}
	if runner := normalizePrimaryRunnerQuickArg(args[0]); runner != "" {
		runPrimaryRunnerQuickFlow(ctx, runner, args[1:])
		return
	}
	switch args[0] {
	case "show", "get", "list", "ls":
		runPrimaryShow(ctx)
	case "set":
		runPrimarySet(ctx, args[1:])
	case "clear", "unset", "remove":
		runPrimaryClear(ctx)
	case "help", "-h", "--help":
		primaryUsage()
	default:
		fmt.Fprintf(os.Stderr, "Unknown subcommand: yaver primary %s\n\n", args[0])
		primaryUsage()
		os.Exit(1)
	}
}

func primaryHasFlag(args []string, name string) bool {
	for _, a := range args {
		if a == name {
			return true
		}
	}
	return false
}

// runPrimaryAuth implements `yaver primary auth` (Yaver-level headless
// auth on the primary device) and `yaver primary auth <runner>` (the
// runner-auth setup flow on the primary device — same path as
// `yaver primary <runner>`, just spelled out).
func runPrimaryAuth(ctx context.Context, args []string) {
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
		fmt.Fprintln(os.Stderr, "No primary device set. Run `yaver primary set <deviceId>` first.")
		os.Exit(1)
	}
	if len(args) == 0 {
		// Pure Yaver-level headless auth. Reuses the same SSH-piped
		// `yaver auth --headless` path as the runner quick flow's
		// reauth recovery branch.
		if err := runRemoteHeadlessYaverAuthOverSSH(current); err != nil {
			fmt.Fprintf(os.Stderr, "primary auth: %v\n", err)
			os.Exit(1)
		}
		return
	}
	runner := normalizeRunnerAuthName(args[0])
	if runner != "claude" && runner != "codex" {
		fmt.Fprintf(os.Stderr, "primary auth: unsupported runner %q. Use claude / claude-code / codex.\n", args[0])
		os.Exit(1)
	}
	runRunnerQuickFlow(current, runner, args[1:])
}

func primaryUsage() {
	fmt.Print(`yaver primary — manage + inspect the auto-connect preferred device

Usage:
  yaver primary                   Show current primary + all devices
  yaver primary status [--json]   Live status of the primary device
                                  (agent version, lifecycle, runners,
                                  dev-server) over the existing direct/
                                  relay transport stack
  yaver primary auth              Run remote 'yaver auth --headless' on
                                  the primary device (Yaver-level auth)
  yaver primary auth <claude|claude-code|codex>
                                  Run the runner sanity/auth flow on the
                                  primary device for the named coding agent
  yaver primary <claude|claude-code|codex>
                                  Same as 'auth <runner>' — kept as a
                                  shortcut so existing scripts still work
  yaver primary set [deviceId|name|alias|self]
                                  Mark a device as primary. With NO arg (or
                                  'self' / 'me' / 'local' / '.') marks THIS
                                  machine as primary. Otherwise resolves a
                                  partial deviceId / name / alias prefix.
  yaver primary clear             Unset the preference (multi-device users
                                  will have to pick manually again)

Single-device users auto-connect regardless of this setting.
`)
}

func normalizePrimaryRunnerQuickArg(arg string) string {
	runner := normalizeRunnerAuthName(arg)
	switch runner {
	case "claude", "codex":
		return runner
	default:
		return ""
	}
}

type primaryDevice struct {
	DeviceID        string   `json:"deviceId"`
	Name            string   `json:"name"`
	Platform        string   `json:"platform"`
	QuicHost        string   `json:"quicHost"`
	LocalIps        []string `json:"localIps,omitempty"`
	PublicEndpoints []string `json:"publicEndpoints,omitempty"`
	IsOnline        bool     `json:"isOnline"`
	IsGuest         bool     `json:"isGuest"`
	LastHeartbeat   int64    `json:"lastHeartbeat"`
}

func primaryListDevices(ctx context.Context, token, convexURL string) ([]primaryDevice, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", convexURL+"/devices/list", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("devices/list failed: %d %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var parsed struct {
		Devices []primaryDevice `json:"devices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, err
	}
	return parsed.Devices, nil
}

func primaryGetCurrent(ctx context.Context, token, convexURL string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", convexURL+"/settings", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("settings: %d", resp.StatusCode)
	}
	var parsed struct {
		Settings struct {
			PrimaryDeviceID string `json:"primaryDeviceId"`
		} `json:"settings"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return "", err
	}
	return parsed.Settings.PrimaryDeviceID, nil
}

// primarySaveRaw posts the primaryDeviceId field to /settings. Pass an empty
// string + clear=true to null-out the preference. Convex's setByToken treats
// null as "clear" and undefined as "leave untouched"; the explicit `clear`
// flag controls which one we send.
func primarySaveRaw(ctx context.Context, token, convexURL, deviceID string, clear bool) error {
	payload := map[string]interface{}{}
	if clear {
		payload["primaryDeviceId"] = nil
	} else {
		payload["primaryDeviceId"] = deviceID
	}
	body, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, "POST", convexURL+"/settings", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		out, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("/settings POST failed: %d %s", resp.StatusCode, strings.TrimSpace(string(out)))
	}
	return nil
}

func primaryLoadAuth() (token, convex string, err error) {
	cfg, loadErr := LoadConfig()
	if loadErr != nil || cfg == nil || strings.TrimSpace(cfg.AuthToken) == "" {
		return "", "", fmt.Errorf("not signed in — run 'yaver auth' first")
	}
	convex = cfg.ConvexSiteURL
	if convex == "" {
		convex = defaultConvexSiteURL
	}
	return cfg.AuthToken, convex, nil
}

func runPrimaryShow(ctx context.Context) {
	token, convex, err := primaryLoadAuth()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	current, err := primaryGetCurrent(ctx, token, convex)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to read settings: %v\n", err)
		os.Exit(1)
	}
	devices, err := primaryListDevices(ctx, token, convex)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to list devices: %v\n", err)
		os.Exit(1)
	}
	if current == "" {
		fmt.Println("Primary device: (none set)")
	} else {
		name := ""
		for _, d := range devices {
			if d.DeviceID == current {
				name = d.Name
				break
			}
		}
		if name == "" {
			fmt.Printf("Primary device: %s (record missing — run 'yaver primary clear' to reset)\n", current)
		} else {
			fmt.Printf("Primary device: %s (%s)\n", name, current[:min(8, len(current))])
		}
	}
	if len(devices) == 0 {
		fmt.Println("\nNo devices registered yet. Run 'yaver serve' on a machine to register it.")
		return
	}
	fmt.Println("\nAll registered devices:")
	for _, d := range devices {
		marker := "  "
		if d.DeviceID == current {
			marker = "★ "
		}
		status := "offline"
		if d.IsOnline {
			status = "online"
		}
		shared := ""
		if d.IsGuest {
			shared = " (shared)"
		}
		fmt.Printf("%s%s — %s — %s%s — %s\n", marker, d.DeviceID[:min(8, len(d.DeviceID))], d.Name, status, shared, d.Platform)
	}
}

func runPrimarySet(ctx context.Context, args []string) {
	target := ""
	if len(args) > 0 {
		target = strings.TrimSpace(args[0])
	}
	// No arg, or explicit "self" / "me" / "local" / "." → mark THIS
	// machine as primary. Most natural after `yaver auth` on a fresh
	// machine: register, then claim primary in one step. Reads
	// device_id from ~/.yaver/config.json (populated when the agent
	// completes its first registration round-trip).
	if target == "" || strings.EqualFold(target, "self") || strings.EqualFold(target, "me") || strings.EqualFold(target, "local") || target == "." {
		cfg, _ := LoadConfig()
		if cfg == nil || strings.TrimSpace(cfg.DeviceID) == "" {
			fmt.Fprintln(os.Stderr, "This machine has no registered deviceId yet.")
			fmt.Fprintln(os.Stderr, "Run `yaver auth` and then `yaver serve` once so the agent registers,")
			fmt.Fprintln(os.Stderr, "then re-run `yaver primary set` to claim primary on this machine.")
			os.Exit(1)
		}
		target = cfg.DeviceID
	}
	token, convex, err := primaryLoadAuth()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	devices, err := primaryListDevices(ctx, token, convex)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to list devices: %v\n", err)
		os.Exit(1)
	}
	// Resolve the target: exact deviceId, then unique prefix, then exact name.
	var matches []primaryDevice
	for _, d := range devices {
		if d.DeviceID == target || strings.EqualFold(d.Name, target) {
			matches = []primaryDevice{d}
			break
		}
		if strings.HasPrefix(d.DeviceID, target) {
			matches = append(matches, d)
		}
	}
	if len(matches) == 0 {
		fmt.Fprintf(os.Stderr, "No device matching %q. Run 'yaver primary' to see the list.\n", target)
		os.Exit(1)
	}
	if len(matches) > 1 {
		fmt.Fprintf(os.Stderr, "%q matches multiple devices — use a longer prefix or the full deviceId:\n", target)
		for _, d := range matches {
			fmt.Fprintf(os.Stderr, "  %s — %s\n", d.DeviceID, d.Name)
		}
		os.Exit(1)
	}
	chosen := matches[0]
	if chosen.IsGuest {
		fmt.Fprintln(os.Stderr, "Cannot mark a shared (guest) device as primary — the host can revoke it at any time.")
		os.Exit(1)
	}
	if err := primarySaveRaw(ctx, token, convex, chosen.DeviceID, false); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to set primary: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Primary device set to %s (%s).\n", chosen.Name, chosen.DeviceID[:min(8, len(chosen.DeviceID))])
}

func runPrimaryClear(ctx context.Context) {
	token, convex, err := primaryLoadAuth()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	if err := primarySaveRaw(ctx, token, convex, "", true); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to clear primary: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("Primary device cleared. Multi-device users will be asked to pick on next login.")
}

func runPrimaryRunnerQuickFlow(ctx context.Context, runner string, extra []string) {
	if len(extra) > 0 {
		fmt.Fprintf(os.Stderr, "primary: unexpected extra arguments after %s: %s\n", runner, strings.Join(extra, " "))
		os.Exit(1)
	}
	token, convex, err := primaryLoadAuth()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	current, err := primaryGetCurrent(ctx, token, convex)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to read settings: %v\n", err)
		os.Exit(1)
	}
	current = strings.TrimSpace(current)
	if current == "" {
		fmt.Fprintln(os.Stderr, "No primary device set. Run `yaver primary set <deviceId>` first.")
		os.Exit(1)
	}
	runRunnerQuickFlow(current, runner, nil)
}
