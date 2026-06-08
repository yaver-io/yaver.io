package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/yaver-io/agent/studio"
)

// --- a fake capture surface + driver (no Docker, no device) ---

type fakeQADriver struct {
	tree   string
	logcat string
	taps   []string
}

func (d *fakeQADriver) Launch(ctx context.Context, app studio.App) error              { return nil }
func (d *fakeQADriver) ForceStop(ctx context.Context, app studio.App) error           { return nil }
func (d *fakeQADriver) StartForegroundService(ctx context.Context, c, a string) error { return nil }
func (d *fakeQADriver) StopService(ctx context.Context, c string) error               { return nil }
func (d *fakeQADriver) Tap(ctx context.Context, x, y int) error                       { return nil }
func (d *fakeQADriver) TapText(ctx context.Context, t string) error {
	d.taps = append(d.taps, t)
	return nil
}
func (d *fakeQADriver) Type(ctx context.Context, t string) error              { return nil }
func (d *fakeQADriver) Key(ctx context.Context, k string) error               { return nil }
func (d *fakeQADriver) WaitText(ctx context.Context, t string, s int) error   { return nil }
func (d *fakeQADriver) Back(ctx context.Context) error                        { return nil }
func (d *fakeQADriver) Home(ctx context.Context) error                        { return nil }
func (d *fakeQADriver) ExpandNotifications(ctx context.Context) error         { return nil }
func (d *fakeQADriver) CollapseNotifications(ctx context.Context) error       { return nil }
func (d *fakeQADriver) NotificationText(ctx context.Context) (string, error)  { return "", nil }
func (d *fakeQADriver) Screenshot(ctx context.Context) ([]byte, error)        { return []byte("PNG"), nil }
func (d *fakeQADriver) RecordStart(ctx context.Context, maxSec int) error     { return nil }
func (d *fakeQADriver) RecordStop(ctx context.Context) ([]byte, error)        { return []byte("MP4"), nil }
func (d *fakeQADriver) ViewTree(ctx context.Context) (string, error)          { return d.tree, nil }
func (d *fakeQADriver) Logcat(ctx context.Context, lines int) (string, error) { return d.logcat, nil }

type fakeQASurface struct{ drv *fakeQADriver }

func (s *fakeQASurface) Provision(ctx context.Context) error            { return nil }
func (s *fakeQASurface) Install(ctx context.Context, path string) error { return nil }
func (s *fakeQASurface) Driver() studio.Driver                          { return s.drv }
func (s *fakeQASurface) Teardown(ctx context.Context) error             { return nil }
func (s *fakeQASurface) Platform() string                               { return "android" }

// scriptBrain ends the flow immediately and passes every assertion — so the test
// isolates the orchestrator + oracle bank, not the model.
type scriptBrain struct{ pass bool }

func (b *scriptBrain) NextAction(ctx context.Context, obs studio.Observation) (studio.BrainAction, error) {
	return studio.BrainAction{Done: true, Why: "scripted end"}, nil
}
func (b *scriptBrain) Assert(ctx context.Context, exp string, png []byte) (studio.AssertVerdict, error) {
	if b.pass {
		return studio.AssertVerdict{Expectation: exp, Pass: true, Severity: "info"}, nil
	}
	return studio.AssertVerdict{Expectation: exp, Pass: false, Severity: "fail", Reason: "missing"}, nil
}

func TestLoadFlows(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "01-signup.flow.yaml"), []byte(
		"name: signup\ngoal: create an account and reach home\nexpectations:\n  - home tab bar visible\nmax_steps: 12\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("ignore me"), 0o644)
	flows, err := loadFlows(dir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(flows) != 1 {
		t.Fatalf("expected 1 flow, got %d", len(flows))
	}
	if flows[0].Name != "signup" || flows[0].MaxSteps != 12 || len(flows[0].Expectations) != 1 {
		t.Errorf("parsed flow wrong: %+v", flows[0])
	}
}

func TestRunQAFlowsCatchesCrash(t *testing.T) {
	surf := &fakeQASurface{drv: &fakeQADriver{
		tree:   `<hierarchy><node text="Home"/><node text="x"/><node text="y"/></hierarchy>`,
		logcat: "E AndroidRuntime: FATAL EXCEPTION: main\nE AndroidRuntime: java.lang.NullPointerException",
	}}
	report, err := runQAFlows(context.Background(), qaFlowConfig{
		Surface:  surf,
		App:      studio.App{Package: "io.yaver.mobile"},
		Flows:    []studio.Scenario{{Name: "smoke", Goal: "open the app", Expectations: []string{"home visible"}, MaxSteps: 3}},
		BrainFor: func(studio.Scenario) studio.TestBrain { return &scriptBrain{pass: true} },
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if report.Caught != 1 {
		t.Fatalf("expected 1 caught bug (the crash), got %d: %+v", report.Caught, report.Bugs)
	}
	if report.Bugs[0].Oracle != "crash" || report.Passed {
		t.Errorf("bug/report wrong: %+v passed=%v", report.Bugs[0], report.Passed)
	}
	if len(report.Flows) != 1 || report.Flows[0].Bugs != 1 {
		t.Errorf("flow result wrong: %+v", report.Flows)
	}
}

// erroringBrain models an unreachable/unconfigured model (every NextAction fails).
type erroringBrain struct{}

func (erroringBrain) NextAction(ctx context.Context, obs studio.Observation) (studio.BrainAction, error) {
	return studio.BrainAction{}, fmt.Errorf("dial 127.0.0.1:11434: connection refused")
}
func (erroringBrain) Assert(ctx context.Context, exp string, png []byte) (studio.AssertVerdict, error) {
	return studio.AssertVerdict{Expectation: exp, Pass: false, Severity: "warn", Reason: "no model"}, nil
}

func TestRunQAFlowsModelUnavailableIsNotPass(t *testing.T) {
	surf := &fakeQASurface{drv: &fakeQADriver{tree: `<hierarchy><node text="X"/></hierarchy>`}}
	report, err := runQAFlows(context.Background(), qaFlowConfig{
		Surface:  surf,
		Flows:    []studio.Scenario{{Name: "f", Goal: "g", Expectations: []string{"home"}, MaxSteps: 3}},
		BrainFor: func(studio.Scenario) studio.TestBrain { return erroringBrain{} },
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if report.Passed {
		t.Fatal("a run where the agent never drove must NOT report PASS")
	}
	if len(report.Bugs) == 0 || report.Bugs[0].Oracle != "harness" {
		t.Fatalf("expected a harness bug, got %+v", report.Bugs)
	}
}

func TestRunQAFlowsExpectationFailureBecomesBug(t *testing.T) {
	surf := &fakeQASurface{drv: &fakeQADriver{
		tree: `<hierarchy><node text="Login"/><node text="a"/><node text="b"/></hierarchy>`,
	}}
	report, err := runQAFlows(context.Background(), qaFlowConfig{
		Surface:  surf,
		Flows:    []studio.Scenario{{Name: "f", Goal: "g", Expectations: []string{"checkout done"}, MaxSteps: 2}},
		BrainFor: func(studio.Scenario) studio.TestBrain { return &scriptBrain{pass: false} },
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if report.Caught != 1 || report.Bugs[0].Oracle != "expectation" {
		t.Fatalf("failed expectation should become a bug; got %+v", report.Bugs)
	}
}
