package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeTestWorkspace writes a minimal yaver.workspace.yaml alongside
// an empty app directory, so resolveAppFromWorkspaceFull can find the
// app when /deploy/ship is called during tests.
func writeTestWorkspace(t *testing.T, dir string, appName string, stack string, appPath string) {
	t.Helper()
	yaml := `version: 1
name: testws
workspace:
  root: .
apps:
  - name: ` + appName + `
    path: ` + appPath + `
    stack: ` + stack + `
`
	if err := os.WriteFile(filepath.Join(dir, "yaver.workspace.yaml"), []byte(yaml), 0644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(dir, appPath), 0755); err != nil {
		t.Fatalf("mkdir app: %v", err)
	}
}

// shipRequest posts to /deploy/ship and reads the SSE response into a
// slice of (event, data) tuples. 5-second read cap so a stuck test
// doesn't hang CI forever.
type sseEvent struct {
	Event string
	Data  map[string]interface{}
}

func readSSE(body io.Reader) []sseEvent {
	out := []sseEvent{}
	buf, _ := io.ReadAll(body)
	blocks := strings.Split(string(buf), "\n\n")
	for _, b := range blocks {
		b = strings.TrimSpace(b)
		if b == "" {
			continue
		}
		var ev sseEvent
		for _, line := range strings.Split(b, "\n") {
			if strings.HasPrefix(line, "event: ") {
				ev.Event = strings.TrimPrefix(line, "event: ")
			} else if strings.HasPrefix(line, "data: ") {
				raw := strings.TrimPrefix(line, "data: ")
				_ = json.Unmarshal([]byte(raw), &ev.Data)
			}
		}
		if ev.Event != "" {
			out = append(out, ev)
		}
	}
	return out
}

func TestDeployShipRejectsMissingAppTarget(t *testing.T) {
	srv := &HTTPServer{token: "t"}
	req := httptest.NewRequest("POST", "/deploy/ship", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer t")
	w := httptest.NewRecorder()
	srv.handleDeployShip(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestDeployShipMethodNotAllowed(t *testing.T) {
	srv := &HTTPServer{}
	req := httptest.NewRequest("GET", "/deploy/ship", nil)
	w := httptest.NewRecorder()
	srv.handleDeployShip(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", w.Code)
	}
}

func TestDeployShipGuestCannotOverrideStack(t *testing.T) {
	srv := &HTTPServer{token: "t"}
	body := `{"app":"web","target":"cloudflare","stack":"evil","path":"/tmp"}`
	req := httptest.NewRequest("POST", "/deploy/ship", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer t")
	req.Header.Set("X-Yaver-Guest", "true")
	req.Header.Set("X-Yaver-GuestUserID", "g1")
	w := httptest.NewRecorder()
	srv.handleDeployShip(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("guest override must be 403, got %d: %s", w.Code, w.Body.String())
	}
}

func TestDeployShipGuestProjectRestriction(t *testing.T) {
	// Run in a temp dir with a workspace manifest declaring "web".
	tmp := t.TempDir()
	writeTestWorkspace(t, tmp, "web", "nextjs", "app")
	// Swap cwd so resolveAppFromWorkspaceFull finds the manifest.
	orig, _ := os.Getwd()
	defer os.Chdir(orig)
	_ = os.Chdir(tmp)

	// Guest is only allowed project "otherapp" — requesting "web"
	// must be rejected.
	mgr := NewGuestConfigManager(t.TempDir())
	mgr.UpdateConfigs([]GuestConfig{
		{
			GuestUserID:     "guestA",
			Scope:           GuestScopeDeploy,
			AllowedProjects: []string{"otherapp"},
		},
	})
	srv := &HTTPServer{token: "t", guestConfigMgr: mgr}

	body := `{"app":"web","target":"cloudflare"}`
	req := httptest.NewRequest("POST", "/deploy/ship", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer t")
	req.Header.Set("X-Yaver-Guest", "true")
	req.Header.Set("X-Yaver-GuestUserID", "guestA")
	w := httptest.NewRecorder()
	srv.handleDeployShip(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("out-of-scope project must be 403, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "not authorised for this project") {
		t.Fatalf("wrong error body: %s", w.Body.String())
	}
}

func TestDeployShipEndToEndWithEchoTemplate(t *testing.T) {
	// End-to-end: register a dummy template that just echoes a vault
	// value, spawn it, and verify the SSE stream carries the expected
	// `exit` event with code=0.
	//
	// Strategy: install a temporary template under ("test-stack",
	// "test-target") whose body is `echo "$APP_SECRET"`. Restore the
	// original templates at the end of the test.
	orig := deployTemplates["test-stack:test-target"]
	deployTemplates["test-stack:test-target"] = deployTemplate{
		Stack:       "test-stack",
		Target:      "test-target",
		Description: "test-only template",
		Body:        `cd "{{.Path}}" && echo "got=$APP_SECRET"` + "\n",
	}
	defer func() {
		if orig.Stack == "" {
			delete(deployTemplates, "test-stack:test-target")
		} else {
			deployTemplates["test-stack:test-target"] = orig
		}
	}()

	// The doctor also needs to know about this target or it'll 409.
	origTarget := buildTargets["test-target"]
	buildTargets["test-target"] = buildTarget{
		Name: "test-target",
		// Probe a tool that always exists on every dev box.
		Tools:   []buildTool{{Name: "bash", VersionFlag: "--version", Required: true}},
		Secrets: nil,
	}
	defer func() {
		if origTarget.Name == "" {
			delete(buildTargets, "test-target")
		} else {
			buildTargets["test-target"] = origTarget
		}
	}()

	// Fresh vault + workspace in a temp dir.
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	// Scrub PATH so the generated script's `yaver doctor build`
	// preflight skips: the test binary's own `yaver` is unsigned and
	// macOS SIGKILLs it, which would make the deploy exit 1 for the
	// wrong reason. /bin + /usr/bin has everything bash actually needs.
	t.Setenv("PATH", "/bin:/usr/bin")
	os.MkdirAll(filepath.Join(tmp, ".yaver"), 0700)
	vs, err := NewVaultStoreWithDevice("p", "test-dev")
	if err != nil {
		t.Fatalf("vault: %v", err)
	}
	if err := vs.Set(VaultEntry{Name: "APP_SECRET", Project: "testapp", Value: "hello-from-vault"}); err != nil {
		t.Fatalf("vault set: %v", err)
	}
	writeTestWorkspace(t, tmp, "testapp", "test-stack", "src")
	orig2, _ := os.Getwd()
	defer os.Chdir(orig2)
	_ = os.Chdir(tmp)

	srv := &HTTPServer{token: "t", vaultStore: vs}

	// SSE needs a real http.Flusher, which httptest.NewRecorder does
	// not provide. Spin up a real loopback server for the E2E stream.
	ts := httptest.NewServer(http.HandlerFunc(srv.handleDeployShip))
	defer ts.Close()

	body := `{"app":"testapp","target":"test-target","timeout_sec":30}`
	req, _ := http.NewRequest("POST", ts.URL, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer t")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /deploy/ship: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200 (SSE OK), got %d: %s", resp.StatusCode, string(raw))
	}
	events := readSSE(resp.Body)
	if len(events) < 2 {
		t.Fatalf("expected at least meta + exit events, got %d: %+v", len(events), events)
	}
	seenMeta := false
	seenOK := false
	seenVaultValue := false
	for _, e := range events {
		switch e.Event {
		case "meta":
			seenMeta = true
		case "line":
			if text, _ := e.Data["text"].(string); strings.Contains(text, "got=hello-from-vault") {
				seenVaultValue = true
			}
		case "exit":
			if code, _ := e.Data["code"].(float64); code == 0 {
				seenOK = true
			}
		}
	}
	if !seenMeta {
		t.Error("missing meta event")
	}
	if !seenOK {
		t.Error("missing exit event with code=0")
	}
	if !seenVaultValue {
		t.Error("vault value did not flow into subprocess env")
	}
}

func TestBuildDeployShipEnvGuestWhitelist(t *testing.T) {
	// Pollute the parent env with a sensitive-looking var; the guest
	// env must NOT include it.
	t.Setenv("GITHUB_TOKEN", "ghp_leak_this")
	t.Setenv("PATH", "/usr/bin")
	t.Setenv("HOME", "/tmp")

	tmp := t.TempDir()
	os.MkdirAll(filepath.Join(tmp, ".yaver"), 0700)
	t.Setenv("HOME", tmp) // redirect vault path
	vs, _ := NewVaultStore("p")
	_ = vs.Set(VaultEntry{Name: "CLOUDFLARE_API_TOKEN", Project: "web", Value: "vault-token"})

	env := buildDeployShipEnv(vs, "web", true /* isGuest */)

	hasGitHubLeak := false
	hasVaultToken := false
	for _, kv := range env {
		if strings.HasPrefix(kv, "GITHUB_TOKEN=") {
			hasGitHubLeak = true
		}
		if kv == "CLOUDFLARE_API_TOKEN=vault-token" {
			hasVaultToken = true
		}
	}
	if hasGitHubLeak {
		t.Error("guest subprocess env must NOT include GITHUB_TOKEN from parent shell")
	}
	if !hasVaultToken {
		t.Error("guest subprocess env must include vault-supplied token")
	}
}

func TestGuestScopeDeployAllowList(t *testing.T) {
	// A scope=deploy guest should be blocked from /tasks but allowed
	// on /deploy/ship.
	if isGuestAllowedPathForScope("/tasks", GuestScopeDeploy) {
		t.Error("scope=deploy must NOT allow /tasks")
	}
	if !isGuestAllowedPathForScope("/deploy/ship", GuestScopeDeploy) {
		t.Error("scope=deploy must allow /deploy/ship")
	}
	if !isGuestAllowedPathForScope("/doctor/build", GuestScopeDeploy) {
		t.Error("scope=deploy must allow /doctor/build")
	}
	if isGuestAllowedPathForScope("/vault/list", GuestScopeDeploy) {
		t.Error("scope=deploy must NOT allow /vault/list")
	}
	// Scope=full keeps the new endpoints accessible.
	if !isGuestAllowedPathForScope("/deploy/ship", GuestScopeFull) {
		t.Error("scope=full must allow /deploy/ship")
	}
	// Feedback-only stays tight.
	if isGuestAllowedPathForScope("/deploy/ship", GuestScopeFeedbackOnly) {
		t.Error("scope=feedback-only must NOT allow /deploy/ship")
	}
}
