package main

// agent_render_request.go implements the runner-only yaver_request_render MCP
// tool. Render intent is structured data; an English phrase in stdout remains
// only a compatibility fallback for old runners.

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

type taskRenderRequest struct {
	Reason  string `json:"reason"`
	Summary string `json:"summary,omitempty"`
}

func normalizeTaskRenderReason(reason string) string {
	switch strings.ToLower(strings.TrimSpace(reason)) {
	case "ui-change", "web-preview", "native-preview", "user-request":
		return strings.ToLower(strings.TrimSpace(reason))
	default:
		return ""
	}
}

func (s *HTTPServer) requestTaskRender(w http.ResponseWriter, r *http.Request, taskID string) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}
	task, ok := s.taskMgr.GetTask(taskID)
	if !ok || task == nil {
		jsonError(w, http.StatusNotFound, "task not found")
		return
	}
	var body taskRenderRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	reason := normalizeTaskRenderReason(body.Reason)
	if reason == "" {
		jsonError(w, http.StatusBadRequest, "reason must be ui-change, web-preview, native-preview, or user-request")
		return
	}
	summary := strings.TrimSpace(body.Summary)
	if len(summary) > 500 {
		summary = summary[:500]
	}
	emitRuntimeRenderRequested(task, "agent-"+reason, summary)
	jsonReply(w, http.StatusOK, map[string]interface{}{
		"ok": true, "taskId": taskID, "queued": true, "reason": "agent-" + reason,
	})
}

func forwardYaverRequestRender(rawArgs json.RawMessage) interface{} {
	var args taskRenderRequest
	if err := json.Unmarshal(rawArgs, &args); err != nil {
		return mcpToolError("invalid arguments: " + err.Error())
	}
	if normalizeTaskRenderReason(args.Reason) == "" {
		return mcpToolError("reason must be ui-change, web-preview, native-preview, or user-request")
	}
	taskID := strings.TrimSpace(os.Getenv("YAVER_TASK_ID"))
	if taskID == "" {
		return mcpToolError("yaver_request_render is only available inside a Yaver task (YAVER_TASK_ID not set)")
	}
	cfg, err := LoadConfig()
	if err != nil || cfg == nil || strings.TrimSpace(cfg.AuthToken) == "" {
		return mcpToolError("yaver_request_render: not authenticated (run `yaver auth`)")
	}
	body, _ := json.Marshal(args)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, localAgentBaseURL()+"/tasks/"+taskID+"/render-request", bytes.NewReader(body))
	if err != nil {
		return mcpToolError("build request: " + err.Error())
	}
	req.Header.Set("Authorization", "Bearer "+cfg.AuthToken)
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		return mcpToolError(fmt.Sprintf("forward render request to daemon: %v", err))
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return mcpToolError(fmt.Sprintf("daemon returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw))))
	}
	return mcpToolJSON(map[string]interface{}{
		"queued": true, "taskId": taskID,
		"hint": "finish the coding turn normally; Yaver will offer or perform one render according to the user's setting",
	})
}
