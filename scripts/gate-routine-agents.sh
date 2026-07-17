#!/usr/bin/env bash
# Gate for tasks/user-authored-routine-agents.md
#
# The autorun loop's ONLY oracle. /goal is a directive the runner can satisfy by
# editing a file; this cannot be satisfied by writing prose. Every check below
# asserts a property of the CODE, not of a doc.
#
# NEVER add `go test ./...` here. TestAuthLogout in desktop/agent hits the real
# ~/.yaver and signs the box out mid-run. Every Go test below is -run scoped on
# purpose.
#
# Usage: scripts/gate-routine-agents.sh [p0|p1|p2|p3|p4|p5|all]
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"
PHASE="${1:-all}"
fail=0
step() { printf '\n=== %s ===\n' "$1"; }
check() { if [ "$1" -ne 0 ]; then echo "GATE FAIL: $2"; fail=1; else echo "ok: $2"; fi; }
want()  { if [ "$PHASE" = "all" ] || [ "$PHASE" = "$1" ]; then return 0; fi; return 1; }

# absent <pattern> <glob-dir> — succeeds when the pattern is GONE
absent() { ! grep -rqn "$1" --include='*.go' "$2" 2>/dev/null; }

# gotest <run-pattern> — REQUIRES that at least one test actually ran.
#
# `go test -run 'TestNoSuchThing'` exits 0. A gate built on a bare `-run` is
# green before a single line is written, which is the exact failure this task
# exists to delete. So: -v, and demand a matching `=== RUN` line.
gotest() {
  local out
  out="$(cd desktop/agent && go test -count=1 -v -run "$1" . 2>&1)" || return 1
  printf '%s' "$out" | grep -q '^=== RUN' || { echo "  (no test matched /$1/)"; return 1; }
  return 0
}

step "go build (agent) — always"
(cd desktop/agent && go build ./...) ; check $? "desktop/agent builds"

# ---------------------------------------------------------------- P0
if want p0; then
  step "P0.1 guest cannot reach a subscription runner via /tasks"
  # The fail-open default must be gone: CheckRunner must not return nil for an
  # empty AllowedRunners without consulting isSubscriptionRunner.
  gotest 'TestGuestRunner|TestCheckRunner|TestIsSubscriptionRunner|TestGuestTasksRunnerGuard' ; check $? "guest subscription-runner guard tests"
  grep -qn 'isSubscriptionRunner' desktop/agent/guest_config.go 2>/dev/null \
    || grep -qn 'isSubscriptionRunner' desktop/agent/httpserver.go 2>/dev/null
  check $? "isSubscriptionRunner consulted on the /tasks path"

  step "P0.2 guest lane's model is glm-5.2, not the silently-no-op'ing 4.7"
  # Scoped to the opencode-provider axis. tasks.go's `Model: "glm-4.7"` belongs
  # to builtinRunners["glm"], which tasks/glm-remove-runner.md deletes — not
  # ours to touch, so it is deliberately NOT asserted here.
  absent 'zai-coding-plan/glm-4\.7' desktop/agent
  check $? "no zai-coding-plan/glm-4.7 pin remains"
  absent 'openrouter/z-ai/glm-4\.7' desktop/agent
  check $? "no openrouter/z-ai/glm-4.7 pin remains"
fi

# ---------------------------------------------------------------- P1
if want p1; then
  step "P1.1 infer + notify ops verbs exist and are registered"
  gotest 'TestOpsInfer|TestOpsNotify|TestRoutineInfer|TestRoutineNotify' ; check $? "infer/notify ops verb tests"

  step "P1.2 RunGLMLoop is no longer orphaned"
  # It must have a caller outside its own test file.
  n=$(grep -rn 'RunGLMLoop' --include='*.go' desktop/agent \
      | grep -v '_test.go' | grep -v 'func RunGLMLoop' | wc -l | tr -d ' ')
  [ "$n" -ge 1 ] ; check $? "RunGLMLoop has >=1 production caller (found $n)"

  step "P1.3 yaverAgentConfig is read by an inference path"
  n=$(grep -rn 'loadYaverAgentConfig' --include='*.go' desktop/agent \
      | grep -v '_test.go' | grep -v 'func loadYaverAgentConfig' | wc -l | tr -d ' ')
  [ "$n" -ge 2 ] ; check $? "loadYaverAgentConfig has a caller beyond the GET handler (found $n)"

  step "P1.4 scheduler does not silently lose routines"
  gotest 'TestScheduler.*Corrupt|TestSchedulerLoad|TestSchedulerSave|TestSchedulerPersist' ; check $? "scheduler corrupt-store fails loudly"

  step "P1.5 notify sends exactly one message"
  gotest 'TestNotifySendsOnce|TestNotify'
  check $? "notify double-send fixed"
fi

# ---------------------------------------------------------------- P2
if want p2; then
  step "P2.1 guest routines are per-guest scoped"
  gotest 'TestGuestRoutine|TestScheduleGuestScope|TestRoutineGuestIsolation' ; check $? "guest routine isolation tests"

  step "P2.2 guest scope default denies"
  gotest 'TestGuestScopeDefault|TestGuestScopeOrDefault'
  check $? "unknown/empty guest scope no longer defaults to full"

  step "P2.3 tmux session names carry a user dimension"
  gotest 'TestTmuxSessionName|TestAutorunTmuxSession'
  check $? "tmux session naming is user-scoped"

  step "P2.4 convex privacy contract still holds for the new rows"
  gotest 'TestConvexPrivacy|TestFieldsWeForbid'
  check $? "convex privacy test"
fi

# ---------------------------------------------------------------- P3
if want p3; then
  step "P3.1 routines are visible to the UIs"
  grep -qn 'verb\|routine\|opsPayload' mobile/app/schedules.tsx
  check $? "mobile schedules screen knows about routines"
  grep -rqn 'routine' web/app/dashboard/ 2>/dev/null
  check $? "web dashboard knows about routines"

  step "P3.2 quota breaker before a deploy-shaped routine"
  n=$(grep -rn 'testflight_builds\|TestFlightBuilds\|testflightBuilds' --include='*.go' desktop/agent \
      | grep -v '_test.go' | grep -v 'case "testflight_builds"' | wc -l | tr -d ' ')
  [ "$n" -ge 1 ] ; check $? "testflight budget is read by something (found $n)"

  step "P3.3 typechecks"
  (cd mobile && npx tsc --noEmit) ; check $? "mobile tsc"
  (cd web && npx tsc --noEmit) ; check $? "web tsc"
fi

# ---------------------------------------------------------------- P4
if want p4; then
  step "P4.1 connector authoring is generic, not health-specific"
  absent 'health_connector_template' desktop/agent
  check $? "health_connector_template generalized away"

  step "P4.2 OTP reaches a browser session"
  n=$(grep -rn 'InjectKeys\|Prefill' --include='*.go' desktop/agent \
      | grep -i 'otp\|gate' | wc -l | tr -d ' ')
  [ "$n" -ge 1 ] ; check $? "gateway OTP wired to browser injection (found $n)"

  step "P4.3 browser profiles namespaced per user"
  gotest 'TestProfileDir|TestBrowserProfileNamespace'
  check $? "browser profile namespacing"
fi

# ---------------------------------------------------------------- P5
if want p5; then
  step "P5.1 morning_* is gone or real"
  if grep -qn 'morning_latest' desktop/agent/mcp_tools.go 2>/dev/null; then
    grep -rqn 'case "morning_latest"' --include='*.go' desktop/agent
    check $? "morning_latest is declared AND handled"
  else
    echo "ok: morning_* declarations removed"
  fi

  step "P5.2 no advertised MCP tool dispatches to droppedMCPStub"
  gotest 'TestNoDroppedToolsAdvertised|TestMCPToolsHaveHandlers'
  check $? "advertised tool surface has no removed-feature stubs"

  step "P5.3 browser sets no UA it isn't, and backs off on blocks"
  absent 'Windows NT 10.0' desktop/agent ; check $? "no Windows UA spoof"
  absent 'AutomationControlled' desktop/agent ; check $? "no anti-detect flag"
  n=$(grep -rn '403\|429\|451' --include='*.go' desktop/agent/browser.go | wc -l | tr -d ' ')
  [ "$n" -ge 1 ] ; check $? "browser backs off on 403/429/451 (found $n)"

  step "P5.4 redroid driver does not fake a snapshot"
  gotest 'TestRedroidSnapshot|TestRedroidTap'
  check $? "redroid Snapshot/Tap honesty tests"

  step "P5.5 dead risk-tier footgun removed"
  absent 'gatewayRiskTier(cap.Verb)' desktop/agent
  check $? "gateway_act dead tier line removed"
fi

step "RESULT"
if [ "$fail" -ne 0 ]; then echo "GATE RED (phase=$PHASE)"; exit 1; fi
echo "GATE GREEN (phase=$PHASE)"
