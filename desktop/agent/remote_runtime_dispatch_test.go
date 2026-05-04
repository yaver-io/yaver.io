package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeBuilderRequest captures one inbound HTTP call to the fake
// Mac builder so tests can assert on method, path, body, and the
// auth header that the proxy was supposed to forward.
type fakeBuilderRequest struct {
	Method string
	Path   string
	Body   []byte
	Auth   string
}

// newFakeMacBuilder spins up an httptest.Server that pretends to be
// a Mac running `yaver serve --builder-platforms=ios`. Every
// inbound call is recorded for later assertions; the response is a
// canned RemoteRuntimeSession + answer SDP that exercises the
// happy path of the proxy.
func newFakeMacBuilder(t *testing.T, expectedToken string) (*httptest.Server, *[]fakeBuilderRequest, *sync.Mutex) {
	t.Helper()
	var (
		mu       sync.Mutex
		captured []fakeBuilderRequest
	)
	mux := http.NewServeMux()
	record := func(r *http.Request) []byte {
		body, _ := io.ReadAll(r.Body)
		_ = r.Body.Close()
		mu.Lock()
		defer mu.Unlock()
		captured = append(captured, fakeBuilderRequest{
			Method: r.Method,
			Path:   r.URL.Path,
			Body:   body,
			Auth:   r.Header.Get("Authorization"),
		})
		return body
	}

	mux.HandleFunc("/remote-runtime/sessions", func(w http.ResponseWriter, r *http.Request) {
		_ = record(r)
		// Mac builder returns a session whose ID belongs to *its*
		// namespace. The proxy will mint a fresh local ID and store
		// the mapping.
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(RemoteRuntimeSession{
			ID:               "rr_remote_42",
			WorkDir:          "/tmp/swift-app",
			Framework:        "swift",
			ExecutionMode:    ExecutionModeNativeWebRTC,
			TargetID:         "ios-simulator",
			TargetLabel:      "iOS Simulator (mac-rack-1)",
			Platform:         "ios",
			RuntimeHostClass: "macos-ios",
			TransportMode:    "direct-webrtc",
			FrameTransport:   "webrtc-rtp-h264-v1",
			Status:           "control-ready",
			DeviceID:         "ABC-123-IPHONE",
			DeviceDims:       &DeviceDims{Width: 393, Height: 852, Rotation: "portrait"},
			CreatedAt:        time.Now().UTC().Format(time.RFC3339),
			UpdatedAt:        time.Now().UTC().Format(time.RFC3339),
			Note:             "boot+attach OK on builder",
		})
	})

	mux.HandleFunc("/remote-runtime/sessions/rr_remote_42", func(w http.ResponseWriter, r *http.Request) {
		_ = record(r)
		switch r.Method {
		case http.MethodGet:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"rr_remote_42","status":"streaming"}`))
		case http.MethodDelete:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"ok":true,"sessionId":"rr_remote_42"}`))
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})

	mux.HandleFunc("/remote-runtime/sessions/rr_remote_42/webrtc/offer", func(w http.ResponseWriter, r *http.Request) {
		body := record(r)
		// Echo the offer SDP back as a fake answer so the test can
		// confirm the body was forwarded without modification.
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Yaver-Remote-Session", "rr_remote_42")
		w.Header().Set("X-Yaver-Remote-Transport", "webrtc-rtp-h264-v1")
		_, _ = w.Write([]byte(`{"answer":{"type":"answer","sdp":"v=0\r\n"},"transport":"webrtc-rtp-h264-v1","echoBytes":` +
			intToString(len(body)) + `}`))
	})

	mux.HandleFunc("/remote-runtime/sessions/rr_remote_42/control", func(w http.ResponseWriter, r *http.Request) {
		_ = record(r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"session":{"id":"rr_remote_42","status":"streaming","lastCommand":"tap"}}`))
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	if expectedToken == "" {
		_ = expectedToken
	}
	return srv, &captured, &mu
}

func intToString(n int) string {
	return strings.TrimSpace((func() string {
		// Avoid pulling strconv into the test helper for one call.
		const digits = "0123456789"
		if n == 0 {
			return "0"
		}
		neg := n < 0
		if neg {
			n = -n
		}
		out := make([]byte, 0, 12)
		for n > 0 {
			out = append([]byte{digits[n%10]}, out...)
			n /= 10
		}
		if neg {
			out = append([]byte{'-'}, out...)
		}
		return string(out)
	}()))
}

// TestPickBuilderForFramework exercises the "should this dispatch?"
// decision for each framework + host class combination. Without this
// the dispatch could silently turn ON for sessions we want to keep
// local, or OFF when we want to forward.
func TestPickBuilderForFramework_NonSwiftStaysLocal(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("YAVER_HOME", dir)
	// Pretend we're on Linux so the dispatch logic doesn't
	// short-circuit at the "Local Mac can serve iOS itself" branch
	// when the test runs on a Mac.
	prev := hostClassForDispatch
	hostClassForDispatch = func() string { return "linux-android" }
	t.Cleanup(func() { hostClassForDispatch = prev })

	reg, _ := LoadBuilders()
	_ = reg.AddBuilder(BuilderEntry{Alias: "mac", URL: "http://x", Platforms: []string{"ios"}})
	_ = SaveBuilders(reg)

	for _, fw := range []string{"kotlin", "react-native", "expo", "next", "flutter"} {
		entry, _ := pickBuilderForFramework(fw, "android-emulator")
		if entry != nil {
			t.Errorf("framework %q (target android-emulator) should NOT dispatch, got %q", fw, entry.Alias)
		}
	}
}

func TestPickBuilderForFramework_FlutterIOSDispatches(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("YAVER_HOME", dir)
	prev := hostClassForDispatch
	hostClassForDispatch = func() string { return "linux-android" }
	t.Cleanup(func() { hostClassForDispatch = prev })

	reg, _ := LoadBuilders()
	_ = reg.AddBuilder(BuilderEntry{Alias: "mac", URL: "http://x", Platforms: []string{"ios"}})
	_ = SaveBuilders(reg)
	if entry, _ := pickBuilderForFramework("flutter", "ios-simulator"); entry == nil {
		t.Error("flutter + ios-simulator should dispatch when an iOS builder is paired")
	}
	if entry, _ := pickBuilderForFramework("flutter", "android-emulator"); entry != nil {
		t.Error("flutter + android-emulator should NOT dispatch")
	}
}

func TestPickBuilderForFramework_NoBuilderReturnsReason(t *testing.T) {
	// A clean YAVER_HOME means no paired builders. The dispatch
	// must return nil + a human-readable reason so callers can
	// surface "pair a builder" guidance instead of a silent
	// fallback to local.
	dir := t.TempDir()
	t.Setenv("YAVER_HOME", dir)
	prev := hostClassForDispatch
	hostClassForDispatch = func() string { return "linux-android" }
	t.Cleanup(func() { hostClassForDispatch = prev })
	entry, reason := pickBuilderForFramework("swift", "ios-simulator")
	if entry != nil {
		t.Fatal("no paired builder should yield nil entry")
	}
	if !strings.Contains(reason, "yaver builder add") {
		t.Errorf("reason should hint at the fix, got %q", reason)
	}
}

func TestDispatchCreateToBuilder_ProxyMappingStored(t *testing.T) {
	srv, captured, mu := newFakeMacBuilder(t, "secret-tok")
	mgr := NewRemoteRuntimeManager()
	entry := BuilderEntry{
		Alias: "mac-rack-1", URL: srv.URL, Token: "secret-tok",
		Platforms: []string{"ios"},
	}
	got, err := mgr.dispatchCreateToBuilder(entry, "/tmp/swift-app", "swift", "ios-simulator", "direct-webrtc")
	if err != nil {
		t.Fatalf("dispatchCreateToBuilder: %v", err)
	}
	// Local stub gets a fresh ID with the rr_proxy_ prefix.
	if !strings.HasPrefix(got.ID, "rr_proxy_") {
		t.Errorf("local session ID = %q, want rr_proxy_* prefix", got.ID)
	}
	if got.RemoteBuilderId != "mac-rack-1" {
		t.Errorf("RemoteBuilderId = %q, want mac-rack-1", got.RemoteBuilderId)
	}
	// Proxy mapping must point at the BUILDER's session ID, not the
	// local stub ID.
	proxy := mgr.proxiedFor(got.ID)
	if proxy == nil {
		t.Fatal("proxiedFor returned nil — mapping not stored")
	}
	if proxy.RemoteID != "rr_remote_42" {
		t.Errorf("RemoteID = %q, want rr_remote_42", proxy.RemoteID)
	}
	if proxy.BuilderToken != "secret-tok" {
		t.Errorf("token didn't round-trip into proxy state")
	}
	// Builder must have seen the create call WITH the auth header.
	mu.Lock()
	defer mu.Unlock()
	if len(*captured) != 1 {
		t.Fatalf("builder saw %d requests, want 1", len(*captured))
	}
	if (*captured)[0].Path != "/remote-runtime/sessions" {
		t.Errorf("first call path = %q", (*captured)[0].Path)
	}
	if (*captured)[0].Auth != "Bearer secret-tok" {
		t.Errorf("auth header = %q, want Bearer secret-tok", (*captured)[0].Auth)
	}
}

func TestForwardSessionRequest_OfferRoundTrip(t *testing.T) {
	srv, captured, mu := newFakeMacBuilder(t, "secret-tok")
	mgr := NewRemoteRuntimeManager()
	entry := BuilderEntry{
		Alias: "mac-rack-1", URL: srv.URL, Token: "secret-tok",
		Platforms: []string{"ios"},
	}
	local, err := mgr.dispatchCreateToBuilder(entry, "/tmp/swift-app", "swift", "ios-simulator", "direct-webrtc")
	if err != nil {
		t.Fatalf("seed dispatch: %v", err)
	}
	// Construct the same handler the real HTTP server uses and hit
	// it with an inbound offer. The proxy should re-issue the call
	// against the builder's `rr_remote_42/webrtc/offer` URL.
	hs := &HTTPServer{remoteRuntimeMgr: mgr}
	req := httptest.NewRequest(http.MethodPost,
		"/remote-runtime/sessions/"+local.ID+"/webrtc/offer",
		strings.NewReader(`{"sdp":"v=0\r\nm=video 9 UDP/TLS/RTP/SAVPF 96\r\n","type":"offer"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	hs.handleRemoteRuntimeSessionRoute(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("X-Yaver-Remote-Session"); got != "rr_remote_42" {
		t.Errorf("X-Yaver-Remote-Session = %q (header should be passed through)", got)
	}
	mu.Lock()
	defer mu.Unlock()
	// Builder saw both the create AND the forwarded offer.
	if len(*captured) != 2 {
		t.Fatalf("builder saw %d requests, want 2 (create + offer)", len(*captured))
	}
	offerCall := (*captured)[1]
	if offerCall.Path != "/remote-runtime/sessions/rr_remote_42/webrtc/offer" {
		t.Errorf("offer path = %q (proxy should rewrite local ID → remote ID)", offerCall.Path)
	}
	if offerCall.Method != http.MethodPost {
		t.Errorf("offer method = %q", offerCall.Method)
	}
	if !strings.Contains(string(offerCall.Body), "m=video") {
		t.Errorf("offer body wasn't forwarded verbatim: %q", offerCall.Body)
	}
	if offerCall.Auth != "Bearer secret-tok" {
		t.Errorf("offer auth header = %q (must use builder token, not browser token)", offerCall.Auth)
	}
}

func TestForwardSessionRequest_ControlRoundTrip(t *testing.T) {
	srv, captured, mu := newFakeMacBuilder(t, "secret-tok")
	mgr := NewRemoteRuntimeManager()
	entry := BuilderEntry{Alias: "mac-rack-1", URL: srv.URL, Token: "secret-tok", Platforms: []string{"ios"}}
	local, _ := mgr.dispatchCreateToBuilder(entry, "/tmp/swift-app", "swift", "ios-simulator", "direct-webrtc")

	hs := &HTTPServer{remoteRuntimeMgr: mgr}
	req := httptest.NewRequest(http.MethodPost,
		"/remote-runtime/sessions/"+local.ID+"/control",
		strings.NewReader(`{"action":"tap","x":100,"y":200}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	hs.handleRemoteRuntimeSessionRoute(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	mu.Lock()
	defer mu.Unlock()
	if len(*captured) < 2 {
		t.Fatalf("builder saw %d requests, want ≥2", len(*captured))
	}
	last := (*captured)[len(*captured)-1]
	if last.Path != "/remote-runtime/sessions/rr_remote_42/control" {
		t.Errorf("control path = %q", last.Path)
	}
	if !strings.Contains(string(last.Body), `"action":"tap"`) {
		t.Errorf("control body wasn't forwarded: %q", last.Body)
	}
}

func TestForwardSessionRequest_DeleteCleansUpProxyMapping(t *testing.T) {
	srv, _, _ := newFakeMacBuilder(t, "secret-tok")
	mgr := NewRemoteRuntimeManager()
	entry := BuilderEntry{Alias: "mac-rack-1", URL: srv.URL, Token: "secret-tok", Platforms: []string{"ios"}}
	local, _ := mgr.dispatchCreateToBuilder(entry, "/tmp/swift-app", "swift", "ios-simulator", "direct-webrtc")

	if mgr.proxiedFor(local.ID) == nil {
		t.Fatal("proxy mapping should be present after dispatchCreateToBuilder")
	}
	hs := &HTTPServer{remoteRuntimeMgr: mgr}
	req := httptest.NewRequest(http.MethodDelete,
		"/remote-runtime/sessions/"+local.ID, nil)
	rec := httptest.NewRecorder()
	hs.handleRemoteRuntimeSessionRoute(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("delete status = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	if mgr.proxiedFor(local.ID) != nil {
		t.Error("proxy mapping should be cleared after DELETE")
	}
	if _, ok := mgr.Get(local.ID); ok {
		t.Error("local session should be cleared after DELETE")
	}
}

func TestSplitSessionRoutePath_Suffixes(t *testing.T) {
	cases := []struct {
		in        string
		wantID    string
		wantSfx   string
	}{
		{"rr_proxy_173/webrtc/offer", "rr_proxy_173", "/webrtc/offer"},
		{"rr_proxy_173/control", "rr_proxy_173", "/control"},
		{"rr_proxy_173/command", "rr_proxy_173", "/command"},
		{"rr_proxy_173/frame", "rr_proxy_173", "/frame"},
		{"rr_proxy_173", "rr_proxy_173", ""},
		{"", "", ""},
	}
	for _, tc := range cases {
		id, sfx := splitSessionRoutePath(tc.in)
		if id != tc.wantID || sfx != tc.wantSfx {
			t.Errorf("split(%q) = (%q, %q), want (%q, %q)",
				tc.in, id, sfx, tc.wantID, tc.wantSfx)
		}
	}
}

func TestForwardSessionRequest_BuilderUnreachableYields502(t *testing.T) {
	mgr := NewRemoteRuntimeManager()
	mgr.proxied["rr_proxy_unreachable"] = &proxiedSession{
		BuilderAlias: "mac-rack-1",
		BuilderURL:   "http://127.0.0.1:1", // port 1 — guaranteed-refused on every supported platform
		BuilderToken: "x",
		RemoteID:     "rr_remote_42",
	}
	mgr.sessions["rr_proxy_unreachable"] = RemoteRuntimeSession{ID: "rr_proxy_unreachable"}
	hs := &HTTPServer{remoteRuntimeMgr: mgr}
	req := httptest.NewRequest(http.MethodGet, "/remote-runtime/sessions/rr_proxy_unreachable", nil)
	rec := httptest.NewRecorder()
	hs.handleRemoteRuntimeSessionRoute(rec, req)
	if rec.Code != http.StatusBadGateway {
		t.Errorf("unreachable builder should yield 502, got %d (%s)", rec.Code, rec.Body.String())
	}
}
