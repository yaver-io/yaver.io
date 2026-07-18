# Autorun: stop it, and don't lose its work — progress + what's left

**For:** the next session. Self-contained; you don't need the originating thread.
**Date:** 2026-07-18. **Branch:** work is UNCOMMITTED in the shared checkout
(`~/Workspace/yaver.io`) unless someone has since committed it — check
`git status desktop/agent/autorun_wrapup.go` first.

---

## The incident this came from

Two autoruns were running on the Mac mini (`pokayoke@100.89.155.25`):
`wake-autorun` and `yaver-autorun-merged-remaining-codex`.

`yaver ops autorun_stop_all` returned **`{"count": 0, "stopped": []}`** — it
stopped nothing, because both had been launched as **raw tmux** by sibling
Claude sessions and the in-process `autorunSessionManager` had never heard of
them. The only way to stop them was `tmux kill-session`, which would have
destroyed everything the converged run had produced: **16 modified files** in
`~/.yaver/worktrees/merged-remaining-codex` that turned out to `go build` clean.

Two defects, both real:

1. **Stop can't see runs it didn't start.** `autorun_stop_all` iterates
   `m.sessions` — an in-memory map. A daemon restart, a second session, or a
   hand-rolled `tmux new-session` makes a live loop invisible to it.
2. **Stop is a kill, not a wrap-up.** Nothing preserves a run's uncommitted
   output before tearing it down.

## What was done operationally (already complete, don't redo)

- `~/.yaver/worktrees/merged-remaining-codex`: 16 files committed as
  `01060f596` and pushed to branch **`autorun/merged-remaining/wrapup-20260718`**.
  `go build ./...` passes. **Not reviewed, not merged to main** — a human should
  review before merging.
- `~/Workspace/yaver-wake-autorun`: nothing worth saving. Its staged
  `tasks/wake-sleep-all-surfaces.md` was byte-identical to `main` (already
  pushed by the sibling session as `99a505836`); the rest was npm lockfile churn.
- Both tmux sessions killed. `tmux ls` → "no server running". Nothing is
  running on the mini now.

⚠️ **Two things I got wrong on the mini — check them:**

1. I chained `git rebase … | tail -3 && git push`, and `tail` masked the
   rebase's non-zero exit. The push ran **mid-rebase with a conflict
   unresolved**, fast-forwarding remote `autorun/merged-remaining/codex` from
   `5f9bb19a6` to `f2f6dfdca` — a partial-rebase state. It was a fast-forward,
   not a force, so no history was lost, but **that branch is in an odd state and
   should be looked at.** I aborted the rebase and pushed to a clean new branch
   instead. Never pipe a git command whose exit code you are about to `&&` on.
2. That worktree's remote is **`git@github.com:kivanccakmak/yaver.io.git`** — the
   pre-org-transfer URL. It still works via GitHub's redirect, which is exactly
   how it hides its own staleness (CLAUDE.md warns about this). Repoint it to
   `yaver-io/yaver.io`.

## What was built (code, uncommitted)

Two files in `desktop/agent/`. `go build ./...` passes; the CLI was exercised by
hand (help / list / stop-refuses-without-args all behave).

### `autorun_wrapup.go` (new)

- `discoverAutorunTmuxSessions(registeredDirs)` — finds loops by **tmux session
  name shape** (`yaver-autorun-*`, `autorun-*`, `*-autorun`) rather than by the
  manager's memory, and marks each `Registered` by comparing its `session_path`
  against the dirs the manager is driving. An unregistered loop is precisely the
  one `autorun_stop_all` cannot stop, and `yaver autorun list` says so inline.
- `wrapupWorkDir(dir, sessionName, push)` — commits whatever is uncommitted onto
  `autorun/wrapup/<run>-<UTC timestamp>` and pushes it. **Never pushes to main**
  (leftovers are unreviewed by construction, and this runs while tearing a run
  down — the worst moment to touch a shared branch). Collects paths explicitly
  from `git status --porcelain` and uses `git add -- <paths>` / `git commit --
  <paths>`, never `add -A`, per the shared-index rule.
- `registeredAutorunSessionNames()` — workDir set from the manager. Keyed by
  directory because `autorunSession` records `WorkDir`, not a tmux name.

### `autorun_cmd.go` (modified)

Subcommand dispatch added ahead of the existing flag parsing, so the bare form
`yaver autorun --task=…` is untouched:

```
yaver autorun list                 # incl. unregistered loops
yaver autorun wrapup [--session=N] [--dir=P] [--no-push]   # alias: toparla
yaver autorun stop  [--session=N] [--all] [--no-wrapup] [--no-push]
yaver autorun help
```

`stop` wraps up **first**, then kills the tmux session, then also cancels
anything the in-process manager owns — so a tmux-only sweep can't leave an
ops-started loop running. `--no-wrapup` is the destructive opt-out.

---

## What's left

### 1. The ops/MCP verb (the "API for toparla") — NOT DONE

Only the CLI exists. `autorun_wrapup.go` was written to be the shared core, so
this should be thin:

- Register `autorun_wrapup` in `desktop/agent/autorun_ops.go` next to
  `autorun_stop` / `autorun_stop_all` (grep `registerOpsVerb(opsVerbSpec{Name:
  "autorun_stop"`). Payload: `{session?, dir?, push?}`. Return `[]WrapupResult`
  — it's already JSON-tagged.
- Make `autorun_stop` / `autorun_stop_all` **wrap up before cancelling**, and
  make them discover tmux loops too. Right now they still only see `m.sessions`,
  which is defect #1 above — the CLI fixes it, the verbs don't. Until this lands,
  `ops autorun_stop_all` on a remote box still silently stops nothing.
- Once the verb exists, `--machine=` works for free through the existing ops
  forwarding (`dispatchRemoteAutorun` is the pattern), giving remote
  stop/wrap-up. **That is the piece that would have made today's cleanup one
  command instead of twenty.**

### 2. Tests — NONE WRITTEN

Nothing in `desktop/agent/*_test.go` covers any of this. Follow the house
pattern (real temp git repos, no mocks):

- `looksLikeAutorunSession` — the three name shapes, plus non-matches.
- `wrapupWorkDir` — temp repo with dirty files ⇒ branch created, files
  committed, `main` untouched; clean repo ⇒ `Clean: true`, no branch; a path
  with spaces and a renamed file (the `old -> new` parse); non-repo dir ⇒ error
  not panic.
- `registeredAutorunSessionNames` ⇒ `Registered` flag correctness.
- Note `--no-push` for tests; don't let a test push a branch.

### 3. Known gaps in what I wrote

- **Rename parsing is untested.** `git status --porcelain` renames come through
  as `old -> new` and I keep the destination; verify against a real rename.
- **`session_created` parsing is fragile.** I convert tmux's epoch seconds via
  `time.ParseDuration(s + "s")`, which works but is a strange route. Prefer
  `strconv.ParseInt`.
- **No dry-run.** `yaver autorun wrapup --dry-run` (print what would be saved)
  would make this much safer to trust.
- **Wrap-up leaves the run's worktree on the new branch.** `wrapupWorkDir` does
  `git checkout -b` and stays there. Harmless when you're about to kill the run,
  wrong if you wrap up a *live* one. Either return to the prior branch or refuse
  to wrap up a running loop without `--force`.
- **No stale-remote guard.** Given today, `wrapupWorkDir` should warn when
  `git remote get-url` points at `kivanccakmak/yaver.io`.

### 4. Adjacent, worth folding in

`autorun_start` should record the tmux session name on `autorunSession` so
`Registered` can be matched by name rather than inferred from `WorkDir`. Two
loops in one directory currently both read as registered.

---

## Ground rules (learned the hard way, this week, in this repo)

1. **Never `&&` off a git command you've piped through `tail`/`head`** — the
   pipeline's exit code is the last stage's. This pushed a mid-rebase state today.
2. **Many sessions share this checkout.** Branch per unit of work;
   `git commit -- <paths>` always; `git diff <file>` before committing it —
   pathspec commits stop foreign *files*, not a sibling's edits *inside a file
   you're also editing*. That broke `main` earlier today.
3. **Committed work is immutable.** Fix forward with a new commit; never revert
   or force-push over someone else's work.
4. **`npm run build` in `web/`, never `tsc --noEmit` alone** — tsc resolves
   untracked files CI can't see. That produced a false green and a wasted release
   run today.
5. **Don't leave a Hetzner box running.** Not relevant to this task, but the
   mini is Tailscale-reachable at `pokayoke@100.89.155.25` and is always-on by
   design — that one is fine.

## Unrelated but open, same session

`docs/architecture/DEVICE_TRUTH.md` + `DEVICE_TRUTH_TODO.md` — device
online/offline honesty across every surface. Phase 1 shipped (`f9e2835f2`);
phases 2–5 are speced in the TODO file and are independent of this work.
