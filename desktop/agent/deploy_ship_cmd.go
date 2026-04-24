package main

// deploy_ship_cmd.go — `yaver deploy ship` CLI. Streams SSE from
// /deploy/ship and pretty-prints stdout/stderr lines + final exit
// status. Can target the local agent (default) or another device the
// caller owns via --machine.
//
// Composite targets: pass `--targets testflight,playstore` and the
// CLI sends one request with a `targets: [...]` array. The server
// spawns per-target goroutines and multiplexes into a single SSE
// stream. The CLI decodes the `target` field on each event and
// prefixes lines with `[target]` so parallel output stays readable.
// The server emits a `composite` event at the end with per-target
// exit codes; the CLI renders that as the `── composite summary ──`
// footer and exits with the worst per-target code.

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

func runDeployShipCmd(args []string) {
	fs := flag.NewFlagSet("deploy ship", flag.ExitOnError)
	app := fs.String("app", "", "App/project name (required)")
	target := fs.String("target", "", "Single target (testflight, playstore, cloudflare, convex, npm-publish, pypi-publish)")
	targets := fs.String("targets", "", "Comma-separated list of targets for parallel deploy (e.g. 'testflight,playstore'). Server-side fan-out — one SSE stream, events prefixed by target.")
	stack := fs.String("stack", "", "Stack override (owner-only; auto-resolved from workspace manifest otherwise)")
	path := fs.String("path", "", "Path override (owner-only)")
	timeout := fs.Int("timeout", 0, "Timeout in seconds (0 = server default)")
	machine := fs.String("machine", "", "Remote deviceId (default: local agent)")
	fs.Parse(args)

	// Normalise target list — plural overrides singular. Empty both
	// is an error.
	var targetList []string
	if raw := strings.TrimSpace(*targets); raw != "" {
		for _, t := range strings.Split(raw, ",") {
			if v := strings.TrimSpace(t); v != "" {
				targetList = append(targetList, v)
			}
		}
	} else if v := strings.TrimSpace(*target); v != "" {
		targetList = []string{v}
	}

	if *app == "" || len(targetList) == 0 {
		fmt.Fprintln(os.Stderr, "Usage: yaver deploy ship --app <name> --target <target> [--machine <deviceId>]")
		fmt.Fprintln(os.Stderr, "   or: yaver deploy ship --app <name> --targets <t1,t2,...>  (parallel, server-side)")
		os.Exit(1)
	}

	cfg, err := LoadConfig()
	if err != nil || cfg.AuthToken == "" {
		fmt.Fprintln(os.Stderr, "Not authenticated. Run 'yaver auth' first.")
		os.Exit(1)
	}
	exit := shipToAgent(cfg, *app, targetList, *stack, *path, *timeout, *machine)
	os.Exit(exit)
}

// shipToAgent posts ONE /deploy/ship request (single or composite —
// the body carries either `target` or `targets: [...]`) and streams
// the SSE response. Returns the CLI exit code: 0 if every target
// succeeded, else the worst per-target exit code, or -1 on transport
// errors.
func shipToAgent(cfg *Config, app string, targets []string, stack, path string, timeout int, machine string) int {
	body := map[string]interface{}{"app": app}
	if len(targets) == 1 {
		body["target"] = targets[0]
	} else {
		body["targets"] = targets
	}
	if stack != "" {
		body["stack"] = stack
	}
	if path != "" {
		body["path"] = path
	}
	if timeout > 0 {
		body["timeout_sec"] = timeout
	}
	raw, _ := json.Marshal(body)

	var req *http.Request
	var err error
	if m := strings.TrimSpace(machine); m != "" {
		candidates, token, rerr := resolveRemoteAgentCandidates(m)
		if rerr != nil {
			fmt.Fprintf(os.Stderr, "Error resolving remote device %s: %v\n", m, rerr)
			return -1
		}
		if len(candidates) == 0 {
			fmt.Fprintf(os.Stderr, "No reachable endpoint for device %s\n", m)
			return -1
		}
		first := candidates[0]
		req, err = http.NewRequest("POST", first.BaseURL+"/deploy/ship", strings.NewReader(string(raw)))
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			return -1
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
			return -1
		}
		req.Header.Set("Authorization", "Bearer "+cfg.AuthToken)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")

	client := &http.Client{Timeout: 0}
	resp, err := client.Do(req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return -1
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		rawBody, _ := io.ReadAll(resp.Body)
		fmt.Fprintf(os.Stderr, "Error: HTTP %d: %s\n", resp.StatusCode, strings.TrimSpace(string(rawBody)))
		return -1
	}
	return printSSEComposite(resp.Body, len(targets) > 1)
}

// stdoutMu guards interleaved writes to os.Stdout/os.Stderr across
// parallel `yaver deploy ship` invocations (the composite path runs
// server-side now but we keep the lock for backward-compat with
// other callers of printlnPrefixed).
var stdoutMu sync.Mutex

func printlnPrefixed(prefix, stream, text string) {
	stdoutMu.Lock()
	defer stdoutMu.Unlock()
	out := text
	if prefix != "" {
		out = prefix + " " + text
	}
	if stream == "stderr" {
		fmt.Fprintln(os.Stderr, out)
	} else {
		fmt.Println(out)
	}
}

// printSSEComposite reads the server-side SSE stream (single or
// composite) and renders events in a terminal-friendly way. When
// composite=true, every per-line/per-meta/per-exit event has a
// `target` field and output is prefixed with `[target]`. The final
// `composite` event renders as a summary block. Returns the
// appropriate CLI exit code.
func printSSEComposite(body io.Reader, composite bool) int {
	reader := bufio.NewReaderSize(body, 64*1024)
	var event, dataBuf string
	started := time.Now()
	perTargetExit := map[string]int{}
	singleExit := 0
	var compositeSummary []map[string]interface{}

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
			if event == "" {
				event, dataBuf = "", ""
				continue
			}
			var payload map[string]interface{}
			_ = json.Unmarshal([]byte(dataBuf), &payload)
			tgt, _ := payload["target"].(string)
			prefix := ""
			if composite && tgt != "" {
				prefix = "[" + tgt + "]"
			}
			switch event {
			case "meta":
				msg := fmt.Sprintf("→ %s / %s  (path %v, timeout %vs, id %v)",
					payload["app"], payload["target"], payload["path"],
					payload["timeout_s"], payload["id"])
				printlnPrefixed(prefix, "stdout", msg)
			case "line":
				text, _ := payload["text"].(string)
				stream, _ := payload["stream"].(string)
				printlnPrefixed(prefix, stream, text)
			case "exit":
				code, _ := payload["code"].(float64)
				dur := time.Since(started).Round(time.Second)
				if int(code) == 0 {
					printlnPrefixed(prefix, "stdout", fmt.Sprintf("✓ deploy ok (%s)", dur))
				} else {
					printlnPrefixed(prefix, "stderr", fmt.Sprintf("✗ deploy failed (exit %d, %s)", int(code), dur))
				}
				if composite && tgt != "" {
					perTargetExit[tgt] = int(code)
				} else {
					singleExit = int(code)
				}
			case "error":
				errMsg, _ := payload["error"].(string)
				printlnPrefixed(prefix, "stderr", "error: "+errMsg)
				if composite && tgt != "" {
					if _, ok := perTargetExit[tgt]; !ok {
						perTargetExit[tgt] = 1
					}
				} else if singleExit == 0 {
					singleExit = 1
				}
			case "composite":
				if summary, ok := payload["summary"].([]interface{}); ok {
					for _, s := range summary {
						if m, ok := s.(map[string]interface{}); ok {
							compositeSummary = append(compositeSummary, m)
						}
					}
				}
			}
			event, dataBuf = "", ""
		}
	}

	if composite {
		fmt.Println()
		fmt.Println("── composite summary ──")
		worst := 0
		for _, s := range compositeSummary {
			tgt, _ := s["target"].(string)
			ok, _ := s["ok"].(bool)
			code, _ := s["code"].(float64)
			if ok {
				fmt.Printf("  ✓ %s\n", tgt)
			} else {
				cls, _ := s["error_class"].(string)
				if cls != "" {
					fmt.Printf("  ✗ %s (exit %d, %s)\n", tgt, int(code), cls)
				} else {
					fmt.Printf("  ✗ %s (exit %d)\n", tgt, int(code))
				}
				if int(code) > worst {
					worst = int(code)
				}
			}
		}
		if len(compositeSummary) == 0 {
			// Fall back to the individual exit events if the composite
			// event was missing (shouldn't happen but worth guarding).
			for t, c := range perTargetExit {
				if c != 0 {
					fmt.Printf("  ✗ %s (exit %d)\n", t, c)
					if c > worst {
						worst = c
					}
				} else {
					fmt.Printf("  ✓ %s\n", t)
				}
			}
		}
		return worst
	}
	return singleExit
}
