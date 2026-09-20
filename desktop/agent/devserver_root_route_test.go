package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"
)

func expoBrowserRouteTestManager(t *testing.T, upstream *httptest.Server) *DevServerManager {
	t.Helper()
	u, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil {
		t.Fatal(err)
	}
	expo := &ExpoDevServer{devMode: "dev-client", webPort: port}
	expo.name = "expo"
	expo.port = 8081
	expo.running = true
	expo.workDir = "/workspace/yaver"
	return &DevServerManager{active: &devServerSession{
		server: expo,
		ctx:    context.Background(), cancel: func() {}, releasePort: func() {},
	}}
}

// Regression for the 2026-09-05 Dogfood incident: /dev-web/ and the entry
// bundle returned 200, React mounted, then the router's visible "/" refreshed
// through the agent mux and returned the bare 19-byte Go 404.
func TestBrowserPreviewLogicalRootSurvivesRefresh(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<html><head></head><body><div id="root"><p>Yaver</p></div></body></html>`))
	}))
	defer upstream.Close()

	s := &HTTPServer{devServerMgr: expoBrowserRouteTestManager(t, upstream)}
	rec := httptest.NewRecorder()
	s.handleBrowserPreviewRoot(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("logical preview root returned %d: %s", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("X-Yaver-Preview-Route") != "logical-root" {
		t.Fatalf("logical-root diagnostic header missing: %#v", rec.Header())
	}
	if !strings.Contains(rec.Body.String(), "Yaver") {
		t.Fatalf("logical root did not reach the browser preview: %s", rec.Body.String())
	}
}

func TestBrowserPreviewLogicalRootFailsClosedWithoutPreview(t *testing.T) {
	rec := httptest.NewRecorder()
	(&HTTPServer{devServerMgr: NewDevServerManager()}).handleBrowserPreviewRoot(
		rec, httptest.NewRequest(http.MethodGet, "/", nil),
	)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("inactive preview root = %d, want 404", rec.Code)
	}
}

func TestBrowserPreviewLogicalRootRejectsMutations(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("POST must never reach the guest preview root")
	}))
	defer upstream.Close()
	rec := httptest.NewRecorder()
	(&HTTPServer{devServerMgr: expoBrowserRouteTestManager(t, upstream)}).handleBrowserPreviewRoot(
		rec, httptest.NewRequest(http.MethodPost, "/guest-action", nil),
	)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("POST logical root = %d, want 404", rec.Code)
	}
}

// Runtime-created CSS is its own resource loader. Patching fetch, XHR, script,
// and img URLs does not affect @font-face, background-image, cursor, masks, or
// any other url(...) inside a <style> element. Expo vector icons expose this
// generally: the app and text render, while every icon is blank because
// expo-font injects a root-relative font URL after the preview bootstrap has
// hidden /d/<device>/dev-web/ from the guest router.
//
// Use a relay-shaped outer mount and a real browser. A request that escapes to
// /assets never reaches the agent; a correctly rebased CSS URL traverses
// /d/device-1/dev-web/assets and arrives at the preview process as /assets.
func TestBrowserPreviewRuntimeCSSAssetsStayInsideRelayLane(t *testing.T) {
	skipWithoutChrome(t)
	assetRequested := make(chan string, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/assets/icons.woff2" {
			select {
			case assetRequested <- r.URL.Path:
			default:
			}
			w.Header().Set("Content-Type", "font/woff2")
			_, _ = w.Write([]byte("not-a-real-font-request-path-is-the-contract"))
			return
		}
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<!doctype html><html><head></head><body>
<span id="icon">x</span><script>
var style=document.createElement("style");
style.appendChild(document.createTextNode('@font-face{font-family:DogfoodProbe;src:url("/assets/icons.woff2")}#icon{font-family:DogfoodProbe}'));
document.head.appendChild(style);
document.fonts.load("16px DogfoodProbe").catch(function(){});
</script></body></html>`))
	}))
	defer upstream.Close()

	s := &HTTPServer{devServerMgr: expoBrowserRouteTestManager(t, upstream)}
	agentMux := http.NewServeMux()
	agentMux.HandleFunc("/dev-web/", s.handleDevWebProxy)
	agentMux.HandleFunc("/", s.handleBrowserPreviewRoot)
	relay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		const mount = "/d/device-1"
		if !strings.HasPrefix(r.URL.Path, mount+"/") {
			http.NotFound(w, r)
			return
		}
		clone := r.Clone(r.Context())
		clone.URL.Path = strings.TrimPrefix(r.URL.Path, mount)
		agentMux.ServeHTTP(w, clone)
	}))
	defer relay.Close()

	bm := NewBrowserManager()
	defer bm.Stop()
	const sessionID = "dogfood-runtime-css-assets"
	if err := bm.OpenSession(sessionID, false); err != nil {
		t.Skipf("could not open browser here: %v", err)
	}
	defer func() { _ = bm.CloseSession(sessionID) }()
	if _, err := bm.Navigate(sessionID, relay.URL+"/d/device-1/dev-web/"); err != nil {
		t.Fatalf("navigate relay-shaped preview: %v", err)
	}
	cssContract, err := bm.Evaluate(sessionID, `(function(){
var c=window.__yaverPreviewCSS;
if(typeof c!=="function")return false;
var root=c('a{background:url("/assets/background.png")}');
var imported=c('@import "/styles/theme.css";');
var data='a{background:url(data:image/png;base64,AAAA)}';
var external='a{background:url("https://cdn.example.invalid/image.png")}';
return root.indexOf('/d/device-1/dev-web/assets/background.png')!==-1 &&
 imported.indexOf('/d/device-1/dev-web/styles/theme.css')!==-1 &&
 c(data)===data && c(external)===external;
})()`)
	if err != nil {
		t.Fatalf("evaluate runtime CSS contract: %v", err)
	}
	if cssContract != true {
		t.Fatalf("runtime CSS URL contract = %v, want true", cssContract)
	}

	select {
	case got := <-assetRequested:
		if got != "/assets/icons.woff2" {
			t.Fatalf("upstream asset path = %q", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("runtime CSS asset escaped the relay-scoped preview lane; upstream never received /assets/icons.woff2")
	}
}
