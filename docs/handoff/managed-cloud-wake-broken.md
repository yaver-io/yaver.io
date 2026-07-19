# Managed Cloud Wake UX Audit

Date: 2026-07-18

Scope: phone-only wake/use of a parked Yaver-managed cloud box, with the same
status truth usable later by web, car, watch, TV, MCP, voice, and spatial
surfaces.

Privacy boundary: this report intentionally avoids real device ids, hostnames,
IP addresses, email addresses, tokens, relay hostnames, and customer/project
contents. It names files and functions because those are needed to fix the
product, but it does not dump source code or runtime secrets.

## Executive Summary

The reported symptom is real: from mobile, a parked cloud box can appear to be
both asleep and waking, progress can sit at a confident-looking percentage, and
failure/recovery can be hidden behind stale connection text or an in-app browser
handoff. The screenshots show the product saying "WAKING" for the managed box
while it remains inside "Sleeping machines"; later it reports an auth-blocked
failure, but the user path still encourages retrying or selecting another
machine instead of treating wake as a durable job with a clear next action.

The current codebase has already moved in the right direction. Backend rows now
carry phase timing, wake start/end timestamps, provider status, last wake
outcome, and a tri-state runner authorization payload. Mobile and web both have
an honest `needs-auth` phase in their newer wake models, and tests cover several
false-100-percent regressions.

The remaining problem is structural: there is no single durable wake run model
that every surface consumes. Mobile still has multiple wake ladders, web mirrors
logic by hand, the provider action progress is not persisted, health probes are
too trust-light, and "sign this box in while it is awake" is still modeled as a
UI affordance rather than a first-class blocking state.

## Evidence Inspected

- Project guidance: `CLAUDE.md`, `docs/architecture/AI_ARCH.md`,
  `docs/architecture/REMOTE_WORKER.md`.
- Backend lifecycle: `backend/convex/cloudLifecycle.ts`,
  `backend/convex/cloudMachines.ts`, `backend/convex/http.ts`,
  `backend/convex/schema.ts`.
- Mobile lifecycle and picker UI: `mobile/src/lib/parkedMachines.ts`,
  `mobile/src/lib/wakeMachineCore.ts`, `mobile/src/lib/wakeMachine.ts`,
  `mobile/src/components/WakeProgress.tsx`,
  `mobile/src/components/RemoteBoxPickerModal.tsx`,
  `mobile/src/lib/deviceStatus.ts`, `mobile/src/lib/probeTargets.ts`,
  `mobile/src/context/DeviceContext.tsx`.
- Web lifecycle mirror: `web/lib/wakeProgress.ts`,
  `web/components/dashboard/WakeProgress.tsx`,
  `web/components/dashboard/ManagedCloudPanel.tsx`,
  `web/components/dashboard/DevicesView.tsx`.
- Tests: `mobile/src/lib/wakeMachine.test.mts`,
  `mobile/src/lib/wakeSleepClosedLoop.test.mts`,
  `web/lib/wakeProgress.test.ts`.
- User screenshots: 23 iPhone captures from the attached image set.

## Screenshot Findings

The screenshots show a consistent sequence:

1. The selected managed cloud box is listed under "Sleeping machines" while its
   card says `WAKING`.
2. The progress line advances from "Booting..." to "Agent reachable..." with
   ETAs like `~3:02` and then "almost there...".
3. Other devices on the same picker show stale/down/live states with repeated
   "No reachable transport. Sign in again to fetch relay..." text.
4. Switching to a local machine enters a mostly black "Switching" screen for a
   long time, repeatedly "Pinging" or "Re-checking" a stale `.local` hostname.
5. One path eventually fails with "Couldn't reach ... No reachable transport,"
   while another says "Connected" even though the surrounding list still has
   stale or contradictory device states.
6. The final managed-box row eventually shows the useful truth: it stayed awake
   waiting for Yaver sign-in, was not authorized in time, and parked again to
   stop the meter. That message arrives too late and is not presented as the
   primary recovery path.

Product interpretation: the UI is not lying because one text string is wrong;
it is lying because each surface derives status from different partial signals.
The user needs "this wake run is blocked on sign-in, server is currently alive
until time X, tap here now" as the primary state, not a spinner plus inferred
ETA.

## What Exists Today

Backend:

- Managed machine rows have lifecycle fields in `schema.ts`: provider status,
  wake started/completed, last wake duration, last wake outcome, phase progress,
  runner authorization, and error/provision state.
- `resumeMachine` writes wake phases such as `checking-snapshot`,
  `preparing-volume`, `restoring-snapshot`, and `booting`.
- `resumeHealthCheck` probes the resumed machine and distinguishes reachable
  from usable. It recognizes `yaver-auth-expired` / bootstrap as
  `awaiting-yaver-auth`, keeps the box alive for a bounded recovery window, and
  later parks it to stop metering.
- `/machine/phase` accepts machine-token-authenticated phase beacons from the
  booting box.
- `/subscription` exposes privacy-safe lifecycle fields to clients.
- `/billing/yaver-cloud/runners-authorized` now records runner authorization
  without always forcing phase `ready` mid-wake.

Mobile:

- `wakeMachineCore.ts` defines the better shared vocabulary:
  `asleep`, `requested`, `resuming`, `booting`, `registering`, `online`,
  `ready`, `needs-auth`, `snapshotting`, `powering-down`, `parked`, `error`.
- `needs-auth` is terminal/warn, not an in-flight progress phase.
- `WakeProgress.tsx` renders a bar, rung ladder, in-phase clock, network line,
  and stall hint from the lifecycle state.
- `probeTargets.ts` prevents bearer tokens from being attached to unsafe
  plaintext public direct probes.
- `RemoteBoxPickerModal.tsx` has a sign-in path for managed machine auth
  recovery and exposes sleeping managed machines.

Web:

- `web/lib/wakeProgress.ts` mirrors the newer mobile lifecycle model and has
  tests for phase mapping, stalled hints, provider line display, measured ETA,
  and false-ready prevention.
- Dashboard components render web `WakeProgress`, but some call sites still
  pass hardcoded reachability values rather than real device transport state.

Tests:

- Mobile tests cover `awaiting-yaver-auth -> needs-auth`, false 100 percent
  prevention, LAN-vs-cloud wake timing, wake-only phase mapping, and rest
  summaries.
- Web tests mirror most of that contract.

## Critical Gaps

### P0: Wake Is Not A Durable User-Visible Job

There is no `wakeRun` object with a stable id, event log, action ids,
current phase, user action requirement, retry policy, timeout, and final
outcome. Instead, the run is reconstructed from mutable machine fields plus
client-local optimistic state.

Consequence: closing/reopening mobile, switching surfaces, or starting wake
from another device can reset perceived elapsed time, hide the action window,
or make the box vanish from the list when it crosses a derived threshold.

Required model:

- `wakeRunId`
- `machineId`
- `requestedBySurface`
- `state`: `queued | provider-action | booting | agent-reachable | blocked | ready | parking | failed | parked`
- `phase`
- `progress`: provider-backed when available, otherwise server-authored
- `startedAt`, `phaseStartedAt`, `deadlineAt`, `completedAt`
- `blockingAction`: `none | sign-in | retry | contact-support | manual-provider-cleanup`
- `blockingActionExpiresAt`
- `providerActionIds`
- `providerStatus`, `providerProgress`, `providerErrorCode`
- `agentLifecycleState`, `agentReachable`, `agentUsable`
- `safeUserMessage`, `operatorMessage`
- append-only `events[]`

This can live in Convex as a separate `machineWakeRuns` table, with the latest
run denormalized onto `cloudMachines` for cheap `/subscription` reads.

### P0: Provider Action Progress Is Still Missing

The current provider probe reads only server status from `GET /servers/{id}`.
Hetzner's long-running operations return action ids with status, progress,
started/finished, and error details. `POST /servers` also returns an initial
action and next actions. The current resume path keeps server id and IP, but
does not persist the action ids or poll `GET /actions/{id}`.

Consequence: the UI still invents most of the restore/boot progress curve. It
can say "almost there" while the provider action is still running, failed, or
never started.

Required backend work:

- Capture provider action ids from create, image restore, volume detach, image
  creation, server delete, and any next actions.
- Add `providerActionId`, `providerActionCommand`, `providerActionStatus`,
  `providerProgress`, `providerErrorCode`, `providerErrorMessage`,
  `providerStartedAt`, `providerFinishedAt`.
- Poll action status during wake/park.
- Prefer provider progress inside provider-owned phases.
- Persist terminal provider failures immediately instead of waiting for a
  synthetic timeout.

### P0: Health Promotion Trust Boundary Is Too Weak

`resumeHealthCheck` probes `/health` on the resumed server without a bearer or
machine token and falls back from HTTPS to plaintext HTTP. A public cloud IP can
be recycled, DNS can be stale, and a different service can answer the same port.

Consequence: the control plane can promote a row based on an unauthenticated
health response from the wrong endpoint. That is especially dangerous because
`active` is also a billing state.

Required backend work:

- Require a machine-bound proof on health promotion: machine token, signed
  device key challenge, or an agent `/health` field signed with the persisted
  device key.
- Do not accept plaintext public-host health for billing promotion.
- Separate "provider says server exists" from "owned agent is usable."
- Keep `/health` public for local recovery if needed, but do not let public
  health alone move billing or readiness.

### P0: Dry-Run/Fallback Can Still Look Active

When `HCLOUD_TOKEN` is unavailable or dry-run mode is active, the resume action
currently patches the machine to `active` and returns success-like shape. The
reason says dry-run/fail-closed, but the state transition can still make clients
show an online or selectable machine despite no provider wake happening.

Required behavior:

- If real spend is disabled, report `failed` or `dry-run` explicitly.
- Do not set `status: active`.
- Do not mark a managed machine usable unless an owned agent proves it is up.
- UI copy should say "Cloud wake is unavailable from this environment" rather
  than animating a wake.

### P1: Multiple Ladders Still Disagree

Mobile still has `parkedMachines.ts::deriveWakeView` for the picker and
`wakeMachineCore.ts` / `useMachineLifecycle` for the newer shared model. Web
has its own mirror. The picker path does not consume the shared `WakeProgress`
state directly.

Consequence: the exact same machine can render as:

- sleeping but waking,
- active but not selectable,
- 100 percent ready but unreachable,
- in-flight when it is blocked on sign-in,
- failed only after the box has already been parked.

Required client work:

- Make `wakeMachineCore.ts` the only mobile lifecycle derivation.
- Convert the sleeping-machine picker row to consume the same lifecycle state
  and renderer as banner/connection surfaces.
- Keep web mirrored only at the pure model boundary, with a parity test that
  asserts every backend phase slug maps identically.
- Add a generated or shared phase schema so new backend slugs cannot silently
  fall through.

### P1: Auth Recovery Is A Side Quest, Not The Main State

The code recognizes `awaiting-yaver-auth`, but the product flow still treats
remote sign-in as a button inside one row. The screenshots show the user
watching wake progress, leaving the app, switching targets, and eventually
seeing the terminal message after the live recovery window is gone.

Required behavior:

- When the agent answers as signed out, transition the wake run to
  `blocked/sign-in`.
- The primary CTA becomes "Sign in this box now."
- Show the hold deadline: "Box is awake until 14:37; sign in before then or it
  parks to stop billing."
- While the sign-in sheet/session is open, defer the abandon timer or extend the
  hold boundedly.
- If the box has already parked, do not open a direct sign-in flow that cannot
  reach it. Say "Wake again to open a sign-in window" or offer a combined
  "Wake and sign in" action.
- After successful recovery, resume the same wake run and finish to ready.

### P1: Switching/Connection UX Uses Stale Local Names

The screenshots show repeated black "Switching" screens pinging or re-checking
a stale `.local` host. `probeTargets.ts` already explains why `.local` should
not be used for direct probe legs on iOS before local-network permission, but
switching copy still exposes a stale target and can spend a long time on an
opaque spinner.

Required behavior:

- Show the chosen transport being tried: relay, direct LAN IP, tunnel, or local
  hostname.
- Put a bounded timer and clear fallback list on the switching screen.
- Stop naming stale `.local` as the primary target when relay or a routable LAN
  IP is what the connector will actually use.
- Make "repair/re-check" a visible state with concrete next step, not a black
  full-screen wait.

### P1: Relay Presence Is Not A Sufficient Readiness Signal

Mobile fetches relay presence for device ids and can overlay it on top of
Convex-derived device state. Presence is useful, but it is not proof that the
agent is usable, signed in, or able to run coding tasks.

Required behavior:

- Treat relay presence as "transport may exist," not "machine ready."
- Require a successful agent info/capability/readiness probe before selectable
  task routing.
- Auth-gate presence or narrow what it reveals.

### P2: Types Still Hide The Tri-State

The `/subscription` payload intentionally sends `runnersAuthorized: null` when
the backend value is unset. Mobile's `ManagedCloudMachineSummary` still types it
as optional boolean, not `boolean | null`.

Consequence: the runtime behavior works with strict `!== false`, but the type
contract does not document the real semantics and invites future flattening
back to `false`.

Required work:

- Type `runnersAuthorized?: boolean | null` everywhere the subscription machine
  summary is declared.
- Add tests for unset/null runner authorization producing ready, not a permanent
  finishing state.

## Desired State Machine

User-facing states should be simple and identical everywhere:

| State | Meaning | User UI |
|---|---|---|
| Asleep | Snapshot/volume kept, provider server deleted, meter stopped | Wake button, last outcome, expected time |
| Wake requested | Convex accepted the action | Immediate acknowledgement, no fake provider progress |
| Provider restoring | Provider is creating/restoring/attaching | Provider-backed progress and plain label |
| Booting | Server exists; OS/agent not answering yet | Boot timer, provider status, stall hint |
| Agent reachable | Agent answers but readiness is incomplete | Show what is missing |
| Sign-in needed | Agent is alive but session/bootstrap blocks use | Primary sign-in CTA and hold deadline |
| Ready | Owned agent reachable and runners authorized or explicitly not required | Select/use machine |
| Parking | Snapshot/delete in progress | Snapshot/delete progress and billing statement |
| Failed | Terminal failure or manual cleanup needed | Cause, billing status, retry/repair action |

Rules:

- A bar may creep only inside an active phase; it must never cross the next rung
  without a server/provider event.
- "Ready" requires owned agent proof plus task readiness.
- "Failed" must say whether the server is still billing.
- "Needs sign-in" is not a failure and not progress. It is a blocked state with
  one obvious action.

## Cross-Surface Contract

All surfaces should consume the same compact status object from Convex or from a
local agent proxy:

```text
MachineLifecycleView
  machineId
  runId
  wakeKind: cloud | lan | manual
  state
  phase
  progress
  startedAt
  phaseStartedAt
  deadlineAt
  provider
  agent
  billing
  primaryAction
  safeTitle
  safeDetail
```

Surface expectations:

- Mobile app: full ladder, ETA, hold deadline, sign-in, retry, and "use this
  machine" only after readiness proof.
- Mobile widget/live activity: short state, progress, hold deadline, tap to
  open the exact recovery action.
- Web dashboard: same ladder; no hardcoded `deviceReachable=false`; provider
  progress visible while provider-owned.
- Car: voice-safe, no long reading. Example: "Your cloud box is awake but needs
  sign-in. Continue on phone." Only allow non-destructive retry when parked.
- Watch/Wear: glanceable state plus haptic on blocked/ready/failed. Sign-in
  delegates to phone.
- TV: large status and QR/deep link to phone for sign-in; no tiny log text.
- MCP/tools: return structured lifecycle JSON and emit progress events; do not
  make coding agents parse prose.
- STT/TTS: announce only state changes and blocking actions. Never loop
  "almost there."
- AR/VR/glass: persistent ambient status chip; details panel uses the same
  `MachineLifecycleView`.

## Backend Implementation Plan

1. Add `machineWakeRuns` and latest-run denormalization fields.
2. Capture and poll provider action ids for create, detach, snapshot, delete,
   and restore operations.
3. Replace unauthenticated public health promotion with owned-agent proof.
4. Fix dry-run/no-token resume so it cannot mark a machine active.
5. Model sign-in hold as a wake-run blocked state with `deadlineAt`.
6. Emit wake events through Convex subscription/query or a polling endpoint.
7. Preserve the current `/subscription` fields for compatibility while clients
   move to the lifecycle object.

## Client Implementation Plan

1. Mobile: move `RemoteBoxPickerModal` sleeping rows onto `wakeMachineCore` and
   `WakeProgress`; delete or shrink `deriveWakeView`.
2. Mobile: make the switching screen show transport attempts and bounded
   fallback decisions.
3. Mobile: make `blocked/sign-in` a full primary recovery flow with a visible
   hold deadline.
4. Web: pass real reachability into `WakeProgress`; keep web/mobile phase
   parity tests.
5. Cross-surface: define a small renderer matrix for phone/web/car/watch/TV/MCP
   so every phase has a primary text, secondary text, allowed action, and
   terminal behavior.
6. Types: make `runnersAuthorized` tri-state everywhere.

## Test Plan

Pure tests:

- Backend phase slug mapping cannot diverge from mobile/web phase maps.
- `runnersAuthorized: null` and omitted both mean "unknown/not blocking," not
  false.
- `awaiting-yaver-auth` is terminal blocked, never in-flight.
- Dry-run/no-token resume cannot produce active/ready.
- Provider action error maps to failed with provider error code.
- Public unauthenticated health cannot promote billing/readiness.

Integration tests:

- Simulated provider create action goes 0 -> 100 and clients show provider
  progress.
- Simulated provider error immediately renders failed with clear cause.
- Agent returns `authExpired`; wake run enters sign-in blocked, shows deadline,
  and does not abandon while recovery is active.
- Recovery succeeds; same wake run finishes ready.
- Recovery timeout expires; server is deleted, row returns asleep with last
  outcome explaining sign-in was missed.
- Mobile switch to stale `.local` does not black-screen indefinitely and falls
  back to relay/LAN candidates with visible status.

Live smoke tests, only with explicit approval because they can spend provider
money:

- Parked managed box -> wake -> provider progress -> boot -> ready -> select
  from mobile -> run a trivial task -> park.
- Parked managed box with expired agent auth -> wake -> sign-in blocked ->
  recover from phone -> ready -> park.
- Provider failure or capacity simulation -> clear failed state, no active
  billing.

## Current Risk Ranking

- Highest product risk: user cannot trust the wake UI because progress and
  selectable state are inferred differently per surface.
- Highest billing risk: readiness/billing promotion is still coupled to weak
  health proof and can be confused by dry-run/no-token paths.
- Highest recovery risk: auth recovery is time-windowed but not modeled as the
  wake run's main blocking state.
- Highest maintenance risk: backend phase vocabulary is duplicated in mobile
  and web without a generated/shared schema.

## Non-Goals

- Do not expose provider branding in consumer UI. Use "Yaver Cloud" or
  "provider" in product surfaces; keep provider-specific diagnostics in
  operator/debug views.
- Do not wake or park any real machine as part of this audit.
- Do not deploy Convex, mobile, or web without explicit approval.
- Do not commit this report without explicit approval.

## Bottom Line

The right fix is not another string tweak. Treat wake/park as a durable,
provider-backed, user-visible job with one lifecycle contract. Convex should
own the truth, provider actions should own provider progress, the agent should
prove readiness, and every surface should render the same `blocked`, `ready`,
`failed`, or `parking` state in its own idiom.
