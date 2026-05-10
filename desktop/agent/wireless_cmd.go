package main

// `yaver wireless <subcmd>` — WiFi-paired iPhone/iPad/Android install flows.
// Mirrors `yaver wire` but targets devices reachable over the local
// network instead of USB cable.
//
// iOS:     device must be paired with the Mac via Xcode (cable initially)
//          with "Connect via network" enabled in Window → Devices and
//          Simulators. After that, devicectl auto-discovers the device
//          whenever both are on the same WiFi.
// Android: device must be paired via Android 11+ Wireless Debugging
//          (`yaver wireless pair-android <ip>:<port> <code>`) or, on older
//          phones, attached over cable once with `adb tcpip 5555` followed
//          by `adb connect <ip>:5555`. After pairing, adb auto-reconnects
//          on the same WiFi.
//
// Subcommands:
//   yaver wireless detect                      list every WiFi-paired phone (iOS + Android)
//   yaver wireless push [path]                 detect framework + push to a wireless device
//   yaver wireless pair-android <ip:port> <c>  pair Android 11+ over WiFi (one-time)
//   yaver wireless connect-android <ip:port>   reconnect a previously-paired Android phone

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

func runWireless(args []string) {
	if len(args) == 0 {
		wirelessUsage()
		os.Exit(2)
	}
	sub := args[0]
	rest := args[1:]
	switch sub {
	case "detect", "list", "ls", "devices":
		runWirelessDetect(rest)
	case "push", "run", "dev":
		runWirelessPush(rest)
	case "pair-android", "pair":
		runWirelessPairAndroid(rest)
	case "connect-android", "connect":
		runWirelessConnectAndroid(rest)
	case "setup-android", "setup":
		runWirelessSetupAndroid(rest)
	case "-h", "--help", "help":
		wirelessUsage()
	default:
		fmt.Fprintf(os.Stderr, "yaver wireless: unknown subcommand %q\n\n", sub)
		wirelessUsage()
		os.Exit(2)
	}
}

func wirelessUsage() {
	fmt.Println("yaver wireless — WiFi-paired phone install flows (iOS + Android)")
	fmt.Println()
	fmt.Println("Subcommands:")
	fmt.Println("  detect                          list WiFi devices: paired + visible-but-unpaired (mDNS)")
	fmt.Println("  push [path]                     detect framework + push to a wireless device")
	fmt.Println("  setup-android                   interactive one-shot: discover → pair → connect → verify")
	fmt.Println("  pair-android [--ip <ip:port>] [--code <c>] | <ip:port> <code>")
	fmt.Println("                                  pair an Android 11+ phone over WiFi (one-time)")
	fmt.Println("  connect-android [--ip <ip:port>] | <ip:port>")
	fmt.Println("                                  reconnect a previously-paired Android phone")
	fmt.Println()
	fmt.Println("Push flags:")
	fmt.Println("  --device <udid|serial>          pick a specific device (default: first wireless)")
	fmt.Println("  --platform ios|android          force platform when both are reachable")
	fmt.Println("  --config Debug|Release          xcode/gradle build configuration (default: Release)")
	fmt.Println("  --no-launch                     install without launching")
	fmt.Println()
	fmt.Println("iOS one-time setup (per device):")
	fmt.Println("  1. Cable the iPhone to this Mac.")
	fmt.Println("  2. Open Xcode → Window → Devices and Simulators.")
	fmt.Println("  3. Select the device → check 'Connect via network'.")
	fmt.Println("  4. Unplug. The device shows up here over WiFi from now on.")
	fmt.Println()
	fmt.Println("Android one-time setup (per device, Android 11+) — easy path:")
	fmt.Println("  yaver wireless setup-android       (walks you through it interactively)")
	fmt.Println()
	fmt.Println("Android manual path:")
	fmt.Println("  1. On phone: Settings → Developer options → Wireless debugging → enable.")
	fmt.Println("  2. Tap 'Pair device with pairing code' to get IP:port + 6-digit code.")
	fmt.Println("  3. yaver wireless pair-android --ip <ip:port> --code <code>")
	fmt.Println("  4. yaver wireless detect")
	fmt.Println()
	fmt.Println("Examples:")
	fmt.Println("  yaver wireless detect")
	fmt.Println("  yaver wireless setup-android")
	fmt.Println("  yaver wireless push                   (cwd)")
	fmt.Println("  yaver wireless push ./mobile --platform android")
	fmt.Println()
	fmt.Println("Tip: `yaver android <subcmd>` is a shorter Android-only alias")
	fmt.Println("     (yaver android pair / connect / setup / push / list).")
}

// ---------- detect ----------

// wirelessDeviceStatus distinguishes "ready to push" from "discoverable
// over mDNS but adb hasn't paired+connected yet". Surfaced in the CLI
// table, /wireless/devices JSON, and MCP responses so callers can hint
// the user toward the next step.
type wirelessDeviceStatus struct {
	wireDevice
	Status string `json:"status"` // "paired" | "visible-unpaired"
	// Hint for visible-unpaired entries: how to actually pair this one.
	// Empty for paired entries.
	HowToPair string `json:"how_to_pair,omitempty"`
}

type wirelessDetectReport struct {
	Devices []wirelessDeviceStatus `json:"devices"`
	// Counts split out so JSON consumers don't have to re-tally.
	PairedCount  int `json:"paired_count"`
	VisibleCount int `json:"visible_unpaired_count"`
	// Tooling preflight (e.g. "adb missing") — empty when everything
	// the platform supports is installed.
	Hint string `json:"hint,omitempty"`
}

func runWirelessDetect(args []string) {
	fs := flag.NewFlagSet("wireless detect", flag.ExitOnError)
	jsonOut := fs.Bool("json", false, "emit JSON")
	noInfo := fs.Bool("no-info", false, "skip device info enrichment (faster, fewer adb/xcrun calls)")
	_ = fs.Parse(args)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	report := buildWirelessDetectReport(ctx)
	if !*noInfo {
		enrichDetectReport(ctx, &report)
	}

	if *jsonOut {
		_ = json.NewEncoder(os.Stdout).Encode(report)
		return
	}

	if len(report.Devices) == 0 {
		fmt.Println("No WiFi devices found (paired or visible).")
		if report.Hint != "" {
			fmt.Println()
			fmt.Println("→", report.Hint)
		}
		fmt.Println()
		printWirelessDetectHints()
		return
	}

	fmt.Printf("%-18s  %-10s  %-44s  %s\n", "STATUS", "PLATFORM", "UDID/SERIAL", "DEVICE")
	fmt.Printf("%-18s  %-10s  %-44s  %s\n", "------", "--------", "-----------", "------")
	for _, d := range report.Devices {
		label := wirelessRowLabel(d)
		fmt.Printf("%-18s  %-10s  %-44s  %s\n", d.Status, d.Platform, d.UDID, label)
	}
	if report.VisibleCount > 0 {
		fmt.Println()
		fmt.Println("→ visible-unpaired entries are reachable on the network but adb hasn't paired with this Mac yet.")
		fmt.Println("  run `yaver wireless setup-android`  (or `yaver android setup`) to walk through pairing.")
	}
	if report.Hint != "" {
		fmt.Println()
		fmt.Println("→", report.Hint)
	}
}

// wirelessRowLabel renders the right-most cell. Prefers the enriched
// "Galaxy Tab S7 FE • Android 14 • 3.3 GB RAM" summary when available;
// falls back to the bare adb model name otherwise.
func wirelessRowLabel(d wirelessDeviceStatus) string {
	if d.Info != nil {
		if s := d.Info.summary(); s != "" {
			return s
		}
	}
	if d.Name != "" {
		return d.Name
	}
	return "(unknown)"
}

// enrichDetectReport runs enrichWireDevices over the embedded wireDevice
// values inside a wirelessDetectReport. Visible-unpaired entries are
// skipped since adb shell against an unpaired serial returns nothing.
func enrichDetectReport(ctx context.Context, report *wirelessDetectReport) {
	devs := make([]wireDevice, len(report.Devices))
	for i, d := range report.Devices {
		devs[i] = d.wireDevice
	}
	// Only enrich paired entries — unpaired devices have no shell.
	target := make([]wireDevice, 0, len(devs))
	idx := make([]int, 0, len(devs))
	for i := range devs {
		if report.Devices[i].Status == "paired" {
			target = append(target, devs[i])
			idx = append(idx, i)
		}
	}
	enrichWireDevices(ctx, target, 4)
	for j, t := range target {
		report.Devices[idx[j]].Info = t.Info
		report.Devices[idx[j]].wireDevice.Info = t.Info
	}
}

// buildWirelessDetectReport unifies the three sources of wireless info
// (iOS devicectl, paired adb devices, mDNS-visible adb devices) into a
// single status-tagged list. Used by the CLI, /wireless/devices, and
// the wireless_detect MCP tool — keep them in sync.
func buildWirelessDetectReport(ctx context.Context) wirelessDetectReport {
	devs := listWirelessDevices(ctx)

	// Set of paired serials/UDIDs already in `devs` so we don't
	// double-list a tablet that's both adb-paired AND visible via mDNS.
	pairedKey := map[string]bool{}
	for _, d := range devs {
		pairedKey[d.UDID] = true
		// Also mark by mDNS instance name when we can derive one,
		// since the IP-form serial and the mDNS form refer to the
		// same physical device.
		if strings.Contains(d.UDID, "._adb-tls-connect.") {
			pairedKey[d.UDID] = true
		}
	}

	// Build status entries for paired devices first.
	out := make([]wirelessDeviceStatus, 0, len(devs)+2)
	for _, d := range devs {
		out = append(out, wirelessDeviceStatus{wireDevice: d, Status: "paired"})
	}

	// Layer mDNS-visible Android devices that aren't paired yet.
	mdns := adbMdnsServices(ctx)
	seenMdnsSerial := map[string]bool{}
	pairedMdnsName := map[string]bool{}
	// Re-derive paired mDNS instance names from the paired list — when
	// adb shows `adb-XXX._adb-tls-connect.tcp` as a UDID we already
	// have the name; when it shows the IP:port form we need to match
	// by host:port against the connect-service mDNS entry.
	pairedHostPort := map[string]bool{}
	for _, d := range devs {
		if d.Platform != "android" {
			continue
		}
		if strings.Contains(d.UDID, "._adb-tls-connect.") {
			pairedMdnsName[d.UDID] = true
			continue
		}
		// IP:port form
		pairedHostPort[d.UDID] = true
	}
	for _, s := range mdns {
		if s.Type != "_adb-tls-connect._tcp" {
			continue
		}
		if pairedMdnsName[s.Name+"._adb-tls-connect._tcp."] || pairedMdnsName[s.Name] {
			continue
		}
		if pairedHostPort[s.HostPort] {
			continue
		}
		if seenMdnsSerial[s.Name] {
			continue
		}
		seenMdnsSerial[s.Name] = true
		out = append(out, wirelessDeviceStatus{
			wireDevice: wireDevice{
				UDID:     s.HostPort,
				Name:     "(android — pair to learn model)",
				Platform: "android",
			},
			Status: "visible-unpaired",
			HowToPair: "yaver wireless setup-android   (or `yaver android setup`); " +
				"or manually: tap 'Pair device with pairing code' on the tablet, then " +
				"`yaver android pair --ip <pair-ip:port> --code <6-digits>`",
		})
	}

	report := wirelessDetectReport{Devices: out, Hint: wireToolHint(true)}
	for _, d := range out {
		switch d.Status {
		case "paired":
			report.PairedCount++
		case "visible-unpaired":
			report.VisibleCount++
		}
	}
	return report
}

// listWirelessDevices is the canonical "all wireless devices" view used by
// the CLI, the agent HTTP endpoint, and (eventually) MCP tools. Returns
// iOS first, then Android, deduped by UDID.
func listWirelessDevices(ctx context.Context) []wireDevice {
	devs := append([]wireDevice{}, listIOSWirelessDevices(ctx)...)
	devs = append(devs, listAndroidWirelessDevices(ctx)...)
	return devs
}

func printWirelessDetectHints() {
	if runtime.GOOS != "darwin" {
		fmt.Println("  iOS wireless detection requires macOS + Xcode (xcrun devicectl).")
	} else if _, err := exec.LookPath("xcrun"); err != nil {
		fmt.Println("  iOS: install Xcode command line tools (xcrun missing).")
	} else {
		fmt.Println("  iOS:")
		fmt.Println("    - Make sure the device is on the same WiFi as this Mac.")
		fmt.Println("    - In Xcode → Window → Devices and Simulators, the device must show")
		fmt.Println("      'Connect via network' (one-time setup over cable).")
		fmt.Println("    - The device must be unlocked and 'Trust This Computer' must be granted.")
	}
	if _, err := exec.LookPath("adb"); err != nil {
		fmt.Println("  Android: install platform-tools (adb missing). brew install android-platform-tools")
	} else {
		fmt.Println("  Android:")
		fmt.Println("    - Same WiFi as this machine.")
		fmt.Println("    - Android 11+: Settings → Developer options → Wireless debugging → enable,")
		fmt.Println("      then `yaver wireless pair-android <ip:port> <code>`.")
		fmt.Println("    - Older Android: cable once, run `adb tcpip 5555`, then")
		fmt.Println("      `yaver wireless connect-android <phone-ip>:5555`.")
	}
}

// ---------- push ----------

// runWirelessPush picks a wireless-reachable device and runs it through
// the existing wire-push native build pipeline. Once a UDID is picked,
// xcrun devicectl install / adb install work the same regardless of
// transport, so we just need a wireless-aware device picker on the front.
//
// IMPORTANT: this uses the SAME native-build pipeline as `yaver wire push`
// — xcodebuild + xcrun devicectl install (or gradle + adb on Android).
// There is no WebView fallback. JS gets baked into the .app/.apk at build
// time so the running app is a real native app, not a Safari-View-Controller
// wrapper. Per CLAUDE.md "Never WebView for third-party RN apps."
func runWirelessPush(args []string) {
	fs := flag.NewFlagSet("wireless push", flag.ExitOnError)
	opts := wirePushOpts{}
	fs.StringVar(&opts.device, "device", "", "specific device UDID/serial")
	fs.StringVar(&opts.platform, "platform", "", "ios|android — force platform when both are reachable")
	fs.StringVar(&opts.config, "config", "Release", "xcode/gradle build configuration: Debug|Release")
	fs.BoolVar(&opts.noLaunch, "no-launch", false, "install without launching")
	_ = fs.Parse(args)
	switch strings.ToLower(opts.config) {
	case "debug":
		opts.config = "Debug"
	case "release", "":
		opts.config = "Release"
	default:
		fmt.Fprintf(os.Stderr, "yaver wireless push: --config must be Debug or Release (got %q)\n", opts.config)
		os.Exit(2)
	}

	root := fs.Arg(0)
	if root == "" {
		var err error
		root, err = os.Getwd()
		if err != nil {
			fmt.Fprintf(os.Stderr, "yaver wireless push: cannot determine cwd: %v\n", err)
			os.Exit(2)
		}
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "yaver wireless push: bad path %q: %v\n", root, err)
		os.Exit(2)
	}

	projectRoot, stack := resolveMobileProject(abs)
	if stack == "" {
		fmt.Fprintf(os.Stderr, "yaver wireless push: no mobile project detected at %s (or its common subdirs)\n", abs)
		os.Exit(2)
	}
	if projectRoot != abs {
		fmt.Printf("→ resolved %s → %s\n", abs, projectRoot)
	}
	abs = projectRoot

	platform := strings.ToLower(opts.platform)
	if platform == "" {
		platform = pickPlatformForStack(stack)
	}
	if platform != "ios" && platform != "android" {
		fmt.Fprintf(os.Stderr, "yaver wireless push: --platform must be ios or android (got %q)\n", platform)
		os.Exit(2)
	}

	ctx, cancel := signalContext()
	defer cancel()

	device, err := pickWirelessDevice(ctx, platform, opts.device)
	if err != nil {
		fmt.Fprintf(os.Stderr, "yaver wireless push: %v\n", err)
		os.Exit(2)
	}

	fmt.Printf("→ project:  %s\n", abs)
	fmt.Printf("→ stack:    %s\n", stack)
	fmt.Printf("→ platform: %s (wireless)\n", platform)
	fmt.Printf("→ device:   %s  (%s)\n", device.UDID, displayName(device))
	fmt.Println()

	if err := dispatchWirePush(ctx, abs, stack, platform, device, opts); err != nil {
		fmt.Fprintf(os.Stderr, "\nyaver wireless push: %v\n", err)
		os.Exit(1)
	}
}

// pickWirelessDevice mirrors pickWireDevice but pulls from the
// wireless-reachable lists (xcrun devicectl filtered to wireless transport;
// adb devices filtered to IP:port serials). Surfaces a clear hint when
// the platform's wireless toolchain is missing or no devices are paired.
func pickWirelessDevice(ctx context.Context, platform, want string) (wireDevice, error) {
	var devs []wireDevice
	switch platform {
	case "ios":
		devs = listIOSWirelessDevices(ctx)
	case "android":
		devs = listAndroidWirelessDevices(ctx)
	}
	if len(devs) == 0 {
		switch platform {
		case "ios":
			return wireDevice{}, fmt.Errorf("no WiFi-paired iPhone/iPad found — set up `Connect via network` in Xcode once over cable, then `yaver wireless detect`")
		case "android":
			return wireDevice{}, fmt.Errorf("no WiFi-paired Android phone found — `yaver wireless pair-android <ip:port> <code>` or `yaver wireless connect-android <ip:port>`, then `yaver wireless detect`")
		}
	}
	if want != "" {
		for _, d := range devs {
			if d.UDID == want || strings.EqualFold(d.Name, want) {
				return d, nil
			}
		}
		return wireDevice{}, fmt.Errorf("device %q not reachable over WiFi — `yaver wireless detect` shows %d %s device(s)", want, len(devs), platform)
	}
	if len(devs) > 1 {
		fmt.Fprintf(os.Stderr, "yaver wireless push: %d %s devices reachable — picking first. Use --device to choose:\n", len(devs), platform)
		for _, d := range devs {
			fmt.Fprintf(os.Stderr, "    %s  %s\n", d.UDID, displayName(d))
		}
		fmt.Fprintln(os.Stderr)
	}
	return devs[0], nil
}

// ---------- pair-android / connect-android ----------

// runWirelessPairAndroid accepts both forms:
//   yaver wireless pair-android <ip:port> <code>
//   yaver wireless pair-android --ip <ip:port> --code <code>
// The flag form is what the `yaver android pair` alias uses.
func runWirelessPairAndroid(args []string) {
	fs := flag.NewFlagSet("wireless pair-android", flag.ExitOnError)
	ipFlag := fs.String("ip", "", "pairing IP:port (use the pairing port, not the connect port)")
	codeFlag := fs.String("code", "", "6-digit pairing code shown on the phone")
	autoConnect := fs.Bool("auto-connect", true, "after pairing, run adb connect against the matching mDNS connect entry")
	_ = fs.Parse(args)

	ipPort := *ipFlag
	code := *codeFlag
	rest := fs.Args()
	if ipPort == "" && len(rest) >= 1 {
		ipPort = rest[0]
	}
	if code == "" && len(rest) >= 2 {
		code = rest[1]
	}
	if ipPort == "" || code == "" {
		fmt.Fprintln(os.Stderr, "Usage:")
		fmt.Fprintln(os.Stderr, "  yaver wireless pair-android --ip <ip:port> --code <6-digit-code>")
		fmt.Fprintln(os.Stderr, "  yaver wireless pair-android <ip:port> <pairing-code>")
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, "On your phone (Android 11+):")
		fmt.Fprintln(os.Stderr, "  Settings → Developer options → Wireless debugging → Pair device with pairing code")
		fmt.Fprintln(os.Stderr, "Use the IP:port and 6-digit code shown there.")
		os.Exit(2)
	}
	if !isAdbWirelessSerial(ipPort) {
		fmt.Fprintf(os.Stderr, "yaver wireless pair-android: %q does not look like ip:port\n", ipPort)
		os.Exit(2)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	fmt.Printf("→ adb pair %s\n", ipPort)
	out, err := adbPair(ctx, ipPort, code)
	if out != "" {
		fmt.Print(out)
		if !strings.HasSuffix(out, "\n") {
			fmt.Println()
		}
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "\nadb pair failed: %v\n", err)
		fmt.Fprintln(os.Stderr, "Common causes: pairing code expired (regenerate on phone), not on same WiFi, firewall.")
		os.Exit(1)
	}

	if *autoConnect {
		if connectHP := findConnectHostPortFromMdns(ctx); connectHP != "" {
			fmt.Printf("→ adb connect %s   (auto-resolved from mDNS)\n", connectHP)
			cout, cerr := adbConnect(ctx, connectHP)
			if cout != "" {
				fmt.Print(cout)
				if !strings.HasSuffix(cout, "\n") {
					fmt.Println()
				}
			}
			if cerr == nil {
				fmt.Println()
				fmt.Println("Paired + connected. `yaver wireless detect` should now show the device as 'paired'.")
				return
			}
			fmt.Fprintf(os.Stderr, "auto-connect failed: %v\n", cerr)
		}
	}
	fmt.Println()
	fmt.Println("Paired. Now run `yaver wireless connect-android --ip <connection-ip:port>` with the")
	fmt.Println("Wireless debugging > IP address & Port (NOT the pairing port) shown on the phone,")
	fmt.Println("or `yaver android connect` to auto-discover from mDNS.")
}

// findConnectHostPortFromMdns picks the first `_adb-tls-connect._tcp`
// service from `adb mdns services` whose host is a real LAN IP (not
// 0.0.0.0). Used after a successful pair to spare the user from typing
// the connect IP/port a second time.
func findConnectHostPortFromMdns(ctx context.Context) string {
	for _, s := range adbMdnsServices(ctx) {
		if s.Type != "_adb-tls-connect._tcp" {
			continue
		}
		if strings.HasPrefix(s.HostPort, "0.0.0.0:") {
			continue
		}
		return s.HostPort
	}
	return ""
}

// runWirelessConnectAndroid accepts:
//   yaver wireless connect-android <ip:port>
//   yaver wireless connect-android --ip <ip:port>
//   yaver wireless connect-android                (auto-discover from mDNS)
func runWirelessConnectAndroid(args []string) {
	fs := flag.NewFlagSet("wireless connect-android", flag.ExitOnError)
	ipFlag := fs.String("ip", "", "connection IP:port (use the connect port shown on the phone, not the pairing port)")
	_ = fs.Parse(args)

	ipPort := *ipFlag
	rest := fs.Args()
	if ipPort == "" && len(rest) >= 1 {
		ipPort = rest[0]
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if ipPort == "" {
		ipPort = findConnectHostPortFromMdns(ctx)
		if ipPort == "" {
			fmt.Fprintln(os.Stderr, "Usage: yaver wireless connect-android --ip <ip:port>")
			fmt.Fprintln(os.Stderr)
			fmt.Fprintln(os.Stderr, "No mDNS-visible connect service found either — make sure Wireless debugging is enabled")
			fmt.Fprintln(os.Stderr, "on the phone and you're on the same WiFi network as this machine.")
			os.Exit(2)
		}
		fmt.Printf("→ auto-resolved %s from mDNS\n", ipPort)
	}
	if !isAdbWirelessSerial(ipPort) {
		fmt.Fprintf(os.Stderr, "yaver wireless connect-android: %q does not look like ip:port\n", ipPort)
		os.Exit(2)
	}

	fmt.Printf("→ adb connect %s\n", ipPort)
	out, err := adbConnect(ctx, ipPort)
	if out != "" {
		fmt.Print(out)
		if !strings.HasSuffix(out, "\n") {
			fmt.Println()
		}
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "\nadb connect failed: %v\n", err)
		fmt.Fprintln(os.Stderr, "If this is the first time, you need to pair first:")
		fmt.Fprintln(os.Stderr, "  yaver wireless setup-android")
		os.Exit(1)
	}
	fmt.Println()
	fmt.Println("Connected. Run `yaver wireless detect` to confirm.")
}

// ---------- setup-android (interactive) ----------

// runWirelessSetupAndroid is the friction-free path for first-time
// pairing. It:
//   1. Prompts the user to enable Wireless debugging + tap "Pair device
//      with pairing code" on the phone.
//   2. Polls `adb mdns services` for the new `_adb-tls-pairing._tcp`
//      entry (up to ~120 s).
//   3. Reads the 6-digit code from stdin (skips prompt if --code given).
//   4. Runs `adb pair` against the discovered pair host:port.
//   5. Resolves the matching `_adb-tls-connect._tcp` entry, runs
//      `adb connect`, and verifies via `adb devices -l`.
//
// Designed so an AI agent can drive the same flow over MCP — see
// mcpWirelessSetupAndroid for the non-interactive entry point.
func runWirelessSetupAndroid(args []string) {
	fs := flag.NewFlagSet("wireless setup-android", flag.ExitOnError)
	codeFlag := fs.String("code", "", "skip the code prompt (useful for scripts)")
	pollSec := fs.Int("poll-seconds", 120, "how long to wait for the pairing service to appear in mDNS")
	_ = fs.Parse(args)

	if _, err := exec.LookPath("adb"); err != nil {
		fmt.Fprintln(os.Stderr, "adb not found — `brew install android-platform-tools` (macOS) or install platform-tools.")
		os.Exit(2)
	}

	fmt.Println("yaver wireless setup-android — interactive Android 11+ pairing")
	fmt.Println()
	fmt.Println("On the Android device:")
	fmt.Println("  1. Settings → Developer options → Wireless debugging → toggle ON")
	fmt.Println("  2. Tap 'Pair device with pairing code'")
	fmt.Println()
	fmt.Println("Waiting for the pairing service to appear on the local network...")

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(*pollSec)*time.Second)
	defer cancel()

	pair := waitForAdbPairingService(ctx, 2*time.Second)
	if pair.HostPort == "" {
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, "Timed out waiting for the pairing service.")
		fmt.Fprintln(os.Stderr, "Make sure you tapped 'Pair device with pairing code' AND that")
		fmt.Fprintln(os.Stderr, "this Mac and the Android device are on the same WiFi network.")
		os.Exit(1)
	}
	fmt.Printf("✓ pairing service found: %s   (%s)\n", pair.HostPort, pair.Name)

	code := strings.TrimSpace(*codeFlag)
	if code == "" {
		fmt.Print("Enter the 6-digit pairing code shown on the device: ")
		reader := bufio.NewReader(os.Stdin)
		line, err := reader.ReadString('\n')
		if err != nil {
			fmt.Fprintf(os.Stderr, "could not read pairing code: %v\n", err)
			os.Exit(2)
		}
		code = strings.TrimSpace(line)
	}
	if len(code) < 6 {
		fmt.Fprintf(os.Stderr, "pairing code looks too short (%q)\n", code)
		os.Exit(2)
	}

	fmt.Printf("→ adb pair %s\n", pair.HostPort)
	out, err := adbPair(ctx, pair.HostPort, code)
	if out != "" {
		fmt.Print(out)
		if !strings.HasSuffix(out, "\n") {
			fmt.Println()
		}
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "adb pair failed: %v\n", err)
		os.Exit(1)
	}

	// Find the matching connect entry (same instance name).
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
		// Fall back to any visible connect entry.
		connectHP = findConnectHostPortFromMdns(ctx)
	}
	if connectHP == "" {
		fmt.Fprintln(os.Stderr, "paired but could not find the connect address via mDNS.")
		fmt.Fprintln(os.Stderr, "On the device, the Wireless debugging screen shows 'IP address & Port' —")
		fmt.Fprintln(os.Stderr, "run: yaver wireless connect-android --ip <that ip:port>")
		os.Exit(1)
	}

	fmt.Printf("→ adb connect %s\n", connectHP)
	cout, cerr := adbConnect(ctx, connectHP)
	if cout != "" {
		fmt.Print(cout)
		if !strings.HasSuffix(cout, "\n") {
			fmt.Println()
		}
	}
	if cerr != nil {
		fmt.Fprintf(os.Stderr, "adb connect failed: %v\n", cerr)
		os.Exit(1)
	}

	fmt.Println()
	fmt.Println("Verifying...")
	report := buildWirelessDetectReport(ctx)
	matched := false
	for _, d := range report.Devices {
		if d.Status == "paired" && d.Platform == "android" {
			matched = true
			fmt.Printf("  ✓ paired:  %s  (%s)\n", d.UDID, d.Name)
		}
	}
	if !matched {
		fmt.Println("  (no paired Android entry visible yet — try `yaver wireless detect` in a moment)")
	}
	fmt.Println()
	fmt.Println("Done. Push your project with:")
	fmt.Println("  yaver wireless push --platform android        (or `yaver android push`)")
}

// waitForAdbPairingService polls `adb mdns services` until a
// `_adb-tls-pairing._tcp` entry appears (or ctx expires). Returns the
// first match. Empty service when ctx expires.
func waitForAdbPairingService(ctx context.Context, every time.Duration) adbMdnsService {
	for {
		for _, s := range adbMdnsServices(ctx) {
			if s.Type == "_adb-tls-pairing._tcp" {
				return s
			}
		}
		select {
		case <-ctx.Done():
			return adbMdnsService{}
		case <-time.After(every):
		}
	}
}
