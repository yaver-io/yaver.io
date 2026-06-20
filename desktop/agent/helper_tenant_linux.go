//go:build linux

package main

// helper_tenant_linux.go — the PTY-brokering half of step 5. The root helper
// spawns the tenant's interactive shell, drops to the tenant uid/gid, and hands
// the PTY master fd back to the unprivileged agent over SCM_RIGHTS. This is what
// removes the last `sudo -u` from the agent, letting the operator agent unit run
// with NoNewPrivileges=true.
//
// Device-verify on a real operator Linux node before relying on it (same posture
// as the rest of tenant_osuser.go) — setuid + controlling-tty + fd-passing can't
// be exercised off a root Linux box.

import (
	"fmt"
	"net"
	"os/exec"
	"syscall"

	"github.com/creack/pty"
)

// serveTenantShell validates the request, spawns the shell as the tenant, sends
// the PTY master fd to the agent, then reaps the child. The JSON header carries
// {ok} / {error}; on success exactly one fd rides along via SCM_RIGHTS.
func (s *helperServer) serveTenantShell(uc *net.UnixConn, req helperRequest) {
	if err := validTenant(req.Tenant); err != nil {
		sendShellErr(uc, err)
		return
	}
	if err := validShell(req.Shell); err != nil {
		sendShellErr(uc, err)
		return
	}
	env, err := sanitizeTenantEnv(req.Env)
	if err != nil {
		sendShellErr(uc, err)
		return
	}
	u, err := userLookupIDs(req.Tenant)
	if err != nil {
		sendShellErr(uc, err)
		return
	}
	cwd := req.Cwd
	if cwd == "" {
		cwd = "/home/" + req.Tenant + "/Workspace"
	}

	ptmx, tty, err := pty.Open()
	if err != nil {
		sendShellErr(uc, fmt.Errorf("openpty: %w", err))
		return
	}
	defer tty.Close()

	cmd := exec.Command(req.Shell)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = tty, tty, tty
	cmd.Dir = cwd
	// Force a clean, tenant-scoped env. HOME/USER/LOGNAME match the dropped uid.
	base := []string{
		"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
		"HOME=/home/" + req.Tenant,
		"USER=" + req.Tenant,
		"LOGNAME=" + req.Tenant,
		"SHELL=" + req.Shell,
	}
	cmd.Env = append(base, env...)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setsid:     true, // new session
		Setctty:    true, // controlling tty = stdin (the slave)
		Credential: &syscall.Credential{Uid: u.uid, Gid: u.gid, NoSetGroups: false},
	}

	if err := cmd.Start(); err != nil {
		ptmx.Close()
		sendShellErr(uc, fmt.Errorf("spawn tenant shell: %w", err))
		return
	}
	// Parent no longer needs the slave (the child holds it).
	_ = tty.Close()

	// Hand the master fd to the agent. After SCM_RIGHTS the agent owns its own
	// dup; we can close ours.
	if err := sendFD(uc, int(ptmx.Fd())); err != nil {
		ptmx.Close()
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
		return
	}
	ptmx.Close()

	// Reap the child when the tenant shell exits (the agent sees EOF on the
	// master and tears the session down on its side).
	go func() { _ = cmd.Wait() }()
}

func sendShellErr(uc *net.UnixConn, err error) {
	writeResp(uc, helperResponse{Error: err.Error()})
}

// sendFD writes a one-line JSON header plus the fd as SCM_RIGHTS ancillary data.
func sendFD(uc *net.UnixConn, fd int) error {
	rights := syscall.UnixRights(fd)
	_, _, err := uc.WriteMsgUnix([]byte("{\"ok\":true}\n"), rights, nil)
	return err
}

type tenantIDs struct {
	uid uint32
	gid uint32
}

func userLookupIDs(name string) (tenantIDs, error) {
	uid, ok := lookupUID(name)
	if !ok {
		return tenantIDs{}, fmt.Errorf("tenant user %q not found", name)
	}
	gid, ok := lookupGID(name)
	if !ok {
		return tenantIDs{}, fmt.Errorf("tenant group for %q not found", name)
	}
	return tenantIDs{uid: uint32(uid), gid: uint32(gid)}, nil
}
