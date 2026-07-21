package main

import "strings"

import "testing"

func TestServerlessIsolation(t *testing.T) {
	// Fail closed on a missing tenant key — an unnamed tenant would collide
	// with every other unnamed tenant.
	if _, err := ServerlessSandboxArgs(ServerlessIsolationSpec{}); err == nil {
		t.Fatal("empty tenant key must be rejected")
	}
	// Reject shell-hostile keys before they reach a container name.
	if _, err := ServerlessSandboxArgs(DefaultServerlessIsolation("a b;rm -rf /")); err == nil {
		t.Fatal("unsafe tenant key must be rejected")
	}
	args, err := ServerlessSandboxArgs(DefaultServerlessIsolation("acme"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	joined := strings.Join(args, " ")
	for _, must := range []string{
		"--cap-drop ALL", "--security-opt no-new-privileges",
		"--memory 512m", "--pids-limit 256", "--read-only",
		"yaver-fn-acme",
	} {
		if !strings.Contains(joined, must) {
			t.Fatalf("missing control %q in: %s", must, joined)
		}
	}
	// Shared-kernel runtimes must NEVER report ready for untrusted tenants.
	for _, rt := range []string{"docker", "podman", "containerd", "", "weird"} {
		if ok, _ := ServerlessIsolationReadyForUntrustedTenants(rt); ok {
			t.Fatalf("runtime %q must not be ready for untrusted tenants", rt)
		}
	}
	if ok, _ := ServerlessIsolationReadyForUntrustedTenants("firecracker"); !ok {
		t.Fatal("firecracker should be ready")
	}
	// Metadata + all RFC1918 ranges must be blocked on a shared host.
	pol := strings.Join(ServerlessEgressPolicy(), " ")
	for _, cidr := range []string{"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "169.254.0.0/16"} {
		if !strings.Contains(pol, cidr) {
			t.Fatalf("egress policy missing %s", cidr)
		}
	}
}
