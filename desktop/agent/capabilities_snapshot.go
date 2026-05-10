package main

import (
	"context"
	"net"
	"net/http"
	"runtime"
	"strings"
	"time"
)

type CapabilityTargetReadiness struct {
	Enabled         bool     `json:"enabled"`
	ReasonCode      string   `json:"reasonCode,omitempty"`
	Reason          string   `json:"reason,omitempty"`
	SuggestedAction string   `json:"suggestedAction,omitempty"`
	Notes           []string `json:"notes,omitempty"`
}

type ConnectivityCapabilitySnapshot struct {
	DirectAvailable    bool `json:"directAvailable"`
	RelayConfigured    bool `json:"relayConfigured"`
	TunnelConfigured   bool `json:"tunnelConfigured"`
	TailscaleAvailable bool `json:"tailscaleAvailable"`
}

type CapabilitySnapshot struct {
	GeneratedAt  string                               `json:"generatedAt"`
	Machine      MachineInfo                          `json:"machine"`
	Infra        InfraSummary                         `json:"infra"`
	Connectivity ConnectivityCapabilitySnapshot       `json:"connectivity"`
	Targets      map[string]CapabilityTargetReadiness `json:"targets"`
}

func (s *HTTPServer) buildCapabilitySnapshot(ctx context.Context) CapabilitySnapshot {
	infra := s.infraSummary(ctx)
	workDir := strings.TrimSpace(infra.Machine.CurrentWorkDir)
	tailscale := DetectTailscale()
	directAvailable := hasReachableLANInterface(infra.Network)
	connectivity := ConnectivityCapabilitySnapshot{
		DirectAvailable:    directAvailable,
		RelayConfigured:    len(infra.Relays) > 0,
		TunnelConfigured:   len(infra.Machine.QuicHost) > 0 || len(infra.Machine.Hostname) > 0 && len(infra.Network) > 0,
		TailscaleAvailable: tailscale != nil && tailscale.Running,
	}

	targets := map[string]CapabilityTargetReadiness{
		"testflight":      capabilityForDoctorTarget("testflight", workDir, s.vaultStore),
		"playstore":       capabilityForDoctorTarget("playstore", workDir, s.vaultStore),
		"mobile-hermes":   capabilityForMobileHermes(),
		"runner-codex":    capabilityForRunner("codex", workDir),
		"runner-claude":   capabilityForRunner("claude", workDir),
		"runner-opencode": capabilityForRunner("opencode", workDir),
		"web-preview": {
			Enabled: connectivity.DirectAvailable || connectivity.RelayConfigured || connectivity.TailscaleAvailable,
		},
	}
	if !targets["web-preview"].Enabled {
		targets["web-preview"] = CapabilityTargetReadiness{
			Enabled:         false,
			ReasonCode:      ReasonConnectivityNoViableTransport,
			Reason:          "This machine does not currently advertise a reachable preview transport.",
			SuggestedAction: "Bring up LAN, relay, tunnel, or Tailscale connectivity before opening preview.",
		}
	}
	s.syncConnectivityIncidents(connectivity, infra.Machine.DeviceID)

	return CapabilitySnapshot{
		GeneratedAt:  time.Now().UTC().Format(time.RFC3339),
		Machine:      infra.Machine,
		Infra:        infra,
		Connectivity: connectivity,
		Targets:      targets,
	}
}

func capabilityForMobileHermes() CapabilityTargetReadiness {
	nodePath, nodeVersion := detectManagedOrSystemNode()
	hermesSummary, hermesErr := embeddedHermescSummary()
	notes := []string{}
	if nodePath != "" {
		notes = append(notes, "Node.js runtime: "+nodeVersion)
	}
	if hermesErr == nil && hermesSummary != "" {
		notes = append(notes, "Embedded hermesc: "+hermesSummary)
	}
	if nodePath != "" && hermesErr == nil {
		return CapabilityTargetReadiness{
			Enabled: true,
			Notes:   append(notes, "Ready for Hermes bundle reload into Yaver mobile."),
		}
	}
	reason := "This machine is not ready for Hermes reload."
	switch {
	case nodePath == "" && hermesErr != nil:
		reason = "Node.js runtime and embedded hermesc validation are missing."
	case nodePath == "":
		reason = "Node.js runtime is missing."
	case hermesErr != nil:
		reason = "Embedded hermesc is unavailable on this machine."
	}
	if hermesErr != nil {
		notes = append(notes, hermesErr.Error())
	}
	return CapabilityTargetReadiness{
		Enabled:         false,
		ReasonCode:      "capability.mobile-hermes.not_ready",
		Reason:          reason,
		SuggestedAction: "Run `yaver install mobile` on this machine, then retry Hermes reload.",
		Notes:           notes,
	}
}

func (s *HTTPServer) syncConnectivityIncidents(connectivity ConnectivityCapabilitySnapshot, deviceID string) {
	key := IncidentKey{
		Category: "connectivity",
		Code:     ReasonConnectivityNoViableTransport,
		DeviceID: strings.TrimSpace(deviceID),
		Target:   "web-preview",
	}
	if connectivity.DirectAvailable || connectivity.RelayConfigured || connectivity.TailscaleAvailable {
		GlobalIncidentStore().ResolveOpenByKey(key, "Connectivity fallback became available.")
		return
	}
	GlobalIncidentStore().UpsertOpen(key, IncidentEvent{
		Timestamp:       time.Now().UnixMilli(),
		Severity:        IncidentSeverityError,
		Category:        "connectivity",
		Code:            ReasonConnectivityNoViableTransport,
		Source:          "capabilities/snapshot",
		Title:           "No viable remote transport",
		UserMessage:     "This machine does not currently expose a usable remote path for preview or remote control.",
		SuggestedAction: "Enable relay, Tailscale, or a directly reachable LAN path before trying remote preview.",
		DeviceID:        strings.TrimSpace(deviceID),
		Target:          "web-preview",
		LogsAvailable:   false,
		Recoverable:     true,
	})
}

func hasReachableLANInterface(ifaces []InfraNetworkInterface) bool {
	for _, iface := range ifaces {
		flags := strings.ToLower(strings.TrimSpace(iface.Flags))
		if strings.Contains(flags, "loopback") {
			continue
		}
		for _, addr := range iface.Addresses {
			host := strings.TrimSpace(addr)
			if idx := strings.Index(host, "/"); idx >= 0 {
				host = host[:idx]
			}
			ip := net.ParseIP(host)
			if ip == nil || ip.IsLoopback() {
				continue
			}
			if ip.To4() != nil {
				return true
			}
		}
	}
	return false
}

func capabilityForDoctorTarget(target, workDir string, vs *VaultStore) CapabilityTargetReadiness {
	report, err := RunBuildDoctor(target, workDir, vs)
	if err != nil {
		return CapabilityTargetReadiness{
			Enabled:         false,
			ReasonCode:      "capability." + target + ".doctor_failed",
			Reason:          err.Error(),
			SuggestedAction: "Re-run the doctor for this target and inspect the host configuration.",
		}
	}
	if report.OK {
		return CapabilityTargetReadiness{Enabled: true, Notes: report.Notes}
	}
	out := CapabilityTargetReadiness{
		Enabled: false,
		Notes:   report.Notes,
	}
	switch target {
	case "testflight":
		out.ReasonCode = ReasonDeployTestFlightXcodeMissing
		out.Reason = "This machine is not ready for TestFlight deploy."
		out.SuggestedAction = "Use a macOS host with Xcode, signing assets, and Apple credentials."
	case "playstore":
		out.ReasonCode = ReasonDeployPlaystoreAndroidSDKMissing
		out.Reason = "This machine is not ready for Play Store deploy."
		out.SuggestedAction = "Install the Android build toolchain and required signing/upload credentials."
	default:
		out.ReasonCode = "capability." + target + ".not_ready"
		out.Reason = "This target is not ready on the current machine."
		out.SuggestedAction = "Resolve the missing prerequisites and try again."
	}
	for _, tool := range report.Tools {
		if tool.Required && !tool.Found && !tool.Skipped {
			out.Reason = "Missing required tool: " + tool.Name
			break
		}
		if tool.Required && tool.Skipped {
			out.Reason = tool.SkipReason
			if target == "testflight" && runtime.GOOS != "darwin" {
				out.ReasonCode = ReasonDeployTestFlightXcodeMissing
			}
			break
		}
	}
	return out
}

func (s *HTTPServer) handleCapabilitiesSnapshot(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, http.StatusMethodNotAllowed, "use GET")
		return
	}
	jsonReply(w, http.StatusOK, map[string]interface{}{
		"ok":       true,
		"snapshot": s.buildCapabilitySnapshot(r.Context()),
	})
}
