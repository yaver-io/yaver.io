package main

// OpenAI TTS streaming — the default Yaver TTS provider.
//
// /v1/audio/speech accepts {model, input, voice, response_format} and
// returns the audio bytes. With response_format=pcm we get raw 16-bit
// LE at 24kHz mono — playable as-is via the same client-side path
// that handles Cartesia's PCM.
//
// We use response_format=pcm + chunked transfer so the client can
// start playing within the first ~300-600ms instead of waiting for
// the whole audio file. Matches the CartesiaFrame contract emitted
// by SpeakCartesia.
//
// Same single user-owned API key as the Whisper STT path — Yaver
// itself does NOT ship a default key.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// OpenAITTSURL is the audio/speech REST endpoint. Var, not const,
// so tests can swap in a local HTTP fixture.
var OpenAITTSURL = "https://api.openai.com/v1/audio/speech"

// SpeakOpenAI synthesizes `text` using the user-configured OpenAI key
// and streams PCM frames into `out`. Closes `out` when done.
//
// Blocking — call in a goroutine. Honors ctx for cancellation.
//
// Output format: PCM 16-bit LE, 24kHz mono. We tag each CartesiaFrame
// with implicit sampleRate=24000 via the broader VoiceConfig — the
// /voice/stream WS handler uses the configured rate when emitting
// tts-frame messages so the client plays at the correct pitch.
func SpeakOpenAI(ctx context.Context, apiKey, model, voice, text string, out chan<- CartesiaFrame) {
	defer close(out)

	if apiKey == "" {
		out <- CartesiaFrame{Error: "openai api key not configured"}
		return
	}
	if model == "" {
		model = "gpt-4o-mini-tts"
	}
	if voice == "" {
		voice = "alloy"
	}
	if text == "" {
		out <- CartesiaFrame{Done: true}
		return
	}

	body, err := json.Marshal(map[string]interface{}{
		"model":           model,
		"input":           text,
		"voice":           voice,
		"response_format": "pcm",
	})
	if err != nil {
		out <- CartesiaFrame{Error: fmt.Sprintf("openai tts marshal: %v", err)}
		return
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, OpenAITTSURL, bytes.NewReader(body))
	if err != nil {
		out <- CartesiaFrame{Error: fmt.Sprintf("openai tts request: %v", err)}
		return
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")

	// No timeout on the client — we honor ctx cancellation instead so
	// a long generation isn't artificially killed mid-stream.
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		out <- CartesiaFrame{Error: fmt.Sprintf("openai tts: %v", err)}
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode/100 != 2 {
		errBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		out <- CartesiaFrame{Error: fmt.Sprintf("openai tts http %d: %s", resp.StatusCode, string(errBody))}
		return
	}

	// Stream the body in 4KB chunks. PCM samples can be played as
	// they arrive — no need to wait for the full file.
	buf := make([]byte, 4096)
	for {
		if ctx.Err() != nil {
			return
		}
		n, err := resp.Body.Read(buf)
		if n > 0 {
			pcm := make([]byte, n)
			copy(pcm, buf[:n])
			select {
			case out <- CartesiaFrame{PCM: pcm}:
			case <-ctx.Done():
				return
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			// Read failed mid-stream; emit error then stop.
			out <- CartesiaFrame{Error: fmt.Sprintf("openai tts stream: %v", err)}
			return
		}
	}

	// Brief settle delay so any tail frames flush before Done.
	time.Sleep(10 * time.Millisecond)
	out <- CartesiaFrame{Done: true}
}
