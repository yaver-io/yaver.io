package main

// Mobile device info enrichment. Wraps `xcrun devicectl device info`
// (iOS) and `adb shell getprop` / `/proc/meminfo` (Android) into a
// single `mobileDeviceInfo` struct surfaced by:
//   - `yaver wireless detect [--info]` (default on for human output)
//   - `yaver wire detect [--info]`
//   - `yaver mobile devices`
//   - the "Mobile devices" section of `yaver devices`
//   - the wireless_detect / wire_detect MCP tools
//
// Enrichment is best-effort: missing fields stay "" so callers can
// render "—" without nil-checking each one. Cold latency is dominated
// by xcrun (~600 ms) and the first adb roundtrip (~250 ms over WiFi).
// We don't cache results — the CLI runs once and exits — but the data
// flows through wireDevice so callers that already have a list don't
// re-shell.

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// mobileDeviceInfo is the cross-platform view of one phone/tablet's
// hardware + OS, enough to render a "Galaxy Tab S7 FE • Android 14 •
// 3.3 GB RAM" one-liner without leaking platform-specific fields into
// the UI layer.
type mobileDeviceInfo struct {
	// User-set device name (e.g. "Kıvanç's iPhone", "Galaxy Tab S7 FE").
	DeviceName string `json:"device_name,omitempty"`
	// Marketing name (e.g. "iPhone 14", "Galaxy Tab S7 FE").
	MarketingName string `json:"marketing_name,omitempty"`
	// Manufacturer (Apple, Samsung, Google, ...).
	Manufacturer string `json:"manufacturer,omitempty"`
	// Brand (samsung, google, oneplus, ...). Empty on iOS.
	Brand string `json:"brand,omitempty"`
	// Internal model code (iPhone14,7 / SM-T733).
	ModelCode string `json:"model_code,omitempty"`
	// OS family ("iOS" / "Android").
	OS string `json:"os,omitempty"`
	// OS version (26.4.2 / 14).
	OSVersion string `json:"os_version,omitempty"`
	// Android API level / iOS build (e.g. "34" / "23E261").
	OSBuild string `json:"os_build,omitempty"`
	// CPU type label (arm64e / arm64-v8a).
	CPUType string `json:"cpu_type,omitempty"`
	// SoC / board (Apple A15 / Snapdragon 750G — best effort).
	SoC string `json:"soc,omitempty"`
	// RAM in bytes (0 = unknown).
	RAMBytes uint64 `json:"ram_bytes,omitempty"`
	// Storage capacity in bytes (0 = unknown). iOS only — Android
	// requires shell privileges we don't request.
	StorageBytes uint64 `json:"storage_bytes,omitempty"`
}

// summary returns a short single-line label suitable for tables.
// Example: "Galaxy Tab S7 FE • Android 14 • 3.3 GB RAM".
func (m mobileDeviceInfo) summary() string {
	parts := []string{}
	if m.DeviceName != "" {
		parts = append(parts, m.DeviceName)
	} else if m.MarketingName != "" {
		parts = append(parts, m.MarketingName)
	} else if m.ModelCode != "" {
		parts = append(parts, m.ModelCode)
	}
	if m.OS != "" {
		osLabel := m.OS
		if m.OSVersion != "" {
			osLabel = m.OS + " " + m.OSVersion
		}
		parts = append(parts, osLabel)
	}
	if m.RAMBytes > 0 {
		parts = append(parts, formatBytes(int64(m.RAMBytes))+" RAM")
	}
	return strings.Join(parts, " • ")
}

// enrichMobileDevice routes to the iOS or Android probe based on
// platform. Best-effort: returns zero-value when the tool is missing
// or the device is unreachable.
func enrichMobileDevice(ctx context.Context, dev wireDevice) mobileDeviceInfo {
	switch dev.Platform {
	case "ios":
		return enrichIOSDeviceInfo(ctx, dev)
	case "android":
		return enrichAndroidDeviceInfo(ctx, dev)
	}
	return mobileDeviceInfo{}
}

// enrichIOSDeviceInfo runs `xcrun devicectl device info details --json-output`
// and parses the {hardware,device}Properties blobs we care about. The
// JSON-to-file dance is the only programmatic interface devicectl
// supports — see `xcrun devicectl device info details --help`.
func enrichIOSDeviceInfo(ctx context.Context, dev wireDevice) mobileDeviceInfo {
	if _, err := exec.LookPath("xcrun"); err != nil {
		return mobileDeviceInfo{}
	}
	tmp, err := os.CreateTemp("", "yaver-devicectl-*.json")
	if err != nil {
		return mobileDeviceInfo{}
	}
	tmpPath := tmp.Name()
	tmp.Close()
	defer os.Remove(tmpPath)
	cctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	cmd := exec.CommandContext(cctx, "xcrun", "devicectl", "device", "info", "details",
		"--device", dev.UDID, "--json-output", tmpPath, "-q")
	if err := cmd.Run(); err != nil {
		return mobileDeviceInfo{}
	}
	data, err := os.ReadFile(tmpPath)
	if err != nil {
		return mobileDeviceInfo{}
	}
	var raw struct {
		Result struct {
			DeviceProperties struct {
				Name          string `json:"name"`
				OSVersion     string `json:"osVersionNumber"`
				OSBuildUpdate string `json:"osBuildUpdate"`
			} `json:"deviceProperties"`
			HardwareProperties struct {
				CPUType struct {
					Name string `json:"name"`
				} `json:"cpuType"`
				DeviceType              string `json:"deviceType"`
				MarketingName           string `json:"marketingName"`
				Platform                string `json:"platform"`
				ProductType             string `json:"productType"`
				InternalStorageCapacity uint64 `json:"internalStorageCapacity"`
			} `json:"hardwareProperties"`
		} `json:"result"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return mobileDeviceInfo{}
	}
	return mobileDeviceInfo{
		DeviceName:    strings.TrimSpace(raw.Result.DeviceProperties.Name),
		MarketingName: raw.Result.HardwareProperties.MarketingName,
		Manufacturer:  "Apple",
		ModelCode:     raw.Result.HardwareProperties.ProductType,
		OS:            raw.Result.HardwareProperties.Platform,
		OSVersion:     raw.Result.DeviceProperties.OSVersion,
		OSBuild:       raw.Result.DeviceProperties.OSBuildUpdate,
		CPUType:       raw.Result.HardwareProperties.CPUType.Name,
		SoC:           appleSoCForProductType(raw.Result.HardwareProperties.ProductType),
		StorageBytes:  raw.Result.HardwareProperties.InternalStorageCapacity,
	}
}

// appleSoCForProductType maps Apple model identifiers (iPhone14,7) to
// the marketing SoC name (Apple A15 Bionic). Best-effort — when an
// entry is missing we fall back to "" rather than guessing wrong.
//
// Source: Apple's hardware identifier list. Coverage focuses on iOS
// 17+ era devices since that's what TestFlight/Play deployments target.
func appleSoCForProductType(pt string) string {
	switch pt {
	case "iPhone14,7", "iPhone14,8", "iPhone14,2", "iPhone14,3", "iPhone14,4", "iPhone14,5", "iPhone14,6":
		return "Apple A15 Bionic"
	case "iPhone15,2", "iPhone15,3":
		return "Apple A16 Bionic"
	case "iPhone15,4", "iPhone15,5", "iPhone16,1", "iPhone16,2":
		return "Apple A17 Pro"
	case "iPhone17,1", "iPhone17,2":
		return "Apple A18 Pro"
	case "iPhone17,3", "iPhone17,4":
		return "Apple A18"
	case "iPad13,18", "iPad13,19":
		return "Apple A14 Bionic"
	case "iPad14,3", "iPad14,4", "iPad14,5", "iPad14,6":
		return "Apple M2"
	case "iPad16,3", "iPad16,4", "iPad16,5", "iPad16,6":
		return "Apple M4"
	}
	return ""
}

// enrichAndroidDeviceInfo runs adb getprop + a few extra shells. We
// batch the queries into one `adb shell` invocation per call to keep
// roundtrips down — over WiFi each round trip is ~150-250 ms.
func enrichAndroidDeviceInfo(ctx context.Context, dev wireDevice) mobileDeviceInfo {
	if _, err := exec.LookPath("adb"); err != nil {
		return mobileDeviceInfo{}
	}
	cctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	// One shell, several lines. Each block is a tagged section so the
	// parser can split them deterministically.
	script := strings.Join([]string{
		"echo '##GETPROP##'",
		"getprop",
		"echo '##DEVNAME##'",
		"settings get global device_name 2>/dev/null",
		"echo '##MEMINFO##'",
		"cat /proc/meminfo 2>/dev/null | head -3",
	}, "; ")
	out, err := exec.CommandContext(cctx, "adb", "-s", dev.UDID, "shell", script).Output()
	if err != nil {
		return mobileDeviceInfo{}
	}
	return parseAndroidEnrichOutput(string(out))
}

// parseAndroidEnrichOutput is the pure-string parser for the batched
// shell output. Split out so it's testable without an actual phone.
func parseAndroidEnrichOutput(s string) mobileDeviceInfo {
	info := mobileDeviceInfo{
		OS: "Android",
	}
	sections := map[string]string{}
	currentTag := ""
	var b strings.Builder
	flush := func() {
		if currentTag != "" {
			sections[currentTag] = b.String()
		}
		b.Reset()
	}
	for _, line := range strings.Split(s, "\n") {
		trimmed := strings.TrimSpace(line)
		switch trimmed {
		case "##GETPROP##", "##DEVNAME##", "##MEMINFO##":
			flush()
			currentTag = trimmed
		default:
			b.WriteString(line)
			b.WriteByte('\n')
		}
	}
	flush()

	// getprop format: [key]: [value]
	props := map[string]string{}
	for _, line := range strings.Split(sections["##GETPROP##"], "\n") {
		// strip "[key]: [value]"
		if !strings.HasPrefix(line, "[") {
			continue
		}
		closeKey := strings.Index(line, "]:")
		if closeKey < 1 {
			continue
		}
		key := line[1:closeKey]
		rest := strings.TrimSpace(line[closeKey+2:])
		if !strings.HasPrefix(rest, "[") || !strings.HasSuffix(rest, "]") {
			continue
		}
		value := rest[1 : len(rest)-1]
		props[key] = value
	}
	info.Manufacturer = props["ro.product.manufacturer"]
	info.Brand = props["ro.product.brand"]
	info.ModelCode = props["ro.product.model"]
	info.OSVersion = props["ro.build.version.release"]
	info.OSBuild = props["ro.build.version.sdk"] // API level
	info.CPUType = props["ro.product.cpu.abi"]
	if mname := strings.TrimSpace(props["ro.config.marketing_name"]); mname != "" {
		info.MarketingName = mname
	} else if mname := strings.TrimSpace(props["ro.product.marketname"]); mname != "" {
		info.MarketingName = mname
	}
	if soc := androidSoCForBoard(props["ro.product.board"]); soc != "" {
		info.SoC = soc
	}

	// settings get global device_name → user-friendly device name.
	if dn := strings.TrimSpace(sections["##DEVNAME##"]); dn != "" && dn != "null" {
		info.DeviceName = dn
	}
	// Fallback: best-effort marketing name as device name.
	if info.DeviceName == "" {
		info.DeviceName = info.MarketingName
	}
	if info.MarketingName == "" {
		info.MarketingName = info.DeviceName
	}

	// MemTotal: <kb> kB → bytes
	for _, line := range strings.Split(sections["##MEMINFO##"], "\n") {
		if !strings.HasPrefix(line, "MemTotal:") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) >= 2 {
			if kb, err := strconv.ParseUint(fields[1], 10, 64); err == nil {
				info.RAMBytes = kb * 1024
			}
		}
		break
	}

	return info
}

// androidSoCForBoard maps Android board codenames to marketing SoC
// names. Like the Apple table, this is best-effort — we cover the
// boards we've actually seen in user devices and leave the rest blank.
func androidSoCForBoard(board string) string {
	switch strings.ToLower(board) {
	case "lahaina":
		return "Snapdragon 888 / 750G family"
	case "kona":
		return "Snapdragon 865"
	case "taro", "kalama":
		return "Snapdragon 8 Gen 2 / 8 Gen 1"
	case "pineapple":
		return "Snapdragon 8 Gen 3"
	case "sun":
		return "Snapdragon 8 Elite"
	case "blueline", "crosshatch":
		return "Snapdragon 845"
	case "redfin", "barbet", "bramble":
		return "Snapdragon 765G"
	case "oriole", "raven":
		return "Google Tensor"
	case "panther", "cheetah":
		return "Google Tensor G2"
	case "shiba", "husky":
		return "Google Tensor G3"
	case "tokay", "caiman", "komodo":
		return "Google Tensor G4"
	}
	return ""
}
