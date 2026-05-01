package main

// monorepo_start_runners.go — runner-location detection + matrix
// rendering for `yaver monorepo start` step 3. Splits into a
// separate file because the helpers are useful elsewhere
// (anywhere that wants "show me which runner is authed where" — e.g.
// `yaver code` could borrow this matrix for its picker too) and to
// keep the wizard file readable.

import (
	"fmt"
	"sort"
	"strings"
)

// runnerLocation is one host the user can target with their
// coding agent. ID is "this" for the local machine; for remote
// hosts it's the device ID returned by listDevices. Rows holds
// one entry per supported runner (claude / codex / opencode) with
// the install + auth status probed live for that location.
type runnerLocation struct {
	ID    string
	Label string
	Rows  []runnerAuthStatusRow
}

// supportedMonorepoRunners is the canonical set the wizard surfaces.
// CLAUDE.md project_lean_stack_2026_04_28.md pins the supported list
// to claude / codex / opencode (opencode wraps the long-tail of
// providers via its own BYOK config), and the mobile sandbox uses
// the same allowlist at phone-projects.tsx:256.
var supportedMonorepoRunners = []string{"claude", "codex", "opencode"}

// probeRunnerLocations builds the (location × runner) availability
// matrix. Always includes "this machine" probed via the existing
// collectRunnerAuthStatusRows helper. If the user is signed in to
// Convex AND has online non-mobile non-self devices registered, each
// such device gets a parallel peer-proxy probe via
// fetchRunnerAuthStatusRowsRemote.
//
// All errors are swallowed — the wizard MUST continue even if Convex
// is unreachable or a remote agent is offline. In those cases the
// user just sees a smaller matrix (often just "this") and a
// short status line so they know remote probes were attempted.
func probeRunnerLocations() []*runnerLocation {
	locations := []*runnerLocation{}

	// 1. Local — always present.
	localRows, _ := collectRunnerAuthStatusRows()
	locations = append(locations, &runnerLocation{
		ID:    "this",
		Label: "This machine",
		Rows:  filterSupportedRunnerRows(localRows),
	})

	// 2. Remote — best-effort. Skip silently if not signed in.
	cfg, err := LoadConfig()
	if err != nil || cfg == nil || strings.TrimSpace(cfg.AuthToken) == "" {
		return locations
	}
	devices, err := listDevicesEnsuringAuth(cfg)
	if err != nil || len(devices) == 0 {
		return locations
	}

	for _, d := range devices {
		if !d.IsOnline {
			continue
		}
		if d.IsGuest {
			continue
		}
		if cfg.DeviceID != "" && d.DeviceID == cfg.DeviceID {
			continue
		}
		// Mobile / edge devices don't run coding wrappers — skip.
		platform := strings.ToLower(strings.TrimSpace(d.Platform))
		if strings.HasPrefix(platform, "ios") || strings.HasPrefix(platform, "android") {
			continue
		}
		rows, perr := fetchRunnerAuthStatusRowsRemote(d.DeviceID)
		if perr != nil || len(rows) == 0 {
			// Still surface the device so the user knows it was tried,
			// but mark all runners as unreachable.
			rows = unreachableRunnerRows()
		}
		locations = append(locations, &runnerLocation{
			ID:    d.DeviceID,
			Label: deviceDisplayLabel(d),
			Rows:  filterSupportedRunnerRows(rows),
		})
	}

	return locations
}

// filterSupportedRunnerRows keeps only rows for runners we surface in
// the wizard, and pads any missing entries with a "not present" row
// so the matrix has consistent shape across locations.
func filterSupportedRunnerRows(rows []runnerAuthStatusRow) []runnerAuthStatusRow {
	byID := map[string]runnerAuthStatusRow{}
	for _, r := range rows {
		byID[r.ID] = r
	}
	out := make([]runnerAuthStatusRow, 0, len(supportedMonorepoRunners))
	for _, id := range supportedMonorepoRunners {
		if r, ok := byID[id]; ok {
			out = append(out, r)
			continue
		}
		out = append(out, runnerAuthStatusRow{
			ID:        id,
			Name:      runnerDisplayName(id),
			Installed: false,
			Detail:    "not installed",
		})
	}
	return out
}

// unreachableRunnerRows returns a synthetic row set used when a remote
// device's `/agent/runners` probe fails so the matrix still renders.
func unreachableRunnerRows() []runnerAuthStatusRow {
	rows := make([]runnerAuthStatusRow, 0, len(supportedMonorepoRunners))
	for _, id := range supportedMonorepoRunners {
		rows = append(rows, runnerAuthStatusRow{
			ID:     id,
			Name:   runnerDisplayName(id),
			Detail: "unreachable",
		})
	}
	return rows
}

// printRunnerLocationMatrix renders the locations as a per-host
// indented list with a glyph + one-line status per supported runner.
// Glyph legend: ✓ authed + ready, ○ installed but not authed,
// × not installed (or unreachable).
func printRunnerLocationMatrix(locations []*runnerLocation) {
	if len(locations) == 0 {
		fmt.Println("  (no machines reachable)")
		return
	}
	for _, loc := range locations {
		fmt.Printf("\n  %s [%s]:\n", loc.Label, loc.ID)
		for _, r := range loc.Rows {
			glyph := "×"
			if r.Installed && r.AuthConfigured && r.Ready {
				glyph = "✓"
			} else if r.Installed {
				glyph = "○"
			}
			detail := strings.TrimSpace(r.Detail)
			if detail == "" {
				switch {
				case !r.Installed:
					detail = "not installed"
				case r.AuthConfigured:
					detail = "authenticated"
				default:
					detail = "installed, not signed in"
				}
			}
			fmt.Printf("    %s %-9s %s\n", glyph, r.ID, detail)
		}
	}
	fmt.Println()
}

// runnerLocationIDs is the set of valid Where answers in their
// presentation order.
func runnerLocationIDs(locations []*runnerLocation) []string {
	ids := make([]string, 0, len(locations))
	for _, l := range locations {
		ids = append(ids, l.ID)
	}
	return ids
}

func runnerLocationByID(locations []*runnerLocation, id string) *runnerLocation {
	for _, l := range locations {
		if l.ID == id {
			return l
		}
	}
	if len(locations) > 0 {
		return locations[0]
	}
	return nil
}

// pickDefaultRunnerID picks the runner to suggest as default for the
// chosen location: the first one that's authed + ready; otherwise the
// first installed one; otherwise "claude" (least-surprising default).
// Mirrors the mobile sandbox's pick-first-authed pattern at
// phone-projects.tsx:264.
func pickDefaultRunnerID(loc *runnerLocation) string {
	if loc != nil {
		for _, id := range supportedMonorepoRunners {
			for _, r := range loc.Rows {
				if r.ID == id && r.Installed && r.AuthConfigured && r.Ready {
					return id
				}
			}
		}
		for _, id := range supportedMonorepoRunners {
			for _, r := range loc.Rows {
				if r.ID == id && r.Installed {
					return id
				}
			}
		}
	}
	return "claude"
}

// runnerDisplayName returns the human label used in the matrix for a
// runner ID. Kept tight — the matrix already shows the ID column;
// this is just a fallback when `Name` isn't populated.
func runnerDisplayName(id string) string {
	switch id {
	case "claude":
		return "Claude Code"
	case "codex":
		return "OpenAI Codex"
	case "opencode":
		return "OpenCode"
	}
	return id
}

// deviceDisplayLabel builds a short human label for a remote device,
// preferring alias > hostname > device-name > a truncated device ID.
func deviceDisplayLabel(d DeviceInfo) string {
	if s := strings.TrimSpace(d.Alias); s != "" {
		return s
	}
	if s := strings.TrimSpace(d.HostName); s != "" {
		return s
	}
	if s := strings.TrimSpace(d.Name); s != "" {
		return s
	}
	id := strings.TrimSpace(d.DeviceID)
	if len(id) > 8 {
		id = id[:8]
	}
	if id == "" {
		return "remote"
	}
	return "remote-" + id
}

// orderedSupportedRows returns rows for `supportedMonorepoRunners` in
// stable order. Used by tests; safe to keep public to the package.
func orderedSupportedRows(rows []runnerAuthStatusRow) []runnerAuthStatusRow {
	byID := map[string]runnerAuthStatusRow{}
	for _, r := range rows {
		byID[r.ID] = r
	}
	out := make([]runnerAuthStatusRow, 0, len(supportedMonorepoRunners))
	for _, id := range supportedMonorepoRunners {
		if r, ok := byID[id]; ok {
			out = append(out, r)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
