package main

// ops_run.go — verb "run": execute a shell command on the target
// machine, stream stdout/stderr, return an exit code. This is the
// escape hatch for anything the typed verb catalogue doesn't cover
// yet, and the primary "drive a remote box" verb vibe coders reach
// for.
//
// Guest sessions are refused at the dispatcher level — run is too
// broad to be scoped safely. The existing /exec + execMgr plumbing
// is reused verbatim so we don't fork subprocess handling.

import (
	"encoding/json"
	"fmt"
	"time"
)

type opsRunPayload struct {
	// Command: the raw command line; passed to `sh -c` by execMgr.
	Command string `json:"command"`
	// WorkDir: optional working directory. Defaults to the agent's cwd.
	WorkDir string `json:"workDir,omitempty"`
	// TimeoutSec: soft deadline. 0 = wait indefinitely (same as exec_command).
	TimeoutSec int `json:"timeoutSec,omitempty"`
}

func init() {
	registerOpsVerb(opsVerbSpec{
		Name:        "run",
		Description: "Execute a shell command on the target machine. Returns exit code + stdout/stderr after completion. For long-running processes, prefer dedicated verbs (build/deploy/logs) that stream progress.",
		Schema: map[string]interface{}{
			"type":     "object",
			"required": []string{"command"},
			"properties": map[string]interface{}{
				"command": map[string]interface{}{
					"type":        "string",
					"description": "Command line to execute (passed to `sh -c`).",
				},
				"workDir": map[string]interface{}{
					"type":        "string",
					"description": "Working directory. Defaults to the agent's CWD.",
				},
				"timeoutSec": map[string]interface{}{
					"type":        "integer",
					"description": "Kill the process after this many seconds. 0 = wait indefinitely.",
				},
			},
			"additionalProperties": false,
		},
		Handler:    opsRunHandler,
		Streaming:  true,
		AllowGuest: false, // never — shell escape is not a guest capability
	})
}

func opsRunHandler(c OpsContext, payload json.RawMessage) OpsResult {
	var p opsRunPayload
	if len(payload) > 0 {
		if err := json.Unmarshal(payload, &p); err != nil {
			return OpsResult{OK: false, Code: "bad_payload", Error: "invalid payload: " + err.Error()}
		}
	}
	if p.Command == "" {
		return OpsResult{OK: false, Code: "bad_payload", Error: "command is required"}
	}
	if c.Server == nil || c.Server.execMgr == nil {
		return OpsResult{OK: false, Code: "unavailable", Error: "exec manager not initialised on this agent"}
	}

	sess, err := c.Server.execMgr.StartExec(p.Command, p.WorkDir, "", nil, p.TimeoutSec)
	if err != nil {
		return OpsResult{OK: false, Code: "exec_failed", Error: err.Error()}
	}

	// Wait for completion with the caller's timeout. 0 means "no wall
	// clock", matches the exec_command MCP tool's semantics so agents
	// see identical behaviour whether they use the old tool or ops.run.
	if p.TimeoutSec > 0 {
		select {
		case <-sess.doneCh:
		case <-time.After(time.Duration(p.TimeoutSec) * time.Second):
			// Let the session finalize naturally — execMgr handles the
			// kill path; we just surface whatever it captured.
		}
	} else {
		<-sess.doneCh
	}

	snapshot := sess.Snapshot()
	result := map[string]interface{}{
		"sessionId": sess.ID,
		"exitCode":  snapshot["exitCode"],
		"stdout":    snapshot["stdout"],
		"stderr":    snapshot["stderr"],
		"startedAt": snapshot["startedAt"],
		"endedAt":   snapshot["endedAt"],
		"command":   p.Command,
		"workDir":   p.WorkDir,
	}
	ok := true
	if code, ok2 := snapshot["exitCode"].(int); ok2 && code != 0 {
		ok = false
	}
	res := OpsResult{OK: ok, Initial: result}
	if !ok {
		// Keep the structured result but also set a short error so
		// agents that only look at `error` get a signal.
		if code, ok2 := snapshot["exitCode"].(int); ok2 {
			res.Error = fmt.Sprintf("command exited %d", code)
			res.Code = "exit_nonzero"
		}
	}
	return res
}
