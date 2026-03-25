package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http"
	"runtime"
	"strings"
	"testing"
	"time"
)

// TestExecStreamSSE verifies that the /exec/{id}/stream endpoint delivers
// real-time SSE events (stdout lines + exit event) for a short-lived command.
func TestExecStreamSSE(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("exec stream test not supported on Windows")
	}

	tm := NewTaskManager(t.TempDir(), nil, defaultRunner)
	baseURL, cancel := startTestServer(t, "tok", tm)
	defer cancel()

	// Start a command that produces multiple lines of output
	status, body := doRequest(t, "POST", baseURL+"/exec", "tok",
		`{"command":"echo line1 && echo line2 && echo line3"}`)
	if status != 200 {
		t.Fatalf("expected 200, got %d: %v", status, body)
	}
	execID := body["execId"].(string)

	// Connect to SSE stream
	req, err := http.NewRequest("GET", baseURL+"/exec/"+execID+"/stream", nil)
	if err != nil {
		t.Fatalf("create stream request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer tok")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("stream request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Fatalf("stream: expected 200, got %d", resp.StatusCode)
	}
	ct := resp.Header.Get("Content-Type")
	if !strings.Contains(ct, "text/event-stream") {
		t.Fatalf("expected text/event-stream, got %s", ct)
	}

	// Read SSE events
	scanner := bufio.NewScanner(resp.Body)
	var events []ExecOutputEvent
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var evt ExecOutputEvent
		if err := json.Unmarshal([]byte(line[6:]), &evt); err != nil {
			t.Logf("skipping non-JSON SSE line: %s", line)
			continue
		}
		events = append(events, evt)
		if evt.Type == "exit" {
			break
		}
	}

	// Verify we got stdout events
	var stdoutLines []string
	var gotExit bool
	for _, evt := range events {
		if evt.Type == "stdout" && evt.Text != "" {
			stdoutLines = append(stdoutLines, strings.TrimSpace(evt.Text))
		}
		if evt.Type == "exit" {
			gotExit = true
			if evt.Code == nil || *evt.Code != 0 {
				t.Fatalf("expected exit code 0, got %v", evt.Code)
			}
		}
	}

	if !gotExit {
		t.Fatal("did not receive exit event")
	}

	// Verify output contains our lines
	combined := strings.Join(stdoutLines, "\n")
	for _, want := range []string{"line1", "line2", "line3"} {
		if !strings.Contains(combined, want) {
			t.Errorf("stdout missing %q, got: %s", want, combined)
		}
	}
}

// TestExecStreamKill verifies that killing an exec session closes the SSE stream.
func TestExecStreamKill(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("exec stream test not supported on Windows")
	}

	tm := NewTaskManager(t.TempDir(), nil, defaultRunner)
	baseURL, cancel := startTestServer(t, "tok", tm)
	defer cancel()

	// Start a long-running command
	status, body := doRequest(t, "POST", baseURL+"/exec", "tok",
		`{"command":"sleep 60","timeout":120}`)
	if status != 200 {
		t.Fatalf("expected 200, got %d: %v", status, body)
	}
	execID := body["execId"].(string)

	// Connect to SSE stream in background
	req, _ := http.NewRequest("GET", baseURL+"/exec/"+execID+"/stream", nil)
	req.Header.Set("Authorization", "Bearer tok")

	streamDone := make(chan error, 1)
	go func() {
		client := &http.Client{Timeout: 30 * time.Second}
		resp, err := client.Do(req)
		if err != nil {
			streamDone <- fmt.Errorf("stream request: %w", err)
			return
		}
		defer resp.Body.Close()
		// Read until stream closes
		scanner := bufio.NewScanner(resp.Body)
		for scanner.Scan() {
			// consume lines until done
		}
		streamDone <- nil
	}()

	// Give stream time to connect
	time.Sleep(300 * time.Millisecond)

	// Kill the exec session
	status, _ = doRequest(t, "DELETE", baseURL+"/exec/"+execID, "tok", "")
	if status != 200 {
		t.Fatalf("kill: expected 200, got %d", status)
	}

	// Stream should close within a reasonable time
	select {
	case err := <-streamDone:
		if err != nil {
			t.Fatalf("stream error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("stream did not close within 5s after kill")
	}
}

// TestExecStreamAuthRequired verifies the stream endpoint requires auth.
func TestExecStreamAuthRequired(t *testing.T) {
	tm := NewTaskManager(t.TempDir(), nil, defaultRunner)
	baseURL, cancel := startTestServer(t, "secret", tm)
	defer cancel()

	// Start a command with correct token
	_, body := doRequest(t, "POST", baseURL+"/exec", "secret", `{"command":"echo hi"}`)
	execID := body["execId"].(string)

	// Try to stream without token
	req, _ := http.NewRequest("GET", baseURL+"/exec/"+execID+"/stream", nil)
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Fatalf("expected 401 without token, got %d", resp.StatusCode)
	}

	// Try with wrong token
	req, _ = http.NewRequest("GET", baseURL+"/exec/"+execID+"/stream", nil)
	req.Header.Set("Authorization", "Bearer wrong")
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != 403 {
		t.Fatalf("expected 403 with wrong token, got %d", resp.StatusCode)
	}
}

// TestExecStreamNotFound verifies 404 for non-existent exec ID.
func TestExecStreamNotFound(t *testing.T) {
	tm := NewTaskManager(t.TempDir(), nil, defaultRunner)
	baseURL, cancel := startTestServer(t, "tok", tm)
	defer cancel()

	req, _ := http.NewRequest("GET", baseURL+"/exec/nonexistent/stream", nil)
	req.Header.Set("Authorization", "Bearer tok")
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != 404 {
		t.Fatalf("expected 404, got %d", resp.StatusCode)
	}
}
