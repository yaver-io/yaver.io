package main

// loop_cmd.go — scaffolding for M8 (autonomous test → fix → deploy loops).
//
// See docs/roadmap_ci_solo_developer_lower_costs.md — "Autonomous loops"
// section. This file is the thin harness that stitches together the existing
// scheduler, task spawner, and artifact store into a single "auto-dev loop"
// concept. It is intentionally minimal: it parses the .loop.yaml spec,
// persists loop state under ~/.yaver/loops/, and delegates every actual
// step (playtest, AI patch, deploy) to the existing subsystems.

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"gopkg.in/yaml.v3"
)

// LoopMode is the top-level behavior of a loop.
type LoopMode string

const (
	LoopModeFix      LoopMode = "fix"       // persona fuzzer → small patches
	LoopModeAutoFix  LoopMode = "auto-fix"  // always-on hardening, radicalness 0
	LoopModeDevelop  LoopMode = "develop"   // dev prompt → "kick until done"
	LoopModeIdeas    LoopMode = "ideas"     // agent proposes features to multi-select
)

// LoopStatus tracks the state of a loop between iterations.
type LoopStatus string

const (
	LoopStatusIdle       LoopStatus = "idle"
	LoopStatusRunning    LoopStatus = "running"
	LoopStatusPaused     LoopStatus = "paused"
	LoopStatusStopped    LoopStatus = "stopped"
	LoopStatusStuck      LoopStatus = "stuck"
	LoopStatusBudgetHit  LoopStatus = "budget_hit"
	LoopStatusNeedsHuman LoopStatus = "needs_human"
)

// LoopSpec is the parsed form of a .loop.yaml file.
type LoopSpec struct {
	Name    string   `yaml:"name" json:"name"`
	Mode    LoopMode `yaml:"mode" json:"mode"`
	Target  string   `yaml:"target" json:"target"`   // web | ios-sim | android-emu
	URL     string   `yaml:"url,omitempty" json:"url,omitempty"`
	App     string   `yaml:"app,omitempty" json:"app,omitempty"`
	Persona string   `yaml:"persona,omitempty" json:"persona,omitempty"`

	Schedule LoopSchedule `yaml:"schedule" json:"schedule"`
	Playtest LoopPlaytest `yaml:"playtest,omitempty" json:"playtest,omitempty"`
	Think    LoopThink    `yaml:"think" json:"think"`
	Ship     LoopShip     `yaml:"ship" json:"ship"`
	Budget   LoopBudget   `yaml:"budget" json:"budget"`
	Knobs    LoopKnobs    `yaml:"knobs,omitempty" json:"knobs,omitempty"`
}

type LoopSchedule struct {
	Every         string   `yaml:"every,omitempty" json:"every,omitempty"`   // "15m", "1h"
	Cron          string   `yaml:"cron,omitempty" json:"cron,omitempty"`
	MaxIterations int      `yaml:"max_iterations,omitempty" json:"max_iterations,omitempty"` // 0 = unlimited
	OnlyWhen      []string `yaml:"only_when,omitempty" json:"only_when,omitempty"`           // plugged_in, idle, not_on_battery
	ActiveHours   string   `yaml:"active_hours,omitempty" json:"active_hours,omitempty"`     // "22:00-08:00"
	// Timeout is an optional wall-clock cap for a single Auto Develop kick.
	// When set (e.g. "5h", "30m"), the loop aborts the in-flight iteration
	// if it runs past the cap, rolls the worktree back to the last green
	// state, and marks the iteration as `stuck`. Empty = no wall-clock cap.
	// Recommended for develop-mode loops so a runaway prompt can't burn an
	// entire Claude Code session on one kick.
	Timeout string `yaml:"timeout,omitempty" json:"timeout,omitempty"`
}

type LoopPlaytest struct {
	// Enabled lets the dev opt out of the playtest step entirely for a
	// given loop. Default is true (the loop runs the persona fuzzer
	// between every commit). Set to false for develop-mode loops where
	// the feature prompt alone is enough direction and live playtesting
	// would just burn time and session budget. Auto-fix mode ignores
	// this field — it always runs the heuristic-only scan.
	Enabled    *bool    `yaml:"enabled,omitempty" json:"enabled,omitempty"`
	Duration   string   `yaml:"duration,omitempty" json:"duration,omitempty"` // "3m"
	Fuzzer     string   `yaml:"fuzzer,omitempty" json:"fuzzer,omitempty"`     // heuristic | persona-llm
	Heuristics []string `yaml:"heuristics,omitempty" json:"heuristics,omitempty"`
}

// playtestEnabled returns the effective playtest state for a spec.
func (p *LoopPlaytest) playtestEnabled(mode LoopMode) bool {
	if mode == LoopModeAutoFix {
		return true // auto-fix is a heuristic scan; always on
	}
	if mode == LoopModeIdeas {
		return false // ideas mode never runs code in the first place
	}
	if p.Enabled == nil {
		return true // default: playtest on
	}
	return *p.Enabled
}

type LoopThink struct {
	Runner       string   `yaml:"runner" json:"runner"`                                 // claude-code | codex | aider | ollama:<model>
	Fallback     []string `yaml:"fallback,omitempty" json:"fallback,omitempty"`         // provider chain when primary is rate-limited
	Prompt       string   `yaml:"prompt,omitempty" json:"prompt,omitempty"`             // path to prompt file
	MaxEdits     int      `yaml:"max_edits,omitempty" json:"max_edits,omitempty"`       // default 1
	RequireGreen []string `yaml:"require_green,omitempty" json:"require_green,omitempty"` // [typecheck, test]
	Worktree     string   `yaml:"worktree,omitempty" json:"worktree,omitempty"`

	// RespectSessionLimits tells the loop to yield to the dev's own manual
	// usage of the same AI provider. Default true. When true, the loop:
	//   - tracks per-provider token / session-window usage across iterations
	//   - checks the provider's session window before each kick and backs
	//     off (or falls back to the next runner in Fallback) if the window
	//     is already >80% consumed
	//   - refuses to kick during the dev's declared "active_hours"
	//     if the provider shares a session with interactive use
	// This is the "don't burn my Claude Code 5-hour window at 3pm" rule.
	RespectSessionLimits *bool `yaml:"respect_session_limits,omitempty" json:"respect_session_limits,omitempty"`
}

// ProviderLimits is the per-provider session budget the loop respects.
// Defaults apply when a spec does not override them.
type ProviderLimits struct {
	// SessionWindow is the rolling window the provider enforces,
	// e.g. "5h" for Claude Code, "1h" for Codex, unlimited for local Ollama.
	SessionWindow string `json:"session_window"`
	// SoftCapPercent is the usage fraction at which the loop yields
	// the provider (default 80 — leave room for the dev's own work).
	SoftCapPercent int `json:"soft_cap_percent"`
	// SharedWithInteractive is true when the provider's session is
	// shared with the dev's own interactive use (Claude Code: yes,
	// a dedicated Codex API key: no).
	SharedWithInteractive bool `json:"shared_with_interactive"`
}

// defaultProviderLimits returns the baked-in defaults for well-known runners.
func defaultProviderLimits(runner string) ProviderLimits {
	r := strings.ToLower(runner)
	switch {
	case r == "claude-code" || r == "claude":
		return ProviderLimits{SessionWindow: "5h", SoftCapPercent: 80, SharedWithInteractive: true}
	case r == "codex":
		return ProviderLimits{SessionWindow: "1h", SoftCapPercent: 80, SharedWithInteractive: true}
	case r == "aider":
		return ProviderLimits{SessionWindow: "1h", SoftCapPercent: 90, SharedWithInteractive: false}
	case strings.HasPrefix(r, "ollama"):
		return ProviderLimits{SessionWindow: "", SoftCapPercent: 100, SharedWithInteractive: false}
	default:
		return ProviderLimits{SessionWindow: "1h", SoftCapPercent: 80, SharedWithInteractive: true}
	}
}

type LoopShip struct {
	Branch       string `yaml:"branch,omitempty" json:"branch,omitempty"`               // default "main"
	CommitPrefix string `yaml:"commit_prefix,omitempty" json:"commit_prefix,omitempty"` // default "yaver-loop:"
	Deploy       string `yaml:"deploy,omitempty" json:"deploy,omitempty"`               // shell command
}

type LoopBudget struct {
	MaxPatchesPerDay         int `yaml:"max_patches_per_day,omitempty" json:"max_patches_per_day,omitempty"`
	MaxCommitsPerDay         int `yaml:"max_commits_per_day,omitempty" json:"max_commits_per_day,omitempty"`
	MaxTestFlightPerDay      int `yaml:"max_testflight_per_day,omitempty" json:"max_testflight_per_day,omitempty"`
	MaxPlayStorePerDay       int `yaml:"max_playstore_per_day,omitempty" json:"max_playstore_per_day,omitempty"`
	MaxTokensPerDay          int `yaml:"max_tokens_per_day,omitempty" json:"max_tokens_per_day,omitempty"`
	StopAfterConsecutiveStuck int `yaml:"stop_after_consecutive_stuck,omitempty" json:"stop_after_consecutive_stuck,omitempty"`
}

type LoopKnobs struct {
	RadicalnessUI       int    `yaml:"radicalness_ui,omitempty" json:"radicalness_ui,omitempty"`             // 0..10
	RadicalnessFeatures int    `yaml:"radicalness_features,omitempty" json:"radicalness_features,omitempty"` // 0..10
	Tone                string `yaml:"tone,omitempty" json:"tone,omitempty"`                                 // conservative | neutral | casual | playful | irreverent
}

// LoopState is the persisted runtime state for a loop.
type LoopState struct {
	ID               string     `json:"id"`
	Spec             LoopSpec   `json:"spec"`
	Status           LoopStatus `json:"status"`
	CreatedAt        string     `json:"createdAt"`
	LastIterationAt  string     `json:"lastIterationAt,omitempty"`
	IterationCount   int        `json:"iterationCount"`
	ConsecutiveStuck int        `json:"consecutiveStuck"`
	LastSummary      string     `json:"lastSummary,omitempty"`
}

// loopsStorePath returns the JSON file where loop state is persisted.
func loopsStorePath() (string, error) {
	dir, err := ConfigDir()
	if err != nil {
		return "", err
	}
	loopsDir := filepath.Join(dir, "loops")
	if err := os.MkdirAll(loopsDir, 0700); err != nil {
		return "", err
	}
	return filepath.Join(loopsDir, "loops.json"), nil
}

func loadLoops() (map[string]*LoopState, error) {
	p, err := loopsStorePath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]*LoopState{}, nil
		}
		return nil, err
	}
	var loops map[string]*LoopState
	if err := json.Unmarshal(data, &loops); err != nil {
		return nil, err
	}
	if loops == nil {
		loops = map[string]*LoopState{}
	}
	return loops, nil
}

func saveLoops(loops map[string]*LoopState) error {
	p, err := loopsStorePath()
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(loops, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(p, data, 0600)
}

// runLoop is the `yaver loop ...` entry point.
func runLoop(args []string) {
	if len(args) == 0 {
		printLoopUsage()
		return
	}
	sub := args[0]
	rest := args[1:]
	switch sub {
	case "add":
		loopAdd(rest)
	case "list", "ls":
		loopList(rest)
	case "run":
		loopRun(rest)
	case "stop":
		loopStop(rest)
	case "pause":
		loopPause(rest)
	case "resume":
		loopResume(rest)
	case "status":
		loopStatus(rest)
	case "remove", "rm":
		loopRemove(rest)
	case "help", "--help", "-h", "":
		printLoopUsage()
	default:
		fmt.Fprintf(os.Stderr, "Unknown loop subcommand: %s\n\n", sub)
		printLoopUsage()
		os.Exit(1)
	}
}

func printLoopUsage() {
	fmt.Print(`Yaver auto-dev loops — test → fix → deploy in a loop.

Usage:
  yaver loop add <path-to-loop.yaml>     Register a loop from a YAML spec
  yaver loop list                        List all registered loops and their status
  yaver loop run <name>                  Run one iteration of a loop (blocking)
  yaver loop stop <name>                 Stop a running loop immediately
  yaver loop pause <name>                Pause a loop until resumed
  yaver loop resume <name>               Resume a paused loop
  yaver loop status <name>               Show detailed status for one loop
  yaver loop remove <name>               Unregister a loop (does not delete branch)

Modes (set via the spec's "mode:" field):
  fix         Persona fuzzer finds friction, AI writes one small patch per tick.
  auto-fix    Always-on hardening — typos, overlaps, contrast, diacritics, dead
              buttons. Pinned to radicalness 0, zero creativity allowed.
  develop     "Kick until done" against a dev-authored feature prompt.
  ideas       Agent proposes features; dev multi-selects; loop queues them.

See docs/roadmap_ci_solo_developer_lower_costs.md for the full M8 spec.
`)
}

// loopAdd loads a .loop.yaml file, validates it, and stores it under ~/.yaver/loops/.
func loopAdd(args []string) {
	fs := flag.NewFlagSet("loop add", flag.ExitOnError)
	fs.Parse(args)
	if fs.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "usage: yaver loop add <path-to-loop.yaml>")
		os.Exit(1)
	}
	path := fs.Arg(0)
	data, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "read %s: %v\n", path, err)
		os.Exit(1)
	}
	var spec LoopSpec
	if err := yaml.Unmarshal(data, &spec); err != nil {
		fmt.Fprintf(os.Stderr, "parse %s: %v\n", path, err)
		os.Exit(1)
	}
	if err := validateLoopSpec(&spec); err != nil {
		fmt.Fprintf(os.Stderr, "invalid loop spec: %v\n", err)
		os.Exit(1)
	}
	applyLoopDefaults(&spec)
	warnLoopDependencies(&spec)

	loops, err := loadLoops()
	if err != nil {
		fmt.Fprintf(os.Stderr, "load loops: %v\n", err)
		os.Exit(1)
	}
	if existing, ok := loops[spec.Name]; ok {
		existing.Spec = spec
		fmt.Printf("Updated loop %q (mode=%s, target=%s).\n", spec.Name, spec.Mode, spec.Target)
	} else {
		loops[spec.Name] = &LoopState{
			ID:        uuid.New().String(),
			Spec:      spec,
			Status:    LoopStatusIdle,
			CreatedAt: time.Now().UTC().Format(time.RFC3339),
		}
		fmt.Printf("Registered loop %q (mode=%s, target=%s).\n", spec.Name, spec.Mode, spec.Target)
	}
	if err := saveLoops(loops); err != nil {
		fmt.Fprintf(os.Stderr, "save loops: %v\n", err)
		os.Exit(1)
	}
}

func validateLoopSpec(s *LoopSpec) error {
	if s.Name == "" {
		return fmt.Errorf("spec is missing required field: name")
	}
	if s.Mode == "" {
		return fmt.Errorf("spec is missing required field: mode")
	}
	switch s.Mode {
	case LoopModeFix, LoopModeAutoFix, LoopModeDevelop, LoopModeIdeas:
	default:
		return fmt.Errorf("unknown mode %q (want fix|auto-fix|develop|ideas)", s.Mode)
	}
	if s.Target == "" {
		return fmt.Errorf("spec is missing required field: target")
	}
	if s.Think.Runner == "" {
		return fmt.Errorf("think.runner is required (claude-code | codex | aider | ollama:<model>)")
	}
	if s.Knobs.RadicalnessUI < 0 || s.Knobs.RadicalnessUI > 10 {
		return fmt.Errorf("knobs.radicalness_ui must be in 0..10")
	}
	if s.Knobs.RadicalnessFeatures < 0 || s.Knobs.RadicalnessFeatures > 10 {
		return fmt.Errorf("knobs.radicalness_features must be in 0..10")
	}
	return nil
}

// applyLoopDefaults fills in missing fields per the M8 defaults doc.
func applyLoopDefaults(s *LoopSpec) {
	if s.Ship.Branch == "" {
		s.Ship.Branch = "main" // solo-dev default per M8
	}
	if s.Ship.CommitPrefix == "" {
		s.Ship.CommitPrefix = "yaver-loop:"
	}
	if s.Think.MaxEdits == 0 {
		s.Think.MaxEdits = 1
	}
	if len(s.Think.RequireGreen) == 0 {
		s.Think.RequireGreen = []string{"typecheck"}
	}
	if s.Budget.MaxPatchesPerDay == 0 {
		s.Budget.MaxPatchesPerDay = 30
	}
	if s.Budget.MaxCommitsPerDay == 0 {
		s.Budget.MaxCommitsPerDay = 30
	}
	if s.Budget.MaxTestFlightPerDay == 0 {
		s.Budget.MaxTestFlightPerDay = 1
	}
	if s.Budget.MaxPlayStorePerDay == 0 {
		s.Budget.MaxPlayStorePerDay = 1
	}
	if s.Budget.StopAfterConsecutiveStuck == 0 {
		s.Budget.StopAfterConsecutiveStuck = 5
	}
	if s.Knobs.Tone == "" {
		s.Knobs.Tone = "casual"
	}
	// Session-limits respect defaults to true — the loop should never
	// burn the dev's Claude Code 5-hour window in the middle of the
	// workday unless the dev explicitly opts out.
	if s.Think.RespectSessionLimits == nil {
		trueVal := true
		s.Think.RespectSessionLimits = &trueVal
	}
	// Auto-fix mode is pinned to radicalness 0 and is never allowed to
	// trigger store deploys regardless of spec — it's the always-on
	// background hardening loop, not a release channel.
	if s.Mode == LoopModeAutoFix {
		s.Knobs.RadicalnessUI = 0
		s.Knobs.RadicalnessFeatures = 0
		s.Budget.MaxTestFlightPerDay = 0
		s.Budget.MaxPlayStorePerDay = 0
	}
	// Ideas mode never writes code, so it cannot commit, patch, or deploy.
	if s.Mode == LoopModeIdeas {
		s.Budget.MaxPatchesPerDay = 0
		s.Budget.MaxCommitsPerDay = 0
		s.Budget.MaxTestFlightPerDay = 0
		s.Budget.MaxPlayStorePerDay = 0
	}
	// Hard ceiling: nothing on the planet gets >10 TestFlight uploads a day.
	if s.Budget.MaxTestFlightPerDay > 10 {
		s.Budget.MaxTestFlightPerDay = 10
	}
}

// warnLoopDependencies prints non-fatal warnings when the tools a loop needs
// to actually run are not installed on the host. It never blocks add/run —
// the dev decides whether to proceed with a partially-satisfied stack.
func warnLoopDependencies(s *LoopSpec) {
	missing := []string{}
	check := func(bin, hint string) {
		if _, err := exec.LookPath(bin); err != nil {
			missing = append(missing, fmt.Sprintf("  - %-14s %s", bin, hint))
		}
	}

	if s.Playtest.playtestEnabled(s.Mode) {
		switch s.Target {
		case "web":
			// Web playtest uses embedded CDP (chromedp is baked into the
			// agent), so Chrome/Chromium is the only external dependency.
			_, chromeErr := exec.LookPath("google-chrome")
			_, chromiumErr := exec.LookPath("chromium")
			if chromeErr != nil && chromiumErr != nil {
				missing = append(missing,
					fmt.Sprintf("  - %-14s %s", "google-chrome",
						"needed for web target playtest (install Chrome or Chromium)"))
			}
		case "ios-sim":
			check("xcrun", "needed for iOS Simulator control (install Xcode)")
		case "android-emu":
			check("adb", "needed for Android Emulator control (install Android SDK)")
			check("emulator", "needed for Android Emulator control")
		}
	}

	// The AI runner is required for every mode except ideas (which still
	// needs a runner to generate the list).
	switch strings.ToLower(s.Think.Runner) {
	case "claude-code", "claude":
		check("claude", "`claude` CLI not on PATH — install Claude Code from https://claude.com/product/claude-code")
	case "codex":
		check("codex", "`codex` CLI not on PATH")
	case "aider":
		check("aider", "`aider` CLI not on PATH — `pip install aider-chat`")
	default:
		if strings.HasPrefix(strings.ToLower(s.Think.Runner), "ollama") {
			check("ollama", "`ollama` CLI not on PATH")
		}
	}

	// Typecheck green-gate needs the project's typechecker — usually tsc
	// or go. We just probe node + tsc as a sanity check for the common
	// RN/web case.
	for _, gate := range s.Think.RequireGreen {
		if gate == "typecheck" {
			check("node", "node is required to run `npx tsc` green gate")
		}
	}

	if len(missing) > 0 {
		fmt.Fprintln(os.Stderr, "\n[yaver loop] dependency warnings — loop may not run cleanly:")
		for _, m := range missing {
			fmt.Fprintln(os.Stderr, m)
		}
		fmt.Fprintln(os.Stderr, "  (spec was still registered; install the tools and re-run `yaver loop run`)")
	}
}

func loopList(_ []string) {
	loops, err := loadLoops()
	if err != nil {
		fmt.Fprintf(os.Stderr, "load loops: %v\n", err)
		os.Exit(1)
	}
	if len(loops) == 0 {
		fmt.Println("No loops registered. Use `yaver loop add <path-to-loop.yaml>`.")
		return
	}
	names := make([]string, 0, len(loops))
	for name := range loops {
		names = append(names, name)
	}
	sort.Strings(names)
	fmt.Printf("%-24s  %-10s  %-10s  %-8s  %s\n", "NAME", "MODE", "STATUS", "ITERS", "LAST SUMMARY")
	for _, name := range names {
		l := loops[name]
		summary := l.LastSummary
		if len(summary) > 50 {
			summary = summary[:47] + "..."
		}
		fmt.Printf("%-24s  %-10s  %-10s  %-8d  %s\n",
			name, l.Spec.Mode, l.Status, l.IterationCount, summary)
	}
}

func loopRun(args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: yaver loop run <name>")
		os.Exit(1)
	}
	name := args[0]
	loops, err := loadLoops()
	if err != nil {
		fmt.Fprintf(os.Stderr, "load loops: %v\n", err)
		os.Exit(1)
	}
	l, ok := loops[name]
	if !ok {
		fmt.Fprintf(os.Stderr, "loop %q not found. Use `yaver loop list`.\n", name)
		os.Exit(1)
	}
	if l.Status == LoopStatusStopped || l.Status == LoopStatusPaused {
		fmt.Fprintf(os.Stderr, "loop %q is %s — resume it first (`yaver loop resume %s`).\n", name, l.Status, name)
		os.Exit(1)
	}

	// Physical kill-switch: if ~/.yaver/loops/<name>/STOP exists, refuse to run.
	killPath, _ := loopKillFilePath(name)
	if _, err := os.Stat(killPath); err == nil {
		fmt.Fprintf(os.Stderr, "STOP file exists at %s — remove it to resume.\n", killPath)
		os.Exit(2)
	}

	fmt.Printf("=== yaver loop run %s ===\n", name)
	fmt.Printf("mode=%s target=%s persona=%s\n", l.Spec.Mode, l.Spec.Target, l.Spec.Persona)
	fmt.Printf("radicalness=(ui:%d features:%d) tone=%s\n",
		l.Spec.Knobs.RadicalnessUI, l.Spec.Knobs.RadicalnessFeatures, l.Spec.Knobs.Tone)
	fmt.Printf("ship branch=%s commit_prefix=%q\n", l.Spec.Ship.Branch, l.Spec.Ship.CommitPrefix)
	fmt.Printf("budget: patches/day=%d commits/day=%d testflight/day=%d\n",
		l.Spec.Budget.MaxPatchesPerDay, l.Spec.Budget.MaxCommitsPerDay, l.Spec.Budget.MaxTestFlightPerDay)

	phases := loopPhasesForMode(l.Spec.Mode)
	fmt.Println("\nPhases for one iteration:")
	for i, phase := range phases {
		fmt.Printf("  %d. %s\n", i+1, phase)
	}

	// Scaffolding stub: a future revision wires these phases to TaskManager.CreateTask()
	// so each phase becomes a managed yaver task with streamed stdout and artifacts
	// routed through the existing per-task store. Until M8 is fully implemented,
	// this command records the intent and leaves execution to the human operator
	// (or the shell wrapper in scripts/).
	fmt.Println("\n[scaffolding] Phase execution is not yet wired to TaskManager.CreateTask.")
	fmt.Println("[scaffolding] This iteration recorded intent to loop state only.")

	l.Status = LoopStatusRunning
	l.IterationCount++
	l.LastIterationAt = time.Now().UTC().Format(time.RFC3339)
	l.LastSummary = fmt.Sprintf("scaffolding run #%d (%d phases)", l.IterationCount, len(phases))
	if err := saveLoops(loops); err != nil {
		fmt.Fprintf(os.Stderr, "save loops: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("\nRecorded iteration #%d.\n", l.IterationCount)
}

func loopPhasesForMode(mode LoopMode) []string {
	switch mode {
	case LoopModeFix:
		return []string{
			"boot target (web/sim/emu)",
			"persona fuzzer plays it for playtest.duration",
			"heuristic detectors flag friction",
			"write per-iteration report",
			"spawn AI runner with report + persona framing",
			"green-gate: typecheck + tests",
			"commit to ship.branch",
			"deploy step",
		}
	case LoopModeAutoFix:
		return []string{
			"boot target",
			"run heuristic-only scan (no creativity)",
			"per finding, write a trivial patch",
			"green-gate: typecheck",
			"commit one patch per finding to ship.branch",
			"deploy step",
		}
	case LoopModeDevelop:
		return []string{
			"load dev-authored feature prompt",
			"spawn AI runner with prompt + last kick's next_step",
			"green-gate: typecheck + tests",
			"commit to ship.branch",
			"deploy step",
			"boot target and re-playtest with persona",
			"parse AI status: in_progress | done | stuck | needs_human",
			"schedule next kick or terminate",
		}
	case LoopModeIdeas:
		return []string{
			"read recent commits, TODOs, product.md",
			"read latest fuzzer reports (unresolved friction)",
			"spawn AI runner asking for ranked feature list",
			"publish idea list to mobile Auto Dev tab",
			"wait for dev multi-select",
			"per selected idea, queue a develop-mode loop",
		}
	default:
		return []string{"unknown mode"}
	}
}

func loopKillFilePath(name string) (string, error) {
	dir, err := ConfigDir()
	if err != nil {
		return "", err
	}
	loopDir := filepath.Join(dir, "loops", name)
	if err := os.MkdirAll(loopDir, 0700); err != nil {
		return "", err
	}
	return filepath.Join(loopDir, "STOP"), nil
}

func loopStop(args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: yaver loop stop <name>")
		os.Exit(1)
	}
	name := args[0]
	loops, err := loadLoops()
	if err != nil {
		fmt.Fprintf(os.Stderr, "load loops: %v\n", err)
		os.Exit(1)
	}
	l, ok := loops[name]
	if !ok {
		fmt.Fprintf(os.Stderr, "loop %q not found\n", name)
		os.Exit(1)
	}
	l.Status = LoopStatusStopped
	if err := saveLoops(loops); err != nil {
		fmt.Fprintf(os.Stderr, "save loops: %v\n", err)
		os.Exit(1)
	}
	// Drop a physical kill file as an extra belt-and-braces signal so a wedged
	// in-flight iteration aborts on its next poll.
	if killPath, err := loopKillFilePath(name); err == nil {
		_ = os.WriteFile(killPath, []byte(time.Now().UTC().Format(time.RFC3339)+"\n"), 0600)
	}
	fmt.Printf("Stopped loop %q.\n", name)
}

func loopPause(args []string) {
	setLoopStatus(args, LoopStatusPaused, "Paused")
}

func loopResume(args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: yaver loop resume <name>")
		os.Exit(1)
	}
	name := args[0]
	loops, err := loadLoops()
	if err != nil {
		fmt.Fprintf(os.Stderr, "load loops: %v\n", err)
		os.Exit(1)
	}
	l, ok := loops[name]
	if !ok {
		fmt.Fprintf(os.Stderr, "loop %q not found\n", name)
		os.Exit(1)
	}
	l.Status = LoopStatusIdle
	if killPath, err := loopKillFilePath(name); err == nil {
		_ = os.Remove(killPath)
	}
	if err := saveLoops(loops); err != nil {
		fmt.Fprintf(os.Stderr, "save loops: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Resumed loop %q.\n", name)
}

func setLoopStatus(args []string, status LoopStatus, verb string) {
	if len(args) < 1 {
		fmt.Fprintf(os.Stderr, "usage: yaver loop %s <name>\n", strings.ToLower(verb))
		os.Exit(1)
	}
	name := args[0]
	loops, err := loadLoops()
	if err != nil {
		fmt.Fprintf(os.Stderr, "load loops: %v\n", err)
		os.Exit(1)
	}
	l, ok := loops[name]
	if !ok {
		fmt.Fprintf(os.Stderr, "loop %q not found\n", name)
		os.Exit(1)
	}
	l.Status = status
	if err := saveLoops(loops); err != nil {
		fmt.Fprintf(os.Stderr, "save loops: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("%s loop %q.\n", verb, name)
}

func loopStatus(args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: yaver loop status <name>")
		os.Exit(1)
	}
	name := args[0]
	loops, err := loadLoops()
	if err != nil {
		fmt.Fprintf(os.Stderr, "load loops: %v\n", err)
		os.Exit(1)
	}
	l, ok := loops[name]
	if !ok {
		fmt.Fprintf(os.Stderr, "loop %q not found\n", name)
		os.Exit(1)
	}
	out, _ := json.MarshalIndent(l, "", "  ")
	fmt.Println(string(out))
}

func loopRemove(args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: yaver loop remove <name>")
		os.Exit(1)
	}
	name := args[0]
	loops, err := loadLoops()
	if err != nil {
		fmt.Fprintf(os.Stderr, "load loops: %v\n", err)
		os.Exit(1)
	}
	if _, ok := loops[name]; !ok {
		fmt.Fprintf(os.Stderr, "loop %q not found\n", name)
		os.Exit(1)
	}
	delete(loops, name)
	if err := saveLoops(loops); err != nil {
		fmt.Fprintf(os.Stderr, "save loops: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Removed loop %q.\n", name)
}
