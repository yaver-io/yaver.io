package main

// voice_control.go — `yaver voice control`: drive yaver hands-free from the
// terminal. Speak a command, it routes to a `yaver ops` verb on the local
// agent, prints the result, and speaks a short confirmation back.
//
// It reuses the same STT plumbing as `voice listen`/`voice test` (streaming
// local whisper by default — free/offline — or whatever provider is
// configured) and the same ops HTTP surface as `yaver ops`, so every
// registered verb is reachable by voice:
//
//   "status"                 → ops status
//   "info"                   → ops info
//   "run git status"         → ops run  (command="git status")
//   "ops cloud status"       → ops cloud_status   (multiword verb)
//   "logs" / "build" / …     → the matching verb by name
//   "stop" / "exit"          → end the session
//
// Routing is deterministic (no LLM): the spoken phrase is normalized and
// matched against the live verb catalogue (GET /ops/verbs). Anything that
// doesn't match a verb is reported, not guessed at.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"sync/atomic"
	"syscall"
)

// voiceAction is the routed result of one spoken utterance.
type voiceAction struct {
	Kind    string // "ops" | "quit" | "none"
	Verb    string // ops verb to run (Kind=="ops")
	Cmd     string // shell command for the "run" verb
	Speak   string // short confirmation to speak before running
	Confirm bool   // destructive — require a spoken "confirm" before running

	// PayloadJSON is the verb's arguments. Before this existed, voice could
	// only ever fire NULLARY verbs: runVoiceOpsVerb built opsCLIRequest{Verb}
	// and never populated PayloadJSON, so "click Save" could match the verb
	// ghost_click_element but arrived with no query and did nothing. Carrying
	// arguments is what turns voice from a launcher into a control surface.
	PayloadJSON string

	// SpeakResult asks the runner to read the verb's result back instead of
	// just "done". This is what makes SPEECH-ONLY operation work: with no
	// video stream, hearing "three buttons: Save, Save As, Cancel" is the
	// only way to know what is on screen.
	SpeakResult bool
}

// voiceControlConfirmVerbs are consequential/destructive verbs that require
// a spoken confirmation before they run.
var voiceControlConfirmVerbs = map[string]bool{
	"destroy": true, "cloud_destroy": true, "cloud_stop": true,
	"deploy": true, "recycle": true, "scale": true,
	"push": true, "git_push": true, "provision": true,
	"cloud_snapshot_delete": true,
}

// voiceControlConfirmWords accept a pending destructive action.
var voiceControlConfirmWords = map[string]bool{
	"confirm": true, "confirmed": true, "yes": true, "do it": true,
	"go ahead": true, "approve": true, "approved": true,
}

// isDestructiveRun flags shell commands that warrant a confirmation even
// though the "run" verb itself is generic.
func isDestructiveRun(cmd string) bool {
	c := " " + strings.ToLower(cmd) + " "
	for _, pat := range []string{
		"rm -rf", "rm -r", " rm ", "dd ", "mkfs", "shutdown", "reboot",
		"> /dev/", ":(){", "git push", "kill -9", "killall", "truncate",
		"drop table", "drop database",
	} {
		if strings.Contains(c, pat) {
			return true
		}
	}
	return false
}

// defaultAssistantName is the spoken wake name used when the user hasn't
// renamed their assistant (VoiceConfig.AssistantName).
const defaultAssistantName = "yaver"

// assistantWakeWords builds the strip-from-front wake phrases for a given
// assistant name. The name is user-configurable so "Hey Sam, deploy web"
// routes exactly like "Hey Yaver, deploy web" — the leading wake phrase is
// removed before the remainder is matched against the verb catalogue.
// "please" is kept as a universal politeness filler regardless of name.
func assistantWakeWords(name string) []string {
	n := strings.ToLower(strings.TrimSpace(name))
	if n == "" {
		n = defaultAssistantName
	}
	return []string{"hey " + n, "ok " + n, "okay " + n, n, "please"}
}

// assistantNameCommonWords are everyday words that a streaming STT engine
// will frequently emit, so using one as the wake name causes stray triggers
// (the mic "hears" the name in ordinary speech). Advisory only — we still
// honour the user's choice.
var assistantNameCommonWords = map[string]bool{
	"yes": true, "no": true, "ok": true, "okay": true, "stop": true,
	"go": true, "run": true, "the": true, "and": true, "hey": true,
	"please": true, "do": true, "it": true, "now": true, "up": true,
}

// assistantNameWarning returns a non-empty advisory when the chosen wake
// name is prone to false triggers — too short for a streaming STT to anchor
// on, or a common word that shows up in ordinary speech. Empty = looks fine.
func assistantNameWarning(name string) string {
	n := strings.ToLower(strings.TrimSpace(name))
	if n == "" || n == defaultAssistantName {
		return ""
	}
	if len([]rune(n)) < 3 {
		return fmt.Sprintf("%q is very short — speech-to-text may mis-hear it and fire stray commands. Prefer the \"hey %s, …\" form, or a 3+ syllable name.", n, n)
	}
	if assistantNameCommonWords[n] {
		return fmt.Sprintf("%q is a common word — it'll trigger during normal speech. Pick a more distinctive name.", n)
	}
	return ""
}

// voiceControlWakeWords are stripped from the front of an utterance so
// "hey yaver, status" and "status" route identically. Reassigned at the
// start of a `voice control` session from VoiceConfig.AssistantName so a
// renamed assistant ("sam", "feyi", …) is recognised.
var voiceControlWakeWords = assistantWakeWords(defaultAssistantName)

// voiceControlQuit phrases end the session.
var voiceControlQuit = map[string]bool{
	"stop": true, "stop listening": true, "exit": true, "quit": true,
	"quit listening": true, "goodbye": true, "good bye": true, "that's all": true,
	"thats all": true, "done": true,
}

// voiceControlRunPrefixes introduce a raw shell command.
var voiceControlRunPrefixes = []string{"run ", "shell ", "execute ", "terminal "}

// routeVoiceCommand turns a spoken transcript into an action against the
// given set of known ops verbs. Pure (no I/O) so it is unit-testable.
func routeVoiceCommand(transcript string, knownVerbs map[string]bool) voiceAction {
	t := normalizeVoiceCommand(transcript)
	if t == "" {
		return voiceAction{Kind: "none"}
	}
	if voiceControlQuit[t] {
		return voiceAction{Kind: "quit"}
	}
	for _, p := range voiceControlRunPrefixes {
		if strings.HasPrefix(t, p) {
			cmd := strings.TrimSpace(t[len(p):])
			if cmd == "" {
				return voiceAction{Kind: "none"}
			}
			return voiceAction{Kind: "ops", Verb: "run", Cmd: cmd, Speak: "running " + cmd, Confirm: isDestructiveRun(cmd)}
		}
	}
	// Desktop control ("click Save", "open Safari", "what's on screen").
	// Tried BEFORE the bare-verb match because these phrases carry arguments
	// and would otherwise fall through to "none" — a bare-verb match can never
	// express "which button". Returns false when nothing matches, so this only
	// ever adds reachable phrases.
	if act, ok := routeVoiceDesktopCommand(t); ok {
		return act
	}
	// Explicit "ops <verb>" prefix, then a bare verb name. Multiword verbs
	// like "git push" / "cloud status" are spoken with spaces and slugged
	// to git_push / cloud_status.
	cand := t
	if strings.HasPrefix(cand, "ops ") {
		cand = strings.TrimSpace(cand[len("ops "):])
	}
	// "run" only routes through the "run <command>" prefix form above; a
	// bare "run" carries no command, so don't match it as a verb here.
	if verb := verbSlug(cand); verb != "run" && knownVerbs[verb] {
		return voiceAction{
			Kind:    "ops",
			Verb:    verb,
			Speak:   strings.ReplaceAll(verb, "_", " "),
			Confirm: voiceControlConfirmVerbs[verb],
		}
	}
	return voiceAction{Kind: "none"}
}

// normalizeVoiceCommand lowercases, strips trailing punctuation, and removes
// a leading wake word so routing is forgiving of how the phrase is spoken.
// It uses the session's configured wake words (voiceControlWakeWords).
func normalizeVoiceCommand(s string) string {
	return normalizeVoiceCommandWith(s, voiceControlWakeWords)
}

// normalizeVoiceCommandWith is the pure form: it strips any of the supplied
// wake phrases from the front of the utterance. Kept separate from the
// global so it is unit-testable with a custom assistant name.
func normalizeVoiceCommandWith(s string, wakeWords []string) string {
	t := strings.ToLower(strings.TrimSpace(s))
	t = strings.TrimRight(t, ".!?,")
	t = strings.TrimSpace(t)
	for _, w := range wakeWords {
		if t == w {
			return ""
		}
		if strings.HasPrefix(t, w+" ") || strings.HasPrefix(t, w+", ") {
			t = strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(t, w), ","))
			break
		}
	}
	return strings.TrimSpace(t)
}

// verbSlug maps a spoken multiword verb to its registered slug:
// "cloud status" → "cloud_status", collapsing internal whitespace.
func verbSlug(s string) string {
	fields := strings.Fields(strings.ToLower(strings.TrimSpace(s)))
	return strings.Join(fields, "_")
}

func runVoiceControl(args []string) {
	device, speak, once, autoYes := "", true, false, false
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--device", "-d":
			if i+1 < len(args) {
				device = args[i+1]
				i++
			}
		case "--no-speak":
			speak = false
		case "--once":
			once = true
		case "--yes", "-y":
			autoYes = true
		case "-h", "--help", "help":
			fmt.Println("yaver voice control — drive yaver hands-free (speak → run ops verb)")
			fmt.Println()
			fmt.Println("  yaver voice control            speak commands; each runs a yaver ops verb")
			fmt.Println("  yaver voice control --once     run one command, then exit")
			fmt.Println("  yaver voice control --yes      skip the spoken confirm for destructive verbs")
			fmt.Println("  yaver voice control --no-speak don't speak confirmations/results back")
			fmt.Println("  yaver voice control --device <name|index>   pick a mic")
			fmt.Println()
			fmt.Println("Examples (say): \"status\" · \"info\" · \"run git status\" · \"cloud status\" · \"stop\"")
			fmt.Println("STT = configured provider (free local whisper by default). Ctrl-C to stop.")
			return
		}
	}

	token, err := opsLoadToken()
	if err != nil {
		fmt.Fprintf(os.Stderr, "voice control: %v\n", err)
		os.Exit(1)
	}

	cfg, _ := LoadConfig()
	v := voiceCfgOrNil(cfg)
	provider := "local"
	if v != nil {
		provider = v.EffectiveSTTProvider()
	}
	if provider == "on-device" {
		provider = "local"
	}

	// Recognise the user's chosen wake name ("sam", "feyi", …) for this
	// session. Falls back to "yaver" when unset.
	assistantName := v.EffectiveAssistantName()
	voiceControlWakeWords = assistantWakeWords(assistantName)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	go func() { <-sigCh; cancel() }()

	knownVerbs := opsKnownVerbs(ctx, token)

	sess, evCh, streaming, err := openVoiceSTTSession(ctx, provider, v)
	if err != nil {
		fmt.Fprintf(os.Stderr, "voice control: %v\n", err)
		os.Exit(1)
	}
	defer sess.Close()

	micCmd, micOut, err := startMicCapture(ctx, device)
	if err != nil {
		fmt.Fprintf(os.Stderr, "voice control: start mic: %v\n", err)
		os.Exit(1)
	}
	defer func() { _ = micCmd.Process.Kill() }()

	fmt.Printf("\n🎙  voice control · provider=%s · wake=\"%s\" · %d verbs reachable\n", provider, assistantName, len(knownVerbs))
	fmt.Printf("   Say a command (\"status\", \"run git status\", \"cloud status\") — optionally prefixed \"hey %s, …\". \"stop\" or Ctrl-C to quit.\n", assistantName)
	if streaming {
		fmt.Println("   \033[2mLive — speak naturally; commands fire on each pause.\033[0m")
	} else {
		fmt.Println("   \033[2mBatch — speak, then Ctrl-C to run the captured command.\033[0m")
	}
	fmt.Println()

	// Half-duplex gate — see speakLocalGated. Without it the spoken
	// confirmations/results ("done", "confirmed", verb names) bleed from the
	// speaker into the open mic, get transcribed, and either fire stray
	// commands or loop. Raised around every speakLocalGated below; the pump
	// keeps draining ffmpeg but drops frames while it's up.
	var ttsSpeaking atomic.Bool

	go func() {
		buf := make([]byte, 32*1024)
		for {
			n, rerr := micOut.Read(buf)
			if n > 0 && !ttsSpeaking.Load() {
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
	var pending *voiceAction // a destructive action awaiting spoken confirmation
	done := ctx.Done()
	for {
		select {
		case <-done:
			if !streaming {
				_ = sess.Finalize()
				done = nil
				continue
			}
			fmt.Println("\n👋 stopped.")
			return
		case ev, ok := <-evCh:
			if !ok {
				return
			}
			switch ev.Kind {
			case "partial":
				lastPartial = ev.Text
				fmt.Printf("\r\033[K\033[2m… %s\033[0m", ev.Text)
			case "final", "eot":
				text := strings.TrimSpace(ev.Text)
				if text == "" {
					text = strings.TrimSpace(lastPartial)
				}
				lastPartial = ""
				if text == "" {
					continue
				}
				fmt.Printf("\r\033[K\033[1m▸ %s\033[0m\n", text)

				// If a destructive action is awaiting confirmation, this
				// utterance is the yes/no answer — not a new command.
				if pending != nil {
					p := pending
					pending = nil
					if voiceControlConfirmWords[normalizeVoiceCommand(text)] {
						fmt.Printf("   \033[32m✓ confirmed\033[0m\n")
						if speak {
							speakLocalGated(ctx, &ttsSpeaking, "confirmed")
						}
						runVoiceOpsVerb(ctx, token, *p, speak, &ttsSpeaking)
					} else {
						fmt.Printf("   \033[33m✗ cancelled\033[0m\n")
						if speak {
							speakLocalGated(ctx, &ttsSpeaking, "cancelled")
						}
					}
					if once {
						cancel()
						return
					}
					continue
				}

				act := routeVoiceCommand(text, knownVerbs)
				switch act.Kind {
				case "quit":
					fmt.Println("👋 stopped.")
					cancel()
					return
				case "none":
					fmt.Printf("   \033[33m(no matching command)\033[0m\n")
					if speak {
						speakLocalGated(ctx, &ttsSpeaking, "sorry, I didn't catch a command")
					}
				case "ops":
					if act.Confirm && !autoYes {
						label := act.Verb
						if act.Verb == "run" {
							label = "run " + act.Cmd
						}
						a := act
						pending = &a
						fmt.Printf("   \033[33m⚠ \"%s\" is destructive — say \"confirm\" to run, or anything else to cancel.\033[0m\n", label)
						if speak {
							speakLocalGated(ctx, &ttsSpeaking, "say confirm to "+act.Speak)
						}
						if once {
							// In --once mode there's no second turn to confirm
							// on; require --yes for destructive one-shots.
							fmt.Printf("   \033[33m(use --yes to run a destructive verb with --once)\033[0m\n")
							cancel()
							return
						}
						continue
					}
					if speak && act.Speak != "" {
						speakLocalGated(ctx, &ttsSpeaking, act.Speak)
					}
					runVoiceOpsVerb(ctx, token, act, speak, &ttsSpeaking)
				}
				if once {
					cancel()
					return
				}
			case "error":
				fmt.Printf("\r\033[K\033[31m! %s\033[0m\n", ev.Error)
			case "closed":
				return
			}
		}
	}
}

// runVoiceOpsVerb executes one routed verb against the local agent and
// prints the JSON result, speaking a terse ok/failed summary.
func runVoiceOpsVerb(ctx context.Context, token string, act voiceAction, speak bool, ttsSpeaking *atomic.Bool) {
	req := opsCLIRequest{Verb: act.Verb}
	if act.Verb == "run" {
		req.RunCmd = act.Cmd
	}
	// Carry the routed arguments. Without this every verb fired nullary, so
	// "click Save" reached ghost_click_element with no query and did nothing.
	req.PayloadJSON = act.PayloadJSON
	body, status := opsLocalRequest(ctx, "POST", "/ops", token, req.Marshal())

	var parsed struct {
		OK      bool        `json:"ok"`
		Code    string      `json:"code"`
		Error   string      `json:"error"`
		Initial interface{} `json:"initial"`
	}
	_ = json.Unmarshal(body, &parsed)

	var pretty bytes.Buffer
	if err := json.Indent(&pretty, body, "   ", "  "); err == nil {
		fmt.Printf("   %s\n", pretty.String())
	} else {
		fmt.Printf("   %s\n", string(body))
	}

	if status == 0 || !parsed.OK {
		msg := parsed.Error
		if msg == "" {
			msg = "command failed"
		}
		fmt.Printf("   \033[31m✗ %s\033[0m\n", msg)
		if speak {
			// A desktop refusal carries the information the user needs —
			// especially "ambiguous", whose candidate list IS the prompt for
			// what to say next. Speaking "ghost_click_element failed" instead
			// would strand them.
			speakLocalGated(ctx, ttsSpeaking,
				desktopVoiceSpeech(act, OpsResult{Code: parsed.Code, Error: parsed.Error, Initial: parsed.Initial}))
		}
		return
	}
	if speak {
		if act.SpeakResult {
			// Speech-only mode: the answer is the point, not "done".
			speakLocalGated(ctx, ttsSpeaking,
				desktopVoiceSpeech(act, OpsResult{OK: true, Initial: parsed.Initial}))
		} else {
			speakLocalGated(ctx, ttsSpeaking, "done")
		}
	}
}

// opsKnownVerbs fetches the live verb catalogue from the local agent so
// voice routing covers every registered verb. Falls back to a built-in
// list if the daemon can't be reached.
func opsKnownVerbs(ctx context.Context, token string) map[string]bool {
	set := map[string]bool{}
	body, status := opsLocalRequest(ctx, "GET", "/ops/verbs", token, nil)
	if status == 200 {
		var parsed struct {
			Verbs []struct {
				Name string `json:"name"`
			} `json:"verbs"`
		}
		if json.Unmarshal(body, &parsed) == nil {
			for _, vb := range parsed.Verbs {
				if vb.Name != "" {
					set[vb.Name] = true
				}
			}
		}
	}
	if len(set) == 0 {
		for _, n := range voiceControlFallbackVerbs {
			set[n] = true
		}
	}
	return set
}

// voiceControlFallbackVerbs is used only when /ops/verbs is unreachable.
var voiceControlFallbackVerbs = []string{
	"status", "info", "run", "logs", "build", "deploy", "test", "session",
	"env", "files", "git_push", "cloud_status", "cloud_start", "cloud_stop",
}
