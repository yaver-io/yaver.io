package main

import (
	"strings"
	"testing"
)

func argVal(args []string, flag string) string {
	for i, a := range args {
		if a == flag && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

func hasArg(args []string, v string) bool {
	for _, a := range args {
		if a == v {
			return true
		}
	}
	return false
}

func TestSanitizeTenant(t *testing.T) {
	cases := map[string]string{
		"User@Beta.io":          "user-beta-io",
		"  spaced  ":            "spaced",
		"":                      "anon",
		"a/b/../c":              "a-b-c",
		strings.Repeat("x", 80): strings.Repeat("x", 40),
	}
	for in, want := range cases {
		if got := sanitizeTenant(in); got != want {
			t.Errorf("sanitizeTenant(%q)=%q want %q", in, got, want)
		}
	}
}

func TestRedroidTenantIsolation(t *testing.T) {
	a := RedroidTenantSpec{TenantID: "alice", BaseDir: "/opt/yaver/redroid-tenants"}
	b := RedroidTenantSpec{TenantID: "bob", BaseDir: "/opt/yaver/redroid-tenants"}

	// Per-tenant volume dirs must differ and live under BaseDir.
	if a.VolumeDir() == b.VolumeDir() {
		t.Fatal("two tenants share a volume dir — data-leak risk")
	}
	if !strings.HasPrefix(a.VolumeDir(), "/opt/yaver/redroid-tenants/") {
		t.Fatalf("volume not under BaseDir: %s", a.VolumeDir())
	}

	args := a.RunArgs()
	// caps present
	if argVal(args, "--memory") == "" || argVal(args, "--cpus") == "" || argVal(args, "--pids-limit") == "" {
		t.Errorf("missing resource caps: %v", args)
	}
	// dedicated per-tenant network, distinct from bob's
	an, bn := argVal(args, "--network"), argVal(b.RunArgs(), "--network")
	if an == "" || an == bn {
		t.Errorf("tenants must be on distinct networks, got %q vs %q", an, bn)
	}
	// per-tenant data mount, no shared /data
	if !hasArg(args, a.VolumeDir()+":/data") {
		t.Errorf("expected per-tenant -v %s:/data, args=%v", a.VolumeDir(), args)
	}
	// no-new-privileges set even though privileged is required for binderfs
	if !hasArg(args, "no-new-privileges") || !hasArg(args, "--privileged") {
		t.Errorf("expected hardening flags + privileged, args=%v", args)
	}
	// labelled for sweeping
	if !hasArg(args, "managed-by=yaver-beta") {
		t.Errorf("expected managed-by label, args=%v", args)
	}

	// Teardown must wipe the volume (no residue) and remove container+network.
	td := strings.Join(a.TeardownCmds(), "\n")
	for _, want := range []string{"docker rm -f", "docker network rm", "rm -rf", a.VolumeDir()} {
		if !strings.Contains(td, want) {
			t.Errorf("teardown missing %q:\n%s", want, td)
		}
	}
	// Teardown rm is guarded by a case-glob to BaseDir (never wipe outside it).
	if !strings.Contains(td, "/*) rm -rf") || !strings.Contains(td, "case ") {
		t.Errorf("teardown rm not guarded to BaseDir:\n%s", td)
	}
}

func TestRedroidTenantNetworkCreateIdempotent(t *testing.T) {
	s := RedroidTenantSpec{TenantID: "carol"}
	cmd := s.NetworkCreateCmd()
	if !strings.Contains(cmd, "network inspect") || !strings.Contains(cmd, "enable_icc=false") {
		t.Errorf("network create should be idempotent + icc-disabled: %s", cmd)
	}
}
