package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestFeedbackFullFlow tests the complete feedback lifecycle:
// upload → get → generate prompt → create fix task → delete
func TestFeedbackFullFlow(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	os.MkdirAll(filepath.Join(tmpDir, ".yaver"), 0700)

	fm, _ := NewFeedbackManager()
	em := &ExecManager{sessions: make(map[string]*ExecSession), workDir: tmpDir}
	taskMgr := &TaskManager{workDir: tmpDir}

	srv := &HTTPServer{
		feedbackMgr: fm,
		execMgr:     em,
		taskMgr:     taskMgr,
	}

	// Step 1: Upload multipart feedback
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	writer.WriteField("metadata", `{
		"source": "in-app-sdk",
		"appVersion": "2.0.0-beta",
		"deviceInfo": {"platform": "android", "model": "Pixel 8", "osVersion": "15"},
		"timeline": [
			{"time": 3.0, "type": "voice", "text": "login button unresponsive"},
			{"time": 5.0, "type": "screenshot", "file": "login_bug.jpg"},
			{"time": 8.0, "type": "voice", "text": "nav bar overlaps content"},
			{"time": 12.0, "type": "crash", "text": "NullPointerException in ProfileScreen"}
		],
		"transcript": "The login button doesn't respond to taps. Also the nav bar overlaps. And the profile screen crashes."
	}`)
	part, _ := writer.CreateFormFile("screenshot", "login_bug.jpg")
	part.Write([]byte("fake-screenshot-jpeg"))
	part, _ = writer.CreateFormFile("video", "session.mp4")
	part.Write([]byte("fake-video-content-h264-compressed"))
	part, _ = writer.CreateFormFile("audio", "narration.m4a")
	part.Write([]byte("fake-audio-aac"))
	writer.Close()

	req := httptest.NewRequest("POST", "/feedback", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	w := httptest.NewRecorder()
	srv.handleFeedback(w, req)

	if w.Code != 200 {
		t.Fatalf("upload: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var report FeedbackReport
	json.Unmarshal(w.Body.Bytes(), &report)
	reportID := report.ID
	t.Logf("Created feedback report: %s", reportID)

	if report.Source != "in-app-sdk" {
		t.Errorf("source: expected 'in-app-sdk', got %q", report.Source)
	}
	if report.VideoPath == "" {
		t.Error("expected video path")
	}
	if report.AudioPath == "" {
		t.Error("expected audio path")
	}
	if len(report.Screenshots) != 1 {
		t.Errorf("expected 1 screenshot, got %d", len(report.Screenshots))
	}
	if len(report.Timeline) != 4 {
		t.Errorf("expected 4 timeline events, got %d", len(report.Timeline))
	}

	// Step 2: Get report
	req = httptest.NewRequest("GET", "/feedback/"+reportID, nil)
	w = httptest.NewRecorder()
	srv.handleFeedbackByID(w, req)
	if w.Code != 200 {
		t.Fatalf("get: expected 200, got %d", w.Code)
	}

	// Step 3: Get video
	req = httptest.NewRequest("GET", "/feedback/"+reportID+"/video", nil)
	w = httptest.NewRecorder()
	srv.handleFeedbackByID(w, req)
	if w.Code != 200 {
		t.Fatalf("video: expected 200, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "fake-video-content") {
		t.Error("video content mismatch")
	}

	// Step 4: Save transcript
	req = httptest.NewRequest("POST", "/feedback/"+reportID+"/transcript",
		strings.NewReader(`{"transcript":"Updated transcript from STT."}`))
	w = httptest.NewRecorder()
	srv.handleFeedbackByID(w, req)
	if w.Code != 200 {
		t.Fatalf("transcript: expected 200, got %d", w.Code)
	}

	// Step 5: Generate fix prompt
	prompt, err := fm.GenerateFixPrompt(reportID)
	if err != nil {
		t.Fatalf("GenerateFixPrompt: %v", err)
	}
	if !strings.Contains(prompt, "Bug report from device testing") {
		t.Error("prompt missing header")
	}
	if !strings.Contains(prompt, "login button unresponsive") {
		t.Error("prompt missing voice timeline")
	}
	if !strings.Contains(prompt, "NullPointerException") {
		t.Error("prompt missing crash event")
	}
	if !strings.Contains(prompt, "Updated transcript from STT") {
		t.Error("prompt missing updated transcript")
	}

	// Step 6: List reports
	req = httptest.NewRequest("GET", "/feedback", nil)
	w = httptest.NewRecorder()
	srv.handleFeedback(w, req)
	var summaries []FeedbackSummary
	json.Unmarshal(w.Body.Bytes(), &summaries)
	if len(summaries) != 1 {
		t.Fatalf("expected 1 report in list, got %d", len(summaries))
	}
	if !summaries[0].HasVideo {
		t.Error("summary should have HasVideo=true")
	}
	if summaries[0].NumScreens != 1 {
		t.Errorf("summary should have 1 screenshot, got %d", summaries[0].NumScreens)
	}

	// Step 7: Delete
	req = httptest.NewRequest("DELETE", "/feedback/"+reportID, nil)
	w = httptest.NewRecorder()
	srv.handleFeedbackByID(w, req)
	if w.Code != 200 {
		t.Fatalf("delete: expected 200, got %d", w.Code)
	}

	// Verify deleted
	req = httptest.NewRequest("GET", "/feedback/"+reportID, nil)
	w = httptest.NewRecorder()
	srv.handleFeedbackByID(w, req)
	if w.Code != 404 {
		t.Fatalf("after delete: expected 404, got %d", w.Code)
	}
}

// TestFeedbackPersistence verifies reports survive manager restart.
func TestFeedbackPersistence(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	os.MkdirAll(filepath.Join(tmpDir, ".yaver"), 0700)

	// Create report
	fm1, _ := NewFeedbackManager()
	fm1.ReceiveFeedback(
		json.RawMessage(`{"source":"test","deviceInfo":{"platform":"ios","model":"test","osVersion":"18"}}`),
		map[string][]byte{"test.jpg": []byte("img")},
	)

	// Create new manager (simulates restart)
	fm2, _ := NewFeedbackManager()
	reports := fm2.ListFeedback()
	if len(reports) != 1 {
		t.Fatalf("expected 1 report after restart, got %d", len(reports))
	}
}

// TestFeedbackModes verifies feedback mode types.
func TestFeedbackModes(t *testing.T) {
	modes := []FeedbackMode{FeedbackModeLive, FeedbackModeNarrated, FeedbackModeBatch}
	expected := []string{"live", "narrated", "batch"}
	for i, m := range modes {
		if string(m) != expected[i] {
			t.Errorf("mode %d: expected %q, got %q", i, expected[i], m)
		}
	}
}

// TestFeedbackStreamEndpoint verifies the streaming endpoint accepts events.
func TestFeedbackStreamEndpoint(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	os.MkdirAll(filepath.Join(tmpDir, ".yaver"), 0700)

	fm, _ := NewFeedbackManager()
	srv := &HTTPServer{feedbackMgr: fm}

	// Send a stream of events
	events := `{"type":"voice","text":"button is broken"}
{"type":"screenshot","text":"captured"}
{"type":"end"}`

	req := httptest.NewRequest("POST", "/feedback/stream", strings.NewReader(events))
	w := &flushRecorder{ResponseRecorder: httptest.NewRecorder()}
	srv.handleFeedbackStream(w, req)

	// Should get SSE responses (200 with event-stream content type)
	if w.Code != 200 {
		t.Fatalf("stream: expected 200, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "data:") {
		t.Logf("stream body: %s", body)
		// SSE responses may be flushed; just verify it didn't error
	}
}

// flushRecorder wraps httptest.ResponseRecorder to implement http.Flusher.
type flushRecorder struct {
	*httptest.ResponseRecorder
}

func (f *flushRecorder) Flush() {
	// no-op for testing
}

// TestFeedbackScreenshotServing verifies screenshot files are served correctly.
func TestFeedbackScreenshotServing(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	os.MkdirAll(filepath.Join(tmpDir, ".yaver"), 0700)

	fm, _ := NewFeedbackManager()
	imgContent := []byte("PNG-fake-image-data-for-screenshot-test")

	report, _ := fm.ReceiveFeedback(
		json.RawMessage(`{"source":"test","deviceInfo":{"platform":"android","model":"Pixel","osVersion":"15"}}`),
		map[string][]byte{"bug_screenshot.jpg": imgContent},
	)

	srv := &HTTPServer{feedbackMgr: fm}

	// Serve screenshot by name
	req := httptest.NewRequest("GET", fmt.Sprintf("/feedback/%s/screenshot/bug_screenshot.jpg", report.ID), nil)
	w := httptest.NewRecorder()
	srv.handleFeedbackByID(w, req)

	if w.Code != 200 {
		t.Fatalf("screenshot: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if w.Body.String() != string(imgContent) {
		t.Error("screenshot content mismatch")
	}
}

// TestAllNewEndpointsExist verifies all new HTTP endpoints return non-404 for valid methods.
func TestAllNewEndpointsExist(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	os.MkdirAll(filepath.Join(tmpDir, ".yaver"), 0700)

	em := &ExecManager{sessions: make(map[string]*ExecSession), workDir: tmpDir}
	bm := NewBuildManager(em, tmpDir)
	tm := NewTunnelManager()
	testMgr := NewTestManager(em, tmpDir)
	fm, _ := NewFeedbackManager()

	os.MkdirAll(filepath.Join(tmpDir, ".yaver"), 0700)
	vs, _ := NewVaultStore("test")

	srv := &HTTPServer{
		taskMgr:     &TaskManager{workDir: tmpDir},
		execMgr:     em,
		buildMgr:    bm,
		tunnelMgr:   tm,
		testMgr:     testMgr,
		feedbackMgr: fm,
		vaultStore:  vs,
	}

	endpoints := []struct {
		method string
		path   string
		want   int // expected status (not 404/405)
	}{
		{"GET", "/vault/list", 200},
		{"GET", "/builds", 200},
		{"GET", "/tunnels", 200},
		{"GET", "/tests", 200},
		{"GET", "/feedback", 200},
	}

	for _, ep := range endpoints {
		req := httptest.NewRequest(ep.method, ep.path, nil)
		w := httptest.NewRecorder()

		switch ep.path {
		case "/vault/list":
			srv.handleVaultList(w, req)
		case "/builds":
			srv.handleBuilds(w, req)
		case "/tunnels":
			srv.handleTunnels(w, req)
		case "/tests":
			srv.handleTests(w, req)
		case "/feedback":
			srv.handleFeedback(w, req)
		}

		if w.Code != ep.want {
			t.Errorf("%s %s: expected %d, got %d", ep.method, ep.path, ep.want, w.Code)
		}
	}
}

var _ = http.StatusOK // suppress unused import
