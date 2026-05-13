package main

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDesignReferenceManagerCRUD(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	if err := os.MkdirAll(filepath.Join(tmpDir, ".yaver"), 0700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	mgr, err := NewDesignReferenceManager()
	if err != nil {
		t.Fatalf("NewDesignReferenceManager: %v", err)
	}
	if list := mgr.List(); len(list) != 0 {
		t.Fatalf("expected empty list, got %d", len(list))
	}

	metadata := json.RawMessage(`{
		"kind": "design-reference",
		"source": "browser-extension",
		"mode": "viewport",
		"selector": "html > body",
		"meta": {
			"url": "https://example.com/pricing",
			"title": "Pricing — Example",
			"viewport": {"w": 1440, "h": 900},
			"docSize": {"w": 1440, "h": 3200},
			"userAgent": "Mozilla/5.0 Chrome/138",
			"capturedAt": "2026-05-13T10:00:00Z"
		}
	}`)
	files := map[string][]byte{
		"viewport.png": []byte("fake-png"),
		"dom.html":     []byte("<html><body>hi</body></html>"),
		"styles.json":  []byte(`{"nodes":[{"i":0},{"i":1},{"i":2}],"assets":["https://cdn.example.com/logo.svg"]}`),
	}
	ref, err := mgr.ReceiveReference(metadata, files)
	if err != nil {
		t.Fatalf("ReceiveReference: %v", err)
	}
	if ref.ID == "" {
		t.Fatal("expected ID")
	}
	if ref.URL != "https://example.com/pricing" {
		t.Errorf("URL: got %q", ref.URL)
	}
	if ref.Mode != "viewport" {
		t.Errorf("Mode: got %q", ref.Mode)
	}
	if ref.HTMLPath == "" || !strings.HasSuffix(ref.HTMLPath, "dom.html") {
		t.Errorf("HTMLPath: got %q", ref.HTMLPath)
	}
	if ref.StylesPath == "" || !strings.HasSuffix(ref.StylesPath, "styles.json") {
		t.Errorf("StylesPath: got %q", ref.StylesPath)
	}
	if len(ref.Screenshots) != 1 {
		t.Errorf("Screenshots: got %d", len(ref.Screenshots))
	}
	if ref.NodeCount != 3 {
		t.Errorf("NodeCount: got %d", ref.NodeCount)
	}
	if len(ref.AssetURLs) != 1 || ref.AssetURLs[0] != "https://cdn.example.com/logo.svg" {
		t.Errorf("AssetURLs: got %#v", ref.AssetURLs)
	}
	if ref.Viewport == nil || ref.Viewport.W != 1440 {
		t.Errorf("Viewport: got %#v", ref.Viewport)
	}

	got, ok := mgr.Get(ref.ID)
	if !ok {
		t.Fatal("Get: not found")
	}
	if got.URL != ref.URL {
		t.Errorf("Get URL mismatch")
	}

	list := mgr.List()
	if len(list) != 1 {
		t.Fatalf("expected 1 in list, got %d", len(list))
	}
	if list[0].NumScreens != 1 || list[0].NodeCount != 3 {
		t.Errorf("summary fields wrong: %#v", list[0])
	}

	if err := mgr.Delete(ref.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if list := mgr.List(); len(list) != 0 {
		t.Fatalf("expected empty after delete, got %d", len(list))
	}
	// Reference directory should be gone.
	if _, err := os.Stat(filepath.Join(mgr.baseDir, ref.ID)); !os.IsNotExist(err) {
		t.Errorf("expected directory removed, stat err = %v", err)
	}
}

func TestDesignReferenceHTTPRoundTrip(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	if err := os.MkdirAll(filepath.Join(tmpDir, ".yaver"), 0700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	mgr, err := NewDesignReferenceManager()
	if err != nil {
		t.Fatalf("NewDesignReferenceManager: %v", err)
	}
	srv := &HTTPServer{designRefMgr: mgr}

	// POST upload.
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	w.WriteField("metadata", `{
		"kind": "design-reference",
		"source": "browser-extension",
		"mode": "fullpage",
		"meta": {"url": "https://linear.app", "title": "Linear"}
	}`)
	part, _ := w.CreateFormFile("screenshot_0", "viewport.png")
	part.Write([]byte("\x89PNG\r\n\x1a\n"))
	part, _ = w.CreateFormFile("html", "dom.html")
	part.Write([]byte("<html></html>"))
	part, _ = w.CreateFormFile("styles", "styles.json")
	part.Write([]byte(`{"nodes":[{"i":0}],"assets":[]}`))
	w.Close()

	req := httptest.NewRequest(http.MethodPost, "/design-references", &body)
	req.Header.Set("Content-Type", w.FormDataContentType())
	rec := httptest.NewRecorder()
	srv.handleDesignReferences(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST: code=%d body=%s", rec.Code, rec.Body.String())
	}
	var uploaded DesignReference
	if err := json.Unmarshal(rec.Body.Bytes(), &uploaded); err != nil {
		t.Fatalf("unmarshal upload: %v", err)
	}
	if uploaded.URL != "https://linear.app" {
		t.Errorf("uploaded URL: %q", uploaded.URL)
	}
	if uploaded.Mode != "fullpage" {
		t.Errorf("uploaded Mode: %q", uploaded.Mode)
	}

	// GET list.
	listReq := httptest.NewRequest(http.MethodGet, "/design-references", nil)
	listRec := httptest.NewRecorder()
	srv.handleDesignReferences(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("GET list: %d", listRec.Code)
	}
	var summaries []DesignReferenceSummary
	if err := json.Unmarshal(listRec.Body.Bytes(), &summaries); err != nil {
		t.Fatalf("unmarshal list: %v", err)
	}
	if len(summaries) != 1 || summaries[0].ID != uploaded.ID {
		t.Fatalf("list mismatch: %#v", summaries)
	}

	// GET single record.
	getReq := httptest.NewRequest(http.MethodGet, "/design-references/"+uploaded.ID, nil)
	getRec := httptest.NewRecorder()
	srv.handleDesignReferenceByID(getRec, getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("GET id: %d body=%s", getRec.Code, getRec.Body.String())
	}

	// GET html file.
	htmlReq := httptest.NewRequest(http.MethodGet, "/design-references/"+uploaded.ID+"/html", nil)
	htmlRec := httptest.NewRecorder()
	srv.handleDesignReferenceByID(htmlRec, htmlReq)
	if htmlRec.Code != http.StatusOK {
		t.Fatalf("GET html: %d", htmlRec.Code)
	}
	if !strings.Contains(htmlRec.Body.String(), "<html>") {
		t.Errorf("html body unexpected: %q", htmlRec.Body.String())
	}

	// DELETE.
	delReq := httptest.NewRequest(http.MethodDelete, "/design-references/"+uploaded.ID, nil)
	delRec := httptest.NewRecorder()
	srv.handleDesignReferenceByID(delRec, delReq)
	if delRec.Code != http.StatusOK {
		t.Fatalf("DELETE: %d body=%s", delRec.Code, delRec.Body.String())
	}
	if list := mgr.List(); len(list) != 0 {
		t.Fatalf("expected empty after delete, got %d", len(list))
	}
}

func TestDesignReferenceRejectsPathTraversal(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	os.MkdirAll(filepath.Join(tmpDir, ".yaver"), 0700)
	mgr, err := NewDesignReferenceManager()
	if err != nil {
		t.Fatalf("NewDesignReferenceManager: %v", err)
	}
	ref, err := mgr.ReceiveReference(
		json.RawMessage(`{"source":"browser-extension","mode":"viewport","meta":{"url":"https://x"}}`),
		map[string][]byte{
			"../escape.png":          []byte("nope"),
			"/etc/passwd":            []byte("nope"),
			"normal.png":             []byte("ok"),
			".hidden.png":            []byte("nope"),
			strings.Repeat("x", 300): []byte("nope"),
		},
	)
	if err != nil {
		t.Fatalf("ReceiveReference: %v", err)
	}
	// Safety invariant: every accepted screenshot lives inside the per-
	// reference directory, regardless of how the multipart name was
	// reduced. We piggyback on the feedback sanitizer (which basenames
	// + rejects hidden/empty/oversized), so "../escape.png" becomes
	// "escape.png" inside baseDir — annoying but not unsafe.
	refDir := filepath.Join(mgr.baseDir, ref.ID)
	for _, p := range ref.Screenshots {
		if !pathInsideFeedbackBaseDir(refDir, p) {
			t.Errorf("screenshot %q escapes ref dir %q", p, refDir)
		}
	}
	// Hidden file must not appear on disk. (The 300-char name is rejected
	// by the sanitizer and additionally would trip the kernel's
	// ENAMETOOLONG — covered by the "screenshots all under refDir"
	// invariant above.)
	if _, err := os.Stat(filepath.Join(refDir, ".hidden.png")); !os.IsNotExist(err) {
		t.Errorf("hidden filename persisted: err=%v", err)
	}
	// And normal.png must have been written.
	if _, err := os.Stat(filepath.Join(refDir, "normal.png")); err != nil {
		t.Errorf("normal.png missing: %v", err)
	}
}
