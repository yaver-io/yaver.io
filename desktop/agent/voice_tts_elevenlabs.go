package main

// ElevenLabs Flash v2.5 streaming TTS client.
//
// Why ElevenLabs Flash: ~75ms TTFA, 32-language multilingual model, top
// voice quality. Sits in the "premium voice / lowest latency" slot of
// Yaver's TTS picker — pick this over Cartesia when voice character
// matters more than raw model latency or per-character price.
//
// Wire format (stream-input):
//
//   wss://api.elevenlabs.io/v1/text-to-speech/<voice_id>/stream-input
//     ?model_id=eleven_flash_v2_5
//     &output_format=pcm_16000        (matches voice_http's 16kHz path)
//
//   xi-api-key header (or xi_api_key query param)
//
//   Send  (JSON text frames):
//     {"text":" ","voice_settings":{...}}     initial config
//     {"text":"hello world","try_trigger_generation":true}
//     {"text":""}                              flush + end
//
//   Recv (JSON text frames):
//     {"audio":"<base64 pcm>","isFinal":false,"normalizedAlignment":...}
//     {"audio":null,"isFinal":true}
//
// We emit the same CartesiaFrame envelope so voice_http's playback
// path is provider-agnostic.

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

// ElevenLabsURL is the base TTS endpoint; the voice id slots in via
// path. Var so tests can swap.
var ElevenLabsURL = "wss://api.elevenlabs.io/v1/text-to-speech"

// ElevenLabsDefaultVoice is "Rachel" — a recognizable neutral
// preset that ships with every ElevenLabs account. Override via
// VoiceConfig.ElevenLabsTTSVoiceID.
const ElevenLabsDefaultVoice = "21m00Tcm4TlvDq8ikWAM"

// ElevenLabsDefaultModel is Flash v2.5 — the latency-optimized
// multilingual model. Other valid models include
// eleven_turbo_v2_5 (English-only, slightly faster) and
// eleven_multilingual_v2 (29 langs, higher quality but +200ms TTFA).
const ElevenLabsDefaultModel = "eleven_flash_v2_5"

// elevenLabsInit is the first JSON frame we send. voice_settings are
// the standard ElevenLabs knobs — defaults chosen for snappy reads.
type elevenLabsInit struct {
	Text          string `json:"text"` // single space starts the session
	VoiceSettings struct {
		Stability       float64 `json:"stability"`
		SimilarityBoost float64 `json:"similarity_boost"`
		Speed           float64 `json:"speed,omitempty"`
		UseSpeakerBoost bool    `json:"use_speaker_boost,omitempty"`
	} `json:"voice_settings"`
	GenerationConfig struct {
		ChunkLengthSchedule []int `json:"chunk_length_schedule"`
	} `json:"generation_config"`
	XIAPIKey string `json:"xi_api_key,omitempty"`
}

type elevenLabsText struct {
	Text                 string `json:"text"`
	TryTriggerGeneration bool   `json:"try_trigger_generation,omitempty"`
}

type elevenLabsIn struct {
	Audio   string `json:"audio,omitempty"` // base64-encoded PCM
	IsFinal bool   `json:"isFinal,omitempty"`
	Message string `json:"message,omitempty"` // error payloads
	Error   string `json:"error,omitempty"`
}

// SpeakElevenLabs synthesizes text via ElevenLabs Flash v2.5 and
// streams PCM-16LE 16kHz frames into out. Reuses the CartesiaFrame
// shape so voice_http's playback path stays provider-agnostic.
//
// Blocking — call in a goroutine. Honors ctx for cancellation.
func SpeakElevenLabs(ctx context.Context, apiKey, voiceID, modelID, text string, out chan<- CartesiaFrame) {
	defer close(out)

	if apiKey == "" {
		out <- CartesiaFrame{Error: "elevenlabs api key not configured"}
		return
	}
	if voiceID = strings.TrimSpace(voiceID); voiceID == "" {
		voiceID = ElevenLabsDefaultVoice
	}
	if modelID = strings.TrimSpace(modelID); modelID == "" {
		modelID = ElevenLabsDefaultModel
	}

	q := url.Values{}
	q.Set("model_id", modelID)
	q.Set("output_format", "pcm_16000") // matches voice_http 16kHz PCM path

	hdr := http.Header{}
	hdr.Set("xi-api-key", apiKey)

	dialer := websocket.Dialer{HandshakeTimeout: 8 * time.Second}
	u := fmt.Sprintf("%s/%s/stream-input?%s", ElevenLabsURL, voiceID, q.Encode())
	conn, _, err := dialer.DialContext(ctx, u, hdr)
	if err != nil {
		out <- CartesiaFrame{Error: fmt.Sprintf("elevenlabs dial: %v", err)}
		return
	}
	defer conn.Close()

	var init elevenLabsInit
	init.Text = " "
	init.VoiceSettings.Stability = 0.5
	init.VoiceSettings.SimilarityBoost = 0.8
	init.VoiceSettings.UseSpeakerBoost = true
	// Trigger generation aggressively — first chunk after only ~50 chars
	// of text, then ramp up so longer reads aren't choppy.
	init.GenerationConfig.ChunkLengthSchedule = []int{50, 90, 120, 150}
	if err := conn.WriteJSON(init); err != nil {
		out <- CartesiaFrame{Error: fmt.Sprintf("elevenlabs init: %v", err)}
		return
	}

	if err := conn.WriteJSON(elevenLabsText{Text: text, TryTriggerGeneration: true}); err != nil {
		out <- CartesiaFrame{Error: fmt.Sprintf("elevenlabs text: %v", err)}
		return
	}
	// Empty-text frame flushes the session and signals end-of-input.
	if err := conn.WriteJSON(elevenLabsText{Text: ""}); err != nil {
		out <- CartesiaFrame{Error: fmt.Sprintf("elevenlabs flush: %v", err)}
		return
	}

	for {
		if ctx.Err() != nil {
			return
		}
		_, payload, err := conn.ReadMessage()
		if err != nil {
			out <- CartesiaFrame{Error: fmt.Sprintf("elevenlabs read: %v", err)}
			return
		}
		var in elevenLabsIn
		if err := json.Unmarshal(payload, &in); err != nil {
			continue
		}
		if in.Error != "" || in.Message != "" {
			msg := in.Error
			if msg == "" {
				msg = in.Message
			}
			out <- CartesiaFrame{Error: msg}
			return
		}
		if in.Audio != "" {
			pcm, derr := base64.StdEncoding.DecodeString(in.Audio)
			if derr != nil {
				continue
			}
			select {
			case out <- CartesiaFrame{PCM: pcm}:
			case <-ctx.Done():
				return
			}
		}
		if in.IsFinal {
			out <- CartesiaFrame{Done: true}
			return
		}
	}
}
