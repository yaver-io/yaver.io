package main

// ssh_keygen.go — frictionless device SSH key lifecycle for the out-of-band
// channel (docs/architecture/ROBUST_TRANSPORT_SSH_QUIC.md). The user never sees
// or manages a key: it is generated automatically on first use, stored 0600
// under ~/.yaver/ssh, and rotated silently on reset/compromise/detach. The phone
// uses the Secure Enclave instead (native, later); this is the desktop/CLI +
// closed-loop-test client key.

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/crypto/ssh"
)

// generateEd25519SSHKey creates a fresh ed25519 keypair and returns the OpenSSH
// private key (PEM) and the authorized_keys public line body (e.g.
// "ssh-ed25519 AAAA… <comment>"). ed25519 is the modern default: small, fast,
// no parameter choices to get wrong.
func generateEd25519SSHKey(comment string) (privPEM, pubLine string, err error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return "", "", err
	}
	block, err := ssh.MarshalPrivateKey(priv, comment)
	if err != nil {
		return "", "", err
	}
	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		return "", "", err
	}
	line := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(sshPub)))
	if strings.TrimSpace(comment) != "" {
		line += " " + comment
	}
	return string(pem.EncodeToMemory(block)), line, nil
}

// localDeviceSSHKeyPaths returns the private/public key paths under ~/.yaver/ssh.
func localDeviceSSHKeyPaths() (privPath, pubPath string, err error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", "", err
	}
	dir := filepath.Join(home, ".yaver", "ssh")
	return filepath.Join(dir, "device_ed25519"), filepath.Join(dir, "device_ed25519.pub"), nil
}

// writeDeviceKeyPair writes a freshly generated keypair to disk with correct
// perms (private 0600, public 0644, dir 0700) and returns the public line.
func writeDeviceKeyPair(comment string) (pubLine string, err error) {
	privPath, pubPath, err := localDeviceSSHKeyPaths()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(privPath), 0o700); err != nil {
		return "", err
	}
	privPEM, pubLine, err := generateEd25519SSHKey(comment)
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(privPath, []byte(privPEM), 0o600); err != nil {
		return "", err
	}
	if err := os.WriteFile(pubPath, []byte(pubLine+"\n"), 0o644); err != nil {
		return "", err
	}
	return pubLine, nil
}

// ensureLocalDeviceSSHKey generates the device keypair on first use and returns
// its public authorized_keys line. Idempotent + frictionless: reuses the existing
// key if present, no prompts, no user action.
func ensureLocalDeviceSSHKey(comment string) (pubLine string, err error) {
	_, pubPath, err := localDeviceSSHKeyPaths()
	if err != nil {
		return "", err
	}
	if b, err := os.ReadFile(pubPath); err == nil {
		if line := strings.TrimSpace(string(b)); line != "" {
			return line, nil
		}
	}
	return writeDeviceKeyPair(comment)
}

// rotateLocalDeviceSSHKey generates fresh key material, replacing the old one.
// Frictionless — invoked on reset-access / suspected compromise / device detach.
// The caller is responsible for re-installing the new public key on the agent
// (applyManagedKey) and removing the old managed entry.
func rotateLocalDeviceSSHKey(comment string) (pubLine string, err error) {
	return writeDeviceKeyPair(comment)
}
