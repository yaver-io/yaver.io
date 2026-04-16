package main

// autodev_cmd.go — top-level `yaver autodev` and `yaver autotest`
// commands. Thin, opinionated wrappers around the existing
// `yaver loop` subsystem (see loop_cmd.go). The goal is the
// shortest possible command that a half-asleep dev can type:
//
//     yaver autodev sfmg                  # 8h sleep-mode dev loop, then deploy
//     yaver autotest sfmg                 # 8h test-fix-commit-push loop
//     yaver autodev sfmg --hours 1        # 1 hour
//     yaver autodev sfmg --infinite       # until SIGINT
//     yaver autodev sfmg --lite           # respect AI session limits (default)
//     yaver autodev sfmg --heavy          # burst mode, ignore session limits
//     yaver autodev sfmg --prompt "focus on onboarding"
//     yaver autodev status                # list running autodev loops
//     yaver autodev help                  # long-form help
//
// Parallel-safe: autodev uses the loop name `<project>-autodev`
// and autotest uses `<project>-autotest`, separate .loop.yaml
// files, separate state dirs — so the two can run at the same
// time on the same project without fighting.
//
// Multi-kick semantics: one invocation of the command is a
// single long-running process that kicks the AI runner in a
// loop. Each kick does one iteration of loop_cmd.loopRun. The
// process keeps kicking until the deadline, the hard cap, SIGINT,
// or an explicit `yaver loop stop`.
//
// Visibility: every run writes a JSON report into
// ~/.yaver/autodev-reports/<loop-name>.json so the mobile app,
// desktop Electron app, and web dashboard can all read "what did
// this overnight run actually do" over the existing P2P/relay
// HTTP surface (/autodev/reports) — no Convex, no telemetry.
// Each kick records before/after git SHAs so the user can pick
// items from the report and revert them from the mobile app
// via POST /autodev/reports/revert.

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	osexec "os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	autodevSleepHours = "8"
	autodevSleepLoad  = "lite"
	autodevBurstLoad  = "high"
)

// autodevDefaults bundles the policy knobs for a single run.
type autodevDefaults struct {
	hours      string
	load       string
	deploy     string
	prompt     string
	project    string
	runner     string
	branch     string
	target     string
	maxIter    int
	notify     bool
	noAutotest bool
	remained   string
	autoIdeas  int
}

// runAutodev is `yaver autodev <project> [flags]`.
func runAutodev(args []string) { runAutodevOrTest("autodev", args) }

// runAutotest is `yaver autotest <project> [flags]`.
func runAutotest(args []string) { runAutodevOrTest("autotest", args) }

func runAutodevOrTest(kind string, args []string) {
	// Sub-subcommands: status / help.
	if len(args) > 0 {
		switch args[0] {
		case "status", "ls", "list":
			runAutodevStatus(kind, args[1:])
			return
		case "help", "--help", "-h":
			printAutodevHelp(kind)
			return
		}
	}

	fs := flag.NewFlagSet(kind, flag.ExitOnError)
	hours := fs.String("hours", autodevSleepHours, "Duration hours, or 'inf'/'infinite'")
	infinite := fs.Bool("infinite", false, "Run until SIGINT (same as --hours inf)")
	load := fs.String("load", autodevSleepLoad, "lite|high")
	lite := fs.Bool("lite", false, "Shortcut for --load lite (respects session limits, default)")
	heavy := fs.Bool("heavy", false, "Shortcut for --load high (burst mode)")
	deploy := fs.String("deploy", "", "auto|testflight|playstore|convex|vercel|both|none (default: auto — runs every shippable surface the project supports)")
	prompt := fs.String("prompt", "", "Focus prompt, e.g. \"focus on the purchase flow\"")
	target := fs.String("target", "", "web|ios-sim|android-emu (auto-detected)")
	runner := fs.String("runner", "", "Primary AI runner (default: claude-code)")
	engine := fs.String("engine", "", "claude|codex|hybrid — high-level engine selector. 'claude' (default) uses Claude Code end-to-end. 'codex' uses OpenAI Codex CLI (often more headroom on Plus/Pro plans, ~4x fewer tokens). 'hybrid' uses Claude as a planner and a local Ollama model (via aider) as the implementer to cut API spend.")
	hybrid := fs.Bool("hybrid", false, "Shortcut for --engine hybrid")
	codex := fs.Bool("codex", false, "Shortcut for --engine codex")
	model := fs.String("model", "", "Model alias for the runner (claude only): sonnet|opus|haiku, or a full model id like 'claude-opus-4-6'. Sonnet is the cheap default and burns the weekly bucket ~5x slower than Opus.")
	planner := fs.String("planner", "", "Hybrid mode: planner agent (claude|claude:opus|claude:sonnet|codex). When set, forces --engine hybrid. Combine with --implementer for full agent×model layering (e.g. --planner claude:opus --implementer codex = 'Opus plans, Codex implements').")
	implementer := fs.String("implementer", "", "Hybrid mode: implementer agent (claude|claude:sonnet|codex|aider-ollama|aider-ollama:<model>). When set, forces --engine hybrid. Default in hybrid is aider-ollama; pick claude:sonnet for cheap-but-quality, codex for token efficiency, opus for highest stakes.")
	autoIdeas := fs.Int("auto-ideas", 999, "Maximum number of times the loop is allowed to auto-generate a fresh batch of ideas when work runs out. Default 999 = effectively unlimited so an overnight run keeps producing + implementing features until the deadline. 0 = exit the moment the checklist empties (legacy).")
	branch := fs.String("branch", "", "Git branch to ship to (default: main)")
	autoBranch := fs.Bool("auto-branch", false, "Work on a dedicated 'autodev/<loop>-<YYYYMMDD>' branch instead of main. Creates it from main if it doesn't exist. Useful for overnight runs you want to PR-review before merging.")
	harden := fs.String("harden", "", "Run a hardening-focused loop: security|memory|perf|quality|all. Use without --prompt to auto-fill a curated focus, or combine with --prompt to bias your own theme toward hardening.")
	maxIter := fs.Int("max-iterations", 0, "Hard cap on total kicks (0 = no cap)")
	notify := fs.Bool("notify", false, "Notify mobile when run ends")
	showPlan := fs.Bool("plan", false, "Print plan and exit (dry-run)")
	noAutotest := fs.Bool("no-autotest", false, "autodev only: skip the interleaved autotest pass")
	remained := fs.String("remained", "", "Path to a remained.md checklist file — each kick picks the next unchecked item, implements it, checks it off, commits")
	_ = remained
	fs.Usage = func() { printAutodevHelp(kind) }

	positional, flagArgs := splitAutodevArgs(args)
	_ = fs.Parse(flagArgs)

	if *heavy {
		*load = autodevBurstLoad
	}
	if *lite {
		*load = autodevSleepLoad
	}
	if *infinite {
		*hours = "infinite"
	}
	// Engine resolution: --hybrid is a shortcut for --engine hybrid;
	// --engine hybrid forces runner = "hybrid" so phaseThink picks the
	// planner+implementer adapter. Default ('' or 'claude') is a no-op
	// and leaves --runner alone (claude-code default).
	if *hybrid {
		*engine = "hybrid"
	}
	if *codex {
		*engine = "codex"
	}
	// --planner / --implementer compose into hybrid layering. Either
	// flag forces engine=hybrid; spawnHybrid reads the env vars below
	// and overrides HybridSpec.Planner / Implementer / Model so the
	// user gets per-tier agent×model control.
	if *planner != "" || *implementer != "" {
		*engine = "hybrid"
		if *planner != "" {
			os.Setenv("YAVER_HYBRID_PLANNER", *planner)
		}
		if *implementer != "" {
			os.Setenv("YAVER_HYBRID_IMPLEMENTER", *implementer)
		}
	}
	switch strings.ToLower(strings.TrimSpace(*engine)) {
	case "", "claude", "claude-code":
		// keep --runner default
	case "codex":
		*runner = "codex"
	case "hybrid":
		*runner = "hybrid"
	default:
		fmt.Fprintf(os.Stderr, "%s: unknown --engine %q (want claude|codex|hybrid)\n", kind, *engine)
		os.Exit(2)
	}

	wd, _ := os.Getwd()
	project := ""
	if len(positional) > 0 {
		project = positional[0]
	}
	if project == "" {
		project = filepath.Base(wd)
	}

	// --harden resolves to a curated focus prompt for the chosen area.
	// If --prompt is also given, the hardening guidance is prepended
	// so the user's theme stays primary; otherwise hardening is the
	// whole prompt. Unknown values fall through with a warning.
	if hp := autodevHardenPrompt(*harden); hp != "" {
		if strings.TrimSpace(*prompt) == "" {
			*prompt = hp
		} else {
			*prompt = hp + "\n\n" + *prompt
		}
	} else if *harden != "" {
		fmt.Fprintf(os.Stderr, "%s: unknown --harden %q (want security|memory|perf|quality|all)\n", kind, *harden)
	}

	// --auto-branch resolves to a deterministic per-day branch name
	// like "autodev/<project>-autodev-20260416". If --branch wasn't
	// also supplied, this becomes the ship branch. We pre-create it
	// from origin/main if it doesn't exist so the loop's worktree
	// has a base to detach from.
	if *autoBranch && *branch == "" {
		auto := fmt.Sprintf("autodev/%s-%s-%s",
			project, kind, time.Now().Format("20060102"))
		ensureAutodevBranch(wd, auto)
		*branch = auto
	}

	d := autodevDefaults{
		hours:      *hours,
		load:       *load,
		deploy:     *deploy,
		prompt:     *prompt,
		project:    project,
		runner:     *runner,
		branch:     *branch,
		target:     *target,
		maxIter:    *maxIter,
		notify:     *notify,
		noAutotest: *noAutotest,
		remained:   *remained,
		autoIdeas:  *autoIdeas,
	}
	d = applyAutodevDefaults(d, kind, wd)

	p := buildAutodevPlan(kind, d, wd)

	// Tee stdout/stderr to the daemon-hosted log stream — but ONLY
	// in the detached child that actually owns the kick loop. The
	// parent CLI must not tee, otherwise its tail of the same
	// stream feeds its own prints back into the stream and we
	// drown in a feedback loop. The parent's role is just spawn-
	// and-tail; the child publishes.
	streamName := fmt.Sprintf("autodev:%s", p.LoopName)
	if autodevDetachActive() {
		stopStream := teeStdoutToStream(streamName)
		defer stopStream()
	}

	printAutodevPlan(p)
	if *showPlan {
		return
	}
	if err := ensureAutodevSpec(p); err != nil {
		fmt.Fprintf(os.Stderr, "%s: scaffold spec: %v\n", kind, err)
		os.Exit(1)
	}
	if p.IncludeAutotest {
		if err := ensureAutodevRegressionSpec(p); err != nil {
			fmt.Fprintf(os.Stderr, "%s: scaffold regression spec: %v\n", kind, err)
			os.Exit(1)
		}
	}
	if d.prompt != "" {
		// loop_cmd's prompt setter persists in loops.json so
		// subsequent runs without a prompt still remember it.
		loopPrompt([]string{"set", p.LoopName, d.prompt})
	}
	// --model is a runtime hint passed to the runner via env var.
	// spawnClaudeCode picks it up and adds --model <id> to the
	// claude CLI invocation. Aliases (sonnet/opus/haiku) are
	// expanded to the current generation's model id at spawn time.
	if *model != "" {
		os.Setenv("YAVER_CLAUDE_MODEL", *model)
	}

	// "Set and forget" mode: the parent CLI fork-execs itself as a
	// detached, session-leader child that owns the kick loop, then
	// the parent attaches to the child's daemon-published log
	// stream over SSE. Ctrl-C in the parent only detaches the tail
	// — the loop survives terminal close, ssh disconnect, lid close.
	// When YAVER_AUTODEV_DETACHED=1 we ARE the detached child and
	// just run the loop directly.
	if !autodevDetachActive() {
		_, streamName := spawnDetachedAutodev(kind, args, p.LoopName)
		if streamName != "" {
			tailDetachedAutodev(streamName)
			return
		}
		// Fork failed — fall through to the legacy in-process loop
		// so the run still happens, just tied to this terminal.
		fmt.Fprintln(os.Stderr, "[autodev] detach failed — running in foreground")
	}

	runAutodevLoop(p)
	runAutodevDeploy(p)
}

// ensureAutodevRegressionSpec scaffolds the interleaved autotest
// loop used by `yaver autodev`. It's a separate loop name and
// spec from `yaver autotest <project>` so standalone autotest
// runs don't clobber it. The prompt instructs the runner to
// focus on code changed since the last passing test run — a
// smart regression pass rather than a full re-test every kick.
func ensureAutodevRegressionSpec(p autodevPlan) error {
	if fileExists(p.TestSpecPath) {
		return nil
	}
	respect := "false"
	if p.RespectLimits {
		respect = "true"
	}
	prompt := `Smart regression pass. First run: git diff --name-only HEAD~1 HEAD
to find files the autodev loop just changed. Focus end-to-end testing on
the code paths those files touch — launch the app, click the screens and
flows they affect, verify with Playwright / Appium / selenium if a suite
is configured. Only widen to full regression if the changed-files pass is
green and there is budget left. When you find a regression, write a
minimal fix, verify, commit with message "autotest(regression): <short>"
and push. Do not add new features.`
	body := fmt.Sprintf(`name: %s
mode: auto-test
target: %s
schedule:
  every: 60s
  timeout: 20m
playtest:
  enabled: true
  duration: 3m
  fuzzer: heuristic
think:
  runner: %s
  fallback:
    - codex
    - aider
    - ollama:qwen2.5-coder:32b
  max_kicks_per_run: 2
  respect_session_limits: %s
  prompt_inline: |
%s
  require_green:
    - typecheck
    - test
ship:
  branch: %s
  commit_prefix: "autotest(regression):"
budget:
  max_iterations_per_day: %d
test:
  framework: playwright
  chrome: true
  data_entry: true
  regression: true
  focus_changed_files: true
`, p.TestLoopName, p.Target, p.Runner, respect,
		indentAutodev(prompt, "    "), p.Branch, p.MaxIterDay)
	if err := os.WriteFile(p.TestSpecPath, []byte(body), 0o644); err != nil {
		return err
	}
	loopAdd([]string{p.TestSpecPath})
	return nil
}

func splitAutodevArgs(args []string) (positional, flags []string) {
	seenFlag := false
	for _, a := range args {
		if !seenFlag && !strings.HasPrefix(a, "-") {
			positional = append(positional, a)
		} else {
			seenFlag = true
			flags = append(flags, a)
		}
	}
	return
}

func applyAutodevDefaults(d autodevDefaults, kind, wd string) autodevDefaults {
	// Auto-detect a repo-local remained.md checklist. Lets the
	// common case be zero-flag: a dev drops a remained.md into
	// the repo root, runs `yaver autodev sfmg`, and the loop
	// picks it up automatically without --remained. Still
	// overridable by passing --remained explicitly.
	if d.remained == "" {
		for _, candidate := range []string{"remained.md", "REMAINED.md", "TODO.md"} {
			if fileExists(filepath.Join(wd, candidate)) {
				d.remained = candidate
				break
			}
		}
	}
	if d.deploy == "" {
		// "auto" lets runAutodevDeploy detect every shippable surface
		// the project supports (testflight, playstore, convex, vercel)
		// and run them all in sequence. Autotest runs never deploy —
		// they're observation-only.
		if kind == "autotest" {
			d.deploy = "none"
		} else {
			d.deploy = "auto"
		}
	}
	if d.prompt == "" {
		if d.remained != "" {
			d.prompt = remainedPromptTemplate(d.remained)
		} else {
			d.prompt = defaultAutodevPrompt(kind)
		}
	}
	return d
}

// remainedPromptTemplate is the prompt the runner sees when the
// user passed --remained <file>. Each kick picks the top
// unchecked item, does it, marks it done, commits both the code
// and the updated checklist together, and pushes.
func remainedPromptTemplate(file string) string {
	return "You are driving an autodev loop against a markdown checklist at `" + file + "`.\n\n" +
		"For this kick:\n" +
		"  1. Read `" + file + "`. It's a plain markdown file where TODO items are\n" +
		"     lines starting with `- [ ]` (unchecked) or `- [x]` (checked).\n" +
		"  2. If every item is already `- [x]`, say so explicitly and stop —\n" +
		"     the loop will exit on its own next tick.\n" +
		"  3. Otherwise, pick the first unchecked item in file order.\n" +
		"  4. Implement that item completely. Write code + tests. Keep the\n" +
		"     change minimal and coherent — no scope creep from other items.\n" +
		"  5. Run typecheck and tests; they must pass before commit.\n" +
		"  6. Replace the `- [ ]` for the item you just did with `- [x]` in\n" +
		"     `" + file + "` (leave other items alone).\n" +
		"  7. Commit the code change and the updated checklist together,\n" +
		"     message `autodev: <short item title>`, then push.\n" +
		"\n" +
		"Do not skip items. Do not re-order the list. Do not mark items done\n" +
		"that you did not actually implement. If you get stuck on an item,\n" +
		"leave it unchecked, add a short `<!-- blocked: reason -->` comment\n" +
		"next to it, pick the next item, and keep going."
}

func defaultAutodevPrompt(kind string) string {
	if kind == "autotest" {
		return "Run the app end-to-end. Exercise every visible feature. Click through flows, enter reasonable test data, navigate between screens. When you find a bug, crash, layout glitch, dead button, typo, or broken flow, write a minimal fix, verify, commit with message 'autotest: <short description>' and push. Do not add new features."
	}
	return "Develop the next coherent feature or improvement in this repo. Keep changes small and reviewable. Write tests alongside code. Do not break existing functionality. If unclear what to work on, pick a bug, UI polish item, or small improvement."
}

// autodevPlan is the fully-resolved run description — printable,
// storable, and used to seed both the loop spec and the report.
type autodevPlan struct {
	Kind           string
	Project        string
	LoopName       string
	Mode           string
	Hours          string
	Load           string
	Deploy         string
	Target         string
	Prompt         string
	SpecPath       string
	Runner         string
	Branch         string
	MaxIterHardCap int
	Notify         bool
	Deadline       time.Time
	InfiniteRun    bool
	RespectLimits  bool
	TickSleepSec   int
	MaxIterDay     int
	// IncludeAutotest causes an autodev run to interleave one
	// autotest kick after every dev kick against the same project.
	// The autotest kick runs in smart regression mode — focused on
	// files changed by the preceding dev commit.
	IncludeAutotest bool
	// TestLoopName / TestSpecPath are the interleaved autotest
	// loop's identifiers (only populated when IncludeAutotest).
	TestLoopName string
	TestSpecPath string
	// RemainedFile is an optional path to a markdown checklist
	// ("remained.md") that drives the loop — each kick picks the
	// next unchecked item, implements it, checks it off, commits
	// both the code change and the updated file together, and
	// pushes. When all items are checked, the loop exits early.
	// Makes autodev usable as a "dump a TODO list and go to bed"
	// primitive, callable from CLI, HTTP, MCP, or mobile.
	RemainedFile string
	// AutoIdeas caps how many times the loop is allowed to refill an
	// emptied --remained checklist by asking the runner to generate
	// fresh ideas. 0 = old behavior (exit when the list empties).
	AutoIdeas int
}

func buildAutodevPlan(kind string, d autodevDefaults, wd string) autodevPlan {
	loopName := d.project + "-" + kind
	mode := "develop"
	if kind == "autotest" {
		mode = "auto-test"
	}
	target := d.target
	if target == "" {
		if fileExists(filepath.Join(wd, "mobile", "ios")) {
			target = "ios-sim"
		} else {
			target = "web"
		}
	}
	respect := d.load == "lite" || d.load == "low"
	tick := 30
	maxIter := 200
	if respect {
		tick = 300
		maxIter = 20
	}
	infinite := d.hours == "inf" || d.hours == "infinite" || d.hours == ""
	var deadline time.Time
	if !infinite {
		n, err := strconv.Atoi(d.hours)
		if err != nil || n <= 0 {
			n = 8
		}
		deadline = time.Now().Add(time.Duration(n) * time.Hour)
	}
	runner := d.runner
	if runner == "" {
		runner = "claude-code"
	}
	branch := d.branch
	if branch == "" {
		branch = "main"
	}
	includeAutotest := kind == "autodev" && !d.noAutotest
	testLoopName := ""
	testSpecPath := ""
	if includeAutotest {
		testLoopName = d.project + "-autodev-regression"
		testSpecPath = filepath.Join(wd, ".autodev-regression.loop.yaml")
	}
	// Resolve a relative --remained path against the repo's
	// working directory so the runner can find the file from
	// whichever cwd it ends up in.
	remainedFile := d.remained
	if remainedFile != "" && !filepath.IsAbs(remainedFile) {
		remainedFile = filepath.Join(wd, remainedFile)
	}
	return autodevPlan{
		Kind:            kind,
		Project:         d.project,
		LoopName:        loopName,
		Mode:            mode,
		Hours:           d.hours,
		Load:            d.load,
		Deploy:          d.deploy,
		Target:          target,
		Prompt:          d.prompt,
		SpecPath:        filepath.Join(wd, "."+kind+".loop.yaml"),
		Runner:          runner,
		Branch:          branch,
		MaxIterHardCap:  d.maxIter,
		Notify:          d.notify,
		Deadline:        deadline,
		InfiniteRun:     infinite,
		RespectLimits:   respect,
		TickSleepSec:    tick,
		MaxIterDay:      maxIter,
		IncludeAutotest: includeAutotest,
		TestLoopName:    testLoopName,
		TestSpecPath:    testSpecPath,
		RemainedFile:    remainedFile,
		AutoIdeas:       d.autoIdeas,
	}
}

func printAutodevPlan(p autodevPlan) {
	fmt.Println()
	fmt.Printf("yaver %s plan\n", p.Kind)
	fmt.Println("---------------")
	fmt.Printf("  project:       %s\n", p.Project)
	fmt.Printf("  loop name:     %s\n", p.LoopName)
	fmt.Printf("  loop mode:     %s\n", p.Mode)
	fmt.Printf("  spec file:     %s\n", p.SpecPath)
	fmt.Printf("  target:        %s\n", p.Target)
	if p.InfiniteRun {
		fmt.Println("  duration:      infinite (SIGINT to stop)")
	} else {
		fmt.Printf("  duration:      %s hour(s) — until %s\n", p.Hours, p.Deadline.Format("Mon 15:04"))
	}
	if p.RespectLimits {
		fmt.Printf("  load:          %s (respects Claude/Codex session windows — safe while you work; backs off when window > 80%% used)\n", p.Load)
	} else {
		fmt.Printf("  load:          %s (burst — competes with your interactive sessions)\n", p.Load)
	}
	fmt.Printf("  runner:        %s (falls back to codex → aider → ollama)\n", p.Runner)
	fmt.Printf("  branch:        %s\n", p.Branch)
	fmt.Printf("  kick interval: %ds (multi-kick loop)\n", p.TickSleepSec)
	fmt.Printf("  daily budget:  %d iterations (enforced even under --infinite)\n", p.MaxIterDay)
	if p.IncludeAutotest {
		fmt.Printf("  regression:    ON — each successful dev kick is followed by a\n")
		fmt.Printf("                 smart-regression autotest pass on changed files\n")
		fmt.Printf("                 (loop name: %s)\n", p.TestLoopName)
	}
	if p.MaxIterHardCap > 0 {
		fmt.Printf("  hard cap:      %d total kicks\n", p.MaxIterHardCap)
	}
	fmt.Printf("  deploy at end: %s\n", p.Deploy)
	if p.Notify {
		fmt.Println("  notify:        on (mobile notification when done)")
	}
	fmt.Printf("  focus prompt:  %s\n", oneLineAutodev(p.Prompt, 80))
	fmt.Println()
	if p.Kind == "autotest" {
		fmt.Println("  autotest will:")
		fmt.Printf("    1. launch the app in %s\n", p.Target)
		fmt.Println("    2. drive every visible feature end-to-end")
		fmt.Println("    3. enter synthetic data and navigate flows")
		fmt.Println("    4. when a bug / crash / layout glitch is found, ask the AI runner to write a fix")
		fmt.Println("    5. verify, commit ('autotest: <desc>'), push")
		fmt.Println("    6. repeat until the deadline")
	} else {
		fmt.Println("  autodev will:")
		fmt.Println("    1. pick one coherent feature / improvement")
		fmt.Println("    2. ask the AI runner to write it + tests")
		fmt.Println("    3. require typecheck + test green")
		fmt.Println("    4. commit ('autodev: <desc>'), push")
		fmt.Println("    5. repeat until the deadline")
		if p.Deploy != "none" && p.Deploy != "" {
			fmt.Printf("    6. bump version, ship final state to %s\n", p.Deploy)
		}
	}
	fmt.Println()
	fmt.Println("  Watch from anywhere:")
	fmt.Printf("    yaver %s status        (this machine)\n", p.Kind)
	fmt.Println("    yaver loop list          (any reachable machine)")
	fmt.Println("    Yaver mobile app → Auto Dev tab (live status over P2P/relay)")
	fmt.Println("    GET /autodev/reports     (over P2P, never Convex)")
	fmt.Println()
}

func oneLineAutodev(s string, max int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > max {
		return s[:max-1] + "…"
	}
	return s
}

// ensureAutodevSpec scaffolds `.autodev.loop.yaml` or `.autotest.loop.yaml`
// on first run and registers the loop via loopAdd().
func ensureAutodevSpec(p autodevPlan) error {
	if fileExists(p.SpecPath) {
		return nil
	}
	respect := "false"
	if p.RespectLimits {
		respect = "true"
	}
	var body string
	promptIndented := indentAutodev(p.Prompt, "    ")
	if p.Kind == "autotest" {
		body = fmt.Sprintf(`name: %s
mode: auto-test
target: %s
schedule:
  every: 60s
  timeout: 30m
playtest:
  enabled: true
  duration: 5m
  fuzzer: heuristic
think:
  runner: %s
  fallback:
    - codex
    - aider
    - ollama:qwen2.5-coder:32b
  max_kicks_per_run: 3
  respect_session_limits: %s
  prompt_inline: |
%s
  require_green:
    - typecheck
    - test
ship:
  branch: %s
  commit_prefix: "autotest:"
budget:
  max_iterations_per_day: %d
test:
  framework: playwright
  chrome: true
  data_entry: true
`, p.LoopName, p.Target, p.Runner, respect, promptIndented, p.Branch, p.MaxIterDay)
	} else {
		body = fmt.Sprintf(`name: %s
mode: develop
target: %s
schedule:
  every: 30s
  timeout: 30m
playtest:
  enabled: false
think:
  runner: %s
  fallback:
    - codex
    - aider
    - ollama:qwen2.5-coder:32b
  max_kicks_per_run: 5
  respect_session_limits: %s
  prompt_inline: |
%s
  require_green:
    - typecheck
    - test
ship:
  branch: %s
  commit_prefix: "autodev:"
budget:
  max_iterations_per_day: %d
`, p.LoopName, p.Target, p.Runner, respect, promptIndented, p.Branch, p.MaxIterDay)
	}
	if err := os.WriteFile(p.SpecPath, []byte(body), 0o644); err != nil {
		return err
	}
	loopAdd([]string{p.SpecPath})
	return nil
}

func indentAutodev(s, prefix string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for i, l := range lines {
		lines[i] = prefix + l
	}
	return strings.Join(lines, "\n")
}

// runAutodevLoop is the main kick schedule. Calls loopRun once per
// tick, records git deltas into the per-run JSON report, keeps
// going until deadline / hard cap / daily budget / SIGINT.
//
// When p.IncludeAutotest is true (the default for `yaver autodev`),
// each dev kick is immediately followed by a regression autotest
// kick on the same repo — smart-regression mode focused on files
// the dev kick just changed. The alternation means one
// `yaver autodev sfmg` invocation both develops AND tests.
//
// Daily-budget enforcement: even with --infinite, the loop
// refuses to kick past p.MaxIterDay iterations in a rolling 24h
// window so it cannot burn the user's entire monthly Claude /
// Codex / API allotment in a single overnight run. Past the cap,
// the loop sleeps an hour and re-checks.
// ensureAutodevBranch creates the named branch off origin/main (or
// HEAD if origin is unreachable) when it doesn't already exist. We
// don't checkout — the loop's git worktree machinery handles that.
// Best-effort: errors are reported but never abort the run.
func ensureAutodevBranch(wd, name string) {
	exists := func() bool {
		cmd := osexec.Command("git", "-C", wd, "rev-parse", "--verify", "--quiet", "refs/heads/"+name)
		return cmd.Run() == nil
	}
	if exists() {
		fmt.Fprintf(os.Stderr, "[autodev] using existing branch %q\n", name)
		return
	}
	// Try to base off origin/main, falling back to local main, then HEAD.
	for _, base := range []string{"origin/main", "main", "HEAD"} {
		cmd := osexec.Command("git", "-C", wd, "branch", name, base)
		if cmd.Run() == nil {
			fmt.Fprintf(os.Stderr, "[autodev] created branch %q from %s\n", name, base)
			return
		}
	}
	fmt.Fprintf(os.Stderr, "[autodev] WARNING: could not create branch %q — falling back to current\n", name)
}

// autodevForceResume clears any stop marker / paused-or-stopped
// status on the given loop so the next kick can actually run. Best-
// effort: missing loop or write errors are swallowed — we don't want
// an autodev invocation to abort over scheduler housekeeping.
func autodevForceResume(name string) {
	loops, err := loadLoops()
	if err != nil {
		return
	}
	l, ok := loops[name]
	if !ok {
		return
	}
	if killPath, err := loopKillFilePath(name); err == nil {
		_ = os.Remove(killPath)
	}
	if l.Status == LoopStatusStopped || l.Status == LoopStatusPaused {
		l.Status = LoopStatusIdle
		_ = saveLoops(loops)
		fmt.Printf("autodev: resumed loop %q (was %s)\n", name, l.Status)
	}
}

func runAutodevLoop(p autodevPlan) {
	report := newAutodevReport(p)
	defer func() {
		report.EndedAt = time.Now().UTC().Format(time.RFC3339)
		report.save()
	}()
	report.save()

	// A previous run may have left the loop's persisted status as
	// "stopped" / "paused" (or a stale STOP file lying around).
	// `kickLoopOnce` refuses to run in either case, so the autodev
	// command would silently no-op forever. Auto-resume both the
	// dev loop and (if applicable) the autotest companion so the
	// user's "yaver autodev sfmg" always means "make this go".
	autodevForceResume(p.LoopName)
	if p.IncludeAutotest && p.TestLoopName != "" {
		autodevForceResume(p.TestLoopName)
	}

	kickOne := func(iter int, name string, label string) (before, after string) {
		fmt.Printf("%s: %s kick #%d at %s\n", p.Kind, label, iter, time.Now().Format("15:04:05"))
		before = autodevGitHead()
		func() {
			defer func() {
				if r := recover(); r != nil {
					fmt.Fprintf(os.Stderr, "%s: %s kick panicked: %v\n", p.Kind, label, r)
				}
			}()
			loopRun([]string{name})
		}()
		after = autodevGitHead()
		return
	}

	iter := 0
	refills := 0
	dayWindowStart := time.Now()
	kicksToday := 0
	for {
		if !p.InfiniteRun && time.Now().After(p.Deadline) {
			fmt.Printf("%s: deadline reached (%s)\n", p.Kind, p.Deadline.Format(time.RFC3339))
			break
		}
		if p.MaxIterHardCap > 0 && iter >= p.MaxIterHardCap {
			fmt.Printf("%s: hard cap of %d kicks reached\n", p.Kind, p.MaxIterHardCap)
			break
		}
		// Aggressive overnight mode: when the --remained checklist
		// empties, refill it with fresh AI-picked items. Refill or
		// generation failures NEVER end the run — we drop the
		// checklist requirement for this kick and let the open-ended
		// kick prompt do its job. The whole point of autodev is
		// "wake up to commits", so the loop has to survive transient
		// hiccups (rate limits, JSON parsing, model mood) without
		// quitting at 2am.
		if p.RemainedFile != "" && !remainedHasWork(p.RemainedFile) {
			if p.AutoIdeas > 0 && refills < p.AutoIdeas {
				refills++
				fmt.Printf("%s: checklist empty — auto-generating new ideas (refill %d/%d)…\n",
					p.Kind, refills, p.AutoIdeas)
				if err := autodevRefillIdeas(p); err != nil {
					fmt.Printf("%s: idea refill failed (%v) — falling back to open-ended kick\n", p.Kind, err)
					// Fall through to a normal kick. Don't break.
				} else if !remainedHasWork(p.RemainedFile) {
					fmt.Printf("%s: refill produced no parseable items — falling back to open-ended kick\n", p.Kind)
				}
			} else if p.AutoIdeas == 0 {
				fmt.Printf("%s: all items in %s are checked off — done\n", p.Kind, p.RemainedFile)
				break
			} else {
				fmt.Printf("%s: refill budget exhausted (%d) — falling back to open-ended kicks until deadline\n", p.Kind, p.AutoIdeas)
			}
		}
		// Rolling 24h window for the daily-budget cap. Past the
		// cap, sleep until the window rolls instead of exiting —
		// an infinite run should survive a daily pause.
		if time.Since(dayWindowStart) > 24*time.Hour {
			dayWindowStart = time.Now()
			kicksToday = 0
		}
		if p.MaxIterDay > 0 && kicksToday >= p.MaxIterDay {
			fmt.Printf("%s: daily cap of %d kicks hit — sleeping until the 24h window rolls\n", p.Kind, p.MaxIterDay)
			time.Sleep(1 * time.Hour)
			continue
		}

		iter++
		kicksToday++

		// --- dev kick ---
		beforeSHA, afterSHA := kickOne(iter, p.LoopName, p.Kind)
		if beforeSHA != afterSHA && afterSHA != "" {
			report.addKick(iter, beforeSHA, afterSHA)
			report.save()
			// Append a one-line entry to init.md's history so the
			// next session knows what this kick produced.
			wd, _ := os.Getwd()
			autoinitAppendHistory(wd, fmt.Sprintf("[%s kick #%d] %s",
				p.Kind, iter, autodevGitSubject(afterSHA)))
		}

		// --- interleaved regression autotest kick ---
		// Smart-regression: the autotest spec reads git diff
		// --name-only HEAD~1 HEAD in its prompt, so it picks up
		// exactly the files the dev kick just changed.
		if p.IncludeAutotest && p.TestLoopName != "" {
			// Only run the regression if the dev kick actually
			// produced a new commit — no point re-testing an
			// unchanged tree.
			if beforeSHA != afterSHA && afterSHA != "" && kicksToday < p.MaxIterDay {
				kicksToday++
				tBefore, tAfter := kickOne(iter, p.TestLoopName, "autotest(regression)")
				if tBefore != tAfter && tAfter != "" {
					report.addKick(iter, tBefore, tAfter)
					report.save()
				}
			}
		}

		nextAt := time.Now().Add(time.Duration(p.TickSleepSec) * time.Second)
		fmt.Printf("%s: next kick at %s (in %s)\n",
			p.Kind, nextAt.Format("15:04:05"),
			time.Duration(p.TickSleepSec)*time.Second)
		// Cancellable sleep: poll the loop's STOP file every second
		// instead of one big time.Sleep so `yaver loop stop` is felt
		// within ~1s rather than waiting up to 5 min for the current
		// tick to finish.
		stopAt := time.Now().Add(time.Duration(p.TickSleepSec) * time.Second)
		killFile, _ := loopKillFilePath(p.LoopName)
		for time.Now().Before(stopAt) {
			if killFile != "" {
				if _, err := os.Stat(killFile); err == nil {
					fmt.Printf("%s: STOP file detected during sleep — exiting\n", p.Kind)
					goto endLoop
				}
			}
			time.Sleep(1 * time.Second)
		}
	}
endLoop:
	if usd, kicks := RunCostSnapshot(); kicks > 0 {
		fmt.Printf("\n%s: opex summary — $%.4f spent across %d kicks (avg $%.4f/kick)\n",
			p.Kind, usd, kicks, usd/float64(kicks))
	}
	loopStop([]string{p.LoopName})
	if p.TestLoopName != "" {
		loopStop([]string{p.TestLoopName})
	}
}

// --- deploy with version bump --------------------------------------------

func runAutodevDeploy(p autodevPlan) {
	if p.Deploy == "" || p.Deploy == "none" {
		fmt.Printf("%s: skipping deploy\n", p.Kind)
		return
	}
	dep := &AutodevDeploy{Target: p.Deploy, StartedAt: time.Now().UTC().Format(time.RFC3339)}
	defer func() {
		dep.EndedAt = time.Now().UTC().Format(time.RFC3339)
		if r, err := LoadAutodevReport(p.LoopName); err == nil {
			r.Deploy = dep
			r.save()
		}
	}()

	if fileExists("versions.json") && fileExists("scripts/sync-versions.sh") {
		dep.VersionBefore = readMobileVersion()
		bumpPatchVersion()
		_ = osexec.Command("./scripts/sync-versions.sh").Run()
		dep.VersionAfter = readMobileVersion()
		fmt.Printf("%s: version %s → %s\n", p.Kind, dep.VersionBefore, dep.VersionAfter)
	}

	targets := resolveAutodevDeployTargets(p.Deploy)
	if len(targets) == 0 {
		fmt.Fprintf(os.Stderr, "%s: deploy target %q resolved to no shippable surface — skipping\n", p.Kind, p.Deploy)
		dep.Error = "no shippable surface detected for deploy=" + p.Deploy
		return
	}

	var firstErr error
	successes := []string{}
	failures := []string{}
	for _, t := range targets {
		fmt.Printf("%s: deploying → %s…\n", p.Kind, t)
		var err error
		switch t {
		case "testflight":
			err = runShellAutodev("./scripts/deploy-testflight.sh")
		case "playstore":
			err = runShellAutodev("JAVA_HOME=$(/usr/libexec/java_home -v 17) ./scripts/deploy-playstore.sh && PLAY_STORE_KEY_FILE=keys/google-play-service-account.json python3 scripts/upload-playstore.py")
		case "convex":
			// `npx convex deploy` ships the prod backend. --yes
			// suppresses the "are you sure" prompt for unattended runs.
			err = runShellAutodev("npx --yes convex deploy --yes")
		case "vercel":
			// `vercel --prod` deploys to the project's production
			// alias. --yes accepts org/project link prompts on first
			// run. Requires VERCEL_TOKEN env or prior `vercel login`.
			err = runShellAutodev("npx --yes vercel deploy --prod --yes")
		default:
			err = fmt.Errorf("unknown deploy target: %s", t)
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s: %s deploy failed: %v\n", p.Kind, t, err)
			failures = append(failures, t)
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		successes = append(successes, t)
	}

	if len(successes) > 0 {
		dep.OK = true
	}
	if len(failures) > 0 {
		dep.Error = fmt.Sprintf("failed: %s", strings.Join(failures, ", "))
	}
	fmt.Printf("%s: deploy summary — ok=%v failed=%v\n", p.Kind, successes, failures)
	_ = firstErr
}

// resolveAutodevDeployTargets expands a high-level deploy spec into
// the concrete surfaces to ship to. "auto" probes the project for
// every supported flavour (mobile + web + backend) and returns
// whichever exists; "both" preserves legacy testflight+playstore;
// "none" / "" returns nothing (caller already short-circuits).
func resolveAutodevDeployTargets(spec string) []string {
	switch spec {
	case "", "none":
		return nil
	case "both":
		return []string{"testflight", "playstore"}
	case "testflight", "playstore", "convex", "vercel":
		return []string{spec}
	case "auto":
		var out []string
		if fileExists("scripts/deploy-testflight.sh") {
			out = append(out, "testflight")
		}
		if fileExists("scripts/deploy-playstore.sh") {
			out = append(out, "playstore")
		}
		// Convex: convex/ dir OR `convex` in package.json deps
		if fileExists("convex") || pkgJSONHasDep("convex") {
			out = append(out, "convex")
		}
		// Vercel: vercel.json, .vercel dir, or a Next.js project
		if fileExists("vercel.json") || fileExists(".vercel") || fileExists("next.config.js") || fileExists("next.config.mjs") || fileExists("next.config.ts") {
			out = append(out, "vercel")
		}
		return out
	default:
		// Unknown spec — pass through as a single target, log will
		// surface the error from the run step.
		return []string{spec}
	}
}

// pkgJSONHasDep returns true if the cwd's package.json lists `name`
// in dependencies or devDependencies. Best-effort; missing or
// malformed package.json returns false.
func pkgJSONHasDep(name string) bool {
	data, err := os.ReadFile("package.json")
	if err != nil {
		return false
	}
	var pkg struct {
		Dependencies    map[string]string `json:"dependencies"`
		DevDependencies map[string]string `json:"devDependencies"`
	}
	if err := json.Unmarshal(data, &pkg); err != nil {
		return false
	}
	if _, ok := pkg.Dependencies[name]; ok {
		return true
	}
	if _, ok := pkg.DevDependencies[name]; ok {
		return true
	}
	return false
}

func runShellAutodev(cmd string) error {
	c := osexec.Command("bash", "-c", cmd)
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	c.Stdin = os.Stdin
	return c.Run()
}

func readMobileVersion() string {
	data, err := os.ReadFile("versions.json")
	if err != nil {
		return ""
	}
	var v map[string]string
	if err := json.Unmarshal(data, &v); err != nil {
		return ""
	}
	return v["mobile"]
}

func bumpPatchVersion() {
	data, err := os.ReadFile("versions.json")
	if err != nil {
		return
	}
	var v map[string]string
	if err := json.Unmarshal(data, &v); err != nil {
		return
	}
	cur, ok := v["mobile"]
	if !ok || cur == "" {
		return
	}
	parts := strings.Split(cur, ".")
	if len(parts) != 3 {
		return
	}
	patch, err := strconv.Atoi(parts[2])
	if err != nil {
		return
	}
	parts[2] = strconv.Itoa(patch + 1)
	v["mobile"] = strings.Join(parts, ".")
	out, _ := json.MarshalIndent(v, "", "  ")
	_ = os.WriteFile("versions.json", append(out, '\n'), 0o644)
}

// --- per-run report ------------------------------------------------------

type AutodevReport struct {
	LoopName  string          `json:"loop_name"`
	Kind      string          `json:"kind"`
	Project   string          `json:"project"`
	WorkDir   string          `json:"work_dir"`
	StartedAt string          `json:"started_at"`
	EndedAt   string          `json:"ended_at,omitempty"`
	Plan      autodevPlanJSON `json:"plan"`
	Kicks     []AutodevKick   `json:"kicks"`
	Deploy    *AutodevDeploy  `json:"deploy,omitempty"`
}

type autodevPlanJSON struct {
	Hours        string `json:"hours"`
	Load         string `json:"load"`
	Runner       string `json:"runner"`
	Branch       string `json:"branch"`
	Target       string `json:"target"`
	Prompt       string `json:"prompt"`
	DeployTarget string `json:"deploy_target"`
}

type AutodevKick struct {
	N         int    `json:"n"`
	At        string `json:"at"`
	BeforeSHA string `json:"before_sha"`
	AfterSHA  string `json:"after_sha"`
	Message   string `json:"message"`
}

type AutodevDeploy struct {
	Target        string `json:"target"`
	StartedAt     string `json:"started_at"`
	EndedAt       string `json:"ended_at"`
	OK            bool   `json:"ok"`
	Error         string `json:"error,omitempty"`
	VersionBefore string `json:"version_before,omitempty"`
	VersionAfter  string `json:"version_after,omitempty"`
}

func newAutodevReport(p autodevPlan) *AutodevReport {
	wd, _ := os.Getwd()
	return &AutodevReport{
		LoopName:  p.LoopName,
		Kind:      p.Kind,
		Project:   p.Project,
		WorkDir:   wd,
		StartedAt: time.Now().UTC().Format(time.RFC3339),
		Plan: autodevPlanJSON{
			Hours:        p.Hours,
			Load:         p.Load,
			Runner:       p.Runner,
			Branch:       p.Branch,
			Target:       p.Target,
			Prompt:       p.Prompt,
			DeployTarget: p.Deploy,
		},
		Kicks: []AutodevKick{},
	}
}

func (r *AutodevReport) addKick(n int, before, after string) {
	r.Kicks = append(r.Kicks, AutodevKick{
		N:         n,
		At:        time.Now().UTC().Format(time.RFC3339),
		BeforeSHA: before,
		AfterSHA:  after,
		Message:   autodevGitSubject(after),
	})
}

func autodevReportsDir() string {
	home, _ := os.UserHomeDir()
	dir := filepath.Join(home, ".yaver", "autodev-reports")
	_ = os.MkdirAll(dir, 0o755)
	return dir
}

func (r *AutodevReport) save() {
	path := filepath.Join(autodevReportsDir(), r.LoopName+".json")
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(path, data, 0o644)
}

// LoadAutodevReport reads one report back from disk for the HTTP handlers.
func LoadAutodevReport(loopName string) (*AutodevReport, error) {
	path := filepath.Join(autodevReportsDir(), loopName+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var r AutodevReport
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

// ListAutodevReports is used by the /autodev/reports HTTP handler.
func ListAutodevReports() ([]*AutodevReport, error) {
	dir := autodevReportsDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	out := []*AutodevReport{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		name := strings.TrimSuffix(e.Name(), ".json")
		if r, err := LoadAutodevReport(name); err == nil {
			out = append(out, r)
		}
	}
	return out, nil
}

// RevertAutodevCommits runs `git revert --no-edit <sha>` for each
// SHA in the list against the loop's workdir, then pushes. Used by
// the "select items to revert" flow from mobile / web / desktop.
func RevertAutodevCommits(loopName string, shas []string) error {
	report, err := LoadAutodevReport(loopName)
	if err != nil {
		return fmt.Errorf("load report: %w", err)
	}
	if report.WorkDir == "" {
		return fmt.Errorf("report has no workdir")
	}
	for _, sha := range shas {
		cmd := osexec.Command("git", "revert", "--no-edit", sha)
		cmd.Dir = report.WorkDir
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("revert %s: %v — %s", sha, err, string(out))
		}
	}
	pushCmd := osexec.Command("git", "push")
	pushCmd.Dir = report.WorkDir
	_ = pushCmd.Run()
	return nil
}

// --- status + help -------------------------------------------------------

func runAutodevStatus(kind string, _ []string) {
	loops, err := loadLoops()
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s status: %v\n", kind, err)
		os.Exit(1)
	}
	suffix := "-" + kind
	matched := 0
	fmt.Printf("%s loops on this machine:\n", kind)
	fmt.Println("---------------------------")
	for name, l := range loops {
		if !strings.HasSuffix(name, suffix) {
			continue
		}
		matched++
		fmt.Printf("  %s\n", name)
		fmt.Printf("    status:   %s\n", l.Status)
		fmt.Printf("    mode:     %s\n", l.Spec.Mode)
		fmt.Printf("    work_dir: %s\n", l.WorkDir)
		if l.PromptInline != "" {
			fmt.Printf("    prompt:   %s\n", oneLineAutodev(l.PromptInline, 80))
		}
		fmt.Println()
	}
	if matched == 0 {
		fmt.Printf("  (none — start one with `yaver %s <project>`)\n\n", kind)
	}
	fmt.Println("Same list is visible from:")
	fmt.Println("  - Yaver mobile app → Auto Dev tab")
	fmt.Println("  - any machine that can reach this agent: yaver loop list")
	fmt.Println("  - agent HTTP:                             GET /autodev/loops  (and /autodev/reports)")
}

func printAutodevHelp(kind string) {
	desc := "Runs a develop-mode AI loop that writes code + tests against this repo on a schedule, optionally shipping a build at the end of the run."
	kickDesc := "picking work, writing code + tests, and committing"
	if kind == "autotest" {
		desc = "Runs the app end-to-end (Chrome / iOS sim / Android emu), finds bugs by exercising every feature, AI-fixes them, commits + pushes."
		kickDesc = "exercising the app, filing bugs, fixing them, and committing"
	}
	fmt.Printf(`yaver %s — overnight / scheduled AI loop for a single repo

Usage:
  yaver %s <project> [flags]
  yaver %s status              list running %s loops on this machine
  yaver %s help                print this help

What it does:
  %s

Defaults (zero-config — "yaver %s sfmg" just works):
  --hours 8             nightly sweet spot. Use --infinite for forever.
  --lite                respects Claude/Codex session windows (safe while you work).
                        Use --heavy to override.
  runner=claude-code    falls back to codex → aider → ollama:qwen2.5-coder:32b.
  branch=main           commits land on main unless you pass --branch.
  deploy=auto           mobile repos default to testflight at the end.

Flags:
  --hours N            duration in hours; accepts "inf"/"infinite".
  --infinite           run until SIGINT. Same as --hours inf.
  --lite               sleep-mode (default). Respects AI session limits.
  --heavy              burst-mode. Ignores limits.
  --load lite|high     explicit form of --lite / --heavy.
  --prompt "..."       focus prompt (overrides stored one).
  --runner NAME        override primary AI runner.
  --branch NAME        override ship branch.
  --max-iterations N   hard cap on kicks.
  --deploy WHAT        testflight | playstore | both | none.
  --target WHERE       web | ios-sim | android-emu (auto-detected).
  --notify             mobile notification when done.
  --plan               dry-run: print plan and exit.

Multi-kick semantics:
  One invocation is a single long-running process. Each kick does one
  iteration of %s.
  The process keeps kicking until deadline, hard cap, SIGINT, or
  `+"`"+`yaver loop stop <project>-%s`+"`"+` from another terminal.

Watch from anywhere (P2P / relay, never Convex):
  On the dev machine:     yaver %s status
  From another PC:        yaver devices ; yaver connect <device> ; yaver loop list
  From the mobile app:    Auto Dev tab — live status + prompt + kicks
  Over HTTP:              GET /autodev/loops     (one-line status per loop)
                          GET /autodev/reports   (per-run report)
                          POST /autodev/reports/revert {name, commit_shas: [...]}

Parallel use (autodev + autotest at the same time):
  yaver autodev sfmg --hours 8 &
  yaver autotest sfmg --hours 8 &

Examples:
  yaver %s sfmg
  yaver %s sfmg --infinite --heavy
  yaver %s sfmg --hours 1 --prompt "focus on the purchase flow"
  yaver %s sfmg --runner codex --hours 2
  yaver %s sfmg --deploy both
  yaver %s sfmg --plan

SSH (detached so you can disconnect):
  ssh macmini 'cd ~/Workspace/sfmg && nohup yaver %s sfmg >%s.log 2>&1 &'
`,
		kind,                               // header
		kind, kind, kind, kind,             // Usage block (4)
		desc, kind,                         // what-it-does + defaults
		kickDesc, kind,                     // multi-kick block
		kind,                               // status command
		kind, kind, kind, kind, kind, kind, // Examples (6)
		kind, kind,                         // SSH
	)
}

// NOTE: fileExists() lives in classify.go — reused here.

// remainedHasWork returns true iff the remained.md file contains
// at least one unchecked line. Missing / unreadable file counts as
// "has work" so a transient read error doesn't silently end the run.
func remainedHasWork(path string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return true
	}
	for _, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "- [ ]") || strings.HasPrefix(trimmed, "* [ ]") {
			return true
		}
	}
	return false
}

// autodevGitHead reads HEAD in the current working directory.
// Separate from loop_exec's gitHeadSHA(workDir) which takes a
// directory argument — we want the binary's own cwd since the
// autodev command is invoked from inside the target repo.
func autodevGitHead() string {
	out, err := osexec.Command("git", "rev-parse", "HEAD").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func autodevGitSubject(sha string) string {
	if sha == "" {
		return ""
	}
	out, err := osexec.Command("git", "log", "--format=%s", "-n1", sha).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
