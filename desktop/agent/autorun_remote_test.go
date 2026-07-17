package main

// autorun_remote_test.go — the remote resolver. Swaps autorunExec to answer
// git without a real repo, matching TestRollbackAutorunChangesUsesDiagnosticStash.

import (
	"context"
	"strings"
	"testing"
)

// fakeGit answers `git remote`, `git rev-parse --abbrev-ref HEAD`, and
// `git config --get branch.<b>.remote` from a small table.
func fakeGit(t *testing.T, remotes, branch, branchRemote string) func() {
	t.Helper()
	original := autorunExec
	autorunExec = func(_ context.Context, name string, args []string, _ string) autorunCommandResult {
		if name != "git" || len(args) == 0 {
			return autorunCommandResult{Err: errFakeGit}
		}
		switch {
		case args[0] == "remote" && len(args) == 1:
			if remotes == "" {
				return autorunCommandResult{}
			}
			return autorunCommandResult{Output: remotes}
		case args[0] == "rev-parse":
			if branch == "" {
				return autorunCommandResult{Err: errFakeGit}
			}
			return autorunCommandResult{Output: branch + "\n"}
		case args[0] == "config":
			if branchRemote == "" {
				return autorunCommandResult{Err: errFakeGit}
			}
			return autorunCommandResult{Output: branchRemote + "\n"}
		}
		return autorunCommandResult{Err: errFakeGit}
	}
	return func() { autorunExec = original }
}

type fakeGitErr struct{}

func (fakeGitErr) Error() string { return "fake git failure" }

var errFakeGit = fakeGitErr{}

func TestAutorunRemoteFor(t *testing.T) {
	cases := []struct {
		name         string
		remotes      string
		branch       string
		branchRemote string
		want         string
		wantErr      string
	}{
		// The regression this file exists for. CLAUDE.md: "Only one remote here
		// — `github`". Autorun assumed origin and died at iteration 0 with
		// "'origin' does not appear to be a git repository".
		{name: "single github remote (this repo's convention)", remotes: "github\n", branch: "main", want: "github"},
		{name: "origin still wins when present", remotes: "github\norigin\n", branch: "main", want: "origin"},
		// branch.<name>.remote is what plain `git push` honors, so it must beat
		// the origin default — otherwise autorun pushes somewhere the human
		// would not.
		{name: "branch config beats origin", remotes: "github\norigin\n", branch: "main", branchRemote: "github", want: "github"},
		{name: "detached HEAD falls back to origin", remotes: "github\norigin\n", want: "origin"},
		{name: "sole remote used when detached", remotes: "upstream\n", want: "upstream"},
		{name: "no remotes errors", remotes: "", wantErr: "no git remote"},
		// Guessing here would push a run's work to the wrong place.
		{name: "ambiguous errors rather than guessing", remotes: "alpha\nbeta\n", branch: "main", wantErr: "cannot tell which remote"},
		// A branch pointing at a remote that no longer exists must not be
		// trusted just because the config says so.
		{name: "stale branch config ignored", remotes: "github\n", branch: "main", branchRemote: "gone", want: "github"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			defer fakeGit(t, tc.remotes, tc.branch, tc.branchRemote)()
			got, err := autorunRemoteFor(context.Background(), "/repo")
			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("expected an error containing %q, got %q", tc.wantErr, got)
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("error = %v, want it to contain %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("autorunRemoteFor = %q, want %q", got, tc.want)
			}
		})
	}
}

// The forgiving form must never fail a best-effort sync — it degrades to the
// old hardcoded behavior rather than crashing.
func TestAutorunRemoteOrOriginDegrades(t *testing.T) {
	defer fakeGit(t, "alpha\nbeta\n", "main", "")()
	if got := autorunRemoteOrOrigin(context.Background(), "/repo"); got != "origin" {
		t.Errorf("ambiguous remotes should degrade to origin, got %q", got)
	}
	defer fakeGit(t, "github\n", "main", "")()
	if got := autorunRemoteOrOrigin(context.Background(), "/repo"); got != "github" {
		t.Errorf("resolvable remote should be used, got %q", got)
	}
}
