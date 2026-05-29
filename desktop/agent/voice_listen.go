package main

// voice_listen.go — `yaver voice listen`: capture the host microphone in
// the terminal, stream it to the configured STT provider, and print the
// live transcript (partial + final) right in the console. Optional --tts
// speaks finals back via the free local engine (`say` on macOS, `espeak`
// on Linux).
//
// Pipeline:
//   ffmpeg (avfoundation/pulse/alsa mic → 16kHz mono s16le PCM on stdout)
//     → DeepgramSession / OpenAIWhisperSession.SendAudio
//     → DeepgramEvent channel → terminal render (\r in-place for partials)
//
// This is the terminal twin of the mobile /voice/stream loop: same STT
// clients, same credential resolver (vault → env → legacy), so a key set
// from mobile Settings or `yaver voice setup` works here with no extra
// wiring. No agent restart — keys are read at session open.
//
// Audio capture is intentionally shelled out to ffmpeg (already a Yaver
// dependency for media) rather than cgo CoreAudio: zero new native deps,
// works the same on macOS/Linux, and the mic permission prompt is owned
// by ffmpeg/the OS.

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strings"
	"syscall"
	"time"
)

// sttSession is the shared shape of DeepgramSession and
// OpenAIWhisperSession so `listen` can drive either behind one var.
type sttSession interface {
	SendAudio(pcm []byte) error
	Finalize() error
	Close() error
}

func runVoiceListen(args []string) {
	// Lightweight flag parse (avoid pulling another FlagSet's exit paths).
	var (
		ttsEcho     bool
		device      string
		once        bool
		showHelp    bool
	)
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--tts", "-t":
			ttsEcho = true
		case "--once":
			once = true
		case "-h", "--help", "help":
			showHelp = true
		case "--device", "-d":
			if i+1 < len(args) {
				device = args[i+1]
				i++
			}
		default:
			if strings.HasPrefix(args[i], "--device=") {
				device = strings.TrimPrefix(args[i], "--device=")
			}
		}
	}
	if showHelp {
		fmt.Println("yaver voice listen — live mic transcription in the terminal")
		fmt.Println()
		fmt.Println("  yaver voice listen            stream mic → STT, print transcript live")
		fmt.Println("  yaver voice listen --tts      also speak each final back (free local TTS)")
		fmt.Println("  yaver voice listen --once     stop after the first end-of-turn final")
		fmt.Println("  yaver voice listen --device <name|index>   pick a mic (default: system default)")
		fmt.Println()
		fmt.Println("Uses the configured STT provider (openai/deepgram/assemblyai) and its")
		fmt.Println("vault key. Set one via `yaver voice setup` or the mobile Settings screen.")
		fmt.Println("Press Ctrl-C to stop.")
		return
	}

	cfg, _ := LoadConfig()
	v := voiceCfgOrNil(cfg)
	provider := "openai"
	if v != nil {
		provider = v.EffectiveSTTProvider()
	}

	// Resolve the model + key per provider through the shared resolver
	// (vault → env → legacy config field).
	var (
		sttModel  string
		legacyKey string
	)
	switch provider {
	case "deepgram":
		sttModel = "nova-3"
		if v != nil {
			legacyKey = v.DeepgramAPIKey
		}
	case "openai":
		sttModel = "whisper-1"
		if v != nil && v.OpenAISTTModel != "" {
			sttModel = v.OpenAISTTModel
			legacyKey = v.OpenAIAPIKey
		} else if v != nil {
			legacyKey = v.OpenAIAPIKey
		}
	case "assemblyai":
		fmt.Fprintln(os.Stderr, "yaver voice listen: assemblyai terminal capture not wired yet — use openai or deepgram")
		os.Exit(2)
	case "on-device":
		fmt.Fprintln(os.Stderr, "yaver voice listen: stt provider is on-device.")
		fmt.Fprintln(os.Stderr, "  Local whisper terminal capture lands in Phase 3. For now run:")
		fmt.Fprintln(os.Stderr, "  `yaver voice setup deepgram --deepgram-api-key dg_...`  (or openai)")
		os.Exit(2)
	default:
		fmt.Fprintf(os.Stderr, "yaver voice listen: unsupported stt provider %q\n", provider)
		os.Exit(2)
	}

	apiKey := LookupVoiceCredential(provider, "api-key", legacyKey)
	if strings.TrimSpace(apiKey) == "" {
		fmt.Fprintf(os.Stderr, "yaver voice listen: no %s key in vault/env/config.\n", provider)
		fmt.Fprintf(os.Stderr, "  Set one: `yaver voice setup %s --%s-api-key <key>`\n", provider, provider)
		os.Exit(1)
	}

	if _, err := exec.LookPath("ffmpeg"); err != nil {
		fmt.Fprintln(os.Stderr, "yaver voice listen: ffmpeg not found (needed for mic capture).")
		fmt.Fprintln(os.Stderr, "  macOS: brew install ffmpeg   ·   Debian/Ubuntu: apt install ffmpeg")
		os.Exit(1)
	}

	// Ctrl-C → graceful shutdown.
	ctx, cancel := context.WithCancel(context.Background())
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	go func() { <-sigCh; cancel() }()

	// Open STT session (shared interface).
	var (
		sess sttSession
		evCh <-chan DeepgramEvent
		err  error
	)
	switch provider {
	case "deepgram":
		var keyterms []string
		if v != nil && v.DefaultProject != "" {
			keyterms = v.ProjectKeyterms[v.DefaultProject]
		}
		var ds *DeepgramSession
		ds, evCh, err = OpenDeepgramSession(ctx, apiKey, sttModel, keyterms)
		sess = ds
	case "openai":
		var os1 *OpenAIWhisperSession
		os1, evCh, err = OpenOpenAIWhisperSession(ctx, apiKey, sttModel)
		sess = os1
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "yaver voice listen: open %s session: %v\n", provider, err)
		os.Exit(1)
	}
	defer sess.Close()

	// OpenAI Whisper is batch-only (no live partials): it buffers until
	// Finalize, then emits one final. So for OpenAI we run a single
	// utterance — record until Ctrl-C (or --once is implied) and then
	// transcribe. Deepgram is true streaming and shows partials live.
	streaming := provider == "deepgram"

	// Start mic capture via ffmpeg → 16kHz mono s16le PCM on stdout.
	micCmd, micOut, err := startMicCapture(ctx, device)
	if err != nil {
		fmt.Fprintf(os.Stderr, "yaver voice listen: start mic: %v\n", err)
		os.Exit(1)
	}
	defer func() { _ = micCmd.Process.Kill() }()

	if streaming {
		fmt.Printf("\n🎙  Listening (%s · %s). Speak — Ctrl-C to stop.\n\n", provider, sttModel)
	} else {
		fmt.Printf("\n🎙  Recording (%s · %s, batch). Speak, then press Ctrl-C to transcribe.\n\n", provider, sttModel)
	}

	// Pump PCM from ffmpeg → STT in ~32KB frames (~1s @16k mono s16le).
	go func() {
		buf := make([]byte, 32*1024)
		for {
			n, rerr := micOut.Read(buf)
			if n > 0 {
				if werr := sess.SendAudio(buf[:n]); werr != nil {
					return
				}
			}
			if rerr != nil {
				_ = sess.Finalize()
				return
			}
			select {
			case <-ctx.Done():
				return
			default:
			}
		}
	}()

	// Render transcript events. `done` is the cancellation case; we nil it
	// out (a nil channel blocks forever in select) once handled so it
	// can't re-fire — without touching the shared ctx the mic loop reads.
	var lastPartial string
	done := ctx.Done()
	for {
		select {
		case <-done:
			if !streaming {
				// Batch provider: Ctrl-C means "done speaking". Flush the
				// buffer and wait for the single final to come back rather
				// than exiting with nothing transcribed.
				fmt.Print("\r\033[K\033[2m… transcribing\033[0m\n")
				_ = sess.Finalize()
				done = nil // disable this case; wait for the final event
				continue
			}
			fmt.Print("\r\033[K")
			fmt.Println("\n👋 stopped.")
			return
		case ev, ok := <-evCh:
			if !ok {
				fmt.Println("\n(session closed)")
				return
			}
			switch ev.Kind {
			case "partial":
				lastPartial = ev.Text
				// In-place update on one line.
				fmt.Printf("\r\033[K\033[2m… %s\033[0m", ev.Text)
			case "final", "eot":
				text := strings.TrimSpace(ev.Text)
				if text == "" {
					text = strings.TrimSpace(lastPartial)
				}
				lastPartial = ""
				fmt.Printf("\r\033[K\033[1m▸ %s\033[0m\n", text)
				if ttsEcho && text != "" {
					speakLocal(ctx, text)
				}
				if once {
					cancel()
				}
			case "error":
				fmt.Printf("\r\033[K\033[31m! %s\033[0m\n", ev.Error)
			case "closed":
				fmt.Println("\n(session closed)")
				return
			}
		}
	}
}

// startMicCapture launches ffmpeg reading the default microphone and
// emitting 16kHz mono signed-16-bit-LE PCM on stdout — exactly what the
// STT clients expect. Platform input format:
//   macOS  → avfoundation ":<device>"  (audio-only; ":default" = system)
//   linux  → pulse "default" (falls back to alsa if pulse missing)
func startMicCapture(ctx context.Context, device string) (*exec.Cmd, io.ReadCloser, error) {
	var inFmt, inSpec string
	switch runtime.GOOS {
	case "darwin":
		inFmt = "avfoundation"
		// avfoundation spec is "<video>:<audio>"; ":<x>" = audio-only.
		if device == "" {
			inSpec = ":default"
		} else {
			inSpec = ":" + device
		}
	case "linux":
		inFmt = "pulse"
		inSpec = "default"
		if device != "" {
			inSpec = device
		}
	default:
		return nil, nil, fmt.Errorf("mic capture unsupported on %s", runtime.GOOS)
	}

	args := []string{
		"-hide_banner", "-loglevel", "error",
		"-f", inFmt, "-i", inSpec,
		"-ac", "1", "-ar", "16000", "-f", "s16le",
		"pipe:1",
	}
	cmd := exec.CommandContext(ctx, "ffmpeg", args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, err
	}
	// Surface ffmpeg errors (mic-permission denial, bad device) to the user.
	stderr, _ := cmd.StderrPipe()
	if stderr != nil {
		go func() {
			sc := bufio.NewScanner(stderr)
			for sc.Scan() {
				line := sc.Text()
				if strings.TrimSpace(line) != "" {
					fmt.Fprintf(os.Stderr, "\r\033[K[mic] %s\n", line)
				}
			}
		}()
	}
	if err := cmd.Start(); err != nil {
		return nil, nil, err
	}
	return cmd, stdout, nil
}

// speakLocal speaks text via the free OS TTS engine: `say` on macOS,
// `espeak`/`espeak-ng` on Linux. Best-effort — silently no-ops if no
// engine is installed (the transcript still prints).
func speakLocal(ctx context.Context, text string) {
	tctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.CommandContext(tctx, "say", text)
	case "linux":
		bin := "espeak"
		if _, err := exec.LookPath("espeak-ng"); err == nil {
			bin = "espeak-ng"
		} else if _, err := exec.LookPath("espeak"); err != nil {
			return
		}
		cmd = exec.CommandContext(tctx, bin, text)
	default:
		return
	}
	_ = cmd.Run()
}
