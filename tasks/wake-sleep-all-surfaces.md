---
doer: codex
---

<!-- Single seat. `claude` has a credentials file on the mini but the existing
     tasks/ci-review-gate.md records it as NOT reliably authed there, and a seat
     named in front matter is binding — an unauthed seat fails the run at
     iteration 1. codex is verified authed. Do not add a `master:` seat. -->

# Managed-cloud wake / sleep / connect — one ladder on every surface, proven by tests

## Why

A managed box takes minutes to wake. For most of 2026 every surface said nothing
for the whole of it, and the owner's report was blunt: *"Try Connect resume box
just shows resuming… no idea whats the status."*

Root cause, verified live on 2026-07-18 against the owner's own box: the box
woke fine, but its Yaver session had expired, so `resumeHealthCheck` parked it at
`provisionPhase: "awaiting-yaver-auth"` while **deliberately holding
`status: "resuming"`** for a bounded recovery window. Surfaces keyed their chip
on `status` alone, so a wake that had already stopped — and could never resume
without the user — was pixel-identical to one still working. Ten minutes later
`abandonWake` deleted the server. Wake → bill ~10 min → silently re-park, on
repeat, with the user never told why.

Much of the fix already shipped. **Read this table before touching anything** —
it is the difference between finishing this task and re-doing it.

## What already shipped (do NOT redo)

| Landed | Where |
|---|---|
| Fine-grained wake phases `checking-snapshot` / `preparing-volume` / `restoring-snapshot`; `registering` only when the agent actually answers | `backend/convex/cloudLifecycle.ts`, `cloudMachines.ts` (`PROVISION_PHASES`) |
| Provider status probe (`providerStatus`/`providerStatusAt`) — Hetzner's own "initializing/running" during the create→agent-answers blind window | `cloudLifecycle.ts::probeProviderStatus` |
| Wake/park telemetry: `wakeStartedAt`/`wakeCompletedAt`/`lastWakeDurationMs`/`lastWakeOutcome`, park equivalents, `snapshotSizeGb`/`snapshotCreatedAt` | `schema.ts`, `cloudMachines.ts::setLifecycleTiming` |
| All of the above served on `/subscription` | `backend/convex/http.ts` |
| Web ladder: bar + steps + live m:ss clock + scaled ETA + stall hints + provider line + `needs-auth`-as-action + optimistic rung on click + `ParkedSummary` | `web/lib/wakeProgress.ts`, `web/components/dashboard/WakeProgress.tsx`, wired in `DevicesView.tsx` + `ManagedCloudPanel.tsx` |
| Mobile core: `expectedWakeMs` / `wakeScaleFor` / `describeRest` / `formatDuration`, scale threaded into creep+stall | `mobile/src/lib/wakeMachineCore.ts`, `wakeMachine.ts` |
| Wear OS `probeHealth` classification + `NeedsAuth` state (it used to march to READY on a signed-out box and re-send the pending turn) | `wear/.../BoxLifecycle.kt`, `ui/WakeProgress.kt` |

Tests that must keep passing: `web/lib/wakeProgress.test.ts` (68 assertions),
`mobile/src/lib/wakeMachine.test.mts` (24). Both assert the SAME phase table on
purpose — the two surfaces share no build, so that duplication is the only thing
stopping them drifting. **Extend both or neither.**

## The invariants — every phase of this task is judged against them

1. **A bar that cannot advance must not exist.** `needs-auth` is terminal: no
   spinner, no creep, no polling. Render the action instead.
2. **Never print a raw control-plane slug.** An unmapped phase is our bug;
   "awaiting-yaver-auth" is not something a user can act on. Fall back to
   generic prose, never `?? phase`.
3. **Estimates come from the box, not from constants.** `expectedWakeMs` prefers
   this box's measured `lastWakeDurationMs`. The old constants were timed on one
   cx43 with a 160 GB disk; a volume-backed box wakes in ~1-2 min and was being
   promised eight.
4. **A parked box must explain its last wake.** `lastWakeOutcome` is why.
5. **Reachability outranks every phase claim.** If the box answers, it is up.
6. **`ok:true` from `/health` does NOT mean usable** — the agent answers while
   signed out because it still serves the pairing routes. Demand
   `lifecycle.usable` + `!authExpired` + `!needsAuth`.

## Phase 1 — mobile has THREE wake ladders; make it one

This is the biggest remaining defect and the highest-value work here.

- `mobile/src/lib/wakeMachineCore.ts` + `src/components/WakeProgress.tsx` — the
  real one (12 phases, creep, stall hints, scale).
- `mobile/src/lib/parkedMachines.ts::deriveWakeView` (~:97) — a SECOND, coarser
  4-stage ladder used by the Infra tab and the remote-box picker.
- `mobile/src/components/ManagedCloudCard.tsx` (~:229) — a THIRD inline bar.

Collapse 2 and 3 onto `wakeMachineCore`. Same phases, same labels, same percents
everywhere. Delete `deriveWakeView` if nothing needs it after the merge; if
something does, make it a thin adapter over `deriveServerPhase` rather than a
parallel implementation. Add tests pinning the picker and the Infra tab to the
same phase table the other two already assert.

## Phase 2 — mobile parity with what web just got

Port, using the web implementations as the reference (mirror, never import
across the surface boundary — a relative cross-surface import passes `tsc` and
CI and then breaks the Turbopack build, see commit `964f142a4`):

- **Optimistic rung on tap — CORRECTION, read this.** An earlier draft of this
  task claimed mobile lacked this. **It does not.** `wakeMachine.ts:262`
  (`setOptimistic`, applied at :287-290) already shows `requested` on tap and
  `snapshotting` on park, and its rule is arguably BETTER than web's: the
  optimistic rung wins only until the server's phase overtakes it by percent,
  where web uses a 45s time grace. **Do not rewrite mobile to match web.** If
  anything, port mobile's rule to web (`computeWakeView`'s `optimistic` param in
  `web/lib/wakeProgress.ts`) and keep the time grace only as the expiry for a
  request the server NEVER accepts, which is the one case percent-comparison
  can't catch. Verify before changing either.
- **Provider line** (`providerLine` in `web/lib/wakeProgress.ts`) — goes quiet
  once our own signal is better, drops data older than 2 min, never prints a raw
  provider enum.
- **`ParkedSummary` everywhere a parked box appears**, not just
  `ManagedCloudCard`: the remote-box picker and the Infra tab list too.

## Phase 3 — native surfaces: tvOS, watchOS, Wear

All three already have a ladder and a `needsAuth` notion. What they lack vs the
RN/web version: **elapsed timer, stall hints, provider line, in-phase creep.**

- `tvos/YaverTV/Views/WakeProgressView.swift` + `tvos/YaverTV/BoxLifecycle.swift`
- `watch/YaverWatch/Views/WakeProgressView.swift` + `watch/YaverWatch/BoxLifecycle.swift`
- `wear/app/src/main/kotlin/io/yaver/wear/ui/WakeProgress.kt`

Port the behaviour, not the pixels — a watch face is not a dashboard card; an
elapsed clock and a stall sentence are what matter, a five-rung stepper may not
fit. **Their percents intentionally differ from `PHASE_META` (52/80/94 vs
40/65/86); the headers now say so. Do not "fix" the numbers** unless you change
all three together and update every comment.

Wear has no Gradle wrapper and no CI — its own `build.gradle.kts` calls itself a
source-only scaffold. That is exactly how its `needsAuth` gap survived. Do not
claim Wear is verified; say plainly that it is reviewed and uncompiled.

## Phase 4 — closed-loop tests with the tools that already exist

Do not invent a framework. Three lanes exist; use all three.

**(a) `yaver-tests/` specs — the in-binary web runner (no Playwright/Node).**
Format: `yaver-tests/landing.test.yaml`. Run with `cd web && npm run dev &` then
`yaver test run yaver-tests/<file>`. Add `yaver-tests/web-wake-states.test.yaml`
asserting, against the dashboard:
- a parked box shows an ETA and, when `lastWakeOutcome` is set, the explanation;
- a `needs-auth` box shows the sign-in action and **no** progress bar;
- the strings `awaiting-yaver-auth`, `checking-snapshot`, `restoring-snapshot`
  never appear as literal text anywhere (invariant 2, asserted directly).
Authenticated specs use `requires_env` and skip cleanly — follow
`web-dashboard-build-panels.test.yaml`.

**(b) Playwright e2e** — model on `e2e/tests/managed-cloud-delete.spec.ts`. Add
`e2e/tests/managed-cloud-wake.spec.ts` for the interaction the in-binary runner
can't easily drive: press Wake → the optimistic rung appears **immediately**
(assert within ~1s, before any poll could have returned) → it yields to real
server state once the row moves.

**(c) redroid** — `yaver-tests/mobile-redroid-auth-smoke.test.yaml` is the
precedent, `target: android-redroid`. Add a wake/park spec for the mobile UI.
If redroid isn't available on this machine, say so and skip the lane — a spec
that cannot run is worse than an honest gap.

## Gate

```
cd web && npx tsc --noEmit && npx tsx lib/wakeProgress.test.ts \
  && cd ../mobile && npx tsc --noEmit && npx tsx --test src/lib/wakeMachine.test.mts \
  && cd ../backend && npx tsc --noEmit -p convex
```

Never `go test ./...` here — `desktop/agent`'s `TestAuthLogout` hits the real
`~/.yaver` and signs this machine out.

## Traps that have already cost time

- **Do not revert or force-push committed work.** If something committed looks
  wrong, land a NEW commit that fixes it.
- **`git commit -- <paths>` only.** Never `-a`, never `add -A`. The index is
  shared with concurrent sessions and goes stale between two commands.
- The repo is `yaver-io/yaver.io`. A stale `kivanccakmak/yaver.io` remote still
  works via redirect and hides its own staleness — check `git remote -v`.
- `deriveServerPhase`'s `default:` branch returns `booting`. Any new
  control-plane slug that lands without a mapper entry silently claims the
  machine is booting when it may not exist yet. Add slugs to BOTH mappers.

## Done means

Every surface that can show a wake shows: which step, how long it has been on
it, what the provider sees, an ETA measured on that box, and — when the wake is
blocked — the action instead of a bar. A parked box explains its last wake. No
surface prints a raw slug. The gate is green and the new specs pass (or their
lane is honestly reported as unavailable).
