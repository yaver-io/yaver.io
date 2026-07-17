package main

import (
	"context"
	"strings"
	"testing"
)

// stubShipMachineCall intercepts the fan-out seam so the suite never touches a
// real device.
func stubShipMachineCall(t *testing.T, fn func(machine, verb string) error) func() {
	t.Helper()
	orig := shipCallVerb
	shipCallVerb = func(_ context.Context, _ *HTTPServer, machine, verb string, _ interface{}) error {
		return fn(machine, verb)
	}
	return func() { shipCallVerb = orig }
}

// This machine is always frozen even when the user named only the mini: the
// deploy runs here, and a local autorun would be committing into the very tree
// being deployed.
func TestShipFreezeTargetsAlwaysIncludesLocal(t *testing.T) {
	got := shipFreezeTargets(shipOptions{FreezeMachines: []string{"mini"}})
	if len(got) != 2 || got[0] != "local" || got[1] != "mini" {
		t.Fatalf("freeze targets = %v, want [local mini]", got)
	}
}

func TestShipFreezeTargetsDedupesAndDropsBlanks(t *testing.T) {
	got := shipFreezeTargets(shipOptions{FreezeMachines: []string{"mini", "", "mini", "local", "  ", "hetzner"}})
	if len(got) != 3 || got[0] != "local" || got[1] != "mini" || got[2] != "hetzner" {
		t.Fatalf("freeze targets = %v, want [local mini hetzner]", got)
	}
}

func TestShipFreezeTargetsLocalOnlyByDefault(t *testing.T) {
	got := shipFreezeTargets(shipOptions{})
	if len(got) != 1 || got[0] != "local" {
		t.Fatalf("freeze targets = %v, want [local]", got)
	}
}

// Freezing three of four machines is not a partial success, it is a false sense
// of one: the fourth keeps pushing straight into the deploy.
func TestShipFreezeAllAbortsOnAnUnreachableMachine(t *testing.T) {
	restore := stubShipMachineCall(t, func(machine, verb string) error {
		if machine == "mini" {
			return errUnreachable{}
		}
		return nil
	})
	defer restore()

	states, err := shipFreezeAll(t.Context(), nil, shipOptions{FreezeMachines: []string{"mini"}})
	if err == nil {
		t.Fatal("an unreachable freeze target must abort the ship, not deploy alongside it")
	}
	if !strings.Contains(err.Error(), "mini") {
		t.Fatalf("the error must name the machine that could not be frozen: %v", err)
	}
	// The local freeze that DID land must still be reported, so the caller can
	// thaw it on the way out.
	if len(states) != 2 || !states[0].OK || states[1].OK {
		t.Fatalf("states = %+v; want local ok, mini failed", states)
	}
}

// Whatever did freeze must be thawed when the ship aborts. A fleet frozen
// forever is worse than a failed deploy.
func TestShipThawAllLiftsOnlyWhatWasActuallyFrozen(t *testing.T) {
	var thawed []string
	restore := stubShipMachineCall(t, func(machine, verb string) error {
		if verb == "autorun_resume_all" {
			thawed = append(thawed, machine)
		}
		return nil
	})
	defer restore()

	shipThawAll(t.Context(), nil, shipOptions{}, []shipMachineState{
		{Machine: "local", OK: true},
		{Machine: "mini", OK: false}, // never froze — nothing to lift
		{Machine: "hetzner", OK: true},
	})
	if strings.Join(thawed, ",") != "local,hetzner" {
		t.Fatalf("thawed %v; must lift exactly the machines that were frozen", thawed)
	}
}

type errUnreachable struct{}

func (errUnreachable) Error() string { return "device offline" }
