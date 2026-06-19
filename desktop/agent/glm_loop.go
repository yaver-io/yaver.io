package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"strings"
	"time"
)

// glm_loop.go — the owned agentic loop core (P1 of the command-cards
// plan; see docs/structured-command-cards-plan.md).
//
// This is the BEST-FIDELITY structured-command producer: because the
// loop executes the model's shell commands itself, it emits
// command_start / command_output / command_end with the REAL exit code
// and duration (claude-code's stream-json can't surface an exit code;
// opencode's raw stream can't surface output). It talks to any
// OpenAI-compatible chat-completions endpoint — GLM direct or
// OpenRouter (z-ai/glm-4.7) — with the user's own key (BYOK; no
// inference revenue, per feedback_no_api_keys_subscription_only).
//
// SCOPE (honest): this is the loop CORE only. It is NOT the full
// Tier-3 on-device RN agent (esbuild-wasm/Re.Pack/super-host — separate
// large work) and is deliberately NOT auto-wired into runner selection
// (runner_resolve.go / code_control.go carry a parallel refactor —
// project_managed_vs_byo_hetzner). It is a self-contained, unit-tested
// function so P1's native-producer contract is real and verifiable,
// not dead code. Wiring it in as a first-class runner is a separate,
// reviewed step.
//
// PRIVACY: command + cwd + output flow only through emitCommand* →
// Task.eventCh → the task SSE stream (P2P). This file never touches
// Convex. command_events_privacy_test.go pins the forbidden keys.
//
// SECURITY: like every Yaver runner (claude/codex/opencode all run
// with their yolo flag — feedback_runners_always_dangerous), this
// executes model-generated shell. It must only be invoked in the same
// trust context as the other runners.

// GLMLoopConfig configures one owned-loop run.
type GLMLoopConfig struct {
	BaseURL  string // e.g. https://openrouter.ai/api/v1  (no trailing slash needed)
	APIKey   string // user's own key (BYOK)
	Model    string // e.g. z-ai/glm-4.7
	MaxSteps int    // hard cap on tool round-trips (default 12)
	// MaxToolOutputBytes caps how much command output is fed back to
	// the model (and marked truncated on the card). 0 → 16 KiB.
	MaxToolOutputBytes int
}

// --- OpenAI-compatible wire types (subset we use) -------------------

type glmMessage struct {
	Role       string        `json:"role"`
	Content    string        `json:"content,omitempty"`
	ToolCalls  []glmToolCall `json:"tool_calls,omitempty"`
	ToolCallID string        `json:"tool_call_id,omitempty"`
	Name       string        `json:"name,omitempty"`
}

type glmToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type glmChatRequest struct {
	Model    string                   `json:"model"`
	Messages []glmMessage             `json:"messages"`
	Tools    []map[string]interface{} `json:"tools,omitempty"`
}

type glmChatResponse struct {
	Choices []struct {
		Message      glmMessage `json:"message"`
		FinishReason string     `json:"finish_reason"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// bashToolSpec is the single tool we expose. A constrained surface
// keeps a weak/cheap model on-rails.
var bashToolSpec = map[string]interface{}{
	"type": "function",
	"function": map[string]interface{}{
		"name":        "bash",
		"description": "Run a shell command in the project working directory and return its combined stdout/stderr and exit code.",
		"parameters": map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"command": map[string]interface{}{
					"type":        "string",
					"description": "The shell command to run.",
				},
			},
			"required": []string{"command"},
		},
	},
}

// RunGLMLoop drives the loop until the model answers without a tool
// call, MaxSteps is hit, or ctx is cancelled. Returns the final
// assistant text. Each bash tool call emits native command_* events on
// task (if non-nil) with the real exit code + duration.
func RunGLMLoop(ctx context.Context, cfg GLMLoopConfig, task *Task, workDir, prompt string) (string, error) {
	if strings.TrimSpace(cfg.BaseURL) == "" || strings.TrimSpace(cfg.Model) == "" {
		return "", fmt.Errorf("glm loop: BaseURL and Model are required")
	}
	maxSteps := cfg.MaxSteps
	if maxSteps <= 0 {
		maxSteps = 12
	}
	maxOut := cfg.MaxToolOutputBytes
	if maxOut <= 0 {
		maxOut = 16 * 1024
	}
	endpoint := strings.TrimRight(cfg.BaseURL, "/") + "/chat/completions"
	client := &http.Client{Timeout: 120 * time.Second}

	messages := []glmMessage{
		{Role: "system", Content: "You are a coding agent. Use the bash tool to inspect and modify the project. When the task is complete, reply with a short summary and no tool call."},
		{Role: "user", Content: prompt},
	}

	cmdSeq := 0
	for step := 0; step < maxSteps; step++ {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		reqBody, _ := json.Marshal(glmChatRequest{
			Model:    cfg.Model,
			Messages: messages,
			Tools:    []map[string]interface{}{bashToolSpec},
		})
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(reqBody))
		if err != nil {
			return "", err
		}
		req.Header.Set("Content-Type", "application/json")
		if cfg.APIKey != "" {
			req.Header.Set("Authorization", "Bearer "+cfg.APIKey)
		}
		resp, err := client.Do(req)
		if err != nil {
			return "", fmt.Errorf("glm loop: request failed: %w", err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return "", fmt.Errorf("glm loop: provider HTTP %d: %s", resp.StatusCode, truncate(string(body), 300))
		}
		var cr glmChatResponse
		if err := json.Unmarshal(body, &cr); err != nil {
			return "", fmt.Errorf("glm loop: bad provider JSON: %w", err)
		}
		if cr.Error != nil {
			return "", fmt.Errorf("glm loop: provider error: %s", cr.Error.Message)
		}
		if len(cr.Choices) == 0 {
			return "", fmt.Errorf("glm loop: provider returned no choices")
		}
		msg := cr.Choices[0].Message
		msg.Role = "assistant"

		// No tool call → final answer.
		if len(msg.ToolCalls) == 0 {
			return strings.TrimSpace(msg.Content), nil
		}

		// Echo the assistant message (with its tool_calls) back into
		// the transcript so the follow-up tool messages are valid.
		messages = append(messages, msg)

		for _, tc := range msg.ToolCalls {
			if tc.Function.Name != "bash" {
				messages = append(messages, glmMessage{
					Role: "tool", ToolCallID: tc.ID, Name: tc.Function.Name,
					Content: fmt.Sprintf("error: unknown tool %q", tc.Function.Name),
				})
				continue
			}
			// Weak-model tolerance: arguments may be malformed JSON.
			var args struct {
				Command string `json:"command"`
			}
			if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil || strings.TrimSpace(args.Command) == "" {
				messages = append(messages, glmMessage{
					Role: "tool", ToolCallID: tc.ID, Name: "bash",
					Content: "error: could not parse a non-empty `command` from the tool arguments",
				})
				continue
			}

			cmdSeq++
			id := fmt.Sprintf("%s-glm%d", glmTaskID(task), cmdSeq)
			emitCommandStart(task, id, args.Command, nil, workDir, "glm")
			stdout, stderr, exitCode, dur := glmRunShell(ctx, workDir, args.Command)

			truncated := false
			feed := stdout
			if stderr != "" {
				feed += "\n" + stderr
			}
			if len(feed) > maxOut {
				feed = feed[len(feed)-maxOut:]
				truncated = true
			}
			emitCommandOutput(task, id, "stdout", stdout, 0)
			emitCommandOutput(task, id, "stderr", stderr, 1)
			emitCommandEnd(task, id, exitCode, true, dur, truncated)

			messages = append(messages, glmMessage{
				Role: "tool", ToolCallID: tc.ID, Name: "bash",
				Content: fmt.Sprintf("exit %d\n%s", exitCode, feed),
			})
		}
	}
	return "", fmt.Errorf("glm loop: hit MaxSteps (%d) without a final answer", maxSteps)
}

// runShell executes command via `sh -c` in workDir, returning stdout,
// stderr, the real exit code, and wall duration in ms.
func glmRunShell(ctx context.Context, workDir, command string) (string, string, int, int64) {
	start := time.Now()
	cmd := exec.CommandContext(ctx, "sh", "-c", command)
	if workDir != "" {
		cmd.Dir = workDir
	}
	var so, se bytes.Buffer
	cmd.Stdout = &so
	cmd.Stderr = &se
	err := cmd.Run()
	dur := time.Since(start).Milliseconds()
	exitCode := 0
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			exitCode = ee.ExitCode()
		} else {
			// Couldn't even start (bad cwd, sh missing) — surface as
			// a non-zero exit with the reason on stderr.
			exitCode = -1
			if se.Len() == 0 {
				se.WriteString(err.Error())
			}
		}
	}
	return so.String(), se.String(), exitCode, dur
}

func glmTaskID(t *Task) string {
	if t == nil {
		return "glm"
	}
	return t.ID
}
