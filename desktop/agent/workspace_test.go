package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestWorkspaceLoadMissingFile — the most common failure mode is
// "ran yaver workspace in a repo without a manifest". The error must
// be typed (not a raw stat error) so CLIs / MCP callers can surface
// a helpful "run --scaffold" hint.
func TestWorkspaceLoadMissingFile(t *testing.T) {
	dir := t.TempDir()
	_, err := LoadWorkspaceManifest(dir)
	if err == nil {
		t.Fatal("expected error for missing manifest")
	}
	if !strings.Contains(err.Error(), "no workspace manifest") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestWorkspaceValidateShape — minimum-viable manifest checks.
func TestWorkspaceValidateShape(t *testing.T) {
	cases := []struct {
		name    string
		yaml    string
		wantErr string
	}{
		{"empty apps", `version: 1`, "no apps"},
		{"duplicate names", `version: 1
apps:
  - name: web
    path: ./web
  - name: web
    path: ./web2`, "duplicate name"},
		{"missing path", `version: 1
apps:
  - name: web`, "path is required"},
		{"unknown dependency", `version: 1
apps:
  - name: web
    path: ./web
    depends: [ghost]`, "depends on unknown app"},
		{"version mismatch", `version: 99
apps:
  - name: web
    path: ./web`, "unsupported manifest version"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "yaver.workspace.yaml")
			if err := os.WriteFile(path, []byte(tc.yaml), 0o644); err != nil {
				t.Fatal(err)
			}
			_, err := LoadWorkspaceManifest(dir)
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("want %q substr, got %v", tc.wantErr, err)
			}
		})
	}
}

// TestWorkspaceTopoSort — dep-order is stable when the graph is a
// DAG, and cycles are caught with an informative message so the
// caller can see which apps are tangled.
func TestWorkspaceTopoSort(t *testing.T) {
	m := &WorkspaceManifest{
		Version: 1,
		Apps: []WorkspaceApp{
			{Name: "web", Path: "./web", Depends: []string{"backend"}},
			{Name: "mobile", Path: "./mobile", Depends: []string{"backend"}},
			{Name: "backend", Path: "./backend"},
		},
	}
	if err := validateWorkspaceManifest(m); err != nil {
		t.Fatal(err)
	}
	order, err := TopoSortApps(m)
	if err != nil {
		t.Fatal(err)
	}
	// backend must come first; alphabetic tiebreak puts mobile before web.
	want := []string{"backend", "mobile", "web"}
	for i, w := range want {
		if order[i] != w {
			t.Fatalf("order[%d] = %q, want %q (full=%v)", i, order[i], w, order)
		}
	}

	// Cycle detection.
	m.Apps = append(m.Apps, WorkspaceApp{Name: "cyclic", Path: "./c", Depends: []string{"web"}})
	m.Apps[0].Depends = append(m.Apps[0].Depends, "cyclic") // web -> cyclic -> web
	if err := validateWorkspaceManifest(m); err == nil || !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("expected cycle error, got %v", err)
	}
}

// TestWorkspaceInitScaffoldsInitMd — the init engine writes init.md
// for every app that doesn't have one, idempotently, and skips apps
// that already have it unless --force is set.
func TestWorkspaceInitScaffoldsInitMd(t *testing.T) {
	root := t.TempDir()
	// Create two app directories.
	for _, name := range []string{"web", "backend"} {
		if err := os.MkdirAll(filepath.Join(root, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	manifest := `version: 1
name: test-monorepo
apps:
  - name: web
    path: ./web
    stack: nextjs
  - name: backend
    path: ./backend
    stack: convex
    depends: []
`
	if err := os.WriteFile(filepath.Join(root, "yaver.workspace.yaml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	m, err := LoadWorkspaceManifest(root)
	if err != nil {
		t.Fatal(err)
	}
	actions := RunWorkspaceInit(m, root, WorkspaceInitOptions{})
	// Every app should get check + init-md actions, both "ok".
	initMdOK := 0
	for _, a := range actions {
		if a.Action == "init-md" && a.Status == "ok" {
			initMdOK++
		}
	}
	if initMdOK != 2 {
		t.Fatalf("want 2 init-md ok, got %d (actions=%+v)", initMdOK, actions)
	}
	// Second run: both should be "skip" because the files now exist.
	actions2 := RunWorkspaceInit(m, root, WorkspaceInitOptions{})
	skipCount := 0
	for _, a := range actions2 {
		if a.Action == "init-md" && a.Status == "skip" {
			skipCount++
		}
	}
	if skipCount != 2 {
		t.Fatalf("want 2 init-md skip on re-run, got %d", skipCount)
	}
}

// TestOpsWorkspaceVerb — the ops verb forwards to the workspace
// engine and returns a typed counts map so agents can branch without
// walking the full actions array.
func TestOpsWorkspaceVerb(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"web", "mobile"} {
		if err := os.MkdirAll(filepath.Join(root, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	manifest := `version: 1
apps:
  - name: web
    path: ./web
    stack: nextjs
  - name: mobile
    path: ./mobile
    stack: react-native-expo
`
	if err := os.WriteFile(filepath.Join(root, "yaver.workspace.yaml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}

	payload, _ := json.Marshal(opsWorkspacePayload{Op: "init", Root: root})
	res := opsWorkspaceHandler(OpsContext{}, payload)
	if !res.OK {
		t.Fatalf("init failed: %s (%s)", res.Error, res.Code)
	}
	data, _ := res.Initial.(map[string]interface{})
	counts, _ := data["counts"].(map[string]int)
	if counts["ok"] == 0 {
		t.Fatalf("expected some ok actions, got %+v", data)
	}
}
