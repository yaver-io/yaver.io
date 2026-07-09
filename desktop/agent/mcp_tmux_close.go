package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

func (s *HTTPServer) mcpCloseTmuxSessionsAllMachines() interface{} {
	cfg, err := LoadConfig()
	if err != nil || cfg == nil || strings.TrimSpace(cfg.AuthToken) == "" || strings.TrimSpace(cfg.ConvexSiteURL) == "" {
		return mcpToolError("Not signed in. Run 'yaver auth' first.")
	}
	devices, err := listDevices(cfg.ConvexSiteURL, cfg.AuthToken)
	if err != nil {
		return mcpToolError(fmt.Sprintf("list devices: %v", err))
	}
	var sb strings.Builder
	count := 0
	for _, d := range devices {
		if d.IsGuest || !d.IsOnline || strings.TrimSpace(d.DeviceID) == "" {
			continue
		}
		count++
		label := firstNonEmpty(d.Alias, d.Name, d.DeviceID)
		status, raw, perr := proxyToDevice(context.Background(), "tmux_close_sessions", d.DeviceID, http.MethodPost, "/runner/sessions/close", nil)
		if perr != nil {
			sb.WriteString(fmt.Sprintf("- %s: failed: %v\n", label, perr))
			continue
		}
		if status >= 300 {
			sb.WriteString(fmt.Sprintf("- %s: HTTP %d: %s\n", label, status, strings.TrimSpace(string(raw))))
			continue
		}
		sb.WriteString("- " + formatCloseSessionsJSON(label, raw) + "\n")
	}
	if count == 0 {
		return mcpToolResult("No online owned machines found.")
	}
	return mcpToolResult(strings.TrimSpace(sb.String()))
}

func formatCloseSessionsJSON(label string, raw []byte) string {
	var out struct {
		Killed []RunnerSessionCloseResult `json:"killed"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return fmt.Sprintf("%s: closed sessions (unparseable response)", label)
	}
	return formatCloseSessionResults(label, out.Killed)
}

func formatCloseSessionResults(label string, results []RunnerSessionCloseResult) string {
	if len(results) == 0 {
		return fmt.Sprintf("%s: no tmux sessions found", label)
	}
	var parts []string
	for _, r := range results {
		name := r.Name
		if r.Runner != "" {
			name += " (" + r.Runner + ")"
		}
		if r.Error != "" {
			name += " failed: " + r.Error
		}
		parts = append(parts, name)
	}
	return fmt.Sprintf("%s: closed %s", label, strings.Join(parts, ", "))
}
