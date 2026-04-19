package main

// ops_status.go — verb "status": rollup of the agent's current
// state. Synchronous, cheap, used as a health-probe + quick-glance
// by agents that want to know whether there are in-flight runners,
// dev servers, tunnels, or relay sessions before planning work.

import (
	"encoding/json"
)

func init() {
	registerOpsVerb(opsVerbSpec{
		Name:        "status",
		Description: "Rollup of agent state: running tasks, active dev servers, support sessions, tunnel state, relay health. Synchronous.",
		Schema: map[string]interface{}{
			"type":                 "object",
			"properties":           map[string]interface{}{},
			"additionalProperties": false,
		},
		Handler:    opsStatusHandler,
		Streaming:  false,
		AllowGuest: true,
	})
}

func opsStatusHandler(c OpsContext, _ json.RawMessage) OpsResult {
	out := map[string]interface{}{}
	if c.Server == nil {
		return OpsResult{OK: true, Initial: map[string]interface{}{"note": "server context unavailable"}}
	}

	// Tasks — counts only; agents needing details call list_tasks.
	if tm := c.Server.taskMgr; tm != nil {
		all := tm.ListTasks()
		running := 0
		for _, t := range all {
			if t.Status == TaskStatusRunning {
				running++
			}
		}
		out["tasks"] = map[string]interface{}{
			"total":   len(all),
			"running": running,
		}
	}

	// Dev servers.
	if dsm := c.Server.devServerMgr; dsm != nil {
		if st := dsm.Status(); st != nil {
			out["devServer"] = st
		} else {
			out["devServer"] = nil
		}
	}

	// Agent version + auth expiry flag (useful for "why aren't my
	// calls going through?" diagnostics). Support-session details
	// belong to /support/status, not the generic ops status.
	out["agentVersion"] = version
	out["authExpired"] = c.Server.authExpired.Load()

	return OpsResult{OK: true, Initial: out}
}
