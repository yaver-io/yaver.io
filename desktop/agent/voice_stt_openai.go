package main

// OpenAI Whisper batch STT — the default Yaver STT provider.
//
// Why batch (vs. Deepgram's WS streaming): OpenAI's /v1/audio/transcriptions
// endpoint takes a whole audio file and returns the transcript in one
// HTTP response. There IS a `stream:true` flag for partial-token output
// but it doesn't accept streaming AUDIO input — only streaming the
// response side. So for Yaver's mic-→-Claude loop the simpler design
// is: buffer the audio chunks on our side, send one POST when the
// client signals end-of-utterance, return the transcript once.
//
// Drop-in interface match with DeepgramSession so voice_http.go can
// dispatch by provider with no extra plumbing.
//
// User of Yaver provides their own OpenAI API key in
// ~/.yaver/config.json's voice.openai_api_key. Yaver itself does NOT
// ship a default key; nothing OpenAI-billed goes to Convex.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"sync"
	"time"
)

// OpenAITranscriptionURL is the audio/transcriptions REST endpoint.
// Var, not const, so tests can swap in a local HTTP fixture.
var OpenAITranscriptionURL = "https://api.openai.com/v1/audio/transcriptions"

// OpenAIWhisperSession buffers incoming PCM frames and posts them to
// OpenAI as a single WAV file at Finalize. Mirrors DeepgramSession's
// surface so voice_http.go's switch statement stays clean.
type OpenAIWhisperSession struct {
	apiKey string
	model  string
	// audio buffer — appends are cheap, single owner per session
	mu    sync.Mutex
	buf   bytes.Buffer
	events chan DeepgramEvent // reused type from voice_stt_deepgram.go
	done  bool
}

// OpenOpenAIWhisperSession returns a freshly-buffered session and the
// event channel the client should range over. The channel emits ONE
// "final" event then "eot" then closes — no partials, by design.
func OpenOpenAIWhisperSession(_ context.Context, apiKey, model string) (*OpenAIWhisperSession, <-chan DeepgramEvent, error) {
	if apiKey == "" {
		return nil, nil, fmt.Errorf("openai api key not configured")
	}
	if model == "" {
		model = "whisper-1"
	}
	sess := &OpenAIWhisperSession{
		apiKey: apiKey,
		model:  model,
		events: make(chan DeepgramEvent, 4),
	}
	return sess, sess.events, nil
}

// SendAudio buffers a raw PCM chunk for batch upload.
func (s *OpenAIWhisperSession) SendAudio(pcm []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.done {
		return fmt.Errorf("session closed")
	}
	_, err := s.buf.Write(pcm)
	return err
}

// Finalize triggers the upload + transcription, emits "final" + "eot"
// events on the channel, and closes the session.
func (s *OpenAIWhisperSession) Finalize() error {
	s.mu.Lock()
	if s.done {
		s.mu.Unlock()
		return nil
	}
	s.done = true
	pcm := make([]byte, s.buf.Len())
	copy(pcm, s.buf.Bytes())
	s.buf.Reset()
	s.mu.Unlock()

	go func() {
		defer close(s.events)
		if len(pcm) == 0 {
			s.events <- DeepgramEvent{Kind: "closed", Error: "no audio captured"}
			return
		}
		text, err := transcribeWhisper(s.apiKey, s.model, pcm)
		if err != nil {
			s.events <- DeepgramEvent{Kind: "error", Error: err.Error()}
			return
		}
		if strings.TrimSpace(text) == "" {
			s.events <- DeepgramEvent{Kind: "closed", Error: "empty transcript"}
			return
		}
		s.events <- DeepgramEvent{Kind: "final", Text: text}
		s.events <- DeepgramEvent{Kind: "eot"}
	}()
	return nil
}

// Close aborts the session without uploading. If the upload goroutine
// is already running, the channel still closes once it terminates.
func (s *OpenAIWhisperSession) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.done {
		return nil
	}
	s.done = true
	s.buf.Reset()
	close(s.events)
	return nil
}

// transcribeWhisper wraps the PCM buffer in a WAV container and POSTs
// to /v1/audio/transcriptions. Returns the final text or an error.
//
// The PCM input is assumed to be 16-bit LE, 16kHz mono — matching the
// agent's incoming /voice/stream WS frames. WAV header is constructed
// inline to avoid pulling in github.com/youpy/go-wav.
func transcribeWhisper(apiKey, model string, pcm []byte) (string, error) {
	wav := wrapPCMAsWAV(pcm, 16000, 1, 16)

	// Build multipart form: file=<wav>, model=<model>, response_format=text
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	part, err := mw.CreateFormFile("file", "audio.wav")
	if err != nil {
		return "", err
	}
	if _, err := part.Write(wav); err != nil {
		return "", err
	}
	if err := mw.WriteField("model", model); err != nil {
		return "", err
	}
	if err := mw.WriteField("response_format", "json"); err != nil {
		return "", err
	}
	if err := mw.Close(); err != nil {
		return "", err
	}

	req, err := http.NewRequest(http.MethodPost, OpenAITranscriptionURL, &body)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", mw.FormDataContentType())

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("openai whisper: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode/100 != 2 {
		return "", fmt.Errorf("openai whisper http %d: %s", resp.StatusCode, snippet(respBody))
	}
	var parsed struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return "", fmt.Errorf("parse whisper response: %w (body: %s)", err, snippet(respBody))
	}
	return strings.TrimSpace(parsed.Text), nil
}

// wrapPCMAsWAV builds a minimal 44-byte WAV header in front of the
// PCM bytes. Same shape as the helper in mobile/src/lib/agentVoice.ts.
func wrapPCMAsWAV(pcm []byte, sampleRate, channels, bitsPerSample int) []byte {
	byteRate := sampleRate * channels * bitsPerSample / 8
	blockAlign := channels * bitsPerSample / 8
	dataSize := len(pcm)
	header := make([]byte, 44)

	copy(header[0:4], "RIFF")
	putLE32(header[4:8], uint32(36+dataSize))
	copy(header[8:12], "WAVE")
	copy(header[12:16], "fmt ")
	putLE32(header[16:20], 16) // PCM fmt chunk size
	putLE16(header[20:22], 1)  // PCM format
	putLE16(header[22:24], uint16(channels))
	putLE32(header[24:28], uint32(sampleRate))
	putLE32(header[28:32], uint32(byteRate))
	putLE16(header[32:34], uint16(blockAlign))
	putLE16(header[34:36], uint16(bitsPerSample))
	copy(header[36:40], "data")
	putLE32(header[40:44], uint32(dataSize))

	out := make([]byte, 0, 44+dataSize)
	out = append(out, header...)
	out = append(out, pcm...)
	return out
}

func putLE32(b []byte, v uint32) {
	b[0] = byte(v)
	b[1] = byte(v >> 8)
	b[2] = byte(v >> 16)
	b[3] = byte(v >> 24)
}

func putLE16(b []byte, v uint16) {
	b[0] = byte(v)
	b[1] = byte(v >> 8)
}

// snippet helper lives in smoke_relay_password.go — reused here to
// avoid a redeclaration. If that ever moves, this file picks up the
// new location automatically because they share package main.
