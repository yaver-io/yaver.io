package main

// `yaver android <subcmd>` — short Android-only alias over the wireless +
// wire flows. The tablet/phone pairing dance lives under `yaver wireless`
// because the same machinery handles iOS WiFi devicectl too, but typing
// `yaver wireless setup-android` for what most users think of as "pair my
// Android" is verbose. This file delegates each verb to the existing
// implementations — no business logic of its own — so behavior, flags,
// and JSON output stay in one place per concept.
//
// Subcommands:
//   yaver android setup                          interactive pair + connect (mDNS-driven)
//   yaver android pair --ip <ip:port> --code <c> manual pair (one-time)
//   yaver android connect [--ip <ip:port>]       reconnect a paired phone (auto if --ip omitted)
//   yaver android list | devices                 list Android devices: paired + visible-unpaired
//   yaver android push [path]                    framework-detect + push to wireless Android device
//   yaver android visible                        what mDNS sees on the local network
//
// Wired Android (USB) keeps living under `yaver wire` — this namespace
// is wireless-first since that's the friction point.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"
)

func runAndroid(args []string) {
	if len(args) == 0 {
		androidUsage()
		os.Exit(2)
	}
	sub := args[0]
	rest := args[1:]
	switch sub {
	case "setup":
		runWirelessSetupAndroid(rest)
	case "pair":
		runWirelessPairAndroid(rest)
	case "connect":
		runWirelessConnectAndroid(rest)
	case "list", "ls", "devices", "detect":
		runAndroidList(rest)
	case "visible", "mdns":
		runAndroidVisible(rest)
	case "push", "run", "dev":
		runAndroidPush(rest)
	case "-h", "--help", "help":
		androidUsage()
	default:
		fmt.Fprintf(os.Stderr, "yaver android: unknown subcommand %q\n\n", sub)
		androidUsage()
		os.Exit(2)
	}
}

func androidUsage() {
	fmt.Println("yaver android — Android phone/tablet over WiFi (Android 11+ Wireless Debugging)")
	fmt.Println()
	fmt.Println("Subcommands:")
	fmt.Println("  setup                          interactive: discover → pair → connect → verify")
	fmt.Println("  pair --ip <ip:port> --code <c> manual one-time pairing")
	fmt.Println("  connect [--ip <ip:port>]       reconnect a paired device (auto-discovers if omitted)")
	fmt.Println("  list                           Android devices: paired + visible-unpaired")
	fmt.Println("  visible                        raw mDNS view (debug)")
	fmt.Println("  push [path]                    detect framework + push native build to wireless device")
	fmt.Println()
	fmt.Println("First-time setup, the easy way:")
	fmt.Println("  yaver android setup")
	fmt.Println()
	fmt.Println("Manual flow:")
	fmt.Println("  1. On phone: Settings → Developer options → Wireless debugging → ON")
	fmt.Println("  2. Tap 'Pair device with pairing code' (separate from the connect port).")
	fmt.Println("  3. yaver android pair --ip <pair-ip:port> --code <6-digits>")
	fmt.Println("  4. yaver android connect              (auto-finds the connect port via mDNS)")
	fmt.Println("  5. yaver android push                 (or `yaver wireless push --platform android`)")
}

// runAndroidList wraps `yaver wireless detect` and filters to Android.
// Same status/JSON shape so AI agents and scripts get a consistent view.
func runAndroidList(args []string) {
	jsonOut := false
	noInfo := false
	for _, a := range args {
		switch a {
		case "--json", "-j":
			jsonOut = true
		case "--no-info":
			noInfo = true
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	report := buildWirelessDetectReport(ctx)
	if !noInfo {
		enrichDetectReport(ctx, &report)
	}
	filtered := wirelessDetectReport{Hint: report.Hint}
	for _, d := range report.Devices {
		if d.Platform != "android" {
			continue
		}
		filtered.Devices = append(filtered.Devices, d)
		if d.Status == "paired" {
			filtered.PairedCount++
		} else {
			filtered.VisibleCount++
		}
	}
	if jsonOut {
		_ = json.NewEncoder(os.Stdout).Encode(filtered)
		return
	}
	if len(filtered.Devices) == 0 {
		fmt.Println("No Android devices found (paired or visible).")
		fmt.Println()
		fmt.Println("→ Enable Wireless debugging on the phone, then `yaver android setup`.")
		if filtered.Hint != "" {
			fmt.Println("→", filtered.Hint)
		}
		return
	}
	fmt.Printf("%-18s  %-44s  %s\n", "STATUS", "UDID/SERIAL", "DEVICE")
	fmt.Printf("%-18s  %-44s  %s\n", "------", "-----------", "------")
	for _, d := range filtered.Devices {
		fmt.Printf("%-18s  %-44s  %s\n", d.Status, d.UDID, wirelessRowLabel(d))
	}
	if filtered.VisibleCount > 0 {
		fmt.Println()
		fmt.Println("→ run `yaver android setup` to pair the visible-unpaired device(s).")
	}
}

// runAndroidVisible is a debug helper that dumps the raw `adb mdns
// services` view through Yaver's parser. Useful when pairing fails and
// you want to know whether adb sees the device at all.
func runAndroidVisible(args []string) {
	jsonOut := false
	for _, a := range args {
		if a == "--json" || a == "-j" {
			jsonOut = true
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	svcs := adbMdnsServices(ctx)
	if jsonOut {
		_ = json.NewEncoder(os.Stdout).Encode(svcs)
		return
	}
	if len(svcs) == 0 {
		fmt.Println("No mDNS-visible adb services. Possible causes:")
		fmt.Println("  - adb not installed (brew install android-platform-tools)")
		fmt.Println("  - phone's Wireless debugging is OFF")
		fmt.Println("  - phone and this Mac are on different WiFi networks / VLANs")
		return
	}
	fmt.Printf("%-30s  %-26s  %s\n", "INSTANCE", "SERVICE", "HOST:PORT")
	fmt.Printf("%-30s  %-26s  %s\n", "--------", "-------", "---------")
	for _, s := range svcs {
		fmt.Printf("%-30s  %-26s  %s\n", s.Name, s.Type, s.HostPort)
	}
}

// runAndroidPush is `yaver wireless push --platform android` with the
// platform pre-set so users iterating on an Android-only project don't
// have to repeat themselves.
func runAndroidPush(args []string) {
	full := append([]string{"--platform", "android"}, args...)
	runWirelessPush(full)
}
