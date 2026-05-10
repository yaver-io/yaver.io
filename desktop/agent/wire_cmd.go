package main

// `yaver wire <subcmd>` — cable-attached phone/tablet flows.
//
// Subcommands:
//   yaver wire detect              list every iPhone/iPad/Android attached over USB
//   yaver wire push [path]         detect framework in `path` (default cwd) and
//                                  push it to a cable-attached device. Always
//                                  builds a self-contained native binary — no
//                                  Metro / expo dev server is ever spawned.
//                                    - Expo / RN iOS     → xcodebuild + xcrun devicectl
//                                    - Expo / RN Android → ./gradlew installRelease + adb launch
//                                    - Flutter           → flutter run --release -d <id>
//                                    - Native iOS        → xcodebuild + xcrun devicectl
//                                    - Native Android    → ./gradlew installRelease + adb launch
//
// Flags (push):
//   --device <udid|serial>   pick a specific device when more than one is attached
//   --platform ios|android   force a platform when the project supports both
//   --config Debug|Release   xcode/gradle build configuration (default: Release)
//   --no-launch              install without launching after push

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"
)

func runWire(args []string) {
	if len(args) == 0 {
		wireUsage()
		os.Exit(2)
	}
	sub := args[0]
	rest := args[1:]
	switch sub {
	case "detect", "list", "ls", "devices":
		runWireDetect(rest)
	case "push", "run", "dev":
		runWirePush(rest)
	case "-h", "--help", "help":
		wireUsage()
	default:
		fmt.Fprintf(os.Stderr, "yaver wire: unknown subcommand %q\n\n", sub)
		wireUsage()
		os.Exit(2)
	}
}

func wireUsage() {
	fmt.Println("yaver wire — cable-attached phone/tablet install flows")
	fmt.Println()
	fmt.Println("Subcommands:")
	fmt.Println("  detect                       list iPhones/iPads (xcrun) + Android (adb)")
	fmt.Println("  push [path]                  detect framework + push to cable-attached device")
	fmt.Println()
	fmt.Println("Push flags:")
	fmt.Println("  --device <udid|serial>       pick a specific device (default: first attached)")
	fmt.Println("  --platform ios|android       force platform when the project supports both")
	fmt.Println("  --config Debug|Release       xcode/gradle build configuration (default: Release)")
	fmt.Println("  --no-launch                  install without launching")
	fmt.Println()
	fmt.Println("Always builds a self-contained native binary via xcodebuild / gradle.")
	fmt.Println("JS is bundled into the .app/.apk at build time — no Metro / expo dev server")
	fmt.Println("is ever spawned. For Metro-driven iteration, use Xcode or Android Studio")
	fmt.Println("directly.")
	fmt.Println()
	fmt.Println("Frameworks supported:")
	fmt.Println("  expo / react-native, flutter, native iOS (xcodeproj), native Android (gradle)")
	fmt.Println()
	fmt.Println("Examples:")
	fmt.Println("  yaver wire detect")
	fmt.Println("  yaver wire push                   (cwd)")
	fmt.Println("  yaver wire push ./mobile")
	fmt.Println("  yaver wire push --platform android --device R5CT123")
}

// ---------- detect ----------

type wireDevice struct {
	UDID     string `json:"udid"`
	Name     string `json:"name"`
	Platform string `json:"platform"` // "ios" | "android"
	OS       string `json:"os,omitempty"`
	// Info is optionally populated by enrichWireDevices() — empty
	// pointer means "not enriched yet" (vs zero-value struct, which
	// would mean "we tried and got nothing back"). The CLI's --info
	// flag, the mobile devices section, and MCP detect tools all
	// populate this; raw `adb devices -l` listings leave it nil.
	Info *mobileDeviceInfo `json:"info,omitempty"`
}

func runWireDetect(args []string) {
	fs := flag.NewFlagSet("wire detect", flag.ExitOnError)
	jsonOut := fs.Bool("json", false, "emit JSON")
	noInfo := fs.Bool("no-info", false, "skip device info enrichment (faster, fewer adb/xcrun calls)")
	_ = fs.Parse(args)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	devices := append([]wireDevice{}, listIOSWireDevices(ctx)...)
	devices = append(devices, listAndroidWireDevices(ctx)...)
	if !*noInfo {
		enrichWireDevices(ctx, devices, 4)
	}

	if *jsonOut {
		_ = json.NewEncoder(os.Stdout).Encode(devices)
		return
	}

	if len(devices) == 0 {
		fmt.Println("No cable-attached devices found.")
		fmt.Println()
		if runtime.GOOS != "darwin" {
			fmt.Println("  iOS detection requires macOS + Xcode (xcrun devicectl).")
		} else if _, err := exec.LookPath("xcrun"); err != nil {
			fmt.Println("  iOS: install Xcode command line tools (xcrun missing).")
		}
		if _, err := exec.LookPath("adb"); err != nil {
			fmt.Println("  Android: install platform-tools (adb missing). brew install android-platform-tools")
		}
		return
	}

	fmt.Printf("%-10s  %-44s  %s\n", "PLATFORM", "UDID/SERIAL", "DEVICE")
	fmt.Printf("%-10s  %-44s  %s\n", "--------", "-----------", "------")
	for _, d := range devices {
		label := wireRowLabel(d)
		fmt.Printf("%-10s  %-44s  %s\n", d.Platform, d.UDID, label)
	}
}

// wireRowLabel mirrors wirelessRowLabel for the cable-attached path:
// enriched summary when we have one, else the raw name.
func wireRowLabel(d wireDevice) string {
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

// listIOSWireDevices runs `xcrun devicectl list devices` (Xcode 15+) and
// falls back to `xcrun xctrace list devices`. Returns USB-attached
// iPhones/iPads only.
func listIOSWireDevices(ctx context.Context) []wireDevice {
	if runtime.GOOS != "darwin" {
		return nil
	}
	if _, err := exec.LookPath("xcrun"); err != nil {
		return nil
	}

	if devs := iosDevicesFromDevicectl(ctx, iosTransportWired); len(devs) > 0 {
		return devs
	}
	return iosDevicesFromXctrace(ctx)
}

// listIOSWirelessDevices returns iPhones/iPads paired over WiFi. The
// device must have been paired via Xcode (cable initially) with
// "Connect via network" enabled — once that's set, devicectl reports
// the device whenever it's on the same network as the Mac with
// transportType containing "wireless"/"network". xcrun devicectl install
// + launch work the same regardless of transport, so the rest of the
// push pipeline is shared with the wired path.
func listIOSWirelessDevices(ctx context.Context) []wireDevice {
	if runtime.GOOS != "darwin" {
		return nil
	}
	if _, err := exec.LookPath("xcrun"); err != nil {
		return nil
	}
	return iosDevicesFromDevicectl(ctx, iosTransportWireless)
}

type iosTransportFilter int

const (
	iosTransportWired iosTransportFilter = iota
	iosTransportWireless
)

func iosDevicesFromDevicectl(ctx context.Context, filter iosTransportFilter) []wireDevice {
	tmp, err := os.CreateTemp("", "yaver-wire-devices-*.json")
	if err != nil {
		return nil
	}
	tmpPath := tmp.Name()
	tmp.Close()
	defer os.Remove(tmpPath)

	cmd := exec.CommandContext(ctx, "xcrun", "devicectl", "list", "devices",
		"--json-output", tmpPath, "--quiet")
	if err := cmd.Run(); err != nil {
		return nil
	}
	data, err := os.ReadFile(tmpPath)
	if err != nil || len(data) == 0 {
		return nil
	}
	var raw struct {
		Result struct {
			Devices []struct {
				Identifier           string `json:"identifier"`
				ConnectionState      string `json:"connectionState"`
				ConnectionProperties struct {
					TransportType string `json:"transportType"`
				} `json:"connectionProperties"`
				DeviceProperties struct {
					Name            string `json:"name"`
					OSVersionNumber string `json:"osVersionNumber"`
					DeviceClass     string `json:"deviceClass"`
				} `json:"deviceProperties"`
				HardwareProperties struct {
					DeviceType  string `json:"deviceType"`
					ProductType string `json:"productType"`
					UDID        string `json:"udid"`
				} `json:"hardwareProperties"`
			} `json:"devices"`
		} `json:"result"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil
	}
	out := make([]wireDevice, 0, len(raw.Result.Devices))
	for _, d := range raw.Result.Devices {
		// skip non-iOS devices (Apple TV, Vision Pro etc come through too).
		dt := strings.ToLower(d.HardwareProperties.DeviceType + " " + d.DeviceProperties.DeviceClass)
		if !(strings.Contains(dt, "iphone") || strings.Contains(dt, "ipad")) {
			continue
		}
		// Filter by transport type per caller's request.
		// devicectl reports "wired" / "usb" for cable, and "wireless" /
		// "network" for WiFi-paired devices. An empty transportType
		// usually means a stale or just-connected entry; keep it on the
		// wired path (legacy behavior) and drop on the wireless path
		// (to avoid showing offline cable-paired entries).
		tt := strings.ToLower(d.ConnectionProperties.TransportType)
		switch filter {
		case iosTransportWired:
			if tt != "" && !strings.Contains(tt, "wired") && !strings.Contains(tt, "usb") {
				continue
			}
		case iosTransportWireless:
			if !strings.Contains(tt, "wireless") && !strings.Contains(tt, "network") &&
				!strings.Contains(tt, "wifi") {
				continue
			}
		}
		udid := d.HardwareProperties.UDID
		if udid == "" {
			udid = d.Identifier
		}
		if udid == "" {
			continue
		}
		out = append(out, wireDevice{
			UDID:     udid,
			Name:     strings.TrimSpace(d.DeviceProperties.Name),
			Platform: "ios",
			OS:       d.DeviceProperties.OSVersionNumber,
		})
	}
	return out
}

func iosDevicesFromXctrace(ctx context.Context) []wireDevice {
	out, err := exec.CommandContext(ctx, "xcrun", "xctrace", "list", "devices").CombinedOutput()
	if err != nil {
		return nil
	}
	var devs []wireDevice
	inSimulators := false
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "== Simulators ==" {
			inSimulators = true
			continue
		}
		if line == "== Devices ==" || line == "== Device Offline ==" {
			inSimulators = false
			continue
		}
		if inSimulators || line == "" || strings.HasPrefix(line, "==") {
			continue
		}
		// Skip Mac entries.
		if strings.Contains(line, "MacBook") || strings.Contains(line, "iMac") ||
			strings.Contains(line, "Mac ") || strings.Contains(line, "Mac mini") ||
			strings.Contains(line, "Mac Pro") || strings.Contains(line, "Mac Studio") {
			continue
		}
		idx := strings.LastIndex(line, "(")
		if idx <= 0 {
			continue
		}
		udid := strings.TrimSuffix(line[idx+1:], ")")
		if len(udid) < 20 || strings.Contains(udid, " ") || strings.Contains(udid, ".") {
			continue
		}
		head := strings.TrimSpace(line[:idx])
		// pull off trailing "(<os>)" if present
		osVer := ""
		if op := strings.LastIndex(head, "("); op > 0 {
			osVer = strings.TrimSuffix(head[op+1:], ")")
			head = strings.TrimSpace(head[:op])
		}
		devs = append(devs, wireDevice{
			UDID:     udid,
			Name:     strings.TrimSpace(head),
			Platform: "ios",
			OS:       osVer,
		})
	}
	return devs
}

func listAndroidWireDevices(ctx context.Context) []wireDevice {
	return androidDevicesFromAdb(ctx, false)
}

// listAndroidWirelessDevices returns Android devices currently reachable
// over WiFi via `adb connect`. adb reports wireless-paired devices with
// an IP:port serial (e.g. `192.168.1.42:5555`) — that's what we filter on.
//
// Pre-condition (one-time per device):
//   - Android 11+: enable Wireless Debugging in Developer Options, run
//     `yaver wireless pair-android <ip>:<port> <code>` to pair.
//   - Older Android: cable + `adb tcpip 5555`, then `adb connect <ip>:5555`.
//
// Once paired, subsequent reboots usually re-attach automatically as long
// as the phone is on the same WiFi.
func listAndroidWirelessDevices(ctx context.Context) []wireDevice {
	return androidDevicesFromAdb(ctx, true)
}

// androidDevicesFromAdb shells out to `adb devices -l` and returns either
// the wired (USB) entries or the wireless (IP:port) entries. Emulators
// (`emulator-*`) are always dropped.
//
// Wireless dedupe: Android 11+ adb often reports the same physical device
// twice — once as `IP:port` (after `adb connect`) and once as the mDNS
// hostname (`adb-<serial>-...._adb-tls-connect.`). When both forms point
// at the same `device:` qualifier, we keep only the IP:port form so the
// UI doesn't show two rows for one tablet.
func androidDevicesFromAdb(ctx context.Context, wireless bool) []wireDevice {
	if _, err := exec.LookPath("adb"); err != nil {
		return nil
	}
	out, err := exec.CommandContext(ctx, "adb", "devices", "-l").Output()
	if err != nil {
		return nil
	}
	type rawDev struct {
		dev      wireDevice
		deviceID string // value of `device:<id>` qualifier (model code)
		isMDNS   bool
	}
	var raws []rawDev
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "List of devices") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 || fields[1] != "device" {
			continue
		}
		serial := fields[0]
		if strings.HasPrefix(serial, "emulator-") {
			continue
		}
		isWireless := isAdbWirelessSerial(serial)
		if wireless != isWireless {
			continue
		}
		name := ""
		deviceID := ""
		for _, f := range fields[2:] {
			switch {
			case strings.HasPrefix(f, "model:"):
				name = strings.ReplaceAll(strings.TrimPrefix(f, "model:"), "_", " ")
			case strings.HasPrefix(f, "device:"):
				deviceID = strings.TrimPrefix(f, "device:")
			}
		}
		raws = append(raws, rawDev{
			dev: wireDevice{
				UDID:     serial,
				Name:     name,
				Platform: "android",
			},
			deviceID: deviceID,
			isMDNS:   strings.Contains(serial, "._adb-tls-connect.") || strings.Contains(serial, "._adb-tls-pairing."),
		})
	}
	if !wireless {
		devs := make([]wireDevice, 0, len(raws))
		for _, r := range raws {
			devs = append(devs, r.dev)
		}
		return devs
	}
	// Wireless dedupe: prefer IP:port form over mDNS form when the same
	// device qualifier appears under both.
	hasIP := map[string]bool{}
	for _, r := range raws {
		if !r.isMDNS && r.deviceID != "" {
			hasIP[r.deviceID] = true
		}
	}
	devs := make([]wireDevice, 0, len(raws))
	for _, r := range raws {
		if r.isMDNS && r.deviceID != "" && hasIP[r.deviceID] {
			continue
		}
		devs = append(devs, r.dev)
	}
	return devs
}

// adbMdnsService is one entry in `adb mdns services` output. adb's mDNS
// browser surfaces every Android 11+ device on the local network whose
// Wireless Debugging is enabled — paired or not. We use this to spot
// "visible but unpaired" tablets so `yaver wireless detect` can hint
// the user toward `yaver wireless setup-android` instead of leaving
// them staring at an empty list.
type adbMdnsService struct {
	Name     string `json:"name"`      // e.g. adb-R52W60BEDXD-x6aGBg
	Serial   string `json:"serial"`    // R52W60BEDXD (extracted from name)
	Type     string `json:"type"`      // _adb-tls-connect._tcp or _adb-tls-pairing._tcp
	HostPort string `json:"host_port"` // 192.168.1.42:39869 or 0.0.0.0:NNNN
}

// adbMdnsServices runs `adb mdns services` and parses the result.
// adb only enumerates devices on the local link, so this is empty when
// the agent and the phone aren't on the same WiFi/subnet — that's the
// correct signal to surface to the user.
//
// adb sometimes reports `0.0.0.0` as the host for the pairing service
// (it's a local-link bind quirk); the connect-service entry for the
// same device usually has the real LAN IP, and repairAdbMdnsHost()
// patches the pairing entry from that.
func adbMdnsServices(ctx context.Context) []adbMdnsService {
	if _, err := exec.LookPath("adb"); err != nil {
		return nil
	}
	out, err := exec.CommandContext(ctx, "adb", "mdns", "services").Output()
	if err != nil {
		return nil
	}
	var svcs []adbMdnsService
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "List of discovered") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		name, typ, hp := fields[0], fields[1], fields[2]
		if !strings.HasPrefix(typ, "_adb-tls-") {
			continue
		}
		svcs = append(svcs, adbMdnsService{
			Name:     name,
			Serial:   adbSerialFromMdnsName(name),
			Type:     typ,
			HostPort: hp,
		})
	}
	return repairAdbMdnsHost(svcs)
}

// adbSerialFromMdnsName pulls the device serial out of an mDNS instance
// name. Format is `adb-<SERIAL>-<random>`. Returns "" on no match so the
// caller can fall back to the full instance name.
func adbSerialFromMdnsName(name string) string {
	if !strings.HasPrefix(name, "adb-") {
		return ""
	}
	rest := strings.TrimPrefix(name, "adb-")
	// The trailing random suffix is separated by '-'. The serial itself
	// never contains '-' on the devices we've seen (Samsung, Pixel,
	// OnePlus) so first-dash split is safe.
	if dash := strings.LastIndex(rest, "-"); dash > 0 {
		return rest[:dash]
	}
	return rest
}

// repairAdbMdnsHost substitutes the real LAN IP from a device's connect
// service into its pairing service when the latter reports `0.0.0.0`.
// Same `adb-<serial>-<suffix>` instance name on both sides identifies
// them as the same device.
func repairAdbMdnsHost(svcs []adbMdnsService) []adbMdnsService {
	hostByName := map[string]string{}
	for _, s := range svcs {
		if s.Type == "_adb-tls-connect._tcp" && !strings.HasPrefix(s.HostPort, "0.0.0.0:") {
			hostByName[s.Name] = strings.SplitN(s.HostPort, ":", 2)[0]
		}
	}
	out := make([]adbMdnsService, len(svcs))
	for i, s := range svcs {
		if strings.HasPrefix(s.HostPort, "0.0.0.0:") {
			if host, ok := hostByName[s.Name]; ok {
				port := strings.TrimPrefix(s.HostPort, "0.0.0.0:")
				s.HostPort = host + ":" + port
			}
		}
		out[i] = s
	}
	return out
}

// enrichWireDevices fans out to xcrun/adb in parallel and attaches a
// mobileDeviceInfo to each entry whose Info is still nil. Bounded by
// `parallel` concurrent shells (default 4) — xcrun's coredevice daemon
// serializes anyway, but parallelizing across the iOS/Android boundary
// keeps total wall time near max(slowest_ios, slowest_android).
func enrichWireDevices(ctx context.Context, devs []wireDevice, parallel int) {
	if parallel <= 0 {
		parallel = 4
	}
	sem := make(chan struct{}, parallel)
	var wg sync.WaitGroup
	for i := range devs {
		if devs[i].Info != nil {
			continue
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(idx int) {
			defer wg.Done()
			defer func() { <-sem }()
			info := enrichMobileDevice(ctx, devs[idx])
			devs[idx].Info = &info
		}(i)
	}
	wg.Wait()
}

// adbPair runs `adb pair <ip:port> <code>`. Returns combined output for
// the caller to surface; non-nil error includes the exit failure.
func adbPair(ctx context.Context, ipPort, code string) (string, error) {
	if _, err := exec.LookPath("adb"); err != nil {
		return "", fmt.Errorf("adb not found")
	}
	out, err := exec.CommandContext(ctx, "adb", "pair", ipPort, code).CombinedOutput()
	return string(out), err
}

// adbConnect runs `adb connect <ip:port>`. adb returns exit 0 even when
// the connect failed — its only signal is the stdout text. We treat any
// "failed", "cannot", or "unauthorized" line as a real error.
func adbConnect(ctx context.Context, ipPort string) (string, error) {
	if _, err := exec.LookPath("adb"); err != nil {
		return "", fmt.Errorf("adb not found")
	}
	out, err := exec.CommandContext(ctx, "adb", "connect", ipPort).CombinedOutput()
	s := string(out)
	if err != nil {
		return s, err
	}
	low := strings.ToLower(s)
	for _, marker := range []string{"failed to connect", "cannot connect", "unauthorized", "no such device"} {
		if strings.Contains(low, marker) {
			return s, fmt.Errorf("adb connect did not establish: %s", strings.TrimSpace(s))
		}
	}
	return s, nil
}

// isAdbWirelessSerial reports whether an adb serial looks like a
// wireless-paired device (IP:port). True positives:
//   - 192.168.1.42:5555
//   - 10.0.0.5:37123
//   - [fe80::1%en0]:5555  (rare IPv6 case)
//
// False positives we explicitly avoid: `adb-XXXXXX-YYY._adb-tls-connect.`
// mDNS hostnames returned by some adb versions on Android 11+ also
// represent wireless devices, so treat those as wireless too.
func isAdbWirelessSerial(serial string) bool {
	if strings.Contains(serial, "._adb-tls-connect.") || strings.Contains(serial, "._adb-tls-pairing.") {
		return true
	}
	// IPv6 form: [...]:port
	if strings.HasPrefix(serial, "[") && strings.Contains(serial, "]:") {
		return true
	}
	// IPv4 form: must contain at least one dot AND end with :<digits>.
	colon := strings.LastIndex(serial, ":")
	if colon <= 0 || colon == len(serial)-1 {
		return false
	}
	host, port := serial[:colon], serial[colon+1:]
	if !strings.Contains(host, ".") {
		return false
	}
	for _, r := range port {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// ---------- push ----------

type wirePushOpts struct {
	device   string
	platform string
	config   string // "Debug" | "Release"
	noLaunch bool
}

// release reports whether the current build configuration is Release.
// Used by the platform-specific push paths to choose installRelease vs
// installDebug, .app vs Debug-iphoneos paths, etc.
func (o wirePushOpts) release() bool {
	return strings.EqualFold(o.config, "Release")
}

func runWirePush(args []string) {
	fs := flag.NewFlagSet("wire push", flag.ExitOnError)
	opts := wirePushOpts{}
	fs.StringVar(&opts.device, "device", "", "specific device UDID/serial")
	fs.StringVar(&opts.platform, "platform", "", "ios|android — force platform when both are supported")
	fs.StringVar(&opts.config, "config", "Release", "xcode/gradle build configuration: Debug|Release")
	fs.BoolVar(&opts.noLaunch, "no-launch", false, "install without launching")
	_ = fs.Parse(args)
	// Normalise + validate the config name.
	switch strings.ToLower(opts.config) {
	case "debug":
		opts.config = "Debug"
	case "release", "":
		opts.config = "Release"
	default:
		fmt.Fprintf(os.Stderr, "yaver wire push: --config must be Debug or Release (got %q)\n", opts.config)
		os.Exit(2)
	}

	root := fs.Arg(0)
	if root == "" {
		var err error
		root, err = os.Getwd()
		if err != nil {
			fmt.Fprintf(os.Stderr, "yaver wire push: cannot determine cwd: %v\n", err)
			os.Exit(2)
		}
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "yaver wire push: bad path %q: %v\n", root, err)
		os.Exit(2)
	}

	projectRoot, stack := resolveMobileProject(abs)
	if stack == "" {
		fmt.Fprintf(os.Stderr, "yaver wire push: no mobile project detected at %s (or its common subdirs)\n", abs)
		fmt.Fprintln(os.Stderr, "  Searched: . , ./mobile, ./app, ./apps/*, ./packages/*")
		fmt.Fprintln(os.Stderr, "  Looked for: pubspec.yaml (Flutter), app.json/package.json with expo or react-native, ios/*.xcodeproj, android/build.gradle")
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
		fmt.Fprintf(os.Stderr, "yaver wire push: --platform must be ios or android (got %q)\n", platform)
		os.Exit(2)
	}

	ctx, cancel := signalContext()
	defer cancel()

	device, err := pickWireDevice(ctx, platform, opts.device)
	if err != nil {
		fmt.Fprintf(os.Stderr, "yaver wire push: %v\n", err)
		os.Exit(2)
	}

	fmt.Printf("→ project:  %s\n", abs)
	fmt.Printf("→ stack:    %s\n", stack)
	fmt.Printf("→ platform: %s\n", platform)
	fmt.Printf("→ device:   %s  (%s)\n", device.UDID, displayName(device))
	fmt.Println()

	if err := dispatchWirePush(ctx, abs, stack, platform, device, opts); err != nil {
		fmt.Fprintf(os.Stderr, "\nyaver wire push: %v\n", err)
		os.Exit(1)
	}
}

// resolveMobileProject locates the actual mobile project starting from
// `start`. If `start` itself is a mobile project, returns (start, stack).
// Otherwise walks one level into common candidate dirs (mobile/, app/,
// apps/*, packages/*) and returns the first match. If exactly zero
// candidates are found, returns ("", ""). If multiple are found, prints
// them and returns the first to keep flow simple — user can re-run
// with an explicit path.
func resolveMobileProject(start string) (root, stack string) {
	if s := detectMobileStack(start); s != "" {
		return start, s
	}
	candidates := []string{
		filepath.Join(start, "mobile"),
		filepath.Join(start, "app"),
	}
	for _, parent := range []string{"apps", "packages"} {
		entries, err := os.ReadDir(filepath.Join(start, parent))
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() {
				candidates = append(candidates, filepath.Join(start, parent, e.Name()))
			}
		}
	}
	type hit struct {
		dir   string
		stack string
	}
	var hits []hit
	for _, c := range candidates {
		if !wireExists(c) {
			continue
		}
		if s := detectMobileStack(c); s != "" {
			hits = append(hits, hit{c, s})
		}
	}
	if len(hits) == 0 {
		return "", ""
	}
	if len(hits) > 1 {
		fmt.Fprintln(os.Stderr, "yaver wire push: multiple mobile projects found — picking first. Re-run with an explicit path to choose:")
		for _, h := range hits {
			fmt.Fprintf(os.Stderr, "    %s   (%s)\n", h.dir, h.stack)
		}
		fmt.Fprintln(os.Stderr)
	}
	return hits[0].dir, hits[0].stack
}

// detectMobileStack returns one of: "expo", "react-native", "flutter",
// "native-ios", "native-android", or "" if nothing recognisable is found.
// Order matters — Expo wins over bare RN, native fallbacks are last.
func detectMobileStack(root string) string {
	if wireExists(filepath.Join(root, "pubspec.yaml")) {
		return "flutter"
	}
	pkgPath := filepath.Join(root, "package.json")
	if data, err := os.ReadFile(pkgPath); err == nil {
		// crude json parse — we only need a few keys.
		var pkg struct {
			Dependencies    map[string]string `json:"dependencies"`
			DevDependencies map[string]string `json:"devDependencies"`
		}
		if err := json.Unmarshal(data, &pkg); err == nil {
			if _, ok := pkg.Dependencies["expo"]; ok {
				return "expo"
			}
			if _, ok := pkg.DevDependencies["expo"]; ok {
				return "expo"
			}
			if _, ok := pkg.Dependencies["react-native"]; ok {
				return "react-native"
			}
		}
	}
	// Native fall-throughs.
	if hasXcodeproj(filepath.Join(root, "ios")) || hasXcodeproj(root) {
		return "native-ios"
	}
	if wireExists(filepath.Join(root, "android", "build.gradle")) ||
		wireExists(filepath.Join(root, "android", "build.gradle.kts")) ||
		wireExists(filepath.Join(root, "build.gradle")) {
		return "native-android"
	}
	return ""
}

func hasXcodeproj(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if e.IsDir() && strings.HasSuffix(e.Name(), ".xcworkspace") {
			return true
		}
	}
	for _, e := range entries {
		if e.IsDir() && strings.HasSuffix(e.Name(), ".xcodeproj") {
			return true
		}
	}
	return false
}

func wireExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// wireFileExists checks for a regular file (not just any path).
func wireFileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

func pickPlatformForStack(stack string) string {
	switch stack {
	case "native-ios":
		return "ios"
	case "native-android":
		return "android"
	}
	if runtime.GOOS == "darwin" {
		return "ios"
	}
	return "android"
}

func pickWireDevice(ctx context.Context, platform, want string) (wireDevice, error) {
	var devs []wireDevice
	switch platform {
	case "ios":
		devs = listIOSWireDevices(ctx)
	case "android":
		devs = listAndroidWireDevices(ctx)
	}
	if len(devs) == 0 {
		switch platform {
		case "ios":
			return wireDevice{}, fmt.Errorf("no cable-attached iPhone/iPad found — connect over USB and trust the Mac, then `yaver wire detect`")
		case "android":
			return wireDevice{}, fmt.Errorf("no cable-attached Android device found — enable USB debugging and accept the trust prompt, then `yaver wire detect`")
		}
	}
	if want != "" {
		for _, d := range devs {
			if d.UDID == want || strings.EqualFold(d.Name, want) {
				return d, nil
			}
		}
		return wireDevice{}, fmt.Errorf("device %q not attached — `yaver wire detect` shows %d %s device(s)", want, len(devs), platform)
	}
	if len(devs) > 1 {
		fmt.Fprintf(os.Stderr, "yaver wire push: %d %s devices attached — picking first. Use --device to choose:\n", len(devs), platform)
		for _, d := range devs {
			fmt.Fprintf(os.Stderr, "    %s  %s\n", d.UDID, displayName(d))
		}
		fmt.Fprintln(os.Stderr)
	}
	return devs[0], nil
}

func displayName(d wireDevice) string {
	if d.Name == "" {
		return "(unknown)"
	}
	return d.Name
}

func dispatchWirePush(ctx context.Context, root, stack, platform string, dev wireDevice, opts wirePushOpts) error {
	switch stack {
	case "expo", "react-native":
		return wirePushExpoOrRN(ctx, root, stack, platform, dev, opts)
	case "flutter":
		return wirePushFlutter(ctx, root, dev, opts)
	case "native-ios":
		if platform != "ios" {
			return fmt.Errorf("native-ios project but platform=%s", platform)
		}
		return wirePushNativeIOS(ctx, root, dev, opts)
	case "native-android":
		if platform != "android" {
			return fmt.Errorf("native-android project but platform=%s", platform)
		}
		return wirePushNativeAndroid(ctx, root, dev, opts)
	}
	return fmt.Errorf("unsupported stack %q", stack)
}

func wirePushExpoOrRN(ctx context.Context, root, stack, platform string, dev wireDevice, opts wirePushOpts) error {
	// Always go straight through xcodebuild / gradle. JS gets baked
	// into the app at build time so no Metro / expo dev server is
	// needed at runtime — this avoids the "Could not connect to
	// development server" red screen entirely.
	if err := ensureNativeProjectDirs(root, stack); err != nil {
		return err
	}
	if platform == "ios" {
		iosDir := filepath.Join(root, "ios")
		return wirePushNativeIOS(ctx, iosDir, dev, opts)
	}
	return wirePushNativeAndroid(ctx, root, dev, opts)
}

// ensureNativeProjectDirs verifies that ios/ or android/ have been
// generated by `npx expo prebuild`. RN+Expo projects gitignore these
// dirs by default, so we run the prebuild step on demand.
func ensureNativeProjectDirs(root, stack string) error {
	needsPrebuild := false
	if !wireExists(filepath.Join(root, "ios")) || !wireExists(filepath.Join(root, "android")) {
		needsPrebuild = true
	}
	if !needsPrebuild {
		return nil
	}
	if stack != "expo" {
		// Bare RN should already have ios/ and android/ committed.
		return fmt.Errorf("missing ios/ or android/ directory at %s — run `npx react-native init` or commit the platform dirs", root)
	}
	if _, err := exec.LookPath("npx"); err != nil {
		return fmt.Errorf("npx not found — install Node.js")
	}
	fmt.Println("→ ios/ or android/ missing — running expo prebuild...")
	cmd := exec.Command("npx", "expo", "prebuild", "--no-install")
	cmd.Dir = root
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("expo prebuild failed: %w", err)
	}
	return nil
}

func wirePushFlutter(ctx context.Context, root string, dev wireDevice, opts wirePushOpts) error {
	if _, err := exec.LookPath("flutter"); err != nil {
		return fmt.Errorf("flutter CLI not found — install Flutter SDK first")
	}
	args := []string{"run", "-d", dev.UDID}
	if opts.release() {
		args = append(args, "--release")
	}
	fmt.Printf("$ flutter %s\n", strings.Join(args, " "))
	return runStreaming(ctx, root, "flutter", args, nil)
}

func wirePushNativeIOS(ctx context.Context, root string, dev wireDevice, opts wirePushOpts) error {
	if runtime.GOOS != "darwin" {
		return fmt.Errorf("native iOS push requires macOS + Xcode")
	}
	if _, err := exec.LookPath("xcodebuild"); err != nil {
		return fmt.Errorf("xcodebuild not found — install Xcode")
	}
	cfg := opts.config
	// Build for the device, then install + (optionally) launch.
	derived := filepath.Join(os.TempDir(), "yaver-wire-derived-"+filepath.Base(root))
	scheme, ws, proj, err := pickXcodeTarget(root)
	if err != nil {
		return err
	}
	buildArgs := []string{
		"-scheme", scheme,
		"-configuration", cfg,
		"-destination", "id=" + dev.UDID,
		"-derivedDataPath", derived,
		"-allowProvisioningUpdates",
		"build",
	}
	// App Store Connect API key, when present, lets xcodebuild auto-
	// register new capabilities (e.g. Associated Domains) on the App ID
	// and regenerate the provisioning profile without an interactive
	// Xcode session. Mirrors what scripts/deploy-testflight.sh does for
	// the archive path. Reads from env first, falls back to yaver vault
	// (project=mobile). Skips silently when the key is missing — keeps
	// the existing "no credentials, fail loud at xcodebuild" behavior
	// intact rather than introducing a new dependency.
	if authPath, authID, authIssuer := resolveAppStoreConnectKey(); authPath != "" && authID != "" && authIssuer != "" {
		buildArgs = append(buildArgs,
			"-authenticationKeyPath", authPath,
			"-authenticationKeyID", authID,
			"-authenticationKeyIssuerID", authIssuer,
		)
	}
	if ws != "" {
		buildArgs = append([]string{"-workspace", ws}, buildArgs...)
	} else if proj != "" {
		buildArgs = append([]string{"-project", proj}, buildArgs...)
	}
	fmt.Printf("$ xcodebuild %s\n", strings.Join(buildArgs, " "))
	if err := runStreaming(ctx, root, "xcodebuild", buildArgs, nil); err != nil {
		return fmt.Errorf("xcodebuild failed: %w", err)
	}
	appPath := findAppInDerivedData(root)
	if appPath == "" {
		// derived path lookup
		patterns := []string{
			filepath.Join(derived, "Build", "Products", cfg+"-iphoneos", "*.app"),
		}
		appPath = detectArtifact("", patterns)
	}
	if appPath == "" {
		return fmt.Errorf("could not locate built .app under %s", derived)
	}
	fmt.Printf("→ installing %s\n", appPath)
	if _, err := installAppOnDevice(ctx, appPath); err != nil {
		return err
	}
	if opts.noLaunch {
		return nil
	}
	bid := readBundleIDFromApp(appPath)
	if bid == "" {
		fmt.Println("(skipping launch — no CFBundleIdentifier found)")
		return nil
	}
	return launchAppOnDevice(ctx, dev.UDID, bid)
}

func wirePushNativeAndroid(ctx context.Context, root string, dev wireDevice, opts wirePushOpts) error {
	gradlew := filepath.Join(root, "gradlew")
	androidDir := root
	if !wireExists(gradlew) {
		gradlew = filepath.Join(root, "android", "gradlew")
		androidDir = filepath.Join(root, "android")
	}
	if !wireExists(gradlew) {
		return fmt.Errorf("no gradlew script found at %s/gradlew or %s/android/gradlew", root, root)
	}
	target := "installDebug"
	if opts.release() {
		target = "installRelease"
	}
	args := []string{target}
	env := append(os.Environ(), "ANDROID_SERIAL="+dev.UDID)
	fmt.Printf("$ ./gradlew %s   (ANDROID_SERIAL=%s, in %s)\n", target, dev.UDID, androidDir)
	if err := runStreaming(ctx, androidDir, gradlew, args, env); err != nil {
		return err
	}
	if opts.noLaunch {
		return nil
	}
	pkg := readAndroidPackageFromGradle(androidDir)
	if pkg == "" {
		fmt.Println("(skipping launch — could not derive applicationId)")
		return nil
	}
	launch := exec.CommandContext(ctx, "adb", "-s", dev.UDID, "shell", "monkey",
		"-p", pkg, "-c", "android.intent.category.LAUNCHER", "1")
	out, err := launch.CombinedOutput()
	if err != nil {
		return fmt.Errorf("adb monkey launch failed: %v\n%s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func pickXcodeTarget(root string) (scheme, workspace, project string, err error) {
	candidates := []string{root, filepath.Join(root, "ios")}
	for _, dir := range candidates {
		entries, e := os.ReadDir(dir)
		if e != nil {
			continue
		}
		var ws, proj string
		for _, en := range entries {
			n := en.Name()
			if strings.HasSuffix(n, ".xcworkspace") {
				ws = filepath.Join(dir, n)
			} else if strings.HasSuffix(n, ".xcodeproj") && proj == "" {
				proj = filepath.Join(dir, n)
			}
		}
		if ws == "" && proj == "" {
			continue
		}
		base := ws
		if base == "" {
			base = proj
		}
		// Scheme = filename without extension, by convention.
		name := filepath.Base(base)
		name = strings.TrimSuffix(name, filepath.Ext(name))
		return name, ws, proj, nil
	}
	return "", "", "", fmt.Errorf("no .xcworkspace or .xcodeproj found in %s or %s/ios", root, root)
}

func readAndroidPackageFromGradle(androidDir string) string {
	candidates := []string{
		filepath.Join(androidDir, "app", "build.gradle"),
		filepath.Join(androidDir, "app", "build.gradle.kts"),
	}
	for _, p := range candidates {
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			for _, key := range []string{"applicationId", "namespace"} {
				if strings.HasPrefix(line, key+" ") || strings.HasPrefix(line, key+"=") || strings.HasPrefix(line, key+":") {
					line = strings.TrimPrefix(line, key)
					line = strings.Trim(line, " =:")
					line = strings.Trim(line, "\"'")
					if line != "" {
						return line
					}
				}
			}
		}
	}
	return ""
}

// resolveAppStoreConnectKey returns the (path, keyID, issuerID) tuple
// for the local App Store Connect API key, or empty strings when any
// piece is missing. Source-of-truth order:
//  1. Direct env vars (APP_STORE_KEY_PATH/ID/ISSUER) — set by the user,
//     by `yaver vault env --project mobile`, or by CI.
//  2. yaver vault (project=mobile) — fetched via `yaver vault env`.
//  3. Heuristic: $HOME/.appstoreconnect/private_keys/AuthKey_<ID>.p8
//     when only APP_STORE_KEY_ID is known, since that's where the
//     manual-install instructions in CLAUDE.md drop the .p8 file.
func resolveAppStoreConnectKey() (path, keyID, issuerID string) {
	path = strings.TrimSpace(os.Getenv("APP_STORE_KEY_PATH"))
	keyID = strings.TrimSpace(os.Getenv("APP_STORE_KEY_ID"))
	issuerID = strings.TrimSpace(os.Getenv("APP_STORE_KEY_ISSUER"))

	if path == "" || keyID == "" || issuerID == "" {
		// Try yaver vault — vault env emits "KEY=value" lines we eval.
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		out, err := exec.CommandContext(ctx, "yaver", "vault", "env", "--project", "mobile").Output()
		if err == nil {
			for _, line := range strings.Split(string(out), "\n") {
				line = strings.TrimSpace(line)
				if strings.HasPrefix(line, "export ") {
					line = strings.TrimPrefix(line, "export ")
				}
				kv := strings.SplitN(line, "=", 2)
				if len(kv) != 2 {
					continue
				}
				val := strings.Trim(kv[1], `"' `)
				switch kv[0] {
				case "APP_STORE_KEY_PATH":
					if path == "" {
						path = val
					}
				case "APP_STORE_KEY_ID":
					if keyID == "" {
						keyID = val
					}
				case "APP_STORE_KEY_ISSUER":
					if issuerID == "" {
						issuerID = val
					}
				}
			}
		}
	}

	// Heuristic .p8 location — `~/.appstoreconnect/private_keys/AuthKey_<ID>.p8`.
	// Surfaces the key when only APP_STORE_KEY_ID is provided.
	if path == "" && keyID != "" {
		if home, err := os.UserHomeDir(); err == nil {
			candidate := filepath.Join(home, ".appstoreconnect", "private_keys", "AuthKey_"+keyID+".p8")
			if _, err := os.Stat(candidate); err == nil {
				path = candidate
			}
		}
	}
	return path, keyID, issuerID
}

func runStreaming(ctx context.Context, dir, bin string, args []string, env []string) error {
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = dir
	if env != nil {
		cmd.Env = env
	}
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	return cmd.Run()
}

func signalContext() (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(context.Background())
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-ch
		cancel()
	}()
	return ctx, func() {
		signal.Stop(ch)
		cancel()
	}
}
