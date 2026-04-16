package main

// support_cmd.go — `yaver support` CLI. Owner-facing control plane
// for the in-memory support session in support.go.
//
// Subcommands:
//
//   yaver support start  [--ttl 30m] [--label "cousin"]
//   yaver support invite [--ttl 30m] [--label "X"]   (alias for start)
//   yaver support status
//   yaver support stop
//   yaver support connect <url> <CODE> [cmd…]  — agent-to-agent TeamViewer moment

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

func urlEscape(s string) string { return url.QueryEscape(s) }

func runSupport(args []string) {
	if len(args) == 0 {
		printSupportUsage()
		return
	}
	switch args[0] {
	case "start", "invite":
		runSupportStart(args[1:])
	case "stop", "end", "revoke":
		runSupportStop()
	case "status", "show":
		runSupportStatus()
	case "connect":
		runSupportConnect(args[1:])
	case "help", "-h", "--help":
		printSupportUsage()
	default:
		fmt.Fprintf(os.Stderr, "unknown support subcommand: %s\n\n", args[0])
		printSupportUsage()
		os.Exit(1)
	}
}

func printSupportUsage() {
	fmt.Println(`usage: yaver support <subcommand>

  start [--ttl 30m] [--label "cousin"]
      Open a remote-support window: 6-char code + bearer token + URL.
      A guest who opens the URL (or types the code in the Yaver app)
      gets scoped access — terminal, exec, file browse, browser session,
      system status — on this machine until the window closes.

  invite [--ttl 30m] [--label "X"]
      Alias for 'start'. Also prints a ready-to-send URL.

  status
      Show active support session (code, time left, allowed paths).

  stop
      Revoke the active session. Anyone connected through it loses
      access on their next request.

  connect <url> <CODE> [command…]
      Redeem a support code against a remote Yaver agent and take
      control. With no command, drops into a remote shell REPL.
      With a command, runs it remotely and streams output back.
      Examples:
          yaver support connect https://relay.yaver.io/d/abc123 K7WP3N
          yaver support connect http://10.0.0.5:18080 ABCD23 "uname -a"`)
}

func runSupportStart(args []string) {
	label, ttl := "", ""
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--ttl":
			if i+1 < len(args) {
				ttl = args[i+1]
				i++
			}
		case "--label":
			if i+1 < len(args) {
				label = args[i+1]
				i++
			}
		}
	}
	body := map[string]interface{}{}
	if ttl != "" {
		body["ttl"] = ttl
	}
	if label != "" {
		body["label"] = label
	}
	resp, err := localAgentRequest("POST", "/support/start", body)
	if err != nil {
		fmt.Fprintf(os.Stderr, "support start: %v\n", err)
		os.Exit(1)
	}
	printSupportSummary(resp, true)
}

func runSupportStop() {
	resp, err := localAgentRequest("POST", "/support/stop", nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "support stop: %v\n", err)
		os.Exit(1)
	}
	stopped, _ := resp["stopped"].(bool)
	if stopped {
		fmt.Println("✓ support session ended")
	} else {
		fmt.Println("(no active support session)")
	}
}

func runSupportStatus() {
	resp, err := localAgentRequest("GET", "/support/status", nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "support status: %v\n", err)
		os.Exit(1)
	}
	if active, _ := resp["active"].(bool); !active {
		fmt.Println("(no active support session)")
		return
	}
	printSupportSummary(resp, false)
}

// printSupportSummary renders a session payload (start/status). The
// bearer token is intentionally not echoed to the terminal — a shoulder
// surfer shouldn't be able to copy it; clients receive it over HTTP.
func printSupportSummary(resp map[string]interface{}, justStarted bool) {
	code, _ := resp["code"].(string)
	host, _ := resp["host"].(string)
	deviceID, _ := resp["deviceId"].(string)
	label, _ := resp["label"].(string)
	expiresAt, _ := resp["expiresAt"].(string)

	fmt.Println()
	if justStarted {
		fmt.Println("✓ support session open — share the code or URL below.")
	} else {
		fmt.Println("Active support session.")
	}
	fmt.Println()
	if code != "" {
		fmt.Println("  Code:")
		fmt.Println()
		fmt.Print(bigPasskey(code))
		fmt.Println()
	}
	if label != "" {
		fmt.Printf("  Label:       %s\n", label)
	}
	if host != "" {
		fmt.Printf("  Host:        %s\n", host)
	}
	if expiresAt != "" {
		if t, err := time.Parse(time.RFC3339, expiresAt); err == nil {
			fmt.Printf("  Expires:     %s (in %s)\n",
				expiresAt, time.Until(t).Round(time.Second))
		} else {
			fmt.Printf("  Expires:     %s\n", expiresAt)
		}
	}
	fmt.Println()

	if deviceID != "" && code != "" {
		fmt.Println("  Shareable URLs:")
		// Hosted landing first — `web/app/support/page.tsx` reads
		// ?agent=&code= and does the redeem in the browser. We don't
		// embed a specific relay since the user may want to pick one
		// or use Tailscale / LAN; show the candidate relays below.
		if cfg, err := LoadConfig(); err == nil && cfg != nil {
			for _, relayBase := range supportRelayBases(cfg) {
				agentBase := fmt.Sprintf("%s/d/%s", relayBase, deviceID)
				fmt.Printf("    https://yaver.io/support?agent=%s&code=%s\n",
					urlEscape(agentBase), code)
				// Also the agent-embedded console on the same relay,
				// in case yaver.io is unavailable.
				fmt.Printf("    %s/app/?support=%s\n", agentBase, code)
			}
		}
		// Local LAN fallback so the host can test the flow without
		// leaving their desk.
		fmt.Printf("    http://127.0.0.1:18080/app/?support=%s  (this machine, LAN only)\n", code)
		// Agent-to-agent "TeamViewer connect" hint.
		fmt.Println()
		fmt.Println("  Agent-to-agent: on another Yaver machine, run:")
		fmt.Printf("    yaver support connect <agent-url> %s\n", code)
	}
	fmt.Println()
	fmt.Println("  Stop anytime with:  yaver support stop")
	fmt.Println()
	fmt.Println("  ⚠ This grants terminal / exec / file-browse access on this machine.")
	fmt.Println("    Only share with someone you trust — revoke with `yaver support stop`")
	fmt.Println("    the moment you're done.")
	fmt.Println()
}

func supportRelayBases(cfg *Config) []string {
	if cfg == nil {
		return nil
	}
	seen := map[string]bool{}
	out := []string{}
	appendRelay := func(raw string) {
		base := strings.TrimRight(strings.TrimSpace(raw), "/")
		if base == "" || seen[base] {
			return
		}
		seen[base] = true
		out = append(out, base)
	}
	for _, relay := range cfg.RelayServers {
		appendRelay(relay.HttpURL)
	}
	for _, relay := range cfg.CachedRelayServers {
		appendRelay(relay.HttpURL)
	}
	return out
}

// -----------------------------------------------------------------------
// support connect — agent-to-agent / laptop-to-remote "take over".
//
// Flow from the caller's side:
//   1. GET  <url>/support/info      — confirm a session is open.
//   2. POST <url>/support/redeem {code} — exchange code for bearer.
//   3. Either:
//        a) if a command was passed on argv, POST /exec with it, poll
//           /exec/{id} until done, print stdout, exit with remote rc.
//        b) otherwise drop into a readline REPL: each line is exec'd
//           remotely, output streamed back, Ctrl-D to exit.
//
// The bearer is never persisted to disk — it dies with the process.

// runSupportConnect parses args and dispatches to exec-one-shot or REPL.
func runSupportConnect(args []string) {
	if len(args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: yaver support connect <url> <CODE> [command…]")
		os.Exit(1)
	}
	baseURL := strings.TrimRight(args[0], "/")
	code := strings.TrimSpace(args[1])
	remainder := args[2:]

	// Probe first so a wrong URL fails with a clean error before we
	// start firing redeem POSTs.
	if err := probeSupportInfo(baseURL); err != nil {
		fmt.Fprintf(os.Stderr, "probe %s/support/info: %v\n", baseURL, err)
		os.Exit(1)
	}

	bearer, info, err := redeemSupportCode(baseURL, code)
	if err != nil {
		fmt.Fprintf(os.Stderr, "redeem: %v\n", err)
		os.Exit(1)
	}
	host, _ := info["host"].(string)
	expiresAt, _ := info["expiresAt"].(string)
	fmt.Printf("✓ connected to %s (session expires %s)\n", host, expiresAt)

	if len(remainder) > 0 {
		command := strings.Join(remainder, " ")
		rc := runRemoteExec(baseURL, bearer, command)
		os.Exit(rc)
	}
	runRemoteShell(baseURL, bearer, host)
}

func probeSupportInfo(baseURL string) error {
	req, err := supportRequest(http.MethodGet, baseURL+"/support/info", "", nil)
	if err != nil {
		return err
	}
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	var info map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return err
	}
	if active, _ := info["active"].(bool); !active {
		return fmt.Errorf("no active support session at %s", baseURL)
	}
	return nil
}

func redeemSupportCode(baseURL, code string) (string, map[string]interface{}, error) {
	body, _ := json.Marshal(map[string]string{"code": code})
	req, err := supportRequest(http.MethodPost, baseURL+"/support/redeem", "", bytes.NewReader(body))
	if err != nil {
		return "", nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return "", nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	var info map[string]interface{}
	if err := json.Unmarshal(raw, &info); err != nil {
		return "", nil, fmt.Errorf("parse redeem response: %v", err)
	}
	token, _ := info["token"].(string)
	if token == "" {
		return "", nil, fmt.Errorf("redeem returned no token")
	}
	return token, info, nil
}

// runRemoteExec runs a single command and streams stdout+stderr back.
// Exit code mirrors the remote process (or 1 on transport error).
func runRemoteExec(baseURL, bearer, command string) int {
	body, _ := json.Marshal(map[string]interface{}{
		"command": command,
		"timeout": 3600,
	})
	req, err := supportRequest(http.MethodPost, baseURL+"/exec", bearer, bytes.NewReader(body))
	if err != nil {
		fmt.Fprintf(os.Stderr, "exec: %v\n", err)
		return 1
	}
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "exec: %v\n", err)
		return 1
	}
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 {
		fmt.Fprintf(os.Stderr, "remote rejected exec (HTTP %d): %s\n",
			resp.StatusCode, strings.TrimSpace(string(raw)))
		return 1
	}
	var r map[string]interface{}
	_ = json.Unmarshal(raw, &r)
	execID, _ := r["execId"].(string)
	if execID == "" {
		fmt.Fprintln(os.Stderr, "remote did not return an execId")
		return 1
	}
	return pollRemoteExec(baseURL, bearer, execID)
}

// pollRemoteExec follows a remote exec to completion, printing new
// output as it arrives. ~300 ms poll interval — fine for a human-driven
// REPL, and it avoids the WebSocket dance that /ws/terminal would need.
func pollRemoteExec(baseURL, bearer, execID string) int {
	client := &http.Client{Timeout: 10 * time.Second}
	seenOut := 0
	seenErr := 0
	for {
		req, err := supportRequest(http.MethodGet, baseURL+"/exec/"+execID, bearer, nil)
		if err != nil {
			fmt.Fprintf(os.Stderr, "poll: %v\n", err)
			return 1
		}
		resp, err := client.Do(req)
		if err != nil {
			fmt.Fprintf(os.Stderr, "poll: %v\n", err)
			return 1
		}
		raw, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 200 {
			fmt.Fprintf(os.Stderr, "poll HTTP %d: %s\n", resp.StatusCode, string(raw))
			return 1
		}
		var r map[string]interface{}
		_ = json.Unmarshal(raw, &r)
		sess, _ := r["exec"].(map[string]interface{})
		out, _ := sess["stdout"].(string)
		errStr, _ := sess["stderr"].(string)
		if len(out) > seenOut {
			fmt.Print(out[seenOut:])
			seenOut = len(out)
		}
		if len(errStr) > seenErr {
			fmt.Fprint(os.Stderr, errStr[seenErr:])
			seenErr = len(errStr)
		}
		status, _ := sess["status"].(string)
		// ExecStatus values from exec.go: "running" | "completed" | "failed".
		if status == "completed" || status == "failed" {
			rcFloat, _ := sess["exitCode"].(float64)
			return int(rcFloat)
		}
		time.Sleep(300 * time.Millisecond)
	}
}

func supportRequest(method, url, bearer string, body io.Reader) (*http.Request, error) {
	req, err := http.NewRequest(method, url, body)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(bearer) != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	base := strings.TrimSpace(url)
	if idx := strings.Index(base, "/support/"); idx >= 0 {
		base = base[:idx]
	} else if idx := strings.Index(base, "/exec"); idx >= 0 {
		base = base[:idx]
	}
	if relayPassword, err := relayPasswordForBase(base); err == nil && relayPassword != "" {
		req.Header.Set("X-Relay-Password", relayPassword)
	}
	return req, nil
}

// runRemoteShell is an interactive REPL — read a line, run it remotely,
// stream output, repeat. Ctrl-D on an empty line exits. This is a
// deliberate stripped-down mode: no PTY, no job control, no colors. The
// real PTY is /ws/terminal once a richer client lands.
func runRemoteShell(baseURL, bearer, host string) {
	fmt.Println()
	fmt.Printf("  yaver support shell — remote: %s\n", host)
	fmt.Println("  (line-oriented; Ctrl-D to exit; each line runs in a fresh shell)")
	fmt.Println()

	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for {
		fmt.Printf("%s$ ", host)
		if !scanner.Scan() {
			fmt.Println()
			return
		}
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if line == "exit" || line == "quit" {
			return
		}
		_ = runRemoteExec(baseURL, bearer, line)
	}
}
