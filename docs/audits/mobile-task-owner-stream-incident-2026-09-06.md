# Mobile remote Task stream incident — 2026-09-06

> Evidence captured on 2026-09-06. Code and executable tests are authoritative
> if this document later drifts.

## User-visible failure

The Yaver mobile app opened running Task `046c78dc`, displayed its recorded
machine as `ubuntu-4gb-hel1-1`, but showed no conversation or live console. The
screen retried five times, then offered **Reattach**. Reattach repeated the same
failure.

## Measured facts

- `yaver status` and `yaver devices` reported the Ubuntu agent online.
- `yaver ssh ubuntu-4gb-hel1-1` reached the box.
- Ubuntu's authenticated `GET /tasks` reported `046c78dc` as `running` with
  accumulated semantic output.
- Ubuntu's direct `GET /tasks/046c78dc/output?since=0&rawSince=0` returned live
  SSE presentation, transcript and raw frames (about 680 KB during the bounded
  six-second probe). The Codex ACP runner process was alive.
- Ubuntu's agent log contained no mobile SSE request during the failure.
- This Mac's agent log contained every mobile attempt. Each request was
  `GET /tasks/046c78dc/output`, and each returned `404 task not found` to the
  phone at `172.20.10.1`.

The task, runner and Ubuntu SSE producer were healthy. The mobile client sent
the request to the wrong machine.

## Root cause

Cross-device lifecycle discovery correctly created a prompt-free row carrying
Ubuntu's `deviceId`. Once the row was opened, several Task-detail paths ignored
that immutable owner and called either the focused `quicClient` or the mutable
account-level `connectionManager.runnerClient()`.

In this session those accessors resolved to the Mac. The stream implementation
also discarded the HTTP status, turning the Mac's deterministic 404 into the
generic `stream closed before the task finished`. The recovery ladder therefore
retried the same wrong machine, and the Reattach button restarted that same
ladder.

Two synchronization gaps compounded the routing defect:

1. A Convex-discovered `session-index` row has no `turnCount` by design. The
   hydration effect treated missing as zero and skipped the authoritative Task
   GET, leaving the screen dependent on the already-misrouted stream.
2. The retained raw terminal tail is capped. Its advertised cursor was the
   current retained length, which stops increasing once the cap is reached.
   A reconnect could therefore claim it was caught up while the tail had moved.

## Contract being implemented

For every existing Task:

1. Convex stores only bounded lifecycle/identity state and supplies
   `(deviceId, taskId)`. It does not store prompts, turns, output or raw bytes.
2. Opening the Task resolves `deviceId` before current focus or runner/render
   preferences and establishes that machine's P2P client first.
3. Task detail, status, questions, follow-ups, controls and lifecycle mutations
   all go to that same owner.
4. The owner supplies an authoritative snapshot, then SSE resumes semantic and
   raw lanes from monotonic byte cursors.
5. A transport failure retains the owner. Reattach may reopen the stream but
   may never re-resolve through mutable focus. HTTP failures retain their real
   status and deterministic failures are not described as a generic disconnect.

## Convex and relay boundary

This fix does not add Convex output replication, polling, mutations or write
cadence. Existing coalesced lifecycle snapshots remain the locator and offline
fallback. Conversation content remains P2P and locally cached on the phone.

The free relay is not implicated by the original failure: the phone never
addressed Ubuntu. Relay changes or deployment are conditional on a correctly
routed owner-stream probe failing specifically in the relay layer. No relay,
mobile, npm or backend deployment is authorized by this audit.

## Source and proof seams

- `mobile/src/lib/taskOwnerRouting.ts` — pure owner resolution.
- `mobile/app/(tabs)/tasks.tsx` — initial owner connect and owner-pinned Task
  operations.
- `mobile/src/lib/quic.ts` — preserve SSE HTTP status and end reason.
- `desktop/agent/tasks.go` + `desktop/agent/httpserver.go` — monotonic raw cursor
  and retained-window replay.
- Focused unit/source guards plus a real RN-web device-context arc against the
  live Ubuntu agent.

## Completion evidence

Pending implementation and verification. Update this section only with exact
commands and observed outcomes; never with inferred success.
