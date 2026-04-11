package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// --- Edge Cases ---

func TestFeedbackEmptyMetadata(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	os.MkdirAll(filepath.Join(tmpDir, ".yaver"), 0700)

	fm, _ := NewFeedbackManager()
	srv := &HTTPServer{feedbackMgr: fm}

	// Upload with empty metadata
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	w.WriteField("metadata", "")
	w.Close()

	req := httptest.NewRequest("POST", "/feedback", &body)
	req.Header.Set("Content-Type", w.FormDataContentType())
	rec := httptest.NewRecorder()
	srv.handleFeedback(rec, req)

	if rec.Code != 400 {
		t.Fatalf("empty metadata: expected 400, got %d", rec.Code)
	}
}

func TestFeedbackInvalidJSON(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	os.MkdirAll(filepath.Join(tmpDir, ".yaver"), 0700)

	fm, _ := NewFeedbackManager()
	srv := &HTTPServer{feedbackMgr: fm}

	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	w.WriteField("metadata", "{invalid json!!!")
	w.Close()

	req := httptest.NewRequest("POST", "/feedback", &body)
	req.Header.Set("Content-Type", w.FormDataContentType())
	rec := httptest.NewRecorder()
	srv.handleFeedback(rec, req)

	if rec.Code != 400 {
		t.Fatalf("invalid json: expected 400, got %d", rec.Code)
	}
}

func TestFeedbackNoFiles(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	os.MkdirAll(filepath.Join(tmpDir, ".yaver"), 0700)

	fm, _ := NewFeedbackManager()
	srv := &HTTPServer{feedbackMgr: fm}

	// Metadata only, no files
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	w.WriteField("metadata", `{"source":"test","deviceInfo":{"platform":"web","model":"Chrome","osVersion":"126"}}`)
	w.Close()

	req := httptest.NewRequest("POST", "/feedback", &body)
	req.Header.Set("Content-Type", w.FormDataContentType())
	rec := httptest.NewRecorder()
	srv.handleFeedback(rec, req)

	if rec.Code != 200 {
		t.Fatalf("no files: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var report FeedbackReport
	json.Unmarshal(rec.Body.Bytes(), &report)
	if report.VideoPath != "" {
		t.Error("expected no video path")
	}
	if len(report.Screenshots) != 0 {
		t.Error("expected no screenshots")
	}
}

func TestFeedbackMultipleScreenshots(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	os.MkdirAll(filepath.Join(tmpDir, ".yaver"), 0700)

	fm, _ := NewFeedbackManager()
	srv := &HTTPServer{feedbackMgr: fm}

	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	w.WriteField("metadata", `{"source":"test","deviceInfo":{"platform":"ios","model":"iPhone","osVersion":"18"}}`)
	for i := 0; i < 5; i++ {
		part, _ := w.CreateFormFile(fmt.Sprintf("screenshot_%d", i), fmt.Sprintf("screen_%d.jpg", i))
		part.Write([]byte(fmt.Sprintf("jpeg-data-%d", i)))
	}
	w.Close()

	req := httptest.NewRequest("POST", "/feedback", &body)
	req.Header.Set("Content-Type", w.FormDataContentType())
	rec := httptest.NewRecorder()
	srv.handleFeedback(rec, req)

	if rec.Code != 200 {
		t.Fatalf("multi screenshots: expected 200, got %d", rec.Code)
	}

	var report FeedbackReport
	json.Unmarshal(rec.Body.Bytes(), &report)
	if len(report.Screenshots) != 5 {
		t.Fatalf("expected 5 screenshots, got %d", len(report.Screenshots))
	}
}

func TestFeedbackLargeTimeline(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	os.MkdirAll(filepath.Join(tmpDir, ".yaver"), 0700)

	fm, _ := NewFeedbackManager()

	// Create 100 timeline events
	timeline := make([]TimelineEvent, 100)
	for i := 0; i < 100; i++ {
		timeline[i] = TimelineEvent{
			Time: float64(i),
			Type: "annotation",
			Text: fmt.Sprintf("Event %d: something happened", i),
		}
	}

	metadata := FeedbackReport{
		Source: "stress-test",
		DeviceInfo: DeviceFBInfo{Platform: "android", Model: "Pixel", OSVersion: "15"},
		Timeline:   timeline,
	}

	metaBytes, _ := json.Marshal(metadata)
	report, err := fm.ReceiveFeedback(metaBytes, nil)
	if err != nil {
		t.Fatalf("large timeline: %v", err)
	}
	if len(report.Timeline) != 100 {
		t.Fatalf("expected 100 events, got %d", len(report.Timeline))
	}
}

func TestFeedbackPromptWithAllEventTypes(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	os.MkdirAll(filepath.Join(tmpDir, ".yaver"), 0700)

	fm, _ := NewFeedbackManager()
	metadata := json.RawMessage(`{
		"source": "test",
		"deviceInfo": {"platform": "ios", "model": "iPhone 16 Pro", "osVersion": "18.2"},
		"appVersion": "3.0.0-rc1",
		"timeline": [
			{"time": 1.0, "type": "voice", "text": "Starting the test session"},
			{"time": 5.0, "type": "screenshot", "file": "home_screen.png"},
			{"time": 8.0, "type": "annotation", "text": "The bottom nav is misaligned"},
			{"time": 12.0, "type": "crash", "text": "SIGABRT in UITableView"},
			{"time": 15.0, "type": "voice", "text": "App crashed when scrolling the list"}
		],
		"transcript": "The app crashes when scrolling the list view. Also the bottom navigation bar is misaligned on iPhone 16 Pro."
	}`)

	report, _ := fm.ReceiveFeedback(metadata, map[string][]byte{
		"home_screen.png": []byte("png-data"),
		"recording.mp4":   []byte("video-data"),
	})

	prompt, err := fm.GenerateFixPrompt(report.ID)
	if err != nil {
		t.Fatalf("GenerateFixPrompt: %v", err)
	}

	// Verify all sections present
	checks := []struct {
		contains string
		label    string
	}{
		{"Bug report from device testing", "header"},
		{"iPhone 16 Pro", "device model"},
		{"3.0.0-rc1", "app version"},
		{"[voice] \"Starting the test session\"", "voice event"},
		{"[screenshot] home_screen.png", "screenshot event"},
		{"[note] The bottom nav is misaligned", "annotation event"},
		{"[CRASH] SIGABRT in UITableView", "crash event"},
		{"app crashes when scrolling", "transcript"},
		{"Screenshots attached: 1", "screenshot count"},
		{"recording.mp4", "video reference"},
		{"Please fix these issues", "closing instruction"},
	}

	for _, c := range checks {
		if !strings.Contains(prompt, c.contains) {
			t.Errorf("prompt missing %s: expected to contain %q", c.label, c.contains)
		}
	}
}

func TestFeedbackConcurrentUploads(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	os.MkdirAll(filepath.Join(tmpDir, ".yaver"), 0700)

	fm, _ := NewFeedbackManager()

	// Upload 10 reports concurrently
	done := make(chan error, 10)
	for i := 0; i < 10; i++ {
		go func(idx int) {
			meta := json.RawMessage(fmt.Sprintf(`{
				"source": "concurrent-%d",
				"deviceInfo": {"platform": "android", "model": "Pixel %d", "osVersion": "15"}
			}`, idx, idx))
			_, err := fm.ReceiveFeedback(meta, map[string][]byte{
				fmt.Sprintf("screen_%d.jpg", idx): []byte(fmt.Sprintf("img-%d", idx)),
			})
			done <- err
		}(i)
	}

	for i := 0; i < 10; i++ {
		if err := <-done; err != nil {
			t.Fatalf("concurrent upload %d failed: %v", i, err)
		}
	}

	reports := fm.ListFeedback()
	if len(reports) != 10 {
		t.Fatalf("expected 10 reports, got %d", len(reports))
	}
}

func TestFeedbackDeleteNonExistent(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	os.MkdirAll(filepath.Join(tmpDir, ".yaver"), 0700)

	fm, _ := NewFeedbackManager()
	err := fm.DeleteFeedback("nonexistent-id")
	if err == nil {
		t.Fatal("expected error deleting non-existent feedback")
	}
}

func TestFeedbackFixNonExistent(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	os.MkdirAll(filepath.Join(tmpDir, ".yaver"), 0700)

	fm, _ := NewFeedbackManager()
	_, err := fm.GenerateFixPrompt("nonexistent-id")
	if err == nil {
		t.Fatal("expected error for non-existent feedback")
	}
}

func TestFeedbackTranscriptNonExistent(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	os.MkdirAll(filepath.Join(tmpDir, ".yaver"), 0700)

	fm, _ := NewFeedbackManager()
	err := fm.SaveTranscript("nonexistent-id", "test")
	if err == nil {
		t.Fatal("expected error for non-existent feedback")
	}
}

func TestFeedbackHTTPMethodNotAllowed(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	os.MkdirAll(filepath.Join(tmpDir, ".yaver"), 0700)

	fm, _ := NewFeedbackManager()
	srv := &HTTPServer{feedbackMgr: fm}

	// PUT to /feedback should be 405
	req := httptest.NewRequest("PUT", "/feedback", nil)
	rec := httptest.NewRecorder()
	srv.handleFeedback(rec, req)
	if rec.Code != 405 {
		t.Fatalf("PUT /feedback: expected 405, got %d", rec.Code)
	}

	// GET to /feedback/stream should be 405
	req = httptest.NewRequest("GET", "/feedback/stream", nil)
	rec = httptest.NewRecorder()
	srv.handleFeedbackStream(rec, req)
	if rec.Code != 405 {
		t.Fatalf("GET /feedback/stream: expected 405, got %d", rec.Code)
	}
}

func TestFeedbackHTTPDeleteAndVerify(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	os.MkdirAll(filepath.Join(tmpDir, ".yaver"), 0700)

	fm, _ := NewFeedbackManager()
	srv := &HTTPServer{feedbackMgr: fm}

	// Upload
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	w.WriteField("metadata", `{"source":"delete-test","deviceInfo":{"platform":"web","model":"test","osVersion":"1"}}`)
	part, _ := w.CreateFormFile("video", "test.mp4")
	part.Write([]byte("video-data"))
	w.Close()

	req := httptest.NewRequest("POST", "/feedback", &body)
	req.Header.Set("Content-Type", w.FormDataContentType())
	rec := httptest.NewRecorder()
	srv.handleFeedback(rec, req)

	var report FeedbackReport
	json.Unmarshal(rec.Body.Bytes(), &report)

	// Verify files exist on disk
	reportDir := filepath.Join(tmpDir, ".yaver", "feedback", report.ID)
	if _, err := os.Stat(reportDir); err != nil {
		t.Fatal("report dir should exist")
	}

	// Delete
	req = httptest.NewRequest("DELETE", "/feedback/"+report.ID, nil)
	rec = httptest.NewRecorder()
	srv.handleFeedbackByID(rec, req)
	if rec.Code != 200 {
		t.Fatalf("delete: expected 200, got %d", rec.Code)
	}

	// Verify files removed from disk
	if _, err := os.Stat(reportDir); !os.IsNotExist(err) {
		t.Fatal("report dir should be deleted from disk")
	}
}

func TestFeedbackTranscriptUpdate(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	os.MkdirAll(filepath.Join(tmpDir, ".yaver"), 0700)

	fm, _ := NewFeedbackManager()
	srv := &HTTPServer{feedbackMgr: fm}

	// Upload
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	w.WriteField("metadata", `{"source":"transcript-test","deviceInfo":{"platform":"ios","model":"test","osVersion":"18"}}`)
	w.Close()

	req := httptest.NewRequest("POST", "/feedback", &body)
	req.Header.Set("Content-Type", w.FormDataContentType())
	rec := httptest.NewRecorder()
	srv.handleFeedback(rec, req)

	var report FeedbackReport
	json.Unmarshal(rec.Body.Bytes(), &report)

	// Update transcript via HTTP
	req = httptest.NewRequest("POST", "/feedback/"+report.ID+"/transcript",
		strings.NewReader(`{"transcript":"Updated via whisper STT on mobile device"}`))
	rec = httptest.NewRecorder()
	srv.handleFeedbackByID(rec, req)
	if rec.Code != 200 {
		t.Fatalf("transcript update: expected 200, got %d", rec.Code)
	}

	// Verify transcript persisted
	got, _ := fm.GetFeedback(report.ID)
	if got.Transcript != "Updated via whisper STT on mobile device" {
		t.Fatalf("transcript not updated: %q", got.Transcript)
	}

	// Verify it survives manager restart (persistence)
	fm2, _ := NewFeedbackManager()
	got2, ok := fm2.GetFeedback(report.ID)
	if !ok {
		t.Fatal("report not found after restart")
	}
	if got2.Transcript != "Updated via whisper STT on mobile device" {
		t.Fatalf("transcript not persisted: %q", got2.Transcript)
	}
}

func TestFeedbackFileTypes(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	os.MkdirAll(filepath.Join(tmpDir, ".yaver"), 0700)

	fm, _ := NewFeedbackManager()

	// Test various file extensions are categorized correctly
	files := map[string][]byte{
		"recording.mp4":   []byte("video"),
		"screen.mov":      []byte("video2"),
		"voice.m4a":       []byte("audio"),
		"narration.aac":   []byte("audio2"),
		"raw.wav":         []byte("audio3"),
		"bug1.jpg":        []byte("screenshot1"),
		"bug2.png":        []byte("screenshot2"),
		"random.txt":      []byte("other"),
	}

	report, err := fm.ReceiveFeedback(
		json.RawMessage(`{"source":"filetype-test","deviceInfo":{"platform":"test","model":"test","osVersion":"1"}}`),
		files,
	)
	if err != nil {
		t.Fatalf("ReceiveFeedback: %v", err)
	}

	// Should detect video (last .mp4 or .mov wins)
	if report.VideoPath == "" {
		t.Error("expected video path")
	}
	// Should detect audio
	if report.AudioPath == "" {
		t.Error("expected audio path")
	}
	// Should have 2 screenshots (jpg + png)
	if len(report.Screenshots) != 2 {
		t.Errorf("expected 2 screenshots, got %d", len(report.Screenshots))
	}
}

func TestFeedbackListOrdering(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	os.MkdirAll(filepath.Join(tmpDir, ".yaver"), 0700)

	fm, _ := NewFeedbackManager()

	// Create 3 reports
	for i := 0; i < 3; i++ {
		fm.ReceiveFeedback(
			json.RawMessage(fmt.Sprintf(`{"source":"order-%d","deviceInfo":{"platform":"test","model":"test","osVersion":"1"}}`, i)),
			nil,
		)
	}

	reports := fm.ListFeedback()
	if len(reports) != 3 {
		t.Fatalf("expected 3 reports, got %d", len(reports))
	}

	// Should be newest first
	for i := 0; i < len(reports)-1; i++ {
		if reports[i].CreatedAt < reports[i+1].CreatedAt {
			t.Error("reports should be sorted newest first")
		}
	}
}
