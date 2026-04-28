package main

// ops_runner.go — verb "runner": unified-runner surface over MCP
// (RUNNER_DEV.md Phase 1 + Phase 2).
//
// One verb, many ops:
//
// Phase 1 (jobs + runs):
//   ops("local", "runner", {op: "list"})                          — every job
//   ops("local", "runner", {op: "pools"})                         — local capability tags
//   ops("local", "runner", {op: "add",     job: {...}})           — upsert a job
//   ops("local", "runner", {op: "remove",  name: "x"})            — delete a job
//   ops("local", "runner", {op: "trigger", name: "x"})            — manual fire (sync)
//   ops("local", "runner", {op: "pause",   name: "x", paused: true})
//   ops("local", "runner", {op: "runs",    name?: "x", limit?: N}) — history
//   ops("local", "runner", {op: "log",     id: "<runId>"})        — full log text
//
// Phase 2 (sandbox + agent sessions):
//   ops("local", "runner", {op: "sandbox_start",        sandbox: {...}})
//   ops("local", "runner", {op: "sandbox_exec",         id, exec: {...}})
//   ops("local", "runner", {op: "sandbox_file_read",    id, path})
//   ops("local", "runner", {op: "sandbox_file_write",   id, path, content})
//   ops("local", "runner", {op: "sandbox_stop",         id})
//   ops("local", "runner", {op: "sandboxes_list"})
//   ops("local", "runner", {op: "agent_start",          agent: {...}})
//   ops("local", "runner", {op: "agent_message",        id, text})
//   ops("local", "runner", {op: "agent_cancel",         id})
//   ops("local", "runner", {op: "agent_get",            id})
//   ops("local", "runner", {op: "agents_list"})
//
// Owner-only in Phase 1+2; cross-machine routing handled by the
// dispatcher (verb-as-payload) so a `machine: <deviceId>` flips to
// the peer-proxy path automatically.

import (
	"encoding/base64"
	"encoding/json"
	"strings"
)

type opsRunnerPayload struct {
	Op     string     `json:"op"`
	Name   string     `json:"name,omitempty"`
	Job    *RunnerJob `json:"job,omitempty"`
	Limit  int        `json:"limit,omitempty"`
	Paused bool       `json:"paused,omitempty"`
	ID     string     `json:"id,omitempty"`
	Pool   string     `json:"pool,omitempty"`
	// Phase 2 — sandbox.
	Sandbox *SandboxStartOpts `json:"sandbox,omitempty"`
	Exec    *SandboxExecOpts  `json:"exec,omitempty"`
	Path    string            `json:"path,omitempty"`
	Content string            `json:"content,omitempty"` // base64 for files; plain for messages
	// Phase 2 — agent sessions.
	Agent *AgentSessionStartOpts `json:"agent,omitempty"`
	Text  string                 `json:"text,omitempty"`
	// Reserved for future ops; ignored.
	Extra json.RawMessage `json:"extra,omitempty"`
}

func init() {
	registerOpsVerb(opsVerbSpec{
		Name:        "runner",
		Description: "Unified runner surface (RUNNER_DEV.md). One verb covers list/add/remove/trigger/pause/runs/log/pools — each chooses via {op}. Phase 1 supports shell jobs only; future kinds (docker/agent/playwright/gpu) extend the same payload shape.",
		Schema: map[string]interface{}{
			"type":     "object",
			"required": []string{"op"},
			"properties": map[string]interface{}{
				"op": map[string]interface{}{
					"type": "string",
					"enum": []string{
						"list", "pools", "add", "remove", "trigger", "pause", "runs", "log",
						"sandbox_start", "sandbox_exec", "sandbox_file_read", "sandbox_file_write", "sandbox_stop", "sandboxes_list",
						"agent_start", "agent_message", "agent_cancel", "agent_get", "agents_list",
					},
				},
				"name":   map[string]interface{}{"type": "string"},
				"id":     map[string]interface{}{"type": "string"},
				"limit":  map[string]interface{}{"type": "integer"},
				"paused": map[string]interface{}{"type": "boolean"},
				"pool":   map[string]interface{}{"type": "string"},
				"job": map[string]interface{}{
					"type":        "object",
					"description": "RunnerJob payload (see runner.go). Required for op=add.",
				},
			},
			"additionalProperties": false,
		},
		Handler:    opsRunnerHandler,
		Streaming:  false,
		AllowGuest: false,
	})
}

func opsRunnerHandler(c OpsContext, payload json.RawMessage) OpsResult {
	if c.Server == nil {
		return OpsResult{OK: false, Code: "internal", Error: "ops runner verb needs an HTTPServer context"}
	}
	var p opsRunnerPayload
	if len(payload) > 0 {
		if err := json.Unmarshal(payload, &p); err != nil {
			return OpsResult{OK: false, Code: "bad_payload", Error: "invalid payload: " + err.Error()}
		}
	}
	op := strings.TrimSpace(p.Op)
	if op == "" {
		return OpsResult{OK: false, Code: "bad_payload", Error: "op is required"}
	}
	store := c.Server.ensureRunnerStore()

	switch op {
	case "list":
		jobs := store.ListJobs(p.Pool)
		return OpsResult{OK: true, Initial: map[string]interface{}{
			"jobs":  jobs,
			"count": len(jobs),
		}}
	case "pools":
		caps := LocalCapabilities()
		return OpsResult{OK: true, Initial: map[string]interface{}{
			"pools": caps,
			"local": true,
		}}
	case "add":
		if p.Job == nil {
			return OpsResult{OK: false, Code: "bad_payload", Error: "job is required for op=add"}
		}
		stored, err := store.AddJob(*p.Job)
		if err != nil {
			return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
		}
		return OpsResult{OK: true, Initial: map[string]interface{}{"job": stored}}
	case "remove":
		if p.Name == "" {
			return OpsResult{OK: false, Code: "bad_payload", Error: "name is required for op=remove"}
		}
		if err := store.RemoveJob(p.Name); err != nil {
			return OpsResult{OK: false, Code: "not_found", Error: err.Error()}
		}
		return OpsResult{OK: true, Initial: map[string]bool{"removed": true}}
	case "pause":
		if p.Name == "" {
			return OpsResult{OK: false, Code: "bad_payload", Error: "name is required for op=pause"}
		}
		if err := store.SetPaused(p.Name, p.Paused); err != nil {
			return OpsResult{OK: false, Code: "not_found", Error: err.Error()}
		}
		return OpsResult{OK: true, Initial: map[string]bool{"paused": p.Paused}}
	case "trigger":
		if p.Name == "" {
			return OpsResult{OK: false, Code: "bad_payload", Error: "name is required for op=trigger"}
		}
		job, ok := store.GetJob(p.Name)
		if !ok {
			return OpsResult{OK: false, Code: "not_found", Error: "job not found"}
		}
		caps := LocalCapabilities()
		if !PoolMatches(job.Pool, caps) {
			return OpsResult{OK: false, Code: "pool_mismatch", Error: "this agent does not match the job's pool"}
		}
		if job.Kind != RunnerJobShell {
			return OpsResult{OK: false, Code: "kind_unsupported", Error: "Phase 1 ships shell only — see RUNNER_DEV.md"}
		}
		// MCP routes are always owner-auth in Phase 1 (AllowGuest:false above).
		final, err := runJobShell(c.Ctx, store, job, "owner", false, c.Server.vaultStore)
		if err != nil {
			return OpsResult{OK: false, Code: "exec_failed", Error: err.Error(), Initial: final}
		}
		final.LogPath = ""
		return OpsResult{OK: final.OK, Initial: map[string]interface{}{"run": final}}
	case "runs":
		limit := p.Limit
		if limit <= 0 {
			limit = 50
		}
		runs := store.ListRuns(p.Name, "", limit)
		for i := range runs {
			runs[i].LogPath = ""
		}
		return OpsResult{OK: true, Initial: map[string]interface{}{
			"runs":  runs,
			"count": len(runs),
		}}
	case "log":
		if p.ID == "" {
			return OpsResult{OK: false, Code: "bad_payload", Error: "id is required for op=log"}
		}
		run, ok := store.GetRun(p.ID, "")
		if !ok {
			return OpsResult{OK: false, Code: "not_found", Error: "run not found"}
		}
		// Tail-only over MCP — consumers that want the full file
		// should hit GET /runner/runs/{id}/log and stream.
		run.LogPath = ""
		return OpsResult{OK: true, Initial: map[string]interface{}{
			"run":  run,
			"tail": run.OutputTail,
		}}

	// --- Phase 2: sandbox ---
	case "sandbox_start":
		mgr := c.Server.ensureSandboxManager()
		if mgr == nil {
			return OpsResult{OK: false, Code: "unavailable", Error: "sandbox manager not available — Docker missing on this agent"}
		}
		opts := SandboxStartOpts{}
		if p.Sandbox != nil {
			opts = *p.Sandbox
		}
		sess, err := mgr.Start(c.Ctx, opts, c.Server.ownerUserID)
		if err != nil {
			return OpsResult{OK: false, Code: "exec_failed", Error: err.Error()}
		}
		return OpsResult{OK: true, Initial: map[string]interface{}{"session": sess}}
	case "sandbox_exec":
		mgr := c.Server.ensureSandboxManager()
		if mgr == nil {
			return OpsResult{OK: false, Code: "unavailable", Error: "sandbox manager not available"}
		}
		if p.ID == "" || p.Exec == nil {
			return OpsResult{OK: false, Code: "bad_payload", Error: "id and exec are required"}
		}
		res, err := mgr.Exec(c.Ctx, p.ID, *p.Exec, "")
		if err != nil {
			return OpsResult{OK: false, Code: "exec_failed", Error: err.Error()}
		}
		return OpsResult{OK: res.OK, Initial: res}
	case "sandbox_file_read":
		mgr := c.Server.ensureSandboxManager()
		if mgr == nil {
			return OpsResult{OK: false, Code: "unavailable", Error: "sandbox manager not available"}
		}
		if p.ID == "" || p.Path == "" {
			return OpsResult{OK: false, Code: "bad_payload", Error: "id and path are required"}
		}
		data, err := mgr.ReadFile(c.Ctx, p.ID, p.Path, "")
		if err != nil {
			return OpsResult{OK: false, Code: "exec_failed", Error: err.Error()}
		}
		return OpsResult{OK: true, Initial: map[string]interface{}{
			"path":    p.Path,
			"content": base64.StdEncoding.EncodeToString(data),
			"bytes":   len(data),
		}}
	case "sandbox_file_write":
		mgr := c.Server.ensureSandboxManager()
		if mgr == nil {
			return OpsResult{OK: false, Code: "unavailable", Error: "sandbox manager not available"}
		}
		if p.ID == "" || p.Path == "" {
			return OpsResult{OK: false, Code: "bad_payload", Error: "id and path are required"}
		}
		raw, err := base64.StdEncoding.DecodeString(p.Content)
		if err != nil {
			return OpsResult{OK: false, Code: "bad_payload", Error: "content must be base64: " + err.Error()}
		}
		if err := mgr.WriteFile(c.Ctx, p.ID, p.Path, raw, ""); err != nil {
			return OpsResult{OK: false, Code: "exec_failed", Error: err.Error()}
		}
		return OpsResult{OK: true, Initial: map[string]interface{}{"bytes": len(raw)}}
	case "sandbox_stop":
		mgr := c.Server.ensureSandboxManager()
		if mgr == nil {
			return OpsResult{OK: false, Code: "unavailable", Error: "sandbox manager not available"}
		}
		if p.ID == "" {
			return OpsResult{OK: false, Code: "bad_payload", Error: "id is required"}
		}
		if err := mgr.StopSandbox(p.ID); err != nil {
			return OpsResult{OK: false, Code: "not_found", Error: err.Error()}
		}
		return OpsResult{OK: true, Initial: map[string]bool{"stopped": true}}
	case "sandboxes_list":
		mgr := c.Server.ensureSandboxManager()
		if mgr == nil {
			return OpsResult{OK: true, Initial: map[string]interface{}{
				"available": false,
				"sessions":  []SandboxSession{},
				"count":     0,
			}}
		}
		return OpsResult{OK: true, Initial: mgr.Snapshot("")}

	// --- Phase 2: agent sessions ---
	case "agent_start":
		mgr := c.Server.ensureAgentSessionManager()
		if mgr == nil {
			return OpsResult{OK: false, Code: "unavailable", Error: "agent session manager not available"}
		}
		if p.Agent == nil {
			return OpsResult{OK: false, Code: "bad_payload", Error: "agent is required for agent_start"}
		}
		sess, err := mgr.Create(*p.Agent, c.Server.ownerUserID)
		if err != nil {
			return OpsResult{OK: false, Code: "exec_failed", Error: err.Error()}
		}
		return OpsResult{OK: true, Initial: map[string]interface{}{"session": sess}}
	case "agent_message":
		mgr := c.Server.ensureAgentSessionManager()
		if mgr == nil {
			return OpsResult{OK: false, Code: "unavailable", Error: "agent session manager not available"}
		}
		if p.ID == "" || strings.TrimSpace(p.Text) == "" {
			return OpsResult{OK: false, Code: "bad_payload", Error: "id and text are required"}
		}
		sess, err := mgr.Message(p.ID, p.Text, "")
		if err != nil {
			return OpsResult{OK: false, Code: "exec_failed", Error: err.Error()}
		}
		return OpsResult{OK: true, Initial: map[string]interface{}{"session": sess}}
	case "agent_cancel":
		mgr := c.Server.ensureAgentSessionManager()
		if mgr == nil {
			return OpsResult{OK: false, Code: "unavailable", Error: "agent session manager not available"}
		}
		if p.ID == "" {
			return OpsResult{OK: false, Code: "bad_payload", Error: "id is required"}
		}
		if err := mgr.Cancel(p.ID, ""); err != nil {
			return OpsResult{OK: false, Code: "not_found", Error: err.Error()}
		}
		return OpsResult{OK: true, Initial: map[string]bool{"cancelled": true}}
	case "agent_get":
		mgr := c.Server.ensureAgentSessionManager()
		if mgr == nil {
			return OpsResult{OK: false, Code: "unavailable", Error: "agent session manager not available"}
		}
		if p.ID == "" {
			return OpsResult{OK: false, Code: "bad_payload", Error: "id is required"}
		}
		sess, ok := mgr.Get(p.ID, "")
		if !ok {
			return OpsResult{OK: false, Code: "not_found", Error: "session not found"}
		}
		return OpsResult{OK: true, Initial: map[string]interface{}{"session": sess}}
	case "agents_list":
		mgr := c.Server.ensureAgentSessionManager()
		if mgr == nil {
			return OpsResult{OK: true, Initial: map[string]interface{}{"sessions": []AgentSession{}, "count": 0}}
		}
		sessions := mgr.List("")
		return OpsResult{OK: true, Initial: map[string]interface{}{
			"sessions": sessions,
			"count":    len(sessions),
		}}

	default:
		return OpsResult{OK: false, Code: "bad_payload", Error: "unknown op: " + op}
	}
}
