package main

import (
	"context"
	"fmt"
	"time"
)

// relay_presence_probe.go — detect a box that keeps DROPPING its own relay
// presence, the false green behind the 2026-07-20 flap incident.
//
// The postmortem (see main.go:relayWSFallbackEnabled): a stale agent failed its
// QUIC relay tunnel over to a WebSocket fallback the relay could not route, so
// the box cycled ~30s connected / ~57s "device not connected to relay" forever.
// Every existing signal stayed GREEN through it — `yaver devices` said "online"
// because the Convex heartbeat rides HTTP and is independent of the relay data
// path, and a single point-in-time relay check had a ~1-in-3 chance of catching
// the box mid-up. The truth only shows when you SAMPLE presence over time: a
// healthy box is up on every sample, a flapping box is up on some and down on
// others.
//
// This file is the reusable classifier for that. It is deliberately split from
// the sampling so the verdict logic is a pure function with a unit test — the
// sampler that feeds it (relayDataPathUsable over an interval) needs a live
// agent and is the thin part.

// RelayPresenceVerdict summarises N presence samples taken over a window.
type RelayPresenceVerdict struct {
	Samples     int    `json:"samples"`
	Up          int    `json:"up"`
	Down        int    `json:"down"`
	Transitions int    `json:"transitions"` // up↔down flips across the sample sequence
	Class       string `json:"class"`       // "stable-up" | "stable-down" | "flapping"
	Detail      string `json:"detail"`
}

// classifyRelayPresence turns a time-ordered slice of "was the relay data path
// usable?" samples into a verdict. Pure — no agent state — so it is unit-tested
// directly.
//
//   - every sample up            → stable-up   (healthy)
//   - every sample down          → stable-down (a plain outage, not a flap —
//     the box is simply not relay-connected, which other checks already name)
//   - a mix, i.e. ≥1 up AND ≥1 down → flapping (the dangerous case: it LOOKS
//     reachable often enough to pass a spot check, but cannot hold a path)
func classifyRelayPresence(samples []bool) RelayPresenceVerdict {
	v := RelayPresenceVerdict{Samples: len(samples)}
	if len(samples) == 0 {
		v.Class = "stable-down"
		v.Detail = "no samples taken"
		return v
	}
	for i, up := range samples {
		if up {
			v.Up++
		} else {
			v.Down++
		}
		if i > 0 && samples[i] != samples[i-1] {
			v.Transitions++
		}
	}
	switch {
	case v.Down == 0:
		v.Class = "stable-up"
		v.Detail = fmt.Sprintf("relay data path usable on all %d samples", v.Samples)
	case v.Up == 0:
		v.Class = "stable-down"
		v.Detail = fmt.Sprintf("relay data path down on all %d samples — the box is not relay-connected", v.Samples)
	default:
		v.Class = "flapping"
		v.Detail = fmt.Sprintf(
			"relay presence flapped: usable on %d/%d samples with %d up/down transition(s) — "+
				"the box keeps dropping its own relay tunnel. A spot check would have passed. "+
				"Update the agent (a stale WS-fallback tears the QUIC tunnel down; see relayWSFallbackEnabled) "+
				"and confirm YAVER_RELAY_WS_FALLBACK is unset.",
			v.Up, v.Samples, v.Transitions)
	}
	return v
}

// sampleRelayPresence takes `count` presence samples spaced `interval` apart by
// asking the same predicate the heartbeat publishes (relayDataPathUsable). It is
// the thin, agent-coupled half; the verdict comes from classifyRelayPresence so
// the interesting logic stays testable. Honours ctx cancellation.
func sampleRelayPresence(ctx context.Context, count int, interval time.Duration, probe func() bool) RelayPresenceVerdict {
	if count < 1 {
		count = 1
	}
	samples := make([]bool, 0, count)
	for i := 0; i < count; i++ {
		if i > 0 {
			select {
			case <-ctx.Done():
				return classifyRelayPresence(samples)
			case <-time.After(interval):
			}
		}
		samples = append(samples, probe())
	}
	return classifyRelayPresence(samples)
}
