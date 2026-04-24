package main

// deploy_ship_cmd.go — `yaver deploy ship` CLI. Streams SSE from
// /deploy/ship and pretty-prints stdout/stderr lines + final exit
// status. Can target the local agent (default) or another device the
// caller owns via --machine.
//
// Composite targets: pass `--targets testflight,playstore` to deploy
// to several destinations in parallel. Each target runs as its own
// /deploy/ship call; output is interleaved line-by-line, prefixed
// with `[target]` so you can tell them apart. Overall exit is the
// max of per-target exit codes.

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
	targets := fs.String("targets", "", "Comma-separated list of targets for parallel deploy (e.g. 'testflight,playstore')")
	stack := fs.String("stack", "", "Stack override (owner-only; auto-resolved from workspace manifest otherwise)")
	path := fs.String("path", "", "Path override (owner-only)")
	timeout := fs.Int("timeout", 0, "Timeout in seconds (0 = server default)")
	machine := fs.String("machine", "", "Remote deviceId (default: local agent)")
	fs.Parse(args)

	// Resolve the final target list. --targets overrides --target when
	// both are set; a single value in --targets is equivalent to --target.
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
		fmt.Fprintln(os.Stderr, "   or: yaver deploy ship --app <name> --targets <t1,t2,...>  (parallel)")
		os.Exit(1)
	}

	cfg, err := LoadConfig()
	if err != nil || cfg.AuthToken == "" {
		fmt.Fprintln(os.Stderr, "Not authenticated. Run 'yaver auth' first.")
		os.Exit(1)
	}

	// Single target: existing one-stream path, no prefix.
	if len(targetList) == 1 {
		code := shipOneTarget(cfg, *app, targetList[0], *stack, *path, *timeout, *machine, "" /*prefix*/)
		os.Exit(code)
	}

	// Composite: fan out N parallel streams, line-prefixed.
	var wg sync.WaitGroup
	results := make([]int, len(targetList))
	for i, t := range targetList {
		wg.Add(1)
		go func(idx int, tgt string) {
			defer wg.Done()
			prefix := fmt.Sprintf("[%s]", tgt)
			results[idx] = shipOneTarget(cfg, *app, tgt, *stack, *path, *timeout, *machine, prefix)
		}(i, t)
	}
	wg.Wait()

	// Overall exit = max of per-target codes; also print a summary
	// block so the user doesn't have to scroll back through merged
	// logs to know what passed.
	fmt.Println()
	fmt.Println("── composite summary ──")
	worst := 0
	for i, t := range targetList {
		code := results[i]
		if code == 0 {
			fmt.Printf("  ✓ %s\n", t)
		} else {
			fmt.Printf("  ✗ %s (exit %d)\n", t, code)
			if code > worst {
				worst = code
			}
		}
	}
	os.Exit(worst)
}

// shipOneTarget posts one /deploy/ship call and streams the SSE
// response. prefix (if non-empty) is prepended to every printed line
// so parallel invocations can share a terminal.
//
// Returns the deploy's exit code (0 on success, -1 on transport
// error, otherwise subprocess exit code).
func shipOneTarget(cfg *Config, app, target, stack, path string, timeout int, machine, prefix string) int {
	body := map[string]interface{}{"app": app, "target": target}
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
			fprintErrf(prefix, "resolve %s: %v", m, rerr)
			return -1
		}
		if len(candidates) == 0 {
			fprintErrf(prefix, "no reachable endpoint for device %s", m)
			return -1
		}
		first := candidates[0]
		req, err = http.NewRequest("POST", first.BaseURL+"/deploy/ship", strings.NewReader(string(raw)))
		if err != nil {
			fprintErrf(prefix, "%v", err)
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
			fprintErrf(prefix, "%v", err)
			return -1
		}
		req.Header.Set("Authorization", "Bearer "+cfg.AuthToken)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")

	client := &http.Client{Timeout: 0}
	resp, err := client.Do(req)
	if err != nil {
		fprintErrf(prefix, "%v", err)
		return -1
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		rawBody, _ := io.ReadAll(resp.Body)
		fprintErrf(prefix, "HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(rawBody)))
		return -1
	}
	return printSSE(resp.Body, prefix)
}

// fprintErrf prints an error to stderr, prefixed if prefix is non-empty.
func fprintErrf(prefix, format string, args ...interface{}) {
	if prefix != "" {
		fmt.Fprintf(os.Stderr, "%s error: %s\n", prefix, fmt.Sprintf(format, args...))
	} else {
		fmt.Fprintf(os.Stderr, "Error: %s\n", fmt.Sprintf(format, args...))
	}
}

// printlnPrefixed writes to stdout/stderr with optional prefix. The
// mu guards stdout/stderr interleaving when several goroutines run
// in parallel.
var stdoutMu sync.Mutex

func printlnPrefixed(prefix string, stream string, text string) {
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

// printSSE reads an SSE stream and renders deploy/ship events in a
// terminal-friendly way. prefix (e.g. `[testflight]`) is prepended to
// every line so parallel runs remain legible on a shared terminal.
func printSSE(body io.Reader, prefix string) int {
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
			if event == "" {
				event, dataBuf = "", ""
				continue
			}
			var payload map[string]interface{}
			_ = json.Unmarshal([]byte(dataBuf), &payload)
			switch event {
			case "meta":
				printlnPrefixed(prefix, "stdout",
					fmt.Sprintf("→ %s / %s  (path %v, timeout %vs, id %v)",
						payload["app"], payload["target"], payload["path"],
						payload["timeout_s"], payload["id"]))
			case "line":
				text, _ := payload["text"].(string)
				stream, _ := payload["stream"].(string)
				printlnPrefixed(prefix, stream, text)
			case "exit":
				code, _ := payload["code"].(float64)
				exitCode = int(code)
				dur := time.Since(started).Round(time.Second)
				if exitCode == 0 {
					printlnPrefixed(prefix, "stdout", fmt.Sprintf("✓ deploy ok (%s)", dur))
				} else {
					printlnPrefixed(prefix, "stderr", fmt.Sprintf("✗ deploy failed (exit %d, %s)", exitCode, dur))
				}
			case "error":
				errMsg, _ := payload["error"].(string)
				printlnPrefixed(prefix, "stderr", "error: "+errMsg)
				if exitCode == 0 {
					exitCode = 1
				}
			}
			event, dataBuf = "", ""
		}
	}
	return exitCode
}
