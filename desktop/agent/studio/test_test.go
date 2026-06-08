package studio

import (
	"context"
	"strings"
	"testing"
)

func TestRunTestScriptedScenario(t *testing.T) {
	f := &fakeRunner{
		getData: []byte("MP4orPNG"),
		respond: func(cmd string) string {
			switch {
			case strings.Contains(cmd, "lsmod"):
				return "1"
			case strings.Contains(cmd, "getprop sys.boot_completed"):
				return "1"
			case strings.Contains(cmd, "pm install"):
				return "Success"
			}
			return ""
		},
	}
	surface := newSurface(f)
	scenarios := []Scenario{{
		Name: "signup-and-delete",
		Goal: "create account, do things, delete account",
		Steps: []TestStep{
			{Verb: "taptext", Args: map[string]string{"text": "Continue with Email"}},
			{Verb: "type", Args: map[string]string{"text": "demo@example.com"}},
			{Verb: "tap", Args: map[string]string{"x": "540", "y": "1475"}},
		},
		Expectations: []string{"Welcome"},
	}}
	brainFor := func(s Scenario) TestBrain { return NewScriptedBrain(s.Steps, nil) }

	res, err := RunTest(context.Background(), surface, App{Package: "io.example", Activity: ".MainActivity"}, "/local/app.apk", scenarios, brainFor, nil)
	if err != nil {
		t.Fatalf("run test: %v", err)
	}
	if len(res.Scenarios) != 1 {
		t.Fatalf("expected 1 scenario result, got %d", len(res.Scenarios))
	}
	sr := res.Scenarios[0]
	// 3 scripted steps + the terminal "Done" action are recorded.
	if len(sr.Steps) < 3 {
		t.Errorf("expected >=3 steps recorded, got %d", len(sr.Steps))
	}
	if len(sr.Verdicts) != 1 {
		t.Errorf("expected 1 verdict, got %d", len(sr.Verdicts))
	}
	if string(res.MP4) != "MP4orPNG" {
		t.Errorf("expected recording bytes")
	}
	// the driver issued the scripted verbs
	if !f.saw("input text") || !f.saw("demo@example.com") {
		t.Errorf("type step not issued; cmds=%v", f.cmds)
	}
	if !f.saw("am start -n io.example/.MainActivity") {
		t.Errorf("app not launched")
	}
}

func TestApplyTestStepVerbs(t *testing.T) {
	f := &fakeRunner{}
	d := newSurface(f).Driver()
	ctx := context.Background()
	for _, st := range []TestStep{
		{Verb: "tap", Args: map[string]string{"x": "100", "y": "200"}},
		{Verb: "key", Args: map[string]string{"key": "BACK"}},
		{Verb: "home"},
	} {
		if err := applyTestStep(ctx, d, st); err != nil {
			t.Errorf("verb %s: %v", st.Verb, err)
		}
	}
	if !f.saw("input tap 100 200") || !f.saw("input keyevent KEYCODE_BACK") || !f.saw("input keyevent KEYCODE_HOME") {
		t.Errorf("verbs not mapped; cmds=%v", f.cmds)
	}
}
