package main

// recap_narrate.go — give the recap a voice.
//
// The agent already has four cloud TTS providers that produce PCM in Go
// (SpeakOpenAI / SpeakCartesia / SpeakElevenLabs / SpeakDeepgram, all
// emitting CartesiaFrame{PCM} over a channel). streamTTS in voice_http.go
// base64s those frames onto a WebSocket for a client to play — and nothing
// in the tree has ever written them to disk. That one missing step is all
// that stands between the existing stack and a narrated video.
//
// WHY PER-CUE, NOT ONE UTTERANCE. Synthesising the whole script as a single
// blob would drift out of sync with the pictures immediately — TTS pacing has
// no relationship to how long a screen was up. Instead each cue is synthesised
// on its own and laid down at its own StartSec, with silence between. The
// subtitles and the voice then agree by construction, because both are
// rendered from the same cue list.
//
// The audio is a separate AAC track, never mixed into the pixels. That's what
// makes muting the narrator a volume control — the subtitles survive it.

import (
	"context"
	"encoding/binary"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// recapTTSTimeout bounds one cue's synthesis. A wedged provider must not hang
// a recap build forever.
const recapTTSTimeout = 60 * time.Second

// synthCue renders one cue to PCM using the configured provider, mirroring
// streamTTS's provider dispatch (voice_http.go) — including its sample rates,
// which differ per provider and are not negotiable.
//
// Returns the PCM bytes and the sample rate they're at.
func synthCue(ctx context.Context, v *VoiceConfig, provider, text string) ([]byte, int, error) {
	if v == nil {
		return nil, 0, fmt.Errorf("voice not configured")
	}
	if provider == "" {
		provider = v.EffectiveTTSProvider()
	}
	ctx, cancel := context.WithTimeout(ctx, recapTTSTimeout)
	defer cancel()

	ch := make(chan CartesiaFrame, 8)
	sampleRate := 22050
	switch provider {
	case "openai":
		key := LookupVoiceCredential("openai", "api-key", v.OpenAIAPIKey)
		if key == "" {
			return nil, 0, fmt.Errorf("no OpenAI key configured")
		}
		sampleRate = 24000
		go SpeakOpenAI(ctx, key, v.OpenAITTSModel, v.OpenAITTSVoice, text, ch)
	case "cartesia":
		key := LookupVoiceCredential("cartesia", "api-key", v.CartesiaAPIKey)
		if key == "" {
			return nil, 0, fmt.Errorf("no Cartesia key configured")
		}
		go SpeakCartesia(ctx, key, v.CartesiaVoiceID, text, ch)
	case "elevenlabs":
		key := LookupVoiceCredential("elevenlabs", "api-key", v.ElevenLabsAPIKey)
		if key == "" {
			return nil, 0, fmt.Errorf("no ElevenLabs key configured")
		}
		sampleRate = 16000
		go SpeakElevenLabs(ctx, key, v.ElevenLabsTTSVoiceID, v.ElevenLabsTTSModel, text, ch)
	case "deepgram":
		key := LookupVoiceCredential("deepgram", "api-key", v.DeepgramAPIKey)
		if key == "" {
			return nil, 0, fmt.Errorf("no Deepgram key configured")
		}
		sampleRate = DeepgramTTSSampleRate
		go SpeakDeepgram(ctx, key, v.DeepgramTTSModel, text, ch)
	case "device", "local", "":
		// "device" means the CLIENT synthesises at playback time. That's a
		// legitimate choice for a live voice loop, but a recap is a file: there
		// is no client at build time. The subtitles carry the script instead,
		// and a surface with its own TTS can still read them aloud.
		return nil, 0, fmt.Errorf("provider %q synthesises on the client; a recap needs a server-side voice (openai/cartesia/elevenlabs/deepgram)", provider)
	default:
		return nil, 0, fmt.Errorf("unknown TTS provider %q", provider)
	}

	var pcm []byte
	for fr := range ch {
		if fr.Error != "" {
			return nil, 0, fmt.Errorf("tts (%s): %s", provider, fr.Error)
		}
		pcm = append(pcm, fr.PCM...)
		if fr.Done {
			break
		}
	}
	if len(pcm) == 0 {
		return nil, 0, fmt.Errorf("tts (%s) returned no audio", provider)
	}
	return pcm, sampleRate, nil
}

// pcmDurationSec computes how long a 16-bit mono PCM buffer plays for.
func pcmDurationSec(pcm []byte, sampleRate int) float64 {
	if sampleRate <= 0 {
		return 0
	}
	return float64(len(pcm)) / 2.0 / float64(sampleRate) // 2 bytes/sample, mono
}

// writeWAV wraps 16-bit little-endian mono PCM in a RIFF/WAVE header.
//
// Hand-rolled because the alternative is piping raw PCM into ffmpeg with
// -f s16le and hoping the rate flags line up; a real header means ffmpeg
// reads the rate from the file and a human can play the intermediate to
// debug it.
func writeWAV(path string, pcm []byte, sampleRate int) error {
	const (
		numChannels   = 1
		bitsPerSample = 16
	)
	byteRate := sampleRate * numChannels * bitsPerSample / 8
	blockAlign := numChannels * bitsPerSample / 8

	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	w := func(v interface{}) error { return binary.Write(f, binary.LittleEndian, v) }
	if _, err := f.WriteString("RIFF"); err != nil {
		return err
	}
	if err := w(uint32(36 + len(pcm))); err != nil { // ChunkSize
		return err
	}
	if _, err := f.WriteString("WAVEfmt "); err != nil {
		return err
	}
	for _, v := range []interface{}{
		uint32(16),            // Subchunk1Size (PCM)
		uint16(1),             // AudioFormat = PCM
		uint16(numChannels),   //
		uint32(sampleRate),    //
		uint32(byteRate),      //
		uint16(blockAlign),    //
		uint16(bitsPerSample), //
	} {
		if err := w(v); err != nil {
			return err
		}
	}
	if _, err := f.WriteString("data"); err != nil {
		return err
	}
	if err := w(uint32(len(pcm))); err != nil {
		return err
	}
	_, err = f.Write(pcm)
	return err
}

// buildNarrationTrack synthesises every cue and lays each one down at its cue
// start, padding the gaps with silence so the voice tracks the pictures.
//
// Overrun policy: if a cue's speech is longer than its slot, it is allowed to
// run over into the following silence rather than being cut off mid-word —
// truncated narration sounds broken, whereas slight drift does not. The next
// cue still starts at its own mark, so drift never accumulates.
func buildNarrationTrack(ctx context.Context, cues []RecapCue, provider string, totalSec float64) ([]byte, int, string, error) {
	cfg, err := LoadConfig()
	if err != nil {
		return nil, 0, "", fmt.Errorf("load config: %w", err)
	}
	v := voiceCfgOrNil(cfg)
	if v == nil {
		return nil, 0, "", fmt.Errorf("no voice config — set a TTS provider to narrate recaps")
	}
	if provider == "" {
		provider = v.EffectiveTTSProvider()
	}

	type rendered struct {
		pcm   []byte
		start float64
	}
	var parts []rendered
	sampleRate := 0
	for _, c := range cues {
		pcm, sr, err := synthCue(ctx, v, provider, c.Text)
		if err != nil {
			// One failed cue shouldn't lose the whole narration, but a
			// missing/unconfigured provider fails every cue — surface that
			// immediately rather than after N timeouts.
			if len(parts) == 0 {
				return nil, 0, "", err
			}
			log.Printf("[recap] narration cue skipped: %v", err)
			continue
		}
		if sampleRate == 0 {
			sampleRate = sr
		} else if sr != sampleRate {
			// Providers don't change mid-run in practice; if one ever did,
			// concatenating rates would pitch-shift the result.
			return nil, 0, "", fmt.Errorf("tts sample rate changed mid-script (%d → %d)", sampleRate, sr)
		}
		parts = append(parts, rendered{pcm: pcm, start: c.StartSec})
	}
	if len(parts) == 0 || sampleRate == 0 {
		return nil, 0, "", fmt.Errorf("no narration synthesised")
	}

	// Lay the parts onto a silent bed the length of the video.
	bedSamples := int(totalSec * float64(sampleRate))
	if bedSamples < 0 {
		bedSamples = 0
	}
	track := make([]byte, bedSamples*2) // 16-bit mono silence == zero bytes
	for _, p := range parts {
		off := int(p.start*float64(sampleRate)) * 2
		if off < 0 {
			off = 0
		}
		if off >= len(track) {
			// Cue starts past the end of the video — nothing to hear.
			continue
		}
		n := copy(track[off:], p.pcm)
		if n < len(p.pcm) {
			// Speech ran past the end of the video: extend rather than clip,
			// then let ffmpeg's -shortest decide. Losing the last few words of
			// the closer is exactly the sentence that says what happened.
			track = append(track, p.pcm[n:]...)
		}
	}
	return track, sampleRate, provider, nil
}

// muxNarration marries the silent video and the narration WAV into a single
// MP4 with an AAC audio track, replacing the video in place.
//
// -c:v copy: the pictures are already encoded; re-encoding would cost minutes
// and quality for nothing.
func muxNarration(ctx context.Context, video, wav string) error {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		return fmt.Errorf("ffmpeg not found")
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	out := video + ".muxed.mp4"
	cmd := exec.CommandContext(ctx, "ffmpeg", "-y",
		"-i", video, "-i", wav,
		"-c:v", "copy",
		"-c:a", "aac", "-b:a", "96k",
		// The narration bed is built to the video's length, but a closer that
		// overran will have made it slightly longer; -shortest keeps the file
		// honest to the pictures.
		"-shortest",
		"-movflags", "+faststart",
		out,
	)
	if b, err := cmd.CombinedOutput(); err != nil {
		os.Remove(out)
		return fmt.Errorf("ffmpeg mux: %v: %s", err, tailStr(string(b), 400))
	}
	// Replace atomically-ish: the caller has already told listeners the recap
	// is building, and a torn file here would be served as ready.
	return os.Rename(out, video)
}

// NarrateRecap synthesises the script and muxes it onto the video. Returns the
// provider that spoke, for the record.
func NarrateRecap(ctx context.Context, dir, video string, cues []RecapCue, provider string) (string, error) {
	if len(cues) == 0 {
		return "", fmt.Errorf("no cues to narrate")
	}
	var totalSec float64
	for _, c := range cues {
		if c.EndSec > totalSec {
			totalSec = c.EndSec
		}
	}
	pcm, sampleRate, used, err := buildNarrationTrack(ctx, cues, provider, totalSec)
	if err != nil {
		return "", err
	}
	wav := filepath.Join(dir, "narration.wav")
	if err := writeWAV(wav, pcm, sampleRate); err != nil {
		return "", fmt.Errorf("write wav: %w", err)
	}
	// The WAV is an intermediate; the MP4 carries the audio from here.
	defer os.Remove(wav)
	if err := muxNarration(ctx, video, wav); err != nil {
		return "", err
	}
	return used, nil
}
