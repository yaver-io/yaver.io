package main

import (
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
)

const foreignKeys = `# the user's own laptop, we must never touch this
ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIFOREIGNuserLaptopKeyDoNotRemove user@laptop
ssh-rsa AAAAB3NzaC1yc2EAAAADAQABsomeOldRsaKey deploy@ci
`

// The single most important property: adding or removing OUR managed key never
// disturbs a key we did not create. An off-by-one here locks the user out.
func TestManagedKeys_NeverTouchForeignKeys(t *testing.T) {
	self := "/usr/local/bin/yaver"
	// Install a managed key for device-A alongside the foreign keys.
	withA := installManagedKey(foreignKeys, self, "device-A", "user-1", "ssh-ed25519 AAAAKEYA", "2026-07-21")
	if !strings.Contains(withA, "FOREIGNuserLaptopKeyDoNotRemove") || !strings.Contains(withA, "someOldRsaKey") {
		t.Fatal("install dropped a foreign key")
	}
	if !strings.Contains(withA, "yaver-managed-device-A") {
		t.Fatal("install did not add the managed key")
	}
	// Install a second device; both managed keys + both foreign keys present.
	withAB := installManagedKey(withA, self, "device-B", "user-1", "ssh-ed25519 AAAAKEYB", "2026-07-21")
	for _, must := range []string{"FOREIGNuserLaptopKeyDoNotRemove", "someOldRsaKey", "yaver-managed-device-A", "yaver-managed-device-B"} {
		if !strings.Contains(withAB, must) {
			t.Fatalf("after installing 2 devices, missing %q", must)
		}
	}
	// Revoke device-A → device-A gone, device-B and BOTH foreign keys survive.
	afterRevoke := removeManagedDevice(withAB, "device-A")
	if strings.Contains(afterRevoke, "yaver-managed-device-A") {
		t.Fatal("revoke left device-A behind")
	}
	for _, must := range []string{"FOREIGNuserLaptopKeyDoNotRemove", "someOldRsaKey", "yaver-managed-device-B"} {
		if !strings.Contains(afterRevoke, must) {
			t.Fatalf("revoke of device-A removed %q — LOCKOUT bug", must)
		}
	}
}

func TestManagedKeys_RotationReplacesSameDevice(t *testing.T) {
	self := "/usr/local/bin/yaver"
	v1 := installManagedKey("", self, "device-A", "user-1", "ssh-ed25519 AAAAKEYOLD", "2026-07-21")
	v2 := installManagedKey(v1, self, "device-A", "user-1", "ssh-ed25519 AAAAKEYNEW", "2026-07-22")
	if strings.Contains(v2, "AAAAKEYOLD") {
		t.Fatal("rotation left the OLD key material behind")
	}
	if !strings.Contains(v2, "AAAAKEYNEW") {
		t.Fatal("rotation did not install the NEW key")
	}
	if strings.Count(v2, "yaver-managed-device-A") != 1 {
		t.Fatalf("expected exactly ONE managed block for device-A after rotation, got %d",
			strings.Count(v2, "yaver-managed-device-A"))
	}
}

func TestManagedKeys_ForcedCommandCage(t *testing.T) {
	block := sshManagedKeyBlock("/usr/local/bin/yaver", "device-A", "user-1", "ssh-ed25519 AAAAKEYA comment-dropped", "2026-07-21")
	// The key line pins the forced command and denies shell/forwarding.
	for _, must := range []string{
		`command="/usr/local/bin/yaver ssh-session --session device-A"`,
		"no-pty", "no-agent-forwarding", "no-port-forwarding", "no-user-rc",
		"# yaver-managed: device=device-A owner=user-1",
	} {
		if !strings.Contains(block, must) {
			t.Errorf("managed block missing required cage/marker: %q", must)
		}
	}
	// The input's trailing comment is stripped; we control the tag.
	if strings.Contains(block, "comment-dropped") {
		t.Error("input key comment should be stripped, not preserved")
	}
}

func TestManagedKeys_RevokeAbsentIsNoop(t *testing.T) {
	if got := removeManagedDevice(foreignKeys, "device-NOPE"); got != foreignKeys {
		t.Error("revoking an absent device must leave content byte-for-byte unchanged")
	}
}

func TestGenerateEd25519SSHKey_ProducesValidKey(t *testing.T) {
	priv, pub, err := generateEd25519SSHKey("yaver-managed-device-X")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(pub, "ssh-ed25519 ") || !strings.HasSuffix(pub, " yaver-managed-device-X") {
		t.Fatalf("public line malformed: %q", pub)
	}
	if _, err := ssh.ParsePrivateKey([]byte(priv)); err != nil {
		t.Fatalf("generated private key does not parse: %v", err)
	}
	if _, _, _, _, err := ssh.ParseAuthorizedKey([]byte(pub)); err != nil {
		t.Fatalf("generated public line is not a valid authorized_keys entry: %v", err)
	}
}
