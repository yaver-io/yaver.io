package main

// ops_runner_auth.go — #9: web-drivable runner OAuth for a managed
// box. Pure transport wrapper over the EXISTING, tested
// mcpRunnerAuth* / mcpRunnerBrowserAuth* functions the MCP tools
// already call — NO new auth logic. Those functions already enforce
// the security-critical invariants (owned-only remote routing via
// deviceID, subscription-token paths, tokens go device→device and
// never touch Convex). The web just needs an ops verb (callOps) it
// can drive the same way it drives git_connect; runner-auth was only
// exposed via MCP/HTTP, so this adds the missing ops surface.
//
// Flow the web uses (mirrors ManagedMachineActions git_connect):
//   browser_start → {verification_uri,user_code,sessionId}
//   poll browser_status → state ∈ {pending,done,error}
//   on done → Convex POST /billing/yaver-cloud/runners-authorized
// Or the friction-free path: credentials_import (copy a locally
// signed-in subscription token to the box). Subscription only.

import (
	"encoding/json"
	"strings"
)

func init() {
	registerOpsVerb(opsVerbSpec{
		Name:        "runner_auth",
		Description: "Authorize a coding runner (claude/codex/opencode) ON an owned box (e.g. a managed cloud box) so it can run agents. op=browser_start returns {verification_uri,user_code,sessionId} to open in any browser; poll op=browser_status; op=credentials_import copies a locally signed-in subscription token to the box (preferred — never API keys). Pass deviceId to target the box; remote routing + ownership are enforced by the underlying runner-auth layer, tokens go device→device and never reach Convex.",
		Schema: map[string]interface{}{
			"type":     "object",
			"required": []string{"op"},
			"properties": map[string]interface{}{
				"op": map[string]interface{}{
					"type": "string",
					"enum": []string{
						"status", "browser_start", "browser_status",
						"submit_code", "cancel", "credentials_import",
					},
				},
				"deviceId":        map[string]interface{}{"type": "string", "description": "Target owned device id (the managed box). Omit for local."},
				"runner":          map[string]interface{}{"type": "string", "enum": []string{"claude", "claude-code", "codex", "opencode"}},
				"sessionId":       map[string]interface{}{"type": "string", "description": "From browser_start; for browser_status/submit_code/cancel."},
				"code":            map[string]interface{}{"type": "string", "description": "Auth code/token for submit_code."},
				"credentialsJson": map[string]interface{}{"type": "string", "description": "Subscription credentials blob for credentials_import."},
			},
			"additionalProperties": false,
		},
		Handler:    opsRunnerAuthHandler,
		Streaming:  false,
		AllowGuest: false,
	})
}

// wrapMCPResult turns the interface{} an mcp* runner-auth function
// returns into an OpsResult. The mcp helpers return a map (often with
// an "error" key on failure); reflect that into OK.
func wrapMCPResult(v interface{}) OpsResult {
	raw, err := json.Marshal(v)
	if err != nil {
		return OpsResult{OK: false, Code: "internal", Error: "marshal: " + err.Error()}
	}
	var m map[string]interface{}
	if e := json.Unmarshal(raw, &m); e != nil {
		// Non-object result — still surface it.
		return OpsResult{OK: true, Initial: map[string]interface{}{"result": json.RawMessage(raw)}}
	}
	if es, ok := m["error"].(string); ok && strings.TrimSpace(es) != "" {
		return OpsResult{OK: false, Code: "runner_auth_failed", Error: es}
	}
	if b, ok := m["ok"].(bool); ok && !b {
		msg, _ := m["error"].(string)
		if msg == "" {
			msg = "runner auth failed"
		}
		return OpsResult{OK: false, Code: "runner_auth_failed", Error: msg}
	}
	return OpsResult{OK: true, Initial: m}
}

func opsRunnerAuthHandler(_ OpsContext, payload json.RawMessage) OpsResult {
	var p struct {
		Op              string `json:"op"`
		DeviceID        string `json:"deviceId"`
		Runner          string `json:"runner"`
		SessionID       string `json:"sessionId"`
		Code            string `json:"code"`
		CredentialsJSON string `json:"credentialsJson"`
	}
	if err := json.Unmarshal(payload, &p); err != nil {
		return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
	}
	switch strings.TrimSpace(p.Op) {
	case "status":
		return wrapMCPResult(mcpRunnerAuthStatus(p.DeviceID))
	case "browser_start":
		if strings.TrimSpace(p.Runner) == "" {
			return OpsResult{OK: false, Code: "bad_payload", Error: "runner required"}
		}
		return wrapMCPResult(mcpRunnerBrowserAuthStart(p.DeviceID, p.Runner))
	case "browser_status":
		if strings.TrimSpace(p.SessionID) == "" {
			return OpsResult{OK: false, Code: "bad_payload", Error: "sessionId required"}
		}
		return wrapMCPResult(mcpRunnerBrowserAuthStatus(p.DeviceID, p.SessionID))
	case "submit_code":
		if strings.TrimSpace(p.SessionID) == "" || strings.TrimSpace(p.Code) == "" {
			return OpsResult{OK: false, Code: "bad_payload", Error: "sessionId and code required"}
		}
		return wrapMCPResult(mcpRunnerBrowserAuthSubmitCode(p.DeviceID, p.SessionID, p.Code))
	case "cancel":
		if strings.TrimSpace(p.SessionID) == "" {
			return OpsResult{OK: false, Code: "bad_payload", Error: "sessionId required"}
		}
		return wrapMCPResult(mcpRunnerBrowserAuthCancel(p.DeviceID, p.SessionID))
	case "credentials_import":
		if strings.TrimSpace(p.Runner) == "" || strings.TrimSpace(p.CredentialsJSON) == "" {
			return OpsResult{OK: false, Code: "bad_payload", Error: "runner and credentialsJson required"}
		}
		return wrapMCPResult(mcpRunnerAuthCredentialsImport(p.DeviceID, p.Runner, p.CredentialsJSON))
	default:
		return OpsResult{OK: false, Code: "bad_payload", Error: "unknown op"}
	}
}
