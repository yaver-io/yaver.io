package main

// hybrid_test.go — unit + integration tests for the planner/implementer
// orchestrator. The integration test uses shell script stand-ins for
// the planner and implementer so the suite is hermetic — no API calls,
// no external runner dependency, and fast enough to run in CI on
// every push.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// TestParseHybridPlan_HappyPath verifies the parser accepts a clean
// {"subtasks":[…]} object.
func TestParseHybridPlan_HappyPath(t *testing.T) {
	raw := `Here is the plan.

{"subtasks":[
  {"title":"Add schema","files":["db/schema.ts"],"prompt":"Create a Portfolio type."},
  {"title":"Wire mutation","files":["convex/portfolio.ts"],"prompt":"Add createPortfolio."}
]}
`
	got, err := parseHybridPlan(raw, 20)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 subtasks, got %d", len(got))
	}
	if got[0].Title != "Add schema" || len(got[0].Files) != 1 || got[0].Files[0] != "db/schema.ts" {
		t.Errorf("subtask[0] mismatch: %+v", got[0])
	}
}

// TestParseHybridPlan_WithFence strips ```json code fences — which the
// Claude Code planner frequently adds around its JSON output.
func TestParseHybridPlan_WithFence(t *testing.T) {
	raw := "Preamble\n```json\n{\"subtasks\":[{\"title\":\"T\",\"files\":[\"a.ts\"],\"prompt\":\"P\"}]}\n```\n"
	got, err := parseHybridPlan(raw, 20)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(got) != 1 || got[0].Title != "T" {
		t.Fatalf("unexpected: %+v", got)
	}
}

// TestParseHybridPlan_CapRespectsMax guards against a runaway planner
// that tries to emit 100 subtasks: the parser must truncate at max.
func TestParseHybridPlan_CapRespectsMax(t *testing.T) {
	var subs []HybridSubtask
	for i := 0; i < 30; i++ {
		subs = append(subs, HybridSubtask{Title: "x", Files: []string{"x"}, Prompt: "x"})
	}
	blob, _ := json.Marshal(map[string]any{"subtasks": subs})
	got, err := parseHybridPlan(string(blob), 5)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(got) != 5 {
		t.Fatalf("want 5, got %d", len(got))
	}
}

// TestParseHybridPlan_NoJSON returns an error when the planner forgot
// to emit a JSON block. We want this to be a hard failure, not a silent
// empty list, because a silent empty list would make the orchestrator
// report "ok" on zero work.
func TestParseHybridPlan_NoJSON(t *testing.T) {
	if _, err := parseHybridPlan("I think we should…", 10); err == nil {
		t.Fatal("expected error for prose-only planner output")
	}
}

// TestApplyHybridDefaults_Requires ensures the orchestrator refuses to
// run without a workDir or a prompt.
func TestApplyHybridDefaults_Requires(t *testing.T) {
	if err := applyHybridDefaults(&HybridSpec{Prompt: "x"}); err == nil {
		t.Fatal("expected workDir requirement")
	}
	if err := applyHybridDefaults(&HybridSpec{WorkDir: t.TempDir()}); err == nil {
		t.Fatal("expected prompt requirement")
	}
}

// TestApplyHybridDefaults_Fills verifies the defaults: claude planner,
// opencode implementer.
func TestApplyHybridDefaults_Fills(t *testing.T) {
	spec := HybridSpec{WorkDir: t.TempDir(), Prompt: "do a thing"}
	if err := applyHybridDefaults(&spec); err != nil {
		t.Fatalf("defaults: %v", err)
	}
	if spec.Planner != "claude" {
		t.Errorf("planner default: %q", spec.Planner)
	}
	if spec.Implementer != "opencode" {
		t.Errorf("implementer default: %q (want opencode)", spec.Implementer)
	}
	if spec.MaxSubtasks == 0 || spec.Timeout == 0 {
		t.Errorf("caps not filled: %+v", spec)
	}
}

// TestApplyHybridDefaults_RejectsUnsupported makes sure an
// unsupported runner id is bounced with a clear error rather than
// silently running an unknown binary.
func TestApplyHybridDefaults_RejectsUnsupported(t *testing.T) {
	spec := HybridSpec{WorkDir: t.TempDir(), Prompt: "do a thing", Implementer: "aider"}
	err := applyHybridDefaults(&spec)
	if err == nil {
		t.Fatal("expected error for unsupported implementer")
	}
	if !strings.Contains(err.Error(), "claude, codex, or opencode") {
		t.Errorf("error should name the supported runners: %v", err)
	}
}

// TestPlannerPrompt_Contract locks in the prompt's core contract:
// the planner is told the implementer is less capable, must emit
// hyper-explicit subtasks, and must stick to JSON. If someone edits
// plannerPrompt and drops these guardrails, the implementer will get
// underspecified tasks and fail silently.
func TestPlannerPrompt_Contract(t *testing.T) {
	p := plannerPrompt("/tmp/x", "build thing", "opencode", "anthropic/claude-sonnet-4-6", 10)
	must := []string{
		"TWO-AGENT",
		"WILL hallucinate",
		"hyper-explicit",
		"ONE file per subtask",
		"DO NOT leave naming to the implementer",
		"acceptance criterion",
		"subtasks",
	}
	for _, m := range must {
		if !strings.Contains(p, m) {
			t.Errorf("planner prompt missing required phrase %q", m)
		}
	}
}

// TestRunHybrid_FakePlannerAndImplementer is the integration test.
// We stub the `claude` and `opencode` commands with tiny shell scripts
// in a temp dir that's prepended to PATH. The fake planner writes a
// 2-subtask plan to stdout; the fake implementer touches the files
// named in its prompt so the test can verify wiring (prompt forwarding,
// file scope inclusion, exit code respected).
//
// Skipped on Windows — the stubs are bash scripts.
func TestRunHybrid_FakePlannerAndImplementer(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("bash stubs not portable to Windows")
	}

	stubDir := t.TempDir()
	workDir := t.TempDir()

	plannerOut := `{"subtasks":[
  {"title":"one","files":["a.txt"],"prompt":"create a"},
  {"title":"two","files":["b.txt"],"prompt":"create b"}
]}`
	writeStub(t, filepath.Join(stubDir, "claude"), `#!/bin/bash
cat <<'EOF'
`+plannerOut+`
EOF
`)
	// The implementer stub records its argv + the prompt it received,
	// then touches every file mentioned in the "Files in scope:" prefix
	// the orchestrator builds. That preface is how runImplementer
	// communicates the planner's file scope to the runner.
	writeStub(t, filepath.Join(stubDir, "opencode"), `#!/bin/bash
echo "ARGV: $@"
# Last positional arg holds the prompt (yaver builds opencode argv as
# `+"`run --dangerously-skip-permissions <prompt>`"+`).
prompt="${@: -1}"
# Pull every "  - <path>" line from the Files-in-scope preface.
echo "$prompt" | awk '/^  - /{print $2}' | while read -r f; do
  [ -n "$f" ] && touch -- "$(pwd)/$f"
done
`)

	oldPath := os.Getenv("PATH")
	t.Setenv("PATH", stubDir+":"+oldPath)

	spec := HybridSpec{
		Planner:     "claude",
		Implementer: "opencode",
		WorkDir:     workDir,
		Prompt:      "make two files",
		Timeout:     30 * time.Second,
	}

	rep, err := RunHybrid(context.Background(), spec)
	if err != nil {
		t.Fatalf("RunHybrid: %v (plan output: %s)", err, rep.PlanOutput)
	}
	if len(rep.Subtasks) != 2 {
		t.Fatalf("subtasks: want 2 got %d", len(rep.Subtasks))
	}
	if rep.FailedSteps != 0 || !rep.OK {
		for _, r := range rep.Results {
			t.Logf("step status=%s err=%s out=%s", r.Status, r.Error, r.Output)
		}
		t.Fatalf("want clean run, failed=%d ok=%v", rep.FailedSteps, rep.OK)
	}
	// The implementer stub touched the scoped files; verify the
	// file-scope plumbing (planner→orchestrator→prompt preface→
	// implementer) actually works.
	for _, f := range []string{"a.txt", "b.txt"} {
		if _, err := os.Stat(filepath.Join(workDir, f)); err != nil {
			t.Errorf("expected implementer to create %s: %v", f, err)
		}
	}
}

// TestRunHybrid_PlannerEmitsGarbage covers the failure path: planner
// returns non-JSON. Orchestrator must return an error with the planner
// output preserved on the report so the caller can debug.
func TestRunHybrid_PlannerEmitsGarbage(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("bash stubs not portable to Windows")
	}
	stubDir := t.TempDir()
	workDir := t.TempDir()
	writeStub(t, filepath.Join(stubDir, "claude"), `#!/bin/bash
echo "hello world, no JSON here"
`)
	writeStub(t, filepath.Join(stubDir, "opencode"), `#!/bin/bash
exit 0
`)
	t.Setenv("PATH", stubDir+":"+os.Getenv("PATH"))

	rep, err := RunHybrid(context.Background(), HybridSpec{
		Planner: "claude", Implementer: "opencode",
		WorkDir: workDir, Prompt: "x", Timeout: 10 * time.Second,
	})
	if err == nil {
		t.Fatal("expected error when planner emits no JSON")
	}
	if rep == nil || rep.PlanOutput == "" {
		t.Fatal("expected planner output preserved on report for debugging")
	}
}

// writeStub writes an executable bash script at path with the given
// body. Helper for the PATH-shadowing tests above.
func writeStub(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatalf("write stub %s: %v", path, err)
	}
}

// TestRunHybrid_RetryOnTransientFailure verifies that a subtask whose
// first attempt fails gets retried with the corrective reminder, and
// that the retry counter + attempt-level prompt prefix are observed
// by the implementer. The stub implementer fails on attempt 0 and
// succeeds on attempt 1.
func TestRunHybrid_RetryOnTransientFailure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("bash stubs not portable")
	}
	stubDir := t.TempDir()
	workDir := t.TempDir()

	writeStub(t, filepath.Join(stubDir, "claude"), `#!/usr/bin/env bash
cat <<'EOF'
{"subtasks":[{"title":"flaky","files":["f.txt"],"prompt":"write something"}]}
EOF
`)
	// Stub implementer counts invocations via a marker file. Fails
	// on first call, succeeds on second — the retry path must kick.
	writeStub(t, filepath.Join(stubDir, "opencode"), `#!/usr/bin/env bash
COUNT_FILE="`+workDir+`/opencode_invocations"
n=$(cat "$COUNT_FILE" 2>/dev/null || echo 0)
n=$((n+1))
echo $n > "$COUNT_FILE"
if [ "$n" -eq 1 ]; then
  echo "first attempt fails on purpose" >&2
  exit 1
fi
touch "$(dirname "$COUNT_FILE")/f.txt"
echo "ok"
`)
	t.Setenv("PATH", stubDir+":"+os.Getenv("PATH"))

	rep, err := RunHybrid(context.Background(), HybridSpec{
		Planner: "claude", Implementer: "opencode",
		WorkDir: workDir, Prompt: "do it",
		MaxRetries: 1, Timeout: 30 * time.Second,
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !rep.OK {
		t.Fatalf("expected OK after retry, got failed=%d", rep.FailedSteps)
	}
	if rep.Retries != 1 {
		t.Errorf("want Retries=1, got %d", rep.Retries)
	}
	invocations, _ := os.ReadFile(filepath.Join(workDir, "opencode_invocations"))
	if strings.TrimSpace(string(invocations)) != "2" {
		t.Errorf("want implementer called twice, counter says %q", invocations)
	}
}

// TestRunHybrid_ReplanKicksIn fires the planner once, makes every
// subtask fail, and verifies the orchestrator asks the planner for a
// replacement plan after MaxConsecutiveFailures. The second plan
// contains a single-subtask that succeeds; the final report should
// have Replanned=true.
func TestRunHybrid_ReplanKicksIn(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("bash stubs not portable")
	}
	stubDir := t.TempDir()
	workDir := t.TempDir()

	// Planner stub: returns 2 bad subtasks on first call, 1 good
	// subtask on the replan. "Good" means the subtask title signals
	// the implementer stub to succeed.
	writeStub(t, filepath.Join(stubDir, "claude"), `#!/usr/bin/env bash
COUNT_FILE="`+workDir+`/plan_invocations"
n=$(cat "$COUNT_FILE" 2>/dev/null || echo 0)
n=$((n+1))
echo $n > "$COUNT_FILE"
if [ "$n" -eq 1 ]; then
cat <<'EOF'
{"subtasks":[
  {"title":"bad-1","files":["a.txt"],"prompt":"fail"},
  {"title":"bad-2","files":["b.txt"],"prompt":"fail"}
]}
EOF
else
cat <<'EOF'
{"subtasks":[{"title":"good","files":["c.txt"],"prompt":"ok"}]}
EOF
fi
`)
	// Implementer stub: fails when the prompt contains "fail",
	// succeeds otherwise. Last positional arg holds the full prompt.
	writeStub(t, filepath.Join(stubDir, "opencode"), `#!/usr/bin/env bash
prompt="${@: -1}"
case "$prompt" in
  *fail*) exit 1 ;;
  *) touch "$(pwd)/c.txt"; exit 0 ;;
esac
`)
	t.Setenv("PATH", stubDir+":"+os.Getenv("PATH"))

	rep, err := RunHybrid(context.Background(), HybridSpec{
		Planner: "claude", Implementer: "opencode",
		WorkDir: workDir, Prompt: "x",
		MaxRetries:             0, // no per-step retry so replan threshold triggers cleanly
		MaxConsecutiveFailures: 2,
		Timeout:                30 * time.Second,
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !rep.Replanned {
		t.Fatalf("expected Replanned=true, got report: %+v", rep)
	}
	// 2 originals failed + 1 replacement succeeded = 3 results total.
	if len(rep.Results) != 3 {
		t.Fatalf("want 3 results, got %d", len(rep.Results))
	}
	if rep.Results[2].Status != "ok" {
		t.Errorf("want replan subtask to succeed, got %+v", rep.Results[2])
	}
	planCalls, _ := os.ReadFile(filepath.Join(workDir, "plan_invocations"))
	if strings.TrimSpace(string(planCalls)) != "2" {
		t.Errorf("want planner called twice (initial + replan), got %q", planCalls)
	}
}

// TestRunHybridWithProgress_EmitsEvents checks that the progress
// callback fires with the expected event sequence for a trivial run.
// This is the contract the SSE handler and both client UIs depend on.
func TestRunHybridWithProgress_EmitsEvents(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("bash stubs not portable")
	}
	stubDir := t.TempDir()
	workDir := t.TempDir()
	writeStub(t, filepath.Join(stubDir, "claude"), `#!/usr/bin/env bash
cat <<'EOF'
{"subtasks":[{"title":"s","files":["x"],"prompt":"p"}]}
EOF
`)
	writeStub(t, filepath.Join(stubDir, "opencode"), `#!/usr/bin/env bash
exit 0
`)
	t.Setenv("PATH", stubDir+":"+os.Getenv("PATH"))

	var types []string
	_, err := RunHybridWithProgress(context.Background(), HybridSpec{
		Planner: "claude", Implementer: "opencode",
		WorkDir: workDir, Prompt: "x",
		Timeout: 30 * time.Second,
	}, func(ev HybridEvent) {
		types = append(types, ev.Type)
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	// Minimum viable sequence: plan_started → plan_done → subtask_started
	// → subtask_done → run_done. Extra events (replan, retries) only
	// fire on the failing paths tested above.
	want := []string{"plan_started", "plan_done", "subtask_started", "subtask_done", "run_done"}
	joined := strings.Join(types, ",")
	for _, w := range want {
		if !strings.Contains(joined, w) {
			t.Errorf("missing event %q in %s", w, joined)
		}
	}
}

// TestHybridPreflight_ReportsMissing verifies the preflight returns a
// helpful hint when the planner or implementer binary is missing.
func TestHybridPreflight_ReportsMissing(t *testing.T) {
	// Empty PATH guarantees neither claude nor opencode can be found.
	t.Setenv("PATH", "")
	pf := checkHybrid("claude", "opencode")
	if pf.PlannerOK || pf.ImplementerOK {
		t.Errorf("expected both probes to fail on empty PATH, got %+v", pf)
	}
	if pf.Hint == "" {
		t.Errorf("expected a hint, got empty")
	}
}
