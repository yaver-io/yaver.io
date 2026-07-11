package main

// park_check.go — the agent's half of idle → scale-to-zero (cost). It DECIDES,
// it never executes: a box cannot cleanly delete the server it is running on, so
// the control plane polls `machine_park_check` and, on an "execute" verdict,
// calls machine_down itself (snapshot + delete). This keeps the dangerous half
// (the actual teardown) on the always-on control plane, and the box only reports
// whether it is idle.
//
// The policy is the tested `scaleToZeroDecision` (hosting_tier.go): managed/byo
// only, idle + grace-confirm. Self-hosted always returns skip.
//
// Config (env, optional):
//
//	YAVER_PARK_IDLE_MIN   idle minutes before arming the park (default 30)
//	YAVER_PARK_GRACE_MIN  grace minutes after the notify       (default 2)

import (
	"encoding/json"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

var (
	parkMu sync.Mutex
	// parkLastActiveAt is the last moment the box did real work — a live runner
	// session or a runner_turn. Polling machine_park_check is deliberately NOT
	// activity (else the control plane's own polling would keep the box awake
	// forever). Zero value means "never active since boot" → treated as active
	// at boot so a just-started box isn't parked before it's used.
	parkLastActiveAt   = time.Now()
	parkNotifiedAt     time.Time // when a park notification was last armed
	parkKeepAliveUntil time.Time // user held the box open until here
)

// touchParkActivity marks the box as doing real work now — resets the idle
// clock and cancels any armed park notification. Called from the runner path.
func touchParkActivity() {
	parkMu.Lock()
	parkLastActiveAt = time.Now()
	parkNotifiedAt = time.Time{}
	parkMu.Unlock()
}

// parkEnvMinutes reads a non-negative minutes value from env, or a default.
func parkEnvMinutes(key string, def int) time.Duration {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			return time.Duration(n) * time.Minute
		}
	}
	return time.Duration(def) * time.Minute
}

// durSince is now.Sub(t), or 0 when t is the zero time (never set).
func durSince(now, t time.Time) time.Duration {
	if t.IsZero() {
		return 0
	}
	return now.Sub(t)
}

func init() {
	registerOpsVerb(opsVerbSpec{
		Name:        "machine_park_check",
		Description: "Decide whether THIS box should scale-to-zero now (idle + grace-confirm). Returns phase=skip|notify|execute plus the inputs. Managed/byo boxes only — a self-hosted box always returns skip (Yaver never power-manages the customer's own machine). The CONTROL PLANE polls this and, on 'execute', calls machine_down (the box can't delete itself); on 'notify' it should warn the user before the next poll can return 'execute'. Read-only; changes no server.",
		Schema: map[string]interface{}{
			"type":                 "object",
			"properties":           map[string]interface{}{},
			"additionalProperties": false,
		},
		Handler:    opsMachineParkCheckHandler,
		Streaming:  false,
		AllowGuest: false,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "machine_keepalive",
		Description: "Cancel a pending auto-park and hold this box awake for one grace window (say this when you got the 'parking soon' warning and want to keep working). Idempotent.",
		Schema: map[string]interface{}{
			"type":                 "object",
			"properties":           map[string]interface{}{},
			"additionalProperties": false,
		},
		Handler:    opsMachineKeepAliveHandler,
		Streaming:  false,
		AllowGuest: false,
	})
}

func opsMachineParkCheckHandler(_ OpsContext, _ json.RawMessage) OpsResult {
	cfg, _ := LoadConfig()
	tier := resolveLocalHostingTier(cfg)
	idleTimeout := parkEnvMinutes("YAVER_PARK_IDLE_MIN", 30)
	graceWindow := parkEnvMinutes("YAVER_PARK_GRACE_MIN", 2)

	now := time.Now()
	activeSessions := len(listRunnerPTYSessions())
	// A live session IS activity — keep the idle clock pinned so we never park
	// mid-run, and clear any armed notify.
	if activeSessions > 0 {
		touchParkActivity()
	}

	parkMu.Lock()
	lastActive := parkLastActiveAt
	notifiedAt := parkNotifiedAt
	keepUntil := parkKeepAliveUntil
	parkMu.Unlock()

	in := ScaleToZeroInput{
		Tier:           tier,
		ActiveSessions: activeSessions,
		IdleFor:        durSince(now, lastActive),
		IdleTimeout:    idleTimeout,
		GraceNotified:  !notifiedAt.IsZero(),
		GraceFor:       durSince(now, notifiedAt),
		GraceWindow:    graceWindow,
		KeepAlive:      now.Before(keepUntil),
	}
	phase := scaleToZeroDecision(in)

	// Arm the grace clock the first time we decide to notify.
	if phase == ParkNotify {
		parkMu.Lock()
		if parkNotifiedAt.IsZero() {
			parkNotifiedAt = now
		}
		parkMu.Unlock()
	}

	return OpsResult{OK: true, Initial: map[string]interface{}{
		"phase":          string(phase),
		"tier":           string(tier),
		"eligible":       tierAllowsAutoLifecycle(tier),
		"activeSessions": activeSessions,
		"idleSeconds":    int(in.IdleFor.Seconds()),
		"idleTimeoutSec": int(idleTimeout.Seconds()),
		"graceWindowSec": int(graceWindow.Seconds()),
		"keepAlive":      in.KeepAlive,
		"note":           parkPhaseNote(phase, tier),
	}}
}

func opsMachineKeepAliveHandler(_ OpsContext, _ json.RawMessage) OpsResult {
	grace := parkEnvMinutes("YAVER_PARK_GRACE_MIN", 2)
	now := time.Now()
	parkMu.Lock()
	parkKeepAliveUntil = now.Add(grace)
	parkNotifiedAt = time.Time{}
	parkLastActiveAt = now
	parkMu.Unlock()
	return OpsResult{OK: true, Initial: map[string]interface{}{
		"keptAliveForSec": int(grace.Seconds()),
		"note":            "Auto-park cancelled — this box will stay awake for the grace window.",
	}}
}

// parkPhaseNote is a one-line, speakable explanation of the verdict.
func parkPhaseNote(phase ScaleToZeroPhase, tier HostingTier) string {
	switch phase {
	case ParkNotify:
		return "This box has gone idle — warn the user it will park soon unless they keep it alive."
	case ParkExecute:
		return "Idle through the grace window — the control plane should snapshot + delete now (machine_down)."
	default:
		if !tierAllowsAutoLifecycle(tier) {
			return "Self-hosted — never auto-parked."
		}
		return "Active or not yet idle — nothing to do."
	}
}
