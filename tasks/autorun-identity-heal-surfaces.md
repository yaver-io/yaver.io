---
doer: codex
---

# Autorun: run identity in git, a self-healing clone, and the missing surfaces

## Context

On 2026-07-17, 16 autorun runs on the Mac mini went mostly-red. The forensics are
done and four fixes already landed on main — read them before touching anything,
because they define the shape of what is left:

- `e7adc27bf` — an explicit `runner:` is a PREFERENCE, not a precondition
  (`selectAutorunRunner` / `selectAutorunRunnerWith` in `autorun.go`).
- `e960121f1` — the `--version` probe trusts the ANSWER, not the exit
  (`looksLikeRunnerVersion`, `runnerVersionProbeTimeout` in `tasks.go`).
- `f7b9f3d54` — landing queue: `autorunLandMu` + rebase-and-retry
  (`autorunLandOntoMain`, `autorunPushWasRejected` in `autorun.go`).
- `a16b05f17` — work outcome vs landing outcome (`autorunLandingError`,
  `autorunWorkSucceeded`, `LandingError` on the session/view).

Facts established by that investigation, which you may rely on:

- `autorun_status` already returns everything a UI needs per run: `id`, `slot`,
  `task`, `runner`, `activeRunner`, `master`, `status`, `startedAt`, `finishedAt`,
  `iterations`, `commits`, `finishReason`, `finalCommit`, `landingError`, `error`,
  `heals[]`, `progressTail`. The wire shape is
  `{ok: true, initial: {sessions: [...]}}`. Verified live.
- `Slot` (task:seat) is an agent's STABLE address across runs; `ID` identifies ONE
  run. Order by slot, never by time or status — see the doc comment on
  `sortAutorunViewsBySlot` and `mobile/src/lib/agentSlots.ts`. A card that moves
  because a sibling changed defeats the whole point.
- A run has ended only when it has a `finalCommit`. A quiet loop is not a finished
  one.

Do NOT touch GLM. `tasks/glm-remove-runner.md` owns retiring it; overlapping edits
will collide.

## Task 1 — Put the run id in git (the reason none of this was answerable)

**The bug:** autorun commits identify by TASK NAME only. `autorun.go:148`
(`autorunFinalCommitSubject`) emits `autorun: <marker> for <task> (<reason>)`, and
the per-iteration commits say `autorun: verified iteration N for <task>`. Two runs
of one task are therefore INDISTINGUISHABLE in git. That really happened:
`2cb604aa3 (converged)` was followed by `abfce0c43 verified iteration 1` for the
same task, which reads as a contradiction but was simply two different runs.
`toolchain-and-remote-git` had NINE runs that morning across two seats.

**Do:**

1. Give every autorun commit git trailers. `autorunFinalCommitBody` in `autorun.go`
   already writes the metadata block (Finish reason / Iterations run / Verified
   commits kept / Runner / Master) — that is the natural home. Add:

   ```
   Autorun-Run-Id:    <id>
   Autorun-Slot:      <task:seat>
   Autorun-Iteration: <n>
   ```

   Use real git trailers (last paragraph, `Key: value`, no blank lines inside), so
   `git log --grep` and `git interpret-trailers` work and they survive a
   cherry-pick. Put them on the per-iteration commits too — those are the ones you
   cannot currently attribute.

2. The id must be unique ACROSS MACHINES. `autorun_ops.go:104` mints
   `fmt.Sprintf("autorun-%d", time.Now().UTC().UnixNano())` — a bare nanosecond
   timestamp. Autoruns now run on this mini AND on the developer's laptop, so two
   runs can collide in principle and no human can tell them apart by eye. Make it a
   ULID/UUID, or keep a sortable time prefix with a short random suffix. Do not add
   a dependency for this if the stdlib will do (`crypto/rand`).
   `autorun_status`/`autorun_stop` already take the id — keep them working, and do
   not break `autorunSlotKey`.

3. The id needs to reach the commit builder. It lives on `autorunSession`
   (`autorun_ops.go`); the builder takes `autorunOptions`. Thread it through
   explicitly — do not reach for a global.

**Verify:** a test asserting a rendered commit body parses as trailers and carries
the run id; a test that two ids minted back-to-back differ.

## Task 2 — The clone must heal itself, not refuse

**The bug:** two runs died on `worktree must be clean before autorun; found:
desktop/agent/binary_discovery_test.go`, and one on `git pull --ff-only: …
Diverging branches can't be fast-forwarded, aborting`.

Autorun OWNS its isolated clone (`~/Workspace/yaver-autorun-<name>`,
`~/.yaver/worktrees/<name>`). Refusing to start because the clone it owns is dirty
is the loop giving up on a mess it made itself. (The diverged case is now largely
fixed at the source by `autorunLandOntoMain`'s rebase-and-retry — but a clone that
is ALREADY diverged from an older failure still has to recover.)

**Do:** when the workspace is autorun's OWN clone/worktree, reset it to a clean
state before starting rather than erroring — and record the recovery as an
`autorunHealEvent` (`autorunHealDiskReclaim` is the precedent; add a kind) so it is
visible in `autorun_status` instead of silent.

**Hard limits — read twice:**

- ONLY ever clean a path autorun created and owns. NEVER the developer's own
  checkout. If you cannot prove the path is autorun's own workspace, refuse exactly
  as today. This repo has already lost a working tree once to a path built from a
  variable.
- Preserve anything that could be work: prefer `git stash push --include-untracked`
  (there is already a diagnostic-stash precedent in `autorun.go`) over
  `git reset --hard`/`git clean -fdx`. Never delete unexamined files.
- `git reset --hard` and `git clean` are on autorun's own forbidden-command list
  for the RUNNER. Do not hand the runner a way to invoke them.

## Task 3 — Autorun UI on the surfaces that have none

Web landed one (`web/components/dashboard/AutorunsView.tsx`, commit `e99cd3331`);
mobile already had `mobile/app/autoruns.tsx`. **car, glass/AR-VR, Wear OS, tvOS and
watchOS have nothing** — the cross-surface parity rule in CLAUDE.md is unmet.

Read `AutorunsView.tsx` first: it is the reference for what to say and what NOT to
collapse (still-running is decided by `finalCommit`, `landingError` means the work
succeeded and only the bookkeeping failed, a differing `activeRunner` is a healthy
self-heal and not an error).

**Do, in this order:**

1. **RN surfaces** — `mobile/app/car-voice-coding.tsx`, `glass-terminal.tsx`,
   `glass-workspace.tsx`. These consume `quic.ts`'s `callOps("autorun_status")`
   like `autoruns.tsx` does. Note `mobile/src/components/SessionStrip.tsx` is
   TASKS, not autoruns — do not confuse them.
2. **Wear OS** (`wear/`), then **tvOS** (`tvos/YaverTV/`) and **watchOS**
   (`watch/YaverWatch/`). These are native and inherit NOTHING from RN — port
   deliberately.

**Scope the ambition to the surface.** A watch or a car HUD must NOT get a
management console: glanceable only — is anything running, is it red, and stop it.
Full detail stays on web and mobile. Use the fixed-slot model
(`mobile/src/lib/agentSlots.ts`) so a pane never moves because another run changed.

**Check before you write:** `tvos/`, `watch/` and parts of `mobile/` were under
active edit by another session. Re-read each file immediately before editing it,
and keep every commit to explicit paths.

## Task 4 — Make graceful landing a verb, not a ritual

**Why:** `autorunLandOntoMain` (`autorun.go`, commit `f7b9f3d54`) knows how to land
onto a moving branch: take the queue lock, fetch, `pull --rebase`, merge, push, and
retry on `! [rejected] (fetch first)` — aborting a stuck rebase instead of stranding
the clone. **Only the autorun loop can reach it.** Every other actor — a coding
agent, a surface, a human at a terminal — hand-rolls the same dance and gets it
wrong. On 2026-07-17 a session had to land work by cherry-picking into a throwaway
`git worktree` FOUR times, and once deleted a branch whose work had not landed
because the push had been rejected and the delete had not.

**Do:**

1. Expose landing as an ops verb (e.g. `git_land`): given a workDir and a target
   branch, do fetch → rebase → push with the same bounded retry, and RETURN what
   happened — landed / retried N times / rebase conflict / rejected-for-a-reason-
   retry-cannot-fix (auth, protected branch, DNS). Reuse the existing helpers;
   do NOT fork a second copy of the rule. `autorunPushWasRejected` is the only
   place that decides "someone landed first".
2. Never `--force`. Never touch a checkout the caller does not own. On conflict,
   stop and report — a merge invented by a retry loop is worse than a failed push.
3. Register it like the other verbs so `ops_verbs` lists it with a payload schema,
   and it is reachable from every surface, not just the CLI.

**And make the loop aware of the git state it is standing in.** Autorun currently
discovers the repo's condition only by failing: a dirty clone, a diverged main, a
branch already landed. Surface it — is the workspace clean, how far ahead/behind
origin, did the last land succeed — in `autorun_status` alongside `landingError`,
so a surface can say "3 commits waiting to land" instead of the user learning it
from a red run an hour later.

**Test the classifier, not the network.** `autorun_land_test.go` is the precedent:
it asserts against verbatim git output (`! [rejected] main -> main (fetch first)`,
`Permission denied (publickey)`, `GH006: Protected branch update failed`) rather
than standing up a remote.

## Definition of done

- Every autorun commit carries `Autorun-Run-Id` + `Autorun-Slot`, and two runs of
  one task are trivially distinguishable in `git log`.
- Ids are unique across machines.
- A dirty/diverged autorun-OWNED clone recovers and records a heal; a
  developer-owned checkout is still refused.
- car, glass, Wear, tvOS, watchOS each show autorun state; a run in progress is
  visible without opening a terminal.
- `go build ./...`, `go vet ./...`, the scoped autorun tests, and `tsc --noEmit`
  all pass.

## Notes

- NEVER run a bare `go test ./...` in `desktop/agent` — `TestAuthLogout` hits the
  real `~/.yaver` and signs the machine out. Always scope with `-run`.
- This checkout is shared with other sessions. Commit with explicit paths
  (`git commit -- <paths>`); never `git add -A`.
