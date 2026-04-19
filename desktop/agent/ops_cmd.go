package main

// ops_cmd.go — `yaver ops` CLI subcommand.
//
// Shape:
//
//   yaver ops                            # list registered verbs
//   yaver ops verbs                      # same, explicit
//   yaver ops info                       # run `info` on local agent
//   yaver ops <verb> --machine=<id> --payload='{"k":"v"}'
//   yaver ops run --cmd="uname -a"       # run a shell command
//
// The CLI shells out to the local daemon at localhost:18080 — same
// self-healing path as every other local-agent CLI command (`yaver
// primary`, `yaver guests`, etc.). If the daemon isn't running it is
// spawned and polled for readiness before the request is retried.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
)

func runOps(args []string) {
	if len(args) == 0 {
		runOpsListVerbs()
		return
	}
	switch args[0] {
	case "help", "-h", "--help":
		opsUsage()
	case "verbs", "list":
		runOpsListVerbs()
	default:
		// Everything else is "run this verb". Support lightweight
		// flags so common verbs are ergonomic without JSON on the
		// command line:
		//   ops run --cmd=... --workDir=... --timeoutSec=...
		//   ops info
		runOpsInvoke(args)
	}
}

func opsUsage() {
	fmt.Print(`yaver ops — unified verb-based API (see YAVER_MCP_COVERAGE.md)

Usage:
  yaver ops                                  List registered verbs
  yaver ops verbs                            Same, explicit
  yaver ops <verb> [--machine=<id>] [--payload='{"...":"..."}']
                                             Run one verb on a machine
  yaver ops info                             Specs snapshot of the local agent
  yaver ops run --cmd='uname -a'             Convenience for the "run" verb

Every verb returns either a sync result (printed as JSON) or a streamId
(with the initial frame printed; subscribe via 'yaver stream <name>').

Guest sessions cannot call owner-only verbs — the agent enforces.
`)
}

func runOpsListVerbs() {
	token, err := opsLoadToken()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	body, status := opsLocalRequest(context.Background(), "GET", "/ops/verbs", token, nil)
	if status != 200 {
		fmt.Fprintf(os.Stderr, "ops/verbs failed: HTTP %d\n%s\n", status, string(body))
		os.Exit(1)
	}
	// Pretty-print the verb catalogue.
	var parsed struct {
		Count int `json:"count"`
		Verbs []struct {
			Name        string                 `json:"name"`
			Description string                 `json:"description"`
			Streaming   bool                   `json:"streaming"`
			AllowGuest  bool                   `json:"allowGuest"`
			Payload     map[string]interface{} `json:"payload"`
		} `json:"verbs"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		// Fall back to raw body when the shape surprises us.
		fmt.Println(string(body))
		return
	}
	fmt.Printf("%d verb(s) registered:\n\n", parsed.Count)
	for _, v := range parsed.Verbs {
		flags := []string{}
		if v.Streaming {
			flags = append(flags, "stream")
		}
		if v.AllowGuest {
			flags = append(flags, "guest-ok")
		}
		tag := ""
		if len(flags) > 0 {
			tag = " [" + strings.Join(flags, ", ") + "]"
		}
		fmt.Printf("  %-14s%s\n", v.Name, tag)
		if v.Description != "" {
			fmt.Printf("      %s\n", v.Description)
		}
	}
	fmt.Println("\nRun: yaver ops <verb> [--machine=<id>] [--payload='{...}']")
}

func runOpsInvoke(args []string) {
	verb := args[0]
	var machine, payloadJSON string
	// Ergonomic flags per common verb.
	var runCmd, runWorkDir string
	var runTimeout int

	i := 1
	for i < len(args) {
		a := args[i]
		switch {
		case strings.HasPrefix(a, "--machine="):
			machine = strings.TrimPrefix(a, "--machine=")
		case a == "--machine" && i+1 < len(args):
			machine = args[i+1]
			i++
		case strings.HasPrefix(a, "--payload="):
			payloadJSON = strings.TrimPrefix(a, "--payload=")
		case a == "--payload" && i+1 < len(args):
			payloadJSON = args[i+1]
			i++
		case strings.HasPrefix(a, "--cmd="):
			runCmd = strings.TrimPrefix(a, "--cmd=")
		case a == "--cmd" && i+1 < len(args):
			runCmd = args[i+1]
			i++
		case strings.HasPrefix(a, "--workDir="):
			runWorkDir = strings.TrimPrefix(a, "--workDir=")
		case a == "--workDir" && i+1 < len(args):
			runWorkDir = args[i+1]
			i++
		case strings.HasPrefix(a, "--timeoutSec="):
			fmt.Sscanf(strings.TrimPrefix(a, "--timeoutSec="), "%d", &runTimeout)
		case a == "--timeoutSec" && i+1 < len(args):
			fmt.Sscanf(args[i+1], "%d", &runTimeout)
			i++
		}
		i++
	}

	if machine == "" {
		machine = "local"
	}

	// Build payload. Verb-specific flags override --payload if both given.
	var payload interface{}
	switch verb {
	case "run":
		if runCmd == "" && payloadJSON == "" {
			fmt.Fprintln(os.Stderr, "run needs --cmd='...' or --payload='{\"command\":\"...\"}'")
			os.Exit(1)
		}
		if runCmd != "" {
			payload = map[string]interface{}{
				"command":    runCmd,
				"workDir":    runWorkDir,
				"timeoutSec": runTimeout,
			}
		}
	}
	if payload == nil && payloadJSON != "" {
		if err := json.Unmarshal([]byte(payloadJSON), &payload); err != nil {
			fmt.Fprintf(os.Stderr, "invalid --payload JSON: %v\n", err)
			os.Exit(1)
		}
	}

	reqBody := map[string]interface{}{
		"machine": machine,
		"verb":    verb,
	}
	if payload != nil {
		reqBody["payload"] = payload
	}
	buf, _ := json.Marshal(reqBody)

	token, err := opsLoadToken()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	body, status := opsLocalRequest(context.Background(), "POST", "/ops", token, buf)
	if status >= 500 {
		fmt.Fprintf(os.Stderr, "HTTP %d\n%s\n", status, string(body))
		os.Exit(2)
	}
	// Print the JSON response verbatim (ok:false + code is embedded
	// in it for typed errors; agents reading machine output can jq it).
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, body, "", "  "); err == nil {
		os.Stdout.Write(pretty.Bytes())
		os.Stdout.WriteString("\n")
	} else {
		os.Stdout.Write(body)
	}
	// Set exit code on typed failure for script integration.
	var parsed struct {
		OK   bool   `json:"ok"`
		Code string `json:"code"`
	}
	_ = json.Unmarshal(body, &parsed)
	if !parsed.OK {
		os.Exit(1)
	}
}

// opsLoadToken reads ~/.yaver/config.json for the bearer token. Same
// path every other *_cmd.go helper uses.
func opsLoadToken() (string, error) {
	cfg, err := LoadConfig()
	if err != nil || cfg == nil || strings.TrimSpace(cfg.AuthToken) == "" {
		return "", fmt.Errorf("not signed in — run 'yaver auth' first")
	}
	return cfg.AuthToken, nil
}

// opsLocalRequest hits the local daemon at 127.0.0.1:18080. If the
// daemon isn't running we spawn it (same self-heal mechanism as
// localAgentRequest in session_cmd.go) and retry once. Returns the
// raw body + status so the caller can pretty-print typed JSON errors.
func opsLocalRequest(ctx context.Context, method, path, token string, body []byte) ([]byte, int) {
	url := "http://127.0.0.1:18080" + path
	do := func() ([]byte, int, error) {
		var reader io.Reader
		if len(body) > 0 {
			reader = bytes.NewReader(body)
		}
		req, err := http.NewRequestWithContext(ctx, method, url, reader)
		if err != nil {
			return nil, 0, err
		}
		req.Header.Set("Authorization", "Bearer "+token)
		if len(body) > 0 {
			req.Header.Set("Content-Type", "application/json")
		}
		resp, err := httpClient.Do(req)
		if err != nil {
			return nil, 0, err
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return b, resp.StatusCode, nil
	}

	b, status, err := do()
	if err == nil {
		return b, status
	}
	// Daemon probably not running — spin it up and retry once.
	if err := ensureDaemonAlive(); err != nil {
		return []byte(fmt.Sprintf(`{"ok":false,"code":"daemon_unreachable","error":%q}`, err.Error())), 0
	}
	b, status, err = do()
	if err != nil {
		return []byte(fmt.Sprintf(`{"ok":false,"code":"daemon_unreachable","error":%q}`, err.Error())), 0
	}
	return b, status
}
