package main

// diskhealth.go — SMART drive-health and per-filesystem space
// monitoring for headless boxes (Mac mini, Linux VPS, random
// Hetzner box the solo dev stopped worrying about).
//
// Replaces host-monitoring SaaS like Datadog Host / Healthchecks.io
// and — critically — catches the two most common ways a headless
// machine silently dies without the dev noticing:
//
//   1. The SSD predicts its own failure (SMART) weeks before it
//      actually goes. macOS: parse `system_profiler
//      SPStorageDataType -json`. Linux: shell out to
//      `smartctl -a -j /dev/sdX` when smartmontools is present.
//
//   2. The root filesystem fills up from build caches / log
//      spam / forgotten Docker volumes. Linux + macOS:
//      syscall.Statfs on every local mount.
//
// Both checks run inside one goroutine (every 10 minutes by
// default) and feed into the existing notification channel +
// the new /machine/health HTTP endpoint which the mobile
// Monitor tab polls.
//
// Everything local. Zero vendor. Zero Convex.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

// DiskSpaceEntry is the result of one filesystem check.
type DiskSpaceEntry struct {
	Mount     string  `json:"mount"`
	TotalGB   float64 `json:"totalGb"`
	UsedGB    float64 `json:"usedGb"`
	FreeGB    float64 `json:"freeGb"`
	UsedPct   float64 `json:"usedPct"`
	Device    string  `json:"device,omitempty"`
	FSType    string  `json:"fsType,omitempty"`
	CheckedAt string  `json:"checkedAt"`
}

// SMARTDrive captures the interesting subset of a SMART report.
// We intentionally DON'T parse every single attribute — the
// dev just needs to know "is this drive going to die soon?"
type SMARTDrive struct {
	Device          string `json:"device"`
	Model           string `json:"model,omitempty"`
	SerialNumber    string `json:"serial,omitempty"`
	Health          string `json:"health"`              // "passed" | "failing" | "unknown"
	TemperatureC    int    `json:"temperatureC,omitempty"`
	PowerOnHours    int    `json:"powerOnHours,omitempty"`
	ReallocatedSect int    `json:"reallocatedSectors,omitempty"`
	PendingSect     int    `json:"pendingSectors,omitempty"`
	CheckedAt       string `json:"checkedAt"`
	RawError        string `json:"rawError,omitempty"` // populated when parsing failed
}

// MachineHealth is the top-level snapshot the mobile card consumes.
type MachineHealth struct {
	Hostname   string            `json:"hostname"`
	OS         string            `json:"os"`
	UpdatedAt  string            `json:"updatedAt"`
	Filesystems []DiskSpaceEntry `json:"filesystems"`
	Drives     []SMARTDrive      `json:"drives"`
	Alerts     []string          `json:"alerts,omitempty"`
}

var (
	machineHealthMu sync.RWMutex
	machineHealth   MachineHealth
)

// DiskWarnPercent is the "space is tight" threshold. Alert
// fires once per crossing so a dev isn't spammed every 10 min.
const DiskWarnPercent = 85.0

// DiskCriticalPercent is the "stop the world" threshold.
const DiskCriticalPercent = 95.0

// --- filesystem space ----------------------------------------------------

// collectDiskSpace walks every local mount and returns a
// snapshot. On macOS we prefer `df -k -P` to skip network
// mounts; on Linux /proc/mounts gives us the list, and
// syscall.Statfs does the math.
func collectDiskSpace() []DiskSpaceEntry {
	var out []DiskSpaceEntry
	now := time.Now().UTC().Format(time.RFC3339)

	// Parse a canonical set of mount points per-OS.
	mounts := enumerateMounts()
	seen := map[string]bool{}
	for _, m := range mounts {
		if seen[m.Mount] {
			continue
		}
		seen[m.Mount] = true
		total, free, ok := statfsGB(m.Mount)
		if !ok {
			continue
		}
		used := total - free
		if total <= 0 {
			continue
		}
		out = append(out, DiskSpaceEntry{
			Mount:     m.Mount,
			Device:    m.Device,
			FSType:    m.FSType,
			TotalGB:   round1(total),
			UsedGB:    round1(used),
			FreeGB:    round1(free),
			UsedPct:   round1((used / total) * 100),
			CheckedAt: now,
		})
	}
	return out
}

type mountEntry struct {
	Device string
	Mount  string
	FSType string
}

// enumerateMounts returns OS-appropriate mount points. We
// filter out pseudo/overlay filesystems that don't matter for
// the "disk full" check.
func enumerateMounts() []mountEntry {
	var out []mountEntry
	switch runtime.GOOS {
	case "linux":
		data, err := os.ReadFile("/proc/mounts")
		if err != nil {
			return nil
		}
		for _, line := range strings.Split(string(data), "\n") {
			fields := strings.Fields(line)
			if len(fields) < 3 {
				continue
			}
			device, mount, fsType := fields[0], fields[1], fields[2]
			if isIgnoredFS(fsType) || strings.HasPrefix(mount, "/proc") || strings.HasPrefix(mount, "/sys") {
				continue
			}
			out = append(out, mountEntry{Device: device, Mount: mount, FSType: fsType})
		}
	case "darwin":
		// `df -k -P` is POSIX-portable and skips network auto-
		// mounts. Format:
		//   Filesystem 1024-blocks Used Available Capacity Mounted on
		data, err := exec.Command("df", "-k", "-P", "-l").Output()
		if err != nil {
			// Fall back to the canonical root mount — better
			// than silence.
			return []mountEntry{{Device: "/", Mount: "/", FSType: "apfs"}}
		}
		lines := strings.Split(string(data), "\n")
		for i, line := range lines {
			if i == 0 { // header
				continue
			}
			fields := strings.Fields(line)
			if len(fields) < 6 {
				continue
			}
			mount := strings.Join(fields[5:], " ")
			out = append(out, mountEntry{
				Device: fields[0],
				Mount:  mount,
				FSType: "apfs",
			})
		}
	}
	return out
}

func isIgnoredFS(fs string) bool {
	switch fs {
	case "proc", "sysfs", "cgroup", "cgroup2", "overlay", "tmpfs",
		"devtmpfs", "devpts", "securityfs", "pstore", "fusectl",
		"debugfs", "hugetlbfs", "tracefs", "mqueue":
		return true
	}
	return false
}

func round1(v float64) float64 {
	return float64(int(v*10+0.5)) / 10
}

// --- SMART ---------------------------------------------------------------

// collectSMART returns per-drive health info, preferring
// smartctl JSON output on Linux and system_profiler on macOS.
// Returns an empty slice when no supported backend is present
// — NOT an error, since many dev boxes don't have smartctl and
// we shouldn't spam them with install prompts.
func collectSMART() []SMARTDrive {
	var out []SMARTDrive
	now := time.Now().UTC().Format(time.RFC3339)

	switch runtime.GOOS {
	case "darwin":
		out = smartDarwin(now)
	case "linux":
		out = smartLinux(now)
	}
	return out
}

// smartDarwin parses `system_profiler SPStorageDataType -json`.
// Every APFS volume has a "smart_status" field — "Verified"
// means healthy, anything else fires an alert.
func smartDarwin(now string) []SMARTDrive {
	cmd := exec.Command("system_profiler", "-json", "SPStorageDataType")
	cmd.Env = append(os.Environ(), "LC_ALL=C")
	data, err := cmd.Output()
	if err != nil {
		return nil
	}
	var payload struct {
		SPStorageDataType []struct {
			Name         string `json:"_name"`
			SMARTStatus  string `json:"smart_status"`
			PhysicalDrive struct {
				DeviceName string `json:"device_name"`
				MediaName  string `json:"media_name"`
				SmartStatus string `json:"smart_status"`
				ProtocolText string `json:"protocol"`
			} `json:"physical_drive"`
		} `json:"SPStorageDataType"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil
	}
	seen := map[string]bool{}
	for _, v := range payload.SPStorageDataType {
		dev := v.PhysicalDrive.DeviceName
		if dev == "" {
			dev = v.Name
		}
		if dev == "" || seen[dev] {
			continue
		}
		seen[dev] = true
		status := v.PhysicalDrive.SmartStatus
		if status == "" {
			status = v.SMARTStatus
		}
		health := "unknown"
		switch strings.ToLower(status) {
		case "verified", "passed", "ok":
			health = "passed"
		case "failing", "failed":
			health = "failing"
		}
		out := SMARTDrive{
			Device:    dev,
			Model:     v.PhysicalDrive.MediaName,
			Health:    health,
			CheckedAt: now,
		}
		_ = out // used below via append
		_ = seen
	}
	// Build the final slice from the sanitized map so we don't
	// return duplicates.
	result := make([]SMARTDrive, 0, len(seen))
	for dev := range seen {
		for _, v := range payload.SPStorageDataType {
			d := v.PhysicalDrive.DeviceName
			if d == "" {
				d = v.Name
			}
			if d != dev {
				continue
			}
			status := v.PhysicalDrive.SmartStatus
			if status == "" {
				status = v.SMARTStatus
			}
			health := "unknown"
			switch strings.ToLower(status) {
			case "verified", "passed", "ok":
				health = "passed"
			case "failing", "failed":
				health = "failing"
			}
			result = append(result, SMARTDrive{
				Device:    dev,
				Model:     v.PhysicalDrive.MediaName,
				Health:    health,
				CheckedAt: now,
			})
			break
		}
	}
	return result
}

// smartLinux shells out to smartctl if installed. The JSON
// output has a stable `smart_status.passed` boolean plus
// temperature + power-on hours we can surface.
func smartLinux(now string) []SMARTDrive {
	if _, err := exec.LookPath("smartctl"); err != nil {
		return nil
	}
	// Enumerate disk devices from /sys/block/ — skip loop*, ram*, dm*.
	entries, err := os.ReadDir("/sys/block")
	if err != nil {
		return nil
	}
	var drives []SMARTDrive
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, "loop") ||
			strings.HasPrefix(name, "ram") ||
			strings.HasPrefix(name, "dm-") ||
			strings.HasPrefix(name, "zram") {
			continue
		}
		dev := "/dev/" + name
		out, err := exec.Command("smartctl", "-a", "-j", dev).Output()
		if err != nil {
			continue
		}
		var payload struct {
			ModelName    string `json:"model_name"`
			SerialNumber string `json:"serial_number"`
			SMARTStatus  struct {
				Passed bool `json:"passed"`
			} `json:"smart_status"`
			Temperature struct {
				Current int `json:"current"`
			} `json:"temperature"`
			PowerOnTime struct {
				Hours int `json:"hours"`
			} `json:"power_on_time"`
		}
		if err := json.Unmarshal(out, &payload); err != nil {
			continue
		}
		health := "failing"
		if payload.SMARTStatus.Passed {
			health = "passed"
		}
		drives = append(drives, SMARTDrive{
			Device:       dev,
			Model:        payload.ModelName,
			SerialNumber: payload.SerialNumber,
			Health:       health,
			TemperatureC: payload.Temperature.Current,
			PowerOnHours: payload.PowerOnTime.Hours,
			CheckedAt:    now,
		})
	}
	return drives
}

// --- scanner loop --------------------------------------------------------

// StartDiskHealthLoop kicks off the periodic scanner. Called
// from runServe exactly once. Each tick refreshes the
// machineHealth snapshot and compares against the previous
// state for alertable crossings.
func StartDiskHealthLoop() {
	go func() {
		// First scan fires ~3s after boot so the agent isn't
		// doing all its startup work on the same goroutine.
		time.Sleep(3 * time.Second)
		for {
			runDiskHealthScan()
			time.Sleep(10 * time.Minute)
		}
	}()
}

// runDiskHealthScan performs one sweep and emits any alerts.
func runDiskHealthScan() {
	hostname, _ := os.Hostname()
	snap := MachineHealth{
		Hostname:    hostname,
		OS:          runtime.GOOS,
		UpdatedAt:   time.Now().UTC().Format(time.RFC3339),
		Filesystems: collectDiskSpace(),
		Drives:      collectSMART(),
	}

	// Compare to previous state for edge-triggered alerts.
	machineHealthMu.Lock()
	prev := machineHealth
	newAlerts := []string{}

	prevPct := map[string]float64{}
	for _, f := range prev.Filesystems {
		prevPct[f.Mount] = f.UsedPct
	}
	for _, f := range snap.Filesystems {
		old := prevPct[f.Mount]
		if f.UsedPct >= DiskCriticalPercent && old < DiskCriticalPercent {
			newAlerts = append(newAlerts,
				fmt.Sprintf("CRITICAL: %s is %.0f%% full (%s)", f.Mount, f.UsedPct, formatGB(f.FreeGB)+" free"))
		} else if f.UsedPct >= DiskWarnPercent && old < DiskWarnPercent {
			newAlerts = append(newAlerts,
				fmt.Sprintf("Warning: %s is %.0f%% full (%s free)", f.Mount, f.UsedPct, formatGB(f.FreeGB)))
		}
	}

	prevHealth := map[string]string{}
	for _, d := range prev.Drives {
		prevHealth[d.Device] = d.Health
	}
	for _, d := range snap.Drives {
		if d.Health == "failing" && prevHealth[d.Device] != "failing" {
			newAlerts = append(newAlerts,
				fmt.Sprintf("CRITICAL: drive %s (%s) reports SMART failure — back up now", d.Device, d.Model))
		}
	}
	snap.Alerts = newAlerts
	machineHealth = snap
	machineHealthMu.Unlock()

	// Fire notifications for every fresh alert.
	for _, msg := range newAlerts {
		fmt.Fprintf(os.Stderr, "[disk-health] %s\n", msg)
		if nm := globalMonitorNotifier; nm != nil {
			nm("disk-health", hostname, msg, 0)
		}
	}
	// Push space alerts to the user's phones. On a headless box this is the
	// ONLY surface that will tell them before a build dies on ENOSPC —
	// stderr and the local notifier reach nobody there.
	notifyDiskPressure(newAlerts)
}

func formatGB(g float64) string {
	if g < 1 {
		return fmt.Sprintf("%.0f MB", g*1024)
	}
	return fmt.Sprintf("%.1f GB", g)
}

// --- HTTP ----------------------------------------------------------------

// handleMachineHealth serves the latest snapshot. Idempotent
// cheap read — the scanner owns the refresh cadence.
func (s *HTTPServer) handleMachineHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, http.StatusMethodNotAllowed, "use GET")
		return
	}
	machineHealthMu.RLock()
	defer machineHealthMu.RUnlock()
	if machineHealth.UpdatedAt == "" {
		jsonReply(w, http.StatusOK, map[string]interface{}{
			"ok":      true,
			"pending": true,
		})
		return
	}
	jsonReply(w, http.StatusOK, map[string]interface{}{
		"ok":     true,
		"health": machineHealth,
	})
}

// runMachineHealthCmd is the CLI counterpart — `yaver machine
// health` prints the latest snapshot in a terminal-friendly
// form. Handy for SSH'd users watching a remote box.
func runMachineHealthCmd() {
	runDiskHealthScan()
	machineHealthMu.RLock()
	defer machineHealthMu.RUnlock()
	fmt.Printf("Machine: %s (%s)\n", machineHealth.Hostname, machineHealth.OS)
	fmt.Printf("Updated: %s\n", machineHealth.UpdatedAt)
	fmt.Println()
	if len(machineHealth.Filesystems) > 0 {
		fmt.Println("Filesystems:")
		for _, f := range machineHealth.Filesystems {
			state := "ok"
			if f.UsedPct >= DiskCriticalPercent {
				state = "CRITICAL"
			} else if f.UsedPct >= DiskWarnPercent {
				state = "warning"
			}
			fmt.Printf("  %-30s  %6.1f GB / %6.1f GB  %4.0f%%  [%s]\n",
				f.Mount, f.UsedGB, f.TotalGB, f.UsedPct, state)
		}
		fmt.Println()
	}
	if len(machineHealth.Drives) > 0 {
		fmt.Println("Drives:")
		for _, d := range machineHealth.Drives {
			extra := ""
			if d.TemperatureC > 0 {
				extra += fmt.Sprintf(" temp=%d°C", d.TemperatureC)
			}
			if d.PowerOnHours > 0 {
				extra += fmt.Sprintf(" power-on=%dh", d.PowerOnHours)
			}
			fmt.Printf("  %-20s  %-30s  [%s]%s\n", d.Device, d.Model, d.Health, extra)
		}
		fmt.Println()
	} else if runtime.GOOS == "linux" {
		fmt.Println("(no SMART data — install smartmontools: `sudo apt install smartmontools`)")
	}
	if len(machineHealth.Alerts) > 0 {
		fmt.Println("Alerts:")
		for _, a := range machineHealth.Alerts {
			fmt.Println("  ⚠", a)
		}
		fmt.Println()
	}
}

// Ensure filepath is referenced so future path validation
// helpers can land here without reimporting.
var _ = filepath.Join
