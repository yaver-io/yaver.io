package main

import (
	"strings"
	"testing"
)

func TestParseUserHostPort(t *testing.T) {
	cases := []struct {
		in               string
		user, host       string
		port             int
	}{
		{"kivi@10.0.0.45", "kivi", "10.0.0.45", 0},
		{"kivi@10.0.0.45:2222", "kivi", "10.0.0.45", 2222},
		{"10.0.0.45", "", "10.0.0.45", 0},
		{"host:22", "", "host", 22},
		{"root@box", "root", "box", 0},
	}
	for _, c := range cases {
		u, h, p := parseUserHostPort(c.in)
		if u != c.user || h != c.host || p != c.port {
			t.Errorf("%q → (%q,%q,%d), want (%q,%q,%d)", c.in, u, h, p, c.user, c.host, c.port)
		}
	}
}

func TestLookupUpsertSSHTarget(t *testing.T) {
	cfg := &Config{}
	upsertSSHTarget(cfg, SSHTarget{Name: "magara", Host: "10.0.0.45", User: "kivi"})
	if t1 := lookupSSHTarget(cfg, "MAGARA"); t1 == nil || t1.User != "kivi" {
		t.Fatalf("case-insensitive lookup failed: %+v", t1)
	}
	// Upsert replaces, doesn't duplicate.
	upsertSSHTarget(cfg, SSHTarget{Name: "magara", Host: "10.0.0.99", User: "kivi"})
	if len(cfg.SSHTargets) != 1 {
		t.Fatalf("upsert duplicated: %d entries", len(cfg.SSHTargets))
	}
	if cfg.SSHTargets[0].Host != "10.0.0.99" {
		t.Fatalf("upsert didn't replace host: %q", cfg.SSHTargets[0].Host)
	}
}

func TestSSHArgsForBuildsIdentityPortUserHost(t *testing.T) {
	tg := &SSHTarget{Name: "magara", Host: "10.0.0.45", User: "kivi", Port: 2222, IdentityFile: "/k/id"}
	argv := sshArgsFor(tg, "/usr/bin/ssh", []string{"-L", "5432:localhost:5432"})
	got := strings.Join(argv, " ")
	for _, want := range []string{"/usr/bin/ssh", "-i /k/id", "-p 2222", "kivi@10.0.0.45", "-L 5432:localhost:5432"} {
		if !strings.Contains(got, want) {
			t.Errorf("argv missing %q: %s", want, got)
		}
	}
	// user@host must come before the passthrough.
	if strings.Index(got, "kivi@10.0.0.45") > strings.Index(got, "-L") {
		t.Errorf("dest must precede passthrough: %s", got)
	}
}

func TestRememberSSHHostNoClobber(t *testing.T) {
	cfg := &Config{}
	upsertSSHTarget(cfg, SSHTarget{Name: "magara", Host: "10.0.0.45", User: "kivi"})
	rememberSSHHost(cfg, "magara", "10.0.0.99") // should NOT clobber existing host/user
	if cfg.SSHTargets[0].Host != "10.0.0.45" || cfg.SSHTargets[0].User != "kivi" {
		t.Fatalf("rememberSSHHost clobbered an existing entry: %+v", cfg.SSHTargets[0])
	}
	rememberSSHHost(cfg, "newbox", "1.2.3.4") // new entry, host only
	if lookupSSHTarget(cfg, "newbox") == nil {
		t.Fatalf("rememberSSHHost didn't add new entry")
	}
}
