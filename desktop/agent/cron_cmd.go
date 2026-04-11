package main

// cron_cmd.go — `yaver cron` — friendly wrapper around the
// existing /schedules subsystem. The scheduler already runs
// cron-style jobs (feedback reports, loop ticks, test runs);
// this command exposes it as a general-purpose facade so the
// dev can `yaver cron add "0 */6 * * *" "echo hi"` without
// learning the internal ScheduledTask shape.
//
// Alternative to Inngest / Trigger.dev / Temporal for the
// solo case. Zero dashboards — cron is a terminal.
//
// Backing store: reuses the agent's in-memory Scheduler +
// persisted schedules.json that the daemon already owns.
// This file is a pure CLI adapter.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
)

// cronClient returns a short-timeout HTTP client pointed at the
// local agent. Reuses the on-disk config for the auth token.
func cronClient() (*http.Client, string, string, error) {
	cfg, err := LoadConfig()
	if err != nil || cfg == nil {
		return nil, "", "", fmt.Errorf("load config: %w", err)
	}
	if cfg.AuthToken == "" {
		return nil, "", "", fmt.Errorf("not signed in — run `yaver auth`")
	}
	base := "http://127.0.0.1:18080"
	return &http.Client{Timeout: 10 * time.Second}, base, cfg.AuthToken, nil
}

func runCron(args []string) {
	if len(args) == 0 {
		printCronUsage()
		os.Exit(0)
	}
	switch args[0] {
	case "add":
		cronAddCmd(args[1:])
	case "list", "ls":
		cronListCmd()
	case "remove", "rm":
		cronRemoveCmd(args[1:])
	case "pause":
		cronToggleCmd(args[1:], true)
	case "resume":
		cronToggleCmd(args[1:], false)
	case "help", "--help", "-h":
		printCronUsage()
	default:
		fmt.Fprintf(os.Stderr, "unknown cron subcommand: %s\n\n", args[0])
		printCronUsage()
		os.Exit(1)
	}
}

func printCronUsage() {
	fmt.Print(`Yaver cron — scheduled jobs on your own agent.

Usage:
  yaver cron add <cron-expr> <command> [--label name] [--kind shell|task]
  yaver cron list
  yaver cron remove <id|label>
  yaver cron pause <id|label>
  yaver cron resume <id|label>

Examples:
  yaver cron add "*/15 * * * *" "yaver test run yaver-tests"
  yaver cron add "0 9 * * 1" "yaver changelog publish" --label weekly-changelog

Backed by the agent's /schedules subsystem — no Inngest / Temporal /
Trigger.dev subscription. Jobs run in-process when the agent is
online; persisted schedules resume on next start.
`)
}

func cronAddCmd(args []string) {
	if len(args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: yaver cron add <cron-expr> <command> [--label name] [--kind shell|task]")
		os.Exit(1)
	}
	expr := args[0]
	cmd := args[1]
	label := ""
	kind := "exec"
	for i := 2; i < len(args); i++ {
		switch args[i] {
		case "--label":
			if i+1 < len(args) {
				label = args[i+1]
				i++
			}
		case "--kind":
			if i+1 < len(args) {
				kind = args[i+1]
				i++
			}
		}
	}
	if label == "" {
		label = "cron-" + randomID()
	}
	payload := map[string]interface{}{
		"id":      label,
		"name":    label,
		"cron":    expr,
		"command": cmd,
		"kind":    kind,
	}
	if err := postSchedules(payload); err != nil {
		fmt.Fprintf(os.Stderr, "add: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("✓ %s  %s  %s\n", label, expr, cmd)
}

func cronListCmd() {
	client, base, token, err := cronClient()
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
	req, _ := http.NewRequest("GET", base+"/schedules", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := client.Do(req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "list: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()
	var body struct {
		Schedules []struct {
			ID      string `json:"id"`
			Name    string `json:"name"`
			Cron    string `json:"cron"`
			Command string `json:"command"`
			Paused  bool   `json:"paused"`
			LastRun string `json:"lastRun"`
		} `json:"schedules"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&body)
	if len(body.Schedules) == 0 {
		fmt.Println("No schedules. `yaver cron add \"*/15 * * * *\" \"...\"` to create one.")
		return
	}
	for _, s := range body.Schedules {
		state := "●"
		if s.Paused {
			state = "⏸"
		}
		fmt.Printf("  %s %s  %s  %s\n", state, s.Name, s.Cron, s.Command)
		if s.LastRun != "" {
			fmt.Printf("      last: %s\n", s.LastRun)
		}
	}
}

func cronRemoveCmd(args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: yaver cron remove <id|label>")
		os.Exit(1)
	}
	client, base, token, err := cronClient()
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
	req, _ := http.NewRequest("DELETE", base+"/schedules/"+args[0], nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := client.Do(req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "remove: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		fmt.Fprintf(os.Stderr, "remove: HTTP %d\n", resp.StatusCode)
		os.Exit(1)
	}
	fmt.Printf("✓ removed %s\n", args[0])
}

func cronToggleCmd(args []string, pause bool) {
	if len(args) < 1 {
		op := "pause"
		if !pause {
			op = "resume"
		}
		fmt.Fprintf(os.Stderr, "usage: yaver cron %s <id|label>\n", op)
		os.Exit(1)
	}
	client, base, token, err := cronClient()
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
	action := "resume"
	if pause {
		action = "pause"
	}
	req, _ := http.NewRequest("POST", base+"/schedules/"+args[0]+"/"+action, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := client.Do(req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", action, err)
		os.Exit(1)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		fmt.Fprintf(os.Stderr, "%s: HTTP %d\n", action, resp.StatusCode)
		os.Exit(1)
	}
	fmt.Printf("✓ %sd %s\n", strings.TrimSuffix(action, "e"), args[0])
}

// postSchedules POSTs a single schedule via /schedules.
func postSchedules(payload map[string]interface{}) error {
	client, base, token, err := cronClient()
	if err != nil {
		return err
	}
	body, _ := json.Marshal(payload)
	req, _ := http.NewRequest("POST", base+"/schedules", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("schedules API returned HTTP %d", resp.StatusCode)
	}
	return nil
}
