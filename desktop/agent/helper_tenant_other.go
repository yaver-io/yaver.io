//go:build !linux

package main

import "net"

// serveTenantShell is Linux-only (setuid + controlling-tty + SCM_RIGHTS). On
// other platforms the helper refuses the verb; operator nodes are Linux.
func (s *helperServer) serveTenantShell(uc *net.UnixConn, req helperRequest) {
	writeResp(uc, helperResponse{Error: "tenant_shell is Linux-only"})
}
