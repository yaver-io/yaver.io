package main

// voice_test.go — `yaver voice test`: a Flux-style live transcription
// playground in the terminal, modeled on https://flux.deepgram.com.
//
// Speak and watch your words appear: an updating partial line ("… text",
// dim) collapses into a timestamped final ("▸ HH:MM:SS.mmm  text", green)
// on end-of-turn, with an end-of-turn confidence read where the provider
// supplies one. Works with every engine:
//   - deepgram  → true streaming, live partials + EOT confidence (Flux)
//   - openai    → batch: records until Ctrl-C, one final
//   - local     → free/offline whisper.cpp, batch (auto-provisions deps)
// With --tts, each final is spoken back via the free local engine.
//
// This is the terminal twin of the web + mobile test panels; they all
// drive the same STT clients and the same vault credential resolver.

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

func runVoiceTest(args []string) {
	ttsEcho, device := false, ""
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--tts", "-t":
			ttsEcho = true
		case "--device", "-d":
			if i+1 < len(args) {
				device = args[i+1]
				i++
			}
		case "-h", "--help", "help":
			fmt.Println("yaver voice test — Flux-style live transcription (speak → text)")
			fmt.Println()
			fmt.Println("  yaver voice test            speak; watch partials → finals live")
			fmt.Println("  yaver voice test --tts      speak each final back (free local TTS)")
			fmt.Println("  yaver voice test --device <name|index>   pick a mic")
			fmt.Println()
			fmt.Println("Engine = configured STT provider (deepgram=live · openai/local=batch).")
			fmt.Println("Local/offline auto-installs its deps on first run. Ctrl-C to stop.")
			return
		}
	}

	cfg, _ := LoadConfig()
	v := voiceCfgOrNil(cfg)
	provider := "openai"
	if v != nil {
		provider = v.EffectiveSTTProvider()
	}
	if provider == "on-device" {
		provider = "local"
	}

	ctx, cancel := context.WithCancel(context.Background())
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	go func() { <-sigCh; cancel() }()

	sess, evCh, streaming, err := openVoiceSTTSession(ctx, provider, v)
	if err != nil {
		fmt.Fprintf(os.Stderr, "voice test: %v\n", err)
		os.Exit(1)
	}
	defer sess.Close()

	micCmd, micOut, err := startMicCapture(ctx, device)
	if err != nil {
		fmt.Fprintf(os.Stderr, "voice test: start mic: %v\n", err)
		os.Exit(1)
	}
	defer func() { _ = micCmd.Process.Kill() }()

	// Flux-style header.
	fmt.Println()
	fmt.Println("\033[1m  FLUX-style voice test\033[0m  ·  provider=" + provider)
	if streaming {
		fmt.Println("  \033[2mReady for conversation — speak, partials update live. Ctrl-C to stop.\033[0m")
	} else {
		fmt.Println("  \033[2mRecording (batch) — speak, then Ctrl-C to transcribe.\033[0m")
	}
	fmt.Println("  \033[2m" + strings.Repeat("─", 56) + "\033[0m")
	fmt.Println("  \033[2mYour voice becomes text here\033[0m")
	fmt.Print("\033[1A") // park cursor on the placeholder line

	// Mic → STT pump.
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

	var lastPartial string
	turn := 0
	done := ctx.Done()
	for {
		select {
		case <-done:
			if !streaming {
				fmt.Print("\r\033[K  \033[2m… transcribing\033[0m\n")
				_ = sess.Finalize()
				done = nil
				continue
			}
			fmt.Print("\r\033[K")
			fmt.Println("\n  👋 stopped.")
			return
		case ev, ok := <-evCh:
			if !ok {
				fmt.Println("\n  (session closed)")
				return
			}
			switch ev.Kind {
			case "partial":
				lastPartial = ev.Text
				// Updating line, dim/italic, in place.
				fmt.Printf("\r\033[K  \033[2;3m… %s\033[0m", ev.Text)
			case "final", "eot":
				text := strings.TrimSpace(ev.Text)
				if text == "" {
					text = strings.TrimSpace(lastPartial)
				}
				lastPartial = ""
				if text == "" {
					continue
				}
				turn++
				ts := nowHMS()
				// Final: green, timestamped, like the Flux "Timestamp:" card.
				fmt.Printf("\r\033[K  \033[32m▸ [%s] %s\033[0m\n", ts, text)
				if ttsEcho {
					speakLocal(ctx, text)
				}
			case "error":
				fmt.Printf("\r\033[K  \033[31m! %s\033[0m\n", ev.Error)
			case "closed":
				fmt.Println("\n  (session closed)")
				return
			}
		}
	}
}

// openVoiceSTTSession resolves the provider's key/model and opens the
// right STT client. Shared by `voice test` (and reusable by listen).
// Returns the session, its event channel, whether it streams live
// partials, and any open error. For "local" it auto-provisions deps.
func openVoiceSTTSession(ctx context.Context, provider string, v *VoiceConfig) (sttSession, <-chan DeepgramEvent, bool, error) {
	switch provider {
	case "deepgram":
		legacy := ""
		if v != nil {
			legacy = v.DeepgramAPIKey
		}
		key := LookupVoiceCredential("deepgram", "api-key", legacy)
		if strings.TrimSpace(key) == "" {
			return nil, nil, false, fmt.Errorf("no deepgram key (set: yaver voice setup deepgram --deepgram-api-key <key>, or use provider=local)")
		}
		var keyterms []string
		if v != nil && v.DefaultProject != "" {
			keyterms = v.ProjectKeyterms[v.DefaultProject]
		}
		ds, ev, err := OpenDeepgramSession(ctx, key, "nova-3", keyterms)
		return ds, ev, true, err
	case "openai":
		legacy, model := "", "whisper-1"
		if v != nil {
			legacy = v.OpenAIAPIKey
			if v.OpenAISTTModel != "" {
				model = v.OpenAISTTModel
			}
		}
		key := LookupVoiceCredential("openai", "api-key", legacy)
		if strings.TrimSpace(key) == "" {
			return nil, nil, false, fmt.Errorf("no openai key (set: yaver voice setup openai --openai-api-key <key>, or use provider=local)")
		}
		os1, ev, err := OpenOpenAIWhisperSession(ctx, key, model)
		return os1, ev, false, err
	case "local", "whisper":
		if !LocalWhisperAvailable() {
			fmt.Fprintln(os.Stderr, "  local voice engine not ready — provisioning (one-time)…")
			ensureVoiceDeps(defaultWhisperModelURL, false)
		}
		ls, ev, err := OpenLocalWhisperSession(ctx)
		return ls, ev, false, err
	default:
		return nil, nil, false, fmt.Errorf("unsupported stt provider %q (use deepgram, openai, or local)", provider)
	}
}

// nowHMS formats the wall clock as HH:MM:SS.mmm like the Flux demo cards.
func nowHMS() string {
	t := time.Now()
	return fmt.Sprintf("%02d:%02d:%02d.%03d", t.Hour(), t.Minute(), t.Second(), t.Nanosecond()/1e6)
}
