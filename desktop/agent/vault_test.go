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
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	os.MkdirAll(filepath.Join(tmpDir, ".yaver"), 0700)

	vs, err := NewVaultStore("test-passphrase")
	if err != nil {
		t.Fatalf("NewVaultStore: %v", err)
	}

	if entries := vs.List("*"); len(entries) != 0 {
		t.Fatalf("expected empty vault, got %d entries", len(entries))
	}

	// Add global entry
	if err := vs.Set(VaultEntry{
		Name:     "openai-key",
		Category: "api-key",
		Value:    "sk-test-12345",
		Notes:    "test key",
	}); err != nil {
		t.Fatalf("Set: %v", err)
	}

	// Add project-scoped entry with same name
	if err := vs.Set(VaultEntry{
		Name:     "openai-key",
		Project:  "yaver",
		Category: "api-key",
		Value:    "sk-yaver-only",
	}); err != nil {
		t.Fatalf("Set (project): %v", err)
	}

	// "*" returns both
	if entries := vs.List("*"); len(entries) != 2 {
		t.Fatalf("expected 2 entries with '*', got %d", len(entries))
	}
	// "" returns only global
	globals := vs.List("")
	if len(globals) != 1 || globals[0].Project != "" {
		t.Fatalf("expected 1 global entry, got %v", globals)
	}
	// "yaver" returns only project
	scoped := vs.List("yaver")
	if len(scoped) != 1 || scoped[0].Project != "yaver" {
		t.Fatalf("expected 1 scoped entry, got %v", scoped)
	}

	// Get by (project, name) — isolation
	global, _ := vs.Get("", "openai-key")
	if global.Value != "sk-test-12345" {
		t.Fatalf("global: expected 'sk-test-12345', got %q", global.Value)
	}
	scopedEntry, _ := vs.Get("yaver", "openai-key")
	if scopedEntry.Value != "sk-yaver-only" {
		t.Fatalf("scoped: expected 'sk-yaver-only', got %q", scopedEntry.Value)
	}

	// Delete one — other must survive
	if err := vs.Delete("yaver", "openai-key"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := vs.Get("yaver", "openai-key"); err == nil {
		t.Fatal("expected error getting deleted project entry")
	}
	if entry, err := vs.Get("", "openai-key"); err != nil || entry.Value != "sk-test-12345" {
		t.Fatalf("global entry should survive scoped delete, got %v / %v", entry, err)
	}

	// Delete non-existent errors
	if err := vs.Delete("nope", "nope"); err == nil {
		t.Fatal("expected error deleting non-existent entry")
	}
}

func TestVaultProjectValidation(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	os.MkdirAll(filepath.Join(tmpDir, ".yaver"), 0700)

	vs, _ := NewVaultStore("p")

	if err := vs.Set(VaultEntry{Name: "bad name", Value: "v"}); err == nil {
		t.Fatal("expected error on space in name")
	}
	if err := vs.Set(VaultEntry{Name: "ok", Project: "bad/project", Value: "v"}); err == nil {
		t.Fatal("expected error on slash in project")
	}
	if err := vs.Set(VaultEntry{Name: "ok", Project: "ok-project", Value: ""}); err == nil {
		t.Fatal("expected error on empty value")
	}
}

func TestVaultPersistence(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	os.MkdirAll(filepath.Join(tmpDir, ".yaver"), 0700)

	vs1, err := NewVaultStoreWithDevice("my-passphrase", "device-alpha")
	if err != nil {
		t.Fatalf("NewVaultStore: %v", err)
	}
	vs1.Set(VaultEntry{Name: "key1", Category: "api-key", Value: "val1"})
	vs1.Set(VaultEntry{Name: "key2", Project: "yaver", Category: "ssh-key", Value: "val2"})

	// Reopen with same passphrase
	vs2, err := NewVaultStoreWithDevice("my-passphrase", "device-alpha")
	if err != nil {
		t.Fatalf("Reopen vault: %v", err)
	}
	if entries := vs2.List("*"); len(entries) != 2 {
		t.Fatalf("expected 2 entries after reopen, got %d", len(entries))
	}
	entry, _ := vs2.Get("", "key1")
	if entry.Value != "val1" || entry.DeviceID != "device-alpha" {
		t.Fatalf("entry after reload wrong: %+v", entry)
	}
	scoped, _ := vs2.Get("yaver", "key2")
	if scoped.Project != "yaver" || scoped.Value != "val2" {
		t.Fatalf("scoped entry after reload wrong: %+v", scoped)
	}
}

// TestVaultV1FormatLoad verifies loadEntries accepts the legacy v1 JSON
// object format (map keyed by name). Full end-to-end on-disk migration
// is exercised by manual upgrade testing; this unit test proves the
// parser branch.
func TestVaultV1FormatLoad(t *testing.T) {
	vs := &VaultStore{entries: map[string]VaultEntry{}}
	v1 := `{"legacy": {"name":"legacy","category":"api-key","value":"v1-secret","created_at":1,"updated_at":2}}`
	if err := vs.loadEntries([]byte(v1)); err != nil {
		t.Fatalf("loadEntries v1: %v", err)
	}
	got, ok := vs.entries[vaultKey("", "legacy")]
	if !ok {
		t.Fatalf("legacy entry missing after v1 load, have: %+v", vs.entries)
	}
	if got.Value != "v1-secret" {
		t.Fatalf("expected 'v1-secret', got %q", got.Value)
	}
	if got.Project != "" {
		t.Fatalf("v1 entry must become global (project=\"\"), got %q", got.Project)
	}
}

func TestVaultV2FormatLoad(t *testing.T) {
	vs := &VaultStore{entries: map[string]VaultEntry{}}
	v2 := `[{"name":"a","project":"yaver","value":"x","created_at":1,"updated_at":2},
	         {"name":"b","value":"y","created_at":1,"updated_at":2}]`
	if err := vs.loadEntries([]byte(v2)); err != nil {
		t.Fatalf("loadEntries v2: %v", err)
	}
	if _, ok := vs.entries[vaultKey("yaver", "a")]; !ok {
		t.Fatalf("scoped entry missing after v2 load")
	}
	if _, ok := vs.entries[vaultKey("", "b")]; !ok {
		t.Fatalf("global entry missing after v2 load")
	}
}

func TestVaultUpsertSyncMerge(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	os.MkdirAll(filepath.Join(tmpDir, ".yaver"), 0700)

	vs, _ := NewVaultStoreWithDevice("p", "device-b")

	// Seed a local value.
	_ = vs.Set(VaultEntry{Name: "K", Value: "local"})
	local, _ := vs.Get("", "K")

	// Older revision — must be rejected.
	older := VaultEntry{Name: "K", Value: "stale", UpdatedAt: local.UpdatedAt - 1000, DeviceID: "device-a"}
	accepted, err := vs.Upsert(older)
	if err != nil {
		t.Fatalf("Upsert older: %v", err)
	}
	if accepted {
		t.Fatal("older revision must not be accepted")
	}
	cur, _ := vs.Get("", "K")
	if cur.Value != "local" {
		t.Fatalf("local must not be clobbered, got %q", cur.Value)
	}

	// Newer revision from another device — accepted.
	newer := VaultEntry{Name: "K", Value: "remote", UpdatedAt: local.UpdatedAt + 1000, DeviceID: "device-a"}
	accepted, err = vs.Upsert(newer)
	if err != nil {
		t.Fatalf("Upsert newer: %v", err)
	}
	if !accepted {
		t.Fatal("newer revision must be accepted")
	}
	cur, _ = vs.Get("", "K")
	if cur.Value != "remote" || cur.DeviceID != "device-a" {
		t.Fatalf("newer revision not applied: %+v", cur)
	}

	// Tombstone from another device — also accepted.
	tomb := VaultEntry{Name: "K", UpdatedAt: cur.UpdatedAt + 1000, Deleted: true, DeviceID: "device-a"}
	accepted, err = vs.Upsert(tomb)
	if err != nil {
		t.Fatalf("Upsert tomb: %v", err)
	}
	if !accepted {
		t.Fatal("tombstone must be accepted")
	}
	if _, err := vs.Get("", "K"); err == nil {
		t.Fatal("Get must fail after tombstone")
	}
}

func TestVaultEnvExport(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	os.MkdirAll(filepath.Join(tmpDir, ".yaver"), 0700)
	vs, _ := NewVaultStore("p")

	_ = vs.Set(VaultEntry{Name: "SHARED", Value: "global"})
	_ = vs.Set(VaultEntry{Name: "APP_ONLY", Project: "yaver", Value: "scoped"})
	_ = vs.Set(VaultEntry{Name: "SHARED", Project: "yaver", Value: "override"})

	// With globals: project override wins for SHARED, APP_ONLY present.
	env := vs.EnvExport("yaver", true)
	if !strings.Contains(env, "export APP_ONLY='scoped'\n") {
		t.Fatalf("APP_ONLY missing from env export:\n%s", env)
	}
	if !strings.Contains(env, "export SHARED='override'\n") {
		t.Fatalf("SHARED override missing from env export:\n%s", env)
	}

	// Without globals: only project entries.
	envNoGlobal := vs.EnvExport("yaver", false)
	if strings.Contains(envNoGlobal, "global") {
		t.Fatalf("global value should not be exported with noGlobals, got:\n%s", envNoGlobal)
	}

	// Value with a single quote must be escaped safely.
	_ = vs.Set(VaultEntry{Name: "TRICKY", Project: "yaver", Value: "a'b"})
	env2 := vs.EnvExport("yaver", false)
	if !strings.Contains(env2, `export TRICKY='a'"'"'b'`) {
		t.Fatalf("single quote not escaped:\n%s", env2)
	}
}

// TestVaultHomePathPortability verifies that absolute paths under
// the writer's HOME are stored as `~/...` and re-expanded against
// the runtime HOME on read — so a vault entry set on machine A
// resolves correctly on machine B with a different home directory.
func TestVaultHomePathPortability(t *testing.T) {
	writerHome := t.TempDir()
	t.Setenv("HOME", writerHome)
	os.MkdirAll(filepath.Join(writerHome, ".yaver"), 0700)
	vs, err := NewVaultStore("p")
	if err != nil {
		t.Fatalf("NewVaultStore: %v", err)
	}

	cases := []struct {
		name    string
		input   string
		stored  string // expected on-disk representation
		readout string // expected Get/EnvExport output (matches input under writerHome)
	}{
		{
			name:    "absolute path under home is normalized to ~/",
			input:   filepath.Join(writerHome, ".appstoreconnect", "AuthKey.p8"),
			stored:  "~/.appstoreconnect/AuthKey.p8",
			readout: filepath.Join(writerHome, ".appstoreconnect", "AuthKey.p8"),
		},
		{
			name:    "explicit ~/ stays portable",
			input:   "~/Workspace/keys/sa.json",
			stored:  "~/Workspace/keys/sa.json",
			readout: filepath.Join(writerHome, "Workspace", "keys", "sa.json"),
		},
		{
			name:    "$HOME/ stays portable, expanded on read",
			input:   "$HOME/secrets/key",
			stored:  "$HOME/secrets/key",
			readout: filepath.Join(writerHome, "secrets", "key"),
		},
		{
			name:    "path outside home is NOT rewritten",
			input:   "/opt/shared/cert.pem",
			stored:  "/opt/shared/cert.pem",
			readout: "/opt/shared/cert.pem",
		},
		{
			name:    "non-path values pass through",
			input:   "secret-token-abc123",
			stored:  "secret-token-abc123",
			readout: "secret-token-abc123",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := vs.Set(VaultEntry{Name: "K", Project: "p", Value: tc.input}); err != nil {
				t.Fatalf("Set: %v", err)
			}
			// Stored form: read raw entry under the lock-bypass map so we
			// see the on-disk representation, not the expanded one.
			vs.mu.RLock()
			raw := vs.entries[vaultKey("p", "K")].Value
			vs.mu.RUnlock()
			if raw != tc.stored {
				t.Errorf("stored value = %q, want %q", raw, tc.stored)
			}
			// Get returns expanded value.
			got, err := vs.Get("p", "K")
			if err != nil {
				t.Fatalf("Get: %v", err)
			}
			if got.Value != tc.readout {
				t.Errorf("Get value = %q, want %q", got.Value, tc.readout)
			}
		})
	}

	// Cross-machine simulation: set a path with one HOME, read with
	// a different HOME — same vault entry should resolve to the
	// reader's home, not the writer's.
	_ = vs.Set(VaultEntry{Name: "PORTABLE_PATH", Project: "p", Value: filepath.Join(writerHome, "foo", "bar")})

	readerHome := t.TempDir()
	t.Setenv("HOME", readerHome)

	got, err := vs.Get("p", "PORTABLE_PATH")
	if err != nil {
		t.Fatalf("Get on reader machine: %v", err)
	}
	want := filepath.Join(readerHome, "foo", "bar")
	if got.Value != want {
		t.Errorf("portable read = %q, want %q (writer home leaked into reader)", got.Value, want)
	}

	// EnvExport emits the reader-expanded form too.
	env := vs.EnvExport("p", false)
	if !strings.Contains(env, "export PORTABLE_PATH='"+want+"'") {
		t.Errorf("EnvExport missing reader-expanded path; got:\n%s", env)
	}
}

func TestVaultDigestAndNewerThan(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	os.MkdirAll(filepath.Join(tmpDir, ".yaver"), 0700)
	vs, _ := NewVaultStore("p")

	_ = vs.Set(VaultEntry{Name: "A", Value: "1"})
	_ = vs.Set(VaultEntry{Name: "B", Project: "yaver", Value: "2"})

	dig := vs.Digest()
	if len(dig) != 2 {
		t.Fatalf("expected 2 digest entries, got %d", len(dig))
	}

	// Peer has nothing → we owe everything.
	owed := vs.EntriesNewerThan(nil)
	if len(owed) != 2 {
		t.Fatalf("expected 2 owed, got %d", len(owed))
	}

	// Peer has same version → we owe nothing.
	peer := make([]VaultDigestEntry, 0, len(dig))
	for _, d := range dig {
		peer = append(peer, d)
	}
	if got := vs.EntriesNewerThan(peer); len(got) != 0 {
		t.Fatalf("expected 0 owed when peer is current, got %d", len(got))
	}

	// Peer has older version of A → we owe only A.
	peer[0].UpdatedAt -= 1000
	owed = vs.EntriesNewerThan(peer)
	if len(owed) != 1 {
		t.Fatalf("expected 1 owed, got %d", len(owed))
	}
}

func TestVaultWrongPassphrase(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	os.MkdirAll(filepath.Join(tmpDir, ".yaver"), 0700)

	vs, err := NewVaultStore("correct-passphrase")
	if err != nil {
		t.Fatalf("NewVaultStore: %v", err)
	}
	vs.Set(VaultEntry{Name: "secret", Category: "api-key", Value: "hidden"})

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
	vs.Set(VaultEntry{Name: "b", Project: "yaver", Category: "ssh-key", Value: "2"})

	data, err := vs.ExportPlaintext()
	if err != nil {
		t.Fatalf("Export: %v", err)
	}

	var exported []VaultEntry
	json.Unmarshal(data, &exported)
	if len(exported) != 2 {
		t.Fatalf("expected 2 exported entries, got %d", len(exported))
	}

	os.Remove(filepath.Join(tmpDir, ".yaver", "vault.enc"))
	vs2, _ := NewVaultStore("pass2")
	count, err := vs2.ImportPlaintext(data)
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if count != 2 {
		t.Fatalf("expected 2 imported, got %d", count)
	}
	entry, _ := vs2.Get("", "a")
	if entry.Value != "1" {
		t.Fatalf("expected value '1', got %q", entry.Value)
	}
	scoped, _ := vs2.Get("yaver", "b")
	if scoped.Value != "2" {
		t.Fatalf("expected scoped value '2', got %q", scoped.Value)
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

	vs, err := NewVaultStoreWithDevice("http-test", "dev-http")
	if err != nil {
		t.Fatalf("NewVaultStore: %v", err)
	}

	srv := &HTTPServer{
		token:       "test-token",
		ownerUserID: "user123",
		deviceID:    "dev-http",
		vaultStore:  vs,
	}

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
		case strings.HasPrefix(path, "/vault/env"):
			srv.handleVaultEnv(w, req)
		case strings.HasPrefix(path, "/vault/digest"):
			srv.handleVaultDigest(w, req)
		case strings.HasPrefix(path, "/vault/sync"):
			srv.handleVaultSync(w, req)
		case strings.HasPrefix(path, "/vault/push"):
			srv.handleVaultPush(w, req)
		}
		return w
	}

	// List empty vault
	w := doReq("GET", "/vault/list?project=*", "")
	if w.Code != 200 {
		t.Fatalf("list empty: expected 200, got %d", w.Code)
	}
	var list struct {
		Entries  []VaultEntrySummary `json:"entries"`
		Projects []string            `json:"projects"`
	}
	json.Unmarshal(w.Body.Bytes(), &list)
	if len(list.Entries) != 0 {
		t.Fatalf("expected empty list, got %d", len(list.Entries))
	}

	// Set global entry
	w = doReq("POST", "/vault/set", `{"name":"test-key","category":"api-key","value":"secret123"}`)
	if w.Code != 200 {
		t.Fatalf("set: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	// Set project entry
	w = doReq("POST", "/vault/set", `{"name":"test-key","project":"yaver","category":"api-key","value":"yaver-secret"}`)
	if w.Code != 200 {
		t.Fatalf("set project: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	// List * should show 2, and projects array should contain "yaver"
	w = doReq("GET", "/vault/list?project=*", "")
	json.Unmarshal(w.Body.Bytes(), &list)
	if len(list.Entries) != 2 {
		t.Fatalf("expected 2 entries with project=*, got %d", len(list.Entries))
	}
	if len(list.Projects) != 1 || list.Projects[0] != "yaver" {
		t.Fatalf("expected projects=['yaver'], got %v", list.Projects)
	}
	// Verify list JSON doesn't contain values
	if strings.Contains(w.Body.String(), "secret123") || strings.Contains(w.Body.String(), "yaver-secret") {
		t.Fatal("list response should not contain vault values")
	}

	// Get global entry
	w = doReq("GET", "/vault/get?name=test-key", "")
	if w.Code != 200 {
		t.Fatalf("get global: expected 200, got %d", w.Code)
	}
	var entry VaultEntry
	json.Unmarshal(w.Body.Bytes(), &entry)
	if entry.Value != "secret123" {
		t.Fatalf("expected 'secret123', got %q", entry.Value)
	}

	// Get project entry
	w = doReq("GET", "/vault/get?name=test-key&project=yaver", "")
	if w.Code != 200 {
		t.Fatalf("get scoped: expected 200, got %d", w.Code)
	}
	json.Unmarshal(w.Body.Bytes(), &entry)
	if entry.Value != "yaver-secret" {
		t.Fatalf("expected 'yaver-secret', got %q", entry.Value)
	}

	// /vault/env — project required
	w = doReq("GET", "/vault/env", "")
	if w.Code != 400 {
		t.Fatalf("env without project: expected 400, got %d", w.Code)
	}
	w = doReq("GET", "/vault/env?project=yaver", "")
	if w.Code != 200 {
		t.Fatalf("env: expected 200, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "export test-key='yaver-secret'") {
		t.Fatalf("env body missing project export:\n%s", w.Body.String())
	}

	// Digest
	w = doReq("GET", "/vault/digest", "")
	if w.Code != 200 {
		t.Fatalf("digest: expected 200, got %d", w.Code)
	}
	var dig struct {
		Entries []VaultDigestEntry `json:"entries"`
	}
	json.Unmarshal(w.Body.Bytes(), &dig)
	if len(dig.Entries) != 2 {
		t.Fatalf("expected 2 digest entries, got %d", len(dig.Entries))
	}

	// Sync: empty digest → peer gets everything (with values).
	w = doReq("POST", "/vault/sync", `{"digest":[]}`)
	if w.Code != 200 {
		t.Fatalf("sync: expected 200, got %d", w.Code)
	}
	var syncResp struct {
		Entries []VaultEntry `json:"entries"`
	}
	json.Unmarshal(w.Body.Bytes(), &syncResp)
	if len(syncResp.Entries) != 2 {
		t.Fatalf("expected 2 entries in sync response, got %d", len(syncResp.Entries))
	}

	// Push: simulate an inbound fresh rev.
	w = doReq("POST", "/vault/push", `{"entries":[{"name":"inbound","value":"v","updated_at":`+
		"9999999999999"+`,"device_id":"other"}]}`)
	if w.Code != 200 {
		t.Fatalf("push: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var pushResp struct {
		Accepted int `json:"accepted"`
	}
	json.Unmarshal(w.Body.Bytes(), &pushResp)
	if pushResp.Accepted != 1 {
		t.Fatalf("expected 1 accepted, got %d", pushResp.Accepted)
	}

	// Delete (tombstone)
	w = doReq("DELETE", "/vault/delete?name=test-key&project=yaver", "")
	if w.Code != 200 {
		t.Fatalf("delete: expected 200, got %d", w.Code)
	}
	// The global entry must survive the scoped delete.
	w = doReq("GET", "/vault/get?name=test-key", "")
	if w.Code != 200 {
		t.Fatalf("global should survive scoped delete, got %d", w.Code)
	}
}

func TestVaultHTTPMethodNotAllowed(t *testing.T) {
	srv := &HTTPServer{vaultStore: &VaultStore{unlocked: true, entries: map[string]VaultEntry{}}}

	req := httptest.NewRequest("POST", "/vault/list", nil)
	w := httptest.NewRecorder()
	srv.handleVaultList(w, req)
	if w.Code != 405 {
		t.Fatalf("expected 405 for POST /vault/list, got %d", w.Code)
	}

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
