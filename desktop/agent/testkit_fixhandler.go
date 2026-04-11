package main

// testkit_fixhandler.go — the seam testkit/autonomous.go has been
// waiting for. Installs a testkit.FixHandler that routes runner
// failures into `claude --print` with a small, bounded prompt and
// parses the JSON FixResult out of claude's response.
//
// Intentionally NOT the full Auto Dev kick path:
//
//   - No worktree: self-heal should be fast, not "kick an entire
//     develop-mode iteration".
//   - No phaseCommit: the runner applies selector replacements
//     in-memory and retries; a real code change goes through
//     `yaver loop run` instead.
//   - No fallback chain: only claude is wired here. The interactive
//     self-heal use case is "I'm watching `yaver test run --watch`
//     and want a quick fix" — if claude isn't available, fall back
//     to the existing heuristic self-heal.
//
// Registered lazily from runTestSDK so the handler only exists when
// the dev explicitly invokes `yaver test run`; we don't install it
// inside the daemon's own test subsystems to avoid surprising
// CI-triggered runs with LLM calls.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/yaver-io/agent/testkit"
)

var fixHandlerOnce sync.Once

// registerInteractiveFixHandler is idempotent — repeat calls reuse
// the first registration. Safe to call from every entry point that
// wants self-heal.
func registerInteractiveFixHandler() {
	fixHandlerOnce.Do(func() {
		testkit.RegisterFixHandler(func(ctx context.Context, req testkit.FixRequest) (*testkit.FixResult, error) {
			return runInteractiveFixClaude(ctx, req)
		})
	})
}

// runInteractiveFixClaude is the actual handler body. Separated so
// tests can call it without touching the package-level registry.
// Wrapped with a small retry loop — rate-limit errors from claude
// are extremely common on a busy solo-dev day (the LLM is already
// serving their interactive session), and a single transient 429
// should not collapse self-heal to "give_up".
func runInteractiveFixClaude(ctx context.Context, req testkit.FixRequest) (*testkit.FixResult, error) {
	if _, err := exec.LookPath("claude"); err != nil {
		// Claude isn't installed — SelectorReplaceFromSelfHeal is
		// still the runner's fallback, so don't treat this as an
		// error. Return "give_up" so AttemptAutonomousFix surfaces
		// a clean status in the auto-fix log.
		return &testkit.FixResult{
			Strategy: "give_up",
			Notes:    "claude CLI not on PATH — `yaver install claude` to enable interactive self-heal",
		}, nil
	}
	if req.Spec == nil {
		return nil, fmt.Errorf("fix request missing spec")
	}

	const maxAttempts = 3
	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		result, err := runInteractiveFixClaudeOnce(ctx, req)
		if err == nil {
			return result, nil
		}
		lastErr = err
		if !isRetryableRunnerError(err) || attempt == maxAttempts {
			break
		}
		// Exponential backoff: 1s, 3s, 9s. The outer
		// AttemptAutonomousFix caps the whole call at 60s, so
		// we're guaranteed to fit even if the first two attempts
		// consume ~5s each.
		wait := time.Duration(1<<(attempt-1)) * 3 * time.Second
		if wait > 15*time.Second {
			wait = 15 * time.Second
		}
		select {
		case <-ctx.Done():
			return &testkit.FixResult{Strategy: "give_up", Notes: "self-heal cancelled before retry"}, nil
		case <-time.After(wait):
		}
	}
	return &testkit.FixResult{
		Strategy: "give_up",
		Notes:    fmt.Sprintf("self-heal exhausted %d attempts: %v", maxAttempts, lastErr),
	}, nil
}

// isRetryableRunnerError inspects a runner error for signatures
// that look like transient rate-limit / 5xx / network blips. Any
// other error is a permanent failure — retrying only delays the
// inevitable "give up."
func isRetryableRunnerError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	for _, marker := range []string{
		"rate limit",
		"rate-limit",
		"429",
		"too many requests",
		"503",
		"overloaded",
		"connection reset",
		"timeout",
		"temporary failure",
	} {
		if strings.Contains(msg, marker) {
			return true
		}
	}
	return false
}

// runInteractiveFixClaudeOnce is one attempt at the claude
// subprocess — split out so the retry wrapper can call it
// repeatedly.
func runInteractiveFixClaudeOnce(ctx context.Context, req testkit.FixRequest) (*testkit.FixResult, error) {

	// Build a tight, bounded prompt. The contract is different from
	// Auto Dev's — we want a small JSON blob, not a full AIResponse.
	prompt := strings.Join([]string{
		"A yaver-test-sdk spec just failed. Propose the smallest possible fix.",
		"",
		fmt.Sprintf("Spec:       %s", req.Spec.Name),
		fmt.Sprintf("Phase:      %s", req.Phase),
		fmt.Sprintf("Step index: %d", req.StepIndex),
		fmt.Sprintf("Action:     %s", req.Action),
		fmt.Sprintf("Error:      %s", req.Error),
		"",
		"Respond with a single JSON object on one line, no narration:",
		"{\"strategy\":\"selector_replace|code_edit|give_up\",",
		" \"selectorReplace\":\"<css selector>\",",
		" \"notes\":\"<1-sentence rationale>\",",
		" \"confidence\":0.0..1.0}",
		"",
		"Use selector_replace only when the step's selector is the",
		"problem. Use code_edit when the source code has drifted.",
		"Use give_up when you don't have enough signal.",
	}, "\n")

	// Bounded claude spawn — --print for non-interactive, 60s
	// deadline (the outer AttemptAutonomousFix also enforces a
	// 60s cap), no --add-dir because we don't want the handler
	// touching files.
	subCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(subCtx, "claude", "--print", "--permission-mode", "default")
	cmd.Stdin = strings.NewReader(prompt)
	cmd.Stderr = os.Stderr
	cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) }
	cmd.WaitDelay = 5 * time.Second

	out, err := cmd.Output()
	if err != nil {
		if errors.Is(subCtx.Err(), context.Canceled) || errors.Is(subCtx.Err(), context.DeadlineExceeded) {
			return &testkit.FixResult{Strategy: "give_up", Notes: "self-heal cancelled (timeout or caller)"}, nil
		}
		return &testkit.FixResult{Strategy: "give_up", Notes: fmt.Sprintf("claude error: %v", err)}, nil
	}
	return parseInteractiveFixResult(string(out)), nil
}

// parseInteractiveFixResult scans claude's stdout for the last
// balanced JSON object that parses into a testkit.FixResult. Mirrors
// parseAIResponse's approach so tolerant-of-narration parsing stays
// consistent across both prompt contracts.
func parseInteractiveFixResult(output string) *testkit.FixResult {
	output = strings.ReplaceAll(output, "```json", "```")
	start := strings.LastIndex(output, "{")
	for start >= 0 {
		candidate := output[start:]
		for end := len(candidate); end > 0; end-- {
			var r testkit.FixResult
			if err := json.Unmarshal([]byte(candidate[:end]), &r); err == nil && r.Strategy != "" {
				return &r
			}
		}
		start = strings.LastIndex(output[:start], "{")
	}
	return &testkit.FixResult{
		Strategy: "give_up",
		Notes:    "claude did not emit a parseable JSON FixResult",
	}
}
