package main

import "testing"

// A relay tunnel that is registered but no longer forwarding used to be
// invisible: the agent could still reach the relay (so every outbound health
// probe passed), the relay still listed the tunnel, and the agent kept
// publishing relayConnected=true — while a phone, whose only route is the
// relay, could not reach the box at all. These tests pin the two behaviours
// that make that state detectable: a confirmed failure must mark the tunnel a
// zombie, and a zombie must not be advertised as a usable relay path.

func TestRoundTripStreakMarksTunnelZombieOnlyAfterConfirmation(t *testing.T) {
	const relay = "https://relay.example"
	resetRoundTripStreak(relay)
	t.Cleanup(func() { resetRoundTripStreak(relay) })

	// One failure is a flake, not a verdict — redialing on every blip would
	// churn a healthy tunnel through a transient network hiccup.
	if got := noteRoundTripOutcome(relay, false); got != 1 {
		t.Fatalf("first failure: streak = %d, want 1", got)
	}
	if anyRelayTunnelZombie() {
		t.Fatal("a single failed probe must not condemn the tunnel")
	}

	// Two in a row is a dead tunnel.
	if got := noteRoundTripOutcome(relay, false); got != roundTripHealThreshold {
		t.Fatalf("second failure: streak = %d, want %d", got, roundTripHealThreshold)
	}
	if !anyRelayTunnelZombie() {
		t.Fatalf("after %d consecutive failures the tunnel must be marked a zombie", roundTripHealThreshold)
	}
}

func TestSuccessfulRoundTripClearsZombie(t *testing.T) {
	const relay = "https://relay.example"
	resetRoundTripStreak(relay)
	t.Cleanup(func() { resetRoundTripStreak(relay) })

	noteRoundTripOutcome(relay, false)
	noteRoundTripOutcome(relay, false)
	if !anyRelayTunnelZombie() {
		t.Fatal("precondition: tunnel should be a zombie")
	}

	// A redial that works must fully rehabilitate the tunnel — otherwise the box
	// would stay marked unreachable after it had already healed.
	if got := noteRoundTripOutcome(relay, true); got != 0 {
		t.Fatalf("streak after success = %d, want 0", got)
	}
	if anyRelayTunnelZombie() {
		t.Fatal("a successful round-trip must clear the zombie flag")
	}
}

func TestRelayDataPathUsableRejectsAZombieTunnel(t *testing.T) {
	const relay = "https://relay.example"
	resetRoundTripStreak(relay)
	t.Cleanup(func() {
		resetRoundTripStreak(relay)
		relayTunnelsLive = 0
	})

	// "Registered" was the old bar for relayConnected, and it let the control
	// plane promise the phone a path that could only ever time out.
	relayTunnelsLive = 1
	if !relayDataPathUsable() {
		t.Fatal("a live, unproblematic tunnel is a usable data path")
	}

	noteRoundTripOutcome(relay, false)
	noteRoundTripOutcome(relay, false)
	if relayDataPathUsable() {
		t.Fatal("a tunnel proven unable to carry a request must not be advertised as a relay path")
	}
}
