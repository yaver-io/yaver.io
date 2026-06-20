//go:build !windows

package main

// helper_client_fd_unix.go — agent-side receiver for the tenant_shell PTY fd.
// Connects to the root helper, sends the request, and receives the PTY master
// file descriptor over SCM_RIGHTS. The returned *os.File is the terminal master
// the agent reads/writes — exactly what `sudo -u` used to give us, but with no
// privilege held by the agent itself.

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"syscall"
	"time"
)

func helperTenantShellFD(tenant, shell string, env []string, cwd string) (*os.File, error) {
	conn, err := net.DialTimeout("unix", helperSocketPath(), 5*time.Second)
	if err != nil {
		return nil, fmt.Errorf("helper dial: %w", err)
	}
	defer conn.Close()
	uc, ok := conn.(*net.UnixConn)
	if !ok {
		return nil, fmt.Errorf("helper socket is not unix")
	}
	_ = uc.SetDeadline(time.Now().Add(30 * time.Second))

	req := helperRequest{Verb: "tenant_shell", Tenant: tenant, Shell: shell, Env: env, Cwd: cwd}
	b, _ := json.Marshal(req)
	if _, err := uc.Write(append(b, '\n')); err != nil {
		return nil, fmt.Errorf("helper write: %w", err)
	}

	buf := make([]byte, 4096)
	oob := make([]byte, syscall.CmsgSpace(4)) // exactly one fd
	n, oobn, _, _, err := uc.ReadMsgUnix(buf, oob)
	if err != nil {
		return nil, fmt.Errorf("helper read: %w", err)
	}

	scms, err := syscall.ParseSocketControlMessage(oob[:oobn])
	if err != nil || len(scms) == 0 {
		// No fd → the helper returned an error response as JSON.
		var r helperResponse
		if jerr := json.Unmarshal(buf[:n], &r); jerr == nil && r.Error != "" {
			return nil, fmt.Errorf("helper: %s", r.Error)
		}
		return nil, fmt.Errorf("helper returned no fd")
	}
	fds, err := syscall.ParseUnixRights(&scms[0])
	if err != nil || len(fds) == 0 {
		return nil, fmt.Errorf("helper sent no usable fd: %v", err)
	}
	// Defensive: drop any extra fds we didn't expect.
	for _, extra := range fds[1:] {
		_ = syscall.Close(extra)
	}
	fd := fds[0]
	// Make it pollable by the Go runtime, then wrap as the PTY master.
	_ = syscall.SetNonblock(fd, true)
	return os.NewFile(uintptr(fd), "tenant-pty"), nil
}
