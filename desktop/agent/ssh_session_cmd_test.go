package main

import "testing"

// The forced-command whitelist IS the security boundary of the out-of-band SSH
// channel. These tests pin the contract: only the intended verbs are reachable,
// everything else is refused (never a shell, never an arbitrary path), and a
// task-scoped verb can never be turned into path traversal.

func TestSSHSessionRoute_AllowsIntendedVerbs(t *testing.T) {
	want := map[string]struct {
		method     string
		path       string
		taskScoped bool
	}{
		"health":           {"GET", "/health", false},
		"status":           {"GET", "/agent/status", false},
		"info":             {"GET", "/info", false},
		"list-projects":    {"GET", "/projects/mobile", false},
		"list-tasks":       {"GET", "/tasks", false},
		"doctor-transport": {"GET", "/doctor/transport", false},
		"repair-relay":     {"POST", "/settings/repair-relay", false},
		"run-task":         {"POST", "/tasks", false},
		"continue-task":    {"POST", "/tasks/{id}/continue", true},
		"stop-task":        {"POST", "/tasks/{id}/stop", true},
	}
	for verb, exp := range want {
		method, path, taskScoped, ok := sshSessionRoute(verb)
		if !ok {
			t.Errorf("verb %q should be allowed", verb)
			continue
		}
		if method != exp.method || path != exp.path || taskScoped != exp.taskScoped {
			t.Errorf("verb %q = (%s %s scoped=%v), want (%s %s scoped=%v)",
				verb, method, path, taskScoped, exp.method, exp.path, exp.taskScoped)
		}
	}
}

func TestSSHSessionRoute_RefusesEverythingElse(t *testing.T) {
	// A shell, arbitrary commands, path traversal, and near-miss verbs must all
	// be refused — the channel is a cage, not a shell.
	for _, verb := range []string{
		"", "  ", "bash", "sh", "/bin/sh", "exec", "shell",
		"GET /etc/passwd", "run-task; rm -rf /", "../../secret",
		"tasks", "task", "health ", "HEALTH", "delete-task", "vault",
		"open-port", "attach-tmux", // not yet whitelisted → must be refused, not silently allowed
	} {
		if _, _, _, ok := sshSessionRoute(verb); ok {
			t.Errorf("verb %q must be REFUSED by the forced-command whitelist", verb)
		}
	}
}

func TestIsSafeTaskID(t *testing.T) {
	good := []string{"task_abc123", "abc-DEF-0", "t.1", "A", "0123456789"}
	for _, id := range good {
		if !isSafeTaskID(id) {
			t.Errorf("taskId %q should be accepted", id)
		}
	}
	bad := []string{
		"", "../etc", "a/b", "a b", "a\tb", "a\nb", "a;b", "a&b",
		"..", "x/..", "$(id)", "`id`", "a|b", "a%2Fb",
	}
	for _, id := range bad {
		if isSafeTaskID(id) {
			t.Errorf("taskId %q should be REJECTED (traversal/injection surface)", id)
		}
	}
	// Overlong ids are rejected.
	long := make([]byte, 200)
	for i := range long {
		long[i] = 'a'
	}
	if isSafeTaskID(string(long)) {
		t.Error("overlong taskId should be rejected")
	}
}
