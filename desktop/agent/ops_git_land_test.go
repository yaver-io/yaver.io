package main

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Real git, real remote, real races — no mocks, matching the house test style.
// autorun_land_test.go asserts the landing path from source because "a
// behavioural test would need a real remote plus two racing pushes". A bare repo
// plus two clones IS that, cheaply, so these assert behaviour instead.

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com",
		"GIT_CONFIG_NOSYSTEM=1", "HOME="+t.TempDir(),
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s in %s: %v\n%s", strings.Join(args, " "), dir, err, out)
	}
	return strings.TrimSpace(string(out))
}

// landFixture builds a bare "remote" plus one clone whose remote is named
// `github` — the name this repo actually uses. A fixture that called it `origin`
// would pass while the real thing broke.
func landFixture(t *testing.T, remoteName string) (bare, clone string) {
	t.Helper()
	root := t.TempDir()
	bare = filepath.Join(root, "remote.git")
	if err := os.MkdirAll(bare, 0o755); err != nil {
		t.Fatal(err)
	}
	git(t, bare, "init", "--bare", "--initial-branch=main", ".")

	seed := filepath.Join(root, "seed")
	os.MkdirAll(seed, 0o755)
	git(t, seed, "init", "--initial-branch=main", ".")
	os.WriteFile(filepath.Join(seed, "f.txt"), []byte("base\n"), 0o644)
	git(t, seed, "add", "f.txt")
	git(t, seed, "commit", "-m", "base")
	git(t, seed, "remote", "add", remoteName, bare)
	git(t, seed, "push", remoteName, "main")

	clone = filepath.Join(root, "clone")
	git(t, root, "clone", "--origin", remoteName, bare, clone)
	git(t, clone, "config", "user.email", "t@example.com")
	git(t, clone, "config", "user.name", "t")
	return bare, clone
}

func commitFile(t *testing.T, dir, name, body string) {
	t.Helper()
	os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644)
	git(t, dir, "add", name)
	git(t, dir, "commit", "-m", "add "+name)
}

func callLand(t *testing.T, payload map[string]interface{}) OpsResult {
	t.Helper()
	b, _ := json.Marshal(payload)
	return opsGitLandHandler(OpsContext{}, b)
}

func callLandState(t *testing.T, payload map[string]interface{}) OpsResult {
	t.Helper()
	b, _ := json.Marshal(payload)
	return opsGitLandStateHandler(OpsContext{}, b)
}

// OpsResult.Initial is interface{}; every verb here fills it with a map.
func body(t *testing.T, res OpsResult) map[string]interface{} {
	t.Helper()
	m, ok := res.Initial.(map[string]interface{})
	if !ok {
		t.Fatalf("Initial is %T, want map[string]interface{}", res.Initial)
	}
	return m
}

// The bug this whole file exists for: autorunLandOntoMain hardcodes "origin",
// but this repo's only remote is named `github` (branch.main.remote=github).
func TestResolveLandRemotePrefersGitConfigOverOriginGuess(t *testing.T) {
	_, clone := landFixture(t, "github")
	ctx := context.Background()

	got, err := resolveLandRemote(ctx, clone, "", "main")
	if err != nil {
		t.Fatalf("resolveLandRemote: %v", err)
	}
	if got != "github" {
		t.Fatalf("remote = %q, want github — a clone with no 'origin' must still land", got)
	}
}

func TestResolveLandRemoteFailsLoudlyWhenNoneExists(t *testing.T) {
	_, clone := landFixture(t, "github")
	git(t, clone, "remote", "remove", "github")
	// Leave branch.main.remote pointing at a remote that no longer exists.

	_, err := resolveLandRemote(context.Background(), clone, "", "main")
	if err == nil {
		t.Fatal("want an error when no landing remote is configured")
	}
	// A caller must be told what IS configured, not just that something failed.
	if !strings.Contains(err.Error(), "no landing remote") {
		t.Fatalf("error = %q, want it to name the problem", err.Error())
	}
}

func TestGitLandPushesWorkToTheRemote(t *testing.T) {
	bare, clone := landFixture(t, "github")
	commitFile(t, clone, "a.txt", "a\n")

	res := callLand(t, map[string]interface{}{"dir": clone, "base": "main"})
	if !res.OK {
		t.Fatalf("git_land failed: %s: %s", res.Code, res.Error)
	}
	if got := body(t, res)["pushed"]; got != true {
		t.Fatalf("pushed = %v, want true", got)
	}
	// The only proof that counts: it's on the remote.
	if log := git(t, bare, "log", "--oneline", "main"); !strings.Contains(log, "add a.txt") {
		t.Fatalf("remote main missing the work:\n%s", log)
	}
}

// The race is the entire feature. Two clones commit from the same base; the
// second must rebase onto the first and still land, not fail.
func TestGitLandWinsAfterLosingARace(t *testing.T) {
	bare, first := landFixture(t, "github")

	second := filepath.Join(t.TempDir(), "clone2")
	git(t, filepath.Dir(second), "clone", "--origin", "github", bare, second)
	git(t, second, "config", "user.email", "t@example.com")
	git(t, second, "config", "user.name", "t")

	commitFile(t, first, "first.txt", "1\n")
	commitFile(t, second, "second.txt", "2\n")

	if res := callLand(t, map[string]interface{}{"dir": first, "base": "main"}); !res.OK {
		t.Fatalf("first land failed: %s: %s", res.Code, res.Error)
	}
	// `second` is now behind: its push would be rejected non-fast-forward.
	res := callLand(t, map[string]interface{}{"dir": second, "base": "main"})
	if !res.OK {
		t.Fatalf("second land failed — the rebase-and-retry did not save it: %s: %s", res.Code, res.Error)
	}

	log := git(t, bare, "log", "--oneline", "main")
	for _, want := range []string{"first.txt", "second.txt"} {
		if !strings.Contains(log, want) {
			t.Fatalf("remote main lost %s — landing must preserve both:\n%s", want, log)
		}
	}
}

func TestGitLandRefusesDirtyWorktree(t *testing.T) {
	_, clone := landFixture(t, "github")
	os.WriteFile(filepath.Join(clone, "dirty.txt"), []byte("x\n"), 0o644)
	git(t, clone, "add", "dirty.txt")

	res := callLand(t, map[string]interface{}{"dir": clone, "base": "main"})
	if res.OK {
		t.Fatal("want refusal: rebasing a dirty tree loses work")
	}
	if res.Code != "dirty_worktree" {
		t.Fatalf("code = %q, want dirty_worktree", res.Code)
	}
}

func TestGitLandDryRunTouchesNothing(t *testing.T) {
	bare, clone := landFixture(t, "github")
	commitFile(t, clone, "a.txt", "a\n")
	before := git(t, bare, "rev-parse", "main")

	res := callLand(t, map[string]interface{}{"dir": clone, "base": "main", "dryRun": true})
	if !res.OK {
		t.Fatalf("dry run failed: %s", res.Error)
	}
	if body(t, res)["wouldLand"] != 1 {
		t.Fatalf("wouldLand = %v, want 1", body(t, res)["wouldLand"])
	}
	if after := git(t, bare, "rev-parse", "main"); after != before {
		t.Fatal("dry run moved the remote — it must not")
	}
}

func TestGitLandPushFalseKeepsRemoteUntouched(t *testing.T) {
	bare, clone := landFixture(t, "github")
	before := git(t, bare, "rev-parse", "main")
	commitFile(t, clone, "a.txt", "a\n")

	push := false
	res := callLand(t, map[string]interface{}{"dir": clone, "base": "main", "push": push})
	if !res.OK {
		t.Fatalf("local land failed: %s: %s", res.Code, res.Error)
	}
	if body(t, res)["pushed"] != false {
		t.Fatalf("pushed = %v, want false", body(t, res)["pushed"])
	}
	if after := git(t, bare, "rev-parse", "main"); after != before {
		t.Fatal("push:false still pushed — that is the whole contract of the flag")
	}
}

// "Finished" is not "landed". This is the distinction autorun_status could not
// previously make.
func TestGitLandStateSeesUnpushedWork(t *testing.T) {
	_, clone := landFixture(t, "github")
	commitFile(t, clone, "a.txt", "a\n")

	res := callLandState(t, map[string]interface{}{"dir": clone, "base": "main"})
	if !res.OK {
		t.Fatalf("git_land_state failed: %s", res.Error)
	}
	if body(t, res)["unpushed"] != 1 {
		t.Fatalf("unpushed = %v, want 1", body(t, res)["unpushed"])
	}
	if body(t, res)["remote"] != "github" {
		t.Fatalf("remote = %v, want github", body(t, res)["remote"])
	}

	if r := callLand(t, map[string]interface{}{"dir": clone, "base": "main"}); !r.OK {
		t.Fatalf("land failed: %s", r.Error)
	}
	after := callLandState(t, map[string]interface{}{"dir": clone, "base": "main"})
	if body(t, after)["unpushed"] != 0 {
		t.Fatalf("unpushed after landing = %v, want 0", body(t, after)["unpushed"])
	}
}

// The snapshot autorun_status hangs off. It must never claim landed when work
// is still sitting locally — that is the exact lie it exists to prevent.
func TestAutorunLandingSnapshotDistinguishesLandedFromLocalOnly(t *testing.T) {
	_, clone := landFixture(t, "github")

	if st := autorunLandingSnapshot(clone, "main"); st == nil || !st.Landed {
		t.Fatalf("clean clone should read as landed, got %+v", st)
	}
	commitFile(t, clone, "a.txt", "a\n")

	st := autorunLandingSnapshot(clone, "main")
	if st == nil {
		t.Fatal("snapshot was nil")
	}
	if st.Landed {
		t.Fatal("landed = true with an unpushed commit — this is the false-positive the field exists to kill")
	}
	if st.Unpushed != 1 {
		t.Fatalf("unpushed = %d, want 1", st.Unpushed)
	}
	if st.Remote != "github" {
		t.Fatalf("remote = %q, want github", st.Remote)
	}
}

func TestAutorunLandingSnapshotIsNilForNoDir(t *testing.T) {
	if st := autorunLandingSnapshot("", "main"); st != nil {
		t.Fatalf("want nil for an empty dir, got %+v", st)
	}
}
