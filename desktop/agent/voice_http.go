package main

// voice_http.go — HTTP + WebSocket edges for the hands-free agent loop.
//
//   GET  /voice/status   — capability probe for mobile / SDK clients
//   WS   /voice/stream   — bidirectional voice session
//
// /voice/stream client protocol:
//
//   client → server (first message, JSON text frame):
//     {"type":"start", "project":"yaver", "model":"sonnet", "runner":""}
//
//   client → server (PCM 16-bit LE, 16kHz mono, ~20-40ms chunks, binary):
//     <raw bytes>
//
//   client → server (PTT release / explicit finalize, JSON):
//     {"type":"stop"}
//
//   server → client (JSON text frames, all):
//     {"type":"transcript-partial", "text":"..."}
//     {"type":"transcript-final",   "text":"..."}
//     {"type":"task-created",       "taskId":"..."}
//     {"type":"task-result",        "taskId":"...", "text":"...", "status":"completed"}
//     {"type":"tts-frame",          "pcm":"<base64>", "sampleRate":22050}
//     {"type":"done"}
//     {"type":"error",              "error":"..."}
//
// The stream stays open across the whole turn: speak → final transcript
// → task fires → result speaks back. Closing the WS at any point aborts
// the in-flight task gracefully.

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"log"
	"net/http"
	"time"

	"github.com/gorilla/websocket"
)

var voiceUpgrader = websocket.Upgrader{
	ReadBufferSize:  4096,
	WriteBufferSize: 4096,
	CheckOrigin:     func(r *http.Request) bool { return true }, // SDK clients live on different origins
}

// voiceStartFrame is the JSON the client sends as its first message.
type voiceStartFrame struct {
	Type    string `json:"type"`
	Project string `json:"project,omitempty"`
	Model   string `json:"model,omitempty"`
	Runner  string `json:"runner,omitempty"`
	// Surface tells the prompt wrapper what display the user is on.
	// Examples: "mobile-phone", "web-spatial-vr", "glasses-mentra-display".
	// See TaskViewport docstring for full enum.
	Surface   string `json:"surface,omitempty"`
	PaneCount int    `json:"paneCount,omitempty"`
	TTSBudget int    `json:"ttsBudget,omitempty"`
}

// voiceCtrlFrame covers all other client-side control frames.
type voiceCtrlFrame struct {
	Type string `json:"type"`
}

// handleVoiceStatus returns enabled/ready flags + which providers
// are configured. Mobile uses this on app boot to decide whether to
// render the mic UI.
func (s *HTTPServer) handleVoiceStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, http.StatusMethodNotAllowed, "use GET")
		return
	}
	cfg, _ := LoadConfig()
	v := voiceCfgOrNil(cfg)
	result := map[string]interface{}{
		"ok":             true,
		"enabled":        v != nil && v.Enabled,
		"sttProvider":    "deepgram-flux",
		"sttReady":       v != nil && v.DeepgramAPIKey != "",
		"ttsProvider":    "cartesia-sonic",
		"ttsReady":       v != nil && v.CartesiaAPIKey != "",
		"defaultProject": "",
	}
	if v != nil {
		result["defaultProject"] = v.DefaultProject
	}
	jsonReply(w, http.StatusOK, result)
}

// handleVoiceStream is the long-lived WebSocket handler.
func (s *HTTPServer) handleVoiceStream(w http.ResponseWriter, r *http.Request) {
	cfg, _ := LoadConfig()
	v := voiceCfgOrNil(cfg)
	if v == nil || !v.Enabled {
		jsonError(w, http.StatusServiceUnavailable, "voice not enabled in config — set voice.enabled=true and supply api keys")
		return
	}
	if v.DeepgramAPIKey == "" {
		jsonError(w, http.StatusServiceUnavailable, "deepgram api key not configured")
		return
	}

	conn, err := voiceUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return // Upgrader already wrote the error
	}
	defer conn.Close()

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	// Phase 1: wait for the client's start frame (with timeout so a
	// stalled mobile doesn't hold a goroutine forever).
	_ = conn.SetReadDeadline(time.Now().Add(15 * time.Second))
	mt, payload, err := conn.ReadMessage()
	if err != nil {
		return
	}
	_ = conn.SetReadDeadline(time.Time{})
	if mt != websocket.TextMessage {
		voiceWriteErr(conn, "first frame must be a JSON 'start' message")
		return
	}
	var start voiceStartFrame
	if err := json.Unmarshal(payload, &start); err != nil || start.Type != "start" {
		voiceWriteErr(conn, "invalid start frame")
		return
	}

	// Resolve project keyterms for STT bias.
	project := start.Project
	if project == "" {
		project = v.DefaultProject
	}
	var keyterms []string
	if project != "" && v.ProjectKeyterms != nil {
		keyterms = v.ProjectKeyterms[project]
	}

	dg, dgEvents, err := OpenDeepgramSession(ctx, v.DeepgramAPIKey, "nova-3", keyterms)
	if err != nil {
		voiceWriteErr(conn, "deepgram: "+err.Error())
		return
	}
	defer dg.Close()

	// Fan-in: client audio + STT events.
	clientIn := make(chan voiceClientMsg, 16)
	go voiceReadClient(ctx, conn, clientIn)

	var finalText string
loop:
	for {
		select {
		case <-ctx.Done():
			break loop
		case msg, ok := <-clientIn:
			if !ok {
				break loop
			}
			if msg.kind == "audio" {
				if err := dg.SendAudio(msg.audio); err != nil {
					voiceWriteErr(conn, "stt audio write: "+err.Error())
					break loop
				}
			} else if msg.kind == "stop" {
				_ = dg.Finalize()
			} else if msg.kind == "close" {
				break loop
			}
		case ev, ok := <-dgEvents:
			if !ok {
				break loop
			}
			switch ev.Kind {
			case "partial":
				voiceWriteJSON(conn, map[string]interface{}{"type": "transcript-partial", "text": ev.Text})
			case "final":
				finalText = ev.Text
				voiceWriteJSON(conn, map[string]interface{}{"type": "transcript-final", "text": ev.Text})
			case "eot":
				if finalText != "" {
					break loop
				}
			case "closed":
				if ev.Error != "" {
					voiceWriteErr(conn, "stt closed: "+ev.Error)
				}
				break loop
			case "error":
				voiceWriteErr(conn, "stt: "+ev.Error)
				break loop
			}
		}
	}

	if finalText == "" {
		voiceWriteErr(conn, "no transcript captured")
		return
	}

	// Fast path: "launch <slug>" / "open <slug>" / "start <slug>"
	// short-circuit straight to Hermes-push without a Claude roundtrip.
	if intent := LaunchIntentMatch(finalText); intent != nil {
		launchRes := HandleVoiceLaunch(ctx, intent, cfg, s.voiceLauncher())
		// Glass + VR subscribers want to know the app actually came
		// up. The launcher already broadcast the open_app command; on
		// success we also fire app_reloaded so the surface can render
		// its confirmation (Mentra speaks, /spatial pops a pane).
		if launchRes.OK {
			BroadcastAppReloaded(s.blackboxMgr, intent.Slug, "", "", "")
		}
		voiceWriteJSON(conn, map[string]interface{}{
			"type":   "task-result",
			"taskId": "",
			"text":   launchRes.SpokenResponse,
			"status": launchOKStatus(launchRes.OK),
		})
		if v.CartesiaAPIKey != "" && launchRes.SpokenResponse != "" {
			ttsCh := make(chan CartesiaFrame, 8)
			go SpeakCartesia(ctx, v.CartesiaAPIKey, v.CartesiaVoiceID, launchRes.SpokenResponse, ttsCh)
			for fr := range ttsCh {
				if fr.Error != "" {
					break
				}
				if len(fr.PCM) > 0 {
					voiceWriteJSON(conn, map[string]interface{}{
						"type":       "tts-frame",
						"pcm":        base64.StdEncoding.EncodeToString(fr.PCM),
						"sampleRate": 22050,
					})
				}
				if fr.Done {
					break
				}
			}
		}
		voiceWriteJSON(conn, map[string]interface{}{"type": "done"})
		return
	}

	// Fire the task. Block until terminal status, then speak result.
	result, derr := DispatchVoiceTranscript(ctx, s.taskMgr, finalText, VoiceDispatchOptions{
		Project: project,
		Model:   start.Model,
		Runner:  start.Runner,
		Viewport: &TaskViewport{
			Surface:   start.Surface,
			PaneCount: start.PaneCount,
			TTSBudget: start.TTSBudget,
		},
	})
	if result != nil && result.TaskID != "" {
		voiceWriteJSON(conn, map[string]interface{}{"type": "task-created", "taskId": result.TaskID})
	}
	if derr != nil {
		voiceWriteErr(conn, derr.Error())
		return
	}
	voiceWriteJSON(conn, map[string]interface{}{
		"type":   "task-result",
		"taskId": result.TaskID,
		"text":   result.ResultText,
		"status": result.Status,
	})

	// TTS readback — skip silently if Cartesia not configured.
	if v.CartesiaAPIKey != "" && result.ResultText != "" {
		ttsCh := make(chan CartesiaFrame, 8)
		go SpeakCartesia(ctx, v.CartesiaAPIKey, v.CartesiaVoiceID, voiceTrimForTTS(result.ResultText), ttsCh)
		for fr := range ttsCh {
			if fr.Error != "" {
				log.Printf("[voice] tts error: %s", fr.Error)
				break
			}
			if len(fr.PCM) > 0 {
				voiceWriteJSON(conn, map[string]interface{}{
					"type":       "tts-frame",
					"pcm":        base64.StdEncoding.EncodeToString(fr.PCM),
					"sampleRate": 22050,
				})
			}
			if fr.Done {
				break
			}
		}
	}

	voiceWriteJSON(conn, map[string]interface{}{"type": "done"})
}

// voiceTrimForTTS keeps the audio short. Long agent monologues are
// unlistenable; we read the headline and tell the user to glance at
// the screen for the rest.
func voiceTrimForTTS(text string) string {
	const max = 280
	if len(text) <= max {
		return text
	}
	return text[:max] + " — see screen for the rest."
}

type voiceClientMsg struct {
	kind  string // "audio" | "stop" | "close"
	audio []byte
}

func voiceReadClient(ctx context.Context, conn *websocket.Conn, out chan<- voiceClientMsg) {
	defer close(out)
	for {
		if ctx.Err() != nil {
			return
		}
		mt, payload, err := conn.ReadMessage()
		if err != nil {
			out <- voiceClientMsg{kind: "close"}
			return
		}
		switch mt {
		case websocket.BinaryMessage:
			b := make([]byte, len(payload))
			copy(b, payload)
			out <- voiceClientMsg{kind: "audio", audio: b}
		case websocket.TextMessage:
			var ctrl voiceCtrlFrame
			if err := json.Unmarshal(payload, &ctrl); err == nil && ctrl.Type == "stop" {
				out <- voiceClientMsg{kind: "stop"}
			}
		}
	}
}

func voiceWriteJSON(conn *websocket.Conn, v interface{}) {
	_ = conn.WriteJSON(v)
}

func voiceWriteErr(conn *websocket.Conn, msg string) {
	voiceWriteJSON(conn, map[string]interface{}{"type": "error", "error": msg})
}

func voiceCfgOrNil(cfg *Config) *VoiceConfig {
	if cfg == nil {
		return nil
	}
	return cfg.Voice
}

func launchOKStatus(ok bool) string {
	if ok {
		return "launched"
	}
	return "launch-failed"
}

// voiceLauncher returns a VoiceLauncher bound to this HTTPServer's
// blackbox bus. After the bundle smoke test passes, broadcasts the
// same "open_app" command that `yaver insert <app>` uses — every
// paired mobile picks it up via /blackbox/command-stream SSE and
// loads the bundle via the existing Hermes-push path.
func (s *HTTPServer) voiceLauncher() VoiceLauncher {
	return func(workDir, slug string) error {
		if s.blackboxMgr == nil {
			return nil // no paired phones — silent no-op for v1
		}
		s.blackboxMgr.BroadcastCommand(BlackBoxCommand{
			Command: "open_app",
			Data: map[string]interface{}{
				"app":     slug,
				"workDir": workDir,
				"reason":  "voice-launch",
			},
		})
		return nil
	}
}
