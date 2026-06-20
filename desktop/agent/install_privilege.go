package main

// install_privilege.go — single source of truth for Yaver's install-time OS
// user & privilege model. See docs/yaver-install-user-privilege-model.md.
//
// The goal: the agent can install/remove software and manage its own (and, in
// operator mode, its tenants') services — but it does NOT run as root and the
// sudo it IS granted cannot `rm -rf` a home directory, read /etc/shadow, or
// stop sshd. Three layers cooperate:
//
//  1. a dedicated non-root `yaver` system user (off the personal machine),
//  2. a SCOPED sudoers allowlist (this file) replacing the old NOPASSWD: ALL,
//  3. systemd unit hardening (ProtectSystem=strict + ProtectHome=read-only with
//     a single read-write hole at the agent's own home).
//
// Honest limit: scoped sudo to `apt install` / `useradd` is still effectively
// root (maintainer scripts, arbitrary user creation). This layer stops the
// casual footgun + gives an audit trail; real multi-tenant isolation comes from
// containers + per-tenant OS users, not from sudoers cleverness.

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// privilegeProfile selects how broad the sudoers grant is.
type privilegeProfile int

const (
	// profileSelfHost — a single-tenant box the user owns (their own VPS,
	// bootstrapped into their mesh). The agent acts as the box owner, so it
	// may manage packages and arbitrary services. It still cannot `sudo rm`,
	// `sudo cat /etc/shadow`, etc. — those simply aren't in the allowlist.
	profileSelfHost privilegeProfile = iota
	// profileOperator — a multi-tenant Yaver-operated fleet node. Tightly
	// scoped: only the package + tenant-lifecycle + yaver/docker service
	// commands the agent actually shells out to. Notably NOT arbitrary
	// `systemctl` (can't stop sshd) and NOT `userdel` of a non-yv- user.
	profileOperator
)

const (
	// yaverSystemUser is the dedicated non-root account the agent runs under
	// on dedicated/cloud boxes. Home stays under /home (not /var/lib) because
	// the $HOME/Workspace convention and several call sites assume /home/yaver,
	// and operator tenant work uses `sudo -iu yaver` login shells.
	yaverSystemUser = "yaver"
	yaverSystemHome = "/home/" + yaverSystemUser

	// yaverSudoersPath is the canonical drop-in. 0440 root:root, validated by
	// `visudo -cf` semantics (single user, full command paths only).
	yaverSudoersPath = "/etc/sudoers.d/90-yaver"
)

// ensureYaverUserSnippet returns the canonical, idempotent shell that creates
// the dedicated `yaver` system user. This REPLACES three drifted call sites
// (cloud_deploy.go, multiregion_orchestrate.go, launch_cmd.go) that each used
// slightly different flags. `--system` keeps it out of the human UID range;
// a real /bin/bash + home is required because operator code logs in via
// `sudo -iu yaver`.
func ensureYaverUserSnippet() string {
	return fmt.Sprintf(
		"id %s >/dev/null 2>&1 || useradd --system --create-home --home-dir %s --shell /bin/bash --comment 'Yaver agent' %s",
		yaverSystemUser, yaverSystemHome, yaverSystemUser,
	)
}

// yaverSudoersContent returns the scoped /etc/sudoers.d/90-yaver body for the
// given profile. It NEVER returns `NOPASSWD: ALL`.
func yaverSudoersContent(p privilegeProfile) string {
	// Package management — both apt and apt-get (the agent shells `sudo apt
	// install` in mcp_registries.go; cloud bootstrap uses apt-get). dpkg/dnf/
	// pacman included so the same drop-in works across the apt/rpm/arch boxes
	// we bootstrap.
	pkg := []string{
		"/usr/bin/apt-get install *", "/usr/bin/apt-get remove *", "/usr/bin/apt-get update",
		"/usr/bin/apt install *", "/usr/bin/apt remove *", "/usr/bin/apt update",
		"/usr/bin/dpkg -i *",
		"/usr/bin/dnf install *", "/usr/bin/dnf remove *",
		"/usr/bin/pacman -S *", "/usr/bin/pacman -R *",
	}

	switch p {
	case profileOperator:
		// Tenant lifecycle: create/destroy yv-* users, wipe their homes, kill
		// their processes, make their Workspace dir. Mirrors the exact argv in
		// tenant_osuser.go. The yv-* wildcards mean userdel can NEVER target a
		// human account, and the install/useradd targets are pinned to
		// /home/yv-*.
		tenant := []string{
			"/usr/sbin/useradd --create-home --home-dir /home/yv-* --shell /bin/bash yv-*",
			"/sbin/useradd --create-home --home-dir /home/yv-* --shell /bin/bash yv-*",
			"/usr/sbin/userdel -r yv-*", "/sbin/userdel -r yv-*",
			"/usr/bin/pkill -KILL -u yv-*", "/bin/pkill -KILL -u yv-*",
			"/usr/bin/install -d -o yv-* -g yv-* -m 0700 /home/yv-*",
		}
		// Services: only yaver's own units + docker. NOT arbitrary systemctl —
		// an operator agent must not be able to stop sshd or other tenants'
		// adjacent services.
		svc := []string{
			"/usr/bin/systemctl start yaver*", "/usr/bin/systemctl stop yaver*", "/usr/bin/systemctl restart yaver*",
			"/bin/systemctl start yaver*", "/bin/systemctl stop yaver*", "/bin/systemctl restart yaver*",
			"/usr/bin/systemctl start docker", "/usr/bin/systemctl enable docker",
			"/bin/systemctl start docker", "/bin/systemctl enable docker",
		}
		return sudoersFile([][2]string{
			{"YAVER_PKG", strings.Join(pkg, ", ")},
			{"YAVER_TENANT", strings.Join(tenant, ", ")},
			{"YAVER_SVC", strings.Join(svc, ", ")},
		}, []string{"YAVER_PKG", "YAVER_TENANT", "YAVER_SVC"})

	default: // profileSelfHost
		// The box owner's own machine: package mgmt + full service control are
		// legitimate. Still no `rm`/`dd`/`mkfs`/`chmod` of foreign homes — they
		// are simply absent from the allowlist, so "install yes, rm $HOME no"
		// holds without enumerating every dangerous binary.
		svc := []string{
			"/usr/bin/systemctl *", "/bin/systemctl *",
		}
		return sudoersFile([][2]string{
			{"YAVER_PKG", strings.Join(pkg, ", ")},
			{"YAVER_SVC", strings.Join(svc, ", ")},
		}, []string{"YAVER_PKG", "YAVER_SVC"})
	}
}

// sudoersFile assembles Cmnd_Alias lines + the single grant line, with a header
// explaining the model. aliasOrder fixes the grant ordering for stable tests.
func sudoersFile(aliases [][2]string, aliasOrder []string) string {
	var b strings.Builder
	b.WriteString("# Managed by Yaver — see docs/yaver-install-user-privilege-model.md\n")
	b.WriteString("# Scoped grant (NOT NOPASSWD: ALL). The yaver user can install\n")
	b.WriteString("# packages and manage the listed services; it CANNOT rm a home,\n")
	b.WriteString("# read /etc/shadow, or stop unlisted services via sudo.\n")
	for _, a := range aliases {
		fmt.Fprintf(&b, "Cmnd_Alias %s = %s\n", a[0], a[1])
	}
	fmt.Fprintf(&b, "%s ALL=(root) NOPASSWD: %s\n", yaverSystemUser, strings.Join(aliasOrder, ", "))
	return b.String()
}

// writeSudoersSnippet returns a shell snippet that installs the scoped sudoers
// drop-in at 0440 root:root, validating it with `visudo -cf` before activating
// so a malformed file can never lock the box out of sudo. Used by remote
// bootstrap paths that pipe a script over SSH.
func writeSudoersSnippet(p privilegeProfile) string {
	content := yaverSudoersContent(p)
	tmp := yaverSudoersPath + ".tmp"
	return fmt.Sprintf(`cat > %s <<'YAVER_SUDOERS_EOF'
%sYAVER_SUDOERS_EOF
chmod 0440 %s && chown root:root %s
if visudo -cf %s >/dev/null 2>&1; then mv %s %s; else rm -f %s; echo 'yaver: sudoers validation failed, not installed' >&2; fi`,
		tmp, content, tmp, tmp, tmp, tmp, yaverSudoersPath, tmp)
}

// hardenedSystemUnit returns a full /etc/systemd/system/yaver.service that runs
// the agent as the dedicated `yaver` user with kernel-enforced confinement.
//
// ProtectSystem=strict makes the whole filesystem read-only except the
// explicit ReadWritePaths — so the agent process itself cannot touch /etc,
// /usr, /boot, or any /home/<otheruser>. ProtectHome=read-only hides every
// home; the single ReadWritePaths hole at the agent's own home is what lets it
// keep working.
//
// NoNewPrivileges is deliberately NOT set: the agent still escalates via sudo
// (apt, systemctl, tenant management) until the privilege-separated helper
// lands (docs §step 4). Flip it to true the day that helper ships.
func hardenedSystemUnit(yaverBin string, operator bool) string {
	execLine := yaverBin + " serve --debug --work-dir " + yaverSystemHome
	if operator {
		execLine += " --operator --relay-only"
	}
	// Operator boxes also write tenant homes under /home/yv-*; those writes go
	// through sudo→root (unconfined), but mount the whole /home rw on operator
	// nodes so any direct bookkeeping under a tenant home also succeeds.
	rwPaths := yaverSystemHome + " /var/lib/yaver /var/log/yaver"
	if operator {
		rwPaths = "/home /var/lib/yaver /var/log/yaver"
	}
	return fmt.Sprintf(`[Unit]
Description=Yaver Agent (dedicated %s user, hardened)
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=%s
Group=%s
ExecStart=%s
Restart=on-failure
RestartSec=5
WorkingDirectory=%s
Environment=HOME=%s

# --- Confinement (docs/yaver-install-user-privilege-model.md) ---
ProtectSystem=strict
ProtectHome=read-only
ReadWritePaths=%s
ProtectKernelTunables=true
ProtectControlGroups=true
RestrictSUIDSGID=true
RestrictRealtime=true
LockPersonality=true
# NoNewPrivileges intentionally unset: the agent still needs sudo (apt /
# systemctl / tenant mgmt) until the privileged helper lands. Flip to true then.

[Install]
WantedBy=multi-user.target
`, yaverSystemUser, yaverSystemUser, yaverSystemUser, execLine, yaverSystemHome, yaverSystemHome, rwPaths)
}

// systemUnitPath is the canonical location for the dedicated-user system unit.
const systemUnitPath = "/etc/systemd/system/yaver.service"

// installSystemdSystemService provisions a dedicated-box install: a non-root
// `yaver` system user, a scoped sudoers drop-in, and a hardened SYSTEM systemd
// unit running the agent as that user. Requires root (writes /etc/systemd and
// /etc/sudoers.d). This is the install path for a VPS or shared box the user
// owns — distinct from `--install-systemd`, which runs as the invoking user on
// a personal machine. Exits the process (matches installSystemdService).
func installSystemdSystemService(operator bool) {
	if runtime.GOOS != "linux" {
		fmt.Fprintln(os.Stderr, "--install-systemd-system is Linux-only (use --install-launchd-daemon on macOS).")
		os.Exit(1)
	}
	if os.Geteuid() != 0 {
		fmt.Fprintln(os.Stderr, "--install-systemd-system writes /etc/systemd/system and /etc/sudoers.d — re-run with sudo:")
		fmt.Fprintln(os.Stderr, "  sudo yaver serve --install-systemd-system")
		os.Exit(1)
	}

	yaverBin, err := os.Executable()
	if err != nil || yaverBin == "" {
		yaverBin = "yaver"
	}

	profile := profileSelfHost
	if operator {
		profile = profileOperator
	}

	// 1. Dedicated non-root user (idempotent).
	if err := runRootShell(ensureYaverUserSnippet()); err != nil {
		fmt.Fprintf(os.Stderr, "Error creating %s user: %v\n", yaverSystemUser, err)
		os.Exit(1)
	}
	// 2. Scoped sudoers, validated with visudo before activation.
	if err := runRootShell(writeSudoersSnippet(profile)); err != nil {
		fmt.Fprintf(os.Stderr, "Error installing scoped sudoers: %v\n", err)
		os.Exit(1)
	}
	// 3. State + log dirs owned by the yaver user.
	_ = runRootShell(fmt.Sprintf("install -d -o %s -g %s -m 0750 /var/lib/yaver /var/log/yaver",
		yaverSystemUser, yaverSystemUser))

	// 4. Hardened system unit.
	unit := hardenedSystemUnit(yaverBin, operator)
	if err := os.MkdirAll(filepath.Dir(systemUnitPath), 0755); err != nil {
		fmt.Fprintf(os.Stderr, "Error creating unit dir: %v\n", err)
		os.Exit(1)
	}
	if err := os.WriteFile(systemUnitPath, []byte(unit), 0644); err != nil {
		fmt.Fprintf(os.Stderr, "Error writing %s: %v\n", systemUnitPath, err)
		os.Exit(1)
	}
	fmt.Printf("Created: %s (User=%s, ProtectSystem=strict)\n", systemUnitPath, yaverSystemUser)
	fmt.Printf("Created: %s (scoped sudo — no NOPASSWD: ALL)\n", yaverSudoersPath)

	for _, c := range [][]string{
		{"systemctl", "daemon-reload"},
		{"systemctl", "enable", "yaver"},
		{"systemctl", "start", "yaver"},
	} {
		cmd := exec.Command(c[0], c[1:]...)
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
		if err := cmd.Run(); err != nil {
			fmt.Printf("Note: '%s' failed: %v\n", strings.Join(c, " "), err)
			fmt.Println("Enable manually with: systemctl daemon-reload && systemctl enable --now yaver")
			os.Exit(1)
		}
	}

	fmt.Println()
	fmt.Printf("Yaver agent installed as a hardened SYSTEM service running as the non-root '%s' user.\n", yaverSystemUser)
	fmt.Println("  Status:  systemctl status yaver")
	fmt.Println("  Logs:    journalctl -u yaver -f")
	fmt.Println("  Stop:    systemctl stop yaver")
	if operator {
		fmt.Println("  Mode:    operator (multi-tenant; tenants scoped + auto-wiped)")
	}
}

// runShell runs a bash snippet, returning combined output in the error.
func runRootShell(script string) error {
	out, err := exec.Command("bash", "-c", script).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}
