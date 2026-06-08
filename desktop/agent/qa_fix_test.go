package main

import (
	"context"
	"testing"

	"github.com/yaver-io/agent/studio"
)

// fakeFixer always reports it patched; fakeReloader clears the planted crash on
// the shared driver so the verify pass sees a clean run — modeling a real fix.
type fakeFixer struct{ calls int }

func (f *fakeFixer) Fix(ctx context.Context, bug studio.Bug, flow studio.Scenario) (fixAttempt, error) {
	f.calls++
	return fixAttempt{Patched: true, Summary: "guarded the nil deref"}, nil
}

type clearingReloader struct{ drv *fakeQADriver }

func (r *clearingReloader) Reload(ctx context.Context) error {
	r.drv.logcat = "" // the patched build no longer crashes
	return nil
}

func TestFixModeFixesCaughtCrash(t *testing.T) {
	drv := &fakeQADriver{
		tree:   `<hierarchy><node text="Home"/><node text="x"/><node text="y"/></hierarchy>`,
		logcat: "E AndroidRuntime: FATAL EXCEPTION: main\nE AndroidRuntime: java.lang.NullPointerException",
	}
	fixer := &fakeFixer{}
	report, err := runQAFlows(context.Background(), qaFlowConfig{
		Surface:  &fakeQASurface{drv: drv},
		Flows:    []studio.Scenario{{Name: "smoke", Goal: "open", Expectations: []string{"home"}, MaxSteps: 2}},
		BrainFor: func(studio.Scenario) studio.TestBrain { return &scriptBrain{pass: true} },
		Mode:     "fix",
		Fixer:    fixer,
		Reloader: &clearingReloader{drv: drv},
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if fixer.calls == 0 {
		t.Fatal("fixer was never dispatched")
	}
	if report.Fixed != 1 {
		t.Fatalf("expected 1 fixed, got %d (bugs=%+v)", report.Fixed, report.Bugs)
	}
	if report.Bugs[0].Outcome != "fixed" || report.Bugs[0].FixSummary == "" {
		t.Errorf("bug not marked fixed with summary: %+v", report.Bugs[0])
	}
	if !report.Passed {
		t.Error("a fully-fixed run should pass")
	}
}

func TestFixModeUnresolvedWhenReloadDoesntHelp(t *testing.T) {
	drv := &fakeQADriver{
		tree:   `<hierarchy><node text="Home"/><node text="x"/><node text="y"/></hierarchy>`,
		logcat: "E AndroidRuntime: FATAL EXCEPTION: main",
	}
	report, err := runQAFlows(context.Background(), qaFlowConfig{
		Surface:  &fakeQASurface{drv: drv},
		Flows:    []studio.Scenario{{Name: "smoke", Goal: "open", MaxSteps: 2}},
		BrainFor: func(studio.Scenario) studio.TestBrain { return &scriptBrain{pass: true} },
		Mode:     "fix",
		Fixer:    &fakeFixer{},
		Reloader: &noopReloader{}, // crash persists
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if report.Fixed != 0 {
		t.Errorf("nothing should be marked fixed; got %d", report.Fixed)
	}
	if report.Bugs[0].Outcome != "attempted-unresolved" {
		t.Errorf("persistent bug should be attempted-unresolved, got %q", report.Bugs[0].Outcome)
	}
	if report.Passed {
		t.Error("run with an unresolved bug must not pass")
	}
}

func TestFixModeFallsBackToCatchWithoutFixer(t *testing.T) {
	drv := &fakeQADriver{logcat: "E AndroidRuntime: FATAL EXCEPTION: main",
		tree: `<hierarchy><node text="a"/><node text="b"/><node text="c"/></hierarchy>`}
	report, _ := runQAFlows(context.Background(), qaFlowConfig{
		Surface:  &fakeQASurface{drv: drv},
		Flows:    []studio.Scenario{{Name: "f", Goal: "g", MaxSteps: 1}},
		BrainFor: func(studio.Scenario) studio.TestBrain { return &scriptBrain{pass: true} },
		Mode:     "fix", // but no Fixer
	})
	if report.Mode != "catch" {
		t.Errorf("should fall back to catch mode, got %q", report.Mode)
	}
	if report.Bugs[0].Outcome != "caught" {
		t.Errorf("bug should be caught, got %q", report.Bugs[0].Outcome)
	}
}

type noopReloader struct{}

func (noopReloader) Reload(ctx context.Context) error { return nil }
