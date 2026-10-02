package main

import (
	"os"
	"strings"
	"testing"
)

// The SSH adopter must never consume the target's one-shot device code from
// the controller. The remote waiter owns that poll; launch completion is gated
// by the real /health operation instead.
func TestLaunchSSHWaitsForOperationalHealthWithoutPollingDeviceCode(t *testing.T) {
	b, err := os.ReadFile("launch_ssh.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	if strings.Contains(src, "pollDeviceForOnline(ctx, dc)") {
		t.Fatal("launchSSH still races the remote waiter for the one-shot auth token")
	}
	for _, want := range []string{"pollSSHAgentReady(ctx, opts.SSHTarget)", "/health", `"usable"`} {
		if !strings.Contains(src, want) {
			t.Fatalf("launchSSH operational guard missing %q", want)
		}
	}
}
