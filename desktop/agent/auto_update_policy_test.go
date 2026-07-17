package main

import (
	"testing"
	"time"
)

// The whole point of the tri-state: "operator never said" must read as
// ON, or the periodic checker is dead code again and the fleet drifts.
func TestShouldAutoUpdateDefaultsOn(t *testing.T) {
	cases := []struct {
		name string
		cfg  *Config
		want bool
	}{
		{"nil config", nil, true},
		{"unset field", &Config{}, true},
		{"explicit true", &Config{AutoUpdate: boolPtr(true)}, true},
		{"explicit false", &Config{AutoUpdate: boolPtr(false)}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := shouldAutoUpdate(tc.cfg); got != tc.want {
				t.Errorf("shouldAutoUpdate() = %v, want %v", got, tc.want)
			}
		})
	}
}

// An explicit opt-out must survive a round trip. Under the old plain
// bool + omitempty this silently failed: false was omitted, so disable
// wrote nothing and the box re-enabled itself on the next read.
func TestAutoUpdateDisablePersists(t *testing.T) {
	cfg := &Config{AutoUpdate: boolPtr(false)}
	if applyDefaultAutoUpdate(cfg) {
		t.Fatal("applyDefaultAutoUpdate overwrote an explicit opt-out")
	}
	if shouldAutoUpdate(cfg) {
		t.Error("explicit false did not survive applyDefaultAutoUpdate")
	}
}

func TestApplyDefaultAutoUpdatePinsUnset(t *testing.T) {
	cfg := &Config{}
	if !applyDefaultAutoUpdate(cfg) {
		t.Fatal("applyDefaultAutoUpdate reported no write on an unset field")
	}
	if cfg.AutoUpdate == nil || !*cfg.AutoUpdate {
		t.Fatalf("AutoUpdate = %v, want pinned true", cfg.AutoUpdate)
	}
	// Second call is a no-op — it must not report a spurious write and
	// trigger a pointless SaveConfig on every boot.
	if applyDefaultAutoUpdate(cfg) {
		t.Error("applyDefaultAutoUpdate reported a write on an already-pinned field")
	}
}

// forcedAutoUpdateConfig must not mutate the caller's config: a forced
// `yaver update` should not silently persist auto-update=true for an
// operator who deliberately opted out.
func TestForcedAutoUpdateConfigDoesNotMutateCaller(t *testing.T) {
	cfg := &Config{AutoUpdate: boolPtr(false), DeviceID: "dev-1"}
	forced := forcedAutoUpdateConfig(cfg)

	if !shouldAutoUpdate(forced) {
		t.Error("forced config did not force auto-update on")
	}
	if shouldAutoUpdate(cfg) {
		t.Error("forcedAutoUpdateConfig mutated the caller's opt-out")
	}
	if forced.DeviceID != "dev-1" {
		t.Errorf("forced config lost unrelated fields: DeviceID = %q", forced.DeviceID)
	}
}

func TestForcedAutoUpdateConfigHandlesNil(t *testing.T) {
	if forced := forcedAutoUpdateConfig(nil); !shouldAutoUpdate(forced) {
		t.Error("forcedAutoUpdateConfig(nil) should still force auto-update on")
	}
}

// Jitter bounds: every draw lands in [6h, 12h), and the draws actually
// differ. A constant would phase-lock the fleet onto one GitHub API
// burst per release — the thing the jitter exists to prevent.
func TestAutoUpdateCheckIntervalIsJittered(t *testing.T) {
	const min = 6 * time.Hour
	const max = 12 * time.Hour

	seen := make(map[time.Duration]bool)
	for i := 0; i < 200; i++ {
		d := autoUpdateCheckInterval()
		if d < min || d >= max {
			t.Fatalf("interval %v out of range [%v, %v)", d, min, max)
		}
		seen[d] = true
	}
	if len(seen) < 2 {
		t.Error("autoUpdateCheckInterval returned a constant — the fleet would phase-lock")
	}
}
