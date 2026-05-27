package main

// runner_auth_glass_bus.go — translates runner-auth health-loop state
// changes into typed blackbox commands so glass / phone / web
// surfaces can react without polling /runner/auth/status.
//
// Two events on the wire:
//
//   {"command":"runner_auth_required",
//    "data":{"runner":"claude","reason":"401 from anthropic.com","box":"<deviceId>"}}
//
//   {"command":"runner_auth_completed",
//    "data":{"runner":"claude","tokenHash":"sha256:…","source":"mirror:macbook-air-kivanc"}}
//
// Plus a Hermes-reload event so a glass-wearer hears / sees that the
// app they asked to launch actually came up on the phone:
//
//   {"command":"app_reloaded",
//    "data":{"slug":"sfmg","bundleId":"io.example.sfmg","screenshotUrl":"...","caption":"login visible"}}
//
// The MentraOS miniapp subscribes via /blackbox/command-stream SSE
// (existing route). The /spatial VR scene subscribes to the same
// channel and renders the screenshotUrl as a CanvasTexture-backed
// floating plane.
//
// We piggyback on s.blackboxMgr (BlackBoxManager) which is already
// wired and tested via blackbox_http.go's command-stream handler.

import (
	"sync"
	"time"
)

// runnerAuthBus is the agent-global signal channel for runner-auth
// state transitions. Initialized lazily on first use.
type runnerAuthBus struct {
	mu        sync.Mutex
	lastState map[string]bool // runner → "was valid last time we checked"
}

var runnerAuthBusOnce sync.Once
var runnerAuthBusInst *runnerAuthBus

func getRunnerAuthBus() *runnerAuthBus {
	runnerAuthBusOnce.Do(func() {
		runnerAuthBusInst = &runnerAuthBus{lastState: map[string]bool{}}
	})
	return runnerAuthBusInst
}

// BroadcastRunnerAuthState compares the current runner-auth health
// against the last-known state and emits a blackbox command if a
// transition happened (valid → invalid OR invalid → valid). Called
// from the existing runner-auth health-loop tick — safe to call on
// every poll; the bus only emits on edge.
func BroadcastRunnerAuthState(blackbox *BlackBoxManager, runner string, valid bool, reason string) {
	if blackbox == nil {
		return
	}
	bus := getRunnerAuthBus()
	bus.mu.Lock()
	prev, hadPrev := bus.lastState[runner]
	bus.lastState[runner] = valid
	bus.mu.Unlock()
	if hadPrev && prev == valid {
		return // no edge — quiet
	}
	if !valid {
		blackbox.BroadcastCommand(BlackBoxCommand{
			Command: "runner_auth_required",
			Data: map[string]interface{}{
				"runner": runner,
				"reason": reason,
				"ts":     time.Now().UnixMilli(),
			},
		})
		return
	}
	blackbox.BroadcastCommand(BlackBoxCommand{
		Command: "runner_auth_completed",
		Data: map[string]interface{}{
			"runner": runner,
			"ts":     time.Now().UnixMilli(),
		},
	})
}

// BroadcastAppReloaded fires after a Hermes-push completes on a
// paired device. Glass / phone / VR subscribers render their
// surface-appropriate confirmation: Mentra speaks "sfmg reloaded",
// /spatial pops a 3D screenshot pane, mobile shows a toast.
//
// Optional screenshotURL points at the agent's /vibe-preview/snapshot
// endpoint (existing primitive — already streams device screens for
// the feedback overlay). When empty, glass surfaces fall back to
// text-only confirmation.
func BroadcastAppReloaded(blackbox *BlackBoxManager, slug, bundleID, screenshotURL, caption string) {
	if blackbox == nil {
		return
	}
	data := map[string]interface{}{
		"slug": slug,
		"ts":   time.Now().UnixMilli(),
	}
	if bundleID != "" {
		data["bundleId"] = bundleID
	}
	if screenshotURL != "" {
		data["screenshotUrl"] = screenshotURL
	}
	if caption != "" {
		data["caption"] = caption
	}
	blackbox.BroadcastCommand(BlackBoxCommand{
		Command: "app_reloaded",
		Data:    data,
	})
}
