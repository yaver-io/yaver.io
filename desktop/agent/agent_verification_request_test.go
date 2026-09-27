package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestVerifyTaskToolRequiresTaskContext(t *testing.T) {
	t.Setenv("YAVER_TASK_ID", "")
	result := forwardYaverVerifyTask(json.RawMessage(`{}`))
	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "YAVER_TASK_ID not set") {
		t.Fatalf("unexpected MCP result: %s", raw)
	}
}

func TestVerifyTaskToolIsFirstClassMCPTool(t *testing.T) {
	tools := (&HTTPServer{}).getMCPToolsList().(map[string]interface{})["tools"].([]map[string]interface{})
	for _, tool := range tools {
		if tool["name"] == "yaver_verify_task" {
			return
		}
	}
	t.Fatal("yaver_verify_task missing from MCP tool list")
}

func TestTaskVerificationRejectsConcurrentAttemptAndGatesReview(t *testing.T) {
	tm := NewTaskManager(t.TempDir(), nil, defaultTestRunner())
	task := &Task{ID: "verified-task", Status: TaskStatusRunning, WorkDir: t.TempDir(), CreatedAt: time.Now()}
	tm.tasks[task.ID] = task

	_, first, err := tm.beginTaskVerification(task.ID, taskVerificationRequest{Feature: "checkout"})
	if err != nil {
		t.Fatal(err)
	}
	if first.Status != "running" || first.Attempt != 1 || first.Feature != "checkout" {
		t.Fatalf("unexpected first attempt: %+v", first)
	}
	if _, _, err := tm.beginTaskVerification(task.ID, taskVerificationRequest{}); err == nil || !strings.Contains(err.Error(), "verification_in_progress") {
		t.Fatalf("concurrent attempt was not rejected: %v", err)
	}
	if err := tm.RequestTaskReview(task.ID, "done"); err == nil || !strings.Contains(err.Error(), "verification is running") {
		t.Fatalf("running verification did not gate review: %v", err)
	}

	_, failed := tm.updateTaskVerification(task.ID, func(v *TaskVerification) { v.Status = "failed" })
	if failed.Status != "failed" {
		t.Fatalf("failed update lost: %+v", failed)
	}
	if err := tm.RequestTaskReview(task.ID, "done"); err == nil || !strings.Contains(err.Error(), "verification is failed") {
		t.Fatalf("failed verification did not gate review: %v", err)
	}

	_, passed := tm.updateTaskVerification(task.ID, func(v *TaskVerification) { v.Status = "passed" })
	if passed.Status != "passed" {
		t.Fatalf("passed update lost: %+v", passed)
	}
	if err := tm.RequestTaskReview(task.ID, "verified"); err != nil {
		t.Fatalf("passing verification should allow review: %v", err)
	}
}

func TestTaskVerificationWireHidesLocalArtifactPaths(t *testing.T) {
	v := &TaskVerification{Artifacts: []TaskVerificationArtifact{{Kind: "screenshot", Path: "/private/project/failure.png", URL: "/tasks/t/verification/artifacts/0"}}}
	wire := taskVerificationForWire(v)
	if wire.Artifacts[0].Path != "" || wire.Artifacts[0].URL == "" {
		t.Fatalf("wire artifact leaked or lost routing: %+v", wire.Artifacts[0])
	}
	if v.Artifacts[0].Path == "" {
		t.Fatal("wire projection mutated persisted artifact path")
	}
}

func TestImportVerificationClipRejectsOversizedVideoWithoutCopying(t *testing.T) {
	path := filepath.Join(t.TempDir(), "oversized.mp4")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Truncate((512 << 20) + 1); err != nil {
		file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := importVerificationClip(path, "oversized"); err == nil || !strings.Contains(err.Error(), "512 MiB") {
		t.Fatalf("expected bounded artifact rejection, got %v", err)
	}
}
