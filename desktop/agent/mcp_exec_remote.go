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
	if id, ok := snapshot["id"].(string); ok && id != "" {
		fmt.Fprintf(&sb, "Exec ID: %s\n", id)
	}
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
			return remoteExecObservationInterrupted(deviceID, execID, err.Error())
		}
		if status >= 300 {
			return remoteExecObservationInterrupted(deviceID, execID, fmt.Sprintf("HTTP %d", status))
		}
		snapshot, err = decodeRemoteExecSnapshot(raw)
		if err != nil {
			return remoteExecObservationInterrupted(deviceID, execID, err.Error())
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

// A lost poll is not a failed command. In the 2026-09-10 Dogfood audit,
// compilation completed successfully after MCP lost the response. Preserve
// its identity and offer a read-only recovery, never a duplicate execution.
func remoteExecObservationInterrupted(deviceID, execID, detail string) interface{} {
	return mcpToolJSON(map[string]any{
		"ok": false, "code": "EXEC_OBSERVATION_INTERRUPTED",
		"execId": execID, "deviceId": strings.TrimSpace(deviceID), "status": "unknown",
		"message": "The command was started, but its current status could not be read. It may still be running. Do not run it again to recover output.",
		"detail":  detail,
		"action":  map[string]any{"tool": "exec_status", "arguments": map[string]string{"device_id": strings.TrimSpace(deviceID), "exec_id": execID}},
	})
}

// decodeRemoteExecSnapshot parses a /exec/{id} poll response into the exec
// session's snapshot map.
//
// The agent answers `{"ok":true,"exec":{...}}` — the session snapshot lives
// under the `exec` key (httpserver.go handleExecByID → jsonReply(..., {"ok":
// true, "exec": sess.Snapshot()})). Parsing the RAW body as the snapshot made
// every field nil — `snapshot["status"]` was missing, the poll loop broke
// immediately, and the MCP tool returned `Status: <nil>` while the command
// had actually run (2026-08-10, ubuntu-4gb-hel1-1). Unwrap `exec` when
// present; fall back to a flat body for agents that answer without the
// wrapper, so neither shape produces a silent empty result.
func decodeRemoteExecSnapshot(raw []byte) (map[string]any, error) {
	var outer map[string]any
	if err := json.Unmarshal(raw, &outer); err != nil {
		return nil, err
	}
	if exec, ok := outer["exec"].(map[string]any); ok {
		return exec, nil
	}
	return outer, nil
}

func mustJSONBytes(v any) []byte {
	raw, _ := json.Marshal(v)
	return raw
}
