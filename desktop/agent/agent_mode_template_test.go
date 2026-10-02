package main

import (
	"strings"
	"testing"
)

func TestApplyAgentNodeExecutionPolicyRespectsAllowedRunners(t *testing.T) {
	node := AgentGraphNodeSpec{
		ID:             "chat",
		Kind:           AgentNodeChat,
		WorkDir:        t.TempDir(),
		AllowedRunners: []string{"opencode", "codex"},
	}
	got := applyAgentNodeExecutionPolicy(node)
	if got.Runner == "" {
		t.Fatalf("expected a runner, got empty")
	}
	normalized := strings.ToLower(got.Runner)
	if normalized != "opencode" && normalized != "codex" {
		t.Fatalf("runner %q escaped allowlist {opencode,codex}", got.Runner)
	}
	if got.Runner == "claude" || got.Runner == "claude-code" {
		t.Fatalf("claude must not be picked when allowlist forbids it, got %q", got.Runner)
	}
}

func TestApplyAgentNodeExecutionPolicyRespectsExplicitRunner(t *testing.T) {
	node := AgentGraphNodeSpec{
		ID:             "chat",
		Kind:           AgentNodeChat,
		Runner:         "codex",
		WorkDir:        t.TempDir(),
		AllowedRunners: []string{"opencode"},
	}
	got := applyAgentNodeExecutionPolicy(node)
	if got.Runner != "codex" {
		t.Fatalf("explicit runner should win, got %q", got.Runner)
	}
}

func TestBuildAgentGraphTemplateFullUsesChatNodes(t *testing.T) {
	req := AgentGraphCreateRequest{
		WorkDir:  "/tmp/example",
		Prompt:   "Build a survey app",
		Template: "full",
	}

	nodes := buildAgentGraphTemplate(req)
	if len(nodes) != 3 {
		t.Fatalf("expected 3 nodes, got %d", len(nodes))
	}
	if nodes[0].ID != "plan" || nodes[0].Kind != AgentNodeChat {
		t.Fatalf("expected first node to be chat plan, got id=%q kind=%q", nodes[0].ID, nodes[0].Kind)
	}
	if nodes[1].ID != "implement" || nodes[1].Kind != AgentNodeChat {
		t.Fatalf("expected second node to be chat implement, got id=%q kind=%q", nodes[1].ID, nodes[1].Kind)
	}
	if len(nodes[1].DependsOn) != 1 || nodes[1].DependsOn[0] != "plan" {
		t.Fatalf("expected implement to depend on plan, got %#v", nodes[1].DependsOn)
	}
	if nodes[2].ID != "verify" || nodes[2].Kind != AgentNodeChat {
		t.Fatalf("expected third node to be chat verify, got id=%q kind=%q", nodes[2].ID, nodes[2].Kind)
	}
	if len(nodes[2].DependsOn) != 1 || nodes[2].DependsOn[0] != "implement" {
		t.Fatalf("expected verify to depend on implement, got %#v", nodes[2].DependsOn)
	}
}

func TestBuildAgentGraphTemplateFleetUsesMasterOpenCodeMasterContract(t *testing.T) {
	nodes := buildAgentGraphTemplate(AgentGraphCreateRequest{
		WorkDir: "/tmp/example", Prompt: "Build the feature", Template: "fleet",
		MasterRunner: "codex", MasterModel: "master-model", WorkerModel: "deepseek/deepseek-flash",
	})
	if len(nodes) != 3 {
		t.Fatalf("fleet nodes = %d, want 3", len(nodes))
	}
	if nodes[0].OrchestrationRole != "master" || nodes[0].Runner != "codex" || nodes[0].DesignPoints != 1 {
		t.Fatalf("master plan node = %#v", nodes[0])
	}
	if nodes[1].OrchestrationRole != "worker" || nodes[1].Runner != "opencode" || nodes[1].Model != "deepseek/deepseek-flash" {
		t.Fatalf("worker node = %#v", nodes[1])
	}
	if !strings.Contains(nodes[1].Prompt, "WORKER_REPORT") || !strings.Contains(nodes[1].Prompt, "iterating through the relevant tests") {
		t.Fatalf("worker prompt lacks iteration/report contract: %s", nodes[1].Prompt)
	}
	if nodes[2].OrchestrationRole != "master" || len(nodes[2].DependsOn) != 2 || !strings.Contains(nodes[2].Prompt, "VALIDATION_REPORT") {
		t.Fatalf("master validation node = %#v", nodes[2])
	}
}

func TestNormalizeAgentNodesRejectsUnknownOrchestrationRole(t *testing.T) {
	_, err := normalizeAgentNodes(t.TempDir(), "", "", nil, []AgentGraphNodeSpec{{ID: "bad", Kind: AgentNodeChat, OrchestrationRole: "boss"}})
	if err == nil || !strings.Contains(err.Error(), "invalid orchestration role") {
		t.Fatalf("error = %v, want invalid orchestration role", err)
	}
}
