package main

// mcp_owner_gate.go — owner-only visibility for the experimental hardware
// cells. Yaver's default product surface is the AI coding / preview / deploy
// loop; the lab-hardware cells (robot arm, circuit sim, 3D printer, Apple TV /
// capture) are owner-only so a normal user sees a simpler product.
//
// Design:
//   - Filter by TOOL-NAME PREFIX, not by editing each cell's files. Any NEW
//     robot_* / arm_* / circuit_* verb added later is hidden automatically.
//   - Owner identity is the SERVER-computed flag user.isOwner, delivered on
//     /auth/validate and surfaced as AuthStatusSnapshot.IsOwner. One source of
//     truth (backend/convex/ownerAllowlist.ts, set via the CLOUD_PREVIEW_OWNER_*
//     Convex env) shared by web, mobile, and this daemon — no owner identity is
//     baked into any client or this public repo. Default (no owner configured)
//     = nobody is owner = the cells are hidden for everyone (simplified product).
//   - Applied in two places: the tools/list (so non-owners never SEE the
//     tools) and the tools/call dispatch (so a guessed name can't be CALLED).

import (
	"strings"
	"sync"
	"time"
)

// ownerOnlyToolPrefixes are the tool-name prefixes for the experimental
// hardware cells. A tool whose name starts with any of these is owner-only.
// IoT/home (hue_/govee_/shelly_/sonos_/ha_/mqtt_), EV (ev_*), and every
// dev/deploy/mobile tool are intentionally NOT here — they stay public.
var ownerOnlyToolPrefixes = []string{
	"robot_",   // robot arm / robotics ops (incl. robot_camera image tool)
	"arm_",     // generic multi-DOF arm layer
	"jig_",     // wiring-harness / fixture jig ops
	"circuit_", // circuit simulator (incl. circuit_plot image tool)
	"printer_", // 3D printer control
	"cad_",     // OpenSCAD / CAD render
	"screw_",   // screw-driving cell analytics
	"appletv_", // Apple TV control (incl. appletv_now_playing image tool)
	"capture_", // capture-card / HDMI streaming
}

// mcpToolIsOwnerOnly reports whether a tool name belongs to an owner-only
// experimental hardware cell.
func mcpToolIsOwnerOnly(name string) bool {
	name = strings.TrimSpace(name)
	for _, p := range ownerOnlyToolPrefixes {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

// --- owner identity (env-driven, mirrors ownerAllowlist.ts) -----------------

// currentUserIsOwner reports whether the daemon's authenticated user is an
// owner, per the SERVER-computed ownerAllowlist flag delivered on
// /auth/validate (AuthStatusSnapshot.IsOwner) — one source of truth, no owner
// identity configured on the client/daemon. The verdict is cached because
// authStatusSnapshot validates the token against Convex (a network call) and
// we must not do that on every tools/call. On a transient failure to resolve
// the user (offline / not signed in) we reuse the last known verdict, else
// fail closed (hide owner-only tools).
var (
	ownerVerdictMu      sync.Mutex
	ownerVerdictKnown   bool
	ownerVerdictValue   bool
	ownerVerdictExpires time.Time
	// ownerVerdictTTL is overridable in tests.
	ownerVerdictTTL = 5 * time.Minute
)

func currentUserIsOwner() bool {
	ownerVerdictMu.Lock()
	defer ownerVerdictMu.Unlock()

	now := time.Now()
	if ownerVerdictKnown && now.Before(ownerVerdictExpires) {
		return ownerVerdictValue
	}

	snap := authStatusSnapshot()
	if !snap.SignedIn {
		// Couldn't confirm the user. Reuse a prior verdict if we have one,
		// else fail closed so non-owners never see owner-only tools.
		if ownerVerdictKnown {
			return ownerVerdictValue
		}
		return false
	}

	ownerVerdictKnown = true
	ownerVerdictValue = snap.IsOwner
	ownerVerdictExpires = now.Add(ownerVerdictTTL)
	return snap.IsOwner
}

// resetOwnerVerdictCache clears the cached owner verdict (tests + sign-out).
func resetOwnerVerdictCache() {
	ownerVerdictMu.Lock()
	ownerVerdictKnown = false
	ownerVerdictValue = false
	ownerVerdictExpires = time.Time{}
	ownerVerdictMu.Unlock()
}

// --- list + call gates ------------------------------------------------------

// filterOwnerOnlyTools drops the experimental-hardware tools from a tools
// list unless the caller is the owner. Returns the input unchanged for the
// owner.
func filterOwnerOnlyTools(tools []map[string]interface{}, isOwner bool) []map[string]interface{} {
	if isOwner {
		return tools
	}
	out := make([]map[string]interface{}, 0, len(tools))
	for _, t := range tools {
		name, _ := t["name"].(string)
		if mcpToolIsOwnerOnly(name) {
			continue
		}
		out = append(out, t)
	}
	return out
}

// mcpToolDeniedByOwnerGate denies a tools/call for an owner-only tool when the
// current user is not the owner. Returns nil when allowed.
func mcpToolDeniedByOwnerGate(toolName string) *AccessDeniedReason {
	if !mcpToolIsOwnerOnly(toolName) {
		return nil
	}
	if currentUserIsOwner() {
		return nil
	}
	return &AccessDeniedReason{
		Denied: true,
		Reason: "tool \"" + toolName + "\" is not available on this account",
	}
}
