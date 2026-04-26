package main

import (
	"sync/atomic"
	"testing"
	"time"
)

func TestAgentUpdateStatusEndpoint(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	tm := NewTaskManager(t.TempDir(), nil, defaultRunner)
	baseURL, cancel := startTestServer(t, "update-token", tm)
	defer cancel()

	prevLatest := latestAgentReleaseVersionFunc
	latestAgentReleaseVersionFunc = func() (string, error) { return "1.99.99", nil }
	defer func() { latestAgentReleaseVersionFunc = prevLatest }()

	status, body := doRequest(t, "GET", baseURL+"/agent/update", "update-token", "")
	if status != 200 {
		t.Fatalf("expected 200, got %d", status)
	}
	if body["latestVersion"] != "1.99.99" {
		t.Fatalf("latestVersion = %v, want 1.99.99", body["latestVersion"])
	}
	if body["repo"] != updateRepo() {
		t.Fatalf("repo = %v, want %s", body["repo"], updateRepo())
	}
}

func TestAgentUpdateTriggerEndpoint(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	tm := NewTaskManager(t.TempDir(), nil, defaultRunner)
	baseURL, cancel := startTestServer(t, "update-token", tm)
	defer cancel()

	prevLatest := latestAgentReleaseVersionFunc
	latestAgentReleaseVersionFunc = func() (string, error) { return "9.99.99", nil }
	defer func() { latestAgentReleaseVersionFunc = prevLatest }()

	prevRun := runForcedAgentUpdate
	var ran atomic.Bool
	done := make(chan struct{}, 1)
	runForcedAgentUpdate = func() {
		ran.Store(true)
		select {
		case done <- struct{}{}:
		default:
		}
	}
	defer func() { runForcedAgentUpdate = prevRun }()

	status, body := doRequest(t, "POST", baseURL+"/agent/update", "update-token", `{}`)
	if status != 202 {
		t.Fatalf("expected 202, got %d", status)
	}
	if body["started"] != true {
		t.Fatalf("started = %v, want true", body["started"])
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("expected update goroutine to run")
	}
	if !ran.Load() {
		t.Fatal("expected update callback to run")
	}
}
