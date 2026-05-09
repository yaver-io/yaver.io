package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestOpsPrepare_ViteWebviewIntent walks through a fresh-clone Vite
// shape (workDir lacks node_modules but has package.json + vite.config.js)
// and asserts the plan names install_workspace_deps and start_dev_server
// in that order — the exact sequence the dashboard's Webview tab needs
// to render a preview.
func TestOpsPrepare_ViteWebviewIntent(t *testing.T) {
	dir := t.TempDir()
	mustMkViteProject(t, dir)

	res := callPrepare(t, prepareReq{WorkDir: dir, Intent: "webview"})
	if !res.OK {
		t.Fatalf("prepare returned not-ok: %s", res.Error)
	}
	out := res.Initial.(map[string]interface{})

	if got, want := out["framework"], "vite"; got != want {
		t.Fatalf("framework = %v, want %v", got, want)
	}
	plan := out["plan"].([]prepareStep)
	if len(plan) < 2 {
		t.Fatalf("plan should have install + start, got %+v", plan)
	}
	if plan[0].Action != "install_workspace_deps" || plan[0].Done {
		t.Fatalf("first step must be install_workspace_deps and pending; got %+v", plan[0])
	}
	if plan[1].Action != "start_dev_server" {
		t.Fatalf("second step must be start_dev_server; got %+v", plan[1])
	}
	if !strings.Contains(plan[1].Reason, "vite") {
		t.Fatalf("vite serve step should mention framework in reason; got %q", plan[1].Reason)
	}
}

// TestOpsPrepare_HermesIntentSurfacesPushChain verifies that intent=hermes
// produces both build_native_bundle and push_to_paired_phone steps, so
// the mobile Hot Reload card can render the full chain (compile → push)
// instead of just the compile step (which leaves the phone in the dark
// about what's about to happen).
func TestOpsPrepare_HermesIntentSurfacesPushChain(t *testing.T) {
	dir := t.TempDir()
	// Expo-shaped project — package.json + app.json, no node_modules.
	pkg := `{"name":"app","version":"0.0.0","dependencies":{"expo":"^52.0.0"}}`
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(pkg), 0o644); err != nil {
		t.Fatalf("write package.json: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "app.json"), []byte(`{"expo":{"name":"app","slug":"app"}}`), 0o644); err != nil {
		t.Fatalf("write app.json: %v", err)
	}

	res := callPrepare(t, prepareReq{WorkDir: dir, Intent: "hermes"})
	if !res.OK {
		t.Fatalf("prepare returned not-ok: %s", res.Error)
	}
	out := res.Initial.(map[string]interface{})
	plan := out["plan"].([]prepareStep)

	wantActions := []string{"build_native_bundle", "push_to_paired_phone"}
	gotActions := []string{}
	for _, s := range plan {
		if s.Action == "build_native_bundle" || s.Action == "push_to_paired_phone" {
			gotActions = append(gotActions, s.Action)
		}
	}
	for _, want := range wantActions {
		found := false
		for _, got := range gotActions {
			if got == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("hermes plan must include %q; got %v", want, gotActions)
		}
	}
}

// TestOpsPrepare_MissingFileTarballSurfacedAsIssue covers carrotbet's
// real-world failure shape: package.json declares a `file:` dep
// pointing at a tarball that doesn't exist on this host. The agent's
// auto-install would fail with EINTEGRITY/ENOENT — prepare should
// surface it as an actionable issue *before* the user pulls the
// trigger so the dashboard can show a fix-it card instead of a 30-line
// npm trace.
func TestOpsPrepare_MissingFileTarballSurfacedAsIssue(t *testing.T) {
	dir := t.TempDir()
	mustMkViteProject(t, dir)
	// Add a file: dep that points at a missing tarball.
	pkg := `{
  "name": "app",
  "version": "0.0.0",
  "dependencies": {
    "vite": "^5.0.0",
    "yaver-feedback-web": "file:./vendor/yaver-feedback-web-0.2.2.tgz"
  }
}`
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(pkg), 0o644); err != nil {
		t.Fatalf("rewrite package.json: %v", err)
	}

	res := callPrepare(t, prepareReq{WorkDir: dir, Intent: "webview"})
	if !res.OK {
		t.Fatalf("prepare returned not-ok: %s", res.Error)
	}
	out := res.Initial.(map[string]interface{})
	issues := out["issues"].([]prepareIssue)

	matched := false
	for _, iss := range issues {
		if strings.Contains(iss.Message, "yaver-feedback-web") && strings.Contains(iss.Fix, "npm pack") {
			matched = true
			break
		}
	}
	if !matched {
		t.Fatalf("expected an actionable issue for missing yaver-feedback-web tarball; got %+v", issues)
	}
}

// TestOpsPrepare_MonorepoFallbackSurfacedAsPickerNote checks that when
// workDir is a monorepo root (no marker at the root), the verb resolves
// the actual sub-app and surfaces the choice in pickerNote so the UI
// can display "we picked apps/web/" instead of silently rerouting.
func TestOpsPrepare_MonorepoFallbackSurfacedAsPickerNote(t *testing.T) {
	root := t.TempDir()
	// Two apps at apps/web (Next) and apps/admin (Next). With our
	// improved picker, apps/web wins on conventional name.
	mustMkNextProject(t, filepath.Join(root, "apps", "admin"))
	mustMkNextProject(t, filepath.Join(root, "apps", "web"))
	// And a workspaces root package.json so DetectMonorepo identifies
	// this as a monorepo.
	rootPkg := `{"name":"r","private":true,"workspaces":["apps/*"]}`
	if err := os.WriteFile(filepath.Join(root, "package.json"), []byte(rootPkg), 0o644); err != nil {
		t.Fatalf("write root package.json: %v", err)
	}

	res := callPrepare(t, prepareReq{WorkDir: root, Intent: "webview"})
	if !res.OK {
		t.Fatalf("prepare returned not-ok: %s", res.Error)
	}
	out := res.Initial.(map[string]interface{})
	if got := out["resolvedWorkDir"].(string); got != filepath.Join(root, "apps", "web") {
		t.Fatalf("resolvedWorkDir = %q, want apps/web", got)
	}
	if note := out["pickerNote"].(string); !strings.Contains(note, "apps/web") {
		t.Fatalf("pickerNote should explain the monorepo pick; got %q", note)
	}
}

// TestOpsPrepare_RejectsBadInput covers the cheap input-validation
// branches so a malformed payload from a misconfigured coding agent
// surfaces a structured error code instead of a panic.
func TestOpsPrepare_RejectsBadInput(t *testing.T) {
	cases := []struct {
		name    string
		req     prepareReq
		wantErr string
	}{
		{"empty workDir", prepareReq{}, "workDir is required"},
		{"relative workDir", prepareReq{WorkDir: "./foo"}, "workDir must be absolute"},
		{"nonexistent workDir", prepareReq{WorkDir: "/tmp/yaver-this-must-not-exist-xyz"}, "does not exist"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := callPrepare(t, tc.req)
			if res.OK {
				t.Fatalf("expected error, got OK")
			}
			if !strings.Contains(res.Error, tc.wantErr) {
				t.Fatalf("error %q must mention %q", res.Error, tc.wantErr)
			}
		})
	}
}

// callPrepare is a small adapter that runs the verb's handler with a
// JSON-encoded payload — exactly how dispatchOps invokes it in production.
// We don't construct a real HTTPServer; the handler doesn't touch
// c.Server for the discovery path.
func callPrepare(t *testing.T, req prepareReq) OpsResult {
	t.Helper()
	payload, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return opsPrepareHandler(OpsContext{}, payload)
}
