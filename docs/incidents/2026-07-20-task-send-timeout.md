# 2026-07-20 — "Task failed: timed out after 30s" on a healthy box

Phone (5G, relay) → Mac mini. Bar read **Connected · Relay · 217ms**, task
"Hello hello!" sat on `Sending…`, relay flipped to `no response`, then:

> Timed out after 30s — the machine accepted the connection but never answered.
> It's usually busy (a heavy build will do it) or the relay path went stale.

The machine was **not** busy and the relay was **not** stale. Both guesses in
that error string were wrong, which is most of why this took a session to find.

## What was actually true

Probed live on the mini during the incident:

| Probe | Result |
|---|---|
| `uptime` | load 1.80 — idle |
| `GET /agent/runners` | **0.57s**, three runners `installed:true ready:true authConfigured:true` |
| newest task in `/tasks` | **Jul 19** — "Hello hello!" never arrived |
| agent log 14:55–15:11 | **only `/health`** — zero phone traffic reached the agent |
| `GET /tasks` payload | **2,236,258 B**, of which **1.8 MB is one task's `resultText`** |
| poll rate of `/tasks` | every **1–3s** (`tasks.tsx:2300`) |

≈**66 MB/min of `/tasks` over a 5G relay.** Relay latency in the UI degraded
200ms → 663ms → **1267ms** across retries as it saturated. The task-create POST
never got bandwidth, so it timed out — and the phone reported that as the
machine being unreachable.

## Root cause

**This is a recurrence of an already-fixed bug.** `httpserver.go:3947-3956`
carries a comment describing this exact symptom from a prior incident (~4000
tasks → ~8 MB → relay 502 → same misleading error). That fix bounded
**row count** (`limit=50`), nilled `Turns`, and capped `Output`.

It did not cap **`ResultText`**. One row with a 1.8 MB answer rebuilt the entire
unservable response on a box with only **50 tasks** — inside every existing
limit.

> Bounding rows does not bound bytes. Only bytes bound bytes.

### The false green

`tasks_list_bounds_test.go` existed, covered this endpoint, and **passed
throughout**. Its fixtures built tasks with only `ID`/`Title`/`CreatedAt`, so
4000 rows serialised to a few hundred KB. It asserted *row count* and *absence
of `turns`* — never *response bytes*. It measured the thing that never broke.

## Fixed in this commit

- `listTasks` now bounds `ResultText` and `Output` per row, and the per-row
  budget is **derived from row count** (`listTasksTextBudget`) so the 1 MiB
  total holds at any `?limit=`. A fixed per-field cap is not enough: 500 × 6 KB
  is still 3 MB — caught by the new test, not by review.
- Truncation is **marked** in the payload, so a client never renders a cut-off
  answer as the complete one.
- Detail view is untouched — `getTask` → `taskMgr.GetTask` still returns
  everything.
- The guard now asserts **bytes**, with realistic 40 KB bodies, across three
  shapes including "few tasks, one huge answer" — the shape that actually broke.

Verified: `go test -run TestListTasks ./desktop/agent` — green, and the new byte
test **fails** against the first version of this fix.

## Still open — in priority order

### P0 · Mobile renders every fetch failure as fact
`quic.ts:3519-3533` `getRunners()` collapses *not connected, !res.ok, network
error, timeout, malformed body* all into `[]`. `tasks.tsx:887` turns `[]` into
**"No agents available"** — which is what the bar claimed while the mini had
three ready runners. Needs: a `runners`/`loading`/`error` tri-state so the bar
can say **"runners unknown"**, plus `fetchWithTimeout` (the helper exists at
`quic.ts:6121` and is not used here) and a retry. Same `[]`-on-failure ambiguity
in `schedules.tsx:91`, `agent.tsx:59`, `shortcuts.tsx:145`.

Asymmetry worth noting: `getAgentStatus` re-polls every 30s and never overwrites
good data with a failure sentinel; `getRunners` does neither, so the app drifts
toward exactly the `agentStatus != null && runners == []` combination that
renders the false banner. `refreshRunnerState` (`tasks.tsx:2057`) has no
`length > 0` guard and will wipe good runner data on a failed refresh.

### P1 · Client half of the payload bug
`listTasks` (`quic.ts:2228`) sends **no `?limit=`** and uses a **raw `fetch`
with no timeout at all**. The server cap saves it today; the client should not
depend on that. It also serves stale cache when disconnected
(`quic.ts:2222-2225`), making a dead transport indistinguishable from "no
tasks" — which is why the Active tab read 0.

### P1 · Failures are swallowed
`fetchTasks` is a bare `catch {}` (`tasks.tsx:2174`) — no log, no error state.
The task-failed path (`tasks.tsx:3212`) alerts but never re-fetches, so a task
that WAS created server-side stays invisible.

### P1 · Send button overflows the composer
Both `Send` and `Sending…` extend past the card's right edge (`tasks.tsx:5045`,
and the follow-up variant at `:5935`). Layout fix — the button should not grow
past its container.

### P2 · `createTask` can block past the client's 30s
`CreateTaskWithOptions` (`tasks.go:1479`) calls `startProcess` **synchronously**
before replying (`httpserver.go:4416` → reply at `:4454`). Inside it,
`waitForSessionSlot` (`tasks.go:2223-2237`) is an **infinite loop with a 5s
sleep**, no context, no deadline. It never receives `r.Context()`, so a client
abort leaves the task **running orphaned** with an id the phone never got.
Not the cause of this incident — the log proves the POST never arrived — but it
is a real second path to the same error message.

### P2 · The error string states two wrong guesses as likely causes
"usually busy (a heavy build)" and "relay path went stale" were both false here
and sent the investigation the wrong way. It should name the payload/saturation
case, ideally with the measured latency.

## Housekeeping found on the mini

Two autorun tmux sessions have been live since **Jul 19**:
`yaver-autorun-connectivity-mesh-harden-{claude,codex}`. Unrelated to this bug,
but they are holding worktrees.
