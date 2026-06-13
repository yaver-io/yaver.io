package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

func formatExecSnapshot(snapshot map[string]any) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Status: %v\n", snapshot["status"]))
	if _, ok := snapshot["exitCode"]; ok {
		sb.WriteString(fmt.Sprintf("Exit code: %v\n", snapshot["exitCode"]))
	}
	if stdout, ok := snapshot["stdout"].(string); ok && stdout != "" {
		sb.WriteString("\n--- stdout ---\n")
		sb.WriteString(stdout)
	}
	if stderr, ok := snapshot["stderr"].(string); ok && stderr != "" {
		sb.WriteString("\n--- stderr ---\n")
		sb.WriteString(stderr)
	}
	return sb.String()
}

func mcpRemoteExecCommand(deviceID, command, workDir string, timeout int) interface{} {
	if strings.TrimSpace(command) == "" {
		return mcpToolError("command is required")
	}
	if timeout <= 0 {
		timeout = 300
	}
	if timeout > 3600 {
		timeout = 3600
	}
	body := map[string]any{"command": command, "workDir": workDir, "timeout": timeout}
	status, raw, err := proxyToDevice(context.Background(), "exec_command", strings.TrimSpace(deviceID), http.MethodPost, "/exec", mustJSONBytes(body))
	if err != nil {
		return mcpToolError(fmt.Sprintf("exec_command: %v", err))
	}
	if status >= 300 {
		return mcpToolError(fmt.Sprintf("exec_command: remote returned %d: %s", status, string(raw)))
	}
	var started map[string]any
	if err := json.Unmarshal(raw, &started); err != nil {
		return mcpToolError(fmt.Sprintf("exec_command: decode remote start: %v", err))
	}
	execID, _ := started["execId"].(string)
	if execID == "" {
		return mcpToolError("exec_command: remote did not return execId")
	}
	deadline := time.Now().Add(time.Duration(timeout) * time.Second)
	var snapshot map[string]any
	for {
		status, raw, err = proxyToDevice(context.Background(), "exec_command", strings.TrimSpace(deviceID), http.MethodGet, "/exec/"+execID, nil)
		if err != nil {
			return mcpToolError(fmt.Sprintf("exec_command: poll remote: %v", err))
		}
		if status >= 300 {
			return mcpToolError(fmt.Sprintf("exec_command: poll remote returned %d: %s", status, string(raw)))
		}
		snapshot = map[string]any{}
		if err := json.Unmarshal(raw, &snapshot); err != nil {
			return mcpToolError(fmt.Sprintf("exec_command: decode remote snapshot: %v", err))
		}
		if fmt.Sprint(snapshot["status"]) != "running" {
			break
		}
		if time.Now().After(deadline) {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	if snapshot == nil {
		return mcpToolError("exec_command: no remote snapshot")
	}
	return mcpToolResult(formatExecSnapshot(snapshot))
}

func mustJSONBytes(v any) []byte {
	raw, _ := json.Marshal(v)
	return raw
}
