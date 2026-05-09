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
	fmt.Println("  detect                          list WiFi-paired iPhones/iPads + Android phones")
	fmt.Println("  push [path]                     detect framework + push to a wireless device")
	fmt.Println("  pair-android <ip:port> <code>   pair an Android 11+ phone over WiFi (one-time)")
	fmt.Println("  connect-android <ip:port>       reconnect a previously-paired Android phone")
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
	fmt.Println("Android one-time setup (per device, Android 11+):")
	fmt.Println("  1. On phone: Settings → Developer options → Wireless debugging → enable.")
	fmt.Println("  2. Tap 'Pair device with pairing code' to get IP:port + 6-digit code.")
	fmt.Println("  3. yaver wireless pair-android <ip:port> <code>")
	fmt.Println("  4. yaver wireless detect")
	fmt.Println()
	fmt.Println("Examples:")
	fmt.Println("  yaver wireless detect")
	fmt.Println("  yaver wireless push                   (cwd)")
	fmt.Println("  yaver wireless push ./mobile --platform android")
}

// ---------- detect ----------

func runWirelessDetect(args []string) {
	fs := flag.NewFlagSet("wireless detect", flag.ExitOnError)
	jsonOut := fs.Bool("json", false, "emit JSON")
	_ = fs.Parse(args)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	devices := listWirelessDevices(ctx)

	if *jsonOut {
		_ = json.NewEncoder(os.Stdout).Encode(devices)
		return
	}

	if len(devices) == 0 {
		fmt.Println("No WiFi-paired phones found.")
		fmt.Println()
		printWirelessDetectHints()
		return
	}

	fmt.Printf("%-10s  %-44s  %s\n", "PLATFORM", "UDID/SERIAL", "NAME")
	fmt.Printf("%-10s  %-44s  %s\n", "--------", "-----------", "----")
	for _, d := range devices {
		name := d.Name
		if name == "" {
			name = "(unknown)"
		}
		fmt.Printf("%-10s  %-44s  %s\n", d.Platform, d.UDID, name)
	}
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

func runWirelessPairAndroid(args []string) {
	if len(args) < 2 {
		fmt.Fprintln(os.Stderr, "yaver wireless pair-android <ip:port> <pairing-code>")
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, "On your phone (Android 11+):")
		fmt.Fprintln(os.Stderr, "  Settings → Developer options → Wireless debugging → Pair device with pairing code")
		fmt.Fprintln(os.Stderr, "Use the IP:port and 6-digit code shown there.")
		os.Exit(2)
	}
	ipPort := args[0]
	code := args[1]
	if _, err := exec.LookPath("adb"); err != nil {
		fmt.Fprintln(os.Stderr, "adb not found — `brew install android-platform-tools` (macOS) or install platform-tools.")
		os.Exit(2)
	}
	if !isAdbWirelessSerial(ipPort) {
		fmt.Fprintf(os.Stderr, "yaver wireless pair-android: %q does not look like ip:port\n", ipPort)
		os.Exit(2)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	fmt.Printf("→ adb pair %s\n", ipPort)
	cmd := exec.CommandContext(ctx, "adb", "pair", ipPort, code)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "\nadb pair failed: %v\n", err)
		fmt.Fprintln(os.Stderr, "Common causes: pairing code expired (regenerate on phone), not on same WiFi, firewall.")
		os.Exit(1)
	}
	fmt.Println()
	fmt.Println("Paired. Now run `yaver wireless connect-android <connection-ip:port>` with the")
	fmt.Println("Wireless debugging > IP address & Port (NOT the pairing port) shown on the phone.")
}

func runWirelessConnectAndroid(args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "yaver wireless connect-android <ip:port>")
		os.Exit(2)
	}
	ipPort := args[0]
	if _, err := exec.LookPath("adb"); err != nil {
		fmt.Fprintln(os.Stderr, "adb not found — `brew install android-platform-tools` (macOS) or install platform-tools.")
		os.Exit(2)
	}
	if !isAdbWirelessSerial(ipPort) {
		fmt.Fprintf(os.Stderr, "yaver wireless connect-android: %q does not look like ip:port\n", ipPort)
		os.Exit(2)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	fmt.Printf("→ adb connect %s\n", ipPort)
	cmd := exec.CommandContext(ctx, "adb", "connect", ipPort)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "\nadb connect failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Println()
	fmt.Println("Run `yaver wireless detect` to confirm the phone shows up.")
}
