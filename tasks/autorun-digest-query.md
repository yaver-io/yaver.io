---
doer: codex
---

<!-- Single seat. claude is not authed on the mini; seats in front matter are
     binding, so naming an unauthed master fails the run at iteration 1. -->

# `autorun_digest` — answer "what is my box doing?" in one screen

## The problem, measured today (2026-07-17)

`ops autorun_status machine:primary` on the mini returned **56 KB / 368 lines**
and **exceeded the MCP tool output limit outright**. It could not be read. The
question being asked was the simplest one possible — *"is the remote autorun
finished?"* — and answering it took a `jq` pipeline over a persisted file.

Why: `autorunSessionView` inlines `progressTail` for EVERY session
(`autorun_ops.go:32`). One field, carrying the runner's verbatim reasoning and
diffs, is ~99% of the payload. 15 sessions × a progress tail = an answer nobody
can read, on any surface — and a phone or a watch will never render it.

**The whole run inventory is ~40 bytes per session.** It is buried in kilobytes
of prose.

## Build: `autorun_digest`

Register in `autorun_ops.go` next to `autorun_status`. **Read `autorun_status`
first (`opsAutorunStatusHandler`, `autorun_ops.go:281`) — this is a projection of
the same session list, NOT a new source of truth. It must not fork the model.**

```
ops autorun_digest { machine?, slot?, includeFinished?=true, limit?=20 }
```

Returns — and this is the ENTIRE contract:

```jsonc
{
  "machine": "<alias>",
  "totals":  { "running": 1, "completed": 3, "failed": 9, "stopped": 2, "stale": 0 },
  "runners": { "codex": 11, "opencode": 4 },      // which runners, how many
  "sessions": [
    {
      "slot":         "toolchain-and-remote-git:opencode",  // stable address
      "runner":       "opencode",
      "activeRunner": "opencode",   // differs after a failover — show THIS
      "master":       "",           // empty = single-seat run
      "status":       "running",
      "iteration":    5,
      "maxIters":     9,            // see BLOCKER below
      "commits":      2,
      "healCount":    1,
      "lastHeal":     "cpu_backoff",
      "finishReason": "",
      "finalCommit":  "",           // empty ⇒ did NOT finish, however quiet
      "startedAt":    "...",
      "ageMin":       40,
      "stale":        false
    }
  ]
}
```

**NO `progressTail`. NO `workDir`. NO `progressPath`. NO task path.** Those are
why `autorun_status` is unreadable, and they are fenced from Convex for the same
reason (`convex_privacy_test.go`). `slot` already carries the task BASENAME,
which is the readable identity — that is all a digest needs. If someone wants the
tail, that is what `autorun_status {id}` is for: **one** session, deliberately.

## BLOCKER — `maxIters` is not in the view

The digest's most-wanted number is `5/9`, and **the denominator does not exist on
the wire.** `MaxIters` lives on `autorunOptions` (`autorun.go:186`) and on the
input payload `autorunStartPayload` (`autorun_ops.go:209`), but
`autorunSessionView` (`autorun_ops.go:32`) never carries it out. `autorun_status`
can render the `5` and has no `9`.

Fix first: add `MaxIters int \`json:"maxIters"\`` to `autorunSessionView` and
populate it from the run's options. ~3 lines. Without it the digest cannot answer
the question it exists to answer.

`maxIters == 0` means UNBOUNDED (`autorun_ops.go:232` `minimum: 0`) — render
`5/∞`, never `5/0`.

## Honesty rules — non-negotiable, these are already law here

- **A `completed` run with an empty `finalCommit` did NOT finish.** Report it as
  `unknown`, never as success. `agentStatus.ts:189` calls showing green there
  "a light that lies"; `agentStatus.test.ts:143` locks it.
- **Stale ⇒ unknown.** `AUTORUN_STALE_MS = 45min`
  (`mobile/src/lib/agentStatus.ts:175`). A `running` session with no progress for
  longer is not running — it is unheard-from. Compute `stale` server-side; every
  surface must not re-derive a 45-minute constant.
- **Report `activeRunner`, not the requested `runner`.** They diverge after a
  failover heal (`autorun.go:105`), and the digest is where "who is actually
  working" must be true.
- **Never silently truncate.** With `limit`, say how many were dropped. A digest
  that hides sessions is worse than the 56 KB it replaced.

## Why the digest is the right shape (real numbers from today)

A digest of the mini's 15 sessions would have said, in one screen:

```
totals:  1 running · 3 completed · 9 failed · 2 stopped
runners: codex 11 · opencode 4
⚠ 4 × "worktree must be clean before autorun"
⚠ 5 × push rejected (fetch first)
⚠ 1 × master glm: "OAuth session expired"
```

Every one of those is a real, distinct, fixable cause. All of them were invisible
behind 56 KB of prose. Consider a `topErrors` aggregate: repeated identical
failures across slots are the highest-signal thing a box can tell you, and they
are exactly what nobody sees today.

## Fleet — one verb, one or MANY machines

The question is rarely about one box. `machine` must accept a fleet:

```
ops autorun_digest { machine: "all" }        # every device registered to the user
ops autorun_digest { machine: "primary" }    # one
ops autorun_digest { machine: "mini,test" }  # a named subset
```

`machine:"all"` enumerates the user's devices (the same registry `yaver devices`
reads) and fans out. Rules, all of which exist because this fleet already
behaves this way:

- **Fan out in PARALLEL with a per-machine timeout.** Serial fan-out over a
  sleeping box makes the verb useless exactly when you need it. Bound it (~5s
  each) and return what answered.
- **`online != reachable`** — the whole point of the 4-layer connectivity fix.
  A device row saying `online` proves nothing. Per machine report
  `reachable: true|false` + `error`, and NEVER let one dead box fail the whole
  query. A partial answer that says which parts are missing beats an error.
- **Unreachable is a RESULT, not an exception.** `{machine, reachable:false,
  reason}` is a first-class row. The most useful thing this verb can say is
  often "the mini has 3 running autoruns; the test box did not answer."
- **Aggregate across machines, but keep the per-machine breakdown.** Fleet
  totals + `byMachine[]`. A bare fleet total cannot tell you WHICH box is stuck.
- Reuse the existing cross-machine seam — `autorun_status` already accepts
  `machine` and routes via `POST /ops` (`dispatchRemoteAutorun`,
  `autorun_cmd.go:96`). **Do not invent new transport.**

Version skew is guaranteed here: an older agent has no `autorun_digest` verb.
Handle it explicitly — either fall back to `autorun_status` and project locally,
or report `{reachable:true, unsupported:true, agentVersion}`. Do not let an old
box look like an idle one; that is a light that lies at fleet scale.

## Every surface — web, mobile, tablet, car, tvOS, watch, Wear, glass/AR-VR

Parity is the law here (CLAUDE.md): a fix is not done until it exists on EVERY
surface. But parity means the *capability* reaches every surface — not that every
surface grows the same panel. Two families propagate differently:

**RN family — mobile, tablet, car, glass — share code.** One `/ops` client seam
and one status vocabulary serve all four.
**Native — web, tvOS, watch, Wear — must be ported explicitly.** They inherit
nothing.

Today **NO surface renders autorun at all.** Verified.

| Surface | Ship | Seam (verified) |
|---|---|---|
| **web** | `components/dashboard/AutorunView.tsx` + tab | `callOps()` **already works** — `web/lib/agent-client.ts:1944`. Register in the `activeTab` ladder (`app/dashboard/page.tsx:2704`) + sidebar (`:1975`, near `ops`). Port `agentSignalFromAutorun` into `web/lib/agentStatus.ts` — it claims to mirror mobile but has NO autorun branch; `web/lib/agentStatus.test.ts:39` is the contract table that keeps them honest. |
| **mobile / tablet** | `app/(tabs)/autorun.tsx`, `href:null` (`_layout.tsx:472`), linked from `more.tsx` | **`quic.ts` has NO `/ops` helper — add one** next to `getRunners()` (`:3395`). This is the only real client work. |
| **tvOS** | `Views/AutorunView.swift` + one `NavigationLink` (`DashboardView.swift:45`) | `AgentClient.ops<T>()` (`:29`) is **already generic** — a one-liner. Lean-back: slot + `5/9` + state. No tail, ever. |
| **glass / AR-VR** | 4th pane in `app/glass-workspace.tsx` | `agentSlots.ts:5` **names "the VR arc picks its panes"** as a surface it was written for. Shares RN code. |
| **car** | spoken one-liner, **no panel** | `app/car-voice-coding.tsx:6` — voice-only, hard confirm gate. "Mini: one running, iteration five of nine, healed once." The digest IS the sentence. |
| **watch** | complication / tile: **ONE aggregate signal** | `WatchStore.swift:8`: *"No task list, no history, no code."* Worst-of across runs. |
| **Wear** | same, + reuse `ConfirmScreen` to stop a run | `WearApp.kt:32`: *"No tabs, no lists, no diffs. Ever."* |

**The digest is what makes the lean-back surfaces possible at all.** `totals`
(`1 running · 3 completed · 9 failed`) IS a watch complication and IS a TV row.
`autorun_status` can never be — it is 56 KB of prose. A watch cannot parse a
progress tail; it does not want to. Build the digest and car/watch/Wear/TV become
small.

Watch and Wear are the parity TEST: shipping a list there would violate an
explicit, load-bearing constraint written in those files. One honest light is the
port. Do not "achieve parity" by ignoring them.

## Surfacing (do not invent a new vocabulary)

`docs/architecture/AUTORUN_SURFACES.md` is the design; read it. The status
vocabulary already exists and is already tested — `agentSignalFromAutorun`
(`mobile/src/lib/agentStatus.ts:189`) maps a session to
`idle|working|healing|blocked|verified|failed|unknown` + `pulse`/`hollow`/`label`.
**Consume it. Do not re-derive status colours** — that file exists because the
colour was defined three times and the definitions disagreed.

`agentSlots.ts` (`assignSlots`, `useAgentSlots`) is written, tested, and has ZERO
importers. It is built for exactly this. Wire it; don't rewrite it.

## MCP + Go agent surface

Three seams, and the verb must exist on all of them — a status query you can only
reach one way is not a status query:

1. **ops verb** — `autorun_digest`, registered in `autorun_ops.go` beside
   `autorun_status` (`:216-221`). This is the source of truth; everything else
   delegates.
2. **MCP tool** — reachable as a first-class tool, not only via the `ops`
   grand-tool, because "what are my boxes doing?" is a thing an agent asks
   constantly. Follow the existing dispatch: `httpserver.go:13099+` routes
   keeper verbs (`runner_autorun`, `runner_status`, …) and `mcp_tools.go` carries
   the schemas. Name it `autorun_digest` on both seams — one name, one meaning.
3. **CLI** — `yaver autorun status` / `--json`. `autorun_cmd.go` already owns the
   `yaver autorun` command surface. The JSON form is the digest verbatim.

The Go agent already has every piece: the session manager
(`autorunSessionManager`, `autorun_ops.go:65`), the cross-machine route
(`dispatchRemoteAutorun`, `autorun_cmd.go:96`), and the device registry the fleet
fan-out needs. **This task adds a projection and a fan-out. It does not add a
subsystem.** If you find yourself writing a new state store, stop — you have
misread the code.

## DO NOT BUILD. DO NOT RUN TESTS.

Owner's instruction: **do the coding, commit, push to main. That is all.**

No `go build`, no `go test`, no `tsc`, no gradle/xcodebuild — not once, not to
check. This box runs several autoruns concurrently and a Go build cache is what
filled its disk to 1.1 GB free before (`reclaimAutorunDisk` exists for that).

So **nothing verifies your edits.** Edit conservatively. If a change needs a
compiler to know whether it is right, write it under "Needs verification" in the
progress file rather than guess.

**NEVER** run a bare `go test ./...` in `desktop/agent` — `TestAuthLogout` hits
the real `~/.yaver` and signs the owner out.

## Done means

- `ops autorun_digest machine:primary` answers, in one readable response:
  how many autoruns, which runners, which finished, which did not, and how far
  each got (`5/9`).
- Its output is small enough for a phone, a TV, and the MCP tool limit — because
  it carries no run content at all.
- `maxIters` reaches the wire, so `5/9` is real rather than `5/?`.
- A quiet run without a final commit reads `unknown`, never green.
- `autorun_status` keeps its full detail for a single `{id}`. The digest does not
  replace it; it makes the common question answerable.
