package main

import (
	"context"
	"os/exec"
	"strings"
	"testing"
)

func TestCIJailRFC1918Ranges(t *testing.T) {
	ranges := ciJailRFC1918Ranges()
	for _, want := range []string{"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "169.254.0.0/16", "100.64.0.0/10"} {
		found := false
		for _, r := range ranges {
			if r == want {
				found = true
			}
		}
		if !found {
			t.Errorf("missing range %q in %v", want, ranges)
		}
	}
}

func TestCIJailNetworkCreateArgs(t *testing.T) {
	args := strings.Join(ciJailNetworkCreateArgs("yaver-ci-jail", "10.201.0.0/24"), " ")
	for _, want := range []string{"network create", "--driver bridge", "--subnet 10.201.0.0/24", "yaver-ci-jail"} {
		if !strings.Contains(args, want) {
			t.Errorf("create args missing %q: %s", want, args)
		}
	}
}

func TestCIJailIptablesRuleSpecs(t *testing.T) {
	specs := ciJailIptablesRuleSpecs("10.201.0.0/24")
	if len(specs) != len(ciJailRFC1918Ranges()) {
		t.Fatalf("expected one rule per range, got %d", len(specs))
	}
	for _, s := range specs {
		j := strings.Join(s, " ")
		if !strings.HasPrefix(j, "-s 10.201.0.0/24 -d ") || !strings.HasSuffix(j, "-j DROP") {
			t.Errorf("malformed rule spec: %q", j)
		}
	}
}

func TestCIJailMarkerRoundtrip(t *testing.T) {
	// Must not leave a marker behind — a phantom marker would make real
	// container runs try to join a non-existent network.
	defer clearCIJailMarker()
	if got := readCIJailMarker(); got != "" {
		t.Skipf("a jail marker already exists (%q) — skipping to avoid clobbering it", got)
	}
	setCIJailMarker("yaver-ci-jail")
	if got := readCIJailMarker(); got != "yaver-ci-jail" {
		t.Errorf("marker readback = %q", got)
	}
	// ciJailNetwork() picks it up (env takes precedence; unset here).
	t.Setenv("YAVER_CI_JAIL_NETWORK", "")
	if got := ciJailNetwork(); got != "yaver-ci-jail" {
		t.Errorf("ciJailNetwork() should read marker, got %q", got)
	}
	clearCIJailMarker()
	if got := readCIJailMarker(); got != "" {
		t.Errorf("marker not cleared: %q", got)
	}
}

// TestCIJailNetworkLifecycleLive exercises the real docker network create /
// inspect / remove on this host's docker. Skips cleanly without docker.
func TestCIJailNetworkLifecycleLive(t *testing.T) {
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker not installed")
	}
	ctx := context.Background()
	if exec.CommandContext(ctx, "docker", "info").Run() != nil {
		t.Skip("docker not running")
	}
	_ = removeCIJailNetwork(ctx) // clean slate
	defer removeCIJailNetwork(ctx)

	subnet, created, err := ensureCIJailNetwork(ctx)
	if err != nil {
		t.Fatalf("ensure (create): %v", err)
	}
	if !created {
		t.Errorf("expected created=true on a fresh network")
	}
	if subnet == "" {
		t.Errorf("no subnet returned")
	}

	// Idempotent: a second call finds the existing network, same subnet.
	subnet2, created2, err := ensureCIJailNetwork(ctx)
	if err != nil {
		t.Fatalf("ensure (existing): %v", err)
	}
	if created2 {
		t.Errorf("second ensure should not re-create")
	}
	if subnet2 != subnet {
		t.Errorf("subnet changed: %s → %s", subnet, subnet2)
	}

	if err := removeCIJailNetwork(ctx); err != nil {
		t.Errorf("remove: %v", err)
	}
}
