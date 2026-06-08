package main

import (
	"strings"
	"testing"
)

// parseGitRemote is the load-bearing bit of /git/pull-request: get the
// provider/host/owner/repo wrong and we open a PR against the wrong project
// (or none). Cover the remote URL shapes git actually emits, including
// embedded-cred HTTPS (which must NOT leak into the parsed coordinates) and
// GitLab subgroups (owner may contain slashes).
func TestParseGitRemote(t *testing.T) {
	cases := []struct {
		name                              string
		raw                               string
		provider, host, owner, repo       string
		wantErr                           bool
	}{
		{"github ssh", "git@github.com:acme/app.git", "github", "github.com", "acme", "app", false},
		{"github https", "https://github.com/acme/app.git", "github", "github.com", "acme", "app", false},
		{"github https no .git", "https://github.com/acme/app", "github", "github.com", "acme", "app", false},
		{"github https embedded creds", "https://x-access-token:ghp_secret@github.com/acme/app.git", "github", "github.com", "acme", "app", false},
		{"gitlab ssh", "git@gitlab.com:acme/app.git", "gitlab", "gitlab.com", "acme", "app", false},
		{"gitlab subgroup", "https://gitlab.com/acme/team/app.git", "gitlab", "gitlab.com", "acme/team", "app", false},
		// Self-hosted host the parser can't classify by name → provider empty;
		// the handler resolves it from the stored providers store by host.
		{"unknown self-hosted", "git@git.example.com:acme/app.git", "", "git.example.com", "acme", "app", false},
		{"github enterprise host", "https://github.example.com/acme/app.git", "github", "github.example.com", "acme", "app", false},
		{"empty", "", "", "", "", "", true},
		{"no path", "https://github.com", "", "", "", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseGitRemote(tc.raw)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error for %q, got %+v", tc.raw, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error for %q: %v", tc.raw, err)
			}
			if got.Provider != tc.provider || got.Host != tc.host || got.Owner != tc.owner || got.Repo != tc.repo {
				t.Errorf("parseGitRemote(%q) = %+v, want provider=%s host=%s owner=%s repo=%s",
					tc.raw, got, tc.provider, tc.host, tc.owner, tc.repo)
			}
			// A parsed remote must never carry credentials in any field.
			for _, field := range []string{got.Host, got.Owner, got.Repo} {
				if substrContainsAny(field, "secret", "ghp_", "@", ":") {
					t.Errorf("parsed field %q leaks creds/garbage from %q", field, tc.raw)
				}
			}
		})
	}
}

func TestGithubAPIBase(t *testing.T) {
	if got := githubAPIBase("github.com"); got != "https://api.github.com" {
		t.Errorf("github.com base = %q", got)
	}
	if got := githubAPIBase(""); got != "https://api.github.com" {
		t.Errorf("empty host base = %q", got)
	}
	if got := githubAPIBase("github.example.com"); got != "https://github.example.com/api/v3" {
		t.Errorf("enterprise base = %q", got)
	}
}

func substrContainsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}
