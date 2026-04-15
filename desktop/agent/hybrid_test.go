package main

// hybrid_test.go — unit + integration tests for the planner/implementer
// orchestrator. The integration test uses shell script stand-ins for
// the planner and implementer so the suite is hermetic — no API calls,
// no Ollama dependency, and fast enough to run in CI on every push.

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

// TestApplyHybridDefaults_Fills verifies the defaults land on a 24 GB
// M-series setup: claude planner, aider-ollama implementer with Qwen.
func TestApplyHybridDefaults_Fills(t *testing.T) {
	spec := HybridSpec{WorkDir: t.TempDir(), Prompt: "do a thing"}
	if err := applyHybridDefaults(&spec); err != nil {
		t.Fatalf("defaults: %v", err)
	}
	if spec.Planner != "claude" {
		t.Errorf("planner default: %q", spec.Planner)
	}
	if spec.Implementer != "aider-ollama" {
		t.Errorf("implementer default: %q", spec.Implementer)
	}
	if !strings.Contains(spec.Model, "qwen2.5-coder") {
		t.Errorf("model default: %q", spec.Model)
	}
	if spec.MaxSubtasks == 0 || spec.Timeout == 0 {
		t.Errorf("caps not filled: %+v", spec)
	}
}

// TestPlannerPrompt_WarnsAboutWeakImplementer locks in the prompt's
// core contract: the planner is told the implementer is a weak local
// model. If someone edits plannerPrompt and drops this warning, the
// downstream Qwen will get underspecified tasks and fail silently.
// This is the guardrail.
func TestPlannerPrompt_WarnsAboutWeakImplementer(t *testing.T) {
	p := plannerPrompt("/tmp/x", "build thing", "aider-ollama", "ollama_chat/qwen2.5-coder:14b", 10)
	must := []string{
		"TWO-AGENT",
		"small, local, open-weights",
		"tiny context window",
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
// We stub the `claude` and `aider` commands with tiny shell scripts in
// a temp dir that's prepended to PATH. The fake planner writes a
// 2-subtask plan to stdout; the fake implementer just touches the
// files named by the subtask so the test can verify wiring (files
// passed in, model/base-url exported, exit code respected).
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
	// The implementer stub records its argv + env so we can assert
	// --model and OLLAMA_API_BASE flow through, then touches every
	// trailing positional arg (aider treats those as files to edit).
	writeStub(t, filepath.Join(stubDir, "aider"), `#!/bin/bash
echo "ARGV: $@"
echo "OLLAMA_API_BASE=${OLLAMA_API_BASE}"
# Touch any positional file args — skip flag values.
skip_next=0
for a in "$@"; do
  if [ $skip_next -eq 1 ]; then skip_next=0; continue; fi
  case "$a" in
    --model|--message) skip_next=1;;
    --*) ;;
    *) touch -- "$a" || true;;
  esac
done
`)

	oldPath := os.Getenv("PATH")
	t.Setenv("PATH", stubDir+":"+oldPath)

	spec := HybridSpec{
		Planner:     "claude",
		Implementer: "aider-ollama",
		Model:       "ollama_chat/qwen2.5-coder:14b",
		BaseURL:     "http://127.0.0.1:11434",
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
	// file-scope plumbing (planner→orchestrator→aider positional
	// args) actually works.
	for _, f := range []string{"a.txt", "b.txt"} {
		if _, err := os.Stat(filepath.Join(workDir, f)); err != nil {
			t.Errorf("expected implementer to create %s: %v", f, err)
		}
	}
	// OLLAMA_API_BASE must have reached the aider child — otherwise
	// litellm would talk to the wrong endpoint in production.
	joined := ""
	for _, r := range rep.Results {
		joined += r.Output
	}
	if !strings.Contains(joined, "OLLAMA_API_BASE=http://127.0.0.1:11434") {
		t.Errorf("implementer did not receive OLLAMA_API_BASE; got output:\n%s", joined)
	}
	if !strings.Contains(joined, "--model ollama_chat/qwen2.5-coder:14b") {
		t.Errorf("implementer did not receive --model flag; got output:\n%s", joined)
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
	t.Setenv("PATH", stubDir+":"+os.Getenv("PATH"))

	rep, err := RunHybrid(context.Background(), HybridSpec{
		Planner: "claude", Implementer: "aider-ollama",
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

// TestHybridPreflight_ReportsMissing checks that a nonsense base URL
// leads to ollamaOk=false and a helpful hint. Aider presence depends
// on the host; skip aider assertions if the test environment has it.
func TestHybridPreflight_ReportsMissing(t *testing.T) {
	pf := checkHybrid("aider-ollama", "ollama_chat/bogus:does-not-exist", "http://127.0.0.1:1")
	if pf.OllamaOK {
		t.Errorf("expected OllamaOK=false at unreachable URL, got %+v", pf)
	}
	if pf.Hint == "" {
		t.Errorf("expected a hint, got empty")
	}
}
