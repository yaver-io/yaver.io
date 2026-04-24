package main

// deploy_ship_cmd.go — `yaver deploy ship` CLI. Streams SSE from
// /deploy/ship and pretty-prints stdout/stderr lines + final exit
// status. Can target the local agent (default) or another device the
// caller owns via --machine.

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

func runDeployShipCmd(args []string) {
	fs := flag.NewFlagSet("deploy ship", flag.ExitOnError)
	app := fs.String("app", "", "App/project name (required)")
	target := fs.String("target", "", "Target (testflight, playstore, cloudflare, convex, npm-publish, pypi-publish)")
	stack := fs.String("stack", "", "Stack override (owner-only; auto-resolved from workspace manifest otherwise)")
	path := fs.String("path", "", "Path override (owner-only)")
	timeout := fs.Int("timeout", 0, "Timeout in seconds (0 = server default)")
	machine := fs.String("machine", "", "Remote deviceId (default: local agent)")
	fs.Parse(args)

	if *app == "" || *target == "" {
		fmt.Fprintln(os.Stderr, "Usage: yaver deploy ship --app <name> --target <target> [--machine <deviceId>]")
		os.Exit(1)
	}

	cfg, err := LoadConfig()
	if err != nil || cfg.AuthToken == "" {
		fmt.Fprintln(os.Stderr, "Not authenticated. Run 'yaver auth' first.")
		os.Exit(1)
	}

	body := map[string]interface{}{"app": *app, "target": *target}
	if *stack != "" {
		body["stack"] = *stack
	}
	if *path != "" {
		body["path"] = *path
	}
	if *timeout > 0 {
		body["timeout_sec"] = *timeout
	}
	raw, _ := json.Marshal(body)

	// Build request.
	var req *http.Request
	if m := strings.TrimSpace(*machine); m != "" {
		// Remote peer via the relay proxy. We build the request ourselves
		// against the peer's public base URL so the SSE stream flows
		// through without a buffering JSON wrapper.
		candidates, token, err := resolveRemoteAgentCandidates(m)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error resolving remote device %s: %v\n", m, err)
			os.Exit(1)
		}
		if len(candidates) == 0 {
			fmt.Fprintf(os.Stderr, "No reachable endpoint for device %s\n", m)
			os.Exit(1)
		}
		first := candidates[0]
		req, err = http.NewRequest("POST", first.BaseURL+"/deploy/ship", strings.NewReader(string(raw)))
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		req.Header.Set("Authorization", "Bearer "+token)
		for k, v := range first.Headers {
			req.Header.Set(k, v)
		}
	} else {
		baseURL := localAgentBaseURL()
		req, err = http.NewRequest("POST", baseURL+"/deploy/ship", strings.NewReader(string(raw)))
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		req.Header.Set("Authorization", "Bearer "+cfg.AuthToken)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")

	client := &http.Client{Timeout: 0} // no overall timeout — the server enforces its own
	resp, err := client.Do(req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		fmt.Fprintf(os.Stderr, "Error: HTTP %d: %s\n", resp.StatusCode, strings.TrimSpace(string(raw)))
		os.Exit(1)
	}

	printSSE(resp.Body)
}

// printSSE reads an SSE stream and renders deploy/ship events in a
// terminal-friendly way: meta in grey, stdout plain, stderr bold,
// final exit line with summary.
func printSSE(body io.Reader) {
	reader := bufio.NewReaderSize(body, 64*1024)
	var event, dataBuf string
	exitCode := 0
	started := time.Now()
	for {
		line, err := reader.ReadString('\n')
		if line == "" && err != nil {
			break
		}
		line = strings.TrimRight(line, "\r\n")
		switch {
		case strings.HasPrefix(line, "event: "):
			event = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			dataBuf = strings.TrimPrefix(line, "data: ")
		case line == "":
			if event != "" {
				var payload map[string]interface{}
				_ = json.Unmarshal([]byte(dataBuf), &payload)
				switch event {
				case "meta":
					fmt.Printf("→ %s / %s   (path %v, timeout %vs)\n",
						payload["app"], payload["target"], payload["path"], payload["timeout_s"])
				case "line":
					text, _ := payload["text"].(string)
					stream, _ := payload["stream"].(string)
					if stream == "stderr" {
						fmt.Fprintln(os.Stderr, text)
					} else {
						fmt.Println(text)
					}
				case "exit":
					code, _ := payload["code"].(float64)
					exitCode = int(code)
					dur := time.Since(started).Round(time.Second)
					if exitCode == 0 {
						fmt.Printf("✓ deploy ok (%s)\n", dur)
					} else {
						fmt.Fprintf(os.Stderr, "✗ deploy failed (exit %d, %s)\n", exitCode, dur)
					}
				case "error":
					errMsg, _ := payload["error"].(string)
					fmt.Fprintf(os.Stderr, "error: %s\n", errMsg)
					exitCode = 1
				}
			}
			event, dataBuf = "", ""
		}
	}
	if exitCode != 0 {
		os.Exit(exitCode)
	}
}
