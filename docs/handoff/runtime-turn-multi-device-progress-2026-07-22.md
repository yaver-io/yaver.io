# Runtime Turn Multi-Device Progress Handoff

Date: 2026-07-22

Working tree: `/private/tmp/yaver-mobile-wt`

Branch: `main`

Purpose: handoff for continuing the Yaver multi-device development-vibing work.
The goal is a simple, generic remote-runtime turn contract that watch, car,
tvOS, mobile, AR/VR, SDK feedback, and dogfooding flows can all use.

> **Update 2026-07-23 — this work has LANDED on `main`.**
>
> The state described below as "uncommitted in a worktree" is historical. The
> 16 feature files were replayed onto latest `main`, verified against the full
> package (it compiles — that had never actually been checked), and committed.
> A second commit filled the gaps this doc listed as remaining. What changed
> since this handoff was written:
>
> - The queue is **durable and owner-scoped** (`runtime_queue_store.go`), not
>   in-memory. Captured ideas survive an agent restart.
> - `captured` is no longer a black hole — `runtime_turn_run` promotes a
>   captured idea into real work, keeping its original `turnId`.
> - `ready_to_test` no longer claims test-readiness. `runtime_turn_verify`
>   attempts the real reload and reports the **live listener count**;
>   `testTarget.state` is `unverified` / `delivered` / `unreachable`.
> - Mobile has a **Runtime Turns screen** (`mobile/app/runtime-turns.tsx`),
>   reachable from More.
>
> Sections below marked as limitations or "do not claim" have been corrected
> in place. Treat the code as the source of truth regardless.

## Current State

The first vertical slice is implemented in the `main` worktree, but not
committed or pushed.

This is not just on the feature branch anymore. The implementation has been
applied to the existing `main` worktree at:

```text
/private/tmp/yaver-mobile-wt
```

The original feature checkout still exists at:

```text
/Users/kivanccakmak/Workspace/yaver.io
```

That original checkout is on `multi-device-synergy-analysis`; use it only as a
backup/reference unless you intentionally want to compare against the first
branch version.

The simple usage shape is now:

```json
{ "text": "fix this", "run": true }
```

Equivalent full shape still works:

```json
{
  "utterance": "fix this",
  "surface": { "class": "watch", "interaction": "voice" },
  "development": { "queue": { "mode": "enqueue-or-run" } }
}
```

Implemented ops verbs:

- `runtime_turn`
- `runtime_turn_status`
- `runtime_turns`

Simple aliases added:

- `text` and `prompt` are accepted aliases for `utterance`
- `run: true` maps to queue mode `run`
- `queue: true` maps to queue mode `enqueue-or-run`
- `runtime_turn_status` accepts `itemId` or `turnId`

## Files Touched

New files:

- `desktop/agent/runtime_queue.go`
- `desktop/agent/ops_runtime_turn.go`
- `desktop/agent/runtime_turn_test.go`
- `docs/multi-device-synergy-remote-runtime-analysis.md`
- `docs/handoff/runtime-turn-multi-device-progress-2026-07-22.md`

Modified files:

- `mobile/src/lib/runtimeSurfaceTypes.ts`
- `mobile/src/lib/runtimeSurfaceClient.ts`
- `mobile/src/lib/watchPrompt.ts`
- `mobile/src/lib/watchBridge.ts`
- `mobile/src/lib/watchEntry.ts`
- `mobile/src/components/WatchBridgeHost.tsx`
- `mobile/src/lib/watchBridge.test.mts`
- `mobile/app/car-voice-coding.tsx`
- `mobile/src/lib/carReplyDispatch.ts`
- `mobile/src/lib/carReplyDispatch.test.mts`
- `tvos/YaverTV/SessionClient.swift`

## Agent Implementation

`runtime_queue.go` defines the shared queue types and state model:

- `captured`
- `queued`
- `running`
- `needs_input`
- `ready_to_test`
- `ready_to_deploy`
- `done`
- `failed`
- `cancelled`

The queue is durable as of 2026-07-23: it persists to
`~/.yaver/runtime-queue.json` (0600, atomic replace), is scoped to the acting
user, caps at 500 items evicting terminal ones first, and downgrades any
`running` item to `queued` on load — because work cannot still be running after
the process died. See the postmortem block at the top of
`desktop/agent/runtime_queue_store.go`.

Important behavior:

- Idea utterances default to capture, not blind code edits.
- `idea: make ...` is classified as `idea-capture` before implementation verbs.
- `run: true`, `queue: true`, and `development.queue.mode` decide whether work
  is captured or started.
- Live runner session turns route through the existing runner-session-turn path.
- Normal development work creates a normal task via `CreateTaskWithOptions`.
- Completed task-backed turns map to `ready_to_test`.
- Deploy is never automatic.

`ready_to_test` means the remote task completed — nothing more. It is NOT a
claim that the mobile container reloaded. That distinction is now explicit in
the payload: `testTarget.state` starts at `unverified`, and only
`runtime_turn_verify` can move it, by actually broadcasting the reload and
reporting how many live command streams accepted it (`delivered`) or that none
did (`unreachable`). A registered phone that is not holding the stream counts
as zero.

## Mobile Shared Client

`runtimeSurfaceClient` now exposes:

- `runtimeTurn(target, request)`
- `turn(target, text, opts)`
- `runtimeTurnStatus(target, itemId)`
- `runtimeTurns(target, limit)`
- `waitForRuntimeTurnDone(target, initial, opts)`
- `isRuntimeTurnTerminal(state)`

For simple usage, prefer:

```ts
await runtimeSurfaceClient.turn(deviceId, "fix the startup flicker", {
  run: true,
});
```

For surface-specific usage, pass `surface` and `development` metadata.

## Watch Wiring

Watch bridge now accepts an injected `runtimeTurn` transport.

Production wiring in `WatchBridgeHost.tsx` injects:

- target device id
- watch surface metadata
- queue semantics

Behavior:

- Ideas route to runtime-turn capture/queue semantics.
- Ready-to-test becomes a watch-safe summary.
- `needs_input` becomes a confirmation/attention response.
- Failed states speak a short failure line.

## Car Wiring

`mobile/app/car-voice-coding.tsx` now sends normal development turns through
`runtime_turn` after:

- STT
- machine switching
- risky-command safety gate
- meeting/mail surface intents

The old car task loop remains as fallback for older agents that do not know
`runtime_turn`.

Android Auto MessagingStyle replies also use `runtime_turn` through
`carReplyDispatch.ts` when the dependency is injected.

Driving safety is preserved:

- risky deploy/push/delete commands still require confirmation
- car readback stays one sentence
- car does not read code/diffs/logs aloud

## tvOS Wiring

`tvos/YaverTV/SessionClient.swift` now tries `runtime_turn` first for:

- prompt turns
- menu choice turns

It falls back to direct `/runner/session/turn` if the agent does not support the
new verb.

The TV still renders panel text and speaks a summarized line.

## Validation Done

From `/private/tmp/yaver-mobile-wt/mobile`:

```sh
npx tsx src/lib/watchBridge.test.mts &&
npx tsx src/lib/runtimeSurfaceClient.test.mts &&
npx tsx src/lib/carReplyDispatch.test.mts
```

Result: passed.

tvOS touched-file typecheck:

```sh
xcrun swiftc -typecheck \
  tvos/YaverTV/AgentClient.swift \
  tvos/YaverTV/SessionClient.swift \
  tvos/YaverTV/Models.swift \
  tvos/YaverTV/Backend.swift \
  tvos/YaverTV/YaverStore.swift \
  tvos/YaverTV/MachineRegistry.swift \
  tvos/YaverTV/BoxLifecycle.swift \
  tvos/YaverTV/Speech.swift
```

Result: passed.

Go runtime-turn tests were validated in a clean detached worktree by copying
only:

- `desktop/agent/runtime_queue.go`
- `desktop/agent/ops_runtime_turn.go`
- `desktop/agent/runtime_turn_test.go`

Then running:

```sh
cd desktop/agent && go test . -run 'TestRuntimeTurn'
```

Result: passed.

## What Is Done

Agent:

- Added a shared runtime-turn queue model.
- Added `runtime_turn` for capture/enqueue/run/session turns.
- Added `runtime_turn_status` for refreshing one queue item.
- Added `runtime_turns` for listing recent queue items.
- Added simple request normalization:
  - `text` -> `utterance`
  - `prompt` -> `utterance`
  - `run: true` -> queue mode `run`
  - `queue: true` -> queue mode `enqueue-or-run`
  - `surface.id` can fill `surface.class`
- Added tests for:
  - idea capture without running
  - queue-backed task creation
  - idea capture enqueued as a task when requested
  - task completion -> `ready_to_test`
  - empty input rejection
  - simple alias payloads
  - recent queue listing

Mobile shared runtime client:

- Added runtime-turn TypeScript types.
- Added direct ops wrappers:
  - `runtimeTurn`
  - `runtimeTurnStatus`
  - `runtimeTurns`
- Added simple helper:
  - `runtimeSurfaceClient.turn(target, text, opts)`
- Added polling helper:
  - `waitForRuntimeTurnDone`
- Added terminal-state helper:
  - `isRuntimeTurnTerminal`

Watch:

- Added `runtimeTurn` as an injectable watch bridge dependency.
- Wired `WatchBridgeHost` to provide that dependency using the selected target
  device.
- Watch idea capture now routes through runtime-turn semantics when available.
- Watch summaries map runtime states into watch-safe replies.
- Existing legacy task dispatch remains as fallback.

Car:

- Car voice coding screen now uses `runtime_turn` after STT, machine-switch,
  risk gate, and car-safe assistant intents.
- Car voice keeps legacy task loop fallback for older agents.
- Android Auto RemoteInput replies can use runtime-turn through
  `carReplyDispatch`.
- Risky commands still require explicit confirmation before runtime-turn
  dispatch.
- Car readback remains one short spoken sentence.

tvOS:

- `SessionClient` now tries `runtime_turn` first.
- Direct `/runner/session/turn` stays as fallback.
- Prompt and choice turns both go through the runtime-turn attempt.
- TV panel rendering still receives pane text when available.

Docs:

- Deep analysis doc added:
  - `docs/multi-device-synergy-remote-runtime-analysis.md`
- This handoff doc added:
  - `docs/handoff/runtime-turn-multi-device-progress-2026-07-22.md`

## What Remains

Must-do next for usability:

- Add a mobile Runtime Turns screen:
  - list recent runtime turns
  - poll active turns
  - open relevant task/session/detail
  - show `ready_to_test` with a clear test action
- Wire completion notification:
  - watch should hear/feel completion after initial ack
  - car should get a safe one-line completion message
  - phone should receive detailed completion/error context
- Verify real test readiness:
  - `ready_to_test` currently means task completed
  - it should eventually mean app reload/build/container verification passed

Should-do soon:

- Persist runtime-turn queue across agent restarts.
- Add queue item detail endpoint or richer `runtime_turn_status` response if
  mobile UI needs it.
- Add SDK evidence ingestion:
  - screenshots
  - video clips
  - console/error context
  - current route/build/version
- Add explicit deploy preflight:
  - TestFlight
  - Google Play internal
  - never automatic
  - confirmation required from phone/watch
- Add tvOS runtime-turn list/dashboard, not only session-turn integration.

Later:

- AR/VR spatial panels using the same runtime-turn contract.
- Dogfood Yaver-from-Yaver end-to-end:
  - capture issue from Yaver mobile/watch/car/TV
  - run on remote runtime
  - reload Yaver mobile container
  - ask for internal deploy
- Durable cross-device target aliases.
- Multi-device handoff history.

## What To Check In Code

Agent checks:

- Confirm ops verbs are registered:

```sh
cd /private/tmp/yaver-mobile-wt
rg -n 'Name: "runtime_turn|Name: "runtime_turn_status|Name: "runtime_turns"' desktop/agent
```

- Confirm simple aliases are normalized:

```sh
rg -n 'normalizeRuntimeTurnRequest|Text string|Prompt string|Run bool|Queue bool' desktop/agent
```

- Confirm queue state mapping:

```sh
rg -n 'runtimeQueueStateFromTask|runtimeTurnSpokenFromTask|ready_to_test' desktop/agent
```

Mobile checks:

- Confirm shared client helpers:

```sh
rg -n 'runtimeTurn:|turn:|runtimeTurnStatus:|runtimeTurns:|waitForRuntimeTurnDone|isRuntimeTurnTerminal' mobile/src/lib/runtimeSurfaceClient.ts
```

- Confirm watch injection:

```sh
rg -n 'runtimeTurn' mobile/src/components/WatchBridgeHost.tsx mobile/src/lib/watchBridge.ts mobile/src/lib/watchEntry.ts
```

- Confirm car runtime-turn path:

```sh
rg -n 'runtimeTurn|waitForRuntimeTurnDone|spokenForRuntimeTurn' mobile/app/car-voice-coding.tsx mobile/src/lib/carReplyDispatch.ts
```

tvOS checks:

```sh
rg -n 'runtimeTurn|directTurn|runtime_turn' tvos/YaverTV/SessionClient.swift
```

## How To Check It

Focused mobile tests:

```sh
cd /private/tmp/yaver-mobile-wt/mobile
npx tsx src/lib/watchBridge.test.mts
npx tsx src/lib/runtimeSurfaceClient.test.mts
npx tsx src/lib/carReplyDispatch.test.mts
```

Expected result:

- all tests pass

tvOS typecheck:

```sh
cd /private/tmp/yaver-mobile-wt
xcrun swiftc -typecheck \
  tvos/YaverTV/AgentClient.swift \
  tvos/YaverTV/SessionClient.swift \
  tvos/YaverTV/Models.swift \
  tvos/YaverTV/Backend.swift \
  tvos/YaverTV/YaverStore.swift \
  tvos/YaverTV/MachineRegistry.swift \
  tvos/YaverTV/BoxLifecycle.swift \
  tvos/YaverTV/Speech.swift
```

Expected result:

- no output and exit code 0

Go check in a clean worktree, because the current `main` worktree has unrelated
dirty/staged changes that break package compilation:

```sh
cd /private/tmp/yaver-mobile-wt
git worktree add --detach /tmp/yaver-rt-validate HEAD
cp \
  desktop/agent/runtime_queue.go \
  desktop/agent/ops_runtime_turn.go \
  desktop/agent/runtime_turn_test.go \
  /tmp/yaver-rt-validate/desktop/agent/
cd /tmp/yaver-rt-validate/desktop/agent
go test . -run 'TestRuntimeTurn'
cd /private/tmp/yaver-mobile-wt
git worktree remove --force /tmp/yaver-rt-validate
```

Expected result:

- `ok github.com/yaver-io/agent ...`

Do not run destructive cleanup commands. If removing the temporary validation
worktree, inspect it first if there is any doubt.

## Manual Smoke Test Shape

Once an agent build includes these files, a minimal ops call should work:

```json
{
  "verb": "runtime_turn",
  "payload": {
    "text": "idea: make the disconnected screen explain the failed probe",
    "surface": { "class": "watch" }
  },
  "machine": "local"
}
```

Expected:

- `ok: true`
- state `captured`
- spoken line similar to `Captured. I'll attach it to the current app.`
- queue item has intent class `idea-capture`

Run-mode call:

```json
{
  "verb": "runtime_turn",
  "payload": {
    "text": "fix the startup flicker",
    "run": true,
    "surface": { "class": "watch" }
  },
  "machine": "local"
}
```

Expected:

- `ok: true`
- state `running` or `queued`
- response has `turnId`
- queue item has a `taskId` when task creation succeeds

Status call:

```json
{
  "verb": "runtime_turn_status",
  "payload": { "turnId": "rq_..." },
  "machine": "local"
}
```

Expected:

- active task maps to `queued` / `running`
- completed task maps to `ready_to_test`
- failed task maps to `failed`

List call:

```json
{
  "verb": "runtime_turns",
  "payload": { "limit": 25 },
  "machine": "local"
}
```

Expected:

- `items` contains recent runtime-turn queue items newest-first
- `count` equals returned item count

## What Not To Claim Yet

Still NOT complete (do not claim these):

- **Automatic deploy — and this is deliberate, not a gap.**
  `runtime_turn_deploy_preflight` checks shippability and prints the command;
  a human runs it. A voice surface cannot meaningfully consent to a store
  submission, and TestFlight has no rollback.
- **Evidence blobs.** `runtime_turn_evidence` attaches *refs*; screenshots and
  clips stay on the device/box and are never uploaded to Convex.
- end-to-end dogfood deploy (capture on watch → ship from phone, unbroken)
- a spatial/AR-VR *native* panel — the spatial surface class is handled
  agent-side, but the RN glass screens still render the generic view

Complete as of 2026-07-23:

- durable, owner-scoped queue
- the full verification ladder: `unverified` → `delivered` → `verified`, with
  `verified` set ONLY from a device-emitted event (`runtime_turn_ack.go`)
- mobile Runtime Turns screen, with Run it / Test on device / Ship it?
- completion announcements on watch + car (`runtimeTurnAnnouncer.ts`), which
  fire on transitions only and never read a stack trace aloud
- evidence attachment to an existing turn
- deploy preflight with full blocker list
- spatial + shared-TV surface classes; tvOS runtime-turns list

The current implementation is the shared contract and first working vertical
slice.

## Validation Caveats

Running `go test . -run 'TestRuntimeTurn'` directly inside
`/private/tmp/yaver-mobile-wt/desktop/agent` is currently blocked by unrelated
dirty/staged main-worktree changes:

- `s.handleGuestDelete` undefined
- `DeleteGuest` undefined
- `StartMobileProjectPeriodicRefresh` undefined

Those are not from the runtime-turn implementation.

Earlier broad mobile typecheck in the other checkout was blocked by unrelated
`runnerBannerState.test.ts` fixture errors around `models` / `isDefault`.

## Important Git State

There are two relevant worktrees:

- `/Users/kivanccakmak/Workspace/yaver.io`
  - branch: `multi-device-synergy-analysis`
  - original feature work was created here
- `/private/tmp/yaver-mobile-wt`
  - branch: `main`
  - current requested implementation has been applied here

`main` in `/private/tmp/yaver-mobile-wt` has many unrelated dirty/staged files.
Do not reset or restore them.

The runtime-turn files were clean before applying this work to `main`.

A recovery stash still exists in the original checkout:

```text
stash@{0}: On multi-device-synergy-analysis: runtime-turn-implementation
```

Do not drop it until the main-worktree version is committed or otherwise safely
preserved.

## Main / Rebase / Conflict Notes

The requested implementation is now in the `main` worktree:

```text
/private/tmp/yaver-mobile-wt
```

At the time of handoff, that worktree reported:

```text
On branch main
Your branch is behind 'github/main' by 36 commits, and can be fast-forwarded.
```

Do not blindly `git pull`, `git rebase`, `git reset`, or restore files in this
worktree. It already contains many unrelated staged and unstaged changes that
pre-date this runtime-turn work. A normal rebase/pull may produce conflicts or
mix this feature with unrelated cloud/provider/mobile changes.

Runtime-turn feature paths to preserve during any rebase/merge:

- `desktop/agent/runtime_queue.go`
- `desktop/agent/ops_runtime_turn.go`
- `desktop/agent/runtime_turn_test.go`
- `docs/multi-device-synergy-remote-runtime-analysis.md`
- `docs/handoff/runtime-turn-multi-device-progress-2026-07-22.md`
- `mobile/src/lib/runtimeSurfaceTypes.ts`
- `mobile/src/lib/runtimeSurfaceClient.ts`
- `mobile/src/lib/watchPrompt.ts`
- `mobile/src/lib/watchBridge.ts`
- `mobile/src/lib/watchEntry.ts`
- `mobile/src/components/WatchBridgeHost.tsx`
- `mobile/src/lib/watchBridge.test.mts`
- `mobile/app/car-voice-coding.tsx`
- `mobile/src/lib/carReplyDispatch.ts`
- `mobile/src/lib/carReplyDispatch.test.mts`
- `tvos/YaverTV/SessionClient.swift`

Most likely conflict areas:

- `mobile/src/lib/runtimeSurfaceTypes.ts`
  - preserve `RuntimeTurnRequest`, `RuntimeTurnResponse`,
    `RuntimeTurnListResponse`, and the simple aliases `text`, `prompt`, `run`,
    `queue`
- `mobile/src/lib/runtimeSurfaceClient.ts`
  - preserve `runtimeTurn`, `turn`, `runtimeTurnStatus`, `runtimeTurns`,
    `waitForRuntimeTurnDone`, and `isRuntimeTurnTerminal`
- `mobile/src/components/WatchBridgeHost.tsx`
  - preserve the injected `runtimeTurn` transport
- `mobile/src/lib/watchBridge.ts`
  - preserve the runtime-turn path before legacy dispatch fallback
- `mobile/app/car-voice-coding.tsx`
  - preserve runtime-turn submission after transcription/risk gate and before
    legacy fallback
- `mobile/src/lib/carReplyDispatch.ts`
  - preserve optional `runtimeTurn` dependency and risky-confirm release through
    runtime-turn
- `tvos/YaverTV/SessionClient.swift`
  - preserve runtime-turn-first behavior with direct session fallback

New Go files should normally have no textual conflicts unless upstream added
similar verbs:

- `desktop/agent/runtime_queue.go`
- `desktop/agent/ops_runtime_turn.go`
- `desktop/agent/runtime_turn_test.go`

If upstream added a competing runtime-turn implementation, reconcile around the
simple usage contract:

```json
{ "text": "fix this", "run": true }
```

and keep these external verbs stable:

- `runtime_turn`
- `runtime_turn_status`
- `runtime_turns`

Recommended safe flow before rebasing:

```sh
cd /private/tmp/yaver-mobile-wt
git status --short -- \
  desktop/agent/runtime_queue.go \
  desktop/agent/ops_runtime_turn.go \
  desktop/agent/runtime_turn_test.go \
  docs/multi-device-synergy-remote-runtime-analysis.md \
  docs/handoff/runtime-turn-multi-device-progress-2026-07-22.md \
  mobile/src/lib/runtimeSurfaceTypes.ts \
  mobile/src/lib/runtimeSurfaceClient.ts \
  mobile/src/lib/watchPrompt.ts \
  mobile/src/lib/watchBridge.ts \
  mobile/src/lib/watchEntry.ts \
  mobile/src/components/WatchBridgeHost.tsx \
  mobile/src/lib/watchBridge.test.mts \
  mobile/app/car-voice-coding.tsx \
  mobile/src/lib/carReplyDispatch.ts \
  mobile/src/lib/carReplyDispatch.test.mts \
  tvos/YaverTV/SessionClient.swift
```

Then either commit only those files, or create a patch from only those files
before any rebase. Do not include unrelated staged deletions or cloud/provider
work in a runtime-turn commit.

## Security And Product Invariants

Keep these intact:

- Do not deploy, publish, push tags, or release without explicit user
  confirmation.
- Watch/car/TV are control surfaces only; secrets stay on the runtime or secure
  stores.
- The relay is multi-tenant. Do not trust relay routing as an authorization
  boundary.
- Car surfaces must not read code, diffs, logs, stack traces, or long output
  aloud.
- TV can show more than watch/car, but should redact home paths before display
  and speech.
- Do not hardcode user paths or assume Windows/Mac/Linux layouts.

## Remaining Work

Priority 1 — all closed 2026-07-23:

- ~~Add durable queue storage for runtime turns.~~ `runtime_queue_store.go`
- ~~Add a small mobile queue/status screen backed by `runtimeTurns`.~~
  `mobile/app/runtime-turns.tsx`
- ~~Verify mobile-container reload before marking `ready_to_test` as truly
  test-ready.~~ `runtime_turn_verify` + device ack in `runtime_turn_ack.go`
- ~~Make watch/car subscribe or poll after initial ack so they can announce
  done.~~ `runtimeTurnAnnouncer.ts` + `RuntimeTurnAnnouncerHost.tsx`
- ~~Close the last verification gap.~~ The device now echoes the turn id on its
  `preview_worker_bundle_loaded` / `_failed` event, so `verified` comes from
  the device, not from an agent-side inference.

Deliberately NOT done: an HTTP wrapper. No non-ops native client needs one —
tvOS, watch, car and mobile all reach the verbs through `/ops`. Adding a second
door would be a second thing to keep authorized.

Priority 2:

- Attach SDK feedback evidence bundles directly to `runtime_turn`.
- Add AR/VR-specific surface panels using the same request contract.
- Add explicit deploy preflight and phone/watch deploy confirmation flow.
- Dogfood Yaver-on-Yaver: tag queue items with `project=yaver`, route evidence
  from Yaver mobile, reload Yaver mobile container, then ask for internal deploy.

Priority 3:

- Persist cross-device target aliases.
- Add queue item ownership/device filters.
- Add push notification / TTS completion delivery.
- Add list/detail UI on tvOS for runtime turns, not just session turns.

## Recommended Next Claude Code Task

Start with the mobile queue/status UI because it makes the whole feature usable:

1. Add a lightweight Runtime Turns screen in mobile.
2. Use `runtimeSurfaceClient.runtimeTurns(deviceId, 25)`.
3. Poll active items with `runtimeTurnStatus`.
4. Show `ready_to_test` with a button to open the Yaver mobile container.
5. Show `needs_input` with a handoff to the existing runner/session UI.
6. Keep deploy as an explicit confirmation CTA only, not automatic.

Do not start with persistence or deploy automation; those are larger and easier
to get wrong before the UI proves the contract.
