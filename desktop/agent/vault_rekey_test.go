package main

import (
	"os"
	"path/filepath"
	"testing"
)

// vaultDirForTest mirrors the shape of openVault/NewVaultStore by
// pointing HOME at a temp dir. NewVaultStore reads ~/.yaver/vault.enc
// directly via VaultPath().
func vaultDirForTest(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("YAVER_VAULT_PASSPHRASE", "")
	if err := os.MkdirAll(filepath.Join(dir, ".yaver"), 0700); err != nil {
		t.Fatalf("mkdir .yaver: %v", err)
	}
	return dir
}

// TestVaultRekeyTo_ReencryptsUnderNewKey is the core property: after
// rekey, the OLD passphrase must FAIL and the NEW one must succeed.
func TestVaultRekeyTo_ReencryptsUnderNewKey(t *testing.T) {
	vaultDirForTest(t)

	vs, err := NewVaultStore("old-passphrase")
	if err != nil {
		t.Fatalf("NewVaultStore: %v", err)
	}
	if err := vs.Set(VaultEntry{Name: "k", Value: "v"}); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := vs.RekeyTo("new-passphrase"); err != nil {
		t.Fatalf("RekeyTo: %v", err)
	}

	if _, err := NewVaultStore("old-passphrase"); err == nil {
		t.Fatal("expected old passphrase to fail after rekey")
	}
	vs2, err := NewVaultStore("new-passphrase")
	if err != nil {
		t.Fatalf("open with new passphrase: %v", err)
	}
	got, err := vs2.Get("", "k")
	if err != nil || got == nil || got.Value != "v" {
		t.Fatalf("expected entry survives rekey, got %+v err=%v", got, err)
	}
}

// TestSetAuthToken_RekeysVault: the wired-in helper actually rekeys
// vault.enc end-to-end so subsequent openVault under the new token
// works without YAVER_VAULT_PASSPHRASE.
func TestSetAuthToken_RekeysVault(t *testing.T) {
	vaultDirForTest(t)

	cfg := &Config{AuthToken: "token-A"}
	vs, err := NewVaultStore(DerivePassphraseFromToken(cfg.AuthToken))
	if err != nil {
		t.Fatalf("seed vault: %v", err)
	}
	if err := vs.Set(VaultEntry{Name: "k", Value: "v"}); err != nil {
		t.Fatalf("seed entry: %v", err)
	}

	if err := SetAuthToken(cfg, "token-B"); err != nil {
		t.Fatalf("SetAuthToken: %v", err)
	}
	if cfg.AuthToken != "token-B" {
		t.Fatalf("AuthToken = %q, want token-B", cfg.AuthToken)
	}
	if cfg.PreviousAuthToken != "token-A" {
		t.Fatalf("PreviousAuthToken = %q, want token-A", cfg.PreviousAuthToken)
	}

	// Vault now opens under the new token directly.
	vs2, err := NewVaultStore(DerivePassphraseFromToken("token-B"))
	if err != nil {
		t.Fatalf("vault under new token: %v", err)
	}
	got, err := vs2.Get("", "k")
	if err != nil || got == nil || got.Value != "v" {
		t.Fatalf("entry not preserved after SetAuthToken: %+v err=%v", got, err)
	}
}

// TestSetAuthToken_NoVaultIsBenign: SetAuthToken on a fresh machine
// (no vault.enc yet) must not error out — bootstrap and pairing
// flows hit this path on a clean install.
func TestSetAuthToken_NoVaultIsBenign(t *testing.T) {
	dir := vaultDirForTest(t)
	cfg := &Config{AuthToken: "token-A"}
	if err := SetAuthToken(cfg, "token-B"); err != nil {
		t.Fatalf("SetAuthToken on missing vault: %v", err)
	}
	// vault.enc must still not exist — we don't want SetAuthToken
	// silently materialising one just to attempt a rekey.
	if _, err := os.Stat(filepath.Join(dir, ".yaver", "vault.enc")); !os.IsNotExist(err) {
		t.Fatalf("expected no vault.enc, stat err=%v", err)
	}
}

// TestSetAuthToken_ManualPassphraseSkipsRekey: when the user has set
// YAVER_VAULT_PASSPHRASE, their vault is decoupled from the auth
// token. SetAuthToken must NOT touch vault.enc — otherwise it would
// re-key under a token-derived passphrase the user didn't ask for and
// they'd lose access.
func TestSetAuthToken_ManualPassphraseSkipsRekey(t *testing.T) {
	vaultDirForTest(t)
	t.Setenv("YAVER_VAULT_PASSPHRASE", "manual-master")

	vs, err := NewVaultStore("manual-master")
	if err != nil {
		t.Fatalf("seed vault: %v", err)
	}
	if err := vs.Set(VaultEntry{Name: "k", Value: "v"}); err != nil {
		t.Fatalf("seed entry: %v", err)
	}

	cfg := &Config{AuthToken: "token-A"}
	if err := SetAuthToken(cfg, "token-B"); err != nil {
		t.Fatalf("SetAuthToken: %v", err)
	}

	// Vault still readable only under the manual passphrase.
	if _, err := NewVaultStore(DerivePassphraseFromToken("token-B")); err == nil {
		t.Fatal("expected token-derived passphrase to fail (rekey should have been skipped)")
	}
	vs2, err := NewVaultStore("manual-master")
	if err != nil {
		t.Fatalf("manual passphrase still works: %v", err)
	}
	got, err := vs2.Get("", "k")
	if err != nil || got == nil || got.Value != "v" {
		t.Fatalf("entry not preserved under manual passphrase: %+v err=%v", got, err)
	}
}

// TestSetAuthToken_NoOpWhenSame: setting the same token must not
// rotate, must not stash a previous token, must not touch vault.
func TestSetAuthToken_NoOpWhenSame(t *testing.T) {
	vaultDirForTest(t)
	cfg := &Config{AuthToken: "token-A", PreviousAuthToken: ""}
	if err := SetAuthToken(cfg, "token-A"); err != nil {
		t.Fatalf("SetAuthToken: %v", err)
	}
	if cfg.PreviousAuthToken != "" {
		t.Fatalf("no-op rotation should not write PreviousAuthToken, got %q", cfg.PreviousAuthToken)
	}
}

// TestRekeyTo_LockedVaultRefuses: RekeyTo on a locked store must
// error rather than write a fresh vault file under the new key.
func TestRekeyTo_LockedVaultRefuses(t *testing.T) {
	vaultDirForTest(t)
	vs := &VaultStore{} // never opened, unlocked == false
	if err := vs.RekeyTo("anything"); err == nil {
		t.Fatal("expected RekeyTo on locked store to error")
	}
}

// TestOpenVaultE_FallsBackToPreviousAuthToken: simulates an older
// build that rotated cfg.AuthToken without rekeying the vault. On
// the first openVault call, the PreviousAuthToken fallback must
// kick in, decrypt the entries, rekey under the new token, and
// clear cfg.PreviousAuthToken on disk so the second open is a
// straightforward current-token success.
func TestOpenVaultE_FallsBackToPreviousAuthToken(t *testing.T) {
	vaultDirForTest(t)

	// Vault was created under token-A.
	seedVS, err := NewVaultStore(DerivePassphraseFromToken("token-A"))
	if err != nil {
		t.Fatalf("seed vault: %v", err)
	}
	if err := seedVS.Set(VaultEntry{Name: "k", Value: "v"}); err != nil {
		t.Fatalf("seed entry: %v", err)
	}

	// Then a buggy/older code path rotated AuthToken to token-B
	// WITHOUT rekeying. Persisted PreviousAuthToken=token-A as the
	// safety-net breadcrumb that openVaultE is designed to consume.
	cfg := &Config{AuthToken: "token-B", PreviousAuthToken: "token-A"}
	if err := SaveConfig(cfg); err != nil {
		t.Fatalf("seed config: %v", err)
	}

	vs, err := openVaultE()
	if err != nil {
		t.Fatalf("openVaultE: %v", err)
	}
	got, err := vs.Get("", "k")
	if err != nil || got == nil || got.Value != "v" {
		t.Fatalf("expected entry survives via fallback: %+v err=%v", got, err)
	}

	// PreviousAuthToken must be cleared on disk after the auto-rekey.
	persisted, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if persisted.PreviousAuthToken != "" {
		t.Fatalf("expected PreviousAuthToken cleared after auto-rekey, got %q", persisted.PreviousAuthToken)
	}

	// And the vault now opens cleanly under token-B without the
	// fallback path running again.
	if _, err := NewVaultStore(DerivePassphraseFromToken("token-B")); err != nil {
		t.Fatalf("vault not openable under current token after rekey: %v", err)
	}
	if _, err := NewVaultStore(DerivePassphraseFromToken("token-A")); err == nil {
		t.Fatal("expected old token to no longer decrypt after rekey")
	}
}

// TestOpenVaultE_NoFallbackWhenWrongCurrentAndNoPrevious: when both
// current decrypt fails AND there's no PreviousAuthToken, the user
// must see the helpful guidance message — not a silent partial open.
func TestOpenVaultE_NoFallbackWhenWrongCurrentAndNoPrevious(t *testing.T) {
	vaultDirForTest(t)

	// Vault under one token, config claims a different token, no
	// PreviousAuthToken breadcrumb (e.g., user hand-edited config or
	// upgraded from an even-older agent).
	if _, err := NewVaultStore(DerivePassphraseFromToken("token-A")); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := SaveConfig(&Config{AuthToken: "token-B"}); err != nil {
		t.Fatalf("seed config: %v", err)
	}

	if _, err := openVaultE(); err == nil {
		t.Fatal("expected openVaultE to fail without a fallback token")
	}
}

// TestSetAuthToken_RekeysRuntimeStoreInPlace: when the agent has a
// long-lived VaultStore registered (HTTPServer.vaultStore — used by
// /vault/sync, /vault/push, etc.), SetAuthToken must rekey THAT
// store, not a fresh disk-opened copy. Writes that race with token
// rotation (sync inbound while heartbeat rotates) would otherwise
// persist under the OLD key, regress vault.enc, and brick the next
// rotation when even the PreviousAuthToken fallback no longer
// matches what's on disk.
func TestSetAuthToken_RekeysRuntimeStoreInPlace(t *testing.T) {
	vaultDirForTest(t)

	cfg := &Config{AuthToken: "token-A"}
	runtimeVS, err := NewVaultStore(DerivePassphraseFromToken(cfg.AuthToken))
	if err != nil {
		t.Fatalf("seed vault: %v", err)
	}
	if err := runtimeVS.Set(VaultEntry{Name: "boot", Value: "1"}); err != nil {
		t.Fatalf("seed entry: %v", err)
	}

	setRuntimeVaultStore(runtimeVS)
	t.Cleanup(func() { setRuntimeVaultStore(nil) })

	if err := SetAuthToken(cfg, "token-B"); err != nil {
		t.Fatalf("rotation A→B: %v", err)
	}

	// Simulates an inbound /vault/push hitting the live runtime
	// store right after rotation. Without the in-place rekey this
	// write encrypts under the OLD key — silently regressing
	// vault.enc — and the next rotation has no recovery path.
	if err := runtimeVS.Set(VaultEntry{Name: "post-rotate", Value: "2"}); err != nil {
		t.Fatalf("write after rotation: %v", err)
	}

	// Second rotation: with the previous fix the on-disk vault is
	// still encrypted under token-B (the runtime store was rekeyed
	// in place), so the standard rekey path can hop B→C cleanly.
	if err := SetAuthToken(cfg, "token-C"); err != nil {
		t.Fatalf("rotation B→C: %v", err)
	}

	// Both entries readable via a fresh open under the current token.
	fresh, err := NewVaultStore(DerivePassphraseFromToken("token-C"))
	if err != nil {
		t.Fatalf("open under current token: %v", err)
	}
	if got, err := fresh.Get("", "boot"); err != nil || got == nil || got.Value != "1" {
		t.Fatalf("boot entry lost across two rotations: %+v err=%v", got, err)
	}
	if got, err := fresh.Get("", "post-rotate"); err != nil || got == nil || got.Value != "2" {
		t.Fatalf("post-rotation entry lost: %+v err=%v", got, err)
	}
}
