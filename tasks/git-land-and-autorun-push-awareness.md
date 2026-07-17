---
doer: codex
---

<!-- Single seat. claude is not authed on the mini; seats in front matter are
     binding, so naming an unauthed master fails the run at iteration 1. -->

# `git_land` — graceful rebase as an ops verb, and push-awareness in autorun

## Why (all of this happened on 2026-07-17, not hypothetically)

**Autorun kills runs on a push race.** Five runs died this way in one morning.
`--push` shells out to `git push`, gets

```
! [rejected]  main -> main (fetch first)
```

and ends the run: `stack-detect-wiring:codex` (`gate failed` + `recording the
final autorun commit also failed`), `toolchain-and-remote-git:codex`,
`toolchain-and-remote-git:opencode` (×2). One even hit
`remote: error: refusing to update checked out branch: refs/heads/main` from
pushing at a non-bare checkout. Another died on
`git pull --ff-only: Not possible to fast-forward, aborting.`

This is not a runner failure. It is `main` moving — which on this box is the
NORMAL state: several sibling sessions and 2-3 concurrent autoruns all push the
same ref. A loop that dies because someone else committed is a loop that dies
every time it works.

**And the humans hit it too.** A `git pull --rebase` in the shared checkout dies
with `cannot pull with rebase: You have unstaged changes` — because the tree
holds ANOTHER session's uncommitted work. The safe move (push from a clean clone)
is 6 manual steps nobody remembers under pressure.

## Part 1 — `git_land` ops verb

One verb that lands local commits on `main` without ever destroying anyone's
work. Register in `ops_git.go` (**it already exists — do NOT create it**).

```
ops git_land { machine?, remote?="github", branch?="main", dryRun?=false }
```

Algorithm, in order. Each step's failure is a distinct, named outcome — never a
generic error:

1. `git fetch <remote> <branch>`. **Re-fetch; never trust a cached ref.** A
   stale `github/main` is why a push was rejected as "behind" while
   `rev-list --count` claimed 2 ahead — `git ls-remote` is the authority.
2. `git cherry` / patch-id: drop local commits already upstream. This is usually
   the WHOLE divergence — a sibling re-landed your work under a different sha,
   so your local copy is redundant. Report `alreadyUpstream: N`.
3. If nothing to land → `outcome: "up_to_date"`. Done.
4. Dirty-tree check, and this is the load-bearing one:
   - dirty files that are **yours to land** → fine.
   - dirty files that are **NOT** part of this landing → **STOP**.
     `outcome: "blocked_foreign_changes"`, list them.
     **NEVER `--autostash`, `stash`, `reset --hard`, or `checkout -- .`.**
     A sibling session wiped ~15 finished, typechecked files this way today.
     Uncommitted work in this tree is someone's live edit, not debris.
5. Rebase onto `<remote>/<branch>`. On conflict → abort cleanly
   (`git rebase --abort`), `outcome: "conflict"`, name the files. Never resolve
   by guessing.
6. Push. On rejection, loop to 1 — **bounded** (3 attempts, jittered backoff).
   A push race is expected; an infinite retry is a busy-wait against siblings.
7. **Never `--force`/`--force-with-lease` to `main`.** Not as a fallback, not on
   the last attempt. If it cannot land honestly it reports why.

Fallback when the tree is blocked by foreign changes: land from a throwaway
clone (fetch → apply the commits → push) so a dirty shared tree never blocks a
clean landing. That is the manual dance done by hand today; make it the verb.

`dryRun` reports the plan (`ahead`, `behind`, `alreadyUpstream`, `foreignDirty`,
`wouldRebaseOnto`) without touching anything.

## Part 2 — autorun push-awareness

**A push race must HEAL, not kill.** The vocabulary already exists —
`autorun.go:63-67`:

```go
autorunHealRunnerFailover = "runner_failover"
autorunHealDiskReclaim    = "disk_reclaim"
autorunHealCPUBackoff     = "cpu_backoff"
```

Add `autorunHealPushRebase = "push_rebase"` and record it via the existing
`autorunHealEvent{Iteration, Kind, Detail}`. It then flows to `autorun_status`,
the final commit body, and every surface for free — the machinery is built.

Change `executeAutorun`'s push path (`autorun_cmd.go:~391`, and the final-commit
push at `:237-241`):

- On `! [rejected] ... (fetch first)` → `git_land` semantics (fetch, rebase,
  retry, bounded), record a `push_rebase` heal. Do NOT end the run.
- Distinguish **rejected-because-behind** (transient, heal it) from
  **rejected-because-refused** (`refusing to update checked out branch`,
  auth failure) — those are terminal; healing them is a busy-wait.
- The **final** autorun commit must land even when its push races. Today it
  double-fails: `"runner failed (recording the final autorun commit also failed:
  push final commit: ...)"` — the run loses BOTH its result and its epitaph. The
  final commit is the marker that says the run ended; it must survive contention.
- Preflight in the same spirit as the clean-worktree check: if the remote is a
  non-bare checkout (`refusing to update checked out branch`), fail at START with
  the reason, not after 40 minutes of work.

## Part 3 — surface it (this is what the UI asked for)

`push_rebase` heals ride the existing `heals[]` in `autorunSessionView`, so
`agentSignalFromAutorun` (`mobile/src/lib/agentStatus.ts:189`) already renders
them as `healing` with a label — no new UI contract. Add the label mapping for
the new kind. See `docs/architecture/AUTORUN_SURFACES.md`.

## DO NOT BUILD. DO NOT RUN TESTS.

Owner's instruction: **do the coding, commit, push to main. That is all.**

No `go build`, no `go test`, no `tsc`, no gradle/xcodebuild — not even to check.
This box runs several autoruns at once and a Go build cache is what filled its
disk to 1.1 GB free before (`reclaimAutorunDisk` exists for that).

So **nothing verifies your edits.** Edit conservatively; if a change needs a
compiler to know whether it is right, write it under "Needs verification" in the
progress file instead of guessing.

**NEVER** run a bare `go test ./...` in `desktop/agent` — `TestAuthLogout` hits
the real `~/.yaver` and signs the owner out.

## Prior art — read before inventing

- `ops_git.go` — **exists** (3 verbs registered); `git_land` belongs there and the
  name is free. **It already has a `git_push` verb** — read it first and decide
  deliberately: either `git_land` supersedes it (and `git_push` delegates), or
  `git_land` is the race-aware wrapper around it. Do NOT ship two verbs that push
  and disagree about what happens on a rejection; that is how the surfaces end up
  calling the naive one. Whatever you choose, say so in the verb description.
- `autorun.go:450` `validateAutorunShellCommand` already forbids
  `git push --force`, `git rebase`, `git reset --hard`, `git tag` in GATES. That
  ban is about untrusted gate strings; `git_land` is trusted agent code doing a
  bounded, non-destructive rebase. Do not "fix" the ban, and do not let
  `git_land` become a way to smuggle a force-push past it.
- `autorun.go:105` `readyAutorunRunners` + `autorun_cmd.go:354` — the failover
  heal: the model for "recoverable failure, record it, carry on".

## Done means

- `ops git_land` lands commits through a push race, bounded, no force, and
  reports a named outcome for every path.
- It **refuses** rather than touches foreign dirty files. There is no flag that
  makes it stash them.
- An autorun whose push races **heals and continues**, recording a `push_rebase`
  heal; the five failure modes above stop ending runs.
- The final autorun commit lands despite contention.
- `dryRun` explains a divergence without changing anything.
