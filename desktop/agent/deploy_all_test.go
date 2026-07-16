package main

// deploy_all_test.go — P9 orchestrator tests. The command runner
// and preflight are behind var seams so the whole verb is hermetic
// (no real convex deploy, no shell execution). writeDeployReport
// is also stubbed to capture the markdown into memory.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

func withDeployStubs(t *testing.T,
	pre func(ctx context.Context, repo string) error,
	run func(ctx context.Context, wd, cmd string, timeout time.Duration) (string, error),
	writer func(DeployAllResult) error) func() {
	t.Helper()
	origPre := deployPreflight
	origRun := deployRunCommand
	origWrite := writeDeployReport
	deployPreflight = pre
	deployRunCommand = run
	writeDeployReport = writer
	return func() {
		deployPreflight = origPre
		deployRunCommand = origRun
		writeDeployReport = origWrite
	}
}

func TestRunDeployAll_PreflightRedBlocksEverything(t *testing.T) {
	var executed int
	cleanup := withDeployStubs(t,
		func(context.Context, string) error { return errors.New("go build failed") },
		func(context.Context, string, string, time.Duration) (string, error) {
			executed++
			return "", nil
		},
		func(DeployAllResult) error { return nil })
	defer cleanup()

	res := RunDeployAll(context.Background(), DeployAllRequest{})
	if res.OK {
		t.Fatal("preflight red must return OK=false")
	}
	if res.GateStatus != "red" {
		t.Fatalf("gate = %q, want red", res.GateStatus)
	}
	if executed != 0 {
		t.Fatalf("red gate must skip every step, saw %d executions", executed)
	}
}

func TestRunDeployAll_PreflightForceRunsSteps(t *testing.T) {
	cleanup := withDeployStubs(t,
		func(context.Context, string) error { return errors.New("still broken") },
		func(context.Context, string, string, time.Duration) (string, error) { return "ok", nil },
		func(DeployAllResult) error { return nil })
	defer cleanup()
	res := RunDeployAll(context.Background(), DeployAllRequest{Force: true, Only: []string{"convex"}})
	if res.GateStatus != "forced" {
		t.Fatalf("gate = %q, want forced", res.GateStatus)
	}
	if len(res.Steps) != 1 || res.Steps[0].Status != "deployed" {
		t.Fatalf("force run expected 1 deployed step, got %+v", res.Steps)
	}
}

func TestRunDeployAll_DryRunSkipsCommands(t *testing.T) {
	cleanup := withDeployStubs(t,
		func(context.Context, string) error { return nil },
		func(context.Context, string, string, time.Duration) (string, error) {
			t.Fatal("dry-run must NOT invoke deployRunCommand")
			return "", nil
		},
		func(DeployAllResult) error { return nil })
	defer cleanup()
	res := RunDeployAll(context.Background(), DeployAllRequest{DryRun: true})
	if !res.OK {
		t.Fatalf("dry-run should be OK, got %+v", res)
	}
	for _, s := range res.Steps {
		if s.Status != "skipped" {
			t.Fatalf("dry-run step %s status = %q, want skipped", s.Name, s.Status)
		}
	}
}

func TestRunDeployAll_TestflightKeychainRetries(t *testing.T) {
	var (
		mu    sync.Mutex
		calls []string
	)
	cleanup := withDeployStubs(t,
		func(context.Context, string) error { return nil },
		func(_ context.Context, _, cmd string, _ time.Duration) (string, error) {
			mu.Lock()
			calls = append(calls, cmd)
			n := len(calls)
			mu.Unlock()
			if n == 1 && strings.Contains(cmd, "deploy-testflight.sh") {
				return "codesign failed: keychain locked", errors.New("exit 65")
			}
			return "TestFlight upload ok", nil
		},
		func(DeployAllResult) error { return nil })
	defer cleanup()
	res := RunDeployAll(context.Background(), DeployAllRequest{Only: []string{"testflight-ios"}})
	if len(res.Steps) != 1 {
		t.Fatalf("expected 1 step, got %+v", res.Steps)
	}
	if res.Steps[0].Status != "deployed" {
		t.Fatalf("testflight retry should succeed, got %+v", res.Steps[0])
	}
	if len(calls) != 2 {
		t.Fatalf("expected 2 calls (fail + retry), got %d: %v", len(calls), calls)
	}
	if !strings.Contains(calls[1], "unlock-keychain") {
		t.Fatalf("retry command should include unlock-keychain, got %q", calls[1])
	}
}

func TestRunDeployAll_OnlyExcludeFiltering(t *testing.T) {
	cleanup := withDeployStubs(t,
		func(context.Context, string) error { return nil },
		func(context.Context, string, string, time.Duration) (string, error) { return "", nil },
		func(DeployAllResult) error { return nil })
	defer cleanup()
	res := RunDeployAll(context.Background(), DeployAllRequest{Only: []string{"convex", "web-cloudflare"}, Exclude: []string{"web-cloudflare"}})
	if len(res.Steps) != 1 || res.Steps[0].Name != "convex" {
		t.Fatalf("only+exclude interaction: %+v", res.Steps)
	}
}

func TestComposeDeployReportMarkdown_HeadersAndTable(t *testing.T) {
	res := DeployAllResult{
		OK:         false,
		GateStatus: "green",
		Steps: []DeployStepResult{
			{Name: "convex", Channel: "infra", Status: "deployed", DurationS: 12.3, Detail: "npx convex deploy ok"},
			{Name: "testflight-ios", Channel: "beta", Status: "blocked", DurationS: 4.1, Detail: "needs keychain"},
		},
	}
	md, err := composeDeployReportMarkdown(res)
	if err != nil {
		t.Fatalf("compose: %v", err)
	}
	if !strings.Contains(md, "# Yaver n2n deploy report") {
		t.Fatal("missing header")
	}
	if !strings.Contains(md, "| convex |") || !strings.Contains(md, "| testflight-ios |") {
		t.Fatal("missing table rows")
	}
	if !strings.Contains(md, "```json") {
		t.Fatal("missing embedded JSON block")
	}
	// Sanity: gate + OK line renders as expected.
	if !strings.Contains(md, fmt.Sprintf("Overall OK: %v", res.OK)) {
		t.Fatal("missing gate summary")
	}
}
