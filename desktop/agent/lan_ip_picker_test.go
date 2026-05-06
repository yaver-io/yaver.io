package main

import "testing"

// TestIsContainerBridgeInterfaceName covers every prefix the LAN-IP
// picker should skip on a Linux box with Docker / Kubernetes / etc.
// installed. Real LAN interfaces (eth0, en0, wlan0, tailscale0, ...)
// must NOT match — those are the ones we want to register as the
// device's host IP.
func TestIsContainerBridgeInterfaceName(t *testing.T) {
	skip := []string{
		"docker0",
		"docker1",
		"br-1234abcd",
		"br-",
		"virbr0",
		"virbr1-nic",
		"podman0",
		"podman1",
		"cni0",
		"cni-podman0",
		"flannel.1",
		"weave",
		"calico_eth0",
		"cali123abc",
		"vxlan.calico",
		"kube-bridge",
		"vethdeadbeef",
	}
	for _, name := range skip {
		if !isContainerBridgeInterfaceName(name) {
			t.Errorf("expected %q to be flagged as a container bridge, was not", name)
		}
	}

	keep := []string{
		"eth0",
		"eth1",
		"en0",
		"en5",
		"wlan0",
		"wlp3s0",
		"enp0s31f6",
		"tailscale0",
		"tun0",
		"utun4",
		"ens3",
	}
	for _, name := range keep {
		if isContainerBridgeInterfaceName(name) {
			t.Errorf("expected %q NOT to be flagged as a container bridge, was", name)
		}
	}

	// Edge cases: empty + whitespace must be safe (returns false), and
	// case-insensitive matching catches "DOCKER0" etc.
	if isContainerBridgeInterfaceName("") {
		t.Error("empty interface name should not match")
	}
	if !isContainerBridgeInterfaceName("DOCKER0") {
		t.Error("uppercase DOCKER0 should match (case-insensitive)")
	}
}
