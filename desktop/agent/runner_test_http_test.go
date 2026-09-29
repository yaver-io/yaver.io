package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRunRunnerProbeOpenCodeUsesMobileWorkspaceLauncher(t *testing.T) {
	original := openCodeProbeRunnerFactory
	t.Cleanup(func() { openCodeProbeRunnerFactory = original })

	var gotSelection sandboxRunnerSelection
	openCodeProbeRunnerFactory = func(selection sandboxRunnerSelection) sandboxRunnerFn {
		gotSelection = selection
		return func(_ context.Context, workDir, prompt string) (sandboxRunMeta, error) {
			if prompt != "prove provider auth" {
				t.Fatalf("prompt = %q", prompt)
			}
			if info, err := os.Stat(workDir); err != nil || !info.IsDir() {
				t.Fatalf("isolated workdir is not usable: info=%v err=%v", info, err)
			}
			return sandboxRunMeta{rationale: "provider verified", model: selection.Model}, nil
		}
	}

	out, err := runRunnerProbe(RunnerConfig{
		RunnerID: "opencode",
		Model:    "deepseek/deepseek-flash",
	}, "opencode", "prove provider auth", 2*time.Second)
	if err != nil {
		t.Fatalf("runRunnerProbe failed: %v", err)
	}
	if out != "provider verified" {
		t.Fatalf("output = %q", out)
	}
	if gotSelection.Model != "deepseek/deepseek-flash" || gotSelection.Provider != "deepseek" || gotSelection.Mode != "build" {
		t.Fatalf("selection = %+v", gotSelection)
	}
	if !gotSelection.SkipYaverMCP {
		t.Fatal("provider readiness probe must not spawn the Yaver MCP tool tree")
	}
}

func TestSuccessfulRunnerTestUpdatesWorkspaceVerification(t *testing.T) {
	ClearRunnerAuthProven("opencode")
	ClearRunnerAuthInvalid("opencode")
	t.Cleanup(func() { ClearRunnerAuthProven("opencode"); ClearRunnerAuthInvalid("opencode") })

	observeRunnerProbeOutcome("opencode", "Hello from the remote mobile workspace", nil)
	if !runnerAuthProofRecent("opencode") {
		t.Fatal("successful /agent/runners/test did not update the readiness proof ledger")
	}
}

func TestRejectedRunnerTestInvalidatesWorkspaceVerification(t *testing.T) {
	ClearRunnerAuthProven("opencode")
	ClearRunnerAuthInvalid("opencode")
	t.Cleanup(func() { ClearRunnerAuthProven("opencode"); ClearRunnerAuthInvalid("opencode") })

	observeRunnerProbeOutcome("opencode", "401 unauthorized: invalid api key", context.DeadlineExceeded)
	if _, rejected := runnerAuthFailureRecent("opencode"); !rejected {
		t.Fatal("provider rejection from /agent/runners/test was not recorded")
	}
}

func TestLiveMobileWorkspaceOpenCodeDeepSeek(t *testing.T) {
	if os.Getenv("YAVER_LIVE_OPENCODE_DEEPSEEK") != "1" {
		t.Skip("set YAVER_LIVE_OPENCODE_DEEPSEEK=1 on a configured remote box")
	}
	out, err := runRunnerProbe(RunnerConfig{
		RunnerID: "opencode",
		Model:    "deepseek/deepseek-flash",
	}, "opencode", "Reply with exactly YAVER_DEEPSEEK_OK and nothing else.", 75*time.Second)
	if err != nil {
		t.Fatalf("live OpenCode + DeepSeek probe failed: %v; output=%q", err, out)
	}
	if !strings.Contains(out, "YAVER_DEEPSEEK_OK") {
		t.Fatalf("live OpenCode + DeepSeek probe returned unexpected output: %q", out)
	}
}

func TestRunRunnerProbeCodexUsesDangerousBypass(t *testing.T) {
	dir := t.TempDir()
	argsFile := filepath.Join(dir, "args.txt")
	fakeCodex := filepath.Join(dir, "codex")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > " + shellQuote(argsFile) + "\nprintf 'OK\\n'\n"
	if err := os.WriteFile(fakeCodex, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	out, err := runRunnerProbe(RunnerConfig{RunnerID: "codex", Command: fakeCodex, Model: "gpt-test"}, "codex", "say ok", 2*time.Second)
	if err != nil {
		t.Fatalf("runRunnerProbe failed: %v; output=%q", err, out)
	}
	raw, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatal(err)
	}
	args := strings.Split(strings.TrimSpace(string(raw)), "\n")
	joined := strings.Join(args, " ")
	for _, want := range []string{"exec", "--dangerously-bypass-approvals-and-sandbox", "--skip-git-repo-check", "--model gpt-test", "say ok"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("codex probe args missing %q: %v", want, args)
		}
	}
}
