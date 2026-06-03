package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

const sampleLocalPolicy = `{
  "enabled": true,
  "runtime": { "mode": "dedicated-compute", "defaultProvider": "onprem", "defaultDeviceId": "box-1" },
  "runners": {
    "defaultRunner": "claude",
    "allowedRunners": ["claude", "opencode"],
    "allowUserOverride": true,
    "credentialMode": "user-auth-on-runtime"
  },
  "opencode": { "providers": [
    { "id": "vllm", "label": "On-prem vLLM", "baseUrl": "http://10.0.0.9:8000/v1", "models": ["qwen2.5-coder"], "keyPolicy": "company-secret", "keyConfigured": false }
  ]},
  "mcp": { "enabledServers": ["yaver"], "requiredServers": ["yaver"] },
  "workKinds": { "appCode": true, "convex": true, "robotTrial": false },
  "approvals": { "requireApprovalForDeploy": true, "requireApprovalForSecretsAccess": true },
  "dataPolicy": { "redactPII": true, "retentionDays": 14 }
}`

func writeLocalPolicy(t *testing.T, body string) {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "company-ai-policy.json")
	if err := os.WriteFile(p, []byte(body), 0600); err != nil {
		t.Fatalf("write policy: %v", err)
	}
	t.Setenv("YAVER_COMPANY_AI_POLICY", p)
}

func TestLoadAndResolveLocalPolicy(t *testing.T) {
	writeLocalPolicy(t, sampleLocalPolicy)
	pol, err := LoadLocalCompanyAIPolicy()
	if err != nil || pol == nil {
		t.Fatalf("load: pol=%v err=%v", pol, err)
	}

	// Enabled work kind, default runner, OAuth credential mode, runtime ready.
	res := ResolveCompanyAILocal(pol, "app-code", "", "", "", "")
	if res["ok"] != true {
		t.Fatalf("resolve not ok: %v", res)
	}
	if res["source"] != "local-airgap" {
		t.Fatalf("source = %v", res["source"])
	}
	if res["runtimeReady"] != true {
		t.Fatalf("expected runtimeReady (enabled+workKind+deviceId): %v", res)
	}
	runner := res["runner"].(map[string]interface{})
	if runner["id"] != "claude" {
		t.Fatalf("runner = %v", runner["id"])
	}
	if runner["credentialMode"] != "user-auth-on-runtime" {
		t.Fatalf("credentialMode = %v", runner["credentialMode"])
	}
	next := res["nextActions"].(map[string]interface{})
	if next["reauthRunner"] != true {
		t.Fatalf("OAuth credential mode should hint reauthRunner")
	}
	// company-secret provider with keyConfigured:false → configureProviderKey.
	if next["configureProviderKey"] != true {
		t.Fatalf("expected configureProviderKey for unconfigured company-secret provider: %v", next)
	}
	// deploy approval required for app-code.
	appr := res["approvals"].(map[string]interface{})
	reqd := appr["required"].([]string)
	hasDeploy := false
	for _, a := range reqd {
		if a == "deploy" {
			hasDeploy = true
		}
	}
	if !hasDeploy {
		t.Fatalf("app-code should require deploy approval: %v", reqd)
	}
}

func TestResolveLocalPolicy_DisabledWorkKind(t *testing.T) {
	writeLocalPolicy(t, sampleLocalPolicy)
	pol, _ := LoadLocalCompanyAIPolicy()
	res := ResolveCompanyAILocal(pol, "robot-trial", "", "", "", "")
	if res["workKindEnabled"] != false {
		t.Fatalf("robot-trial is disabled in policy: %v", res["workKindEnabled"])
	}
	if res["runtimeReady"] != false {
		t.Fatalf("disabled work kind must not be runtimeReady")
	}
	next := res["nextActions"].(map[string]interface{})
	if next["enableWorkKind"] != true {
		t.Fatalf("expected enableWorkKind hint")
	}
}

func TestResolveLocalPolicy_UserOverrideRunner(t *testing.T) {
	writeLocalPolicy(t, sampleLocalPolicy)
	pol, _ := LoadLocalCompanyAIPolicy()
	// opencode is allowed → override honored.
	res := ResolveCompanyAILocal(pol, "app-code", "opencode", "", "", "")
	if res["runner"].(map[string]interface{})["id"] != "opencode" {
		t.Fatalf("allowed override should be honored")
	}
	// codex is NOT allowed → falls back to default.
	res2 := ResolveCompanyAILocal(pol, "app-code", "codex", "", "", "")
	if res2["runner"].(map[string]interface{})["id"] != "claude" {
		t.Fatalf("disallowed runner must fall back to default, got %v", res2["runner"])
	}
}

func TestLoadLocalPolicy_AbsentReturnsNil(t *testing.T) {
	t.Setenv("YAVER_COMPANY_AI_POLICY", filepath.Join(t.TempDir(), "does-not-exist.json"))
	t.Setenv("HOME", t.TempDir())
	pol, err := LoadLocalCompanyAIPolicy()
	if err != nil {
		t.Fatalf("absent file should not error: %v", err)
	}
	if pol != nil {
		t.Fatalf("absent policy should be nil (→ use hosted Convex), got %+v", pol)
	}
}

func TestResolveCompanyAIWithFallback(t *testing.T) {
	// Unreachable Convex (loopback port 1 → immediate connection refused) + a
	// local policy present → fall back to the local resolution.
	writeLocalPolicy(t, sampleLocalPolicy)
	payload := map[string]interface{}{"teamId": "t1", "workKind": "app-code"}
	data, err := resolveCompanyAIWithFallback("http://127.0.0.1:1", "tok", payload)
	if err != nil {
		t.Fatalf("fallback should not error when local policy exists: %v", err)
	}
	var out map[string]interface{}
	if jerr := json.Unmarshal(data, &out); jerr != nil {
		t.Fatalf("unmarshal: %v", jerr)
	}
	if out["source"] != "local-airgap" || out["fallback"] != "convex-unreachable" {
		t.Fatalf("expected local-airgap fallback, got source=%v fallback=%v", out["source"], out["fallback"])
	}
	if out["runner"].(map[string]interface{})["id"] != "claude" {
		t.Fatalf("local fallback should resolve the default runner")
	}
}

func TestResolveCompanyAIWithFallback_NoLocalPolicy_SurfacesError(t *testing.T) {
	// No local policy file anywhere → the Convex error is surfaced unchanged.
	t.Setenv("YAVER_COMPANY_AI_POLICY", filepath.Join(t.TempDir(), "absent.json"))
	t.Setenv("HOME", t.TempDir())
	_, err := resolveCompanyAIWithFallback("http://127.0.0.1:1", "tok", map[string]interface{}{"workKind": "app-code"})
	if err == nil {
		t.Fatalf("expected the Convex error to surface when no local policy exists")
	}
}

func TestPruneExpiredTasks(t *testing.T) {
	now := time.Date(2026, 6, 3, 12, 0, 0, 0, time.UTC)
	old := now.Add(-40 * 24 * time.Hour)
	recent := now.Add(-2 * 24 * time.Hour)
	tm := &TaskManager{tasks: map[string]*Task{
		"old":     {ID: "old", Status: TaskStatusFinished, FinishedAt: &old},
		"recent":  {ID: "recent", Status: TaskStatusFinished, FinishedAt: &recent},
		"running": {ID: "running", Status: TaskStatusRunning},
	}}
	n := tm.PruneExpiredTasks(14, now)
	if n != 1 {
		t.Fatalf("expected 1 pruned, got %d", n)
	}
	if _, ok := tm.tasks["old"]; ok {
		t.Fatalf("old task should be pruned")
	}
	if _, ok := tm.tasks["recent"]; !ok {
		t.Fatalf("recent task must survive")
	}
	if _, ok := tm.tasks["running"]; !ok {
		t.Fatalf("running task must never be pruned")
	}
	if tm.PruneExpiredTasks(0, now) != 0 {
		t.Fatalf("retentionDays=0 must be a no-op")
	}
}
