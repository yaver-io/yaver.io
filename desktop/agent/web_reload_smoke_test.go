package main

// web_reload_smoke_test.go — end-to-end smoke of the Web Reload
// surface: workspace manifest → /workspace/apps projection → /dev/start
// surface gating. Runs in-process against a fresh HTTPServer instance,
// so no external agent, no browser, no relay is required.

import (
	"os"
	"path/filepath"
	"testing"
)

const webReloadTestManifest = `version: 1
name: test-monorepo
workspace:
  root: "."
apps:
  - name: web-dashboard
    path: ./web
    stack: nextjs
  - name: marketing
    path: ./marketing
    stack: vite
  - name: mobile
    path: ./mobile
    stack: react-native-expo
  - name: mobile-native
    path: ./mobile-native
    stack: react-native
  - name: backend
    path: ./backend
    stack: convex
  - name: agent
    path: ./agent
    stack: go
`

// writeTestManifest creates a temp workspace root with the manifest
// above plus matching app directories so Exists passes.
func writeTestManifest(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, sub := range []string{"web", "marketing", "mobile", "mobile-native", "backend", "agent"} {
		if err := os.MkdirAll(filepath.Join(root, sub), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", sub, err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, WorkspaceManifestPath), []byte(webReloadTestManifest), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	return root
}

func TestWebReload_WorkspaceAppsProjection(t *testing.T) {
	root := writeTestManifest(t)
	tm := NewTaskManager(root, nil, defaultRunner)
	baseURL, cancel := startTestServer(t, "tok", tm)
	defer cancel()

	// Unfiltered: 6 apps come back.
	status, body := doRequest(t, "GET", baseURL+"/workspace/apps", "tok", "")
	if status != 200 {
		t.Fatalf("GET /workspace/apps → %d, body=%v", status, body)
	}
	apps, ok := body["apps"].([]interface{})
	if !ok {
		t.Fatalf("apps array missing: %v", body)
	}
	if len(apps) != 6 {
		t.Fatalf("expected 6 apps, got %d: %v", len(apps), apps)
	}

	// web,hybrid filter: web-dashboard (nextjs) + marketing (vite) + mobile
	// (react-native-expo, hybrid). Not backend or agent.
	status, body = doRequest(t, "GET", baseURL+"/workspace/apps?kind=web,hybrid", "tok", "")
	if status != 200 {
		t.Fatalf("GET /workspace/apps?kind=web,hybrid → %d", status)
	}
	apps = body["apps"].([]interface{})
	if len(apps) != 3 {
		t.Fatalf("expected 3 web+hybrid apps, got %d: %v", len(apps), apps)
	}
	names := map[string]string{}
	for _, a := range apps {
		m := a.(map[string]interface{})
		names[m["name"].(string)] = m["kind"].(string)
	}
	if names["web-dashboard"] != "web" {
		t.Fatalf("web-dashboard kind: got %q want web", names["web-dashboard"])
	}
	if names["marketing"] != "web" {
		t.Fatalf("marketing kind: got %q want web", names["marketing"])
	}
	if names["mobile"] != "hybrid" {
		t.Fatalf("mobile kind: got %q want hybrid", names["mobile"])
	}
	if _, excluded := names["backend"]; excluded {
		t.Fatalf("backend (stack=convex) should be filtered out")
	}
	if _, excluded := names["agent"]; excluded {
		t.Fatalf("agent (stack=go) should be filtered out")
	}
}

func TestWebReload_WorkspaceRequiresAuth(t *testing.T) {
	root := writeTestManifest(t)
	tm := NewTaskManager(root, nil, defaultRunner)
	baseURL, cancel := startTestServer(t, "tok", tm)
	defer cancel()

	status, _ := doRequest(t, "GET", baseURL+"/workspace/apps", "", "")
	if status != 401 {
		t.Fatalf("unauthenticated request should be 401, got %d", status)
	}
}

func TestWebReload_DevStartSurfaceGating(t *testing.T) {
	root := writeTestManifest(t)
	tm := NewTaskManager(root, nil, defaultRunner)
	baseURL, cancel := startTestServer(t, "tok", tm)
	defer cancel()

	// The test server doesn't wire up a DevServerManager by default;
	// without one, handleDevServerStart short-circuits with 503 and
	// our surface gate never runs. Attach a manager so we exercise
	// the monorepo/surface logic, not the availability check.
	if srv := currentTestHTTPServer; srv != nil && srv.devServerMgr == nil {
		srv.devServerMgr = NewDevServerManager()
	}

	// Pure mobile (react-native, not Expo) requested from Web Reload → 400.
	// react-native-expo is hybrid (Expo supports web), so that surface
	// IS allowed — only pure native RN is blocked.
	status, body := doRequest(t, "POST", baseURL+"/dev/start", "tok", `{"app":"mobile-native","surface":"web-reload"}`)
	if status != 400 {
		t.Fatalf("expected 400 for mobile-native from web-reload, got %d body=%v", status, body)
	}
	if body["error"] == nil {
		t.Fatalf("expected error message in 400 response: %v", body)
	}

	// Unknown app → 400 with "not in workspace manifest".
	status, body = doRequest(t, "POST", baseURL+"/dev/start", "tok", `{"app":"nonexistent","surface":"web-reload"}`)
	if status != 400 {
		t.Fatalf("unknown app: expected 400, got %d", status)
	}

	// Non-web stack (agent, stack=go) → 400 because Kind is empty.
	status, _ = doRequest(t, "POST", baseURL+"/dev/start", "tok", `{"app":"agent","surface":"web-reload"}`)
	if status != 400 {
		t.Fatalf("agent (stack=go): expected 400, got %d", status)
	}

	// Web app requested from Hot Reload → 400 (reverse gate).
	status, _ = doRequest(t, "POST", baseURL+"/dev/start", "tok", `{"app":"web-dashboard","surface":"hot-reload"}`)
	if status != 400 {
		t.Fatalf("web-dashboard from hot-reload: expected 400, got %d", status)
	}
}

// TestWebReload_DevStartFallbackSurfaceGating covers the no-workspace-
// manifest path: the dashboard's project picker fallback POSTs framework
// + workDir without an `app` key. The gate must still reject mobile
// frameworks from web-reload (and vice versa) so a Web Reload click
// can't accidentally start Metro for an Expo project.
func TestWebReload_DevStartFallbackSurfaceGating(t *testing.T) {
	root := t.TempDir()
	tm := NewTaskManager(root, nil, defaultRunner)
	baseURL, cancel := startTestServer(t, "tok", tm)
	defer cancel()
	if srv := currentTestHTTPServer; srv != nil && srv.devServerMgr == nil {
		srv.devServerMgr = NewDevServerManager()
	}

	// A REAL directory. /dev/start now rejects a workDir that does not exist
	// (404) before it reaches the surface gate, so the old "/tmp/whatever"
	// fixture stopped exercising the thing this test is named for — it asserted
	// 400 and got the 404. The existence check is correct and worth keeping;
	// the fixture was the stale part.
	workDir := root

	// Pure react-native (mobile) requested from web-reload → 400.
	status, body := doRequest(t, "POST", baseURL+"/dev/start", "tok",
		`{"framework":"react-native","workDir":"`+workDir+`","surface":"web-reload"}`)
	if status != 400 {
		t.Fatalf("react-native + web-reload: expected 400, got %d (body=%v)", status, body)
	}
	if body["error"] == nil {
		t.Fatalf("expected error message in 400 response: %v", body)
	}

	// Vite (web) requested from hot-reload → 400 (reverse gate).
	status, _ = doRequest(t, "POST", baseURL+"/dev/start", "tok",
		`{"framework":"vite","workDir":"`+workDir+`","surface":"hot-reload"}`)
	if status != 400 {
		t.Fatalf("vite + hot-reload: expected 400, got %d", status)
	}

	// Expo is intentionally classified as Mobile in
	// FrameworkToDevServerKind because mobile-first projects (sfmg /
	// talos / etc.) shouldn't appear in Web Reload's iframe surface —
	// even though Expo can technically serve a web build. The dedicated
	// "expo-web" framework string routes to Web for projects whose
	// primary target IS the browser.
	status, body = doRequest(t, "POST", baseURL+"/dev/start", "tok",
		`{"framework":"expo","workDir":"`+workDir+`","surface":"web-reload"}`)
	if status != 400 {
		t.Fatalf("expo + web-reload: expected 400 (Expo is mobile-first), got %d (body=%v)", status, body)
	}

	// expo-web is the explicit web target — must pass the gate.
	status, body = doRequest(t, "POST", baseURL+"/dev/start", "tok",
		`{"framework":"expo-web","workDir":"`+workDir+`","surface":"web-reload"}`)
	if status == 400 {
		if msg, _ := body["error"].(string); msg != "" &&
			(containsCI(msg, "mobile-only") || containsCI(msg, "web-only")) {
			t.Fatalf("expo-web from web-reload should not hit surface gate, got %v", body)
		}
	}
}

func containsCI(s, sub string) bool {
	if len(sub) == 0 {
		return true
	}
	S := []byte(s)
	for i := 0; i+len(sub) <= len(S); i++ {
		match := true
		for j := 0; j < len(sub); j++ {
			a := S[i+j]
			b := sub[j]
			if 'A' <= a && a <= 'Z' {
				a += 'a' - 'A'
			}
			if 'A' <= b && b <= 'Z' {
				b += 'a' - 'A'
			}
			if a != b {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

func TestWebReload_WorkspaceRootFromTaskMgr(t *testing.T) {
	// resolveWorkspaceRoot should fall back to taskMgr.workDir when
	// no ?root= query param is given. This is the behaviour the web
	// dashboard relies on — it never has to know the machine's CWD.
	root := writeTestManifest(t)
	tm := NewTaskManager(root, nil, defaultRunner)
	baseURL, cancel := startTestServer(t, "tok", tm)
	defer cancel()

	status, body := doRequest(t, "GET", baseURL+"/workspace", "tok", "")
	if status != 200 {
		t.Fatalf("GET /workspace → %d", status)
	}
	if body["root"] != root {
		t.Fatalf("resolved root mismatch: got %v want %s", body["root"], root)
	}
}
