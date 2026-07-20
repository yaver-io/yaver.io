package main

import "testing"

// When YAVER_TMUX_RUNNER is unset, ensureTmuxRunnerSession is a no-op and
// returns "" — a normal desktop daemon must not spawn a tmux session.
func TestEnsureTmuxRunnerSession_OffByDefault(t *testing.T) {
	t.Setenv("YAVER_TMUX_RUNNER", "")
	if got := ensureTmuxRunnerSession(); got != "" {
		t.Fatalf("expected no session when feature off, got %q", got)
	}
}
