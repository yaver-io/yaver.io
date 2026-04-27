package main

import "strings"

// code_orchestrator_policy.go is the policy surface for optional
// multi-runner auto-orchestration inside `yaver code`.
//
// Goal:
// - keep the visible terminal as one coherent Yaver session
// - allow Yaver to choose or fork helper runners behind the scenes
// - stay explicit and opt-in via `set orchestration auto`
//
// This is intentionally separate from the execution layer so the policy can
// evolve without rewriting task/session plumbing.

type CodeBudgetWindow struct {
	Name         string `json:"name"`
	TokenBudget  int64  `json:"tokenBudget,omitempty"`
	MinuteBudget int64  `json:"minuteBudget,omitempty"`
	HourBudget   int64  `json:"hourBudget,omitempty"`
	Notes        string `json:"notes,omitempty"`
}

type CodeRunnerCapability struct {
	RunnerID          string `json:"runnerId"`
	DefaultModel      string `json:"defaultModel,omitempty"`
	Strength          string `json:"strength,omitempty"`
	CostTier          string `json:"costTier,omitempty"`
	Interactive       bool   `json:"interactive"`
	GoodForPlanning   bool   `json:"goodForPlanning"`
	GoodForExecution  bool   `json:"goodForExecution"`
	GoodForRefactors  bool   `json:"goodForRefactors"`
	GoodForCheapWork  bool   `json:"goodForCheapWork"`
	RequiresAPIKey    bool   `json:"requiresApiKey,omitempty"`
	SupportsBrowserUX bool   `json:"supportsBrowserUx,omitempty"`
}

type CodeAutoPolicy struct {
	Mode             string                 `json:"mode"` // manual | auto
	BudgetWindows    []CodeBudgetWindow     `json:"budgetWindows,omitempty"`
	RunnerProfiles   []CodeRunnerCapability `json:"runnerProfiles,omitempty"`
	PreferLocalCheap bool                   `json:"preferLocalCheap,omitempty"`
}

func defaultCodeAutoPolicy() CodeAutoPolicy {
	return CodeAutoPolicy{
		Mode: "manual",
		BudgetWindows: []CodeBudgetWindow{
			{Name: "weekly-frontier", HourBudget: 5, Notes: "Use frontier interactive models carefully when the user has a limited weekly allotment."},
		},
		RunnerProfiles: []CodeRunnerCapability{
			{RunnerID: "claude", DefaultModel: "claude default", Strength: "frontier", CostTier: "high", Interactive: true, GoodForPlanning: true, GoodForExecution: true, GoodForRefactors: true, SupportsBrowserUX: true},
			{RunnerID: "codex", DefaultModel: "gpt-5.4 default", Strength: "frontier", CostTier: "high", Interactive: true, GoodForPlanning: true, GoodForExecution: true, GoodForRefactors: true, SupportsBrowserUX: true},
			{RunnerID: "opencode", DefaultModel: "opencode default", Strength: "adapter", CostTier: "mixed", Interactive: true, GoodForPlanning: true, GoodForExecution: true, GoodForCheapWork: true},
			{RunnerID: "aider-ollama", DefaultModel: "ollama_chat/qwen2.5-coder:14b", Strength: "local", CostTier: "low", Interactive: false, GoodForExecution: true, GoodForCheapWork: true},
		},
		PreferLocalCheap: true,
	}
}

func chooseCodeDelegateRunner(policy CodeAutoPolicy, prompt string) string {
	if strings.TrimSpace(policy.Mode) != "auto" {
		return ""
	}
	p := strings.ToLower(strings.TrimSpace(prompt))
	switch {
	case strings.Contains(p, "small"), strings.Contains(p, "easy"), strings.Contains(p, "mechanical"), strings.Contains(p, "cheap"):
		return "opencode"
	case strings.Contains(p, "refactor"), strings.Contains(p, "plan"), strings.Contains(p, "architecture"):
		return "claude"
	default:
		if policy.PreferLocalCheap {
			return "opencode"
		}
		return ""
	}
}
