#!/usr/bin/env bash
#
# run-client-guards.sh — run EVERY deterministic client-side guard.
#
# WHY THIS EXISTS (2026-08-02)
#
#   `.github/workflows/ci.yml` gated client tests by NAMING them, four at a
#   time. Meanwhile 63 `*.test.ts` files existed under mobile/src/lib and
#   web/lib. So 59 guards — including the connectivity ones the whole
#   CONNECTIVITY ROBUSTNESS rule rests on (unroutableCache, probeTargets,
#   probeWithRepair, deviceStatus, deviceListEquality, autoConnectStatus,
#   platformTransport, directProbeFailure, sseClient, reconnectLadder,
#   relayDeny, agentAuthError, runtimeTargetProbeFailure) — had never run in
#   CI. One of them was RED on main and nobody knew: the mobile/web
#   device-code approvers had drifted apart on rate-limit copy.
#
#   A hand-maintained list of tests fails exactly the way a hand-maintained
#   list of models failed in the same session: the list does not get the memo.
#   So this GLOBS. Add a `*.test.ts` next to the code it guards and it is
#   gated on the next push — no CI edit, nothing to forget.
#
# WHY A SCRIPT AND NOT INLINE YAML
#
#   So the command CI runs is the command you run locally. A guard that only
#   the robot knows how to invoke gets skipped by humans, and a guard humans
#   skip is a guard nobody has watched fail.
#
# CONTRACT
#   - exits non-zero if ANY guard fails
#   - prints one line per file, then a summary
#   - needs no credentials and no network: every guard here is a pure
#     primitive. If you add a test that needs either, it does NOT belong in
#     this sweep — put it in e2e/ with the other live-oracle loops.
#
# Usage:  scripts/run-client-guards.sh [mobile|web]

set -uo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT"

ONLY="${1:-all}"
PASS=0
FAIL=0
SKIPPED=0
FAILED_FILES=()

# A few guards are written against `bun:test`, not node's runner. tsx cannot
# load them (ERR_UNSUPPORTED_ESM_URL_SCHEME on the `bun:` scheme), and silently
# skipping them would be the exact false green this sweep exists to prevent.
# Route them to bun when it is installed; SAY SO loudly when it is not.
runner_for() {
  local file="$1"
  if grep -q '"bun:test"' "$file" 2>/dev/null; then echo bun; else echo tsx; fi
}

run_one() {
  local file="$1" ; shift
  local label="${file#"$REPO_ROOT"/}"
  if out="$("$@" 2>&1)"; then
    printf 'ok   %s\n' "$label"
    PASS=$((PASS + 1))
  else
    printf 'FAIL %s\n' "$label"
    printf '%s\n' "$out" | tail -20 | sed 's/^/       /'
    FAIL=$((FAIL + 1))
    FAILED_FILES+=("$label")
  fi
}

# ── mobile: plain tsx, these are dependency-free primitives ────────────────
if [[ "$ONLY" == "all" || "$ONLY" == "mobile" ]]; then
  echo "── mobile native source integrity ──"
  # mobile/ios is mostly generated and therefore ignored, while selected native
  # overlays are force-tracked. A referenced-but-untracked overlay can exist in a
  # developer checkout yet disappear on a clean release runner. Guard both the
  # Xcode reference and Git index so local state cannot create a false green.
  for native_source in \
    mobile/ios/Yaver/YaverMouthCropper.swift \
    mobile/ios/Yaver/YaverMouthCropper.m; do
    if [[ ! -f "$native_source" ]] || ! git ls-files --error-unmatch "$native_source" >/dev/null 2>&1; then
      printf 'FAIL %s — Xcode native overlay must exist and be tracked by Git\n' "$native_source"
      FAIL=$((FAIL + 1))
      FAILED_FILES+=("$native_source")
    elif ! grep -Fq "$(basename "$native_source")" mobile/ios/Yaver.xcodeproj/project.pbxproj; then
      printf 'FAIL %s — missing from Xcode project\n' "$native_source"
      FAIL=$((FAIL + 1))
      FAILED_FILES+=("$native_source")
    else
      printf 'ok   %s\n' "$native_source"
      PASS=$((PASS + 1))
    fi
  done

  echo "── mobile/src/lib guards ──"
  while IFS= read -r f; do
    [[ -n "$f" ]] || continue
    if [[ "$(runner_for "$f")" == "bun" ]]; then
      if command -v bun >/dev/null 2>&1; then
        run_one "$f" bun test "$f"
      else
        printf 'SKIP %s — needs `bun` (bun:test), which is not installed\n' "${f#"$REPO_ROOT"/}"
        SKIPPED=$((SKIPPED + 1))
      fi
      continue
    fi
    run_one "$f" npx tsx "$f"
  done < <(find mobile/src/lib \( -name '*.test.ts' -o -name '*.test.mts' \) | sort)
fi

# ── web: run FROM web/ with its tsconfig so `@/lib/...` path aliases resolve.
#    Without this, five guards (agent-client, device-lifecycle,
#    pending-cloud-dispatch, task-placement-request, use-auth) die with
#    MODULE_NOT_FOUND — they were effectively unrunnable as documented, which
#    is the same as not existing.
if [[ "$ONLY" == "all" || "$ONLY" == "web" ]]; then
  echo "── web/lib guards ──"
  while IFS= read -r f; do
    [[ -n "$f" ]] || continue
    rel="${f#web/}"
    run_one "$f" env -C "$REPO_ROOT/web" npx tsx --tsconfig tsconfig.json "$rel"
  done < <(find web/lib -name '*.test.ts' | sort)
fi

echo
if (( FAIL > 0 )); then
  echo "client guards: ${PASS} passed, ${FAIL} FAILED, ${SKIPPED} skipped"
  printf '  - %s\n' "${FAILED_FILES[@]}"
  exit 1
fi
echo "client guards: ${PASS} passed, 0 failed, ${SKIPPED} skipped"
