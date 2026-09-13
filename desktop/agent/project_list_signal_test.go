package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestProjectListHasRunnableSignalAcceptsYaverWorkspaceRoot(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "yaver.workspace.yaml"), []byte("version: 1\napps: []\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !projectListHasRunnableSignal(root) {
		t.Fatal("expected yaver.workspace.yaml root to be listed as a project")
	}
}

func TestProjectListHasRunnableSignalAcceptsDeployScript(t *testing.T) {
	root := t.TempDir()
	scripts := filepath.Join(root, "scripts")
	if err := os.MkdirAll(scripts, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(scripts, "deploy-web.sh"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if !projectListHasRunnableSignal(root) {
		t.Fatal("expected scripts/deploy-*.sh root to be listed as a project")
	}
}

func TestProjectListHasRunnableSignalRejectsEmptyRoot(t *testing.T) {
	if projectListHasRunnableSignal(t.TempDir()) {
		t.Fatal("expected empty root to stay hidden from project list")
	}
}

func TestMergeLiveWorkspaceReposIntoProjectsDedupes(t *testing.T) {
	home := withHome(t)
	ws := filepath.Join(home, "Workspace")
	repo := mkRepo(t, ws, "yaver.io")
	projects := []projectInfo{{Path: repo, Branch: "main"}}
	got := mergeLiveWorkspaceReposIntoProjects(projects)
	count := 0
	for _, project := range got {
		if project.Path == repo {
			count++
			if project.Branch != "main" {
				t.Fatalf("existing project branch changed during merge: %+v", project)
			}
		}
	}
	if count != 1 {
		t.Fatalf("expected yaver.io repo to appear once, got %d in %+v", count, got)
	}
}

func TestMergeLiveWorkspaceReposIntoProjectsAddsWorkspaceRepoRoots(t *testing.T) {
	home := withHome(t)
	ws := filepath.Join(home, "Workspace")
	repo := mkRepo(t, ws, "yaver.io")
	got := mergeLiveWorkspaceReposIntoProjects(nil)
	for _, project := range got {
		if project.Path == repo {
			return
		}
	}
	t.Fatalf("expected workspace repo root %q to be merged into /projects, got %+v", repo, got)
}

// TestMergeLiveWorkspaceReposAddsManifestApps — the 2026-08-12 blocker:
// tvos/, watch/, visionos/ inside the yaver.io monorepo are declared apps in
// yaver.workspace.yaml but are NOT their own git repos, so scanDirForRepos
// never found them and the webui chat could never pick the TV/watch/vision
// projects ("no tvos at all too"). The manifest merge must surface every
// declared app whose path exists.
func TestMergeLiveWorkspaceReposAddsManifestApps(t *testing.T) {
	home := withHome(t)
	root := mkRepo(t, filepath.Join(home, "Workspace"), "declared-monorepo")
	for _, app := range []string{"tvos", "watch", "visionos", "wear"} {
		if err := os.MkdirAll(filepath.Join(root, app), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	manifest := "version: 1\nname: test\nworkspace:\n  root: .\napps:\n" +
		"  - name: tvos\n    path: ./tvos\n    stack: swift\n" +
		"  - name: watchos\n    path: ./watch\n    stack: swift\n" +
		"  - name: visionos\n    path: ./visionos\n    stack: swift\n" +
		"  - name: wear-os\n    path: ./wear\n    stack: kotlin\n"
	if err := os.WriteFile(filepath.Join(root, "yaver.workspace.yaml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}

	got := mergeLiveWorkspaceReposIntoProjects(nil)
	found := map[string]bool{}
	for _, project := range got {
		found[project.Name] = project.ManifestApp
	}
	for _, want := range []string{"tvos", "watchos", "visionos", "wear-os"} {
		if !found[want] {
			t.Errorf("manifest app %q not merged into /projects (got %v)", want, found)
		}
	}
	collapsed := collapseNestedReposOutsideHome(got, home)
	for _, want := range []string{"tvos", "watchos", "visionos", "wear-os"} {
		kept := false
		for _, project := range collapsed {
			kept = kept || project.Name == want
		}
		if !kept {
			t.Errorf("declared nested app %q was collapsed under its repo root", want)
		}
	}
}
