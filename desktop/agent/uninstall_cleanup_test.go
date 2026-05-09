package main

// uninstall_cleanup_test.go — pin the shell-rc + authorized_keys
// strippers. These are the cleanup helpers `yaver uninstall` runs
// outside ~/.yaver, so a regression here means uninstall silently
// leaves stale state on the user's machine.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStripShellRcYaverPath_RemovesPostinstallBlock(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	preserved := `# user content above
export PATH="$HOME/bin:$PATH"
alias ll='ls -la'
`
	yaverBlock := `
# yaver-cli PATH (added by yaver-cli postinstall)
case ":$PATH:" in *":/usr/local/bin:"*) ;; *) export PATH="/usr/local/bin:$PATH" ;; esac
`
	postBlock := `
# user content below
export EDITOR=vim
`
	for _, name := range []string{".bashrc", ".zshrc", ".profile"} {
		if err := os.WriteFile(filepath.Join(tmp, name), []byte(preserved+yaverBlock+postBlock), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	patched, err := stripShellRcYaverPath()
	if err != nil {
		t.Fatalf("strip returned err: %v", err)
	}
	if patched != 3 {
		t.Fatalf("expected 3 files patched, got %d", patched)
	}
	for _, name := range []string{".bashrc", ".zshrc", ".profile"} {
		body, _ := os.ReadFile(filepath.Join(tmp, name))
		s := string(body)
		if strings.Contains(s, "# yaver-cli PATH") {
			t.Fatalf("%s still has the marker after strip:\n%s", name, s)
		}
		if !strings.Contains(s, "alias ll='ls -la'") {
			t.Fatalf("%s lost user content above the block:\n%s", name, s)
		}
		if !strings.Contains(s, "export EDITOR=vim") {
			t.Fatalf("%s lost user content below the block:\n%s", name, s)
		}
	}
}

func TestStripShellRcYaverPath_Idempotent(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	if err := os.WriteFile(filepath.Join(tmp, ".bashrc"), []byte("alias x=y\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	patched, err := stripShellRcYaverPath()
	if err != nil {
		t.Fatalf("strip on clean: %v", err)
	}
	if patched != 0 {
		t.Fatalf("expected 0 patched on clean rc, got %d", patched)
	}
	body, _ := os.ReadFile(filepath.Join(tmp, ".bashrc"))
	if string(body) != "alias x=y\n" {
		t.Fatalf("strip mutated clean file: %q", string(body))
	}
}

func TestStripYaverBootstrapAuthorizedKeys_RemovesOnlyTaggedLines(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	sshDir := filepath.Join(tmp, ".ssh")
	if err := os.MkdirAll(sshDir, 0o700); err != nil {
		t.Fatal(err)
	}
	akPath := filepath.Join(sshDir, "authorized_keys")

	body := strings.Join([]string{
		"ssh-ed25519 AAAAC3aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa user@laptop",
		"ssh-ed25519 AAAAC3bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb yaver-bootstrap from foo @ 2026-05-09T12:00:00Z",
		"ssh-rsa AAAAB3cccccccccccccccccccccccccccccccccccccccccccc github-actions",
		"ssh-ed25519 AAAAC3ddddddddddddddddddddddddddddddddddddddddddd yaver-bootstrap from bar @ 2026-05-09T12:30:00Z",
	}, "\n") + "\n"
	if err := os.WriteFile(akPath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	removed, err := stripYaverBootstrapAuthorizedKeys()
	if err != nil {
		t.Fatalf("strip authorized_keys: %v", err)
	}
	if removed != 2 {
		t.Fatalf("expected 2 lines removed, got %d", removed)
	}
	out, _ := os.ReadFile(akPath)
	s := string(out)
	if strings.Contains(s, "yaver-bootstrap") {
		t.Fatalf("yaver-bootstrap entries still present:\n%s", s)
	}
	if !strings.Contains(s, "user@laptop") {
		t.Fatalf("non-yaver entry was wiped:\n%s", s)
	}
	if !strings.Contains(s, "github-actions") {
		t.Fatalf("github-actions key was wiped:\n%s", s)
	}
	info, err := os.Stat(akPath)
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Fatalf("authorized_keys perms = %o, want 0600", mode)
	}
}

func TestStripYaverBootstrapAuthorizedKeys_MissingFile(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	removed, err := stripYaverBootstrapAuthorizedKeys()
	if err != nil {
		t.Fatalf("expected no error on missing authorized_keys, got %v", err)
	}
	if removed != 0 {
		t.Fatalf("expected 0 removed, got %d", removed)
	}
}
