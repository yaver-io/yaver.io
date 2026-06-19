package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestDummyMode(t *testing.T) {
	tm := NewTaskManager(t.TempDir(), nil, defaultRunner)
	tm.DummyMode = true

	baseURL, cancel := startTestServer(t, "test-tok", tm)
	defer cancel()

	// Create task
	body := `{"title":"Run ls","source":"mobile"}`
	req, _ := http.NewRequest("POST", baseURL+"/tasks", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer test-tok")
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 && resp.StatusCode != 201 {
		t.Fatalf("expected 200/201, got %d", resp.StatusCode)
	}

	var result map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&result)
	taskID := result["taskId"].(string)
	fmt.Printf("Created task: %s (status: %s)\n", taskID, result["status"])

	// Wait for dummy task to complete
	time.Sleep(4 * time.Second)

	// Get task
	req2, _ := http.NewRequest("GET", baseURL+"/tasks/"+taskID, nil)
	req2.Header.Set("Authorization", "Bearer test-tok")
	resp2, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	var taskResp map[string]interface{}
	json.NewDecoder(resp2.Body).Decode(&taskResp)
	task := taskResp["task"].(map[string]interface{})

	status := task["status"].(string)
	output := task["output"].(string)
	fmt.Printf("Task status: %s\n", status)
	fmt.Printf("Output:\n%s\n", output)

	if status != "review" && status != "completed" {
		t.Fatalf("expected review/completed, got %s", status)
	}
	if !strings.Contains(output, "Dummy Response") {
		t.Fatal("expected dummy output")
	}
	if !strings.Contains(output, "Run ls") {
		t.Fatal("expected prompt echo in output")
	}
}
