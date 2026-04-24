package main

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func phoneOnlyPostJSON(t *testing.T, baseURL, token, path, body string) map[string]interface{} {
	t.Helper()
	req, err := http.NewRequest("POST", baseURL+path, strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		t.Fatalf("POST %s returned %d: %s", path, resp.StatusCode, raw)
	}
	var out map[string]interface{}
	_ = json.Unmarshal(raw, &out)
	return out
}

func phoneOnlyGetJSON(t *testing.T, baseURL, token, path string) map[string]interface{} {
	t.Helper()
	req, err := http.NewRequest("GET", baseURL+path, nil)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		t.Fatalf("GET %s returned %d: %s", path, resp.StatusCode, raw)
	}
	var out map[string]interface{}
	_ = json.Unmarshal(raw, &out)
	return out
}

func TestPhoneOnlyTodoE2E_MobileHTTPFlow(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	tm := NewTaskManager(t.TempDir(), nil, defaultRunner)
	tm.DummyMode = true
	baseURL, cancel := startTestServer(t, "phone-tok", tm)
	defer cancel()

	create := phoneOnlyPostJSON(t, baseURL, "phone-tok", "/phone/projects/create", `{
		"name":"Phone Only Todo",
		"template":"todos",
		"prompt":"Build a mobile-only todo app with a local backend."
	}`)
	slug, _ := create["slug"].(string)
	projectDir, _ := create["dir"].(string)
	if slug == "" || projectDir == "" {
		t.Fatalf("create missing slug/dir: %+v", create)
	}

	getResp := phoneOnlyGetJSON(t, baseURL, "phone-tok", "/phone/projects/get?slug="+slug)
	if getResp["slug"] != slug {
		t.Fatalf("get slug = %v, want %s", getResp["slug"], slug)
	}

	schema, _ := getResp["schema"].(map[string]interface{})
	tables, _ := schema["tables"].([]interface{})
	if len(tables) < 2 {
		t.Fatalf("expected seeded todo schema, got %+v", schema)
	}
	stats, _ := getResp["stats"].(map[string]interface{})
	if stats == nil {
		t.Fatalf("missing project stats: %+v", getResp)
	}

	browse := phoneOnlyGetJSON(t, baseURL, "phone-tok", "/phone/projects/browse?slug="+slug+"&table=todos&limit=20")
	rows, _ := browse["rows"].([]interface{})
	if len(rows) < 1 {
		t.Fatalf("expected seeded todos, got %+v", browse)
	}

	vibing := phoneOnlyGetJSON(t, baseURL, "phone-tok", "/vibing?query="+projectDir)
	if vibing["path"] != projectDir {
		t.Fatalf("vibing path = %v, want %s", vibing["path"], projectDir)
	}
	quickActions, _ := vibing["quickActions"].([]interface{})
	if len(quickActions) < 1 {
		t.Fatalf("expected quick actions, got %+v", vibing)
	}
	firstAction, _ := quickActions[0].(map[string]interface{})
	prompt, _ := firstAction["prompt"].(string)
	if prompt == "" {
		t.Fatalf("quick action missing prompt: %+v", firstAction)
	}

	execBody, _ := json.Marshal(map[string]string{
		"prompt":      prompt,
		"projectPath": projectDir,
		"projectName": "Phone Only Todo",
	})
	execResp := phoneOnlyPostJSON(t, baseURL, "phone-tok", "/vibing/execute", string(execBody))
	taskID, _ := execResp["taskId"].(string)
	if taskID == "" {
		t.Fatalf("expected taskId from vibing execute, got %+v", execResp)
	}

	taskList := phoneOnlyGetJSON(t, baseURL, "phone-tok", "/tasks")
	tasks, _ := taskList["tasks"].([]interface{})
	if len(tasks) < 1 {
		t.Fatalf("expected created vibing task, got %+v", taskList)
	}

	insertBody := `{"slug":"` + slug + `","table":"todos","doc":{"id":"phone-only-added","title":"created from phone flow","done":false,"owner_id":"alice"}}`
	insertResp := phoneOnlyPostJSON(t, baseURL, "phone-tok", "/phone/projects/insert", insertBody)
	if insertResp["id"] == nil {
		t.Fatalf("insert missing id: %+v", insertResp)
	}

	time.Sleep(100 * time.Millisecond)
	afterInsert := phoneOnlyGetJSON(t, baseURL, "phone-tok", "/phone/projects/browse?slug="+slug+"&table=todos&limit=50")
	afterRows, _ := afterInsert["rows"].([]interface{})
	if len(afterRows) < len(rows)+1 {
		t.Fatalf("expected inserted todo row, before=%d after=%d", len(rows), len(afterRows))
	}

	req, err := http.NewRequest("GET", baseURL+"/phone/projects/export?slug="+slug+"&includeData=true", nil)
	if err != nil {
		t.Fatalf("export request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer phone-tok")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("export request failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("export returned %d: %s", resp.StatusCode, raw)
	}
	payload, _ := io.ReadAll(resp.Body)
	if len(payload) == 0 {
		t.Fatal("expected non-empty export payload")
	}
}
