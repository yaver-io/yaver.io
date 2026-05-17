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
	"encoding/json"
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
