package main

// voice_stt_local.go — free, offline STT for the Go agent using a local
// whisper.cpp binary. This is the desktop/agent twin of mobile's
// whisper.rn on-device path: $0, no network, no API key.
//
// Design mirrors voice_stt_openai.go (batch, not streaming): buffer PCM,
// then on Finalize() write a WAV to a temp file, run the whisper.cpp CLI
// against a ggml model, and emit one "final" + "eot". Implementing it
// behind the same (SendAudio/Finalize/Close + DeepgramEvent channel)
// shape as the other providers means `yaver voice listen` and the voice
// HTTP loop drive it with zero special-casing.
//
// We shell out to the whisper.cpp binary (whisper-cli / whisper-cpp /
// main, brew: `brew install whisper-cpp`) rather than cgo-linking
// libwhisper: no new build deps, no model compiled into the binary, and
// the heavy native code stays out of the Go build. The model file is
// resolved from (in order) YAVER_WHISPER_MODEL, the repo's bundled
// ggml-whisper-tiny.bin, or ~/.yaver/models/.

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

// localWhisperBins are the known whisper.cpp CLI names, newest first.
// whisper.cpp renamed `main` → `whisper-cli` in 2024; brew ships
// `whisper-cpp`. We probe all three.
var localWhisperBins = []string{"whisper-cli", "whisper-cpp", "whisper", "main"}

// LocalWhisperSession buffers PCM and transcribes it on Finalize via the
// whisper.cpp CLI. Same contract as OpenAIWhisperSession.
type LocalWhisperSession struct {
	bin       string
	modelPath string
	mu        sync.Mutex
	buf       bytes.Buffer
	events    chan DeepgramEvent
	done      bool
}

// localWhisperBin returns the first whisper.cpp CLI found on PATH, or ""
// if none is installed.
func localWhisperBin() string {
	for _, b := range localWhisperBins {
		if p, err := exec.LookPath(b); err == nil {
			return p
		}
	}
	return ""
}

// localWhisperModel resolves a ggml model path: explicit env override,
// then ~/.yaver/models/, then the repo-bundled mobile model (dev), else
// "". Caller treats "" as "not configured".
func localWhisperModel() string {
	if m := strings.TrimSpace(os.Getenv("YAVER_WHISPER_MODEL")); m != "" {
		if fileExists(m) {
			return m
		}
	}
	home, _ := os.UserHomeDir()
	candidates := []string{}
	if home != "" {
		candidates = append(candidates,
			filepath.Join(home, ".yaver", "models", "ggml-whisper-tiny.bin"),
			filepath.Join(home, ".yaver", "models", "ggml-base.en.bin"),
			filepath.Join(home, ".yaver", "models", "ggml-tiny.en.bin"),
			// Dev fallback: the model the mobile app already vendors.
			filepath.Join(home, "Workspace", "yaver.io", "mobile", "assets", "models", "ggml-whisper-tiny.bin"),
		)
	}
	for _, c := range candidates {
		if fileExists(c) {
			return c
		}
	}
	return ""
}

// LocalWhisperAvailable reports whether both a CLI and a model are
// present, so the UI / status can show "local STT ready".
func LocalWhisperAvailable() bool {
	return localWhisperBin() != "" && localWhisperModel() != ""
}

// OpenLocalWhisperSession returns a buffered local-STT session. Returns
// an error (with an install hint) if the binary or model is missing, so
// the caller can fall back or message the user.
func OpenLocalWhisperSession(_ context.Context) (*LocalWhisperSession, <-chan DeepgramEvent, error) {
	bin := localWhisperBin()
	if bin == "" {
		return nil, nil, fmt.Errorf("no whisper.cpp CLI found (install: brew install whisper-cpp)")
	}
	model := localWhisperModel()
	if model == "" {
		return nil, nil, fmt.Errorf("no whisper ggml model found (set YAVER_WHISPER_MODEL or place one in ~/.yaver/models/)")
	}
	sess := &LocalWhisperSession{
		bin:       bin,
		modelPath: model,
		events:    make(chan DeepgramEvent, 4),
	}
	return sess, sess.events, nil
}

// SendAudio buffers raw 16kHz mono s16le PCM.
func (s *LocalWhisperSession) SendAudio(pcm []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.done {
		return fmt.Errorf("session closed")
	}
	_, err := s.buf.Write(pcm)
	return err
}

// Finalize wraps the PCM as a WAV, runs whisper.cpp, and emits the
// transcript. One-shot: emits "final" + "eot", then closes the channel.
func (s *LocalWhisperSession) Finalize() error {
	s.mu.Lock()
	if s.done {
		s.mu.Unlock()
		return nil
	}
	s.done = true
	pcm := make([]byte, s.buf.Len())
	copy(pcm, s.buf.Bytes())
	s.buf.Reset()
	bin, model := s.bin, s.modelPath
	s.mu.Unlock()

	go func() {
		defer close(s.events)
		if len(pcm) == 0 {
			s.events <- DeepgramEvent{Kind: "closed", Error: "no audio captured"}
			return
		}
		text, err := transcribeLocalWhisper(bin, model, pcm)
		if err != nil {
			s.events <- DeepgramEvent{Kind: "error", Error: err.Error()}
			return
		}
		if strings.TrimSpace(text) == "" {
			s.events <- DeepgramEvent{Kind: "closed", Error: "empty transcript"}
			return
		}
		s.events <- DeepgramEvent{Kind: "final", Text: strings.TrimSpace(text)}
		s.events <- DeepgramEvent{Kind: "eot"}
	}()
	return nil
}

// Close aborts without transcribing if Finalize hasn't run.
func (s *LocalWhisperSession) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.done {
		s.done = true
		close(s.events)
	}
	return nil
}

// transcribeLocalWhisper writes the PCM to a temp WAV and runs the
// whisper.cpp CLI, returning the plain-text transcript. Uses -nt
// (no timestamps) and -otxt to a temp output, reading the .txt back.
func transcribeLocalWhisper(bin, model string, pcm []byte) (string, error) {
	tmpDir, err := os.MkdirTemp("", "yaver-whisper-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmpDir)

	wavPath := filepath.Join(tmpDir, "audio.wav")
	if err := writeWAV16kMono(wavPath, pcm); err != nil {
		return "", fmt.Errorf("write wav: %w", err)
	}

	// whisper.cpp: -m model -f input.wav -nt (no timestamps) -otxt
	// (write <input>.txt) -l auto. Output text file sits next to the wav.
	outBase := filepath.Join(tmpDir, "audio") // whisper appends .txt
	cmd := exec.Command(bin,
		"-m", model,
		"-f", wavPath,
		"-nt",
		"-otxt",
		"-of", outBase,
		"-l", "auto",
	)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		// Fall back to stdout-only invocation for CLI variants that
		// don't support -of/-otxt the same way.
		out, e2 := exec.Command(bin, "-m", model, "-f", wavPath, "-nt").Output()
		if e2 != nil {
			return "", fmt.Errorf("whisper run: %v (%s)", err, strings.TrimSpace(stderr.String()))
		}
		return cleanWhisperText(string(out)), nil
	}

	txt, err := os.ReadFile(outBase + ".txt")
	if err != nil {
		return "", fmt.Errorf("read transcript: %w", err)
	}
	return cleanWhisperText(string(txt)), nil
}

// cleanWhisperText strips whisper.cpp's bracketed annotations
// ([BLANK_AUDIO], [_BEG_], timestamps) and collapses whitespace.
func cleanWhisperText(s string) string {
	lines := strings.Split(s, "\n")
	var keep []string
	for _, ln := range lines {
		ln = strings.TrimSpace(ln)
		if ln == "" {
			continue
		}
		if strings.HasPrefix(ln, "[") && strings.HasSuffix(ln, "]") {
			continue // [BLANK_AUDIO], [Music], etc.
		}
		keep = append(keep, ln)
	}
	return strings.TrimSpace(strings.Join(keep, " "))
}

// writeWAV16kMono writes a minimal 44-byte WAV header + PCM for
// 16kHz mono signed-16-bit-LE audio (whisper.cpp's required input).
func writeWAV16kMono(path string, pcm []byte) error {
	const (
		sampleRate    = 16000
		numChannels   = 1
		bitsPerSample = 16
	)
	byteRate := sampleRate * numChannels * bitsPerSample / 8
	blockAlign := numChannels * bitsPerSample / 8
	dataLen := len(pcm)
	riffLen := 36 + dataLen

	var h bytes.Buffer
	h.WriteString("RIFF")
	binary.Write(&h, binary.LittleEndian, uint32(riffLen))
	h.WriteString("WAVE")
	h.WriteString("fmt ")
	binary.Write(&h, binary.LittleEndian, uint32(16)) // PCM fmt chunk size
	binary.Write(&h, binary.LittleEndian, uint16(1))  // audio format = PCM
	binary.Write(&h, binary.LittleEndian, uint16(numChannels))
	binary.Write(&h, binary.LittleEndian, uint32(sampleRate))
	binary.Write(&h, binary.LittleEndian, uint32(byteRate))
	binary.Write(&h, binary.LittleEndian, uint16(blockAlign))
	binary.Write(&h, binary.LittleEndian, uint16(bitsPerSample))
	h.WriteString("data")
	binary.Write(&h, binary.LittleEndian, uint32(dataLen))

	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.Write(h.Bytes()); err != nil {
		return err
	}
	_, err = f.Write(pcm)
	return err
}
