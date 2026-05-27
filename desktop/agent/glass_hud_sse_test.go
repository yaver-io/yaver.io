package main

// glass_hud_sse_test.go — proves the HUD wire format from agent →
// MentraOS miniapp without needing physical glasses.
//
// What this exercises:
//
//   1. POST /glass/hud accepts the four typed views and rejects bad
//      input cleanly (400 + error string).
//   2. BroadcastHUD* helpers + POST /glass/hud emit blackbox commands
//      whose JSON payloads survive a round-trip through the existing
//      /blackbox/command-stream SSE handler.
//   3. The four `command` names that mentra-miniapp's switch reacts
//      to are the EXACT strings the agent emits — a rename on either
//      side breaks the HUD silently otherwise.
//
// We stand up a full HTTPServer with a real BlackBoxManager, subscribe
// a fake SSE client to /blackbox/command-stream, then push HUD views
// and assert delivery. No physical glasses, no Bluetooth — same wire
// the miniapp's `subscribeToAgentCommands` reads.

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func newHUDTestServer(t *testing.T) (*HTTPServer, *httptest.Server) {
	t.Helper()
	mgr, err := NewBlackBoxManager()
	if err != nil {
		t.Fatalf("NewBlackBoxManager: %v", err)
	}
	srv := &HTTPServer{blackboxMgr: mgr}
	mux := http.NewServeMux()
	mux.HandleFunc("/glass/hud", srv.handleGlassHUDPush)
	mux.HandleFunc("/blackbox/command-stream", srv.handleBlackBoxCommandStream)
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return srv, ts
}

type sseFrame struct {
	Type    string          `json:"type"`
	Command json.RawMessage `json:"command"`
}

// subscribeSSE opens /blackbox/command-stream and feeds parsed frames
// into the returned channel. Caller closes the cancel func to tear
// the subscription down. Mirrors mentra-miniapp's subscribe loop.
func subscribeSSE(t *testing.T, base, deviceID string) (chan map[string]any, func()) {
	t.Helper()
	req, err := http.NewRequest("GET", base+"/blackbox/command-stream?device="+deviceID, nil)
	if err != nil {
		t.Fatalf("new req: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("sse GET: %v", err)
	}
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		t.Fatalf("sse status %d: %s", resp.StatusCode, body)
	}

	out := make(chan map[string]any, 16)
	stop := make(chan struct{})
	go func() {
		defer resp.Body.Close()
		buf := make([]byte, 4096)
		acc := bytes.Buffer{}
		for {
			select {
			case <-stop:
				return
			default:
			}
			n, err := resp.Body.Read(buf)
			if n > 0 {
				acc.Write(buf[:n])
				for {
					data := acc.Bytes()
					idx := bytes.Index(data, []byte("\n\n"))
					if idx == -1 {
						break
					}
					frame := acc.Next(idx + 2)
					line := bytes.TrimSpace(bytes.TrimPrefix(frame, []byte("data: ")))
					if len(line) == 0 {
						continue
					}
					var envelope sseFrame
					if err := json.Unmarshal(line, &envelope); err != nil {
						continue
					}
					if envelope.Type != "command" || len(envelope.Command) == 0 {
						continue
					}
					var cmd map[string]any
					if err := json.Unmarshal(envelope.Command, &cmd); err != nil {
						continue
					}
					select {
					case out <- cmd:
					default:
					}
				}
			}
			if err != nil {
				return
			}
		}
	}()
	return out, func() { close(stop); resp.Body.Close() }
}

func waitForCommand(t *testing.T, ch chan map[string]any, want string, timeout time.Duration) map[string]any {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case msg := <-ch:
			if name, _ := msg["command"].(string); name == want {
				return msg
			}
		case <-deadline:
			t.Fatalf("never received command %q within %s", want, timeout)
		}
	}
}

func TestGlassHUDTerminalTailRoundtrip(t *testing.T) {
	srv, ts := newHUDTestServer(t)
	// Force the session to exist so the SSE subscriber survives the
	// race with the producer. Without this, BroadcastCommand may fire
	// before the GET handler grabs its session, and the SSE loop
	// never sees the message.
	srv.blackboxMgr.GetOrCreateSession("hud-test-a", "glasses-mentra", "yaver")
	ch, stop := subscribeSSE(t, ts.URL, "hud-test-a")
	defer stop()
	// Give the subscriber a beat to register its channel.
	time.Sleep(150 * time.Millisecond)

	body, _ := json.Marshal(map[string]any{
		"view": "terminal_tail",
		"payload": map[string]any{
			"sessionLabel": "yaver:dev",
			"lines":        []string{"line one", "line two", "line three"},
		},
	})
	resp, err := http.Post(ts.URL+"/glass/hud", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST /glass/hud: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("POST /glass/hud status = %d", resp.StatusCode)
	}

	got := waitForCommand(t, ch, "glass_terminal_tail", 3*time.Second)
	data, ok := got["data"].(map[string]any)
	if !ok {
		t.Fatalf("data missing on terminal_tail: %+v", got)
	}
	if got, want := data["sessionLabel"], "yaver:dev"; got != want {
		t.Errorf("sessionLabel = %v, want %v", got, want)
	}
	lines, ok := data["lines"].([]any)
	if !ok || len(lines) != 3 {
		t.Errorf("lines = %v (want 3 items)", data["lines"])
	}
}

func TestGlassHUDEmailSubjectsRoundtrip(t *testing.T) {
	srv, ts := newHUDTestServer(t)
	srv.blackboxMgr.GetOrCreateSession("hud-test-b", "glasses-mentra", "yaver")
	ch, stop := subscribeSSE(t, ts.URL, "hud-test-b")
	defer stop()
	time.Sleep(150 * time.Millisecond)

	body, _ := json.Marshal(map[string]any{
		"view": "email_subjects",
		"payload": map[string]any{
			"folder": "inbox",
			"items": []map[string]string{
				{"from": "Stripe", "subject": "Payment received"},
				{"from": "GitHub", "subject": "PR #42 merged"},
			},
		},
	})
	resp, err := http.Post(ts.URL+"/glass/hud", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST /glass/hud: %v", err)
	}
	resp.Body.Close()

	got := waitForCommand(t, ch, "glass_email_subjects", 3*time.Second)
	data := got["data"].(map[string]any)
	if data["folder"] != "inbox" {
		t.Errorf("folder = %v", data["folder"])
	}
	items, _ := data["items"].([]any)
	if len(items) != 2 {
		t.Fatalf("items len = %d, want 2", len(items))
	}
}

func TestGlassHUDNotificationRoundtrip(t *testing.T) {
	srv, ts := newHUDTestServer(t)
	srv.blackboxMgr.GetOrCreateSession("hud-test-c", "glasses-mentra", "yaver")
	ch, stop := subscribeSSE(t, ts.URL, "hud-test-c")
	defer stop()
	time.Sleep(150 * time.Millisecond)

	body, _ := json.Marshal(map[string]any{
		"view": "notification",
		"payload": map[string]any{
			"title":  "Build green",
			"body":   "1.99.231 ready for TestFlight",
			"source": "ci",
		},
	})
	resp, err := http.Post(ts.URL+"/glass/hud", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	resp.Body.Close()

	got := waitForCommand(t, ch, "glass_notification", 3*time.Second)
	data := got["data"].(map[string]any)
	if data["title"] != "Build green" {
		t.Errorf("title = %v", data["title"])
	}
	if data["source"] != "ci" {
		t.Errorf("source = %v", data["source"])
	}
}

func TestGlassHUDVoiceOverlayRoundtrip(t *testing.T) {
	srv, ts := newHUDTestServer(t)
	srv.blackboxMgr.GetOrCreateSession("hud-test-d", "glasses-mentra", "yaver")
	ch, stop := subscribeSSE(t, ts.URL, "hud-test-d")
	defer stop()
	time.Sleep(150 * time.Millisecond)

	body, _ := json.Marshal(map[string]any{
		"view": "voice_overlay",
		"payload": map[string]any{
			"partial":   "open gmail in the spatial bro",
			"final":     "open gmail in the spatial browser",
			"latencyMs": 240,
		},
	})
	resp, err := http.Post(ts.URL+"/glass/hud", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	resp.Body.Close()

	got := waitForCommand(t, ch, "glass_voice_overlay", 3*time.Second)
	data := got["data"].(map[string]any)
	if !strings.HasPrefix(data["final"].(string), "open gmail") {
		t.Errorf("final = %v", data["final"])
	}
}

func TestGlassHUDPushRejectsUnknownView(t *testing.T) {
	_, ts := newHUDTestServer(t)
	body, _ := json.Marshal(map[string]any{
		"view":    "not_a_view",
		"payload": map[string]any{},
	})
	resp, err := http.Post(ts.URL+"/glass/hud", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	raw, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(raw), "unknown view") {
		t.Errorf("body missing 'unknown view': %s", raw)
	}
}

func TestGlassHUDPushRejectsBadJSON(t *testing.T) {
	_, ts := newHUDTestServer(t)
	resp, err := http.Post(ts.URL+"/glass/hud", "application/json", strings.NewReader("{ not json"))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}

func TestGlassHUDPushMissingBlackbox(t *testing.T) {
	srv := &HTTPServer{} // no blackboxMgr
	mux := http.NewServeMux()
	mux.HandleFunc("/glass/hud", srv.handleGlassHUDPush)
	ts := httptest.NewServer(mux)
	defer ts.Close()
	body, _ := json.Marshal(map[string]any{"view": "notification", "payload": map[string]any{}})
	resp, err := http.Post(ts.URL+"/glass/hud", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", resp.StatusCode)
	}
}

func TestBroadcastHUDDirectHelpers(t *testing.T) {
	// The HTTP path covers the JSON wire format. The Broadcast helpers
	// are also called directly from inside the agent (e.g. the runner
	// auth bus → glass_notification path) — make sure THAT path also
	// produces the right command name + clamps oversize input.
	srv, ts := newHUDTestServer(t)
	srv.blackboxMgr.GetOrCreateSession("hud-test-direct", "glasses-mentra", "yaver")
	ch, stop := subscribeSSE(t, ts.URL, "hud-test-direct")
	defer stop()
	time.Sleep(150 * time.Millisecond)

	long := strings.Repeat("y", 800)
	BroadcastHUDNotification(srv.blackboxMgr, long, long, "internal")
	got := waitForCommand(t, ch, "glass_notification", 3*time.Second)
	data := got["data"].(map[string]any)
	title := data["title"].(string)
	if n := len([]rune(title)); n > hudMaxLineLen {
		t.Errorf("title not clamped: %d runes (want ≤ %d)", n, hudMaxLineLen)
	}
}

func TestGlassPCFocusBroadcastsToHUD(t *testing.T) {
	// opsGlassBroadcastFocus is the side-channel that ALSO emits a
	// glass_pc_focus command — important because spatial viewers
	// react to it. The HUD just shows "focused · <id>", but the
	// command must still land.
	srv, ts := newHUDTestServer(t)
	srv.blackboxMgr.GetOrCreateSession("hud-test-focus", "glasses-mentra", "yaver")
	ch, stop := subscribeSSE(t, ts.URL, "hud-test-focus")
	defer stop()
	time.Sleep(150 * time.Millisecond)

	opsGlassBroadcastFocus(srv, "rr_session_xyz")
	got := waitForCommand(t, ch, "glass_pc_focus", 3*time.Second)
	data := got["data"].(map[string]any)
	if data["sessionId"] != "rr_session_xyz" {
		t.Errorf("sessionId = %v", data["sessionId"])
	}
}

func TestImapInboxNoEmailManager(t *testing.T) {
	srv := &HTTPServer{} // emailMgr == nil
	mux := http.NewServeMux()
	mux.HandleFunc("/imap/inbox", srv.handleIMAPInbox)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/imap/inbox")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (no email mgr should respond cleanly)", resp.StatusCode)
	}
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if items, _ := body["items"].([]any); items == nil {
		t.Errorf("items should be an empty array, got %+v", body)
	}
	if note, _ := body["note"].(string); note == "" {
		t.Errorf("note should explain why items is empty")
	}
}
