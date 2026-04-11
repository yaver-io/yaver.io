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
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"gopkg.in/yaml.v3"
)

// loopsFileMu serializes read-modify-write cycles against loops.json
// across the CLI, scheduler ticks, and HTTP handlers in the same
// process. Cross-process races are mitigated by saveLoops's atomic
// write-then-rename — concurrent writers race, but the file never
// ends up half-written.
var loopsFileMu sync.Mutex

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
	PromptInline string   `yaml:"prompt_inline,omitempty" json:"prompt_inline,omitempty"` // inline prompt embedded in the spec
	MaxEdits     int      `yaml:"max_edits,omitempty" json:"max_edits,omitempty"`       // default 1
	MaxKicksPerRun int    `yaml:"max_kicks_per_run,omitempty" json:"max_kicks_per_run,omitempty"` // develop-mode safety cap (default 10)
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
	// ReleaseTrain gates the Deploy step so the loop only ships after
	// N consecutive green kicks and stays under the daily TestFlight
	// budget. If unset / disabled, Deploy runs on every done status
	// like before.
	ReleaseTrain LoopReleaseTrain `yaml:"release_train,omitempty" json:"release_train,omitempty"`
}

// LoopReleaseTrain controls how aggressively a loop ships. The defaults
// give a solo dev a "fix three things green, then cut a build" cadence
// without them having to think about it.
type LoopReleaseTrain struct {
	// N is the number of consecutive green iterations required before
	// Deploy runs. 0 = disabled (runs on every done status — old
	// behavior). Typical value: 3.
	N int `yaml:"n,omitempty" json:"n,omitempty"`
	// Paused is a kill-switch the dev flips when they don't want the
	// loop to cut builds — e.g. during a manual QA day. `yaver loop
	// release-train pause <name>` toggles it.
	Paused bool `yaml:"paused,omitempty" json:"paused,omitempty"`
	// Target labels what the deploy command ships to, used only for
	// log output. "testflight" / "playstore" / "web" / etc.
	Target string `yaml:"target,omitempty" json:"target,omitempty"`
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
	ID        string     `json:"id"`
	Spec      LoopSpec   `json:"spec"`
	Status    LoopStatus `json:"status"`
	CreatedAt string     `json:"createdAt"`
	// WorkDir is the project directory this loop operates on. Captured
	// at `loop add` time from the dev's current working directory — the
	// repo root they want the loop to edit, commit, and push in. The
	// scheduler's subprocess invocations `cd` into this directory before
	// running the iteration, so a loop registered from ~/Workspace/sfmg
	// always operates on sfmg no matter who invokes it.
	WorkDir string `json:"workDir,omitempty"`
	// ScheduleID is the ID of the ScheduledTask the daemon registered
	// for this loop (empty if the daemon wasn't running at add time).
	// Used by `loop remove` / `loop stop` to tear down the schedule.
	ScheduleID       string `json:"scheduleId,omitempty"`
	LastIterationAt  string `json:"lastIterationAt,omitempty"`
	IterationCount   int    `json:"iterationCount"`
	ConsecutiveStuck int    `json:"consecutiveStuck"`
	LastSummary      string `json:"lastSummary,omitempty"`

	// PromptInline is a runtime-set feature prompt that overrides
	// anything in the spec. Set via `yaver loop prompt set <name>
	// "<message>"` (or the mobile Auto Dev tab once wired). The
	// override persists until explicitly cleared via `yaver loop
	// prompt clear <name>` — that lets the dev write a prompt on the
	// phone, kick the loop, and have the same prompt drive every
	// tick until the feature is done without having to re-send it.
	PromptInline string `json:"promptInline,omitempty"`

	// Budget counters — reset on day change (UTC). Enforced in
	// develop-mode multi-kick loops to keep an Auto Develop session
	// from blowing past the dev's max_commits_per_day or
	// max_patches_per_day before they wake up.
	BudgetDayKey  string `json:"budgetDayKey,omitempty"` // YYYY-MM-DD UTC
	CommitsToday  int    `json:"commitsToday"`
	PatchesToday  int    `json:"patchesToday"`
	TestflightToday int  `json:"testflightToday"`

	// LastIdeasPath is the filesystem path to the most recently
	// generated ideas.json from an ideas-mode kick. Populated by
	// runIdeasKick and read by the mobile Auto Dev tab over HTTP.
	LastIdeasPath string `json:"lastIdeasPath,omitempty"`

	// GreenRunSinceLastDeploy tracks how many consecutive green
	// iterations have happened since the last successful deploy.
	// Release-train gating in phaseDeploy checks this against
	// Spec.Ship.ReleaseTrain.N and resets it on a successful ship.
	GreenRunSinceLastDeploy int `json:"greenRunSinceLastDeploy"`
}

// rollBudgetDay zeroes the daily counters if the current UTC day is
// different from BudgetDayKey. Idempotent; safe to call on every kick.
func (l *LoopState) rollBudgetDay() {
	today := time.Now().UTC().Format("2006-01-02")
	if l.BudgetDayKey == today {
		return
	}
	l.BudgetDayKey = today
	l.CommitsToday = 0
	l.PatchesToday = 0
	l.TestflightToday = 0
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
	loopsFileMu.Lock()
	defer loopsFileMu.Unlock()
	return loadLoopsLocked()
}

func loadLoopsLocked() (map[string]*LoopState, error) {
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
	loopsFileMu.Lock()
	defer loopsFileMu.Unlock()
	return saveLoopsLocked(loops)
}

// saveLoopsLocked writes the loops map to loops.json via an atomic
// write-then-rename pattern: a sibling `loops.json.tmp` is written
// and fsync'd, then renamed over the live path. Readers either see
// the old file or the new file — never a half-written one. Callers
// that already hold loopsFileMu must use this variant.
func saveLoopsLocked(loops map[string]*LoopState) error {
	p, err := loopsStorePath()
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(loops, "", "  ")
	if err != nil {
		return err
	}
	tmp := p + ".tmp"
	// O_CREATE|O_TRUNC|O_WRONLY — a stale tmp from a crashed previous
	// run gets overwritten, not appended to.
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	if _, werr := f.Write(data); werr != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return werr
	}
	if serr := f.Sync(); serr != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return serr
	}
	if cerr := f.Close(); cerr != nil {
		_ = os.Remove(tmp)
		return cerr
	}
	return os.Rename(tmp, p)
}

// withLoops runs fn on the current loops map under the file mutex
// and, if fn returns true, persists the mutated map. Use this for
// any read-modify-write sequence so racing writers don't clobber
// each other. Returns the same map that was passed into fn (for
// callers that want to inspect it after save).
func withLoops(fn func(map[string]*LoopState) (bool, error)) (map[string]*LoopState, error) {
	loopsFileMu.Lock()
	defer loopsFileMu.Unlock()
	loops, err := loadLoopsLocked()
	if err != nil {
		return nil, err
	}
	save, err := fn(loops)
	if err != nil {
		return loops, err
	}
	if save {
		if err := saveLoopsLocked(loops); err != nil {
			return loops, err
		}
	}
	return loops, nil
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
	case "prompt":
		loopPrompt(rest)
	case "ideas":
		loopIdeas(rest)
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
  yaver loop add <path-to-loop.yaml>      Register a loop from a YAML spec
  yaver loop list                         List all registered loops and their status
  yaver loop run <name>                   Run one iteration of a loop (blocking)
  yaver loop stop <name>                  Stop a running loop immediately
  yaver loop pause <name>                 Pause a loop until resumed
  yaver loop resume <name>                Resume a paused loop
  yaver loop status <name>                Show detailed status for one loop
  yaver loop remove <name>                Unregister a loop (does not delete branch)

Auto Develop prompt management:
  yaver loop prompt set <name> "<msg>"              Set inline feature prompt
  yaver loop prompt show <name>                     Show current active prompt
  yaver loop prompt clear <name>                    Clear inline prompt
  yaver loop prompt pick <develop> <idea-id> [--run] [--ideas-from <loop>]
                                                    Pick an idea from ideas.json and
                                                    stash its .prompt as the inline
                                                    prompt; --run kicks immediately

Ideas mode:
  yaver loop ideas show <name>            Print the latest generated ideas.json
  yaver loop ideas kick <name>            Regenerate ideas immediately (same as run)

Modes (set via the spec's "mode:" field):
  fix         Persona fuzzer finds friction, AI writes one small patch per tick.
  auto-fix    Always-on hardening — typos, overlaps, contrast, diacritics, dead
              buttons. Pinned to radicalness 0, zero creativity allowed.
  develop     "Kick until done" against a dev-authored feature prompt. Runs
              multiple kicks per invocation, threading next_step between them,
              stops on done/stuck/needs_human/budget_hit/stopped.
  ideas       Agent proposes features; dev multi-selects; loop queues them.

See docs/roadmap_ci_solo_developer_lower_costs.md for the full M8 spec.
`)
}

// loopPrompt handles `yaver loop prompt {set|show|clear|pick} <name>`.
// This is the Auto Develop entry point for devs writing one-off
// feature prompts from their phone / terminal without touching any
// .md files on disk.
//
//	set   — stash an inline feature prompt on the loop
//	show  — print the currently effective prompt with its source
//	clear — wipe the runtime inline prompt (falls back to spec file)
//	pick  — load an idea from the loop's ideas.json by ID and stash
//	        its .prompt field as the inline prompt. Pass --run to
//	        kick the loop immediately after the stash succeeds.
func loopPrompt(args []string) {
	if len(args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: yaver loop prompt {set|show|clear|pick} <name> [<id|msg>]")
		os.Exit(1)
	}
	sub := args[0]
	if sub == "pick" {
		loopPromptPick(args[1:])
		return
	}
	name := args[1]
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

	switch sub {
	case "set":
		if len(args) < 3 {
			fmt.Fprintln(os.Stderr, "usage: yaver loop prompt set <name> \"<feature prompt>\"")
			os.Exit(1)
		}
		// Join any remaining args so quoted multi-word prompts work
		// without the shell fighting us.
		msg := strings.Join(args[2:], " ")
		msg = strings.TrimSpace(msg)
		if msg == "" {
			fmt.Fprintln(os.Stderr, "prompt is empty — use `yaver loop prompt clear` to wipe")
			os.Exit(1)
		}
		l.PromptInline = msg
		// Clearing consecutive_stuck gives the new prompt a clean start;
		// otherwise an ambiguous previous prompt could immediately pause
		// the loop before the new feature has a chance to run.
		l.ConsecutiveStuck = 0
		if l.Status == LoopStatusPaused || l.Status == LoopStatusStuck ||
			l.Status == LoopStatusNeedsHuman || l.Status == LoopStatusBudgetHit {
			l.Status = LoopStatusIdle
		}
		loops[name] = l
		if err := saveLoops(loops); err != nil {
			fmt.Fprintf(os.Stderr, "save loops: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Prompt set for loop %q (%d chars). The next `yaver loop run %s` will use it.\n",
			name, len(msg), name)

	case "show":
		if l.PromptInline != "" {
			fmt.Println("--- runtime inline prompt (yaver loop prompt set) ---")
			fmt.Println(l.PromptInline)
			return
		}
		if l.Spec.Think.PromptInline != "" {
			fmt.Println("--- spec inline prompt (.loop.yaml think.prompt_inline) ---")
			fmt.Println(l.Spec.Think.PromptInline)
			return
		}
		if l.Spec.Think.Prompt != "" {
			path := l.Spec.Think.Prompt
			if !filepath.IsAbs(path) && l.WorkDir != "" {
				path = filepath.Join(l.WorkDir, path)
			}
			fmt.Printf("--- file prompt (%s) ---\n", path)
			if data, rerr := os.ReadFile(path); rerr == nil {
				fmt.Print(string(data))
			} else {
				fmt.Fprintf(os.Stderr, "(could not read prompt file: %v)\n", rerr)
			}
			return
		}
		fmt.Println("(no prompt set — use `yaver loop prompt set " + name + " \"<msg>\"`)")

	case "clear":
		if l.PromptInline == "" {
			fmt.Println("(no inline prompt to clear)")
			return
		}
		l.PromptInline = ""
		loops[name] = l
		if err := saveLoops(loops); err != nil {
			fmt.Fprintf(os.Stderr, "save loops: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Cleared inline prompt for loop %q. Next run falls back to the spec's prompt source.\n", name)

	default:
		fmt.Fprintf(os.Stderr, "unknown prompt subcommand %q (want set|show|clear)\n", sub)
		os.Exit(1)
	}
}

// loopPromptPick loads an idea by ID out of a loop's ideas.json and
// sets that idea's `.prompt` field as the loop's runtime inline
// prompt. Optional --run kicks the loop immediately so a dev can go
// from "tap an idea in the mobile Auto Dev tab" to a committed patch
// in one sentence: `yaver loop prompt pick sfmg-genz derbi-basinc --run`.
//
// The idea can come from any loop's ideas.json — an ideas-mode loop
// typically owns one set of candidate features, and any develop-mode
// loop can pick one of them to drive. So the signature is:
//
//	yaver loop prompt pick <develop-loop-name> <idea-id> [--ideas-from <source-loop>] [--run]
//
// Default for --ideas-from is the same loop name (looks for
// ideas.json inside <develop-loop-name>'s own state dir) so a
// single-loop setup works without extra flags.
func loopPromptPick(args []string) {
	// Permissive arg scanner so devs can put --run / --ideas-from in
	// any position — Go's flag package only accepts flags before
	// positionals, which is unnatural for this command's usage.
	var ideasFrom string
	var runNow bool
	positional := []string{}
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--run" || a == "-run":
			runNow = true
		case a == "--ideas-from" || a == "-ideas-from":
			if i+1 < len(args) {
				ideasFrom = args[i+1]
				i++
			}
		case strings.HasPrefix(a, "--ideas-from="):
			ideasFrom = strings.TrimPrefix(a, "--ideas-from=")
		default:
			positional = append(positional, a)
		}
	}
	if len(positional) < 2 {
		fmt.Fprintln(os.Stderr, "usage: yaver loop prompt pick <develop-loop> <idea-id> [--ideas-from <source-loop>] [--run]")
		os.Exit(1)
	}
	targetName := positional[0]
	ideaID := positional[1]
	sourceName := ideasFrom
	if sourceName == "" {
		sourceName = targetName
	}

	loops, err := loadLoops()
	if err != nil {
		fmt.Fprintf(os.Stderr, "load loops: %v\n", err)
		os.Exit(1)
	}
	target, ok := loops[targetName]
	if !ok {
		fmt.Fprintf(os.Stderr, "loop %q not found\n", targetName)
		os.Exit(1)
	}

	// Resolve the ideas.json path: prefer LastIdeasPath if the source
	// loop has been run before, otherwise fall back to the canonical
	// location.
	var ideasPath string
	if src, ok := loops[sourceName]; ok && src.LastIdeasPath != "" {
		ideasPath = src.LastIdeasPath
	} else {
		base, _ := ConfigDir()
		ideasPath = filepath.Join(base, "loops", sourceName, "ideas.json")
	}

	data, rerr := os.ReadFile(ideasPath)
	if rerr != nil {
		fmt.Fprintf(os.Stderr, "no ideas.json for loop %q at %s — run `yaver loop run %s` first\n",
			sourceName, ideasPath, sourceName)
		os.Exit(1)
	}

	// ideas.json is { generated_at, loop_name, persona, ideas: [...] }
	// per persistIdeas. Decode loosely so we can cope with extra fields.
	var payload struct {
		Ideas []struct {
			ID          string `json:"id"`
			Title       string `json:"title"`
			Description string `json:"description"`
			Prompt      string `json:"prompt"`
		} `json:"ideas"`
	}
	if jerr := json.Unmarshal(data, &payload); jerr != nil {
		fmt.Fprintf(os.Stderr, "parse ideas.json: %v\n", jerr)
		os.Exit(1)
	}

	var picked *struct {
		ID          string `json:"id"`
		Title       string `json:"title"`
		Description string `json:"description"`
		Prompt      string `json:"prompt"`
	}
	for i := range payload.Ideas {
		if payload.Ideas[i].ID == ideaID {
			picked = &payload.Ideas[i]
			break
		}
	}
	if picked == nil {
		fmt.Fprintf(os.Stderr, "idea %q not found in %s. Available ids:\n", ideaID, ideasPath)
		for _, it := range payload.Ideas {
			fmt.Fprintf(os.Stderr, "  - %s: %s\n", it.ID, it.Title)
		}
		os.Exit(1)
	}
	if strings.TrimSpace(picked.Prompt) == "" {
		fmt.Fprintf(os.Stderr, "idea %q has no .prompt field — regenerate ideas.json with a newer Auto Dev build\n", ideaID)
		os.Exit(1)
	}

	target.PromptInline = picked.Prompt
	target.ConsecutiveStuck = 0
	if target.Status == LoopStatusPaused || target.Status == LoopStatusStuck ||
		target.Status == LoopStatusNeedsHuman || target.Status == LoopStatusBudgetHit {
		target.Status = LoopStatusIdle
	}
	loops[targetName] = target
	if err := saveLoops(loops); err != nil {
		fmt.Fprintf(os.Stderr, "save loops: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Picked idea %q → %s\n", ideaID, picked.Title)
	fmt.Printf("Loop %q inline prompt set (%d chars).\n", targetName, len(picked.Prompt))

	if runNow {
		fmt.Println()
		loopRun([]string{targetName})
	}
}

// loopIdeas handles `yaver loop ideas {show|kick} <name>` — inspect
// or regenerate the ranked feature list an ideas-mode loop writes.
func loopIdeas(args []string) {
	if len(args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: yaver loop ideas {show|kick} <name>")
		os.Exit(1)
	}
	sub := args[0]
	name := args[1]
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

	switch sub {
	case "show":
		path := l.LastIdeasPath
		if path == "" {
			// Fall back to the canonical path if this is the first
			// time we're looking after a daemon-side kick.
			base, _ := ConfigDir()
			path = filepath.Join(base, "loops", name, "ideas.json")
		}
		data, rerr := os.ReadFile(path)
		if rerr != nil {
			fmt.Fprintf(os.Stderr, "no ideas.json yet for loop %q (run `yaver loop ideas kick %s` to generate one)\n", name, name)
			os.Exit(1)
		}
		fmt.Println(string(data))

	case "kick":
		// Reuse loopRun by delegating — ideas mode is just one kick.
		loopRun([]string{name})

	default:
		fmt.Fprintf(os.Stderr, "unknown ideas subcommand %q (want show|kick)\n", sub)
		os.Exit(1)
	}
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
	wd, _ := os.Getwd()
	var state *LoopState
	if existing, ok := loops[spec.Name]; ok {
		existing.Spec = spec
		if wd != "" {
			existing.WorkDir = wd
		}
		state = existing
		fmt.Printf("Updated loop %q (mode=%s, target=%s).\n", spec.Name, spec.Mode, spec.Target)
	} else {
		state = &LoopState{
			ID:        uuid.New().String(),
			Spec:      spec,
			Status:    LoopStatusIdle,
			CreatedAt: time.Now().UTC().Format(time.RFC3339),
			WorkDir:   wd,
		}
		loops[spec.Name] = state
		fmt.Printf("Registered loop %q (mode=%s, target=%s).\n", spec.Name, spec.Mode, spec.Target)
	}
	if err := saveLoops(loops); err != nil {
		fmt.Fprintf(os.Stderr, "save loops: %v\n", err)
		os.Exit(1)
	}

	// Try to register a recurring schedule with the running daemon so
	// the loop ticks on its own. This is best-effort: if the daemon is
	// not running or the endpoint rejects us, we fall through with a
	// warning and the dev can still trigger iterations manually via
	// `yaver loop run <name>`.
	if schedID, serr := scheduleLoopViaDaemon(state); serr == nil && schedID != "" {
		state.ScheduleID = schedID
		loops[spec.Name] = state
		_ = saveLoops(loops)
		fmt.Printf("Scheduled via daemon: schedule_id=%s (next tick via scheduler.go)\n", schedID)
	} else if serr != nil {
		fmt.Fprintf(os.Stderr, "[warn] could not register schedule with daemon: %v\n", serr)
		fmt.Fprintln(os.Stderr, "      run iterations manually with `yaver loop run "+spec.Name+"` for now.")
	}
}

// scheduleLoopViaDaemon POSTs a ScheduledTask to the running daemon so
// the loop ticks automatically on its `schedule.every` / `schedule.cron`
// cadence. Returns the schedule ID on success, or an error that callers
// treat as non-fatal.
func scheduleLoopViaDaemon(state *LoopState) (string, error) {
	s := state.Spec.Schedule

	// Convert `every: 15m` to RepeatInterval (minutes). Scheduler's
	// RepeatInterval is an int of minutes, so anything under 1 minute
	// rounds up to 1 and the dev gets a warning.
	repeatMinutes := 0
	cronExpr := ""
	if s.Every != "" {
		d, err := time.ParseDuration(s.Every)
		if err != nil {
			return "", fmt.Errorf("schedule.every %q is not a valid duration", s.Every)
		}
		repeatMinutes = int(d.Minutes())
		if repeatMinutes < 1 {
			repeatMinutes = 1
		}
	} else if s.Cron != "" {
		cronExpr = s.Cron
	} else {
		// No schedule = on-demand only. Not an error; we just skip
		// registering anything with the daemon.
		return "", nil
	}

	workDir := state.WorkDir
	if workDir == "" {
		workDir, _ = os.Getwd()
	}
	customCmd := fmt.Sprintf("cd %q && yaver loop run %s", workDir, state.Spec.Name)

	body := map[string]interface{}{
		"title":         fmt.Sprintf("yaver-loop:%s", state.Spec.Name),
		"description":   fmt.Sprintf("Auto-dev loop %s (mode=%s, target=%s)", state.Spec.Name, state.Spec.Mode, state.Spec.Target),
		"customCommand": customCmd,
		"runner":        "",
	}
	if repeatMinutes > 0 {
		body["repeatInterval"] = repeatMinutes
	}
	if cronExpr != "" {
		body["cron"] = cronExpr
	}

	resp, err := localAgentRequest("POST", "/schedules", body)
	if err != nil {
		return "", err
	}
	if sched, ok := resp["schedule"].(map[string]interface{}); ok {
		if id, ok := sched["id"].(string); ok {
			return id, nil
		}
	}
	return "", fmt.Errorf("daemon returned no schedule ID")
}

// unscheduleLoopViaDaemon removes a loop's scheduled task from the
// daemon. Best-effort: failures are logged but not fatal.
func unscheduleLoopViaDaemon(state *LoopState) {
	if state.ScheduleID == "" {
		return
	}
	if _, err := localAgentRequest("DELETE", "/schedules/"+state.ScheduleID, nil); err != nil {
		fmt.Fprintf(os.Stderr, "[warn] could not remove schedule %s from daemon: %v\n",
			state.ScheduleID, err)
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
			// chromedp auto-detects Chrome in a handful of well-known
			// locations (macOS .app bundle, Linux PATH, Windows Program
			// Files), so we mirror that logic instead of just probing
			// PATH — otherwise the warning cries wolf on every Mac that
			// has Chrome installed the normal way.
			if !chromeLooksInstalled() {
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

// chromeLooksInstalled checks the usual suspects for a Chrome /
// Chromium install across macOS, Linux, and Windows. Mirrors what
// chromedp's default exec allocator will try at runtime.
func chromeLooksInstalled() bool {
	pathProbes := []string{
		"google-chrome",
		"google-chrome-stable",
		"chromium",
		"chromium-browser",
	}
	for _, p := range pathProbes {
		if _, err := exec.LookPath(p); err == nil {
			return true
		}
	}
	fileProbes := []string{
		"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
		"/Applications/Chromium.app/Contents/MacOS/Chromium",
		"/Applications/Google Chrome Canary.app/Contents/MacOS/Google Chrome Canary",
		"/Applications/Google Chrome Beta.app/Contents/MacOS/Google Chrome Beta",
		"C:\\Program Files\\Google\\Chrome\\Application\\chrome.exe",
		"C:\\Program Files (x86)\\Google\\Chrome\\Application\\chrome.exe",
	}
	for _, p := range fileProbes {
		if _, err := os.Stat(p); err == nil {
			return true
		}
	}
	return false
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
	fs := flag.NewFlagSet("loop run", flag.ExitOnError)
	dryRun := fs.Bool("dry-run", false, "Print what the iteration would do without executing it")
	fs.Parse(args)
	if fs.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "usage: yaver loop run [--dry-run] <name>")
		os.Exit(1)
	}
	name := fs.Arg(0)
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

	if *dryRun {
		phases := loopPhasesForMode(l.Spec.Mode)
		fmt.Println("\n[dry-run] Phases for one iteration:")
		for i, phase := range phases {
			fmt.Printf("  %d. %s\n", i+1, phase)
		}
		return
	}

	ctx := contextBackground()
	post, result, kerr := kickLoopOnce(ctx, name)
	if kerr != nil {
		fmt.Fprintf(os.Stderr, "%v\n", kerr)
		os.Exit(1)
	}
	l = post

	// Human-friendly footer.
	fmt.Println()
	fmt.Printf("iteration status=%s\n", result.Status)
	if result.Summary != "" {
		fmt.Printf("summary:        %s\n", result.Summary)
	}
	if result.PatchCommit != "" {
		fmt.Printf("patch commit:   %s\n", result.PatchCommit)
	}
	if len(result.FilesChanged) > 0 {
		fmt.Printf("files changed:  %d\n", len(result.FilesChanged))
		for _, f := range result.FilesChanged {
			fmt.Printf("  - %s\n", f)
		}
	}
	if result.ReportPath != "" {
		fmt.Printf("report:         %s\n", result.ReportPath)
	}
	if len(result.Kicks) > 0 {
		fmt.Printf("kicks:          %d\n", len(result.Kicks))
		for _, k := range result.Kicks {
			sha := k.CommitSHA
			if len(sha) > 8 {
				sha = sha[:8]
			}
			fmt.Printf("  %d. [%s] %s (%s)\n", k.Index, k.Status, k.Summary, sha)
		}
	}
	if l.Spec.Budget.MaxCommitsPerDay > 0 || l.Spec.Budget.MaxPatchesPerDay > 0 {
		fmt.Printf("budget today:   commits=%d/%d  patches=%d/%d\n",
			l.CommitsToday, l.Spec.Budget.MaxCommitsPerDay,
			l.PatchesToday, l.Spec.Budget.MaxPatchesPerDay)
	}
	if result.Err != "" {
		fmt.Printf("error:          %s\n", result.Err)
	}
	fmt.Printf("duration:       %s\n", result.FinishedAt.Sub(result.StartedAt).Round(time.Millisecond))

	if result.Status != "done" && result.Status != "stuck" && result.Status != "budget_hit" {
		os.Exit(1)
	}
}

// contextBackground is a tiny indirection so tests can swap in a
// cancellable parent context in the future.
func contextBackground() context.Context {
	return context.Background()
}

// kickLoopOnce runs a single iteration of the named loop, updates
// its persisted state, and returns (post-kick loop state, iteration
// result). All side effects — mark running, run phases, reload &
// merge, persist new status, bump budget counters — are contained
// here so both the CLI (`yaver loop run`) and the HTTP handler
// (`POST /autodev/loops/<name>/run`) can share one code path
// without the CLI's printf/os.Exit churn.
//
// Returns a friendly error if the loop doesn't exist, is paused /
// stopped, or has a live STOP file. Any error from
// runLoopIteration itself surfaces on the IterationResult.
func kickLoopOnce(ctx context.Context, name string) (*LoopState, *IterationResult, error) {
	loops, err := loadLoops()
	if err != nil {
		return nil, nil, fmt.Errorf("load loops: %w", err)
	}
	l, ok := loops[name]
	if !ok {
		return nil, nil, fmt.Errorf("loop %q not found", name)
	}
	if l.Status == LoopStatusStopped || l.Status == LoopStatusPaused {
		return l, nil, fmt.Errorf("loop %q is %s — resume it first", name, l.Status)
	}
	if killPath, kerr := loopKillFilePath(name); kerr == nil {
		if _, serr := os.Stat(killPath); serr == nil {
			return l, nil, fmt.Errorf("STOP file exists at %s — remove it to resume", killPath)
		}
	}

	l.Status = LoopStatusRunning
	l.IterationCount++
	l.LastIterationAt = time.Now().UTC().Format(time.RFC3339)
	if err := saveLoops(loops); err != nil {
		return l, nil, fmt.Errorf("save loops: %w", err)
	}

	saveCallback := func(state *LoopState) {
		// Reload + write-back under the mutex so a concurrent CLI
		// `loop stop` doesn't get clobbered by intra-kick progress
		// writes.
		_, _ = withLoops(func(latest map[string]*LoopState) (bool, error) {
			latest[state.Spec.Name] = state
			return true, nil
		})
	}

	result := runLoopIteration(ctx, l, saveCallback)

	// Reload + merge to absorb any sibling writes during the kick,
	// then apply final status + counters.
	loops, _ = loadLoops()
	if cur, ok := loops[name]; ok {
		reloadKeep := cur
		reloadKeep.CommitsToday = l.CommitsToday
		reloadKeep.PatchesToday = l.PatchesToday
		reloadKeep.BudgetDayKey = l.BudgetDayKey
		reloadKeep.LastIdeasPath = l.LastIdeasPath
		l = reloadKeep
	}

	if l.Spec.Mode != LoopModeDevelop && result.PatchCommit != "" {
		l.rollBudgetDay()
		l.CommitsToday++
		l.PatchesToday++
	}
	switch result.Status {
	case "done":
		l.Status = LoopStatusIdle
		l.ConsecutiveStuck = 0
	case "stuck":
		l.Status = LoopStatusStuck
		l.ConsecutiveStuck++
		// Broken kicks reset the release train so it takes N fresh
		// greens before we ship again.
		l.GreenRunSinceLastDeploy = 0
		if l.Spec.Budget.StopAfterConsecutiveStuck > 0 &&
			l.ConsecutiveStuck >= l.Spec.Budget.StopAfterConsecutiveStuck {
			l.Status = LoopStatusPaused
		}
	case "stopped":
		l.Status = LoopStatusStopped
	case "needs_human":
		l.Status = LoopStatusNeedsHuman
		l.GreenRunSinceLastDeploy = 0
	case "budget_hit":
		l.Status = LoopStatusBudgetHit
	case "failed":
		l.Status = LoopStatusStuck
		l.ConsecutiveStuck++
		l.GreenRunSinceLastDeploy = 0
	default:
		l.Status = LoopStatusIdle
	}
	l.LastSummary = result.Summary
	loops[name] = l
	if err := saveLoops(loops); err != nil {
		return l, result, fmt.Errorf("save loops: %w", err)
	}
	return l, result, nil
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
	// Also remove the daemon schedule so the loop does not auto-tick
	// again after being stopped.
	unscheduleLoopViaDaemon(l)
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
	l, ok := loops[name]
	if !ok {
		fmt.Fprintf(os.Stderr, "loop %q not found\n", name)
		os.Exit(1)
	}
	unscheduleLoopViaDaemon(l)
	if err := removeWorktree(l); err != nil {
		fmt.Fprintf(os.Stderr, "warn: prune worktree for %q: %v\n", name, err)
	}
	delete(loops, name)
	if err := saveLoops(loops); err != nil {
		fmt.Fprintf(os.Stderr, "save loops: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Removed loop %q.\n", name)
}
