package main

// storage_pressure.go — turns "the disk is nearly full" from something you
// discover when a build dies into something your phone tells you.
//
// diskhealth.go has fired edge-triggered alerts at 85% and 95% since it was
// written, but nothing consumed them beyond a stderr line and the local
// notifier — so on a headless box (the exact machine this matters on) the
// alert went precisely nowhere. This wires them to the phone over the same
// device_broadcast_command path the gateway gates use.
//
// Edge-triggered, not level-triggered: diskhealth only emits when a threshold
// is CROSSED, so a box that sits at 90% for a week pushes once, not every ten
// minutes. A notification you learn to swipe away is worse than none.
//
// The push carries the reclaimable figure when a scan is warm, because
// "92% full" is alarming and "92% full, 22 GB of build caches reclaimable"
// is actionable — and the phone can deep-link straight into the approval.

import (
	"fmt"
	"strings"
	"sync"
)

var (
	diskPressureMu       sync.RWMutex
	diskPressureNotifier *BlackBoxManager
)

// bindDiskPressureNotifier hands the disk-health loop a way to reach the
// user's phones. Called from runServe once the BlackBox manager exists.
func bindDiskPressureNotifier(mgr *BlackBoxManager) {
	diskPressureMu.Lock()
	diskPressureNotifier = mgr
	diskPressureMu.Unlock()
}

// notifyDiskPressure pushes a disk alert to the user's paired phones.
// Best-effort: an unpaired box still logs and still serves /machine/health.
func notifyDiskPressure(alerts []string) {
	if len(alerts) == 0 {
		return
	}
	diskPressureMu.RLock()
	mgr := diskPressureNotifier
	diskPressureMu.RUnlock()
	if mgr == nil {
		return // no paired phone; the alert is still on /machine/health
	}

	// Only push for SPACE alerts. SMART "your drive is dying" is a different
	// problem with a different remedy (back up, replace the disk) and does
	// not belong in a "free some space?" flow.
	var spaceAlerts []string
	for _, a := range alerts {
		if strings.Contains(a, "full") {
			spaceAlerts = append(spaceAlerts, a)
		}
	}
	if len(spaceAlerts) == 0 {
		return
	}

	data := map[string]interface{}{
		"alerts":   spaceAlerts,
		"hostname": machineHealthHostname(),
		"deepLink": "yaver://storage",
	}

	// Attach the reclaimable figure if a scan is already warm — this is what
	// makes the notification actionable rather than merely stressful. Never
	// trigger a scan here: the disk-health loop runs on a timer and must not
	// grow a du storm as a side effect.
	scanCacheMu.Lock()
	if scanCache != nil {
		data["reclaimableBytes"] = scanCache.TotalReclaimableBytes
		data["reclaimable"] = formatBytes(scanCache.TotalReclaimableBytes)
	}
	scanCacheMu.Unlock()

	if pct, free, ok := worstFilesystemPressure(); ok {
		data["usedPct"] = pct
		data["freeGb"] = free
	}

	res := runDeviceBroadcastCommand(mgr, deviceBroadcastCommandArgs{
		Command: "storage_pressure",
		Data:    data,
	})
	if ok, _ := res["ok"].(bool); !ok {
		if msg, _ := res["error"].(string); msg != "" {
			fmt.Printf("[disk-health] phone notify failed: %s\n", msg)
		}
	}
}

func machineHealthHostname() string {
	machineHealthMu.RLock()
	defer machineHealthMu.RUnlock()
	return machineHealth.Hostname
}

// worstFilesystemPressure returns the fullest user-visible filesystem — the
// one the alert is actually about.
func worstFilesystemPressure() (usedPct float64, freeGB float64, ok bool) {
	machineHealthMu.RLock()
	fs := append([]DiskSpaceEntry(nil), machineHealth.Filesystems...)
	machineHealthMu.RUnlock()

	for _, f := range userVisibleFilesystems(fs) {
		if f.UsedPct > usedPct {
			usedPct, freeGB, ok = f.UsedPct, f.FreeGB, true
		}
	}
	return
}
