package main

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A stray $HOME/.git must never turn HOME into a selectable project root.
// /projects already rejects HOME; /repos/list used to add it back from the
// task manager's default workDir, and the desktop/web top-level merge then
// collapsed every real checkout below HOME into one broad unknown row.
func TestRepoListRejectsHomeWithoutHidingWorkspaceRepos(t *testing.T) {
	home := withHome(t)
	if err := os.MkdirAll(filepath.Join(home, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	want := mkRepo(t, filepath.Join(home, "Workspace"), "yaver.io")

	repoCache.mu.Lock()
	repoCache.repos = nil
	repoCache.dirTimes = nil
	repoCache.cachedAt = time.Time{}
	repoCache.mu.Unlock()
	defer func() {
		repoCache.mu.Lock()
		repoCache.repos = nil
		repoCache.dirTimes = nil
		repoCache.cachedAt = time.Time{}
		repoCache.mu.Unlock()
	}()

	srv := &HTTPServer{taskMgr: &TaskManager{workDir: home}}
	recorder := httptest.NewRecorder()
	srv.handleRepoList(recorder, httptest.NewRequest("GET", "/repos/list", nil))

	var got []RepoInfo
	if err := json.Unmarshal(recorder.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v (%s)", err, recorder.Body.String())
	}
	if recorder.Code != 200 {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	for _, repo := range got {
		if filepath.Clean(repo.Path) == filepath.Clean(home) {
			t.Fatalf("HOME leaked into /repos/list: %+v", got)
		}
	}
	if !containsRepoPath(got, want) {
		t.Fatalf("real workspace repo %q missing from /repos/list: %+v", want, got)
	}
}

func TestRepoListFindsManagedSiblingRepos(t *testing.T) {
	home := withHome(t)
	managed := filepath.Join(home, "Workspace", "repos")
	wants := []string{
		mkRepo(t, managed, "yaver.io"),
		mkRepo(t, managed, "sfmg"),
		mkRepo(t, managed, "talos"),
	}

	repoCache.mu.Lock()
	repoCache.repos = nil
	repoCache.dirTimes = nil
	repoCache.cachedAt = time.Time{}
	repoCache.mu.Unlock()
	defer func() {
		repoCache.mu.Lock()
		repoCache.repos = nil
		repoCache.dirTimes = nil
		repoCache.cachedAt = time.Time{}
		repoCache.mu.Unlock()
	}()

	srv := &HTTPServer{taskMgr: &TaskManager{workDir: wants[0]}}
	recorder := httptest.NewRecorder()
	srv.handleRepoList(recorder, httptest.NewRequest("GET", "/repos/list", nil))

	var got []RepoInfo
	if err := json.Unmarshal(recorder.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v (%s)", err, recorder.Body.String())
	}
	for _, want := range wants {
		if !containsRepoPath(got, want) {
			t.Fatalf("managed sibling repo %q missing from /repos/list: %+v", want, got)
		}
	}
}

func containsRepoPath(repos []RepoInfo, want string) bool {
	for _, repo := range repos {
		if filepath.Clean(repo.Path) == filepath.Clean(want) {
			return true
		}
	}
	return false
}
