package main

import (
	"runtime"
	"testing"
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
