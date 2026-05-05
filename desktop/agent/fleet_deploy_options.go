package main

// fleet_deploy_options.go — `GET /fleet/deploy-options?app=<slug>` returns
// "for app <slug>, here is every machine the user can reach + per-target
// deploy capability." Powers the mobile shake-overlay Deploy pane and any
// future desktop/web Deploy picker — one call, one rendered list, no
// client-side platform smarts.
//
// Capability check is a fan-out of `/doctor/build` (already darwin-gated
// via buildTargets[*].Tools[].Platforms). The local agent answers from
// RunBuildDoctor directly; remote devices answer over the same transport
// path that `/deploy/ship --machine` uses (LAN > Tailscale > relay).
//
// Why this lives in the agent and not Convex: the privacy contract keeps
// vault state, doctor reports, and workdir paths off Convex. Doctor
// results are computed on each device and shipped peer-to-peer through
// the user's own auth token; nothing here ever leaves the user's mesh.

import (
	"context"
	"fmt"
	"net/http"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
)

// FleetDeployTargetCap reports whether one machine can deploy one target.
// Matches the shape the mobile Deploy pane consumes — keep field names
// stable; iOS/Android/SDK clients all decode this verbatim.
type FleetDeployTargetCap struct {
	Target string `json:"target"`           // "testflight" | "playstore"
	OK     bool   `json:"ok"`               // true if the agent can run this deploy as-is
	Reason string `json:"reason,omitempty"` // first blocker (missing tool, wrong OS, missing secret)
}

// FleetDeployDevice is one row in the picker.
type FleetDeployDevice struct {
	DeviceID     string                 `json:"deviceId"`
	Name         string                 `json:"name"`
	Alias        string                 `json:"alias,omitempty"`
	Platform     string                 `json:"platform"` // os/arch from heartbeat (e.g. "darwin/arm64")
	IsLocal      bool                   `json:"isLocal"`  // this is the agent serving the request
	IsOnline     bool                   `json:"isOnline"`
	Probed       bool                   `json:"probed"`             // false if the doctor probe failed (offline / unreachable)
	ProbeError   string                 `json:"probeError,omitempty"`
	Capabilities []FleetDeployTargetCap `json:"capabilities"`
}

// FleetDeployOptions is the response body.
type FleetDeployOptions struct {
	App      string              `json:"app"`
	Stack    string              `json:"stack,omitempty"`
	Targets  []string            `json:"targets"` // targets actually probed, in stable order
	Devices  []FleetDeployDevice `json:"devices"`
	Warnings []string            `json:"warnings,omitempty"`
}

// validDeployTargetsForFleet is the subset of buildTargets that we surface
// in the mobile Deploy picker. Cloudflare/convex deploys are valid targets
// of /deploy/ship but the picker today is mobile-app focused.
var validDeployTargetsForFleet = map[string]bool{
	"testflight": true,
	"playstore":  true,
}

// fleetDeployTargetOrder enforces a stable target order in responses so
// the mobile UI doesn't have to sort.
var fleetDeployTargetOrder = []string{"testflight", "playstore"}

// firstBlockerFromReport summarises a BuildDoctorReport into a one-line
// reason. Empty string means OK.
func firstBlockerFromReport(rep BuildDoctorReport) string {
	if rep.OK {
		return ""
	}
	for _, t := range rep.Tools {
		if t.Skipped {
			// Platforms gate — most common case. doctor formats SkipReason
			// as "only on darwin (this host: linux)" already; surface it
			// verbatim with the tool name so the UI shows "xcodebuild:
			// only on darwin (this host: linux)".
			return fmt.Sprintf("%s: %s", t.Name, t.SkipReason)
		}
	}
	for _, t := range rep.Tools {
		if t.Required && !t.Found {
			hint := ""
			if t.InstallHint != "" {
				hint = " — " + t.InstallHint
			}
			return fmt.Sprintf("missing %s%s", t.Name, hint)
		}
	}
	for _, sec := range rep.Secrets {
		if !sec.Found {
			return fmt.Sprintf("missing secret %s (yaver vault add %s)", sec.Name, sec.Name)
		}
	}
	return "preflight failed"
}

// localFleetDevice composes the row for the agent serving this request.
// Capabilities are computed without crossing the wire.
func (s *HTTPServer) localFleetDevice(project string, targets []string) FleetDeployDevice {
	caps := make([]FleetDeployTargetCap, 0, len(targets))
	for _, target := range targets {
		rep, err := RunBuildDoctor(target, project, s.vaultStore)
		if err != nil {
			caps = append(caps, FleetDeployTargetCap{
				Target: target, OK: false, Reason: err.Error(),
			})
			continue
		}
		caps = append(caps, FleetDeployTargetCap{
			Target: target, OK: rep.OK, Reason: firstBlockerFromReport(rep),
		})
	}
	platform := runtime.GOOS + "/" + runtime.GOARCH
	name := s.hostname
	if name == "" {
		name = "this machine"
	}
	return FleetDeployDevice{
		DeviceID:     s.deviceID,
		Name:         name,
		Platform:     platform,
		IsLocal:      true,
		IsOnline:     true,
		Probed:       true,
		Capabilities: caps,
	}
}

// remoteFleetDevice probes one remote device for every requested target
// and composes its row. Errors at the transport layer mark Probed=false
// so the UI can render "couldn't reach this machine" without trying to
// guess capabilities. The probe runs every target sequentially per device
// over a single resolved candidate set — RunBuildDoctor on the remote is
// fast (< 2s for the 4 tools we care about), so we don't need to fan out
// targets within a device.
func remoteFleetDevice(ctx context.Context, info DeviceInfo, project string, targets []string) FleetDeployDevice {
	row := FleetDeployDevice{
		DeviceID: info.DeviceID,
		Name:     info.Name,
		Alias:    info.Alias,
		Platform: info.Platform,
		IsLocal:  false,
		IsOnline: info.IsOnline,
	}
	if !info.IsOnline {
		row.ProbeError = "device offline"
		// Surface "we couldn't probe" rows with all-target unknown so the
		// UI shows them as disabled with a clear reason instead of hiding.
		caps := make([]FleetDeployTargetCap, 0, len(targets))
		for _, target := range targets {
			caps = append(caps, FleetDeployTargetCap{
				Target: target, OK: false, Reason: "device offline",
			})
		}
		row.Capabilities = caps
		return row
	}
	caps := make([]FleetDeployTargetCap, 0, len(targets))
	for _, target := range targets {
		path := "/doctor/build?target=" + target
		if project != "" {
			path += "&project=" + project
		}
		var rep BuildDoctorReport
		probeCtx, cancel := context.WithTimeout(ctx, 6*time.Second)
		err := remoteAgentJSONForDevice(probeCtx, info.DeviceID, "GET", path, nil, &rep)
		cancel()
		if err != nil {
			// First failure aborts the rest of this device's probes —
			// once the candidate set is unreachable, every other target
			// will fail too. Mark all targets unknown with the transport
			// error.
			for _, t := range targets[len(caps):] {
				caps = append(caps, FleetDeployTargetCap{
					Target: t, OK: false, Reason: "unreachable: " + err.Error(),
				})
			}
			row.ProbeError = err.Error()
			row.Capabilities = caps
			return row
		}
		caps = append(caps, FleetDeployTargetCap{
			Target: target, OK: rep.OK, Reason: firstBlockerFromReport(rep),
		})
	}
	row.Probed = true
	row.Capabilities = caps
	return row
}

// handleFleetDeployOptions: GET /fleet/deploy-options?app=<slug>[&targets=...]
//
// Returns FleetDeployOptions. Owner-only for now (see allowGuest). Targets
// default to {testflight, playstore} when ?targets= is omitted; pass a
// comma-separated list to narrow.
func (s *HTTPServer) handleFleetDeployOptions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonReply(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	app := strings.TrimSpace(r.URL.Query().Get("app"))
	if app == "" {
		jsonReply(w, http.StatusBadRequest, map[string]string{"error": "app is required"})
		return
	}

	// Parse targets, validate against the picker's allowlist, and order
	// them stably so the response is deterministic (mobile clients depend
	// on order for column layout).
	var targets []string
	if raw := strings.TrimSpace(r.URL.Query().Get("targets")); raw != "" {
		seen := map[string]bool{}
		for _, t := range strings.Split(raw, ",") {
			t = strings.TrimSpace(t)
			if t == "" || seen[t] {
				continue
			}
			if !validDeployTargetsForFleet[t] {
				jsonReply(w, http.StatusBadRequest, map[string]interface{}{
					"error": "unknown target: " + t,
					"known": fleetDeployTargetOrder,
				})
				return
			}
			seen[t] = true
			targets = append(targets, t)
		}
	}
	if len(targets) == 0 {
		targets = append(targets, fleetDeployTargetOrder...)
	} else {
		// stable order
		ordered := make([]string, 0, len(targets))
		for _, want := range fleetDeployTargetOrder {
			for _, have := range targets {
				if have == want {
					ordered = append(ordered, want)
					break
				}
			}
		}
		targets = ordered
	}

	// Resolve project ref locally for the stack hint + so the local doctor
	// scopes vault lookups to the right project. Failure is non-fatal —
	// the user may have the project on a remote machine but not here.
	stack := ""
	{
		if ref, err := resolveProjectRef(app, ""); err == nil {
			stack = ref.Stack
		}
	}

	out := FleetDeployOptions{
		App:     app,
		Stack:   stack,
		Targets: targets,
	}

	// Local row first.
	out.Devices = append(out.Devices, s.localFleetDevice(app, targets))

	// Remote rows. We pull the full device list from Convex (same source
	// `yaver devices` uses), filter out the local one, and fan out probes
	// in parallel — bounded to maxFleetProbeParallel so a 30-device fleet
	// doesn't fork 30 simultaneous QUIC dials.
	cfg, err := LoadConfig()
	if err != nil {
		out.Warnings = append(out.Warnings, "load config: "+err.Error())
		jsonReply(w, http.StatusOK, out)
		return
	}
	if strings.TrimSpace(cfg.AuthToken) == "" || strings.TrimSpace(cfg.ConvexSiteURL) == "" {
		out.Warnings = append(out.Warnings, "not signed in — only local machine listed")
		jsonReply(w, http.StatusOK, out)
		return
	}
	devices, err := listDevices(cfg.ConvexSiteURL, cfg.AuthToken)
	if err != nil {
		out.Warnings = append(out.Warnings, "list devices: "+err.Error())
		jsonReply(w, http.StatusOK, out)
		return
	}

	remotes := make([]DeviceInfo, 0, len(devices))
	for _, d := range devices {
		if d.DeviceID == "" || d.DeviceID == s.deviceID {
			continue
		}
		if d.IsGuest {
			// Guest grants give the caller access TO that host, not the
			// other way round — they don't run our deploys.
			continue
		}
		remotes = append(remotes, d)
	}

	const maxFleetProbeParallel = 6
	const overallTimeout = 12 * time.Second

	rows := make([]FleetDeployDevice, len(remotes))
	sem := make(chan struct{}, maxFleetProbeParallel)
	var wg sync.WaitGroup
	probeCtx, cancel := context.WithTimeout(r.Context(), overallTimeout)
	defer cancel()
	for i, d := range remotes {
		i, d := i, d
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			rows[i] = remoteFleetDevice(probeCtx, d, app, targets)
		}()
	}
	wg.Wait()
	out.Devices = append(out.Devices, rows...)

	// Stable sort: probed-and-online first (so the picker's first row is
	// usable), then by name.
	sort.SliceStable(out.Devices[1:], func(i, j int) bool {
		a, b := out.Devices[1+i], out.Devices[1+j]
		if a.Probed != b.Probed {
			return a.Probed
		}
		if a.IsOnline != b.IsOnline {
			return a.IsOnline
		}
		return a.Name < b.Name
	})

	jsonReply(w, http.StatusOK, out)
}
