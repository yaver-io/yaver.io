package main

import "testing"

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
		Kind:   AgentNodeAutodev,
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
		Kind:   AgentNodeAutodev,
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
		Kind:   AgentNodeAutodev,
		Prompt: "Use ollama to do a local LLM coding pass",
	}
	machines := []MachineInfo{
		{
			DeviceID: "mac-mini",
			Name:     "mac-mini",
			IsOnline: true,
			Capabilities: &MachineCapabilities{
				Runners:          []MachineRunnerCapability{{ID: "ollama", Ready: true}},
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
	if placement.Runner != "ollama" && placement.Runner != "aider-ollama" {
		t.Fatalf("expected local runner, got %q", placement.Runner)
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
	first := chooseNodePlacement(req, AgentGraphNodeSpec{ID: "n1", Kind: AgentNodeAutodev, Prompt: "Implement settings screen"}, machines, state)
	state.reserve(first)
	second := chooseNodePlacement(req, AgentGraphNodeSpec{ID: "n2", Kind: AgentNodeAutodev, Prompt: "Implement billing flow"}, machines, state)
	if first.DeviceID == second.DeviceID {
		t.Fatalf("expected balanced placement across machines, got both on %q", first.DeviceID)
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
