package main

// MCP tools for the WiFi-paired phone install flow. Mirrors mcp_wire_tools.go
// (cable) but routes through the wireless-aware device picker so AI agents
// can drive an Android tablet over WiFi without ever shelling out.
//
// Tools registered here (see mcp_tools.go for the JSONSchemas + http
// dispatch in httpserver.go):
//   - wireless_detect          paired + visible-unpaired across iOS/Android
//   - wireless_pair_android    one-shot adb pair + (optional) auto-connect
//   - wireless_connect_android adb connect to a previously-paired phone
//   - wireless_setup_android   non-interactive variant of `yaver android setup`
//                              (caller supplies the 6-digit code)
//   - wireless_push            framework-detect + native build + install,
//                              long-running, captures to ~/.yaver/logs/

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// mcpWirelessDetect surfaces the same shape as `yaver wireless detect --json`.
// AI agents call this first to learn:
//   - paired iOS/Android devices (push targets)
//   - visible-unpaired Android devices that need pairing (so the agent can
//     ask the user for a 6-digit code instead of failing silently)
//   - tooling gaps (`xcrun` / `adb` missing)
func mcpWirelessDetect() (interface{}, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	report := buildWirelessDetectReport(ctx)
	return map[string]interface{}{
		"devices":                 report.Devices,
		"paired_count":            report.PairedCount,
		"visible_unpaired_count":  report.VisibleCount,
		"hint":                    report.Hint,
		"next_step_when_unpaired": "call wireless_setup_android (after the user taps 'Pair device with pairing code' on the phone) or wireless_pair_android with the explicit ip/code",
	}, nil
}

// mcpWirelessPairAndroidArgs is the JSON shape MCP clients send.
type mcpWirelessPairAndroidArgs struct {
	// IPPort is the *pairing* host:port the phone shows under
	// "Pair device with pairing code" — DIFFERENT from the connect
	// port shown on the main Wireless debugging screen.
	IPPort string `json:"ip_port"`
	// Code is the 6-digit pairing code shown next to the IP/port.
	Code string `json:"code"`
	// AutoConnect = true (default) makes the tool resolve the
	// matching `_adb-tls-connect._tcp` mDNS entry after pairing and
	// run `adb connect` against it, returning a single
	// already-connected device. Set false when the agent wants to
	// drive the connect step itself.
	AutoConnect *bool `json:"auto_connect"`
}

// mcpWirelessPairAndroid runs `adb pair` + (optional) `adb connect` and
// returns the resulting device list so the caller can verify in one round
// trip. Output:
//
//   {
//     ok, paired_to, pair_output,
//     connected, connect_to, connect_output,
//     devices_now, error
//   }
func mcpWirelessPairAndroid(args mcpWirelessPairAndroidArgs) (interface{}, error) {
	ipPort := strings.TrimSpace(args.IPPort)
	code := strings.TrimSpace(args.Code)
	if ipPort == "" || code == "" {
		return nil, fmt.Errorf("ip_port and code are both required (got ip_port=%q, code-len=%d)", ipPort, len(code))
	}
	if !isAdbWirelessSerial(ipPort) {
		return nil, fmt.Errorf("ip_port %q does not look like ip:port", ipPort)
	}
	autoConnect := true
	if args.AutoConnect != nil {
		autoConnect = *args.AutoConnect
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	out := map[string]interface{}{
		"ok":         false,
		"paired_to":  ipPort,
		"connected":  false,
		"auto_connect": autoConnect,
	}
	pairOut, perr := adbPair(ctx, ipPort, code)
	out["pair_output"] = pairOut
	if perr != nil {
		out["error"] = "adb pair failed: " + perr.Error()
		return out, nil
	}
	out["paired"] = true

	if autoConnect {
		hp := findConnectHostPortFromMdns(ctx)
		if hp == "" {
			out["error"] = "paired but no connect address visible via mDNS yet — call wireless_connect_android with the connect IP:port shown on the phone"
		} else {
			out["connect_to"] = hp
			cout, cerr := adbConnect(ctx, hp)
			out["connect_output"] = cout
			if cerr != nil {
				out["error"] = "adb connect failed: " + cerr.Error()
			} else {
				out["connected"] = true
			}
		}
	}

	report := buildWirelessDetectReport(ctx)
	out["devices_now"] = report.Devices
	out["paired_count"] = report.PairedCount
	out["visible_unpaired_count"] = report.VisibleCount
	if out["error"] == nil {
		out["ok"] = true
	}
	return out, nil
}

// mcpWirelessConnectAndroidArgs is the JSON shape MCP clients send.
type mcpWirelessConnectAndroidArgs struct {
	// IPPort is the *connect* host:port from the phone's main Wireless
	// debugging screen. Empty = auto-discover via mDNS.
	IPPort string `json:"ip_port"`
}

// mcpWirelessConnectAndroid is the re-attach path for an already-paired
// phone. Returns the post-connect device list so the agent can confirm.
func mcpWirelessConnectAndroid(args mcpWirelessConnectAndroidArgs) (interface{}, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	ipPort := strings.TrimSpace(args.IPPort)
	if ipPort == "" {
		ipPort = findConnectHostPortFromMdns(ctx)
		if ipPort == "" {
			return map[string]interface{}{
				"ok":    false,
				"error": "no ip_port provided and nothing visible via mDNS — make sure Wireless debugging is enabled and the phone is on the same WiFi",
			}, nil
		}
	}
	if !isAdbWirelessSerial(ipPort) {
		return nil, fmt.Errorf("ip_port %q does not look like ip:port", ipPort)
	}
	cout, cerr := adbConnect(ctx, ipPort)
	out := map[string]interface{}{
		"ok":            cerr == nil,
		"connect_to":    ipPort,
		"connect_output": cout,
	}
	if cerr != nil {
		out["error"] = cerr.Error()
		return out, nil
	}
	report := buildWirelessDetectReport(ctx)
	out["devices_now"] = report.Devices
	out["paired_count"] = report.PairedCount
	return out, nil
}

// mcpWirelessSetupAndroidArgs is the JSON shape MCP clients send for
// the non-interactive setup variant. The user still has to tap "Pair
// device with pairing code" on the phone — only then does this tool see
// the pairing service via mDNS — but the AI agent supplies the code
// (collected via `yaver_ask_user`) so no human-stdin is needed.
type mcpWirelessSetupAndroidArgs struct {
	Code         string `json:"code"`
	PollSeconds  int    `json:"poll_seconds"`
}

// mcpWirelessSetupAndroid wraps the `yaver android setup` flow for AI
// agents:
//   1. Polls mDNS up to PollSeconds for `_adb-tls-pairing._tcp`.
//   2. Pairs against the discovered host:port using `code`.
//   3. Resolves `_adb-tls-connect._tcp` for the same instance and
//      `adb connect`s.
//   4. Returns the post-setup device list.
//
// AI agents should typically call yaver_ask_user *before* this tool to
// (a) tell the user to tap "Pair device with pairing code", (b) collect
// the 6-digit code. PollSeconds defaults to 120 (matches the CLI).
func mcpWirelessSetupAndroid(args mcpWirelessSetupAndroidArgs) (interface{}, error) {
	code := strings.TrimSpace(args.Code)
	if len(code) < 6 {
		return nil, fmt.Errorf("code is required (6 digits from the phone's Pair device with pairing code screen)")
	}
	poll := args.PollSeconds
	if poll <= 0 {
		poll = 120
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(poll+30)*time.Second)
	defer cancel()

	pairCtx, pairCancel := context.WithTimeout(ctx, time.Duration(poll)*time.Second)
	pair := waitForAdbPairingService(pairCtx, 2*time.Second)
	pairCancel()
	if pair.HostPort == "" {
		return map[string]interface{}{
			"ok": false,
			"error": "no _adb-tls-pairing._tcp service appeared within poll_seconds — confirm the user tapped 'Pair device with pairing code' on the phone and that the phone is on the same WiFi as the agent",
		}, nil
	}

	pairOut, perr := adbPair(ctx, pair.HostPort, code)
	if perr != nil {
		return map[string]interface{}{
			"ok":          false,
			"paired_to":   pair.HostPort,
			"pair_output": pairOut,
			"error":       "adb pair failed: " + perr.Error(),
		}, nil
	}

	// Look for the matching connect entry — same instance name.
	connectHP := ""
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		for _, s := range adbMdnsServices(ctx) {
			if s.Type == "_adb-tls-connect._tcp" && s.Name == pair.Name && !strings.HasPrefix(s.HostPort, "0.0.0.0:") {
				connectHP = s.HostPort
				break
			}
		}
		if connectHP != "" {
			break
		}
		select {
		case <-ctx.Done():
			break
		case <-time.After(1 * time.Second):
		}
	}
	if connectHP == "" {
		connectHP = findConnectHostPortFromMdns(ctx)
	}
	if connectHP == "" {
		return map[string]interface{}{
			"ok":          false,
			"paired_to":   pair.HostPort,
			"pair_output": pairOut,
			"error":       "paired but could not find the connect address via mDNS — call wireless_connect_android with the IP:port from the phone's Wireless debugging screen",
		}, nil
	}

	cout, cerr := adbConnect(ctx, connectHP)
	report := buildWirelessDetectReport(ctx)
	out := map[string]interface{}{
		"ok":             cerr == nil,
		"paired_to":      pair.HostPort,
		"pair_output":    pairOut,
		"connect_to":     connectHP,
		"connect_output": cout,
		"devices_now":    report.Devices,
		"paired_count":   report.PairedCount,
	}
	if cerr != nil {
		out["error"] = "adb connect failed: " + cerr.Error()
	}
	return out, nil
}

// mcpWirelessPushArgs is identical in shape to mcpWirePushArgs — same
// build flags, just routed through the wireless device picker so the AI
// can target a phone without a USB cable.
type mcpWirelessPushArgs = mcpWirePushArgs

// mcpWirelessPush builds + installs a self-contained native binary on a
// WiFi-paired phone. Same return shape as mcpWirePush. Long-running;
// stdout/stderr captured to ~/.yaver/logs/wireless-push-*.log.
func mcpWirelessPush(args mcpWirelessPushArgs) (interface{}, error) {
	root := strings.TrimSpace(args.Path)
	if root == "" {
		// Prefer the AI session's pinned cwd over the agent process's
		// runtime cwd. See mcp_session_cwd.go. The caller can always
		// override by passing an explicit `path`.
		root = ResolveMCPCwd()
		if root == "" {
			return nil, fmt.Errorf("cwd: no session cwd and os.Getwd() returned empty")
		}
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("bad path %q: %w", root, err)
	}

	resolvedRoot, stack := resolveMobileProject(abs)
	if stack == "" {
		return nil, fmt.Errorf(
			"no mobile project detected at %s (or its mobile/, app/, apps/*, packages/* subdirs)",
			abs,
		)
	}

	platform := strings.ToLower(strings.TrimSpace(args.Platform))
	if platform == "" {
		platform = pickPlatformForStack(stack)
	}
	if platform != "ios" && platform != "android" {
		return nil, fmt.Errorf("platform must be ios or android (got %q)", args.Platform)
	}

	timeoutSec := args.TimeoutSec
	if timeoutSec <= 0 {
		timeoutSec = 1800
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutSec)*time.Second)
	defer cancel()

	device, err := pickWirelessDevice(ctx, platform, args.Device)
	if err != nil {
		// Be specific when the failure is "no paired device" so the
		// caller can chain wireless_setup_android automatically.
		report := buildWirelessDetectReport(ctx)
		hint := ""
		if report.VisibleCount > 0 {
			hint = "; mDNS shows " + fmt.Sprint(report.VisibleCount) + " visible-unpaired " + platform + " device(s) — call wireless_setup_android first"
		}
		return nil, fmt.Errorf("%s%s", err.Error(), hint)
	}

	cfg := strings.TrimSpace(args.Config)
	if cfg == "" {
		cfg = "Release"
	}
	if cfg != "Debug" && cfg != "Release" {
		return nil, fmt.Errorf("config must be Debug or Release (got %q)", args.Config)
	}

	homeDir, _ := os.UserHomeDir()
	logsDir := filepath.Join(homeDir, ".yaver", "logs")
	_ = os.MkdirAll(logsDir, 0o755)
	logName := fmt.Sprintf("wireless-push-%s-%s-%s.log",
		platform,
		strings.ReplaceAll(strings.ReplaceAll(device.UDID, ":", ""), "/", ""),
		time.Now().Format("20060102-150405"),
	)
	logPath := filepath.Join(logsDir, logName)
	logFile, err := os.Create(logPath)
	if err != nil {
		return nil, fmt.Errorf("create log: %w", err)
	}
	defer logFile.Close()

	startedAt := time.Now()
	opts := wirePushOpts{
		device:   device.UDID,
		platform: platform,
		config:   cfg,
		noLaunch: args.NoLaunch,
	}

	dispatchErr := dispatchWirePushTo(ctx, resolvedRoot, stack, platform, device, opts, logFile)
	elapsed := int(time.Since(startedAt).Seconds())

	exitCode := 0
	ok := dispatchErr == nil
	errMsg := ""
	if dispatchErr != nil {
		errMsg = dispatchErr.Error()
		exitCode = -1
	}

	tail := readTailLines(logPath, 30)

	return map[string]interface{}{
		"ok":          ok,
		"exit_code":   exitCode,
		"device":      device,
		"platform":    platform,
		"transport":   "wireless",
		"stack":       stack,
		"path":        resolvedRoot,
		"config":      cfg,
		"log_path":    logPath,
		"log_tail":    tail,
		"elapsed_sec": elapsed,
		"error":       errMsg,
	}, nil
}
