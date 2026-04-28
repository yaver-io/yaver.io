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

	// Morning match-report capability on this host. Lets the UI
	// show a "Record video (requires iOS Simulator or Android
	// emulator)" label when the toggle is effectively advisory.
	Morning autodevOptionMorning `json:"morning"`
}

type autodevOptionMorning struct {
	// CreateSummaryAvailable is always true (pure on-disk JSON).
	CreateSummaryAvailable bool `json:"createSummaryAvailable"`
	// CreateVideoAvailable is true iff at least one product-demo
	// driver is ready RIGHT NOW on this host. If false, the UI
	// should still let the user enable video (in case they boot a
	// simulator mid-run), but can explain why it will likely be
	// skipped.
	CreateVideoAvailable bool                   `json:"createVideoAvailable"`
	CreateVideoReason    string                 `json:"createVideoReason,omitempty"`
	Drivers              []RecordingDriverStatus `json:"drivers"`
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
	Engine         string `json:"engine"`          // "claude"
	Runner         string `json:"runner"`          // "claude-code" (only when engine=claude)
	Hours          string `json:"hours"`           // "8"
	Load           string `json:"load"`            // "lite"
	NoAutotest     bool   `json:"no_autotest"`     // false → autotest on
	AutoIdeas      int    `json:"auto_ideas"`      // 999
	AutoBranch     bool   `json:"auto_branch"`     // false → work on main
	Branch         string `json:"branch"`          // "main"
	CreateSummary  bool   `json:"create_summary"`  // true — morning match report
	CreateVideo    bool   `json:"create_video"`    // true — product-demo video
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
	hasOpencode := have("opencode")

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
			Label:       "Hybrid (Claude planner + opencode implementer)",
			Description: "Claude plans up to 5 file-scoped subtasks per kick; opencode (with whichever provider you've configured — Anthropic / OpenRouter / Ollama via BYOK) implements them. Lets you split planner cost from implementer cost without yaver having to ship a wrapper for every CLI.",
			Available:   hasClaude && hasOpencode,
			Missing:     missing("claude", "opencode"),
		},
	}

	runners := []autodevRunnerOption{
		{Value: "claude-code", Label: "Claude Code", Available: hasClaude, Missing: ifNot(hasClaude, "claude")},
		{Value: "codex", Label: "OpenAI Codex", Available: hasCodex, Missing: ifNot(hasCodex, "codex")},
		{Value: "opencode", Label: "opencode (BYOK)", Available: hasOpencode, Missing: ifNot(hasOpencode, "opencode")},
		{Value: "hybrid", Label: "Hybrid (claude planner + opencode implementer)", Available: hasClaude && hasOpencode, Missing: firstMissing(hasClaude, "claude", hasOpencode, "opencode")},
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
		{Value: "opus-plan-opencode-impl", Label: "Opus plans · opencode implements", Description: "Premium planning, opencode-driven implementation (route through whichever provider you've BYOK'd into opencode — Anthropic / OpenRouter / Ollama / etc.).", Planner: "claude:opus", Implementer: "opencode", Available: hasClaude && hasOpencode},
		{Value: "codex-end-to-end", Label: "Codex end-to-end", Description: "No Anthropic spend. Codex plans + implements every kick.", Planner: "codex", Implementer: "codex", Available: hasCodex},
		{Value: "opus-bug-fix", Label: "Opus everywhere (bug-fix mode)", Description: "Highest stakes both tiers. Use with --max-iterations 1 + a focused --prompt.", Planner: "claude:opus", Implementer: "claude:opus", Available: hasClaude},
	}

	// Recording driver availability — mirrors GET /morning/drivers
	// but embedded here so the autodev start form has everything
	// it needs in one round trip.
	recMgr := DefaultRecordingManager()
	driverMap := recMgr.Drivers()
	drivers := make([]RecordingDriverStatus, 0, len(driverMap))
	for _, d := range driverMap {
		drivers = append(drivers, d)
	}
	hasAppDemoDriver := recMgr.HasAnyAppDemoDriver()
	videoReason := ""
	if !hasAppDemoDriver {
		videoReason = "no iOS Simulator booted and no Android device attached — video will be skipped. Morning summary still works."
	}

	return autodevOptions{
		OK:            true,
		Engines:       engines,
		Runners:       runners,
		Hardens:       hardens,
		Layerings:     layerings,
		DeployTargets: resolveAutodevDeployTargets("auto"),
		Defaults: autodevOptionDefaults{
			Engine:        "claude",
			Runner:        "claude-code",
			Hours:         autodevSleepHours,
			Load:          autodevSleepLoad,
			NoAutotest:    false,
			AutoIdeas:     999,
			Branch:        "main",
			CreateSummary: true,
			CreateVideo:   true,
		},
		Morning: autodevOptionMorning{
			CreateSummaryAvailable: true,
			CreateVideoAvailable:   hasAppDemoDriver,
			CreateVideoReason:      videoReason,
			Drivers:                drivers,
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
