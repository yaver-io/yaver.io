package main

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestTenantRuntimeSeparatesClaudeAndCodexHomes(t *testing.T) {
	alice := tenantRuntimeForGuest("Alice Example")
	bob := tenantRuntimeForGuest("Bob Example")
	if !alice.Enabled || !bob.Enabled {
		t.Fatal("expected tenant runtimes")
	}
	if alice.Root == bob.Root || alice.Home == bob.Home || alice.User == bob.User {
		t.Fatalf("tenant runtimes collided: alice=%+v bob=%+v", alice, bob)
	}
	env := strings.Join(alice.authEnv(), "\n")
	for _, want := range []string{
		"HOME=" + alice.Home,
		"CLAUDE_CONFIG_DIR=" + filepath.Join(alice.Home, ".claude"),
		"CODEX_HOME=" + filepath.Join(alice.Home, ".codex"),
		"GIT_TERMINAL_PROMPT=0",
		"GIT_ASKPASS=/bin/true",
	} {
		if !strings.Contains(env, want) {
			t.Fatalf("tenant env missing %q in:\n%s", want, env)
		}
	}
	if strings.Contains(env, bob.Home) {
		t.Fatalf("alice env references bob home:\n%s", env)
	}
}

func TestTenantRuntimeForTaskIsolatedGuests(t *testing.T) {
	cases := []struct {
		name   string
		task   *Task
		expect bool
	}{
		{name: "owner", task: &Task{RunnerID: "codex"}, expect: false},
		{name: "guest no isolation", task: &Task{GuestUserID: "u1", RunnerID: "codex"}, expect: false},
		// Beta is opencode-only: an isolation-required opencode guest MUST be
		// confined as a tenant (previously a hole — it ran unconfined).
		{name: "guest opencode isolated", task: &Task{GuestUserID: "u1", GuestRequireIsolation: true, RunnerID: "opencode"}, expect: true},
		{name: "guest codex", task: &Task{GuestUserID: "u1", GuestRequireIsolation: true, RunnerID: "codex"}, expect: true},
		{name: "guest claude-code alias", task: &Task{GuestUserID: "u1", GuestRequireIsolation: true, RunnerID: "claude-code"}, expect: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tenantRuntimeForTask(tc.task).Enabled
			if got != tc.expect {
				t.Fatalf("tenantRuntimeForTask enabled=%v, want %v", got, tc.expect)
			}
		})
	}
}
