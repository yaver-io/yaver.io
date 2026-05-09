package main

// agent_question_forward.go — implements the `yaver_ask_user` MCP tool.
//
// The handler is the same regardless of where MCP is hosted: stdio
// (child process spawned by claude / codex / opencode) or HTTP (the
// daemon's own /mcp endpoint). In both cases we forward the call to
// the daemon's HTTP endpoint /tasks/{id}/question:
//
//   - In the stdio child, hitting our parent daemon over loopback is
//     the only way to reach the in-memory pendingQuestionRegistry.
//   - In the daemon, hitting our own HTTP listener costs ~1ms and
//     keeps the long-poll plumbing in exactly one place. The
//     alternative — branching on "are we the daemon? then short-
//     circuit the registry directly" — duplicates the auth /
//     timeout / cancellation logic.
//
// The tool blocks on the loopback HTTP response, which itself is
// parked inside registerTaskQuestion until a human answers via
// /tasks/{id}/answer. Net effect from the runner's side: one tool
// call, returns when the user replies, no streaming or polling.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// forwardYaverAskUser is the dispatcher cited by httpserver.go's
// switch case. Returns an MCP tool result either way; never returns
// nil. On cancellation it returns a structured `{cancelled:true}` so
// the runner can decide what to do (the system prompt instructs it
// to fall back to a sensible default).
func forwardYaverAskUser(rawArgs json.RawMessage) interface{} {
	var args struct {
		Prompt     string   `json:"prompt"`
		Kind       string   `json:"kind"`
		Choices    []string `json:"choices"`
		VaultHint  string   `json:"vault_hint"`
		TimeoutSec int      `json:"timeout_sec"`
	}
	if err := json.Unmarshal(rawArgs, &args); err != nil {
		return mcpToolError("invalid arguments: " + err.Error())
	}
	if strings.TrimSpace(args.Prompt) == "" {
		return mcpToolError("prompt is required")
	}

	taskID := strings.TrimSpace(os.Getenv("YAVER_TASK_ID"))
	if taskID == "" {
		// Not running inside a yaver-spawned task — there's no
		// surface to forward the question to. The runner should
		// pick a default and proceed; we surface that intent
		// explicitly instead of pretending the call succeeded.
		return mcpToolError("yaver_ask_user is only available inside a Yaver task (YAVER_TASK_ID not set). Pick a sensible default and proceed; if you really need the human, ask them to relaunch the task through the Yaver mobile app, web dashboard, or `yaver code`.")
	}

	body, _ := json.Marshal(map[string]interface{}{
		"prompt":     args.Prompt,
		"kind":       args.Kind,
		"choices":    args.Choices,
		"vault_hint": args.VaultHint,
		"timeoutSec": args.TimeoutSec,
	})

	timeout := args.TimeoutSec
	if timeout <= 0 {
		timeout = defaultQuestionTimeoutSec
	}
	if timeout > maxQuestionTimeoutSec {
		timeout = maxQuestionTimeoutSec
	}
	// Add a generous safety margin on top of the question TTL — the
	// daemon's auto-expire fires at exactly TTL and resolves the
	// long-poll, but on a heavily-loaded box the JSON write +
	// loopback round-trip can take a few seconds. We never want the
	// HTTP client to give up before the daemon's expirer does.
	clientTimeout := time.Duration(timeout)*time.Second + 30*time.Second

	cfg, err := LoadConfig()
	if err != nil || cfg == nil || strings.TrimSpace(cfg.AuthToken) == "" {
		return mcpToolError("yaver_ask_user: not authenticated (run `yaver auth`)")
	}

	url := localAgentBaseURL() + "/tasks/" + taskID + "/question"
	ctx, cancel := context.WithTimeout(context.Background(), clientTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return mcpToolError("build request: " + err.Error())
	}
	req.Header.Set("Authorization", "Bearer "+cfg.AuthToken)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: clientTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return mcpToolError(fmt.Sprintf("forward question to daemon: %v", err))
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode == http.StatusConflict {
		// A previous question on this task is still pending. The
		// runner shouldn't queue a second one; surface clearly.
		return mcpToolError("a previous yaver_ask_user is still waiting on the user; serialize your asks")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return mcpToolError(fmt.Sprintf("daemon returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw))))
	}
	var result struct {
		OK         bool   `json:"ok"`
		Answer     string `json:"answer"`
		Cancelled  bool   `json:"cancelled"`
		QuestionID string `json:"questionId"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return mcpToolError("decode daemon response: " + err.Error())
	}
	if result.Cancelled {
		// Return as JSON, not error, so the runner can handle the
		// fallback path inside its turn rather than aborting the
		// whole task.
		return mcpToolJSON(map[string]interface{}{
			"cancelled":  true,
			"questionId": result.QuestionID,
			"hint":       "no answer received before timeout. Pick a sensible default and proceed.",
		})
	}
	return mcpToolJSON(map[string]interface{}{
		"answer":     result.Answer,
		"questionId": result.QuestionID,
	})
}
