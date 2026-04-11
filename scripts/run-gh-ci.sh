#!/usr/bin/env bash
#
# run-gh-ci.sh — Trigger GitHub Actions workflows on the current branch, wait
# for them to finish, and dump failing-step logs inline.
#
# Usage:
#   ./scripts/run-gh-ci.sh                 # run every workflow_dispatch-enabled workflow on the current branch
#   ./scripts/run-gh-ci.sh e2e             # run just .github/workflows/e2e.yml
#   ./scripts/run-gh-ci.sh ci test-suite   # run multiple by name (matches file stem)
#   ./scripts/run-gh-ci.sh --list          # list dispatchable workflows
#
# Requires: gh (GitHub CLI) authenticated for the repo. Workflows must include
# `on: workflow_dispatch:` to be triggerable manually.
#
# Intended as the single entry point when the user says "run tests"/"run CI".
# Called from CLAUDE.md.

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT"

if ! command -v gh >/dev/null 2>&1; then
  echo "error: gh (GitHub CLI) not found. Install from https://cli.github.com/" >&2
  exit 1
fi

if ! gh auth status >/dev/null 2>&1; then
  echo "error: not authenticated with gh. Run: gh auth login" >&2
  exit 1
fi

BRANCH="$(git rev-parse --abbrev-ref HEAD)"
if [[ "$BRANCH" == "HEAD" ]]; then
  echo "error: detached HEAD — check out a branch first." >&2
  exit 1
fi

# Resolve the GitHub repo slug. Prefer the `github` remote if present,
# otherwise let gh guess from the current directory.
REPO_SLUG=""
if git remote | grep -qx github; then
  REPO_SLUG="$(git remote get-url github \
    | sed -E 's#^.*github\.com[:/]##; s#\.git$##')"
else
  REPO_SLUG="$(gh repo view --json nameWithOwner -q .nameWithOwner 2>/dev/null || true)"
fi
if [[ -z "$REPO_SLUG" ]]; then
  echo "error: could not determine GitHub repo slug. Set a 'github' remote." >&2
  exit 1
fi

echo "=> repo: $REPO_SLUG"
echo "=> branch: $BRANCH"

# Build the list of dispatchable workflows: those whose YAML contains `workflow_dispatch`.
# Avoid `mapfile` so this runs on macOS bash 3.
ALL_WORKFLOWS=()
for f in .github/workflows/*.yml .github/workflows/*.yaml; do
  [[ -f "$f" ]] || continue
  grep -q "workflow_dispatch" "$f" || continue
  name="$(basename "$f")"
  name="${name%.yml}"
  name="${name%.yaml}"
  ALL_WORKFLOWS+=("$name")
done

if [[ "${1:-}" == "--list" ]]; then
  echo "Dispatchable workflows on $BRANCH:"
  for w in "${ALL_WORKFLOWS[@]}"; do echo "  - $w"; done
  exit 0
fi

# Figure out which workflows to run.
declare -a TARGETS=()
if [[ $# -gt 0 ]]; then
  for arg in "$@"; do
    match=""
    for w in "${ALL_WORKFLOWS[@]}"; do
      if [[ "$w" == "$arg" ]]; then match="$w"; break; fi
    done
    if [[ -z "$match" ]]; then
      echo "error: workflow '$arg' is not dispatchable (missing workflow_dispatch?) or does not exist." >&2
      echo "  available: ${ALL_WORKFLOWS[*]}" >&2
      exit 1
    fi
    TARGETS+=("$match")
  done
else
  TARGETS=("${ALL_WORKFLOWS[@]}")
fi

if [[ ${#TARGETS[@]} -eq 0 ]]; then
  echo "error: no dispatchable workflows found under .github/workflows." >&2
  echo "Add 'on: workflow_dispatch:' to any workflow you want to run manually." >&2
  exit 1
fi

echo "=> workflows to run: ${TARGETS[*]}"
echo

# Trigger each workflow and record its run id. We keep parallel arrays so
# this runs on macOS bash 3 (no associative arrays).
RUN_NAMES=()
RUN_IDS=()
for w in "${TARGETS[@]}"; do
  echo "-- dispatching: $w"
  before_ids=$(gh run list --repo "$REPO_SLUG" --workflow "$w.yml" --branch "$BRANCH" --limit 20 --json databaseId -q '[.[].databaseId] | join(",")' || echo "")
  gh workflow run "$w.yml" --repo "$REPO_SLUG" --ref "$BRANCH" >/dev/null

  # Poll for the new run (up to 30s) — dispatch → run creation has lag.
  new_id=""
  for _ in 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15 16 17 18 19 20 21 22 23 24 25 26 27 28 29 30; do
    sleep 1
    after_ids=$(gh run list --repo "$REPO_SLUG" --workflow "$w.yml" --branch "$BRANCH" --limit 20 --json databaseId -q '[.[].databaseId] | join(",")' || echo "")
    # First id in the new set that isn't in the old set is our run.
    for id in ${after_ids//,/ }; do
      if [[ ",$before_ids," != *",$id,"* ]]; then
        new_id="$id"
        break
      fi
    done
    [[ -n "$new_id" ]] && break
  done
  if [[ -z "$new_id" ]]; then
    echo "   ! could not locate new run for $w (maybe dispatch was rate-limited)"
    continue
  fi
  RUN_NAMES+=("$w")
  RUN_IDS+=("$new_id")
  echo "   run: https://github.com/$REPO_SLUG/actions/runs/$new_id"
done

if [[ ${#RUN_IDS[@]} -eq 0 ]]; then
  echo "error: no runs were created." >&2
  exit 1
fi

echo
echo "=> waiting for runs to finish..."

exit_code=0
i=0
while [[ $i -lt ${#RUN_IDS[@]} ]]; do
  w="${RUN_NAMES[$i]}"
  id="${RUN_IDS[$i]}"
  echo
  echo "── $w (run $id) ──────────────────────────────────────────────"
  # gh run watch blocks until completion and exits non-zero on failure.
  if gh run watch "$id" --repo "$REPO_SLUG" --exit-status --interval 10; then
    echo "✓ $w passed"
  else
    exit_code=1
    echo "✗ $w failed — dumping failing step logs:"
    gh run view "$id" --repo "$REPO_SLUG" --log-failed || true
  fi
  i=$((i + 1))
done

exit "$exit_code"
