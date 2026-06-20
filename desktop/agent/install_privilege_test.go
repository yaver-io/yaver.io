package main

import (
	"strings"
	"testing"
)

// grantLines strips the human-readable header comments so substring assertions
// inspect only the actual Cmnd_Alias / grant lines (the header legitimately
// mentions "NOPASSWD: ALL", "rm", "/etc/shadow" while explaining what it forbids).
func grantLines(sudoers string) string {
	var out []string
	for _, l := range strings.Split(sudoers, "\n") {
		if strings.HasPrefix(strings.TrimSpace(l), "#") {
			continue
		}
		out = append(out, l)
	}
	return strings.Join(out, "\n")
}

// The whole point of this module: scoped grants, never NOPASSWD: ALL.
func TestSudoersNeverGrantsAll(t *testing.T) {
	for _, p := range []privilegeProfile{profileSelfHost, profileOperator} {
		got := grantLines(yaverSudoersContent(p))
		if strings.Contains(got, "NOPASSWD: ALL") {
			t.Fatalf("profile %d grants NOPASSWD: ALL — the exact hole this replaces:\n%s", p, got)
		}
		if !strings.Contains(got, "NOPASSWD:") {
			t.Fatalf("profile %d missing any NOPASSWD grant:\n%s", p, got)
		}
		// No catch-all command in any alias body.
		for _, line := range strings.Split(got, "\n") {
			if strings.HasPrefix(line, "Cmnd_Alias") && strings.Contains(line, "= ALL") {
				t.Fatalf("alias resolves to ALL:\n%s", line)
			}
		}
	}
}

// "install yes, rm $HOME no" — the literal user requirement.
func TestSudoersAllowsInstallDeniesRm(t *testing.T) {
	for _, p := range []privilegeProfile{profileSelfHost, profileOperator} {
		got := grantLines(yaverSudoersContent(p))
		if !strings.Contains(got, "apt install") && !strings.Contains(got, "apt-get install") {
			t.Errorf("profile %d cannot install packages:\n%s", p, got)
		}
		for _, banned := range []string{"/bin/rm", "/usr/bin/rm", " rm ", "mkfs", "/bin/dd", "/etc/shadow", "chown root"} {
			if strings.Contains(got, banned) {
				t.Errorf("profile %d allowlist contains dangerous %q:\n%s", p, banned, got)
			}
		}
	}
}

// Operator profile must be tight: no arbitrary systemctl (can't stop sshd),
// and userdel pinned to yv-* (can't delete a human account).
func TestOperatorProfileIsTight(t *testing.T) {
	got := grantLines(yaverSudoersContent(profileOperator))
	if strings.Contains(got, "systemctl *") {
		t.Errorf("operator grants arbitrary systemctl (could stop sshd):\n%s", got)
	}
	if !strings.Contains(got, "userdel -r yv-*") {
		t.Errorf("operator userdel not pinned to yv-*:\n%s", got)
	}
	if strings.Contains(got, "userdel -r *") || strings.Contains(got, "userdel *") {
		t.Errorf("operator userdel is not tenant-scoped:\n%s", got)
	}
}

// Self-host may manage arbitrary services (it's the owner's own box).
func TestSelfHostAllowsServiceControl(t *testing.T) {
	got := grantLines(yaverSudoersContent(profileSelfHost))
	if !strings.Contains(got, "systemctl *") {
		t.Errorf("self-host should allow full systemctl on the owner's box:\n%s", got)
	}
}

// The canonical user-create snippet is a dedicated --system user, idempotent.
func TestEnsureYaverUserSnippet(t *testing.T) {
	got := ensureYaverUserSnippet()
	for _, want := range []string{"useradd", "--system", "--home-dir " + yaverSystemHome, "id " + yaverSystemUser} {
		if !strings.Contains(got, want) {
			t.Errorf("user snippet missing %q: %s", want, got)
		}
	}
}

// Hardened SYSTEM unit confines the agent: runs as yaver, FS read-only except
// the agent's own home, and explicitly does NOT set NoNewPrivileges yet
// (sudo still needed). The personal user unit must NOT carry ProtectHome.
func TestHardenedSystemUnit(t *testing.T) {
	got := hardenedSystemUnit("/usr/bin/yaver", false)
	for _, want := range []string{
		"User=" + yaverSystemUser,
		"ProtectSystem=strict",
		"ProtectHome=read-only",
		"ReadWritePaths=" + yaverSystemHome,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("hardened unit missing %q:\n%s", want, got)
		}
	}
	// Self-host keeps sudo, so NoNewPrivileges must stay off.
	if strings.Contains(got, "NoNewPrivileges=true") {
		t.Errorf("self-host must not set NoNewPrivileges (still uses scoped sudo):\n%s", got)
	}

	op := hardenedSystemUnit("/usr/bin/yaver", true)
	if !strings.Contains(op, "--operator") {
		t.Errorf("operator unit missing --operator flag:\n%s", op)
	}
	// Operator needs to write tenant homes under /home → whole /home is rw.
	if !strings.Contains(op, "ReadWritePaths=/home ") {
		t.Errorf("operator unit should mount /home rw for tenant homes:\n%s", op)
	}
	// Step 5 payoff: the confined operator agent runs zero-sudo.
	if !strings.Contains(op, "NoNewPrivileges=true") {
		t.Errorf("operator unit must set NoNewPrivileges=true (all privileged ops via helper):\n%s", op)
	}
	if !strings.Contains(op, "Requires="+helperUnitName) {
		t.Errorf("operator unit must Require the helper:\n%s", op)
	}
}

// visudo-guard: the writer never blindly activates an unvalidated file.
func TestWriteSudoersSnippetValidatesBeforeActivating(t *testing.T) {
	got := writeSudoersSnippet(profileSelfHost)
	if !strings.Contains(got, "visudo -cf") {
		t.Errorf("sudoers writer must validate with visudo before mv:\n%s", got)
	}
	if !strings.Contains(got, "mv "+yaverSudoersPath+".tmp "+yaverSudoersPath) {
		t.Errorf("sudoers writer should stage to .tmp then mv on success:\n%s", got)
	}
}
