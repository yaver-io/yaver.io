package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestMeshFullTemplateEndToEnd is the in-process smoke test for
// `yaver code --mesh`. It builds a fake git repo in a temp dir, creates
// an agent graph run using the "full" template (plan → implement →
// verify chat chain), and verifies all three nodes reach the completed
// state within the dummy runner's response window.
//
// DummyMode avoids spawning real runner binaries. The aim is to catch
// regressions in the graph scheduler, placement, slice prep, and task
// lifecycle — not model quality.
func TestMeshFullTemplateEndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("mesh smoke test skipped in -short mode")
	}

	t.Setenv("HOME", t.TempDir())

	repo := initMeshSmokeRepo(t)
	taskMgr := NewTaskManager(repo, nil, defaultTestRunner())
	taskMgr.DummyMode = true
	defer taskMgr.Shutdown()

	gm := NewAgentGraphManager(taskMgr)

	req := AgentGraphCreateRequest{
		Name:        "smoke",
		WorkDir:     repo,
		Prompt:      "add a hello-world survey form",
		Template:    "full",
		MaxParallel: 2,
	}
	run, err := gm.CreateRun(req)
	if err != nil {
		t.Fatalf("CreateRun error: %v", err)
	}
	if run == nil || run.ID == "" {
		t.Fatalf("expected a run with id, got %+v", run)
	}
	if len(run.Nodes) != 3 {
		t.Fatalf("expected 3 nodes in full template, got %d", len(run.Nodes))
	}

	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		latest, ok := gm.GetRun(run.ID)
		if !ok {
			t.Fatalf("run %s disappeared", run.ID)
		}
		if latest.Status == AgentGraphCompleted ||
			latest.Status == AgentGraphFailed ||
			latest.Status == AgentGraphStopped {
			run = latest
			break
		}
		time.Sleep(300 * time.Millisecond)
	}

	if run.Status != AgentGraphCompleted {
		t.Fatalf("run did not complete: status=%s summary=%q", run.Status, run.Summary)
	}
	for _, node := range run.Nodes {
		if node.Status != AgentNodeCompleted {
			t.Fatalf("node %s status = %s, want completed", node.Spec.ID, node.Status)
		}
		if node.Placement == nil {
			t.Fatalf("node %s has no placement", node.Spec.ID)
		}
		if node.Placement.DeviceID == "" {
			t.Fatalf("node %s placement has no device id", node.Spec.ID)
		}
	}
}

func initMeshSmokeRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	steps := [][]string{
		{"git", "init", "-b", "main"},
		{"git", "config", "user.email", "smoke@test"},
		{"git", "config", "user.name", "Smoke Test"},
	}
	for _, args := range steps {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%s: %v (%s)", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
		}
	}
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("# smoke\n"), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	for _, args := range [][]string{{"git", "add", "README.md"}, {"git", "commit", "-m", "init"}} {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%s: %v (%s)", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
		}
	}
	return repo
}
