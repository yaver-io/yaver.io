package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The blocking prompt these guard against is invisible in `autorun status` — a
// pane stuck on it reports as a running session — so the only place it can be
// caught is here.

func writeCodexVersionJSON(t *testing.T, home, body string) string {
	t.Helper()
	dir := filepath.Join(home, ".codex")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	path := filepath.Join(dir, "version.json")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	return path
}

func readCodexVersionJSON(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	out := map[string]any{}
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return out
}

func TestCodexUpdatePromptDismissed(t *testing.T) {
	home := t.TempDir()
	// Exactly the shape observed on the mini while a run sat blocked.
	path := writeCodexVersionJSON(t, home,
		`{"latest_version":"0.144.5","last_checked_at":"2026-07-17T13:01:27.635640Z"}`)

	if err := ensureCodexUpdatePromptDismissed(home); err != nil {
		t.Fatalf("dismiss: %v", err)
	}

	got := readCodexVersionJSON(t, path)
	if got["dismissed_version"] != "0.144.5" {
		t.Fatalf("dismissed_version = %v, want 0.144.5", got["dismissed_version"])
	}
	// Every other key must survive — this file is codex's, not ours.
	if got["last_checked_at"] != "2026-07-17T13:01:27.635640Z" {
		t.Fatalf("last_checked_at was not preserved: %v", got["last_checked_at"])
	}
}

// A newer offer must re-arm the prompt suppression, otherwise the next release
// blocks the pane exactly as 0.144.5 did.
func TestCodexUpdatePromptDismissedFollowsNewerVersion(t *testing.T) {
	home := t.TempDir()
	path := writeCodexVersionJSON(t, home,
		`{"latest_version":"0.150.0","dismissed_version":"0.144.5"}`)

	if err := ensureCodexUpdatePromptDismissed(home); err != nil {
		t.Fatalf("dismiss: %v", err)
	}
	if got := readCodexVersionJSON(t, path); got["dismissed_version"] != "0.150.0" {
		t.Fatalf("dismissed_version = %v, want 0.150.0", got["dismissed_version"])
	}
}

// Never invent a version. With nothing recorded as latest there is no prompt to
// pre-answer, and writing a guess would suppress a screen we have not seen.
func TestCodexUpdatePromptNoLatestLeavesFileAlone(t *testing.T) {
	home := t.TempDir()
	body := `{"last_checked_at":"2026-07-17T13:01:27.635640Z"}`
	path := writeCodexVersionJSON(t, home, body)

	if err := ensureCodexUpdatePromptDismissed(home); err != nil {
		t.Fatalf("dismiss: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(data) != body {
		t.Fatalf("file was rewritten: %s", data)
	}
}

// A missing file is a codex that has never checked for updates. Not an error.
func TestCodexUpdatePromptMissingFileIsNotAnError(t *testing.T) {
	if err := ensureCodexUpdatePromptDismissed(t.TempDir()); err != nil {
		t.Fatalf("missing version.json should be a no-op, got %v", err)
	}
}

// Refuse to clobber a file we cannot parse: a prompt is a smaller problem than
// a destroyed state file.
func TestCodexUpdatePromptUnparseableIsRefused(t *testing.T) {
	home := t.TempDir()
	path := writeCodexVersionJSON(t, home, `{not json`)
	if err := ensureCodexUpdatePromptDismissed(home); err == nil {
		t.Fatal("expected an error for unparseable version.json")
	}
	data, _ := os.ReadFile(path)
	if string(data) != `{not json` {
		t.Fatalf("unparseable file must be left untouched, got: %s", data)
	}
}

func TestCodexFolderTrustedAppendsWorktree(t *testing.T) {
	home := t.TempDir()
	work := filepath.Join(home, "worktrees", "merged-remaining-codex")
	if err := os.MkdirAll(work, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	cfgDir := filepath.Join(home, ".codex")
	if err := os.MkdirAll(cfgDir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	cfg := filepath.Join(cfgDir, "config.toml")
	existing := "[projects.\"/Users/someone/Workspace/yaver.io\"]\ntrust_level = \"trusted\"\n"
	if err := os.WriteFile(cfg, []byte(existing), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	if err := ensureCodexFolderTrusted(home, work); err != nil {
		t.Fatalf("trust: %v", err)
	}

	data, err := os.ReadFile(cfg)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	got := string(data)
	if !strings.Contains(got, existing) {
		t.Fatalf("pre-existing project entry was lost:\n%s", got)
	}
	if !strings.Contains(got, "[projects."+strconvQuote(work)+"]") {
		t.Fatalf("worktree not trusted:\n%s", got)
	}
	if !strings.Contains(got, "trust_level = \"trusted\"") {
		t.Fatalf("trust_level missing:\n%s", got)
	}
}

// Idempotent: a second call must not append a duplicate block.
func TestCodexFolderTrustedIsIdempotent(t *testing.T) {
	home := t.TempDir()
	work := filepath.Join(home, "wt")
	if err := os.MkdirAll(work, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	for i := 0; i < 3; i++ {
		if err := ensureCodexFolderTrusted(home, work); err != nil {
			t.Fatalf("trust %d: %v", i, err)
		}
	}
	data, err := os.ReadFile(filepath.Join(home, ".codex", "config.toml"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if n := strings.Count(string(data), "[projects."+strconvQuote(work)+"]"); n != 1 {
		t.Fatalf("entry written %d times, want 1:\n%s", n, data)
	}
}

func strconvQuote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
