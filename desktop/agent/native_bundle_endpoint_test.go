package main

import (
	"archive/zip"
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestNativeBundleEndpointUsesBuildSpecificInfoWithoutActiveProject(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	workDir := t.TempDir()
	buildDir := filepath.Join(workDir, ".yaver-build")
	if err := os.MkdirAll(buildDir, 0o755); err != nil {
		t.Fatalf("mkdir build dir: %v", err)
	}
	bundlePath := filepath.Join(buildDir, "main.jsbundle")
	want := []byte("fake-hermes-bundle")
	if err := os.WriteFile(bundlePath, want, 0o644); err != nil {
		t.Fatalf("write bundle: %v", err)
	}

	mgr := NewDevServerManager()
	mgr.SetNativeBundleInfo(NativeBundleInfo{
		BuildID:      "ios-build-1",
		WorkDir:      workDir,
		BuildDir:     buildDir,
		BundlePath:   bundlePath,
		Platform:     "ios",
		ModuleName:   "main",
		MetadataJSON: `{"bcVersion":96}`,
	})
	srv := &HTTPServer{devServerMgr: mgr}

	req := httptest.NewRequest(http.MethodGet, "/dev/native-bundle?build=ios-build-1", nil)
	rr := httptest.NewRecorder()
	srv.handleServeNativeBundle(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%q", rr.Code, rr.Body.String())
	}
	if got := rr.Header().Get("X-Yaver-Bundle-Metadata"); got != `{"bcVersion":96}` {
		t.Fatalf("metadata header = %q", got)
	}
	if !bytes.Equal(rr.Body.Bytes(), want) {
		t.Fatalf("bundle bytes = %q, want %q", rr.Body.Bytes(), want)
	}
}

func TestNativeAssetsEndpointUsesBuildSpecificInfoWithoutActiveProject(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	workDir := t.TempDir()
	assetsDir := filepath.Join(workDir, ".yaver-build", "assets")
	if err := os.MkdirAll(filepath.Join(assetsDir, "nested"), 0o755); err != nil {
		t.Fatalf("mkdir assets dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(assetsDir, "nested", "asset.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatalf("write asset: %v", err)
	}
	bundlePath := filepath.Join(workDir, ".yaver-build", "main.jsbundle")
	if err := os.WriteFile(bundlePath, []byte("bundle"), 0o644); err != nil {
		t.Fatalf("write bundle: %v", err)
	}

	mgr := NewDevServerManager()
	mgr.SetNativeBundleInfo(NativeBundleInfo{
		BuildID:    "android-build-1",
		WorkDir:    workDir,
		BuildDir:   filepath.Dir(bundlePath),
		BundlePath: bundlePath,
		AssetsDir:  assetsDir,
		Platform:   "android",
		ModuleName: "main",
	})
	srv := &HTTPServer{devServerMgr: mgr}

	req := httptest.NewRequest(http.MethodGet, "/dev/native-assets?build=android-build-1", nil)
	rr := httptest.NewRecorder()
	srv.handleServeNativeAssets(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%q", rr.Code, rr.Body.String())
	}
	zr, err := zip.NewReader(bytes.NewReader(rr.Body.Bytes()), int64(rr.Body.Len()))
	if err != nil {
		t.Fatalf("zip.NewReader: %v", err)
	}
	if len(zr.File) != 1 || zr.File[0].Name != "nested/asset.txt" {
		t.Fatalf("zip entries = %#v", zr.File)
	}
	rc, err := zr.File[0].Open()
	if err != nil {
		t.Fatalf("open zip entry: %v", err)
	}
	defer rc.Close()
	data, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("read zip entry: %v", err)
	}
	if string(data) != "hello" {
		t.Fatalf("asset payload = %q, want hello", string(data))
	}
}

func TestNativeBundleInfoPersistsAcrossManagerRestart(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	workDir := t.TempDir()
	buildDir := filepath.Join(workDir, ".yaver-build")
	if err := os.MkdirAll(buildDir, 0o755); err != nil {
		t.Fatalf("mkdir build dir: %v", err)
	}
	bundlePath := filepath.Join(buildDir, "main.jsbundle")
	if err := os.WriteFile(bundlePath, []byte("bundle"), 0o644); err != nil {
		t.Fatalf("write bundle: %v", err)
	}

	mgr := NewDevServerManager()
	mgr.SetNativeBundleInfo(NativeBundleInfo{
		BuildID:    "persisted-build",
		WorkDir:    workDir,
		BuildDir:   buildDir,
		BundlePath: bundlePath,
		Platform:   "ios",
		ModuleName: "main",
	})

	restarted := NewDevServerManager()
	info := restarted.GetNativeBundleInfo("persisted-build")
	if info.BuildID != "persisted-build" {
		t.Fatalf("build id = %q, want persisted-build", info.BuildID)
	}
	if info.BundlePath != bundlePath {
		t.Fatalf("bundle path = %q, want %q", info.BundlePath, bundlePath)
	}
}
