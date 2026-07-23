package main

// doctor_webrtc_ice.go — can a WebRTC session actually CONNECT from here?
//
// ── Why this exists (2026-07-24 WebRTC audit) ────────────────────────────────
//
// doctor_webrtc.go answers "is the machinery installed?" — pion present, H.264
// extractor present, xcrun/adb on PATH, targets probeable. All of it is
// inventory, and all of it stays green on a box where no WebRTC session can
// ever connect across networks.
//
// The thing that decides that is ICE, and it was invisible. TURN is gated on
// three switches that must all be on (relay --turn-port + TURN_PUBLIC_IP, agent
// YAVER_TURN_URL, a shared secret) and a repo-wide grep found the first two set
// in documentation and tests ONLY. Without TURN the agent returns STUN-only —
// stream_webrtc.go says so in a comment: "works on same-network". Host + srflx
// candidates succeed on a LAN and behind cone NAT, and fail on symmetric NAT and
// CG-NAT, i.e. most cellular. A phone on LTE reaching a box at home is exactly
// the case that fails, and it is a headline use case.
//
// So this probe attempts the real operation: it gathers ICE candidates the same
// way a live session would, and reports WHICH KINDS came back. That single fact
// is the whole diagnosis:
//
//	host only                 -> LAN only; STUN unreachable (or blocked)
//	host + srflx              -> same-network + cone NAT; WILL FAIL on CG-NAT
//	host + srflx + relay      -> TURN live; works anywhere
//
// Deliberately reports "degraded" rather than "ok" for the srflx-only case. It
// is not broken — it genuinely works on a LAN — but calling it healthy is how
// the product ends up telling a user on cellular that everything is fine while
// nothing connects.

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/pion/webrtc/v4"
)

// ICEReachability is the verdict. Ordered worst -> best.
type ICEReachability string

const (
	ICEReachabilityNone     ICEReachability = "none"       // not even host candidates — no usable interface
	ICEReachabilityLANOnly  ICEReachability = "lan-only"   // host only; STUN never answered
	ICEReachabilityDegraded ICEReachability = "srflx-only" // works on LAN + cone NAT, fails on CG-NAT
	ICEReachabilityFull     ICEReachability = "relay-ok"   // TURN answered; works anywhere
)

// ICEProbeResult is what every surface renders.
type ICEProbeResult struct {
	Reachability ICEReachability `json:"reachability"`
	// OK is true only for relay-ok. srflx-only is deliberately NOT ok — see
	// the file header.
	OK    bool `json:"ok"`
	Host  int  `json:"hostCandidates"`
	Srflx int  `json:"srflxCandidates"`
	Relay int  `json:"relayCandidates"`
	// ServersTried echoes the ICE servers actually used, so a wrong or unset
	// YAVER_TURN_URL is visible rather than inferred.
	ServersTried   []string `json:"serversTried"`
	TURNConfigured bool     `json:"turnConfigured"`
	ElapsedMs      int64    `json:"elapsedMs"`
	Detail         string   `json:"detail"`
	Remedy         string   `json:"remedy,omitempty"`
}

// ProbeICEReachability gathers candidates against the agent's real ICE
// configuration (iceServersForPeer) and classifies what came back.
//
// It creates a PeerConnection, adds a recvonly video transceiver so the
// gatherer has something to gather for, and waits for gathering to complete or
// the timeout — whichever first. Nothing is sent anywhere; no session is
// created.
func ProbeICEReachability(ctx context.Context, wait time.Duration) ICEProbeResult {
	start := time.Now()
	if wait <= 0 {
		wait = 8 * time.Second
	}
	servers := iceServersForPeer()
	res := ICEProbeResult{Reachability: ICEReachabilityNone}
	for _, s := range servers {
		res.ServersTried = append(res.ServersTried, strings.Join(s.URLs, ","))
		for _, u := range s.URLs {
			if strings.HasPrefix(u, "turn:") || strings.HasPrefix(u, "turns:") {
				res.TURNConfigured = true
			}
		}
	}

	finish := func() ICEProbeResult {
		res.ElapsedMs = time.Since(start).Milliseconds()
		res.Reachability, res.OK, res.Detail, res.Remedy = classifyICECandidates(
			res.Host, res.Srflx, res.Relay, res.TURNConfigured)
		return res
	}

	pc, err := webrtc.NewPeerConnection(webrtc.Configuration{ICEServers: servers})
	if err != nil {
		res.Detail = "could not create a peer connection: " + err.Error()
		return finish()
	}
	defer pc.Close()

	if _, err := pc.AddTransceiverFromKind(webrtc.RTPCodecTypeVideo,
		webrtc.RTPTransceiverInit{Direction: webrtc.RTPTransceiverDirectionRecvonly}); err != nil {
		res.Detail = "could not add a video transceiver: " + err.Error()
		return finish()
	}

	done := make(chan struct{})
	var closeOnce bool
	pc.OnICEGatheringStateChange(func(s webrtc.ICEGatheringState) {
		if s == webrtc.ICEGatheringStateComplete && !closeOnce {
			closeOnce = true
			close(done)
		}
	})
	pc.OnICECandidate(func(c *webrtc.ICECandidate) {
		if c == nil {
			return
		}
		switch c.Typ {
		case webrtc.ICECandidateTypeHost:
			res.Host++
		case webrtc.ICECandidateTypeSrflx:
			res.Srflx++
		case webrtc.ICECandidateTypeRelay:
			res.Relay++
		}
	})

	offer, err := pc.CreateOffer(nil)
	if err != nil {
		res.Detail = "could not create an offer: " + err.Error()
		return finish()
	}
	if err := pc.SetLocalDescription(offer); err != nil {
		res.Detail = "could not set the local description: " + err.Error()
		return finish()
	}

	select {
	case <-done:
	case <-time.After(wait):
		// Timing out is normal when TURN is unreachable — gathering stalls on
		// the allocation attempt. Whatever arrived is still the answer.
	case <-ctx.Done():
	}
	return finish()
}

// classifyICECandidates turns a candidate census into a verdict. Pure, so the
// rule — not the networking — is what gets pinned by tests.
//
// srflx-only is deliberately NOT ok. It genuinely works on a LAN and behind cone
// NAT, so calling it healthy is tempting; that is exactly how the product ends
// up telling a user on cellular that everything is fine while nothing connects.
func classifyICECandidates(host, srflx, relay int, turnConfigured bool) (ICEReachability, bool, string, string) {
	switch {
	case relay > 0:
		return ICEReachabilityFull, true,
			"TURN answered — sessions can traverse symmetric NAT / CG-NAT", ""
	case srflx > 0:
		remedy := "no TURN server is configured, so this box is same-network only. Start the relay with --turn-port 3478 --turn-public-ip <WAN_IP> and set YAVER_TURN_URL=turn:<WAN_IP>:3478 on the agent (auth reuses RELAY_PASSWORD)"
		if turnConfigured {
			remedy = "a TURN server is configured but returned no relay candidate — check the relay is running with --turn-port and TURN_PUBLIC_IP, that UDP 3478 is open, and that the agent secret matches the relay (TURN_AUTH_SECRET or RELAY_PASSWORD)"
		}
		return ICEReachabilityDegraded, false,
			"only host + server-reflexive candidates — this works on a LAN and behind cone NAT, but a viewer on cellular or CG-NAT will fail to connect", remedy
	case host > 0:
		return ICEReachabilityLANOnly, false,
			"only host candidates — STUN never answered, so peers on other networks cannot find this box",
			"check outbound UDP is not blocked and that the STUN server is reachable (YAVER_STUN_URL, default stun:stun.l.google.com:19302)"
	}
	return ICEReachabilityNone, false,
		"no ICE candidates at all — no usable network interface",
		"the box has no route ICE can use; check network interfaces and any firewall blocking UDP entirely"
}

// iceReachabilitySummary renders one human line for the CLI/doctor output.
func iceReachabilitySummary(r ICEProbeResult) string {
	return fmt.Sprintf("ICE: %s (host=%d srflx=%d relay=%d) — %s",
		r.Reachability, r.Host, r.Srflx, r.Relay, r.Detail)
}
