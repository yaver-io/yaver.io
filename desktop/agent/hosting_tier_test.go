package main

import (
	"testing"
	"time"
)

func TestClassifyHostingTier(t *testing.T) {
	cases := []struct {
		managed, byo bool
		want         HostingTier
	}{
		{true, false, HostingManaged},
		{true, true, HostingManaged}, // managed wins
		{false, true, HostingBYO},
		{false, false, HostingSelfHosted}, // the fail-safe default
	}
	for _, c := range cases {
		if got := classifyHostingTier(c.managed, c.byo); got != c.want {
			t.Errorf("classify(managed=%v,byo=%v)=%q want %q", c.managed, c.byo, got, c.want)
		}
	}
}

func TestTierAllowsAutoLifecycle(t *testing.T) {
	if !tierAllowsAutoLifecycle(HostingManaged) {
		t.Error("managed must allow auto-lifecycle")
	}
	if !tierAllowsAutoLifecycle(HostingBYO) {
		t.Error("byo must allow auto-lifecycle")
	}
	if tierAllowsAutoLifecycle(HostingSelfHosted) {
		t.Fatal("SAFETY: self-hosted must NEVER allow auto-lifecycle")
	}
	if tierAllowsAutoLifecycle(HostingTier("")) {
		t.Fatal("SAFETY: unknown tier must not allow auto-lifecycle")
	}
}

func TestResolveLocalHostingTier_FailsSafe(t *testing.T) {
	// No config and no device id must both resolve to self-hosted (hands-off).
	if got := resolveLocalHostingTier(nil); got != HostingSelfHosted {
		t.Fatalf("nil cfg must fail safe to self-hosted, got %q", got)
	}
	if got := resolveLocalHostingTier(&Config{}); got != HostingSelfHosted {
		t.Fatalf("empty cfg must fail safe to self-hosted, got %q", got)
	}
}

func TestScaleToZeroDecision(t *testing.T) {
	const (
		idleTO = 30 * time.Minute
		grace  = 2 * time.Minute
	)
	base := ScaleToZeroInput{
		Tier:        HostingManaged,
		IdleTimeout: idleTO,
		GraceWindow: grace,
	}
	withIdle := func(mut func(*ScaleToZeroInput)) ScaleToZeroInput {
		in := base
		mut(&in)
		return in
	}

	// SAFETY: self-hosted never parks, even when very idle and past grace.
	if got := scaleToZeroDecision(withIdle(func(i *ScaleToZeroInput) {
		i.Tier = HostingSelfHosted
		i.IdleFor = time.Hour
		i.GraceNotified = true
		i.GraceFor = time.Hour
	})); got != ParkSkip {
		t.Fatalf("SAFETY: self-hosted must skip, got %q", got)
	}

	// Busy box (active session) → skip regardless of idle clock.
	if got := scaleToZeroDecision(withIdle(func(i *ScaleToZeroInput) {
		i.IdleFor = time.Hour
		i.ActiveSessions = 1
	})); got != ParkSkip {
		t.Fatalf("active session must skip, got %q", got)
	}

	// Idle past timeout, not yet notified → NOTIFY (arm grace).
	if got := scaleToZeroDecision(withIdle(func(i *ScaleToZeroInput) {
		i.IdleFor = idleTO + time.Minute
	})); got != ParkNotify {
		t.Fatalf("idle-past-timeout must notify, got %q", got)
	}

	// Not idle long enough → skip.
	if got := scaleToZeroDecision(withIdle(func(i *ScaleToZeroInput) {
		i.IdleFor = idleTO - time.Minute
	})); got != ParkSkip {
		t.Fatalf("not-idle-enough must skip, got %q", got)
	}

	// Notified, grace elapsed, still idle → EXECUTE.
	if got := scaleToZeroDecision(withIdle(func(i *ScaleToZeroInput) {
		i.GraceNotified = true
		i.GraceFor = grace + time.Second
	})); got != ParkExecute {
		t.Fatalf("grace elapsed must execute, got %q", got)
	}

	// Notified but keep-alive arrived → skip (user held it open).
	if got := scaleToZeroDecision(withIdle(func(i *ScaleToZeroInput) {
		i.GraceNotified = true
		i.GraceFor = grace + time.Second
		i.KeepAlive = true
	})); got != ParkSkip {
		t.Fatalf("keep-alive must cancel the park, got %q", got)
	}

	// Notified but grace window not yet elapsed → skip (wait).
	if got := scaleToZeroDecision(withIdle(func(i *ScaleToZeroInput) {
		i.GraceNotified = true
		i.GraceFor = grace - time.Second
	})); got != ParkSkip {
		t.Fatalf("mid-grace must wait (skip), got %q", got)
	}
}
