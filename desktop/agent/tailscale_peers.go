package main

// tailscale_peers.go — ask Tailscale whether it can reach a peer, instead of
// guessing.
//
// WHY. Yaver publishes a peer's 100.x address on the heartbeat and then races
// it. That address only proves the peer HAD a tailnet address when it last
// beat; it says nothing about whether this machine can reach it now. The
// 2026-07-20 connect logs are entirely this mistake: every lan-tailscale leg
// failing in the same second while the tailnet was healthy and the peer was
// green in the Tailscale app.
//
// Tailscale already knows, authoritatively and locally:
//   Online        — is the peer up
//   CurAddr/Relay — direct or DERP-relayed
//   LastHandshake — proof a path is REAL, not merely configured
//
// The agent already parses `tailscale status --json` in diagnose_checks_v2.go —
// for diagnostics only, never for a path decision. This file wires that same
// source into the decision that needs it.
//
// COST. Zero Convex, zero network: a local socket to a daemon already running,
// cached so a probe sweep cannot fork a subprocess per candidate.
//
// WHAT IT DECIDES. Deferral is a PER-PEER question — "can the incumbent carry
// THIS pair?" — that the mesh conflict guard currently answers per-host by
// scanning interfaces. That is why a mac mini on Tailscale withdraws mesh from
// a phone that is not on the tailnet at all, and the pair falls to the relay.

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"
)

// tailscalePeerReach is what the rest of the agent needs to know about one peer.
type tailscalePeerReach struct {
	Known   bool // Tailscale has this peer in its netmap at all
	Online  bool
	Direct  bool   // true = direct path, false = via DERP
	Addr    string // current tailnet address, when known
	FreshHS bool   // handshake recent enough to call the path real
	Detail  string // human-readable, for error text and doctor output
}

// Reachable is the single question the connect ladder and the mesh route
// planner ask: can the incumbent carry this pair?
//
// It is deliberately NOT gated on FreshHS, and that took a live probe to get
// right. Tailscale handshakes ON DEMAND: an idle peer that is perfectly
// reachable reports Online with an empty or stale LastHandshake, because
// nothing has sent it traffic yet. Requiring a fresh handshake here made this
// MacBook conclude the mac mini was unreachable — a peer it can reach instantly
// — which would have had mesh install a /32 and start competing with Tailscale
// for a pair Tailscale serves fine. That is the exact invariant this design
// promises not to break ("the incumbent wins ties", audit §10.3).
//
// Known+Online is what "the incumbent can carry this" means. FreshHS is kept as
// a separate CONFIDENCE signal — it distinguishes "a path is proven up right
// now" from "Tailscale will establish one when asked" — and callers that want
// the stronger claim ask for it explicitly via ProvenLive.
func (r tailscalePeerReach) Reachable() bool {
	return r.Known && r.Online
}

// ProvenLive is the stricter claim: a path is up and carrying traffic right
// now. Use for diagnostics and for "why is this slow", never for deferral —
// see the note on Reachable.
func (r tailscalePeerReach) ProvenLive() bool {
	return r.Known && r.Online && r.FreshHS
}

// tailscaleStatusJSON is the subset of `tailscale status --json` we depend on.
// Deliberately partial: Tailscale adds fields freely, and unmarshalling into a
// narrow struct is what keeps a version bump from breaking the agent.
type tailscaleStatusJSON struct {
	BackendState string `json:"BackendState"`
	Self         struct {
		Online       bool     `json:"Online"`
		TailscaleIPs []string `json:"TailscaleIPs"`
	} `json:"Self"`
	Peer map[string]struct {
		HostName      string   `json:"HostName"`
		DNSName       string   `json:"DNSName"`
		TailscaleIPs  []string `json:"TailscaleIPs"`
		Online        bool     `json:"Online"`
		CurAddr       string   `json:"CurAddr"`
		Relay         string   `json:"Relay"`
		LastHandshake string   `json:"LastHandshake"`
	} `json:"Peer"`
}

// tailscaleHandshakeFresh bounds how old a handshake may be and still count as
// proof of a live path. WireGuard rekeys well inside this; a peer that has not
// handshaken in 3 minutes is not one we should route traffic at.
const tailscaleHandshakeFresh = 3 * time.Minute

// tailscaleStatusCacheTTL keeps a candidate sweep from forking a subprocess per
// leg. Tailscale's own netcheck cadence is ~20-26 s, so anything shorter than
// that is sampling noise.
const tailscaleStatusCacheTTL = 10 * time.Second

var (
	tsStatusMu     sync.Mutex
	tsStatusCached *tailscaleStatusJSON
	tsStatusAt     time.Time
	tsStatusErr    error
)

// tailscaleBinary finds the CLI.
//
// LookPath alone is WRONG on macOS and silently so: the App Store build ships
// the binary inside the app bundle and does NOT put it on PATH, so
// `exec.LookPath("tailscale")` fails and the caller concludes "tailscale not
// installed" on the platform where the user is most likely running it. That is
// exactly what diagnose_checks_v2.go:96 does today.
func tailscaleBinary() string {
	if p, err := exec.LookPath("tailscale"); err == nil {
		return p
	}
	candidates := []string{
		"/Applications/Tailscale.app/Contents/MacOS/Tailscale",
		"/usr/local/bin/tailscale",
		"/opt/homebrew/bin/tailscale",
	}
	if runtime.GOOS != "darwin" {
		candidates = []string{"/usr/bin/tailscale", "/usr/local/bin/tailscale"}
	}
	for _, c := range candidates {
		if st, err := os.Stat(c); err == nil && !st.IsDir() {
			return c
		}
	}
	return ""
}

// tailscaleStatus returns the cached netmap, refreshing at most every TTL.
func tailscaleStatus(ctx context.Context) (*tailscaleStatusJSON, error) {
	tsStatusMu.Lock()
	defer tsStatusMu.Unlock()
	if tsStatusCached != nil && time.Since(tsStatusAt) < tailscaleStatusCacheTTL {
		return tsStatusCached, tsStatusErr
	}
	bin := tailscaleBinary()
	if bin == "" {
		tsStatusCached, tsStatusErr, tsStatusAt = nil, nil, time.Now()
		return nil, nil // not installed is not an error — it is a topology
	}
	cctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	out, err := exec.CommandContext(cctx, bin, "status", "--json").Output()
	if err != nil {
		tsStatusCached, tsStatusErr, tsStatusAt = nil, err, time.Now()
		return nil, err
	}
	var st tailscaleStatusJSON
	if err := json.Unmarshal(out, &st); err != nil {
		tsStatusCached, tsStatusErr, tsStatusAt = nil, err, time.Now()
		return nil, err
	}
	tsStatusCached, tsStatusErr, tsStatusAt = &st, nil, time.Now()
	return &st, nil
}

// tailscaleSelfOnTailnet reports whether THIS machine is on a tailnet.
// Feeds the coarse `onTailnet` hint (heartbeat-only, no extra Convex calls).
func tailscaleSelfOnTailnet(ctx context.Context) bool {
	st, err := tailscaleStatus(ctx)
	if err != nil || st == nil {
		return false
	}
	return st.BackendState == "Running" && len(st.Self.TailscaleIPs) > 0
}

// tailscalePeerReachability answers the per-peer deferral question.
//
// hint may be a tailnet IP, a hostname, or a DNS name — callers have different
// things to hand, and requiring one canonical key would just push the matching
// problem up a layer.
func tailscalePeerReachability(ctx context.Context, hint string) tailscalePeerReach {
	hint = strings.TrimSpace(strings.ToLower(hint))
	if hint == "" {
		return tailscalePeerReach{Detail: "no peer hint given"}
	}
	st, err := tailscaleStatus(ctx)
	if err != nil {
		return tailscalePeerReach{Detail: "tailscale status failed: " + err.Error()}
	}
	if st == nil {
		return tailscalePeerReach{Detail: "tailscale not installed on this machine"}
	}
	if st.BackendState != "Running" {
		return tailscalePeerReach{Detail: "tailscale is installed but not running (" + st.BackendState + ")"}
	}
	for _, p := range st.Peer {
		match := strings.EqualFold(p.HostName, hint) ||
			strings.EqualFold(strings.TrimSuffix(p.DNSName, "."), hint) ||
			strings.HasPrefix(strings.ToLower(p.DNSName), hint+".")
		if !match {
			for _, ip := range p.TailscaleIPs {
				if strings.EqualFold(ip, hint) {
					match = true
					break
				}
			}
		}
		if !match {
			continue
		}
		r := tailscalePeerReach{Known: true, Online: p.Online, Direct: p.Relay == "" && p.CurAddr != ""}
		if len(p.TailscaleIPs) > 0 {
			r.Addr = p.TailscaleIPs[0]
		}
		if ts, perr := time.Parse(time.RFC3339, p.LastHandshake); perr == nil {
			r.FreshHS = time.Since(ts) < tailscaleHandshakeFresh
		}
		switch {
		case !p.Online:
			r.Detail = "tailscale knows this peer but reports it offline"
		case !r.FreshHS:
			// Online but idle. Tailscale handshakes on demand, so this is a
			// perfectly usable peer — NOT a reason to route around it.
			r.Detail = "tailscale has this peer online (idle — it handshakes on demand)"
		case r.Direct:
			r.Detail = "tailscale reaches this peer directly at " + r.Addr
		default:
			r.Detail = "tailscale reaches this peer via DERP (" + p.Relay + ")"
		}
		return r
	}
	return tailscalePeerReach{Detail: "tailscale is running but does not know this peer"}
}

// meshShouldRoutePeer is the per-peer deferral decision (audit §9.1/§11.1).
//
// Yaver Mesh is a FALLBACK overlay, not a competitor: if the incumbent tunnel
// already carries this pair, mesh must not install a route for it. But when the
// incumbent cannot reach the peer — the hybrid case, e.g. a mac mini on
// Tailscale and a phone that is not — mesh should serve that pair rather than
// stand down globally because some OTHER peer is on the tailnet.
//
// Returns (shouldRoute, why) so the caller can log the reason; a silent
// deferral is indistinguishable from a bug.
func meshShouldRoutePeer(ctx context.Context, peerHint string) (bool, string) {
	r := tailscalePeerReachability(ctx, peerHint)
	if r.Reachable() {
		return false, "deferring to tailscale: " + r.Detail
	}
	return true, "incumbent cannot carry this peer (" + r.Detail + ") — mesh may route it"
}
