package main

import (
	"log"
	"strings"
	"sync"
)

// Agent side of the "update this box even though you can't reach it"
// path. A surface writes desiredAgentVersion onto the device's Convex
// row (POST /devices/request-update); the box discovers it on its own
// next heartbeat and applies it here.
//
// This exists because the direct POST /agent/update that web and mobile
// use requires reachability, and three of our surfaces don't have it:
// tvOS is direct-LAN with no relay, and the watches hold no box host at
// all unless the user opted into standalone mode. All of them can reach
// Convex, so Convex is the only common ground.

var agentUpdateRequestMu sync.Mutex

func claimAndApplyAgentUpdateRequestSingleFlight(baseURL, token, deviceID string) {
	// A heartbeat every 30s can outrun a download + restart. TryLock so
	// a second beat during an in-flight update is dropped rather than
	// queued — by the time this returns, the request is already claimed
	// and cleared anyway.
	if !agentUpdateRequestMu.TryLock() {
		return
	}
	defer agentUpdateRequestMu.Unlock()
	claimAndApplyAgentUpdateRequest(baseURL, token, deviceID)
}

func claimAndApplyAgentUpdateRequest(baseURL, token, deviceID string) {
	requested, err := ClaimAgentUpdateRequest(baseURL, token, deviceID)
	if err != nil {
		log.Printf("[auto-update] Could not claim the update request: %v", err)
		return
	}
	if requested == "" {
		// Raced another claim, or the requester withdrew it. Not an error.
		return
	}

	log.Printf("[auto-update] A surface requested version %q for this box — applying now", requested)
	emitAgentUpdate("queued", "Update requested remotely (%s) — preparing", requested)

	cfg, _ := LoadConfig()

	// A remote request is an explicit instruction from the owner, so it
	// overrides `auto-update disable` for this one run. Pinning a box is
	// a statement about unattended updates, not a refusal to ever be
	// updated — and the operator asking for it right now, by hand, is
	// exactly the attended case. forcedAutoUpdateConfig copies, so the
	// pin survives: the next periodic tick still won't fire.
	forced := forcedAutoUpdateConfig(cfg)

	if !strings.EqualFold(requested, "latest") && requested != "" {
		// A pinned version. checkAutoUpdate only ever resolves GitHub's
		// `latest` and refuses to move backwards, so it cannot honour a
		// pin. Say so rather than silently installing something the
		// caller didn't ask for — a silent substitution here would be
		// indistinguishable, from the dashboard, from a successful pin.
		log.Printf("[auto-update] Pinned version %q requested, but this agent can only track `latest` — installing latest instead is not what was asked; ignoring", requested)
		emitAgentUpdate("error", "Pinned version %s was requested, but this agent can only install the latest release. Update to latest from any surface, or run `yaver update` on the box.", requested)
		return
	}

	// checkAutoUpdate replaces the binary and exits the process on
	// success; systemd/launchd restarts us on the new version. If it
	// returns, it was a no-op (already current) or it failed — either
	// way it has already emitted the reason to the agent-update stream.
	checkAutoUpdate(forced)
}
