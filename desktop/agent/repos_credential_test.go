package main

import (
	"context"
	osexec "os/exec"
	"strings"
	"testing"
)

// TestStripURLCredentials verifies the pure helper that keeps a cloned repo's
// origin token-free. It must remove embedded userinfo from http(s) URLs and
// leave everything else (public URLs, SSH scp-style) untouched.
func TestStripURLCredentials(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"https://x-access-token:ghp_secret@github.com/me/app.git", "https://github.com/me/app.git"},
		{"https://github.com/me/app.git", "https://github.com/me/app.git"},
		{"http://user:pw@host/repo.git", "http://host/repo.git"},
		{"git@github.com:me/app.git", "git@github.com:me/app.git"}, // SSH untouched
		{"", ""},
	}
	for _, c := range cases {
		got := stripURLCredentials(c.in)
		if got != c.want {
			t.Errorf("stripURLCredentials(%q) = %q, want %q", c.in, got, c.want)
		}
		if strings.Contains(got, "ghp_secret") || strings.Contains(got, ":pw@") {
			t.Errorf("stripURLCredentials(%q) leaked a credential: %q", c.in, got)
		}
	}
}

// TestResetOriginToCleanURL is the real end-to-end proof of the leak fix: after
// a clone persists a tokenised origin in .git/config, resetOriginToCleanURL
// must strip it so a tester/guest sharing the workdir can't read the PAT.
// Uses a real local git repo (no mocks), matching the repo's test convention.
func TestResetOriginToCleanURL(t *testing.T) {
	if _, err := osexec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := osexec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v (%s)", args, err, out)
		}
	}
	run("init", "-q")
	// Simulate what `git clone https://user:token@host/…` leaves behind.
	tokenURL := "https://x-access-token:ghp_TESTSECRET@github.com/me/app.git"
	cleanURL := "https://github.com/me/app.git"
	run("remote", "add", "origin", tokenURL)

	// Sanity: the token is really persisted before we strip.
	before := gitRemoteURL(dir, "origin")
	if !strings.Contains(before, "ghp_TESTSECRET") {
		t.Fatalf("precondition failed: origin has no token: %q", before)
	}

	resetOriginToCleanURL(context.Background(), dir, cleanURL)

	after := gitRemoteURL(dir, "origin")
	if strings.Contains(after, "ghp_TESTSECRET") {
		t.Fatalf("token still present in origin after strip: %q", after)
	}
	if after != cleanURL {
		t.Fatalf("origin = %q, want token-free %q", after, cleanURL)
	}

	// Belt-and-suspenders: grep the raw .git/config for the token.
	cfg, err := osexec.Command("git", "-C", dir, "config", "--get", "remote.origin.url").Output()
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if strings.Contains(string(cfg), "ghp_TESTSECRET") {
		t.Fatalf(".git/config still leaks the token: %q", strings.TrimSpace(string(cfg)))
	}
}
