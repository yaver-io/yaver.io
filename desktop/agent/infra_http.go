package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os/exec"
	"runtime"
	"sort"
	"strings"
)

type InfraNetworkInterface struct {
	Name      string   `json:"name"`
	MAC       string   `json:"mac,omitempty"`
	Flags     string   `json:"flags,omitempty"`
	Addresses []string `json:"addresses,omitempty"`
}

type InfraRelaySummary struct {
	ID               string `json:"id"`
	Label            string `json:"label,omitempty"`
	HttpURL          string `json:"httpUrl,omitempty"`
	QuicAddr         string `json:"quicAddr,omitempty"`
	Region           string `json:"region,omitempty"`
	Source           string `json:"source"`
	PasswordRequired bool   `json:"passwordRequired"`
}

type InfraSharingSummary struct {
	IsShared       bool   `json:"isShared"`
	AccessScope    string `json:"accessScope,omitempty"`
	PendingGuests  int    `json:"pendingGuests"`
	AcceptedGuests int    `json:"acceptedGuests"`
}

type InfraCapabilities struct {
	Terminal       bool `json:"terminal"`
	MCP            bool `json:"mcp"`
	DevServices    bool `json:"devServices"`
	SystemServices bool `json:"systemServices"`
	AgentShutdown  bool `json:"agentShutdown"`
	HostReboot     bool `json:"hostReboot"`
}

type InfraSummary struct {
	Machine         MachineInfo             `json:"machine"`
	Metrics         *HostMetrics            `json:"metrics,omitempty"`
	DevServices     []ServiceStatus         `json:"devServices,omitempty"`
	Network         []InfraNetworkInterface `json:"network,omitempty"`
	Relays          []InfraRelaySummary     `json:"relays,omitempty"`
	Sharing         InfraSharingSummary     `json:"sharing"`
	Sandbox         ContainerSandboxSummary `json:"sandbox"`
	Capabilities    InfraCapabilities       `json:"capabilities"`
	PackageManagers []string                `json:"packageManagers,omitempty"`
	Binaries        []DetectedBinary        `json:"binaries,omitempty"`
}

func (s *HTTPServer) infraSummary(ctx context.Context) InfraSummary {
	workDir := "."
	if s != nil && s.taskMgr != nil && strings.TrimSpace(s.taskMgr.workDir) != "" {
		workDir = s.taskMgr.workDir
	}
	machine := selfMachine(ctx)
	if strings.TrimSpace(s.deviceID) != "" {
		machine.DeviceID = s.deviceID
	}
	machine.Capabilities = detectMachineCapabilities(workDir)

	summary := InfraSummary{
		Machine:         machine,
		Metrics:         infraMetricsSnapshot(ctx),
		DevServices:     infraDevServices(workDir),
		Network:         infraNetworkInterfaces(),
		Relays:          infraRelaySummary(),
		Sharing:         infraSharingSummary(),
		Sandbox:         s.sandboxSummary(),
		Capabilities:    infraCapabilities(),
		PackageManagers: detectPackageManagers(),
		Binaries:        DiscoverInstalledBinaries(),
	}
	return summary
}

// detectPackageManagers probes the PATH for package managers the AI
// runner (claude-code / codex / aider) and the install catalogue can
// use. Returned in priority order so the caller can pick the first
// match. Kept tiny on purpose — distro-specific ones like `zypper` or
// `emerge` can be added on demand, but every entry here has to map
// cleanly to an install command in install_cmd.go.
func detectPackageManagers() []string {
	candidates := []string{
		// OS-native package managers
		"brew", "apt-get", "dnf", "pacman", "zypper", "apk", "pkg",
		// Universal Linux package sources — often present alongside
		// the distro's own manager, so we advertise both.
		"snap", "flatpak",
		// Windows
		"winget", "choco", "scoop",
		// Language-level — listed separately so prompts can pick the
		// right one for whatever the user is trying to install.
		"npm", "pnpm", "yarn", "pip", "pip3", "uv", "pipx",
		"cargo", "go", "gem", "composer", "dart",
		// Generic fallbacks — "curl" is here so one-line installer
		// scripts (Rust, Ollama) always have a matcher.
		"curl",
	}
	out := make([]string, 0, len(candidates))
	for _, name := range candidates {
		// DiscoverBinary also falls back to common install prefixes
		// (~/.local/bin, /snap/bin, /opt/homebrew/bin, ~/.cargo/bin)
		// so this works even when the agent was launched without the
		// user's shell profile — common under systemd and launchd.
		if DiscoverBinary(name) != "" {
			out = append(out, name)
		}
	}
	return out
}

func infraMetricsSnapshot(ctx context.Context) *HostMetrics {
	snap, _ := sampleHostMetrics(ctx, nil)
	return snap
}

func infraDevServices(workDir string) []ServiceStatus {
	sm := NewServicesManager(workDir)
	statuses, err := sm.Status()
	if err != nil {
		return nil
	}
	sort.Slice(statuses, func(i, j int) bool {
		if statuses[i].Running == statuses[j].Running {
			return statuses[i].Name < statuses[j].Name
		}
		return statuses[i].Running
	})
	return statuses
}

func infraNetworkInterfaces() []InfraNetworkInterface {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	out := make([]InfraNetworkInterface, 0, len(ifaces))
	for _, iface := range ifaces {
		addrs, _ := iface.Addrs()
		addrStrs := make([]string, 0, len(addrs))
		for _, addr := range addrs {
			addrStrs = append(addrStrs, addr.String())
		}
		out = append(out, InfraNetworkInterface{
			Name:      iface.Name,
			MAC:       iface.HardwareAddr.String(),
			Flags:     iface.Flags.String(),
			Addresses: addrStrs,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func infraRelaySummary() []InfraRelaySummary {
	cfg, err := LoadConfig()
	if err != nil || cfg == nil {
		return nil
	}
	var out []InfraRelaySummary
	appendRelays := func(relays []RelayServerConfig, source string, globalPassword string) {
		for _, relay := range relays {
			out = append(out, InfraRelaySummary{
				ID:               relay.ID,
				Label:            relay.Label,
				HttpURL:          relay.HttpURL,
				QuicAddr:         relay.QuicAddr,
				Region:           relay.Region,
				Source:           source,
				PasswordRequired: strings.TrimSpace(relay.Password) != "" || strings.TrimSpace(globalPassword) != "",
			})
		}
	}
	appendRelays(cfg.RelayServers, "configured", cfg.RelayPassword)
	appendRelays(cfg.CachedRelayServers, "cached", cfg.CachedRelayPassword)
	sort.Slice(out, func(i, j int) bool {
		if out[i].Source == out[j].Source {
			return out[i].ID < out[j].ID
		}
		return out[i].Source < out[j].Source
	})
	return out
}

func infraSharingSummary() InfraSharingSummary {
	summary := InfraSharingSummary{}
	cfg, err := LoadConfig()
	if err != nil || cfg == nil || strings.TrimSpace(cfg.ConvexSiteURL) == "" || strings.TrimSpace(cfg.AuthToken) == "" {
		return summary
	}
	guests, err := FetchGuestList(cfg.ConvexSiteURL, cfg.AuthToken)
	if err != nil {
		return summary
	}
	for _, guest := range guests {
		switch guest.Status {
		case "accepted":
			summary.AcceptedGuests++
		case "pending":
			summary.PendingGuests++
		}
	}
	return summary
}

func infraCapabilities() InfraCapabilities {
	return InfraCapabilities{
		Terminal:       true,
		MCP:            true,
		DevServices:    true,
		SystemServices: true,
		AgentShutdown:  true,
		HostReboot:     runtime.GOOS == "darwin" || runtime.GOOS == "linux",
	}
}

func (s *HTTPServer) handleInfraSummary(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	writeJSON(w, http.StatusOK, s.infraSummary(r.Context()))
}

func (s *HTTPServer) handleInfraServiceAction(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req struct {
		Scope  string `json:"scope"`
		Name   string `json:"name"`
		Action string `json:"action"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid json body")
		return
	}
	req.Scope = strings.TrimSpace(req.Scope)
	req.Name = strings.TrimSpace(req.Name)
	req.Action = strings.TrimSpace(req.Action)

	switch req.Scope {
	case "dev":
		if req.Name == "" {
			jsonError(w, http.StatusBadRequest, "service name required")
			return
		}
		workDir := "."
		if s.taskMgr != nil && strings.TrimSpace(s.taskMgr.workDir) != "" {
			workDir = s.taskMgr.workDir
		}
		sm := NewServicesManager(workDir)
		var result interface{}
		switch req.Action {
		case "start":
			msg, err := sm.Start(req.Name)
			if err != nil {
				jsonError(w, http.StatusBadRequest, err.Error())
				return
			}
			result = map[string]interface{}{"output": msg}
		case "stop":
			msg, err := sm.Stop(req.Name)
			if err != nil {
				jsonError(w, http.StatusBadRequest, err.Error())
				return
			}
			result = map[string]interface{}{"output": msg}
		case "restart":
			if _, err := sm.Stop(req.Name); err != nil {
				jsonError(w, http.StatusBadRequest, err.Error())
				return
			}
			msg, err := sm.Start(req.Name)
			if err != nil {
				jsonError(w, http.StatusBadRequest, err.Error())
				return
			}
			result = map[string]interface{}{"output": msg}
		default:
			jsonError(w, http.StatusBadRequest, "unsupported dev service action")
			return
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true, "result": result})
	case "system":
		if req.Name == "" {
			jsonError(w, http.StatusBadRequest, "service name required")
			return
		}
		if req.Action == "status" {
			writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true, "result": mcpServiceStatus(req.Name)})
			return
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true, "result": mcpServiceAction(req.Name, req.Action)})
	default:
		jsonError(w, http.StatusBadRequest, "scope must be 'dev' or 'system'")
	}
}

func (s *HTTPServer) handleInfraPower(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req struct {
		Action  string `json:"action"`
		Confirm bool   `json:"confirm"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid json body")
		return
	}
	if !req.Confirm {
		jsonError(w, http.StatusBadRequest, "confirm=true required")
		return
	}
	switch strings.TrimSpace(req.Action) {
	case "agent_shutdown":
		writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true, "action": req.Action})
		go func() {
			if s.onShutdown != nil {
				s.onShutdown()
			}
		}()
	case "host_reboot":
		command, err := infraHostReboot()
		if err != nil {
			jsonError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true, "action": req.Action, "command": command})
	default:
		jsonError(w, http.StatusBadRequest, "unsupported power action")
	}
}

func infraHostReboot() (string, error) {
	type candidate struct {
		name string
		args []string
	}
	var candidates []candidate
	switch runtime.GOOS {
	case "darwin":
		candidates = []candidate{
			{name: "sudo", args: []string{"-n", "shutdown", "-r", "now"}},
			{name: "shutdown", args: []string{"-r", "now"}},
		}
	case "linux":
		candidates = []candidate{
			{name: "sudo", args: []string{"-n", "systemctl", "reboot"}},
			{name: "systemctl", args: []string{"reboot"}},
			{name: "sudo", args: []string{"-n", "reboot"}},
			{name: "reboot"},
		}
	default:
		return "", fmt.Errorf("host reboot unsupported on %s", runtime.GOOS)
	}
	var errs []string
	for _, cand := range candidates {
		cmd := exec.Command(cand.name, cand.args...)
		out, err := cmd.CombinedOutput()
		if err == nil {
			return strings.TrimSpace(strings.Join(append([]string{cand.name}, cand.args...), " ")), nil
		}
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			msg = err.Error()
		}
		errs = append(errs, fmt.Sprintf("%s: %s", cand.name, msg))
	}
	return "", fmt.Errorf("reboot failed: %s", strings.Join(errs, "; "))
}
