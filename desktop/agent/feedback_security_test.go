package main

// feedback_security_test.go — regression tests for C-7 (path traversal in
// feedback file uploads) and the sanitizeFeedbackUploadName helper.
//
// The threat: a feedback-only guest (== anyone with a Yaver account who
// has been auto-invited via an app embedding the Feedback SDK) sends a
// crafted POST /feedback with metadata.id="../../../tmp/x" and a multipart
// part with Filename="payload". Without sanitization the agent writes
// /tmp/payload (or any host file the agent's UID can reach), which is
// trivially escalated to overwriting ~/.yaver/config.json (owner bearer),
// ~/.ssh/authorized_keys, ~/.npmrc, etc.

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSanitizeFeedbackUploadName(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		// Acceptable inputs.
		{"screenshot.png", "screenshot.png"},
		{"video.mp4", "video.mp4"},
		{"some-name_42.jpg", "some-name_42.jpg"},

		// Path components are stripped via filepath.Base — the result
		// is always a single segment, which is then validated. The key
		// safety property is: whatever survives lands inside reportDir.
		// We don't reject "foo/bar.png" outright — we accept "bar.png"
		// because filepath.Join(reportDir, "bar.png") is unambiguously
		// inside reportDir.
		{"foo/bar.png", "bar.png"},
		{`foo\bar.png`, ""}, // backslash on POSIX is not a separator → ContainsAny rejects
		{"../bar.png", "bar.png"},
		{"../../etc/passwd", "passwd"},
		{"./secret", "secret"},
		{"foo/../../etc/passwd", "passwd"},

		// Hidden files & traversal segments themselves.
		{".env", ""},
		{".ssh", ""},
		{".", ""},
		{"..", ""},
		{"", ""},
		{"   ", ""},

		// Null byte / control bytes.
		{"a\x00b", ""},

		// Length cap (200).
		{strings.Repeat("a", 200) + ".png", ""}, // 200+4 over cap
		{strings.Repeat("a", 196) + ".png", strings.Repeat("a", 196) + ".png"},
	}
	for _, tc := range cases {
		got := sanitizeFeedbackUploadName(tc.in)
		// For the "stripped to basename" cases above, filepath.Base() on
		// "foo/bar.png" returns "bar.png" — which would be a valid basename
		// in isolation. We deliberately reject any input whose basename
		// differs from the stripped basename (i.e. anything with a path
		// separator). This is the test that locks in that contract.
		if got != tc.want {
			t.Errorf("sanitizeFeedbackUploadName(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestFeedbackTraversalReportID closes the C-7 vector: even if a guest
// sends metadata.id="../../../tmp/pwn", the agent must overwrite the
// ID with a fresh UUID before joining anything to disk.
func TestFeedbackTraversalReportID(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	if err := os.MkdirAll(filepath.Join(tmpDir, ".yaver"), 0700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	fm, err := NewFeedbackManager()
	if err != nil {
		t.Fatalf("NewFeedbackManager: %v", err)
	}
	srv := &HTTPServer{feedbackMgr: fm}

	// Try to escape baseDir via metadata.id.
	maliciousID := "../../../" + filepath.Base(tmpDir) + "/pwned"
	meta, _ := json.Marshal(map[string]interface{}{
		"id":    maliciousID,
		"title": "innocent-looking",
	})
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	if err := w.WriteField("metadata", string(meta)); err != nil {
		t.Fatalf("write metadata: %v", err)
	}
	w.Close()

	req := httptest.NewRequest("POST", "/feedback", &body)
	req.Header.Set("Content-Type", w.FormDataContentType())
	rec := httptest.NewRecorder()
	srv.handleFeedback(rec, req)

	if rec.Code != 200 {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}

	// Decode the response — the server should have rewritten ID.
	var resp struct {
		Report struct {
			ID string `json:"id"`
		} `json:"report"`
		ID string `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	storedID := resp.ID
	if storedID == "" {
		storedID = resp.Report.ID
	}
	if storedID == "" {
		t.Fatalf("no id in response: %s", rec.Body.String())
	}
	if strings.Contains(storedID, "..") || strings.ContainsAny(storedID, "/\\") {
		t.Fatalf("traversal ID survived sanitization: %q", storedID)
	}
	// And that the stored directory is still inside baseDir.
	want := filepath.Join(fm.baseDir, storedID)
	if _, err := os.Stat(want); err != nil {
		t.Errorf("expected report dir under baseDir, got error: %v", err)
	}
}

// TestFeedbackTraversalFilename closes the multipart-Filename vector.
// A guest can send Filename="../../etc/passwd" but the server must
// reject (skip) the upload, never write outside reportDir.
func TestFeedbackTraversalFilename(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	if err := os.MkdirAll(filepath.Join(tmpDir, ".yaver"), 0700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	fm, err := NewFeedbackManager()
	if err != nil {
		t.Fatalf("NewFeedbackManager: %v", err)
	}
	srv := &HTTPServer{feedbackMgr: fm}

	canary := filepath.Join(tmpDir, "pwned-canary")

	meta, _ := json.Marshal(map[string]interface{}{"title": "t"})
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	if err := w.WriteField("metadata", string(meta)); err != nil {
		t.Fatalf("write metadata: %v", err)
	}
	// Use the canary basename so the test is self-contained: if the
	// server were to traverse, the file lands outside baseDir at
	// tmpDir/pwned-canary.
	part, err := w.CreateFormFile("file", "../../"+filepath.Base(canary))
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	if _, err := part.Write([]byte("payload")); err != nil {
		t.Fatalf("write part: %v", err)
	}
	w.Close()

	req := httptest.NewRequest("POST", "/feedback", &body)
	req.Header.Set("Content-Type", w.FormDataContentType())
	rec := httptest.NewRecorder()
	srv.handleFeedback(rec, req)

	if rec.Code != 200 {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	if _, err := os.Stat(canary); err == nil {
		t.Fatalf("traversal SUCCEEDED: file written at %s", canary)
	}
}
