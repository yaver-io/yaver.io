package main

import (
	"context"
	"runtime"
	"strconv"
	"strings"
)

// Autorun is resource-aware because it runs unattended on a machine it shares —
// and because it is the thing that exhausts that machine. On 2026-07-16 this
// loop drove the Mac mini to 1.1 GB free by feeding a 5.2 GB go-build cache with
// hourly full `go test ./...` runs. At zero free space a machine cannot write a
// command's output, so the loop cannot even report why it died.
//
// The rule: measure BEFORE spending, reclaim what we generated, and refuse a
// kick we cannot finish — never discover exhaustion halfway through.

// autorunCPUBackoffPerCore is the 1-minute load-per-core above which the machine
// is already saturated and a fresh runner kick would only thrash it.
const autorunCPUBackoffPerCore = 4.0

// autorunMinRAMGB is the floor below which build/test toolchains (Go link steps,
// Metro, Xcode) start swapping or getting OOM-killed rather than failing clean.
const autorunMinRAMGB = 2.0

// autorunResources is a point-in-time snapshot of what the machine can spend.
type autorunResources struct {
	FreeDiskGB float64 `json:"freeDiskGB"`
	TotalRAMGB float64 `json:"totalRamGB"`
	CPUs       int     `json:"cpus"`
	LoadAvg1   float64 `json:"loadAvg1"`
	LoadPerCPU float64 `json:"loadPerCpu"`
}

// probeAutorunResources reuses the agent's existing probes rather than adding
// new ones: statfsGB (diskhealth_unix.go) and getSystemMemoryMB (process_unix.go).
func probeAutorunResources(ctx context.Context, workDir string) autorunResources {
	res := autorunResources{CPUs: runtime.NumCPU()}
	if free, ok := autorunFreeDiskGB(workDir); ok {
		res.FreeDiskGB = free
	}
	if mb, err := getSystemMemoryMB(); err == nil && mb > 0 {
		res.TotalRAMGB = float64(mb) / 1024
	}
	if load, ok := autorunLoadAvg1(ctx, workDir); ok {
		res.LoadAvg1 = load
		if res.CPUs > 0 {
			res.LoadPerCPU = load / float64(res.CPUs)
		}
	}
	return res
}

// autorunLoadAvg1 reads the 1-minute load average. Linux exposes /proc/loadavg;
// macOS answers `sysctl -n vm.loadavg` as "{ 1.83 2.04 2.11 }".
func autorunLoadAvg1(ctx context.Context, workDir string) (float64, bool) {
	if out := autorunExec(ctx, "sh", []string{"-lc", "cat /proc/loadavg 2>/dev/null || sysctl -n vm.loadavg 2>/dev/null"}, workDir); out.Err == nil {
		for _, field := range strings.Fields(strings.ReplaceAll(strings.ReplaceAll(out.Output, "{", " "), "}", " ")) {
			if v, err := strconv.ParseFloat(field, 64); err == nil {
				return v, true
			}
		}
	}
	return 0, false
}

// Summary renders a snapshot for the progress handoff and the final commit, so a
// run that stopped for resources says so in numbers rather than vibes.
func (r autorunResources) Summary() string {
	var b strings.Builder
	b.WriteString("disk ")
	b.WriteString(strconv.FormatFloat(r.FreeDiskGB, 'f', 1, 64))
	b.WriteString(" GB free")
	if r.TotalRAMGB > 0 {
		b.WriteString(", RAM ")
		b.WriteString(strconv.FormatFloat(r.TotalRAMGB, 'f', 1, 64))
		b.WriteString(" GB")
	}
	b.WriteString(", ")
	b.WriteString(strconv.Itoa(r.CPUs))
	b.WriteString(" CPUs, load ")
	b.WriteString(strconv.FormatFloat(r.LoadAvg1, 'f', 2, 64))
	b.WriteString(" (")
	b.WriteString(strconv.FormatFloat(r.LoadPerCPU, 'f', 2, 64))
	b.WriteString("/core)")
	return b.String()
}

// Saturated reports whether the box is already too busy to be given more work.
// Load is advisory, not fatal: the loop waits it out rather than failing a run
// because something else was compiling at that moment.
func (r autorunResources) Saturated() bool {
	return r.LoadPerCPU > autorunCPUBackoffPerCore
}
