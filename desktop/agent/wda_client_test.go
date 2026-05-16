package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// fakeWDA is a real HTTP server that speaks just enough WebDriverAgent
// for the client tests (repo convention: real server on a random
// port, no mocks). It records the last body seen at each path.
type fakeWDA struct {
	mu     sync.Mutex
	bodies map[string]map[string]any
	srv    *httptest.Server
}

func newFakeWDA(t *testing.T) *fakeWDA {
	t.Helper()
	f := &fakeWDA{bodies: map[string]map[string]any{}}
	mux := http.NewServeMux()
	record := func(r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var m map[string]any
		_ = json.Unmarshal(raw, &m)
		f.mu.Lock()
		f.bodies[r.URL.Path] = m
		f.mu.Unlock()
	}
	mux.HandleFunc("/status", func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"value":{"ready":true}}`)
	})
	mux.HandleFunc("/session", func(w http.ResponseWriter, r *http.Request) {
		record(r)
		io.WriteString(w, `{"sessionId":"sess-1","value":{}}`)
	})
	mux.HandleFunc("/screenshot", func(w http.ResponseWriter, r *http.Request) {
		b64 := base64.StdEncoding.EncodeToString([]byte("PNGBYTES"))
		io.WriteString(w, `{"value":"`+b64+`"}`)
	})
	// Everything under /session/sess-1/... records + returns ok.
	mux.HandleFunc("/session/sess-1/window/size", func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"value":{"width":390,"height":844}}`)
	})
	mux.HandleFunc("/session/sess-1/wda/tap/0", func(w http.ResponseWriter, r *http.Request) {
		record(r)
		io.WriteString(w, `{"value":null}`)
	})
	mux.HandleFunc("/session/sess-1/wda/dragfromtoforduration", func(w http.ResponseWriter, r *http.Request) {
		record(r)
		io.WriteString(w, `{"value":null}`)
	})
	mux.HandleFunc("/session/sess-1/wda/keys", func(w http.ResponseWriter, r *http.Request) {
		record(r)
		io.WriteString(w, `{"value":null}`)
	})
	mux.HandleFunc("/session/sess-1/wda/pressButton", func(w http.ResponseWriter, r *http.Request) {
		record(r)
		io.WriteString(w, `{"value":null}`)
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeWDA) body(path string) map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.bodies[path]
}

func TestWDAClient_ControlContract(t *testing.T) {
	f := newFakeWDA(t)
	c := newWDAClient(f.srv.URL)
	ctx := context.Background()

	if err := c.Status(ctx); err != nil {
		t.Fatalf("status: %v", err)
	}
	if err := c.Tap(ctx, 42, 99); err != nil {
		t.Fatalf("tap: %v", err)
	}
	if c.sessionID != "sess-1" {
		t.Fatalf("session not captured, got %q", c.sessionID)
	}
	if b := f.body("/session/sess-1/wda/tap/0"); b["x"].(float64) != 42 || b["y"].(float64) != 99 {
		t.Fatalf("tap body wrong: %v", b)
	}
	if err := c.Swipe(ctx, 1, 2, 3, 4, 500); err != nil {
		t.Fatalf("swipe: %v", err)
	}
	if b := f.body("/session/sess-1/wda/dragfromtoforduration"); b["duration"].(float64) != 0.5 {
		t.Fatalf("swipe duration should be seconds (0.5), got %v", b["duration"])
	}
	if err := c.Text(ctx, "hi"); err != nil {
		t.Fatalf("text: %v", err)
	}
	if v, _ := f.body("/session/sess-1/wda/keys")["value"].([]any); len(v) != 2 || v[0] != "h" {
		t.Fatalf("text value should be per-rune array, got %v", f.body("/session/sess-1/wda/keys")["value"])
	}
	if err := c.PressButton(ctx, "volume-up"); err != nil {
		t.Fatalf("pressButton: %v", err)
	}
	if b := f.body("/session/sess-1/wda/pressButton"); b["name"] != "volumeUp" {
		t.Fatalf("button name mapping wrong: %v", b)
	}
	if err := c.PressButton(ctx, "back"); err == nil ||
		!strings.Contains(err.Error(), "unsupported key") {
		t.Fatalf("iOS has no Back button — expect a clear error, got %v", err)
	}

	png, err := c.Screenshot(ctx)
	if err != nil || string(png) != "PNGBYTES" {
		t.Fatalf("screenshot decode: %q %v", png, err)
	}
	w, h, err := c.WindowSize(ctx)
	if err != nil || w != 390 || h != 844 {
		t.Fatalf("window size: %d x %d %v", w, h, err)
	}
}

func TestWDAClient_Non2xxIsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)
	if err := newWDAClient(srv.URL).Status(context.Background()); err == nil ||
		!strings.Contains(err.Error(), "HTTP 500") {
		t.Fatalf("expected HTTP 500 surfaced, got %v", err)
	}
}

func TestWDABaseURL_EnvOverride(t *testing.T) {
	t.Setenv("YAVER_WDA_BASE_URL", "http://example.test:9999/")
	if got := wdaBaseURL(); got != "http://example.test:9999" {
		t.Fatalf("env override + trailing-slash trim failed: %q", got)
	}
}
