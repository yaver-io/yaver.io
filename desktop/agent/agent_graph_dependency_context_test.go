package main

import (
	"strings"
	"testing"
)

func TestGraphNodeReceivesCompletedDependencySummary(t *testing.T) {
	parent := &AgentGraphNodeState{
		Spec:    AgentGraphNodeSpec{ID: "plan", Title: "Plan Slice"},
		Status:  AgentNodeCompleted,
		Summary: "Use isolated branches and merge only after tests pass.",
	}
	child := &AgentGraphNodeState{
		Spec: AgentGraphNodeSpec{
			ID:        "implement",
			Prompt:    "Implement the feature.",
			DependsOn: []string{"plan"},
		},
	}
	got := graphNodeWithDependencyContext(&AgentGraphRun{Nodes: []*AgentGraphNodeState{parent, child}}, child)
	if got == child {
		t.Fatal("dependency handoff should return an isolated node copy")
	}
	if !strings.Contains(got.Spec.Prompt, "Plan Slice") || !strings.Contains(got.Spec.Prompt, parent.Summary) {
		t.Fatalf("child prompt missing dependency result: %q", got.Spec.Prompt)
	}
	if child.Spec.Prompt != "Implement the feature." {
		t.Fatalf("source graph node was mutated: %q", child.Spec.Prompt)
	}
}

func TestGraphDependencyContextIsBounded(t *testing.T) {
	parent := &AgentGraphNodeState{
		Spec:    AgentGraphNodeSpec{ID: "plan", Title: "Plan"},
		Status:  AgentNodeCompleted,
		Summary: strings.Repeat("x", graphDependencyContextLimit*2),
	}
	child := &AgentGraphNodeState{Spec: AgentGraphNodeSpec{ID: "build", DependsOn: []string{"plan"}}}
	got := graphNodeWithDependencyContext(&AgentGraphRun{Nodes: []*AgentGraphNodeState{parent, child}}, child)
	if len(got.Spec.Prompt) > graphDependencyContextLimit+128 {
		t.Fatalf("dependency context grew past bound: %d", len(got.Spec.Prompt))
	}
	if !strings.Contains(got.Spec.Prompt, "truncated") {
		t.Fatal("bounded handoff should name truncation")
	}
}

func TestGraphDependencyContextReservesSpaceForEveryParentReport(t *testing.T) {
	plan := &AgentGraphNodeState{Spec: AgentGraphNodeSpec{ID: "plan", Title: "MASTER_PLAN"}, Status: AgentNodeCompleted, Summary: "PLAN_MARKER\n" + strings.Repeat("p", graphDependencyContextLimit)}
	worker := &AgentGraphNodeState{Spec: AgentGraphNodeSpec{ID: "worker", Title: "WORKER_REPORT"}, Status: AgentNodeCompleted, Summary: "WORKER_MARKER\n" + strings.Repeat("w", graphDependencyContextLimit)}
	review := &AgentGraphNodeState{Spec: AgentGraphNodeSpec{ID: "review", DependsOn: []string{"worker", "plan"}}}
	got := graphNodeWithDependencyContext(&AgentGraphRun{Nodes: []*AgentGraphNodeState{plan, worker, review}}, review)
	for _, marker := range []string{"WORKER_MARKER", "PLAN_MARKER"} {
		if !strings.Contains(got.Spec.Prompt, marker) {
			t.Fatalf("final master context lost %s", marker)
		}
	}
}
