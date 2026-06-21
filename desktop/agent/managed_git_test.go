package main

import (
	"os"
	osexec "os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestManagedGitCreateCheckpointAndBackup(t *testing.T) {
	if _, err := osexec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	t.Setenv("HOME", t.TempDir())
	workDir := filepath.Join(t.TempDir(), "project")
	if err := os.MkdirAll(filepath.Join(workDir, ".yaver"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workDir, "README.md"), []byte("# Demo\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	meta, err := EnsureManagedGitForProject(workDir, "demo", "Demo", &ManagedGitCreateOptions{
		Enabled:    true,
		Visibility: "private",
	})
	if err != nil {
		t.Fatalf("EnsureManagedGitForProject: %v", err)
	}
	if !meta.Enabled || meta.RepoID != "demo" || meta.Visibility != "private" {
		t.Fatalf("unexpected meta: %+v", meta)
	}
	if _, err := os.Stat(meta.BarePath); err != nil {
		t.Fatalf("bare repo missing: %v", err)
	}
	if strings.TrimSpace(meta.LastCommit) == "" {
		t.Fatalf("initial commit missing: %+v", meta)
	}

	if err := os.WriteFile(filepath.Join(workDir, "feature.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	commit, err := ManagedGitCheckpoint(workDir, "yaver: add feature")
	if err != nil {
		t.Fatalf("ManagedGitCheckpoint: %v", err)
	}
	if commit == "" || commit == meta.LastCommit {
		t.Fatalf("checkpoint did not advance: before=%q after=%q", meta.LastCommit, commit)
	}

	backup, err := ManagedGitBackup(workDir)
	if err != nil {
		t.Fatalf("ManagedGitBackup: %v", err)
	}
	if backup.Path == "" || backup.SizeBytes <= 0 {
		t.Fatalf("bad backup: %+v", backup)
	}
	if _, err := os.Stat(backup.Path); err != nil {
		t.Fatalf("backup missing: %v", err)
	}
}

// Mirror-on-push and sync must be safe no-ops when no mirror is connected (the
// common case) — and a checkpoint must still succeed.
func TestManagedGitSyncNoMirrorIsSafe(t *testing.T) {
	if _, err := osexec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	t.Setenv("HOME", t.TempDir())
	workDir := filepath.Join(t.TempDir(), "project")
	if err := os.MkdirAll(filepath.Join(workDir, ".yaver"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workDir, "README.md"), []byte("# Demo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := EnsureManagedGitForProject(workDir, "demo", "Demo", &ManagedGitCreateOptions{Enabled: true, Visibility: "private"}); err != nil {
		t.Fatalf("EnsureManagedGitForProject: %v", err)
	}
	if got := ManagedGitMirrorSyncAll(workDir); got != nil {
		t.Fatalf("mirror-on-push with no mirror should be nil, got %+v", got)
	}
	res, err := ManagedGitMirrorPull(workDir)
	if err != nil {
		t.Fatalf("ManagedGitMirrorPull no-mirror: %v", err)
	}
	if res != "no-mirror" {
		t.Fatalf("expected no-mirror, got %q", res)
	}
	// checkpoint (which now calls mirror-on-push) must still succeed
	if err := os.WriteFile(filepath.Join(workDir, "x.txt"), []byte("hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ManagedGitCheckpoint(workDir, "yaver: with no mirror"); err != nil {
		t.Fatalf("checkpoint with no mirror: %v", err)
	}
}

func TestCreateTodoPhoneProjectWithManagedGitLifecycle(t *testing.T) {
	if _, err := osexec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	t.Setenv("HOME", t.TempDir())

	project, err := CreatePhoneProject(PhoneCreateSpec{
		Slug:     "todo-demo",
		Name:     "Todo Demo",
		Template: "todos",
		ManagedGit: &ManagedGitCreateOptions{
			Enabled:    true,
			Visibility: "private",
		},
	})
	if err != nil {
		t.Fatalf("CreatePhoneProject: %v", err)
	}
	if project.ManagedGit == nil || !project.ManagedGit.Enabled {
		t.Fatalf("managed git missing: %+v", project)
	}
	if project.ManagedGit.DefaultBranch != "main" {
		t.Fatalf("default branch = %q, want main", project.ManagedGit.DefaultBranch)
	}

	note := filepath.Join(project.Dir, "src", "todo-note.txt")
	if err := os.MkdirAll(filepath.Dir(note), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(note, []byte("normie todo app\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	commit, err := ManagedGitCheckpoint(project.Dir, "yaver: add todo note")
	if err != nil {
		t.Fatalf("checkpoint: %v", err)
	}
	if commit == "" {
		t.Fatal("empty checkpoint commit")
	}

	backup, err := ManagedGitBackup(project.Dir)
	if err != nil {
		t.Fatalf("backup: %v", err)
	}
	if backup.SizeBytes <= 0 {
		t.Fatalf("empty backup: %+v", backup)
	}

	if err := os.WriteFile(note, []byte("broken local edit\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	restored, err := ManagedGitRestoreBundle(project.Dir, backup.Path)
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	if restored == "" {
		t.Fatal("empty restored commit")
	}
	got, err := os.ReadFile(note)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "normie todo app\n" {
		t.Fatalf("restore content = %q", string(got))
	}

	ownedBackupRoot := filepath.Join(t.TempDir(), "owned-pc-backup")
	external, err := ManagedGitBackupToTarget(project.Dir, "local-folder", "", ownedBackupRoot)
	if err != nil {
		t.Fatalf("external backup: %v", err)
	}
	if external.Path == "" || external.SizeBytes <= 0 {
		t.Fatalf("bad external backup: %+v", external)
	}
	if _, err := os.Stat(filepath.Join(ownedBackupRoot, "YaverBackups", "todo-demo", "latest.bundle")); err != nil {
		t.Fatalf("latest external backup missing: %v", err)
	}
}
