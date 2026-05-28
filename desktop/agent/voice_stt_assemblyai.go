package main

// AssemblyAI Universal-Streaming STT client.
//
// Why AssemblyAI: 99+ languages (including Turkish), ~$0.0025/min — the
// cheapest mainstream STT in the 2026 market — and stable wire format
// that hasn't churned in a year. Sits in the "budget multilingual" slot
// of Yaver's STT picker.
//
// Wire format (Universal-Streaming v3):
//   wss://streaming.assemblyai.com/v3/ws?sample_rate=16000&encoding=pcm_s16le
//   Authorization: <api_key>           (raw key, no "Bearer" prefix)
//   Audio in: binary frames, 16-bit PCM, 16kHz mono, 50-1000ms each
//   Frames out: JSON
//     {"type":"PartialTranscript","text":"...","end_of_turn":false}
//     {"type":"FinalTranscript","text":"...","end_of_turn":true}
//     {"type":"Termination",...}
//
// AAI's end_of_turn is server-detected (silence + acoustic cues) — close
// enough to Flux's model-native EOT for the voice loop's purposes.

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

// AssemblyAIURL is the Universal-Streaming v3 endpoint base. Var so
// tests can swap in a local WS fixture.
var AssemblyAIURL = "wss://streaming.assemblyai.com/v3/ws"

// AssemblyAISession holds one open WS to AssemblyAI for one voice
// client.
type AssemblyAISession struct {
	conn   *websocket.Conn
	cancel context.CancelFunc
}

// We emit DeepgramEvent directly (canonical STTEvent across providers
// — see voice_http.go's STT switch). The name predates the abstraction.

// aaiFrame is the subset of fields we read. Unknown fields are dropped
// silently (forward-compat with new AAI message types).
type aaiFrame struct {
	Type      string  `json:"type"`
	Text      string  `json:"text,omitempty"`
	Confidence float64 `json:"confidence,omitempty"`
	EndOfTurn bool    `json:"end_of_turn,omitempty"`
	Error     string  `json:"error,omitempty"`
}

// OpenAssemblyAISession dials AAI Universal-Streaming and returns a
// ready-to-write session. languageCode is optional; "" lets AAI
// auto-detect.
func OpenAssemblyAISession(parentCtx context.Context, apiKey, languageCode string) (*AssemblyAISession, <-chan DeepgramEvent, error) {
	if apiKey == "" {
		return nil, nil, fmt.Errorf("assemblyai api key not configured")
	}

	q := url.Values{}
	q.Set("sample_rate", "16000")
	q.Set("encoding", "pcm_s16le")
	q.Set("format_turns", "true")
	if lc := strings.TrimSpace(languageCode); lc != "" {
		q.Set("language_code", lc)
	}

	hdr := http.Header{}
	hdr.Set("Authorization", apiKey) // AAI uses bare key, not "Bearer"

	dialer := websocket.Dialer{HandshakeTimeout: 8 * time.Second}
	conn, _, err := dialer.DialContext(parentCtx, AssemblyAIURL+"?"+q.Encode(), hdr)
	if err != nil {
		return nil, nil, fmt.Errorf("assemblyai dial: %w", err)
	}

	ctx, cancel := context.WithCancel(parentCtx)
	out := make(chan DeepgramEvent, 16)
	sess := &AssemblyAISession{conn: conn, cancel: cancel}

	go sess.readLoop(ctx, out)
	return sess, out, nil
}

// SendAudio pushes a PCM chunk into AAI. Same 16-bit LE 16kHz mono
// contract as Deepgram so the voice_http frame plumbing is unchanged.
func (s *AssemblyAISession) SendAudio(pcm []byte) error {
	return s.conn.WriteMessage(websocket.BinaryMessage, pcm)
}

// Finalize tells AAI to flush + emit a final transcript. Map to the
// "Terminate" control frame.
func (s *AssemblyAISession) Finalize() error {
	return s.conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"Terminate"}`))
}

// Close terminates the session.
func (s *AssemblyAISession) Close() error {
	s.cancel()
	return s.conn.Close()
}

func (s *AssemblyAISession) readLoop(ctx context.Context, out chan<- DeepgramEvent) {
	defer close(out)
	defer s.conn.Close()
	for {
		if ctx.Err() != nil {
			return
		}
		_, payload, err := s.conn.ReadMessage()
		if err != nil {
			select {
			case out <- DeepgramEvent{Kind: "closed", Error: err.Error()}:
			case <-ctx.Done():
			}
			return
		}
		var f aaiFrame
		if err := json.Unmarshal(payload, &f); err != nil {
			continue // ignore garbage
		}
		switch f.Type {
		case "PartialTranscript", "Turn":
			text := strings.TrimSpace(f.Text)
			if text == "" {
				continue
			}
			kind := "partial"
			if f.EndOfTurn {
				kind = "final"
			}
			select {
			case out <- DeepgramEvent{Kind: kind, Text: text}:
			case <-ctx.Done():
				return
			}
			if f.EndOfTurn {
				select {
				case out <- DeepgramEvent{Kind: "eot"}:
				case <-ctx.Done():
					return
				}
			}
		case "FinalTranscript":
			text := strings.TrimSpace(f.Text)
			if text != "" {
				select {
				case out <- DeepgramEvent{Kind: "final", Text: text}:
				case <-ctx.Done():
					return
				}
			}
			select {
			case out <- DeepgramEvent{Kind: "eot"}:
			case <-ctx.Done():
				return
			}
		case "Termination":
			select {
			case out <- DeepgramEvent{Kind: "closed"}:
			case <-ctx.Done():
			}
			return
		case "Error":
			select {
			case out <- DeepgramEvent{Kind: "error", Error: f.Error}:
			case <-ctx.Done():
			}
			return
		}
	}
}
