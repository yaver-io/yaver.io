package main

// Deepgram Aura-2 streaming TTS client.
//
// Why Deepgram Aura-2: pairs with the already-wired Deepgram Flux STT so
// a user can run the full voice loop on a single signup + single API key.
// Pricing is ~$0.030/1k chars (roughly half Cartesia Sonic-3) with
// comparable real-time latency and 40+ English voices. Sits in the
// "one-vendor, lowest billing-friction" slot of Yaver's TTS picker.
//
// Wire format (WebSocket):
//
//   wss://api.deepgram.com/v1/speak
//     ?model=aura-2-thalia-en
//     &encoding=linear16
//     &sample_rate=24000
//     &container=none
//
//   Authorization: Token <api_key>
//
//   Send (JSON text frames):
//     {"type":"Speak","text":"hello world"}
//     {"type":"Flush"}                    ask server to emit pending audio
//     {"type":"Close"}                    end session
//
//   Recv:
//     binary frames = raw PCM 16-bit LE @ sample_rate
//     JSON text frames = control / lifecycle (Metadata, Flushed, Warning,
//                        Error). We only care about Error / Flushed.
//
// We emit the same CartesiaFrame envelope so voice_http's playback path
// stays provider-agnostic.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

// DeepgramTTSURL is the streaming speak endpoint. Var so tests can swap.
var DeepgramTTSURL = "wss://api.deepgram.com/v1/speak"

// DeepgramTTSDefaultModel is Aura-2 Thalia — Deepgram's flagship English
// voice, natural conversational tone, real-time latency. Override via
// VoiceConfig.DeepgramTTSModel (e.g. "aura-2-asteria-en", "aura-2-orion-en").
const DeepgramTTSDefaultModel = "aura-2-thalia-en"

// DeepgramTTSSampleRate is the PCM sample rate emitted by Aura-2 when
// container=none + encoding=linear16. Exposed so voice_http can stamp
// the matching sampleRate on outbound frames.
const DeepgramTTSSampleRate = 24000

// deepgramTTSCtrl is the JSON envelope we send.
type deepgramTTSCtrl struct {
	Type string `json:"type"`           // "Speak" | "Flush" | "Close"
	Text string `json:"text,omitempty"` // only on Speak
}

// deepgramTTSIn is the JSON we read for control / error frames.
// Audio arrives as binary, not in this struct.
type deepgramTTSIn struct {
	Type        string `json:"type"`
	Description string `json:"description,omitempty"`
	Message     string `json:"message,omitempty"`
}

// SpeakDeepgram synthesizes `text` via Deepgram Aura-2 and streams PCM
// frames into `out`. Closes `out` when done.
//
// Blocking — call in a goroutine. Honors ctx for cancellation.
func SpeakDeepgram(ctx context.Context, apiKey, model, text string, out chan<- CartesiaFrame) {
	defer close(out)

	if strings.TrimSpace(apiKey) == "" {
		out <- CartesiaFrame{Error: "deepgram api key not configured"}
		return
	}
	if model = strings.TrimSpace(model); model == "" {
		model = DeepgramTTSDefaultModel
	}

	q := url.Values{}
	q.Set("model", model)
	q.Set("encoding", "linear16")
	q.Set("sample_rate", fmt.Sprintf("%d", DeepgramTTSSampleRate))
	q.Set("container", "none")

	hdr := http.Header{}
	hdr.Set("Authorization", "Token "+apiKey)

	dialer := websocket.Dialer{HandshakeTimeout: 8 * time.Second}
	conn, _, err := dialer.DialContext(ctx, DeepgramTTSURL+"?"+q.Encode(), hdr)
	if err != nil {
		out <- CartesiaFrame{Error: fmt.Sprintf("deepgram tts dial: %v", err)}
		return
	}
	defer conn.Close()

	if err := conn.WriteJSON(deepgramTTSCtrl{Type: "Speak", Text: text}); err != nil {
		out <- CartesiaFrame{Error: fmt.Sprintf("deepgram tts speak: %v", err)}
		return
	}
	if err := conn.WriteJSON(deepgramTTSCtrl{Type: "Flush"}); err != nil {
		out <- CartesiaFrame{Error: fmt.Sprintf("deepgram tts flush: %v", err)}
		return
	}

	for {
		if ctx.Err() != nil {
			return
		}
		mt, payload, err := conn.ReadMessage()
		if err != nil {
			out <- CartesiaFrame{Error: fmt.Sprintf("deepgram tts read: %v", err)}
			return
		}
		switch mt {
		case websocket.BinaryMessage:
			if len(payload) == 0 {
				continue
			}
			pcm := make([]byte, len(payload))
			copy(pcm, payload)
			select {
			case out <- CartesiaFrame{PCM: pcm}:
			case <-ctx.Done():
				return
			}
		case websocket.TextMessage:
			var in deepgramTTSIn
			if err := json.Unmarshal(payload, &in); err != nil {
				continue
			}
			switch in.Type {
			case "Flushed":
				// Server has emitted everything pending — end the turn.
				_ = conn.WriteJSON(deepgramTTSCtrl{Type: "Close"})
				out <- CartesiaFrame{Done: true}
				return
			case "Error":
				msg := in.Description
				if msg == "" {
					msg = in.Message
				}
				if msg == "" {
					msg = "unknown deepgram tts error"
				}
				out <- CartesiaFrame{Error: msg}
				return
			}
		}
	}
}
