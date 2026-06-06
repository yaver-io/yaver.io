package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Round-trip: PUT writes a file (creating parent dirs + preserving mode), GET
// streams it back byte-identical with the mode advertised. Auth is bypassed
// here (handler called directly) since s.auth is exercised elsewhere.
func TestFleetFilePutGetRoundTrip(t *testing.T) {
	s := &HTTPServer{}
	dir := t.TempDir()
	target := filepath.Join(dir, "nested", "artifact.bin")
	payload := []byte("hello fleet \x00\x01\x02 binary-safe")

	// PUT
	putReq := httptest.NewRequest(http.MethodPost, "/fleet/file?path="+target+"&mode=600", bytes.NewReader(payload))
	putRec := httptest.NewRecorder()
	s.handleFleetFile(putRec, putReq)
	if putRec.Code != http.StatusOK {
		t.Fatalf("PUT status = %d, body %s", putRec.Code, putRec.Body.String())
	}
	onDisk, err := os.ReadFile(target)
	if err != nil || !bytes.Equal(onDisk, payload) {
		t.Fatalf("file content mismatch: err=%v", err)
	}
	if info, _ := os.Stat(target); info != nil && info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %o, want 600", info.Mode().Perm())
	}

	// GET
	getReq := httptest.NewRequest(http.MethodGet, "/fleet/file?path="+target, nil)
	getRec := httptest.NewRecorder()
	s.handleFleetFile(getRec, getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("GET status = %d", getRec.Code)
	}
	if !bytes.Equal(getRec.Body.Bytes(), payload) {
		t.Fatalf("GET body mismatch")
	}
	if mode := getRec.Header().Get("X-Yaver-File-Mode"); mode != "600" {
		t.Fatalf("X-Yaver-File-Mode = %q, want 600", mode)
	}
}

func TestFleetFileRejectsRelativeAndDirs(t *testing.T) {
	s := &HTTPServer{}
	dir := t.TempDir()

	// relative path → 400
	rec := httptest.NewRecorder()
	s.handleFleetFile(rec, httptest.NewRequest(http.MethodGet, "/fleet/file?path=relative/x", nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("relative path: status = %d, want 400", rec.Code)
	}

	// directory → 404 (not a file)
	rec = httptest.NewRecorder()
	s.handleFleetFile(rec, httptest.NewRequest(http.MethodGet, "/fleet/file?path="+dir, nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("dir GET: status = %d, want 404", rec.Code)
	}

	// wrong method → 405
	rec = httptest.NewRecorder()
	s.handleFleetFile(rec, httptest.NewRequest(http.MethodDelete, "/fleet/file?path="+filepath.Join(dir, "x"), nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("DELETE: status = %d, want 405", rec.Code)
	}
}

func TestFleetFileMissingPath(t *testing.T) {
	s := &HTTPServer{}
	rec := httptest.NewRecorder()
	s.handleFleetFile(rec, httptest.NewRequest(http.MethodPost, "/fleet/file", strings.NewReader("x")))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("missing path: status = %d, want 400", rec.Code)
	}
}
