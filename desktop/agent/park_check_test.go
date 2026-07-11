package main

import (
	"testing"
	"time"
)

func TestParkEnvMinutes(t *testing.T) {
	t.Setenv("YAVER_PARK_IDLE_MIN", "45")
	if got := parkEnvMinutes("YAVER_PARK_IDLE_MIN", 30); got != 45*time.Minute {
		t.Fatalf("env value not honored: %v", got)
	}
	t.Setenv("YAVER_PARK_IDLE_MIN", "")
	if got := parkEnvMinutes("YAVER_PARK_IDLE_MIN", 30); got != 30*time.Minute {
		t.Fatalf("default not applied: %v", got)
	}
	t.Setenv("YAVER_PARK_IDLE_MIN", "-5")
	if got := parkEnvMinutes("YAVER_PARK_IDLE_MIN", 30); got != 30*time.Minute {
		t.Fatalf("negative must fall back to default: %v", got)
	}
	t.Setenv("YAVER_PARK_IDLE_MIN", "notanumber")
	if got := parkEnvMinutes("YAVER_PARK_IDLE_MIN", 30); got != 30*time.Minute {
		t.Fatalf("garbage must fall back to default: %v", got)
	}
}

func TestDurSince(t *testing.T) {
	if got := durSince(time.Now(), time.Time{}); got != 0 {
		t.Fatalf("zero time must give 0 duration, got %v", got)
	}
	now := time.Now()
	if got := durSince(now, now.Add(-5*time.Minute)); got < 4*time.Minute {
		t.Fatalf("expected ~5m, got %v", got)
	}
}

// TestTouchAndKeepAlive verifies the activity/keep-alive state transitions the
// park verbs rely on, without needing a live box.
func TestTouchAndKeepAlive(t *testing.T) {
	// touchParkActivity clears any armed notify and pins the idle clock.
	parkMu.Lock()
	parkNotifiedAt = time.Now().Add(-time.Hour)
	parkMu.Unlock()
	touchParkActivity()
	parkMu.Lock()
	notified := parkNotifiedAt
	lastActive := parkLastActiveAt
	parkMu.Unlock()
	if !notified.IsZero() {
		t.Fatal("touchParkActivity must clear the armed notify")
	}
	if time.Since(lastActive) > time.Second {
		t.Fatal("touchParkActivity must pin lastActive to now")
	}

	// keepalive holds the box awake and cancels the notify.
	res := opsMachineKeepAliveHandler(OpsContext{}, nil)
	if !res.OK {
		t.Fatal("keepalive should succeed")
	}
	parkMu.Lock()
	keepUntil := parkKeepAliveUntil
	parkMu.Unlock()
	if !time.Now().Before(keepUntil) {
		t.Fatal("keepalive must set a future keep-alive deadline")
	}
}

func TestParkVerbsRegistered(t *testing.T) {
	for _, name := range []string{"machine_park_check", "machine_keepalive", "machine_seed", "machine_wake"} {
		opsRegistryMu.RLock()
		_, ok := opsRegistry[name]
		opsRegistryMu.RUnlock()
		if !ok {
			t.Fatalf("verb %q not registered", name)
		}
	}
}
