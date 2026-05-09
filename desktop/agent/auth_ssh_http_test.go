package main

// auth_ssh_http_test.go — covers the local pieces of the SSH-key
// bootstrap endpoint without spinning up the full HTTPServer. The
// auth gate (s.auth()) is exercised in its own tests; here we just
// pin down parse/append/dedup so a future edit to authorized_keys
// handling can't silently drop a key or duplicate one.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseAuthorizedKeyLine_AcceptsKnownTypes(t *testing.T) {
	cases := []struct {
		name string
		raw  string
	}{
		{"ed25519", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAICTbZQyKGPSh3HCgVmRcWbm/aP9R5o0n/3JphRcsJ8s+ kivanc@laptop"},
		{"rsa", "ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABgQC9eA== test"},
		{"ecdsa", "ecdsa-sha2-nistp256 AAAAE2VjZHNhLXNoYTItbmlzdHAyNTYAAAAIbmlzdHAyNTY= ec"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			kt, kb, err := parseAuthorizedKeyLine(tc.raw)
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if !strings.HasPrefix(tc.raw, kt) {
				t.Fatalf("type mismatch: got %q", kt)
			}
			if kb == "" {
				t.Fatalf("blob is empty")
			}
		})
	}
}

func TestParseAuthorizedKeyLine_RejectsBadInputs(t *testing.T) {
	cases := []struct {
		name string
		raw  string
	}{
		{"empty", ""},
		{"single-field", "ssh-ed25519"},
		{"unknown-type", "ssh-fake AAAA test"},
		{"bad-base64", "ssh-ed25519 not_base64!! comment"},
		{"options-prefix-command", `command="rm -rf /" ssh-ed25519 AAAA test`},
		{"options-prefix-from", "from=\"10.0.0.0/8\" ssh-ed25519 AAAA test"},
		{"options-prefix-restrict", "restrict ssh-ed25519 AAAA test"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, err := parseAuthorizedKeyLine(tc.raw); err == nil {
				t.Fatalf("expected rejection for %q", tc.raw)
			}
		})
	}
}

// TestAppendAuthorizedKeyLocal_WritesAndDedupes covers the
// read-modify-write happy path: first call appends + creates the
// file with 0600, second call with the same key is a no-op (no
// duplication, alreadyPresent semantics surfaced via added=false).
func TestAppendAuthorizedKeyLocal_WritesAndDedupes(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	added, fp1, err := appendAuthorizedKeyLocal("ssh-ed25519", "AAAAC3NzaC1lZDI1NTE5AAAAICTbZQyKGPSh3HCgVmRcWbm/aP9R5o0n/3JphRcsJ8s+", "first-push")
	if err != nil {
		t.Fatalf("first append: %v", err)
	}
	if !added {
		t.Fatalf("expected added=true on first call")
	}
	if !strings.HasPrefix(fp1, "SHA256:") {
		t.Fatalf("expected SHA256 fingerprint, got %q", fp1)
	}

	akPath := filepath.Join(tmp, ".ssh", "authorized_keys")
	info, err := os.Stat(akPath)
	if err != nil {
		t.Fatalf("authorized_keys missing: %v", err)
	}
	// 0600 on the file, 0700 on the dir.
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Fatalf("authorized_keys perms = %o, want 0600", mode)
	}
	dirInfo, _ := os.Stat(filepath.Join(tmp, ".ssh"))
	if mode := dirInfo.Mode().Perm(); mode != 0o700 {
		t.Fatalf(".ssh perms = %o, want 0700", mode)
	}

	// Second call with same key is a no-op.
	added2, fp2, err := appendAuthorizedKeyLocal("ssh-ed25519", "AAAAC3NzaC1lZDI1NTE5AAAAICTbZQyKGPSh3HCgVmRcWbm/aP9R5o0n/3JphRcsJ8s+", "second-push")
	if err != nil {
		t.Fatalf("second append: %v", err)
	}
	if added2 {
		t.Fatalf("expected added=false on dedup")
	}
	if fp2 != fp1 {
		t.Fatalf("fingerprint changed: %q vs %q", fp1, fp2)
	}

	// File should contain exactly one matching line.
	body, _ := os.ReadFile(akPath)
	count := strings.Count(string(body), "AAAAC3NzaC1lZDI1NTE5AAAAICTbZQyKGPSh3HCgVmRcWbm/aP9R5o0n/3JphRcsJ8s+")
	if count != 1 {
		t.Fatalf("expected 1 occurrence of the blob, got %d", count)
	}
}

// TestAppendAuthorizedKeyLocal_AppendsNotOverwrites preserves any
// pre-existing keys (other tools, manual edits) when a yaver
// bootstrap pushes a fresh key.
func TestAppendAuthorizedKeyLocal_AppendsNotOverwrites(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	sshDir := filepath.Join(tmp, ".ssh")
	if err := os.MkdirAll(sshDir, 0o700); err != nil {
		t.Fatal(err)
	}
	akPath := filepath.Join(sshDir, "authorized_keys")
	preexisting := "ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABgQC9eA== preexisting@old\n"
	if err := os.WriteFile(akPath, []byte(preexisting), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, _, err := appendAuthorizedKeyLocal("ssh-ed25519", "AAAAC3NzaC1lZDI1NTE5AAAAICTbZQyKGPSh3HCgVmRcWbm/aP9R5o0n/3JphRcsJ8s+", "new"); err != nil {
		t.Fatalf("append: %v", err)
	}
	body, _ := os.ReadFile(akPath)
	if !strings.Contains(string(body), "preexisting@old") {
		t.Fatalf("preexisting key was wiped: %q", string(body))
	}
	if !strings.Contains(string(body), "AAAAC3NzaC1lZDI1NTE5") {
		t.Fatalf("new key not appended: %q", string(body))
	}
}

func TestAgentRuntimeUserInfo_ReturnsSomething(t *testing.T) {
	// On every developer / CI box agentRuntimeUserInfo() must return
	// a non-empty user + home dir (or the numeric uid fallback for
	// stripped containers). Hard requirement: /info publishes this
	// payload and the CLI relies on it for SSH user inference.
	name, home := agentRuntimeUserInfo()
	if name == "" {
		t.Fatalf("expected non-empty user name, got empty")
	}
	if home == "" {
		t.Fatalf("expected non-empty home dir, got empty")
	}
}

func TestSSHKeyFingerprint_StableOutput(t *testing.T) {
	// Same blob → same fingerprint, deterministic across runs. Not
	// pinning to a golden value because the SHA256 of an arbitrary
	// blob isn't user-meaningful — we only care about stability.
	a := sshKeyFingerprint("AAAAC3NzaC1lZDI1NTE5AAAAICTbZQyKGPSh3HCgVmRcWbm/aP9R5o0n/3JphRcsJ8s+")
	b := sshKeyFingerprint("AAAAC3NzaC1lZDI1NTE5AAAAICTbZQyKGPSh3HCgVmRcWbm/aP9R5o0n/3JphRcsJ8s+")
	if a == "" || a != b {
		t.Fatalf("fingerprint not stable: %q vs %q", a, b)
	}
	c := sshKeyFingerprint("AAAAC3NzaC1lZDI1NTE5AAAAICDIFFERENTBLOBzzzzzzzzzzzzzzzzzzzzzzzz")
	if c == a {
		t.Fatalf("different blob produced same fingerprint")
	}
}
