# Autorun Collective Sync Audit

Date: 2026-07-18
Status: read-only audit. No implementation. Every claim is either a `file:line`
citation or an observation from the live mac mini, and the two are labelled
differently on purpose.

Scope: how multiple autoruns, remote machines, deploys, git landing, and
resource awareness coordinate **today** — and what has to exist before running
N topics in parallel on a remote box stops corrupting itself while a human works
on a different machine.

---

## Executive finding

There is a real deploy barrier. `ship` freezes autorun machines, drains them at
iteration boundaries, pins a SHA, gate-repairs main, detects deploy targets from
the diff, deploys **once**, then thaws on every exit path
(`ship.go:103-260`, `ship_fanout.go:88-190`). "N autoruns must not mean N
deploys" is solved for callers who go through `ship`.

Two layers are missing, and they are at opposite ends:

**Before work starts — no ownership.** A run declares `--scope`, but scope is an
*allowlist checked after the runner has already edited*
(`autorun_cmd.go:366-376`). Two runs targeting the same subsystem both spend
tokens, both compile against each other's half-finished state, and discover it
at scope failure, gate failure, push rejection, or landing conflict. Scope
answers "were these edits allowed?" — never "may I start?"

**After work ends — no truth.** The terminal states lie in both directions. A
run that did nothing reports `converged`. A run whose agent died reports nothing
at all. Measured over one night on the mini: **19 runs, 4 produced any code.**

The system is therefore partially coordinated:

- **Good:** per-run worktrees, signed commits, scope validation, per-iteration
  resource probes, runner failover, freeze/drain/pin/deploy-once, push-race
  classification with rebase-retry.
- **Missing:** source-area leases, build leases, a landing coordinator across
  processes, deploy budgets, stale-session and orphan reconciliation,
  local-human awareness, and an honest terminal-state vocabulary.

---

## Part 1 — Live mac mini autopsy (empirical, 2026-07-17 → 18)

Not inferred from code. Collected from `~/.yaver/worktrees/`, every
`docs/handoff/*-progress.md` on the box, and the loop logs.

### Mortality table

| Finish reason | Runs | Commits kept |
|---|---|---|
| `scope violation` | 6 | **0** |
| `runner failed` | 4 | **0** |
| `converged: runner stopped making changes` | 3 | 1 |
| **no finish record at all** | 5 | unknown |
| `gate failed` | 1 | 1 |
| `task marked DONE` | 2 | 3 |

**~19 runs. 4 kept any commits. 5 have no terminal record whatsoever.**

Individual pathologies worth naming:

- `webrtc-vibe-loop-parity`: 4 iterations, **15 self-heals**, 0 commits kept.
  The heal machinery ran fifteen times and produced nothing — self-healing is
  not progress, and nothing distinguishes the two.
- `glm-remove-runner`, `mail-surfaces-wiring`: scope violation *with* 2 heals —
  the run repaired itself into a wall.
- 6 of 6 scope violations were **iteration 1**. None survived first contact.

### Orphans (nothing reaps these)

Seven worktrees in `~/.yaver/worktrees/` survive their runs, several from the
previous day. Every clone carries 1–2 leftover `autorun/*` branches. `$TMPDIR`
prompt files are never deleted. 13 GB free of 228 GB with 13 clones + orphaned
worktrees.

Slot naming is **inconsistent on disk**: `forge-parity:codex` and
`mail-surfaces-wiring:codex` use a colon; `merged-remaining-codex` uses a dash.
Two naming conventions coexist in one directory, which any future reconciler has
to handle.

### The loop log is empty

`autorun-merged.log` = **811 bytes** for an entire run. `autorun-merged2.log` and
`autorun-merged3.log` = **90 bytes each**. The loop logs the runner-check and
essentially nothing else. The actual work exists only in the tmux pane
(ephemeral, dies with the server) and the handoff file. **Post-hoc diagnosis of
a finished run is impossible from logs.**

### Three stuck-modes reproduced live today

1. **Blocked TUI reports as RUNNING.** Codex opened on
   `Update available! 0.128.0 -> 0.144.5 / 1. Update now 2. Skip 3. Skip until
   next version` and sat there. The loop kicked twice into a dialog, counted two
   no-ops, and wrote `converged`. `autorun status` said the session was running
   the whole time. (Now pre-answered in `codex_onboarding.go`; the *class* —
   any blocking first screen — remains.)

2. **The worktree is reused; resetting the clone does nothing.** The runner works
   in `~/.yaver/worktrees/<task>-<seat>`, not the clone.
   `autorunPrepareWorkspace` returns the existing worktree if git still lists it
   (`autorun.go:514-517`). A relaunch inherited the previous run's tree —
   including its `final autorun commit ... (converged)` **and a completed
   handoff file**. The runner read "this run already finished" and correctly did
   nothing. Cost: one full relaunch cycle, reported as `converged`.

3. **The gate was unsatisfiable.** `gofmt -l desktop/agent` flags **283 of 1597
   files on clean main** (pre-existing struct-alignment drift). Every one of
   these four tasks shipped with `test -z "$(gofmt -l desktop/agent)"` as its
   gate. Had the scope bug been fixed alone, every iteration's commits would
   still have been discarded — the same `0 commits kept`, from the other
   direction. A gate must be verified green on the untouched tree before launch.

### A hypothesis tested and rejected

`autorunTmuxBusyMarker = "esc to interrupt"` (`autorun_tmux.go:26`) is a single
hardcoded string used for **all** runners, and it reads as Claude-specific. The
obvious theory — codex never renders it, so `sawBusy` stays false, the 45s grace
at `:219` expires and every turn is falsely reported complete — is **wrong**.
Verified by driving the live codex TUI on the mini: it renders
`• Working (30s • esc to interrupt)`. The marker matches.

The real mechanism is worse because it is not a bug in any one line:

> **A no-op is not convergence.** The loop ends a run after 2 consecutive
> iterations with no file changes (`autorun_cmd.go:412-417`). A large task whose
> first turns are legitimately spent *reading task files and orienting* — which
> is exactly what a good runner does, and what the merged task explicitly
> instructs — is killed as "converged" before it writes its first line.

The final run of the day died this way: fresh worktree, load 0.23/core, codex
demonstrably working and reading the staged prompt, 2 iterations, 0 commits,
`converged`. **Convergence is currently indistinguishable from "the task was too
big to start in two turns."**

---

## Part 2 — What exists (verified)

### Execution envelope

`autorun_cmd.go:133-239` resolves task/seats, prepares a worktree, checks runner
readiness, fetches, loops, writes a final commit, releases. The loop
(`:282-451`) parks at the freeze gate (`:297-305`), probes resources
(`:307-341`), optionally runs a master planning seat (`:343-360`), kicks the
doer (`:362`), validates scope (`:366-376`), gates (`:420-427`), commits and
optionally pushes (`:428-440`).

Strong **local** safety envelope. Not a concurrency-control system.

### Freeze / drain / lease

`autorun_gate.go:10-28` — machine-wide, deliberately not per-run so a loop that
*starts* mid-window also parks. Freeze ≠ drained: a kick already in flight can
run to the 30-minute turn timeout and still commit and push (`:23-28`).
`autorunDrain()` (`:289-322`) is the honest signal; `Drained` is true only when
no loop is mid-iteration. Dead-man lease (`:49-54`, `:116-153`) exists because
the coordinator is usually on another machine, and it **fails toward running**
— a spurious resume costs one racing push; a permanent freeze costs the whole
point of autorun (`:46-48`). Narrow one-ID exemption so ship's repair loop does
not deadlock against its own barrier (`:56-63`, `ship.go:287-288`).

**Ownership is advisory only.** `resume()` (`:157-165`) takes no owner token and
`opsAutorunResumeAllHandler` accepts an empty payload — any ops caller can thaw
a freeze it does not own. `alreadyFrozen` (`autorun_ops.go:412-421`) informs; it
does not enforce.

### Git landing

The strongest area. `autorunLandOntoMain` (`autorun.go:771-825`) serializes on
`autorunLandMu`, does fetch → `pull --rebase` → `merge --ff-only` → push, retries
4×, and **classifies push races explicitly** — `autorunPushWasRejected`
(`:829-834`) matches `[rejected]`/`fetch first`/`non-fast-forward` and retries
only those, returning immediately on auth/protected-branch. `rebase --abort` on
failure (`:802`) so a run is never stranded mid-rebase. `ops_git_land.go` mirrors
this discipline for non-autorun callers, on a **deliberately separate** mutex
(`:33-43`) so an ops caller cannot stall an autorun.

### Remote dispatch

`--machine` does not stream a loop over the wire; it POSTs `autorun_start` to
the local `/ops`, which forwards (`autorun_cmd.go:79-131`). The remote daemon
owns the session with a context detached from the request
(`autorun_ops.go:92-98`), so it outlives the CLI. Remote runs **additionally
require `--scope`** — "the loop edits a checkout you are not watching"
(`:83-85`).

The local CLI then **exits**. There is no tail, no wait, no stream. State flows
back only when someone later calls `autorun_status`.

### Capacity planning that autorun does not use

`agent_mesh.go` has a capacity-aware planner considering online state, runner
readiness, platform capability, RAM/disk, shared-machine policy and task slots.
`console_machines.go` exposes `MaxTaskSlots`. **Neither is consulted by
`autorun_start`.**

---

## Part 3 — What is missing

### 3.1 Terminal states that tell the truth *(new — highest value, lowest cost)*

The vocabulary is broken in three places, and every downstream layer inherits it:

- **`converged` conflates** "nothing left to do" with "made no edits in 2 turns"
  (`autorun_cmd.go:412-417`). Proven live on a healthy, working runner.
- **`gate` conflates** a real gate failure (`:426`) with a **commit/signing**
  failure (`:430,434`) and a **push** failure (`:439`). A dead GPG agent is
  recorded identically to failing tests.
- **`runner` covers a `git status` failure** (`:368`) that has nothing to do
  with the runner.
- **Pre-loop failures produce `FinishReason: ""`** — no reason, no commit, no
  marker (`autorun_cmd.go:136-236`, ~12 distinct exits). Indistinguishable in
  git from a run that never started. This is the 5 "no finish record" rows.

Needed: distinct reasons (`no_edits_yet` vs `converged`, `sign_failed`,
`push_failed`, `precondition_failed`), and a first-class **`progress`** signal
that is not "did files change" — tokens spent, files read, tools called.

### 3.2 Runner TUI dialects — autorun reads one runner's screen for all of them

Autorun drives a **TUI**, so the pane is its only sensor. Every judgement it
makes about a runner — is it working, is it done, is it stuck, is it asking for
something — comes from string-matching that pane. There is exactly **one**
vocabulary in the codebase, and it is Claude's.

**Busy.** `autorunTmuxBusyMarker = "esc to interrupt"` (`autorun_tmux.go:26`),
one hardcoded string for claude, codex, opencode and glm. Codex passes only by
luck — verified live, it renders `• Working (30s • esc to interrupt)` and
happens to contain the substring. Nothing guarantees that, and nothing tests it.

**Idle / composer ready.** `strings.Contains(pane, ">")`
(`autorun_tmux.go:280`). A single `>` character — which appears in diffs, git
output, shell redirection, quoted mail, and markdown blockquotes. This reports
"composer ready" on almost any pane content.

**Blocked on login.** `runnerLoginScreenMarkers` (`runner_pty.go:216-225`) is
the right *shape* — a phrase list, matched case-insensitively against the bottom
8 lines so scrollback cannot false-trigger, with a documented rationale for
excluding generic phrases. But every entry is Claude-specific: "select login
method", "claude account with subscription", "anthropic console account",
"choose the text style that looks best". **A codex or opencode session parked on
its own login screen is invisible to this check.**

**Blocked on anything else.** Not modelled at all. Today's live failure was
codex sitting on `Update available! … 1. Update now 2. Skip 3. Skip until next
version` — reported as a healthy RUNNING session while the loop kicked into a
dialog twice and then wrote `converged`. Nothing in the repo knows what any
runner's update prompt, trust prompt, model-picker, rate-limit notice, or
approval dialog looks like. A grep for Claude's own working vocabulary —
Pondering, Cogitating, Deciphering, Wrangling, Billowing, Crunching, Boogieing,
Pouncing, Gusting, Noodling, Simmering — returns **zero hits** across
`desktop/agent/`.

**Do not enumerate the gerunds.** That vocabulary is decorative, randomised per
turn, and expanded by upstream whenever they feel like it — the sample above was
collected by watching real sessions, and it is certainly incomplete. Any list we
hardcode is stale at the next release, which is precisely how a screen-scraping
integration rots. Match on the parts that are **contractual** rather than
cosmetic:

- the interrupt affordance (`esc to interrupt`) — it must be there, because it
  is the user's only way to stop a turn;
- the **elapsed-time pattern** (`(4s`, `(30s`, `(18m 52s`) — it must be there,
  because it is how a human judges whether to keep waiting.

Both survive a vocabulary change; neither depends on knowing that this
particular turn chose to call itself *Boogieing*. Treat the gerund as an opaque
token, and if a dialect ever needs it, capture the observed word as **data for
the status view** — the honest, useful thing to show a user on a phone is
"Gusting, 18m 52s", not a spinner we invented.

**Progress is displayed and thrown away.** Every one of these TUIs already
renders elapsed work time — `Cogitating… (18m 52s)`, `Crunched for 32m 58s`,
`• Working (30s • …)`. Autorun captures the pane containing that number on every
poll and extracts nothing from it. This is the missing progress signal from
§3.1, already on screen: a run that has been *visibly working for 18 minutes* is
provably not converged, no matter how many files it has yet to change.

**What is needed** is a per-runner dialect table — one entry per runner, none of
them privileged. All three are installed on the mini today: claude
(`2.1.205`), codex (`codex-cli 0.128.0`), opencode (`1.17.15`), plus glm, which
drives the **claude binary** against z.ai (`tasks.go:222`) and therefore inherits
claude's dialect, not its own.

| Concept | Used for |
|---|---|
| busy markers (list, not string) | is the turn live |
| elapsed-time pattern | **real progress signal**; feeds status, distinguishes working from stalled |
| composer/idle markers | turn finished, safe to type |
| login-blocked markers | park + surface auth, per runner |
| prompt/dialog markers (update, trust, model picker, approval) | a blocking screen is not a running session |
| rate-limit / quota markers | park rather than burn the interval |
| the pre-answer for each known blocking screen | what `codex_onboarding.go` and `claude_onboarding.go` do today, ad hoc |

Verification status of what we actually know today — this table is the honest
state, not a design:

| Runner | Busy marker | Elapsed shown | Login screen | Blocking dialogs |
|---|---|---|---|---|
| **codex** | `• Working (30s • esc to interrupt)` — **verified live on the mini** | yes, `(30s` | **not modelled** | update prompt **verified live**, now pre-answered |
| **claude** | `<gerund>… (esc to interrupt)` — matches the hardcoded string | yes, `(18m 52s)` | the only one modelled (`runner_pty.go:216-225`) | folder-trust, pre-answered (`claude_onboarding.go:148`) |
| **opencode** | **unverified — nobody has looked** | unknown | **not modelled** | unknown |
| **glm** | inherits claude's binary and screens | as claude | as claude | as claude |

The opencode row is the point: it is a first-class supported runner
(`supportedRunnerIDs`, `tasks.go:267`) that autorun drives through a pane whose
vocabulary **no one has ever checked**. It is also the runner most likely to
diverge, since it is the one that is not an Anthropic-shaped TUI. Before any
dialect work ships, someone has to sit and watch an opencode turn the way I
watched codex's — that observation is a prerequisite, not a detail.

**Unknown verbs must pass through, not be dropped.** When the pane shows a token
the dialect does not recognise, autorun must forward it verbatim — to the
session view, to `autorun_status`, and down to the runner/keeper layer — rather
than discarding it or guessing. Three consequences, all of which the current code
gets wrong by having nowhere to put the value:

- An unrecognised gerund is still *evidence of life*. "I don't know what
  *Gusting* means, but the pane changed 4 seconds ago and claims 18m 52s
  elapsed" is enough to refuse the `converged` verdict.
- An unrecognised **prompt** is evidence of a block. A pane that is neither busy
  nor composer-ready for N consecutive polls is stuck on *something*, and the
  honest report is "blocked on an unrecognised screen, here are its last 8
  lines" — which is exactly what a human needs to fix it, and exactly what today
  produced silence for the codex update dialog.
- Forwarding the raw token is how the dialect table gets **maintained**: the
  words upstream invents show up in status output and in the recap, so the next
  unknown screen is discovered by reading a report instead of by losing a night.

…and it has to be **passed down to the runner layer too**: `runner_pty.go`,
`runner_keeper.go` and the autorun loop each string-match panes independently
today, with three different vocabularies and no shared source of truth. The
dialect belongs beside `builtinRunners` (`tasks.go:146`), where a runner is
already defined by its command and args — a runner's *screen* is as much part of
its contract as its CLI flags.

Until that exists, every new runner and every upstream TUI redesign silently
degrades autorun into the failure mode measured in Part 1: a loop that cannot
tell working from blocked from finished, reporting `converged` for all three.

### 3.3 Source-area leases

`scopes` is an allowlist validated after the fact. Needed before a run starts, or
before an iteration compiles: a claimed ownership set, from explicit task front
matter (`owns: [...]`), inferred from `scopes` when absent, refined by observed
changes after iteration 1.

Conservative policy: same-file overlap → block; same package with build coupling
→ queue unless marked compatible; same deploy target → parallel only if file
ownership is disjoint and deploy defers to `ship`; docs-only → allow.

### 3.3 Build leases

An autorun must not compile while a sibling is mid-edit in the same area.
Separate from source ownership: `build:web`, `build:mobile`, `build:agent`,
`build:ios`, `build:android` — acquired before expensive gates, exclusive for
high-coupling targets (native mobile, TestFlight, Play, full `go test`).

Concrete cross-run sabotage this prevents, observed in code:
`reclaimAutorunDisk` runs **`go clean -cache`** (`autorun.go:124-135`) when free
disk drops below the floor. The build cache is **global**. One run's disk
self-heal wipes the cache out from under every other concurrent run *and the
human on that box*, forcing N slow rebuilds — while the run that did it records
a successful "heal".

### 3.4 Distributed lease store

The freeze gate is in-process and per-machine — correct for deploy drain, wrong
for source/build ownership, which must span machines. Local fast path in the
daemon; sanitized publication for fleet visibility; **TTL mandatory** so a dead
autorun releases by expiry.

Privacy contract (non-negotiable, `CLAUDE.md`): metadata only — repo slug/hash,
branch, lease key, holder device alias/id, slot, timestamps, TTL, state. Never
prompts, stdout, file contents, or absolute paths.

### 3.5 Admission control

`autorun_start` (`autorun_ops.go:100-158`) mints an ID and launches a goroutine.
No count check, no semaphore, no queue, **no slot-collision check** — nothing
refuses a second run on a busy slot. It should answer: is this area owned? is
the runner lane saturated? is the machine within budget? is another machine
better suited? does the gate need a platform this box lacks? Answer "not now" as
`queued`/`parked` with a reason, rather than starting and failing later.

Note `runner_queue_*` is **not** this. It belongs to `RunnerKeeper`, queues
*prompt strings* against a tmux session, drains on a manually-declared mode flag
(`KeeperModeUserDriven`), and shares no state or identifier space with
`autorunSessions` (`runner_keeper_mcp.go:8-18,44-48,119-144`).

### 3.6 Slot collisions — a sharp edge

Worktree path, branch, tmux session name, slot key **and the `$TMPDIR` prompt
file** are all derived from the task file **basename** (`autorun.go:142-148`,
`:380-410`, `autorun_tmux.go:98-104`, `:180-184`). Two task files with the same
basename in different directories collide on all five. The prompt file is
overwritten in place while the sibling's runner is told to read that path.
`autorunPrepareWorkspace` will either adopt the other run's worktree
(`autorun.go:514-517`) or `os.RemoveAll` it (`:518-520`).

### 3.7 Shared mutable state (the "harm each other" inventory)

| Shared thing | Hazard | Cite |
|---|---|---|
| Source checkout | `git checkout main` + dirty-check run **outside** `autorunLandMu`; the `push=false` path takes no lock at all | `autorun.go:836-870`, `:772-779` |
| `autorunLandMu` | covers only fetch→rebase→ff→push **inside one process** | `autorun.go:752`, `:781` |
| tmux server | **no `-L`/`-S` socket separation anywhere** — all runs, keeper sessions and the human share one default server; TUI inherits the server's env | `tmux.go:95-100`, `autorun_tmux.go:151-160` |
| `~/.claude.json` | unlocked read-modify-write; N runs lost-update each other's trust entry, and the loser blocks on the trust dialog | `claude_onboarding.go:55-95` |
| `~/.codex/*` | same shape, best-effort/unlocked | `codex_onboarding.go` |
| Go build cache | global; one run's `go clean -cache` hits everyone | `autorun.go:124-135` |
| `$TMPDIR` prompt files | basename-keyed, overwritten, never cleaned | `autorun_tmux.go:180-184` |

### 3.8 The shared checkout is actively destructive — three forms, one day

This is no longer a theoretical hazard. On 2026-07-18 the single shared checkout
produced **three distinct forms of data loss in one day**:

1. **Sweep** — a bare `git add` + `commit` swept 9 files belonging to another
   session into an unrelated commit. (Known; the mitigation is
   `git commit -- <explicit paths>`, which every commit in this session used.)
2. **Split** — one logical change landed across two sessions' commits.
3. **Revert — the severe one.** A parallel session's `backend/convex/` work was
   reverted **off disk**: gone from `git status`, absent from `HEAD`. It had
   **already been deployed to prod**. For a window, production ran a schema the
   source tree no longer contained — and the next `npx convex deploy` from that
   tree **would have dropped live fields**. It was caught, reconstructed,
   re-verified, committed by explicit path and re-deployed; source and prod agree
   again (independently re-verified while writing this: the handlers and the
   schema field are present in `HEAD`).

A fourth, quieter instance the same day: a concurrent write silently clobbered a
block inside `web/lib/wakeProgress.ts` **after the edit reported success**. Only
`tsc` caught it. An edit tool returning "success" is not evidence the bytes are
still there when a sibling is writing the same tree.

**Why this belongs in an autorun audit.** Autorun has a code path that produces
exactly this shape. `autorunReleaseWorkspace` runs **`git checkout main` on the
shared source checkout** (`autorun.go:854-859`). Its dirty-guard
(`autorun.go:843-849`) is checked earlier and **outside `autorunLandMu`** — which
only covers fetch→rebase→ff→push inside `autorunLandOntoMain`
(`autorun.go:752`, `:781`). Between that check and the checkout, another actor
can dirty the tree; the `push == false` path takes no lock at all
(`autorun.go:772-779`). A `git checkout main` over uncommitted work is precisely
"gone from disk, gone from `git status`, absent from `HEAD`".

I am **not** asserting autorun caused this particular incident — a parallel
interactive session is equally capable of it, and the two are indistinguishable
after the fact precisely because **no durable record of git operations exists**
(§3.12). That indistinguishability is itself the finding.

**Invariant this implies, stated plainly:**

> An autorun must never mutate a working tree it does not own. No `checkout`, no
> `reset`, no `clean`, no `stash`, on any checkout shared with a human or another
> session. Its own worktree is the only tree it may write.

Today autorun violates this by design: it treats `ws.SourceWorkDir` as its own,
runs `git checkout main` there, and removes worktrees and branches from it
(`autorun.go:864-869`) — all outside any lock.

**The structural fix is per-session worktrees, not more discipline.** Explicit-path
commits prevent *sweep*; they do not prevent *revert* or a clobbered write,
because those happen at the filesystem, below git. Every session — human,
autorun, or agent — should get its own worktree off one bare repo, so a
`checkout` or `clean` can only ever destroy that session's own work. Autorun
already proves the model works: its per-run worktrees are the one part of this
system that has never lost anyone else's data. The bug is that the *source*
checkout was left shared.

This is the highest-severity finding in this document. Sweep costs a confusing
commit; revert cost a window where prod and source disagreed on a live schema.

### 3.9 Local-human awareness — **zero**

There is no mechanism by which an autorun knows a human is using the machine.
No presence probe exists anywhere in the package (no `HIDIdleTime`,
`CGSessionCopyCurrentDictionary`, `loginwindow`, `who -u`, `pmset -g
assertions`). `who_is_logged_in` exists as an MCP tool
(`httpserver.go:11890`) and **autorun never calls it**. `reportMachineActivity`
(`machine_activity.go:70-85`) is the opposite signal — it tells the server Yaver
is busy so a managed box is not idle-paused.

The only human-aware concept, `KeeperModeUserDriven` (`runner_keeper.go:55`,
"human attached and vibing"), is **manually declared** via `runner_attach`, never
detected — no `tmux list-clients`, no pane-activity inference.

The CPU backoff is load-aware, not user-aware: a human editing code or reading
docs generates no load and gets no consideration, while a sibling's `go test`
does. Net: autorun will `go clean -cache`, saturate CPU, `git checkout main` in
the shared checkout, and drive the default tmux server while someone is sitting
at the machine.

### 3.9 Resource awareness is real for autorun, absent for deploy

Autorun measures disk/RAM/load before every kick (`autorun_resources.go:28-52`)
after an incident that drove the mini to 1.1 GB free (`:10-17`).

Two flaws:
- **The RAM check reads total, not free.** `getSystemMemoryMB` returns
  `hw.memsize`/`MemTotal` — a constant for the life of the box
  (`process_unix.go:276-299`, floor at `autorun_resources.go:25`). A 64 GB
  machine with 200 MB actually free passes forever. It cannot detect exhaustion,
  which is precisely the multi-run failure mode.
- **The CPU backoff waits one interval and proceeds without re-measuring**
  (`autorun_cmd.go:332-341`). With `Interval: 0` (permitted; `ship.go:295` passes
  it) `time.After(0)` fires immediately and the backoff degenerates into a
  log line. 1-minute loadavg also lags a just-launched sibling by design.

Every check is per-run and blind to siblings: N loops each observe "load
3.9/core, fine" and all kick together.

**Deploy paths have no resource awareness at all.** `RunDeploy`,
`RunDeployAll`, `opsDeployHandler`, `native_build.go` contain zero disk/RAM/CPU
probes. `deployMu` (`deploy_pipeline.go:71`) serializes only `RunDeploy` within
one process — two `build_ios`/`native_build` invocations run fully concurrently
with no admission control. `deployPreflight` (`deploy_all.go:164-177`) is a
build-*correctness* gate, bypassable with `Force: true`.

### 3.10 Cost / quota — nothing

No deploy-cost tracking, no CI-minute tracking, **no TestFlight upload-quota
check** before deploying (the one hard external limit: ~15–20/app/day, no
rollback). `switch_cost.go` is hosting-provider estimation, unrelated and never
consulted by a deploy path. `remote_cost` is a registration stub. Nothing stops N
runs from each deploying; coalescing exists only for callers who voluntarily go
through `ship`. `ops_deploy.go:68` sets `AllowGuest: true` — a guest can deploy
with no owner in the loop, and no confirmation gate exists on any deploy path.

### 3.11 Git awareness gaps

- **No `index.lock` detection anywhere.** Zero hits across `.go` and `.sh`. The
  single most likely human-collision hazard — an editor or GUI holding the index
  — surfaces as a raw git error, unclassified, while push rejection *is*
  carefully classified.
- The dirty-source check (`autorun.go:843-849`) refuses safely but **cannot
  attribute**: it cannot tell a human's in-progress edits from a stale runner
  artifact, and reports neither owner nor paths.
- The shared-checkout guard — the strongest human-concurrency protection in the
  repo — is **shell-only and advisory** (`scripts/autorun-preflight.sh:47-58`),
  enforced by no Go code.
- Landing races are returned in-band and logged to stderr; **no durable record**.
  "How many push races happened last night?" is unanswerable.

### 3.12 Crash recovery and stale sessions

`autorunSessions` is an in-memory map (`autorun_ops.go:86-91`); nothing reloads
at startup. After an agent restart an operator faces an orphaned worktree, an
orphaned branch, and an **orphaned tmux TUI still burning subscription quota** —
while `autorun_status` returns nothing and `autorun_stop` returns `not_found`.
**The abandoned TUI cannot be stopped through any autorun verb.** `finalCommit`
stays empty, which the API documents as "has not finished" — so a crashed run and
a live run are indistinguishable by that field.

Within one agent lifetime the opposite problem: finished sessions accumulate and
`autorun_status` grows until it exceeds the MCP output limit (56 KB / 368 lines
observed) — which is what `tasks/autorun-digest-query.md` exists to fix.

### 3.13 Observability

`autorun autopsy --machine <box>` does not exist. Producing Part 1 of this
document required SSH plus hand-written `find`/`grep` over handoff files, because
the loop log is 90 bytes and the pane is ephemeral. A read-only verb should
report: daemon version and reachability path; running/parked/draining counts;
stale session count; top failure classes; orphaned worktrees/branches/tmux
sessions; push-rejection count; max load and free-disk range; recommended
cleanup — and stop or delete nothing.

---

## Part 4 — Proposed layers

**Layer A — honest local runs.** Split the conflated finish reasons; add a
progress signal that is not "did files change"; expose `maxIters`; paginate and
filter `autorun_status`; trim progress tails; record lease keys in the session
view.

**Layer B — daemon lease manager.** `lease_acquire` / `renew` / `release` /
`status`. Keys: `repo:<id>:path:<glob>`, `:package:<name>`, `:build:<target>`,
`:land:<base>`, `deploy:<target>`. Source leases before kick; build leases before
gate.

**Layer C — fleet visibility.** Publish sanitized lease state (holder alias,
slot, repo hash, key, TTL, state). Never prompts, stdout, contents, or absolute
paths.

**Layer D — scheduler / admission.** Route `autorun_start` through it: overlap →
queued; machine saturated → queued; lane full → queued; platform mismatch →
route to a capable machine; freeze active → parked. Reuse `agent_mesh` capacity
scoring rather than inventing a second model.

**Layer E — ship integration.** Discover freeze targets **from leases** rather
than a user-supplied machine list; block deploy while a non-frozen machine holds
an overlapping lease; keep deploy-once from the pinned SHA.

**Layer F — cost and quota.** Budget providers per target; `ship --dry-run` shows
quota impact; **park** on exhausted quota rather than retrying; deploy history
carries cost/quota fields.

**Layer G — human presence (new).** Detect a live human on the box (idle time,
`tmux list-clients`, unlocked session) and expose it as a first-class input:
defer `go clean -cache`, defer exclusive build leases, and prefer another machine
while a human is actively working there.

---

## Part 5 — Use git as the coordination substrate

Most of what is missing above is a distributed lock with a TTL, a registry of who
holds what, and a durable audit trail. Building that as a new service — or worse,
a Convex table — is the wrong instinct twice over: it duplicates machinery git
already has, and the privacy contract forbids work-derived data (paths, branches,
file contents) in Convex anyway.

Git gives us every primitive, already replicated to every machine, already
surviving restarts, already owned by the user.

| Missing piece | Git primitive that already does it |
|---|---|
| Lease store with atomic acquire | **`git update-ref` with compare-and-swap.** `refs/yaver/lease/<key>` + old-value CAS is a genuine atomic test-and-set; the loser observes rejection rather than silently sharing the lock. Pushing the ref makes it fleet-wide. |
| Who holds what, right now | **`git worktree list`** — an on-disk registry that already exists, survives agent restart, and would have answered "what are these 7 orphans?" without a single `find`. |
| Per-run claim | **Branch names.** `autorun/<task>/<seat>` already exists (`autorun.go:403-410`); a branch on the remote *is* a published claim. |
| Landing coordinator | **`git push --force-with-lease`** is literally a lease, and `--atomic` makes a multi-ref land all-or-nothing. |
| Clobber prevention | Same: `--force-with-lease` refuses to overwrite work that appeared after you last fetched — precisely the revert class in §3.8. |
| Durable record of git ops | **`reflog`** already records every checkout/reset/rebase with a timestamp, per repo. §3.12's "how many push races last night?" is answerable from data already on disk. |
| Per-run metadata without polluting history | **`git notes`** (`refs/notes/yaver`) — attach run id, lease key, resources, heals to a commit without touching its message. |
| Concurrent-git detection | **`.git/index.lock`** — its existence is the signal §3.11 says nobody checks. |
| Session isolation | **`git worktree add`** — the fix in §3.8, and the one mechanism in this system that has never lost anyone's data. |

**Sketch of the shape.** A lease is a ref whose name is the key and whose content
is metadata:

```
refs/yaver/lease/repo/<repoHash>/path/<encodedGlob>   -> blob{holder, slot, machine, acquiredAt, ttl}
refs/yaver/lease/repo/<repoHash>/build/mobile
refs/yaver/lease/repo/<repoHash>/land/main
```

Acquire = create the ref with CAS from *nonexistent*; renew = CAS from your own
value; release = delete. Publish by pushing the ref namespace; discover by
fetching it. TTL is a timestamp in the blob, and any actor may reap an expired
ref — the same fail-toward-running policy the freeze lease already chose
(`autorun_gate.go:46-48`).

**Why this fits Yaver specifically:** it is P2P by construction (no new server),
it keeps work-derived data in the user's own repo instead of Convex, it costs
nothing to host, and a human can inspect and break a stuck lease with plain git
rather than a bespoke admin verb.

**Honest limits — this is not a general-purpose lock service:**

- **CAS is atomic per-remote, not instantaneous.** Two machines can both believe
  they hold a lease until one pushes; the push is the serialization point. Good
  enough for "don't compile the same subsystem", wrong for anything needing
  sub-second mutual exclusion.
- **Fetch latency is the staleness window.** A lease is only as fresh as your last
  fetch of the namespace, so the local daemon still needs a fast in-process path
  (Layer B) with git as the cross-machine tier.
- **Clock skew** makes TTL approximate — reap generously, never aggressively.
- **Ref contention on one hot ref** (`land/main`) needs backoff, which the
  existing push-rejection classifier (`autorun.go:829-834`) already knows how to
  do.
- It does **not** solve §3.2 (TUI dialects) or §3.1 (honest terminal states) —
  those are unrelated and remain the cheapest wins.

## Priority order

Ordered by (evidence of harm × cost to build), not by architectural elegance.

0. **Stop sharing one checkout (§3.8).** Per-session worktrees off one bare repo,
   and the invariant that no automation mutates a tree it does not own — no
   `checkout`/`reset`/`clean` on a shared tree. This is ahead of everything else
   because it is the only item whose failure mode reached **production**: work
   reverted off disk after being deployed left prod and source disagreeing on a
   live schema, one deploy away from dropping fields. Sweep costs a confusing
   commit; revert costs data. Uses `git worktree` — no new machinery (Part 5).

1. **Honest terminal states + a real progress signal.** Cheapest, and it is why
   the mortality table cannot currently be trusted. Until `converged` stops
   meaning "did nothing", every other layer optimises against a lying oracle.
   The progress signal is already on screen and thrown away (§3.2).
2. **`autorun autopsy` verb + orphan reconciler.** Makes every future failure
   cheap to diagnose and reaps the 7 stale worktrees, leftover branches, tmux
   sessions and `$TMPDIR` files.
3. **Launch preconditions in Go, not shell** — dedicated clone, clean tree, not
   behind main, gate green on the untouched tree, scope covering the surfaces the
   task names. `scripts/autorun-preflight.sh` already encodes these; the four
   scope deaths and the unsatisfiable gate would all have been refused at launch.
4. **Local source/build leases + slot-collision refusal**, exposed in
   `autorun_status`. Fixes the basename collision and "do not compile while a
   sibling owns this area".
5. **Free-RAM (not total) + sibling-aware admission**, and make disk reclaim
   stop wiping a shared cache other runs depend on.
6. **Cross-machine lease publication with TTL.**
7. **One landing coordinator** shared by autorun and `git_land`.
8. **Deploy budgets** — TestFlight quota first; it is the only hard external
   limit with no rollback.

---

## Bottom line

The repo has a credible deploy barrier. What it lacks is a coordination layer
*before* work starts and an honest report *after* it ends.

The next structural improvement is not better prompts and not more retries. It is
**leases** — source leases so siblings stop editing the same subsystem, build
leases so nothing compiles into a moving target, landing leases so pushes stop
racing, deploy leases and quota so budget stops burning silently.

But leases come second. First the system has to stop reporting that it succeeded
when it did nothing at all: over one night, **19 runs produced 4 runs' worth of
code, and the logs said almost every one of them converged.**
