package main

// files_browser_security_test.go — H-9 regression: a symlink dropped
// into a project root must NOT let safeJoin escape and let /files/read
// or /files/raw return host files outside the root.

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestSafeJoinRejectsSymlinkEscape(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink semantics differ on Windows")
	}
	root := t.TempDir()
	outside := t.TempDir()

	// Drop a sentinel file that we don't want anyone to read.
	secret := filepath.Join(outside, "secret.txt")
	if err := os.WriteFile(secret, []byte("you-must-not-read-this"), 0o600); err != nil {
		t.Fatalf("write secret: %v", err)
	}

	// Plant a symlink inside the root pointing at the outside file.
	link := filepath.Join(root, "evil-link")
	if err := os.Symlink(secret, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	// Textual containment passes (the symlink itself is inside root)
	// but EvalSymlinks resolves out → must reject.
	got, ok := safeJoin(root, "evil-link")
	if ok {
		t.Errorf("safeJoin returned ok=true for symlink escape: %q", got)
	}

	// And confirm a normal file still works.
	normal := filepath.Join(root, "ok.txt")
	if err := os.WriteFile(normal, []byte("hello"), 0o600); err != nil {
		t.Fatalf("write normal: %v", err)
	}
	if _, ok := safeJoin(root, "ok.txt"); !ok {
		t.Error("safeJoin must accept a regular file inside root")
	}
}

func TestSafeJoinRejectsTraversal(t *testing.T) {
	root := t.TempDir()
	for _, sub := range []string{
		"../etc/passwd",
		"../../etc/passwd",
		"/etc/passwd",
		"foo/../../etc/passwd",
	} {
		got, ok := safeJoin(root, sub)
		if ok {
			// Some of these will textually clean to inside root
			// (e.g. "/etc/passwd" → root/etc/passwd, which is fine
			// because root + "etc/passwd" stays inside root). The
			// only thing we forbid is the result LANDING outside root.
			abs, _ := filepath.Abs(root)
			if !strings.HasPrefix(got, abs) {
				t.Errorf("safeJoin(%q, %q) = %q, escaped root", root, sub, got)
			}
		}
	}
}

func TestSafeJoinHandlesSymlinkedRoot(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink semantics differ on Windows")
	}
	// macOS /var → /private/var, /tmp → /private/tmp. EvalSymlinks
	// resolves t.TempDir() to that real path. The check must still
	// match a child of the resolved root.
	root := t.TempDir()
	child := filepath.Join(root, "child.txt")
	if err := os.WriteFile(child, []byte("ok"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, ok := safeJoin(root, "child.txt"); !ok {
		t.Error("safeJoin must accept a regular file even when root contains symlinks (macOS /var → /private/var)")
	}
}
