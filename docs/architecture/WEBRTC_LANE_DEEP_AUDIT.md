# WebRTC Lane — Deep Audit (mobile + web UI)

**Date:** 2026-07-24
**Scope:** the WebRTC preview/streaming lane end to end — `desktop/agent/
remote_runtime_*.go`, `stream_webrtc.go`, `turn_credentials.go`, `relay/turn.go`,
mobile `app/remote-runtime.tsx`, web `RemoteRuntimeViewer.tsx` /
`RemoteSessionView.tsx`.
**Method:** source read + config/deploy grep on this machine. No live NAT
traversal test was performed — §2 states exactly what that means for confidence.

---

## 0. Headline

The WebRTC lane is **architecturally complete and operationally same-network
only**. Media, codecs, transceivers, multi-viewer fan-out, input injection and
control arbitration are all built and look sound. But the one thing that makes
WebRTC work *between networks* — a TURN relay — is **not enabled in any shipped
configuration**, and the health probe that would tell you cannot see it.

Concretely: phone on cellular → dev box behind home NAT will fail to connect,
and the product will report WebRTC as healthy while it does.

This is the same false-green class as the browser-lane blank screen
(2026-07-24): every inventory check is green, the operation is impossible.

---

## 1. Architecture as built

Two transports, negotiated per viewer:

| Mode | Trigger | Viewers | Notes |
|---|---|---|---|
| **`direct-webrtc-rtp-h264`** | viewer's offer contains `m=video` | **many** — Pion track fan-out | the real path |
| **JPEG over DataChannel** | offer has no `m=video` (legacy viewer) | **one** | `framesDC` payload too big to broadcast |

`remote_runtime_webrtc.go:29-60` is explicit that the video track outlives any
single peer, so a second viewer attaches without restarting capture. That is a
genuinely good multi-viewer design and it is the same primitive the Multiplayer
AI work needs.

**Both clients now offer a video transceiver** — `mobile/app/remote-runtime.tsx:699`
and `web/components/dashboard/RemoteRuntimeViewer.tsx:250`, both
`addTransceiver("video", {direction:"recvonly"})`. The historical "phone never
offered m=video, so every phone session silently fell back to JPEG" defect is
**fixed on both surfaces**. Verified by reading both call sites, not from memory.

Control arbitration is `ControlLease` (`remote_runtime_lease.go`): ≤1 controller,
the rest viewers, idle-steal after 60s, every mutation broadcast to peers. Also
good — and, as noted in the Multiplayer AI audit, scoped to one owner's own
devices and in-process only.

---

## 2. The TURN gap — why cross-network sessions cannot work

### 2.1 The chain, and where it breaks

Three independent switches must all be on:

| # | Switch | Where | Shipped state |
|---|---|---|---|
| 1 | relay runs a TURN server | `relay/main.go:251` — needs `--turn-port` **and** `TURN_PUBLIC_IP` | **never set** in any script/unit/workflow |
| 2 | agent advertises the TURN URL | `turn_credentials.go:77` — reads `YAVER_TURN_URL` | **never set** outside tests |
| 3 | shared secret | `TURN_AUTH_SECRET`, falling back to `RELAY_PASSWORD` | the only one that works by default |

A repo-wide grep for `YAVER_TURN_URL` / `TURN_PUBLIC_IP` / `--turn-port` across
`*.sh`, `*.yml`, `*.service`, `*.ts` returns **only documentation and tests** —
`docs/yaver-streaming-go-live.md:58-60` and
`docs/yaver-appletv-remote-control.md:1064` describe it as a manual operator
step. And the repo's own task list already says so:

> `docs/tasks/webrtc-vibe-loop-parity.md:372` — "Enabling relay TURN in
> production (`--turn-port`) — config + deploy"

listed as outstanding work.

### 2.2 What that means in practice

`stream_webrtc.go:46-48`:

```go
if turnURL == "" || secret == "" {
    return out // STUN-only; ICE tries its best (works on same-network)
}
```

The comment is honest: **same-network**. STUN gives host + server-reflexive
candidates, which succeed on a shared LAN and behind cone NATs, and fail on
symmetric NAT and CG-NAT — which is most cellular and a lot of home ISPs.

So the lane's advertised property — "universal: RN, Flutter, Swift/iOS,
Kotlin/Android; streams the app from the dev box" (`apps.tsx:695-707`) — is
universal across *stacks* but not across *networks*. A user on LTE testing a box
at home is the exact case it fails, and that is a headline use case for a
phone-first product.

**Not verified by execution.** I did not run a cross-NAT session. The claim rests
on: TURN URL absent from every shipped config, plus the code path that returns
STUN-only when it is. If someone has `YAVER_TURN_URL` exported in a shell that
launched their agent, their sessions work and this audit looks wrong for them —
which is itself the problem, because nothing surfaces which mode you are in.

### 2.3 A documented fallback that does not exist

`turn_credentials.go:71-74`:

```go
// TURN is opt-in. If the operator hasn't pointed us at one (via
// either YAVER_TURN_URL or, for a self-hosted relay, the same
// host that backs RELAY_URL), we just return STUN-only …
```

The code immediately below reads **only** `YAVER_TURN_URL`. There is no
RELAY_URL-derived path. A self-hoster who follows that comment — points
`RELAY_URL` at their relay, starts it with `--turn-port`, and expects TURN to
"just work" — silently gets STUN-only.

This is cheap to fix and would make the common self-host case correct by
default: derive `turn:<relay-host>:3478` from the configured relay when
`YAVER_TURN_URL` is unset. Same secret already flows (`RELAY_PASSWORD`).

### 2.4 Guests can never traverse NAT — even with TURN on

`turn_credentials.go:50-52`:

> Owner-only — guests on the vibing scope cannot mint TURN credentials (they'd
> be eligible to relay arbitrary UDP through the operator's bandwidth).

The reasoning is sound — TURN relays arbitrary UDP and costs the host's
bandwidth. But the consequence is structural: **a guest viewer's WebRTC session
can only ever use STUN**, so cross-account WebRTC viewing fails on any NAT that
needs a relay, permanently and by design.

That matters directly for the Multiplayer AI direction: "drop into the same live
session to watch it work" over WebRTC cannot work for a second *person* off-LAN,
no matter what else is built. If guest streaming is wanted, this needs a
deliberate answer — a metered per-guest TURN allocation, or an explicit
owner-granted permission (the same shape as the `allowWake` flag added for
wake-on-request), not silent STUN-only.

---

## 3. The probe cannot see any of this

`doctor_webrtc.go` checks:

- `pion/webrtc` present (in-tree)
- the in-tree H.264 extractor
- `xcrun` / `adb` on PATH
- per-target booleans (`ios-device`, `android-device`, `ios-simulator RTP encode`)

All of it is **inventory**. Not one check touches ICE, STUN, TURN, or attempts a
connection. So on a box with no TURN configured and a viewer on cellular, the
doctor is fully green and the session still cannot connect.

This is precisely the rule the repo keeps re-learning — *probe the real
capability, never the proxy*. The browser-lane incident this week produced
`doctor_browser_lane.go`, which drives a real browser because nothing else can
prove a page painted. WebRTC needs the equivalent: **gather ICE candidates and
report which types came back.**

The check is small and decisive, because candidate types tell you the whole
story:

| Candidates gathered | Verdict |
|---|---|
| `host` only | LAN only; no STUN reachable |
| `host` + `srflx` | same-network + cone NAT; **will fail on symmetric NAT / CG-NAT** |
| `host` + `srflx` + `relay` | TURN live — works anywhere |

A probe that gathers candidates for ~5s and reports that triple would have made
this entire audit a ten-second command. That is the shape to build.

---

## 4. Surface parity

| Surface | Offers `m=video` | Fetches TURN creds | Notes |
|---|---|---|---|
| **mobile** (`remote-runtime.tsx`) | yes (`:699`) | yes (`:684-688`), falls back to hardcoded Google STUN | RN surfaces share this screen → tablet/car/glass inherit |
| **web** (`RemoteRuntimeViewer.tsx`) | yes (`:250`) | yes (`agent-client.ts:4318`) | same fallback |
| tvOS / watchOS / Wear | — | — | native surfaces, not audited here; per the parity rule they do **not** inherit RN fixes |

Both clients hardcode `stun:stun.l.google.com:19302` as the fallback when the
credentials fetch fails. Worth noting for the privacy/no-third-party posture:
every viewer contacts Google's STUN by default. Fine technically, but it is an
external dependency in a product that otherwise prides itself on P2P and
self-hosting, and `YAVER_STUN_URL` exists precisely so a self-hoster can avoid
it — it is just never surfaced as a setting.

---

## 5. What to fix, ordered

**F1 — ICE candidate probe (P0).** `doctor_webrtc` gathers candidates and reports
`host` / `srflx` / `relay`. Refuses to report the lane healthy when only `host`
came back. This turns an invisible, network-dependent failure into a one-line
answer, and it is the "attempt the operation" fix the standing rule requires.

**F2 — derive TURN from the relay (P0).** When `YAVER_TURN_URL` is unset but a
relay is configured, default to `turn:<relay-host>:3478`. Makes the comment at
`turn_credentials.go:72` true instead of aspirational, and makes self-hosted
deploys correct by default.

**F3 — enable TURN in the managed relay deploy (P0, ops).** `--turn-port 3478
--turn-public-ip <WAN>`, port opened alongside 4433. Without this, F2 has nothing
to point at for managed users. Note the cost dimension: TURN relays media
through the relay box, so it is real egress — it belongs in the same
metered-cost thinking as everything else, not switched on unbounded.

**F4 — surface the transport mode in the UI (P1).** The viewer currently cannot
tell whether it is on a direct path or a relayed one, or whether it fell back to
JPEG-DC. A one-line badge ("direct" / "relayed" / "JPEG fallback") makes a slow
or failing session self-diagnosing.

**F5 — decide the guest-TURN question (P1).** Either an owner-granted,
metered permission (mirror `allowWake`), or documented explicitly as "guest
WebRTC is LAN-only" so nobody builds on an assumption that cannot hold.

---

## 6. Confidence

Verified by reading code and grepping every shipped config: the TURN switches,
the STUN-only fallback, the missing RELAY_URL derivation, the probe's contents,
and both clients' transceiver setup.

**Not verified by execution:** an actual cross-NAT session, the H.264 pipeline
under load, and multi-viewer fan-out with real concurrent peers. The TURN
conclusion is a configuration finding, which is exactly the kind that F1 would
turn into a measurement — and until F1 exists, neither this document nor the
product can tell any individual user which mode they are actually in.
