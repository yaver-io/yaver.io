package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/yaver-io/agent/studio"
	"gopkg.in/yaml.v3"
)

// qa_flow.go — the agentic flow orchestrator (catch-only mode, P2). It drives a
// studio capture surface through each flow with the LLM brain, runs the oracle
// bank after every action, and produces a bug report. Fix mode (P4) wraps this:
// on a caught bug it dispatches a coding-agent job, reloads, and re-verifies.
//
// The loop is the studio drive+assert loop (studio/test.go) plus per-step oracle
// scanning + a report; it lives here (not in studio) so it can reach the
// inference lane (qa_brain) and, later, the coding-agent/build/reload lane.

// qaFlowFile is one yaver-tests/flows/*.flow.yaml document.
type qaFlowFile struct {
	Name         string   `yaml:"name"`
	Goal         string   `yaml:"goal"`
	Package      string   `yaml:"package,omitempty"`
	Expectations []string `yaml:"expectations,omitempty"`
	MaxSteps     int      `yaml:"max_steps,omitempty"`
}

// loadFlows reads every *.flow.yaml in dir into studio.Scenarios (sorted by file
// name for stable ordering).
func loadFlows(dir string) ([]studio.Scenario, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read flows dir: %w", err)
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".flow.yaml") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	var flows []studio.Scenario
	for _, n := range names {
		data, err := os.ReadFile(filepath.Join(dir, n))
		if err != nil {
			return nil, err
		}
		var ff qaFlowFile
		if err := yaml.Unmarshal(data, &ff); err != nil {
			return nil, fmt.Errorf("%s: %w", n, err)
		}
		if strings.TrimSpace(ff.Goal) == "" {
			return nil, fmt.Errorf("%s: flow has no goal", n)
		}
		name := ff.Name
		if name == "" {
			name = strings.TrimSuffix(n, ".flow.yaml")
		}
		flows = append(flows, studio.Scenario{
			Name: name, Goal: ff.Goal, Expectations: ff.Expectations, MaxSteps: ff.MaxSteps,
		})
	}
	return flows, nil
}

// qaFlowResult is the per-flow outcome in the report.
type qaFlowResult struct {
	Name         string                 `json:"name"`
	Goal         string                 `json:"goal"`
	Steps        int                    `json:"steps"`
	Bugs         int                    `json:"bugs"`
	Expectations []studio.AssertVerdict `json:"expectations,omitempty"`
}

// qaReport is the whole run's outcome — the "report card" the UI renders.
type qaReport struct {
	Mode   string         `json:"mode"`
	Flows  []qaFlowResult `json:"flows"`
	Bugs   []studio.Bug   `json:"bugs"`
	Caught int            `json:"caught"`
	Fixed  int            `json:"fixed"`
	Passed bool           `json:"passed"`

	Screenshots [][]byte `json:"-"` // captured frames, indexed by Bug.ShotIdx
}

// qaFlowConfig parameterizes a run. The surface + brain factory + oracles are
// injected so the orchestrator is testable with fakes (no device, no model).
type qaFlowConfig struct {
	Surface   studio.CaptureSurface
	App       studio.App
	APKPath   string
	Flows     []studio.Scenario
	BrainFor  func(studio.Scenario) studio.TestBrain
	Oracles   func() []studio.Oracle // factory: fresh dedup state per drive pass (so a fix verify pass re-detects)
	Mode      string                 // "catch" (default) | "fix"
	Provision bool                   // provision the surface (cold/EnsureReady); false = caller already brought it up
	Teardown  bool                   // tear the surface down at the end (false for a warm base to reuse)
	Log       func(string)

	// Fix mode (P4) seams — nil ⇒ catch-only even if Mode=="fix". Fixer dispatches
	// a coding-agent draft; Reloader rebuilds + reloads the app so the verify pass
	// runs the patched code. Both are injected so the loop is fully testable.
	Fixer    bugFixer
	Reloader appReloader
	MaxFixes int // cap fix attempts per flow (default 3)
}

func (c *qaFlowConfig) logf(format string, a ...any) {
	if c.Log != nil {
		c.Log(fmt.Sprintf(format, a...))
	}
}

// runQAFlows executes every flow in catch-only mode and returns the bug report.
func runQAFlows(ctx context.Context, cfg qaFlowConfig) (*qaReport, error) {
	if cfg.Oracles == nil {
		cfg.Oracles = studio.DefaultOracles
	}
	mode := cfg.Mode
	if mode == "" {
		mode = "catch"
	}
	fixing := mode == "fix" && cfg.Fixer != nil
	if mode == "fix" && cfg.Fixer == nil {
		cfg.logf("fix mode requested but no fixer configured — running catch-only")
		mode = "catch"
	}
	if cfg.MaxFixes <= 0 {
		cfg.MaxFixes = 3
	}
	report := &qaReport{Mode: mode, Passed: true}

	if cfg.Provision {
		if err := cfg.Surface.Provision(ctx); err != nil {
			return nil, fmt.Errorf("provision: %w", err)
		}
	}
	if cfg.Teardown {
		defer cfg.Surface.Teardown(context.WithoutCancel(ctx)) //nolint:errcheck
	}
	if strings.TrimSpace(cfg.APKPath) != "" {
		if err := cfg.Surface.Install(ctx, cfg.APKPath); err != nil {
			return nil, fmt.Errorf("install: %w", err)
		}
	}

	d := cfg.Surface.Driver()
	dumper, _ := d.(studio.Dumper)
	logreader, _ := d.(studio.LogReader)

	_ = d.Launch(ctx, cfg.App)
	sleepCtx(ctx, 3*time.Second)

	rc := &runContext{cfg: cfg, report: report, d: d, dumper: dumper, logreader: logreader}

	for _, flow := range cfg.Flows {
		cfg.logf("flow: %s — %s", flow.Name, flow.Goal)
		bugs, verdicts, steps := rc.driveFlowOnce(ctx, flow)

		if fixing && len(bugs) > 0 {
			bugs, verdicts = rc.fixFlow(ctx, flow, bugs)
		} else {
			markCaught(bugs)
		}

		report.Bugs = append(report.Bugs, bugs...)
		report.Flows = append(report.Flows, qaFlowResult{
			Name: flow.Name, Goal: flow.Goal, Steps: steps,
			Bugs: len(bugs), Expectations: verdicts,
		})
	}

	report.Caught = len(report.Bugs)
	report.Passed = countUnfixed(report.Bugs) == 0
	cfg.logf("done — %d flow(s), %d caught, %d fixed", len(report.Flows), report.Caught, report.Fixed)
	return report, nil
}

// runContext bundles the per-run surface handles so driveFlowOnce / fixFlow can
// be re-invoked (the fix verify pass re-drives a flow).
type runContext struct {
	cfg       qaFlowConfig
	report    *qaReport
	d         studio.Driver
	dumper    studio.Dumper
	logreader studio.LogReader
}

// driveFlowOnce runs a single drive+assert pass: it returns the bugs the oracle
// bank caught and the expectation verdicts, WITHOUT appending to report.Bugs (so
// the fix verify pass can re-run and diff bug sets). Screenshots are appended to
// the shared report for ShotIdx stability.
func (rc *runContext) driveFlowOnce(ctx context.Context, flow studio.Scenario) ([]studio.Bug, []studio.AssertVerdict, int) {
	cfg := rc.cfg
	brain := cfg.BrainFor(flow)
	oracles := cfg.Oracles() // fresh dedup state for this pass
	var bugs []studio.Bug
	history := []string{}
	maxSteps := flow.MaxSteps
	if maxSteps <= 0 {
		maxSteps = 20
	}

	_ = rc.d.Launch(ctx, cfg.App)
	sleepCtx(ctx, 2*time.Second)

	for i := 0; i < maxSteps; i++ {
		shot, _ := rc.d.Screenshot(ctx)
		tree := dumpTree(ctx, rc.dumper)
		logs := readLog(ctx, rc.logreader)
		shotIdx := len(rc.report.Screenshots)
		rc.report.Screenshots = append(rc.report.Screenshots, shot)

		for _, b := range studio.ScanAll(oracles, studio.Frame{
			Step: i, Screenshot: shot, ViewTree: tree, Logcat: logs, ShotIdx: shotIdx,
		}) {
			b.Detail = "[" + flow.Name + "] " + b.Detail
			bugs = append(bugs, b)
			cfg.logf("  ⚠ %s: %s (%s)", b.Severity, b.Title, b.Oracle)
		}

		act, err := brain.NextAction(ctx, studio.Observation{
			Step: i, Screenshot: shot, ViewTree: tree, Goal: flow.Goal, History: history,
		})
		if err != nil {
			cfg.logf("  brain error: %v", err)
			// The agent couldn't decide an action — almost always the model is
			// unreachable / unconfigured. A run that never drove must NOT report
			// PASS, so surface it as a harness bug (once per flow, on no progress).
			if len(history) == 0 {
				bugs = append(bugs, studio.Bug{
					Title:    "App-test agent could not run (navigator/model unavailable)",
					Severity: "high",
					Oracle:   "harness",
					Detail:   "[" + flow.Name + "] " + err.Error(),
					Step:     i, ShotIdx: shotIdx,
				})
			}
			break
		}
		if act.Done {
			cfg.logf("  done: %s", act.Why)
			break
		}
		if err := applyQAStep(ctx, rc.d, act.Step); err != nil {
			cfg.logf("  step %d (%s) failed: %v", i, act.Step.Verb, err)
		}
		history = append(history, describeQAStep(act.Step))
		sleepCtx(ctx, 2*time.Second)
	}

	final, _ := rc.d.Screenshot(ctx)
	finalIdx := len(rc.report.Screenshots)
	rc.report.Screenshots = append(rc.report.Screenshots, final)
	var verdicts []studio.AssertVerdict
	for _, exp := range flow.Expectations {
		v, _ := brain.Assert(ctx, exp, final)
		verdicts = append(verdicts, v)
		if !v.Pass && v.Severity == "fail" {
			bugs = append(bugs, studio.Bug{
				Title:    "Expectation not met: " + exp,
				Severity: "high",
				Oracle:   "expectation",
				Detail:   "[" + flow.Name + "] " + v.Reason,
				Step:     -1, ShotIdx: finalIdx,
			})
		}
	}
	return bugs, verdicts, len(history)
}

func markCaught(bugs []studio.Bug) {
	for i := range bugs {
		if bugs[i].Outcome == "" {
			bugs[i].Outcome = "caught"
		}
	}
}

func countUnfixed(bugs []studio.Bug) int {
	n := 0
	for _, b := range bugs {
		if b.Outcome != "fixed" {
			n++
		}
	}
	return n
}

func dumpTree(ctx context.Context, dumper studio.Dumper) string {
	if dumper == nil {
		return ""
	}
	t, _ := dumper.ViewTree(ctx)
	return t
}

func readLog(ctx context.Context, lr studio.LogReader) string {
	if lr == nil {
		return ""
	}
	l, _ := lr.Logcat(ctx, 200)
	return l
}

// applyQAStep maps a studio.TestStep to the Driver's verbs.
func applyQAStep(ctx context.Context, d studio.Driver, s studio.TestStep) error {
	a := s.Args
	switch s.Verb {
	case "tap":
		return d.Tap(ctx, atoiQA(a["x"]), atoiQA(a["y"]))
	case "taptext":
		return d.TapText(ctx, a["text"])
	case "type":
		return d.Type(ctx, a["text"])
	case "key":
		return d.Key(ctx, a["key"])
	case "back":
		return d.Back(ctx)
	case "home":
		return d.Home(ctx)
	case "wait", "screenshot", "":
		return nil
	default:
		return fmt.Errorf("unknown verb %q", s.Verb)
	}
}

func describeQAStep(s studio.TestStep) string {
	switch s.Verb {
	case "taptext":
		return "tap '" + s.Args["text"] + "'"
	case "type":
		return "type '" + truncQA(s.Args["text"], 24) + "'"
	case "key":
		return "key " + s.Args["key"]
	case "tap":
		return fmt.Sprintf("tap %s,%s", s.Args["x"], s.Args["y"])
	default:
		return s.Verb
	}
}

func atoiQA(s string) int {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return n
		}
		n = n*10 + int(c-'0')
	}
	return n
}
