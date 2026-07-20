package main

import (
	"testing"
	"time"
)

// The idle-park timing resolver is the single source of truth shared by the
// control-plane decision and the in-agent self-park loop. This pins its
// precedence + unified default so the two paths can never diverge again.
func TestResolveIdleParkMinutes(t *testing.T) {
	// Unified default (both env vars unset) = 30 min for BOTH paths.
	t.Setenv("YAVER_PARK_IDLE_MIN", "")
	t.Setenv("YAVER_CLOUD_IDLE_MINUTES", "")
	if got := resolveIdleParkMinutes(); got != 30*time.Minute {
		t.Errorf("default idle = %v, want 30m", got)
	}
	// Canonical env wins.
	t.Setenv("YAVER_PARK_IDLE_MIN", "20")
	t.Setenv("YAVER_CLOUD_IDLE_MINUTES", "45")
	if got := resolveIdleParkMinutes(); got != 20*time.Minute {
		t.Errorf("canonical env = %v, want 20m", got)
	}
	// Legacy env honored as fallback when canonical unset.
	t.Setenv("YAVER_PARK_IDLE_MIN", "")
	t.Setenv("YAVER_CLOUD_IDLE_MINUTES", "50")
	if got := resolveIdleParkMinutes(); got != 50*time.Minute {
		t.Errorf("legacy fallback = %v, want 50m", got)
	}
}

func TestResolveParkGraceMinutes(t *testing.T) {
	t.Setenv("YAVER_PARK_GRACE_MIN", "")
	t.Setenv("YAVER_CLOUD_IDLE_GRACE_MINUTES", "")
	if got := resolveParkGraceMinutes(); got != 2*time.Minute {
		t.Errorf("default grace = %v, want 2m", got)
	}
	t.Setenv("YAVER_PARK_GRACE_MIN", "5")
	if got := resolveParkGraceMinutes(); got != 5*time.Minute {
		t.Errorf("canonical grace = %v, want 5m", got)
	}
}
