package main

// stream_cmd.go — `yaver stream <name>` tails a daemon-hosted log
// stream over SSE and prints lines as they arrive. The same channel
// the mobile app and web dashboard subscribe to, exposed in the
// terminal for instant live visibility into a running autodev / loop.
//
//   yaver stream                          # list active stream names
//   yaver stream autodev:sfmg-autodev     # tail one
//   yaver stream sfmg-autodev             # shorthand: prepends "autodev:"
//                                         # if no exact match exists
//
// The command auto-uses the local daemon's auth token, so no flags
// or env vars are needed. SIGINT / Ctrl-C exits cleanly.

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"golang.org/x/term"
)

func runStream(args []string) {
	// --to <device> tails a stream on a remote yaver agent. Recognised
	// before the help/list parsing so `yaver stream --to <dev> <name>`
	// works as expected.
	to := ""
	filtered := []string{}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--to":
			if i+1 < len(args) {
				to = args[i+1]
				i++
			}
		case "-h", "--help", "help":
			filtered = append(filtered, args[i])
		default:
			filtered = append(filtered, args[i])
		}
	}
	args = filtered

	if to != "" && len(args) > 0 && args[0] != "help" && args[0] != "--help" && args[0] != "-h" {
		tailRemoteStream(to, resolveStreamName(args[0]))
		return
	}

	if len(args) == 0 || args[0] == "help" || args[0] == "--help" || args[0] == "-h" {
		listStreams()
		fmt.Println()
		fmt.Println("Usage:")
		fmt.Println("  yaver stream                    list active streams")
		fmt.Println("  yaver stream <name>             tail a stream (Ctrl-C to exit)")
		fmt.Println()
		fmt.Println("Tip: `yaver stream sfmg-autodev` is shorthand for `yaver stream autodev:sfmg-autodev`.")
		return
	}

	name := args[0]
	resolved := resolveStreamName(name)
	tailStream(resolved)
}

func listStreams() {
	resp, err := localAgentRequest("GET", "/streams", nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "stream: %v\n", err)
		os.Exit(1)
	}
	names, _ := resp["streams"].([]interface{})
	if len(names) == 0 {
		fmt.Println("No active streams. Start one with `yaver autodev <project>`.")
		return
	}
	fmt.Println("Active streams:")
	for _, n := range names {
		if s, ok := n.(string); ok {
			fmt.Printf("  %s\n", s)
		}
	}
}

// ANSI helpers — kept tiny on purpose. Color codes are only emitted
// when stdout is a terminal so piped output stays clean.
const (
	ansiCyan  = "\x1b[36m"
	ansiDim   = "\x1b[2m"
	ansiBold  = "\x1b[1m"
	ansiReset = "\x1b[0m"
)

func streamANSI(code string) string {
	if !term.IsTerminal(int(os.Stdout.Fd())) {
		return ""
	}
	return code
}

// renderStreamEvent prints one structured stream event to stdout in
// chat-style. Generic across runners — yaver "speaks" on the left
// (cyan), runner replies on the right (default), tool uses are dim
// inline tags. Falls back to a raw text line for legacy "line"
// events so old publishers still render fine.
func renderStreamEvent(ev map[string]interface{}) {
	t, _ := ev["type"].(string)
	switch t {
	case "yaver_say":
		txt, _ := ev["text"].(string)
		fmt.Printf("\n%s[yaver]%s %s\n", streamANSI(ansiCyan), streamANSI(ansiReset), txt)
	case "runner_action":
		runner, _ := ev["runner"].(string)
		tool, _ := ev["tool"].(string)
		detail, _ := ev["detail"].(string)
		fmt.Printf("%s  %s · %s %s%s\n", streamANSI(ansiDim), runner, tool, detail, streamANSI(ansiReset))
	case "runner_text":
		txt, _ := ev["text"].(string)
		if strings.TrimSpace(txt) != "" {
			fmt.Println(txt)
		}
	case "runner_result":
		runner, _ := ev["runner"].(string)
		status, _ := ev["status"].(string)
		dur, _ := ev["duration_ms"].(float64)
		cost, _ := ev["cost_usd"].(float64)
		fmt.Printf("%s[%s done · %s · %.1fs · $%.4f]%s\n",
			streamANSI(ansiBold), runner, status, dur/1000.0, cost, streamANSI(ansiReset))
	case "line", "":
		// Legacy text frame — print verbatim.
		if txt, ok := ev["text"].(string); ok {
			fmt.Println(txt)
		}
	default:
		// Unknown event type — surface compactly so it isn't lost.
		if b, err := json.Marshal(ev); err == nil {
			fmt.Printf("%s[%s] %s%s\n", streamANSI(ansiDim), t, string(b), streamANSI(ansiReset))
		}
	}
}

// resolveStreamName accepts either a fully-qualified stream name
// ("autodev:sfmg-autodev") or a bare loop name ("sfmg-autodev") and
// returns whichever exists. Falls back to "autodev:<name>" when
// neither resolves so the user gets a clear "stream not found" later
// rather than a confusing fallback to a different stream.
func resolveStreamName(name string) string {
	resp, err := localAgentRequest("GET", "/streams", nil)
	if err != nil {
		return name
	}
	raw, _ := resp["streams"].([]interface{})
	have := map[string]bool{}
	for _, v := range raw {
		if s, ok := v.(string); ok {
			have[s] = true
		}
	}
	if have[name] {
		return name
	}
	if !strings.Contains(name, ":") && have["autodev:"+name] {
		return "autodev:" + name
	}
	return name
}

// tailRemoteStream attaches to a stream on a remote yaver agent
// over the existing P2P / relay transport. Same SSE parsing as
// the local tail; just hits resolveDeviceURL'd base instead of
// localhost.
func tailRemoteStream(deviceHint, name string) {
	cfg := mustLoadAuthConfig()
	client := &http.Client{Timeout: 0}
	var lastErr string
	for _, base := range remoteYaverTargets(cfg, deviceHint) {
		url := fmt.Sprintf("%s/streams/%s", base, name)
		req, err := http.NewRequest("GET", url, nil)
		if err != nil {
			lastErr = err.Error()
			continue
		}
		req.Header.Set("Authorization", "Bearer "+cfg.AuthToken)
		req.Header.Set("Accept", "text/event-stream")
		resp, err := client.Do(req)
		if err != nil {
			lastErr = err.Error()
			continue
		}
		if resp.StatusCode >= 400 {
			lastErr = fmt.Sprintf("server returned %d", resp.StatusCode)
			resp.Body.Close()
			continue
		}
		fmt.Fprintf(os.Stderr, "── tailing %s on %s (Ctrl-C to exit) ──\n", name, deviceHint)
		scanner := bufio.NewScanner(resp.Body)
		scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
		for scanner.Scan() {
			line := scanner.Text()
			if !strings.HasPrefix(line, "data:") {
				continue
			}
			payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			var ev map[string]interface{}
			if err := json.Unmarshal([]byte(payload), &ev); err != nil {
				continue
			}
			renderStreamEvent(ev)
		}
		resp.Body.Close()
		if err := scanner.Err(); err != nil {
			fmt.Fprintf(os.Stderr, "stream remote: connection closed: %v\n", err)
		}
		return
	}
	if lastErr == "" {
		lastErr = "no reachable target"
	}
	fmt.Fprintf(os.Stderr, "stream remote: %s\n", lastErr)
	os.Exit(1)
}

func tailStream(name string) {
	cfg, err := LoadConfig()
	if err != nil || cfg.AuthToken == "" {
		fmt.Fprintln(os.Stderr, "stream: not authenticated — run `yaver auth`")
		os.Exit(1)
	}
	url := fmt.Sprintf("http://127.0.0.1:18080/streams/%s", name)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "stream: %v\n", err)
		os.Exit(1)
	}
	req.Header.Set("Authorization", "Bearer "+cfg.AuthToken)
	req.Header.Set("Accept", "text/event-stream")

	// No client-side timeout — SSE connections stay open indefinitely.
	client := &http.Client{Timeout: 0}
	resp, err := client.Do(req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "stream: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		fmt.Fprintf(os.Stderr, "stream: server returned %d\n", resp.StatusCode)
		os.Exit(1)
	}

	fmt.Fprintf(os.Stderr, "── tailing %s (Ctrl-C to exit) ──\n", name)

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		var ev map[string]interface{}
		if err := json.Unmarshal([]byte(payload), &ev); err != nil {
			continue
		}
		renderStreamEvent(ev)
	}
	if err := scanner.Err(); err != nil {
		fmt.Fprintf(os.Stderr, "stream: connection closed: %v\n", err)
		// brief delay so the user sees the message before the prompt returns
		time.Sleep(150 * time.Millisecond)
	}
}
