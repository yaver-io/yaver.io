//go:build !linux

package main

import (
	"fmt"
	"net"
)

// peerUID is Linux-only (SO_PEERCRED). The privileged helper exists for Linux
// operator nodes; on other platforms it is unsupported, so the credential
// check fails closed and the helper refuses every connection.
func peerUID(conn net.Conn) (uint32, error) {
	return 0, fmt.Errorf("privileged helper is Linux-only")
}
