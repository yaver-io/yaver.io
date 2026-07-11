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

// DeployCapabilityTool is the per-tool detail line surfaced to UIs so
// they can render "xcodebuild: 16.0 found at /Applications/Xcode.app",
// "gradle: missing — sudo gem install cocoapods" etc. instead of a
// flat name array. Found / Path / Version come straight from the
// existing BuildDoctorReport.Tools probe.
type DeployCapabilityTool struct {
	Name        string `json:"name"`
	Required    bool   `json:"required"`
	Found       bool   `json:"found"`
	Path        string `json:"path,omitempty"`
	Version     string `json:"version,omitempty"`
	InstallHint string `json:"install_hint,omitempty"`
	// DeepValid mirrors BuildToolResult.DeepValid — the deep probe
	// for tools that have one (xcodebuild = real Xcode vs CLT stub,
	// java = major version >= 17). nil when no deep check applies.
	DeepValid *bool  `json:"deep_valid,omitempty"`
	DeepError string `json:"deep_error,omitempty"`
	// PlatformSkipped — the tool's Platforms list excluded this
	// host. UI surfaces this as a row reason ("xcodebuild only on
	// macOS; this device runs Linux") so the user understands why
	// the tool wasn't even probed.
	PlatformSkipped bool   `json:"platform_skipped,omitempty"`
	SkipReason      string `json:"skip_reason,omitempty"`
}

// DeployCapabilitySecret is the per-secret detail line. Source tells
// the UI where the value lives (vault project / global / env) so the
// "fix this" button knows which vault project to write into. PathValid
// + PathError surface filesystem checks for path-shaped secrets
// (APP_STORE_KEY_PATH, PLAY_STORE_KEY_FILE) — a non-existent file is
// just as deploy-blocking as a missing vault entry.
type DeployCapabilitySecret struct {
	Name      string `json:"name"`
	Found     bool   `json:"found"`
	Source    string `json:"source,omitempty"`  // "vault:project" / "vault:global" / "env"
	Project   string `json:"project,omitempty"` // vault project name when Source starts vault:
	PathValid *bool  `json:"path_valid,omitempty"`
	PathError string `json:"path_error,omitempty"`
}

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
//	Tools / Secrets  — full per-item detail (see DeployCapabilityTool /
//	                    DeployCapabilitySecret). The UI renders rows
//	                    against these — e.g. "Xcode: not installed",
//	                    "APP_STORE_KEY_PATH: file missing at /Users/…"
//	                    so the user knows exactly which thing is
//	                    blocking and what to do about it.
//	MissingTools     — convenience flat list (subset of Tools). Same
//	                    truth as iterating Tools where Required &&
//	                    !Found && !PlatformSkipped — kept because old
//	                    UIs may already render off the flat array.
//	MissingSecrets   — convenience flat list (subset of Secrets).
//	Warnings         — non-fatal hints (deep-probe quirks, stale
//	                    archive, etc.) the UI shows below the headline.
//	                    Empty strings are filtered out so a probe that
//	                    couldn't surface a reason doesn't render as a
//	                    blank line.
//	Reason           — one-line headline shown when CanDeploy=false.
//	                    Priority: platform-lock → missing tools →
//	                    missing secrets.
//	CIAlternative    — the GH Actions workflow that can do this deploy
//	                    instead, when the local device can't (empty
//	                    string when there is no CI alternative).
//	VaultProject     — the vault project the resolver looked under
//	                    (after the target-default fallback applied).
//	                    UIs reuse this when offering "save secret" or
//	                    "sync from peer" so the fix lands in the same
//	                    project the resolver expected.
type DeployCapability struct {
	Target         string                   `json:"target"`
	Stack          string                   `json:"stack,omitempty"`
	CanDeploy      bool                     `json:"can_deploy"`
	PlatformLock   string                   `json:"platform_lock,omitempty"`
	Tools          []DeployCapabilityTool   `json:"tools,omitempty"`
	Secrets        []DeployCapabilitySecret `json:"secrets,omitempty"`
	MissingTools   []string                 `json:"missing_tools,omitempty"`
	MissingSecrets []string                 `json:"missing_secrets,omitempty"`
	Warnings       []string                 `json:"warnings,omitempty"`
	Reason         string                   `json:"reason,omitempty"`
	CIAlternative  string                   `json:"ci_alternative,omitempty"`
	VaultProject   string                   `json:"vault_project,omitempty"`
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
	DeviceID string             `json:"device_id"`
	Platform string             `json:"platform"`
	Arch     string             `json:"arch"`
	IsWSL    bool               `json:"is_wsl"`
	Targets  []DeployCapability `json:"targets"`
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
	"testflight":        "release-mobile.yml workflow_dispatch (upload_testflight=true)",
	"playstore":         "release-mobile.yml workflow_dispatch (upload_playstore=true)",
	"convex":            "release-web.yml on web/* tag (also runs convex deploy)",
	"convex-selfhosted": "release-web.yml on web/* tag (self-hosted Convex deploy script)",
	"cloudflare":        "release-web.yml on web/* tag",
}

// targetDefaultVaultProject is the vault project a target's secrets
// canonically live under. The mobile UI passes a phone-project slug
// (e.g. "myapp") as the project query param; if that slug doesn't
// have the secrets, we fall back to the target's default project so
// shared signing materials stored once in `mobile`/`backend`/`web`
// continue to satisfy capability checks across every phone-project
// the user creates.
var targetDefaultVaultProject = map[string]string{
	"testflight":           "mobile",
	"playstore":            "mobile",
	"playstore-production": "mobile",
	"convex":               "backend",
	"convex-selfhosted":    "backend",
	"cloudflare":           "web",
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

// resolveVaultProject picks the project the BuildDoctor probe should
// consult. Order: caller's explicit project (if it has any of the
// target's secrets) → target's default project → empty (global only).
// Saves the UI from having to know the per-target naming convention.
func resolveVaultProject(target, callerProject string, vs *VaultStore) string {
	if vs == nil {
		return strings.TrimSpace(callerProject)
	}
	bt, ok := buildTargets[target]
	if !ok {
		return strings.TrimSpace(callerProject)
	}
	if cp := strings.TrimSpace(callerProject); cp != "" {
		// Trust the caller's project if it actually contains any of
		// the target's secrets; otherwise fall through to default.
		for _, name := range bt.Secrets {
			if e, err := vs.Get(cp, name); err == nil && e != nil && e.Value != "" {
				return cp
			}
		}
	}
	if def, ok := targetDefaultVaultProject[target]; ok {
		return def
	}
	return strings.TrimSpace(callerProject)
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

	// Pick the vault project to consult. Caller's slug wins if it has
	// the target's secrets; otherwise we fall back to the canonical
	// `mobile` / `backend` / `web` project so shared signing materials
	// stay reachable from any per-app deploy UI.
	resolvedProject := resolveVaultProject(target, project, vs)
	cap.VaultProject = resolvedProject

	rep, err := RunBuildDoctor(target, resolvedProject, vs)
	if err != nil {
		cap.Reason = err.Error()
		return cap
	}
	for _, tool := range rep.Tools {
		row := DeployCapabilityTool{
			Name:            tool.Name,
			Required:        tool.Required,
			Found:           tool.Found,
			Path:            tool.Path,
			Version:         tool.Version,
			InstallHint:     tool.InstallHint,
			DeepValid:       tool.DeepValid,
			DeepError:       tool.DeepError,
			PlatformSkipped: tool.Skipped,
			SkipReason:      tool.SkipReason,
		}
		cap.Tools = append(cap.Tools, row)
		if tool.Skipped {
			continue
		}
		if tool.Required && !tool.Found {
			cap.MissingTools = append(cap.MissingTools, tool.Name)
		}
		if tool.DeepValid != nil && !*tool.DeepValid && tool.DeepError != "" {
			cap.Warnings = append(cap.Warnings, tool.Name+": "+tool.DeepError)
		}
	}
	for _, sec := range rep.Secrets {
		row := DeployCapabilitySecret{
			Name:      sec.Name,
			Found:     sec.Found,
			Source:    sec.Source,
			Project:   sec.Project,
			PathValid: sec.PathValid,
			PathError: sec.PathError,
		}
		cap.Secrets = append(cap.Secrets, row)
		if !sec.Found {
			cap.MissingSecrets = append(cap.MissingSecrets, sec.Name)
		}
		if sec.PathValid != nil && !*sec.PathValid && sec.PathError != "" {
			cap.Warnings = append(cap.Warnings, sec.Name+": "+sec.PathError)
		}
	}

	// CanDeploy needs every required tool + every secret + no path
	// validation failures. A path-secret with PathValid=false counts
	// as missing — the script will read the path and fail at first
	// open even though the vault entry exists.
	pathBroken := false
	for _, sec := range cap.Secrets {
		if sec.PathValid != nil && !*sec.PathValid {
			pathBroken = true
			break
		}
	}
	switch {
	case len(cap.MissingTools) > 0:
		cap.Reason = "missing tools: " + strings.Join(cap.MissingTools, ", ")
	case len(cap.MissingSecrets) > 0:
		cap.Reason = "missing vault entries: " + strings.Join(cap.MissingSecrets, ", ")
	case pathBroken:
		cap.Reason = "secret references a file that doesn't exist on this host"
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
