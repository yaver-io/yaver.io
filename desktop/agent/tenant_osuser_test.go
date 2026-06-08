package main

import (
	"strings"
	"testing"
)

func TestTenantShellArgv(t *testing.T) {
	argv := tenantShellArgv("yv-abc123", "/bin/bash",
		[]string{"OPENAI_BASE_URL=https://gw/v1", "OPENAI_API_KEY=ygw_x", "TERM=xterm-256color"})
	want := []string{
		"sudo", "-n", "-u", "yv-abc123", "-H", "env",
		"OPENAI_BASE_URL=https://gw/v1", "OPENAI_API_KEY=ygw_x", "TERM=xterm-256color",
		"/bin/bash",
	}
	if strings.Join(argv, " ") != strings.Join(want, " ") {
		t.Fatalf("argv mismatch:\n got %v\nwant %v", argv, want)
	}
}

func TestTenantShellArgvDefaultShell(t *testing.T) {
	argv := tenantShellArgv("yv-x", "", nil)
	if argv[len(argv)-1] != "/bin/bash" {
		t.Fatalf("expected default /bin/bash, got %q", argv[len(argv)-1])
	}
	// Must always launch via sudo -u <user> (drop privileges to the tenant).
	if argv[0] != "sudo" || argv[2] != "-u" || argv[3] != "yv-x" {
		t.Fatalf("must sudo -u the tenant user: %v", argv)
	}
}
