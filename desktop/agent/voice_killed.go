package main

// voice_killed.go — CLI entrypoint for the hands-free agent loop.
// The filename is a vestige from the 2026-04-28 kill; the surface is
// alive again as of 2026-05-27 (project_voice_glasses_revival_2026_05_27.md).
// Rename in a follow-up sweep to voice_cli.go.

import (
	"flag"
	"fmt"
	"os"
)

func runVoice(args []string) {
	fs := flag.NewFlagSet("voice", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "yaver voice — hands-free agent loop")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "  yaver voice status                 show readiness + provider state")
		fmt.Fprintln(os.Stderr, "  yaver voice setup                  print interactive setup hints")
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
	case "setup":
		voiceCLISetup()
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
	fmt.Printf("  stt provider:   deepgram-flux   ready=%v\n", v.DeepgramAPIKey != "")
	fmt.Printf("  tts provider:   cartesia-sonic  ready=%v\n", v.CartesiaAPIKey != "")
	if v.DefaultProject != "" {
		fmt.Printf("  default project: %s\n", v.DefaultProject)
	}
	if len(v.ProjectKeyterms) > 0 {
		fmt.Printf("  keyterm bias for %d project(s)\n", len(v.ProjectKeyterms))
	}
}

func voiceCLISetup() {
	fmt.Println("Voice setup — add this block to ~/.yaver/config.json:")
	fmt.Println()
	fmt.Println(`  "voice": {`)
	fmt.Println(`    "enabled": true,`)
	fmt.Println(`    "deepgram_api_key": "...",`)
	fmt.Println(`    "cartesia_api_key": "...",`)
	fmt.Println(`    "cartesia_voice_id": "",`)
	fmt.Println(`    "default_project": "yaver",`)
	fmt.Println(`    "project_keyterms": {`)
	fmt.Println(`      "yaver":   ["Convex","Hermes","useState","TestFlight","Cloudflare"],`)
	fmt.Println(`      "talos":   ["Convex","useState","TestFlight"]`)
	fmt.Println(`    }`)
	fmt.Println(`  }`)
	fmt.Println()
	fmt.Println("Get keys from:")
	fmt.Println("  Deepgram:  https://console.deepgram.com  (Flux Nova-3 streaming)")
	fmt.Println("  Cartesia:  https://play.cartesia.ai/keys (Sonic-3 model)")
	fmt.Println()
	fmt.Println("Then restart the agent (yaver serve) and call GET /voice/status to verify.")
}
