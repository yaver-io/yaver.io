package main

// auth_ssh_http.go — POST /auth/ssh/authorized-keys.
//
// Same-Convex-user trust channel for bootstrapping SSH keys. Once two
// boxes are signed in as the same Yaver user, either side can ask the
// other to append its pubkey to ~/.ssh/authorized_keys without anyone
// touching ssh-copy-id, an out-of-band paste, or a prior SSH session.
//
// The trust gate is reused from the existing /info path: this endpoint
// is registered behind s.auth(), so the request must carry a Convex
// bearer token whose userId matches the device's ownerUserID. No
// in-handler permission code — if it got here, it's authorized.
//
// Side effects are local-only: nothing about the request reaches Convex.
// The privacy contract (no key material, no paths, no secrets in
// userland Convex documents) holds.

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	osuser "os/user"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

// agentRuntimeUserInfo reports the OS user + home dir the agent
// process is running as. Used by /info so the CLI can pick the same
// SSH user when dialing this box (root vs. yaver vs. kivanc), instead
// of guessing from the local $USER and falling back to "root".
//
// Falls back to a numeric uid + best-guess home dir when the user
// lookup fails (containers without /etc/passwd entries, busybox
// images, etc.). Never returns an error — `/info` should always
// publish *something*.
func agentRuntimeUserInfo() (name, home string) {
	if u, err := osuser.Current(); err == nil {
		name = strings.TrimSpace(u.Username)
		home = strings.TrimSpace(u.HomeDir)
	}
	if name == "" {
		name = "uid:" + strconv.Itoa(os.Geteuid())
	}
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	return name, home
}

type sshAuthorizedKeysRequest struct {
	PublicKey string `json:"publicKey"`
	Label     string `json:"label"`
}

// sshAuthorizedKeysMu serializes read-modify-write on authorized_keys to
// avoid races when two yaver agents bootstrap the same box concurrently
// (e.g. macmini + laptop both newly signed in, both run `yaver ssh
// primary` in the same minute). Per-process is enough — we don't expect
// multiple yaver daemons on the same OS user.
var sshAuthorizedKeysMu sync.Mutex

func (s *HTTPServer) handleSSHAuthorizedKeys(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}
	var req sshAuthorizedKeysRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid json body")
		return
	}
	keyType, keyBlob, err := parseAuthorizedKeyLine(req.PublicKey)
	if err != nil {
		jsonError(w, http.StatusBadRequest, err.Error())
		return
	}
	label := strings.TrimSpace(req.Label)

	sshAuthorizedKeysMu.Lock()
	defer sshAuthorizedKeysMu.Unlock()

	added, fingerprint, err := appendAuthorizedKeyLocal(keyType, keyBlob, label)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "append authorized_keys: "+err.Error())
		return
	}
	jsonReply(w, http.StatusOK, map[string]any{
		"ok":             true,
		"alreadyPresent": !added,
		"fingerprint":    fingerprint,
		"keyType":        keyType,
	})
}

// parseAuthorizedKeyLine accepts a single OpenSSH-format authorized_keys
// line ("<type> <base64> [comment]") and returns (type, base64-blob).
// Rejects unknown algorithms — we only allow the modern set.
func parseAuthorizedKeyLine(raw string) (keyType, keyBlob string, err error) {
	line := strings.TrimSpace(raw)
	if line == "" {
		return "", "", errors.New("publicKey is empty")
	}
	// Strip authorized_keys options if any — `command="..." ssh-ed25519 …`
	// is legal in authorized_keys but not in pubkey files. Rejecting them
	// keeps the surface predictable and avoids privilege-shell surprises.
	if strings.HasPrefix(line, "command=") || strings.HasPrefix(line, "no-") || strings.HasPrefix(line, "restrict") || strings.HasPrefix(line, "from=") {
		return "", "", errors.New("authorized_keys options are not allowed; send a plain pubkey")
	}
	fields := strings.Fields(line)
	if len(fields) < 2 {
		return "", "", errors.New("publicKey must be `<type> <base64> [comment]`")
	}
	keyType = fields[0]
	keyBlob = fields[1]
	switch keyType {
	case "ssh-ed25519", "ssh-rsa", "ecdsa-sha2-nistp256", "ecdsa-sha2-nistp384", "ecdsa-sha2-nistp521", "sk-ecdsa-sha2-nistp256@openssh.com", "sk-ssh-ed25519@openssh.com":
		// allowed
	default:
		return "", "", fmt.Errorf("unsupported key type %q", keyType)
	}
	if _, err := base64.StdEncoding.DecodeString(keyBlob); err != nil {
		return "", "", fmt.Errorf("base64 blob is invalid: %w", err)
	}
	return keyType, keyBlob, nil
}

// appendAuthorizedKeyLocal writes the key to ~/.ssh/authorized_keys for
// the OS user the agent runs as, deduping on the (type, blob) pair.
// Returns (added, fingerprint, err).
func appendAuthorizedKeyLocal(keyType, keyBlob, label string) (bool, string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return false, "", err
	}
	sshDir := filepath.Join(home, ".ssh")
	if err := os.MkdirAll(sshDir, 0o700); err != nil {
		return false, "", err
	}
	// Best-effort tighten in case the dir existed with looser perms.
	_ = os.Chmod(sshDir, 0o700)

	akPath := filepath.Join(sshDir, "authorized_keys")
	existing, err := os.ReadFile(akPath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return false, "", err
	}
	wantPrefix := keyType + " " + keyBlob
	for _, ln := range strings.Split(string(existing), "\n") {
		fields := strings.Fields(strings.TrimSpace(ln))
		if len(fields) >= 2 && fields[0] == keyType && fields[1] == keyBlob {
			return false, sshKeyFingerprint(keyBlob), nil
		}
	}
	comment := strings.TrimSpace(label)
	if comment == "" {
		comment = "yaver-bootstrap"
	}
	// Sanitize comment: single-line, no shell metacharacters that could
	// confuse downstream tooling reading authorized_keys.
	comment = strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' || r == '\t' {
			return ' '
		}
		return r
	}, comment)
	newLine := wantPrefix + " " + comment
	var buf strings.Builder
	if len(existing) > 0 {
		buf.Write(existing)
		if !strings.HasSuffix(string(existing), "\n") {
			buf.WriteByte('\n')
		}
	}
	buf.WriteString(newLine)
	buf.WriteByte('\n')
	if err := os.WriteFile(akPath, []byte(buf.String()), 0o600); err != nil {
		return false, "", err
	}
	_ = os.Chmod(akPath, 0o600)
	return true, sshKeyFingerprint(keyBlob), nil
}

// sshKeyFingerprint returns the SHA256 fingerprint OpenSSH prints
// (e.g. "SHA256:abc…"). Input is the base64 key blob without the
// "ssh-ed25519 " prefix or trailing comment.
func sshKeyFingerprint(keyBlob string) string {
	raw, err := base64.StdEncoding.DecodeString(keyBlob)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(raw)
	return "SHA256:" + strings.TrimRight(base64.StdEncoding.EncodeToString(sum[:]), "=")
}
