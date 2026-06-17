# Yaver Anywhere Runtime — Software Architecture & Build Handoff

Last updated: 2026-06-17
Companion strategy doc: [`strategy-and-reality.md`](strategy-and-reality.md)

> **For the implementing agent (Codex):** this is a build spec, not a vision doc.
> Every file path, function, and line number below was verified against the repo on
> 2026-06-17. Per `CLAUDE.md`, re-grep before editing — other threads bump line
> numbers. Each Workstream (A–E) is independently assignable and ends with a
> Definition of Done + tests. Do not commit or push without explicit user
> permission. Respect the Convex privacy contract (`convex_privacy_test.go`) on
> every new sync path.

---

## 0. What we are building (and the order)

Five gaps separate a working runtime *fabric* from a sellable *product*. They are
mostly finishing, not greenfield.

| WS | Goal | Net new vs finishing | Blocks |
| --- | --- | --- | --- |
| **A** | TURN on the interactive WebRTC path → "Anywhere" works off-LAN | finishing (1-line core + plumbing) | the whole product |
| **B** | Auto-down: idle detect → snapshot+delete → wake-on-demand → cost floor | net new (the cost story) | personal-assistant economics |
| **C** | Publish one Hetzner image → instant provision | finishing (CI sync) | "one-click runtime" |
| **D** | Metering live behind the real wallet | finishing (flip flags, verify) | charging anyone |
| **E** | Commit + harden the Remote Session MVP | finishing (hardening + tests) | beta quality |
| **F** | Doorman: catch request to a downed box, wake it, hold the client | net new (small) | auto-down UX (B) |
| **G** | Free own-hardware tier (NUC/Pi fleet) for initial customer acquisition — **isolation-first** | finishing operator-fleet gaps | the free on-ramp |

Dependency DAG:

```text
A (TURN) ──┐
C (image) ─┼─> E (remote session beta) ──> D (billing live) ──> SHIP: AI Agent Runtime
B (auto-down) ─── F (doorman) ──────────────┘   (B+F make the price defensible)
G (free NUC tier) ── gated behind ISOLATION GATE (§13.3) ──> initial customers
```

A, C, B are parallelizable. E depends on A+C. F depends on B. D depends on E+B+F
(don't bill until cost is controlled and sessions are reliable). **G must not open to
strangers until the §13.3 isolation gate fully passes** — user-data isolation is a
hard precondition, not a follow-up.

---

## 1. System architecture

```text
┌─────────────── Surfaces (consume) ───────────────┐
│  web dashboard · mobile · watch (approve) · TV    │
└───────────────────────┬───────────────────────────┘
                        │  ops verbs + WebRTC offer/ICE + /rd/input
                        v
┌──────────── Control plane (metadata only) ────────┐
│  Convex: devices, cloudMachines, prepaidCredits,   │
│  managedUsage/creditUsage, sessions  (NO secrets,  │
│  NO streams, NO paths — convex_privacy_test.go)    │
│  + crons: meterTick / reconcile / idle-sweep       │
└───────────┬───────────────────────────┬───────────┘
            │ provision/snapshot/delete   │ wake trigger
            v (Hetzner API, vault token)  │
┌──────── Doorman (always-on, ~€0) ──────┤
│  relay / CF Worker: receives request    │
│  while box is DOWN, triggers wake,      │
│  holds the client until box is ready    │
└───────────┬─────────────────────────────┘
            v
┌──────── Runtime target (the box, ephemeral) ──────┐
│  Yaver agent (FSL core) on Hetzner/BYO/redroid     │
│  ├ managed headful browser (browser.go)            │
│  ├ ghost/capture frame buffers                     │
│  ├ WebRTC encode + fan-out (pion/webrtc v4)        │
│  ├ /rd/input control + /rd/policy gate + audit     │
│  └ vault (Hetzner token, TURN secret — never sync) │
└───────────┬─────────────────────────────────────┘
            v
   STUN + self-hosted TURN (relay/turn.go) ── relays media off-LAN
```

**Key architectural insight (drives WS-B):** a box that is fully **down** cannot
receive its own wake request. You need a cheap **always-on doorman** (the relay, or
a Cloudflare Worker, or even the user's phone) that: (1) receives the trigger, (2)
calls Convex to provision-from-snapshot, (3) holds/queues the client until the box
registers and is session-ready. Without the doorman, "auto-down" means "manually
restart from the dashboard," which kills the personal-assistant UX.

---

## 2. Boot time & cost model (answers the questions)

These numbers are **approximate — verify against live Hetzner pricing/timings before
quoting a customer.** Hetzner prices move; treat as order-of-magnitude.

### 2.1 How fast can we boot? (optimization ladder)

| Strategy | Cold→session-ready | Idle cost | Notes |
| --- | --- | --- | --- |
| Create new server + run `bootstrap.sh` per boot | **5–10 min** | none (deleted) | today's path if no image published. Unacceptable for assistant. |
| **Create from published snapshot** (WS-C) | **~45–90 s** | none (deleted) | server create + boot + agent register. The realistic auto-down wake target. |
| Power-off / power-on a stopped server | **~15–30 s** | **full price** ⚠ | Hetzner **bills stopped servers at full rate** — only *deletion* stops billing. Power-cycling saves nothing. Don't rely on it for cost. |
| Warm running pool, assign on demand | **~instant** | full price × pool | only worth it at scale/SLA, not for one assistant. |
| Container / redroid golden snapshot | **~2–3 s** | tiny (shared host) | best for assistant workloads that fit a container, not a full VM. See `studio/redroid.go` `yaver-base` pattern. |
| Firecracker microVM (future, own host) | **~1–2 s** | per-second | E2B/Vercel-Sandbox class; needs our own Firecracker host, not Hetzner Cloud. |

**Practical target for v1:** publish the image (WS-C) → wake from snapshot in
**~60 s**, masked by the doorman showing "waking your assistant…". For sub-3s, the
assistant should run in a **container on a small shared always-on box**, not a
dedicated VM — recommend this as the default assistant runtime (see 2.3).

### 2.2 Can personal-assistant costs really be small? Yes — math:

Assume CAX11-class (arm64, 2 vCPU, 4 GB) ≈ **€3.79/mo** ≈ **€0.0063/hr**; snapshot
storage ≈ **€0.0119/GB·mo**; ~10–20 GB image.

- **24/7 dedicated VM, no auto-down:** ~**€3.8/mo** (floor for one always-on box).
- **Auto-down (snapshot+delete), bursty ~1 hr/day active:**
  - snapshot standing cost: 15 GB × €0.0119 ≈ **€0.18/mo**
  - runtime: 30 hr × €0.0063 ≈ **€0.19/mo**
  - **total ≈ €0.4–1/mo** (add slack for re-provision churn) → **~80% cheaper**.
- **Shared container pool (golden snapshot), per session:** fractions of a cent —
  the box is shared and always-on; the user pays only their slice of compute-seconds.
- **BYO compute (phone / Raspberry Pi / their own box):** **€0 cloud** — the agent
  runs on hardware they already own; Yaver charges only the control-plane fee.

**Honest conclusion:** dedicated-VM auto-down gets you to **~€0.4–1/mo** for a bursty
assistant, but the *structurally cheapest* assistant is **a container on a shared box
or BYO compute** — not a per-user VM. Architect WS-B so the runtime kind is
pluggable (VM | container | BYO), defaulting the assistant to container/BYO and
reserving dedicated VMs for heavy/isolated workloads.

### 2.3 Auto-down is necessary but not sufficient

Auto-down (WS-B) is what makes the *price honest* and is the single biggest margin
protector (one forgotten idle box eats ten boxes' markup). But because Hetzner bills
stopped servers, "down" must mean **snapshot + delete**, and "wake" must mean
**recreate from snapshot via the doorman**. Build it that way or it saves nothing.

---

## 3. Workstream A — TURN on the interactive path

**Status:** Code wired 2026-06-17; scoped tests pass. The remaining DoD is an
off-network phone/laptop proof using a real relay+TURN host.

**Why:** interactive sessions previously built the PeerConnection with an empty ICE
config, so they only connected on LAN/Tailscale. The stream-source path already had
working TURN, and the interactive path now reuses it.

**Anchors (verified):**
- `desktop/agent/remote_runtime_webrtc.go:257` — `pc, err := webrtc.NewPeerConnection(webrtc.Configuration{ICEServers: iceServersForPeer()})`.
- `desktop/agent/stream_webrtc.go:37` — `func iceServersForPeer() []webrtc.ICEServer` (reads `YAVER_STUN_URL`, `YAVER_TURN_URL`, `turnAuthSecret()` = `TURN_AUTH_SECRET`/`RELAY_PASSWORD`; mints long-term creds). Already used at `stream_webrtc.go:207`.
- `desktop/agent/turn_credentials.go:52` — `handleRemoteRuntimeTURNCredentials` backing `GET /stream/webrtc/ice` (httpserver.go:487). The web client already fetches this.

**Change completed:**
1. `remote_runtime_webrtc.go:257` now uses `webrtc.NewPeerConnection(webrtc.Configuration{ICEServers: iceServersForPeer()})`.
2. Grep confirmed the remaining empty `webrtc.Configuration{}` call sites are test clients.
3. `RemoteSessionView.tsx` already calls `/stream/webrtc/ice` before creating its offer.

**DoD:** From a second network (phone hotspot), web dashboard opens a managed
browser session on a Hetzner box and gets video without LAN/Tailscale.

**Tests (add `desktop/agent/turn_interactive_test.go`):**
- ICE config returned for an interactive offer contains the TURN entry when
  `YAVER_TURN_URL`+secret are set; STUN-only otherwise.
- Guest/stream-scoped token cannot mint TURN creds (owner-only — preserve existing
  guard in `handleRemoteRuntimeTURNCredentials`).

---

## 4. Workstream B — Auto-down (idle → snapshot+delete → wake)

**Why:** the cost story (Section 2). Net-new feature.

### 4.1 Schema (Convex)

`backend/convex/schema.ts` — add to the `cloudMachines` table:
- `idleTimeoutMinutes: v.optional(v.number())` — 0/undefined = never auto-down.
- `idleAction: v.optional(v.union(v.literal("snapshot-delete"), v.literal("none")))` — default `"snapshot-delete"`.
- `wakeOnRequest: v.optional(v.boolean())` — doorman may recreate from snapshot.
- `lastActivityAt: v.optional(v.number())` — distinct from `lastHealthCheck` (liveness ≠ user activity).
- `snapshotImageId: v.optional(v.string())` — the snapshot to wake from (already produced by the mandatory snapshot-before-delete path).
- status union: add `"idle-stopped"` (machine intentionally downed, snapshot retained).

Existing anchors: `schema.ts:1254` `lastHealthCheck`; status patched in
`cloudMachines.ts:1143` / `:1733`.

### 4.2 Activity tracking

`lastActivityAt` must be bumped on *user-facing* work, not heartbeats. Bump it from
the agent when: a remote session starts/receives input, an ops verb runs, or a
stream peer connects. Lightweight: piggyback on the existing heartbeat payload with
an `activeSession bool` / `lastActivityAt` field (metadata-safe, no secrets).
Heartbeat path: `desktop/agent/auth.go::SendHeartbeat` → `backend/convex/http.ts
/devices/heartbeat` → `devices.ts::heartbeat`. Mirror onto the cloudMachines row.

### 4.3 Idle sweep (cron)

Reuse the existing meter tick rather than a new cron.
- `backend/convex/cloudLifecycle.ts:474` — `meterTick` action iterates active/paused
  machines (already auto-suspends on low balance). **Insert after the per-machine
  meter step (~line 500):**

```ts
// idle sweep (WS-B)
if (
  m.status === "active" &&
  (m.idleTimeoutMinutes ?? 0) > 0 &&
  (m.lastActivityAt ?? m.lastHealthCheck ?? 0) + m.idleTimeoutMinutes * 60_000 < now
) {
  await ctx.runMutation(internal.cloudMachines.requestIdleDown, { machineId: m._id });
}
```

- New `internal.cloudMachines.requestIdleDown`: set status `"stopping"`, then (in the
  agent-side action that holds the Hetzner token) call the existing
  `hetznerSnapshotServer` → store `snapshotImageId` → `hetznerDeleteServer` →
  status `"idle-stopped"`. **Reuse the existing decommission flow**
  (`cloudMachines.ts:1739`) — it already snapshots before delete. Do NOT power-off
  (Hetzner keeps billing).

Cron entry: `crons.ts:18` + `http.ts:6838` `case "cloudMeter"`.

### 4.4 Wake-on-demand (doorman)

- **Doorman** = an always-on cheap endpoint (start with the **relay**, or a CF
  Worker). It receives the user's first request to an `idle-stopped` machine.
- Flow: doorman → `internal.cloudMachines.wake(machineId)` → recreate Hetzner server
  from `snapshotImageId` (reuse `hetznerCreateServer`, pass `image=snapshotImageId`)
  → agent boots, re-registers (existing pending-claim path) → doorman polls
  `status==="active"` → returns/redirects the client. UX: "waking your assistant…
  ~60s" (Section 2.1).
- Idempotency: a wake while already `provisioning` returns the in-flight machine.

### 4.5 Cost floor / circuit breaker

- If `prepaidCredits.balanceCents <= 0` → auto-down (snapshot+delete), don't keep
  burning. Existing low-balance suspend (`meterTick`) becomes "snapshot+delete"
  instead of "pause" for the assistant tier.

**DoD:** A box with `idleTimeoutMinutes=10` snapshots+deletes after 10 min idle
(verify in Hetzner console it's gone, snapshot retained); a request via the doorman
recreates it in ~60 s and the session resumes; wallet shows runtime stopped during
down.

**Tests:**
- `cloudLifecycle` idle-sweep marks a stale machine `stopping` and not a fresh one.
- `wake` is idempotent; recreates from `snapshotImageId`.
- Convex privacy: new fields carry no token/path/url (extend `convex_privacy_test.go`
  `byoMachines`/`cloudMachines` field guards).

---

## 5. Workstream C — Publish one Hetzner image

**Why:** provisioning boots bare Ubuntu today; instant-provision needs a snapshot.

**Anchors:**
- `cloud-images.json:8` — `providers.hetzner.snapshots: { "arm64": null, "amd64": null }`.
- `scripts/build-cloud-image.sh:163` `provision_hetzner()`; snapshot at `:215`
  (`hcloud server create-image --type snapshot …`), ID extracted `:223`, written to
  `dist/cloud-image/hetzner-<ver>-<arch>.json` via `write_release_json` (`:458`) —
  **not** back into `cloud-images.json`.

**Change:**
1. Run `build-cloud-image.sh` for hetzner/arm64 (CAX-class is the assistant default).
2. Add a CI step (or a small `scripts/sync-cloud-images.sh`) that takes the captured
   ID from `dist/cloud-image/*.json` and writes it into `cloud-images.json`
   `providers.hetzner.snapshots.arm64`, plus `updatedAt`/`yaverVersion`.
3. Verify the provisioner reads the snapshot ID (`cloud_provisioners.go` /
   `cloud_deploy.go::hetznerCreateServer` — pass `image=<snapshot>`).

**DoD:** Fresh provision from the published snapshot reaches agent-registered +
session-ready in ~60–90 s with no per-boot `bootstrap.sh` run.

**Tests:** managed-cloud test asserts published image contains Chrome/ffmpeg/Xvfb/
Pulse (boot the snapshot, run a readiness probe); readiness endpoint passes on a
fresh box.

---

## 6. Workstream D — Metering live behind the wallet

**Why:** everything is `dryRun` until two env flags flip. The wallet path is
complete.

**Anchors:**
- Managed meter: `managedMeter.ts:137` `sim = p.dryRun !== false || !optedIn`;
  global flag `http.ts:6796` `YAVER_MANAGED_METER_LIVE` (default false).
- Cloud/compute meter: `cloudLifecycle.ts:477` `sim = dryRun !== false`; flag
  `http.ts:6849` `YAVER_CLOUD_METER_LIVE` (default false).
- Credit (top-up): `cloudLifecycle.ts:162` `topUpForOrder` (LemonSqueezy
  `order_created` webhook `http.ts:3968`) → `prepaidCredits.balanceCents +=`.
- Debit: `cloudLifecycle.ts:250` `recordUsageAndDeduct`; managed:
  `managedMeter.ts:118` `applyManagedUsage` (markup at `:143`, wallet patch `:165`).

**Change (no new code, careful rollout):**
1. Keep per-user opt-in gate (`managedServices.ts:71`) — do not bypass.
2. Flip `YAVER_CLOUD_METER_LIVE=true` first (compute is the real COGS), validate the
   ledger on your own account for a week, then `YAVER_MANAGED_METER_LIVE=true`.
3. Add a "would-have-cost vs charged" reconciliation report before going live for
   non-owner users (the honest-ledger UX already exists: `managedServices.ts:194`
   `burnBreakdownForUser`).

**DoD:** A non-owner test account tops up via LemonSqueezy, runs a session, sees the
wallet debit a real (non-dryRun) charge with correct markup; auto-down (WS-B) stops
the meter during idle.

**Tests:** metering posts a non-dryRun row when the flag is on + user opted in;
posts nothing/dryRun otherwise; markup matches `managedMarkup(kind)`.

---

## 7. Workstream E — Commit + harden Remote Session MVP

**Why:** `ops_remote_session.go` + `RemoteSessionView.tsx` are wired but uncommitted
and unhardened; no test file exists.

**Anchors:**
- `ops_remote_session.go:34` verbs `remote_session_start|status|stop`; opens via
  `browserMgr.OpenSessionWithProfileOptions(remoteSessionBrowserID, true, "",
  profile, BrowserSessionOptions{MuteAudio:false})` (`:121`).
- `browser.go:76` `SessionIdleTimeout = 30*time.Minute`; cleanup loop `:101`.

**Change:**
1. Land the two files (with user permission).
2. Harden: detect Chrome crash (browser context cancelled) → set `lastError`, allow
   restart; **per-user session cap** (guard OOM — currently unbounded); server-side
   input rate cap on `/rd/input`.
3. Make `MuteAudio` explicit per session (don't hardcode), keep automation sessions
   muted by default.

**DoD:** Beta-quality: start/control/stop a managed browser from web across networks
(needs WS-A), survives a Chrome crash, refuses to exceed the session cap.

**Tests (add `ops_remote_session_test.go`):**
- `remote_session_start` rejects non-http(s) URLs.
- start spins the ghost stream; start reuses an existing session (no second browser).
- stop closes the browser; `stopStream` also stops the frame buffer.
- **guest token cannot call `remote_session_*` or `/rd/input`** (privacy/authz).
- unmuted option does not leak into default-muted automation sessions.

---

## 8. Data model summary (Convex — metadata only)

New/changed fields, all metadata-safe (extend `convex_privacy_test.go` guards):

| Table | Field | Type | Purpose |
| --- | --- | --- | --- |
| cloudMachines | `idleTimeoutMinutes` | number? | auto-down threshold |
| cloudMachines | `idleAction` | enum? | `snapshot-delete`\|`none` |
| cloudMachines | `wakeOnRequest` | bool? | doorman may recreate |
| cloudMachines | `lastActivityAt` | number? | user activity (≠ liveness) |
| cloudMachines | `snapshotImageId` | string? | wake source |
| cloudMachines | status | +`idle-stopped` | downed-but-restorable |

**Never in Convex:** Hetzner token, TURN secret, snapshot *contents*, browser
profile/cookies, meeting URLs marked private, stream data, absolute paths. All stay
in the agent vault / on-box.

---

## 9. Interfaces / contracts (stable across surfaces)

- **Runtime Target interface** (uniform over VM | container | BYO | redroid):
  `prepare · start · stop · status · streamOffer · sendInput · logs · cleanup`.
  Implement auto-down/wake at this layer so the assistant can be VM *or* container
  without surface changes.
- **ICE:** `GET /stream/webrtc/ice` is the single source both peers fetch (WS-A).
- **Control:** `POST /rd/policy` (consent), `POST /rd/input` (normalized 0..1 coords),
  audit JSONL — already standard.
- **Ops:** `remote_session_start|status|stop` + machine routing (`local|primary|auto|
  <deviceId>`).
- **Doorman:** `POST /wake {machineId}` → `{status, etaSeconds}`; client polls until
  `active`.

---

## 10. Sequencing for the Codex handoff

1. **WS-A** (TURN) — smallest, unblocks everything. Do first, verify off-network.
2. **WS-C** (publish image) — parallel with A.
3. **WS-E** (commit + harden remote session) — after A+C; gives a beta product.
4. **WS-B** (auto-down) then **WS-F** (doorman) — after schema lands; makes price honest.
5. **WS-G** (free NUC tier) — parallelizable, but **does not open to strangers until
   the §13.3 isolation gate passes**. This is the customer-acquisition on-ramp.
6. **WS-D** (metering live) — last; only after B controls cost and E is reliable.

Each WS is self-contained with its own DoD + tests above. Do not flip WS-D flags in
production until WS-B's cost floor and WS-A's connectivity are verified on a real
non-owner account. Do not advertise the free tier (WS-G) until §13.3 passes.

## 11. Global definition of done

A non-owner user can: provision (or BYO) → open a browser/agent session → view &
control it **from another network** → let it auto-down when idle (snapshot+delete) →
wake it on next request in ~60 s via the doorman → pay a real metered charge with
honest markup → and the same Remote Runtime Session object is ready to power TV/car/
watch/OEM later without a rewrite.

---

## 12. Workstream F — Doorman (wake a downed box)

**Why:** a box that has been snapshot+deleted (WS-B) cannot receive its own wake
request. An always-on, near-free endpoint must catch the request, trigger the
recreate, and hold the client until the box is session-ready.

### 12.1 Where it lives — decision

Three candidates were evaluated against the code:

| Option | Anchor | Verdict |
| --- | --- | --- |
| **Convex httpAction** | `backend/convex/http.ts:10` `httpRouter()`; route pattern `http.route({path,method,handler:httpAction})` (`:516`) | **Chosen.** Same backend as auth + heartbeat, already holds `cloudMachines` + the Hetzner-provision mutations, always-on, free tier. No QUIC hop. |
| Relay route | `relay/server.go:645` `runHTTPProxy` (mux at `:646`) | Rejected — relay is **pass-through QUIC only**, does not talk to Convex; would need new Convex coupling for no benefit. |
| CF Worker | `gateway/wrangler.toml` (`yaver-gateway`) | Rejected — that Worker is inference-only; don't overload it with lifecycle. |

### 12.2 Contract

- `POST /wake` (Convex httpAction) `{ machineId }` → `{ status, etaSeconds }`.
  - If `status === "idle-stopped"`: call `internal.cloudMachines.wake(machineId)`
    (WS-B 4.4), set `provisioning`, return `etaSeconds≈90`.
  - If `provisioning`/`active`: return current status (idempotent, no double-create).
  - Auth: caller must own the machine (reuse the session-token check used by other
    `cloudMachines` mutations). **Guests cannot wake.**
- Client (web/phone) polls `GET /cloud/machines` (existing) until `active`, showing
  "waking your assistant… ~60 s".

### 12.3 Implementation notes

- `wake` reuses `hetznerCreateServer` with `image=<snapshotImageId>` (WS-B 4.1) — the
  agent boots, re-registers via the existing pending-claim path, status → `active`.
- Rate-limit `/wake` per user (a tight loop must not spawn servers). One in-flight
  wake per machine.
- The "hold the client" UX is poll-based (no socket needed). Optional: a
  `wakeRequestedAt` timestamp on the row so the dashboard can show progress.

**DoD:** From a phone on cellular, hitting an `idle-stopped` assistant triggers
`/wake`, the box recreates from snapshot, and the session is usable in ~60–90 s with
no manual dashboard step.

**Tests:** `/wake` on an `active` machine is a no-op; on `idle-stopped` it sets
`provisioning` exactly once under concurrent calls; a non-owner token is rejected.

---

## 13. Workstream G — Free own-hardware tier (initial customer acquisition)

**Goal:** a *temporary* free on-ramp running on **our own hardware** (NUC / mini-PC /
Raspberry Pi fleet) to acquire the first cohort of users cheaply, then convert them to
**BYO-key** or **paid managed cloud**. This is an acquisition tactic with a planned
off-ramp — not the permanent margin model. It reuses the existing operator-fleet work.

### 13.1 How far can "free" actually go? (own-hardware economics)

Marginal cost of an own-hardware node ≈ **electricity only** (internet is sunk).
Approximate — verify locally:

| Node | Power (avg) | €/mo elec @ €0.30/kWh | Bursty assistants it can host |
| --- | --- | --- | --- |
| Intel N100 mini-PC, 16 GB | ~15 W 24/7 | **~€3.2** | dozens idle / ~5–15 active light (CRUD/voice) |
| Raspberry Pi 5, 8 GB | ~5 W 24/7 | **~€1.1** | several light containers |

So a **handful of NUCs/Pis (~€10–30/mo total electricity)** can host **low-hundreds
of bursty free users**. Marginal orchestration cost per free user ≈ **€0.05–0.15/mo**
— effectively free, because the assistant workload (chromedp / redroid / CRUD /
gateway calls) is light and bursty; idle containers cost ~nothing.

**The one thing that is NOT free is LLM inference.** That is the whole budget. Two
clean ways to keep the free tier solvent:
1. **BYO key (free to us)** — user supplies their own model key; we orchestrate only.
2. **Shared gateway key with a hard free-tier cap** — the gateway already mints
   *scoped* tokens (`gateway_runner_env.go:59` `mintGatewayToken`,
   `:183` `gatewayInjectEnv`) and keeps the real upstream key in a Worker secret
   (never on the box). Add a per-user monthly token cap; when hit, the free user must
   add a BYO key or upgrade. **Never put the upstream key in box env**
   (`cleanTenantEnv` `:153` already strips secret-shaped vars — keep using it).

Other resources: STUN is free; **TURN relays real media over our home upload** —
cap free-tier TURN minutes and prefer STUN/direct (most sessions connect without
TURN). Control plane (Convex) + doorman ride free tiers.

**Bottom line:** yes — we can offer a genuinely free tier to the first low-hundreds of
users for ~€10–30/mo electricity plus a *capped* shared-inference budget, enough to
bootstrap, with BYO-key as the pressure valve and paid managed cloud as the upgrade.

### 13.2 How it plugs in

The free NUC node is just another **Runtime Target kind: `operator-fleet`** behind the
same uniform interface (§9). It already has a mode flag:
- `desktop/agent/main.go:2146` `--operator` → `httpserver.go` `operatorMode=true`
  (disables paired-token owner fast-path, enables the host-share reaper, forces tenant
  containerization).

A free user gets an **ephemeral container** on a fleet node, reachable **relay-only**,
with their assistant session inside it. On logout/idle the container is destroyed.

### 13.3 ISOLATION GATE — user data is first-class (hard precondition)

Strangers sharing **our own hardware** is the highest-risk configuration in the whole
plan. The free tier **must not open** until every item below passes. These map to the
four known operator-fleet gaps (`docs/yaver-public-compute-operator-fleet.md`):

| # | Requirement | Anchor / gap | State today | Must reach |
| --- | --- | --- | --- | --- |
| I1 | **Ephemeral per-tenant container**, no shared FS/cache, fresh per allocation, destroyed on release | `container_runner.go:257` (cgroup limits, RO root) + `host_share_reaper.go:86`/`:108` (kill+wipe) | partial (reaper shipped; per-tenant container + cron not fully wired) | **finish** — every free session = its own container; nothing persists across users |
| I2 | **No paired-token = owner** on operator boxes (a foreign token must never gain owner scope) | gap B (`httpserver.go:1928`, `multiuser_http.go:173`) | partial (operator mode disables fast-path; verify no-op end-to-end) | **prove** with a test: foreign token cannot read another tenant's data/FS |
| I3 | **Network jail** — relay-only inbound + RFC1918 egress block (free user cannot reach our home LAN) | `egress_proxy.go:149` `isPrivateOrReserved`; `httpserver.go` `directBindHost()` | egress block works; `--relay-only` direct HTTP/TLS bind is wired; field relay test pending | verify a fleet node has no LAN-reachable listener over real network conditions |
| I4 | **Teardown leaves zero residue** — processes killed, workspace + container wiped, no cross-tenant cache | `host_share_reaper.go` `reapHostShareSessions` `:108`, `ReapExcept` `:140` | shipped (hard-kill + wipe); add scheduled cron + verify | **verify** with a test: after release, next tenant sees a clean FS, no prior env/secrets |
| I5 | **Operator/service identity**, not a personal account token, binds the node | gap A (`main.go`, `httpserver.go:224`) | stub | **build** a scoped operator principal so a leaked node token ≠ a person's account |
| I6 | **Free-tier data residency** — a free user's data lives only in their ephemeral container + their own Convex *metadata* rows; nothing of theirs in our vault; nothing of ours reachable by them | privacy contract `convex_privacy_test.go` | enforced for sync; extend to free-tier rows | **extend** privacy tests to cover free-tier/tenant fields |

**Gate rule:** I1–I4 are blocking for any stranger traffic. I5 hardens against node
compromise (strongly recommended before scale). I6 is a privacy-test extension that
ships with the feature. Until I1–I4 pass on a real fleet node, the free tier runs for
**owner test accounts only**.

### 13.4 Off-ramp (so free doesn't become a permanent sink)

- Free tier is **capped** (inference tokens, TURN minutes, session hours) and
  **time-boxed** per user (e.g., a trial window) — caps already expressible via the
  gateway token + meter.
- Upgrade paths: **BYO key** (stay free on our HW, you pay your model bill) or **paid
  managed cloud** (WS-A/B/C/D). Surface the upgrade when a cap is hit.
- Operationally: a fleet node is disposable; if abused, revoke the operator principal
  (I5) and the reaper (I4) wipes it. Mirrors the "do no harm / network jail" rules in
  `CLAUDE.md`.

**DoD:** A stranger signs up for the free tier, gets an isolated ephemeral container on
a NUC reachable only via relay, runs an assistant within capped inference, and on
logout the node retains **zero** of their data — proven by I1–I4 tests — with a visible
upgrade-to-BYO/paid prompt when a cap is hit.

**Tests:**
- Two concurrent free tenants on one node cannot see each other's filesystem,
  processes, or env (I1/I2).
- A fleet node exposes no RFC1918-reachable listener and cannot egress to RFC1918
  (I3) — extend the `egress_proxy` test + add a bind-surface assertion.
- After tenant release, a fresh allocation starts from a clean container with no prior
  secrets/cache (I4).
- Convex privacy tests cover free-tier/tenant rows (I6).
