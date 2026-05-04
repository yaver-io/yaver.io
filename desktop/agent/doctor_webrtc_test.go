package main

import (
	"context"
	"strings"
	"testing"
)

func TestBuildWebRTCDoctorReport_AlwaysIncludesBuiltins(t *testing.T) {
	// pion/webrtc and the in-tree H.264 extractor are statically
	// compiled into the agent. They must always show as ✓ regardless
	// of the host environment — that's the whole point of getting
	// the npm install down to "no extra deps required for the agent
	// role". A regression that shells out for these would silently
	// degrade the report on minimal hosts.
	r := buildWebRTCDoctorReport(context.Background())
	wantBuiltins := map[string]bool{
		"pion/webrtc":               false,
		"in-tree H.264 extractor":   false,
	}
	for _, c := range r.Checks {
		if _, want := wantBuiltins[c.Name]; want {
			if !c.OK {
				t.Errorf("builtin check %q should be OK, got: %+v", c.Name, c)
			}
			wantBuiltins[c.Name] = true
		}
	}
	for name, found := range wantBuiltins {
		if !found {
			t.Errorf("missing builtin check %q from report", name)
		}
	}
}

func TestBuildWebRTCDoctorReport_PopulatesMetadata(t *testing.T) {
	r := buildWebRTCDoctorReport(context.Background())
	if r.Platform == "" {
		t.Error("Platform should be set")
	}
	if r.Arch == "" {
		t.Error("Arch should be set")
	}
	if r.HostClass == "" {
		t.Error("HostClass should be set")
	}
	if r.AgentVersion == "" {
		t.Error("AgentVersion should be set (defaults to 'dev' or the compiled version)")
	}
	// Targets map always carries the well-known keys, even if the
	// values are false. This way the dashboard can iterate without
	// a presence check.
	if _, ok := r.Targets["android-emulator"]; !ok {
		t.Error("Targets must include android-emulator key")
	}
	if _, ok := r.Targets["ios-simulator"]; !ok {
		t.Error("Targets must include ios-simulator key")
	}
}

func TestFirstNonEmptyLine_BasicShapes(t *testing.T) {
	cases := map[string]string{
		"":                  "",
		"   \n  ":           "",
		"only one line":     "only one line",
		"\n\n  hi  \nrest":  "hi",
		"first\nsecond":     "first",
	}
	for in, want := range cases {
		got := firstNonEmptyLine(in)
		if got != want {
			t.Errorf("firstNonEmptyLine(%q)=%q want %q", in, got, want)
		}
	}
}

func TestProbeBinary_MissingReturnsNotOK(t *testing.T) {
	// A path that definitely doesn't exist anywhere reasonable.
	got := probeBinary(context.Background(), "yaver-doctor-webrtc-test-nonexistent-binary", "--version")
	if got.OK {
		t.Errorf("missing binary should return OK=false, got %+v", got)
	}
	if !strings.Contains(got.Detail, "PATH") {
		t.Errorf("Detail should mention PATH for missing binary, got %q", got.Detail)
	}
}

func TestYaverVersionString_NeverEmpty(t *testing.T) {
	// version is a const defined in main.go. Even if a future change
	// blanks it, the function must fall back to "dev" rather than
	// returning "" (which would render as a confusing blank in the
	// header).
	if got := yaverVersionString(); got == "" {
		t.Error("yaverVersionString must never return empty")
	}
}
