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
)

func runStream(args []string) {
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
		var ev struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		if err := json.Unmarshal([]byte(payload), &ev); err != nil {
			continue
		}
		if ev.Type == "line" {
			fmt.Println(ev.Text)
		}
	}
	if err := scanner.Err(); err != nil {
		fmt.Fprintf(os.Stderr, "stream: connection closed: %v\n", err)
		// brief delay so the user sees the message before the prompt returns
		time.Sleep(150 * time.Millisecond)
	}
}
