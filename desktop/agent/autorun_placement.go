package main

// autorun_placement.go — decide WHICH machine can do a given build or deploy,
// and say plainly why the others cannot.
//
// The gap this closes, hit by hand during the 2026-07-18 release: on one Mac, in
// one deploy, Convex went local (logged-in CLI present) while web went to CI
// (CLOUDFLARE_API_TOKEN exists only as a GitHub secret and cannot be read back),
// and TestFlight could only ever go from that Mac because CI runners lack the
// registered device UDIDs. Three different destinations for three targets, all
// decided by a human reading a memory file.
//
// That decision is mechanical. A target is placeable on a machine when three
// things hold, and they fail for different reasons and want different fallbacks:
//
//	platform   — tvOS/iOS archives demand macOS + Xcode; Go builds anywhere
//	credential — present on this box, or CI-only, or genuinely absent
//	quota      — TestFlight's ~15-20/day, counted, and with NO rollback
//
// What this file does NOT do is score preference — agent_mesh.go already scores
// readiness, affinity, pinning, locality and cost lane, and doing it twice is how
// two planners start disagreeing. This answers the prior question agent_mesh
// never asks: *can this machine do it at all?* Feed the eligible set to that
// scorer and it picks the best of them.
//
// See docs/architecture/AUTORUN_TASK_GRAPH.md.

import (
	"fmt"
	"sort"
	"strings"
)

// autorunPlacementKind separates "compile it" from "publish it". The same
// target name means different requirements: building iOS needs Xcode, while
// deploying it additionally needs App Store Connect credentials AND upload quota.
type autorunPlacementKind string

const (
	placementBuild  autorunPlacementKind = "build"
	placementDeploy autorunPlacementKind = "deploy"
)

// autorunPlacementRoute is where work can go when no owned machine qualifies.
type autorunPlacementRoute string

const (
	// routeMachine — an owned machine can do it. The normal case.
	routeMachine autorunPlacementRoute = "machine"
	// routeCI — no machine can, but CI holds the credential. This is a ROUTING
	// decision, not a failure, and must be recorded as one: reporting it as an
	// error sends someone to debug a healthy fleet.
	routeCI autorunPlacementRoute = "ci"
	// routeParked — nothing can do it now and retrying will not help. Quota
	// exhaustion is the case that matters: TestFlight has no rollback, so a
	// retry burns the next slot rather than recovering the last one.
	routeParked autorunPlacementRoute = "parked"
	// routeImpossible — no machine, no CI, no amount of waiting.
	routeImpossible autorunPlacementRoute = "impossible"
)

// autorunPlacement is the answer, including the rejected machines. The rejects
// are not debug noise: "why did my iOS build go to CI?" is the question people
// actually ask, and an answer that cannot name the missing capability just moves
// the investigation somewhere else.
type autorunPlacement struct {
	Target     string                `json:"target"`
	Kind       autorunPlacementKind  `json:"kind"`
	Route      autorunPlacementRoute `json:"route"`
	Eligible   []string              `json:"eligible"` // deviceIDs, capability-only (unscored)
	Rejected   map[string]string     `json:"rejected"` // deviceID -> why not
	Reason     string                `json:"reason"`
	CIWorkflow string                `json:"ciWorkflow,omitempty"`
}

// autorunTargetRule states what a target needs, and what to do when nothing has
// it. Written as data so adding a target is one row, not a new code path.
var autorunTargetRules = map[string]struct {
	// Build reports whether m can COMPILE this target.
	Build func(m MachineInfo) (bool, string)
	// Deploy reports whether m can PUBLISH it. Nil means "same as build".
	Deploy func(m MachineInfo) (bool, string)
	// CIWorkflow is the fallback when no machine holds the credential. Empty
	// means CI cannot do it either — true for TestFlight, deliberately.
	CIWorkflow string
	// CIOnly marks targets whose credential lives ONLY in CI, so the local
	// answer is "route to CI", not "this box is broken".
	CIOnly bool
}{
	"ios": {
		Build:  func(m MachineInfo) (bool, string) { return capIOS(m) },
		Deploy: func(m MachineInfo) (bool, string) { return capTestFlight(m) },
		// release-mobile.yml is `if: false` on purpose: GitHub runner keychains
		// do not carry the registered iPhone UDIDs, so CI cannot substitute.
		CIWorkflow: "",
	},
	"tvos": {
		Build:      func(m MachineInfo) (bool, string) { return capIOS(m) },
		Deploy:     func(m MachineInfo) (bool, string) { return capTestFlight(m) },
		CIWorkflow: "",
	},
	"watchos": {
		Build: func(m MachineInfo) (bool, string) { return capIOS(m) },
		// The watch app is a COMPANION with no App Store record of its own — it
		// ships inside the iPhone binary. "Deploying watchOS" means deploying iOS.
		Deploy:     func(m MachineInfo) (bool, string) { return capTestFlight(m) },
		CIWorkflow: "",
	},
	"android": {
		Build:      func(m MachineInfo) (bool, string) { return capAndroid(m) },
		Deploy:     func(m MachineInfo) (bool, string) { return capPlayStore(m) },
		CIWorkflow: "release-mobile.yml",
	},
	"web": {
		Build: func(m MachineInfo) (bool, string) { return capOnline(m) },
		// CLOUDFLARE_API_TOKEN is a GitHub secret and cannot be read back, so no
		// owned machine can publish web even though every one of them can build it.
		Deploy:     func(m MachineInfo) (bool, string) { return false, "CLOUDFLARE_API_TOKEN is CI-only" },
		CIWorkflow: "release-web.yml",
		CIOnly:     true,
	},
	"agent": {
		Build: func(m MachineInfo) (bool, string) { return capOnline(m) },
		// The npm package ships a signed, notarized 5-platform matrix that only
		// the release workflow produces; publishing the wrapper alone yields a
		// package whose postinstall cannot fetch its binary.
		Deploy:     func(m MachineInfo) (bool, string) { return false, "NPM_TOKEN + signed build matrix are CI-only" },
		CIWorkflow: "release-cli.yml",
		CIOnly:     true,
	},
	"convex": {
		Build:      func(m MachineInfo) (bool, string) { return capOnline(m) },
		Deploy:     func(m MachineInfo) (bool, string) { return capOnline(m) },
		CIWorkflow: "",
	},
	"sdk": {
		Build:      func(m MachineInfo) (bool, string) { return capOnline(m) },
		Deploy:     func(m MachineInfo) (bool, string) { return capOnline(m) },
		CIWorkflow: "",
	},
}

func capOnline(m MachineInfo) (bool, string) {
	if !m.IsOnline {
		return false, "offline"
	}
	return true, ""
}

func capIOS(m MachineInfo) (bool, string) {
	if ok, why := capOnline(m); !ok {
		return false, why
	}
	if m.Capabilities == nil {
		return false, "capabilities unknown"
	}
	if !m.Capabilities.SupportsIOS {
		return false, "no iOS toolchain (needs macOS + Xcode)"
	}
	return true, ""
}

func capAndroid(m MachineInfo) (bool, string) {
	if ok, why := capOnline(m); !ok {
		return false, why
	}
	if m.Capabilities == nil {
		return false, "capabilities unknown"
	}
	if !m.Capabilities.SupportsAndroid {
		return false, "no Android SDK"
	}
	return true, ""
}

func capTestFlight(m MachineInfo) (bool, string) {
	if ok, why := capIOS(m); !ok {
		return false, why
	}
	if !m.Capabilities.SupportsTestFlight {
		return false, "no App Store Connect credentials"
	}
	return true, ""
}

func capPlayStore(m MachineInfo) (bool, string) {
	if ok, why := capAndroid(m); !ok {
		return false, why
	}
	if !m.Capabilities.SupportsPlayStore {
		return false, "no Play service-account credentials"
	}
	return true, ""
}

// placeAutorunTarget answers "who can do this, and if nobody, what now?".
//
// quotaExhausted is passed in rather than probed here so the caller owns the
// external call; it exists because an exhausted quota must PARK, and parking is
// a different outcome from "no machine has the credential".
func placeAutorunTarget(target string, kind autorunPlacementKind, machines []MachineInfo, quotaExhausted bool) autorunPlacement {
	p := autorunPlacement{
		Target:   strings.TrimSpace(strings.ToLower(target)),
		Kind:     kind,
		Rejected: map[string]string{},
	}

	rule, known := autorunTargetRules[p.Target]
	if !known {
		p.Route = routeImpossible
		p.Reason = fmt.Sprintf("unknown target %q — add a row to autorunTargetRules rather than guessing", target)
		return p
	}

	// Quota is checked before capability on purpose: a box that CAN deploy is
	// not a reason to deploy when the daily allowance is gone, and TestFlight
	// has no rollback, so the retry would consume tomorrow's slot too.
	if kind == placementDeploy && quotaExhausted {
		p.Route = routeParked
		p.Reason = fmt.Sprintf("%s deploy quota is exhausted — parking, because a retry burns the next slot rather than recovering the last one", p.Target)
		return p
	}

	check := rule.Build
	if kind == placementDeploy && rule.Deploy != nil {
		check = rule.Deploy
	}
	for _, m := range machines {
		ok, why := check(m)
		if ok {
			p.Eligible = append(p.Eligible, m.DeviceID)
			continue
		}
		p.Rejected[m.DeviceID] = why
	}
	sort.Strings(p.Eligible)

	if len(p.Eligible) > 0 {
		p.Route = routeMachine
		p.Reason = fmt.Sprintf("%d machine(s) can %s %s", len(p.Eligible), kind, p.Target)
		return p
	}
	if rule.CIWorkflow != "" {
		p.Route = routeCI
		p.CIWorkflow = rule.CIWorkflow
		if rule.CIOnly {
			p.Reason = fmt.Sprintf("no owned machine holds the %s credential — it lives only in CI; dispatching %s is the intended path, not a failure", p.Target, rule.CIWorkflow)
		} else {
			p.Reason = fmt.Sprintf("no eligible machine for %s; falling back to %s", p.Target, rule.CIWorkflow)
		}
		return p
	}
	p.Route = routeImpossible
	p.Reason = fmt.Sprintf("no machine can %s %s and there is no CI fallback for it", kind, p.Target)
	return p
}

// autorunPlacementPlan places every target a run implies, so a caller sees the
// whole picture before starting rather than discovering the third one late.
func autorunPlacementPlan(areas []string, kind autorunPlacementKind, machines []MachineInfo, quotaExhausted map[string]bool) []autorunPlacement {
	targets := autorunBuildTargetsForAreas(areas)
	out := make([]autorunPlacement, 0, len(targets))
	for _, t := range targets {
		out = append(out, placeAutorunTarget(t, kind, machines, quotaExhausted[t]))
	}
	return out
}
