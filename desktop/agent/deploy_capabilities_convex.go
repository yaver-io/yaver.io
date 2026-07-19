package main

// deploy_capabilities_convex.go — publish this box's REAL deploy capability to
// Convex so every surface can answer "which of my machines can ship this?"
// without first reaching the machine.
//
// WHY THIS EXISTS. `publishCapabilities` (publish_worker.go) has been the only
// cross-machine capability signal, and it is a pure runtime.GOOS switch: every
// darwin box claims it can publish iOS, whether or not Xcode is installed,
// whether or not a signing identity exists, whether or not the keychain can be
// unlocked headlessly. That is the inventory-says-yes/operation-says-no class
// this repo keeps paying for. On 2026-07-19 the mac mini was picked as a deploy
// box while it had NO npm token at all and no google-auth for Play uploads —
// both invisible until something actually tried.
//
// So this rides on ComputeDeployCapability, which probes: binary on PATH AND
// runnable, xcodebuild that is real Xcode rather than a CLT stub, java >= 17,
// secrets resolvable in the vault, path-valued secrets that actually stat.
//
// COST. Probing every target shells out with per-tool timeouts, so it must NOT
// run on the heartbeat's hot path — heartbeats are frequent and this is not.
// Same treatment as hardwareProfile: computed at most once per interval, cached,
// and served from cache on every beat in between. The first beat after start
// carries nothing rather than blocking; the next one carries the result.
//
// PRIVACY. Convex gets target NAMES and nothing else — no tool paths, no
// versions, no secret names, no reason strings. `MissingSecrets` is exactly the
// kind of free text that leaks a secret's name under a respectable field, and
// `DeployCapabilityTool.Path` leaks the home-dir username. Anyone who wants the
// detail calls GET /deploy/capabilities on the box itself, P2P, where it never
// touches our servers. convex_privacy_test.go enforces this.

import (
	"runtime"
	"sort"
	"sync"
	"time"
)

// goosForCapability is the host OS the platform-lock filter compares against.
var goosForCapability = runtime.GOOS

// deployCapabilityRefreshEvery is how stale the published summary may get.
// Deploy capability changes when a human installs a toolchain or adds a vault
// secret — hours, not seconds — and the probe is expensive, so this is
// deliberately long. `yaver doctor build` remains the instant, on-demand path.
const deployCapabilityRefreshEvery = 6 * time.Hour

type deployCapabilitySummary struct {
	Ready    []string
	Blocked  []string
	Computed time.Time
}

var (
	deployCapabilityMu     sync.Mutex
	deployCapabilityCache  *deployCapabilitySummary
	deployCapabilityInFlgt bool
)

// deployCapabilitiesForHeartbeat returns the cached summary, kicking off a
// background refresh when it is missing or stale.
//
// Never blocks the caller: a heartbeat that waits on fourteen tool probes is a
// heartbeat that misses its window, and a device that looks offline because it
// was busy describing itself is strictly worse than one whose capability list
// is six hours old.
func deployCapabilitiesForHeartbeat(vs *VaultStore) *deployCapabilitySummary {
	deployCapabilityMu.Lock()
	cached := deployCapabilityCache
	stale := cached == nil || time.Since(cached.Computed) > deployCapabilityRefreshEvery
	if stale && !deployCapabilityInFlgt {
		deployCapabilityInFlgt = true
		go refreshDeployCapabilitySummary(vs)
	}
	deployCapabilityMu.Unlock()
	return cached
}

func refreshDeployCapabilitySummary(vs *VaultStore) {
	defer func() {
		deployCapabilityMu.Lock()
		deployCapabilityInFlgt = false
		deployCapabilityMu.Unlock()
	}()
	summary := computeDeployCapabilitySummary(vs)
	deployCapabilityMu.Lock()
	deployCapabilityCache = &summary
	deployCapabilityMu.Unlock()
}

// computeDeployCapabilitySummary probes every known target and reduces the
// result to two name lists. Exported shape is deliberately minimal — see the
// PRIVACY note at the top of this file.
func computeDeployCapabilitySummary(vs *VaultStore) deployCapabilitySummary {
	report := BuildDeployCapabilitiesReport(nil, "", "", vs)
	out := deployCapabilitySummary{Computed: time.Now()}
	for _, t := range report.Targets {
		if t.CanDeploy {
			out.Ready = append(out.Ready, t.Target)
			continue
		}
		// A target this OS can never satisfy is not "blocked" — reporting
		// TestFlight as blocked on every Linux box turns the fleet view into a
		// wall of red that says nothing. It is simply not applicable here.
		if t.PlatformLock != "" && t.PlatformLock != runtimeGOOSForCapability() {
			continue
		}
		out.Blocked = append(out.Blocked, t.Target)
	}
	sort.Strings(out.Ready)
	sort.Strings(out.Blocked)
	return out
}

// runtimeGOOSForCapability is a seam so tests can exercise the platform-lock
// filter without cross-compiling.
var runtimeGOOSForCapability = func() string { return goosForCapability }
