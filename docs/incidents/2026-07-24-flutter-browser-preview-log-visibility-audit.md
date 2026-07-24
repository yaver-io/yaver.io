# Flutter Browser Preview Log Visibility Audit — 2026-07-24

## Trigger

TestFlight Yaver mobile opened the Flutter project `e-mobile (Elevathor)` through the browser/WebView lane. The screen stayed on:

```text
Starting flutter dev server...
flutter run -d web-server
0:43 elapsed · waiting for the first output from the box
```

The user first reported the same state for about 2:40, then again at 4:22. That second timestamp changes the diagnosis: it is longer than the agent's Flutter web startup deadline, so the phone is not merely hiding slow compiler output. It is also failing to surface the terminal failure or failing to notice that the start lifecycle has moved on.

This audit is about the browser load path for Flutter web, not the Hermes/native bundle path.

## Relevant Code Paths

- Mobile Apps browser lane: `mobile/app/(tabs)/apps.tsx`
- Mobile Tasks embedded preview: `mobile/src/components/DevPreview.tsx`
- Shared lane progress text: `mobile/src/components/LaneStartupStatus.tsx`, `mobile/src/lib/laneProgress.ts`
- Agent dev-server manager and SSE events: `desktop/agent/devserver.go`, `desktop/agent/devserver_http.go`, `desktop/agent/devserver_progress.go`
- Flutter start command: `FlutterDevServer.Start` and `FlutterDevServer.startProcessWithStdin` in `desktop/agent/devserver.go`

## What The Product Should Do

For a Flutter browser preview, the mobile overlay should show all of these within seconds:

1. The exact command and working directory:

   ```text
   $ flutter run -d web-server --web-port 9100 --web-hostname 0.0.0.0 (in /path/to/e-mobile)
   ```

2. The live lane state:

   ```text
   0:43 elapsed · last output 3s ago
   ```

3. A rolling tail of recent Flutter output:

   ```text
   Resolving dependencies...
   Launching lib/main.dart on Web Server in debug mode...
   Waiting for connection from debug service on Web Server...
   ```

4. If the stream is alive but Flutter is quiet, the UI should say that explicitly:

   ```text
   agent stream alive · process pid 12345 · no Flutter output for 53s
   ```

5. If the dev server exits or times out, the failure panel should show the tail from the agent, not a generic timeout.

## Current Architecture

The agent already has most of the right primitives:

- `/dev/events` is SSE.
- `DevServerEvent` supports `log`, `phase`, `progress`, `snapshot`, `heartbeat`, `ready`, `error`, and `stopped`.
- `DevServerManager.heartbeatLoop` emits `heartbeat` and `snapshot` every 5 seconds.
- Snapshots include `recentLogs`, process liveness, pid, idle seconds, port, workDir, and progress.
- Flutter uses `flutter run -d web-server --web-port <port> --web-hostname 0.0.0.0` for browser lane.
- Flutter startup waits up to 180 seconds before returning a startup failure.
- `startProcessWithStdin` is intended to emit an immediate command log before the Flutter process produces its first line.
- `isActiveDevServerStatus` currently returns true only for `running === true || building === true`.

So the missing user signal is not primarily an agent data-model problem. The data model can carry it.

## Findings

### 1. The Apps WebView SSE parser can drop split SSE frames

`mobile/app/(tabs)/apps.tsx` reads chunks from `/dev/events`, splits each chunk by `\n`, and parses lines that start with `data: `.

That is not a safe SSE parser. A JSON event can be split across network chunks. When that happens, the first chunk might contain:

```text
data: {"type":"log","framework":"flutter","log
```

and the next chunk might contain:

```text
Line":"Resolving dependencies..."}
```

The current parser drops both pieces. On relay/mobile networks this is a realistic failure mode. The shared `quicClient.subscribeDevEvents` helper already buffers until `\n\n`; the Apps WebView path should use that helper or the same buffering logic.

Impact: the agent can emit logs correctly while the mobile overlay still says `waiting for the first output from the box`.

### 2. The Apps WebView overlay ignores snapshots and heartbeats

`LaneStartupStatus` receives `lastOutputAt`. In the Apps WebView path, that timestamp is updated for `log`, `building`, and `error` events.

It is not updated from:

- `snapshot`
- `heartbeat`
- `phase`
- `progress`

For Flutter, there may be long quiet periods between meaningful stdout lines. The agent is still alive and emits snapshots every 5 seconds, but the Apps overlay does not count those as stream liveness.

Impact: a healthy but quiet compile is presented as "waiting for first output" or later "no output", even though the control channel is live.

The UI needs two clocks:

- `lastStreamAt`: last SSE event or keepalive-class event from the agent.
- `lastOutputAt`: last actual Flutter stdout/stderr line.

Those are different facts and should be rendered differently.

### 3. The first command log is emitted before `cmd.Start`, but not recorded in `recentLogs`

`FlutterDevServer.startProcessWithStdin` emits:

```go
f.emitFn(DevServerEvent{Type: "log", LogLine: "$ flutter ... (in ...)"})
```

That direct emit does not call `recordRecentLog`. If the mobile subscriber connects after this event, the command line may be available only through event history, not through the next snapshot's `recentLogs`.

History replay should cover this for default `/dev/events`, but snapshot is documented as the source of truth for reconnecting consumers. The command line should be in `recentLogs` too.

Impact: late mobile subscribers can miss the most useful line: what command was actually launched and where.

### 4. Mobile has two browser preview implementations with drift

There are at least two browser-lane consumers:

- `mobile/app/(tabs)/apps.tsx`
- `mobile/src/components/DevPreview.tsx`

They parse `/dev/events` separately. `DevPreview.tsx` has buffering for partial lines through an `incomplete` string. Apps does not. Both implement their own event handling instead of sharing one browser-lane event consumer.

Impact: fixes can land in one tab and not the other. A user can see better logs in Tasks but not Apps, or the reverse.

### 5. The retry budget is shorter than the agent's Flutter startup budget

The Apps and DevPreview WebView retry loops are around:

```text
30 retries * 2.5s = 75s
```

The agent gives Flutter web:

```text
180s
```

The text says first web compile can take "up to a minute", while the agent comment says Flutter can take 3+ minutes.

Impact: the mobile UI can declare failure or continue a misleading waiting state while the agent still considers Flutter startup in progress.

The mobile budget should be aligned with the agent deadline, or the failure message should say "still compiling on the box" when the process is alive.

### 6. Flutter carriage-return progress is still weak

`devLogWriter.Write` splits only on `\n`. Flutter and Dart tooling can update progress using carriage returns (`\r`) or long silent phases. A comment says Flutter spinner output is surfaced via `\r` handling, but the current writer code only flushes on newline.

Impact: some real Flutter activity may never become a `log` event until a newline arrives.

This is a code/doc mismatch. Either implement bounded `\r` flushing or remove the misleading comment.

### 7. A failed dev-server status is dropped by mobile polling

The agent intentionally keeps a failed session around so `/dev/status` can report the failure:

```go
if setter, ok := ds.(interface{ SetError(string) }); ok {
    setter.SetError(err.Error())
}
...
m.active.failed = true
```

But the mobile helper says only running/building statuses are active:

```ts
export function isActiveDevServerStatus(status) {
  return !!status && (status.running === true || status.building === true);
}
```

Both Apps and DevPreview use this helper in polling. A status like this can be discarded:

```json
{
  "framework": "flutter",
  "running": false,
  "building": false,
  "error": "flutter did not become ready within 180s\n..."
}
```

Impact: after the agent hits the 180s Flutter deadline, the mobile poll can erase the failed status instead of turning the overlay into the failure panel with logs. If the modal still has stale local state, the user can remain on the old spinner past 4 minutes.

This is the strongest explanation for the 4:22 report. Past 180 seconds, the required behavior is no longer "show progress"; it is "show the startup failure or prove the agent is still running a newer attempt."

### 8. The WebView overlay has no absolute max-start timer tied to the opened attempt

The modal has retry counters, but the visible overlay text is driven by local state and `devStatus`. There is no explicit `openedAttemptStartedAt + agentDeadline + grace` guard that forces a failure panel when:

- no content is rendered,
- no ready event arrived,
- and the attempt has exceeded the agent's known Flutter deadline.

Impact: if polling drops the error and SSE misses the `error` event, the overlay can keep rendering "starting" indefinitely.

## Likely Root Cause For The Screenshot

The initial screenshot is consistent with the Apps browser preview overlay opening before it receives a parseable `log` event. There are three plausible paths:

1. The immediate command log was emitted before the mobile SSE subscription was active, and it was not present in `recentLogs`.
2. The subsequent SSE log frames were split across fetch chunks and dropped by the Apps parser.
3. Flutter produced no newline-terminated output for a while, while snapshots/heartbeats arrived but were ignored for `lastOutputAt`.

Any one of those can leave the UI saying "waiting for the first output from the box" while the agent is actually working.

The 4:22 update adds a second root cause:

4. The agent should have timed out Flutter startup at 180s and exposed `status.error`, but mobile polling likely discarded that failed status because `isActiveDevServerStatus` ignores error-bearing statuses. If the SSE error was also missed, the modal had no remaining path to leave the spinner.

## Required Fixes

### P0 — Use one robust `/dev/events` parser everywhere

Replace the Apps WebView raw SSE loop with `quicClient.subscribeDevEvents`, or extract a shared parser that buffers complete SSE frames by `\n\n`.

Acceptance test:

- Feed a `data: {...}\n\n` frame split across arbitrary byte chunks.
- Verify Apps preview receives the log line.

### P0 — Preserve failed dev-server statuses

Change the dev-server status predicate or the polling call sites so failure is a first-class state:

```ts
export function isRelevantDevServerStatus(status) {
  return !!status && (
    status.running === true ||
    status.building === true ||
    !!String(status.error || "").trim()
  );
}
```

Then render `status.error` into the browser preview failure panel. Do not set `devStatus` to `null` until the user closes the preview or the agent reports no framework/workDir/error.

Acceptance test:

- Mock `/dev/status` returning `{ framework: "flutter", running: false, building: false, error: "flutter did not become ready within 180s\nTAIL" }`.
- Verify Apps preview shows `Dev server didn't come up` and includes `TAIL`.
- Verify it does not continue showing `Starting flutter dev server...`.

### P0 — Add a modal-level absolute deadline

For Flutter browser lane:

```text
deadline = webPreviewStartedAt + 190s
```

If content is not rendered by then and no newer attempt has started, force the failure panel with:

```text
Flutter did not serve content after 3:10. The agent should have reported a startup error; checking latest status...
```

Then poll `/dev/status` once and show either `status.error`, the latest snapshot logs, or a concrete "lost contact with agent" message.

This is a backstop, not the primary signal. It prevents a missed SSE event from becoming an infinite spinner.

### P0 — Split stream liveness from process output

Track both:

```ts
lastStreamAt
lastOutputAt
```

Update `lastStreamAt` on `snapshot`, `heartbeat`, `phase`, `progress`, `ready`, `log`, and `error`.

Update `lastOutputAt` only on actual output (`log`, and maybe `snapshot.recentLogs` when new).

Render:

- no stream: `waiting for agent stream`
- stream alive, no output: `agent stream alive · waiting for first Flutter output`
- stream alive, output seen: `last output Ns ago`
- no stream for >15s: `agent stream quiet · reconnecting`

### P0 — Render snapshot `recentLogs`

When a `snapshot` arrives with `snapshot.recentLogs`, merge those lines into the rolling tail.

This makes reconnects and late subscribers recover without depending on event-history replay.

### P1 — Record the immediate Flutter command line into the snapshot tail

In `FlutterDevServer.startProcessWithStdin`, route the immediate command log through the same recent-log path used by stdout/stderr, or add a manager method that emits and records atomically.

Acceptance test:

- Start Flutter web.
- Subscribe after the immediate command event.
- Verify the next snapshot contains the command line in `recentLogs`.

### P1 — Align mobile retry budget with Flutter's agent deadline

Flutter browser lane should not fail at 75 seconds while the agent waits 180 seconds.

Recommended:

- Keep WebView reload retries at 2.5s.
- Do not show terminal failure until either:
  - agent emits `error`, or
  - 190 seconds elapse without ready while the process is no longer alive.

### P1 — Implement bounded carriage-return flushing

Update `devLogWriter.Write` to treat `\r` as a progress boundary, but throttle to avoid flooding:

- flush at most every 250ms for `\r` updates
- always flush newline-terminated lines
- keep tail history bounded

Acceptance test:

- Write `Compiling...\rStill compiling...\rDone\n`.
- Verify at least one intermediate progress line and the final line are emitted.

## Product Copy Recommendation

Replace:

```text
First web compile can take up to a minute — retrying automatically.
0:43 elapsed · waiting for the first output from the box
```

with a stateful version:

```text
Starting Flutter web on the box
$ flutter run -d web-server --web-port 9100 --web-hostname 0.0.0.0
0:43 elapsed · agent stream live · waiting for first Flutter output
```

If snapshots are arriving:

```text
0:43 elapsed · agent stream live · Flutter process alive · no Flutter output yet
```

If output exists:

```text
0:43 elapsed · last Flutter output 3s ago
Resolving dependencies...
```

If stream dies:

```text
0:43 elapsed · lost the agent log stream 18s ago · reconnecting
```

## Tests To Add

- `mobile/src/lib/devEventsParser.test.mts`: split-frame SSE parsing.
- `mobile/app/(tabs)/apps` or extracted hook test: `snapshot.recentLogs` populates preview log tail.
- `mobile/src/lib/laneProgress.test.ts`: add cases for `lastStreamAt` vs `lastOutputAt`.
- `mobile/src/lib/devServerState.test.mts`: failed status with `error` remains renderable.
- Apps preview component/hook test: `flutter did not become ready within 180s` status opens the failure panel.
- `desktop/agent/devserver_sse_repro_test.go`: late subscriber receives command line via snapshot recent logs.
- `desktop/agent/devserver_progress_test.go`: Flutter phase parser handles pub get, launching, compiling, served.
- `desktop/agent/dev_log_writer_test.go`: carriage-return output is flushed boundedly.

## Definition Of Done

The fix is done only when a TestFlight user opening `e-mobile` through Apps → Browser Reload sees one of these within 5 seconds:

- exact Flutter command and workDir,
- a snapshot-derived "agent stream live / process alive" line,
- a real Flutter stdout/stderr line,
- or a concrete connection failure.

The UI must never sit for multiple minutes on "waiting for the first output from the box" without also saying whether the agent stream and Flutter process are alive.

Hard upper bound: at 190 seconds, if the Flutter browser preview is not rendered, the user must be looking at a failure panel with logs or a precise "agent stream lost" diagnosis. At 4:22, the old spinner must be impossible.
