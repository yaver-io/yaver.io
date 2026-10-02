package main

import (
	"testing"
)

func TestChooseNodePlacementPrefersPinnedMachine(t *testing.T) {
	req := AgentGraphCreateRequest{PreferredDevice: "mac-mini"}
	node := AgentGraphNodeSpec{ID: "chat", Kind: AgentNodeChat, Prompt: "Plan the release"}
	machines := []MachineInfo{
		{
			DeviceID: "linux-box",
			Name:     "linux-box",
			IsOnline: true,
			Capabilities: &MachineCapabilities{
				Runners: []MachineRunnerCapability{{ID: "codex", Ready: true}},
			},
		},
		{
			DeviceID: "mac-mini",
			Name:     "mac-mini",
			IsOnline: true,
			Capabilities: &MachineCapabilities{
				Runners: []MachineRunnerCapability{{ID: "claude", Ready: true}},
			},
		},
	}

	placement := chooseNodePlacement(req, node, machines, &meshPlannerState{})
	if placement.DeviceID != "mac-mini" {
		t.Fatalf("expected pinned machine, got %q", placement.DeviceID)
	}
}

func TestChooseNodePlacementPrefersIOSMachineForTestFlight(t *testing.T) {
	node := AgentGraphNodeSpec{
		ID:     "ship-ios",
		Kind:   AgentNodeAutoIdeas,
		Prompt: "Build and deploy the app to TestFlight",
	}
	machines := []MachineInfo{
		{
			DeviceID: "linux-box",
			Name:     "linux-box",
			IsOnline: true,
			Capabilities: &MachineCapabilities{
				Runners:         []MachineRunnerCapability{{ID: "codex", Ready: true}},
				SupportsAndroid: true,
			},
		},
		{
			DeviceID: "mac-mini",
			Name:     "mac-mini",
			IsOnline: true,
			Capabilities: &MachineCapabilities{
				Runners:            []MachineRunnerCapability{{ID: "claude", Ready: true}, {ID: "codex", Ready: true}},
				SupportsIOS:        true,
				SupportsTestFlight: true,
			},
		},
	}

	placement := chooseNodePlacement(AgentGraphCreateRequest{}, node, machines, &meshPlannerState{})
	if placement.DeviceID != "mac-mini" {
		t.Fatalf("expected mac-mini for TestFlight, got %q", placement.DeviceID)
	}
}

func TestChooseNodePlacementPrefersAndroidMachine(t *testing.T) {
	node := AgentGraphNodeSpec{
		ID:     "ship-android",
		Kind:   AgentNodeAutoIdeas,
		Prompt: "Prepare the Android release and Play Store rollout",
	}
	machines := []MachineInfo{
		{
			DeviceID: "mac-mini",
			Name:     "mac-mini",
			IsOnline: true,
			Capabilities: &MachineCapabilities{
				Runners:     []MachineRunnerCapability{{ID: "claude", Ready: true}},
				SupportsIOS: true,
			},
		},
		{
			DeviceID: "linux-box",
			Name:     "linux-box",
			IsOnline: true,
			Capabilities: &MachineCapabilities{
				Runners:           []MachineRunnerCapability{{ID: "codex", Ready: true}},
				SupportsAndroid:   true,
				SupportsPlayStore: true,
			},
		},
	}

	placement := chooseNodePlacement(AgentGraphCreateRequest{}, node, machines, &meshPlannerState{})
	if placement.DeviceID != "linux-box" {
		t.Fatalf("expected linux-box for Android flow, got %q", placement.DeviceID)
	}
}

func TestChooseNodePlacementPrefersLocalLLMWhenRequested(t *testing.T) {
	node := AgentGraphNodeSpec{
		ID:     "local-dev",
		Kind:   AgentNodeAutoIdeas,
		Prompt: "Use opencode with local LLM (BYOK) for the coding pass",
	}
	machines := []MachineInfo{
		{
			DeviceID: "mac-mini",
			Name:     "mac-mini",
			IsOnline: true,
			Capabilities: &MachineCapabilities{
				Runners:          []MachineRunnerCapability{{ID: "opencode", Ready: true}},
				SupportsLocalLLM: true,
			},
		},
		{
			DeviceID: "cloud-box",
			Name:     "cloud-box",
			IsOnline: true,
			Capabilities: &MachineCapabilities{
				Runners: []MachineRunnerCapability{{ID: "codex", Ready: true}},
			},
		},
	}

	placement := chooseNodePlacement(AgentGraphCreateRequest{}, node, machines, &meshPlannerState{})
	if placement.DeviceID != "mac-mini" {
		t.Fatalf("expected local-llm machine, got %q", placement.DeviceID)
	}
	if placement.Runner != "opencode" {
		t.Fatalf("expected opencode runner for BYOK local-LLM path, got %q", placement.Runner)
	}
}

func TestPlanGraphPlacementsBalancesAcrossAllowedMachines(t *testing.T) {
	req := AgentGraphCreateRequest{AllowedDevices: []string{"mac", "linux"}}
	machines := []MachineInfo{
		{
			DeviceID: "mac",
			Name:     "mac",
			IsOnline: true,
			Capabilities: &MachineCapabilities{
				Hardware:     HardwareProfile{MaxParallel: 4},
				MaxTaskSlots: 2,
				Runners: []MachineRunnerCapability{
					{ID: "claude", Ready: true},
					{ID: "codex", Ready: true},
				},
			},
		},
		{
			DeviceID: "linux",
			Name:     "linux",
			IsOnline: true,
			Capabilities: &MachineCapabilities{
				Hardware:     HardwareProfile{MaxParallel: 4},
				MaxTaskSlots: 2,
				Runners: []MachineRunnerCapability{
					{ID: "codex", Ready: true},
				},
			},
		},
	}
	state := &meshPlannerState{
		machines:           map[string]MachineInfo{"mac": machines[0], "linux": machines[1]},
		machineAssignments: map[string]int{},
		runnerAssignments:  map[string]int{},
	}
	first := chooseNodePlacement(req, AgentGraphNodeSpec{ID: "n1", Kind: AgentNodeAutoIdeas, Prompt: "Implement settings screen"}, machines, state)
	state.reserve(first)
	second := chooseNodePlacement(req, AgentGraphNodeSpec{ID: "n2", Kind: AgentNodeAutoIdeas, Prompt: "Implement billing flow"}, machines, state)
	if first.DeviceID == second.DeviceID {
		t.Fatalf("expected balanced placement across machines, got both on %q", first.DeviceID)
	}
}

func TestAllowedDevicesMatchesMachineNameAndPrefix(t *testing.T) {
	req := AgentGraphCreateRequest{AllowedDevices: []string{"ubuntu-4gb", "mac"}}
	machines := []MachineInfo{
		{DeviceID: "local", Name: "Kvancs-MacBook-Air.local", IsLocal: true, IsOnline: true},
		{DeviceID: "6d5c0624-128d-419e-9da9-47362d5de434", Name: "ubuntu-4gb-hel1-1", IsOnline: true},
	}

	filtered := filterPlacementMachines(req, AgentGraphNodeSpec{}, machines)
	if len(filtered) != 1 {
		t.Fatalf("expected one allowed machine, got %d", len(filtered))
	}
	if filtered[0].Name != "ubuntu-4gb-hel1-1" {
		t.Fatalf("expected Hetzner machine by name match, got %q", filtered[0].Name)
	}
}

func TestExplicitWorkerPinOverridesAutomaticFleetDisclosure(t *testing.T) {
	worker := MachineInfo{DeviceID: "pi-worker", Name: "worker-pi", IsOnline: true}
	req := AgentGraphCreateRequest{AllowedDevices: []string{"worker-pi"}}
	prefs := &agentFleetPreferences{
		ControllerDeviceID: "mac-master",
		WorkerDeviceIDs:    map[string]bool{"pi-worker": true},
		Opportunistic:      false,
	}
	if !fleetMachineEligible(prefs, req, nil, worker) {
		t.Fatal("explicit graph worker must remain eligible when opportunistic placement is disabled")
	}
	if fleetMachineEligible(prefs, AgentGraphCreateRequest{}, nil, worker) {
		t.Fatal("an unpinned worker must not bypass automatic fleet policy")
	}
}

func TestWorkerOnlyFleetRemainsUsableWithoutMaster(t *testing.T) {
	worker := MachineInfo{DeviceID: "pi-worker", Name: "worker-pi", IsOnline: true}
	prefs := &agentFleetPreferences{
		WorkerDeviceIDs: map[string]bool{"pi-worker": true},
		Opportunistic:   false,
	}
	if !fleetMachineEligible(prefs, AgentGraphCreateRequest{}, nil, worker) {
		t.Fatal("worker-only fleet must remain usable without a master")
	}
}

func TestMeshPolicySerializesClaude(t *testing.T) {
	state := &meshPolicyState{
		machines: map[string]MachineInfo{
			"mac": {
				DeviceID: "mac",
				Capabilities: &MachineCapabilities{
					Hardware:     HardwareProfile{MaxParallel: 4},
					MaxTaskSlots: 2,
				},
			},
			"linux": {
				DeviceID: "linux",
				Capabilities: &MachineCapabilities{
					Hardware:     HardwareProfile{MaxParallel: 4},
					MaxTaskSlots: 2,
				},
			},
		},
		machineUse:   map[string]int{},
		runnerGlobal: map[string]int{},
	}
	first := &AgentGraphNodeState{Placement: &AgentNodePlacement{DeviceID: "mac", Runner: "claude-code"}}
	second := &AgentGraphNodeState{Placement: &AgentNodePlacement{DeviceID: "linux", Runner: "claude-code"}}
	if !state.CanStart(first) {
		t.Fatalf("expected first claude node to start")
	}
	state.Reserve(first)
	if state.CanStart(second) {
		t.Fatalf("expected second claude node to be blocked by policy")
	}
}

func TestMeshPolicyAllowsOpenCodeAcrossIndependentWorkers(t *testing.T) {
	state := &meshPolicyState{
		machines: map[string]MachineInfo{
			"pi-1": {DeviceID: "pi-1", Capabilities: &MachineCapabilities{LowPower: true, MaxTaskSlots: 1}},
			"pi-2": {DeviceID: "pi-2", Capabilities: &MachineCapabilities{LowPower: true, MaxTaskSlots: 1}},
		},
		machineUse:       map[string]int{},
		runnerGlobal:     map[string]int{},
		machineRunnerUse: map[string]int{},
	}
	first := &AgentGraphNodeState{Placement: &AgentNodePlacement{DeviceID: "pi-1", Runner: "opencode"}}
	second := &AgentGraphNodeState{Placement: &AgentNodePlacement{DeviceID: "pi-2", Runner: "opencode"}}
	if !state.CanStart(first) {
		t.Fatal("first OpenCode worker should start")
	}
	state.Reserve(first)
	if !state.CanStart(second) {
		t.Fatal("a low-power worker's per-machine OpenCode cap must not serialize the whole fleet")
	}
}

func TestFleetPreferencesChoosePerMachineRunnerAndModel(t *testing.T) {
	machine := MachineInfo{
		DeviceID: "pi-worker",
		Name:     "pi-worker",
		IsOnline: true,
		Capabilities: &MachineCapabilities{Runners: []MachineRunnerCapability{
			{ID: "opencode", Ready: true},
			{ID: "codex", Ready: true},
		}},
	}
	state := &meshPlannerState{
		machines:           map[string]MachineInfo{machine.DeviceID: machine},
		machineAssignments: map[string]int{},
		runnerAssignments:  map[string]int{},
		fleetPreferences: &agentFleetPreferences{ByDevice: map[string]primaryRunnerPreference{
			"pi-worker": {RunnerID: "opencode", Model: "deepseek-v4.1-flash", Provider: "deepseek"},
		}},
	}
	node := AgentGraphNodeSpec{ID: "worker", Kind: AgentNodeChat, BuildPoints: 1}
	placement := chooseNodePlacement(AgentGraphCreateRequest{}, node, []MachineInfo{machine}, state)
	if placement.Runner != "opencode" {
		t.Fatalf("saved worker runner = %q, want opencode", placement.Runner)
	}
	if placement.Model != "deepseek-v4.1-flash" {
		t.Fatalf("saved worker model = %q, want deepseek-v4.1-flash", placement.Model)
	}
}

func TestFleetControllerPreferredForPlanAndReview(t *testing.T) {
	machines := []MachineInfo{
		{DeviceID: "mac-controller", Name: "mac-controller", IsOnline: true, Capabilities: &MachineCapabilities{Runners: []MachineRunnerCapability{{ID: "codex", Ready: true}}}},
		{DeviceID: "pi-worker", Name: "pi-worker", IsOnline: true, Capabilities: &MachineCapabilities{Runners: []MachineRunnerCapability{{ID: "opencode", Ready: true}}}},
	}
	state := &meshPlannerState{
		machines:           map[string]MachineInfo{"mac-controller": machines[0], "pi-worker": machines[1]},
		machineAssignments: map[string]int{},
		runnerAssignments:  map[string]int{},
		fleetPreferences: &agentFleetPreferences{
			ControllerDeviceID: "mac-controller",
			ByDevice: map[string]primaryRunnerPreference{
				"mac-controller": {RunnerID: "codex", Model: "gpt-controller"},
				"pi-worker":      {RunnerID: "opencode", Model: "cheap-worker"},
			},
		},
	}
	for _, node := range []AgentGraphNodeSpec{
		{ID: "plan", Kind: AgentNodeChat, DesignPoints: 1},
		{ID: "review", Kind: AgentNodeChat, VerifyPoints: 1},
	} {
		placement := chooseNodePlacement(AgentGraphCreateRequest{}, node, machines, state)
		if placement.DeviceID != "mac-controller" {
			t.Fatalf("%s placed on %q, want saved controller", node.ID, placement.DeviceID)
		}
	}
}

func TestMasterAndOpenCodeWorkerCanShareOneDevice(t *testing.T) {
	machine := MachineInfo{
		DeviceID: "one-box", Name: "one-box", IsOnline: true,
		Capabilities: &MachineCapabilities{Runners: []MachineRunnerCapability{
			{ID: "codex", Ready: true}, {ID: "opencode", Ready: true},
		}},
	}
	state := &meshPlannerState{
		machines: map[string]MachineInfo{"one-box": machine}, machineAssignments: map[string]int{},
		runnerAssignments: map[string]int{}, machineRunnerAssignments: map[string]int{},
		fleetPreferences: &agentFleetPreferences{ControllerDeviceID: "one-box", WorkerDeviceIDs: map[string]bool{}, ByDevice: map[string]primaryRunnerPreference{}},
	}
	master := chooseNodePlacement(AgentGraphCreateRequest{}, AgentGraphNodeSpec{ID: "plan", Kind: AgentNodeChat, Runner: "codex", OrchestrationRole: "master"}, []MachineInfo{machine}, state)
	worker := chooseNodePlacement(AgentGraphCreateRequest{}, AgentGraphNodeSpec{ID: "build", Kind: AgentNodeChat, Runner: "opencode", OrchestrationRole: "worker"}, []MachineInfo{machine}, state)
	if master.DeviceID != "one-box" || worker.DeviceID != "one-box" {
		t.Fatalf("placements master=%q worker=%q, want same device", master.DeviceID, worker.DeviceID)
	}
	if master.Runner != "codex" || worker.Runner != "opencode" {
		t.Fatalf("runners master=%q worker=%q", master.Runner, worker.Runner)
	}
}

func TestWorkerRolePrefersEnabledWorkerOverController(t *testing.T) {
	machines := []MachineInfo{
		{DeviceID: "master-box", Name: "master-box", IsOnline: true, Capabilities: &MachineCapabilities{Runners: []MachineRunnerCapability{{ID: "opencode", Ready: true}}}},
		{DeviceID: "worker-box", Name: "worker-box", IsOnline: true, Capabilities: &MachineCapabilities{Runners: []MachineRunnerCapability{{ID: "opencode", Ready: true}}}},
	}
	state := &meshPlannerState{
		machines:           map[string]MachineInfo{"master-box": machines[0], "worker-box": machines[1]},
		machineAssignments: map[string]int{}, runnerAssignments: map[string]int{}, machineRunnerAssignments: map[string]int{},
		fleetPreferences: &agentFleetPreferences{ControllerDeviceID: "master-box", WorkerDeviceIDs: map[string]bool{"worker-box": true}, ByDevice: map[string]primaryRunnerPreference{}},
	}
	placement := chooseNodePlacement(AgentGraphCreateRequest{}, AgentGraphNodeSpec{ID: "build", Kind: AgentNodeChat, Runner: "opencode", OrchestrationRole: "worker"}, machines, state)
	if placement.DeviceID != "worker-box" {
		t.Fatalf("worker placed on %q, want enabled worker", placement.DeviceID)
	}
}
