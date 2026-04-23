package main

import (
	"bytes"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestHandleHostShareFSWriteSupportsBase64AndRootPath(t *testing.T) {
	root := t.TempDir()
	srv := &HTTPServer{}

	body := []byte(`{"rootPath":"` + root + `","path":"bin/data.bin","contentBase64":"` + base64.StdEncoding.EncodeToString([]byte{0x00, 0xff, 0x7f}) + `"}`)
	req := httptest.NewRequest(http.MethodPost, "/host-share/fs/write", bytes.NewReader(body))
	req.Header.Set("X-Yaver-HostShare", "true")
	rec := httptest.NewRecorder()

	srv.handleHostShareFSWrite(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	got, err := os.ReadFile(filepath.Join(root, "bin", "data.bin"))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	want := []byte{0x00, 0xff, 0x7f}
	if !bytes.Equal(got, want) {
		t.Fatalf("file bytes = %v, want %v", got, want)
	}
}

func TestHandleHostShareFSDeleteSupportsRootPath(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "nested", "file.txt")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(target, []byte("hello"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	srv := &HTTPServer{}
	body := []byte(`{"rootPath":"` + root + `","path":"nested/file.txt"}`)
	req := httptest.NewRequest(http.MethodPost, "/host-share/fs/delete", bytes.NewReader(body))
	req.Header.Set("X-Yaver-HostShare", "true")
	rec := httptest.NewRecorder()

	srv.handleHostShareFSDelete(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("expected file to be deleted, stat err=%v", err)
	}
}
