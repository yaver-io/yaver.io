package testkit

import (
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
)

// HostStatus is a snapshot of the dev's machine. The scheduler uses
// this to decide whether to fire a queued run or wait. Reading these
// fields is cheap (one shell-out on macOS, one /sys read on Linux).
type HostStatus struct {
	OS         string  // "darwin" | "linux" | other
	OnBattery  bool    // true if running on battery (macOS pmset, Linux /sys)
	BatteryPct int     // 0..100, -1 if unknown
	LoadAvg1   float64 // 1-minute load average
	NumCPU     int
}

// SnapshotHost returns the current host status. Never panics; missing
// data is reported as zero / -1.
func SnapshotHost() HostStatus {
	st := HostStatus{
		OS:         runtime.GOOS,
		BatteryPct: -1,
		NumCPU:     runtime.NumCPU(),
	}
	switch runtime.GOOS {
	case "darwin":
		readBatteryDarwin(&st)
	case "linux":
		readBatteryLinux(&st)
	}
	st.LoadAvg1 = readLoadAvg1()
	return st
}

// readBatteryDarwin shells out to `pmset -g batt` and parses its output.
// Avoids cgo / IOKit binding for portability.
func readBatteryDarwin(st *HostStatus) {
	out, err := exec.Command("pmset", "-g", "batt").Output()
	if err != nil {
		return
	}
	text := string(out)
	if strings.Contains(text, "AC Power") {
		st.OnBattery = false
	} else if strings.Contains(text, "Battery Power") {
		st.OnBattery = true
	}
	for _, field := range strings.Fields(text) {
		if strings.HasSuffix(field, "%;") {
			pct := strings.TrimSuffix(field, "%;")
			if n, err := strconv.Atoi(pct); err == nil {
				st.BatteryPct = n
			}
			return
		}
	}
}

// readBatteryLinux walks /sys/class/power_supply for the first BAT* and
// AC* node. Works on every modern distro without extra deps.
func readBatteryLinux(st *HostStatus) {
	dir := "/sys/class/power_supply"
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		name := e.Name()
		switch {
		case strings.HasPrefix(name, "BAT"):
			if data, err := os.ReadFile(dir + "/" + name + "/capacity"); err == nil {
				if n, err := strconv.Atoi(strings.TrimSpace(string(data))); err == nil {
					st.BatteryPct = n
				}
			}
		case strings.HasPrefix(name, "AC"), strings.HasPrefix(name, "ADP"):
			if data, err := os.ReadFile(dir + "/" + name + "/online"); err == nil {
				if strings.TrimSpace(string(data)) == "1" {
					st.OnBattery = false
				} else {
					st.OnBattery = true
				}
			}
		}
	}
}

// readLoadAvg1 reads /proc/loadavg on Linux or runs `uptime` on macOS.
// Returns 0 on failure (we don't want to block runs because of a parse
// error in a power-saving routine).
func readLoadAvg1() float64 {
	if runtime.GOOS == "linux" {
		if data, err := os.ReadFile("/proc/loadavg"); err == nil {
			parts := strings.Fields(string(data))
			if len(parts) > 0 {
				if v, err := strconv.ParseFloat(parts[0], 64); err == nil {
					return v
				}
			}
		}
		return 0
	}
	if runtime.GOOS == "darwin" {
		out, err := exec.Command("uptime").Output()
		if err != nil {
			return 0
		}
		// "load averages: 1.45 1.32 1.18"
		i := strings.Index(string(out), "load average")
		if i < 0 {
			return 0
		}
		rest := string(out)[i:]
		colon := strings.Index(rest, ":")
		if colon < 0 {
			return 0
		}
		fields := strings.FieldsFunc(rest[colon+1:], func(r rune) bool {
			return r == ' ' || r == ',' || r == '\n'
		})
		if len(fields) > 0 {
			if v, err := strconv.ParseFloat(fields[0], 64); err == nil {
				return v
			}
		}
	}
	return 0
}

// ShouldRun returns (true) when the host is in a state that's friendly
// for kicking off a CI run, and (false, reason) otherwise. The
// scheduler uses this to defer runs when the dev is on battery or the
// machine is already pegged. Solo dev never wants their laptop to
// drain its battery running tests.
func ShouldRun(st HostStatus, requireACPower bool, maxLoad float64) (bool, string) {
	if requireACPower && st.OnBattery {
		return false, "on battery"
	}
	if maxLoad > 0 && st.LoadAvg1 > maxLoad {
		return false, "load average too high"
	}
	return true, ""
}
