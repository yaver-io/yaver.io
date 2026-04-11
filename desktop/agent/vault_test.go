package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestVaultCRUD(t *testing.T) {
	// Use a temp dir for the vault
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	// Create .yaver dir
	os.MkdirAll(filepath.Join(tmpDir, ".yaver"), 0700)

	// Create vault
	vs, err := NewVaultStore("test-passphrase")
	if err != nil {
		t.Fatalf("NewVaultStore: %v", err)
	}

	// Empty vault
	if entries := vs.List(); len(entries) != 0 {
		t.Fatalf("expected empty vault, got %d entries", len(entries))
	}

	// Add entry
	if err := vs.Set(VaultEntry{
		Name:     "openai-key",
		Category: "api-key",
		Value:    "sk-test-12345",
		Notes:    "test key",
	}); err != nil {
		t.Fatalf("Set: %v", err)
	}

	// List should return 1 entry without value
	entries := vs.List()
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].Name != "openai-key" {
		t.Fatalf("expected name 'openai-key', got %q", entries[0].Name)
	}

	// Get should return full entry with value
	entry, err := vs.Get("openai-key")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if entry.Value != "sk-test-12345" {
		t.Fatalf("expected value 'sk-test-12345', got %q", entry.Value)
	}

	// Update entry
	if err := vs.Set(VaultEntry{
		Name:     "openai-key",
		Category: "api-key",
		Value:    "sk-updated-67890",
	}); err != nil {
		t.Fatalf("Set update: %v", err)
	}
	entry, _ = vs.Get("openai-key")
	if entry.Value != "sk-updated-67890" {
		t.Fatalf("expected updated value, got %q", entry.Value)
	}
	if entry.CreatedAt > entry.UpdatedAt {
		t.Fatal("createdAt should be <= updatedAt after update")
	}

	// Delete
	if err := vs.Delete("openai-key"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if entries := vs.List(); len(entries) != 0 {
		t.Fatalf("expected empty vault after delete, got %d entries", len(entries))
	}

	// Delete non-existent should error
	if err := vs.Delete("nonexistent"); err == nil {
		t.Fatal("expected error deleting non-existent entry")
	}

	// Get non-existent should error
	if _, err := vs.Get("nonexistent"); err == nil {
		t.Fatal("expected error getting non-existent entry")
	}
}

func TestVaultPersistence(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	os.MkdirAll(filepath.Join(tmpDir, ".yaver"), 0700)

	// Create and populate vault
	vs1, err := NewVaultStore("my-passphrase")
	if err != nil {
		t.Fatalf("NewVaultStore: %v", err)
	}
	vs1.Set(VaultEntry{Name: "key1", Category: "api-key", Value: "val1"})
	vs1.Set(VaultEntry{Name: "key2", Category: "ssh-key", Value: "val2"})

	// Reopen with same passphrase
	vs2, err := NewVaultStore("my-passphrase")
	if err != nil {
		t.Fatalf("Reopen vault: %v", err)
	}
	if entries := vs2.List(); len(entries) != 2 {
		t.Fatalf("expected 2 entries after reopen, got %d", len(entries))
	}
	entry, _ := vs2.Get("key1")
	if entry.Value != "val1" {
		t.Fatalf("expected 'val1', got %q", entry.Value)
	}
}

func TestVaultWrongPassphrase(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	os.MkdirAll(filepath.Join(tmpDir, ".yaver"), 0700)

	// Create vault with one passphrase
	vs, err := NewVaultStore("correct-passphrase")
	if err != nil {
		t.Fatalf("NewVaultStore: %v", err)
	}
	vs.Set(VaultEntry{Name: "secret", Category: "api-key", Value: "hidden"})

	// Try to open with wrong passphrase
	_, err = NewVaultStore("wrong-passphrase")
	if err == nil {
		t.Fatal("expected error with wrong passphrase")
	}
	if !strings.Contains(err.Error(), "wrong passphrase") {
		t.Fatalf("expected 'wrong passphrase' error, got: %v", err)
	}
}

func TestVaultExportImport(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	os.MkdirAll(filepath.Join(tmpDir, ".yaver"), 0700)

	vs, _ := NewVaultStore("pass")
	vs.Set(VaultEntry{Name: "a", Category: "api-key", Value: "1"})
	vs.Set(VaultEntry{Name: "b", Category: "ssh-key", Value: "2"})

	data, err := vs.ExportPlaintext()
	if err != nil {
		t.Fatalf("Export: %v", err)
	}

	// Check exported JSON contains values
	var exported []VaultEntry
	json.Unmarshal(data, &exported)
	if len(exported) != 2 {
		t.Fatalf("expected 2 exported entries, got %d", len(exported))
	}

	// Create fresh vault and import
	os.Remove(filepath.Join(tmpDir, ".yaver", "vault.enc"))
	vs2, _ := NewVaultStore("pass2")
	count, err := vs2.ImportPlaintext(data)
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if count != 2 {
		t.Fatalf("expected 2 imported, got %d", count)
	}
	entry, _ := vs2.Get("a")
	if entry.Value != "1" {
		t.Fatalf("expected value '1', got %q", entry.Value)
	}
}

func TestVaultDerivePassphraseFromToken(t *testing.T) {
	p1 := DerivePassphraseFromToken("token-a")
	p2 := DerivePassphraseFromToken("token-b")
	p3 := DerivePassphraseFromToken("token-a")

	if p1 == p2 {
		t.Fatal("different tokens should produce different passphrases")
	}
	if p1 != p3 {
		t.Fatal("same token should produce same passphrase")
	}
}

func TestVaultHTTPEndpoints(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	os.MkdirAll(filepath.Join(tmpDir, ".yaver"), 0700)

	vs, err := NewVaultStore("http-test")
	if err != nil {
		t.Fatalf("NewVaultStore: %v", err)
	}

	srv := &HTTPServer{
		token:       "test-token",
		ownerUserID: "user123",
		vaultStore:  vs,
	}

	// Helper to make authenticated requests
	doReq := func(method, path, body string) *httptest.ResponseRecorder {
		var req *http.Request
		if body != "" {
			req = httptest.NewRequest(method, path, strings.NewReader(body))
		} else {
			req = httptest.NewRequest(method, path, nil)
		}
		req.Header.Set("Authorization", "Bearer test-token")
		w := httptest.NewRecorder()

		switch {
		case strings.HasPrefix(path, "/vault/list"):
			srv.handleVaultList(w, req)
		case strings.HasPrefix(path, "/vault/get"):
			srv.handleVaultGet(w, req)
		case strings.HasPrefix(path, "/vault/set"):
			srv.handleVaultSet(w, req)
		case strings.HasPrefix(path, "/vault/delete"):
			srv.handleVaultDelete(w, req)
		}
		return w
	}

	// List empty vault
	w := doReq("GET", "/vault/list", "")
	if w.Code != 200 {
		t.Fatalf("list empty: expected 200, got %d", w.Code)
	}
	var list []VaultEntrySummary
	json.Unmarshal(w.Body.Bytes(), &list)
	if len(list) != 0 {
		t.Fatalf("expected empty list, got %d", len(list))
	}

	// Set entry
	w = doReq("POST", "/vault/set", `{"name":"test-key","category":"api-key","value":"secret123"}`)
	if w.Code != 200 {
		t.Fatalf("set: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	// List should have 1 entry — no value exposed
	w = doReq("GET", "/vault/list", "")
	json.Unmarshal(w.Body.Bytes(), &list)
	if len(list) != 1 || list[0].Name != "test-key" {
		t.Fatalf("expected 1 entry named 'test-key', got %v", list)
	}
	// Verify list JSON doesn't contain the value
	if strings.Contains(w.Body.String(), "secret123") {
		t.Fatal("list response should not contain vault values")
	}

	// Get entry — should include value
	w = doReq("GET", "/vault/get?name=test-key", "")
	if w.Code != 200 {
		t.Fatalf("get: expected 200, got %d", w.Code)
	}
	var entry VaultEntry
	json.Unmarshal(w.Body.Bytes(), &entry)
	if entry.Value != "secret123" {
		t.Fatalf("expected value 'secret123', got %q", entry.Value)
	}

	// Get non-existent
	w = doReq("GET", "/vault/get?name=nope", "")
	if w.Code != 404 {
		t.Fatalf("get missing: expected 404, got %d", w.Code)
	}

	// Set without name
	w = doReq("POST", "/vault/set", `{"category":"api-key","value":"x"}`)
	if w.Code != 400 {
		t.Fatalf("set no name: expected 400, got %d", w.Code)
	}

	// Set without value
	w = doReq("POST", "/vault/set", `{"name":"x","category":"api-key"}`)
	if w.Code != 400 {
		t.Fatalf("set no value: expected 400, got %d", w.Code)
	}

	// Delete
	w = doReq("DELETE", "/vault/delete?name=test-key", "")
	if w.Code != 200 {
		t.Fatalf("delete: expected 200, got %d", w.Code)
	}

	// Verify deleted
	w = doReq("GET", "/vault/list", "")
	json.Unmarshal(w.Body.Bytes(), &list)
	if len(list) != 0 {
		t.Fatalf("expected empty after delete, got %d", len(list))
	}

	// Delete non-existent
	w = doReq("DELETE", "/vault/delete?name=nope", "")
	if w.Code != 404 {
		t.Fatalf("delete missing: expected 404, got %d", w.Code)
	}
}

func TestVaultHTTPMethodNotAllowed(t *testing.T) {
	srv := &HTTPServer{vaultStore: &VaultStore{unlocked: true, entries: map[string]VaultEntry{}}}

	// POST to list should fail
	req := httptest.NewRequest("POST", "/vault/list", nil)
	w := httptest.NewRecorder()
	srv.handleVaultList(w, req)
	if w.Code != 405 {
		t.Fatalf("expected 405 for POST /vault/list, got %d", w.Code)
	}

	// GET to set should fail
	req = httptest.NewRequest("GET", "/vault/set", nil)
	w = httptest.NewRecorder()
	srv.handleVaultSet(w, req)
	if w.Code != 405 {
		t.Fatalf("expected 405 for GET /vault/set, got %d", w.Code)
	}
}

func TestVaultHTTPNoStore(t *testing.T) {
	srv := &HTTPServer{vaultStore: nil}

	req := httptest.NewRequest("GET", "/vault/list", nil)
	w := httptest.NewRecorder()
	srv.handleVaultList(w, req)
	if w.Code != 503 {
		t.Fatalf("expected 503 when vault not available, got %d", w.Code)
	}
}
