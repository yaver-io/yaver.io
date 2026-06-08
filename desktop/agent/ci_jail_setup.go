package main

// ci_jail_setup.go — the HOST-LEVEL half of the CI network jail (operator-fleet
// gap C). ci_jail.go runs each CI container ON the jail network when one is
// configured; this file CREATES that network + the egress firewall.
//
// Policy: a CI job (potentially arbitrary fork-PR code on an operator's home
// box) may reach the PUBLIC internet (it needs github/npm/pypi to build) but
// must NOT reach RFC1918 / link-local / CGNAT private space — i.e. the
// operator's LAN: routers, NAS, printers, other machines. Mechanism: a
// dedicated docker bridge network + DOCKER-USER iptables rules that DROP
// forwarded packets from the jail subnet to private ranges. Public-internet
// traffic (dest = a public IP, SNAT'd out) is unaffected; same-subnet delivery
// to the gateway (for NAT) is L2, not in the FORWARD chain.
//
// Linux-only (the operator fleet is Linux PCs/NUCs/Pis). On macOS Docker
// Desktop the network is created but host iptables don't gate the VM's bridge,
// so the firewall step reports unsupported — don't rely on the jail there.

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

const (
	ciJailNetworkName   = "yaver-ci-jail"
	ciJailDefaultSubnet = "10.201.0.0/24"
)

// ciJailRFC1918Ranges are the private / link-local / CGNAT destination ranges a
// jailed CI job must not reach. Pure — unit-tested.
func ciJailRFC1918Ranges() []string {
	return []string{
		"10.0.0.0/8",
		"172.16.0.0/12",
		"192.168.0.0/16",
		"169.254.0.0/16", // link-local
		"100.64.0.0/10",  // carrier-grade NAT
	}
}

// ciJailNetworkCreateArgs builds the `docker network create` arg list for the
// jail bridge. Pure — unit-tested.
func ciJailNetworkCreateArgs(name, subnet string) []string {
	return []string{
		"network", "create",
		"--driver", "bridge",
		"--subnet", subnet,
		"--opt", "com.docker.network.bridge.name=yaverci0",
		name,
	}
}

// ciJailIptablesRuleSpecs returns the DOCKER-USER rule bodies (without the
// -I/-C/-A verb or chain) that DROP jail-subnet → private-range egress. Pure —
// unit-tested.
func ciJailIptablesRuleSpecs(subnet string) [][]string {
	var specs [][]string
	for _, r := range ciJailRFC1918Ranges() {
		specs = append(specs, []string{"-s", subnet, "-d", r, "-j", "DROP"})
	}
	return specs
}

func ciJailMarkerPath() string {
	dir, err := ConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "runner", "ci-jail.txt")
}

// setCIJailMarker persists the jail network name so container runs use it
// across restarts (read back by ciJailNetwork in ci_jail.go).
func setCIJailMarker(name string) {
	p := ciJailMarkerPath()
	if p == "" {
		return
	}
	_ = os.MkdirAll(filepath.Dir(p), 0700)
	_ = os.WriteFile(p, []byte(strings.TrimSpace(name)+"\n"), 0600)
}

func clearCIJailMarker() {
	if p := ciJailMarkerPath(); p != "" {
		_ = os.Remove(p)
	}
}

func readCIJailMarker() string {
	p := ciJailMarkerPath()
	if p == "" {
		return ""
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// ensureCIJailNetwork creates the jail bridge if absent and returns its subnet
// (parsed from an existing network, or the default for a fresh one).
func ensureCIJailNetwork(ctx context.Context) (subnet string, created bool, err error) {
	docker, err := exec.LookPath("docker")
	if err != nil {
		return "", false, fmt.Errorf("docker not found: %w", err)
	}
	// Already exists? Read its subnet.
	inspect := exec.CommandContext(ctx, docker, "network", "inspect", ciJailNetworkName,
		"--format", "{{range .IPAM.Config}}{{.Subnet}}{{end}}")
	if out, e := inspect.Output(); e == nil {
		if s := strings.TrimSpace(string(out)); s != "" {
			return s, false, nil
		}
	}
	// Create it.
	createCmd := exec.CommandContext(ctx, docker, ciJailNetworkCreateArgs(ciJailNetworkName, ciJailDefaultSubnet)...)
	if out, e := createCmd.CombinedOutput(); e != nil {
		return "", false, fmt.Errorf("create jail network: %v: %s", e, strings.TrimSpace(string(out)))
	}
	return ciJailDefaultSubnet, true, nil
}

// removeCIJailNetwork removes the jail bridge (best-effort).
func removeCIJailNetwork(ctx context.Context) error {
	docker, err := exec.LookPath("docker")
	if err != nil {
		return err
	}
	out, e := exec.CommandContext(ctx, docker, "network", "rm", ciJailNetworkName).CombinedOutput()
	if e != nil && !strings.Contains(string(out), "not found") {
		return fmt.Errorf("%v: %s", e, strings.TrimSpace(string(out)))
	}
	return nil
}

// applyCIJailFirewall installs the DOCKER-USER DROP rules (idempotent: -C check
// before -I insert). Linux-only.
func applyCIJailFirewall(ctx context.Context, subnet string) error {
	if runtime.GOOS != "linux" {
		return fmt.Errorf("egress firewall is linux-only (this host is %s); the jail network blocks nothing here — run on a linux operator box", runtime.GOOS)
	}
	ipt, err := exec.LookPath("iptables")
	if err != nil {
		return fmt.Errorf("iptables not found: %w", err)
	}
	for _, spec := range ciJailIptablesRuleSpecs(subnet) {
		check := append([]string{"-C", "DOCKER-USER"}, spec...)
		if exec.CommandContext(ctx, ipt, check...).Run() == nil {
			continue // rule already present
		}
		insert := append([]string{"-I", "DOCKER-USER"}, spec...)
		if out, e := exec.CommandContext(ctx, ipt, insert...).CombinedOutput(); e != nil {
			return fmt.Errorf("install rule %v: %v: %s (need root + the DOCKER-USER chain — start docker first)", spec, e, strings.TrimSpace(string(out)))
		}
	}
	return nil
}

// removeCIJailFirewall deletes the DROP rules (best-effort, linux-only).
func removeCIJailFirewall(ctx context.Context, subnet string) error {
	if runtime.GOOS != "linux" {
		return nil
	}
	ipt, err := exec.LookPath("iptables")
	if err != nil {
		return err
	}
	for _, spec := range ciJailIptablesRuleSpecs(subnet) {
		del := append([]string{"-D", "DOCKER-USER"}, spec...)
		_ = exec.CommandContext(ctx, ipt, del...).Run()
	}
	return nil
}

// ciJailFirewallActive reports whether all DROP rules are present (linux-only).
func ciJailFirewallActive(ctx context.Context, subnet string) bool {
	if runtime.GOOS != "linux" {
		return false
	}
	ipt, err := exec.LookPath("iptables")
	if err != nil {
		return false
	}
	for _, spec := range ciJailIptablesRuleSpecs(subnet) {
		check := append([]string{"-C", "DOCKER-USER"}, spec...)
		if exec.CommandContext(ctx, ipt, check...).Run() != nil {
			return false
		}
	}
	return true
}

// setupCIJail is the operator-fleet entry point: create the network, apply the
// firewall, and persist the marker so container CI runs use it.
func setupCIJail(ctx context.Context) (map[string]interface{}, error) {
	subnet, created, err := ensureCIJailNetwork(ctx)
	if err != nil {
		return nil, err
	}
	fwErr := applyCIJailFirewall(ctx, subnet)
	setCIJailMarker(ciJailNetworkName)
	res := map[string]interface{}{
		"network":        ciJailNetworkName,
		"subnet":         subnet,
		"created":        created,
		"firewallActive": ciJailFirewallActive(ctx, subnet),
	}
	if fwErr != nil {
		res["firewallWarning"] = fwErr.Error()
	}
	return res, nil
}

// teardownCIJail removes the firewall + network + marker.
func teardownCIJail(ctx context.Context) error {
	subnet, _, _ := ensureCIJailNetwork(ctx) // resolve subnet for rule deletion
	_ = removeCIJailFirewall(ctx, subnet)
	clearCIJailMarker()
	return removeCIJailNetwork(ctx)
}
