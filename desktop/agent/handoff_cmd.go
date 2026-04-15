package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// runHandoff implements `yaver handoff` — the CLI face of the
// "pass session to yaver" feature. All flags except --workdir are
// optional; sensible defaults take over when they are omitted, which
// keeps the user-facing invocation as short as:
//
//   yaver handoff
//
// Anything richer is opt-in:
//
//   yaver handoff --from <taskId|sessionFile>
//   yaver handoff --to <deviceId>
//   yaver handoff --engine hybrid
//   yaver handoff --engine runner --runner aider
//   yaver handoff --engine runner --runner ollama:qwen2.5-coder:14b
//   yaver handoff --max-kicks 50 --deadline 3600
//   yaver handoff --message "focus on the failing tests first"
//
// Local handoff hits the local daemon's /session/handoff endpoint.
// Remote handoff exports locally, then POSTs the bundle to the target.
func runHandoff(args []string) {
	// `yaver handoff status` — quick "did Yaver actually take over?" probe.
	if len(args) > 0 && args[0] == "status" {
		runHandoffStatus(args[1:])
		return
	}

	// Sub-mode shorthand: `yaver handoff autodev [flags]` is sugar for
	// `yaver handoff --autodev [flags]`. Keeps the user's invocation
	// natural ("hand off and go autodev mode") without a new top-level
	// command.
	autodevSub := false
	if len(args) > 0 && args[0] == "autodev" {
		autodevSub = true
		args = args[1:]
	}

	fs := flag.NewFlagSet("handoff", flag.ExitOnError)
	from := fs.String("from", "", "Source task id or path to session file (defaults to most recent local task)")
	to := fs.String("to", "", "Target device id/hostname for remote handoff (default: local)")
	engine := fs.String("engine", "claude", "Engine: claude | hybrid | runner")
	runner := fs.String("runner", "", "Runner id when --engine=runner (e.g. aider, codex, ollama:qwen2.5-coder:14b)")
	workDir := fs.String("workdir", "", "Working directory for the resumed loop (default: cwd)")
	maxKicks := fs.Int("max-kicks", 0, "Max autodev kicks (default 20)")
	deadlineSec := fs.Int("deadline", 0, "Wall-clock cap in seconds (0 = no cap)")
	message := fs.String("message", "", "Extra prompt appended to the resume instructions")
	stopSource := fs.Bool("stop-source", true, "Stop the source Yaver task before kicking the new loop")
	autodev := fs.Bool("autodev", false, "Autodev mode: also mine the session for new ideas, write missing tests, propose follow-ups")
	hours := fs.String("hours", "", "Wall-clock cap, e.g. '8' or 'inf' (default inf)")
	load := fs.String("load", "lite", "lite (respects AI session windows) | burst (max throughput)")
	callerPID := fs.Int("caller-pid", 0, "PID of the calling AI agent — Yaver will SIGTERM/SIGKILL it after takeover (default: auto-detect)")
	fs.Parse(args)
	if autodevSub {
		*autodev = true
	}

	wd := *workDir
	if wd == "" {
		wd, _ = os.Getwd()
	}

	body := map[string]interface{}{
		"sourceTaskId": "",
		"target":       *to,
		"engine":       *engine,
		"runner":       *runner,
		"workDir":      wd,
		"maxKicks":     *maxKicks,
		"deadlineSec":  *deadlineSec,
		"extraPrompt":  *message,
		"stopSource":   *stopSource,
		"autodev":      *autodev,
		"hours":        *hours,
		"load":         *load,
		"callerPid":    *callerPID,
	}
	if *from != "" {
		// Heuristic: if --from points at a file we can stat, treat it as a
		// session file. Otherwise treat it as a Yaver task id (or a Claude
		// Code session UUID, which ExportSession also recognises).
		if _, err := os.Stat(*from); err == nil {
			body["sourceSessionFile"] = *from
		} else {
			body["sourceTaskId"] = *from
		}
	}

	if *to != "" {
		runRemoteHandoff(body, *to)
		return
	}

	resp, err := localAgentRequest("POST", "/session/handoff", body)
	if err != nil {
		fmt.Fprintf(os.Stderr, "handoff failed: %v\n", err)
		os.Exit(1)
	}
	if ok, _ := resp["ok"].(bool); !ok {
		fmt.Fprintf(os.Stderr, "handoff failed: %v\n", resp["error"])
		os.Exit(1)
	}
	printHandoffResult(resp)
}

// runRemoteHandoff exports the source bundle locally, then POSTs it to
// the target device's /session/handoff endpoint. The target re-enters
// RunHandoff with SourceBundle pre-populated.
func runRemoteHandoff(body map[string]interface{}, deviceHint string) {
	cfg := mustLoadAuthConfig()

	// Step 1: export source on the local daemon (if the caller named one).
	var bundle interface{}
	if id, _ := body["sourceTaskId"].(string); id != "" {
		exp, err := localAgentRequest("POST", "/session/export", map[string]interface{}{
			"taskId":           id,
			"includeWorkspace": true,
			"workspaceMode":    "git",
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "remote handoff: export failed: %v\n", err)
			os.Exit(1)
		}
		bundle = exp["bundle"]
		body["sourceBundle"] = bundle
		body["sourceTaskId"] = "" // cleared — bundle is authoritative on the target
	}

	body["target"] = "" // target side runs locally relative to itself
	targetURL := resolveDeviceURL(cfg, deviceHint, true)

	payload, _ := json.Marshal(body)
	req, _ := http.NewRequest("POST", targetURL+"/session/handoff", strings.NewReader(string(payload)))
	req.Header.Set("Authorization", "Bearer "+cfg.AuthToken)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 120 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "remote handoff: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	var out map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&out)
	if ok, _ := out["ok"].(bool); !ok {
		fmt.Fprintf(os.Stderr, "remote handoff failed: %v\n", out["error"])
		os.Exit(1)
	}
	out["remoteDevice"] = deviceHint
	printHandoffResult(out)
}

// runHandoffStatus prints the most recent handoff sentinel + the
// associated loop's progress. Used to verify "did Yaver actually take
// over and is it doing work?" without inspecting raw files.
func runHandoffStatus(_ []string) {
	dir, err := ConfigDir()
	if err != nil {
		fmt.Fprintf(os.Stderr, "config dir: %v\n", err)
		os.Exit(1)
	}
	latest := filepath.Join(dir, "handoff", "latest.json")
	data, err := os.ReadFile(latest)
	if err != nil {
		fmt.Println("No handoff has happened on this machine yet.")
		fmt.Printf("(Looked for %s)\n", latest)
		return
	}
	var sentinel HandoffSentinel
	if err := json.Unmarshal(data, &sentinel); err != nil {
		fmt.Fprintf(os.Stderr, "parse sentinel: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("Last handoff:")
	fmt.Printf("  Loop:     %s\n", sentinel.LoopName)
	fmt.Printf("  Task:     %s\n", sentinel.LocalTaskID)
	fmt.Printf("  Runner:   %s\n", sentinel.Runner)
	fmt.Printf("  At:       %s\n", sentinel.WrittenAt)

	// Best-effort: ask the daemon for the loop's current state.
	resp, err := localAgentRequest("GET", "/autodev/loops", nil)
	if err != nil {
		fmt.Println("\n(daemon not reachable — can't show live loop progress)")
		return
	}
	loops, _ := resp["loops"].([]interface{})
	for _, l := range loops {
		m, _ := l.(map[string]interface{})
		spec, _ := m["spec"].(map[string]interface{})
		if name, _ := spec["name"].(string); name == sentinel.LoopName {
			fmt.Println("\nLoop progress:")
			fmt.Printf("  Status:           %v\n", m["status"])
			fmt.Printf("  Iterations:       %v\n", m["iterationCount"])
			fmt.Printf("  Last summary:     %v\n", m["lastSummary"])
			fmt.Printf("  Last iteration:   %v\n", m["lastIterationAt"])
			return
		}
	}
	fmt.Println("\n(loop not currently registered with the daemon)")
}

func printHandoffResult(resp map[string]interface{}) {
	fmt.Println("Handoff complete.")
	if v, _ := resp["loopName"].(string); v != "" {
		fmt.Printf("  Loop:        %s\n", v)
	}
	if v, _ := resp["localTaskId"].(string); v != "" {
		fmt.Printf("  Task:        %s\n", v)
	}
	if v, _ := resp["engine"].(string); v != "" {
		fmt.Printf("  Engine:      %s\n", v)
	}
	if v, _ := resp["runner"].(string); v != "" {
		fmt.Printf("  Runner:      %s\n", v)
	}
	if v, _ := resp["sentinelFile"].(string); v != "" {
		fmt.Printf("  Sentinel:    %s\n", v)
	}
	if v, _ := resp["remoteDevice"].(string); v != "" {
		fmt.Printf("  Remote:      %s\n", v)
	}
	if warns, ok := resp["warnings"].([]interface{}); ok && len(warns) > 0 {
		fmt.Println("  Warnings:")
		for _, w := range warns {
			fmt.Printf("    - %v\n", w)
		}
	}
	if v, _ := resp["message"].(string); v != "" {
		fmt.Println()
		fmt.Println(v)
	}
}
