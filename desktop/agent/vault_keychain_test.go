package main

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// isolatedVaultDir gives tests their own ~/.yaver root + neuters the
// macOS keychain shell-out so unit runs don't pollute the user's real
// login keychain. Returns the absolute config dir so individual cases
// can sanity-check file existence.
func isolatedVaultDir(t *testing.T) string {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("YAVER_VAULT_SKIP_KEYCHAIN", "1")
	dir := filepath.Join(tmp, ".yaver")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatalf("mkdir ~/.yaver: %v", err)
	}
	return dir
}

// TestEnsureMasterKeyFirstRun: no key on disk ⇒ EnsureMasterKey generates
// one, writes the 32-byte file (mode 0600) + sidecar with the userID.
// A second call returns the SAME key (idempotent — never silently
// rotates).
func TestEnsureMasterKeyFirstRun(t *testing.T) {
	dir := isolatedVaultDir(t)

	k1, err := EnsureMasterKey("user-abc", "device-xyz")
	if err != nil {
		t.Fatalf("EnsureMasterKey first call: %v", err)
	}
	st, err := os.Stat(filepath.Join(dir, masterKeyFilename))
	if err != nil {
		t.Fatalf("master.key not created: %v", err)
	}
	if st.Size() != masterKeyLen {
		t.Fatalf("master.key wrong size: got %d, want %d", st.Size(), masterKeyLen)
	}
	if mode := st.Mode().Perm(); mode != 0600 {
		t.Fatalf("master.key wrong perms: got %o, want 0600", mode)
	}
	meta, err := readMasterKeyMeta(filepath.Join(dir, masterKeyMetaName))
	if err != nil {
		t.Fatalf("read meta: %v", err)
	}
	if meta == nil || meta.UserID != "user-abc" {
		t.Fatalf("meta missing or wrong user: %+v", meta)
	}

	k2, err := EnsureMasterKey("user-abc", "device-xyz")
	if err != nil {
		t.Fatalf("EnsureMasterKey second call: %v", err)
	}
	if !bytes.Equal(k1[:], k2[:]) {
		t.Fatalf("EnsureMasterKey rotated the key on second call — must be idempotent")
	}
}

// TestEnsureMasterKeyUserGuard: a different userID on the same machine
// is refused. Protects the on-disk vault from being read by a different
// Yaver user who happens to share the same OS account.
func TestEnsureMasterKeyUserGuard(t *testing.T) {
	isolatedVaultDir(t)
	if _, err := EnsureMasterKey("user-first", "device-1"); err != nil {
		t.Fatalf("first user: %v", err)
	}
	_, err := EnsureMasterKey("user-second", "device-2")
	if err == nil {
		t.Fatalf("expected guard rejection, got nil")
	}
}

// TestVaultV2RoundTrip: a v2-only vault can be created, written to, then
// re-opened with the same master key and the entries are preserved.
func TestVaultV2RoundTrip(t *testing.T) {
	isolatedVaultDir(t)
	var mk [masterKeyLen]byte
	if _, err := io.ReadFull(rand.Reader, mk[:]); err != nil {
		t.Fatalf("gen master key: %v", err)
	}
	vs1, err := NewVaultStoreV2(mk, "dev-1")
	if err != nil {
		t.Fatalf("create v2 vault: %v", err)
	}
	if err := vs1.Set(VaultEntry{Name: "NPM_TOKEN", Project: "cli", Category: "api-key", Value: "npm_secretvalue"}); err != nil {
		t.Fatalf("set: %v", err)
	}
	// On-disk first 4 bytes must be the v2 magic.
	path, _ := VaultPath()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read vault file: %v", err)
	}
	if !hasV2Magic(data) {
		t.Fatalf("v2 file lacks magic header (first 4 bytes: %q)", data[:4])
	}

	// Reopen with the same key.
	vs2, err := NewVaultStoreV2(mk, "dev-1")
	if err != nil {
		t.Fatalf("reopen v2 vault: %v", err)
	}
	got, err := vs2.Get("cli", "NPM_TOKEN")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Value != "npm_secretvalue" {
		t.Fatalf("value mismatch: %q", got.Value)
	}

	// Wrong key fails with the expected error message.
	var wrong [masterKeyLen]byte
	if _, err := io.ReadFull(rand.Reader, wrong[:]); err != nil {
		t.Fatalf("gen wrong key: %v", err)
	}
	_, err = NewVaultStoreV2(wrong, "dev-1")
	if err == nil {
		t.Fatalf("expected wrong-key error, got nil")
	}
}

// TestResetDeadVaultToV2: when the master key that sealed a v2 vault is gone
// (the headless "wrong passphrase (v2)" brick), resetDeadVaultToV2 archives the
// unreadable file and starts a fresh empty vault under the current master key —
// so a cloud box self-heals instead of failing every vault op forever.
func TestResetDeadVaultToV2(t *testing.T) {
	isolatedVaultDir(t)
	// Seal a vault with key A + a secret we can no longer reach after "loss".
	var keyA [masterKeyLen]byte
	if _, err := io.ReadFull(rand.Reader, keyA[:]); err != nil {
		t.Fatalf("gen key A: %v", err)
	}
	vs, err := NewVaultStoreV2(keyA, "dev-1")
	if err != nil {
		t.Fatalf("create v2 vault: %v", err)
	}
	if err := vs.Set(VaultEntry{Name: "GLM_API_KEY", Project: "runners", Value: "dead-secret"}); err != nil {
		t.Fatalf("set: %v", err)
	}
	path, _ := VaultPath()

	// Simulate key loss: a different master key B cannot open the file.
	var keyB [masterKeyLen]byte
	if _, err := io.ReadFull(rand.Reader, keyB[:]); err != nil {
		t.Fatalf("gen key B: %v", err)
	}
	if _, err := NewVaultStoreV2(keyB, "dev-1"); err == nil {
		t.Fatalf("expected wrong-key error before reset")
	}

	// Self-heal under B.
	fresh, err := resetDeadVaultToV2(keyB, "dev-1")
	if err != nil {
		t.Fatalf("resetDeadVaultToV2: %v", err)
	}
	if got := fresh.List("runners"); len(got) != 0 {
		t.Fatalf("expected empty fresh vault, got %d entries", len(got))
	}
	// The file is now sealed with B and reopens cleanly; a new secret persists.
	if err := fresh.Set(VaultEntry{Name: "GLM_API_KEY", Project: "runners", Value: "new-secret"}); err != nil {
		t.Fatalf("set on fresh vault: %v", err)
	}
	reopened, err := NewVaultStoreV2(keyB, "dev-1")
	if err != nil {
		t.Fatalf("reopen fresh vault under B: %v", err)
	}
	if got, err := reopened.Get("runners", "GLM_API_KEY"); err != nil || got.Value != "new-secret" {
		t.Fatalf("fresh secret not persisted (val=%q err=%v)", got.Value, err)
	}
	// The unreadable original was archived (kept for forensics), not destroyed.
	entries, _ := os.ReadDir(filepath.Dir(path))
	archived := false
	for _, e := range entries {
		if strings.Contains(e.Name(), "unreadable") {
			archived = true
		}
	}
	if !archived {
		t.Fatalf("expected an archived 'unreadable' vault file in %s", filepath.Dir(path))
	}
}

// TestVaultV2RejectsLegacyV1File: opening a v1-format file with the v2
// constructor surfaces ErrVaultIsLegacyV1 so the caller can fall back
// to the passphrase chain and migrate.
func TestVaultV2RejectsLegacyV1File(t *testing.T) {
	isolatedVaultDir(t)

	// Create a v1 vault with a passphrase.
	vs1, err := NewVaultStoreWithDevice("v1-pass", "dev-1")
	if err != nil {
		t.Fatalf("create v1 vault: %v", err)
	}
	if err := vs1.Set(VaultEntry{Name: "API_KEY", Value: "v1-value"}); err != nil {
		t.Fatalf("set v1: %v", err)
	}

	// v2 constructor must reject this file with the sentinel.
	var mk [masterKeyLen]byte
	if _, err := io.ReadFull(rand.Reader, mk[:]); err != nil {
		t.Fatalf("gen mk: %v", err)
	}
	_, err = NewVaultStoreV2(mk, "dev-1")
	if !errors.Is(err, ErrVaultIsLegacyV1) {
		t.Fatalf("expected ErrVaultIsLegacyV1, got %v", err)
	}
}

// TestVaultV1RejectsV2File: the inverse — a v1 passphrase open of a v2
// file returns ErrVaultIsV2, so the caller routes to the master-key
// path instead of producing a misleading "wrong passphrase" message.
func TestVaultV1RejectsV2File(t *testing.T) {
	isolatedVaultDir(t)
	var mk [masterKeyLen]byte
	if _, err := io.ReadFull(rand.Reader, mk[:]); err != nil {
		t.Fatalf("gen mk: %v", err)
	}
	if _, err := NewVaultStoreV2(mk, "dev-1"); err != nil {
		t.Fatalf("create v2 vault: %v", err)
	}
	_, err := NewVaultStoreWithDevice("any-pass", "dev-1")
	if !errors.Is(err, ErrVaultIsV2) {
		t.Fatalf("expected ErrVaultIsV2, got %v", err)
	}
}

// TestVaultV1ToV2Migration: simulate the happy migration path — create
// a populated v1 vault, then call RekeyToMasterKey with a fresh master
// key. The file flips to v2 (magic header present), the entries
// survive, and v1 opens of the same file now fail with ErrVaultIsV2.
func TestVaultV1ToV2Migration(t *testing.T) {
	isolatedVaultDir(t)

	vs, err := NewVaultStoreWithDevice("v1-pass", "dev-1")
	if err != nil {
		t.Fatalf("create v1 vault: %v", err)
	}
	for k, v := range map[string]string{
		"APP_STORE_KEY_ID": "AAA",
		"APPLE_TEAM_ID":    "BBB",
	} {
		if err := vs.Set(VaultEntry{Name: k, Project: "mobile", Value: v}); err != nil {
			t.Fatalf("set %s: %v", k, err)
		}
	}

	var mk [masterKeyLen]byte
	if _, err := io.ReadFull(rand.Reader, mk[:]); err != nil {
		t.Fatalf("gen mk: %v", err)
	}
	if err := vs.RekeyToMasterKey(mk); err != nil {
		t.Fatalf("RekeyToMasterKey: %v", err)
	}

	// On-disk should now have the v2 magic.
	path, _ := VaultPath()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read vault file: %v", err)
	}
	if !hasV2Magic(data) {
		t.Fatalf("post-migration file lacks v2 magic (first 4 bytes: %q)", data[:4])
	}

	// v2 open under the same master key returns the migrated entries.
	vs2, err := NewVaultStoreV2(mk, "dev-1")
	if err != nil {
		t.Fatalf("reopen as v2: %v", err)
	}
	got, err := vs2.Get("mobile", "APP_STORE_KEY_ID")
	if err != nil || got.Value != "AAA" {
		t.Fatalf("migrated entry lost: got=%v err=%v", got, err)
	}

	// v1 open of the now-v2 file must surface ErrVaultIsV2 — never
	// silently "wrong passphrase".
	_, err = NewVaultStoreWithDevice("v1-pass", "dev-1")
	if !errors.Is(err, ErrVaultIsV2) {
		t.Fatalf("expected ErrVaultIsV2 after migration, got %v", err)
	}
}

// TestPurgeMasterKey: signout-style wipe deletes both files; subsequent
// EnsureMasterKey starts fresh with a NEW random key (the old vault.enc
// is now undecryptable, by design — same as logout-different-user).
func TestPurgeMasterKey(t *testing.T) {
	dir := isolatedVaultDir(t)
	k1, err := EnsureMasterKey("user-x", "dev-1")
	if err != nil {
		t.Fatalf("ensure 1: %v", err)
	}
	if err := PurgeMasterKey(); err != nil {
		t.Fatalf("purge: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, masterKeyFilename)); !os.IsNotExist(err) {
		t.Fatalf("master.key should be gone, err=%v", err)
	}
	k2, err := EnsureMasterKey("user-x", "dev-1")
	if err != nil {
		t.Fatalf("ensure 2: %v", err)
	}
	if bytes.Equal(k1[:], k2[:]) {
		t.Fatalf("purge-then-ensure must produce a NEW key (got the same bytes back)")
	}
}

// TestMasterKeyMetaJSON: smoke-test the sidecar format so future
// changes (e.g. adding a "version" field) get caught.
func TestMasterKeyMetaJSON(t *testing.T) {
	m := masterKeyMeta{UserID: "u-1", DeviceID: "d-1", CreatedAt: 12345}
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back masterKeyMeta
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if back != m {
		t.Fatalf("roundtrip mismatch: %+v vs %+v", m, back)
	}
}
