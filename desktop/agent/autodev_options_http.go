package main

// autodev_options_http.go — capabilities endpoint that mobile, web,
// and MCP clients call to discover what autodev settings the *remote
// dev machine* actually supports. The mobile Auto Dev settings panel
// only shows engines whose CLIs are installed; web does the same.
// Defaults are returned alongside, so the UI can pre-fill the form
// the way the CLI would have if invoked with no flags.

import (
	"net/http"
	"os/exec"
)

type autodevEngineOption struct {
	Value       string `json:"value"`        // "claude" | "hybrid"
	Label       string `json:"label"`        // human-readable
	Description string `json:"description"`  // tooltip in UI
	Available   bool   `json:"available"`    // true if all required CLIs are installed
	Missing     []string `json:"missing,omitempty"` // which CLIs are missing if not available
}

type autodevRunnerOption struct {
	Value     string `json:"value"`     // runner ID, e.g. "claude-code"
	Label     string `json:"label"`     // human-readable
	Available bool   `json:"available"` // true if the underlying CLI is installed
	Missing   string `json:"missing,omitempty"`
}

type autodevOptions struct {
	OK      bool                  `json:"ok"`
	Engines []autodevEngineOption `json:"engines"`
	Runners []autodevRunnerOption `json:"runners"`
	Hardens []autodevHardenOption `json:"hardens"`
	// Layerings is an opinionated preset list mobile / web render
	// in the start form so a user can pick "Opus plans, Sonnet
	// implements" without learning the planner/implementer flag
	// shape. Empty preset = the default, no-slicing path.
	Layerings []autodevLayeringPreset `json:"layerings"`
	// DeployTargets the project actually supports, computed against
	// the daemon's cwd by resolveAutodevDeployTargets("auto"). The
	// mobile / web start form pre-checks these boxes so the user
	// doesn't have to figure out which surfaces exist.
	DeployTargets []string `json:"deploy_targets"`
	// Defaults the UI should pre-select. Match the CLI defaults so
	// "click Start with no changes" behaves like `yaver autodev`.
	Defaults autodevOptionDefaults `json:"defaults"`
}

type autodevHardenOption struct {
	Value       string `json:"value"`       // "" | "security" | ...
	Label       string `json:"label"`       // human-readable
	Description string `json:"description"` // tooltip
}

// autodevLayeringPreset is a one-click "Opus plans / Sonnet
// implements" style configuration the UI can offer alongside the
// raw planner / implementer fields. Available reflects whether
// the underlying CLIs are installed on this machine.
type autodevLayeringPreset struct {
	Value       string `json:"value"`        // stable id
	Label       string `json:"label"`        // human-readable
	Description string `json:"description"`  // tooltip / explainer
	Planner     string `json:"planner"`      // agent[:model]
	Implementer string `json:"implementer"`  // agent[:model]
	Available   bool   `json:"available"`    // all required CLIs present
}

type autodevOptionDefaults struct {
	Engine     string `json:"engine"`      // "claude"
	Runner     string `json:"runner"`      // "claude-code" (only when engine=claude)
	Hours      string `json:"hours"`       // "8"
	Load       string `json:"load"`        // "lite"
	NoAutotest bool   `json:"no_autotest"` // false → autotest on
	AutoIdeas  int    `json:"auto_ideas"`  // 999
	AutoBranch bool   `json:"auto_branch"` // false → work on main
	Branch     string `json:"branch"`      // "main"
}

// handleAutodevOptions answers GET /autodev/options.
func (s *HTTPServer) handleAutodevOptions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, http.StatusMethodNotAllowed, "use GET")
		return
	}
	jsonReply(w, http.StatusOK, BuildAutodevOptions())
}

// BuildAutodevOptions probes the local toolchain and returns the
// engine/runner availability + UI defaults. Shared by the HTTP
// handler and the MCP "autodev_options" tool so both surfaces see
// identical capability data.
func BuildAutodevOptions() autodevOptions {
	have := func(bin string) bool { _, err := exec.LookPath(bin); return err == nil }
	missing := func(bins ...string) []string {
		var out []string
		for _, b := range bins {
			if !have(b) {
				out = append(out, b)
			}
		}
		return out
	}

	hasClaude := have("claude")
	hasCodex := have("codex")
	hasAider := have("aider")
	hasOllama := have("ollama")

	engines := []autodevEngineOption{
		{
			Value:       "claude",
			Label:       "Claude Code",
			Description: "Claude Code writes the code end-to-end. Highest quality (67% win rate vs Codex in blind tests, 80.9% SWE-bench), strongest at architecture / refactoring / long-context. Burns weekly limits fast on Max plans.",
			Available:   hasClaude,
			Missing:     missing("claude"),
		},
		{
			Value:       "codex",
			Label:       "OpenAI Codex",
			Description: "OpenAI Codex CLI. ~4x fewer tokens per task than Claude Code → way more headroom on Plus/Pro plans. Leads Terminal-Bench 2.0 (77.3%), better at autonomous DevOps tasks. Slightly lower code quality than Claude Code but actually usable when limits matter. Recommended fallback when your Claude weekly is depleted.",
			Available:   hasCodex,
			Missing:     missing("codex"),
		},
		{
			Value:       "hybrid",
			Label:       "Hybrid (Claude planner + local Aider/Ollama)",
			Description: "Claude plans up to 5 file-scoped subtasks per kick; a local Ollama model executes them via Aider. ~80–95 % cheaper, quality varies with the local model. Best for overnight runs where the planner cost is amortised across many local-model implementations.",
			Available:   hasClaude && hasAider && hasOllama,
			Missing:     missing("claude", "aider", "ollama"),
		},
	}

	runners := []autodevRunnerOption{
		{Value: "claude-code", Label: "Claude Code", Available: hasClaude, Missing: ifNot(hasClaude, "claude")},
		{Value: "codex", Label: "OpenAI Codex", Available: hasCodex, Missing: ifNot(hasCodex, "codex")},
		{Value: "aider-ollama", Label: "Aider + Ollama (local)", Available: hasAider && hasOllama, Missing: firstMissing(hasAider, "aider", hasOllama, "ollama")},
		{Value: "hybrid", Label: "Hybrid (planner+implementer)", Available: hasClaude && hasAider && hasOllama, Missing: firstMissing(hasClaude, "claude", hasAider && hasOllama, "aider+ollama")},
	}

	hardens := []autodevHardenOption{
		{Value: "", Label: "(none)", Description: "Open-ended autodev — no hardening focus"},
		{Value: "security", Label: "Security", Description: "Audit auth, input validation, secrets, deps, CSRF/CORS, rate limiting"},
		{Value: "memory", Label: "Memory", Description: "Find leaks, unbounded caches, retain cycles, oversized assets"},
		{Value: "perf", Label: "Performance", Description: "Cold-start, bundle size, render passes, query batching, slow endpoints"},
		{Value: "quality", Label: "Code Quality", Description: "Dead code, duplication, missing types/tests, brittle mocks"},
		{Value: "all", Label: "All Areas", Description: "Round-robin across security + memory + perf + quality"},
	}

	layerings := []autodevLayeringPreset{
		{Value: "default", Label: "Default (no slicing)", Description: "Single engine end-to-end. Picks the user's --engine choice.", Planner: "", Implementer: "", Available: true},
		{Value: "opus-plan-sonnet-impl", Label: "Opus plans · Sonnet implements", Description: "Cheap-default with high-quality planning. Best when your Claude Max bucket is tight on Opus but you still want premium reasoning on the planning subtask.", Planner: "claude:opus", Implementer: "claude:sonnet", Available: hasClaude},
		{Value: "opus-plan-codex-impl", Label: "Opus plans · Codex implements", Description: "Premium planning, token-efficient implementation. Best for hours-long unattended runs.", Planner: "claude:opus", Implementer: "codex", Available: hasClaude && hasCodex},
		{Value: "opus-plan-aider-impl", Label: "Opus plans · Aider+Ollama implements", Description: "Premium planning, free local implementation. Quality of edits varies with the local Ollama model.", Planner: "claude:opus", Implementer: "aider-ollama", Available: hasClaude && hasAider && hasOllama},
		{Value: "codex-end-to-end", Label: "Codex end-to-end", Description: "No Anthropic spend. Codex plans + implements every kick.", Planner: "codex", Implementer: "codex", Available: hasCodex},
		{Value: "opus-bug-fix", Label: "Opus everywhere (bug-fix mode)", Description: "Highest stakes both tiers. Use with --max-iterations 1 + a focused --prompt.", Planner: "claude:opus", Implementer: "claude:opus", Available: hasClaude},
	}

	return autodevOptions{
		OK:            true,
		Engines:       engines,
		Runners:       runners,
		Hardens:       hardens,
		Layerings:     layerings,
		DeployTargets: resolveAutodevDeployTargets("auto"),
		Defaults: autodevOptionDefaults{
			Engine:     "claude",
			Runner:     "claude-code",
			Hours:      autodevSleepHours,
			Load:       autodevSleepLoad,
			NoAutotest: false,
			AutoIdeas:  999,
			Branch:     "main",
		},
	}
}

func ifNot(ok bool, name string) string {
	if ok {
		return ""
	}
	return name
}

func firstMissing(okA bool, nameA string, okB bool, nameB string) string {
	if !okA {
		return nameA
	}
	if !okB {
		return nameB
	}
	return ""
}
