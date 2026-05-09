package main

// deploy_capabilities.go — single per-device, per-target verdict the
// mobile + web deploy UIs render against. Composes the existing
// BuildDoctorReport (tools + vault secrets) with platform locks
// computed from each target's Tools[].Platforms restriction, so a
// Linux box can't be offered "Deploy to TestFlight" even when the
// CLI scripts would shell out and silently fail in xcodebuild.
//
// The UI contract is intentionally narrow — one struct per target,
// boolean CanDeploy + structured Reason + lists of what's missing —
// because pre-deploy gating UIs need a yes/no first and details on
// hover, not a four-level tree of probe data. Anything that wants
// the full diagnostic still has /doctor/build.

import (
	"net/http"
	"runtime"
	"sort"
	"strings"
)

// DeployCapability is the per-target verdict for a single device.
//
//	CanDeploy        — host can attempt the deploy right now (tools
//	                    present, secrets present, OS not blocked).
//	PlatformLock     — non-empty when the target only runs on certain
//	                    GOOS values ("darwin" for TestFlight). UI
//	                    surfaces this as the headline reason: a Linux
//	                    box should see "macOS only — pick another
//	                    device or run via CI" before it sees the
//	                    long missing-tools list.
//	MissingTools     — required tools whose probe failed (subset of
//	                    BuildDoctorReport).
//	MissingSecrets   — vault entries the deploy script will read that
//	                    aren't set.
//	Warnings         — non-fatal hints (stale archive, deep-probe
//	                    quirks, etc.) the UI shows in a less-prominent
//	                    spot so the user can still attempt the deploy.
//	Reason           — one-line headline shown when CanDeploy=false.
//	                    Picked in priority order: platform-lock first,
//	                    then missing tools, then missing secrets.
//	CIAlternative    — the GH Actions workflow that can do this deploy
//	                    instead, when the local device can't (empty
//	                    string when there is no CI alternative).
type DeployCapability struct {
	Target         string   `json:"target"`
	Stack          string   `json:"stack,omitempty"`
	CanDeploy      bool     `json:"can_deploy"`
	PlatformLock   string   `json:"platform_lock,omitempty"`
	MissingTools   []string `json:"missing_tools,omitempty"`
	MissingSecrets []string `json:"missing_secrets,omitempty"`
	Warnings       []string `json:"warnings,omitempty"`
	Reason         string   `json:"reason,omitempty"`
	CIAlternative  string   `json:"ci_alternative,omitempty"`
}

// DeployCapabilitiesReport is the full per-device response.
//
// Platform / Arch / IsWSL surface the host shape so the UI can render
// "macOS arm64" or "Linux (WSL2)" labels next to the device name
// without making a second call. DeviceID is intentionally on the
// envelope (not on each target) — when the mobile app proxies this
// call through Convex relay to a remote agent, the relay rewrites the
// envelope with the real responder's deviceID so the UI never has to
// trust a self-reported value inside the body.
type DeployCapabilitiesReport struct {
	DeviceID  string             `json:"device_id"`
	Platform  string             `json:"platform"`
	Arch      string             `json:"arch"`
	IsWSL     bool               `json:"is_wsl"`
	Targets   []DeployCapability `json:"targets"`
}

// targetCIWorkflow maps a deploy target to the GH Actions workflow
// that can run it from CI when the local device can't. Surfaced to
// the UI as a fallback path: "TestFlight needs macOS — your Linux
// box can trigger release-mobile.yml workflow_dispatch instead."
//
// Keep in sync with .github/workflows/release-*.yml. Empty string
// means there is no CI fallback (everything has one today, but the
// shape stays open for future targets that are inherently local).
var targetCIWorkflow = map[string]string{
	"testflight": "release-mobile.yml workflow_dispatch (upload_testflight=true)",
	"playstore":  "release-mobile.yml workflow_dispatch (upload_playstore=true)",
	"convex":     "release-web.yml on web/* tag (also runs convex deploy)",
	"cloudflare": "release-web.yml on web/* tag",
}

// targetPlatformLock returns the GOOS the target is locked to (single
// value), derived from the target's required Tools[].Platforms list.
// Empty string when the target works on any platform. If different
// required tools demand different platforms, returns the FIRST one —
// in practice every locked target today only locks to a single OS.
func targetPlatformLock(target string) string {
	bt, ok := buildTargets[target]
	if !ok {
		return ""
	}
	for _, tool := range bt.Tools {
		if !tool.Required || len(tool.Platforms) == 0 {
			continue
		}
		// Tool's platform list: if it includes the current GOOS, no
		// effective lock from this tool's perspective. Otherwise the
		// tool's accepted platforms become the target's lock.
		return strings.Join(tool.Platforms, "/")
	}
	return ""
}

// ComputeDeployCapability synthesises the per-target verdict for the
// local device by composing platform-lock metadata with
// RunBuildDoctor's structured tool + secret probe results.
func ComputeDeployCapability(target, project string, vs *VaultStore) DeployCapability {
	cap := DeployCapability{Target: target, CIAlternative: targetCIWorkflow[target]}
	bt, ok := buildTargets[target]
	if !ok {
		cap.Reason = "unknown target"
		return cap
	}
	cap.Stack = bt.Stack

	cap.PlatformLock = targetPlatformLock(target)
	if cap.PlatformLock != "" {
		// Lock includes current GOOS? Then no effective lock for this
		// device. Otherwise the target is OS-blocked for this host.
		platforms := strings.Split(cap.PlatformLock, "/")
		hostMatches := false
		for _, p := range platforms {
			if p == runtime.GOOS {
				hostMatches = true
				break
			}
		}
		if !hostMatches {
			cap.Reason = bt.Name + " only runs on " + cap.PlatformLock + " (this host: " + runtime.GOOS + ")"
			return cap
		}
	}

	rep, err := RunBuildDoctor(target, project, vs)
	if err != nil {
		cap.Reason = err.Error()
		return cap
	}
	for _, tool := range rep.Tools {
		if tool.Skipped {
			// Platform mismatch already surfaced via PlatformLock above.
			continue
		}
		if tool.Required && !tool.Found {
			cap.MissingTools = append(cap.MissingTools, tool.Name)
		}
		if tool.DeepValid != nil && !*tool.DeepValid {
			cap.Warnings = append(cap.Warnings, tool.Name+": "+tool.DeepError)
		}
	}
	for _, sec := range rep.Secrets {
		if !sec.Found {
			cap.MissingSecrets = append(cap.MissingSecrets, sec.Name)
		}
		if sec.PathValid != nil && !*sec.PathValid {
			cap.Warnings = append(cap.Warnings, sec.Name+": "+sec.PathError)
		}
	}

	switch {
	case len(cap.MissingTools) > 0:
		cap.Reason = "missing tools: " + strings.Join(cap.MissingTools, ", ")
	case len(cap.MissingSecrets) > 0:
		cap.Reason = "missing vault entries: " + strings.Join(cap.MissingSecrets, ", ")
	default:
		cap.CanDeploy = true
	}
	return cap
}

// BuildDeployCapabilitiesReport computes capabilities for every known
// target (or just the requested subset) and packages them with the
// host's platform metadata for the UI.
func BuildDeployCapabilitiesReport(targets []string, project, deviceID string, vs *VaultStore) DeployCapabilitiesReport {
	if len(targets) == 0 {
		targets = BuildTargetNames()
	}
	report := DeployCapabilitiesReport{
		DeviceID: deviceID,
		Platform: runtime.GOOS,
		Arch:     runtime.GOARCH,
		IsWSL:    isWSL(),
		Targets:  make([]DeployCapability, 0, len(targets)),
	}
	for _, t := range targets {
		report.Targets = append(report.Targets, ComputeDeployCapability(t, project, vs))
	}
	sort.SliceStable(report.Targets, func(i, j int) bool {
		return report.Targets[i].Target < report.Targets[j].Target
	})
	return report
}

// handleDeployCapabilities: GET /deploy/capabilities[?target=X&project=Y]
//
// Returns DeployCapabilitiesReport for the local device. Mobile + web
// deploy UIs hit this per device they're considering for a deploy and
// render disabled/explained buttons accordingly. When called via
// proxyToDevice, the relay rewrites DeviceID on the envelope before
// delivering, so the UI can trust the responder's identity.
func (s *HTTPServer) handleDeployCapabilities(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonReply(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	target := strings.TrimSpace(r.URL.Query().Get("target"))
	project := strings.TrimSpace(r.URL.Query().Get("project"))

	var targets []string
	if target != "" {
		if _, ok := buildTargets[target]; !ok {
			jsonReply(w, http.StatusBadRequest, map[string]interface{}{
				"error": "unknown target",
				"known": BuildTargetNames(),
			})
			return
		}
		targets = []string{target}
	}
	report := BuildDeployCapabilitiesReport(targets, project, s.deviceID, s.vaultStore)
	jsonReply(w, http.StatusOK, report)
}
