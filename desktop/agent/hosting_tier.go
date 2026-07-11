package main

// hosting_tier.go — the authoritative distinction between how a Yaver box is
// hosted, and the lifecycle policy that distinction implies. This is the "clear
// distinction between self-hosted and managed" the product requires: Yaver may
// aggressively cut cost on boxes it provisioned, and must NEVER touch a box the
// customer runs themselves.
//
// Three tiers (user decision 2026-07-11):
//
//	managed      — Yaver infra, customer pays Yaver (metered). A cloudMachines
//	               row with origin != "self-hosted". Yaver owns the bill, so it
//	               auto scale-to-zeros to cut the customer's cost.
//	byo          — the customer's own cloud account, but Yaver provisioned the
//	               box and holds its snapshot/recreate path (a byoMachines row).
//	               The customer pays the provider directly; scale-to-zero still
//	               cuts THEIR bill, and CLAUDE.md requires it for any Hetzner
//	               box. Flagged separately from managed in the UI.
//	self-hosted  — the customer's own pre-existing machine that merely runs the
//	               agent (no provisioning record). Yaver NEVER touches its power
//	               state: no snapshot, no delete, no auto-anything. Hands off.
//
// SAFETY INVARIANT: when provenance is uncertain, classify as self-hosted. An
// auto-delete of the wrong box is unrecoverable; a missed cost saving is not.

import (
	"encoding/json"
	"io"
	"net/url"
	"strings"
	"time"
)

type HostingTier string

const (
	HostingManaged    HostingTier = "managed"
	HostingBYO        HostingTier = "byo"
	HostingSelfHosted HostingTier = "self-hosted"
)

// classifyHostingTier derives the tier from provisioning provenance. Pure so the
// rule is unit-tested in isolation.
//
//	hasManagedCloudRow — a cloudMachines row with origin != "self-hosted"
//	hasByoRow          — a byoMachines row Yaver provisioned (not deleted)
func classifyHostingTier(hasManagedCloudRow, hasByoRow bool) HostingTier {
	switch {
	case hasManagedCloudRow:
		return HostingManaged
	case hasByoRow:
		return HostingBYO
	default:
		return HostingSelfHosted
	}
}

// tierAllowsAutoLifecycle reports whether Yaver may auto-manage this box's power
// (snapshot + scale-to-zero + wake). True for managed and byo — boxes Yaver
// provisioned and can recreate. NEVER true for self-hosted.
func tierAllowsAutoLifecycle(t HostingTier) bool {
	return t == HostingManaged || t == HostingBYO
}

// resolveLocalHostingTier best-effort determines THIS agent's own tier. It asks
// the control plane (GET /machine/hosting), which applies the same three-way
// join as devices.listMyDevices (managed cloudMachines → byo byoMachines →
// self-hosted). Fails safe to self-hosted on any uncertainty (nil cfg, no
// deviceId, endpoint error, unknown tier) so the auto-lifecycle can never act
// on a box we can't positively confirm Yaver provisioned.
func resolveLocalHostingTier(cfg *Config) HostingTier {
	if cfg == nil || strings.TrimSpace(cfg.DeviceID) == "" {
		return HostingSelfHosted
	}
	if tier, ok := fetchHostingTier(cfg, strings.TrimSpace(cfg.DeviceID)); ok {
		return tier
	}
	// Endpoint unreachable → degrade to a local byo check (still safe: byo and
	// self-hosted are both more conservative than managed). Managed can't be
	// confirmed offline, so an unreachable control plane never yields managed.
	if rows, err := fetchByoMachines(cfg); err == nil {
		for i := range rows {
			if rows[i].DeviceID == cfg.DeviceID && rows[i].State != "deleted" {
				return HostingBYO
			}
		}
	}
	return HostingSelfHosted
}

// fetchHostingTier asks the control plane for a device's tier. Returns ok=false
// on any transport/parse error or unrecognized tier so the caller fails safe.
func fetchHostingTier(cfg *Config, deviceID string) (HostingTier, bool) {
	if cfg == nil || strings.TrimSpace(cfg.ConvexSiteURL) == "" {
		return "", false
	}
	u := strings.TrimRight(cfg.ConvexSiteURL, "/") + "/machine/hosting?deviceId=" + url.QueryEscape(deviceID)
	req, err := newBearerRequest("GET", u, cfg.AuthToken, nil)
	if err != nil {
		return "", false
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return "", false
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return "", false
	}
	var out struct {
		OK   bool   `json:"ok"`
		Tier string `json:"tier"`
	}
	if err := json.Unmarshal(body, &out); err != nil || !out.OK {
		return "", false
	}
	switch HostingTier(out.Tier) {
	case HostingManaged:
		return HostingManaged, true
	case HostingBYO:
		return HostingBYO, true
	case HostingSelfHosted:
		return HostingSelfHosted, true
	default:
		return "", false
	}
}

// --- idle → scale-to-zero policy (managed/byo only, idle + grace-confirm) -----

// ScaleToZeroPhase is where an idle box is in the park lifecycle.
type ScaleToZeroPhase string

const (
	ParkSkip    ScaleToZeroPhase = "skip"    // not eligible, or not idle yet
	ParkNotify  ScaleToZeroPhase = "notify"  // idle long enough — warn, start grace
	ParkExecute ScaleToZeroPhase = "execute" // grace elapsed, still idle — snapshot+delete
)

// ScaleToZeroInput is everything the park decision needs. Kept flat + pure so
// the exact "idle + grace confirm" behavior is testable without a clock, a box,
// or Convex.
type ScaleToZeroInput struct {
	Tier           HostingTier
	ActiveSessions int           // live runner PTY sessions on the box
	RecentActivity bool          // connector / task activity inside the idle window
	IdleFor        time.Duration // how long the box has been idle
	IdleTimeout    time.Duration // arm the park after this much idle
	GraceNotified  bool          // a "parking soon" notification was already sent this streak
	GraceFor       time.Duration // time since that notification
	GraceWindow    time.Duration // wait this long after notifying before executing
	KeepAlive      bool          // the user answered keep-alive during the grace window
}

// scaleToZeroDecision implements idle + grace-confirm: arm after IdleTimeout by
// NOTIFYING, then only EXECUTE (snapshot + delete) once the grace window has
// elapsed and the box is still idle and no keep-alive arrived. Self-hosted (and
// any non-provisioned) boxes always skip — the safety invariant.
func scaleToZeroDecision(in ScaleToZeroInput) ScaleToZeroPhase {
	if !tierAllowsAutoLifecycle(in.Tier) {
		return ParkSkip
	}
	// Any sign of life resets the streak — never park a working box.
	if in.ActiveSessions > 0 || in.RecentActivity || in.KeepAlive {
		return ParkSkip
	}
	if !in.GraceNotified {
		if in.IdleFor >= in.IdleTimeout {
			return ParkNotify
		}
		return ParkSkip
	}
	if in.GraceFor >= in.GraceWindow {
		return ParkExecute
	}
	return ParkSkip
}
