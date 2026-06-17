# Yaver Anywhere Runtime — Reality, Moat, and Build Sequence

Last updated: 2026-06-17

> This file was rewritten from an aspirational 9-phase plan into a reality-anchored
> one. Every "exists / partial / stub" claim below was ground-truthed against the
> code on 2026-06-17, not copied from older docs. Per `CLAUDE.md`: **code is the
> source of truth.** Re-grep before acting; fix the doc in the same change when it
> drifts.

---

## 1. Thesis

Yaver is **not** a remote-desktop tool, a cloud browser, or a device farm. Each of
those is a single modality on a single machine, and for each there is a focused
open-source project that will out-feature us on its one axis (RustDesk on screen
share, Steel/browserless on headless browsers, E2B/Browserbase on agent sandboxes,
DeviceFarmer on device count). We do not win those fights and should not pick them.

What Yaver is:

> **The identity-bound control plane that turns any compute you already own or rent
> — mac, Linux, Windows, phone, Pi, Hetzner, redroid, Apple TV, an ARM robot — into
> a streamable, agent-drivable, human-gated runtime, with approval on a glanceable
> surface, and a control plane that never holds your data.**

```text
apps / browsers / AI agents / devices run on compute you own or rent
  -> Yaver captures, streams, controls, audits, and gates them
  -> web, phone, watch, TV, car, and partner surfaces consume them
  -> the control plane stores metadata only; secrets/streams stay on your box
```

The business is the reusable runtime layer, not any single surface. But the
*go-to-market* is one buyer at a time (Section 6), because breadth is the moat and
scope-sprawl is the risk.

---

## 2. Why this is defensible (the moat)

Three properties no focused OSS clone *and* no VC-funded competitor can assemble at
once:

1. **Breadth is the moat, and it is structurally anti-VC.** The agent already exposes
   **81 ops verb families collapsing 744 specialist tools** (`desktop/agent/ops.go`).
   That breadth only makes economic sense for a bootstrapped owner-operator reusing
   one agent binary across IoT, robotics, browsers, devices, and circuits. A
   VC-funded company is *forced* to pick one vertical (Browserbase = browsers,
   period). Our breadth looks irrational to them — which is exactly why it is
   defensible.

2. **Human-in-the-loop approval is absent from every agent-runtime competitor.**
   E2B/Browserbase hand an agent a browser; none give you "approve/deny this tool
   call from your watch, with an audit trail." We already have the control gate
   (`/rd/policy`, `rdControlEnforce`), the audit log (`remotedesktop` JSONL), and a
   watch approval scaffold (`wear/`, `mobile/native-watch/`). This is the
   enterprise-grade differentiator.

3. **The privacy posture is a moat a closed competitor cannot copy.** The control
   plane (Convex) is forbidden *by CI tests* (`convex_privacy_test.go`, 40+ banned
   field names + path-leak scanner) from holding secrets, absolute paths, tokens, or
   stream data. Browserbase's business model *is* holding your session. "Bring your
   own box, we never see your data, verified in CI" is a wedge their cap table
   forbids.

Marketing line: **the runtime fabric + approval layer for agents and screens,
self-hostable, that never holds your data** — not "remote desktop," not "cloud
browser."

---

## 3. Ground-Truth State (verified 2026-06-17)

Maturity tags: **prod** = production-ish, works end-to-end; **alpha** = wired
end-to-end but unhardened; **partial** = real but OS-/path-limited; **stub** = code
exists, gated off or unpublished; **absent** = not in repo.

### 3.1 Streaming

| Component | Anchor | State | Notes |
| --- | --- | --- | --- |
| WebRTC offer/answer (stream sources) | `stream_webrtc.go` `/stream/webrtc/offer` | **prod** | `pion/webrtc v4`, H.264 via ffmpeg, adaptive tiers (source/high/balanced/saver). |
| Stream sources | `scene.go::sourceFrameJPEG`, `capture.go`, `ghost_stream.go` | **prod** | `screen`, `capture` (V4L2/AVFoundation, HDCP-black detect), `scene`, pushed frames (`/stream/push`) all wired. |
| Multi-viewer fan-out | `stream_webrtc_fanout.go` | **prod** | One encode → N peers, refcounted, last viewer stops encoder. Tested. |
| Audio (Opus) | `stream_webrtc_audio.go` | **partial** | ALSA→libopus, **Linux only**; mac/Windows silently skipped. |
| MJPEG/snapshot fallback | `ghost_stream.go`, `capture.go`, `remote_runtime_webrtc.go` | **prod** | `/ghost/stream`, `/capture/stream`, JPEG data-channel `/remote-runtime/.../frame`. |
| TURN for stream sources | `turn_credentials.go`, `/stream/webrtc/ice` | **prod** | STUN always; TURN if `YAVER_TURN_URL` + secret. |
| **TURN for interactive sessions** | `remote_runtime_webrtc.go::ApplyWebRTCOffer` | **stub** | PeerConnection built with **empty ICE config** (line ~257, comment "TURN is not wired yet"). Off-LAN interactive sessions rely on direct/Tailscale only. **This is the #1 gap — see §4.** |

### 3.2 Remote Control

| Component | Anchor | State | Notes |
| --- | --- | --- | --- |
| Input injection | `/rd/input`, `input_{darwin,linux,windows}.go` | **prod** | All 3 OSes — CGEvent / X11 XTEST / Win32 SendInput. Normalized coords. |
| Control policy + consent | `/rd/policy`, `rdControlEnforce`, `rdViewEnforce` | **prod** | Control opt-in default off; local JSON policy; JSONL audit; throttled notify. |
| D-pad mode (TV/car) | — | **absent** | Only pointer+keyboard. Would need a button-grid abstraction. |

### 3.3 Remote Session MVP (Phase 1 candidate — currently uncommitted)

| Component | Anchor | State | Notes |
| --- | --- | --- | --- |
| `remote_session_start/status/stop` ops | `desktop/agent/ops_remote_session.go` | **alpha** | Headful browser + persistent profile (`~/.yaver/remote-session/chrome-profile`) + **unmuted audio** + ghost stream + `/rd/input`. Real, not placeholder. |
| Web operator UI | `web/components/dashboard/RemoteSessionView.tsx` | **alpha** | Fully wired: device picker, WebRTC offer/ICE, control toggle→`/rd/policy`, input batching (40ms flush), audio + quality selectors. |
| Hardening | — | **gap** | No error recovery on Chrome crash, no session limits (OOM risk), no server-side rate control. Idle cleanup via 30-min browser timeout only. |

### 3.4 Managed Browser

| Component | Anchor | State | Notes |
| --- | --- | --- | --- |
| Long-lived headful browser | `browser.go`, `browser_interactive.go` | **prod** | Persistent + ephemeral profiles, proxy/egress, idle cleanup (30 min). |
| Audio control | `BrowserSessionOptions{MuteAudio}` | **prod** | Remote session passes `MuteAudio:false`; interactive default muted. |

### 3.5 Managed Cloud

| Component | Anchor | State | Notes |
| --- | --- | --- | --- |
| Hetzner provisioning | `cloud_deploy.go::hetznerCreateServer` | **partial** | Real API calls, returns IP+ID, auto-registers device. **Gated** behind active subscription OR `CLOUD_PREVIEW_OWNER_EMAIL` allowlist. |
| AWS/GCP provisioning | `scripts/build-cloud-image.sh` | **stub** | Build functions exist; never registered in provisioner; image IDs all `null`. |
| Teardown / snapshot-before-delete | `cloudMachines.ts`, `hetznerSnapshotServer` | **prod** | Mandatory snapshot before delete; orphan-reaper reconcile cron; LemonSqueezy cancel. |
| **Idle shutdown** | — | **absent** | No code turns off an idle box. **Margin landmine — see §4.** |
| **TURN egress billing** | — | **absent** | No bandwidth metering. |
| Metering framework | `backend/convex/managedMeter.ts`, `managedServices.ts` | **stub** | Ledger, markup (compute 2×, inference 1.5×…), wallet debit, per-capability opt-in — all real but **`dryRun` unless `YAVER_MANAGED_METER_LIVE=true`.** |
| Base cloud image | `ci/remote/bootstrap.sh`, `cloud-image/cloud-init/` | **stub** | Toolchain (Chrome, ffmpeg, Xvfb, Pulse) bootstrap is real and comprehensive, but **no image captured/published** (`cloud-images.json` all `null`). |
| Privacy contract | `convex_privacy_test.go` | **prod** | Enforced in CI; token stays in agent vault, never Convex. |

### 3.6 Surfaces & Licensing

| Item | State | Notes |
| --- | --- | --- |
| Web dashboard | **prod** | 55 `*View.tsx`, incl. `RemoteDesktopView`, `RemoteSessionView`, `ScreenMonitorView`, `AppleTVCellView`. |
| Mobile | **prod** | RN/Expo, iOS+Android native, remote-desktop/appletv/capture screens. |
| tvOS | **scaffold** | `tvos/` SwiftUI source, needs Xcode target + submission pipeline. |
| Wear OS / watch | **scaffold** | `wear/` Compose + `mobile/native-watch`; voice/approval protocol, not CI-wired. |
| Car | **absent** | No `/car` route. |
| Licensing split | **prod** | FSL-1.1-Apache-2.0 core + per-package Apache-2.0 SDK `LICENSE` files. **README badge still wrongly shows AGPL — fix it.** |

---

## 4. The Five Gaps Between Fabric and Product

Everything else in this doc is reuse. These five are the whole game, and four of the
five are *finishing*, not building.

1. **Wire TURN into the interactive session path.** `remote_runtime_webrtc.go`
   builds the PeerConnection with an empty ICE config — reuse `iceServersForPeer()` /
   `turn_credentials.go` already used by the stream-source path. Without this,
   "Anywhere" does not work anywhere off-LAN. **#1 unlock.**

2. **Idle shutdown for managed cloud.** A box runs until manually killed. At a 2×
   markup, one forgotten idle box eats the margin on ten active ones. Without VC,
   margin *is* survival. **#1 margin protector.**

3. **Publish one cloud image.** Provisioning boots bare Ubuntu today; "one-click
   instant runtime" needs one snapshot captured and its ID committed to
   `cloud-images.json` (Hetzner first).

4. **Flip metering live behind a real wallet.** Set `YAVER_MANAGED_METER_LIVE=true`,
   connect the existing LemonSqueezy wallet path. The hard part is built; the switch
   is off.

5. **Fix the README AGPL badge.** Trivial, but it contradicts the actual FSL license
   and will surface in any B2B/legal review. Do before anything ships.

---

## 5. Architecture Target

```text
Surface clients
  web / mobile / watch / tvOS / Android TV / car PWA
        |
        v
Yaver identity + relay + TURN + surface policy + approval gate
        |
        v
Runtime target (uniform interface: prepare/start/stop/status/streamOffer/sendInput/logs/cleanup)
  managed cloud browser/desktop | redroid/emulator | self-hosted box | real device
  | capture-card source | machine/IoT agent
        |
        v
Streams + tools
  WebRTC video/audio  ·  MJPEG/snapshot fallback  ·  input/control events
  ·  MCP/ops verbs  ·  logs/events/audit
```

### Remote Runtime Session (one product object across surfaces)

`sessionId`, `ownerUserId`, `targetDeviceId`,
`runtimeKind` (`browser|desktop|android|ios|capture|machine|agent`),
`surfaceKind` (`web|phone|watch|tv|car|guest`),
`source` (`screen|capture|scene|redroid|browser|camera`),
`url`/`app`/`command`, `videoTransport` (`webrtc|mjpeg|snapshot`),
`audioTransport` (`webrtc-opus|none`),
`controlMode` (`none|pointer-keyboard|dpad|voice|tool-only`),
`policy`, `status`, `startedAt`/`updatedAt`/`endedAt`, `metering`.

### Surface Policy (surface-aware)

- watch: approval/control only — no full desktop, no dense chat.
- TV: view-only unless a paired controller is active.
- car: audio-only unless parked/passenger-safe; never marketed as "video while driving."
- guest: view-only by default; stream token cannot call exec/vault/control.
- enterprise: no clipboard / no downloads / no local profile persistence.
- machine: e-stop gate for risky writes.

### Profile / secret locality (explicit, enforced)

Ephemeral cloud · persistent cloud · self-hosted local · enterprise-managed. Never
put browser cookies, meeting tokens, customer IPs, relay passwords, or secrets in
Convex/logs (`convex_privacy_test.go` enforces this).

---

## 6. Monetization — extreme, open-source, zero VC

The no-VC constraint is the strategy, not a handicap. No VC means **you cannot hold
compute inventory at negative margin to buy market share.** So don't. Be cashflow-
positive per transaction from day one.

### The core move: sell the control plane, not the compute

> Let customers bring their own Hetzner/AWS/box. Charge a flat per-runtime-hour or
> per-seat **control-plane fee**. Zero COGS, zero capacity pre-buy, zero capital
> requirement. Browserbase needs VC because they own the inventory. We don't have to.

FSL already makes self-hosting free for internal use and chargeable for anyone
running Yaver *as a service* — this is open-core done correctly. Keep it.

### Margin ladder (ordered by realism for a solo bootstrapper)

1. **Control-plane fee on BYO-compute — ship first.** Pure margin, no inventory risk.
2. **Hosted TURN/relay capacity, metered.** Fixing Gap #1 *creates* this product:
   self-host TURN, meter egress. Two birds — it unblocks "Anywhere" and is pure-margin
   SaaS competitors give away because they don't need it.
3. **Managed compute arbitrage (the existing meter).** 2× Hetzner markup — only safe
   *after* idle shutdown (Gap #2). Cashflow-positive per transaction.
4. **Commercial license + SLA support.** FSL enables this directly; charge for the
   right to run a competing hosted service and for support. Capital-free.
5. **B2B / OEM (Togg, enterprise).** Highest ticket, longest cycle. **Do not anchor
   on this.** Keep it in the deck, off the critical path — a 2027 outcome, not a
   bootstrapping engine.

### Licensing strategy (already implemented — keep, don't re-architect)

Per `../LICENSING.md`:

- **FSL-1.1-Apache-2.0** core: agent, relay, backend/control-plane, web, mobile/
  watch/TV clients, managed-cloud lifecycle, remote runtime/session orchestration,
  MCP server, device registry/auth. Allows self-host, audit, internal use,
  consulting, modification; blocks a direct competing hosted clone for 2 years, then
  becomes Apache.
- **Apache-2.0** SDKs (package-local `LICENSE`): `sdk/js`, `sdk/python`,
  `sdk/flutter`, `sdk/errors-js`, client protocol libs, example MCP clients.
- **Proprietary/NDA (outside repo)** only if added later: hosted fleet ops,
  abuse-detection internals, premium runtime images, OEM/Togg white-label assets.

Rule:

```text
Open the parts users need to audit, self-host, integrate, and trust.
Charge for managed compute, scale, uptime, compliance, OEM packaging, and enterprise ops.
```

New-component license defaults: `remote_session_*` ops, Remote Session UI,
phone/watch/TV clients, managed-cloud provisioner → **FSL core**. Generic
third-party viewer SDK, generated embeddable clients → **Apache-2.0** with a
package-local `LICENSE`. Togg/OEM-specific connector → commercial/NDA if
contract-specific; the generic car viewer stays FSL.

---

## 7. Build Sequence (reality-anchored)

The single sequence that turns the fabric into revenue. Everything not on this list
(car OEM, TV, watch meeting controls, signage) is **reuse-later**, explicitly cut
from the critical path — the fabric *enables* them; that is a footnote, not a
roadmap.

### Phase 0 — Close the product gaps (the five in §4)

- Wire TURN into `remote_runtime_webrtc.go` (reuse `iceServersForPeer`). Add
  `yaver doctor stream` to report missing ffmpeg / screen perm / TURN / audio.
- Implement managed-cloud idle shutdown (inactivity → snapshot-safe stop).
- Capture + publish one Hetzner image; commit its ID to `cloud-images.json`.
- Flip metering live behind LemonSqueezy wallet; keep per-capability opt-in.
- Fix README AGPL badge → FSL.

Exit gate: from a *different network*, web dashboard selects a Linux box, opens a
managed browser session, sees video + (Linux) audio, controls it, and stops cleanly
— and a non-owner account can pay for the runtime minutes.

### Phase 1 — Commit + harden the Remote Session MVP

- Land `ops_remote_session.go` + `RemoteSessionView.tsx` (currently uncommitted).
- Add: Chrome-crash detection/recovery, per-user session limit (OOM guard),
  server-side input rate cap.
- Tests: `remote_session_start` rejects non-http URLs; start spins ghost stream;
  start reuses existing session; stop closes browser; **guest token cannot call
  `remote_session_*` or `/rd/input`**; unmuted option doesn't leak into muted
  automation sessions.

Exit gate: beta-quality managed browser session, gated by control policy + audit.

### Phase 2 — One paid buyer: AI Agent Runtime (BYO-compute, control-plane pricing)

Most differentiated lane (approval gate + privacy posture), least capital-intensive.

- Agent API for browser/device sessions over the Remote Runtime Session object.
- Human-takeover demo (agent drives, human approves/overrides from web/watch).
- Session replay/audit option; enterprise policy controls (clipboard/download off).
- Control-plane usage billing; self-hosted license path.

Exit gate: a non-OEM customer pays for managed-or-BYO runtime sessions with
human-in-the-loop approval.

### Later (reuse, not now)

TV viewer (tvOS/Android TV + D-pad), car-safe `/car` PWA viewer, watch meeting
controls, Togg/OEM B2B package, device cloud (redroid farm), machine MCP packaging.
All ride the same Remote Runtime Session object — build when a buyer is in hand.

---

## 8. Data Model Additions

Local-first for sensitive runtime state; Convex stores metadata only.

- **Convex-safe**: session id, device id, runtime kind, status, non-sensitive
  timestamps, usage meters, policy ids, public package state.
- **Local/vault-only**: browser cookies/profile, private meeting URLs, raw
  frames/screenshots, audio/video data, relay passwords, TURN secrets, app
  credentials, enterprise documents.

Any new sync path must add its fields to `fieldsWeForbidInAnyConvexPayload` and a
test in `convex_privacy_test.go`.

---

## 9. Dependency Audit (for managed runtime)

**Linux managed worker** (baked in `ci/remote/bootstrap.sh`, verified present):
Chrome/Chromium, ffmpeg, PulseAudio/PipeWire, Xvfb/Wayland headful display,
fonts/codecs, libopus via ffmpeg, KVM where redroid needs it, GPU optional, systemd
supervisor. **macOS self-hosted**: screen-recording + accessibility perms, Chrome,
ffmpeg, virtual audio device (BlackHole-class), notarized agent. **Windows
self-hosted**: capture backend, SendInput injection (done), WASAPI loopback, Chrome,
ffmpeg, service autostart. **redroid/Android**: kernel/container support, GPU audit,
ADB bridge, audio-capture limits, Play-Services boundary. **iOS devices**:
Xcode/xctrace, physical pairing, capture path; no generic cloud container.

**Cloud**: relay capacity, TURN capacity+billing, opt-in object storage for
recordings, compute provisioning, region selection, snapshot/image pipeline, secrets
bootstrap, usage metering, prepaid credits, abuse guard.

---

## 10. Tests To Add (beyond §7)

- **Web**: RemoteSessionView device picker renders; Start disabled without URL;
  WebRTC failure surfaces a readable message; control toggle calls `/rd/policy`;
  input overlay serializes normalized events.
- **Relay/TURN**: ICE route returns STUN **plus TURN** when configured (covers Gap
  #1); relay path preserves stream auth; browser-session query auth stays
  path-scoped.
- **Security**: Convex privacy tests cover runtime/session additions; guest stream
  token cannot call `remote_session_*` or `/rd/input`.
- **Managed cloud**: published image contains Chrome/ffmpeg/audio stack; readiness
  endpoint passes on a fresh box; idle cleanup ends the runtime (covers Gap #2);
  metering posts a real (non-dryRun) charge when `YAVER_MANAGED_METER_LIVE=true`.

---

## 11. Metrics

**Per session**: startup latency, first-frame latency, WebRTC connection success,
audio availability, reconnect count, average bitrate, TURN bytes, runtime minutes,
CPU/mem, stop reason, user-visible failures.

**Business**: active runtimes, session minutes/user, **cloud gross margin** (the
no-VC north star), managed vs BYO-vs-self-hosted split, conversion to paid, support
tickets/session.

---

## 12. Safety / Legal Guardrails (retained)

- Car: never describe or build "video while driving"; audio-only default when parked
  state is unknown; no safety-critical vehicle control; vehicle APIs read-only first.
- Capture-card: neutral-tool position retained; **no HDCP circumvention** docs or
  features; pass through exactly what hardware gives.
- redroid/Play-Services licensing boundaries documented.
- Meeting privacy warning on managed-browser meeting sessions.

---

## 13. Non-Goals (first release)

Arbitrary APKs in cars · bypassing OEM app stores · safety-critical vehicle control ·
HDCP circumvention · full enterprise RBI parity · full MDM · always-on car video ·
generic signage CMS · holding customer secrets/streams in the control plane.

---

## 14. Open Questions

- First managed-compute provider for margin: Hetzner (default), or BYO-only to start?
- Linux cloud browser display: Xvfb, Wayland, or native headful?
- First audio stack: PulseAudio or PipeWire?
- First paid package: AI Agent Runtime (recommended) vs Cloud Browser?
- Are private meeting URLs ever allowed in Convex metadata, or strictly local?

---

## Success Definition

Working when a user can: select or provision a runtime → start a browser/app/agent
session → view/control it from web **across networks (TURN)** → stop it cleanly →
approve/continue from phone/watch → **pay for managed runtime, or run BYO/self-hosted
at a control-plane fee** — and the same Remote Runtime Session object later powers
TV, car, OEM, enterprise-browser, support, and device-cloud packages without a
rewrite.
