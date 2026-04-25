package main

import (
	"os"
	osexec "os/exec"
	"path/filepath"
	"testing"
)

func TestDetectRepoRemoteFromGitFallsBackToNamedProviderRemote(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", t.TempDir())

	runGitCmd(t, dir, "init")
	runGitCmd(t, dir, "remote", "add", "origin", "https://example.com/not-supported.git")
	runGitCmd(t, dir, "remote", "add", "gitlab", "git@gitlab.com:group/project.git")

	detected := detectRepoRemoteFromGit(dir)
	if detected.Provider != CIGitLab {
		t.Fatalf("provider = %q, want %q", detected.Provider, CIGitLab)
	}
	if detected.Host != "gitlab.com" {
		t.Fatalf("host = %q, want gitlab.com", detected.Host)
	}
	if detected.Repo != "group/project" {
		t.Fatalf("repo = %q, want group/project", detected.Repo)
	}
}

func TestDetectRepoRemoteFromGitSupportsConfiguredCustomGitLabHost(t *testing.T) {
	home := t.TempDir()
	dir := t.TempDir()
	t.Setenv("HOME", home)

	if err := saveGitProviders([]GitProvider{{
		Host:     "gitlab.myco.internal",
		Provider: "gitlab",
		Token:    "test-token",
	}}); err != nil {
		t.Fatalf("saveGitProviders: %v", err)
	}

	runGitCmd(t, dir, "init")
	runGitCmd(t, dir, "remote", "add", "origin", "git@gitlab.myco.internal:platform/mobile/app.git")

	detected := detectRepoRemoteFromGit(dir)
	if detected.Provider != CIGitLab {
		t.Fatalf("provider = %q, want %q", detected.Provider, CIGitLab)
	}
	if detected.Host != "gitlab.myco.internal" {
		t.Fatalf("host = %q, want gitlab.myco.internal", detected.Host)
	}
	if detected.Repo != "platform/mobile/app" {
		t.Fatalf("repo = %q, want platform/mobile/app", detected.Repo)
	}
}

func runGitCmd(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := osexec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = os.Environ()
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, string(out))
	}
}

func TestParseGitRemoteHostRepo(t *testing.T) {
	tests := []struct {
		raw      string
		wantHost string
		wantRepo string
	}{
		{raw: "https://github.com/acme/demo.git", wantHost: "github.com", wantRepo: "acme/demo"},
		{raw: "git@gitlab.com:group/project.git", wantHost: "gitlab.com", wantRepo: "group/project"},
		{raw: "ssh://git@gitlab.myco.internal/platform/mobile/app.git", wantHost: "gitlab.myco.internal", wantRepo: "platform/mobile/app"},
	}

	for _, tt := range tests {
		t.Run(filepath.Base(tt.wantRepo), func(t *testing.T) {
			host, repo := parseGitRemoteHostRepo(tt.raw)
			if host != tt.wantHost || repo != tt.wantRepo {
				t.Fatalf("parseGitRemoteHostRepo(%q) = (%q, %q), want (%q, %q)", tt.raw, host, repo, tt.wantHost, tt.wantRepo)
			}
		})
	}
}
