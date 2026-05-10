package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

// TestWebBundleInfoIncludesWorkDir guards the dashboard's cross-project
// staleness check: the poll at WebReloadView.tsx compares the bundle's
// workDir against the user's selected project before promoting a stale
// failed → ready transition. Without workDir in the JSON, the dashboard
// can't tell that a leftover bundle from a *different* project is what
// /dev/web-bundle/ is currently serving, and the iframe re-renders the
// previous project's content after a failed build.
func TestWebBundleInfoIncludesWorkDir(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	workDir := t.TempDir()
	buildDir := filepath.Join(workDir, ".yaver-build-web")

	mgr := NewDevServerManager()
	mgr.SetWebBundleInfo(WebBundleInfo{
		Target:    "web-js-bundle",
		BuildDir:  buildDir,
		WorkDir:   workDir,
		IndexFile: "index.html",
		Size:      1234,
		FileCount: 7,
		BuiltAt:   "2026-05-10T08:00:00Z",
		Caller:    "web-ui-test",
	})
	srv := &HTTPServer{devServerMgr: mgr}

	req := httptest.NewRequest(http.MethodGet, "/dev/web-bundle/info", nil)
	req.RemoteAddr = "127.0.0.1:1234"
	rr := httptest.NewRecorder()
	srv.handleWebBundleInfo(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%q", rr.Code, rr.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal body: %v; raw=%q", err, rr.Body.String())
	}
	if body["built"] != true {
		t.Fatalf("built = %v, want true", body["built"])
	}
	if got, _ := body["workDir"].(string); got != workDir {
		t.Fatalf("workDir = %q, want %q (full body=%v)", got, workDir, body)
	}
	if got, _ := body["buildDir"].(string); got != buildDir {
		t.Fatalf("buildDir = %q, want %q", got, buildDir)
	}
}
