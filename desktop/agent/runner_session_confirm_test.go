package main

import (
	"os/exec"
	"strings"
	"testing"
	"time"
)

// sessionExists reports whether a tmux session of this name is already live, so
// the test never touches a real one belonging to the person running it.
func sessionExists(name string) bool {
	return exec.Command("tmux", "has-session", "-t", "="+name).Run() == nil
}

// A tmux session named `yaver-codex` whose runner has exited is a plain shell.
// The old classifier called it a codex session on the strength of the name alone,
// and a "prompt" sent to it was typed into zsh and submitted — i.e. executed.
// Refuse the turn, and do not run anything.
func TestTurnRefusesSessionThatIsOnlyAShell(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	// Must be exactly `yaver-<supported runner>`: that is the name-prefix rule
	// that misclassifies, and the whole point is to exercise it. Anything else
	// (e.g. "yaver-codex-shelltest") is not classified at all and the test would
	// pass without ever touching the dangerous path.
	const name = "yaver-codex"
	if sessionExists(name) {
		t.Skipf("a real %q session is running on this machine; refusing to touch it", name)
	}

	// A session whose name carries a runner id but which is running nothing but a
	// shell — exactly what is left behind when a runner exits and tmux persists.
	if out, err := exec.Command("tmux", "new-session", "-d", "-s", name).CombinedOutput(); err != nil {
		t.Skipf("cannot create tmux session: %v (%s)", err, out)
	}
	defer exec.Command("tmux", "kill-session", "-t", name).Run()

	// It must still be LISTED — it is the user's session and a picker should show
	// it — but never marked Confirmed.
	var found *RunnerPTYSession
	for _, s := range listRunnerPTYSessions() {
		if s.Name == name {
			sess := s
			found = &sess
		}
	}
	if found == nil {
		t.Fatalf("a %q session was not classified at all — this test is no longer exercising the name-prefix path it exists to guard", name)
	}
	if found.Confirmed {
		t.Fatalf("a bare shell was reported as a confirmed %q runner — a prompt would be executed as a command", found.Runner)
	}

	// The turn must be refused rather than typed.
	resp, status := executeRunnerSessionTurn(runnerSessionTurnRequest{
		Session: name,
		Text:    "yaver-shell-injection-canary",
		WaitMs:  500,
	})
	if status != 409 {
		t.Fatalf("expected 409 refusal for an unconfirmed session, got %d (%+v)", status, resp)
	}
	if !strings.Contains(resp.Error, "shell") {
		t.Fatalf("refusal should explain the shell hazard, got %q", resp.Error)
	}

	// And crucially: nothing was executed. If the text had been sent, zsh would
	// have run it and the pane would show the command and its "not found" error.
	time.Sleep(400 * time.Millisecond)
	pane, err := exec.Command("tmux", "capture-pane", "-p", "-t", name).CombinedOutput()
	if err != nil {
		t.Fatalf("capture-pane: %v", err)
	}
	if strings.Contains(string(pane), "command not found") {
		t.Fatalf("the prompt was EXECUTED in the shell; pane:\n%s", pane)
	}
}
