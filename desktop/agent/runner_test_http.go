package main

// runner_test_http.go — POST /agent/runners/test wraps each runner with a
// short, deterministic prompt and returns a structured pass/fail. The web
// + mobile clients render this as a "Test" button on each LLM chip so a
// user can verify "is claude actually working on that machine right now"
// without leaving the device card.
//
// Auth-failure detection is conservative — if the per-runner readiness
// detector already says "not authenticated" or the subprocess output
// looks like a login prompt, we flip `needsAuth: true` and tell the
// client whether the existing browser-auth flow can rescue it (only
// claude + codex have one — `aider`/`opencode`/`goose` need manual
// API-key configuration; `ollama` + `aider-ollama` are local). The
// client uses `supportsBrowserAuth` to decide whether to auto-pop
// the headless login modal.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os/exec"
	"strings"
	"time"
)

// runnerTestResult is the wire contract between agent and clients.
//
//   - ok = the runner produced a usable response (or, for local
//     daemons, the daemon responded). False if anything failed.
//   - needsAuth = the failure was specifically "no credentials" — the
//     UI should offer a sign-in CTA rather than a generic error.
//   - supportsBrowserAuth = an existing /runner-auth/browser/start
//     flow can complete sign-in headlessly. Clients use this to
//     auto-open the modal on failure.
//   - probe = which check actually fired ("binary" / "auth" /
//     "subprocess" / "daemon"). Useful for telemetry and for clients
//     that want to phrase the error.
type runnerTestResult struct {
	OK                  bool   `json:"ok"`
	Runner              string `json:"runner"`
	Probe               string `json:"probe"`
	NeedsAuth           bool   `json:"needsAuth,omitempty"`
	SupportsBrowserAuth bool   `json:"supportsBrowserAuth,omitempty"`
	Output              string `json:"output,omitempty"`
	Error               string `json:"error,omitempty"`
	DurationMs          int64  `json:"durationMs"`
	Model               string `json:"model,omitempty"`
}

const (
	runnerTestDefaultPrompt = "Reply with the single word OK and nothing else."
	runnerTestTimeout       = 25 * time.Second
	runnerTestMaxTimeout    = 2 * time.Minute
)

func runnerSupportsBrowserAuth(id string) bool {
	switch normalizeRunnerID(id) {
	case "claude", "codex":
		return true
	}
	return false
}

func runnerIsLocalLLM(id string) bool {
	switch normalizeRunnerID(id) {
	case "ollama", "aider-ollama":
		return true
	}
	return false
}

// looksLikeAuthFailure scans subprocess output for the canonical
// "you need to log in" phrases each CLI prints. Conservative on
// purpose — false positives turn a real error into a confusing
// "sign in" pop-up.
func looksLikeAuthFailure(text string) bool {
	lower := strings.ToLower(text)
	needles := []string{
		"not authenticated",
		"please log in",
		"please login",
		"please sign in",
		"unauthorized",
		"401 unauthorized",
		"invalid api key",
		"authentication failed",
		"missing api key",
		"not logged in",
		"credentials not found",
		"openai_api_key",
		"anthropic_api_key",
		"expired token",
		"run `claude login`",
		"run `codex login`",
		"run claude login",
		"run codex login",
	}
	for _, n := range needles {
		if strings.Contains(lower, n) {
			return true
		}
	}
	return false
}

func (s *HTTPServer) handleRunnerTest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Runner    string `json:"runner"`
		Prompt    string `json:"prompt"`
		Model     string `json:"model"`
		TimeoutMs int64  `json:"timeoutMs"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad json: "+err.Error(), http.StatusBadRequest)
		return
	}
	runnerID := normalizeRunnerID(req.Runner)
	if runnerID == "" {
		http.Error(w, "runner required", http.StatusBadRequest)
		return
	}
	cfg, ok := builtinRunners[runnerID]
	if !ok {
		writeRunnerTestResult(w, runnerTestResult{
			Runner: runnerID,
			OK:     false,
			Probe:  "binary",
			Error:  "unknown runner id",
		})
		return
	}
	if model := strings.TrimSpace(req.Model); model != "" {
		cfg.Model = model
	}
	timeout := runnerTestTimeout
	if req.TimeoutMs > 0 {
		requested := time.Duration(req.TimeoutMs) * time.Millisecond
		if requested > runnerTestMaxTimeout {
			requested = runnerTestMaxTimeout
		}
		timeout = requested
	} else if runnerID == "opencode" {
		timeout = 75 * time.Second
	}
	started := time.Now()

	// Step 1 — binary on PATH? Clearer signal than a launch failure.
	if err := CheckRunnerBinary(cfg.Command); err != nil {
		writeRunnerTestResult(w, runnerTestResult{
			Runner:     runnerID,
			OK:         false,
			Probe:      "binary",
			Error:      err.Error(),
			DurationMs: time.Since(started).Milliseconds(),
		})
		return
	}

	// Step 2 — per-runner readiness detector. If it already knows
	// auth is missing, skip the subprocess entirely.
	status := DetectRunnerRuntimeStatus(cfg, "")
	if !status.Ready {
		writeRunnerTestResult(w, runnerTestResult{
			Runner:              runnerID,
			OK:                  false,
			Probe:               "auth",
			NeedsAuth:           true,
			SupportsBrowserAuth: runnerSupportsBrowserAuth(runnerID),
			Error:               status.Error,
			DurationMs:          time.Since(started).Milliseconds(),
		})
		return
	}

	// Step 3 — local LLMs: don't actually generate. Probe the daemon
	// listing instead. Matches "for local LLMs just test pass/fail".
	if runnerIsLocalLLM(runnerID) {
		result := probeOllamaDaemon(timeout, cfg.Model)
		result.Runner = runnerID
		result.DurationMs = time.Since(started).Milliseconds()
		writeRunnerTestResult(w, result)
		return
	}

	// Step 4 — cloud LLMs: spawn a tiny subprocess generation.
	prompt := strings.TrimSpace(req.Prompt)
	if prompt == "" {
		prompt = runnerTestDefaultPrompt
	}
	output, err := runRunnerProbe(cfg, runnerID, prompt, timeout)
	duration := time.Since(started).Milliseconds()
	if err != nil {
		combined := strings.TrimSpace(output) + "\n" + err.Error()
		result := runnerTestResult{
			Runner:     runnerID,
			OK:         false,
			Probe:      "subprocess",
			Output:     truncateRunnerOutput(output, 2000),
			Error:      err.Error(),
			DurationMs: duration,
			Model:      cfg.Model,
		}
		if looksLikeAuthFailure(combined) {
			result.NeedsAuth = true
			result.SupportsBrowserAuth = runnerSupportsBrowserAuth(runnerID)
		}
		writeRunnerTestResult(w, result)
		return
	}
	writeRunnerTestResult(w, runnerTestResult{
		Runner:     runnerID,
		OK:         true,
		Probe:      "subprocess",
		Output:     truncateRunnerOutput(output, 1000),
		DurationMs: duration,
		Model:      cfg.Model,
	})
}

// runRunnerProbe builds the per-runner argv for a one-shot test
// generation. Stays close to how the loop actually invokes each
// runner so a test pass means a real loop kick will pass.
func runRunnerProbe(cfg RunnerConfig, runnerID, prompt string, timeout time.Duration) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	var args []string
	switch runnerID {
	case "claude":
		args = []string{
			"--print", prompt,
			"--permission-mode", "bypassPermissions",
			"--output-format", "text",
		}
		if mid := strings.TrimSpace(cfg.Model); mid != "" {
			args = append(args, "--model", mid)
		}
	case "codex":
		args = []string{"exec", "--skip-git-repo-check"}
		if mid := strings.TrimSpace(cfg.Model); mid != "" {
			args = append(args, "--model", mid)
		}
		args = append(args, prompt)
	case "aider":
		args = []string{
			"--yes-always", "--no-git", "--no-pretty", "--no-stream",
			"--message", prompt, "--exit",
		}
	case "goose":
		args = []string{"run", "--text", prompt}
	case "opencode":
		args = []string{"run", "--dangerously-skip-permissions"}
		if mid := strings.TrimSpace(cfg.Model); mid != "" {
			args = append(args, "--model", mid)
		}
		args = append(args, prompt)
	case "amp":
		args = []string{"run", prompt}
	default:
		return "", fmt.Errorf("no probe wiring for runner %q", runnerID)
	}

	cmd := exec.CommandContext(ctx, cfg.Command, args...)
	out, err := cmd.CombinedOutput()
	text := string(out)
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return text, fmt.Errorf("runner did not respond within %s", timeout)
	}
	if err != nil {
		return text, fmt.Errorf("runner exited with error: %v", err)
	}
	return text, nil
}

// probeOllamaDaemon checks the local ollama daemon by running
// `ollama list` — fast (<1s) when the daemon is up, errors out
// crisply when it isn't. We deliberately don't call `ollama run`
// with a model because it would download GBs on first use and
// the answer to "is the runtime working" is the same either way.
func probeOllamaDaemon(timeout time.Duration, model string) runnerTestResult {
	if err := CheckRunnerBinary("ollama"); err != nil {
		return runnerTestResult{
			OK:    false,
			Probe: "binary",
			Error: err.Error(),
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, "ollama", "list").CombinedOutput()
	if err != nil {
		return runnerTestResult{
			OK:     false,
			Probe:  "daemon",
			Output: truncateRunnerOutput(string(out), 800),
			Error:  fmt.Sprintf("ollama daemon not reachable: %v", err),
		}
	}
	return runnerTestResult{
		OK:     true,
		Probe:  "daemon",
		Output: truncateRunnerOutput(string(out), 800),
		Model:  strings.TrimSpace(model),
	}
}

func writeRunnerTestResult(w http.ResponseWriter, r runnerTestResult) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(r)
}

func truncateRunnerOutput(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}
