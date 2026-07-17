package main

import (
	"context"
	"sort"
	"sync"
	"time"
)

// autorunGate holds every autorun loop on this machine at an iteration
// boundary without ending any run.
//
// It is machine-wide on purpose, and deliberately keyed on neither run ID nor
// Slot. The point of a freeze is that nothing is mid-flight while a deploy
// runs, so a loop that STARTS during the window has to park too — a per-run
// flag cannot hold a run that did not exist when the freeze was called.
//
// Nothing here is persisted, and nothing needs to be: autorun state is
// in-memory and dies with the agent process, so a restart drops the loops and
// the freeze together. There is no reachable state where the flag outlives the
// loops it was holding.
//
// The freeze is not the same thing as a drain, and callers must not conflate
// them. paused stops the NEXT iteration the instant it is set; a loop already
// inside autorunKick keeps running for up to autorunKickTimeout (30m) before it
// reaches the gate. Until it does, it can still commit and push. Ask parked()
// which loops actually arrived — that, not paused, is the "safe to deploy"
// signal.
type autorunGate struct {
	mu     sync.Mutex
	paused bool
	reason string
	since  time.Time
	// resumed is closed by resume. Each pause installs a fresh channel so a loop
	// that parked during an earlier freeze can never wake on a stale one.
	resumed chan struct{}
	parked  map[string]time.Time

	// expiry is the lease deadline; zero means an unleased (manual) freeze.
	//
	// The lease exists because the coordinator is usually NOT on this machine.
	// A local freeze needs no persistence — if this agent dies, its loops and its
	// freeze die together, so the flag can never outlive the loops it holds. That
	// reasoning collapses across machines: when a MacBook freezes the mini and
	// then crashes, the mini's agent is still up and still frozen, with no one
	// left to thaw it. The lease is the dead-man switch, and it fails toward the
	// fleet RUNNING: a spurious resume costs one racing push, a permanent freeze
	// costs the entire point of having autoruns.
	expiry     time.Time
	leaseTimer *time.Timer
	// expired records that the last thaw was the lease firing rather than a
	// coordinator asking. Surfaced so "the fleet resumed on its own" is never
	// silent — it means a ship died holding the freeze.
	expired bool

	// exemptions are run IDs that pass the gate untouched.
	//
	// Required by the repair loop, which would otherwise deadlock: "make it
	// compilable" is itself an autorun, started BY ship while ship holds the
	// freeze. Without an exemption it parks instantly and ship waits forever for
	// a repair that is waiting for ship. Kept narrow — one ID at a time, cleared
	// when the repair ends — because a category exemption ("repairs are exempt")
	// would let any loop claim to be one.
	exemptions map[string]bool
}

var autorunFreeze = newAutorunGate()

func newAutorunGate() *autorunGate {
	return &autorunGate{parked: map[string]time.Time{}, exemptions: map[string]bool{}}
}

type autorunGateState struct {
	Paused bool      `json:"paused"`
	Reason string    `json:"reason,omitempty"`
	Since  time.Time `json:"since,omitempty"`
	// Parked lists the loops that have actually reached the gate. A frozen
	// machine with a running loop absent from this list is still draining: that
	// loop is mid-kick and may yet commit.
	Parked []string `json:"parked,omitempty"`
	// Expiry is when the dead-man lease thaws this freeze on its own. Zero means
	// an unleased freeze that only an explicit resume lifts.
	Expiry time.Time `json:"expiry,omitempty"`
	// LeaseExpired records that the last thaw was the lease firing, not a
	// coordinator asking — i.e. a ship died holding the freeze. Never silent.
	LeaseExpired bool `json:"leaseExpired,omitempty"`
	// Exempt lists run IDs that pass the gate untouched (the repair loop).
	Exempt []string `json:"exempt,omitempty"`
}

// pause freezes the machine's autoruns and reports whether this call is what
// changed the state. Re-pausing an already-frozen machine is a no-op rather
// than an error, so two overlapping ships cannot deadlock each other — but the
// false return tells the second one it does not own the freeze and must not
// lift it.
//
// ttl arms the dead-man lease (see autorunGate.expiry). A ttl of 0 means an
// unleased freeze that only an explicit resume lifts — correct for a human at a
// terminal on this machine, wrong for a remote coordinator.
func (g *autorunGate) pause(reason string, ttl time.Duration) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.paused {
		return false
	}
	g.paused = true
	g.reason = reason
	g.since = time.Now().UTC()
	g.expired = false
	g.resumed = make(chan struct{})
	g.armLeaseLocked(ttl)
	return true
}

// armLeaseLocked (re)starts the dead-man timer. Caller holds g.mu.
func (g *autorunGate) armLeaseLocked(ttl time.Duration) {
	if g.leaseTimer != nil {
		g.leaseTimer.Stop()
		g.leaseTimer = nil
	}
	if ttl <= 0 {
		g.expiry = time.Time{}
		return
	}
	g.expiry = time.Now().UTC().Add(ttl)
	g.leaseTimer = time.AfterFunc(ttl, g.expireLease)
}

// renew extends the lease. A live coordinator calls this on a heartbeat; the
// fleet stays frozen only for as long as someone is actually there.
func (g *autorunGate) renew(ttl time.Duration) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.paused {
		return false
	}
	g.armLeaseLocked(ttl)
	return true
}

// expireLease thaws the fleet because nobody renewed. It is the dead coordinator
// path, and it deliberately looks exactly like a resume to the parked loops —
// they simply continue. The difference is recorded, not enacted.
func (g *autorunGate) expireLease() {
	g.mu.Lock()
	if !g.paused {
		g.mu.Unlock()
		return
	}
	g.resumeLocked()
	g.expired = true
	g.mu.Unlock()
}

// resume lifts the freeze and wakes every parked loop. Reports whether a freeze
// was actually lifted.
func (g *autorunGate) resume() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.paused {
		return false
	}
	g.resumeLocked()
	return true
}

func (g *autorunGate) resumeLocked() {
	g.paused = false
	g.reason = ""
	g.since = time.Time{}
	g.expiry = time.Time{}
	if g.leaseTimer != nil {
		g.leaseTimer.Stop()
		g.leaseTimer = nil
	}
	if g.resumed != nil {
		close(g.resumed)
		g.resumed = nil
	}
}

// exempt lets one run through the gate untouched. See autorunGate.exemptions.
func (g *autorunGate) exempt(id string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.exemptions[id] = true
	// An exempted run may already be parked from before it was exempted. Wake the
	// whole gate so it re-checks; the others find paused still true and park again.
	if g.paused && g.resumed != nil {
		close(g.resumed)
		g.resumed = make(chan struct{})
	}
}

func (g *autorunGate) unexempt(id string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.exemptions, id)
}

func (g *autorunGate) state() autorunGateState {
	g.mu.Lock()
	defer g.mu.Unlock()
	s := autorunGateState{Paused: g.paused, Reason: g.reason, Since: g.since, Expiry: g.expiry, LeaseExpired: g.expired}
	for id := range g.parked {
		s.Parked = append(s.Parked, id)
	}
	for id := range g.exemptions {
		s.Exempt = append(s.Exempt, id)
	}
	sort.Strings(s.Parked)
	sort.Strings(s.Exempt)
	return s
}

func (g *autorunGate) isParked(id string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	_, ok := g.parked[id]
	return ok
}

// await parks the calling loop until the freeze lifts.
//
// It is called at the top of an iteration — after the previous one committed
// and pushed, before the next one spends anything — so a parked loop is holding
// no uncommitted work and its worktree matches what it pushed. That is the
// whole reason the barrier sits here and not at an arbitrary point.
//
// ctx stays live while parked, so autorun_stop and autorun_stop_all still reach
// a frozen loop. A freeze must never make the fleet unkillable.
func (g *autorunGate) await(ctx context.Context, id string, onPark func()) error {
	announced := false
	for {
		g.mu.Lock()
		// The repair loop runs UNDER the freeze it is fixing main for. Checked
		// before paused, or ship deadlocks against its own barrier.
		if g.exemptions[id] {
			delete(g.parked, id)
			g.mu.Unlock()
			return nil
		}
		if !g.paused {
			delete(g.parked, id)
			g.mu.Unlock()
			return nil
		}
		if _, ok := g.parked[id]; !ok {
			g.parked[id] = time.Now().UTC()
		}
		ch := g.resumed
		g.mu.Unlock()

		// Announce once, and only after we know we are really parking. Logging
		// before the paused check would write a park line on every iteration of
		// an unfrozen machine.
		if !announced {
			announced = true
			if onPark != nil {
				onPark()
			}
		}
		select {
		case <-ctx.Done():
			g.mu.Lock()
			delete(g.parked, id)
			g.mu.Unlock()
			return ctx.Err()
		case <-ch:
		}
	}
}

// autorunDrainState reports, for one machine, which running loops have reached
// the gate and which are still mid-iteration. This is the honest answer to "is
// it safe to deploy yet?" — see the autorunGate doc comment.
type autorunDrainState struct {
	Paused   bool     `json:"paused"`
	Reason   string   `json:"reason,omitempty"`
	Parked   []string `json:"parked"`
	Draining []string `json:"draining"`
	// Drained is true when every running loop has reached the gate. Only then
	// does the working tree on this machine correspond to what is on the remote.
	Drained bool `json:"drained"`
}

// autorunDrain cross-references the freeze against live sessions. A session
// that is running but not parked is still inside its kick and may yet push.
func autorunDrain() autorunDrainState {
	gate := autorunFreeze.state()
	parkedSet := map[string]bool{}
	for _, id := range gate.Parked {
		parkedSet[id] = true
	}
	exemptSet := map[string]bool{}
	for _, id := range gate.Exempt {
		exemptSet[id] = true
	}
	d := autorunDrainState{Paused: gate.Paused, Reason: gate.Reason, Parked: []string{}, Draining: []string{}}
	autorunSessions.mu.RLock()
	for id, s := range autorunSessions.sessions {
		if s.Status != "running" {
			continue
		}
		// An exempt run is ship's own repair loop. Counting it as draining would
		// make drained permanently false — ship would wait for the very loop it
		// started, which is only allowed to run because ship started it.
		if exemptSet[id] {
			continue
		}
		if parkedSet[id] {
			d.Parked = append(d.Parked, id)
		} else {
			d.Draining = append(d.Draining, id)
		}
	}
	autorunSessions.mu.RUnlock()
	sort.Strings(d.Parked)
	sort.Strings(d.Draining)
	d.Drained = len(d.Draining) == 0
	return d
}

// autorunAwaitDrain blocks until every running loop has parked, or the deadline
// passes. It returns the final drain state either way rather than an error on
// timeout: a partial drain is a fact the caller has to decide about, not a
// failure. A caller that deploys anyway ships everything through the last
// completed iteration, and the in-flight one lands on the next ship.
func autorunAwaitDrain(ctx context.Context, timeout time.Duration) autorunDrainState {
	deadline := time.Now().Add(timeout)
	for {
		d := autorunDrain()
		if d.Drained {
			return d
		}
		if time.Now().After(deadline) {
			return d
		}
		select {
		case <-ctx.Done():
			return autorunDrain()
		case <-time.After(autorunDrainPollInterval):
		}
	}
}

const autorunDrainPollInterval = 2 * time.Second
