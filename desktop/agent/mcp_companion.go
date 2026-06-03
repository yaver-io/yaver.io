package main

import (
	"encoding/json"
	"fmt"
	"strings"
)

// mcp_companion.go — MCP verbs for the companion engine. Mirrors mcp_phone.go:
// tool defs appended into the master list (mcp_tools.go), dispatch added beside
// dispatchPhoneMCP (httpserver.go).

func companionMCPTools() []map[string]interface{} {
	repoProp := map[string]interface{}{
		"type":     "object",
		"required": []string{"repo"},
		"properties": map[string]interface{}{
			"repo": map[string]interface{}{"type": "string", "description": "Absolute path to the serverless project repo"},
		},
	}
	projectProp := map[string]interface{}{
		"type":     "object",
		"required": []string{"project"},
		"properties": map[string]interface{}{
			"project": map[string]interface{}{"type": "string", "description": "Companion project slug"},
		},
	}
	return []map[string]interface{}{
		{
			"name":        "companion_detect",
			"description": "Scan a serverless project repo (Supabase / Convex / Cloudflare Workers) and propose a yaver.companion.yaml: token-authed cron endpoints that have no scheduler, missing subscription-reconcile sweeps, and long-running workers. Read-only — never writes the repo. Returns proposed manifest + reasoned items.",
			"inputSchema": repoProp,
		},
		{
			"name":        "companion_up",
			"description": "Arm the companion manifest (yaver.companion.yaml) at repo: schedule HTTP crons on the in-process scheduler and start/instal durable workers as OS units. Idempotent. Reboot-durable.",
			"inputSchema": repoProp,
		},
		{
			"name":        "companion_down",
			"description": "Disarm a companion project: remove its scheduled crons and stop/remove its durable service units.",
			"inputSchema": projectProp,
		},
		{
			"name":        "companion_status",
			"description": "Show a companion project's live state: each cron's schedule, next run, last outcome, and each durable service.",
			"inputSchema": projectProp,
		},
		{
			"name":        "companion_cron_list",
			"description": "List the armed crons for a companion project with their schedules and last outcomes.",
			"inputSchema": projectProp,
		},
	}
}

func dispatchCompanionMCP(s *HTTPServer, name string, arguments json.RawMessage) (bool, interface{}) {
	switch name {
	case "companion_detect":
		var args struct {
			Repo string `json:"repo"`
		}
		_ = json.Unmarshal(arguments, &args)
		if strings.TrimSpace(args.Repo) == "" {
			return true, mcpToolError("repo is required")
		}
		m, items, err := DetectCompanion(args.Repo)
		if err != nil {
			return true, mcpToolError(err.Error())
		}
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("Companion detection for %s — %d item(s):\n\n", m.Project, len(items)))
		for _, it := range items {
			sb.WriteString(fmt.Sprintf("- [%s] %s (%s)", it.Kind, it.Name, it.Status))
			if it.Schedule != "" {
				sb.WriteString(" · " + it.Schedule)
			}
			sb.WriteString("\n    " + it.Reason + "\n")
		}
		sb.WriteString("\nReview + arm with companion_up after writing yaver.companion.yaml.")
		return true, mcpToolResult(sb.String())

	case "companion_up":
		var args struct {
			Repo string `json:"repo"`
		}
		_ = json.Unmarshal(arguments, &args)
		if strings.TrimSpace(args.Repo) == "" {
			return true, mcpToolError("repo is required")
		}
		m, err := LoadCompanionManifest(args.Repo)
		if err != nil {
			return true, mcpToolError(err.Error())
		}
		if s == nil {
			return true, mcpToolError("agent server unavailable")
		}
		status, err := s.companionEngine().Up(m)
		if err != nil {
			return true, mcpToolError(err.Error())
		}
		return true, mcpToolJSON(status)

	case "companion_down":
		var args struct {
			Project string `json:"project"`
		}
		_ = json.Unmarshal(arguments, &args)
		if strings.TrimSpace(args.Project) == "" {
			return true, mcpToolError("project is required")
		}
		if s == nil {
			return true, mcpToolError("agent server unavailable")
		}
		if err := s.companionEngine().Down(args.Project); err != nil {
			return true, mcpToolError(err.Error())
		}
		return true, mcpToolResult(fmt.Sprintf("Companion project %q disarmed.", args.Project))

	case "companion_status", "companion_cron_list":
		var args struct {
			Project string `json:"project"`
		}
		_ = json.Unmarshal(arguments, &args)
		if strings.TrimSpace(args.Project) == "" {
			return true, mcpToolError("project is required")
		}
		if s == nil {
			return true, mcpToolError("agent server unavailable")
		}
		status, err := s.companionEngine().Status(args.Project)
		if err != nil {
			return true, mcpToolError(err.Error())
		}
		if name == "companion_cron_list" {
			return true, mcpToolJSON(status.Crons)
		}
		return true, mcpToolJSON(status)
	}
	return false, nil
}
