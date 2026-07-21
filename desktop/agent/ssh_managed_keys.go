package main

// ssh_managed_keys.go — Phase 2 of the out-of-band SSH channel
// (docs/architecture/ROBUST_TRANSPORT_SSH_QUIC.md): lifecycle of the
// `# yaver-managed` forced-command keys in the box's authorized_keys.
//
// A paired device gets ONE managed entry: a `command="yaver ssh-session …"`
// forced-command line pinned to its public key, tagged in the key comment as
// `yaver-managed-<deviceId>` and preceded by a `# yaver-managed: …` marker line.
//
// THE hard safety rule (canonical doc + CLAUDE.md): we may ONLY ever add/replace/
// remove lines WE created. Any key line we did not tag is untouchable — install,
// rotate, and revoke must leave every unknown key exactly as found. That
// invariant is why the mutation logic is pure (string in → string out) and
// unit-tested: an off-by-one that drops a user's real key is a lockout.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// sshForcedCommandOptions is the cage the device key is pinned to: it can invoke
// ONLY `yaver ssh-session` (the whitelisted verb proxy), never a shell, never
// forwarding, never a user rc file.
func sshForcedCommandOptions(selfPath, deviceID string) string {
	return fmt.Sprintf(
		`command="%s ssh-session --session %s",no-pty,no-agent-forwarding,no-port-forwarding,no-user-rc,no-X11-forwarding`,
		selfPath, deviceID,
	)
}

// sshManagedComment is the trailing comment on the key line — the tag we use to
// recognize (and only ever touch) our own entries.
func sshManagedComment(deviceID string) string {
	return "yaver-managed-" + deviceID
}

// sshManagedMarker is the `#` comment line we write before each managed key so a
// human reading authorized_keys sees provenance (device/owner/when).
func sshManagedMarker(deviceID, userID, createdAt string) string {
	return fmt.Sprintf("# yaver-managed: device=%s owner=%s created=%s", deviceID, userID, createdAt)
}

// sshManagedKeyBlock builds the full 2-line block (marker + forced-command key)
// for a device. pubKey is the bare `ssh-ed25519 AAAA…` (no options, no comment);
// any trailing comment on the input is stripped so we control the tag.
func sshManagedKeyBlock(selfPath, deviceID, userID, pubKey, createdAt string) string {
	fields := strings.Fields(strings.TrimSpace(pubKey))
	keyBody := pubKey
	if len(fields) >= 2 {
		keyBody = fields[0] + " " + fields[1] // keytype + base64, drop any comment
	}
	line := fmt.Sprintf("%s %s %s",
		sshForcedCommandOptions(selfPath, deviceID), keyBody, sshManagedComment(deviceID))
	return sshManagedMarker(deviceID, userID, createdAt) + "\n" + line
}

// lineIsManagedFor reports whether an authorized_keys line is OUR managed key for
// the given device (matched by the `yaver-managed-<deviceId>` tag in the comment
// field or the forced-command referencing that deviceId). Marker `#` lines are
// matched separately in removeManagedDevice.
func lineIsManagedFor(line, deviceID string) bool {
	tag := sshManagedComment(deviceID)
	// Comment field tag (exact word, not a prefix of a longer id).
	if fs := strings.Fields(line); len(fs) > 0 {
		if fs[len(fs)-1] == tag {
			return true
		}
	}
	// Or the forced-command names this exact session id.
	return strings.Contains(line, `ssh-session --session `+deviceID+`"`)
}

// lineIsAnyManagedMarker reports whether a line is one of OUR `# yaver-managed:`
// marker comments (for any device). Only our markers are ever removed.
func lineIsAnyManagedMarker(line string) bool {
	return strings.HasPrefix(strings.TrimSpace(line), "# yaver-managed:")
}

// removeManagedDevice returns authorized_keys content with the managed block for
// deviceID removed — the key line AND an immediately-preceding `# yaver-managed:`
// marker. Every other line (unknown keys, the user's real keys, blank lines,
// foreign comments) is preserved byte-for-byte. Pure; the tested safety core.
func removeManagedDevice(content, deviceID string) string {
	lines := strings.Split(content, "\n")
	keep := make([]string, 0, len(lines))
	for i := 0; i < len(lines); i++ {
		if lineIsManagedFor(lines[i], deviceID) {
			// Drop this key line, and drop the marker line just above it if that
			// marker is ours (never drop a foreign comment).
			if len(keep) > 0 && lineIsAnyManagedMarker(keep[len(keep)-1]) {
				keep = keep[:len(keep)-1]
			}
			continue
		}
		keep = append(keep, lines[i])
	}
	return strings.Join(keep, "\n")
}

// installManagedKey returns authorized_keys content with the device's managed
// block present exactly once: any prior managed block for the SAME device is
// replaced (rotation), all other lines untouched, and the new block appended.
func installManagedKey(content, selfPath, deviceID, userID, pubKey, createdAt string) string {
	pruned := removeManagedDevice(content, deviceID)
	block := sshManagedKeyBlock(selfPath, deviceID, userID, pubKey, createdAt)
	pruned = strings.TrimRight(pruned, "\n")
	if pruned == "" {
		return block + "\n"
	}
	return pruned + "\n" + block + "\n"
}

// authorizedKeysPath is ~/.ssh/authorized_keys for the current user.
func authorizedKeysPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".ssh", "authorized_keys"), nil
}

// applyManagedKey installs/rotates the device key on disk, creating ~/.ssh
// (0700) and authorized_keys (0600) if needed, and never touching non-managed
// lines. selfPath defaults to this executable.
func applyManagedKey(deviceID, userID, pubKey, createdAt string) error {
	if strings.TrimSpace(deviceID) == "" || strings.TrimSpace(pubKey) == "" {
		return fmt.Errorf("applyManagedKey: deviceID and pubKey required")
	}
	self, _ := os.Executable()
	if self == "" {
		self = "yaver"
	}
	path, err := authorizedKeysPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	existing := ""
	if b, err := os.ReadFile(path); err == nil {
		existing = string(b)
	}
	updated := installManagedKey(existing, self, deviceID, userID, pubKey, createdAt)
	return os.WriteFile(path, []byte(updated), 0o600)
}

// revokeManagedKeyOnDisk removes the device's managed block, leaving all other
// keys intact. Idempotent (no-op if the file or the entry is absent).
func revokeManagedKeyOnDisk(deviceID string) error {
	path, err := authorizedKeysPath()
	if err != nil {
		return err
	}
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	updated := removeManagedDevice(string(b), deviceID)
	if updated == string(b) {
		return nil
	}
	return os.WriteFile(path, []byte(updated), 0o600)
}
