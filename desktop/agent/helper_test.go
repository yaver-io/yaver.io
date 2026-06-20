package main

import (
	"strings"
	"testing"
)

// The helper runs as root and must not trust the agent. These tests pin the
// validation surface: anything outside the fixed allowlist is rejected before
// a command ever runs.

func TestPackageInstallArgv(t *testing.T) {
	ok, err := packageInstallArgv("apt", []string{"tmux", "lib_icu-dev", "foo.bar+1"})
	if err != nil {
		t.Fatalf("valid names rejected: %v", err)
	}
	if got := strings.Join(ok, " "); got != "apt install -y tmux lib_icu-dev foo.bar+1" {
		t.Errorf("unexpected argv: %s", got)
	}

	bad := [][]string{
		{"; rm -rf /"}, {"foo; reboot"}, {"--option"}, {"-t"}, {"./evil.deb"},
		{"a b"}, {"foo$(id)"}, {"foo&bar"}, {"../etc/passwd"}, {""},
	}
	for _, names := range bad {
		if _, err := packageInstallArgv("apt", names); err == nil {
			t.Errorf("dangerous package name %q was accepted", names)
		}
	}
	if _, err := packageInstallArgv("evil", []string{"tmux"}); err == nil {
		t.Errorf("unknown package manager accepted")
	}
	if _, err := packageInstallArgv("apt", nil); err == nil {
		t.Errorf("empty package list accepted")
	}
}

func TestServiceAllowed(t *testing.T) {
	op := &helperServer{profile: profileOperator}
	// Operator: yaver* + docker only.
	for _, unit := range []string{"yaver", "yaver-helper.service", "docker", "docker.service"} {
		if err := op.serviceAllowed("restart", unit); err != nil {
			t.Errorf("operator should allow %q: %v", unit, err)
		}
	}
	for _, unit := range []string{"sshd", "nginx", "cron.service", "systemd-journald"} {
		if err := op.serviceAllowed("stop", unit); err == nil {
			t.Errorf("operator must refuse %q (could disrupt the host / other tenants)", unit)
		}
	}
	// Injection / traversal in the unit name.
	for _, unit := range []string{"yaver; reboot", "../etc", "yaver /x", "yaver\t", "yaver&"} {
		if err := op.serviceAllowed("start", unit); err == nil {
			t.Errorf("malformed unit %q accepted", unit)
		}
	}
	// Bad action.
	if err := op.serviceAllowed("nuke", "yaver"); err == nil {
		t.Errorf("disallowed action accepted")
	}
	// Self-host may manage any well-formed unit.
	sh := &helperServer{profile: profileSelfHost}
	if err := sh.serviceAllowed("restart", "nginx"); err != nil {
		t.Errorf("self-host should manage arbitrary units: %v", err)
	}
}

func TestValidTenant(t *testing.T) {
	for _, ok := range []string{"yv-abc", "yv-a1b2c3", "yv-123456789012"} {
		if err := validTenant(ok); err != nil {
			t.Errorf("valid tenant %q rejected: %v", ok, err)
		}
	}
	for _, bad := range []string{"yv-", "root", "yv-ABC", "yv-../x", "yv-a b", "../yv-x", "yv-toolongusername", "admin"} {
		if err := validTenant(bad); err == nil {
			t.Errorf("invalid tenant %q accepted — could target a human account", bad)
		}
	}
}

// Dispatch wiring: a fake executor records the argv so we verify the helper
// turns a valid request into the right root command — without running it.
func TestHandleDispatch(t *testing.T) {
	var calls [][]string
	srv := &helperServer{
		profile: profileOperator,
		exec: func(name string, args ...string) (string, error) {
			calls = append(calls, append([]string{name}, args...))
			return "ok", nil
		},
	}

	if r := srv.handle(helperRequest{Verb: "package_install", Manager: "apt", Names: []string{"tmux"}}); !r.OK {
		t.Fatalf("package_install failed: %s", r.Error)
	}
	if r := srv.handle(helperRequest{Verb: "service", Action: "restart", Unit: "yaver"}); !r.OK {
		t.Fatalf("service failed: %s", r.Error)
	}
	if got := lastCall(calls); strings.Join(got, " ") != "systemctl restart yaver" {
		t.Errorf("service argv = %v", got)
	}

	// Rejected requests must NOT reach the executor.
	before := len(calls)
	if r := srv.handle(helperRequest{Verb: "service", Action: "stop", Unit: "sshd"}); r.OK {
		t.Errorf("operator stopped sshd — must be refused")
	}
	if r := srv.handle(helperRequest{Verb: "tenant_remove", Tenant: "root"}); r.OK {
		t.Errorf("tenant_remove targeted root — must be refused")
	}
	if r := srv.handle(helperRequest{Verb: "wat"}); r.OK {
		t.Errorf("unknown verb accepted")
	}
	if len(calls) != before {
		t.Errorf("a rejected request reached the executor: %v", calls[before:])
	}
}

func lastCall(calls [][]string) []string {
	if len(calls) == 0 {
		return nil
	}
	return calls[len(calls)-1]
}

// Client shims fall back cleanly when no helper socket is present.
func TestHelperUnavailableFallback(t *testing.T) {
	t.Setenv("YAVER_HELPER_SOCKET", "/nonexistent/yaver-helper-test.sock")
	if helperAvailable() {
		t.Fatalf("helperAvailable true for a missing socket")
	}
	if handled, _ := privilegedTenantCreate("yv-x"); handled {
		t.Errorf("tenant create claimed handled with no helper (should defer to sudo path)")
	}
	if handled, _ := privilegedTenantRemove("yv-x"); handled {
		t.Errorf("tenant remove claimed handled with no helper")
	}
}
