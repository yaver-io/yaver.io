package main

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestChooseSSHTransport(t *testing.T) {
	if got := chooseSSHTransport(true); got != sshTransportNativeDirect {
		t.Errorf("direct route → native-direct, got %q", got)
	}
	if got := chooseSSHTransport(false); got != sshTransportReverseRelay {
		t.Errorf("no direct route → reverse-relay, got %q", got)
	}
}

func TestReverseTunnelBackoff_BoundedAndMonotonic(t *testing.T) {
	prev := time.Duration(0)
	for attempt := 1; attempt <= 12; attempt++ {
		d := reverseTunnelBackoff(attempt)
		if d < sshReverseTunnelBaseBackoff {
			t.Fatalf("attempt %d: backoff %v below base", attempt, d)
		}
		if d > sshReverseTunnelBackoffCap {
			t.Fatalf("attempt %d: backoff %v exceeds cap %v", attempt, d, sshReverseTunnelBackoffCap)
		}
		if d < prev {
			t.Fatalf("attempt %d: backoff %v decreased from %v (must be monotonic)", attempt, d, prev)
		}
		prev = d
	}
	// It must actually REACH the cap and stay there (no unbounded growth).
	if reverseTunnelBackoff(20) != sshReverseTunnelBackoffCap {
		t.Fatal("late attempts must sit at the cap, not grow unbounded")
	}
}

// The supervisor must GIVE UP after the attempt cap on persistent failure — never
// an infinite hammer loop (the "no high-frequency loops even in troubleshooting"
// contract).
func TestSuperviseReverseTunnel_GivesUpAfterCap(t *testing.T) {
	calls := 0
	dial := func(ctx context.Context, generation int) error {
		calls++
		return errors.New("relay refused")
	}
	err := superviseReverseTunnel(context.Background(), dial, func(time.Duration) {})
	if err == nil {
		t.Fatal("persistent failure must surface an error, not loop forever")
	}
	if calls != sshReverseTunnelMaxAttempts {
		t.Fatalf("expected exactly %d attempts before giving up, got %d", sshReverseTunnelMaxAttempts, calls)
	}
}

// A tunnel that comes up and later drops cleanly resets the failure streak — a
// long-lived tunnel that finally drops is not a failing tunnel, so it keeps
// redialing (not counted toward the give-up cap).
func TestSuperviseReverseTunnel_CleanDropResetsStreak(t *testing.T) {
	calls := 0
	dial := func(ctx context.Context, generation int) error {
		calls++
		if calls >= 5 {
			return context.Canceled // simulate shutdown to end the test
		}
		return nil // "tunnel held then closed cleanly"
	}
	ctx, cancel := context.WithCancel(context.Background())
	// Stop after the 5th call via the dial returning; but our supervisor only
	// stops on ctx — so cancel from inside via a wrapper.
	wrapped := func(c context.Context, g int) error {
		err := dial(c, g)
		if err == context.Canceled {
			cancel()
		}
		return nil
	}
	if err := superviseReverseTunnel(ctx, wrapped, func(time.Duration) {}); err != nil {
		t.Fatalf("clean drops + shutdown should return nil, got %v", err)
	}
	if calls < 5 {
		t.Fatalf("expected to keep redialing across clean drops, only %d calls", calls)
	}
}

// Generation increments per dial so the relay can generation-replace stale tunnels.
func TestSuperviseReverseTunnel_GenerationIncrements(t *testing.T) {
	var gens []int
	dial := func(ctx context.Context, generation int) error {
		gens = append(gens, generation)
		if len(gens) >= 3 {
			return context.Canceled
		}
		return errors.New("drop")
	}
	ctx, cancel := context.WithCancel(context.Background())
	wrapped := func(c context.Context, g int) error {
		err := dial(c, g)
		if err == context.Canceled {
			cancel()
			return nil
		}
		return err
	}
	_ = superviseReverseTunnel(ctx, wrapped, func(time.Duration) {})
	for i, g := range gens {
		if g != i+1 {
			t.Fatalf("generation should increment 1,2,3…; got %v", gens)
		}
	}
}
