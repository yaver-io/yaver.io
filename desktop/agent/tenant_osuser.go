package main

import (
	"fmt"
	"log"
	"os/exec"
	"os/user"
	"runtime"
	"strings"
)

// tenant_osuser.go — per-tenant OS users for the operator fleet (docs §4b).
//
// The primary tenant-isolation model: each tenant is its own unprivileged
// Linux user `yv-<id>` with home /home/yv-<id> and projects in
// $HOME/Workspace. This delivers, in one move: a per-tenant Workspace
// (their own home), OS-level separation (own uid — a tenant can't read
// another's home or the operator/yaver files), and a clean removable wipe
// (`userdel -r` takes the home + Workspace + leaves zero residue).
//
// Gated: only meaningful on a Linux operator box where the agent (running as
// the unprivileged `yaver` user) has passwordless sudo to manage users. On
// macOS / non-operator / no-sudo boxes this is a no-op and callers fall back
// to the in-process per-tenant Workspace dir (workspace_dir.go).
//
// NOTE: the setuid/sudo PTY launch path is gated OFF on every non-operator
// box; it is exercised only on a real operator Linux node and should be
// device-verified before relying on it in production.

// tenantOSUsersEnabled reports whether per-tenant OS users can be used here:
// Linux + sudo present. (Operator-mode gating is applied by the caller.)
func tenantOSUsersEnabled() bool {
	if runtime.GOOS != "linux" {
		return false
	}
	_, err := exec.LookPath("sudo")
	return err == nil
}

// ensureTenantOSUser creates (idempotently) the per-tenant Linux user and
// its $HOME/Workspace, returning the username + home dir. Requires
// passwordless sudo (the yaver agent user has it on operator boxes).
func ensureTenantOSUser(tenantUserID string) (username, home string, err error) {
	name := tenantUserName(tenantUserID)
	home = "/home/" + name
	if _, lookErr := user.Lookup(name); lookErr == nil {
		// Already exists — just make sure Workspace is there.
		if e := sudoRun("install", "-d", "-o", name, "-g", name, "-m", "0700", home+"/Workspace"); e != nil {
			return name, home, e
		}
		return name, home, nil
	}
	// Create a login-capable, unprivileged user with its own home.
	if e := sudoRun("useradd", "--create-home", "--home-dir", home, "--shell", "/bin/bash", name); e != nil {
		return "", "", fmt.Errorf("useradd %s: %w", name, e)
	}
	if e := sudoRun("install", "-d", "-o", name, "-g", name, "-m", "0700", home+"/Workspace"); e != nil {
		return name, home, e
	}
	return name, home, nil
}

// removeTenantOSUser hard-removes a tenant: kill all their processes, then
// `userdel -r` (deletes the home + Workspace). Idempotent — a missing user
// is success. This is the OS-level half of "removable allocation".
func removeTenantOSUser(tenantUserID string) error {
	name := tenantUserName(tenantUserID)
	if _, err := user.Lookup(name); err != nil {
		return nil // already gone
	}
	// Best-effort: kill everything the tenant is running first so userdel
	// doesn't fail on a busy home / live processes.
	_ = sudoRun("pkill", "-KILL", "-u", name)
	if err := sudoRun("userdel", "-r", name); err != nil {
		return fmt.Errorf("userdel %s: %w", name, err)
	}
	log.Printf("[OPERATOR] removed tenant OS user %s (home + Workspace wiped)", name)
	return nil
}

// sudoRun runs `sudo -n <args...>` (-n = never prompt; fail if a password
// would be needed). Returns the combined output in the error on failure.
func sudoRun(args ...string) error {
	full := append([]string{"-n"}, args...)
	out, err := exec.Command("sudo", full...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("sudo %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

// tenantShellArgv builds the argv that launches a login shell AS the tenant
// OS user, with the gateway inference env overlaid via `env` (sudo resets
// the environment, so we re-inject the vars we need on the command line).
// shell is the desired shell (e.g. /bin/bash). injectEnv is the slice of
// KEY=VALUE strings to expose inside the tenant shell (e.g. OPENAI_BASE_URL,
// OPENAI_API_KEY, TERM).
func tenantShellArgv(username, shell string, injectEnv []string) []string {
	if strings.TrimSpace(shell) == "" {
		shell = "/bin/bash"
	}
	argv := []string{"sudo", "-n", "-u", username, "-H", "env"}
	argv = append(argv, injectEnv...)
	argv = append(argv, shell)
	return argv
}
