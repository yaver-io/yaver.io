package main

// voice_agent.go — `yaver voice agent`: the CLI, half-duplex twin of the
// mobile voice loop. Speak a free-form request and it becomes a full AGENT
// task: the runner (claude/codex/opencode) decides what to do with the whole
// yaver MCP toolset — connect a machine, run hermes, deploy, edit code — the
// output streams to the terminal, and a short spoken headline is read back.
//
// This is the missing piece between `voice control` (deterministic ops-verb
// matching, no LLM) and the mobile agent-voice flow: here the AI decides.
//
// Half-duplex: a single `busy` gate mutes the mic→STT feed while a task runs
// and during the spoken readback, so the loop is turn-based over a continuous
// mic and never transcribes the agent's own TTS. Say "stop" / Ctrl-C to end.

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync/atomic"
	"syscall"
	"time"
)

func runVoiceAgent(args []string) {
	var device, runner, model string
	once, noSpeak := false, false
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--device", "-d":
			if i+1 < len(args) {
				device = args[i+1]
				i++
			}
		case "--runner", "-r":
			if i+1 < len(args) {
				runner = args[i+1]
				i++
			}
		case "--model", "-m":
			if i+1 < len(args) {
				model = args[i+1]
				i++
			}
		case "--once":
			once = true
		case "--no-speak":
			noSpeak = true
		case "-h", "--help", "help":
			printVoiceAgentUsage()
			return
		}
	}

	cfg, err := LoadConfig()
	if err != nil || cfg.AuthToken == "" {
		fmt.Fprintln(os.Stderr, "voice agent: not authenticated — run 'yaver auth'")
		os.Exit(1)
	}
	v := voiceCfgOrNil(cfg)
	provider := v.EffectiveSTTProvider() // free local engine by default
	if provider == "on-device" {
		provider = "local"
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	go func() { <-sigCh; cancel() }()

	sess, evCh, _, err := openVoiceSTTSession(ctx, provider, v)
	if err != nil {
		fmt.Fprintf(os.Stderr, "voice agent: %v\n", err)
		os.Exit(1)
	}
	defer sess.Close()

	micCmd, micOut, err := startMicCapture(ctx, device)
	if err != nil {
		fmt.Fprintf(os.Stderr, "voice agent: start mic: %v\n", err)
		os.Exit(1)
	}
	defer func() { _ = micCmd.Process.Kill() }()

	// busy gates the mic→STT feed while a task runs and during the spoken
	// readback — turn-based over a continuous mic, half-duplex so the agent
	// never hears its own voice. The pump keeps draining ffmpeg regardless.
	var busy atomic.Bool
	go func() {
		buf := make([]byte, 32*1024)
		for {
			n, rerr := micOut.Read(buf)
			if n > 0 && !busy.Load() {
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

	fmt.Printf("\n🤖🎙  Voice agent (STT %s · runner %s). Speak a request — the agent will act. Say \"stop\" or Ctrl-C to end.\n\n",
		provider, firstNonEmpty(runner, "default"))

	var lastPartial string
	done := ctx.Done()
	for {
		select {
		case <-done:
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
				if voiceControlQuit[normalizeVoiceCommand(text)] {
					fmt.Print("\r\033[K")
					fmt.Println("\n👋 stopped.")
					return
				}
				fmt.Printf("\r\033[K\033[1m▸ you:\033[0m %s\n", text)
				busy.Store(true)
				task, remote, cerr := createVoiceAgentTask(cfg.AuthToken, text, runner, model, provider, !noSpeak)
				if cerr != nil {
					fmt.Printf("   \033[31m✗ %v\033[0m\n", cerr)
					if !noSpeak {
						speakLocal(ctx, "sorry, that failed")
					}
					busy.Store(false)
					if once {
						return
					}
					continue
				}
				result, _ := streamVoiceTaskRef(cfg.AuthToken, task.TaskID, remote)
				if !noSpeak {
					if spoken := voiceBudget(result); spoken != "" {
						speakLocal(ctx, spoken)
						time.Sleep(350 * time.Millisecond)
					}
				}
				busy.Store(false)
				if once {
					return
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

func printVoiceAgentUsage() {
	fmt.Println(`yaver voice agent — speak a request, the AI agent acts (full MCP toolset)

  yaver voice agent                 continuous: speak → agent runs → spoken headline
  yaver voice agent --once          handle one request, then exit
  yaver voice agent --no-speak      don't read the result back
  yaver voice agent --runner codex  pick the runner (claude/codex/opencode)
  yaver voice agent --model <id>    model override
  yaver voice agent --device <mic>  pick a mic

Unlike ` + "`voice control`" + ` (deterministic ops verbs), this drives a real agent
task: the runner decides what to do — connect a machine, run hermes, deploy,
edit code — with the whole yaver toolset, and reads a short answer back.
Say "stop" to end. STT defaults to the free local engine. Ctrl-C to stop.`)
}

// createVoiceAgentTask posts the spoken transcript to the local daemon as a
// full agent task. source "voice" gets the yaver-wrapper capability context
// (so the runner knows it has the MCP toolset) + the voice viewport (so the
// answer is tuned for a short spoken headline). speechContext carries the
// STT/TTS state into the prompt wrapper.
func createVoiceAgentTask(authToken, transcript, runner, model, sttProvider string, ttsEnabled bool) (*taskCreateHTTPResponse, *RemoteAgentCandidate, error) {
	body, _ := json.Marshal(map[string]interface{}{
		"title":  transcript,
		"runner": runner,
		"model":  model,
		"source": "voice",
		"speechContext": map[string]interface{}{
			"inputFromSpeech": true,
			"sttProvider":     sttProvider,
			"ttsEnabled":      ttsEnabled,
		},
	})
	task, remote, err := createHTTPTaskWithCloudHandoff(context.Background(), &http.Client{Timeout: 30 * time.Second}, "http://127.0.0.1:18080", "Bearer "+authToken, body, 60*time.Second, newTerminalCloudHandoffProgressPrinter())
	if err != nil {
		if strings.Contains(err.Error(), "connect") {
			return nil, nil, fmt.Errorf("is the agent running? start it with 'yaver serve' (%w)", err)
		}
	}
	return task, remote, err
}

// streamVoiceTask streams the task output to the terminal and returns the
// accumulated text so the caller can read a budgeted headline back.
func streamVoiceTask(authToken, taskID string) (string, error) {
	return streamVoiceTaskRef(authToken, taskID, nil)
}

func streamVoiceTaskRef(authToken, taskID string, remote *RemoteAgentCandidate) (string, error) {
	baseURL := "http://127.0.0.1:18080"
	extraHeaders := map[string]string(nil)
	if remote != nil && strings.TrimSpace(remote.BaseURL) != "" {
		baseURL = strings.TrimRight(remote.BaseURL, "/")
		extraHeaders = remote.Headers
	}
	req, _ := http.NewRequestWithContext(context.Background(), "GET", baseURL+"/tasks/"+taskID+"/output", nil)
	req.Header.Set("Authorization", "Bearer "+authToken)
	for k, v := range extraHeaders {
		if strings.TrimSpace(v) != "" {
			req.Header.Set(k, v)
		}
	}
	resp, err := (&http.Client{Timeout: 30 * time.Minute}).Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var sb strings.Builder
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 1<<20), 1<<20)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var event struct {
			Type   string `json:"type"`
			Text   string `json:"text"`
			Status string `json:"status"`
		}
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &event); err != nil {
			continue
		}
		switch event.Type {
		case "output":
			fmt.Print(event.Text)
			sb.WriteString(event.Text)
		case "done":
			fmt.Printf("\n\033[2m[%s]\033[0m\n", event.Status)
			return sb.String(), nil
		}
	}
	return sb.String(), scanner.Err()
}

// voiceBudget reduces an agent's output to a short spoken headline: strip ANSI,
// take the last non-empty line (usually the conclusion), collapse whitespace,
// cap at ~280 chars so the readback stays snappy.
func voiceBudget(s string) string {
	s = stripAnsiVoice(s)
	lines := strings.Split(s, "\n")
	pick := ""
	for i := len(lines) - 1; i >= 0; i-- {
		if t := strings.TrimSpace(lines[i]); t != "" {
			pick = t
			break
		}
	}
	if pick == "" {
		pick = strings.TrimSpace(s)
	}
	pick = strings.Join(strings.Fields(pick), " ")
	if len(pick) > 280 {
		pick = strings.TrimSpace(pick[:280])
	}
	return pick
}

// stripAnsiVoice removes ANSI CSI escape sequences (colors, cursor moves) so
// the spoken headline doesn't read control codes aloud.
func stripAnsiVoice(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if s[i] == 0x1b { // ESC
			j := i + 1
			if j < len(s) && s[j] == '[' {
				j++
				for j < len(s) && (s[j] < 0x40 || s[j] > 0x7e) {
					j++
				}
				i = j // skip the final byte too
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}
