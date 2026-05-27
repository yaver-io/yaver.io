package main

// Deepgram Flux (Nova-3) streaming STT client.
//
// Why Flux: model-integrated end-of-turn detection. When the user finishes
// speaking, Deepgram emits an explicit turn-final event — we don't have to
// guess from silence. That's the linchpin of a snappy agent loop: fire the
// Claude Code roundtrip the instant the user actually stops, not 500ms
// after a silence timer.
//
// Audio in: 16kHz mono PCM (signed 16-bit little-endian), ~20-40ms chunks.
// Transcript out: streaming partials + finals, plus a turn-final flag.
//
// One DeepgramSession per WS client connection. Bring-your-own-context
// for cancellation when the upstream WS closes.

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

// DeepgramURL is the Flux Nova-3 streaming endpoint base. Audio params
// (sample rate, encoding, keyterms) are appended per-session.
// Var, not const, so tests can swap in a local WS fixture.
var DeepgramURL = "wss://api.deepgram.com/v1/listen"

// DeepgramSession holds one open WS to Deepgram for one voice client.
type DeepgramSession struct {
	conn   *websocket.Conn
	cancel context.CancelFunc
}

// DeepgramEvent is emitted on the channel from Run. The caller bridges
// these into the client-facing /voice/stream WS.
type DeepgramEvent struct {
	Kind  string // "partial" | "final" | "eot" | "error" | "closed"
	Text  string
	Error string
}

// dgFrame mirrors the subset of Deepgram's JSON payload we read. We
// deliberately ignore fields we don't need; new fields appearing in
// the wire format are silently dropped (forward-compat).
type dgFrame struct {
	Type    string `json:"type"`
	Channel *struct {
		Alternatives []struct {
			Transcript string  `json:"transcript"`
			Confidence float64 `json:"confidence"`
		} `json:"alternatives"`
	} `json:"channel,omitempty"`
	IsFinal     bool   `json:"is_final,omitempty"`
	SpeechFinal bool   `json:"speech_final,omitempty"` // Flux: emitted on end-of-turn
	Event       string `json:"event,omitempty"`        // Flux variant: "eot"
}

// OpenDeepgramSession dials Deepgram and returns a ready-to-write session.
// keyterms are passed as Deepgram's `keyterm` query param (Flux feature) so
// project-specific names like "Convex", "Hermes", "useState" don't get
// mangled.
func OpenDeepgramSession(parentCtx context.Context, apiKey, model string, keyterms []string) (*DeepgramSession, <-chan DeepgramEvent, error) {
	if apiKey == "" {
		return nil, nil, fmt.Errorf("deepgram api key not configured")
	}
	if model == "" {
		model = "nova-3"
	}

	q := url.Values{}
	q.Set("model", model)
	q.Set("encoding", "linear16")
	q.Set("sample_rate", "16000")
	q.Set("channels", "1")
	q.Set("interim_results", "true")
	q.Set("punctuate", "true")
	q.Set("smart_format", "true")
	q.Set("endpointing", "300") // 300ms silence → speech_final on non-Flux models
	for _, kt := range keyterms {
		kt = strings.TrimSpace(kt)
		if kt != "" {
			q.Add("keyterm", kt)
		}
	}

	hdr := http.Header{}
	hdr.Set("Authorization", "Token "+apiKey)

	dialer := websocket.Dialer{HandshakeTimeout: 8 * time.Second}
	conn, _, err := dialer.DialContext(parentCtx, DeepgramURL+"?"+q.Encode(), hdr)
	if err != nil {
		return nil, nil, fmt.Errorf("deepgram dial: %w", err)
	}

	ctx, cancel := context.WithCancel(parentCtx)
	out := make(chan DeepgramEvent, 16)
	sess := &DeepgramSession{conn: conn, cancel: cancel}

	go sess.readLoop(ctx, out)
	return sess, out, nil
}

// SendAudio pushes a PCM chunk into Deepgram. Caller owns frame
// timing — Deepgram tolerates anything in 20-100ms range.
func (s *DeepgramSession) SendAudio(pcm []byte) error {
	return s.conn.WriteMessage(websocket.BinaryMessage, pcm)
}

// Finalize tells Deepgram to flush any pending audio and emit a final
// transcript. Use this when the client signals end-of-utterance
// (button release / PTT off).
func (s *DeepgramSession) Finalize() error {
	return s.conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"CloseStream"}`))
}

// Close terminates the session.
func (s *DeepgramSession) Close() error {
	s.cancel()
	return s.conn.Close()
}

func (s *DeepgramSession) readLoop(ctx context.Context, out chan<- DeepgramEvent) {
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
		var f dgFrame
		if err := json.Unmarshal(payload, &f); err != nil {
			continue // ignore garbage; Deepgram sometimes emits non-Results frames
		}
		if f.Channel == nil || len(f.Channel.Alternatives) == 0 {
			// Flux EOT marker can arrive with no alternatives
			if f.Event == "eot" || f.SpeechFinal {
				select {
				case out <- DeepgramEvent{Kind: "eot"}:
				case <-ctx.Done():
					return
				}
			}
			continue
		}
		text := strings.TrimSpace(f.Channel.Alternatives[0].Transcript)
		if text == "" {
			continue
		}
		kind := "partial"
		if f.IsFinal {
			kind = "final"
		}
		select {
		case out <- DeepgramEvent{Kind: kind, Text: text}:
		case <-ctx.Done():
			return
		}
		if f.SpeechFinal || f.Event == "eot" {
			select {
			case out <- DeepgramEvent{Kind: "eot"}:
			case <-ctx.Done():
				return
			}
		}
	}
}
