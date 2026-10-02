package main

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
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
	machines                 map[string]MachineInfo
	machineAssignments       map[string]int
	runnerAssignments        map[string]int
	machineRunnerAssignments map[string]int
	fleetPreferences         *agentFleetPreferences
}

// agentFleetPreferences is the non-secret account policy already stored in
// Convex userSettings: the primary device is the controller and each device's
// primaryRunnerByDevice row selects its runner/model/provider. Provider keys
// deliberately remain in that machine's local vault/config.
type agentFleetPreferences struct {
	ControllerDeviceID string
	LocalDeviceID      string
	WorkerDeviceIDs    map[string]bool
	Opportunistic      bool
	ByDevice           map[string]primaryRunnerPreference
}

func loadAgentFleetPreferences() *agentFleetPreferences {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	row, err := fetchUserSettings(ctx, nil)
	if err != nil || row == nil {
		return nil
	}
	prefs := &agentFleetPreferences{
		ControllerDeviceID: strings.TrimSpace(row.PrimaryDeviceID),
		WorkerDeviceIDs:    map[string]bool{},
		Opportunistic:      row.OpportunisticFleet == nil || *row.OpportunisticFleet,
		ByDevice:           map[string]primaryRunnerPreference{},
	}
	for _, deviceID := range row.WorkerDeviceIDs {
		if deviceID = strings.TrimSpace(deviceID); deviceID != "" && deviceID != prefs.ControllerDeviceID {
			prefs.WorkerDeviceIDs[deviceID] = true
		}
	}
	if cfg, cfgErr := LoadConfig(); cfgErr == nil && cfg != nil {
		prefs.LocalDeviceID = strings.TrimSpace(cfg.DeviceID)
	}
	for _, entry := range row.PrimaryRunnerByDevice {
		deviceID := strings.TrimSpace(entry.DeviceID)
		if deviceID == "" {
			continue
		}
		prefs.ByDevice[deviceID] = primaryRunnerPreference{
			RunnerID: strings.TrimSpace(entry.RunnerID),
			Model:    strings.TrimSpace(entry.Model),
			Mode:     strings.TrimSpace(entry.Mode),
			Provider: strings.TrimSpace(entry.Provider),
		}
	}
	return prefs
}

func (p *agentFleetPreferences) canonicalDeviceID(m MachineInfo) string {
	if p == nil {
		return strings.TrimSpace(m.DeviceID)
	}
	if m.IsLocal || m.DeviceID == "local" {
		if p.LocalDeviceID != "" {
			return p.LocalDeviceID
		}
	}
	return strings.TrimSpace(m.DeviceID)
}

func (p *agentFleetPreferences) runnerFor(m MachineInfo) primaryRunnerPreference {
	if p == nil {
		return primaryRunnerPreference{}
	}
	return p.ByDevice[p.canonicalDeviceID(m)]
}

func (p *agentFleetPreferences) isController(m MachineInfo) bool {
	return p != nil && p.ControllerDeviceID != "" && p.canonicalDeviceID(m) == p.ControllerDeviceID
}

func (p *agentFleetPreferences) isWorker(m MachineInfo) bool {
	return p != nil && p.WorkerDeviceIDs[p.canonicalDeviceID(m)]
}

type meshPolicyState struct {
	machines         map[string]MachineInfo
	machineUse       map[string]int
	runnerGlobal     map[string]int
	machineRunnerUse map[string]int
}

func machineRunnerKey(deviceID, runner string) string {
	return strings.TrimSpace(deviceID) + "|" + normalizedPlacementRunner(runner)
}

func runnerUsesAccountWideLimit(runner string) bool {
	switch normalizedPlacementRunner(runner) {
	case "claude-code", "codex":
		return true
	default:
		return false
	}
}

func planGraphPlacements(req AgentGraphCreateRequest, nodes []AgentGraphNodeSpec) []*AgentNodePlacement {
	if graphPlacementLocalOnly(req, nodes) {
		out := make([]*AgentNodePlacement, 0, len(nodes))
		for _, node := range nodes {
			out = append(out, &AgentNodePlacement{
				DeviceID: "local",
				Runner:   normalizedPlacementRunner(node.Runner),
				Model:    node.Model,
				Reason:   "local execution requested",
			})
		}
		return out
	}

	machineList := listAllMachines(context.Background())
	prefs := loadAgentFleetPreferences()
	// Once explicit fleet roles exist, automatic placement stays inside that
	// pool. With no master, workers remain fully usable. With opportunistic use
	// disabled, the master alone runs automatic work; explicit device pins are
	// handled before this planner and remain valid.
	if prefs != nil && (prefs.ControllerDeviceID != "" || len(prefs.WorkerDeviceIDs) > 0) {
		filtered := make([]MachineInfo, 0, len(machineList))
		for _, machine := range machineList {
			if fleetMachineEligible(prefs, req, nodes, machine) {
				filtered = append(filtered, machine)
			}
		}
		machineList = filtered
	}
	state := &meshPlannerState{
		machines:                 map[string]MachineInfo{},
		machineAssignments:       map[string]int{},
		runnerAssignments:        map[string]int{},
		machineRunnerAssignments: map[string]int{},
		fleetPreferences:         prefs,
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

func fleetMachineEligible(prefs *agentFleetPreferences, req AgentGraphCreateRequest, nodes []AgentGraphNodeSpec, machine MachineInfo) bool {
	if prefs == nil {
		return true
	}
	// A worker-only fleet stays usable even without an elected master. Once a
	// master exists, Opportunistic controls automatic worker placement.
	automaticWorker := prefs.isWorker(machine) && (prefs.ControllerDeviceID == "" || prefs.Opportunistic)
	return prefs.isController(machine) || automaticWorker || graphExplicitlyAllowsMachine(req, nodes, machine)
}

// graphExplicitlyAllowsMachine preserves a user/task pin even when automatic
// opportunistic placement is disabled. Fleet policy governs automatic choice;
// it must not silently turn an explicit worker request into local execution.
func graphExplicitlyAllowsMachine(req AgentGraphCreateRequest, nodes []AgentGraphNodeSpec, machine MachineInfo) bool {
	if len(req.AllowedDevices) > 0 && placementTargetMatchesMachine(req.AllowedDevices, machine) {
		return true
	}
	for _, node := range nodes {
		if len(node.AllowedDevices) > 0 && placementTargetMatchesMachine(node.AllowedDevices, machine) {
			return true
		}
	}
	return false
}

func graphPlacementLocalOnly(req AgentGraphCreateRequest, nodes []AgentGraphNodeSpec) bool {
	if !placementTargetsLocalOnly(req.AllowedDevices) {
		return false
	}
	if v := strings.TrimSpace(req.PreferredDevice); v != "" && v != "local" {
		return false
	}
	for _, node := range nodes {
		if !placementTargetsLocalOnly(node.AllowedDevices) {
			return false
		}
		if v := strings.TrimSpace(node.PreferredDevice); v != "" && v != "local" {
			return false
		}
	}
	return len(req.AllowedDevices) > 0 || strings.TrimSpace(req.PreferredDevice) == "local"
}

func placementTargetsLocalOnly(targets []string) bool {
	if len(targets) == 0 {
		return true
	}
	for _, target := range targets {
		if strings.TrimSpace(target) != "local" {
			return false
		}
	}
	return true
}

func (ps *meshPlannerState) reserve(placement *AgentNodePlacement) {
	if ps == nil || placement == nil {
		return
	}
	ps.machineAssignments[placement.DeviceID]++
	ps.runnerAssignments[normalizedPlacementRunner(placement.Runner)]++
	if ps.machineRunnerAssignments == nil {
		ps.machineRunnerAssignments = map[string]int{}
	}
	ps.machineRunnerAssignments[machineRunnerKey(placement.DeviceID, placement.Runner)]++
}

func buildMeshPolicyState(run *AgentGraphRun, nodeIndex map[string]*AgentGraphNodeState) *meshPolicyState {
	state := &meshPolicyState{
		machines:         map[string]MachineInfo{},
		machineUse:       map[string]int{},
		runnerGlobal:     map[string]int{},
		machineRunnerUse: map[string]int{},
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
	if ps.machineRunnerUse == nil {
		ps.machineRunnerUse = map[string]int{}
	}
	ps.machineRunnerUse[machineRunnerKey(node.Placement.DeviceID, node.Placement.Runner)]++
}

func (ps *meshPolicyState) CanStart(node *AgentGraphNodeState) bool {
	if ps == nil || node == nil || node.Placement == nil {
		return true
	}
	runner := normalizedPlacementRunner(node.Placement.Runner)
	deviceID := node.Placement.DeviceID
	machine := ps.machines[deviceID]
	if cap := runnerParallelLimit(runner, machine.Capabilities); cap > 0 {
		if runnerUsesAccountWideLimit(runner) {
			if ps.runnerGlobal[runner] >= cap {
				return false
			}
		} else if ps.machineRunnerUse[machineRunnerKey(deviceID, runner)] >= cap {
			return false
		}
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
		Model:      choosePlacementModelForMachine(node, best.runner, best.machine, state),
		Reason:     best.reason,
	}
}

func filterPlacementMachines(req AgentGraphCreateRequest, node AgentGraphNodeSpec, machines []MachineInfo) []MachineInfo {
	var targets []string
	for _, id := range req.AllowedDevices {
		if v := strings.TrimSpace(id); v != "" {
			targets = append(targets, v)
		}
	}
	for _, id := range node.AllowedDevices {
		if v := strings.TrimSpace(id); v != "" {
			targets = append(targets, v)
		}
	}
	if v := strings.TrimSpace(req.PreferredDevice); v != "" {
		targets = append(targets, v)
	}
	if v := strings.TrimSpace(node.PreferredDevice); v != "" {
		targets = append(targets, v)
	}
	if len(targets) == 0 {
		return machines
	}
	out := make([]MachineInfo, 0, len(machines))
	for _, m := range machines {
		if placementTargetMatchesMachine(targets, m) {
			out = append(out, m)
		}
	}
	return out
}

func placementTargetMatchesMachine(targets []string, m MachineInfo) bool {
	for _, target := range targets {
		target = strings.TrimSpace(target)
		if target == "" {
			continue
		}
		if m.DeviceID == target || strings.HasPrefix(m.DeviceID, target) {
			return true
		}
		if strings.EqualFold(m.Name, target) || strings.HasPrefix(strings.ToLower(m.Name), strings.ToLower(target)) {
			return true
		}
	}
	return false
}

func scoreNodePlacement(req AgentGraphCreateRequest, node AgentGraphNodeSpec, m MachineInfo, state *meshPlannerState) (int, string, string) {
	runner := chooseCandidateRunnerWithState(req, node, m, state)
	score := 0
	reasons := []string{}
	if tier := runnerCostTier(runner); runner != "" {
		if tier == "subscription" {
			reasons = append(reasons, "subscription lane ("+runner+", flat plan)")
		} else {
			reasons = append(reasons, "cheap apikey lane ("+runner+", parallel overflow)")
		}
	}
	if m.IsLocal {
		score += 5
		reasons = append(reasons, "local machine available")
	}
	if strings.TrimSpace(node.PreferredDevice) != "" && (node.PreferredDevice == m.DeviceID || strings.EqualFold(node.PreferredDevice, m.Name)) {
		score += 1000
		reasons = append(reasons, "node pinned to this machine")
	}
	if strings.TrimSpace(node.PriorDevice) != "" && (node.PriorDevice == m.DeviceID || strings.EqualFold(node.PriorDevice, m.Name)) {
		bonus := 180
		if node.StickyDevice {
			bonus = 900
		}
		score += bonus
		reasons = append(reasons, "node has prior machine affinity")
	}
	if strings.TrimSpace(req.PreferredDevice) != "" && (req.PreferredDevice == m.DeviceID || strings.EqualFold(req.PreferredDevice, m.Name)) {
		score += 900
		reasons = append(reasons, "graph default machine preference")
	}
	if state != nil && state.fleetPreferences.isController(m) {
		switch {
		case node.OrchestrationRole == "master":
			score += 700
			reasons = append(reasons, "master agent role uses selected controller")
		case node.DesignPoints >= 0.8:
			score += 420
			reasons = append(reasons, "saved fleet controller handles planning/coherence")
		case node.VerifyPoints >= 0.8:
			score += 360
			reasons = append(reasons, "saved fleet controller handles final review")
		default:
			score += 25
			reasons = append(reasons, "saved fleet controller available")
		}
	}
	if state != nil && node.OrchestrationRole == "worker" && state.fleetPreferences.isWorker(m) {
		score += 480
		reasons = append(reasons, "worker agent role prefers enabled worker node")
	}
	if state != nil && node.OrchestrationRole == "worker" && state.fleetPreferences.isController(m) {
		// Deliberately no penalty: a single computer may host both the master
		// runner and an OpenCode worker process. Distinct worker devices win via
		// the bonus above when they are available and ready.
		reasons = append(reasons, "controller may also host a local worker process")
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
	if node.BuildPoints >= 0.8 || graphNodeWantsResource(node, "build") {
		score += 80
		reasons = append(reasons, "node emphasizes build-oriented resources")
	}
	if node.VerifyPoints >= 0.8 || graphNodeWantsAnyResource(node, "video-summary", "proof-video", "test-video") {
		score += 40
		reasons = append(reasons, "node emphasizes verification/proof resources")
	}
	if graphNodeWantsResource(node, "deploy") {
		score += 70
		reasons = append(reasons, "node requests deploy-capable self-hosted resources")
	}
	if graphNodeWantsResource(node, "sim-ios") {
		if machineSupportsIOS(m.Capabilities) {
			score += 240
			reasons = append(reasons, "video/build resource prefers iOS-capable host")
		} else {
			score -= 160
		}
	}
	if graphNodeWantsResource(node, "sim-android") {
		if machineSupportsAndroid(m.Capabilities) {
			score += 220
			reasons = append(reasons, "video/build resource prefers Android-capable host")
		} else {
			score -= 150
		}
	}
	if graphNodeWantsResource(node, "browser") {
		score += 35
		reasons = append(reasons, "browser-capable resource request")
	}
	if graphNodeWantsResource(node, "phone") {
		score += 25
		reasons = append(reasons, "phone-connected proof resource request")
	}
	if strings.Contains(intent, "local llm") || strings.Contains(intent, "byok") {
		if m.Capabilities != nil && m.Capabilities.SupportsLocalLLM {
			score += 260
			reasons = append(reasons, "local-LLM-capable machine for BYOK runner")
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

	if state != nil {
		score -= state.machineAssignments[m.DeviceID] * 55
		score -= state.runnerAssignments[normalizedPlacementRunner(runner)] * 70
		if cap := machineTaskCapacity(m.Capabilities); cap > 0 && state.machineAssignments[m.DeviceID] >= cap {
			score -= 120
			reasons = append(reasons, "planner balancing away from busy machine")
		}
		if cap := runnerParallelLimit(runner, m.Capabilities); cap > 0 {
			used := state.machineRunnerAssignments[machineRunnerKey(m.DeviceID, runner)]
			if runnerUsesAccountWideLimit(runner) {
				used = state.runnerAssignments[normalizedPlacementRunner(runner)]
			}
			if used >= cap {
				score -= 260
				reasons = append(reasons, "runner policy discourages parallel instances")
			}
		}
	}

	if reason := strings.Join(uniqStrings(reasons), "; "); reason != "" {
		return score, runner, reason
	}
	return score, runner, fmt.Sprintf("selected %s for %s", m.Name, node.Kind)
}

func chooseCandidateRunner(req AgentGraphCreateRequest, node AgentGraphNodeSpec, m MachineInfo) string {
	return chooseCandidateRunnerWithState(req, node, m, nil)
}

func chooseCandidateRunnerWithState(req AgentGraphCreateRequest, node AgentGraphNodeSpec, m MachineInfo, state *meshPlannerState) string {
	runner := normalizedPlacementRunner(node.Runner)
	if runner != "" {
		return runner
	}
	if node.StickyRunner {
		if sticky := normalizedPlacementRunner(node.PriorRunner); sticky != "" {
			if len(node.AllowedRunners) == 0 || stringSliceContainsNormalized(node.AllowedRunners, sticky) {
				return sticky
			}
		}
	}
	// A saved per-machine runner is the strongest automatic hint. This is what
	// lets a Mac controller use Codex while worker boxes independently use
	// OpenCode with their own provider/model configuration.
	if state != nil && state.fleetPreferences != nil {
		preferred := normalizedPlacementRunner(state.fleetPreferences.runnerFor(m).RunnerID)
		if preferred != "" &&
			(len(node.AllowedRunners) == 0 || stringSliceContainsNormalized(node.AllowedRunners, preferred)) &&
			(len(req.AllowedRunners) == 0 || stringSliceContainsNormalized(req.AllowedRunners, preferred)) &&
			readyRunner(m.Capabilities, preferred) {
			return preferred
		}
	}
	candidates := inferPreferredRunnerCandidates(node)
	if sticky := normalizedPlacementRunner(node.PriorRunner); sticky != "" && !stringSliceContainsNormalized(candidates, sticky) {
		candidates = append([]string{sticky}, candidates...)
	}
	// Hybrid duo/trio: constrain the rotation to the requested runner lanes
	// (one/both subscription lanes + the cheap apikey lane) so independent
	// slices spread across exactly those backends.
	candidates = filterRunnerLanes(candidates, hybridLaneSet(req.HybridDegree))
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

// inferPreferredRunnerCandidates returns the cost-aware runner preference order
// for a node. The ordering encodes the hybrid duo/trio cost model: spend the
// flat-rate SUBSCRIPTION lanes (claude-code, codex — marginal $≈0 but capped at
// one instance per machine) first, then spill overflow/parallel slices to the
// cheap metered APIKEY lane (glm). The per-run load balancer in
// scoreNodePlacement (runnerAssignments penalty + runnerParallelLimit=1 for
// claude/codex) routes the 2nd+ concurrent slice of a kind onto glm, so N
// independent slices spread across claude-code + codex + glm automatically.
func inferPreferredRunnerCandidates(node AgentGraphNodeSpec) []string {
	intent := nodeIntent(node)
	if node.DesignPoints >= 0.8 {
		// Plan/coherence: strongest subscription model first; glm is the cheap
		// fallback when the subscription lane is already busy.
		return []string{"claude-code", "codex", "glm", "opencode"}
	}
	if node.BuildPoints >= 0.8 {
		// Bulk implement: free subscription lane first, then the cheap apikey
		// overflow lane before paying nothing extra falls back to claude-code.
		return []string{"codex", "glm", "opencode", "claude-code"}
	}
	if node.VerifyPoints >= 0.8 {
		return []string{"codex", "claude-code", "glm", "opencode"}
	}
	if strings.Contains(intent, "local llm") || strings.Contains(intent, "byok") {
		return []string{"opencode", "glm", "codex", "claude-code"}
	}
	if strings.Contains(intent, "testflight") || strings.Contains(intent, "ios") {
		// iOS/TestFlight needs first-party Claude tooling on macOS; keep glm out.
		return []string{"claude-code", "codex", "opencode"}
	}
	switch node.Kind {
	case AgentNodeChat:
		return []string{"claude-code", "codex", "glm", "opencode"}
	case AgentNodeAutoIdeas:
		return []string{"claude-code", "codex", "glm", "opencode"}
	default:
		return []string{"claude-code", "codex", "glm"}
	}
}

// runnerCostTier classifies how a runner is billed for the operator. The hybrid
// planner uses this to keep coherence-critical work on the flat subscription
// plans and push parallel overflow onto the cheap metered apikey lane.
//
//	subscription — claude-code, codex: flat plan, marginal $≈0, rate-limited
//	apikey       — glm, opencode, others: metered per-token, parallelizable
func runnerCostTier(runner string) string {
	switch normalizedPlacementRunner(runner) {
	case "claude-code", "codex":
		return "subscription"
	default:
		return "apikey"
	}
}

// hybridLaneSet maps a duo/trio degree to the runner lanes a hybrid run may
// spread independent slices across, or nil for the default unconstrained
// rotation. duo (2) = one subscription lane + the cheap apikey lane; trio (3) =
// both subscription lanes + apikey. Degrees ≥3 keep the trio lane set.
func hybridLaneSet(degree int) map[string]bool {
	switch {
	case degree <= 0:
		return nil
	case degree == 1:
		return map[string]bool{"claude-code": true}
	case degree == 2:
		return map[string]bool{"claude-code": true, "glm": true}
	default:
		return map[string]bool{"claude-code": true, "codex": true, "glm": true}
	}
}

// filterRunnerLanes keeps only candidates inside the hybrid lane set, preserving
// the cost-aware order. If none of the preferred candidates are in the lane set,
// it falls back to the lane set itself (stable order) so the slice still places.
func filterRunnerLanes(candidates []string, lanes map[string]bool) []string {
	if lanes == nil {
		return candidates
	}
	out := make([]string, 0, len(candidates))
	for _, c := range candidates {
		if lanes[normalizedPlacementRunner(c)] {
			out = append(out, c)
		}
	}
	if len(out) == 0 {
		for _, r := range []string{"claude-code", "codex", "glm"} {
			if lanes[r] {
				out = append(out, r)
			}
		}
	}
	return out
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
	case "glm":
		// z.ai's GLM speaks the Anthropic wire protocol through the claude
		// binary; glm-5.2 is the current coding model (≈94% of Opus on
		// SWE-bench at ~1/3 the cost — the cheap apikey overflow lane).
		return "glm-5.2"
	default:
		return ""
	}
}

func choosePlacementModelForMachine(node AgentGraphNodeSpec, runner string, machine MachineInfo, state *meshPlannerState) string {
	if strings.TrimSpace(node.Model) != "" {
		return node.Model
	}
	if state != nil && state.fleetPreferences != nil {
		pref := state.fleetPreferences.runnerFor(machine)
		if normalizedPlacementRunner(pref.RunnerID) == normalizedPlacementRunner(runner) && strings.TrimSpace(pref.Model) != "" {
			return strings.TrimSpace(pref.Model)
		}
	}
	return choosePlacementModel(node, runner)
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

// runnerParallelLimit returns an account-wide limit for subscription runners
// and a per-machine limit for provider/BYOK runners. The caller selects the
// scope through runnerUsesAccountWideLimit.
func runnerParallelLimit(runner string, caps *MachineCapabilities) int {
	switch normalizedPlacementRunner(runner) {
	case "claude-code", "codex":
		// Anthropic/OpenAI rate-limit aggressively per-account, so
		// keep these single-instance per machine.
		return 1
	case "opencode":
		// opencode's effective limit depends on the BYOK provider
		// the user has wired up. Allow modest parallelism by default;
		// the provider's own rate limiter handles real backpressure.
		// Scale with hardware so bigger boxes do more.
		if caps == nil || caps.LowPower {
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
		strings.Join(node.ResourceModes, " "),
	}, " "))
}

func graphNodeWantsResource(node AgentGraphNodeSpec, want string) bool {
	want = strings.ToLower(strings.TrimSpace(want))
	for _, mode := range node.ResourceModes {
		if strings.EqualFold(strings.TrimSpace(mode), want) {
			return true
		}
	}
	return false
}

func graphNodeWantsAnyResource(node AgentGraphNodeSpec, wants ...string) bool {
	for _, want := range wants {
		if graphNodeWantsResource(node, want) {
			return true
		}
	}
	return false
}

func stringSliceContainsNormalized(values []string, want string) bool {
	want = normalizedPlacementRunner(want)
	for _, value := range values {
		if normalizedPlacementRunner(value) == want {
			return true
		}
	}
	return false
}

func (n AgentGraphNodeSpec) KindString() string {
	return string(n.Kind)
}
