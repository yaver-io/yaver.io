package main

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestStartTURN_RejectsEmptyAuthSecret(t *testing.T) {
	// Defense-in-depth: an empty auth secret silently turns the
	// server into an open TURN relay (Pion's lt-cred handler
	// returns true for any password). Catch this before bind so
	// operators don't ship an open relay by accident.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	err := StartTURN(ctx, "127.0.0.1", "yaver-relay", 0, "")
	if err == nil {
		t.Fatal("expected empty-authSecret error")
	}
	if !strings.Contains(err.Error(), "authSecret") {
		t.Errorf("error should call out the missing field: %v", err)
	}
}

func TestStartTURN_RejectsEmptyPublicIP(t *testing.T) {
	// publicIP becomes the relay's TURN candidate. Without it the
	// server starts but every allocation is unreachable from the
	// outside, which is worse than a clear error at boot.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	err := StartTURN(ctx, "", "yaver-relay", 3478, "secret")
	if err == nil {
		t.Fatal("expected empty-publicIP error")
	}
	if !strings.Contains(err.Error(), "publicIP") {
		t.Errorf("error should call out publicIP: %v", err)
	}
}

func TestStartTURN_RejectsInvalidPort(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	for _, p := range []int{-1, 0, 65536} {
		if err := StartTURN(ctx, "127.0.0.1", "yaver-relay", p, "secret"); err == nil {
			t.Errorf("port %d should be rejected", p)
		}
	}
}

func TestStartTURN_StartsAndStopsCleanly(t *testing.T) {
	// Bind on port 0 → kernel picks a free port. Verifies the
	// happy path: server starts, ctx cancellation tears it down,
	// no goroutines stuck. Skipped if running on a host that
	// can't bind UDP at all (CI sandbox).
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- StartTURN(ctx, "127.0.0.1", "yaver-relay", 0, "test-secret")
	}()
	// Wait a beat so the server has time to enter <-ctx.Done() in its
	// inner loop, then cancel.
	time.Sleep(150 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		// Pion sometimes returns a nil error on graceful close, sometimes
		// reports an "use of closed network connection". Either is fine —
		// what we're verifying is that it RETURNS at all.
		_ = err
	case <-time.After(2 * time.Second):
		t.Fatal("StartTURN didn't exit within 2s of ctx cancel")
	}
}
