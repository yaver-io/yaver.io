package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func isolateWorkspaceLayoutTest(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("YAVER_WORKSPACE_DIR", "")
	t.Setenv("YAVER_REPOS_DIR", "")
	t.Setenv("YAVER_WORKTREES_DIR", "")
	return home
}

func TestWorkspaceLayoutHTTPReturnsManagedDefaults(t *testing.T) {
	home := isolateWorkspaceLayoutTest(t)
	req := httptest.NewRequest(http.MethodGet, "/workspace/layout", nil)
	rec := httptest.NewRecorder()
	(&HTTPServer{}).handleWorkspaceLayout(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var got WorkspaceLayoutStatus
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Repositories != filepath.Join(home, "Workspace", "repos") || got.Worktrees != filepath.Join(home, "Workspace", "worktrees") {
		t.Fatalf("unexpected layout: %#v", got)
	}
	if !got.ExplicitPathsSupported || !got.CleanupRequiresExplicit {
		t.Fatalf("safety contract missing: %#v", got)
	}
}

func TestWorkspaceLayoutHTTPRejectsMutationMethods(t *testing.T) {
	isolateWorkspaceLayoutTest(t)
	req := httptest.NewRequest(http.MethodPost, "/workspace/layout", strings.NewReader(`{}`))
	rec := httptest.NewRecorder()
	(&HTTPServer{}).handleWorkspaceLayout(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusMethodNotAllowed)
	}
}

func TestWorkspaceLayoutMCPIsAdvertisedAndOperational(t *testing.T) {
	home := isolateWorkspaceLayoutTest(t)
	wrapper, ok := (&HTTPServer{}).getMCPToolsList().(map[string]interface{})
	if !ok {
		t.Fatal("tools/list wrapper has unexpected type")
	}
	tools, _ := wrapper["tools"].([]map[string]interface{})
	found := false
	for _, tool := range tools {
		if tool["name"] == "workspace_layout" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("workspace_layout is not advertised")
	}
	result := billingToolText(t, (&HTTPServer{}).handleMCPToolCall([]byte(`{"name":"workspace_layout","arguments":{}}`)))
	if !strings.Contains(result, filepath.Join(home, "Workspace", "repos")) || !strings.Contains(result, filepath.Join(home, "Workspace", "worktrees")) {
		t.Fatalf("workspace_layout result missing resolved paths: %s", result)
	}
}
