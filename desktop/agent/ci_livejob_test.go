package main

// ci_livejob_test.go — opt-in FULL end-to-end: bring up one ephemeral
// self-hosted runner (host mode, this Mac) that claims + runs ONE real queued
// GitHub Actions job, through the production code path (runEphemeralRunner).
// Skipped unless CI_LIVE_JOB=1. The caller (scripts in the conversation) creates
// a throwaway PRIVATE repo, pushes a `runs-on: [self-hosted, yaver]` workflow to
// queue a job, runs this test, verifies, then deletes the repo.
//
//   CI_LIVE_JOB=1 GH_TEST_TOKEN=$(gh auth token) GH_TEST_REPO=owner/repo \
//     go test -run TestLiveCIFullJob -v -count=1 -timeout 8m .

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestLiveCIFullJob(t *testing.T) {
	if os.Getenv("CI_LIVE_JOB") != "1" {
		t.Skip("live full-job test: set CI_LIVE_JOB=1, GH_TEST_TOKEN, GH_TEST_REPO")
	}
	token := os.Getenv("GH_TEST_TOKEN")
	repo := os.Getenv("GH_TEST_REPO")
	if token == "" || repo == "" {
		t.Fatal("GH_TEST_TOKEN and GH_TEST_REPO required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 7*time.Minute)
	defer cancel()

	reg := CIRunnerRegistration{
		Provider:      CIGitHub,
		Target:        repo,
		Scope:         "repo",
		Isolation:     CIIsolationHost, // this Mac advertises os:darwin; run directly
		Where:         CIWhereOwn,
		MaxConcurrent: 1,
		PrivateOnly:   true,
	}

	// Mint a fresh registration token (production path) using the test token.
	rt, err := fetchRegistrationToken(ctx,
		githubRegistrationTokenURL(reg.Host, reg.Scope, reg.Target),
		"Authorization", "Bearer "+token)
	if err != nil {
		t.Fatalf("mint registration token: %v", err)
	}
	t.Logf("minted registration token (len %d) for %s; labels=%v", len(rt), repo, reg.runnerLabels())

	store := NewRunnerStore(50)
	run := store.Start(RunnerRun{JobName: "livejob:" + repo, Kind: RunnerJobWorkflow})

	t.Logf("bringing up ephemeral runner (host mode) — will claim one queued job ...")
	runnerOS, exitCode, execErr := runEphemeralRunner(ctx, reg, rt, run.ID, store)
	store.Finish(run.ID, exitCode, false)

	final, _ := store.GetRun(run.ID, "")
	t.Logf("runner os=%s exit=%d err=%v\n----- run log tail -----\n%s\n------------------------",
		runnerOS, exitCode, execErr, final.OutputTail)

	if execErr != nil {
		t.Fatalf("ephemeral runner errored: %v", execErr)
	}
	if exitCode != 0 {
		t.Fatalf("job exit code = %d (want 0)", exitCode)
	}
	t.Logf("FULL E2E OK — ephemeral runner claimed + ran a real GitHub job on this Mac, exit 0")
}
