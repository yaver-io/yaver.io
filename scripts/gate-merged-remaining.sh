#!/usr/bin/env bash
# Gate for tasks/merged-remaining.md
#
# NEVER add `go test ./...` here. TestAuthLogout in desktop/agent hits the real
# ~/.yaver and signs the box out mid-run.
#
# Two modes, and the difference matters:
#
#   (default)  gofmt + go build. This is what the AUTORUN LOOP runs.
#   check      the four objectives' end-state assertions. NOT for the loop.
#
# Why the loop does not run `check`: autorun keeps an iteration's commits only
# when the gate passes. A gate asserting all four objectives is red until the
# last one lands, so every intermediate commit would be thrown away — which is
# exactly how these four tasks died the first time (scope violation, "Verified
# commits kept: 0"). The loop needs a gate that a half-finished run can pass.
# `check` is for a human, or for the final iteration, to ask "is it actually
# done?" — the task file's "Done means", expressed as code assertions.
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"
MODE="${1:-build}"
fail=0
step()  { printf '\n=== %s ===\n' "$1"; }
check() { if [ "$1" -ne 0 ]; then echo "GATE FAIL: $2"; fail=1; else echo "ok: $2"; fi; }

# gofmt ONLY the files this run touched.
#
# `gofmt -l desktop/agent` — the gate all four of these tasks originally used —
# lists 283 of 1597 files on a clean main (struct-alignment drift, none of it
# this run's doing). As a gate that is unsatisfiable: it is red before the
# runner types anything, so every iteration's commits get discarded and the run
# ends with "Verified commits kept: 0". That is the same outcome the scope bug
# produced, from a different direction. Judge the run on what it wrote.
step "gofmt (files changed by this run)"
BASE="${GATE_BASE:-github/main}"
changed="$(
  {
    git diff --name-only "$BASE"...HEAD -- 'desktop/agent/*.go' 2>/dev/null
    git diff --name-only -- 'desktop/agent/*.go' 2>/dev/null
    git diff --cached --name-only -- 'desktop/agent/*.go' 2>/dev/null
    # Untracked too. `git diff` never lists a file that was never added, and
    # these objectives all CREATE Go files (autorun_digest, the review gate).
    # Without this the gate silently skips exactly the new code it exists to
    # judge — verified by probe: a deliberately misformatted new file passed.
    git ls-files --others --exclude-standard -- 'desktop/agent/*.go' 2>/dev/null
  } | sort -u
)"
touched=""
for f in $changed; do [ -f "$f" ] && touched="$touched $f"; done
if [ -z "${touched// /}" ]; then
  echo "no desktop/agent/*.go changed vs $BASE — nothing to format-check"
  check 0 "gofmt (no Go files touched)"
else
  # shellcheck disable=SC2086
  unformatted="$(gofmt -l $touched 2>/dev/null)"
  if [ -n "$unformatted" ]; then echo "unformatted (yours):"; echo "$unformatted"; fi
  [ -z "$unformatted" ]; check $? "files this run touched are gofmt-clean"
fi

step "go build desktop/agent"
( cd desktop/agent && go build ./... ) ; check $? "desktop/agent builds"

if [ "$MODE" = "check" ]; then
  # End-state assertions. Informational during the run; meaningful at the end.
  step "objective 1 — glm retired as a runner"
  ! grep -rqn '"glm"' --include='*.go' desktop/agent/runner*.go 2>/dev/null
  check $? "no \"glm\" runner id left in desktop/agent/runner*.go"

  step "objective 2 — autorun_digest exists"
  grep -rqn 'autorun_digest' --include='*.go' desktop/agent 2>/dev/null
  check $? "autorun_digest verb is registered"

  step "objective 3 — pipeline reachable off MCP-only"
  grep -rqn 'Name: *"pipeline' --include='*.go' desktop/agent 2>/dev/null
  check $? "a pipeline_* verb exists on the ops bus"

  step "objective 4 — code-review gate exists and is off by default"
  grep -rqn 'code_review\|codeReview' --include='*.go' desktop/agent 2>/dev/null
  check $? "a code-review gate symbol exists in desktop/agent"
fi

if [ "$fail" -ne 0 ]; then
  echo; echo "GATE: FAIL"; exit 1
fi
echo; echo "GATE: PASS"
