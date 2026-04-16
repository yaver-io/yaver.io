package main

import (
	"context"
	"fmt"
	"strings"
)

type AgentNodePlacement struct {
	DeviceID   string `json:"deviceId"`
	DeviceName string `json:"deviceName,omitempty"`
	Runner     string `json:"runner,omitempty"`
	Model      string `json:"model,omitempty"`
	Reason     string `json:"reason,omitempty"`
}

func planGraphPlacements(req AgentGraphCreateRequest, nodes []AgentGraphNodeSpec) []*AgentNodePlacement {
	machines := listAllMachines(context.Background())
	out := make([]*AgentNodePlacement, 0, len(nodes))
	for _, node := range nodes {
		out = append(out, chooseNodePlacement(req, node, machines))
	}
	return out
}

func chooseNodePlacement(req AgentGraphCreateRequest, node AgentGraphNodeSpec, machines []MachineInfo) *AgentNodePlacement {
	candidates := filterPlacementMachines(req, node, machines)
	if len(candidates) == 0 {
		candidates = machines
	}
	if len(candidates) == 0 {
		return &AgentNodePlacement{
			DeviceID: "local",
			Runner:   normalizedPlacementRunner(node.Runner),
			Model:    node.Model,
			Reason:   "no machine inventory available; defaulting to local execution",
		}
	}

	bestScore := -1 << 30
	var best MachineInfo
	bestRunner := normalizedPlacementRunner(node.Runner)
	bestReason := ""
	for _, m := range candidates {
		score, runner, reason := scoreNodePlacement(req, node, m)
		if score > bestScore {
			bestScore = score
			best = m
			bestRunner = runner
			bestReason = reason
		}
	}
	if best.DeviceID == "" {
		best = candidates[0]
	}
	return &AgentNodePlacement{
		DeviceID:   best.DeviceID,
		DeviceName: best.Name,
		Runner:     bestRunner,
		Model:      choosePlacementModel(node, bestRunner),
		Reason:     bestReason,
	}
}

func filterPlacementMachines(req AgentGraphCreateRequest, node AgentGraphNodeSpec, machines []MachineInfo) []MachineInfo {
	allowed := node.AllowedDevices
	if len(allowed) == 0 && strings.TrimSpace(req.PreferredDevice) != "" {
		allowed = []string{req.PreferredDevice}
	}
	if len(allowed) == 0 && strings.TrimSpace(node.PreferredDevice) == "" {
		return machines
	}
	targets := map[string]bool{}
	for _, id := range allowed {
		if v := strings.TrimSpace(id); v != "" {
			targets[v] = true
		}
	}
	if v := strings.TrimSpace(node.PreferredDevice); v != "" {
		targets[v] = true
	}
	out := make([]MachineInfo, 0, len(machines))
	for _, m := range machines {
		if targets[m.DeviceID] || strings.EqualFold(m.Name, node.PreferredDevice) || strings.EqualFold(m.Name, req.PreferredDevice) {
			out = append(out, m)
		}
	}
	return out
}

func scoreNodePlacement(req AgentGraphCreateRequest, node AgentGraphNodeSpec, m MachineInfo) (int, string, string) {
	runner := normalizedPlacementRunner(node.Runner)
	if runner == "" {
		runner = inferPreferredRunner(node, m)
	}
	score := 0
	reasons := []string{}
	if m.IsLocal {
		score += 5
		reasons = append(reasons, "local machine available")
	}
	if strings.TrimSpace(node.PreferredDevice) != "" && (node.PreferredDevice == m.DeviceID || strings.EqualFold(node.PreferredDevice, m.Name)) {
		score += 1000
		reasons = append(reasons, "user pinned this machine")
	}
	if strings.TrimSpace(req.PreferredDevice) != "" && (req.PreferredDevice == m.DeviceID || strings.EqualFold(req.PreferredDevice, m.Name)) {
		score += 900
		reasons = append(reasons, "graph default machine preference")
	}
	if !m.IsOnline {
		score -= 5000
		reasons = append(reasons, "machine offline")
	}
	if readyRunner(m.Capabilities, runner) {
		score += 220
		reasons = append(reasons, "runner ready on machine")
	} else if runner != "" {
		score -= 300
		reasons = append(reasons, "preferred runner not ready here")
	}

	intent := strings.ToLower(strings.Join([]string{node.Title, node.Prompt, node.Target, node.KindString()}, " "))
	switch {
	case strings.Contains(intent, "testflight") || strings.Contains(intent, "ios") || strings.Contains(intent, "xcode"):
		if machineSupportsIOS(m.Capabilities) {
			score += 280
			reasons = append(reasons, "iOS/TestFlight workload prefers macOS tooling")
		} else {
			score -= 180
		}
	case strings.Contains(intent, "android") || strings.Contains(intent, "playstore") || strings.Contains(intent, "gradle") || strings.Contains(intent, "adb"):
		if machineSupportsAndroid(m.Capabilities) {
			score += 260
			reasons = append(reasons, "Android workload prefers Android-capable machine")
		} else {
			score -= 150
		}
	}
	if strings.Contains(intent, "ollama") || strings.Contains(intent, "local llm") {
		if m.Capabilities != nil && m.Capabilities.SupportsLocalLLM {
			score += 260
			reasons = append(reasons, "local LLM requested")
		} else {
			score -= 120
		}
	}

	switch node.Kind {
	case AgentNodeChat, AgentNodeAutoIdeas:
		if runner == "claude-code" || runner == "claude" {
			score += 70
			reasons = append(reasons, "planning/classification favors Claude")
		}
	case AgentNodeAutodev, AgentNodeAutotest:
		if runner == "codex" || runner == "aider-ollama" || runner == "ollama" {
			score += 120
			reasons = append(reasons, "implementation path prefers cheaper/high-throughput runner")
		}
		if m.Capabilities != nil && m.Capabilities.LowPower {
			score -= 70
			reasons = append(reasons, "low-power machine penalized for build/test work")
		}
	}

	limits := defaultProviderLimits(runner)
	if limits.SharedWithInteractive && m.IsLocal && (node.Kind == AgentNodeAutodev || node.Kind == AgentNodeAutotest) {
		score -= 35
		reasons = append(reasons, "shared interactive budget penalized on primary machine")
	}
	if limits.SessionWindow == "" {
		score += 40
		reasons = append(reasons, "runner has effectively no session-window cap")
	}

	if reason := strings.Join(reasons, "; "); reason != "" {
		return score, runner, reason
	}
	return score, runner, fmt.Sprintf("selected %s for %s", m.Name, node.Kind)
}

func inferPreferredRunner(node AgentGraphNodeSpec, m MachineInfo) string {
	intent := strings.ToLower(strings.Join([]string{node.Title, node.Prompt, node.Target, node.KindString()}, " "))
	if strings.Contains(intent, "ollama") || strings.Contains(intent, "local llm") {
		for _, candidate := range []string{"aider-ollama", "ollama", "codex", "claude-code"} {
			if readyRunner(m.Capabilities, candidate) {
				return candidate
			}
		}
	}
	if strings.Contains(intent, "testflight") || strings.Contains(intent, "ios") {
		for _, candidate := range []string{"claude-code", "codex", "aider-ollama", "ollama"} {
			if readyRunner(m.Capabilities, candidate) {
				return candidate
			}
		}
	}
	switch node.Kind {
	case AgentNodeChat:
		for _, candidate := range []string{"claude-code", "codex", "opencode", "aider"} {
			if readyRunner(m.Capabilities, candidate) {
				return candidate
			}
		}
	case AgentNodeAutoIdeas:
		for _, candidate := range []string{"claude-code", "codex", "opencode", "ollama"} {
			if readyRunner(m.Capabilities, candidate) {
				return candidate
			}
		}
	case AgentNodeAutodev, AgentNodeAutotest:
		for _, candidate := range []string{"codex", "aider-ollama", "claude-code", "ollama", "opencode"} {
			if readyRunner(m.Capabilities, candidate) {
				return candidate
			}
		}
	}
	return normalizedPlacementRunner(node.Runner)
}

func normalizedPlacementRunner(runner string) string {
	switch strings.ToLower(strings.TrimSpace(runner)) {
	case "", "auto":
		return ""
	case "claude", "claude-code":
		return "claude-code"
	default:
		return strings.ToLower(strings.TrimSpace(runner))
	}
}

func choosePlacementModel(node AgentGraphNodeSpec, runner string) string {
	if strings.TrimSpace(node.Model) != "" {
		return node.Model
	}
	switch runner {
	case "claude-code":
		if node.Kind == AgentNodeChat {
			return "claude-opus-4-6"
		}
		return "claude-sonnet-4-6"
	case "ollama":
		return "qwen2.5-coder:14b"
	case "aider-ollama":
		return "ollama_chat/qwen2.5-coder:14b"
	default:
		return ""
	}
}

func readyRunner(caps *MachineCapabilities, runner string) bool {
	if caps == nil {
		return runner == ""
	}
	runner = strings.TrimSpace(strings.TrimPrefix(runner, "claude-code"))
	if runner == "" {
		return true
	}
	for _, r := range caps.Runners {
		id := normalizedPlacementRunner(r.ID)
		if id == "" {
			id = normalizedPlacementRunner(runner)
		}
		if normalizedPlacementRunner(runner) == id && r.Ready {
			return true
		}
	}
	return false
}

func machineSupportsIOS(caps *MachineCapabilities) bool {
	return caps != nil && (caps.SupportsIOS || caps.SupportsTestFlight)
}

func machineSupportsAndroid(caps *MachineCapabilities) bool {
	return caps != nil && (caps.SupportsAndroid || caps.SupportsPlayStore)
}

func (n AgentGraphNodeSpec) KindString() string {
	return string(n.Kind)
}
