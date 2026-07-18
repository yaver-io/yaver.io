# Autorun task graph — why a run must stop being atomic

Date: 2026-07-18
Companion to `AUTORUN_COLLECTIVE_SYNC_AUDIT.md`. That document asked *"may two
runs touch the same code?"*. This one asks the next question: **"why is the
machine idle while a run is 'busy'?"**

---

## The observation that starts this

While deploying today, one tvOS archive occupied Xcode, most of the CPU and a
chunk of disk for ~15 minutes. During that window nothing prevented editing
`web/`, `desktop/agent/`, or `backend/convex/` — different files, different
toolchain, different everything. But an autorun in that state holds its runner
seat, its worktree and its slot for the entire run, so a queued web task waits
on a tvOS *compiler*.

That is the waste. It is not a bug in any function; it is a consequence of the
unit of scheduling being **the run** rather than **the step**.

Measured cost of atomicity, from the same night the audit examined: six runs
overlapped between 12:03 and 12:40 on an 8-core box, load hit 17.40, and four
runners in one run died in sequence with *"TUI session vanished mid-turn"*. Every
one of those runs held every resource it might ever need, for its whole life,
whether it was compiling or thinking.

---

## A run is not one thing — it is a sequence of very different needs

An autorun iteration is at least four phases, and they contend for almost
disjoint resources:

| Phase | Genuinely needs | Does NOT need |
|---|---|---|
| **orient** — read task, grep, plan | runner seat, read-only tree | build toolchain, disk, exclusive anything |
| **edit** — write code | runner seat, **exclusive source area** | build toolchain, CPU |
| **build/gate** — compile, test | **exclusive build target**, CPU, RAM, disk | runner seat (the runner is *waiting*) |
| **land** — commit, rebase, push | **the landing lease** for `base:main` | runner seat, build toolchain |

The middle two are where the money goes, and they need opposite things. During
**build/gate** the expensive subscription runner seat sits idle waiting on a
compiler; during **edit** the toolchain sits idle waiting on a model.

Today one loop holds all four columns from start to finish. Two consequences:

1. **A build blocks unrelated development.** tvOS compiling should never stop a
   `web/` edit, but it does, because the seat is inside the same loop.
2. **A thinking runner blocks a build slot it isn't using.** The reverse waste,
   equally real and less visible.

---

## Resource classes, and their exclusivity

Scheduling needs typed resources, not one lock. The classes and *why* each has
the exclusivity it has:

| Class | Key | Exclusivity | Why |
|---|---|---|---|
| source area | `repo:<id>:path:<area>` | **exclusive** | two writers in one area lose an iteration to a stash — measured six times in one night |
| build target | `build:ios` / `android` / `web` / `agent` | **exclusive per target** | concurrent Xcode/Gradle thrash cache and disk; `go clean -cache` in one run wipes it for all |
| runner seat | `runner:<machine>:<id>` | **exclusive** | one TUI, one conversation |
| device | `device:<udid>` | **exclusive** | one app installs at a time |
| external quota | `quota:testflight` | **counted** (~15–20/day) | hard external limit, no rollback |
| landing | `repo:<id>:land:main` | **exclusive** | serializes the push race |
| machine capacity | CPU / free RAM / disk | **shared, admission-controlled** | N runs each seeing "load fine" and all kicking is how load reached 17.40 |

The critical property: **a step releases what it does not need.** A run in
`build:ios` must hand its runner seat back, because that is precisely the seat a
queued `web/` task can use for its edit phase.

That single rule is what lets "building tvOS" and "developing web UI" overlap.

---

## Task identity — deriving roles instead of declaring them

The scheduler cannot overlap what it cannot classify. But a task already carries
almost everything needed, and none of it requires the author to write a manifest:

- **Source areas** — from `--scope` globs, already normalized by
  `autorunOwnedAreas` (`autorun_coordination.go`).
- **Build targets** — a deterministic map from area → target:
  `mobile/**`→`ios`+`android`, `web/**`→`web`, `desktop/agent/**`→`agent`,
  `tvos/**`→`tvos`. This is the same mapping `ship_targets.go` already does for
  deploy detection; it should not be written twice.
- **Toolchain/platform requirement** — from build target: `ios`/`tvos` demand
  macOS + Xcode, `agent` demands Go anywhere, `web` demands Node anywhere. This
  is what makes placement *decidable* rather than a preference.
- **Cost lane** — `runnerCostTier` already distinguishes a flat subscription seat
  from an API-key seat, and already reasons about "parallel overflow"
  (`agent_mesh.go`). Overflow work belongs on the cheap lane; the scarce
  subscription seat should be spent on the phases that actually need it.

So "roles" are **inferred**, then optionally overridden by explicit task front
matter. Inference first matters because every task written before this exists
still gets classified.

---

## The graph, and what "going faster" actually means

Two graphs, and conflating them is the trap:

**1. Dependency graph (correctness).** Task B needs A's output. Real, but rare
between topics — most parallel autoruns are genuinely independent, which is why
they were launched together in the first place.

**2. Contention graph (throughput).** Task B needs a resource A holds. This is
the one that was actually costing us, and it is *dynamic*: it changes as each
task moves between phases.

Speed comes from the second graph, and specifically from three rules:

- **Release-on-phase-change.** Hand back the seat when entering build; hand back
  the build target when entering land.
- **Schedule the step, not the run.** A ready step whose resources are free may
  start even if its task is "behind" another in queue order.
- **Prefer disjoint work when blocked.** If the head of the queue wants
  `build:ios` and it is held, run the next task that needs only `web` — rather
  than idling, which is today's behaviour.

The scheduling shape is a **resource-constrained project schedule**, not a DAG
walk. It is well understood, and a greedy list scheduler (pick the highest
priority *ready* step whose resources are all free) captures most of the
available speedup without any of the machinery a full optimizer implies.

---

## Multi-device: the placement scorer already exists and autorun ignores it

This is the same finding as the bus: the piece is built, and not wired.

`agent_mesh.go` has `planGraphPlacements` → `chooseNodePlacement` →
`scoreNodePlacement`, with a `meshPlannerState.reserve` so a plan does not
double-book a machine. It already scores: runner readiness (+220), online state
(−5000 offline), local preference, explicit pinning (+1000), prior-machine
affinity with a sticky bonus, and **cost lane** — subscription vs API-key, with
"parallel overflow" reasoning. `console_machines.go` supplies `MaxTaskSlots`.

`autorun_start` calls none of it (`autorun_ops.go`). It runs wherever it was
asked to run, regardless of whether that machine has the toolchain, the seat, or
the headroom.

Wiring autorun through the existing planner gives multi-device for free, and the
step model makes it *useful*: an `ios` build step is placeable only on a macOS
machine, while the same task's `edit` and `agent` steps can run anywhere — so a
Linux box can keep developing while a Mac compiles.

Cross-machine claims ride the two mechanisms that already exist and need no new
server: the **bus** for state (single-writer per topic, no broker, no election)
and **git-ref CAS** for the leases themselves (`AUTORUN_COLLECTIVE_SYNC_AUDIT.md`
Part 5). No scheduler node, therefore no scheduler node to die: a machine that
wants work CAS-claims the lease and the winner proceeds — work-stealing, not
assignment.

---

## What "fork the runner" should and should not mean

The instinct — *when blocked on a build, let the runner keep developing* — is
right, but "fork" is the wrong primitive for the dangerous version of it.

**Safe (do this):** while task A is in `build:ios`, its seat runs **task B's**
edit step in **B's own worktree**. Different task, different area, different
tree. This is just the release-on-phase-change rule, and it is the whole win.

**Unsafe (do not):** the same task continuing to edit its own tree while that
tree is being built. The build would compile a moving target, and the gate result
would describe a tree that no longer exists — which is precisely how "verified"
commits stop meaning anything.

So: **overlap across tasks, never within a task's own build.** The seat is
shared; the tree is not.

---

## Increments, in dependency order

1. **Split the loop's phases and release between them.** No scheduler yet — just
   stop holding the seat during gate. This alone unblocks "build tvOS while
   developing web".
2. **Type the leases** (source / build / seat / land) on top of the existing
   local admission (`autorun_coordination.go`), with the area→target map shared
   with `ship_targets.go`.
3. **Greedy step scheduler** over ready steps; queue rather than refuse when a
   resource is held — today's admission refuses, which is correct but wastes the
   caller's intent.
4. **Wire `autorun_start` into `planGraphPlacements`** so placement respects
   toolchain, seat and capacity instead of trusting the caller.
5. **Publish lease + step state on the bus**, so every surface sees one honest
   picture and the fleet needs no coordinator.
6. **Quota as a resource** — `quota:testflight` counted before a deploy step is
   admitted, rather than discovered at upload.

Steps 1 and 2 are where the throughput is. Steps 4–5 are where the *fleet* is.

---

## The honest caveat

None of this helps while a run cannot tell working from blocked from finished. A
scheduler fed by a loop that reports `converged` when a runner did nothing will
schedule confidently against fiction. **Honest terminal states and a real
progress signal remain prerequisite** — they are priority 1 in the audit for
exactly this reason, and the evidence-based recap that landed today
(`recap_evidence.go`, claim vs landed) is the first half of it.
