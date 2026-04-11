package main

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFeedbackManagerCRUD(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	os.MkdirAll(filepath.Join(tmpDir, ".yaver"), 0700)

	fm, err := NewFeedbackManager()
	if err != nil {
		t.Fatalf("NewFeedbackManager: %v", err)
	}

	// Empty list
	if reports := fm.ListFeedback(); len(reports) != 0 {
		t.Fatalf("expected empty, got %d", len(reports))
	}

	// Receive feedback with files
	metadata := json.RawMessage(`{
		"source": "yaver-app",
		"appVersion": "1.0.0",
		"deviceInfo": {"platform": "ios", "model": "iPhone 16", "osVersion": "18.2"},
		"timeline": [
			{"time": 15.0, "type": "voice", "text": "login button broken"},
			{"time": 15.0, "type": "screenshot", "file": "screen_0015.jpg"}
		]
	}`)

	files := map[string][]byte{
		"screen_0015.jpg": []byte("fake-jpeg-data"),
		"recording.mp4":   []byte("fake-video-data"),
		"voice.m4a":       []byte("fake-audio-data"),
	}

	report, err := fm.ReceiveFeedback(metadata, files)
	if err != nil {
		t.Fatalf("ReceiveFeedback: %v", err)
	}

	if report.Source != "yaver-app" {
		t.Errorf("expected source 'yaver-app', got %q", report.Source)
	}
	if report.VideoPath == "" {
		t.Error("expected video path to be set")
	}
	if report.AudioPath == "" {
		t.Error("expected audio path to be set")
	}
	if len(report.Screenshots) != 1 {
		t.Errorf("expected 1 screenshot, got %d", len(report.Screenshots))
	}
	if len(report.Timeline) != 2 {
		t.Errorf("expected 2 timeline events, got %d", len(report.Timeline))
	}

	// List
	reports := fm.ListFeedback()
	if len(reports) != 1 {
		t.Fatalf("expected 1 report, got %d", len(reports))
	}
	if !reports[0].HasVideo {
		t.Error("expected HasVideo=true")
	}

	// Get
	got, ok := fm.GetFeedback(report.ID)
	if !ok {
		t.Fatal("expected to find report")
	}
	if got.AppVersion != "1.0.0" {
		t.Errorf("expected appVersion '1.0.0', got %q", got.AppVersion)
	}

	// Save transcript
	if err := fm.SaveTranscript(report.ID, "The login button is broken"); err != nil {
		t.Fatalf("SaveTranscript: %v", err)
	}
	got, _ = fm.GetFeedback(report.ID)
	if got.Transcript != "The login button is broken" {
		t.Errorf("expected transcript, got %q", got.Transcript)
	}

	// Delete
	if err := fm.DeleteFeedback(report.ID); err != nil {
		t.Fatalf("DeleteFeedback: %v", err)
	}
	if reports := fm.ListFeedback(); len(reports) != 0 {
		t.Fatalf("expected empty after delete, got %d", len(reports))
	}
}

func TestFeedbackGeneratePrompt(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	os.MkdirAll(filepath.Join(tmpDir, ".yaver"), 0700)

	fm, _ := NewFeedbackManager()

	metadata := json.RawMessage(`{
		"source": "in-app-sdk",
		"appVersion": "2.0.0-beta",
		"deviceInfo": {"platform": "android", "model": "Pixel 8", "osVersion": "15"},
		"timeline": [
			{"time": 5.0, "type": "voice", "text": "app crashes on startup"},
			{"time": 10.0, "type": "screenshot", "file": "crash_screen.jpg"}
		],
		"transcript": "The app crashes immediately after the splash screen."
	}`)

	report, _ := fm.ReceiveFeedback(metadata, map[string][]byte{
		"crash_screen.jpg": []byte("img"),
	})

	prompt, err := fm.GenerateFixPrompt(report.ID)
	if err != nil {
		t.Fatalf("GenerateFixPrompt: %v", err)
	}

	if !strings.Contains(prompt, "Bug report from device testing") {
		t.Error("prompt missing header")
	}
	if !strings.Contains(prompt, "Pixel 8") {
		t.Error("prompt missing device info")
	}
	if !strings.Contains(prompt, "app crashes on startup") {
		t.Error("prompt missing timeline voice entry")
	}
	if !strings.Contains(prompt, "crashes immediately after the splash") {
		t.Error("prompt missing transcript")
	}
}

func TestFeedbackHTTPUpload(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	os.MkdirAll(filepath.Join(tmpDir, ".yaver"), 0700)

	fm, _ := NewFeedbackManager()
	srv := &HTTPServer{feedbackMgr: fm}

	// Create multipart request
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)

	// Add metadata
	writer.WriteField("metadata", `{
		"source": "yaver-app",
		"deviceInfo": {"platform": "ios", "model": "iPhone 16", "osVersion": "18.2"}
	}`)

	// Add screenshot file
	part, _ := writer.CreateFormFile("screenshot", "bug.jpg")
	part.Write([]byte("fake-jpeg"))

	// Add video file
	part, _ = writer.CreateFormFile("video", "recording.mp4")
	part.Write([]byte("fake-video"))

	writer.Close()

	req := httptest.NewRequest("POST", "/feedback", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	w := httptest.NewRecorder()
	srv.handleFeedback(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var report FeedbackReport
	json.Unmarshal(w.Body.Bytes(), &report)
	if report.ID == "" {
		t.Fatal("expected report ID")
	}
	if report.VideoPath == "" {
		t.Error("expected video path")
	}
}

func TestFeedbackHTTPList(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	os.MkdirAll(filepath.Join(tmpDir, ".yaver"), 0700)

	fm, _ := NewFeedbackManager()
	srv := &HTTPServer{feedbackMgr: fm}

	req := httptest.NewRequest("GET", "/feedback", nil)
	w := httptest.NewRecorder()
	srv.handleFeedback(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestFeedbackHTTPNotFound(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	os.MkdirAll(filepath.Join(tmpDir, ".yaver"), 0700)

	fm, _ := NewFeedbackManager()
	srv := &HTTPServer{feedbackMgr: fm}

	req := httptest.NewRequest("GET", "/feedback/nonexistent", nil)
	w := httptest.NewRecorder()
	srv.handleFeedbackByID(w, req)

	if w.Code != 404 {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestFeedbackHTTPNoManager(t *testing.T) {
	srv := &HTTPServer{feedbackMgr: nil}

	req := httptest.NewRequest("GET", "/feedback", nil)
	w := httptest.NewRecorder()
	srv.handleFeedback(w, req)

	if w.Code != 503 {
		t.Fatalf("expected 503, got %d", w.Code)
	}
}

func TestFeedbackHTTPVideoServe(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	os.MkdirAll(filepath.Join(tmpDir, ".yaver"), 0700)

	fm, _ := NewFeedbackManager()
	videoContent := []byte("fake-mp4-video-content-for-serving")

	report, _ := fm.ReceiveFeedback(
		json.RawMessage(`{"source":"test","deviceInfo":{"platform":"ios","model":"test","osVersion":"18"}}`),
		map[string][]byte{"recording.mp4": videoContent},
	)

	srv := &HTTPServer{feedbackMgr: fm}

	req := httptest.NewRequest("GET", "/feedback/"+report.ID+"/video", nil)
	w := httptest.NewRecorder()
	srv.handleFeedbackByID(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	respBody, _ := io.ReadAll(w.Body)
	if string(respBody) != string(videoContent) {
		t.Fatal("video content mismatch")
	}
}

// suppress unused import warnings
var _ = http.StatusOK
