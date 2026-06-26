package main

// mcp_owner_gate.go — owner-only visibility for the experimental hardware
// cells. Yaver's default product surface is the AI coding / preview / deploy
// loop; the lab-hardware cells (robot arm, circuit sim, 3D printer, Apple TV /
// capture) are owner-only so a normal user sees a simpler product.
//
// Design:
//   - Filter by TOOL-NAME PREFIX, not by editing each cell's files. Any NEW
//     robot_* / arm_* / circuit_* verb added later is hidden automatically.
//   - Owner identity is ENV-driven (no personal email baked into this public
//     repo), mirroring backend/convex/ownerAllowlist.ts and the web
//     NEXT_PUBLIC_YAVER_OWNER_EMAIL gate. Default = no owner configured = the
//     cells are hidden for everyone (the simplified default). The owner sets
//     YAVER_OWNER_EMAILS (or the existing CLOUD_PREVIEW_* vars) on their daemon
//     to reveal them.
//   - Applied in two places: the tools/list (so non-owners never SEE the
//     tools) and the tools/call dispatch (so a guessed name can't be CALLED).

import (
	"os"
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

func envCSVLower(keys ...string) []string {
	for _, k := range keys {
		if v := strings.TrimSpace(os.Getenv(k)); v != "" {
			parts := strings.Split(v, ",")
			out := make([]string, 0, len(parts))
			for _, p := range parts {
				if p = strings.ToLower(strings.TrimSpace(p)); p != "" {
					out = append(out, p)
				}
			}
			return out
		}
	}
	return nil
}

func ownerEmails() []string {
	return envCSVLower("YAVER_OWNER_EMAILS", "YAVER_CLOUD_PREVIEW_EMAILS", "CLOUD_PREVIEW_OWNER_EMAIL")
}

func ownerUserIDs() []string {
	return envCSVLower("YAVER_OWNER_USER_IDS", "CLOUD_PREVIEW_OWNER_USER_IDS")
}

// isOwnerUser reports whether the given identity matches a configured owner.
// With no owner env set, nobody is the owner (cells stay hidden — the
// simplified default).
func isOwnerUser(email, userID string) bool {
	if e := strings.ToLower(strings.TrimSpace(email)); e != "" {
		for _, o := range ownerEmails() {
			if o == e {
				return true
			}
		}
	}
	if u := strings.TrimSpace(userID); u != "" {
		for _, o := range ownerUserIDs() {
			if u == o {
				return true
			}
		}
	}
	return false
}

// currentUserIsOwner resolves the daemon's authenticated user and checks it
// against the owner allowlist. The verdict is cached: authStatusSnapshot
// validates the token against Convex (a network call), which we must not do
// on every tools/call. On a transient failure to resolve the user (offline,
// not signed in) we reuse the last known verdict, else fail closed (hide).
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
	if !snap.SignedIn || (strings.TrimSpace(snap.UserEmail) == "" && strings.TrimSpace(snap.UserID) == "") {
		// Couldn't determine the user. Reuse a prior verdict if we have
		// one, else fail closed so non-owners never see owner-only tools.
		if ownerVerdictKnown {
			return ownerVerdictValue
		}
		return false
	}

	v := isOwnerUser(snap.UserEmail, snap.UserID)
	ownerVerdictKnown = true
	ownerVerdictValue = v
	ownerVerdictExpires = now.Add(ownerVerdictTTL)
	return v
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
