package main

import (
	"context"
	"encoding/binary"
	"math"
	"testing"
	"time"
)

// pcm16 builds a signed-16-bit-LE mono buffer of n samples all at the given
// amplitude — a deterministic stand-in for "loud" (speech) or "silent" mic
// frames the VAD has to segment.
func pcm16(amplitude int16, n int) []byte {
	b := make([]byte, n*2)
	for i := 0; i < n; i++ {
		binary.LittleEndian.PutUint16(b[i*2:], uint16(amplitude))
	}
	return b
}

func TestPCMRMS(t *testing.T) {
	if got := pcmRMS(pcm16(0, 8000)); got != 0 {
		t.Fatalf("silence RMS = %v, want 0", got)
	}
	// Constant-amplitude signal: RMS equals the amplitude.
	if got := pcmRMS(pcm16(1000, 8000)); math.Abs(got-1000) > 1 {
		t.Fatalf("RMS of const 1000 = %v, want ~1000", got)
	}
	// A loud frame must read above the silence gate; a quiet one below.
	if pcmRMS(pcm16(5000, 1600)) < lwSilenceRMS {
		t.Fatal("5000-amplitude frame should be classified as speech")
	}
	if pcmRMS(pcm16(50, 1600)) >= lwSilenceRMS {
		t.Fatal("50-amplitude frame should be classified as silence")
	}
}

// TestStreamingLocalSegmentation drives the VAD/segmentation + worker end to
// end with a stubbed transcriber (no whisper.cpp needed): speech followed by
// trailing silence must emit one final + eot, and Finalize must close the
// event stream.
func TestStreamingLocalSegmentation(t *testing.T) {
	orig := localTranscribe
	localTranscribe = func(bin, model string, pcm []byte) (string, error) {
		return "test transcript", nil
	}
	t.Cleanup(func() { localTranscribe = orig })

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sess, ev := startStreamingLocal(ctx, "fake-bin", "fake-model", true)

	// 1s of speech (two 500ms loud frames), then >700ms of silence to
	// cross the end-of-turn hangover and trigger a final.
	half := lwBytesPerS / 2 / 2 // bytes for 500ms → sample count
	_ = sess.SendAudio(pcm16(6000, half))
	_ = sess.SendAudio(pcm16(6000, half))
	_ = sess.SendAudio(pcm16(0, half)) // 500ms silence
	_ = sess.SendAudio(pcm16(0, half)) // 1000ms total → end of turn

	final, eot := waitForFinal(t, ev)
	if final != "test transcript" {
		t.Fatalf("final text = %q, want %q", final, "test transcript")
	}
	if !eot {
		t.Fatal("expected an eot event after the final")
	}

	// Finalize with no pending audio must close the stream cleanly.
	_ = sess.Finalize()
	if !channelClosed(t, ev) {
		t.Fatal("event channel should close after Finalize")
	}
}

// TestStreamingLocalPushToTalk verifies autoEOT=false: trailing silence must
// NOT cut the turn — only an explicit Finalize produces the final.
func TestStreamingLocalPushToTalk(t *testing.T) {
	orig := localTranscribe
	calls := 0
	localTranscribe = func(bin, model string, pcm []byte) (string, error) {
		calls++
		return "ptt result", nil
	}
	t.Cleanup(func() { localTranscribe = orig })

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sess, ev := startStreamingLocal(ctx, "fake-bin", "fake-model", false)

	half := lwBytesPerS / 2 / 2
	_ = sess.SendAudio(pcm16(6000, half))
	_ = sess.SendAudio(pcm16(6000, half))
	// Long silence that WOULD trigger auto-EOT if it were enabled.
	_ = sess.SendAudio(pcm16(0, half))
	_ = sess.SendAudio(pcm16(0, half))
	_ = sess.SendAudio(pcm16(0, half))

	// No final should arrive from silence alone.
	select {
	case e, ok := <-ev:
		if ok && (e.Kind == "final" || e.Kind == "eot") {
			t.Fatalf("push-to-talk must not auto-finalize on silence, got %q", e.Kind)
		}
		// partials are fine
	case <-time.After(200 * time.Millisecond):
		// expected: no final yet
	}

	// Explicit Finalize flushes the buffered utterance as the one final.
	_ = sess.Finalize()
	final, eot := waitForFinal(t, ev)
	if final != "ptt result" || !eot {
		t.Fatalf("expected flushed final 'ptt result'+eot, got %q eot=%v", final, eot)
	}
	if !channelClosed(t, ev) {
		t.Fatal("event channel should close after Finalize")
	}
}

// waitForFinal reads events (ignoring partials) until it sees a final, then
// checks whether an eot immediately follows.
func waitForFinal(t *testing.T, ev <-chan DeepgramEvent) (string, bool) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	var finalText string
	for {
		select {
		case e, ok := <-ev:
			if !ok {
				t.Fatal("channel closed before a final arrived")
			}
			switch e.Kind {
			case "partial":
				continue
			case "final":
				finalText = e.Text
			case "eot":
				return finalText, true
			case "error":
				t.Fatalf("unexpected error event: %s", e.Error)
			}
		case <-deadline:
			t.Fatal("timed out waiting for final")
		}
	}
}

// channelClosed drains any trailing events and reports whether the channel
// closes within a short window.
func channelClosed(t *testing.T, ev <-chan DeepgramEvent) bool {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case _, ok := <-ev:
			if !ok {
				return true
			}
		case <-deadline:
			return false
		}
	}
}
