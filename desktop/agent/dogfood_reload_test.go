package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type dogfoodReloadTestServer struct{ workDir string }

func (s *dogfoodReloadTestServer) Name() string                               { return "dogfood-test" }
func (s *dogfoodReloadTestServer) Detect(string) bool                         { return true }
func (s *dogfoodReloadTestServer) Start(context.Context, DevServerOpts) error { return nil }
func (s *dogfoodReloadTestServer) Stop() error                                { return nil }
func (s *dogfoodReloadTestServer) Port() int                                  { return 4173 }
func (s *dogfoodReloadTestServer) BundleURL(string) string                    { return "/dev/" }
func (s *dogfoodReloadTestServer) SupportsHotReload() bool                    { return true }
func (s *dogfoodReloadTestServer) Reload() error                              { return nil }
func (s *dogfoodReloadTestServer) PreStart(string, int, string)               {}
func (s *dogfoodReloadTestServer) Kind() DevServerKind                        { return DevServerKindWeb }
func (s *dogfoodReloadTestServer) Status() DevServerStatus {
	return DevServerStatus{Framework: "vite", Running: true, Serving: true, Port: 4173, WorkDir: s.workDir}
}

func TestSameDogfoodCheckoutRequiresExactPath(t *testing.T) {
	if !sameDogfoodCheckout("./project", "project") {
		t.Fatal("equivalent checkout paths should match")
	}
	if sameDogfoodCheckout("/tmp/app-a", "/tmp/app-b") {
		t.Fatal("different checkouts must never match")
	}
	if !sameDogfoodCheckout("/tmp/yaver", "/tmp/yaver/mobile") {
		t.Fatal("a checkout root must match its direct mobile workspace")
	}
	if sameDogfoodCheckout("/tmp/yaver", "/tmp/yaver/mobile/nested") || sameDogfoodCheckout("/tmp/yaver", "/tmp/mobile") {
		t.Fatal("nested and sibling workspaces must not match the selected checkout")
	}
	if sameDogfoodCheckout("", "/tmp/app") {
		t.Fatal("an empty requested checkout must not match an active project")
	}
}

func TestReloadAppNamedTargetNeverFallsBackToBroadcast(t *testing.T) {
	mgr, err := NewBlackBoxManager()
	if err != nil {
		t.Fatalf("NewBlackBoxManager: %v", err)
	}
	other := mgr.GetOrCreateSession("other-app", "ios", "OtherApp")
	commands := other.SubscribeCommands()
	defer other.UnsubscribeCommands(commands)

	req := httptest.NewRequest(http.MethodPost, "/dev/reload-app", strings.NewReader(`{"mode":"dev","projectPath":"/workspace/sfmg","targetDeviceId":"selected-app"}`))
	rec := httptest.NewRecorder()
	(&HTTPServer{blackboxMgr: mgr}).handleReloadApp(rec, req)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "DOGFOOD_TARGET_NOT_CONNECTED") {
		t.Fatalf("missing fail-closed target response, got %d: %s", rec.Code, rec.Body.String())
	}
	select {
	case command := <-commands:
		t.Fatalf("named-target reload leaked to another app: %#v", command)
	case <-time.After(20 * time.Millisecond):
		// The guard works: no broadcast fallback occurred.
	}
}

func TestDogfoodBrowserNamedTargetNeverFallsBackToBroadcast(t *testing.T) {
	mgr, err := NewBlackBoxManager()
	if err != nil {
		t.Fatalf("NewBlackBoxManager: %v", err)
	}
	other := mgr.GetOrCreateSession("other-app", "ios", "OtherApp")
	commands := other.SubscribeCommands()
	defer other.UnsubscribeCommands(commands)

	dev := NewDevServerManager()
	dev.active = &devServerSession{
		server: &dogfoodReloadTestServer{workDir: "/workspace/sfmg"},
		ctx:    context.Background(), cancel: func() {}, releasePort: func() {},
	}
	req := httptest.NewRequest(http.MethodPost, "/dogfood/reload", strings.NewReader(`{"lane":"browser","mode":"fast","projectPath":"/workspace/sfmg","targetDeviceId":"selected-app"}`))
	rec := httptest.NewRecorder()
	(&HTTPServer{blackboxMgr: mgr, devServerMgr: dev}).handleDogfoodReload(rec, req)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "DOGFOOD_TARGET_NOT_CONNECTED") {
		t.Fatalf("missing fail-closed browser target response, got %d: %s", rec.Code, rec.Body.String())
	}
	select {
	case command := <-commands:
		t.Fatalf("named-target browser reload leaked to another app: %#v", command)
	case <-time.After(20 * time.Millisecond):
	}
}

func TestDogfoodReloadHermesRequiresProjectIdentity(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/dogfood/reload", strings.NewReader(`{"lane":"hermes","mode":"fast"}`))
	rec := httptest.NewRecorder()
	(&HTTPServer{}).handleDogfoodReload(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "DOGFOOD_PROJECT_REQUIRED") {
		t.Fatalf("missing structured project failure: %s", rec.Body.String())
	}
}

func TestDogfoodReloadRequiresExactCheckoutForEveryLane(t *testing.T) {
	for _, lane := range []string{"browser", "hermes", "webrtc"} {
		req := httptest.NewRequest(http.MethodPost, "/dogfood/reload", strings.NewReader(`{"lane":"`+lane+`","mode":"fast"}`))
		rec := httptest.NewRecorder()
		(&HTTPServer{}).handleDogfoodReload(rec, req)
		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "DOGFOOD_PROJECT_REQUIRED") {
			t.Fatalf("%s reload must require the exact checkout, got %d: %s", lane, rec.Code, rec.Body.String())
		}
	}
}

func TestDogfoodReloadWebRTCRetainsSelectedCheckout(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/dogfood/reload", strings.NewReader(`{"lane":"webrtc","runtimeSessionId":"session-1"}`))
	rec := httptest.NewRecorder()
	(&HTTPServer{}).handleDogfoodReload(rec, req)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "DOGFOOD_PROJECT_REQUIRED") {
		t.Fatalf("WebRTC reload must require its selected checkout, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestMCPAdvertisesBranchNeutralDogfoodReload(t *testing.T) {
	tools := (&HTTPServer{}).getMCPToolsList().(map[string]interface{})["tools"].([]map[string]interface{})
	for _, tool := range tools {
		if tool["name"] != "dogfood_reload" {
			continue
		}
		description, _ := tool["description"].(string)
		schema := tool["inputSchema"].(map[string]interface{})
		required := schema["required"].([]string)
		if len(required) != 2 || required[0] != "lane" || required[1] != "project_path" {
			t.Fatalf("dogfood_reload must require lane + exact checkout: %#v", required)
		}
		for _, required := range []string{"current working tree", "never commits", "project_path"} {
			if !strings.Contains(description, required) {
				t.Fatalf("dogfood_reload description must contain %q: %s", required, description)
			}
		}
		return
	}
	t.Fatal("dogfood_reload is not advertised")
}
