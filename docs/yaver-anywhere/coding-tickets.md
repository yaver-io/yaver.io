# Yaver Anywhere — Codex Coding Handoff (Tickets)

Last updated: 2026-06-17
Context docs (read for "why"):
[Architecture](build-handoff-ws-a-g.md) ·
[Edge fleet / colo / trust](edge-fleet-colo-donate.md) ·
[Rollout & CapEx](rollout-and-capex.md)

> **For the implementing agent (Codex).** Each ticket is self-contained: context →
> exact change → tests → verification → done. Anchors verified against the repo on
> 2026-06-17 — **re-grep before editing**, other commits move line numbers
> (`CLAUDE.md` rule: code is the source of truth).
>
> **Global guardrails (non-negotiable):**
> - **Do NOT commit or push without explicit user permission.** Work in the tree;
>   if you must branch, branch off `main`, never push.
> - Respect the Convex **privacy contract** (`desktop/agent/convex_privacy_test.go`):
>   no secrets, tokens, absolute paths, or stream/user data into Convex payloads.
>   New sync fields must be added to `fieldsWeForbidInAnyConvexPayload` coverage +
>   a test.
> - Go tests: scope them (`go test -run TestX ./...` or a single package) — a full
>   `desktop/agent` run hits the macOS login keychain (vault/auth tests) and spams
>   prompts. See `desktop/agent/*_test.go` for the real-HTTP-server, no-mocks pattern.
> - Tabs, `gofmt`, match surrounding style. TS/React: functional components + hooks.

---

## Ticket 1 — WS-A: Wire TURN into the interactive WebRTC path  ⭐ DO FIRST

**Priority:** highest. This is the single load-bearing fix for the entire "Anywhere"
thesis. Until it lands, interactive remote sessions only connect on the same LAN /
Tailscale; off-network (CG-NAT, cellular, different WiFi) silently fails to connect.

### 1.1 Problem (verified)

The interactive session PeerConnection is built with an **empty ICE config**, so it
never offers STUN/TURN candidates:

`desktop/agent/remote_runtime_webrtc.go:257`
```go
pc, err := webrtc.NewPeerConnection(webrtc.Configuration{})
```

Meanwhile the **stream-source** path already does it correctly,
`desktop/agent/stream_webrtc.go:207`:
```go
pc, err := webrtc.NewPeerConnection(webrtc.Configuration{ICEServers: iceServersForPeer()})
```

`iceServersForPeer()` (`stream_webrtc.go:37`, same package — **no import needed**)
returns a public STUN entry always, plus the relay's TURN when
`YAVER_TURN_URL` + `turnAuthSecret()` (`turn_credentials.go:113`, reads
`TURN_AUTH_SECRET`/`RELAY_PASSWORD`) are set. The browser already fetches the matching
config from `GET /stream/webrtc/ice` (`handleRemoteRuntimeTURNCredentials`,
`turn_credentials.go:52`), so once the agent offers TURN candidates too, both peers
agree on the relay candidate and off-network viewing works.

### 1.2 The change (one line)

In `desktop/agent/remote_runtime_webrtc.go`, in `ApplyWebRTCOffer`, line ~257:

```diff
-	pc, err := webrtc.NewPeerConnection(webrtc.Configuration{})
+	pc, err := webrtc.NewPeerConnection(webrtc.Configuration{ICEServers: iceServersForPeer()})
```

This single PC construction is shared by **both** the RTP-H.264 and the
JPEG-data-channel interactive transports (it sits before the `negotiatedTransport`
branch), so one change fixes both.

### 1.3 Do NOT touch these

The other `NewPeerConnection(webrtc.Configuration{})` matches are **test clients** on
loopback and must stay empty:
- `remote_runtime_webrtc_test.go` (lines ~73, 153, 229, 281, 354)
- `remote_runtime_browser_integration_test.go` (~101)

Before editing, re-confirm the production site is the only one:
```bash
cd desktop/agent && grep -rn "NewPeerConnection(webrtc.Configuration{" .
```
Only the non-`_test.go` hit gets the change. (If a new prod call site has appeared in
`ghost_stream.go` / another remote-runtime file, apply the same `ICEServers:` fix
there too.)

### 1.4 Tests to add

New file `desktop/agent/turn_interactive_test.go`:

1. **`iceServersForPeer` shape:**
   - With no TURN env → returns exactly one STUN entry (default
     `stun:stun.l.google.com:19302`), no credentials.
   - With `YAVER_TURN_URL` + `TURN_AUTH_SECRET` set (use `t.Setenv`) → returns STUN +
     a TURN entry whose `Username`/`Credential` are non-empty (long-term creds minted).
   - With `YAVER_TURN_URL` set but secret empty → STUN-only (no panic).
2. **Owner-only TURN minting (regression guard):** `handleRemoteRuntimeTURNCredentials`
   / `GET /stream/webrtc/ice` must reject a guest/stream-scoped token and only mint for
   the owner (a guest must not be able to relay arbitrary UDP through the operator's
   bandwidth). Assert the existing guard still holds. Follow the real-HTTP-server test
   pattern in the package.

Run scoped:
```bash
cd desktop/agent && go test -run 'TestTURN|TestIceServers' ./... 2>&1 | tail -20
```

### 1.5 Manual verification (the real DoD)

Off-network proof, using one of the user's 4 GB laptops as the relay+TURN host:
1. On Laptop-1 run the relay with a password, and start the agent with
   `YAVER_TURN_URL=<relay-turn-url>` and the shared `TURN_AUTH_SECRET`/`RELAY_PASSWORD`
   set, plus a managed remote session (`remote_session_start`).
2. From a **phone on cellular** (not the LAN), open the web dashboard
   `RemoteSessionView`, target that device, and start the session.
3. **Expect video to connect** with the laptop on a different network. Before the fix
   it stalls at ICE; after the fix it connects via the TURN relay candidate.
4. Confirm `RemoteSessionView.tsx` calls `GET /stream/webrtc/ice` before creating its
   offer (it should already — verify the fetch is present; if missing on any
   interactive viewer, add it).

### 1.6 Done when
- One-line prod change in `remote_runtime_webrtc.go`; test clients untouched.
- New tests pass (scoped run).
- Off-network phone→laptop interactive session connects.
- No commit/push (await user).

---

## Ticket 2 — WS-H/I: Consumer onboarding (home-hosting + prepare-for-colo)

**Priority:** after Ticket 1. **Android-only** (iOS cannot be repurposed as a headless
appliance the same way — note this and scope the iOS app to home-hosting display only).
This is **multi-PR**; ship the sub-tickets in order. 2A delivers value alone.

Background & rationale: `edge-fleet-colo-donate.md` §11–§12. The non-engineer
truth: an app **cannot** "delete personal data but keep apps" (Android sandbox), so the
two real paths are **home-hosting (no wipe)** and **guided factory reset + QR enroll
(for colo/donation)**. Do **not** build a "delete my data, keep apps" button.

Existing anchors (verified):
- On-device node: `mobile/android/app/src/main/java/io/yaver/mobile/sandbox/SandboxService.kt:99`
  runs `libyaver.so serve --port 18080`, foreground + `WakeLock` + `START_STICKY`.
- Operator mode: `desktop/agent/main.go:2146` `--operator` → `httpserver.go:46`
  `operatorMode`.
- Egress jail: `desktop/agent/egress_proxy.go:149` `isPrivateOrReserved` (RFC1918 block).
- Relay-only inbound bind: **NOT yet wired** (must add — see 2A).

### Sub-ticket 2A — "Host my assistant here" (home-hosting, no wipe) — START HERE

Goal: a non-engineer taps one button; their existing phone (all apps/data intact)
starts serving their own assistant, reachable only via relay. Zero wipe, zero trust.

- **Mobile (Android):** add a "Host my assistant on this phone" toggle that starts
  `SandboxService` in a **single-owner home-host mode** (NOT `--operator`; this serves
  only the signed-in user), shows a clear foreground notification, and surfaces status
  (online / battery / charging).
- **Agent:** add a `--relay-only` (or config flag) inbound mode: bind the HTTP/QUIC
  listeners to loopback only + relay tunnel, **no LAN-reachable listener**. This is
  isolation item **I3** and is required for any phone that serves. Reuse the egress
  jail; add the inbound bind restriction.
- **Control plane:** register the phone as the user's device (existing device-registry
  path); store metadata only (no paths/secrets — privacy contract).
- **Tests:** agent in `--relay-only` exposes no non-loopback listener (assert bind
  surface); home-host serves the owner and rejects other identities.
- **DoD:** install app → tap host → from another network the user reaches their own
  assistant via relay; phone keeps all its apps/data; nothing wiped.

### Sub-ticket 2B — "Prepare this phone" wizard (guided factory reset)

Goal: non-engineer makes a phone safe to send for colo/donation. On-phone, no PC.

- **Mobile:** a wizard with a checklist (back up · sign out of Google/accounts · we
  will erase this device), then **deep-link to Settings → Reset**. A normal app cannot
  reset programmatically (needs Device Owner — 2C); this step is guided, not automated.
- Copy must be honest: this **erases the device** (apps included); on Android 10+ it
  **cryptographically destroys** the data (FBE keys gone). Do not imply "keep apps."
- **DoD:** wizard walks a non-technical user to a completed factory reset with a clear
  pre-erase checklist.

### Sub-ticket 2C — QR managed enrollment (Android Enterprise / Device Owner) — SPIKE FIRST

Goal: after reset, the user scans a Yaver QR in the setup wizard → phone becomes a
managed, kiosk-locked Yaver appliance. This is the standard EMM path
(Android Management API / Device Owner provisioning).

- **Spike (do this before building):** evaluate Android Management API vs DIY Device
  Owner (`DEVICE_OWNER` via QR provisioning intent
  `android.app.action.PROVISION_MANAGED_DEVICE` / zero-touch). Report: enrollment UX,
  whether it needs a Google Enterprise account, and the minimal path to **kiosk
  lock-task + remote wipe**. Write findings into `edge-fleet-colo-donate.md` §12.
- **Then build:** QR generate (control plane) + the provisioning flow that auto-installs
  only the node app and enrolls hardware-backed keys (attestation, §11.3).
- **DoD:** scanning the QR on a freshly-reset phone yields a single-app-kiosk Yaver
  appliance, no PC/terminal/root.

### Sub-ticket 2D — Appliance hardening (Device Owner powers)

After 2C: kiosk lock to the node app, enforce FDE, remote-wipe, and (donation only) a
Yaver-side **verified intake wipe + signed "data destroyed" certificate**. Maps to
edge-fleet WS-I.

- **DoD:** a colocated appliance is locked to the node, encrypted, remote-wipeable;
  donation flow produces a wipe certificate.

### Ticket 2 sequencing
`2A (home-host, ship-alone) → 2B (reset wizard) → 2C (SPIKE then QR enroll) → 2D (hardening)`

---

## Suggested order across both tickets

1. **Ticket 1** (TURN) — small, unblocks everything, verifiable on owner hardware.
2. **Ticket 2A** (home-hosting) — the non-engineer default; depends on the `--relay-only`
   bind which is also isolation item I3.
3. **Ticket 2B**, then **2C spike → 2C → 2D**.

Stop and report after each ticket; do not commit or push without explicit user
permission.
