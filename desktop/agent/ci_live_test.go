package main

// ci_live_test.go — opt-in LIVE test of the self-hosted CI runner seams against
// the real GitHub API + release CDN. Skipped unless CI_LIVE=1. Run with:
//
//   CI_LIVE=1 GH_TEST_TOKEN=$(gh auth token) \
//     go test -run TestLiveCIRunnerSeams -v -count=1 .
//
// SEAM 1 mints a registration token (short-lived, expires ~1h, registers
// nothing — harmless). SEAM 2 downloads + extracts the real actions/runner into
// ~/.yaver/runner/gha/<ver>/.

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLiveCIRunnerSeams(t *testing.T) {
	if os.Getenv("CI_LIVE") != "1" {
		t.Skip("live test: set CI_LIVE=1 and GH_TEST_TOKEN to run")
	}
	token := os.Getenv("GH_TEST_TOKEN")
	if token == "" {
		t.Fatal("GH_TEST_TOKEN required (e.g. $(gh auth token))")
	}
	repo := os.Getenv("GH_TEST_REPO")
	if repo == "" {
		repo = "kivanccakmak/yaver.io"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	// SEAM 1 — mint a real registration token through the production code path.
	url := githubRegistrationTokenURL("", "repo", repo)
	t.Logf("SEAM 1: POST %s", url)
	rt, err := fetchRegistrationToken(ctx, url, "Authorization", "Bearer "+token)
	if err != nil {
		t.Fatalf("SEAM 1 mint failed: %v", err)
	}
	if len(rt) < 10 {
		t.Fatalf("SEAM 1 returned implausible token (len %d)", len(rt))
	}
	t.Logf("SEAM 1 OK — minted registration token (len %d, value redacted)", len(rt))

	// SEAM 2 — download + extract the real runner for this OS/arch.
	store := NewRunnerStore(10)
	dir, err := ensureGitHubRunnerExtracted(ctx, githubRunnerVersion, store, "livetest")
	if err != nil {
		t.Fatalf("SEAM 2 download/extract failed: %v", err)
	}
	if !fileExistsCI(filepath.Join(dir, "config.sh")) {
		t.Fatalf("SEAM 2: config.sh missing in %s", dir)
	}
	if !fileExistsCI(filepath.Join(dir, "run.sh")) {
		t.Fatalf("SEAM 2: run.sh missing in %s", dir)
	}
	t.Logf("SEAM 2 OK — actions/runner %s extracted to %s (config.sh + run.sh present)", githubRunnerVersion, dir)
}
