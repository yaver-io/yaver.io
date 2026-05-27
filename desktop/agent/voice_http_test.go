package main

// Tests for the voice surface. Per CLAUDE.md: real HTTP/WS servers on
// random ports, no mocks. Each test stands up a tiny in-process WS
// server that mimics just enough of Deepgram / Cartesia to verify
// our client decodes their wire format correctly.

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// --- /voice/status ------------------------------------------------------

func TestVoiceStatus_NoConfig(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/voice/status", nil)

	s := &HTTPServer{}
	s.handleVoiceStatus(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var body map[string]interface{}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["enabled"] != false {
		t.Errorf("enabled = %v, want false on empty config", body["enabled"])
	}
	if body["sttReady"] != false || body["ttsReady"] != false {
		t.Errorf("sttReady/ttsReady should be false on empty config")
	}
}

func TestVoiceStatus_ConfigPresent(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	cfgDir := filepath.Join(home, configDirName)
	if err := os.MkdirAll(cfgDir, 0700); err != nil {
		t.Fatal(err)
	}
	cfg := `{"voice":{"enabled":true,"stt_provider":"deepgram","tts_provider":"cartesia","deepgram_api_key":"dg-test","cartesia_api_key":"ct-test","default_project":"yaver"}}`
	if err := os.WriteFile(filepath.Join(cfgDir, "config.json"), []byte(cfg), 0600); err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/voice/status", nil)
	s := &HTTPServer{}
	s.handleVoiceStatus(rec, req)

	var body map[string]interface{}
	_ = json.NewDecoder(rec.Body).Decode(&body)
	if body["enabled"] != true {
		t.Errorf("enabled = %v, want true", body["enabled"])
	}
	if body["sttReady"] != true {
		t.Errorf("sttReady = %v, want true", body["sttReady"])
	}
	if body["ttsReady"] != true {
		t.Errorf("ttsReady = %v, want true", body["ttsReady"])
	}
	if body["defaultProject"] != "yaver" {
		t.Errorf("defaultProject = %v, want yaver", body["defaultProject"])
	}
}

func TestVoiceStatus_OpenAIDefault(t *testing.T) {
	// OpenAI is the default when no provider is explicitly set. One
	// API key covers both STT + TTS — easiest path for first-time
	// users + the "Yaver trio" (phone+glasses+keyboard) crowd.
	home := t.TempDir()
	t.Setenv("HOME", home)
	cfgDir := filepath.Join(home, configDirName)
	if err := os.MkdirAll(cfgDir, 0700); err != nil {
		t.Fatal(err)
	}
	cfg := `{"voice":{"enabled":true,"openai_api_key":"sk-test"}}`
	if err := os.WriteFile(filepath.Join(cfgDir, "config.json"), []byte(cfg), 0600); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	(&HTTPServer{}).handleVoiceStatus(rec, httptest.NewRequest(http.MethodGet, "/voice/status", nil))
	var body map[string]interface{}
	_ = json.NewDecoder(rec.Body).Decode(&body)
	if body["sttProvider"] != "openai" {
		t.Errorf("sttProvider = %v, want openai", body["sttProvider"])
	}
	if body["ttsProvider"] != "openai" {
		t.Errorf("ttsProvider = %v, want openai", body["ttsProvider"])
	}
	if body["sttReady"] != true || body["ttsReady"] != true {
		t.Errorf("expected both ready with openai key set, got stt=%v tts=%v", body["sttReady"], body["ttsReady"])
	}
}

func TestVoiceStatus_KeyboardOnlyMode(t *testing.T) {
	// Trio = phone + glasses + Bluetooth keyboard. User types instead
	// of speaking. Yaver MUST work cleanly with voice keys absent —
	// status reports not-ready, mobile mic orb hides itself, no
	// crashes on the agent side. This test locks in that contract.
	home := t.TempDir()
	t.Setenv("HOME", home)
	cfgDir := filepath.Join(home, configDirName)
	_ = os.MkdirAll(cfgDir, 0700)
	cfg := `{"voice":{"enabled":false}}`
	_ = os.WriteFile(filepath.Join(cfgDir, "config.json"), []byte(cfg), 0600)
	rec := httptest.NewRecorder()
	(&HTTPServer{}).handleVoiceStatus(rec, httptest.NewRequest(http.MethodGet, "/voice/status", nil))
	var body map[string]interface{}
	_ = json.NewDecoder(rec.Body).Decode(&body)
	if body["enabled"] != false {
		t.Errorf("expected enabled=false in keyboard-only mode")
	}
	// availableProviders MUST still be exposed so the Settings UI
	// can render the picker on first launch.
	if body["availableProviders"] == nil {
		t.Errorf("availableProviders missing — Settings picker won't render")
	}
}

func TestVoiceStatus_WrongMethod(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/voice/status", nil)
	(&HTTPServer{}).handleVoiceStatus(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", rec.Code)
	}
}

// --- Deepgram client ----------------------------------------------------

func TestDeepgramSession_PartialFinalEOT(t *testing.T) {
	// Fake Deepgram: accepts WS, echoes one partial + one final + one EOT.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		up := websocket.Upgrader{CheckOrigin: func(_ *http.Request) bool { return true }}
		conn, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		// Sanity: confirm Authorization header carried our token
		if r.Header.Get("Authorization") != "Token test-key" {
			_ = conn.WriteJSON(map[string]string{"type": "Error", "msg": "bad auth"})
			return
		}
		// Wait for any audio frame, then reply.
		_, _, _ = conn.ReadMessage()
		_ = conn.WriteJSON(map[string]interface{}{
			"type": "Results",
			"channel": map[string]interface{}{
				"alternatives": []map[string]interface{}{{"transcript": "hello", "confidence": 0.9}},
			},
			"is_final": false,
		})
		_ = conn.WriteJSON(map[string]interface{}{
			"type": "Results",
			"channel": map[string]interface{}{
				"alternatives": []map[string]interface{}{{"transcript": "hello world", "confidence": 0.99}},
			},
			"is_final":     true,
			"speech_final": true,
		})
		// Give the client a beat to drain before we close
		time.Sleep(50 * time.Millisecond)
	}))
	defer srv.Close()
	prev := DeepgramURL
	DeepgramURL = "ws" + strings.TrimPrefix(srv.URL, "http")
	defer func() { DeepgramURL = prev }()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	sess, events, err := OpenDeepgramSession(ctx, "test-key", "nova-3", []string{"useState"})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer sess.Close()

	if err := sess.SendAudio([]byte{0, 0, 0, 0}); err != nil {
		t.Fatalf("send audio: %v", err)
	}

	var kinds []string
	var lastFinal string
	for {
		select {
		case <-ctx.Done():
			t.Fatalf("timed out — got events: %v", kinds)
		case ev, ok := <-events:
			if !ok {
				goto done
			}
			kinds = append(kinds, ev.Kind)
			if ev.Kind == "final" {
				lastFinal = ev.Text
			}
			if ev.Kind == "eot" {
				goto done
			}
		}
	}
done:
	if lastFinal != "hello world" {
		t.Errorf("final transcript = %q, want %q", lastFinal, "hello world")
	}
	gotPartial := false
	gotFinal := false
	gotEOT := false
	for _, k := range kinds {
		switch k {
		case "partial":
			gotPartial = true
		case "final":
			gotFinal = true
		case "eot":
			gotEOT = true
		}
	}
	if !gotPartial || !gotFinal || !gotEOT {
		t.Errorf("missing event kinds; got %v", kinds)
	}
}

func TestDeepgramSession_NoKey(t *testing.T) {
	_, _, err := OpenDeepgramSession(context.Background(), "", "nova-3", nil)
	if err == nil {
		t.Fatal("expected error on empty key")
	}
}

// --- Cartesia client ----------------------------------------------------

func TestCartesia_Roundtrip(t *testing.T) {
	// Fake Cartesia: read the JSON request, reply with one base64-PCM
	// chunk + done.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("api_key") != "ct-test" {
			http.Error(w, "bad key", 401)
			return
		}
		up := websocket.Upgrader{CheckOrigin: func(_ *http.Request) bool { return true }}
		conn, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		_, body, err := conn.ReadMessage()
		if err != nil {
			return
		}
		var req map[string]interface{}
		_ = json.Unmarshal(body, &req)
		if req["transcript"] != "hello yaver" {
			_ = conn.WriteJSON(map[string]string{"type": "error", "error": "bad transcript"})
			return
		}
		// 4 bytes of PCM as a smoke payload
		pcm := []byte{1, 2, 3, 4}
		_ = conn.WriteJSON(map[string]interface{}{
			"type": "chunk",
			"data": base64.StdEncoding.EncodeToString(pcm),
		})
		_ = conn.WriteJSON(map[string]interface{}{"type": "done"})
	}))
	defer srv.Close()
	prev := CartesiaURL
	CartesiaURL = "ws" + strings.TrimPrefix(srv.URL, "http")
	defer func() { CartesiaURL = prev }()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	out := make(chan CartesiaFrame, 8)
	go SpeakCartesia(ctx, "ct-test", "", "hello yaver", out)

	var pcm []byte
	gotDone := false
	for fr := range out {
		if fr.Error != "" {
			t.Fatalf("frame error: %s", fr.Error)
		}
		if len(fr.PCM) > 0 {
			pcm = append(pcm, fr.PCM...)
		}
		if fr.Done {
			gotDone = true
		}
	}
	if !gotDone {
		t.Error("never got done frame")
	}
	if len(pcm) != 4 || pcm[0] != 1 {
		t.Errorf("pcm = %v, want [1 2 3 4]", pcm)
	}
}

// --- Pure helpers -------------------------------------------------------

func TestVoiceTitleFromTranscript(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"short prompt", "short prompt"},
		{"add a logout button please.", "add a logout button please."},
		{
			"add a logout button to the settings screen and wire it up to the auth slice",
			"add a logout button to the settings screen…",
		},
	}
	for _, c := range cases {
		got := voiceTitleFromTranscript(c.in)
		// We don't pin exact truncation — just verify cap.
		if len(got) > 65 {
			t.Errorf("title too long: %q (%d chars)", got, len(got))
		}
		if c.in == c.want && got != c.want {
			t.Errorf("short input mangled: got %q want %q", got, c.want)
		}
	}
}

func TestVoiceTrimForTTS(t *testing.T) {
	short := "Touched 2 files. Approve?"
	if voiceTrimForTTS(short) != short {
		t.Error("short text mangled")
	}
	long := strings.Repeat("x", 500)
	out := voiceTrimForTTS(long)
	if !strings.HasSuffix(out, "see screen for the rest.") {
		t.Errorf("long text not suffixed: %q", out[len(out)-40:])
	}
}
