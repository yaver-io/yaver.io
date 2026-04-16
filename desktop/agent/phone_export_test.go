package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Roundtrip: Create → Export → Delete → Import → verify schema + seed.
// This is the happy path for "mobile app → any target agent".
func TestExportImportRoundtrip(t *testing.T) {
	setupPhoneTestHome(t)

	orig, err := CreatePhoneProject(PhoneCreateSpec{
		Name:     "Roundtrip Todos",
		Template: "todos",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	origSlug := orig.Slug

	bundle, err := ExportPhoneProject(origSlug)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if len(bundle) == 0 {
		t.Fatal("empty bundle")
	}

	if err := DeletePhoneProject(origSlug); err != nil {
		t.Fatalf("delete: %v", err)
	}

	imported, err := ImportPhoneProject(bundle, PhoneImportOptions{})
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if imported.Slug != origSlug {
		t.Errorf("slug = %q, want %q", imported.Slug, origSlug)
	}
	// Schema must round-trip.
	if imported.Schema == nil || len(imported.Schema.Tables) == 0 {
		t.Fatalf("schema missing after import")
	}
	var hasTodos bool
	for _, tbl := range imported.Schema.Tables {
		if tbl.Name == "todos" {
			hasTodos = true
			break
		}
	}
	if !hasTodos {
		t.Errorf("imported schema missing 'todos' table")
	}
	// Seed rows must land in SQLite.
	if imported.Stats == nil || imported.Stats.PerTable["todos"] < 1 {
		t.Errorf("imported seed missing: stats=%+v", imported.Stats)
	}
}

func TestImportConflictReject(t *testing.T) {
	setupPhoneTestHome(t)

	p, err := CreatePhoneProject(PhoneCreateSpec{Name: "Conflict", Template: "blank"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	bundle, err := ExportPhoneProject(p.Slug)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	// Default OnConflict is reject.
	_, err = ImportPhoneProject(bundle, PhoneImportOptions{})
	if !errors.Is(err, ErrPhoneProjectExists) {
		t.Errorf("expected ErrPhoneProjectExists, got %v", err)
	}
}

func TestImportConflictRename(t *testing.T) {
	setupPhoneTestHome(t)
	p, err := CreatePhoneProject(PhoneCreateSpec{Name: "Renamer", Template: "crud"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	bundle, err := ExportPhoneProject(p.Slug)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	imp, err := ImportPhoneProject(bundle, PhoneImportOptions{OnConflict: "rename"})
	if err != nil {
		t.Fatalf("rename-import: %v", err)
	}
	if imp.Slug == p.Slug {
		t.Errorf("expected renamed slug, got same %q", imp.Slug)
	}
	if !strings.HasPrefix(imp.Slug, p.Slug+"-") {
		t.Errorf("expected prefix %q- in renamed slug, got %q", p.Slug, imp.Slug)
	}
	// Both should exist.
	all, _ := ListPhoneProjects()
	if len(all) < 2 {
		t.Errorf("expected >=2 projects after rename-import, got %d", len(all))
	}
}

func TestImportConflictOverwrite(t *testing.T) {
	setupPhoneTestHome(t)
	p, err := CreatePhoneProject(PhoneCreateSpec{Name: "Overwriter", Template: "todos"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	bundle, err := ExportPhoneProject(p.Slug)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	// Mutate the project so overwrite is observable.
	adapter, err := PhoneAdapter(p.Slug)
	if err != nil {
		t.Fatalf("adapter: %v", err)
	}
	if _, err := adapter.Insert("todos", map[string]interface{}{
		"id": "marker", "title": "present before overwrite", "done": false,
	}); err != nil {
		t.Fatalf("insert marker: %v", err)
	}

	imp, err := ImportPhoneProject(bundle, PhoneImportOptions{OnConflict: "overwrite"})
	if err != nil {
		t.Fatalf("overwrite-import: %v", err)
	}
	if imp.Slug != p.Slug {
		t.Errorf("slug changed after overwrite: got %q, want %q", imp.Slug, p.Slug)
	}
	// Marker row should be gone — overwrite replaced the dir.
	adapter2, _ := PhoneAdapter(p.Slug)
	res, err := adapter2.Query(`SELECT count(*) FROM todos WHERE id='marker'`, nil)
	if err != nil {
		t.Fatalf("query marker: %v", err)
	}
	b, _ := json.Marshal(res)
	if strings.Contains(string(b), `"count(*)":1`) {
		t.Errorf("overwrite did not clear project: still has marker row; result=%s", string(b))
	}
}

func TestImportSkipSeed(t *testing.T) {
	setupPhoneTestHome(t)
	p, err := CreatePhoneProject(PhoneCreateSpec{Name: "Seedless", Template: "todos"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	bundle, err := ExportPhoneProject(p.Slug)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	_ = DeletePhoneProject(p.Slug)
	imp, err := ImportPhoneProject(bundle, PhoneImportOptions{SkipSeed: true})
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if imp.Stats != nil && imp.Stats.PerTable["todos"] != 0 {
		t.Errorf("expected 0 todos with SkipSeed=true, got %d", imp.Stats.PerTable["todos"])
	}
}

func TestImportRejectsTraversalPath(t *testing.T) {
	setupPhoneTestHome(t)
	// Hand-build a malicious tgz with a ".." entry.
	bundle := buildMaliciousTgz(t, []string{"../../evil"})
	_, err := ImportPhoneProject(bundle, PhoneImportOptions{})
	if err == nil {
		t.Fatal("expected error on traversal path")
	}
	if !strings.Contains(err.Error(), "unsafe") {
		t.Errorf("expected unsafe tar error, got %v", err)
	}
}

func TestImportRejectsEmpty(t *testing.T) {
	setupPhoneTestHome(t)
	if _, err := ImportPhoneProject(nil, PhoneImportOptions{}); err == nil {
		t.Error("expected error on empty bundle")
	}
}

// HTTP handler test — exercises the full path used by the mobile app when it
// POSTs to /phone/projects/receive on a remote agent (dev hw or Hetzner).
func TestHandlePhoneReceive_Multipart(t *testing.T) {
	setupPhoneTestHome(t)

	// Build a bundle we'll post back in.
	p, err := CreatePhoneProject(PhoneCreateSpec{Name: "Recv", Template: "todos"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	bundle, err := ExportPhoneProject(p.Slug)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	// Delete so the import lands cleanly on the "target" side.
	_ = DeletePhoneProject(p.Slug)

	// Craft multipart body.
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, _ := mw.CreateFormFile("bundle", "project.tgz")
	_, _ = fw.Write(bundle)
	_ = mw.WriteField("onConflict", "rename")
	_ = mw.Close()

	req := httptest.NewRequest(http.MethodPost, "/phone/projects/receive", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())

	srv := &HTTPServer{}
	w := httptest.NewRecorder()
	srv.handlePhoneReceive(w, req)

	if w.Code != http.StatusOK {
		resp, _ := io.ReadAll(w.Body)
		t.Fatalf("status %d: %s", w.Code, string(resp))
	}
	var out struct {
		Slug      string `json:"slug"`
		BrowseUrl string `json:"browseUrl"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v (body=%s)", err, w.Body.String())
	}
	if out.Slug == "" {
		t.Error("empty slug in response")
	}
	if !strings.HasPrefix(out.BrowseUrl, "/phone/projects/browse?slug=") {
		t.Errorf("unexpected browseUrl: %q", out.BrowseUrl)
	}
	// Confirm the project is actually materialised on the target side.
	projs, _ := ListPhoneProjects()
	if len(projs) != 1 {
		t.Errorf("expected 1 project on target, got %d", len(projs))
	}
}

func TestHandlePhoneReceive_RawBody(t *testing.T) {
	setupPhoneTestHome(t)
	p, err := CreatePhoneProject(PhoneCreateSpec{Name: "Raw", Template: "blank"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	bundle, _ := ExportPhoneProject(p.Slug)
	_ = DeletePhoneProject(p.Slug)

	req := httptest.NewRequest(http.MethodPost, "/phone/projects/receive?slug=raw-target&onConflict=reject",
		bytes.NewReader(bundle))
	req.Header.Set("Content-Type", "application/gzip")
	srv := &HTTPServer{}
	w := httptest.NewRecorder()
	srv.handlePhoneReceive(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	var out struct{ Slug string }
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	if out.Slug != "raw-target" {
		t.Errorf("slug = %q, want raw-target", out.Slug)
	}
}

func TestHandlePhoneReceive_MethodNotAllowed(t *testing.T) {
	setupPhoneTestHome(t)
	req := httptest.NewRequest(http.MethodGet, "/phone/projects/receive", nil)
	srv := &HTTPServer{}
	w := httptest.NewRecorder()
	srv.handlePhoneReceive(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", w.Code)
	}
}

func TestHandlePhoneReceive_EmptyBundleRejected(t *testing.T) {
	setupPhoneTestHome(t)
	req := httptest.NewRequest(http.MethodPost, "/phone/projects/receive", bytes.NewReader(nil))
	req.Header.Set("Content-Type", "application/gzip")
	srv := &HTTPServer{}
	w := httptest.NewRecorder()
	srv.handlePhoneReceive(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

// buildMaliciousTgz creates a minimal gzipped tar containing entries with
// path-traversal names so ImportPhoneProject's safety check is exercised.
func buildMaliciousTgz(t *testing.T, names []string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, n := range names {
		if err := tw.WriteHeader(&tar.Header{Name: n, Size: 1, Mode: 0o644}); err != nil {
			t.Fatalf("header: %v", err)
		}
		if _, err := tw.Write([]byte("x")); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("close tar: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("close gz: %v", err)
	}
	return buf.Bytes()
}

// Regression: before 2026-04-17 an unknown slug leaked through PhoneAdapter
// and surfaced as a confusing SQLite CANTOPEN 500 (reported as "unable to
// open database file: out of memory (14)"). PhoneAdapter must now return
// ErrPhoneProjectNotFound so HTTP handlers can map it to 404.
func TestPhoneAdapter_MissingProjectReturnsNotFound(t *testing.T) {
	setupPhoneTestHome(t)
	_, err := PhoneAdapter("never-existed")
	if err == nil {
		t.Fatalf("expected error for missing project")
	}
	if !errors.Is(err, ErrPhoneProjectNotFound) {
		t.Errorf("expected ErrPhoneProjectNotFound, got %v", err)
	}
}
