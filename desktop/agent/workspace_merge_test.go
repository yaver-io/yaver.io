package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMergeWorkspaceReposPreservesHistoryAndWritesManifest(t *testing.T) {
	webDir := makeGitRepoForWorkspaceMerge(t, "web", map[string]string{
		"package.json":   `{"name":"web"}`,
		"next.config.js": `module.exports = {}`,
	}, "web initial")
	writeAndCommitGitRepo(t, webDir, map[string]string{
		"pages/index.tsx": "export default function Home(){ return 'web'; }\n",
	}, "web second")
	webHead := gitRepoHead(t, webDir)

	apiDir := makeGitRepoForWorkspaceMerge(t, "api", map[string]string{
		"go.mod":    "module example.com/api\n\ngo 1.22\n",
		"main.go":   "package main\nfunc main() {}\n",
		"README.md": "# api\n",
	}, "api initial")
	writeAndCommitGitRepo(t, apiDir, map[string]string{
		"internal/server/server.go": "package server\n",
	}, "api second")
	apiHead := gitRepoHead(t, apiDir)

	mobileDir := makeGitRepoForWorkspaceMerge(t, "mobile", map[string]string{
		"app.json": `{"expo":{"name":"mobile"}}`,
	}, "mobile initial")
	writeAndCommitGitRepo(t, mobileDir, map[string]string{
		"App.tsx": "export default function App(){ return null }\n",
	}, "mobile second")
	mobileHead := gitRepoHead(t, mobileDir)

	root := filepath.Join(t.TempDir(), "acme")
	res, err := mergeWorkspaceRepos([]string{webDir, apiDir, mobileDir}, workspaceMergeOptions{
		Root: root,
		Name: "acme",
	})
	if err != nil {
		t.Fatalf("mergeWorkspaceRepos: %v", err)
	}
	if len(res.Imports) != 3 {
		t.Fatalf("imports = %d, want 3", len(res.Imports))
	}

	manifest, err := LoadWorkspaceManifest(root)
	if err != nil {
		t.Fatalf("LoadWorkspaceManifest: %v", err)
	}
	gotPaths := map[string]string{}
	for _, app := range manifest.Apps {
		gotPaths[app.Name] = app.Path + "|" + app.Stack
	}
	if gotPaths["web"] != "./apps/web|nextjs" {
		t.Fatalf("web app = %q", gotPaths["web"])
	}
	if gotPaths["backend"] != "./backend|go" {
		t.Fatalf("backend app = %q", gotPaths["backend"])
	}
	if gotPaths["mobile"] != "./apps/mobile|react-native-expo" {
		t.Fatalf("mobile app = %q", gotPaths["mobile"])
	}

	for _, rel := range []string{"apps/web", "backend", "apps/mobile"} {
		if _, err := os.Stat(filepath.Join(root, rel)); err != nil {
			t.Fatalf("expected imported path %s: %v", rel, err)
		}
	}

	assertAncestorCommit(t, root, webHead)
	assertAncestorCommit(t, root, apiHead)
	assertAncestorCommit(t, root, mobileHead)
}

func TestParseWorkspaceMergeSource(t *testing.T) {
	src, target := parseWorkspaceMergeSource("git@github.com:acme/web.git=apps/web")
	if src != "git@github.com:acme/web.git" {
		t.Fatalf("src = %q", src)
	}
	if target != "apps/web" {
		t.Fatalf("target = %q", target)
	}
}

func makeGitRepoForWorkspaceMerge(t *testing.T, name string, files map[string]string, msg string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	runGitForWorkspaceMergeTest(t, dir, "init", "-b", "main")
	writeAndCommitGitRepo(t, dir, files, msg)
	return dir
}

func writeAndCommitGitRepo(t *testing.T, dir string, files map[string]string, msg string) {
	t.Helper()
	for rel, content := range files {
		full := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	runGitForWorkspaceMergeTest(t, dir, "add", "-A")
	runGitForWorkspaceMergeTest(t, dir, "commit", "-m", msg)
}

func gitRepoHead(t *testing.T, dir string) string {
	t.Helper()
	out, err := workspaceGitOutput(dir, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(out)
}

func assertAncestorCommit(t *testing.T, repo string, sha string) {
	t.Helper()
	if err := workspaceGit(repo, "merge-base", "--is-ancestor", sha, "HEAD"); err != nil {
		t.Fatalf("expected %s to be ancestor of HEAD: %v", sha, err)
	}
}

func runGitForWorkspaceMergeTest(t *testing.T, dir string, args ...string) {
	t.Helper()
	if err := workspaceGit(dir, args...); err != nil {
		t.Fatalf("git %v in %s: %v", args, dir, err)
	}
}
