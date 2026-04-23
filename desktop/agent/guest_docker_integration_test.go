package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestGuestTaskRunsInDockerIsolation(t *testing.T) {
	cr := NewContainerRunner()
	if !cr.IsAvailable() {
		t.Skip("docker not available")
	}

	buildCtx, cancelBuild := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancelBuild()
	if !cr.AutoBuild(buildCtx) {
		t.Fatal("sandbox image not ready after autobuild")
	}

	projectDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(projectDir, "workspace-visible.txt"), []byte("visible"), 0o644); err != nil {
		t.Fatalf("write workspace marker: %v", err)
	}

	hostOnlyDir := t.TempDir()
	hostOnlyPath := filepath.Join(hostOnlyDir, "host-only.txt")
	if err := os.WriteFile(hostOnlyPath, []byte("hidden"), 0o644); err != nil {
		t.Fatalf("write host-only marker: %v", err)
	}

	prevSecret, hadSecret := os.LookupEnv("OPENAI_API_KEY")
	t.Setenv("OPENAI_API_KEY", "yaver-host-secret-should-not-leak")
	defer func() {
		if hadSecret {
			_ = os.Setenv("OPENAI_API_KEY", prevSecret)
		} else {
			_ = os.Unsetenv("OPENAI_API_KEY")
		}
	}()

	tm := NewTaskManager(projectDir, nil, defaultTestRunner())
	tm.ContainerRunner = cr
	tm.ContainerizeGuests = true
	tm.ContainerNetwork = "host"
	tm.ContainerReadOnly = false
	defer tm.Shutdown()

	rawScript := strings.Join([]string{
		"if [ -f /workspace/workspace-visible.txt ]; then echo WORKSPACE_VISIBLE; else echo WORKSPACE_HIDDEN; fi",
		"if [ -f " + shellQuote(hostOnlyPath) + " ]; then echo HOST_PATH_LEAKED; else echo HOST_PATH_HIDDEN; fi",
		"if [ -f /.dockerenv ]; then echo IN_CONTAINER; else echo NOT_IN_CONTAINER; fi",
		"if [ -n \"$OPENAI_API_KEY\" ]; then echo SECRET_LEAKED; else echo SECRET_HIDDEN; fi",
	}, " ; ")
	cmd := shellQuote(rawScript)

	task, err := tm.CreateTaskWithOptions(
		"guest docker isolation smoke",
		"",
		"",
		"cli",
		"",
		cmd,
		nil,
		TaskCreateOptions{
			GuestUserID:                 "guest-smoke",
			GuestUseHostAPIKeys:         false,
			GuestAllowGuestProvidedKeys: false,
			GuestRequireIsolation:       true,
		},
	)
	if err != nil {
		t.Fatalf("CreateTaskWithOptions: %v", err)
	}

	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		got, ok := tm.GetTask(task.ID)
		if !ok {
			t.Fatalf("task %s disappeared", task.ID)
		}
		switch got.Status {
		case TaskStatusFinished, TaskStatusFailed, TaskStatusStopped:
			if got.Status != TaskStatusFinished {
				t.Fatalf("task status = %s, output:\n%s", got.Status, got.Output)
			}
			out := got.Output
			for _, needle := range []string{"WORKSPACE_VISIBLE", "HOST_PATH_HIDDEN", "IN_CONTAINER", "SECRET_HIDDEN"} {
				if !strings.Contains(out, needle) {
					t.Fatalf("expected %q in output, got:\n%s", needle, out)
				}
			}
			for _, needle := range []string{"WORKSPACE_HIDDEN", "HOST_PATH_LEAKED", "NOT_IN_CONTAINER", "SECRET_LEAKED"} {
				if strings.Contains(out, needle) {
					t.Fatalf("unexpected %q in output:\n%s", needle, out)
				}
			}
			return
		}
		time.Sleep(500 * time.Millisecond)
	}

	got, _ := tm.GetTask(task.ID)
	t.Fatalf("task %s did not complete in time, status=%s output:\n%s", task.ID, got.Status, got.Output)
}
