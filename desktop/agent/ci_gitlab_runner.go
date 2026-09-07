package main

// ci_gitlab_runner.go — GitLab half of the self-hosted runner adapter (the
// GitHub half lives in ci_selfhosted_runner.go). GitLab's one-shot equivalent
// of `actions/runner --ephemeral` is `gitlab-runner run-single --max-builds 1`:
// it claims exactly one job, runs it, and exits — so it slots into the same
// CISupervisor ephemeral loop. The runner auth token is already minted by
// mintCIRunnerLease (POST /user/runners); that lease is protected/tagged at
// creation and deleted from GitLab after the runner exits. See
// docs/yaver-managed-cloud-ci-absorption.md.

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

// gitlabRunnerVersion pins the downloaded gitlab-runner. "latest" tracks the
// current release; gitlab-runner is backward-compatible with gitlab.com.
const gitlabRunnerChannel = "latest"

var gitLabRunnerDownloadMu sync.Mutex

// ciGitLabDockerImage is the fallback image for the docker executor when the
// project's .gitlab-ci.yml doesn't pin one.
const ciGitLabDockerImage = "alpine:latest"

// gitlabRunnerDownloadURL maps GOOS/GOARCH to the gitlab-runner binary. Pure —
// unit-tested.
func gitlabRunnerDownloadURL(channel, goos, goarch string) (string, error) {
	var osLabel string
	switch goos {
	case "linux", "darwin", "windows":
		osLabel = goos
	default:
		return "", fmt.Errorf("unsupported gitlab-runner OS %q", goos)
	}
	var archLabel string
	switch goarch {
	case "amd64":
		archLabel = "amd64"
	case "arm64":
		archLabel = "arm64"
	case "386":
		archLabel = "386"
	case "arm":
		archLabel = "arm"
	default:
		return "", fmt.Errorf("unsupported gitlab-runner arch %q", goarch)
	}
	bin := fmt.Sprintf("gitlab-runner-%s-%s", osLabel, archLabel)
	if goos == "windows" {
		bin += ".exe"
	}
	return fmt.Sprintf("https://gitlab-runner-downloads.s3.amazonaws.com/%s/binaries/%s", channel, bin), nil
}

// ensureGitLabRunner downloads the gitlab-runner binary once into
// ~/.yaver/runner/gitlab/ and returns its path (executable).
func ensureGitLabRunner(ctx context.Context, store *RunnerStore, runID string) (string, error) {
	gitLabRunnerDownloadMu.Lock()
	defer gitLabRunnerDownloadMu.Unlock()

	dir, err := ConfigDir()
	if err != nil {
		return "", err
	}
	target := filepath.Join(dir, "runner", "gitlab")
	bin := filepath.Join(target, "gitlab-runner")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	if err := os.MkdirAll(target, 0700); err != nil {
		return "", err
	}
	url, err := gitlabRunnerDownloadURL(gitlabRunnerChannel, runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return "", err
	}
	asset := filepath.Base(url)
	checksumURL := fmt.Sprintf("https://gitlab-runner-downloads.s3.amazonaws.com/%s/release.sha256", gitlabRunnerChannel)
	expected, err := fetchGitLabRunnerChecksum(ctx, checksumURL, asset)
	if err != nil {
		return "", fmt.Errorf("fetch gitlab-runner checksum: %w", err)
	}
	checksumPath := bin + ".sha256"
	if fileExistsCI(bin) {
		actual, hashErr := sha256FileCI(bin)
		stored, readErr := os.ReadFile(checksumPath)
		if hashErr == nil && readErr == nil && strings.EqualFold(strings.TrimSpace(string(stored)), actual) && strings.EqualFold(actual, expected) {
			return bin, nil
		}
		store.Append(runID, "[ci] existing gitlab-runner is unverified or stale; replacing it with a checksum-verified binary")
	}
	store.Append(runID, "[ci] downloading gitlab-runner ("+runtime.GOOS+"/"+runtime.GOARCH+") ...")
	candidate := bin + ".download"
	if err := downloadFileCI(ctx, url, candidate); err != nil {
		return "", fmt.Errorf("download gitlab-runner: %w", err)
	}
	defer os.Remove(candidate)
	actual, err := sha256FileCI(candidate)
	if err != nil {
		return "", fmt.Errorf("hash downloaded gitlab-runner: %w", err)
	}
	if !strings.EqualFold(actual, expected) {
		return "", fmt.Errorf("gitlab-runner checksum mismatch for %s: got %s, expected %s", asset, actual, expected)
	}
	if err := os.Chmod(candidate, 0755); err != nil {
		return "", err
	}
	if err := os.Rename(candidate, bin); err != nil {
		return "", fmt.Errorf("install verified gitlab-runner: %w", err)
	}
	if err := os.WriteFile(checksumPath, []byte(expected+"\n"), 0600); err != nil {
		return "", fmt.Errorf("record gitlab-runner checksum: %w", err)
	}
	return bin, nil
}

func fetchGitLabRunnerChecksum(ctx context.Context, checksumURL, asset string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, checksumURL, nil)
	if err != nil {
		return "", err
	}
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return "", fmt.Errorf("checksum download returned %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4*1024*1024))
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(string(body), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		name := strings.TrimPrefix(fields[len(fields)-1], "*")
		if filepath.Base(name) == asset && len(fields[0]) == sha256.Size*2 {
			return strings.ToLower(fields[0]), nil
		}
	}
	return "", fmt.Errorf("%s not found in release.sha256", asset)
}

func sha256FileCI(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", h.Sum(nil)), nil
}

// gitlabRunnerRunArgs builds the `run-single` arg list for a one-shot job. Pure
// — unit-tested. executor is "shell" (host) or "docker" (container).
func gitlabRunnerRunArgs(instanceURL, token, executor, dockerImage string) []string {
	args := []string{
		"run-single",
		"--url", instanceURL,
		"--token", token,
		"--executor", executor,
		"--max-builds", "1",
	}
	if executor == "docker" {
		args = append(args, "--docker-image", dockerImage)
	}
	return args
}

// runGitLabRunner downloads gitlab-runner and runs exactly one job, streaming
// output into the run log. Returns the exit code.
func runGitLabRunner(ctx context.Context, reg CIRunnerRegistration, token, runID string, store *RunnerStore) (int, error) {
	bin, err := ensureGitLabRunner(ctx, store, runID)
	if err != nil {
		store.Append(runID, "[ci] gitlab-runner download: "+err.Error())
		return -1, err
	}
	executor := "shell"
	if reg.Isolation == CIIsolationContainer {
		if _, lookErr := exec.LookPath("docker"); lookErr != nil {
			return -1, fmt.Errorf("isolation=container needs docker (not found); register the GitLab runner with isolation=host on a dedicated box, or install docker: %w", lookErr)
		}
		executor = "docker"
	}
	args := gitlabRunnerRunArgs(reg.forgeURL(), token, executor, ciGitLabDockerImage)
	cmd := exec.CommandContext(ctx, bin, args...)
	return streamCmdToRun(store, runID, cmd)
}

// gitlabExecutorFor exposes the host/container → shell/docker mapping for tests.
func gitlabExecutorFor(iso CIIsolation) string {
	if iso == CIIsolationContainer {
		return "docker"
	}
	return "shell"
}
