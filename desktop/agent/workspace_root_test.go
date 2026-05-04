package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestFindWorkspaceRoot_NpmWorkspaces simulates carrotbet's structure:
// a root package.json with `"workspaces": [...]` and a leaf mobile/
// inside it. Exercising this proves the bug we hit on
// yaver-test-ephemeral — bundle build running install in mobile/ and
// missing the workspace symlinks at the root — won't recur.
func TestFindWorkspaceRoot_NpmWorkspaces(t *testing.T) {
	root := t.TempDir()
	rootPkg := `{
  "name": "monorepo",
  "private": true,
  "workspaces": ["apps/*", "packages/*", "mobile"]
}`
	mustWrite(t, filepath.Join(root, "package.json"), rootPkg)

	leaf := filepath.Join(root, "mobile")
	mustMkdir(t, leaf)
	leafPkg := `{
  "name": "mobile",
  "private": true,
  "main": "expo/AppEntry",
  "dependencies": { "@scope/foo": "*" }
}`
	mustWrite(t, filepath.Join(leaf, "package.json"), leafPkg)

	got := findWorkspaceRoot(leaf)
	if got != root {
		t.Errorf("expected workspace root %q, got %q", root, got)
	}
}

// TestFindWorkspaceRoot_YarnClassicObject covers yarn 1's
// {"workspaces": {"packages": [...]}} shape. We support it because
// older expo/RN templates still ship that style and we don't want a
// well-formed monorepo to fall through to leaf-only install just
// because the manifest used the object form.
func TestFindWorkspaceRoot_YarnClassicObject(t *testing.T) {
	root := t.TempDir()
	rootPkg := `{
  "name": "yarn-monorepo",
  "private": true,
  "workspaces": { "packages": ["apps/*"], "nohoist": ["**/react-native"] }
}`
	mustWrite(t, filepath.Join(root, "package.json"), rootPkg)

	leaf := filepath.Join(root, "apps", "x")
	mustMkdirAll(t, leaf)
	mustWrite(t, filepath.Join(leaf, "package.json"), `{"name":"x"}`)

	got := findWorkspaceRoot(leaf)
	if got != root {
		t.Errorf("expected workspace root %q, got %q", root, got)
	}
}

// TestFindWorkspaceRoot_NotAMonorepo is the negative case — sfmg /
// other single-package projects must NOT be flagged as monorepos and
// must NOT have their install routed somewhere unexpected. Returns "".
func TestFindWorkspaceRoot_NotAMonorepo(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "package.json"), `{"name":"sfmg","main":"expo-router/entry","dependencies":{"expo":"~54.0.33"}}`)

	if got := findWorkspaceRoot(dir); got != "" {
		t.Errorf("expected non-monorepo to return \"\", got %q", got)
	}
}

// TestFindWorkspaceRoot_EmptyWorkspaces — a package.json with
// `"workspaces": []` is technically a workspace declaration but
// claims no members. Treat it as not-a-workspace; install should
// run at the leaf where it's invoked.
func TestFindWorkspaceRoot_EmptyWorkspaces(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "package.json"), `{"name":"empty","workspaces":[]}`)

	leaf := filepath.Join(root, "x")
	mustMkdir(t, leaf)
	mustWrite(t, filepath.Join(leaf, "package.json"), `{"name":"x"}`)

	if got := findWorkspaceRoot(leaf); got != "" {
		t.Errorf("empty workspaces array should not match; got %q", got)
	}
}

// TestDetectProjectPreparation_MonorepoForcesInstall ties the whole
// chain together: even when the leaf has node_modules, if the
// workspace root doesn't, the bundle preflight must mark
// NeedsDependencyInstall and route to the root. This is exactly the
// failure mode that broke carrotbet on yaver-test-ephemeral —
// mobile/node_modules existed (from an in-mobile npm install), the
// root never got installed, Metro's parent walk found no
// @backgammon/*, and bundle built failed with HTTP 500.
func TestDetectProjectPreparation_MonorepoForcesInstall(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "package.json"), `{"name":"monorepo","workspaces":["mobile"]}`)
	leaf := filepath.Join(root, "mobile")
	mustMkdir(t, leaf)
	mustMkdir(t, filepath.Join(leaf, "node_modules"))
	mustWrite(t, filepath.Join(leaf, "package.json"), `{"name":"mobile","dependencies":{"@scope/foo":"*"}}`)

	prep := detectProjectPreparation(leaf, nil)
	if prep.WorkspaceRoot != root {
		t.Fatalf("expected WorkspaceRoot=%q, got %q", root, prep.WorkspaceRoot)
	}
	if !prep.NeedsDependencyInstall {
		t.Fatal("expected NeedsDependencyInstall=true when root has no node_modules even though leaf does")
	}
}

// TestDetectProjectPreparation_MonorepoRootInstalled confirms the
// inverse: once the workspace root has node_modules, the leaf no
// longer triggers a fresh install on every bundle. Without this the
// preflight would loop endlessly.
func TestDetectProjectPreparation_MonorepoRootInstalled(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "package.json"), `{"name":"monorepo","workspaces":["mobile"]}`)
	mustMkdir(t, filepath.Join(root, "node_modules"))
	leaf := filepath.Join(root, "mobile")
	mustMkdir(t, leaf)
	mustMkdir(t, filepath.Join(leaf, "node_modules"))
	mustWrite(t, filepath.Join(leaf, "package.json"), `{"name":"mobile"}`)

	prep := detectProjectPreparation(leaf, nil)
	if prep.WorkspaceRoot != root {
		t.Fatalf("expected WorkspaceRoot=%q, got %q", root, prep.WorkspaceRoot)
	}
	if prep.NeedsDependencyInstall {
		t.Error("expected NeedsDependencyInstall=false when both root and leaf have node_modules")
	}
}

// mustWrite (wipe_cmd_test.go) and mustMkdirAll (unity_http_test.go)
// are reused as-is to avoid duplicate-symbol collisions in the same
// test package. mustMkdir is unique to this file.

func mustMkdir(t *testing.T, path string) {
	t.Helper()
	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
}
