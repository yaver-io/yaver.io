package main

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestAgenticQAIntegration(t *testing.T) {
	if os.Getenv("YAVER_QA_AGENTIC_INTEGRATION") != "1" {
		t.Skip("set YAVER_QA_AGENTIC_INTEGRATION=1 to run the redroid/LLM integration suite")
	}
	req := qaRunRequest{
		Package:     envOrDefault("YAVER_QA_PACKAGE", "io.yaver.mobile"),
		Activity:    os.Getenv("YAVER_QA_ACTIVITY"),
		FlowsDir:    envOrDefault("YAVER_QA_FLOWS_DIR", "/Users/kivanccakmak/Workspace/yaver.io/yaver-tests/flows"),
		Mode:        envOrDefault("YAVER_QA_MODE", "catch"),
		SSHHost:     os.Getenv("YAVER_REDROID_SSH_HOST"),
		HostWorkDir: os.Getenv("YAVER_REDROID_HOST_WORKDIR"),
		Container:   envOrDefault("YAVER_REDROID_CONTAINER", "yaver-qa"),
		TestAccount: envOrDefault("YAVER_QA_TEST_ACCOUNT", "ephemeral"),
		ConvexURL:   os.Getenv("YAVER_QA_CONVEX_URL"),
	}
	if strings.TrimSpace(req.SSHHost) == "" || strings.TrimSpace(req.HostWorkDir) == "" {
		t.Fatal("YAVER_REDROID_SSH_HOST and YAVER_REDROID_HOST_WORKDIR are required")
	}
	job, err := studioJobs.startQARun(req)
	if err != nil {
		t.Fatalf("start qa run: %v", err)
	}
	t.Logf("job: %s", job.ID)

	deadline := time.Now().Add(30 * time.Minute)
	var lastLen int
	for time.Now().Before(deadline) {
		job.mu.Lock()
		state, errText := job.State, job.Error
		lines := append([]string(nil), job.LogLines...)
		job.mu.Unlock()
		for _, line := range lines[lastLen:] {
			t.Log(line)
		}
		lastLen = len(lines)
		switch state {
		case studioCompleted:
			report := getQAReport(job.ID)
			if report == nil {
				t.Fatalf("job completed without report")
			}
			t.Logf("report: flows=%d caught=%d fixed=%d passed=%v", len(report.Flows), report.Caught, report.Fixed, report.Passed)
			for _, flow := range report.Flows {
				t.Logf("flow %q: steps=%d bugs=%d", flow.Name, flow.Steps, flow.Bugs)
			}
			for _, bug := range report.Bugs {
				t.Logf("bug: [%s/%s] %s - %s", bug.Severity, bug.Oracle, bug.Title, bug.Detail)
			}
			if !report.Passed {
				t.Fatalf("qa run found %d bug(s)", report.Caught)
			}
			return
		case studioFailed:
			t.Fatalf("qa job failed: %s", errText)
		}
		time.Sleep(5 * time.Second)
	}
	t.Fatalf("qa job %s timed out", job.ID)
}

func envOrDefault(name, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(name)); v != "" {
		return v
	}
	return fallback
}
