# Autorun across surfaces — audit + design

Status: **design, not built.** Audit performed 2026-07-17 against the code, not
the docs. Every claim below cites `file:line`.

Goal: every surface (web, mobile, tablet, car, tvOS, watch, Wear, glass/AR-VR)
can see *that* an autorun is running, *which* runners hold which seats, *how far
along* it is (`5/9`), whether it will deploy and to how many platforms — without
Convex ever learning what the code says.

---

## 1. What exists today

### 1.1 The engine is real and mature

| Thing | Where |
|---|---|
| `autorunSession` (in-memory record) | `desktop/agent/autorun_ops.go:15` |
| `autorunSessionView` (wire shape) | `desktop/agent/autorun_ops.go:32` |
| `autorunOptions` | `desktop/agent/autorun.go:181` |
| `autorunRunSummary` | `desktop/agent/autorun.go:86` |
| Ops verbs `autorun_start/status/stop/stop_all` | `desktop/agent/autorun_ops.go:217-220` |
| Runner registry | `desktop/agent/tasks.go:267` |

Statuses: `running`, `completed`, `stopped`, `failed`, `stopping`.
Finish reasons: `autorun.go:49-58` (`task marked DONE`, `converged`,
`reached --max-iters`, `gate failed`, `runner failed`, `scope violation`,
`stopped by operator`, `insufficient machine resources`).
Heal kinds: `autorun.go:63-67` (`runner_failover`, `disk_reclaim`, `cpu_backoff`).

### 1.2 Five findings that change the design

**F1 — There are no stages.** The unit of progress is the *iteration*, a flat
`for iteration := 1; ...` loop (`autorun_cmd.go:253`). There is no
plan→build→test→deploy model anywhere. The only structure is the two-seat
**master/doer** split (`autorunSeats`, `autorun.go:233`), where master plans and
never edits. A "stage shower" therefore has nothing to show *unless we build it*
— see §4.

**F2 — Autorun is forbidden from deploying.** `validateAutorunShellCommand`
(`autorun.go:450`) rejects any gate containing `yaver deploy`, `npm publish`,
`fastlane`, `xcodebuild -exportarchive`, `git push --force`, `git tag`. The most
a run does is `git push` when `--push` is set (`autorun_cmd.go:391`). So today
the honest answer to "will it deploy at the end?" is **never, by design**.
Showing a deploy stage would be a UI that lies.

**F3 — Zero autorun state reaches Convex.** No `callMutation`/`convex` reference
in any `autorun*.go` or `runner*.go`. `runner.go:26-30` already *pre-commits* in
a comment: *"Cross-machine sync is metadata-only (id, status, durationMs,
exitCode, errorClass)."* This is greenfield, and the intended shape is already
written down.

**F4 — Sessions are in-memory only.** `autorunSessionManager.sessions` is a map
(`autorun_ops.go:65`); a daemon restart loses every run. The durable artifacts
are the progress markdown (`docs/handoff/<task>-progress.md`, `autorun.go:463`)
and the git commits. Any Convex row therefore *outlives the agent's own memory*
— which is an argument for syncing, and a hazard (§3.4).

**F5 — The mobile status vocabulary is already written, tested, and dead.**
`mobile/src/lib/agentStatus.ts:154` defines `AutorunSession` (explicitly
"Mirrors autorunSessionView"), `:189` `agentSignalFromAutorun`, `:255`
`slotKeyForAutorun`. `mobile/src/lib/agentSlots.ts` has **zero importers**.
`agentStatus.test.ts:143` ("Autorun: the light must not lie") already locks the
contract. **Do not re-derive status — consume this.**

### 1.3 Dead and drifted code found on the way

- **`ReportRunnerUsage` has no callers.** The table (`schema.ts:1010`), the
  route `POST /usage/record` (`backend/convex/http.ts:3378`), and the Go sender
  (`auth.go:2025`) all exist. Nothing calls it. Runner usage has **never** been
  recorded. Same class as the `yaver diagnose` bug CLAUDE.md warns about.
- **`aiRunners` has drifted from the Go registry.** Convex advertises
  `aider`, `ollama`, `goose`, `amp`, `continue` (`aiRunners.ts:33-85`) — none
  supported in Go — and omits `glm`, which is first-class (`tasks.go:267`).
- **Join bug:** Convex stores `runnerId: "claude-code"` (`aiRunners.ts:9`) but Go
  normalizes `claude-code` → `claude` (`runner_auth.go:380`). Any UI joining a
  run's `runner` against `aiRunners.runnerId` **breaks on the most-used runner**.
- **`web/lib/agentStatus.ts` claims to mirror the mobile file but has no autorun
  branch.** The mirror has already drifted.

### 1.4 UI reality: nothing renders autorun, anywhere

| Surface | Autorun UI | Insertion point | Client seam |
|---|---|---|---|
| web | none | `web/app/dashboard/page.tsx:2704` tab ladder + sidebar `:1975` | `callOps()` **already works** — `web/lib/agent-client.ts:1944` |
| mobile / tablet | none (lib dead) | new `app/(tabs)/autorun.tsx`, `href:null` + link from `more.tsx` | needs an `/ops` helper on `quic.ts` (none exists) |
| tvOS | none | `Views/DashboardView.swift:45` NavigationLink | generic `ops<T>()` **already exists** — `AgentClient.swift:29` |
| car | none | `app/car-voice-coding.tsx` — voice-only, hard confirm gate | shares RN libs |
| glass / AR-VR | none | `app/glass-workspace.tsx` — pane host; autorun = 4th pane | shares RN libs |
| watch | none — **by design** | `WatchStore.swift:8`: "No task list, no history, no code." | turn-only |
| Wear | none — **by design** | `WearApp.kt:32`: "No tabs, no lists, no diffs. Ever." | turn-only |

---

## 2. What Convex may hold

### 2.1 The precedent chain

The privacy contract permits **bookkeeping counters and slugs**, forbids
**content, paths, secrets, output**. Precedents:

- `runnerUsage` (`schema.ts:1010`) — `userId, deviceId, taskId, runner, model,
  durationSec, startedAt, finishedAt, source`. Exactly our class.
- `userActivity` (`schema.ts:2081`) — `action, target, outcome, timestamp`.
- `deviceFlightEvents` (`schema.ts:812`) — `session, kind, detail, at`.
- `userProjects` — **"slug + deviceId + flags + branch — no absolute paths"**.
- `prepaidCredits` comment (`schema.ts:~2093`): *"convex_privacy_test.go bans
  secrets/output/paths, NOT balances."*

**The `userProjects` precedent settles the hardest question.** A project *slug*
is allowed; an absolute *path* is not. `autorunTaskName()` (`autorun.go:139`)
already reduces a task path to its basename (`n2n`), which is slug-class.
So the task **name** may be synced; the task **path** may not. This is what makes
a readable UI possible without breaking the contract.

### 2.2 The projection — what is dropped

`autorunSessionView` is **content-bearing** and must never be sent whole. Four of
its fields are exactly what the fence forbids:

| Field | Why it must never reach Convex |
|---|---|
| `workDir` | absolute path → leaks `/Users/<username>` |
| `progressPath` | absolute path, same |
| `task` | absolute path to the task file |
| `progressTail` | **verbatim run content** |

Also dropped: `gate` (a shell command — `command` is already fenced),
`goal` (user-written natural language), `heals[].detail` (free text),
`finalCommitSubject` (free text), `error` (free text, may embed paths).

### 2.3 Proposed tables

Runner-agnostic by construction: `runner` is a free `v.string()`, never an enum
— matching `runnerUsage`'s existing `v.string()` and its "claude, codex, aider,
etc." comment. New runners need **no schema change**.

```ts
// One row per autorun run. Metadata only — never content.
autorunRuns: defineTable({
  userId: v.id("users"),
  deviceId: v.string(),
  runId: v.string(),          // autorun-<unixnano>, autorun_ops.go:90
  slot: v.string(),           // "<taskName>:<seat>" — slug-class, autorun.go:310
  taskName: v.string(),       // basename only. NEVER the path.
  runner: v.string(),         // doer seat. free string, joins aiRunners.runnerId
  master: v.optional(v.string()),   // planning seat; absent = single-seat run
  activeRunner: v.optional(v.string()), // differs from `runner` after failover
  status: v.string(),         // running|completed|failed|stopped|stopping
  finishReason: v.optional(v.string()), // CLOSED enum only — autorun.go:49-58
  iteration: v.number(),      // the "5" in 5/9
  maxIters: v.number(),       // the "9" in 5/9. 0 = unbounded
  commits: v.number(),
  finalCommit: v.optional(v.string()),  // SHA only, never the subject
  healCount: v.number(),      // count only — detail stays local
  stageIndex: v.optional(v.number()),   // see §4
  stageCount: v.optional(v.number()),
  platforms: v.optional(v.array(v.string())), // matrix IDs, §5
  source: v.optional(v.string()),  // mobile|cli|mcp — matches runnerUsage
  startedAt: v.number(),
  finishedAt: v.optional(v.number()),
  updatedAt: v.number(),
})
  .index("by_user", ["userId", "startedAt"])
  .index("by_device", ["deviceId", "startedAt"])
  .index("by_slot", ["userId", "slot"]),   // stable across runs — §6.1
```

Deliberately **absent**: `detail`, `note`, `message`, `error`, any free-text
field. `deviceFlightEvents.detail` (`schema.ts:818`) is a free-text escape
hatch and is precisely how content leaks; this table has no such hole.

`finishReason` must be validated against the closed list at `autorun.go:49-58`
**on the agent** before sending. It is an enum in practice; letting it be an
arbitrary string reintroduces the free-text hole under a respectable name.

### 2.4 The fence must be extended

`convex_privacy_test.go:31` is a name-blocklist and has **autorun-shaped holes**.
It fences `runner_output`/`runner_log`/`runner_workdir` (`:127-134`) but not:

```
progressTail, progressPath, taskPath, gate, goal,
finalCommitSubject, healDetail, autorunOutput
```

Add these in the same change. Note the test is **not** a global interceptor — it
walks payloads from explicitly-registered cases, so the autorun sync path needs
its own test case or it is unpoliced.

**Blind spot worth fixing:** `runnerUsage` syncs over `POST /usage/record`
(`http.ts:3378`), not `convexSyncer.callMutation` — so it never passes the
fence's interceptor at all. An HTTP-route sync path for autorun would inherit
that gap. Either route autorun through `callMutation`, or extend the test to
cover the HTTP senders. The route's one saving grace is that it **explicitly
projects each field** rather than spreading `...body` (`http.ts:3392`) — copy
that pattern; never spread.

---

## 3. Policy

Precedent: `data_policy.go:3` — *"The policy is RESOLVED in Convex but must be
ENFORCED on the runtime, or the corp-privacy claim is decorative."*

```go
// autorunSyncPolicy governs what a run publishes beyond the machine.
// Enforced at the projection boundary, not at the UI.
type autorunSyncPolicy struct {
    Mode string // "off" | "counts" | "labels"
}
```

| Mode | Convex sees | UI you get |
|---|---|---|
| `off` | nothing | P2P only: surfaces poll `autorun_status` over `/ops`. Full fidelity when reachable, blank when not. |
| `counts` | run rows with `taskName` replaced by `sha256(taskName)[:12]` | "3 runs, 2 running, iteration 5/9" — honest, unreadable labels |
| `labels` (**default**) | as `counts`, plus `taskName`/`slot` in the clear | the readable UI |

`labels` is the proposed default because `taskName` is slug-class and
`userProjects` already sets that precedent — but it is a **real disclosure**: a
task named `acquire-competitor.md` leaks intent to Convex even though no code
does. That is the one judgement call in this design and it belongs to you, not
to me. `off` must remain a first-class, fully-functional mode (it is what
"P2P, no Convex" means), and the UI must be built so that `off` degrades to
"reachable → live detail; unreachable → honest blank", never to a fake.

---

## 4. Stages — the actual decision

Autorun has no stages (F1). There are two ways to give you a stage shower, and
they are very different amounts of work.

### Option A — render what already exists (recommended first step)

The "stages" a run really has today:

```
seat: master (codex) → doer (claude)     ·  iteration 5/9  ·  gate: pass
```

Most of what is needed is already in `autorunSessionView`: `iterations`,
`master`, `activeRunner`, `heals`, `finishReason`.

**One gap blocks `5/9`: the view has no denominator.** `MaxIters` lives on
`autorunOptions` (`autorun.go:186`) and on the *input* payload
`autorunStartPayload` (`autorun_ops.go:209`) — but `autorunSessionView`
(`autorun_ops.go:32`) never carries it out. `autorun_status` can render the
`5` and has no `9`. Fix is three lines: add `MaxIters int \`json:"maxIters"\``
to the view and populate it from the run's options. Do this in step 3 — it is
the only engine change Option A needs, and without it the counter is
`iteration 5` with no end in sight.

### Option B — real multi-stage pipelines

**The primitive already exists and is not autorun's.** `Task` already has:

```go
ChainID    string  // shared ID linking tasks in a chain   tasks.go:944
ChainOrder int     // 0-based position in the chain        tasks.go:945
RunnerID   string  // which runner executes this task      tasks.go:921
Source     string  // "mobile", "mcp", "cli"               tasks.go:918
```

"Chained tasks: execute in order, next starts when previous completes"
(`tasks.go:943`). **That is a stage engine.** `ChainOrder` *is* the stage index,
and because each `Task` carries its own `RunnerID`, **each stage can already pick
a different runner** — which is exactly what you asked for.

So: do not invent a parallel stage model inside autorun. Define

> **an autorun = a gate-verified loop over a task chain; stage index =
> `ChainOrder`, stage count = `len(chain)`, per-stage runner = `Task.RunnerID`.**

This also answers **"a regular task created from mobile can be autorun"** for
free: a mobile-created task (`source: "mobile"`, `quic.ts:1862`) is already a
`Task`; promoting it means giving it a `ChainID` and handing the chain to
autorun. No new task type, no mobile-specific path.

`stageIndex`/`stageCount` in §2.3 are optional precisely so Option A ships
without them and Option B fills them in later.

---

## 5. Deploy — and how many platforms

Today: **autorun cannot deploy** (F2), and the ban is deliberate and correct
(a loop that force-pushes tags at 3am is how you lose a repo). Three coherent
positions:

1. **Keep the ban.** UI shows "Deploy: no". Honest, zero work, and consistent
   with the deploy-coalescing rule in CLAUDE.md ("one deploy per converged
   change — never one per iteration"). An autorun that deploys every iteration
   is *precisely* the anti-pattern that rule exists to prevent.
2. **Deploy once, at convergence, behind explicit opt-in.** A `deploy:` block in
   the task front matter (same place seats are declared, `autorun.go:241`), fired
   only on `finishReason == "task marked DONE"`, never on `max-iters`. Lifts the
   ban only for the final commit.
3. **Deploy as the last chain stage** (Option B). Cleanest — the deploy is a
   `Task` like any other, and the existing `deploy_all` path runs it.

The platform vocabulary already exists and is already agnostic:
`mobile_platform_matrix.go:12` — `{ID, Label, Family, Surface, DeployTarget,
StoreTarget, BuildSupported, SubmitSupported}` with surfaces
`mobile | tv | car | watch | ar-vr`. Its verb description says it is *"Used by
lean-back clients to show what the selected runtime can build or submit"* —
i.e. it was built for exactly this. `platforms: v.array(v.string())` holds
matrix IDs (`android-mobile`, `tvos`, `android-xr`, …). "Deploys to 3 platforms"
= `platforms.length`, resolved to labels client-side via the matrix.

**Recommendation: (1) now, (3) if/when Option B lands.** Do not build (2) —
it invents a second deploy path that (3) would immediately obsolete.

---

## 6. UI wiring

### 6.1 Rules the code already tells us

- **Pin to `slot`, never to `id` or list position.** `autorun_ops.go:33`: *"A UI
  pins its fixed slots to this, never to ID (new every run) or to list position
  (moves whenever any sibling changes)."* `id` is new every run (`:90`).
- **Never show green on a quiet run.** `agentStatus.ts:189`: a `completed` run
  with empty `finalCommit` renders **`unknown`**, not green — "showing green
  there would be a light that lies." Locked by `agentStatus.test.ts:143`.
- **`activeRunner` ≠ `runner` after a failover.** Show `activeRunner`; surface
  the failover as a heal, or the UI silently misreports who is working.
- **Stale = unknown.** `AUTORUN_STALE_MS = 45min` (`agentStatus.ts:175`) —
  autorun kicks a runner for up to 30min/turn, so shorter silence is normal.

### 6.2 The one vocabulary

`agentStatus.ts:2` — *"The one status vocabulary. Every surface reads agent state
from here."* Written because the colour was defined three times and disagreed.
Per CLAUDE.md's parity rule, this splits two ways:

- **RN family (mobile, tablet, car, glass)** — import `agentStatus.ts` +
  `agentSlots.ts`. Free once wired. They are dead today; wiring them *is* the
  mobile work.
- **Native (web, tvOS, watch, Wear)** — must port explicitly. Web's
  `web/lib/agentStatus.ts` already claims to mirror and doesn't; its
  `agentStatus.test.ts:39` is "THE CONTRACT TABLE. Mirrored in mobile/…" and is
  the enforcement point.

### 6.3 The label / icon / stage shower

One shared component contract, per-surface rendering:

```
[◐ autorun]  n2n:codex   5/9   codex→claude   ⚡healed 1
 │           │           │     │              └ healCount
 │           │           │     └ master→doer seats
 │           │           └ iteration/maxIters ("∞" when maxIters == 0)
 │           └ slot (stable address)
 └ AgentState → icon+colour, from agentSignalFromAutorun
```

`AgentState` (`agentStatus.ts:36`) drives the icon: `idle | working | healing |
blocked | verified | failed | unknown`, plus `pulse`/`hollow`/`label`. The
autorun *tag* is the badge; `state` is the colour; **`hollow` is what carries
"we don't actually know"** — do not flatten it away on the small surfaces.

`maxIters == 0` means unbounded (`autorun_ops.go:232` `minimum: 0`) — render
`5/∞`, never `5/0`.

### 6.4 Per-surface plan

| Surface | Ship | Notes |
|---|---|---|
| **web** | `components/dashboard/AutorunView.tsx` + tab (`page.tsx:2704`, sidebar `:1975` near `ops`) | `callOps("autorun_status")` already works (`agent-client.ts:1944`). Port `agentSignalFromAutorun` into `web/lib/agentStatus.ts` and extend the contract table. |
| **mobile / tablet** | `app/(tabs)/autorun.tsx`, `href:null`, linked from `more.tsx` | Needs an `/ops` helper on `quic.ts` next to `getRunners()` `:3395`. Consume the dead libs. |
| **tvOS** | `Views/AutorunView.swift` + NavigationLink (`DashboardView.swift:45`) | `AgentClient.ops<T>()` `:29` already generic. Port the vocabulary to Swift. Lean-back: slot + `5/9` + state, no tail. |
| **glass / AR-VR** | 4th pane in `glass-workspace.tsx` | `agentSlots.ts:5` names "the VR arc picks its panes" as a target it was written for. |
| **car** | spoken one-liner, no panel | `car-voice-coding.tsx:6` — voice-only, hard confirm gate. "n2n, iteration five of nine, healed once." |
| **watch** | complication / tile: **one aggregate signal** | `WatchStore.swift:8` forbids lists. Worst-of across runs. |
| **Wear** | same, + reuse `ConfirmScreen` for stop | `WearApp.kt:32` forbids lists "Ever." |

Watch and Wear are the parity test: the rule is that the *fix* reaches every
surface, not that every surface grows the same panel. A list on the watch would
violate an explicit, load-bearing constraint. One honest light is the port.

---

## 7. Work order

Each step is independently shippable and none blocks on a decision from a later one.

1. **Fence first.** Add the autorun names to
   `convex_privacy_test.go:31` + a registered payload case. Land *before* any
   sync path, so the sync path is born policed.
2. **Fix the drift** (standalone bugs, worth landing regardless):
   `aiRunners.ts` ← `supportedRunnerIDs`; `claude-code`→`claude`; either wire
   `ReportRunnerUsage` or delete it.
3. **Option A UI, `off` mode, P2P only.** Add `MaxIters` to
   `autorunSessionView` (§4 — the only engine change, and `5/9` needs it). Wire
   the dead mobile libs; add the web view. No Convex at all. This alone delivers
   the tag, the label, the icon, `5/9`, and the seats.
4. **Then** decide the policy default (§3) and add `autorunRuns` + the
   projection + the policy gate.
5. **Then** tvOS, glass, car, watch/Wear.
6. **Option B / deploy stages** only if the chain unification (§4) is what you
   want.

Steps 1–3 answer everything you asked for except cross-machine visibility when
the box is unreachable — which is the *only* thing Convex actually buys here.
That is worth naming plainly: **the Convex table is not needed for the UI. It is
needed for the UI to work when the machine is asleep.**
