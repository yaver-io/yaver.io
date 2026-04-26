package main

// vibe_preview_appium_test.go — Phase 15 tests. Stubs the Appium server
// with httptest so the suite stays hermetic. Real Appium integration
// (talks to localhost:4723) is exercised manually + by future
// integration tests gated on YAVER_TEST_APPIUM=1.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestAppiumClient_StartAndStopSession(t *testing.T) {
	mux := http.NewServeMux()
	var startCalls atomic.Int32

	mux.HandleFunc("/session", func(w http.ResponseWriter, r *http.Request) {
		startCalls.Add(1)
		var body map[string]interface{}
		_ = json.NewDecoder(r.Body).Decode(&body)
		caps, _ := body["capabilities"].(map[string]interface{})
		always, _ := caps["alwaysMatch"].(map[string]interface{})
		if always["platformName"] != "iOS" {
			t.Errorf("expected platformName=iOS, got %v", always["platformName"])
		}
		_, _ = w.Write([]byte(`{"value":{"sessionId":"sess-123"}}`))
	})
	mux.HandleFunc("/session/sess-123", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "DELETE" {
			_, _ = w.Write([]byte(`{"value":null}`))
			return
		}
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := NewAppiumClient(srv.URL)
	id, err := client.StartSession(context.Background(), AppiumStartCaps{
		Platform: "iOS",
	})
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	if id != "sess-123" {
		t.Fatalf("expected sess-123, got %q", id)
	}
	if err := client.StopSession(context.Background(), id); err != nil {
		t.Fatalf("StopSession: %v", err)
	}
	if startCalls.Load() != 1 {
		t.Errorf("expected 1 start call, got %d", startCalls.Load())
	}
}

func TestAppiumClient_PageSource(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/session/sess/source", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"value":"<view>RCTRedBox: Unhandled JS Exception: TypeError: undefined is not a function</view>"}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	src, err := NewAppiumClient(srv.URL).PageSource(context.Background(), "sess")
	if err != nil {
		t.Fatalf("PageSource: %v", err)
	}
	if !strings.Contains(src, "RCTRedBox") {
		t.Fatalf("expected RCTRedBox, got %q", src)
	}
}

func TestDetectAppiumCrashSignal(t *testing.T) {
	cases := []struct {
		src       string
		signature string
	}{
		{`<view>RCTRedBox</view>`, "rn-redbox"},
		{`<dialog>Unhandled JS Exception: TypeError</dialog>`, "rn-redbox"},
		{`<msg>undefined is not a function — line 42</msg>`, "rn-redbox"},
		{`<dialog>com.example has stopped</dialog>`, "android-anr"},
		{`<dialog>Yaver isn't responding. Wait or close.</dialog>`, "android-anr"},
		{`<view>welcome to my app</view>`, ""},
	}
	for _, c := range cases {
		sig, msg := DetectAppiumCrashSignal(c.src)
		if sig != c.signature {
			t.Errorf("for %q: want %q, got %q (msg=%q)", c.src, c.signature, sig, msg)
		}
		if c.signature != "" && msg == "" {
			t.Errorf("for %q: matched %q but message is empty", c.src, c.signature)
		}
	}
}

func TestAppiumBugHunter_emitsCrashOnRedBox(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/session/sess/source", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"value":"<view>RCTRedBox: Unhandled JS Exception: app crashed</view>"}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	mgr := NewVibePreviewManager(newFakeBrowser())
	mgr.SetDiskRoot(t.TempDir())
	defer mgr.StopAll()

	if _, err := mgr.Start(VibePreviewStartOpts{
		Project: "p", TargetURL: "http://x", Mode: VibePreviewModeChangeOnly,
	}); err != nil {
		t.Fatalf("start: %v", err)
	}
	ch, _, unsub := mgr.Subscribe("p")
	defer unsub()
	// Drain the start + initial frame events.
	drainLoop:
	for {
		select {
		case <-ch:
		case <-time.After(50 * time.Millisecond):
			break drainLoop
		}
	}

	client := NewAppiumClient(srv.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	found := mgr.AppiumBugHunter(ctx, client, "sess", "p", 3*time.Second)
	if found == 0 {
		t.Fatal("expected bug-hunter to find at least one crash")
	}

	// Crash event should land on the SSE channel.
	gotCrash := false
	deadline := time.After(1 * time.Second)
	collect:
	for {
		select {
		case ev := <-ch:
			if ev.Type == "crash" && strings.HasPrefix(ev.Source, "appium-rn-redbox") {
				gotCrash = true
				break collect
			}
		case <-deadline:
			break collect
		}
	}
	if !gotCrash {
		t.Fatal("expected crash SSE event with appium-rn-redbox source")
	}
}

func TestAppiumBugHunter_dedupsRepeatedCrashes(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/session/sess/source", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"value":"<view>RCTRedBox: same error every time</view>"}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	mgr := NewVibePreviewManager(newFakeBrowser())
	mgr.SetDiskRoot(t.TempDir())
	defer mgr.StopAll()

	if _, err := mgr.Start(VibePreviewStartOpts{
		Project: "p", TargetURL: "http://x", Mode: VibePreviewModeChangeOnly,
	}); err != nil {
		t.Fatalf("start: %v", err)
	}

	client := NewAppiumClient(srv.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	// Run for ~5 s; would naively find 2-3 hits at 2 s poll interval,
	// but the hunter dedups by signature+message, so we expect 1.
	found := mgr.AppiumBugHunter(ctx, client, "sess", "p", 5*time.Second)
	if found != 1 {
		t.Fatalf("expected 1 deduped crash, got %d", found)
	}
}
