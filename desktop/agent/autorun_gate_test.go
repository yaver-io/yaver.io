package main

import (
	"context"
	"testing"
	"time"
)

// Always via the constructor. Hand-rolling the struct here is how this file
// first panicked on a nil exemptions map: a field added to the gate silently
// missed the test's copy of the initialization.
func newTestGate() *autorunGate { return newAutorunGate() }

func TestAutorunGatePassesThroughWhenNotFrozen(t *testing.T) {
	g := newTestGate()
	done := make(chan error, 1)
	go func() { done <- g.await(context.Background(), "a", nil) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("await on an unfrozen gate: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("await blocked on an unfrozen gate; an unfrozen machine must never hold a loop")
	}
}

func TestAutorunGateParksUntilResume(t *testing.T) {
	g := newTestGate()
	if !g.pause("deploy", 0) {
		t.Fatal("first pause should report ownership of the freeze")
	}
	done := make(chan error, 1)
	go func() { done <- g.await(context.Background(), "a", nil) }()

	// The loop must still be held.
	select {
	case <-done:
		t.Fatal("await returned while the gate was frozen")
	case <-time.After(150 * time.Millisecond):
	}
	if !g.isParked("a") {
		t.Fatal("a held loop must report as parked; that is the drain signal a deploy waits on")
	}

	g.resume()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("await after resume: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("resume did not wake the parked loop")
	}
	if g.isParked("a") {
		t.Fatal("a resumed loop must not still report as parked")
	}
}

// A freeze must never make the fleet unkillable — autorun_stop has to reach a
// parked loop.
func TestAutorunGateParkedLoopStaysCancellable(t *testing.T) {
	g := newTestGate()
	g.pause("deploy", 0)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- g.await(ctx, "a", nil) }()
	time.Sleep(100 * time.Millisecond)

	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("cancelling a parked loop must surface the context error, not a clean resume")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("a parked loop ignored context cancellation; a freeze must not make autorun_stop unreachable")
	}
	if g.isParked("a") {
		t.Fatal("a cancelled loop must deregister from the gate, or a drain would wait on a corpse forever")
	}
}

// Two overlapping ships must not deadlock, and the second must know it does not
// own the freeze so it never thaws the first one's fleet mid-deploy.
func TestAutorunGateDoublePauseReportsNonOwnership(t *testing.T) {
	g := newTestGate()
	if !g.pause("ship A", 0) {
		t.Fatal("first pause should own the freeze")
	}
	if g.pause("ship B", 0) {
		t.Fatal("second pause must report that it does NOT own the freeze")
	}
	if got := g.state().Reason; got != "ship A" {
		t.Fatalf("the owner's reason must survive a second pause, got %q", got)
	}
	if !g.resume() {
		t.Fatal("resume should report lifting a real freeze")
	}
	if g.resume() {
		t.Fatal("resume on an unfrozen gate must report that there was nothing to lift")
	}
}

// The park announcement explains the gap in the run's own progress log, and must
// fire exactly once no matter how many times the loop re-checks the gate.
func TestAutorunGateAnnouncesParkOnce(t *testing.T) {
	g := newTestGate()
	g.pause("deploy", 0)
	calls := make(chan struct{}, 8)
	go func() { _ = g.await(context.Background(), "a", func() { calls <- struct{}{} }) }()

	time.Sleep(100 * time.Millisecond)
	if len(calls) != 1 {
		t.Fatalf("expected exactly one park announcement while held, got %d", len(calls))
	}
	g.resume()
	time.Sleep(100 * time.Millisecond)
	if len(calls) != 1 {
		t.Fatalf("park announced %d times; a resumed loop must not re-announce", len(calls))
	}
}

// An unfrozen gate must not announce at all — otherwise every iteration of every
// healthy loop would write a park line.
func TestAutorunGateSilentWhenNotFrozen(t *testing.T) {
	g := newTestGate()
	calls := 0
	if err := g.await(context.Background(), "a", func() { calls++ }); err != nil {
		t.Fatal(err)
	}
	if calls != 0 {
		t.Fatalf("an unfrozen gate announced %d times; it must be silent", calls)
	}
}

// The repair loop runs UNDER the freeze it exists to fix main for. Without an
// exemption it parks instantly and ship waits forever for a repair that is
// waiting for ship — deadlock on day one.
func TestAutorunGateExemptRunPassesThroughAFreeze(t *testing.T) {
	g := newTestGate()
	g.pause("ship: deploying", 0)
	g.exempt("repair-1")

	done := make(chan error, 1)
	go func() { done <- g.await(context.Background(), "repair-1", nil) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("exempt run: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the repair loop parked under its own freeze; ship would deadlock against its own barrier")
	}

	// The exemption must be narrow: everyone else still parks.
	other := make(chan error, 1)
	go func() { other <- g.await(context.Background(), "ordinary-run", nil) }()
	select {
	case <-other:
		t.Fatal("a non-exempt run passed the gate; the exemption must cover exactly one run")
	case <-time.After(150 * time.Millisecond):
	}
	g.resume()
}

// An exemption granted while the run is already parked must wake it — ship
// exempts the repair after the freeze is up.
func TestAutorunGateExemptWakesAnAlreadyParkedRun(t *testing.T) {
	g := newTestGate()
	g.pause("ship: deploying", 0)
	done := make(chan error, 1)
	go func() { done <- g.await(context.Background(), "repair-1", nil) }()
	time.Sleep(100 * time.Millisecond)
	if !g.isParked("repair-1") {
		t.Fatal("precondition: the run should be parked before it is exempted")
	}

	g.exempt("repair-1")
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("exempt run: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("exempting an already-parked run did not wake it")
	}
}

// The dead-man lease. A coordinator on another machine can die holding this
// gate; the fleet must thaw itself rather than stay frozen forever.
func TestAutorunGateLeaseThawsAStrandedFleet(t *testing.T) {
	g := newTestGate()
	g.pause("ship: deploying", 120*time.Millisecond)
	done := make(chan error, 1)
	go func() { done <- g.await(context.Background(), "a", nil) }()

	select {
	case <-done:
		t.Fatal("the loop resumed before the lease expired")
	case <-time.After(50 * time.Millisecond):
	}

	// Nobody renews — the coordinator is gone.
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("lease expiry should resume cleanly, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the lease never fired; a dead coordinator would strand this fleet forever")
	}
	st := g.state()
	if st.Paused {
		t.Fatal("gate still frozen after the lease expired")
	}
	if !st.LeaseExpired {
		t.Fatal("a lease-expiry thaw must be recorded, not silent — it means a ship died holding the freeze")
	}
}

// A renew keeps the fleet frozen while the coordinator is alive, so an ordinary
// slow deploy never trips the dead-man switch.
func TestAutorunGateRenewKeepsFleetFrozen(t *testing.T) {
	g := newTestGate()
	g.pause("ship: deploying", 100*time.Millisecond)
	done := make(chan error, 1)
	go func() { done <- g.await(context.Background(), "a", nil) }()

	for i := 0; i < 4; i++ {
		time.Sleep(50 * time.Millisecond)
		if !g.renew(100 * time.Millisecond) {
			t.Fatal("renew reported no live freeze; the lease expired despite a live coordinator")
		}
	}
	select {
	case <-done:
		t.Fatal("the fleet thawed while its coordinator was still renewing")
	default:
	}
	if g.state().LeaseExpired {
		t.Fatal("a renewed lease must never report as expired")
	}
	g.resume()
}
