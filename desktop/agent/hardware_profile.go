package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

// DeviceHardwareProfile is a cached, best-effort machine capability snapshot.
// Missing fields are normal and should never block agent startup or sync.
type DeviceHardwareProfile struct {
	OS               string   `json:"os,omitempty"`
	OSVersion        string   `json:"osVersion,omitempty"`
	CPU              string   `json:"cpu,omitempty"`
	GPU              string   `json:"gpu,omitempty"`
	RAMMB            int64    `json:"ramMb,omitempty"`
	VRAMMB           int64    `json:"vramMb,omitempty"`
	NumCores         int      `json:"numCores,omitempty"`
	Arch             string   `json:"arch,omitempty"`
	IOSSimulators    []string `json:"iosSimulators,omitempty"`
	AndroidEmulators []string `json:"androidEmulators,omitempty"`
	// IsWSL is true when the agent is running inside Microsoft's
	// Windows Subsystem for Linux. Detected via WSL_DISTRO_NAME /
	// WSL_INTEROP env vars and /proc/version (which always contains
	// "Microsoft" or "WSL2" on a real WSL kernel). The web/mobile
	// surfaces consume this boolean to label the machine — without
	// it they fall back to a 172.16-31 host-IP heuristic that
	// false-positives on any Linux box with a Docker bridge as the
	// reported host (Hetzner, Pi, plain VPS, …).
	IsWSL bool `json:"isWsl,omitempty"`
	// DiskTotalGB is total capacity of the volume holding $HOME. A static
	// spec like RAM, so it lives here on the 24h-gated profile rather than
	// on the every-beat `storage` gauge, which carries free/used.
	DiskTotalGB float64 `json:"diskTotalGb,omitempty"`
}

func (p *DeviceHardwareProfile) isEmpty() bool {
	return p == nil || (p.OS == "" &&
		p.OSVersion == "" &&
		p.CPU == "" &&
		p.GPU == "" &&
		p.RAMMB == 0 &&
		p.VRAMMB == 0 &&
		p.NumCores == 0 &&
		p.Arch == "" &&
		!p.IsWSL &&
		len(p.IOSSimulators) == 0 &&
		len(p.AndroidEmulators) == 0)
}

var (
	hardwareProfileOnce      sync.Once
	hardwareProfileMu        sync.Mutex
	hardwareProfileCached    *DeviceHardwareProfile
	hardwareProfileSentMu    sync.Mutex
	hardwareProfileLastSent  time.Time
	hardwareProfileRefreshIn = 24 * time.Hour
)

func cachedHardwareProfile() *DeviceHardwareProfile {
	hardwareProfileOnce.Do(func() {
		profile := detectHardwareProfile()
		hardwareProfileMu.Lock()
		if !profile.isEmpty() {
			hardwareProfileCached = profile
		}
		hardwareProfileMu.Unlock()
	})
	hardwareProfileMu.Lock()
	defer hardwareProfileMu.Unlock()
	return hardwareProfileCached
}

// forceRefreshHardwareProfile re-runs detection, replaces the cached snapshot,
// and clears the heartbeat-sent timestamp so the next heartbeat carries the
// fresh profile to Convex (bypassing the 24h gate).
func forceRefreshHardwareProfile() *DeviceHardwareProfile {
	// Make sure the sync.Once has fired so the slow-path/fast-path agree
	// on whether the cache is populated.
	cachedHardwareProfile()

	profile := detectHardwareProfile()
	hardwareProfileMu.Lock()
	if !profile.isEmpty() {
		hardwareProfileCached = profile
	}
	hardwareProfileMu.Unlock()

	hardwareProfileSentMu.Lock()
	hardwareProfileLastSent = time.Time{}
	hardwareProfileSentMu.Unlock()

	hardwareProfileMu.Lock()
	defer hardwareProfileMu.Unlock()
	return hardwareProfileCached
}

func hardwareProfileForHeartbeat() *DeviceHardwareProfile {
	profile := cachedHardwareProfile()
	if profile == nil {
		return nil
	}
	hardwareProfileSentMu.Lock()
	defer hardwareProfileSentMu.Unlock()
	if !hardwareProfileLastSent.IsZero() && time.Since(hardwareProfileLastSent) < hardwareProfileRefreshIn {
		return nil
	}
	return profile
}

func markHardwareProfileSent() {
	hardwareProfileSentMu.Lock()
	hardwareProfileLastSent = time.Now()
	hardwareProfileSentMu.Unlock()
}

func detectHardwareProfile() *DeviceHardwareProfile {
	profile := &DeviceHardwareProfile{
		OS:       normalizedHardwareOS(),
		NumCores: runtime.NumCPU(),
		Arch:     runtime.GOARCH,
		IsWSL:    isWSL(),
	}
	if ramMB, err := getSystemMemoryMB(); err == nil && ramMB > 0 {
		profile.RAMMB = ramMB
	}
	profile.DiskTotalGB = homeVolumeTotalGB()

	switch runtime.GOOS {
	case "darwin":
		fillDarwinHardwareProfile(profile)
	case "linux":
		fillLinuxHardwareProfile(profile)
	case "windows":
		fillWindowsHardwareProfile(profile)
	}
	fillCommonGPUProfile(profile)
	fillEmulatorProfile(profile)

	if profile.isEmpty() {
		return nil
	}
	return profile
}

func normalizedHardwareOS() string {
	switch {
	case isWSL():
		return "wsl"
	case runtime.GOOS == "darwin":
		return "macos"
	default:
		return runtime.GOOS
	}
}

func fillDarwinHardwareProfile(profile *DeviceHardwareProfile) {
	profile.OSVersion = strings.TrimSpace(runOutput("sw_vers", "-productVersion"))
	profile.CPU = strings.TrimSpace(runOutput("sysctl", "-n", "machdep.cpu.brand_string"))
	if profile.CPU == "" {
		profile.CPU = strings.TrimSpace(runOutput("sysctl", "-n", "hw.model"))
	}
	out := strings.TrimSpace(runOutput("system_profiler", "SPDisplaysDataType", "-json"))
	if out == "" {
		if strings.Contains(profile.CPU, "Apple") {
			profile.GPU = profile.CPU
		}
		return
	}
	var decoded struct {
		Displays []struct {
			Model string `json:"sppci_model"`
			VRAM  string `json:"spdisplays_vram"`
		} `json:"SPDisplaysDataType"`
	}
	if err := json.Unmarshal([]byte(out), &decoded); err == nil {
		for _, display := range decoded.Displays {
			if profile.GPU == "" && strings.TrimSpace(display.Model) != "" {
				profile.GPU = strings.TrimSpace(display.Model)
			}
			if profile.VRAMMB == 0 {
				profile.VRAMMB = parseMemoryStringMB(display.VRAM)
			}
		}
	}
	if profile.GPU == "" && strings.Contains(profile.CPU, "Apple") {
		profile.GPU = profile.CPU
	}
}

func fillLinuxHardwareProfile(profile *DeviceHardwareProfile) {
	if osRelease, err := os.Open("/etc/os-release"); err == nil {
		defer osRelease.Close()
		scanner := bufio.NewScanner(osRelease)
		for scanner.Scan() {
			line := scanner.Text()
			if strings.HasPrefix(line, "PRETTY_NAME=") {
				profile.OSVersion = strings.Trim(strings.TrimPrefix(line, "PRETTY_NAME="), "\"")
				break
			}
		}
	}
	if profile.OSVersion == "" {
		profile.OSVersion = strings.TrimSpace(runOutput("uname", "-r"))
	}
	if cpuInfo, err := os.Open("/proc/cpuinfo"); err == nil {
		defer cpuInfo.Close()
		scanner := bufio.NewScanner(cpuInfo)
		for scanner.Scan() {
			line := scanner.Text()
			if strings.HasPrefix(line, "model name") || strings.HasPrefix(line, "Hardware") {
				if _, value, ok := strings.Cut(line, ":"); ok {
					profile.CPU = strings.TrimSpace(value)
					break
				}
			}
		}
	}
}

func fillWindowsHardwareProfile(profile *DeviceHardwareProfile) {
	profile.OSVersion = strings.TrimSpace(runOutput("cmd", "/c", "ver"))
	profile.CPU = firstNonEmptyCapabilityLine(runOutput("wmic", "cpu", "get", "Name"))
	if profile.CPU == "" {
		profile.CPU = firstNonEmptyCapabilityLine(runOutput("powershell", "-NoProfile", "-Command", "(Get-CimInstance Win32_Processor | Select-Object -First 1 -ExpandProperty Name)"))
	}
}

func fillCommonGPUProfile(profile *DeviceHardwareProfile) {
	if out := strings.TrimSpace(runOutput("nvidia-smi", "--query-gpu=name,memory.total", "--format=csv,noheader")); out != "" {
		line := strings.SplitN(out, "\n", 2)[0]
		parts := strings.Split(line, ",")
		if len(parts) > 0 && strings.TrimSpace(parts[0]) != "" {
			profile.GPU = strings.TrimSpace(parts[0])
		}
		if len(parts) > 1 {
			profile.VRAMMB = parseMemoryStringMB(parts[1])
		}
		return
	}
	if runtime.GOOS == "linux" {
		if out := strings.TrimSpace(runOutput("sh", "-c", "lspci 2>/dev/null | grep -i 'vga\\|3d\\|display' | head -n 1")); out != "" {
			profile.GPU = out
		}
	}
}

func fillEmulatorProfile(profile *DeviceHardwareProfile) {
	if runtime.GOOS == "darwin" {
		type simctlList struct {
			Devices map[string][]struct {
				Name         string `json:"name"`
				IsAvailable  bool   `json:"isAvailable"`
				Availability string `json:"availability"`
			} `json:"devices"`
		}
		if out := strings.TrimSpace(runOutput("xcrun", "simctl", "list", "devices", "available", "--json")); out != "" {
			var decoded simctlList
			if json.Unmarshal([]byte(out), &decoded) == nil {
				seen := make(map[string]bool)
				for _, devices := range decoded.Devices {
					for _, device := range devices {
						if !device.IsAvailable && !strings.EqualFold(device.Availability, "(available)") {
							continue
						}
						name := strings.TrimSpace(device.Name)
						if name == "" || seen[name] {
							continue
						}
						seen[name] = true
						profile.IOSSimulators = append(profile.IOSSimulators, name)
					}
				}
			}
		}
	}
	if out := strings.TrimSpace(runAndroidEmulatorList()); out != "" {
		for _, line := range strings.Split(out, "\n") {
			name := strings.TrimSpace(line)
			if name != "" {
				profile.AndroidEmulators = append(profile.AndroidEmulators, name)
			}
		}
	}
}

func runAndroidEmulatorList() string {
	if out := runOutput("emulator", "-list-avds"); strings.TrimSpace(out) != "" {
		return out
	}
	androidHome := strings.TrimSpace(os.Getenv("ANDROID_HOME"))
	if androidHome == "" {
		return ""
	}
	return runOutput(filepath.Join(androidHome, "emulator", "emulator"), "-list-avds")
}

func runOutput(name string, args ...string) string {
	cmd := exec.Command(name, args...)
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &bytes.Buffer{}
	if err := cmd.Run(); err != nil {
		return ""
	}
	return stdout.String()
}

func firstNonEmptyCapabilityLine(raw string) string {
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		switch strings.ToLower(line) {
		case "name", "adapterram":
			continue
		}
		return line
	}
	return ""
}

func parseMemoryStringMB(raw string) int64 {
	text := strings.ToLower(strings.TrimSpace(raw))
	if text == "" {
		return 0
	}
	for _, noisy := range []string{"videoram", "(dynamic, max)", "(builtin)"} {
		text = strings.ReplaceAll(text, noisy, "")
	}
	fields := strings.Fields(strings.TrimSpace(text))
	if len(fields) == 0 {
		return 0
	}
	value, err := strconv.ParseFloat(strings.Trim(fields[0], ","), 64)
	if err != nil {
		return 0
	}
	unit := "mb"
	if len(fields) > 1 {
		unit = fields[1]
	}
	switch {
	case strings.HasPrefix(unit, "tb"):
		return int64(value * 1024 * 1024)
	case strings.HasPrefix(unit, "gb"):
		return int64(value * 1024)
	default:
		return int64(value)
	}
}
