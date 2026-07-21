package main

// ssh_reverse_tunnel.go — Phase 3b of the out-of-band SSH channel: choosing
// native-direct vs reverse-SSH, and the autossh-grade lifecycle discipline for
// the reverse tunnel (docs/architecture/ROBUST_TRANSPORT_SSH_QUIC.md).
//
// "Yaver Mesh wraps SSH": from the Convex-signaled reachability, we either wrap
// NATIVE DIRECT SSH (a usable overlay/LAN route to the box exists — Tailscale/
// VPN/mesh) or REVERSE SSH (box behind NAT / no shared overlay: the box dials out
// and holds the tunnel at the relay). The lifecycle is deliberately NOT a tight
// loop: bounded exponential backoff, a hard attempt cap, then surface to the user
// — a cost rule (metered relay/Convex) AND an anti-abuse rule (a datacenter box
// must never hammer infra), per CLAUDE.md.

import (
	"context"
	"time"
)

type sshTransportChoice string

const (
	sshTransportNativeDirect sshTransportChoice = "native-direct"
	sshTransportReverseRelay sshTransportChoice = "reverse-relay"
)

// chooseSSHTransport decides how to reach the box's SSH control server. A direct
// route (overlay/LAN — Tailscale/VPN/Yaver-mesh, established by ssh_resolve_*)
// means native direct SSH; otherwise the box is behind NAT / no shared overlay
// and we use the reverse tunnel it holds at the relay. Pure so the selection rule
// is unit-tested; the reachability boolean comes from the same signal the data-
// plane selector uses (a real, bounded route probe — never a guess).
func chooseSSHTransport(directOverlayRouteAvailable bool) sshTransportChoice {
	if directOverlayRouteAvailable {
		return sshTransportNativeDirect
	}
	return sshTransportReverseRelay
}

// Lifecycle bounds — the whole "no high-frequency loops even in troubleshooting"
// contract in three constants.
const (
	sshReverseTunnelBaseBackoff  = 1 * time.Second
	sshReverseTunnelBackoffCap   = 30 * time.Second
	sshReverseTunnelMaxAttempts  = 8 // then surface to the user; do NOT keep spinning
	sshReverseTunnelKeepAlive    = 10 * time.Second // ServerAliveInterval equivalent
	sshReverseTunnelKeepAliveMax = 3                // ServerAliveCountMax equivalent
)

// reverseTunnelBackoff returns the wait before redial attempt `attempt`
// (1-based): exponential from base, capped. Bounded and monotonic — a dropped
// tunnel redials seconds → tens of seconds, never a busy loop. Attempts past the
// cap stay at the cap so the caller's max-attempts gate (not an ever-growing
// delay) is what stops it.
func reverseTunnelBackoff(attempt int) time.Duration {
	if attempt <= 1 {
		return sshReverseTunnelBaseBackoff
	}
	d := sshReverseTunnelBaseBackoff
	for i := 1; i < attempt; i++ {
		d *= 2
		if d >= sshReverseTunnelBackoffCap {
			return sshReverseTunnelBackoffCap
		}
	}
	return d
}

// reverseDialFunc establishes one reverse tunnel; it blocks until the tunnel
// drops (returning nil for a clean close, or an error). Injected so the
// supervisor is testable without a live relay.
type reverseDialFunc func(ctx context.Context, generation int) error

// superviseReverseTunnel runs the bounded dial→hold→redial lifecycle. Each
// successful dial that later drops resets the attempt counter (a long-lived
// tunnel that finally drops is not a "failing" tunnel); consecutive dial FAILURES
// accrue and, after sshReverseTunnelMaxAttempts, the supervisor gives up and
// returns the last error so the caller can surface it (and fall back to the data
// plane / tell the user) — it never loops forever. generation increments per
// dial so the relay can generation-replace a stale tunnel.
//
// Returns nil if ctx is cancelled (normal shutdown).
func superviseReverseTunnel(ctx context.Context, dial reverseDialFunc, sleep func(time.Duration)) error {
	if sleep == nil {
		sleep = func(d time.Duration) { time.Sleep(d) }
	}
	generation := 0
	failures := 0
	for {
		if ctx.Err() != nil {
			return nil
		}
		generation++
		err := dial(ctx, generation)
		if ctx.Err() != nil {
			return nil
		}
		if err == nil {
			// The tunnel was up and closed cleanly/after a good run — reset the
			// failure streak; a normal drop is not a failure to back off from.
			failures = 0
			continue
		}
		failures++
		if failures >= sshReverseTunnelMaxAttempts {
			return err // give up — surface, do NOT keep hammering the relay
		}
		sleep(reverseTunnelBackoff(failures))
	}
}
