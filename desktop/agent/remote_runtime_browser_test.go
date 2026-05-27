package main

// remote_runtime_browser_test.go — interface-contract + capability
// probe tests for the browser-window runtime target. We deliberately
// do NOT spawn chromedp in CI — that's the e2e path (manual or
// hardware lab). These tests pin the wiring so a future rename in
// the runtimeTarget interface trips them immediately.

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestBrowserWindowTargetImplementsRuntimeTarget(t *testing.T) {
	// Compile-time guarantee — assigning to a runtimeTarget variable
	// fails at build if the interface drifts.
	var _ runtimeTarget = browserWindowTarget{}
}

func TestBrowserWindowTargetRegistered(t *testing.T) {
	tgt, err := runtimeTargetFor("browser-window")
	if err != nil {
		t.Fatalf("runtimeTargetFor(browser-window) returned err: %v", err)
	}
	if _, ok := tgt.(browserWindowTarget); !ok {
		t.Fatalf("unexpected target type %T — expected browserWindowTarget", tgt)
	}
}

func TestBrowserFrameworkCapabilities(t *testing.T) {
	caps := remoteRuntimeCapabilitiesForProject("/tmp/whatever", "browser")
	if !caps.RemoteRuntimeEligible {
		t.Fatalf("browser framework should be RemoteRuntimeEligible — capabilities = %+v", caps)
	}
	if caps.ExecutionMode != ExecutionModeNativeWebRTC {
		t.Fatalf("browser framework should be ExecutionModeNativeWebRTC, got %q", caps.ExecutionMode)
	}
	if len(caps.Targets) == 0 || caps.Targets[0].ID != "browser-window" {
		t.Fatalf("expected single browser-window target, got %+v", caps.Targets)
	}
}

func TestProbeBrowserWindowTargetShape(t *testing.T) {
	target := probeBrowserWindowTarget()
	if target.ID != "browser-window" {
		t.Fatalf("ID = %q, want browser-window", target.ID)
	}
	if target.Platform != "browser" {
		t.Fatalf("Platform = %q, want browser", target.Platform)
	}
	// Enabled depends on the test box. Reason must be populated when
	// disabled so the dashboard can render a usable error.
	if !target.Enabled && strings.TrimSpace(target.Reason) == "" {
		t.Fatalf("disabled target must carry a Reason explaining why")
	}
}

func TestBrowserPoolListEmpty(t *testing.T) {
	// Snapshot the pool count before our test ran (parallel test
	// processes won't share state but defensive coding helps when
	// somebody adds a t.Parallel() above).
	pool := &browserWindowPool{entries: map[string]*browserWindowEntry{}}
	if got := pool.list(); len(got) != 0 {
		t.Fatalf("fresh pool.list() = %v, want empty", got)
	}
}

func TestBrowserPoolCloseUnknown(t *testing.T) {
	pool := &browserWindowPool{entries: map[string]*browserWindowEntry{}}
	if pool.close("does-not-exist") {
		t.Fatalf("close on unknown id should return false")
	}
}

func TestOpsGlassPCVerbsRegistered(t *testing.T) {
	wantVerbs := []string{
		"glass_pc_open",
		"glass_pc_navigate",
		"glass_pc_focus",
		"glass_pc_close",
		"glass_pc_list",
		"glass_hud",
	}
	for _, name := range wantVerbs {
		opsRegistryMu.RLock()
		_, ok := opsRegistry[name]
		opsRegistryMu.RUnlock()
		if !ok {
			t.Errorf("verb %q not registered in opsRegistry", name)
		}
	}
}

func TestHUDPayloadClampers(t *testing.T) {
	long := strings.Repeat("x", 200)
	got := hudClampLine(long)
	if n := len([]rune(got)); n > hudMaxLineLen {
		t.Fatalf("hudClampLine returned %d runes (want ≤ %d)", n, hudMaxLineLen)
	}
	if !strings.HasSuffix(got, "…") {
		t.Fatalf("hudClampLine should mark truncation with …; got %q", got)
	}
	lines := make([]string, 12)
	for i := range lines {
		lines[i] = "line"
	}
	if got := hudClampLines(lines); len(got) != hudMaxLines {
		t.Fatalf("hudClampLines returned %d lines, want %d", len(got), hudMaxLines)
	}
}

func TestOpsGlassHUDBadView(t *testing.T) {
	// Stub server with no blackbox manager → handler should return
	// blackbox_missing. Confirms the early-exit path before we burn
	// payload unmarshal time.
	c := OpsContext{Server: &HTTPServer{}}
	body, _ := json.Marshal(map[string]any{"view": "notification", "payload": map[string]any{}})
	res := opsGlassHUDHandler(c, body)
	if res.OK {
		t.Fatalf("expected NOT OK when blackbox manager is nil; got %+v", res)
	}
	if res.Code != "blackbox_missing" {
		t.Fatalf("expected code blackbox_missing, got %q", res.Code)
	}
}
