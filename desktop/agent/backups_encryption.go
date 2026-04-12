package main

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// Encrypted backups at rest. Reuses the same AES-GCM key as the accounts
// manager (~/.yaver/master.key). Backup files get an .enc extension when
// encryption is on. Restore auto-detects .enc and decrypts before replay.

// BackupEncryptionConfig controls whether new backups should be encrypted.
type BackupEncryptionConfig struct {
	Enabled bool `json:"enabled" yaml:"enabled"`
}

func backupEncryptionPath(projectDir string) string {
	return filepath.Join(projectDir, ".yaver", "backups-encryption.json")
}

// IsBackupEncryptionEnabled checks the project's config. Defaults to false.
func IsBackupEncryptionEnabled(projectDir string) bool {
	data, err := os.ReadFile(backupEncryptionPath(projectDir))
	if err != nil {
		return false
	}
	return strings.Contains(string(data), `"enabled":true`) || strings.Contains(string(data), `"enabled": true`)
}

// SetBackupEncryption flips the flag on/off for a project.
func SetBackupEncryption(projectDir string, enabled bool) error {
	_ = os.MkdirAll(filepath.Dir(backupEncryptionPath(projectDir)), 0o755)
	body := fmt.Sprintf(`{"enabled":%v}`, enabled)
	return os.WriteFile(backupEncryptionPath(projectDir), []byte(body), 0o644)
}

// EncryptBackupFile reads plaintext, writes ciphertext to path+".enc", and
// returns the new path. Removes the plaintext original. Fails-open: if the
// master key isn't available, the file stays plaintext.
func EncryptBackupFile(path string) (string, error) {
	key, err := globalAccountsManager.ensureKey()
	if err != nil {
		return path, err
	}
	plaintext, err := os.ReadFile(path)
	if err != nil {
		return path, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return path, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return path, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return path, err
	}
	ciphertext := gcm.Seal(nonce, nonce, plaintext, nil)
	encPath := path + ".enc"
	if err := os.WriteFile(encPath, ciphertext, 0o600); err != nil {
		return path, err
	}
	// Remove the plaintext dump only after the encrypted copy is on disk.
	_ = os.Remove(path)
	return encPath, nil
}

// DecryptBackupFile reads path (expected .enc), writes a plaintext temp file,
// and returns the new path. Caller must remove the plaintext when done.
func DecryptBackupFile(path string) (string, error) {
	key, err := globalAccountsManager.ensureKey()
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	if len(data) < gcm.NonceSize() {
		return "", fmt.Errorf("ciphertext too short")
	}
	nonce, body := data[:gcm.NonceSize()], data[gcm.NonceSize():]
	plaintext, err := gcm.Open(nil, nonce, body, nil)
	if err != nil {
		return "", err
	}
	plainPath := strings.TrimSuffix(path, ".enc")
	if plainPath == path {
		plainPath = path + ".decrypted"
	}
	if err := os.WriteFile(plainPath, plaintext, 0o600); err != nil {
		return "", err
	}
	return plainPath, nil
}

// HTTP handler for toggling backup encryption on/off per project.
func (s *HTTPServer) handleBackupEncryption(w http.ResponseWriter, r *http.Request) {
	dir := s.dirParam(r)
	if r.Method == "GET" {
		writeJSON(w, 200, map[string]interface{}{"enabled": IsBackupEncryptionEnabled(dir)})
		return
	}
	if r.Method == "POST" {
		var b struct{ Enabled bool `json:"enabled"` }
		decodeJSON(r, &b)
		if err := SetBackupEncryption(dir, b.Enabled); err != nil {
			writeJSON(w, 200, map[string]interface{}{"error": err.Error()})
			return
		}
		writeJSON(w, 200, map[string]interface{}{"ok": true, "enabled": b.Enabled})
		return
	}
	jsonError(w, 405, "GET or POST")
}
