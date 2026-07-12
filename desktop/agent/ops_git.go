package main

// ops_git.go — Phase D3: the secure ops contract for pushing the
// user's already-connected GitHub/GitLab creds onto a managed (or any
// owned) box. Thin wrapper over the existing mcpGitPushCreds, which
// already enforces the security-critical invariants:
//   - only OWNED remote devices (ownership checked agent-side),
//   - self is always excluded,
//   - tokens go device→device via /machine/onboarding/apply and
//     NEVER touch Convex.
// The UI is just a trigger with an explicit deviceId — this verb does
// not invent a target; the cloudMachines→agent-deviceId mapping is
// resolved by the caller from the device list, never guessed here
// (a credentials push must target an explicit, owned device id).

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
)

func init() {
	registerOpsVerb(opsVerbSpec{
		Name:        "git_push",
		Description: "Push locally-connected GitHub/GitLab credentials to one owned remote box (e.g. a managed cloud box) so it can clone/pull/deploy. Wraps git_push_creds: owned-only, self-excluded, tokens never reach Convex. Requires an explicit deviceId.",
		Schema: map[string]interface{}{
			"type":     "object",
			"required": []string{"deviceId"},
			"properties": map[string]interface{}{
				"deviceId": map[string]interface{}{"type": "string", "description": "Target owned device id/alias (the managed box)"},
				"provider": map[string]interface{}{"type": "string", "enum": []string{"github", "gitlab", "all"}, "description": "default all"},
			},
			"additionalProperties": false,
		},
		Handler:    opsGitPushHandler,
		Streaming:  false,
		AllowGuest: false,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "git_connect",
		Description: "Start a first-class GitHub/GitLab OAuth Device Flow (RFC 8628). Returns {user_code, verification_uri} to open in any browser. Pass deviceId to run the flow ON a newly-provisioned box (self-hosted or managed) so that box gets its own git creds first-class. Token is persisted to git-credentials.json plus the local vault on the target (P2P) and NEVER reaches Convex. Poll git_connect_status.",
		Schema: map[string]interface{}{
			"type":     "object",
			"required": []string{"provider"},
			"properties": map[string]interface{}{
				"provider": map[string]interface{}{"type": "string", "enum": []string{"github", "gitlab"}},
				"host":     map[string]interface{}{"type": "string", "description": "default github.com / gitlab.com"},
				"deviceId": map[string]interface{}{"type": "string", "description": "Optional owned box to run the flow on (its creds, P2P)"},
			},
			"additionalProperties": false,
		},
		Handler:    opsGitConnectHandler,
		Streaming:  false,
		AllowGuest: false,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "git_connect_status",
		Description: "Poll an in-flight git_connect Device Flow session. state ∈ {pending,done,error,expired,unknown}; username on success. Pass the same deviceId if git_connect targeted a box.",
		Schema: map[string]interface{}{
			"type":     "object",
			"required": []string{"sessionId"},
			"properties": map[string]interface{}{
				"sessionId": map[string]interface{}{"type": "string"},
				"deviceId":  map[string]interface{}{"type": "string"},
			},
			"additionalProperties": false,
		},
		Handler:    opsGitConnectStatusHandler,
		Streaming:  false,
		AllowGuest: false,
	})
}

// opsGitConnectHandler mirrors the git_oauth_start MCP path: local
// device-flow, or proxied onto an owned box (deviceId) so that box
// gets its own first-class git creds. Token stays on the target
// (P2P), never Convex.
func opsGitConnectHandler(_ OpsContext, payload json.RawMessage) OpsResult {
	var p struct {
		Provider string `json:"provider"`
		Host     string `json:"host"`
		DeviceID string `json:"deviceId"`
	}
	if err := json.Unmarshal(payload, &p); err != nil {
		return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
	}
	if strings.TrimSpace(p.Provider) == "" {
		return OpsResult{OK: false, Code: "bad_payload", Error: "provider required (github|gitlab)"}
	}
	if dev := strings.TrimSpace(p.DeviceID); dev != "" {
		body, _ := json.Marshal(map[string]string{"provider": p.Provider, "host": p.Host})
		status, raw, err := proxyToDevice(context.Background(), "git_oauth_start", dev, http.MethodPost, "/git/provider/oauth/start", body)
		if err != nil {
			return OpsResult{OK: false, Code: "proxy_error", Error: err.Error()}
		}
		if status/100 != 2 {
			return OpsResult{OK: false, Code: "git_connect_failed", Error: strings.TrimSpace(string(raw))}
		}
		var out map[string]interface{}
		_ = json.Unmarshal(raw, &out)
		return OpsResult{OK: true, Initial: out}
	}
	sess, err := startGitOAuthDevice(context.Background(), p.Provider, p.Host)
	if err != nil {
		return OpsResult{OK: false, Code: "git_connect_failed", Error: err.Error()}
	}
	return OpsResult{OK: true, Initial: map[string]interface{}{
		"sessionId":        sess.ID,
		"provider":         sess.Provider,
		"host":             sess.Host,
		"user_code":        sess.UserCode,
		"verification_uri": sess.VerificationURI,
		"interval":         sess.Interval,
		"expires_at":       sess.ExpiresAt.Unix(),
	}}
}

func opsGitConnectStatusHandler(_ OpsContext, payload json.RawMessage) OpsResult {
	var p struct {
		SessionID string `json:"sessionId"`
		DeviceID  string `json:"deviceId"`
	}
	if err := json.Unmarshal(payload, &p); err != nil {
		return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
	}
	if strings.TrimSpace(p.SessionID) == "" {
		return OpsResult{OK: false, Code: "bad_payload", Error: "sessionId required"}
	}
	if dev := strings.TrimSpace(p.DeviceID); dev != "" {
		path := "/git/provider/oauth/status?session=" + url.QueryEscape(p.SessionID)
		status, raw, err := proxyToDevice(context.Background(), "git_oauth_status", dev, http.MethodGet, path, nil)
		if err != nil {
			return OpsResult{OK: false, Code: "proxy_error", Error: err.Error()}
		}
		if status/100 != 2 {
			return OpsResult{OK: false, Code: "git_status_failed", Error: strings.TrimSpace(string(raw))}
		}
		var out map[string]interface{}
		_ = json.Unmarshal(raw, &out)
		return OpsResult{OK: true, Initial: out}
	}
	sess, ok := getGitOAuthSession(p.SessionID)
	if !ok {
		return OpsResult{OK: true, Initial: map[string]interface{}{"state": "unknown", "error": "session not found"}}
	}
	return OpsResult{OK: true, Initial: map[string]interface{}{
		"sessionId": sess.ID,
		"provider":  sess.Provider,
		"host":      sess.Host,
		"state":     sess.State,
		"username":  sess.Username,
		"error":     sess.Error,
	}}
}

func opsGitPushHandler(_ OpsContext, payload json.RawMessage) OpsResult {
	var p struct {
		DeviceID string `json:"deviceId"`
		Provider string `json:"provider"`
	}
	if err := json.Unmarshal(payload, &p); err != nil {
		return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
	}
	if strings.TrimSpace(p.DeviceID) == "" {
		return OpsResult{OK: false, Code: "bad_payload", Error: "deviceId required (the box to push git creds to)"}
	}
	provider := strings.TrimSpace(p.Provider)
	if provider == "" {
		provider = "all"
	}
	res := mcpGitPushCreds(gitPushCredsMCPArgs{
		DeviceID: strings.TrimSpace(p.DeviceID),
		Provider: provider,
	})
	if m, ok := res.(map[string]interface{}); ok {
		if e, has := m["error"]; has && e != nil {
			return OpsResult{OK: false, Code: "git_push_failed", Error: toStringOps(e), Initial: res}
		}
	}
	return OpsResult{OK: true, Initial: res}
}

func toStringOps(v interface{}) string {
	if s, ok := v.(string); ok {
		return s
	}
	b, _ := json.Marshal(v)
	return string(b)
}
