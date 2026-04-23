package main

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestNormalizeUnityTestMode(t *testing.T) {
	cases := map[string]string{
		"":         "EditMode",
		"edit":     "EditMode",
		"editmode": "EditMode",
		"play":     "PlayMode",
		"playmode": "PlayMode",
		"all":      "All",
		"weird":    "EditMode",
	}
	for in, want := range cases {
		if got := normalizeUnityTestMode(in); got != want {
			t.Fatalf("normalizeUnityTestMode(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestResolveUnityProjectPath_PrefersExplicitPath(t *testing.T) {
	dir := t.TempDir()
	mustMkdirAll(t, filepath.Join(dir, "ProjectSettings"))
	mustMkdirAll(t, filepath.Join(dir, "Packages"))
	mustWriteFile(t, filepath.Join(dir, "ProjectSettings", "ProjectVersion.txt"), []byte("m_EditorVersion: 2021.3.45f1\n"))
	mustWriteFile(t, filepath.Join(dir, "ProjectSettings", "ProjectSettings.asset"), []byte("productName: Demo\n"))
	mustWriteFile(t, filepath.Join(dir, "Packages", "manifest.json"), []byte("{\"dependencies\":{}}"))

	s := &HTTPServer{}
	got, err := s.resolveUnityProjectPath(httptest.NewRequest("POST", "/unity/test", nil), "", dir)
	if err != nil {
		t.Fatalf("resolveUnityProjectPath err = %v", err)
	}
	if got != dir {
		t.Fatalf("resolveUnityProjectPath = %q, want %q", got, dir)
	}
}

func TestResolveUnityProjectPath_RejectsNonUnityPath(t *testing.T) {
	dir := t.TempDir()
	s := &HTTPServer{}
	_, err := s.resolveUnityProjectPath(httptest.NewRequest("POST", "/unity/test", nil), "", dir)
	if err == nil {
		t.Fatal("expected error for non-Unity path")
	}
}

func TestReadUnityBuildManifestAndResolveExecutablePath(t *testing.T) {
	dir := t.TempDir()
	manifestPath := filepath.Join(dir, "result.json")
	manifest := unityBuildManifest{
		ExecutablePath: "Builds/YaverDemo.exe",
		OutputPath:     "Builds",
		BuildTarget:    "StandaloneWindows64",
		ExecuteMethod:  "YaverBuildTools.BuildWindows64",
	}
	raw, _ := json.Marshal(manifest)
	mustWriteFile(t, manifestPath, raw)

	got := readUnityBuildManifest(manifestPath)
	if got == nil || got.ExecutablePath != manifest.ExecutablePath {
		t.Fatalf("readUnityBuildManifest() = %#v", got)
	}

	resolved := resolveUnityExecutablePath(dir, "Builds", got)
	want := filepath.Join(dir, manifest.ExecutablePath)
	if resolved != want {
		t.Fatalf("resolveUnityExecutablePath() = %q, want %q", resolved, want)
	}
}

func TestRecordUnityRun_CapsHistory(t *testing.T) {
	unityRunHistory.mu.Lock()
	unityRunHistory.items = nil
	unityRunHistory.mu.Unlock()

	for i := 0; i < 55; i++ {
		recordUnityRun(unityRunResponse{Summary: "run"})
	}

	got := listUnityRuns()
	if len(got) != 50 {
		t.Fatalf("len(listUnityRuns()) = %d, want 50", len(got))
	}
}

func mustMkdirAll(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
}

func mustWriteFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
