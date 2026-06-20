//go:build windows

package main

import (
	"fmt"
	"os"
)

// helperTenantShellFD is a no-op on Windows: the privilege-separated helper and
// its operator-fleet tenant model are Linux-only.
func helperTenantShellFD(tenant, shell string, env []string, cwd string) (*os.File, error) {
	return nil, fmt.Errorf("tenant_shell helper is not supported on windows")
}
