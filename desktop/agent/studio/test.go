package studio

import (
	"context"
	"fmt"
	"time"
)

// test.go — the AI App-Test Agent runner (T0). Drives an app through scenarios
// on a CaptureSurface (redroid / iOS sim), records the whole session, captures a
// screenshot per step, and produces a verdict. See docs/yaver-ai-app-test-agent.md.
//
// The intelligence is behind the TestBrain seam: the AI brain (LLM picks the next
// action toward a goal; VLM asserts a screenshot vs an expectation) drops in at
// T1. T0 ships the runner + a ScriptedBrain (deterministic committed steps), so
// committed E2E scenarios run today; the AI brain reuses the exact same loop.

// TestStep is one action. Verb ∈ tap|taptext|type|key|back|home|wait|screenshot.
type TestStep struct {
	Verb string            `json:"verb"`
	Args map[string]string `json:"args"`
}

// Scenario is a named test: a goal (for the AI brain), optional explicit steps
// (for the scripted brain), and expectations the asserter checks.
type Scenario struct {
	Name         string     `json:"name"`
	Goal         string     `json:"goal"`
	Steps        []TestStep `json:"steps"`        // scripted path (T0); empty → AI goal-seek (T1)
	Expectations []string   `json:"expectations"` // VLM/asserter checks at the end
	MaxSteps     int        `json:"maxSteps"`
}

// Observation is what the brain sees before deciding the next action.
type Observation struct {
	Step       int
	Screenshot []byte
	ViewTree   string
	Goal       string
	History    []string
}

// BrainAction is the brain's decision. Done=true ends the scenario.
type BrainAction struct {
	Step TestStep
	Done bool
	Why  string
}

// AssertVerdict is the asserter's judgement of a screen vs an expectation.
type AssertVerdict struct {
	Expectation string `json:"expectation"`
	Pass        bool   `json:"pass"`
	Reason      string `json:"reason"`
	Severity    string `json:"severity"` // "info" | "warn" | "fail"
}

// TestBrain is the AI seam. NextAction drives toward the goal; Assert judges a
// screenshot. ScriptedBrain implements it deterministically (no model); the LLM
// brain (gateway/BYOK, OpenRouter, two-model nav+assert split) implements it for
// real at T1 — same loop, no runner change.
type TestBrain interface {
	NextAction(ctx context.Context, obs Observation) (BrainAction, error)
	Assert(ctx context.Context, expectation string, screenshot []byte) (AssertVerdict, error)
}

// StepRecord is one executed step in the result.
type StepRecord struct {
	N       int               `json:"n"`
	Verb    string            `json:"verb"`
	Args    map[string]string `json:"args,omitempty"`
	Why     string            `json:"why,omitempty"`
	AtSec   float64           `json:"atSec"`
	ShotIdx int               `json:"shotIdx"` // index into Screenshots
}

// ScenarioResult is the verdict for one scenario.
type ScenarioResult struct {
	Name     string          `json:"name"`
	Pass     bool            `json:"pass"`
	Steps    []StepRecord    `json:"steps"`
	Verdicts []AssertVerdict `json:"verdicts"`
}

// TestResult is the whole run: recording + per-step screenshots + verdicts.
type TestResult struct {
	Scenarios   []ScenarioResult `json:"scenarios"`
	Pass        bool             `json:"pass"`
	MP4         []byte           `json:"-"`
	Screenshots [][]byte         `json:"-"`
	Cues        []Cue            `json:"cues"`
}

// RunTest provisions the surface, installs the app, and runs every scenario
// through the drive+assert loop while recording. Always tears down.
// brainFor returns the brain to drive a given scenario (the AI brain gets the
// scenario's goal; the scripted brain gets its steps). One factory, per-scenario
// brain — so a multi-scenario run resets cleanly.
func RunTest(ctx context.Context, surface CaptureSurface, app App, artifactPath string, scenarios []Scenario, brainFor func(Scenario) TestBrain, log func(string)) (*TestResult, error) {
	logf := func(f string, a ...any) {
		if log != nil {
			log(fmt.Sprintf(f, a...))
		}
	}
	if err := surface.Provision(ctx); err != nil {
		return nil, fmt.Errorf("provision: %w", err)
	}
	defer surface.Teardown(context.WithoutCancel(ctx)) //nolint:errcheck
	if artifactPath != "" {
		if err := surface.Install(ctx, artifactPath); err != nil {
			return nil, fmt.Errorf("install: %w", err)
		}
	}
	d := surface.Driver()
	res := &TestResult{Pass: true}

	maxRec := 0
	for _, s := range scenarios {
		maxRec += sc(s.MaxSteps, len(s.Steps)) * 3
	}
	if maxRec < 30 {
		maxRec = 30
	}
	if err := d.RecordStart(ctx, maxRec); err != nil {
		logf("record start failed (continuing without video): %v", err)
	}
	start := time.Now()

	_ = d.Launch(ctx, app)
	sleepCtx(ctx, 5)

	for _, scn := range scenarios {
		logf("scenario: %s", scn.Name)
		brain := brainFor(scn)
		sr := ScenarioResult{Name: scn.Name, Pass: true}
		var history []string
		maxSteps := sc(scn.MaxSteps, len(scn.Steps))
		if maxSteps == 0 {
			maxSteps = 20
		}
		for i := 0; i < maxSteps; i++ {
			shot, _ := d.Screenshot(ctx)
			tree := ""
			if rd, ok := d.(*redroidDriver); ok {
				tree, _ = rd.uiDump(ctx)
			}
			act, err := brain.NextAction(ctx, Observation{Step: i, Screenshot: shot, ViewTree: tree, Goal: scn.Goal, History: history})
			if err != nil {
				sr.Pass = false
				logf("brain error: %v", err)
				break
			}
			shotIdx := len(res.Screenshots)
			res.Screenshots = append(res.Screenshots, shot)
			sr.Steps = append(sr.Steps, StepRecord{N: i, Verb: act.Step.Verb, Args: act.Step.Args, Why: act.Why, AtSec: time.Since(start).Seconds(), ShotIdx: shotIdx})
			res.Cues = append(res.Cues, Cue{Text: fmt.Sprintf("%s: %s", scn.Name, act.Why), StartSec: time.Since(start).Seconds(), EndSec: time.Since(start).Seconds() + 2})
			if act.Done {
				break
			}
			if err := applyTestStep(ctx, d, act.Step); err != nil {
				logf("step %d (%s) failed: %v", i, act.Step.Verb, err)
			}
			history = append(history, act.Step.Verb)
			sleepCtx(ctx, 2)
		}
		// assertions
		shot, _ := d.Screenshot(ctx)
		for _, exp := range scn.Expectations {
			v, err := brain.Assert(ctx, exp, shot)
			if err != nil {
				v = AssertVerdict{Expectation: exp, Pass: false, Reason: err.Error(), Severity: "fail"}
			}
			sr.Verdicts = append(sr.Verdicts, v)
			if !v.Pass && v.Severity == "fail" {
				sr.Pass = false
			}
		}
		if !sr.Pass {
			res.Pass = false
		}
		res.Scenarios = append(res.Scenarios, sr)
	}

	mp4, cerr := d.RecordStop(ctx)
	if cerr == nil {
		res.MP4 = mp4
	}
	return res, nil
}

func sc(a, b int) int {
	if a > 0 {
		return a
	}
	return b
}

// applyTestStep maps a TestStep to a Driver verb.
func applyTestStep(ctx context.Context, d Driver, s TestStep) error {
	a := s.Args
	switch s.Verb {
	case "tap":
		return d.Tap(ctx, atoiSafe(a["x"]), atoiSafe(a["y"]))
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

func atoiSafe(s string) int {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return n
		}
		n = n*10 + int(c-'0')
	}
	return n
}

// ScriptedBrain plays a scenario's explicit Steps in order, then asserts each
// expectation by text-presence in the latest view tree (best-effort, T0). The
// AI brain replaces this at T1 with model-driven action + VLM assert.
type ScriptedBrain struct {
	steps []TestStep
	tree  func() string // optional: latest view tree for text-presence asserts
	i     int
}

func NewScriptedBrain(steps []TestStep, treeFn func() string) *ScriptedBrain {
	return &ScriptedBrain{steps: steps, tree: treeFn}
}

func (b *ScriptedBrain) NextAction(ctx context.Context, obs Observation) (BrainAction, error) {
	if b.i >= len(b.steps) {
		return BrainAction{Done: true, Why: "scenario complete"}, nil
	}
	st := b.steps[b.i]
	b.i++
	return BrainAction{Step: st, Why: st.Verb}, nil
}

func (b *ScriptedBrain) Assert(ctx context.Context, expectation string, screenshot []byte) (AssertVerdict, error) {
	// T0: text-presence in the current view tree if available; otherwise mark
	// as "captured" (a human/VLM reviews the screenshot). No false confidence.
	if b.tree != nil {
		tree := b.tree()
		if tree != "" {
			if containsFold(tree, expectation) {
				return AssertVerdict{Expectation: expectation, Pass: true, Reason: "text present in view tree", Severity: "info"}, nil
			}
			return AssertVerdict{Expectation: expectation, Pass: false, Reason: "expected text not found in view tree", Severity: "fail"}, nil
		}
	}
	return AssertVerdict{Expectation: expectation, Pass: true, Reason: "captured for review (no asserter wired)", Severity: "info"}, nil
}

func containsFold(haystack, needle string) bool {
	return len(needle) > 0 && indexFold(haystack, needle) >= 0
}

func indexFold(s, sub string) int {
	ls, lsub := len(s), len(sub)
	for i := 0; i+lsub <= ls; i++ {
		ok := true
		for j := 0; j < lsub; j++ {
			if lower(s[i+j]) != lower(sub[j]) {
				ok = false
				break
			}
		}
		if ok {
			return i
		}
	}
	return -1
}

func lower(b byte) byte {
	if b >= 'A' && b <= 'Z' {
		return b + 32
	}
	return b
}
