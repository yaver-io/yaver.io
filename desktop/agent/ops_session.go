package main

// ops_session.go — verb "session": list / export / import / transfer /
// handoff AI coding sessions. Thin proxy over the existing
// session_export / session_import / session_transfer / session_handoff
// MCP handlers so agents can drive the full lifecycle with one verb.
//
// The heavy lifting (moving chat history, agent-specific state files,
// workspace dirs, sentinel files for graceful source exit) all lives
// in handoff.go + transfer.go — this verb is a router.

import (
	"encoding/json"
	"fmt"
)

type opsSessionPayload struct {
	// Op: "list" | "export" | "import" | "transfer" | "handoff"
	Op string `json:"op"`
	// For export:
	ID      string `json:"id,omitempty"`
	Runner  string `json:"runner,omitempty"`  // "claude-code" | "codex" | "aider" | ...
	WorkDir string `json:"workDir,omitempty"`
	// For import/transfer/handoff:
	Bundle    json.RawMessage `json:"bundle,omitempty"`
	ToDevice  string          `json:"toDevice,omitempty"`
	FromAgent string          `json:"fromAgent,omitempty"`
	// For handoff:
	Message     string `json:"message,omitempty"`
	StopSource  bool   `json:"stopSource,omitempty"`
	MaxKicks    int    `json:"maxKicks,omitempty"`
	DeadlineSec int    `json:"deadlineSec,omitempty"`
}

func init() {
	registerOpsVerb(opsVerbSpec{
		Name:        "session",
		Description: "List / export / import / transfer / handoff AI coding sessions. op-discriminated: list (all live), export {id}, import {bundle}, transfer {id, toDevice}, handoff {runner, workDir, ...}.",
		Schema: map[string]interface{}{
			"type":     "object",
			"required": []string{"op"},
			"properties": map[string]interface{}{
				"op":          map[string]interface{}{"type": "string", "enum": []string{"list", "export", "import", "transfer", "handoff"}},
				"id":          map[string]interface{}{"type": "string"},
				"runner":      map[string]interface{}{"type": "string"},
				"workDir":     map[string]interface{}{"type": "string"},
				"bundle":      map[string]interface{}{"type": "object"},
				"toDevice":    map[string]interface{}{"type": "string"},
				"fromAgent":   map[string]interface{}{"type": "string"},
				"message":     map[string]interface{}{"type": "string"},
				"stopSource":  map[string]interface{}{"type": "boolean"},
				"maxKicks":    map[string]interface{}{"type": "integer"},
				"deadlineSec": map[string]interface{}{"type": "integer"},
			},
			"additionalProperties": false,
		},
		Handler:    opsSessionHandler,
		Streaming:  false,
		AllowGuest: false, // sessions contain owner-only chat + vault references
	})
}

func opsSessionHandler(c OpsContext, payload json.RawMessage) OpsResult {
	var p opsSessionPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
	}
	if p.Op == "" {
		return OpsResult{OK: false, Code: "bad_payload", Error: "op is required"}
	}
	if c.Server == nil || c.Server.taskMgr == nil {
		return OpsResult{OK: false, Code: "unavailable", Error: "task manager not initialised"}
	}

	switch p.Op {
	case "list":
		// Reuse the same snapshot that session_list returns. The full
		// implementation lives in httpserver.go; here we mirror the
		// shape so agents get one schema whether they call the domain
		// tool or ops.
		tasks := c.Server.taskMgr.ListTasks()
		out := make([]map[string]interface{}, 0, len(tasks))
		for _, t := range tasks {
			out = append(out, map[string]interface{}{
				"id":        t.ID,
				"title":     t.Title,
				"status":    t.Status,
				"runner":    t.RunnerID,
				"source":    t.Source,
				"createdAt": t.CreatedAt,
			})
		}
		return OpsResult{OK: true, Initial: map[string]interface{}{"count": len(out), "sessions": out}}

	case "export":
		if p.ID == "" {
			return OpsResult{OK: false, Code: "bad_payload", Error: "id required for export"}
		}
		// Defer to the existing export path. The agent's /session/export
		// + session_export MCP tool is the canonical implementation;
		// here we surface the pointer so the caller hits it directly.
		// A future revision inlines the marshalling; today the pointer
		// is the contract because the export format is evolving.
		return OpsResult{OK: true, Initial: map[string]interface{}{
			"hint":      fmt.Sprintf("POST /session/export with {taskId:%q} to get the full bundle", p.ID),
			"mcpTool":   "session_export",
			"payloadId": p.ID,
		}}

	case "import":
		if len(p.Bundle) == 0 {
			return OpsResult{OK: false, Code: "bad_payload", Error: "bundle required for import"}
		}
		return OpsResult{OK: true, Initial: map[string]interface{}{
			"hint":    "POST /session/import with the bundle to materialise a new task + its agent state",
			"mcpTool": "session_import",
		}}

	case "transfer":
		if p.ID == "" || p.ToDevice == "" {
			return OpsResult{OK: false, Code: "bad_payload", Error: "id and toDevice required for transfer"}
		}
		return OpsResult{OK: true, Initial: map[string]interface{}{
			"hint":     fmt.Sprintf("POST /session/transfer {taskId:%q, toDevice:%q}", p.ID, p.ToDevice),
			"mcpTool":  "session_transfer",
			"id":       p.ID,
			"toDevice": p.ToDevice,
		}}

	case "handoff":
		if p.Runner == "" {
			return OpsResult{OK: false, Code: "bad_payload", Error: "runner required for handoff"}
		}
		return OpsResult{OK: true, Initial: map[string]interface{}{
			"hint":      "POST /session/handoff to kick an autodev loop + write sentinel for source exit",
			"mcpTool":   "session_handoff",
			"runner":    p.Runner,
			"workDir":   p.WorkDir,
			"maxKicks":  p.MaxKicks,
			"deadline":  p.DeadlineSec,
			"stopSrc":   p.StopSource,
			"message":   p.Message,
		}}
	default:
		return OpsResult{OK: false, Code: "bad_payload", Error: "unknown op: " + p.Op}
	}
}
