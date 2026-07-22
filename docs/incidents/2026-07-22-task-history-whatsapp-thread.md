# Task history was not WhatsApp-like: history vanished, nothing cached

**Date:** 2026-07-22
**Surfaces:** mobile (primary), web (inherits the server fix), Go agent
**Reported as:** *"i lost summary its not like that in claude mobile app etc"* →
*"make it like whatsapp"* → *"i should see agent's prior responses and my prior
requests"* → *"perfect, no miss data, instant, caching, never duplicate"*.

## Symptom

Opening a task chat showed only the **latest single exchange**. Sending a
follow-up appeared to "start a fresh chat" — the earlier `Hello!` and its reply
were gone. When history *did* appear (right after a fork) it **vanished again a
few seconds later**. Nothing was cached, so cold start showed an empty screen
until the network answered.

## Root causes (three, compounding)

All three trace back to one wire fact: **`GET /tasks` (the list) strips
`Turns`** to bound its payload (`httpserver.go:4079` nils `Turns`, keeps
`TurnCount`), while **`getTask` (detail) returns the full turns**. Anything that
fed the chat from a *list* row therefore had no history.

1. **Server: a forked child carried no parent history.** Continuing a finished
   task forks a new child (resuming a completed runner session is unreliable).
   `task_fork.go` compacted the parent thread into the hidden *handoff prompt*
   but never seeded the child's `Turns`. So the fetched child showed only its
   own one exchange.

2. **Client: no hydrate-on-open.** Tapping a task from the list did
   `setSelectedTask(listRow)` — turns stripped — and `buildChatMessages` fell
   back to "title + last result" = one exchange. The only `getTask` calls were
   for *running* tasks on idle, and the pill path.

3. **Client: the 3s poll wiped the open thread.** `fetchTasks`'s
   `setSelectedTask(nextTasks.find(id===prev.id))` replaced the open task with
   the stripped list row every 3 seconds — erasing hydrated turns — and
   hydration didn't re-run because the id was unchanged. **This is why history
   flickered in and then vanished.** The server fix alone could never survive
   it.

## What was changed

### Go agent (server) — ships history to *every* surface at once
- `desktop/agent/tasks.go`
  - `TaskCreateOptions.SeedTurns` — display-only prior turns to prepend to a new
    task's `Turns` (never re-sent to the runner).
  - `CreateTaskWithOptions` prepends seeded turns, **dedups the seam** (the
    client optimistically appends the follow-up to the parent, so the parent
    tail can equal the child's first user turn), drops empties.
  - `seedForkTurns` + `maxSeededForkTurns=40` — bound so a long fork chain can't
    bloat every child's persisted state.
- `desktop/agent/task_fork.go` — `SeedTurns: parent.Turns`, so a fork renders as
  one continuous thread. The `[Conversation Handoff]` scaffold never leaks into
  visible bubbles.
- Tests: `TestHandleTaskForkSeedsParentTurnsForContinuousThread`,
  `TestCreateTaskSeedTurnsDedupesSeamDuplicate`, `TestSeedForkTurnsBoundsTail`.

### Mobile — reliable, instant, cached, deduped
- `mobile/src/lib/quic.ts` — parse `turnCount` in both list & detail, so the UI
  can tell "opened-from-list, needs hydration" (turnCount>0, turns empty) from
  "genuinely empty".
- `mobile/app/(tabs)/tasks.tsx`
  - **Hydrate-on-open**: paint cached turns *instantly*, then reconcile via
    `getTask` (server wins), then refresh the cache.
  - **Poll-clobber fix** (`keepTurns` merge): a stripped poll row never
    overwrites in-memory turns — the vanishing bug, closed.
  - **Instant cold-start list** + per-task turns cache wired in.
  - **WhatsApp-style assistant bubble**: assistant replies now render as a left
    bubble (subtle fill, rounded, bottom-left tail, snug width) mirroring the
    user's right bubble, instead of borderless full-width text. Fenced code
    keeps its own inner border.
- `mobile/src/lib/storage.ts` — `cacheTaskTurns`/`getCachedTaskTurns`
  (LRU-bounded, cap 60 tasks / 256KB each) + `nextTurnsCacheIndex` (pure,
  tested) + wired the previously-unused `cacheTaskList`/`getCachedTaskList`.
- Test: `mobile/src/lib/turnsCacheIndex.test.ts` (5/5, LRU bounding + no-dup).

### Dedup — "never show duplicate sent/received"
`buildChatMessages` keeps adjacent-exact dedup (collapses optimistic+server, the
fork seam, and cache→authoritative). The server also dedups the fork seam. Cache
hydration *replaces* rather than appends, so no accumulation.

## Verification

- **Static / unit — done:**
  - `desktop/agent`: `go build ./...` clean; fork seeding/dedup/bounds tests pass.
  - `mobile`: `tsc --noEmit` clean on all touched files; `turnsCacheIndex.test.ts`
    5/5.
- **On-device — pending at time of writing.** The Go agent fix is live on the
  local MacBook dev agent (so a phone pointed at that box sees the continuous
  thread). The mobile changes require an app install; the iOS build succeeded
  (see toolchain note) and a TestFlight upload was cut for OTA testing.

## Toolchain note (incident within the incident)

Local iOS builds were blocked twice, unrelated to the code:
1. **DDI won't mount** — the iPhone is on **iOS 26.5.2** but this Mac's Xcode
   ships the **26.2 SDK**; the device is newer than Xcode, so the developer disk
   image can't mount for a direct device build (`yaver wire push` failed). Bypass:
   build for `generic/platform=iOS` and install with `devicectl` (no DDI).
2. **`whisper.rn` codegen drift** — node_modules and Pods had drifted: the
   CocoaPods `ReactCodegen` umbrella referenced `RNWhisperSpec`, but RN codegen
   discovery no longer emitted it, so every build died on the missing
   `RNWhisperSpecJSI-generated.cpp`. Fix: generate the spec directly —
   `combine-js-to-schema-cli.js` then `generate-all.js` into
   `ios/build/generated/ios` — which survives the build's per-library codegen
   phase. The build then succeeded.

## What is explicitly NOT done / open

- **On-device runtime confirmation** by the user (the "perfect in actual use"
  step) — pending the TestFlight build landing + the user testing
  `Hello! → What's up?`.
- **No deploy of the Go agent** beyond the local MacBook dev process — simkab /
  mini / other boxes still need the agent change to see continuous threads for
  tasks they host. (They were separately pointed at `desiredAgentVersion` for an
  unrelated relay fix.)
- **Web** inherits the server fix for free (it reads `activeTask.turns`), but was
  not separately re-tested here.
- **Underlying codegen drift** was worked around, not root-caused; a clean
  `node_modules` reinstall + `pod install` is the durable fix but was skipped to
  avoid churning the shared checkout.

## Related memory

`project_mobile_followup_ui_forks_silently` — carries the full three-cause
diagnosis and the KEY LESSON: *a payload-bounding trim on a LIST endpoint
silently breaks any client that syncs detail state from list rows; the trim and
the sync are coupled across the wire.*
