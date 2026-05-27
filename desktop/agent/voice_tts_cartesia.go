package main

// Cartesia Sonic-3 streaming TTS client.
//
// Why Cartesia: 40ms model latency, expressive, cheap. We feed it the
// agent's result text after a voice task finishes ("Touched login.tsx
// +12 -3. Approve?") and stream PCM audio frames back to the client.
//
// The client-side WS bridge plays the frames in order via Web Audio
// (web) / AudioContext / AVAudioEngine — no decoding required because
// Cartesia emits PCM 16-bit / 22050Hz mono when output_format.container
// is set to "raw".

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"time"

	"github.com/gorilla/websocket"
)

// CartesiaURL is the streaming TTS endpoint. Var, not const, so tests
// can swap in a local WS fixture.
var CartesiaURL = "wss://api.cartesia.ai/tts/websocket"

// CartesiaDefaultVoice is a generic neutral voice id used when the
// user hasn't picked one. Override via VoiceConfig.CartesiaVoiceID.
const CartesiaDefaultVoice = "a0e99841-438c-4a64-b679-ae501e7d6091"

// CartesiaFrame is one audio chunk delivered to the caller.
type CartesiaFrame struct {
	PCM   []byte // PCM 16-bit LE, 22050Hz mono — playable as-is
	Done  bool   // true on the final frame
	Error string
}

// cartesiaWire is the JSON envelope we send.
type cartesiaWire struct {
	ModelID        string `json:"model_id"`
	Transcript     string `json:"transcript"`
	Voice          struct {
		Mode string `json:"mode"`
		ID   string `json:"id"`
	} `json:"voice"`
	OutputFormat struct {
		Container  string `json:"container"`
		Encoding   string `json:"encoding"`
		SampleRate int    `json:"sample_rate"`
	} `json:"output_format"`
	Language    string `json:"language"`
	ContextID   string `json:"context_id,omitempty"`
	Continue    bool   `json:"continue,omitempty"`
}

// cartesiaIn is the JSON we read from the server. Cartesia ships
// audio inside JSON frames as base64 — no raw binary WS frames.
type cartesiaIn struct {
	Type     string `json:"type"` // "chunk" | "done" | "error"
	Data     string `json:"data,omitempty"`
	StepTime int    `json:"step_time,omitempty"`
	Done     bool   `json:"done,omitempty"`
	Error    string `json:"error,omitempty"`
}

// SpeakCartesia synthesizes `text` using `voiceID` (or the default if
// empty) and streams PCM frames into `out`. Closes `out` when done.
//
// Blocking — call in a goroutine. Honors ctx for cancellation.
func SpeakCartesia(ctx context.Context, apiKey, voiceID, text string, out chan<- CartesiaFrame) {
	defer close(out)

	if apiKey == "" {
		out <- CartesiaFrame{Error: "cartesia api key not configured"}
		return
	}
	if voiceID == "" {
		voiceID = CartesiaDefaultVoice
	}

	q := url.Values{}
	q.Set("api_key", apiKey)
	q.Set("cartesia_version", "2024-11-13")

	dialer := websocket.Dialer{HandshakeTimeout: 8 * time.Second}
	conn, _, err := dialer.DialContext(ctx, CartesiaURL+"?"+q.Encode(), nil)
	if err != nil {
		out <- CartesiaFrame{Error: fmt.Sprintf("cartesia dial: %v", err)}
		return
	}
	defer conn.Close()

	req := cartesiaWire{
		ModelID:    "sonic-2",
		Transcript: text,
		Language:   "en",
	}
	req.Voice.Mode = "id"
	req.Voice.ID = voiceID
	req.OutputFormat.Container = "raw"
	req.OutputFormat.Encoding = "pcm_s16le"
	req.OutputFormat.SampleRate = 22050

	if err := conn.WriteJSON(req); err != nil {
		out <- CartesiaFrame{Error: fmt.Sprintf("cartesia write: %v", err)}
		return
	}

	for {
		if ctx.Err() != nil {
			return
		}
		_, payload, err := conn.ReadMessage()
		if err != nil {
			out <- CartesiaFrame{Error: fmt.Sprintf("cartesia read: %v", err)}
			return
		}
		var in cartesiaIn
		if err := json.Unmarshal(payload, &in); err != nil {
			continue
		}
		switch in.Type {
		case "chunk":
			pcm, derr := base64.StdEncoding.DecodeString(in.Data)
			if derr != nil {
				continue
			}
			select {
			case out <- CartesiaFrame{PCM: pcm}:
			case <-ctx.Done():
				return
			}
			if in.Done {
				out <- CartesiaFrame{Done: true}
				return
			}
		case "done":
			out <- CartesiaFrame{Done: true}
			return
		case "error":
			out <- CartesiaFrame{Error: in.Error}
			return
		}
	}
}
