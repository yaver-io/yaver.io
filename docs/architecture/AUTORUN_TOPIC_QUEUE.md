# Autorun topic queue — feed a running loop by intent, from any surface

> **Status:** design (2026-07-19). **No feature code changed by this doc.**
> Grounded in a read-only audit of `main` @ 560e76317; every claim carries a
> `file:line`. Per CLAUDE.md: the code is the source of truth — re-grep first.
>
> Supersedes the aspirational spec in `~/autorun-queue.md` on the mini (which
> keys the queue on `{machine}` alone — insufficient, see §2).

## The ask

> "I will simply say *add this into existing autorun* — I may or may not declare
> remote device id / tmux session / runner. MCP should find the perfect one, or
> generate one if there is none. Never block things from running. All UI —
> mobile, web, car, TV, watch, AR/VR — should do it, with STT/TTS and text."

Two requirements do the most design work:

1. **Resolve-or-create, never block.** Under-specified input is the *normal*
   case, not an error case. No ambiguity errors, no "which one did you mean?"
   dead ends.
2. **Autorun identity must align with tmux sessions** (user's own framing) —
   because on one machine there are already several concurrent loops.

## Verdict up front

| Thing | State |
|---|---|
| Autorun topic queue | ❌ **Does not exist.** No `autorun_queue*.go`; no queue in any of the 25+ `autorun_*.go` |
| Feeding a live autorun new work | ❌ **Impossible today** — task md read ONCE (`autorun_cmd.go:177`) |
| `runner_queue_add` MCP verb | ⚠️ **EXISTS BUT INERT** — writes to disk, nothing ever drains it |
| Remote routing for a new autorun verb | ✅ **Free** — ops verbs remote generically (`ops.go:194-320`) |
| Mid-run input channel into a live loop | ✅ **One exists** — progress file re-read every iteration (`autorun_cmd.go:312`) |
| Precedent for mutating a live loop | ✅ `autorun_pause_all` → `autorunGate` (`autorun_gate.go:69,232`) |

---

## 1. Two bugs to fix before building on top

### 1a. `runner_queue_add` silently swallows prompts — **P1**

The RunnerKeeper queue is real: `QueueEntry{ID, SessionName, Prompt, Source, AddedAt}`
(`runner_keeper.go:61`), persisted at `~/.yaver/runner/queue.json` (`:335`,
mode 0600), keyed by **tmux sessionName**. `runner_queue_add` writes to it
(`runner_keeper_mcp.go:119`).

The drain step `RunnerKeeper.Tick(sessionName)` (`runner_keeper.go:247`) does the
right thing — hash the pane, require `KeeperModeAuto`, require the 90s
`idleDebounce`, pop the first entry for that session, `sendKeys` into tmux
(`:287-312`).

**`Tick` has no production caller.** Every call site is a test
(`runner_keeper_test.go:100,107,139,142,158,163,167`). There is no ticker
goroutine; `pollInterval: 15 * time.Second` is set in `NewRunnerKeeper`
(`:121`) and never read. `ensureRunnerKeeper` (`runner_keeper_mcp.go:358`)
constructs lazily on first MCP call and starts nothing.

**Anyone who queued a prompt today believes it is pending. It is not.** Either
start the loop or make the verb fail loudly — a queue that accepts writes and
never delivers is worse than no queue.

### 1b. A comment claims remote support that does not exist

`runner_keeper_mcp.go:12` — *"enqueue a prompt (from any device, remote OK)"*.
`runnerQueueAddArgs` (`:44-48`) has `SessionName`, `Prompt`, `Source` and **no
`machine` field**; `runRunnerQueueAdd` (`:119`) always hits the local keeper.
Sibling args do carry `Machine`, but both are self-documented as dead:
*"Reserved for future remote-attach routing (empty = local)"* (`mcp_tools.go:3606`)
and *"Reserved for remote status"* (`:3675`).

---

## 2. Autorun identity — why `{machine}` is not enough

The mini right now, observed 2026-07-19:

- tmux `1` → **codex gpt-5.5**, idle ~25m at an empty prompt, cwd
  `~/Workspace/yaver.io` (the shared, dirty checkout)
- tmux `claude-remote` → **claude**, actively working, cwd `~/Workspace/yaver-integrate`
- 8+ `~/Workspace/yaver-*autorun*` clones on disk

`autorun_enqueue {machine: "mac-mini"}` cannot name a target here. Identity is
the tuple:

```
autorun = (deviceId, tmuxSession, workDir/clone, runner, currentTask, state)
```

Most of this is already derived — task name drives slot key, branch, worktree dir,
**tmux session name**, and progress path (`autorun.go:423-447`,
`autorun_tmux.go:98`). The pieces exist; they are simply not exposed as an
addressable registry.

**Aligning identity on the tmux session is the correct spine**, and there is hard
evidence for it: `autorun_stop_all` once reported zero sessions while two
autoruns were demonstrably running, because both had been started as raw tmux by
a sibling session and the in-process manager had no record of them
(`autorun_cmd.go:605-610`). The fix already adopted there was **tmux discovery
alongside the manager**. Any registry must do the same, or it will be blind to
exactly the runs a human started by hand.

---

## 3. Resolution ladder — resolve-or-create, never block

`autorun_topic_add {topic, [machine], [session], [runner], [project]}`.
Every hint optional. Order:

1. **Explicit** — any of `session` / `machine` / `runner` given → filter to it.
2. **Single live candidate** → use it.
3. **Score the candidates** and take the best. Signals available today:
   - project/workDir match against the topic's repo (strongest)
   - runner match, if named
   - `primaryDeviceId` — already the heaviest weight in the sibling scorer,
     `project_manifest.go:1016-1019` (+40)
   - **prefer ACTIVE over IDLE**; prefer the loop whose current task is
     topically nearest
   - de-prefer a run in a shared/dirty checkout (that is the iter-0 killer)
4. **No candidate → CREATE one.** `autorun_start` already exists
   (`autorun_ops.go:349`) and already remotes (`autorun_cmd.go:96-116`).
   Create implies: dedicated clone (never the shared checkout), own branch,
   own tmux session, task md written from the topic.
5. **Never return "ambiguous".** Pick, then *report what was picked and why* —
   the `projectRuntimeReason` pattern (`project_manifest.go:1055-1070`) is the
   precedent for a human-readable justification string.

> Design tension worth stating honestly: "never block" and "never feed the wrong
> runner" are in tension. Resolve it with **reversibility, not refusal** — every
> enqueue returns the resolved target + reason, and `autorun_topic_cancel`
> removes an item that has not yet been spliced. Auto-pick is safe when it is
> visible and undoable; it is dangerous when silent.

## 4. Where a topic actually enters a live loop

The blocker: `autorunLoop` reads the task **once** before iterating
(`autorun_cmd.go:177`), then re-uses that string every iteration
(`:379,381,394`). Appending to the task md mid-run is a no-op.

Two viable splice points, both already proven:

**(a) Iteration-boundary state — the `autorunGate` precedent.** `autorun_pause_all`
mutates a process-global in-memory struct (`autorun_gate.go:69`) that each loop
consults at its boundary via `g.await(...)` (`:232`). A per-session topic queue
mirroring that shape drains at the same boundary, near `autorun_cmd.go:379`.
This is the clean path.

**(b) The progress file — the channel that already flows.** Unlike the task,
`progressBytes` is re-read **every iteration** (`autorun_cmd.go:312`) and injected
into the prompt (`:379`). Path
`<workDir>/docs/handoff/<taskbase>-progress.md` (`autorun.go:989-996`).
Appending a topic there reaches a live loop **today, with zero code**.

**Recommendation: ship (b) as the interim, build (a) as the real thing.** (b) is
a genuine escape hatch you can use this week; it is unstructured (no per-topic
tracking, no cancel, no depth) which is exactly why it is not the destination.

Prompt assembly for reference: preamble + TASK MARKDOWN + git log + progress
handoff (`autorun.go:1100,1128,1136`).

## 5. Transport — mostly free

**Ops verbs remote generically.** `ops.go:40` puts `Machine` on the envelope;
`ops.go:194-320` forwards *any* verb to the named peer before local registry
lookup and returns the peer's result verbatim (`:307-317`). `autorun_start`
already uses this (`autorun_cmd.go:96-116`).

**So a new `autorun_topic_*` ops verb inherits remoting for free.** This is the
decisive argument for building it as an **ops verb, not an MCP tool** — the
RunnerKeeper tools are MCP-registry tools and that is precisely why they never
got remote support (§1b).

Existing autorun ops verbs (`autorun_ops.go:349-355`): `autorun_start`,
`autorun_status`, `autorun_stop`, `autorun_stop_all`, `autorun_pause_all`,
`autorun_resume_all`, `autorun_runs`. No autorun HTTP routes exist — everything
flows through `/ops` (`httpserver.go:541`).

Proposed additions:

| Verb | Purpose |
|---|---|
| `autorun_topic_add` | resolve-or-create, enqueue, return target + reason |
| `autorun_topic_list` | queued / active / done per run |
| `autorun_topic_cancel` | remove a not-yet-spliced item |
| `autorun_resolve` | dry-run the ladder — "where would this go?" |

`autorun_resolve` matters more than it looks: it is how a voice surface confirms
*"adding to the yaver-integrate loop on mac-mini"* before committing, and how the
auto-pick stays visible rather than silent.

Surface queue depth in `autorunSessionView` (`autorun_ops.go:45-96`) so
`autorun_status` shows it.

## 6. Surfaces — what parity actually costs

Per CLAUDE.md, two families propagate differently.

**Shared RN code — one implementation covers four surfaces:** mobile, tablet,
**car** (`app/car-voice-coding.tsx`), and **glass/AR-VR** (`app/glass-*.tsx`) all
consume the same `DeviceContext`/`AuthContext`. Voice is already surface-agnostic:
`mobile/src/lib/voice/conversationCore.ts` (streaming STT → `endpointer.ts` →
`completenessJudge.ts` → dispatch → TTS → barge-in), on-device-first via
whisper.rn / expo-speech / llama.rn.

**Native ports, each explicit:** web (`web/lib/`, `web/app/dashboard/`), tvOS
(`tvos/YaverTV/`), watchOS (`watch/YaverWatch/`), Wear OS (`wear/`).

Known constraints that shape the voice story:

- **Voice core is wired on only 2 of 7 surfaces today** — car and glass. Phone,
  web, tvOS, watch, Wear each lack or re-implement it.
- **tvOS has no streaming mic** — Siri-remote press-to-dictate into a TextField
  only; TTS works. So tvOS enqueue is *phone-as-mic + TV-as-render*, not
  on-device STT.
- **watch/Wear are voice terminals by explicit product stance** — enqueue there
  should be voice-in + spoken confirmation, never a queue-management UI.
- `voice_listen_start` / `voice_speak` MCP verbs exist (`httpserver.go:13709,13713`)
  for driving STT/TTS on a *named remote surface*.

**"Add a topic by voice" is a near-perfect fit for the existing voice core** —
it is a single short utterance with no code output, which is exactly the shape
`runner_turn --surface=voice` already handles.

## 7. Build order

Each slice is independently useful; nothing later is required for earlier to ship.

1. **Fix the inert keeper queue (§1a)** — start `Tick`, or make the verb fail
   loudly. Do not build a second queue next to a broken one.
2. **Autorun registry keyed on tmux session (§2)** — discovery must include raw
   tmux, per `autorun_cmd.go:605-610`. Expose via `autorun_runs`.
3. **`autorun_resolve` (§3)** — the ladder, dry-run only. Cheap, and it makes
   every later step debuggable.
4. **Topic queue + drain at the iteration boundary (§4a)**, mirroring
   `autorunGate`. Meanwhile use the progress-file hatch (§4b).
5. **`autorun_topic_add/list/cancel` as ops verbs (§5)** — remotes for free.
6. **Surfaces (§6)** — RN first (covers mobile/tablet/car/glass at once), then
   web, then tvOS/watch/Wear as explicit native ports.

## 8. Open questions

1. **Splice semantics.** Does a topic become a *new task* for the current loop,
   or an *appended section* to the running task? Affects DONE-marker accounting
   and whether per-topic progress is trackable.
2. **Does an idle-but-alive loop count as a candidate?** tmux `1` on the mini is
   idle at an empty prompt after finishing. Feeding it is arguably ideal (free
   capacity) or arguably wrong (it converged; its task is done).
3. **Create-path defaults.** Which runner, which clone, which branch, when the
   ladder falls through to create? A wrong default here is how you end up with a
   9th autorun clone nobody asked for.
4. **Does the shared checkout ever get chosen?** Recommend: never — it is the
   documented iter-0 killer.

---

## One line

`autorun_topic_add {topic}` — resolve by (device, tmux session, clone, runner)
or create; splice at the iteration boundary; ops-verb transport so remote is
free; RN once for mobile/tablet/car/glass and explicit native ports elsewhere;
**always report which loop was chosen and why.**
