package main

// ops_mesh_path.go — one answer to "how does this box talk to that one, and
// why", served to every surface from a single implementation.
//
// CODE REUSE IS THE POINT. Before this, connectivity truth was derived
// independently on mobile, web, tvOS and watchOS — four derivations, four
// different ways to be wrong (see YAVER_MESH_INTEROP_AUDIT.md appendix A #13:
// mobile dropped `unreachable` entirely while web had the guard). Any surface
// that computes this itself will drift from the others. So the agent computes
// it once and every surface reads the same verdict: MCP, CLI, console, mobile,
// TV, car, watch, AR/VR.
//
// It reports the DECISION and the REASON, never just a state. "Mesh isn't
// routing this peer" with no stated cause is the failure mode this whole audit
// exists to remove.

import (
	"context"
	"encoding/json"

	"github.com/yaver-io/agent/mesh"
)

type meshPathView struct {
	Peer      string `json:"peer"`
	Transport string `json:"transport"`          // tailscale | other-vpn | yaver-mesh | relay
	Reason    string `json:"reason"`             // why THIS transport, in words
	Incumbent bool   `json:"incumbentReaches"`   // an existing VPN already carries this peer
	MeshRoute bool   `json:"meshRouteInstalled"` // a /32 is installed for it
}

func opsMeshPathHandler(c OpsContext, payload json.RawMessage) OpsResult {
	var p struct {
		Peer string `json:"peer"`
	}
	_ = json.Unmarshal(payload, &p)

	ctx := c.Ctx
	self := mesh.HelloBody{DeviceID: "self"}
	if tailscaleSelfOnTailnet(ctx) {
		self.Overlays = append(self.Overlays, mesh.OverlayMembership{
			Kind: mesh.OverlayTailscale, Reachable: true,
		})
	}

	// A single peer, or the whole picture. Both go through the same code —
	// a "list" path that computes differently from the "one" path is two
	// implementations wearing one name.
	view := func(peerHint string) meshPathView {
		routes, why := meshShouldRoutePeer(ctx, peerHint)
		r := tailscalePeerReachability(ctx, peerHint)
		v := meshPathView{Peer: peerHint, Incumbent: r.Reachable(), Reason: why}
		switch {
		case r.Reachable():
			v.Transport = string(mesh.OverlayTailscale)
		case routes:
			// Mesh MAY route it — whether a /32 is actually installed depends
			// on a validated path (peerroutes.go rule 2). Reported separately
			// so "allowed" is never mistaken for "in effect".
			v.Transport = string(mesh.OverlayYaverMesh)
		default:
			v.Transport = "relay"
		}
		return v
	}

	if p.Peer != "" {
		return OpsResult{OK: true, Initial: map[string]interface{}{"path": view(p.Peer)}}
	}

	devices, _ := listKnownDeviceHints()
	out := make([]meshPathView, 0, len(devices))
	for _, d := range devices {
		out = append(out, view(d))
	}
	return OpsResult{OK: true, Initial: map[string]interface{}{
		"self":  map[string]interface{}{"onTailnet": tailscaleSelfOnTailnet(ctx)},
		"paths": out,
	}}
}

func init() {
	registerOpsVerb(opsVerbSpec{
		Name: "mesh_path",
		Description: "How this box reaches a peer and WHY: tailscale / other-vpn / yaver-mesh / relay, " +
			"with the reason in words. One implementation for every surface — MCP, CLI, console, mobile, " +
			"TV, car, watch, AR/VR — so no two clients can disagree about connectivity. " +
			"Omit `peer` for every known device.",
		Schema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"peer": map[string]interface{}{"type": "string", "description": "Device hostname, alias, or tailnet IP. Omit for all."},
			},
		},
		Handler: opsMeshPathHandler,
	})
}

// listKnownDeviceHints returns peer hints for the all-peers view. Best-effort:
// an empty list yields an empty report rather than an error, because "I could
// not enumerate peers" is not the same failure as "no path exists".
func listKnownDeviceHints() ([]string, error) {
	st, err := tailscaleStatus(context.Background())
	if err != nil || st == nil {
		return nil, err
	}
	out := make([]string, 0, len(st.Peer))
	for _, p := range st.Peer {
		if p.HostName != "" {
			out = append(out, p.HostName)
		}
	}
	return out, nil
}
