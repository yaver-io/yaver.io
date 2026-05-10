package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRecommendNextActions_YaverAuthBlocksRunnerChecks(t *testing.T) {
	audit := YaverAgentDeviceAudit{
		LifecycleState: string(AgentLifecycleAuthExpired),
		Usable:         false,
		NeedsAuth:      true,
		Runners: []YaverAgentRunnerAudit{
			{ID: "claude", Name: "Claude Code", Installed: true, Ready: false, AuthConfigured: false},
			{ID: "codex", Name: "Codex", Installed: true, Ready: true, AuthConfigured: true},
		},
	}
	recs := recommendNextActions(audit)
	if len(recs) != 1 {
		t.Fatalf("expected 1 recommendation when yaver auth is missing, got %d: %+v", len(recs), recs)
	}
	if recs[0].Kind != "yaver_auth_required" {
		t.Errorf("expected yaver_auth_required, got %q", recs[0].Kind)
	}
	if recs[0].Action != "yaver.start_auth" {
		t.Errorf("expected action yaver.start_auth, got %q", recs[0].Action)
	}
}

func TestRecommendNextActions_RunnerNotAuthed(t *testing.T) {
	audit := YaverAgentDeviceAudit{
		LifecycleState: string(AgentLifecycleReadyToConnect),
		Usable:         true,
		NeedsAuth:      false,
		Runners: []YaverAgentRunnerAudit{
			{ID: "claude", Name: "Claude Code", Installed: true, Ready: false, AuthConfigured: false, Error: "no claude credentials"},
			{ID: "codex", Name: "Codex", Installed: true, Ready: true, AuthConfigured: true},
			{ID: "opencode", Name: "OpenCode", Installed: false},
		},
	}
	recs := recommendNextActions(audit)
	if len(recs) != 1 {
		t.Fatalf("expected exactly 1 runner recommendation, got %d: %+v", len(recs), recs)
	}
	if recs[0].Kind != "runner_auth_required" || recs[0].Target != "claude" {
		t.Errorf("unexpected recommendation: %+v", recs[0])
	}
}

func TestRecommendNextActions_AllConfigured(t *testing.T) {
	audit := YaverAgentDeviceAudit{
		LifecycleState: string(AgentLifecycleReadyToConnect),
		Usable:         true,
		NeedsAuth:      false,
		Runners: []YaverAgentRunnerAudit{
			{ID: "claude", Installed: true, Ready: true, AuthConfigured: true},
			{ID: "codex", Installed: false},
			{ID: "opencode", Installed: false},
		},
	}
	recs := recommendNextActions(audit)
	if len(recs) != 1 || recs[0].Kind != "configured" {
		t.Errorf("expected single 'configured' recommendation, got %+v", recs)
	}
}

func TestHandleYaverAgentDeviceAudit_HTTPShape(t *testing.T) {
	srv := &HTTPServer{deviceID: "test-device-id"}

	req := httptest.NewRequest(http.MethodGet, "/yaver-agent/audit", nil)
	rec := httptest.NewRecorder()
	srv.handleYaverAgentDeviceAudit(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: %d, body=%s", rec.Code, rec.Body.String())
	}
	var resp YaverAgentDeviceAudit
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	// Three runners always present in fixed order.
	if len(resp.Runners) != 3 {
		t.Fatalf("expected 3 runners, got %d: %+v", len(resp.Runners), resp.Runners)
	}
	expected := []string{"claude", "codex", "opencode"}
	for i, want := range expected {
		if resp.Runners[i].ID != want {
			t.Errorf("runner[%d].ID = %q, want %q", i, resp.Runners[i].ID, want)
		}
	}

	// DeviceID surfaced.
	if resp.DeviceID != "test-device-id" {
		t.Errorf("deviceId not surfaced: %q", resp.DeviceID)
	}

	// Recommendations array always populated.
	if len(resp.Recommendations) == 0 {
		t.Errorf("expected at least one recommendation")
	}
}

func TestHandleYaverAgentDeviceAudit_RejectsNonGet(t *testing.T) {
	srv := &HTTPServer{}
	req := httptest.NewRequest(http.MethodPost, "/yaver-agent/audit", nil)
	rec := httptest.NewRecorder()
	srv.handleYaverAgentDeviceAudit(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", rec.Code)
	}
}
