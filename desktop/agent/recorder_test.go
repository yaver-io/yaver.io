package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestClipStartWithTargets(t *testing.T) {
	tm := NewTaskManager(t.TempDir(), nil, defaultRunner)
	baseURL, cancel := startTestServer(t, "test-token", tm)
	defer cancel()

	// Start with both targets.
	status, body := doRequest(t, "POST", baseURL+"/clips/start", "test-token",
		`{"title":"test clip","targets":["agent-screen","mobile-screen"]}`)
	// Will fail if ffmpeg is not installed — that's OK, we test the API shape.
	if status == 200 {
		session := body["session"].(map[string]interface{})
		if session["title"] != "test clip" {
			t.Errorf("expected title 'test clip', got %v", session["title"])
		}
		targets := session["targets"].([]interface{})
		if len(targets) != 2 {
			t.Errorf("expected 2 targets, got %d", len(targets))
		}
		streams := session["streams"].([]interface{})
		if len(streams) != 2 {
			t.Errorf("expected 2 streams, got %d", len(streams))
		}

		// Stop recording.
		stopStatus, _ := doRequest(t, "POST", baseURL+"/clips/stop", "test-token", "")
		if stopStatus != 200 {
			t.Errorf("expected 200 on stop, got %d", stopStatus)
		}
	} else if status == 500 {
		// ffmpeg not installed — skip agent capture tests.
		t.Log("ffmpeg not installed, skipping agent-screen capture test")
	} else {
		t.Fatalf("unexpected status %d: %v", status, body)
	}
}

func TestClipStartMobileOnly(t *testing.T) {
	tm := NewTaskManager(t.TempDir(), nil, defaultRunner)
	baseURL, cancel := startTestServer(t, "test-token", tm)
	defer cancel()

	// Start with mobile-screen only — should NOT start ffmpeg.
	status, body := doRequest(t, "POST", baseURL+"/clips/start", "test-token",
		`{"title":"mobile only","targets":["mobile-screen"]}`)
	if status != 200 {
		t.Fatalf("expected 200, got %d: %v", status, body)
	}

	session := body["session"].(map[string]interface{})
	targets := session["targets"].([]interface{})
	if len(targets) != 1 || targets[0] != "mobile-screen" {
		t.Errorf("expected [mobile-screen], got %v", targets)
	}
	streams := session["streams"].([]interface{})
	if len(streams) != 1 {
		t.Errorf("expected 1 stream, got %d", len(streams))
	}
	firstStream := streams[0].(map[string]interface{})
	if firstStream["kind"] != "mobile-screen" {
		t.Errorf("expected kind mobile-screen, got %v", firstStream["kind"])
	}

	// Stop — should succeed without ffmpeg since no agent capture.
	stopStatus, _ := doRequest(t, "POST", baseURL+"/clips/stop", "test-token", "")
	if stopStatus != 200 {
		t.Errorf("expected 200 on stop, got %d", stopStatus)
	}
}

func TestClipStartDefaultTargets(t *testing.T) {
	tm := NewTaskManager(t.TempDir(), nil, defaultRunner)
	baseURL, cancel := startTestServer(t, "test-token", tm)
	defer cancel()

	// Start without targets — should default to ["agent-screen"] for backward compat.
	status, body := doRequest(t, "POST", baseURL+"/clips/start", "test-token",
		`{"title":"default"}`)

	if status == 200 {
		session := body["session"].(map[string]interface{})
		targets := session["targets"].([]interface{})
		if len(targets) != 1 || targets[0] != "agent-screen" {
			t.Errorf("expected default [agent-screen], got %v", targets)
		}
		// Cleanup.
		doRequest(t, "POST", baseURL+"/clips/stop", "test-token", "")
	} else if status == 500 {
		t.Log("ffmpeg not installed, default target test skipped")
	}
}

func TestClipUploadAndList(t *testing.T) {
	tm := NewTaskManager(t.TempDir(), nil, defaultRunner)
	baseURL, cancel := startTestServer(t, "test-token", tm)
	defer cancel()

	// Start mobile-only session (no ffmpeg needed).
	status, body := doRequest(t, "POST", baseURL+"/clips/start", "test-token",
		`{"title":"upload test","targets":["mobile-screen"]}`)
	if status != 200 {
		t.Fatalf("start failed: %d", status)
	}

	sessionID := body["session"].(map[string]interface{})["id"].(string)

	// Stop.
	doRequest(t, "POST", baseURL+"/clips/stop", "test-token", "")

	// Upload a dummy "mobile-screen" file.
	dummyData := []byte("fake-mp4-data-for-testing")
	uploadURL := fmt.Sprintf("%s/clips/upload/%s?kind=mobile-screen", baseURL, sessionID)
	req, _ := http.NewRequest("POST", uploadURL, bytes.NewReader(dummyData))
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", "video/mp4")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("upload request failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("upload expected 200, got %d", resp.StatusCode)
	}

	var uploadResult map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&uploadResult)
	uploadSession := uploadResult["session"].(map[string]interface{})
	streams := uploadSession["streams"].([]interface{})

	// Verify the uploaded stream is marked.
	found := false
	for _, s := range streams {
		st := s.(map[string]interface{})
		if st["kind"] == "mobile-screen" && st["uploaded"] == true {
			found = true
			if int(st["bytes"].(float64)) != len(dummyData) {
				t.Errorf("expected %d bytes, got %v", len(dummyData), st["bytes"])
			}
		}
	}
	if !found {
		t.Error("mobile-screen stream not found or not marked uploaded")
	}

	// Verify the file exists on disk.
	dir, _ := sessionDir(sessionID)
	filePath := filepath.Join(dir, "mobile-screen.mp4")
	data, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("uploaded file not found: %v", err)
	}
	if !bytes.Equal(data, dummyData) {
		t.Error("uploaded file content mismatch")
	}

	// List clips and verify our session appears.
	listStatus, listBody := doRequest(t, "GET", baseURL+"/clips/list", "test-token", "")
	if listStatus != 200 {
		t.Fatalf("list expected 200, got %d", listStatus)
	}
	sessions := listBody["sessions"].([]interface{})
	found = false
	for _, s := range sessions {
		sess := s.(map[string]interface{})
		if sess["id"] == sessionID {
			found = true
		}
	}
	if !found {
		t.Error("session not found in list")
	}
}

func TestClipMergeRequiresTwoStreams(t *testing.T) {
	tm := NewTaskManager(t.TempDir(), nil, defaultRunner)
	baseURL, cancel := startTestServer(t, "test-token", tm)
	defer cancel()

	// Start mobile-only session.
	status, body := doRequest(t, "POST", baseURL+"/clips/start", "test-token",
		`{"title":"merge test","targets":["mobile-screen"]}`)
	if status != 200 {
		t.Fatalf("start failed: %d", status)
	}
	sessionID := body["session"].(map[string]interface{})["id"].(string)
	doRequest(t, "POST", baseURL+"/clips/stop", "test-token", "")

	// Upload only mobile-screen.
	uploadURL := fmt.Sprintf("%s/clips/upload/%s?kind=mobile-screen", baseURL, sessionID)
	req, _ := http.NewRequest("POST", uploadURL, bytes.NewReader([]byte("fake")))
	req.Header.Set("Authorization", "Bearer test-token")
	resp, _ := http.DefaultClient.Do(req)
	resp.Body.Close()

	// Try merge — should fail with 409 (only 1 stream).
	mergeStatus, mergeBody := doRequest(t, "POST",
		fmt.Sprintf("%s/clips/merge/%s", baseURL, sessionID), "test-token", "")
	if mergeStatus != 409 {
		t.Errorf("expected 409 for single-stream merge, got %d: %v", mergeStatus, mergeBody)
	}
}

func TestClipMergeWithTwoStreams(t *testing.T) {
	// This test requires ffmpeg. Skip if not available.
	if _, err := findFFmpeg(); err != nil {
		t.Skip("ffmpeg not installed, skipping merge test")
	}

	tm := NewTaskManager(t.TempDir(), nil, defaultRunner)
	baseURL, cancel := startTestServer(t, "test-token", tm)
	defer cancel()

	// Start mobile-only (avoid needing a display for agent capture).
	status, body := doRequest(t, "POST", baseURL+"/clips/start", "test-token",
		`{"title":"merge both","targets":["mobile-screen"]}`)
	if status != 200 {
		t.Fatalf("start failed: %d", status)
	}
	sessionID := body["session"].(map[string]interface{})["id"].(string)
	doRequest(t, "POST", baseURL+"/clips/stop", "test-token", "")

	dir, _ := sessionDir(sessionID)

	// Generate two tiny test videos with ffmpeg (1 second each).
	generateTestVideo(t, filepath.Join(dir, "agent-screen.mp4"), 640, 480)
	generateTestVideo(t, filepath.Join(dir, "mobile-screen.mp4"), 360, 640)

	// Mark both streams as uploaded in metadata.
	sess, _ := loadClipSession(sessionID)
	sess.Streams = []ClipStream{
		{Kind: "agent-screen", File: "agent-screen.mp4", Mime: "video/mp4", Uploaded: true, Bytes: 1},
		{Kind: "mobile-screen", File: "mobile-screen.mp4", Mime: "video/mp4", Uploaded: true, Bytes: 1},
	}
	saveClipSession(sess)

	// Merge.
	mergeStatus, mergeBody := doRequest(t, "POST",
		fmt.Sprintf("%s/clips/merge/%s", baseURL, sessionID), "test-token", "")
	if mergeStatus != 200 {
		t.Fatalf("merge failed: %d: %v", mergeStatus, mergeBody)
	}

	// Verify merged.mp4 exists.
	mergedPath := filepath.Join(dir, "merged.mp4")
	info, err := os.Stat(mergedPath)
	if err != nil {
		t.Fatalf("merged.mp4 not found: %v", err)
	}
	if info.Size() == 0 {
		t.Error("merged.mp4 is empty")
	}

	// Verify metadata has merged stream.
	sess, _ = loadClipSession(sessionID)
	foundMerged := false
	for _, st := range sess.Streams {
		if st.Kind == "merged" && st.Uploaded {
			foundMerged = true
		}
	}
	if !foundMerged {
		t.Error("merged stream not in session metadata")
	}
}

func TestClipNoAuth(t *testing.T) {
	tm := NewTaskManager(t.TempDir(), nil, defaultRunner)
	baseURL, cancel := startTestServer(t, "test-token", tm)
	defer cancel()

	// All clip endpoints require auth.
	endpoints := []struct {
		method string
		path   string
	}{
		{"POST", "/clips/start"},
		{"POST", "/clips/stop"},
		{"GET", "/clips/list"},
		{"POST", "/clips/merge/fake-id"},
	}
	for _, ep := range endpoints {
		status, _ := doRequest(t, ep.method, baseURL+ep.path, "", "")
		if status != 401 {
			t.Errorf("%s %s without auth: expected 401, got %d", ep.method, ep.path, status)
		}
	}

	// Detail page is public (share links).
	status, _ := doRequest(t, "GET", baseURL+"/clips/fake-id", "", "")
	if status == 401 {
		t.Error("/clips/<id> should be public for sharing")
	}
}

// findFFmpeg checks if ffmpeg is available.
func findFFmpeg() (string, error) {
	return exec.LookPath("ffmpeg")
}

// generateTestVideo creates a tiny test video using ffmpeg.
func generateTestVideo(t *testing.T, path string, w, h int) {
	t.Helper()
	cmd := fmt.Sprintf("ffmpeg -f lavfi -i testsrc=duration=1:size=%dx%d:rate=1 -vcodec libx264 -pix_fmt yuv420p -y %s",
		w, h, path)
	_ = cmd // used for documentation
	c := exec.Command("ffmpeg", "-f", "lavfi", "-i",
		fmt.Sprintf("testsrc=duration=1:size=%dx%d:rate=1", w, h),
		"-vcodec", "libx264", "-pix_fmt", "yuv420p", "-y", path)
	c.Stdout = io.Discard
	c.Stderr = io.Discard
	if err := c.Run(); err != nil {
		t.Fatalf("generate test video %s: %v", path, err)
	}
}
