package main

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

type AgentNodePlacement struct {
	DeviceID   string `json:"deviceId"`
	DeviceName string `json:"deviceName,omitempty"`
	Runner     string `json:"runner,omitempty"`
	Model      string `json:"model,omitempty"`
	Reason     string `json:"reason,omitempty"`
}

func (p *AgentNodePlacement) DeviceNameOrID() string {
	if p == nil {
		return ""
	}
	if strings.TrimSpace(p.DeviceName) != "" {
		return p.DeviceName
	}
	return p.DeviceID
}

type meshPlannerState struct {
	machines           map[string]MachineInfo
	machineAssignments map[string]int
	runnerAssignments  map[string]int
}

type meshPolicyState struct {
	machines     map[string]MachineInfo
	machineUse   map[string]int
	runnerGlobal map[string]int
}

func planGraphPlacements(req AgentGraphCreateRequest, nodes []AgentGraphNodeSpec) []*AgentNodePlacement {
	machineList := listAllMachines(context.Background())
	state := &meshPlannerState{
		machines:           map[string]MachineInfo{},
		machineAssignments: map[string]int{},
		runnerAssignments:  map[string]int{},
	}
	for _, m := range machineList {
		state.machines[m.DeviceID] = m
	}
	out := make([]*AgentNodePlacement, 0, len(nodes))
	for _, node := range nodes {
		placement := chooseNodePlacement(req, node, machineList, state)
		out = append(out, placement)
		state.reserve(placement)
	}
	return out
}

func (ps *meshPlannerState) reserve(placement *AgentNodePlacement) {
	if ps == nil || placement == nil {
		return
	}
	ps.machineAssignments[placement.DeviceID]++
	ps.runnerAssignments[normalizedPlacementRunner(placement.Runner)]++
}

func buildMeshPolicyState(run *AgentGraphRun, nodeIndex map[string]*AgentGraphNodeState) *meshPolicyState {
	state := &meshPolicyState{
		machines:     map[string]MachineInfo{},
		machineUse:   map[string]int{},
		runnerGlobal: map[string]int{},
	}
	for _, m := range listAllMachines(context.Background()) {
		state.machines[m.DeviceID] = m
	}
	for _, node := range nodeIndex {
		if node.Status != AgentNodeRunning || node.Placement == nil {
			continue
		}
		state.Reserve(node)
	}
	return state
}

func (ps *meshPolicyState) Reserve(node *AgentGraphNodeState) {
	if ps == nil || node == nil || node.Placement == nil {
		return
	}
	ps.machineUse[node.Placement.DeviceID]++
	ps.runnerGlobal[normalizedPlacementRunner(node.Placement.Runner)]++
}

func (ps *meshPolicyState) CanStart(node *AgentGraphNodeState) bool {
	if ps == nil || node == nil || node.Placement == nil {
		return true
	}
	runner := normalizedPlacementRunner(node.Placement.Runner)
	deviceID := node.Placement.DeviceID
	machine := ps.machines[deviceID]
	if cap := machineRunnerGlobalLimit(runner, machine.Capabilities); cap > 0 && ps.runnerGlobal[runner] >= cap {
		return false
	}
	if cap := machineTaskCapacity(machine.Capabilities); cap > 0 && ps.machineUse[deviceID] >= cap {
		return false
	}
	return true
}

func chooseNodePlacement(req AgentGraphCreateRequest, node AgentGraphNodeSpec, machines []MachineInfo, state *meshPlannerState) *AgentNodePlacement {
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

	type candidatePlacement struct {
		machine MachineInfo
		runner  string
		score   int
		reason  string
	}
	placements := make([]candidatePlacement, 0, len(candidates))
	for _, m := range candidates {
		score, runner, reason := scoreNodePlacement(req, node, m, state)
		placements = append(placements, candidatePlacement{
			machine: m,
			runner:  runner,
			score:   score,
			reason:  reason,
		})
	}
	sort.SliceStable(placements, func(i, j int) bool {
		return placements[i].score > placements[j].score
	})
	best := placements[0]
	return &AgentNodePlacement{
		DeviceID:   best.machine.DeviceID,
		DeviceName: best.machine.Name,
		Runner:     best.runner,
		Model:      choosePlacementModel(node, best.runner),
		Reason:     best.reason,
	}
}

func filterPlacementMachines(req AgentGraphCreateRequest, node AgentGraphNodeSpec, machines []MachineInfo) []MachineInfo {
	targets := map[string]bool{}
	for _, id := range req.AllowedDevices {
		if v := strings.TrimSpace(id); v != "" {
			targets[v] = true
		}
	}
	for _, id := range node.AllowedDevices {
		if v := strings.TrimSpace(id); v != "" {
			targets[v] = true
		}
	}
	if v := strings.TrimSpace(req.PreferredDevice); v != "" {
		targets[v] = true
	}
	if v := strings.TrimSpace(node.PreferredDevice); v != "" {
		targets[v] = true
	}
	if len(targets) == 0 {
		return machines
	}
	out := make([]MachineInfo, 0, len(machines))
	for _, m := range machines {
		if targets[m.DeviceID] || strings.EqualFold(m.Name, req.PreferredDevice) || strings.EqualFold(m.Name, node.PreferredDevice) {
			out = append(out, m)
		}
	}
	return out
}

func scoreNodePlacement(req AgentGraphCreateRequest, node AgentGraphNodeSpec, m MachineInfo, state *meshPlannerState) (int, string, string) {
	runner := chooseCandidateRunner(node, m)
	score := 0
	reasons := []string{}
	if m.IsLocal {
		score += 5
		reasons = append(reasons, "local machine available")
	}
	if strings.TrimSpace(node.PreferredDevice) != "" && (node.PreferredDevice == m.DeviceID || strings.EqualFold(node.PreferredDevice, m.Name)) {
		score += 1000
		reasons = append(reasons, "node pinned to this machine")
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

	intent := nodeIntent(node)
	switch {
	case strings.Contains(intent, "testflight") || strings.Contains(intent, "ios") || strings.Contains(intent, "xcode"):
		if machineSupportsIOS(m.Capabilities) {
			score += 300
			reasons = append(reasons, "iOS/TestFlight workload prefers macOS tooling")
		} else {
			score -= 180
		}
	case strings.Contains(intent, "android") || strings.Contains(intent, "playstore") || strings.Contains(intent, "gradle") || strings.Contains(intent, "adb"):
		if machineSupportsAndroid(m.Capabilities) {
			score += 280
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
		if runner == "claude-code" {
			score += 90
			reasons = append(reasons, "planning/classification favors Claude")
		}
	case AgentNodeAutodev, AgentNodeAutotest:
		if runner == "codex" || runner == "aider-ollama" || runner == "ollama" {
			score += 140
			reasons = append(reasons, "implementation path prefers cheaper/high-throughput runner")
		}
		if m.Capabilities != nil && m.Capabilities.LowPower {
			score -= 70
			reasons = append(reasons, "low-power machine penalized for build/test work")
		}
	}

	if m.Capabilities != nil {
		if m.Capabilities.Hardware.RAM >= 24*1024*1024*1024 {
			score += 35
			reasons = append(reasons, "high-memory machine")
		}
		if m.Capabilities.Hardware.DiskFree >= 120*1024*1024*1024 {
			score += 20
			reasons = append(reasons, "ample free disk for builds/artifacts")
		}
	}
	if m.Capabilities != nil && m.Capabilities.Profile != nil {
		if profileHasAny(m.Capabilities.Profile, "ssd", "nvme") {
			score += 15
			reasons = append(reasons, "machine profile advertises fast disk")
		}
		if profileMatchesIntent(m.Capabilities.Profile, intent) {
			score += 90
			reasons = append(reasons, "machine profile matches requested workload")
		}
	}

	limits := defaultProviderLimits(runner)
	if limits.SharedWithInteractive && m.IsLocal && (node.Kind == AgentNodeAutodev || node.Kind == AgentNodeAutotest) {
		score -= 40
		reasons = append(reasons, "shared interactive budget penalized on primary machine")
	}
	if limits.SessionWindow == "" {
		score += 40
		reasons = append(reasons, "runner has effectively no session-window cap")
	}

	if m.IsShared {
		hostLabel := firstNonEmpty(m.HostName, m.HostEmail, "a shared host")
		if runnerNeedsHostedAPIKey(runner) && !m.UseHostAPIKeys && !m.AllowGuestProvidedAPIKeys {
			score -= 1500
			reasons = append(reasons, "shared machine from "+hostLabel+" blocks runner API keys")
		} else if m.UseHostAPIKeys {
			score += 10
			reasons = append(reasons, "shared machine from "+hostLabel+" permits host API keys")
		} else {
			reasons = append(reasons, "shared from "+hostLabel)
		}
		switch strings.ToLower(strings.TrimSpace(m.PriorityMode)) {
		case "spare-capacity":
			score -= 400
			reasons = append(reasons, "spare-capacity policy keeps shared machine as fallback")
		case "scheduled":
			score -= 60
			reasons = append(reasons, "shared machine is scheduled-only")
		default:
			score -= 80
			reasons = append(reasons, "shared infra courtesy deboost")
		}
	}
	if state != nil {
		score -= state.machineAssignments[m.DeviceID] * 55
		score -= state.runnerAssignments[normalizedPlacementRunner(runner)] * 70
		if cap := machineTaskCapacity(m.Capabilities); cap > 0 && state.machineAssignments[m.DeviceID] >= cap {
			score -= 120
			reasons = append(reasons, "planner balancing away from busy machine")
		}
		if cap := machineRunnerGlobalLimit(runner, m.Capabilities); cap > 0 && state.runnerAssignments[normalizedPlacementRunner(runner)] >= cap {
			score -= 260
			reasons = append(reasons, "runner policy discourages parallel instances")
		}
	}

	if reason := strings.Join(uniqStrings(reasons), "; "); reason != "" {
		return score, runner, reason
	}
	return score, runner, fmt.Sprintf("selected %s for %s", m.Name, node.Kind)
}

func chooseCandidateRunner(node AgentGraphNodeSpec, m MachineInfo) string {
	runner := normalizedPlacementRunner(node.Runner)
	if runner != "" {
		return runner
	}
	candidates := inferPreferredRunnerCandidates(node)
	if len(node.AllowedRunners) > 0 {
		allowed := map[string]bool{}
		for _, r := range node.AllowedRunners {
			allowed[normalizedPlacementRunner(r)] = true
		}
		filtered := candidates[:0]
		for _, candidate := range candidates {
			if allowed[normalizedPlacementRunner(candidate)] {
				filtered = append(filtered, candidate)
			}
		}
		candidates = filtered
	}
	for _, candidate := range candidates {
		if readyRunner(m.Capabilities, candidate) {
			return candidate
		}
	}
	if len(candidates) > 0 {
		return normalizedPlacementRunner(candidates[0])
	}
	return ""
}

func inferPreferredRunnerCandidates(node AgentGraphNodeSpec) []string {
	intent := nodeIntent(node)
	if strings.Contains(intent, "ollama") || strings.Contains(intent, "local llm") {
		return []string{"aider-ollama", "ollama", "codex", "claude-code"}
	}
	if strings.Contains(intent, "testflight") || strings.Contains(intent, "ios") {
		return []string{"claude-code", "codex", "aider-ollama", "ollama"}
	}
	switch node.Kind {
	case AgentNodeChat:
		return []string{"claude-code", "codex", "opencode", "aider"}
	case AgentNodeAutoIdeas:
		return []string{"claude-code", "codex", "opencode", "ollama"}
	case AgentNodeAutodev, AgentNodeAutotest:
		return []string{"codex", "aider-ollama", "claude-code", "ollama", "opencode"}
	default:
		return []string{"claude-code", "codex"}
	}
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
	switch normalizedPlacementRunner(runner) {
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
	runner = normalizedPlacementRunner(runner)
	if runner == "" {
		return true
	}
	for _, r := range caps.Runners {
		if normalizedPlacementRunner(r.ID) == runner && r.Ready {
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

// runnerNeedsHostedAPIKey reports whether the runner calls an external
// paid model API (Anthropic, OpenAI Codex, etc.) and therefore requires
// either the host's API key or a guest-provided key to run on a shared
// machine. Local-only runners (ollama, aider-ollama) return false.
func runnerNeedsHostedAPIKey(runner string) bool {
	switch normalizedPlacementRunner(runner) {
	case "claude-code", "codex", "opencode", "aider":
		return true
	default:
		return false
	}
}

func machineRunnerGlobalLimit(runner string, caps *MachineCapabilities) int {
	switch normalizedPlacementRunner(runner) {
	case "claude-code", "codex", "opencode":
		return 1
	case "aider":
		return 2
	case "aider-ollama", "ollama":
		if caps == nil {
			return 2
		}
		if caps.LowPower {
			return 1
		}
		if caps.Hardware.MaxParallel >= 8 {
			return 3
		}
		return 2
	default:
		return 2
	}
}

func profileMatchesIntent(profile *MachineProfile, intent string) bool {
	if profile == nil {
		return false
	}
	intent = strings.ToLower(intent)
	switch {
	case strings.Contains(intent, "testflight"), strings.Contains(intent, "ios"), strings.Contains(intent, "xcode"):
		return profileHasAny(profile, "testflight", "ios", "xcode")
	case strings.Contains(intent, "android"), strings.Contains(intent, "playstore"), strings.Contains(intent, "gradle"):
		return profileHasAny(profile, "android", "playstore", "gradle")
	case strings.Contains(intent, "ollama"), strings.Contains(intent, "local llm"):
		return profileHasAny(profile, "ollama", "local-llm")
	default:
		return false
	}
}

func nodeIntent(node AgentGraphNodeSpec) string {
	return strings.ToLower(strings.Join([]string{
		node.Title,
		node.Prompt,
		node.Target,
		string(node.Kind),
	}, " "))
}

func (n AgentGraphNodeSpec) KindString() string {
	return string(n.Kind)
}
