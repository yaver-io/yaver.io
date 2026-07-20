# 2026-07-20 — mobile voice send says connected, then times out

Status: audit + partial mobile hardening in this worktree. No commit or deploy
was made.

User-visible path:

1. Open Yaver mobile on 5G.
2. Tasks automatically selects the primary Mac mini.
3. Header says `Connected`, `Relay`, and sometimes shows low ping.
4. Tap mic, speak "Hello hello", STT fills the composer.
5. Tap `Send`.
6. Button stays on `Sending...`; ping eventually flips to `no response`.
7. Alert says task failed after 30s.

The important point: automatic primary-device connection is not the problem.
The problem is that the UI treats "some relay/health presence exists" as enough
to imply "task creation and runner state are usable now".

## Evidence gathered in this session

Video/screenshot evidence:

- First screen: Tasks selected the primary Mac mini and showed `Connected`.
- Transport line showed `Relay`; the banner sometimes displayed a successful
  ping around 200 ms.
- Compose sheet showed only the target host chip and runner chip; it did not
  prove the selected runner was freshly fetched, OAuth-authenticated, and able
  to accept work.
- After STT, `Send` entered `Sending...`.
- While send was still pending, the ping badge later changed to `no response`.
- Final error was the generic 30s timeout.

Live Mac mini probes from this session:

- SSH reached the Mac mini immediately, so the machine itself was reachable.
- Running agent version reported `yaver 1.99.327`.
- Local HTTP listener was on `127.0.0.1:18080`.
- `/health` returned 200.
- `/agent/status` returned 200 in about 0.7s.
- `/agent/runners` returned 200 immediately with OpenCode, Claude Code, and
  Codex installed/ready/auth-configured.
- `/tasks?limit=3` returned about 4.5 KB in about 0.03s.
- Full `/tasks` now returned about 172 KB in about 0.03s.
- A later probe briefly saw connection refused on `18080`, then SSH showed a
  fresh `yaver serve --debug --work-dir=...` process and `/health` returned 200
  again. Treat this as additional evidence that mobile must distinguish stale
  connection UI from freshly proven task-send readiness.

This means the original payload saturation described in
`docs/incidents/2026-07-20-task-send-timeout.md` is fixed on the currently
running Mac mini agent. The remaining product bug is connection semantics and
send-path hardening: mobile should not present "connected and ready to send" by
using only presence/health when the actual task-send capability is unproven.

## Existing related incident

Read `docs/incidents/2026-07-20-task-send-timeout.md` first. It already
establishes the previous root cause:

- `GET /tasks` was 2.2 MB.
- One task contributed about 1.8 MB of `resultText`.
- Mobile polled `/tasks` every 1-3 seconds.
- That consumed relay bandwidth and the `POST /tasks` request never reached
  the agent before the 30s mobile timeout.
- The Mac mini was not busy; runners were healthy; runner OAuth was not the
  cause.

The server-side payload cap landed in `bb4a02110`.

## Architecture finding: "connected" is too weak

The Tasks screen currently derives its green state from connection-manager pool
state:

- `mobile/app/(tabs)/tasks.tsx`
- `activeLiveInPool = activeDevice && connectedDeviceIds.includes(activeDevice.id)`
- `effectiveState = activeLiveInPool ? "connected" : ...`

That is an improvement over raw `connectionStatus`, but it is still only a
transport/pool truth. It does not prove the user can create a task.

The send path depends on stricter capabilities:

- the selected device's route resolves to the intended Mac mini;
- `/health` answers over the selected transport;
- `/agent/runners` answers with a fresh row for the selected runner;
- selected runner is installed;
- selected runner is OAuth/auth-configured;
- selected runner is ready;
- `POST /tasks` can reach the agent and receive the new task id quickly;
- background task-list polling is not occupying the same constrained relay path.

Today the UI can be green after only a subset of those are true. That is why
the user sees "Connected" and only discovers task-send failure after pressing
Send.

## Yaver Mesh / relay / signaling audit

Current mental model from code:

- Convex/device registry and heartbeat tell mobile that a device exists and is
  probably online.
- Mobile chooses direct, tunnel, or relay paths in `mobile/src/lib/quic.ts`.
- `connectedDeviceIds` records a live pooled client.
- Relay path exposes agent endpoints through `/d/<deviceId>/...`.
- `/health` is public/lightweight and often succeeds even when heavier or
  authenticated endpoints are degraded.
- Runner state comes from `/agent/runners`.
- Task creation is `POST /tasks`.

Problem: these signals are not layered in the UI. A single label,
`Connected`, is overloaded to mean:

- device presence is fresh;
- transport handshake succeeded;
- relay tunnel is alive;
- agent HTTP server is answering;
- authenticated agent endpoints are usable;
- runner OAuth is usable;
- task-create request can complete.

Those are different states and they fail differently. A beach/5G workflow needs
the UI to show the highest state actually proven, for example:

- `Relay connected`
- `Agent answering`
- `Checking runners`
- `Claude Code ready`
- `Task send blocked: task list poll saturated relay`
- `Task send ready`

The existing ping only proves a small health request. It is not a task-send
preflight.

## Runner OAuth / readiness finding

For this incident, runner OAuth was not the root cause. Live `/agent/runners`
on the Mac mini reported all relevant coding runners installed, ready, and
auth-configured.

But the mobile UI still has a runner-state bug:

- `mobile/src/lib/quic.ts` `getRunners()` collapses every failure into `[]`.
- `mobile/app/(tabs)/tasks.tsx` can render empty runner data as an
  authoritative "No agents available" style state.
- Fetch failure, timeout, malformed JSON, not connected, and true zero runners
  are not the same product state.

The runner row in the Tasks header must be tri-state:

- loading: checking runner state;
- loaded: show the selected runner and readiness/OAuth state;
- failed: runner state unavailable, do not claim no runners exist.

## Send-path finding

The specific send path is:

- user taps mic;
- STT fills `newTaskText`;
- user taps Send;
- `handleCreateTask()` resolves runner/model;
- `sendClient.sendTask(...)`;
- `QuicClient.sendTask()` sends `POST /tasks` with a hard 30s timeout.

Failure mode from the incident:

- background `/tasks` polling was using the same constrained relay path;
- `/health` still answered, so the UI stayed connected;
- `/agent/runners` still answered when manually probed;
- `POST /tasks` did not reach the agent during the timeout window;
- the phone showed a generic timeout.

Required product invariant:

When the user presses Send, the app should reserve the path for `POST /tasks`.
The task-list poll must not be allowed to compete with task creation over the
same relay. If the POST still times out, the error should say whether `/health`,
`/agent/runners`, and `/tasks?limit=1` were reachable immediately afterward.

## Edits already made in this worktree

These are focused mobile hardening edits from this session. They are not
committed.

- `mobile/src/lib/quic.ts`
  - Added `RunnerFetchResult`.
  - Added `getRunnersState()` returning `loaded`, `failed`, or `disconnected`.
  - Kept `getRunners()` for compatibility, but changed it to throw on unknown
    runner state instead of returning `[]`.
  - Added `getRunnersOrEmpty()` for old best-effort callers.
  - Changed `listTasks()` to call `/tasks?limit=50`.
  - Changed `listTasks()` to use the existing `fetchWithTimeout` helper with a
    10s timeout.
  - Changed disconnected/list-failure behavior to throw instead of rendering
    cached tasks as live truth.
  - Bounded `getAgentStatus()` with `fetchWithTimeout`.

- `mobile/app/(tabs)/tasks.tsx`
  - Added runner fetch UI states: `idle`, `loading`, `loaded`, `failed`,
    `disconnected`.
  - Header can now show `checking runners...` or `runners unavailable`.
  - Runner fetch failure preserves prior runner rows instead of overwriting
    truth with `[]`.
  - Task-list fetch failure is logged through `appLog`.
  - Task-list failure gets a visible `Tasks unavailable. Tap to retry.` banner.
  - `/tasks` polling pauses while `isSubmitting` is true, reserving the relay
    path for `POST /tasks`.

- `mobile/app/schedules.tsx`
  - Uses `getRunnersState()` and only updates runner rows on `loaded`.

- `mobile/app/(tabs)/agent.tsx`
  - Uses `getRunnersState()` so runner fetch failure does not poison graph and
    machine inventory refresh.

- `mobile/app/(tabs)/shortcuts.tsx`
  - Uses `getRunnersState()` and preserves previous runner truth on failure.

- `mobile/src/components/RunningTasksPill.tsx`
  - Preserves last known running-task rows on list poll failure instead of
    clearing to zero.

Not yet done in this worktree:

- Add a send preflight that probes `/agent/runners` for the selected runner and
  optionally `/tasks?limit=1` before enabling Send.
- Add a post-timeout diagnostic ladder to distinguish relay transport failure,
  agent-auth failure, runner-state failure, and list-payload saturation.
- Add automated tests for the mobile changes.
- Fix Go `CreateTaskWithOptions` synchronous start/orphan risk.

## What Claude Code should do next

1. Preserve the existing server payload cap from `bb4a02110`.
2. Finish mobile send-path hardening:
   - keep the new `/tasks` polling pause while `isSubmitting` is true;
   - verify polling resumes after the POST settles;
   - force an immediate lightweight refresh after task creation or failure;
   - keep runner tri-state behavior;
   - make Send disabled or visually "checking" until selected runner state is
     freshly known.
3. Add a task-send diagnostic helper in mobile:
   - after timeout, probe `/health`, `/agent/runners`, `/tasks?limit=1`;
   - report which one failed and the measured latency;
   - do not say "busy" unless `/agent/status` or server load actually supports
     that claim.
4. Add Go-side capability endpoint or doctor probe:
   - `task_send_ready` should check the real task creation dependencies, not
     just `/health`;
   - include runner selected, runner readiness, auth state, task-list response
     bytes, and relay path status.
5. Fix `CreateTaskWithOptions`/`waitForSessionSlot`:
   - `waitForSessionSlot` must accept a context/deadline or task-create must
     reply with the task id before blocking runner start;
   - client abort must not create an invisible/orphaned task.
6. Add tests that fail without the fixes:
   - mobile runner fetch failure does not render "No agents available";
   - `listTasks()` calls `/tasks?limit=50` with a timeout;
   - polling pauses while send is pending;
   - failed `/tasks` poll does not clear running-task UI to zero;
   - Go task creation cannot block forever before replying.

## Temporary operator guidance

Until the mobile send-path is fully hardened:

- If the app says `Connected` but Send hangs, do not assume the Mac mini or
  runner OAuth is broken.
- Check `/agent/runners` and `/tasks?limit=3` on the box; those are stronger
  signals than `/health`.
- If `/tasks` is large or slow, clear old task history or deploy the capped
  agent build before testing mobile again.
- Prefer `?limit=` on every task-list client path.
