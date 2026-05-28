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

// handleVoiceStatus returns enabled/ready flags + which providers are
// configured. Mobile + /spatial use this on app boot to decide whether
// to render the mic UI at all — when the user has only configured the
// keyboard-on-glasses path (no voice keys), the mic orb hides itself
// gracefully instead of failing mid-loop.
func (s *HTTPServer) handleVoiceStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, http.StatusMethodNotAllowed, "use GET")
		return
	}
	cfg, _ := LoadConfig()
	v := voiceCfgOrNil(cfg)

	sttProvider := "openai"
	ttsProvider := "openai"
	sttReady := false
	ttsReady := false
	defaultProject := ""

	if v != nil {
		sttProvider = v.EffectiveSTTProvider()
		ttsProvider = v.EffectiveTTSProvider()
		defaultProject = v.DefaultProject

		switch sttProvider {
		case "openai":
			sttReady = HasVoiceCredential("openai", "api-key", v.OpenAIAPIKey)
		case "deepgram":
			sttReady = HasVoiceCredential("deepgram", "api-key", v.DeepgramAPIKey)
		case "assemblyai":
			sttReady = HasVoiceCredential("assemblyai", "api-key", v.AssemblyAIAPIKey)
		case "on-device":
			sttReady = true // mobile owns capture; agent has no key to set
		}
		switch ttsProvider {
		case "openai":
			ttsReady = HasVoiceCredential("openai", "api-key", v.OpenAIAPIKey)
		case "cartesia":
			ttsReady = HasVoiceCredential("cartesia", "api-key", v.CartesiaAPIKey)
		case "elevenlabs":
			ttsReady = HasVoiceCredential("elevenlabs", "api-key", v.ElevenLabsAPIKey)
		case "deepgram":
			// Same key as Deepgram STT — one signup, one credential.
			ttsReady = HasVoiceCredential("deepgram", "api-key", v.DeepgramAPIKey)
		case "device":
			ttsReady = true // mobile owns playback via AVSpeech / TextToSpeech
		}
	}

	// Per-provider key-set booleans so the mobile picker can show the
	// "key set ✓" badge for every provider, not only the currently
	// selected one. Each lookup hits the credential resolver, which is
	// fast (vault map lookup); skip when there's no VoiceConfig yet.
	openaiSet := false
	deepgramSet := false
	cartesiaSet := false
	assemblyaiSet := false
	elevenlabsSet := false
	if v != nil {
		openaiSet = HasVoiceCredential("openai", "api-key", v.OpenAIAPIKey)
		deepgramSet = HasVoiceCredential("deepgram", "api-key", v.DeepgramAPIKey)
		cartesiaSet = HasVoiceCredential("cartesia", "api-key", v.CartesiaAPIKey)
		assemblyaiSet = HasVoiceCredential("assemblyai", "api-key", v.AssemblyAIAPIKey)
		elevenlabsSet = HasVoiceCredential("elevenlabs", "api-key", v.ElevenLabsAPIKey)
	}

	jsonReply(w, http.StatusOK, map[string]interface{}{
		"ok":             true,
		"enabled":        v != nil && v.Enabled,
		"sttProvider":    sttProvider,
		"sttReady":       sttReady,
		"ttsProvider":    ttsProvider,
		"ttsReady":       ttsReady,
		"defaultProject": defaultProject,
		"openaiSet":      openaiSet,
		"deepgramSet":    deepgramSet,
		"cartesiaSet":    cartesiaSet,
		"assemblyaiSet":  assemblyaiSet,
		"elevenlabsSet":  elevenlabsSet,
		// availableProviders lets the mobile Settings UI render the
		// picker even on first launch (no key set yet).
		"availableProviders": map[string][]string{
			"stt": {"openai", "deepgram", "assemblyai", "on-device"},
			"tts": {"openai", "deepgram", "cartesia", "elevenlabs", "device"},
		},
	})
}

// handleVoiceStream is the long-lived WebSocket handler.
func (s *HTTPServer) handleVoiceStream(w http.ResponseWriter, r *http.Request) {
	cfg, _ := LoadConfig()
	v := voiceCfgOrNil(cfg)
	if v == nil || !v.Enabled {
		jsonError(w, http.StatusServiceUnavailable, "voice not enabled in config — set voice.enabled=true and supply an api key (openai by default). Keyboard-on-glasses users without voice keys can ignore this and use the agent normally.")
		return
	}
	sttProvider := v.EffectiveSTTProvider()
	switch sttProvider {
	case "openai":
		if !HasVoiceCredential("openai", "api-key", v.OpenAIAPIKey) {
			jsonError(w, http.StatusServiceUnavailable, "openai api key not configured (stt_provider=openai)")
			return
		}
	case "deepgram":
		if !HasVoiceCredential("deepgram", "api-key", v.DeepgramAPIKey) {
			jsonError(w, http.StatusServiceUnavailable, "deepgram api key not configured (stt_provider=deepgram)")
			return
		}
	case "assemblyai":
		if !HasVoiceCredential("assemblyai", "api-key", v.AssemblyAIAPIKey) {
			jsonError(w, http.StatusServiceUnavailable, "assemblyai api key not configured (stt_provider=assemblyai)")
			return
		}
	case "on-device":
		// Mobile owns capture; the agent never opens a session for this
		// path. Reject voice-stream attempts so the caller knows it's
		// the wrong endpoint — the mobile transcribes locally and
		// posts the final transcript to /tasks directly.
		jsonError(w, http.StatusBadRequest, "stt_provider=on-device — mobile must transcribe locally and POST /tasks; do not open /voice/stream for this provider")
		return
	default:
		jsonError(w, http.StatusBadRequest, "unknown stt_provider "+sttProvider+" — supported: openai, deepgram, assemblyai, on-device")
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

	// Open the configured STT provider. Each one publishes
	// DeepgramEvent on the channel (the type name predates
	// provider abstraction; it's effectively STTEvent now).
	var sttClose func() error
	var dgEvents <-chan DeepgramEvent
	var sttSendAudio func([]byte) error
	var sttFinalize func() error
	switch sttProvider {
	case "openai":
		key := LookupVoiceCredential("openai", "api-key", v.OpenAIAPIKey)
		sess, ev, err := OpenOpenAIWhisperSession(ctx, key, v.OpenAISTTModel)
		if err != nil {
			voiceWriteErr(conn, "openai stt: "+err.Error())
			return
		}
		dgEvents = ev
		sttSendAudio = sess.SendAudio
		sttFinalize = sess.Finalize
		sttClose = sess.Close
	case "deepgram":
		key := LookupVoiceCredential("deepgram", "api-key", v.DeepgramAPIKey)
		sess, ev, err := OpenDeepgramSession(ctx, key, "nova-3", keyterms)
		if err != nil {
			voiceWriteErr(conn, "deepgram: "+err.Error())
			return
		}
		dgEvents = ev
		sttSendAudio = sess.SendAudio
		sttFinalize = sess.Finalize
		sttClose = sess.Close
	case "assemblyai":
		key := LookupVoiceCredential("assemblyai", "api-key", v.AssemblyAIAPIKey)
		sess, ev, err := OpenAssemblyAISession(ctx, key, v.AssemblyAILanguage)
		if err != nil {
			voiceWriteErr(conn, "assemblyai: "+err.Error())
			return
		}
		dgEvents = ev
		sttSendAudio = sess.SendAudio
		sttFinalize = sess.Finalize
		sttClose = sess.Close
	}
	defer sttClose()

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
				if err := sttSendAudio(msg.audio); err != nil {
					voiceWriteErr(conn, "stt audio write: "+err.Error())
					break loop
				}
			} else if msg.kind == "stop" {
				_ = sttFinalize()
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
		if launchRes.SpokenResponse != "" {
			streamTTS(ctx, conn, v, launchRes.SpokenResponse)
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

	// TTS readback — skip silently if no TTS provider is configured.
	if result.ResultText != "" {
		streamTTS(ctx, conn, v, voiceTrimForTTS(result.ResultText))
	}

	voiceWriteJSON(conn, map[string]interface{}{"type": "done"})
}

// streamTTS picks the configured TTS provider (OpenAI default,
// Cartesia alternate) and streams its PCM output to the WS as
// tts-frame messages. Skips silently when no provider key is set —
// so a keyboard-on-glasses user without voice keys gets clean text
// results without errors.
func streamTTS(ctx context.Context, conn *websocket.Conn, v *VoiceConfig, text string) {
	if v == nil || text == "" {
		return
	}
	provider := v.EffectiveTTSProvider()
	ttsCh := make(chan CartesiaFrame, 8)
	sampleRate := 22050
	switch provider {
	case "openai":
		key := LookupVoiceCredential("openai", "api-key", v.OpenAIAPIKey)
		if key == "" {
			return
		}
		sampleRate = 24000 // OpenAI TTS pcm response is 24kHz
		go SpeakOpenAI(ctx, key, v.OpenAITTSModel, v.OpenAITTSVoice, text, ttsCh)
	case "cartesia":
		key := LookupVoiceCredential("cartesia", "api-key", v.CartesiaAPIKey)
		if key == "" {
			return
		}
		go SpeakCartesia(ctx, key, v.CartesiaVoiceID, text, ttsCh)
	case "elevenlabs":
		key := LookupVoiceCredential("elevenlabs", "api-key", v.ElevenLabsAPIKey)
		if key == "" {
			return
		}
		sampleRate = 16000 // ElevenLabs configured for pcm_16000
		go SpeakElevenLabs(ctx, key, v.ElevenLabsTTSVoiceID, v.ElevenLabsTTSModel, text, ttsCh)
	case "deepgram":
		key := LookupVoiceCredential("deepgram", "api-key", v.DeepgramAPIKey)
		if key == "" {
			return
		}
		sampleRate = DeepgramTTSSampleRate // Aura-2 with linear16 = 24kHz PCM
		go SpeakDeepgram(ctx, key, v.DeepgramTTSModel, text, ttsCh)
	case "device":
		// Mobile owns playback via AVSpeechSynthesizer / TextToSpeech.
		// Nothing to stream from the agent — caller infers from the
		// task-result frame and synthesizes locally.
		return
	default:
		return
	}
	for fr := range ttsCh {
		if fr.Error != "" {
			log.Printf("[voice] tts error (%s): %s", provider, fr.Error)
			break
		}
		if len(fr.PCM) > 0 {
			voiceWriteJSON(conn, map[string]interface{}{
				"type":       "tts-frame",
				"pcm":        base64.StdEncoding.EncodeToString(fr.PCM),
				"sampleRate": sampleRate,
			})
		}
		if fr.Done {
			break
		}
	}
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
