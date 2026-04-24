package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type guestShareStubDevServer struct {
	status DevServerStatus
}

func (s *guestShareStubDevServer) Name() string               { return "stub" }
func (s *guestShareStubDevServer) Detect(workDir string) bool { return false }
func (s *guestShareStubDevServer) Start(ctx context.Context, opts DevServerOpts) error {
	return nil
}
func (s *guestShareStubDevServer) Stop() error                                    { return nil }
func (s *guestShareStubDevServer) Port() int                                      { return s.status.Port }
func (s *guestShareStubDevServer) BundleURL(platform string) string               { return s.status.BundleURL }
func (s *guestShareStubDevServer) SupportsHotReload() bool                        { return true }
func (s *guestShareStubDevServer) Reload() error                                  { return nil }
func (s *guestShareStubDevServer) PreStart(name string, port int, workDir string) {}
func (s *guestShareStubDevServer) Status() DevServerStatus                        { return s.status }
func (s *guestShareStubDevServer) Kind() DevServerKind                            { return DevServerKindMobile }

type guestShareFixture struct {
	baseURL    string
	cancel     context.CancelFunc
	server     *HTTPServer
	taskMgr    *TaskManager
	sfmgDir    string
	talosDir   string
	hostToken  string
	guestToken string
}

func startGuestShareFixture(t *testing.T, requireIsolation bool) *guestShareFixture {
	t.Helper()

	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)

	repoRoot := t.TempDir()
	sfmgDir := filepath.Join(repoRoot, "sfmg")
	talosDir := filepath.Join(repoRoot, "talos")
	for _, dir := range []string{sfmgDir, talosDir} {
		if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}

	projectsPath, err := projectsFilePath()
	if err != nil {
		t.Fatalf("projectsFilePath: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(projectsPath), 0o755); err != nil {
		t.Fatalf("mkdir projects dir: %v", err)
	}
	projectsBody := fmt.Sprintf("## Projects\n### %s\n### %s\n", sfmgDir, talosDir)
	if err := os.WriteFile(projectsPath, []byte(projectsBody), 0o644); err != nil {
		t.Fatalf("write PROJECTS.md: %v", err)
	}

	taskMgr := NewTaskManager(repoRoot, nil, defaultTestRunner())
	taskMgr.DummyMode = true

	port := getFreePort(t)
	hostToken := "host-agent-token"
	guestToken := "guest-session-token"

	srv := NewHTTPServer(port, hostToken, "host-user", "hetzner-talos-1", "", "hetzner-talos", taskMgr)
	srv.execMgr = NewExecManager(taskMgr.workDir, nil)
	srv.devServerMgr = NewDevServerManager()
	srv.guestConfigMgr = NewGuestConfigManager(t.TempDir())

	shareAllDevices := false
	requireIsolationPtr := requireIsolation
	srv.guestConfigMgr.UpdateConfigs([]GuestConfig{{
		GuestUserID:      "guest-user",
		GuestEmail:       "guest@example.com",
		GuestName:        "Guest User",
		Scope:            GuestScopeFull,
		AllowedProjects:  []string{"sfmg"},
		AllowedRunners:   []string{"codex"},
		ShareAllDevices:  &shareAllDevices,
		DeviceIDs:        []string{"hetzner-talos-1"},
		RequireIsolation: &requireIsolationPtr,
	}})
	srv.guestConfigMgr.SetProjectAccess("guest-user", []string{"sfmg"})
	srv.guestUserIDs = []string{"guest-user"}
	srv.tokenCache.Store(guestToken, &cachedTokenInfo{
		userID:   "guest-user",
		isSdk:    false,
		storedAt: time.Now(),
	})

	ctx, cancel := context.WithCancel(context.Background())
	go func() { _ = srv.Start(ctx) }()

	baseURL := fmt.Sprintf("http://127.0.0.1:%d", port)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(baseURL + "/health")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return &guestShareFixture{
					baseURL:    baseURL,
					cancel:     cancel,
					server:     srv,
					taskMgr:    taskMgr,
					sfmgDir:    sfmgDir,
					talosDir:   talosDir,
					hostToken:  hostToken,
					guestToken: guestToken,
				}
			}
		}
		time.Sleep(50 * time.Millisecond)
	}

	cancel()
	t.Fatalf("guest share test server did not start within 3s")
	return nil
}

func TestGuestShareLinuxStack_ConfigAndTaskScoping(t *testing.T) {
	fx := startGuestShareFixture(t, false)
	defer fx.cancel()
	defer fx.taskMgr.Shutdown()

	status, body := doRequest(t, "GET", fx.baseURL+"/guests/config", fx.hostToken, "")
	if status != http.StatusOK {
		t.Fatalf("host config fetch status = %d, body = %#v", status, body)
	}
	configs, ok := body["configs"].([]interface{})
	if !ok || len(configs) != 1 {
		t.Fatalf("unexpected configs payload: %#v", body["configs"])
	}
	cfg, ok := configs[0].(map[string]interface{})
	if !ok {
		t.Fatalf("config entry type = %T", configs[0])
	}
	if got := cfg["guestEmail"]; got != "guest@example.com" {
		t.Fatalf("guestEmail = %#v, want guest@example.com", got)
	}
	if got := cfg["allowedProjects"]; fmt.Sprint(got) != "[sfmg]" {
		t.Fatalf("allowedProjects = %#v, want [sfmg]", got)
	}
	if got := cfg["deviceIds"]; fmt.Sprint(got) != "[hetzner-talos-1]" {
		t.Fatalf("deviceIds = %#v, want [hetzner-talos-1]", got)
	}
	if got := cfg["requireIsolation"]; got != false {
		t.Fatalf("requireIsolation = %#v, want false", got)
	}

	status, body = doRequest(t, "GET", fx.baseURL+"/agent/runners", fx.guestToken, "")
	if status != http.StatusOK {
		t.Fatalf("guest runners status = %d, body = %#v", status, body)
	}
	if got := body["default"]; got != "codex" {
		t.Fatalf("guest runners default = %#v, want codex", got)
	}
	runners, ok := body["runners"].([]interface{})
	if !ok || len(runners) != 1 {
		t.Fatalf("guest runners payload = %#v, want exactly one runner", body["runners"])
	}
	runner, ok := runners[0].(map[string]interface{})
	if !ok {
		t.Fatalf("runner entry type = %T", runners[0])
	}
	if got := runner["id"]; got != "codex" {
		t.Fatalf("guest runner id = %#v, want codex", got)
	}

	negativeBodies := []struct {
		name string
		body string
		code int
	}{
		{
			name: "missing projectName",
			body: `{"title":"fix sfmg","runner":"codex","source":"mobile"}`,
			code: http.StatusForbidden,
		},
		{
			name: "disallowed project",
			body: `{"title":"fix talos","runner":"codex","source":"mobile","projectName":"talos"}`,
			code: http.StatusForbidden,
		},
		{
			name: "disallowed runner",
			body: `{"title":"fix sfmg","runner":"claude","source":"mobile","projectName":"sfmg"}`,
			code: http.StatusForbidden,
		},
		{
			name: "default runner not allowed",
			body: `{"title":"fix sfmg","source":"mobile","projectName":"sfmg"}`,
			code: http.StatusForbidden,
		},
	}
	for _, tc := range negativeBodies {
		t.Run(tc.name, func(t *testing.T) {
			status, resp := doRequest(t, "POST", fx.baseURL+"/tasks", fx.guestToken, tc.body)
			if status != tc.code {
				t.Fatalf("status = %d, want %d, body = %#v", status, tc.code, resp)
			}
		})
	}

	status, body = doRequest(t, "POST", fx.baseURL+"/tasks", fx.guestToken, `{"title":"fix sfmg login","runner":"codex","source":"mobile","projectName":"sfmg","workDir":"/etc"}`)
	if status != http.StatusCreated {
		t.Fatalf("guest task create status = %d, body = %#v", status, body)
	}
	taskID, _ := body["taskId"].(string)
	if taskID == "" {
		t.Fatalf("missing taskId in response: %#v", body)
	}
	task, ok := fx.taskMgr.GetTask(taskID)
	if !ok {
		t.Fatalf("task %s not found", taskID)
	}
	if task.WorkDir != fx.sfmgDir {
		t.Fatalf("task.WorkDir = %q, want %q", task.WorkDir, fx.sfmgDir)
	}
	if task.GuestUserID != "guest-user" {
		t.Fatalf("task.GuestUserID = %q, want guest-user", task.GuestUserID)
	}
	if task.GuestRequireIsolation {
		t.Fatalf("task.GuestRequireIsolation = true, want false")
	}
}

func TestGuestShareLinuxStack_GuestCannotManageGuestConfig(t *testing.T) {
	fx := startGuestShareFixture(t, true)
	defer fx.cancel()
	defer fx.taskMgr.Shutdown()

	status, body := doRequest(t, "GET", fx.baseURL+"/guests/config", fx.guestToken, "")
	if status != http.StatusForbidden {
		t.Fatalf("guest /guests/config status = %d, body = %#v", status, body)
	}
}

func TestGuestShareLinuxStack_DevEndpointAuthAllowsGuestReload(t *testing.T) {
	fx := startGuestShareFixture(t, true)
	defer fx.cancel()
	defer fx.taskMgr.Shutdown()

	guestStartReq := httptest.NewRequest(http.MethodPost, "/dev/start", bytes.NewReader([]byte(`{}`)))
	guestStartReq.RemoteAddr = "198.51.100.24:45678"
	guestStartReq.Header.Set("Authorization", "Bearer "+fx.guestToken)
	guestStartReq.Header.Set("Content-Type", "application/json")
	guestStartRec := httptest.NewRecorder()
	fx.server.authOrLocalhost(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})(guestStartRec, guestStartReq)
	if guestStartRec.Code != http.StatusNoContent {
		t.Fatalf("guest /dev/start auth path status = %d, body = %s", guestStartRec.Code, guestStartRec.Body.String())
	}

	guestReloadReq := httptest.NewRequest(http.MethodPost, "/dev/reload", nil)
	guestReloadReq.RemoteAddr = "198.51.100.24:45678"
	guestReloadReq.Header.Set("Authorization", "Bearer "+fx.guestToken)
	guestReloadRec := httptest.NewRecorder()
	fx.server.authSDKOrGuest(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})(guestReloadRec, guestReloadReq)
	if guestReloadRec.Code != http.StatusNoContent {
		t.Fatalf("guest /dev/reload auth path status = %d, body = %s", guestReloadRec.Code, guestReloadRec.Body.String())
	}

	hostReloadReq := httptest.NewRequest(http.MethodPost, "/dev/reload", nil)
	hostReloadReq.Header.Set("Authorization", "Bearer "+fx.hostToken)
	hostReloadRec := httptest.NewRecorder()
	fx.server.authSDKOrGuest(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})(hostReloadRec, hostReloadReq)
	if hostReloadRec.Code != http.StatusNoContent {
		t.Fatalf("host /dev/reload auth path status = %d, body = %s", hostReloadRec.Code, hostReloadRec.Body.String())
	}
}

func TestGuestShareLinuxStack_GuestReloadBlockedForUnsharedActiveProject(t *testing.T) {
	fx := startGuestShareFixture(t, false)
	defer fx.cancel()
	defer fx.taskMgr.Shutdown()

	fx.server.devServerMgr = &DevServerManager{
		active: &devServerSession{
			server: &guestShareStubDevServer{status: DevServerStatus{
				Framework: "vite",
				Running:   true,
				WorkDir:   fx.talosDir,
				Port:      5173,
			}},
		},
	}

	req := httptest.NewRequest(http.MethodPost, "/dev/reload", nil)
	req.Header.Set("Authorization", "Bearer "+fx.guestToken)
	rec := httptest.NewRecorder()
	fx.server.authSDKOrGuest(fx.server.handleDevServerReload)(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestGuestShareLinuxStack_GuestResolveDevWorkDirPinsProject(t *testing.T) {
	fx := startGuestShareFixture(t, false)
	defer fx.cancel()
	defer fx.taskMgr.Shutdown()

	req := httptest.NewRequest(http.MethodPost, "/dev/start", nil)
	req.Header.Set("X-Yaver-GuestUserID", "guest-user")

	workDir, err := fx.server.guestResolveDevWorkDir(req, "sfmg", "/etc")
	if err != nil {
		t.Fatalf("guestResolveDevWorkDir err = %v", err)
	}
	if workDir != fx.sfmgDir {
		t.Fatalf("workDir = %q, want %q", workDir, fx.sfmgDir)
	}

	_, err = fx.server.guestResolveDevWorkDir(req, "", "/etc")
	if err == nil {
		t.Fatalf("expected missing projectName error for restricted guest")
	}
}

func TestGuestShareLinuxStack_TaskAPIFailsClosedWhenIsolationRequiredWithoutDocker(t *testing.T) {
	fx := startGuestShareFixture(t, true)
	defer fx.cancel()
	defer fx.taskMgr.Shutdown()

	payload := map[string]interface{}{
		"title":       "fix sfmg button",
		"runner":      "codex",
		"source":      "mobile",
		"projectName": "sfmg",
	}
	buf, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/tasks", bytes.NewReader(buf))
	req.Header.Set("Authorization", "Bearer "+fx.guestToken)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	fx.server.auth(fx.server.createTask)(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var resp map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got, _ := resp["error"].(string); got == "" {
		t.Fatalf("expected isolation failure error, body = %#v", resp)
	}
}

func TestGuestShareLinuxStack_IsolatedGuestCannotStartHostDevServer(t *testing.T) {
	fx := startGuestShareFixture(t, true)
	defer fx.cancel()
	defer fx.taskMgr.Shutdown()

	req := httptest.NewRequest(http.MethodPost, "/dev/start", bytes.NewReader([]byte(`{"projectName":"sfmg","framework":"vite"}`)))
	req.RemoteAddr = "198.51.100.24:45678"
	req.Header.Set("Authorization", "Bearer "+fx.guestToken)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	fx.server.auth(fx.server.handleDevServerStart)(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestGuestShareLinuxStack_IsolatedGuestCanReloadAllowedActiveProject(t *testing.T) {
	fx := startGuestShareFixture(t, true)
	defer fx.cancel()
	defer fx.taskMgr.Shutdown()

	fx.server.devServerMgr = &DevServerManager{
		active: &devServerSession{
			server: &guestShareStubDevServer{status: DevServerStatus{
				Framework: "vite",
				Running:   true,
				WorkDir:   fx.sfmgDir,
				Port:      5173,
			}},
		},
	}

	req := httptest.NewRequest(http.MethodPost, "/dev/reload", nil)
	req.Header.Set("Authorization", "Bearer "+fx.guestToken)
	rec := httptest.NewRecorder()
	fx.server.authSDKOrGuest(fx.server.handleDevServerReload)(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestGuestShareLinuxStack_IsolatedGuestCannotBuildNativeBundle(t *testing.T) {
	fx := startGuestShareFixture(t, true)
	defer fx.cancel()
	defer fx.taskMgr.Shutdown()

	fx.server.devServerMgr = &DevServerManager{}

	req := httptest.NewRequest(http.MethodPost, "/dev/build-native", bytes.NewReader([]byte(`{"projectName":"sfmg","platform":"ios"}`)))
	req.Header.Set("Authorization", "Bearer "+fx.guestToken)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	fx.server.authSDKOrGuest(fx.server.handleBuildNativeBundle)(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}
