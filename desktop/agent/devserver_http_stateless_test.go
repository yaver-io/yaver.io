package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestBuildNative_RejectsWithoutProject locks in the stateless contract:
// /dev/build-native must always be told which guest project to bundle.
// Falling back to devServerMgr.Status().WorkDir let webview/vite/next
// dev-server state silently dictate which project the Hermes bundle
// came from — across devices and across the monorepo auto-pick path
// in devserver.go::Manager.Start. Refuse instead.
func TestBuildNative_RejectsWithoutProject(t *testing.T) {
	srv := &HTTPServer{devServerMgr: NewDevServerManager()}

	req := httptest.NewRequest(http.MethodPost, "/dev/build-native",
		bytes.NewReader([]byte(`{"platform":"ios"}`)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.handleBuildNativeBundle(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body = %s", rec.Code, rec.Body.String())
	}
	var resp map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v; body = %s", err, rec.Body.String())
	}
	if resp["code"] != "PROJECT_REQUIRED" {
		t.Fatalf("code = %q, want PROJECT_REQUIRED; body = %s", resp["code"], rec.Body.String())
	}
}

// TestBuildNative_RejectsNonHermesFramework guards the second half of
// the stateless contract: even when a project path is supplied, the
// Hermes/Metro pipeline must not silently chew on a Vite or Next.js
// workdir. detectFramework() looks at config files + package.json
// keywords; a Vite project gets detected as "vite" and rejected.
func TestBuildNative_RejectsNonHermesFramework(t *testing.T) {
	tmp := t.TempDir()
	viteDir := filepath.Join(tmp, "my-vite-app")
	if err := os.MkdirAll(viteDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// A Vite project is identified by vite.config.js — no package.json
	// peek required for detectFramework() to label it correctly.
	if err := os.WriteFile(filepath.Join(viteDir, "vite.config.js"),
		[]byte("export default {};\n"), 0o644); err != nil {
		t.Fatalf("write vite.config.js: %v", err)
	}
	if err := os.WriteFile(filepath.Join(viteDir, "package.json"),
		[]byte(`{"name":"vite-app","dependencies":{"vite":"^5.0.0"}}`), 0o644); err != nil {
		t.Fatalf("write package.json: %v", err)
	}

	srv := &HTTPServer{devServerMgr: NewDevServerManager()}

	body, _ := json.Marshal(map[string]string{
		"platform":    "ios",
		"projectPath": viteDir,
	})
	req := httptest.NewRequest(http.MethodPost, "/dev/build-native", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.handleBuildNativeBundle(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body = %s", rec.Code, rec.Body.String())
	}
	var resp map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v; body = %s", err, rec.Body.String())
	}
	if resp["code"] != "FRAMEWORK_NOT_SUPPORTED" {
		t.Fatalf("code = %q, want FRAMEWORK_NOT_SUPPORTED; body = %s",
			resp["code"], rec.Body.String())
	}
}

// TestBuildNative_RejectsReactWebProject covers the third branch of
// the framework gate: a vanilla React (CRA / non-RN) project. The gate
// must produce the same FRAMEWORK_NOT_SUPPORTED response as the Vite
// case, with the detected framework name surfaced so the user knows
// what got picked. This is the typical "user pointed Yaver at the
// wrong sub-project of a monorepo" foot-gun.
func TestBuildNative_RejectsReactWebProject(t *testing.T) {
	tmp := t.TempDir()
	reactDir := filepath.Join(tmp, "react-only")
	if err := os.MkdirAll(reactDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(reactDir, "package.json"),
		[]byte(`{"name":"react-only","dependencies":{"react":"^19.1.0"}}`),
		0o644); err != nil {
		t.Fatalf("write package.json: %v", err)
	}

	srv := &HTTPServer{devServerMgr: NewDevServerManager()}

	body, _ := json.Marshal(map[string]string{
		"platform":    "ios",
		"projectPath": reactDir,
	})
	req := httptest.NewRequest(http.MethodPost, "/dev/build-native", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.handleBuildNativeBundle(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body = %s", rec.Code, rec.Body.String())
	}
	var resp map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v; body = %s", err, rec.Body.String())
	}
	if resp["code"] != "FRAMEWORK_NOT_SUPPORTED" {
		t.Fatalf("code = %q, want FRAMEWORK_NOT_SUPPORTED; body = %s",
			resp["code"], rec.Body.String())
	}
	// The error string must name the detected framework so the operator
	// can trace which workdir got mis-pointed at the Hermes path.
	if !strings.Contains(resp["error"], "react") {
		t.Fatalf("error should name detected framework %q; got %q", "react", resp["error"])
	}
}
