package main

import "testing"

// Hybrid duo/trio cost-aware routing tests. These exercise the pure placement
// helpers — no vault / keychain / network — so they run in isolation.

func TestInferPreferredRunnerCandidatesIncludesGLM(t *testing.T) {
	build := AgentGraphNodeSpec{Kind: AgentNodeChat, BuildPoints: 1.0}
	if !stringSliceContainsNormalized(inferPreferredRunnerCandidates(build), "glm") {
		t.Fatalf("bulk/build node should offer the cheap glm lane: %v", inferPreferredRunnerCandidates(build))
	}
	// Plan/coherence keeps the strongest subscription model first.
	plan := AgentGraphNodeSpec{Kind: AgentNodeChat, DesignPoints: 1.0}
	if got := inferPreferredRunnerCandidates(plan)[0]; got != "codex" {
		t.Fatalf("plan node should prefer codex first, got %s", got)
	}
}

func TestHybridLaneSet(t *testing.T) {
	if hybridLaneSet(0) != nil {
		t.Fatal("degree 0 must be nil (default unconstrained / single-model capable)")
	}
	if solo := hybridLaneSet(1); !solo["opencode"] || solo["glm"] || solo["codex"] {
		t.Fatalf("degree 1 = single runner lane, got %v", solo)
	}
	if duo := hybridLaneSet(2); !duo["opencode"] || !duo["glm"] || duo["codex"] {
		t.Fatalf("duo = opencode + glm, got %v", duo)
	}
	if trio := hybridLaneSet(3); !trio["opencode"] || !trio["codex"] || !trio["glm"] {
		t.Fatalf("trio = opencode + codex + glm, got %v", trio)
	}
	if hi := hybridLaneSet(9); !hi["opencode"] || !hi["codex"] || !hi["glm"] {
		t.Fatalf("degree >3 keeps trio lanes, got %v", hi)
	}
}

func TestFilterRunnerLanesOrderAndFallback(t *testing.T) {
	cands := []string{"codex", "glm", "opencode"}
	got := filterRunnerLanes(cands, hybridLaneSet(2)) // duo: opencode + glm, order preserved
	want := []string{"glm", "opencode"}
	if len(got) != len(want) {
		t.Fatalf("got %v want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("lane order: got %v want %v", got, want)
		}
	}
	// nil lane set = passthrough (single-model / default).
	if out := filterRunnerLanes(cands, nil); len(out) != len(cands) {
		t.Fatalf("nil lanes should passthrough, got %v", out)
	}
	// none of the preferred candidates in-lane → fall back to the lane set.
	if fb := filterRunnerLanes([]string{"codex"}, hybridLaneSet(2)); len(fb) == 0 || (fb[0] != "opencode" && fb[0] != "glm") {
		t.Fatalf("fallback to lane set failed: %v", fb)
	}
}

func TestChoosePlacementModelGLM(t *testing.T) {
	if m := choosePlacementModel(AgentGraphNodeSpec{}, "glm"); m != "glm-5.2" {
		t.Fatalf("glm default model = %q, want glm-5.2", m)
	}
	if m := choosePlacementModel(AgentGraphNodeSpec{Model: "glm-4.7"}, "glm"); m != "glm-4.7" {
		t.Fatalf("explicit node model pin must win, got %q", m)
	}
}

func TestRunnerCostTier(t *testing.T) {
	for _, r := range []string{"claude-code", "claude", "codex"} {
		if runnerCostTier(r) != "subscription" {
			t.Fatalf("%s should be subscription tier", r)
		}
	}
	for _, r := range []string{"glm", "opencode", "ollama"} {
		if runnerCostTier(r) != "apikey" {
			t.Fatalf("%s should be apikey tier", r)
		}
	}
}

func TestChooseCandidateRunnerHybridSpreadsLanes(t *testing.T) {
	caps := &MachineCapabilities{Runners: []MachineRunnerCapability{
		{ID: "opencode", Ready: true},
		{ID: "codex", Ready: true},
		{ID: "glm", Ready: true},
	}}
	m := MachineInfo{DeviceID: "d1", Capabilities: caps}
	build := AgentGraphNodeSpec{Kind: AgentNodeChat, BuildPoints: 1.0}

	// duo: only opencode + glm; a bulk slice routes to the cheap glm lane.
	if r := chooseCandidateRunner(AgentGraphCreateRequest{HybridDegree: 2}, build, m); r != "glm" {
		t.Fatalf("duo bulk slice should pick glm, got %s", r)
	}
	// trio: codex is available and preferred first for bulk (free subscription).
	if r := chooseCandidateRunner(AgentGraphCreateRequest{HybridDegree: 3}, build, m); r != "codex" {
		t.Fatalf("trio bulk slice should pick codex first, got %s", r)
	}
	// single-model (degree 1): forced onto the default runner lane.
	if r := chooseCandidateRunner(AgentGraphCreateRequest{HybridDegree: 1}, build, m); r != "opencode" {
		t.Fatalf("degree 1 should force opencode, got %s", r)
	}
	// default (degree 0): unconstrained — bulk prefers codex.
	if r := chooseCandidateRunner(AgentGraphCreateRequest{HybridDegree: 0}, build, m); r != "codex" {
		t.Fatalf("default bulk slice should pick codex, got %s", r)
	}
}
