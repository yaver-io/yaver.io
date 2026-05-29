package main

// voice_killed.go — CLI entrypoint for the hands-free agent loop.
// The filename is a vestige from the 2026-04-28 kill; the surface is
// alive again as of 2026-05-27 (project_voice_glasses_revival_2026_05_27.md).
// Rename in a follow-up sweep to voice_cli.go.

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"golang.org/x/term"
)

var (
	setVoiceCredentialForCLI = SetVoiceCredential
	hasVoiceCredentialForCLI = HasVoiceCredential
)

func runVoice(args []string) {
	fs := flag.NewFlagSet("voice", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "yaver voice — hands-free agent loop")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "  yaver voice status                 show readiness + provider state")
		fmt.Fprintln(os.Stderr, "  yaver voice test                   Flux-style live transcription test (speak → text)")
		fmt.Fprintln(os.Stderr, "  yaver voice listen                 live mic transcription → stdout")
		fmt.Fprintln(os.Stderr, "  yaver voice listen --tts           also speak finals back (free local TTS)")
		fmt.Fprintln(os.Stderr, "  yaver voice deps [--install]       check/install local deps (ffmpeg, whisper.cpp, model)")
		fmt.Fprintln(os.Stderr, "  yaver voice setup                  print setup hints")
		fmt.Fprintln(os.Stderr, "  yaver voice setup cartesia         set Cartesia as TTS provider")
		fmt.Fprintln(os.Stderr, "  yaver voice setup deepgram         single-vendor: Flux STT + Aura-2 TTS, one key")
		fmt.Fprintln(os.Stderr, "  yaver voice setup deepgram-cartesia --deepgram-api-key dg_... --cartesia-api-key ck_...")
		fmt.Fprintln(os.Stderr, "  yaver voice setup openai --openai-api-key sk-...")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Voice surface lives at /voice/status + /voice/stream on the running agent.")
		fmt.Fprintln(os.Stderr, "Mobile + Feedback-SDK clients drive it; this CLI is for inspection only.")
	}

	if len(args) == 0 {
		fs.Usage()
		return
	}
	sub := args[0]
	switch sub {
	case "status":
		voiceCLIStatus()
	case "listen":
		runVoiceListen(args[1:])
	case "test":
		runVoiceTest(args[1:])
	case "deps":
		runVoiceDeps(args[1:])
	case "setup":
		voiceCLISetupWithArgs(args[1:])
	case "-h", "--help", "help":
		fs.Usage()
	default:
		fmt.Fprintf(os.Stderr, "yaver voice: unknown subcommand %q\n", sub)
		fs.Usage()
		os.Exit(2)
	}
}

func voiceCLIStatus() {
	cfg, _ := LoadConfig()
	v := voiceCfgOrNil(cfg)
	if v == nil {
		fmt.Println("voice: not configured (config.json has no `voice` block)")
		fmt.Println("       run `yaver voice setup` for hints")
		return
	}
	fmt.Println("voice:")
	fmt.Printf("  enabled:        %v\n", v.Enabled)
	fmt.Printf("  stt provider:   %s   ready=%v\n", v.EffectiveSTTProvider(), voiceSTTReady(v))
	fmt.Printf("  tts provider:   %s   ready=%v\n", v.EffectiveTTSProvider(), voiceTTSReady(v))
	if v.DefaultProject != "" {
		fmt.Printf("  default project: %s\n", v.DefaultProject)
	}
	if len(v.ProjectKeyterms) > 0 {
		fmt.Printf("  keyterm bias for %d project(s)\n", len(v.ProjectKeyterms))
	}
}

func voiceCLISetup() {
	voiceCLISetupWithArgs(nil)
}

type voiceSetupOptions struct {
	Stack           string
	OpenAIAPIKey    string
	DeepgramAPIKey  string
	CartesiaAPIKey  string
	CartesiaVoiceID string
	DefaultProject  string
	Disable         bool
	PrintOnly       bool
}

func voiceCLISetupWithArgs(args []string) {
	fs := flag.NewFlagSet("voice setup", flag.ExitOnError)
	fs.SetOutput(os.Stderr)
	opt := voiceSetupOptions{}
	fs.StringVar(&opt.Stack, "stack", "", "Provider stack: openai, cartesia, deepgram, deepgram-cartesia")
	fs.StringVar(&opt.OpenAIAPIKey, "openai-api-key", "", "OpenAI API key")
	fs.StringVar(&opt.DeepgramAPIKey, "deepgram-api-key", "", "Deepgram API key")
	fs.StringVar(&opt.CartesiaAPIKey, "cartesia-api-key", "", "Cartesia API key")
	fs.StringVar(&opt.CartesiaVoiceID, "cartesia-voice-id", "", "Cartesia voice ID")
	fs.StringVar(&opt.DefaultProject, "default-project", "", "Default voice project slug")
	fs.BoolVar(&opt.Disable, "disable", false, "Disable voice without removing saved keys")
	fs.BoolVar(&opt.PrintOnly, "print", false, "Print setup examples without writing config")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: yaver voice setup [openai|cartesia|deepgram|deepgram-cartesia] [flags]")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}
	if fs.NArg() > 0 {
		opt.Stack = fs.Arg(0)
	}

	if opt.PrintOnly || !voiceSetupHasWriteIntent(opt) {
		printVoiceSetupHints()
		return
	}

	cfg, err := LoadConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: load config: %v\n", err)
		os.Exit(1)
	}
	if cfg == nil {
		cfg = &Config{}
	}
	if err := applyVoiceSetup(cfg, &opt, os.Stdin.Fd()); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	if err := SaveConfig(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "Error: save config: %v\n", err)
		os.Exit(1)
	}

	path, _ := ConfigPath()
	fmt.Printf("Saved voice setup to %s\n", path)
	voiceCLIStatus()
	fmt.Println("Restart yaver serve if the agent is already running.")
}

func printVoiceSetupHints() {
	fmt.Println("Voice setup — pick ONE provider stack + paste keys into ~/.yaver/config.json")
	fmt.Println()
	fmt.Println("Fast path:")
	fmt.Println()
	fmt.Println("  yaver voice setup openai --openai-api-key sk-...")
	fmt.Println("  yaver voice setup cartesia --cartesia-api-key ck_...")
	fmt.Println("  yaver voice setup deepgram --deepgram-api-key dg_...        # STT+TTS, one key")
	fmt.Println("  yaver voice setup deepgram-cartesia --deepgram-api-key dg_... --cartesia-api-key ck_...")
	fmt.Println()
	fmt.Println("OPTION 1 (default, simplest — one signup, one key):")
	fmt.Println()
	fmt.Println(`  "voice": {`)
	fmt.Println(`    "enabled": true,`)
	fmt.Println(`    "stt_provider": "openai",`)
	fmt.Println(`    "tts_provider": "openai",`)
	fmt.Println(`    "openai_api_key": "sk-..."`)
	fmt.Println(`  }`)
	fmt.Println()
	fmt.Println("  Get key: https://platform.openai.com/api-keys (your account, your billing)")
	fmt.Println()
	fmt.Println("OPTION 2 (Deepgram only — Flux STT + Aura-2 TTS, ONE signup, one key):")
	fmt.Println()
	fmt.Println(`  "voice": {`)
	fmt.Println(`    "enabled": true,`)
	fmt.Println(`    "stt_provider": "deepgram",`)
	fmt.Println(`    "tts_provider": "deepgram",`)
	fmt.Println(`    "deepgram_api_key": "..."`)
	fmt.Println(`  }`)
	fmt.Println()
	fmt.Println("  Deepgram: https://console.deepgram.com  (Flux Nova-3 STT + Aura-2 TTS,")
	fmt.Println("  ~$30/M chars TTS — roughly half Cartesia for comparable latency).")
	fmt.Println()
	fmt.Println("OPTION 3 (Deepgram STT + Cartesia TTS — premium voice quality, two signups):")
	fmt.Println()
	fmt.Println(`  "voice": {`)
	fmt.Println(`    "enabled": true,`)
	fmt.Println(`    "stt_provider": "deepgram",`)
	fmt.Println(`    "tts_provider": "cartesia",`)
	fmt.Println(`    "deepgram_api_key": "...",`)
	fmt.Println(`    "cartesia_api_key": "...",`)
	fmt.Println(`    "project_keyterms": {`)
	fmt.Println(`      "yaver":   ["Convex","Hermes","useState","TestFlight","Cloudflare"]`)
	fmt.Println(`    }`)
	fmt.Println(`  }`)
	fmt.Println()
	fmt.Println("  Cartesia: https://play.cartesia.ai/keys (Sonic-3 model, 40ms TTFA)")
	fmt.Println()
	fmt.Println("OPTION 4 (mix + match — e.g. Deepgram STT + OpenAI TTS, or any pair):")
	fmt.Println()
	fmt.Println("  Just set stt_provider + tts_provider to different values + supply both keys.")
	fmt.Println()
	fmt.Println("KEYBOARD-ONLY MODE (Yaver trio with foldable BT keyboard — no voice keys):")
	fmt.Println()
	fmt.Println("  Skip this entirely. Don't add the voice block. Yaver works fully via the")
	fmt.Println("  keyboard input + agent task list; voice is an enhancement, not a requirement.")
	fmt.Println()
	fmt.Println("Then restart the agent (yaver serve) and call GET /voice/status to verify.")
	fmt.Println("Keys stay on YOUR machine — never sync to Convex (enforced by convex_privacy_test.go).")
}

func voiceSetupHasWriteIntent(opt voiceSetupOptions) bool {
	return opt.Disable ||
		strings.TrimSpace(opt.Stack) != "" ||
		strings.TrimSpace(opt.OpenAIAPIKey) != "" ||
		strings.TrimSpace(opt.DeepgramAPIKey) != "" ||
		strings.TrimSpace(opt.CartesiaAPIKey) != "" ||
		strings.TrimSpace(opt.CartesiaVoiceID) != "" ||
		strings.TrimSpace(opt.DefaultProject) != ""
}

func applyVoiceSetup(cfg *Config, opt *voiceSetupOptions, stdin uintptr) error {
	if cfg.Voice == nil {
		cfg.Voice = &VoiceConfig{}
	}
	v := cfg.Voice
	if opt.Disable {
		v.Enabled = false
		return nil
	}

	stack := strings.ToLower(strings.TrimSpace(opt.Stack))
	stack = strings.ReplaceAll(stack, "_", "-")
	switch stack {
	case "", "existing":
	case "openai":
		v.STTProvider = "openai"
		v.TTSProvider = "openai"
	case "cartesia":
		v.TTSProvider = "cartesia"
	case "deepgram", "deepgram-only", "deepgram-deepgram":
		v.STTProvider = "deepgram"
		v.TTSProvider = "deepgram"
	case "deepgram-cartesia", "deepgram+cartesia", "dg-cartesia":
		v.STTProvider = "deepgram"
		v.TTSProvider = "cartesia"
	default:
		return fmt.Errorf("unknown voice setup stack %q (use openai, cartesia, deepgram, or deepgram-cartesia)", opt.Stack)
	}

	wroteOpenAIKey := false
	wroteDeepgramKey := false
	wroteCartesiaKey := false
	if key := strings.TrimSpace(opt.OpenAIAPIKey); key != "" {
		if err := setVoiceCredentialForCLI("openai", "api-key", key); err != nil {
			return fmt.Errorf("save openai credential: %w", err)
		}
		wroteOpenAIKey = true
		v.OpenAIAPIKey = key
		if stack == "" {
			v.STTProvider = "openai"
			v.TTSProvider = "openai"
		}
	}
	if key := strings.TrimSpace(opt.DeepgramAPIKey); key != "" {
		if err := setVoiceCredentialForCLI("deepgram", "api-key", key); err != nil {
			return fmt.Errorf("save deepgram credential: %w", err)
		}
		wroteDeepgramKey = true
		v.DeepgramAPIKey = key
		if stack == "" {
			v.STTProvider = "deepgram"
		}
	}
	if key := strings.TrimSpace(opt.CartesiaAPIKey); key != "" {
		if err := setVoiceCredentialForCLI("cartesia", "api-key", key); err != nil {
			return fmt.Errorf("save cartesia credential: %w", err)
		}
		wroteCartesiaKey = true
		v.CartesiaAPIKey = key
		if stack == "" {
			v.TTSProvider = "cartesia"
		}
	}
	if voiceID := strings.TrimSpace(opt.CartesiaVoiceID); voiceID != "" {
		v.CartesiaVoiceID = voiceID
	}
	if project := strings.TrimSpace(opt.DefaultProject); project != "" {
		v.DefaultProject = project
	}

	if term.IsTerminal(int(stdin)) {
		if v.EffectiveSTTProvider() == "openai" && v.OpenAIAPIKey == "" {
			key, err := readVoiceSecret(stdin, "OpenAI API key")
			if err != nil {
				return err
			}
			if err := setVoiceCredentialForCLI("openai", "api-key", key); err != nil {
				return fmt.Errorf("save openai credential: %w", err)
			}
			wroteOpenAIKey = true
			v.OpenAIAPIKey = key
		}
		if v.EffectiveSTTProvider() == "deepgram" && v.DeepgramAPIKey == "" {
			key, err := readVoiceSecret(stdin, "Deepgram API key")
			if err != nil {
				return err
			}
			if err := setVoiceCredentialForCLI("deepgram", "api-key", key); err != nil {
				return fmt.Errorf("save deepgram credential: %w", err)
			}
			wroteDeepgramKey = true
			v.DeepgramAPIKey = key
		}
		if v.EffectiveTTSProvider() == "cartesia" && v.CartesiaAPIKey == "" {
			key, err := readVoiceSecret(stdin, "Cartesia API key")
			if err != nil {
				return err
			}
			if err := setVoiceCredentialForCLI("cartesia", "api-key", key); err != nil {
				return fmt.Errorf("save cartesia credential: %w", err)
			}
			wroteCartesiaKey = true
			v.CartesiaAPIKey = key
		}
	}
	if err := validateVoiceSetupReady(v); err != nil {
		return err
	}

	// New writes live in the encrypted voice vault. Keep legacy fields
	// readable as fallback, but do not persist freshly provided keys
	// back into config.json.
	if wroteOpenAIKey {
		v.OpenAIAPIKey = ""
	}
	if wroteDeepgramKey {
		v.DeepgramAPIKey = ""
	}
	if wroteCartesiaKey {
		v.CartesiaAPIKey = ""
	}
	v.Enabled = true
	return nil
}

func readVoiceSecret(stdin uintptr, label string) (string, error) {
	fmt.Fprintf(os.Stderr, "%s: ", label)
	b, err := term.ReadPassword(int(stdin))
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", err
	}
	value := strings.TrimSpace(string(b))
	if value == "" {
		return "", fmt.Errorf("%s cannot be empty", label)
	}
	return value, nil
}

func voiceSTTReady(v *VoiceConfig) bool {
	if v == nil {
		return false
	}
	switch v.EffectiveSTTProvider() {
	case "openai":
		return hasVoiceCredentialForCLI("openai", "api-key", v.OpenAIAPIKey)
	case "deepgram":
		return hasVoiceCredentialForCLI("deepgram", "api-key", v.DeepgramAPIKey)
	default:
		return false
	}
}

func voiceTTSReady(v *VoiceConfig) bool {
	if v == nil {
		return false
	}
	switch v.EffectiveTTSProvider() {
	case "openai":
		return hasVoiceCredentialForCLI("openai", "api-key", v.OpenAIAPIKey)
	case "cartesia":
		return hasVoiceCredentialForCLI("cartesia", "api-key", v.CartesiaAPIKey)
	case "deepgram":
		// Aura-2 reuses the Deepgram STT key.
		return hasVoiceCredentialForCLI("deepgram", "api-key", v.DeepgramAPIKey)
	case "elevenlabs":
		return hasVoiceCredentialForCLI("elevenlabs", "api-key", v.ElevenLabsAPIKey)
	default:
		return false
	}
}

func validateVoiceSetupReady(v *VoiceConfig) error {
	switch v.EffectiveSTTProvider() {
	case "openai":
		if !hasVoiceCredentialForCLI("openai", "api-key", v.OpenAIAPIKey) {
			return fmt.Errorf("missing OpenAI API key for STT; pass --openai-api-key or run setup from an interactive terminal")
		}
	case "deepgram":
		if !hasVoiceCredentialForCLI("deepgram", "api-key", v.DeepgramAPIKey) {
			return fmt.Errorf("missing Deepgram API key; pass --deepgram-api-key or run setup from an interactive terminal")
		}
	}
	switch v.EffectiveTTSProvider() {
	case "openai":
		if !hasVoiceCredentialForCLI("openai", "api-key", v.OpenAIAPIKey) {
			return fmt.Errorf("missing OpenAI API key for TTS; pass --openai-api-key or run setup from an interactive terminal")
		}
	case "cartesia":
		if !hasVoiceCredentialForCLI("cartesia", "api-key", v.CartesiaAPIKey) {
			return fmt.Errorf("missing Cartesia API key; pass --cartesia-api-key or run setup from an interactive terminal")
		}
	case "deepgram":
		if !hasVoiceCredentialForCLI("deepgram", "api-key", v.DeepgramAPIKey) {
			return fmt.Errorf("missing Deepgram API key for TTS; pass --deepgram-api-key or run setup from an interactive terminal")
		}
	}
	return nil
}
