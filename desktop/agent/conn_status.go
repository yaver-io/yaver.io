package main

// conn_status.go — publish this device's connectivity shape to Convex, on the
// heartbeat, only when it changes.
//
// WHAT THIS IS FOR. When a device loses its path, its peers need to know what
// it WANTS, not just that it went quiet: a box that is actively seeking should
// have its probes answered, and a box that is deliberately shutting down should
// stop everyone else's backoff immediately instead of absorbing retries until
// they exhaust. Silence cannot distinguish those, so intent is published.
//
// BILL DISCIPLINE — the reason this file is as small as it is.
// Convex charges per function call, so:
//   1. It rides the EXISTING heartbeat. No new table, no new mutation, no
//      poller, no subscription.
//   2. It is sent ONLY when a field actually changes (DeviceConnStatus.Changed
//      deliberately ignores the timestamp — comparing it would make every beat
//      a write, which is exactly the bill this avoids).
//   3. Scalars only. No endpoints, no candidates, no addresses: those are both
//      volatile and forbidden by the Convex privacy contract. Exact addresses
//      travel peer-to-peer over the relay (mesh/disco.go); Convex holds only
//      the SHAPE of the network.
//
// A device that is up and unchanged adds ZERO writes and ZERO calls.

import (
	"context"
	"sync"
	"time"

	"github.com/yaver-io/agent/mesh"
)

var (
	connStatusMu   sync.Mutex
	connStatusLast *mesh.DeviceConnStatus
	// connStatusIntent is set by the connect/mesh layers when the device's
	// posture changes (lost a path, going down, relay-only). Defaults to
	// online, which is the honest answer for a box nobody has told us about.
	connStatusIntent = mesh.IntentOnline
	connStatusTier   = "relay"
)

// SetConnIntent records what this device wants right now. Cheap and
// non-blocking: it only marks state, and the next heartbeat publishes it if it
// actually differs.
func SetConnIntent(intent mesh.ConnIntent, tier string) {
	connStatusMu.Lock()
	connStatusIntent = intent
	if tier != "" {
		connStatusTier = tier
	}
	connStatusMu.Unlock()
}

// currentConnStatus builds the status from live local sources.
//
// Nothing here touches the network or Convex: tailscale state comes from a
// cached local daemon query, and the mesh verdict from the same conflict
// detector the status command uses.
func currentConnStatus(ctx context.Context) mesh.DeviceConnStatus {
	connStatusMu.Lock()
	intent, tier := connStatusIntent, connStatusTier
	connStatusMu.Unlock()

	onTailnet := tailscaleSelfOnTailnet(ctx)

	// meshOK means the data plane is genuinely usable — not merely compiled in.
	// A box that is deferring to an incumbent VPN reports false, because for
	// the purpose of "can a peer reach me over Yaver Mesh" that is the truth.
	meshOK := false
	if conflict, err := mesh.SubnetRouteConflict(""); err == nil && conflict == nil {
		meshOK = true
	}

	return mesh.DeviceConnStatus{
		Intent:    intent,
		Tier:      tier,
		OnTailnet: onTailnet,
		MeshOK:    meshOK,
		AtUnixMs:  time.Now().UnixMilli(),
	}
}

// connStatusForHeartbeat returns the status to attach to this beat, or nil when
// nothing changed.
//
// Returning nil is the whole point: the heartbeat omits the field entirely, the
// mutation's `!== undefined` guard leaves the stored value alone, and the beat
// costs exactly what it cost before this feature existed.
func connStatusForHeartbeat(ctx context.Context) map[string]interface{} {
	cur := currentConnStatus(ctx)

	connStatusMu.Lock()
	prev := connStatusLast
	changed := cur.Changed(prev)
	if changed {
		snapshot := cur
		connStatusLast = &snapshot
	}
	connStatusMu.Unlock()

	if !changed {
		return nil
	}
	out := map[string]interface{}{
		"intent":    string(cur.Intent),
		"tier":      cur.Tier,
		"onTailnet": cur.OnTailnet,
		"meshOk":    cur.MeshOK,
		"at":        cur.AtUnixMs,
	}
	if cur.NATClass != "" {
		out["nat"] = cur.NATClass
	}
	return out
}
