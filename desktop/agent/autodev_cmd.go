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
	hours   string
	load    string
	deploy  string
	prompt  string
	project string
	runner  string
	branch  string
	target  string
	maxIter int
	notify  bool
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
	deploy := fs.String("deploy", "", "testflight|playstore|both|none (default: auto-detected)")
	prompt := fs.String("prompt", "", "Focus prompt, e.g. \"focus on the purchase flow\"")
	target := fs.String("target", "", "web|ios-sim|android-emu (auto-detected)")
	runner := fs.String("runner", "", "Primary AI runner (default: claude-code)")
	branch := fs.String("branch", "", "Git branch to ship to (default: main)")
	maxIter := fs.Int("max-iterations", 0, "Hard cap on total kicks (0 = no cap)")
	notify := fs.Bool("notify", false, "Notify mobile when run ends")
	showPlan := fs.Bool("plan", false, "Print plan and exit (dry-run)")
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

	wd, _ := os.Getwd()
	project := ""
	if len(positional) > 0 {
		project = positional[0]
	}
	if project == "" {
		project = filepath.Base(wd)
	}

	d := autodevDefaults{
		hours:   *hours,
		load:    *load,
		deploy:  *deploy,
		prompt:  *prompt,
		project: project,
		runner:  *runner,
		branch:  *branch,
		target:  *target,
		maxIter: *maxIter,
		notify:  *notify,
	}
	d = applyAutodevDefaults(d, kind, wd)

	p := buildAutodevPlan(kind, d, wd)
	printAutodevPlan(p)
	if *showPlan {
		return
	}
	if err := ensureAutodevSpec(p); err != nil {
		fmt.Fprintf(os.Stderr, "%s: scaffold spec: %v\n", kind, err)
		os.Exit(1)
	}
	if d.prompt != "" {
		// loop_cmd's prompt setter persists in loops.json so
		// subsequent runs without a prompt still remember it.
		loopPrompt([]string{"set", p.LoopName, d.prompt})
	}
	runAutodevLoop(p)
	runAutodevDeploy(p)
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
	if d.deploy == "" {
		// Mobile repos default to testflight. Go CLI repos (yaver.io)
		// detected via desktop/agent/main.go presence default to none.
		hasTestFlight := fileExists(filepath.Join(wd, "scripts", "deploy-testflight.sh"))
		hasMobileDir := fileExists(filepath.Join(wd, "mobile"))
		isMobileRepo := hasTestFlight || hasMobileDir
		if fileExists(filepath.Join(wd, "desktop", "agent", "main.go")) {
			isMobileRepo = false
		}
		switch {
		case kind == "autotest":
			d.deploy = "none"
		case isMobileRepo:
			d.deploy = "testflight"
		default:
			d.deploy = "none"
		}
	}
	if d.prompt == "" {
		d.prompt = defaultAutodevPrompt(kind)
	}
	return d
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
	return autodevPlan{
		Kind:           kind,
		Project:        d.project,
		LoopName:       loopName,
		Mode:           mode,
		Hours:          d.hours,
		Load:           d.load,
		Deploy:         d.deploy,
		Target:         target,
		Prompt:         d.prompt,
		SpecPath:       filepath.Join(wd, "."+kind+".loop.yaml"),
		Runner:         runner,
		Branch:         branch,
		MaxIterHardCap: d.maxIter,
		Notify:         d.notify,
		Deadline:       deadline,
		InfiniteRun:    infinite,
		RespectLimits:  respect,
		TickSleepSec:   tick,
		MaxIterDay:     maxIter,
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
		fmt.Printf("  load:          %s (respects Claude/Codex session windows — safe while you work)\n", p.Load)
	} else {
		fmt.Printf("  load:          %s (burst — competes with your interactive sessions)\n", p.Load)
	}
	fmt.Printf("  runner:        %s (falls back to codex → aider → ollama)\n", p.Runner)
	fmt.Printf("  branch:        %s\n", p.Branch)
	fmt.Printf("  kick interval: %ds (multi-kick loop)\n", p.TickSleepSec)
	fmt.Printf("  daily budget:  %d iterations\n", p.MaxIterDay)
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
// going until deadline / hard cap / SIGINT.
func runAutodevLoop(p autodevPlan) {
	report := newAutodevReport(p)
	defer func() {
		report.EndedAt = time.Now().UTC().Format(time.RFC3339)
		report.save()
	}()
	report.save()

	iter := 0
	for {
		if !p.InfiniteRun && time.Now().After(p.Deadline) {
			fmt.Printf("%s: deadline reached (%s)\n", p.Kind, p.Deadline.Format(time.RFC3339))
			break
		}
		if p.MaxIterHardCap > 0 && iter >= p.MaxIterHardCap {
			fmt.Printf("%s: hard cap of %d kicks reached\n", p.Kind, p.MaxIterHardCap)
			break
		}
		iter++
		fmt.Printf("%s: kick #%d at %s\n", p.Kind, iter, time.Now().Format("15:04:05"))
		beforeSHA := autodevGitHead()
		func() {
			defer func() {
				if r := recover(); r != nil {
					fmt.Fprintf(os.Stderr, "%s: kick panicked: %v\n", p.Kind, r)
				}
			}()
			loopRun([]string{p.LoopName})
		}()
		afterSHA := autodevGitHead()
		if beforeSHA != afterSHA && afterSHA != "" {
			report.addKick(iter, beforeSHA, afterSHA)
			report.save()
		}
		time.Sleep(time.Duration(p.TickSleepSec) * time.Second)
	}
	loopStop([]string{p.LoopName})
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

	var err error
	switch p.Deploy {
	case "testflight":
		fmt.Printf("%s: deploying to TestFlight…\n", p.Kind)
		err = runShellAutodev("./scripts/deploy-testflight.sh")
	case "playstore":
		fmt.Printf("%s: deploying to Google Play internal…\n", p.Kind)
		err = runShellAutodev("JAVA_HOME=$(/usr/libexec/java_home -v 17) ./scripts/deploy-playstore.sh && PLAY_STORE_KEY_FILE=keys/google-play-service-account.json python3 scripts/upload-playstore.py")
	case "both":
		fmt.Printf("%s: deploying to TestFlight + Google Play internal…\n", p.Kind)
		err = runShellAutodev("./scripts/deploy-testflight.sh && JAVA_HOME=$(/usr/libexec/java_home -v 17) ./scripts/deploy-playstore.sh && PLAY_STORE_KEY_FILE=keys/google-play-service-account.json python3 scripts/upload-playstore.py")
	default:
		fmt.Fprintf(os.Stderr, "%s: unknown deploy target %q\n", p.Kind, p.Deploy)
		dep.Error = "unknown deploy target: " + p.Deploy
		return
	}
	if err != nil {
		dep.Error = err.Error()
		return
	}
	dep.OK = true
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
