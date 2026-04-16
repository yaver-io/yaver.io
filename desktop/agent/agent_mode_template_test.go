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
		AllowedRunners: []string{"ollama", "codex"},
	}
	got := applyAgentNodeExecutionPolicy(node)
	if got.Runner == "" {
		t.Fatalf("expected a runner, got empty")
	}
	normalized := strings.ToLower(got.Runner)
	if normalized != "ollama" && normalized != "codex" && normalized != "aider-ollama" {
		t.Fatalf("runner %q escaped allowlist {ollama,codex}", got.Runner)
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
		AllowedRunners: []string{"ollama"},
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
