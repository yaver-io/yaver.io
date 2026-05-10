package main

import (
	"os"
	"path/filepath"
	"testing"
)

// gh CLI shape (>=2.40): "Logged in to <host> account <username> (<source>)".
// glab CLI shape: "Logged in to <host> as <username> (<source>)". Both must
// parse so `yaver status` doesn't misreport "Not configured" when the user
// has the standard CLI logged in.
func TestParseLoggedInUsernameHandlesBothCliShapes(t *testing.T) {
	cases := []struct {
		name     string
		output   string
		host     string
		wantUser string
		wantOk   bool
	}{
		{
			name: "gh github account form",
			output: "github.com\n" +
				"  ✓ Logged in to github.com account alice (keyring)\n" +
				"  - Active account: true\n" +
				"  - Token: gho_******\n",
			host:     "github.com",
			wantUser: "alice",
			wantOk:   true,
		},
		{
			name: "glab gitlab as form",
			output: "gitlab.com\n" +
				"  ✓ Logged in to gitlab.com as bob (/path/to/config.yml)\n" +
				"  ✓ Token found\n",
			host:     "gitlab.com",
			wantUser: "bob",
			wantOk:   true,
		},
		{
			name:     "host not present",
			output:   "github.com\n  ✓ Logged in to github.com account alice\n",
			host:     "gitlab.com",
			wantUser: "",
			wantOk:   false,
		},
		{
			name:     "not authenticated",
			output:   "You are not logged into any GitHub hosts.\n",
			host:     "github.com",
			wantUser: "",
			wantOk:   false,
		},
		{
			name: "logged in line without username marker",
			output: "github.com\n" +
				"  ✓ Logged in to github.com (oauth_token)\n",
			host:     "github.com",
			wantUser: "",
			wantOk:   true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			user, ok := parseLoggedInUsername(tc.output, tc.host)
			if user != tc.wantUser || ok != tc.wantOk {
				t.Fatalf("parseLoggedInUsername(%q, %q) = (%q, %v); want (%q, %v)",
					tc.output, tc.host, user, ok, tc.wantUser, tc.wantOk)
			}
		})
	}
}

// SSH heuristic: ~/.ssh has at least one private key AND the host appears
// in ~/.ssh/config (Host line) or ~/.ssh/known_hosts. With neither present
// it must report false. Not having a key short-circuits to false even if
// known_hosts mentions the host (a key is required for git@<host>:... to
// actually work).
func TestSshConfiguredForHost(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	sshDir := filepath.Join(home, ".ssh")
	if err := os.MkdirAll(sshDir, 0700); err != nil {
		t.Fatalf("mkdir .ssh: %v", err)
	}

	if sshConfiguredForHost("github.com") {
		t.Fatal("expected false with empty .ssh")
	}

	// Key present but no config / known_hosts mention → still false.
	if err := os.WriteFile(filepath.Join(sshDir, "id_ed25519"), []byte("priv"), 0600); err != nil {
		t.Fatalf("write key: %v", err)
	}
	if sshConfiguredForHost("github.com") {
		t.Fatal("expected false with key but no host reference")
	}

	// Add a Host line in ~/.ssh/config — should now report true.
	if err := os.WriteFile(filepath.Join(sshDir, "config"), []byte("Host github.com\n  IdentityFile ~/.ssh/id_ed25519\n"), 0600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if !sshConfiguredForHost("github.com") {
		t.Fatal("expected true with Host github.com entry")
	}

	// Different host: not configured (no config/known_hosts mention).
	if sshConfiguredForHost("gitlab.com") {
		t.Fatal("expected false for gitlab.com when only github.com is configured")
	}

	// known_hosts fallback — no Host line, but known_hosts mentions gitlab.com.
	if err := os.WriteFile(filepath.Join(sshDir, "known_hosts"), []byte("gitlab.com ssh-ed25519 AAAA...\n"), 0600); err != nil {
		t.Fatalf("write known_hosts: %v", err)
	}
	if !sshConfiguredForHost("gitlab.com") {
		t.Fatal("expected true when known_hosts has gitlab.com entry and a key exists")
	}
}
