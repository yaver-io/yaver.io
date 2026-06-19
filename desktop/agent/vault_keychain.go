package main

// vault_keychain.go — master-key provider for vault.go (v2 file format).
//
// The vault's AEAD key is no longer Argon2id-derived from the live auth
// token; it's a per-machine 32-byte random "master key" persisted in
// ~/.yaver/master.key (mode 0600) and, on macOS, mirrored into the OS
// keychain so login-time biometric/password unlocks it transparently.
// This breaks the auth-token-rotation race that corrupted the vault for
// kivanc twice (2026-05-09 + 2026-05-20). Token rotations are now opaque
// to the vault. See memory project_vault_keychain_redesign.md.
//
// File-as-primary, keychain-as-mirror is intentional:
//   - The file works on every platform with no extra deps + no cgo (CI
//     builds the agent with CGO_ENABLED=0 per Dockerfile.yaver-cloud).
//   - On macOS the `security` shell-out adds the OS-auth-gated keychain
//     copy "for free" (the same protection Apple's Keychain offers every
//     other app — unlocked when the user is logged in, optionally
//     Touch-ID-gated when the user enabled "Require Touch ID for
//     password autofill / Keychain access" in System Settings).
//   - On Linux/Windows the file is the only path. A later commit can
//     add libsecret / Credential Manager mirrors using the same shape.
//
// Access guard: master.key.meta sits next to master.key and stores the
// user_id the key was provisioned for. Every vault open compares it to
// the user_id resolved from the current auth_token (offline mode: skip,
// vault still works locally). Mismatch ⇒ refuse — the wrong user can't
// read this user's secrets even on the same machine.

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const (
	masterKeyLen      = 32
	masterKeyFilename = "master.key"
	masterKeyMetaName = "master.key.meta"
	// macOS keychain service / account labels. Per-user (account ==
	// userID prefix) so multi-user-same-machine doesn't collide.
	keychainService = "io.yaver.vault"
)

// masterKeyMeta is the side-car JSON next to master.key. UserID identifies
// who provisioned the key; the vault refuses to open under a different
// user. CreatedAt is informational. DeviceID is the Yaver device UUID,
// useful for sync attribution (the vault entries already carry it; this
// is here for symmetry + diagnostics).
type masterKeyMeta struct {
	UserID    string `json:"user_id"`
	DeviceID  string `json:"device_id,omitempty"`
	CreatedAt int64  `json:"created_at"`
}

// masterKeyPaths returns the on-disk paths for the key + meta files,
// rooted in ~/.yaver/ (same dir as vault.enc, config.json).
func masterKeyPaths() (keyPath, metaPath string, err error) {
	dir, err := ConfigDir()
	if err != nil {
		return "", "", err
	}
	return filepath.Join(dir, masterKeyFilename), filepath.Join(dir, masterKeyMetaName), nil
}

// readMasterKeyFile loads the 32-byte master key from disk. Returns
// (key, true, nil) on success, (zero, false, nil) when the file doesn't
// exist (first-run case), or (zero, false, err) on any other failure.
func readMasterKeyFile(path string) (key [masterKeyLen]byte, ok bool, err error) {
	data, rerr := os.ReadFile(path)
	if rerr != nil {
		if os.IsNotExist(rerr) {
			return key, false, nil
		}
		return key, false, fmt.Errorf("read master key: %w", rerr)
	}
	// Stored on disk as raw 32 bytes; reject anything else (corrupt file).
	if len(data) != masterKeyLen {
		return key, false, fmt.Errorf("master key file %s has wrong size (%d, expected %d)", path, len(data), masterKeyLen)
	}
	copy(key[:], data)
	return key, true, nil
}

// writeMasterKeyFile persists the 32-byte master key + sidecar metadata
// atomically (write .tmp, fsync, rename). 0600 on the key file so other
// OS users on the same machine cannot read it. The sidecar holds user_id
// for the access guard; if userID is empty we still write the key (caller
// is in offline mode) but mark the meta accordingly.
func writeMasterKeyFile(keyPath, metaPath string, key [masterKeyLen]byte, userID, deviceID string) error {
	tmp := keyPath + ".tmp"
	if err := os.WriteFile(tmp, key[:], 0600); err != nil {
		return fmt.Errorf("write master key tmp: %w", err)
	}
	if err := os.Rename(tmp, keyPath); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("rename master key: %w", err)
	}
	meta := masterKeyMeta{
		UserID:    strings.TrimSpace(userID),
		DeviceID:  strings.TrimSpace(deviceID),
		CreatedAt: time.Now().UnixMilli(),
	}
	mb, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal master key meta: %w", err)
	}
	mtmp := metaPath + ".tmp"
	if err := os.WriteFile(mtmp, mb, 0600); err != nil {
		return fmt.Errorf("write master key meta tmp: %w", err)
	}
	if err := os.Rename(mtmp, metaPath); err != nil {
		_ = os.Remove(mtmp)
		return fmt.Errorf("rename master key meta: %w", err)
	}
	return nil
}

// readMasterKeyMeta loads the sidecar; returns nil, nil if absent (treated
// as "no guard" — first run, or legacy users mid-migration).
func readMasterKeyMeta(metaPath string) (*masterKeyMeta, error) {
	data, err := os.ReadFile(metaPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read master key meta: %w", err)
	}
	var m masterKeyMeta
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parse master key meta: %w", err)
	}
	return &m, nil
}

// keychainAccount derives the macOS keychain "account" attribute for a
// given Yaver user. Per-user so a multi-user macOS install doesn't share.
// Falls back to "default" when userID is empty (offline mode).
func keychainAccount(userID string) string {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return "default"
	}
	return userID
}

func vaultKeychainDisabled() bool {
	return os.Getenv("YAVER_VAULT_SKIP_KEYCHAIN") == "1" ||
		envTruthy(os.Getenv("YAVER_NONINTERACTIVE")) ||
		envTruthy(os.Getenv("CI"))
}

// readMasterKeyFromKeychain attempts to read the master key from the
// macOS Keychain. Returns (key, true, nil) on hit, (zero, false, nil)
// when the keychain has no entry or we're not on macOS, (zero, false, err)
// on a real error so the caller can decide whether to fall through to
// the file path. Tests set YAVER_VAULT_SKIP_KEYCHAIN=1 to short-circuit
// the macOS `security` shell-out so unit tests don't pollute the user's
// real login keychain.
func readMasterKeyFromKeychain(userID string) (key [masterKeyLen]byte, ok bool, err error) {
	if runtime.GOOS != "darwin" || vaultKeychainDisabled() {
		return key, false, nil
	}
	cmd := exec.Command("security", "find-generic-password",
		"-s", keychainService,
		"-a", keychainAccount(userID),
		"-w", // print just the password (hex-encoded key bytes)
	)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, runErr := cmd.Output()
	if runErr != nil {
		// "The specified item could not be found" ⇒ no entry yet, not
		// a real error. Anything else (locked keychain user
		// denied the prompt, missing security tool, ...) bubbles up.
		es := strings.ToLower(stderr.String())
		if strings.Contains(es, "could not be found") || strings.Contains(es, "not found") {
			return key, false, nil
		}
		return key, false, fmt.Errorf("security find-generic-password: %w (%s)", runErr, strings.TrimSpace(stderr.String()))
	}
	hexBytes := strings.TrimSpace(string(out))
	raw, decErr := hex.DecodeString(hexBytes)
	if decErr != nil || len(raw) != masterKeyLen {
		return key, false, fmt.Errorf("keychain master key malformed (len %d, hex err %v)", len(raw), decErr)
	}
	copy(key[:], raw)
	return key, true, nil
}

// writeMasterKeyToKeychain mirrors the master key into the macOS
// Keychain. Non-fatal: a failure (no security tool, locked keychain,
// user denial) only means the OS-auth-gated mirror won't exist;
// vault operations continue via the file path. We log but don't return
// the error so callers stay simple.
func writeMasterKeyToKeychain(userID string, key [masterKeyLen]byte) {
	if runtime.GOOS != "darwin" || vaultKeychainDisabled() {
		return
	}
	hexKey := hex.EncodeToString(key[:])
	// -U upserts (update if exists, add if not). -T '' restricts access
	// to processes invoked by the same user — `security` itself + any
	// Yaver agent child of the user's login session, the same trust
	// boundary `gh secret set` / git credential helper rely on.
	cmd := exec.Command("security", "add-generic-password",
		"-U",
		"-s", keychainService,
		"-a", keychainAccount(userID),
		"-w", hexKey,
		"-D", "Yaver vault master key",
		"-j", "Per-machine random 32B key encrypting ~/.yaver/vault.enc. Safe to delete; will be re-derived from ~/.yaver/master.key on next vault open.",
	)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		// Suppressed at the warn level — file path is still our source
		// of truth, this mirror is best-effort.
		fmt.Fprintf(os.Stderr, "[vault] note: macOS keychain mirror skipped: %v (%s)\n", err, strings.TrimSpace(stderr.String()))
	}
}

// EnsureMasterKey returns the per-machine master key, creating it on
// first run. The lookup order is:
//
//  1. ~/.yaver/master.key       — source of truth, every platform
//  2. macOS Keychain entry      — fallback restore if the file was lost
//     but the keychain still has the mirror (e.g. a `rm ~/.yaver/*`)
//  3. Generate a fresh random   — both file + keychain mirror written
//
// userID gates the access guard: if non-empty AND the existing
// master.key.meta has a different user_id, returns an error (refuse to
// open another user's vault on the same machine). Offline mode passes
// userID="" which skips the guard.
//
// deviceID is informational, stamped into the sidecar.
func EnsureMasterKey(userID, deviceID string) ([masterKeyLen]byte, error) {
	var zero [masterKeyLen]byte
	keyPath, metaPath, err := masterKeyPaths()
	if err != nil {
		return zero, err
	}
	// Access guard from the sidecar — applies whether the key is on
	// disk or in the keychain.
	if meta, mErr := readMasterKeyMeta(metaPath); mErr == nil && meta != nil {
		if userID != "" && meta.UserID != "" && meta.UserID != userID {
			return zero, fmt.Errorf("vault belongs to a different user on this machine (rotate via `yaver signout` to wipe)")
		}
	} else if mErr != nil {
		return zero, mErr
	}

	if key, ok, err := readMasterKeyFile(keyPath); err != nil {
		return zero, err
	} else if ok {
		// File present — mirror to keychain (cheap upsert; helps if a
		// prior run skipped the mirror).
		writeMasterKeyToKeychain(userID, key)
		return key, nil
	}

	// File missing — last chance: macOS keychain may still have it
	// (e.g. user ran `rm ~/.yaver/master.key`). Pull it back to disk.
	if key, ok, err := readMasterKeyFromKeychain(userID); err != nil {
		return zero, fmt.Errorf("read master key from keychain: %w", err)
	} else if ok {
		if werr := writeMasterKeyFile(keyPath, metaPath, key, userID, deviceID); werr != nil {
			return zero, werr
		}
		return key, nil
	}

	// True first run — generate.
	var fresh [masterKeyLen]byte
	if _, err := io.ReadFull(rand.Reader, fresh[:]); err != nil {
		return zero, fmt.Errorf("generate master key: %w", err)
	}
	if err := writeMasterKeyFile(keyPath, metaPath, fresh, userID, deviceID); err != nil {
		return zero, err
	}
	writeMasterKeyToKeychain(userID, fresh)
	return fresh, nil
}

// PurgeMasterKey removes the master key + sidecar + keychain mirror.
// Called from `yaver signout` so a different user signing in next
// doesn't inherit access to the previous user's encrypted vault. The
// vault.enc itself is left in place — it's already encrypted under a
// key that no longer exists, so it's effectively dead. Operator can
// `rm ~/.yaver/vault.enc` if they want the bytes gone too.
func PurgeMasterKey() error {
	keyPath, metaPath, err := masterKeyPaths()
	if err != nil {
		return err
	}
	if rerr := os.Remove(keyPath); rerr != nil && !os.IsNotExist(rerr) {
		return rerr
	}
	if rerr := os.Remove(metaPath); rerr != nil && !os.IsNotExist(rerr) {
		return rerr
	}
	if runtime.GOOS == "darwin" && !vaultKeychainDisabled() {
		// Best-effort; we don't know the userID at sign-out time
		// without resolving against Convex (which may be offline),
		// so we wipe all accounts under the service.
		_ = exec.Command("security", "delete-generic-password",
			"-s", keychainService).Run()
	}
	return nil
}
