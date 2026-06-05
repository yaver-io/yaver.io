package mesh

// netconfig.go — shared (platform-independent) helpers for assigning the overlay
// IP and route to the TUN interface. The OS-specific command sequences live in
// netconfig_{darwin,linux,windows,other}.go behind build tags.

import (
	"fmt"
	"os/exec"
	"strings"
)

// runCmd runs a privileged networking command and wraps failures with the
// combined output so the caller sees *why* (e.g. "permission denied").
func runCmd(name string, args ...string) error {
	out, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %s: %v: %s",
			name, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

// cidrPrefix returns the prefix length string from a CIDR ("100.96.0.0/12"->"12").
func cidrPrefix(cidr string) string {
	if i := strings.IndexByte(cidr, '/'); i >= 0 {
		return cidr[i+1:]
	}
	return "32"
}

// ConfigureNetwork assigns the overlay IP to the TUN interface and ensures the
// mesh subnet routes through it. Platform-specific; requires elevated privilege.
func (d *Device) ConfigureNetwork(selfIPv4, meshCIDR string) error {
	return configureInterface(d.name, selfIPv4, meshCIDR)
}
