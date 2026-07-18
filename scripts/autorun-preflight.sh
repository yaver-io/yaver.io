#!/usr/bin/env bash
# autorun-preflight.sh — refuse to start an autorun that cannot possibly finish.
#
# Every check below is a failure we actually shipped into, not a hypothetical.
# On 2026-07-17 six runs went out on the mini overnight and four came back with
#
#     Finish reason: scope violation
#     Iterations run: 1
#     Verified commits kept: 0
#
# — a whole night of paid compute that produced four progress notes and no code.
# The runs were not wrong and the runner was not wrong; they were launched into
# a state where finishing was impossible. That is what this catches.
#
# Usage:
#   scripts/autorun-preflight.sh --task <abs-path> --gate <cmd> --runner <name> \
#       [--scope <glob>]... [--base github/main] [--min-disk-gb N] [--max-load-per-core F]
#
# Exit 0 = safe to launch. Non-zero = do not launch; the reason is printed.
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

TASK=""; GATE=""; RUNNER=""; BASE="github/main"
MIN_DISK_GB=12; MAX_LOAD_PER_CORE=4.0
SCOPES=()
while [ "$#" -gt 0 ]; do
  case "$1" in
    --task) TASK="$2"; shift 2 ;;
    --gate) GATE="$2"; shift 2 ;;
    --runner) RUNNER="$2"; shift 2 ;;
    --scope) SCOPES+=("$2"); shift 2 ;;
    --base) BASE="$2"; shift 2 ;;
    --min-disk-gb) MIN_DISK_GB="$2"; shift 2 ;;
    --max-load-per-core) MAX_LOAD_PER_CORE="$2"; shift 2 ;;
    *) echo "unknown option: $1" >&2; exit 2 ;;
  esac
done

fail=0
warn=0
step()  { printf '\n=== %s ===\n' "$1"; }
ok()    { echo "ok: $1"; }
bad()   { echo "PREFLIGHT FAIL: $1"; fail=1; }
soft()  { echo "PREFLIGHT WARN: $1"; warn=1; }

# 1. Dedicated clone. A shared checkout means another session's edits land in
#    this run's commits, and `git add` sweeps files the run never touched.
step "dedicated clone"
if [ "$ROOT" = "$HOME/Workspace/yaver.io" ]; then
  bad "running in the SHARED checkout ($ROOT). Autorun needs its own clone — a
     parallel session's edits will be swept into this run's commits."
else
  ok "dedicated clone: $ROOT"
fi

# 2. Clean worktree. Autorun commits the tree it finds; a dirty tree means it
#    commits someone else's work under this task's name, or dies at iteration 0.
step "clean worktree"
dirty="$(git status --porcelain | wc -l | tr -d ' ')"
if [ "$dirty" != "0" ]; then
  git status --short | head -10
  bad "$dirty uncommitted path(s). Commit, stash, or reset before launching."
else
  ok "worktree clean"
fi

# 3. Not stale. A clone 33 commits behind main re-solves solved problems and
#    conflicts on push. This is the single most common way a run wastes a night.
step "up to date with $BASE"
if git fetch "${BASE%%/*}" "${BASE#*/}" -q 2>/dev/null; then
  behind="$(git rev-list --count "HEAD..$BASE" 2>/dev/null || echo "?")"
  ahead="$(git rev-list --count "$BASE..HEAD" 2>/dev/null || echo "?")"
  if [ "$behind" = "?" ]; then
    bad "cannot compare against $BASE — is the remote/branch name right?"
  elif [ "$behind" -gt 0 ]; then
    bad "$behind commit(s) behind $BASE. Run: git reset --hard $BASE"
  else
    ok "level with $BASE"
  fi
  if [ "$ahead" != "?" ] && [ "$ahead" -gt 0 ]; then
    soft "$ahead unpushed commit(s) already here — harvest them before starting,
     or this run's history will be mixed with the last one's."
  fi
else
  bad "git fetch $BASE failed — a stale fetch is how a run silently hangs."
fi

# 4. The task file, at an absolute path. A remote autorun resolves the task
#    relative to ITS cwd, not yours; a relative path silently reads nothing.
step "task file"
if [ -z "$TASK" ]; then
  bad "--task is required"
elif [ "${TASK#/}" = "$TASK" ]; then
  bad "--task must be ABSOLUTE ($TASK). A remote run resolves it on the far side."
elif [ ! -f "$TASK" ]; then
  bad "task file not found: $TASK"
else
  ok "task: $TASK"
fi

# 5. The gate must PASS on the base tree, before the runner changes anything.
#    `gofmt -l desktop/agent` lists 283 of 1597 files on a clean main, so a gate
#    asserting it is red from the start: every iteration's commits are discarded
#    and the run ends "Verified commits kept: 0" having done real work.
step "gate is satisfiable on the base tree"
if [ -z "$GATE" ]; then
  bad "--gate is required — a loop with no oracle cannot converge"
else
  if bash -c "$GATE" >/tmp/autorun-preflight-gate.log 2>&1; then
    ok "gate passes on the untouched tree"
  else
    tail -15 /tmp/autorun-preflight-gate.log
    bad "gate FAILS before the runner has done anything. It cannot be satisfied
     by working, so every commit will be thrown away. Fix the gate first
     (full log: /tmp/autorun-preflight-gate.log)."
  fi
fi

# 6. Scope must cover what the task actually reaches for. This is what killed
#    four runs: each edited a file its scope did not list, and its work was
#    stashed and discarded. Cross-surface tasks (this repo's own rule) can never
#    fit inside desktop/agent/**.
step "scope covers the task's own surfaces"
if [ "${#SCOPES[@]}" -eq 0 ]; then
  soft "no --scope given: the run is unrestricted. Intentional for a broad task,
     dangerous for a narrow one."
else
  echo "scopes: ${SCOPES[*]}"
  if [ -n "$TASK" ] && [ -f "$TASK" ]; then
    for surface in desktop/agent mobile web backend/convex tvos watch relay cli sdk; do
      grep -q "$surface" "$TASK" 2>/dev/null || continue
      covered=0
      for s in "${SCOPES[@]}"; do case "$surface/" in ${s%%\**}*) covered=1 ;; esac; done
      [ "$covered" = "1" ] || soft "task mentions '$surface' but no --scope covers it.
     If the runner edits there, its work is stashed and thrown away."
    done
  fi
  ok "scope reviewed"
fi

# 7. tmux. yaver drives TUI runners through it; claude is forced to tmux. A
#    server whose cwd is a DELETED directory kills every runner it spawns with
#    an error that blames the runner ("Bun ENOENT", "os error 2").
step "tmux"
if ! command -v tmux >/dev/null 2>&1; then
  bad "tmux not on PATH. yaver REQUIRES it for TUI runners (try: export PATH=/opt/homebrew/bin:\$PATH)"
else
  ok "tmux present: $(tmux -V)"
  if tmux ls >/dev/null 2>&1; then
    srv_cwd="$(tmux display-message -p '#{pane_current_path}' 2>/dev/null || echo '')"
    if [ -n "$srv_cwd" ] && [ ! -d "$srv_cwd" ]; then
      bad "the running tmux SERVER's cwd no longer exists ($srv_cwd). Every runner
     it spawns dies with a misleading error. Fix: tmux kill-server"
    else
      ok "tmux server cwd is live"
    fi
  else
    ok "no tmux server yet (autorun will start one)"
  fi
fi

# 8. The runner must be authed HERE. Seats are binding: naming an unauthed
#    runner fails the run at iteration 1 rather than falling back.
step "runner auth"
if [ -z "$RUNNER" ]; then
  soft "--runner not given; autorun will pick one"
else
  case "$RUNNER" in
    claude|claude-code) bin=claude ;;
    codex) bin=codex ;;
    opencode) bin=opencode ;;
    *) bin="$RUNNER" ;;
  esac
  if command -v "$bin" >/dev/null 2>&1; then
    ok "$RUNNER binary present ($(command -v $bin))"
    echo "   NOTE: presence != authed. Seats are binding — an unauthed runner"
    echo "   fails at iteration 1. Confirm with: yaver runner auth status"
  else
    bad "$RUNNER not on PATH"
  fi
fi

# 9. Headroom. A run that backs off on load, or fills the disk mid-archive,
#    burns its interval budget doing nothing.
step "machine headroom"
avail_gb="$(df -g . 2>/dev/null | tail -1 | awk '{print $4}')"
if [ -n "$avail_gb" ] && [ "$avail_gb" -lt "$MIN_DISK_GB" ] 2>/dev/null; then
  bad "only ${avail_gb} GB free (want >= ${MIN_DISK_GB})."
else
  ok "disk: ${avail_gb:-?} GB free"
fi
cores="$(sysctl -n hw.ncpu 2>/dev/null || nproc 2>/dev/null || echo 1)"
load1="$(sysctl -n vm.loadavg 2>/dev/null | awk '{print $2}')"
[ -z "$load1" ] && load1="$(cut -d' ' -f1 /proc/loadavg 2>/dev/null || echo 0)"
per_core="$(awk -v l="$load1" -v c="$cores" 'BEGIN{printf "%.2f", (c>0? l/c : l)}')"
if awk -v p="$per_core" -v m="$MAX_LOAD_PER_CORE" 'BEGIN{exit !(p>m)}'; then
  soft "load ${per_core}/core exceeds ${MAX_LOAD_PER_CORE} — autorun will back off
     and burn intervals idling. Consider waiting or reducing parallel runs."
else
  ok "load: ${per_core}/core across ${cores} cores"
fi

echo
if [ "$fail" -ne 0 ]; then
  echo "PREFLIGHT: FAIL — not launching. Fix the above; each one has cost us a run."
  exit 1
fi
[ "$warn" -ne 0 ] && echo "PREFLIGHT: PASS (with warnings — read them)" || echo "PREFLIGHT: PASS"
exit 0
