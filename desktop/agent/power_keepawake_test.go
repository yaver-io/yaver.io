package main

import (
	osexec "os/exec"
	"runtime"
	"sync/atomic"
	"testing"
	"time"
)

func TestShouldEnableHeadlessKeepAwake_DefaultsOnSupportedPlatforms(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("platform-specific default")
	}
	if isWSL() {
		t.Skip("WSL intentionally excluded")
	}
	if !shouldEnableHeadlessKeepAwake(&Config{}) {
		t.Fatal("expected keep-awake to default on")
	}
}

func TestShouldEnableHeadlessKeepAwake_ExplicitFalseWins(t *testing.T) {
	disabled := false
	cfg := &Config{HeadlessKeepAwake: &disabled}
	if shouldEnableHeadlessKeepAwake(cfg) {
		t.Fatal("expected explicit false to disable keep-awake")
	}
}

func TestApplyDefaultHeadlessKeepAwake(t *testing.T) {
	cfg := &Config{}
	changed := applyDefaultHeadlessKeepAwake(cfg)
	if runtime.GOOS == "darwin" || (runtime.GOOS == "linux" && !isWSL()) {
		if !changed {
			t.Fatal("expected supported platform default to be applied")
		}
		if cfg.HeadlessKeepAwake == nil || !*cfg.HeadlessKeepAwake {
			t.Fatal("expected keep-awake to be enabled")
		}
		return
	}
	if changed {
		t.Fatal("expected unsupported platform to remain unchanged")
	}
	if cfg.HeadlessKeepAwake != nil {
		t.Fatal("expected keep-awake to stay unset")
	}
}

// The 2026-07-21 incident fix: if the sleep-inhibitor dies while the agent runs,
// it MUST be respawned (a box that sleeps drops off the network). Proves the
// supervisor respawns a short-lived helper and stops cleanly on the stop signal.
func TestSuperviseKeepAwake_RespawnsThenStops(t *testing.T) {
	var spawns int32
	build := func() *osexec.Cmd {
		atomic.AddInt32(&spawns, 1)
		return osexec.Command("sh", "-c", "exit 0") // exits immediately (like a killed helper)
	}
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() { superviseKeepAwakeWith(build, 10*time.Millisecond, stop); close(done) }()

	time.Sleep(150 * time.Millisecond)
	if n := atomic.LoadInt32(&spawns); n < 3 {
		t.Fatalf("expected the inhibitor to be respawned multiple times, got %d spawns", n)
	}
	close(stop)
	select {
	case <-done:
	case <-time.After(1 * time.Second):
		t.Fatal("supervisor did not stop after the stop signal")
	}
}

// A platform with no inhibitor (build returns nil) exits immediately, never loops.
func TestSuperviseKeepAwake_NoInhibitorExits(t *testing.T) {
	done := make(chan struct{})
	go func() {
		superviseKeepAwakeWith(func() *osexec.Cmd { return nil }, time.Millisecond, make(chan struct{}))
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(1 * time.Second):
		t.Fatal("supervisor should exit immediately when no inhibitor is available")
	}
}
