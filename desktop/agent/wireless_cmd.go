package main

// `yaver wireless <subcmd>` — WiFi-paired iPhone/iPad install flows.
// Mirrors `yaver wire` but targets devices reachable over the local
// network instead of USB cable. Requires the device to be paired with
// the Mac via Xcode (cable initially) with "Connect via network"
// enabled in Window → Devices and Simulators. After that, devicectl
// auto-discovers the device whenever both are on the same WiFi.
//
// Subcommands:
//   yaver wireless detect       list every WiFi-paired iPhone/iPad
//   yaver wireless push [path]  detect framework + push to a wireless
//                               device (same build pipeline as wire push;
//                               only the device list source differs)
//
// Android wireless: separate flow (adb tcpip + adb connect <ip>:5555)
// — out of scope for v1; cable still required on Android.

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"runtime"
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
	case "-h", "--help", "help":
		wirelessUsage()
	default:
		fmt.Fprintf(os.Stderr, "yaver wireless: unknown subcommand %q\n\n", sub)
		wirelessUsage()
		os.Exit(2)
	}
}

func wirelessUsage() {
	fmt.Println("yaver wireless — WiFi-paired iPhone/iPad install flows")
	fmt.Println()
	fmt.Println("Subcommands:")
	fmt.Println("  detect                       list WiFi-paired iPhones/iPads")
	fmt.Println("  push [path]                  detect framework + push to wireless device")
	fmt.Println()
	fmt.Println("Push flags:")
	fmt.Println("  --device <udid>              pick a specific device (default: first wireless)")
	fmt.Println("  --config Debug|Release       xcode build configuration (default: Release)")
	fmt.Println("  --no-launch                  install without launching")
	fmt.Println()
	fmt.Println("Setup (one-time, per device):")
	fmt.Println("  1. Cable the iPhone to this Mac.")
	fmt.Println("  2. Open Xcode → Window → Devices and Simulators.")
	fmt.Println("  3. Select the device → check 'Connect via network'.")
	fmt.Println("  4. Unplug. The device shows up here over WiFi from now on.")
	fmt.Println()
	fmt.Println("Examples:")
	fmt.Println("  yaver wireless detect")
	fmt.Println("  yaver wireless push                   (cwd)")
	fmt.Println("  yaver wireless push ./mobile")
}

func runWirelessDetect(args []string) {
	fs := flag.NewFlagSet("wireless detect", flag.ExitOnError)
	jsonOut := fs.Bool("json", false, "emit JSON")
	_ = fs.Parse(args)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	devices := listIOSWirelessDevices(ctx)

	if *jsonOut {
		_ = json.NewEncoder(os.Stdout).Encode(devices)
		return
	}

	if len(devices) == 0 {
		fmt.Println("No WiFi-paired iOS devices found.")
		fmt.Println()
		if runtime.GOOS != "darwin" {
			fmt.Println("  iOS wireless detection requires macOS + Xcode (xcrun devicectl).")
			return
		}
		if _, err := exec.LookPath("xcrun"); err != nil {
			fmt.Println("  Install Xcode command line tools (xcrun missing).")
			return
		}
		fmt.Println("  - Make sure the device is on the same WiFi as this Mac.")
		fmt.Println("  - In Xcode → Window → Devices and Simulators, the device must show")
		fmt.Println("    'Connect via network' (one-time setup over cable).")
		fmt.Println("  - The device must be unlocked and 'Trust This Computer' must be granted.")
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

// runWirelessPush is a thin wrapper over the wire push pipeline that
// pre-resolves the device list to wireless-only candidates. Once a UDID
// is picked, the actual install (xcrun devicectl install + launch) is
// transport-agnostic — devicectl uses whichever transport the device is
// currently reachable over. So we just shell into runWirePush after
// injecting the right device list.
func runWirelessPush(args []string) {
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	devices := listIOSWirelessDevices(ctx)
	if len(devices) == 0 {
		fmt.Fprintln(os.Stderr, "No WiFi-paired iOS devices found. Run `yaver wireless detect` for setup help.")
		os.Exit(2)
	}

	// If --device wasn't passed, pre-fill it with the first wireless
	// device's UDID so the wire push device-resolver doesn't accidentally
	// pick a USB-cabled one.
	hasDeviceFlag := false
	for _, a := range args {
		if a == "--device" || (len(a) > 9 && a[:9] == "--device=") {
			hasDeviceFlag = true
			break
		}
	}
	if !hasDeviceFlag {
		args = append([]string{"--device", devices[0].UDID, "--platform", "ios"}, args...)
	} else {
		// User explicitly named a device; still force --platform ios so
		// the wire push doesn't try to dispatch to Android even if the
		// project supports both.
		hasPlatform := false
		for _, a := range args {
			if a == "--platform" || (len(a) > 11 && a[:11] == "--platform=") {
				hasPlatform = true
				break
			}
		}
		if !hasPlatform {
			args = append([]string{"--platform", "ios"}, args...)
		}
	}

	runWirePush(args)
}
