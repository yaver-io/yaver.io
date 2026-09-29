package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func artifactWriteFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// realTempDir returns a symlink-resolved temp dir so prefix checks are stable
// on macOS (/var → /private/var).
func realTempDir(t *testing.T) string {
	t.Helper()
	d := t.TempDir()
	if r, err := filepath.EvalSymlinks(d); err == nil {
		return r
	}
	return d
}

func TestResolveArtifactPathRefusesOutside(t *testing.T) {
	outside := realTempDir(t)
	f := filepath.Join(outside, "secret.txt")
	artifactWriteFile(t, f, []byte("x"))
	if _, err := resolveArtifactPath(f); err == nil {
		t.Fatal("a path outside every allowed root must be refused")
	}
}

func TestResolveArtifactPathAllowsEnvRoot(t *testing.T) {
	dir := realTempDir(t)
	t.Setenv("YAVER_ARTIFACT_ROOTS", dir)
	f := filepath.Join(dir, "out.pdf")
	artifactWriteFile(t, f, []byte("%PDF-1.4"))
	got, err := resolveArtifactPath(f)
	if err != nil {
		t.Fatalf("expected allowed, got %v", err)
	}
	if got == "" {
		t.Fatal("empty resolved path")
	}
}

func TestResolveArtifactPathRejectsSymlinkEscape(t *testing.T) {
	root := realTempDir(t)
	t.Setenv("YAVER_ARTIFACT_ROOTS", root)
	outside := realTempDir(t)
	secret := filepath.Join(outside, "secret.txt")
	artifactWriteFile(t, secret, []byte("top"))
	link := filepath.Join(root, "link.txt")
	if err := os.Symlink(secret, link); err != nil {
		t.Skip("symlinks unsupported here")
	}
	if _, err := resolveArtifactPath(link); err == nil {
		t.Fatal("a symlink inside a root that points outside it must be refused")
	}
}

func TestArtifactFetchReturnsBytes(t *testing.T) {
	dir := realTempDir(t)
	t.Setenv("YAVER_ARTIFACT_ROOTS", dir)
	f := filepath.Join(dir, "report.pdf")
	artifactWriteFile(t, f, []byte("%PDF-1.4 hello"))
	payload, _ := json.Marshal(map[string]interface{}{"path": f})
	res := artifactFetchHandler(OpsContext{Ctx: context.Background()}, payload)
	if !res.OK {
		t.Fatalf("fetch failed: code=%s err=%s", res.Code, res.Error)
	}
	m, ok := res.Initial.(map[string]interface{})
	if !ok {
		t.Fatalf("unexpected Initial: %T", res.Initial)
	}
	if m["mime"] != "application/pdf" {
		t.Fatalf("mime = %v", m["mime"])
	}
	if b64, _ := m["base64"].(string); b64 == "" {
		t.Fatal("missing base64")
	}
	if sha, _ := m["sha256"].(string); sha == "" {
		t.Fatal("missing sha256")
	}
}

func TestArtifactFetchTooLarge(t *testing.T) {
	dir := realTempDir(t)
	t.Setenv("YAVER_ARTIFACT_ROOTS", dir)
	f := filepath.Join(dir, "big.bin")
	artifactWriteFile(t, f, make([]byte, 2048))
	payload, _ := json.Marshal(map[string]interface{}{"path": f, "maxBytes": 10})
	res := artifactFetchHandler(OpsContext{}, payload)
	if res.OK || res.Code != "too_large" {
		t.Fatalf("want too_large, got ok=%v code=%s", res.OK, res.Code)
	}
}

func TestArtifactFetchForbiddenNamesRoots(t *testing.T) {
	outside := realTempDir(t)
	f := filepath.Join(outside, "s.txt")
	artifactWriteFile(t, f, []byte("x"))
	payload, _ := json.Marshal(map[string]interface{}{"path": f})
	res := artifactFetchHandler(OpsContext{}, payload)
	if res.OK || res.Code != "forbidden" {
		t.Fatalf("want forbidden, got ok=%v code=%s", res.OK, res.Code)
	}
	if res.Error == "" {
		t.Fatal("a refusal must name the allowed roots (route to the fix)")
	}
}

func TestMimeForExt(t *testing.T) {
	cases := map[string]string{
		".pdf": "application/pdf", ".png": "image/png", ".dwg": "application/octet-stream",
		".md": "text/plain",
	}
	for ext, want := range cases {
		if got := mimeForExt(ext); got != want {
			t.Errorf("mimeForExt(%q) = %q, want %q", ext, got, want)
		}
	}
}
