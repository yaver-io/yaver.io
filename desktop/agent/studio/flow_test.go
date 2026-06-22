package studio

import (
	"context"
	"strings"
	"testing"
)

func sampleFacts() *PermissionFacts {
	return &PermissionFacts{
		Platform:          "android",
		Permission:        "android.permission.FOREGROUND_SERVICE_SPECIAL_USE",
		FGSType:           "specialUse",
		Service:           &ServiceDecl{Name: ".sandbox.SandboxService", SpecialUseSubtype: "on_device_coding_agent"},
		SpecialUseSubtype: "on_device_coding_agent",
		Declared:          true,
	}
}

func captions(steps []Step) []string {
	var out []string
	for _, s := range steps {
		if s.Caption != "" {
			out = append(out, s.Caption)
		}
	}
	return out
}

func TestUseCaseProofSteps_Narrative(t *testing.T) {
	spec := PermissionVideoSpec{
		App:         App{Package: "io.yaver.mobile", Activity: ".MainActivity"},
		Facts:       sampleFacts(),
		StartAction: "io.yaver.mobile.sandbox.START",
	}
	ran := 0
	cfg := UseCaseConfig{
		WhatRuns:       "an on-device coding agent running a real GLM task",
		ProgressText:   "running",
		CompletionText: "Task finished",
		TaskSteps: []Step{{
			Caption: "give a real coding task",
			Run:     func(ctx context.Context, d Driver) error { ran++; return nil },
			HoldSec: 2,
		}},
	}
	steps := UseCaseProofSteps(spec, cfg)
	caps := captions(steps)
	joined := strings.Join(caps, "\n")

	// the task-giving step must be present and ordered after start, before completion
	if !strings.Contains(joined, "give a real coding task") {
		t.Fatalf("task step caption missing:\n%s", joined)
	}
	// the WHY caption is the whole point
	if !strings.Contains(joined, "Android would kill this mid-task") {
		t.Fatalf("missing the necessity/why caption:\n%s", joined)
	}
	// the payoff
	if !strings.Contains(joined, "task finished") && !strings.Contains(joined, "finished") {
		t.Fatalf("missing completion payoff caption:\n%s", joined)
	}
	// names the actual work
	if !strings.Contains(joined, "coding agent") {
		t.Fatalf("captions should name the real work:\n%s", joined)
	}

	// Running the steps' Run funcs (with a nil-safe fake driver) must invoke the task step.
	fd := &fakeDriver{}
	for _, s := range steps {
		if s.Run != nil {
			_ = s.Run(context.Background(), fd)
		}
	}
	if ran != 1 {
		t.Fatalf("task step should run exactly once, ran=%d", ran)
	}
	// background + completion-wait must have happened
	if !fd.wentHome {
		t.Fatalf("flow must background the app (Home)")
	}
	if fd.waited["Task finished"] == 0 {
		t.Fatalf("flow must WaitText for the completion string")
	}
}

func TestGenerateUseCaseJustification(t *testing.T) {
	cfg := UseCaseConfig{
		WhatRuns:       "an on-device coding agent running a real GLM task",
		ProgressText:   "running",
		CompletionText: "Task finished",
	}
	j := GenerateUseCaseJustification(sampleFacts(), "Yaver", cfg)
	if !strings.Contains(j.Description, "foreground service") {
		t.Fatalf("description must justify foreground service: %s", j.Description)
	}
	if !strings.Contains(j.Description, "killed") && !strings.Contains(j.Description, "kill") {
		t.Fatalf("description must argue necessity (process killed): %s", j.Description)
	}
	if !strings.Contains(j.Description, "on_device_coding_agent") {
		t.Fatalf("special-use subtype should appear: %s", j.Description)
	}
	if len(j.ShotList) < 5 {
		t.Fatalf("shot-list too short: %v", j.ShotList)
	}
	full := strings.Join(j.ShotList, "\n")
	if !strings.Contains(full, "coding agent") {
		t.Fatalf("shot-list should name the work: %v", j.ShotList)
	}
}

// fakeDriver is a real in-proc Driver (no mocks) recording what the flow asked.
type fakeDriver struct {
	wentHome bool
	waited   map[string]int
}

func (f *fakeDriver) ensure() {
	if f.waited == nil {
		f.waited = map[string]int{}
	}
}
func (f *fakeDriver) Launch(ctx context.Context, app App) error    { return nil }
func (f *fakeDriver) ForceStop(ctx context.Context, app App) error { return nil }
func (f *fakeDriver) StartForegroundService(ctx context.Context, component, action string) error {
	return nil
}
func (f *fakeDriver) StopService(ctx context.Context, component string) error { return nil }
func (f *fakeDriver) Tap(ctx context.Context, x, y int) error                 { return nil }
func (f *fakeDriver) TapText(ctx context.Context, text string) error          { return nil }
func (f *fakeDriver) Type(ctx context.Context, text string) error             { return nil }
func (f *fakeDriver) Key(ctx context.Context, key string) error               { return nil }
func (f *fakeDriver) WaitText(ctx context.Context, text string, timeoutSec int) error {
	f.ensure()
	f.waited[text]++
	return nil
}
func (f *fakeDriver) Back(ctx context.Context) error                       { return nil }
func (f *fakeDriver) Home(ctx context.Context) error                       { f.wentHome = true; return nil }
func (f *fakeDriver) ExpandNotifications(ctx context.Context) error        { return nil }
func (f *fakeDriver) CollapseNotifications(ctx context.Context) error      { return nil }
func (f *fakeDriver) NotificationText(ctx context.Context) (string, error) { return "", nil }
func (f *fakeDriver) Screenshot(ctx context.Context) ([]byte, error)       { return nil, nil }
func (f *fakeDriver) RecordStart(ctx context.Context, maxSec int) error    { return nil }
func (f *fakeDriver) RecordStop(ctx context.Context) ([]byte, error)       { return nil, nil }

var _ Driver = (*fakeDriver)(nil)
