//go:build linux

package main

import (
	"fmt"
	"net"
	"syscall"
)

// peerUID returns the uid of the process on the other end of a Unix socket via
// SO_PEERCRED. This is the kernel-attested caller identity — it cannot be
// spoofed by the client — so the helper uses it to admit only the yaver user
// (or root).
func peerUID(conn net.Conn) (uint32, error) {
	uc, ok := conn.(*net.UnixConn)
	if !ok {
		return 0, fmt.Errorf("not a unix socket")
	}
	raw, err := uc.SyscallConn()
	if err != nil {
		return 0, err
	}
	var cred *syscall.Ucred
	var sockErr error
	if cerr := raw.Control(func(fd uintptr) {
		cred, sockErr = syscall.GetsockoptUcred(int(fd), syscall.SOL_SOCKET, syscall.SO_PEERCRED)
	}); cerr != nil {
		return 0, cerr
	}
	if sockErr != nil {
		return 0, sockErr
	}
	return cred.Uid, nil
}
