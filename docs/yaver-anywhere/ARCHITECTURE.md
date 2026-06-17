# Yaver Anywhere — Software Architecture

Last updated: 2026-06-17 · Part of the [handoff package](README.md)

This is the full system architecture for the Yaver Anywhere runtime. It is the
conceptual + structural reference; the per-workstream build steps with exact code
anchors live in [build-handoff-ws-a-g.md](build-handoff-ws-a-g.md) and
[edge-fleet-colo-donate.md](edge-fleet-colo-donate.md). Code is the source of truth —
re-grep anchors before editing.

## Contents
1. Purpose & scope
2. Design principles
3. System context
4. Component catalog
5. Core abstractions
6. Runtime target kinds (capability matrix)
7. Streaming subsystem
8. Control & input subsystem
9. Identity, auth & principals
10. Control-plane data model & privacy contract
11. Networking & the network jail
12. Auto-down lifecycle & the doorman
13. Edge-fleet topology
14. Trust & security architecture
15. Key sequence flows
16. Licensing boundaries per component
17. Dependency DAG & build order
18. Failure modes & degradation

---

## 1. Purpose & scope

One reusable runtime layer that lets a **surface** (web/phone/watch/TV/car) start,
view, control, and gate a **runtime** (browser/desktop/android/device/capture/agent)
running on a **target** (managed cloud / self-hosted / BYO / phone-edge / redroid),
over **WebRTC + relay/TURN**, with **MCP/ops** tool execution and **human approval**,
where the **control plane stores metadata only**. The business packages this as: AI
agent runtime, cloud browser, car/TV/screens, device cloud, machine MCP — but all ride
the *same* session object (§5).

## 2. Design principles

1. **The control plane never holds user data.** Convex stores identity, device
   registry, session bookkeeping, and usage meters — never secrets, paths, stream data,
   or app credentials. Enforced by CI (`convex_privacy_test.go`).
2. **Trust by cryptography and inspectability, not by faith.** Open source + FDE + TEE
   + attestation. A competitor whose business model is holding your data cannot match
   this; that's the moat, not a constraint.
3. **Physical single-tenancy where it's cheaper than software isolation.** A phone
   can't be safely multi-tenanted (proot ≠ security boundary, no unprivileged cgroups);
   so 1 phone = 1 user. Isolation by physics.
4. **BYO / own-compute first.** Sell the control plane; let compute be the user's,
   donated, or rented. No capital tied up in inventory (anti-VC).
5. **Breadth is the moat.** 81 ops verb families / 744 tools across one agent binary —
   only sane for a bootstrapped owner-operator; structurally un-clonable by a
   single-vertical VC competitor.
6. **Do no harm.** Network jail (relay-only + RFC1918 block), policy guard, no
   third-party abuse, killable loops, back off on blocks.
7. **Surface-appropriate.** Watch approves, TV views, car goes audio-first, guest is
   view-only. Policy is surface-aware.

## 3. System context

```text
┌──────────────── Surfaces (consume the runtime) ────────────────┐
│ web dashboard · mobile · watch (approve) · tvOS/AndroidTV · car │
└───────────────────────────────┬─────────────────────────────────┘
        ops verbs · WebRTC offer/ICE · /rd/input · approvals
                                │
┌──────────── Control plane (Convex) — METADATA ONLY ────────────┐
│ users · devices · cloudMachines · sessions · prepaidCredits ·   │
│ managedUsage/creditUsage · policy ids · crons(meter/reconcile/  │
│ idle-sweep) · doorman /wake httpAction                          │
└──────┬───────────────────────────────┬──────────────────────────┘
       │ provision/snapshot/delete       │ wake (box is DOWN)
       v (Hetzner API; token in vault)   │
┌──── Doorman (always-on, ~free) ────────┤
│ Convex httpAction: receives request,    │
│ recreates box from snapshot, holds      │
│ client until session-ready (~60s)       │
└──────┬──────────────────────────────────┘
       v
┌──── Runtime target (the box / device — ephemeral) ──────────────┐
│ Yaver agent (Go, FSL core)                                       │
│  ├ managed headful browser (browser.go) — profiles, audio        │
│  ├ ghost/capture frame buffers                                   │
│  ├ WebRTC encode + fan-out (pion v4)                             │
│  ├ /rd/input control + /rd/policy consent + JSONL audit          │
│  ├ ops/MCP verbs (81 families)                                   │
│  ├ gateway client (scoped LLM token; key never on box)           │
│  └ vault (Hetzner token, TURN secret, app creds — never synced)  │
└──────┬───────────────────────────────────────────────────────────┘
       v
  STUN + self-hosted TURN (relay/turn.go) ── relays media off-LAN
```

## 4. Component catalog

| Component | Where | Role |
| --- | --- | --- |
| **Surfaces** | `web/`, `mobile/`, `wear/`, `tvos/` | start/view/control/approve sessions |
| **Control plane** | `backend/convex/` | identity, device registry, bookkeeping, meters, crons, doorman |
| **Relay** | `relay/` | application-layer QUIC pass-through; self-hostable; never stores task data |
| **TURN** | `relay/turn.go` + `turn_credentials.go` | media relay for off-LAN; long-term creds, owner-only mint |
| **Doorman** | Convex httpAction (`http.ts`) | wake a downed box; relay is pass-through and can't, gateway is inference-only |
| **Agent** | `desktop/agent/` | the runtime host; streaming, control, ops, vault, gateway client |
| **Gateway** | `gateway/` (CF Worker) | OpenAI-compatible; holds upstream LLM key as Worker secret; mints scoped tenant tokens |
| **Vault** | `desktop/agent/vault.go` | NaCl secretbox + Argon2id, auth-token-derived key; on-box only |

## 5. Core abstractions

### Remote Runtime Session — one product object across all surfaces
`sessionId · ownerUserId · targetDeviceId · runtimeKind(browser|desktop|android|ios|
capture|machine|agent) · surfaceKind(web|phone|watch|tv|car|guest) · source(screen|
capture|scene|redroid|browser|camera) · url/app/command · videoTransport(webrtc|mjpeg|
snapshot) · audioTransport(webrtc-opus|none) · controlMode(none|pointer-keyboard|dpad|
voice|tool-only) · policy · status · startedAt/updatedAt/endedAt · metering`.

### Runtime Target — uniform interface over heterogeneous compute
`prepare · start · stop · status · streamOffer · sendInput · listAudioDevices · logs ·
cleanup`. **Auto-down/wake and isolation are implemented at this layer**, so the same
session works whether the target is a cloud VM, a container, a BYO box, a phone-edge
appliance, or redroid.

### Surface Policy — surface-aware gating
watch: approve/control only · TV: view-only unless paired controller · car: audio-only
unless parked/passenger-safe · guest: view-only, no exec/vault/control · enterprise: no
clipboard/downloads/local profile · machine: e-stop on risky writes.

### Profile & Secret locality — explicit, enforced
ephemeral cloud · persistent cloud · self-hosted local · enterprise-managed. Never put
cookies, meeting tokens, customer IPs, relay/TURN secrets, or app creds in Convex/logs.

## 6. Runtime target kinds (capability matrix)

| Target kind | Isolation | Browser/stream | Heavy compute | Always-on cost | Use |
| --- | --- | --- | --- | --- | --- |
| **Managed cloud VM** (Hetzner) | software (VM) | yes | yes | ~€4/mo (auto-down → ~€0.4–1) | paid managed / burst |
| **Container** (on anchor/donated PC) | software (cgroup+netns) | yes (≥16 GB host) | medium | shared | multi-tenant light/medium |
| **BYO / home-host** | the user's own machine | depends | depends | user's | zero-trust default |
| **Phone-edge appliance** (colo/donate) | **physical (1/user)** | no (light assistant) | no | ~€1/mo power | free tier + colo |
| **redroid / emulator** | container (Linux host) | android apps | medium | host | device cloud / QA |
| **capture-card source** | n/a (read source) | passthrough | n/a | host | TV/streaming |

Phones run the **light personal-assistant** workload (gateway LLM calls, CRUD
connectors, voice relay, routines); browser-streaming / redroid / heavy work routes to
the anchor node or a Hetzner burst box.

## 7. Streaming subsystem

Two WebRTC paths share encode/fan-out but differ in signaling:

- **Stream-source path** — `stream_webrtc.go` `/stream/webrtc/offer`. For `screen`,
  `capture`, `scene`, pushed frames. **prod**: H.264 (ffmpeg), adaptive tiers, refcounted
  multi-viewer fan-out (`stream_webrtc_fanout.go`), Opus audio on Linux
  (`stream_webrtc_audio.go`), STUN+TURN via `iceServersForPeer()`.
- **Interactive session path** — `remote_runtime_webrtc.go` `ApplyWebRTCOffer`. For
  device/browser control. RTP-H.264 or JPEG-data-channel fallback, multi-viewer. **Gap:**
  PeerConnection built with **empty ICE config** at `:257` → no TURN off-LAN → **WS-A**.
- Fallbacks: MJPEG (`/ghost/stream`, `/capture/stream`) and JPEG snapshot/data-channel
  exist everywhere.
- TURN: `relay/turn.go` colocated with the relay; `turn_credentials.go` mints long-term
  creds (owner-only) and serves `GET /stream/webrtc/ice` to viewers.

## 8. Control & input subsystem

- `/rd/input` — normalized (0..1) pointer + keyboard; native injection per OS
  (`input_darwin.go` CGEvent, `input_linux.go` X11 XTEST, `input_windows.go` SendInput).
  **prod, all 3 OSes.**
- `/rd/policy` — runtime consent: `ViewEnabled`, `ControlEnabled` (opt-in, default off),
  `AllowRemoteControl`, `NotifyOnControl`. Local JSON, never synced. `rdControlEnforce`
  gates every input.
- Audit — local JSONL of view/control/policy/deny events.
- **Human approval** — the differentiator vs E2B/Browserbase: approve/deny tool calls
  and control from web/watch with audit. (Watch approval surfaces scaffolded.)
- Missing: a D-pad/button-grid abstraction for TV/car (reuse-later).

## 9. Identity, auth & principals

- **Per-surface tokens** — each surface (web/phone/watch/CLI/agent) holds its own
  1-year session token, refreshed on heartbeat; same OAuth user across all.
- **Guest / capability-scoped tokens** — e.g. `circuit`, `stream` scopes; a guest stream
  token cannot call exec/vault/control or mint TURN. Preserve these guards in new verbs.
- **Operator / donor principal** *(net-new, operator-fleet gap A)* — a fleet/donated
  node must bind a **scoped service identity**, not a person's user token, so a leaked
  node token ≠ an account. Required before strangers touch shared/donated hardware.
- **TEE-bound colo keys** *(net-new)* — for a colocated phone, the assistant vault key is
  generated in StrongBox/TEE, non-exportable, released only on authenticated-owner
  unwrap. Yaver-the-host holds ciphertext it cannot read.

## 10. Control-plane data model & privacy contract

**Convex-safe (metadata):** users, devices, cloudMachines (+ new auto-down fields,
§12), sessions (token hashes only), prepaidCredits, managedUsage/creditUsage, policy
ids, public package state, audit summaries.

**Local/vault-only (never Convex):** browser cookies/profiles, private meeting URLs,
raw frames/audio/video, relay passwords, TURN secrets, app credentials, absolute paths,
customer LAN IPs.

**Enforcement:** all Convex writes go through `convexSyncer.callMutation`;
`convex_privacy_test.go` enumerates forbidden field names + scans for path leaks
(`/Users/`, `/home/`, …). New sync fields → add to
`fieldsWeForbidInAnyConvexPayload` coverage + a test.

New auto-down fields on `cloudMachines`: `idleTimeoutMinutes`, `idleAction`,
`wakeOnRequest`, `lastActivityAt`, `snapshotImageId`, status `+idle-stopped` — all
metadata, no secrets.

## 11. Networking & the network jail

| Layer | Port | Purpose |
| --- | --- | --- |
| HTTP | 18080 | agent API |
| QUIC | 4433 | relay tunnel + direct phone connections |
| UDP beacon | 19837 | LAN auto-discovery (auth-aware) |
| HTTPS (LAN) | 18443 | self-signed TLS for SDK clients |
| Phone HTTP | 8347 | mobile inbound for `yaver push` |

**Network jail (mandatory for shared/donated/colo nodes):**
- **Egress:** RFC1918/loopback/link-local blocked — `egress_proxy.go:149`
  `isPrivateOrReserved` (**works**). A node cannot reach the host LAN or pivot.
- **Inbound:** **relay-only bind** — loopback + relay tunnel, no LAN-reachable listener
  (**NOT yet wired** — isolation item **I3**, part of WS-G/2A). This is the single
  highest-value safety item for putting strangers on your own hardware.

## 12. Auto-down lifecycle & the doorman

Hetzner **bills powered-off servers at full rate** — only *deletion* stops billing. So
"down" = **snapshot + delete**, "wake" = **recreate from snapshot**.

State machine (`cloudMachines` status): `provisioning → active → (idle N min) →
stopping → idle-stopped → (wake) → provisioning → active`.

- **Idle sweep**: extend `cloudLifecycle.ts meterTick` to mark `active` machines past
  `idleTimeoutMinutes` of `lastActivityAt` → `requestIdleDown` → reuse the mandatory
  snapshot-before-delete decommission path → `idle-stopped` (keep `snapshotImageId`).
- **Activity** (`lastActivityAt`) is bumped on *user-facing* work (session/input/ops),
  not heartbeats; piggyback on the heartbeat payload (metadata-safe).
- **Doorman** (`POST /wake` Convex httpAction): a downed box can't receive its own wake,
  so the always-on control plane catches it, recreates from snapshot, and the client
  polls until `active` (~60–90 s) behind a "waking…" UX. Owner-only, idempotent,
  rate-limited.
- **Cost floor**: balance ≤ 0 → snapshot+delete instead of run-on.

## 13. Edge-fleet topology

```text
        Anchor node (buy ONE, ≤ €200; 16–32 GB N100 / used SFF)
        roles: relay + self-hosted TURN · allocator/control ·
               heavy/browser sessions · gateway for the phone shelf
                │ relay (QUIC), jailed             │ LAN/USB
   ┌────────────┴───────────┐         ┌────────────┴────────────┐
   │ Colocated phones (paid)│         │ Donated phones (free)    │
   │ 1 phone = 1 user       │         │ 1 phone = 1 free user    │
   │ charge relay + colo    │         │ donor earns credits      │
   └────────────────────────┘         └──────────────────────────┘
                │                                  │
         Hetzner burst (auto-down) — overflow & heavy workloads
```

Capacity grows by **colo fees (user-funded)** + **donations (community-funded)** +
**rented burst (OpEx)** — not by buying boxes. Your 3×4 GB boxes serve relay/TURN, dev,
and dogfood/isolation-pilot roles (they can't multi-tenant browsers). See
[rollout-and-capex.md](rollout-and-capex.md).

## 14. Trust & security architecture

The adoption blocker for colo/donation is "you'll hold my data." Cleared by three
principles (full treatment: [edge-fleet-colo-donate.md](edge-fleet-colo-donate.md)
§11–§12):

1. **The serving phone has zero personal data** — wiped clean appliance. Consumer path
   = guided **factory reset** (Android 10+ FBE crypto-erase) + **QR Device-Owner
   enrollment** (Android Enterprise), no PC/root. Donation adds a Yaver-side verified
   intake wipe + certificate.
2. **Yaver holds only ciphertext it cannot read** — FDE + TEE/StrongBox-sealed,
   owner-gated, non-exportable secrets + verified boot + remote attestation + open
   source + CI privacy tests. *Confidential edge computing.*
3. **Default = home-hosting (zero physical trust)** — the phone never leaves the user;
   Yaver provides only relay + control plane.

Trust ladder (user's choice): **home-host (none) → clean-appliance colo (crypto) →
donation (verified wipe).** Honest residual risk: physical access is powerful; TEE
raises the bar enormously but isn't infinite → that's why home-hosting is the zero-trust
default. Never market colo as "perfectly secure" — market "an appliance we cannot read,
and you can verify that."

**Non-interference:** phones = physical (strongest). Multi-tenant PC/container nodes =
software gate — close I7 (`--pids-limit`, disk quota, blkio, per-tenant netns vs the
current `--network host` default) and I8 (encrypted per-tenant volume, secure
zero-residue teardown) before opening PC pools (**WS-J**). Today
`container_runner.go:274` caps only `--cpus`/`--memory`.

## 15. Key sequence flows

**A) Start + view a session off-network (post WS-A):**
surface → `remote_session_start{url}` → agent opens headful browser (persistent
profile, unmuted) + starts frame buffer → surface fetches `GET /stream/webrtc/ice`
(STUN+TURN) → POST offer to interactive path → agent answers with `iceServersForPeer()`
candidates → media flows via TURN relay → input via `/rd/input` (after `/rd/policy`
opt-in).

**B) Auto-down + wake:**
idle ≥ N min → meterTick → snapshot + delete → `idle-stopped`. Later request → doorman
`POST /wake` → recreate from `snapshotImageId` → agent re-registers → `active` → client
resumes (~60 s).

**C) Colo onboarding (consumer):**
in-app prep checklist → guided factory reset (crypto-erase) → setup-wizard QR scan →
Device-Owner enrollment → node app auto-installed, kiosk-locked, FDE on, TEE keys
enrolled, attested → bound to user, relay-only → user's assistant runs; Yaver bills
relay+colo.

**D) Donation:**
donor in-app reset → ship → Yaver verified intake wipe + signed "data destroyed"
certificate → enroll as blank node → allocator assigns one free-tier user (physical
isolation) → donor earns credits.

**E) Agent action with human approval:**
agent proposes a tool call → control plane notifies surface (push/watch) → user
approves/denies (audited) → agent proceeds or halts. Human-takeover available mid-session.

## 16. Licensing boundaries per component

Per [`../../LICENSING.md`](../../LICENSING.md):
- **FSL-1.1-Apache-2.0 (core):** agent, relay, backend/control plane, web/mobile/watch/
  TV clients, managed-cloud lifecycle, remote runtime/session orchestration, doorman,
  MCP server, device registry/auth, `remote_session_*` ops, RemoteSession UI, generic
  `/car` viewer, in-repo provisioner.
- **Apache-2.0 (embeddable SDKs, package-local LICENSE):** `sdk/js|python|flutter|
  errors-js`, client protocol libs, example MCP clients, generated stubs.
- **Proprietary/NDA (outside repo, if added):** hosted fleet ops, abuse internals,
  premium runtime images, OEM/Togg white-label, contract-specific connectors.

## 17. Dependency DAG & build order

```text
A (TURN) ──┐
C (image) ─┼─> E (remote-session beta) ──> D (billing live) ──> SHIP: AI Agent Runtime
B (auto-down) ─ F (doorman) ────────────────┘   (B+F make the price honest)
G (free NUC tier) ── ISOLATION GATE (I1–I4) ──> initial customers
H/I (colo+donate+consumer onboarding) ── ride G + the gate
J (multi-tenant resource isolation) ── before opening PC pools
```

Order: **A → C → E → B → F → (G/H/I behind the gate) → D last.** Don't bill until cost
(B) and connectivity (A) are verified on a real non-owner account. Don't advertise the
free tier until the isolation gate passes. Per-WS DoD + tests:
[build-handoff-ws-a-g.md](build-handoff-ws-a-g.md) and
[edge-fleet-colo-donate.md](edge-fleet-colo-donate.md).

## 18. Failure modes & degradation

- **No TURN reachable** → ICE fails off-LAN → surface a readable error (add
  `yaver doctor stream`). Degrade to MJPEG/snapshot where possible.
- **No audio device / non-Linux** → video-only; say so explicitly.
- **Chrome crash** → detect cancelled browser context → set `lastError`, allow restart
  (WS-E hardening).
- **Box OOM** → per-user session cap (WS-E); per-tenant memory limit (WS-J) so one
  tenant can't kill the host.
- **Phone overheats / unplugged** → node heartbeat reports temp/charge → auto-drain
  (WS-I).
- **Doorman wake storm** → rate-limit per user, one in-flight wake per machine.
- **Donated node abused** → revoke operator principal → reaper wipes (zero residue).
