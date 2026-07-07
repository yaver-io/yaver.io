package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

func runCode(args []string) {
	if handled, err := runCodeControl(args); handled {
		if err != nil {
			fmt.Fprintf(os.Stderr, "code: %v\n", err)
			os.Exit(1)
		}
		return
	}

	if len(args) > 0 {
		switch args[0] {
		case "help", "-h", "--help":
			printCodeUsage()
			return
		case "list", "ls":
			runAgentMode([]string{"list"})
			return
		case "show":
			runAgentMode(args)
			return
		case "stop":
			runAgentMode(args)
			return
		case "sessions":
			runAgentMode([]string{"list"})
			return
		}
	}

	fs := flag.NewFlagSet("code", flag.ExitOnError)
	uiMode := fs.Bool("ui", false, "full-screen console mode (currently uses the interactive terminal)")
	meshMode := fs.Bool("mesh", false, "fan work out across multiple machines using agent graph mode")
	attachTarget := fs.String("attach", "", "attach terminal mode to a remote device ID or device name")
	username := fs.String("username", "", "filter remote attach targets by host email")
	workDir := fs.String("work-dir", "", "project working directory")
	runner := fs.String("runner", "", "runner ID override")
	agent := fs.String("agent", "", "terminal coding agent override (alias for --runner)")
	model := fs.String("model", "", "model override")
	mode := fs.String("mode", "", "runner mode override (opencode agent: build|plan|<custom>)")
	template := fs.String("template", "full", "mesh template: full|ship")
	maxParallel := fs.Int("max-parallel", 2, "mesh max concurrency")
	name := fs.String("name", "", "session name (mesh mode)")
	allowedDevices := fs.String("allowed-devices", "", "comma-separated device IDs or names to form the mesh execution pool")
	allowedRunners := fs.String("allowed-runners", "", "comma-separated runner allowlist for mesh nodes, e.g. ollama,opencode,codex")
	plain := fs.Bool("plain", false, "force plain line-by-line output (no TUI)")
	ptyMode := fs.Bool("pty", false, "with --attach: raw PTY wrap of the runner's own TUI on the remote machine (tmux-persistent, zero chrome)")
	chrome := fs.Bool("chrome", false, "with --pty: keep the remote tmux status bar visible")
	_ = fs.Parse(args)
	if cfg, profile, err := loadCodeConfig(); err == nil && cfg != nil && profile != nil {
		if strings.TrimSpace(*runner) == "" {
			*runner = strings.TrimSpace(profile.Runner)
		}
		if strings.TrimSpace(*model) == "" {
			*model = strings.TrimSpace(profile.Model)
		}
		if strings.TrimSpace(*mode) == "" {
			*mode = strings.TrimSpace(profile.Mode)
		}
		if strings.TrimSpace(*workDir) == "" && !profile.RepoRemote {
			*workDir = strings.TrimSpace(profile.RepoPath)
		}
		if strings.TrimSpace(*attachTarget) == "" && strings.TrimSpace(profile.WorkMode) == codeWorkModeAttached && strings.TrimSpace(profile.AttachedDeviceID) != "" {
			*attachTarget = strings.TrimSpace(profile.AttachedDeviceID)
		}
	}
	if strings.TrimSpace(*agent) != "" && strings.TrimSpace(*runner) == "" {
		*runner = normalizeRunnerID(*agent)
	}

	prompt := strings.TrimSpace(strings.Join(fs.Args(), " "))
	if *ptyMode {
		// `yaver code --attach <device> --pty [--runner codex] [-- args…]`:
		// same transport + endpoint as `yaver codex --machine=<device>` —
		// the runner's own TUI, wrapped in tmux on the remote end.
		if strings.TrimSpace(*attachTarget) == "" {
			fmt.Fprintln(os.Stderr, "code: --pty requires --attach <device> (for a local TUI just run the runner directly)")
			os.Exit(1)
		}
		ptyRunner := normalizeRunnerID(firstNonEmpty(strings.TrimSpace(*runner), "claude"))
		ptyArgs := applyRunnerYoloDefaults(ptyRunner, fs.Args())
		if err := runRemoteRunnerPTY(*attachTarget, ptyRunner, ptyArgs, "", "", *chrome, false); err != nil {
			fmt.Fprintf(os.Stderr, "code: %v\n", err)
			os.Exit(1)
		}
		return
	}
	if strings.TrimSpace(*attachTarget) != "" {
		if err := runRemoteCodeAttach(prompt, *attachTarget, *username, *runner, *model, *mode); err != nil {
			fmt.Fprintf(os.Stderr, "code: %v\n", err)
			os.Exit(1)
		}
		return
	}
	if prompt == "" {
		if *uiMode {
			fmt.Println("`yaver code --ui` currently uses the interactive terminal frontend.")
		}
		if *meshMode {
			fmt.Println("`yaver code --mesh` without a prompt drops into the shared interactive console.")
		}
		if *workDir != "" {
			restore, err := switchAgentWorkDir(*workDir)
			if err != nil {
				fmt.Fprintf(os.Stderr, "code: %v\n", err)
				os.Exit(1)
			}
			defer restore()
		}
		runCodeTerminal(strings.TrimSpace(*runner), strings.TrimSpace(*model), strings.TrimSpace(*mode))
		return
	}

	if err := ensureDaemonAlive(); err != nil {
		fmt.Fprintf(os.Stderr, "code: %v\n", err)
		os.Exit(1)
	}

	restore := func() {}
	if *workDir != "" {
		var err error
		restore, err = switchAgentWorkDir(*workDir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "code: %v\n", err)
			os.Exit(1)
		}
	}
	defer restore()

	effectiveWorkDir := *workDir
	if effectiveWorkDir == "" {
		if current, err := currentAgentWorkDir(); err == nil {
			effectiveWorkDir = current
		}
	}
	enrichedPrompt := enrichCodePrompt(prompt, effectiveWorkDir)

	if *meshMode || *maxParallel > 1 {
		runID, runName, err := createCodeGraph(CodeGraphRequest{
			Name:           *name,
			WorkDir:        firstNonEmpty(*workDir, effectiveWorkDir, "."),
			Prompt:         enrichedPrompt,
			Runner:         *runner,
			Model:          *model,
			Template:       *template,
			MaxParallel:    *maxParallel,
			AllowedDevices: splitCSVAllowlist(*allowedDevices),
			AllowedRunners: splitCSVAllowlist(*allowedRunners),
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "code mesh: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Started yaver code session %s (%s)\n\n", runID, runName)
		var streamErr error
		if !*plain && stdoutIsTTY() {
			streamErr = streamCodeGraphTUI(runID)
		} else {
			streamErr = streamCodeGraph(runID)
		}
		if streamErr != nil {
			fmt.Fprintf(os.Stderr, "code mesh stream: %v\n", streamErr)
			os.Exit(1)
		}
		return
	}

	taskID, err := createCodeTask(enrichedPrompt, *runner, *model, *mode)
	if err != nil {
		fmt.Fprintf(os.Stderr, "code: %v\n", err)
		os.Exit(1)
	}
	if err := streamCodeTask(taskID, "local"); err != nil {
		fmt.Fprintf(os.Stderr, "code stream: %v\n", err)
		os.Exit(1)
	}
}

func printCodeUsage() {
	fmt.Print(`yaver code — terminal-first coding UX

Usage:
  yaver code [flags] [prompt...]
  yaver code help

Terminal mode:
  yaver code
      Open the local Yaver terminal.

  yaver code --attach <alias|deviceId|deviceName> [--username <email>]
      Open the Yaver terminal on another machine you already own or can access.

  yaver code --attach <device> --pty [--runner codex] [runner args...]
      Raw PTY wrap of the runner's OWN TUI on the remote machine (zero yaver
      chrome, tmux-persistent). Shorthand: yaver claude|codex|opencode|glm
      [args...] --machine=<device>.

One-shot mode:
  yaver code "fix the failing tests"
  yaver code --agent codex --model gpt-5.4 "implement dark mode"
  yaver code --agent opencode --mode plan --model openai/gpt-5.4 "investigate the prod issue"

  These run against your local repo/files on this machine.

Remote one-shot mode:
  yaver code --attach mac-mini --agent opencode --model openai/gpt-5.4 "ship the onboarding fix"

  This runs against the remote machine's repo/files. Use --attach only when you
  explicitly want the remote machine as the editing workspace.

Flags:
  --attach <device>      Attach to a remote Yaver machine by device ID or name
  --username <email>     Filter remote attach targets by host email
  --agent <runner>       Alias for --runner in terminal coding mode
  --runner <runner>      Runner override: claude, codex, or opencode
  --model <model>        Model override for the chosen runner
  --mode <mode>          Runner mode override (OpenCode agent: build, plan, or a custom agent)
  --work-dir <path>      Local work dir override
  --mesh                 Run agent-graph mesh mode instead of a single terminal task
  --plain                Force plain output in mesh mode

Terminal behavior:
  - direct terminal sessions default to plain text, not markdown
  - help, tasks, agent, get agent, set agent, clear, /exit, and \exit are handled locally
  - yaver code --agent ... without --attach edits local files
  - yaver code --attach ... edits files on the attached remote machine
  - mobile/web sessions keep their own response contracts

Control plane:
  yaver code attach pc select
  yaver code set agent codex
  yaver code set byok openrouter --api-key $OPENROUTER_API_KEY --model openrouter/openai/gpt-5
  yaver code set repo <name-or-path>
  yaver code repo clone <git-url>
  yaver code dev reload
  yaver code deploy frontend
`)
}

type CodeGraphRequest struct {
	Name           string
	WorkDir        string
	Prompt         string
	Runner         string
	Model          string
	Template       string
	MaxParallel    int
	AllowedDevices []string
	AllowedRunners []string
}

func createCodeGraph(req CodeGraphRequest) (string, string, error) {
	resp, err := localAgentRequest("POST", "/agent/graphs", map[string]interface{}{
		"name":           req.Name,
		"workDir":        req.WorkDir,
		"prompt":         req.Prompt,
		"runner":         req.Runner,
		"model":          req.Model,
		"template":       req.Template,
		"maxParallel":    req.MaxParallel,
		"allowedDevices": req.AllowedDevices,
		"allowedRunners": req.AllowedRunners,
	})
	if err != nil {
		return "", "", err
	}
	runID, _ := resp["runId"].(string)
	if runID == "" {
		if run, ok := resp["run"].(map[string]interface{}); ok {
			runID, _ = run["id"].(string)
			runName, _ := run["name"].(string)
			if runID != "" {
				return runID, runName, nil
			}
		}
	}
	runName, _ := resp["name"].(string)
	if runID == "" {
		return "", "", fmt.Errorf("local agent returned no run id for graph start")
	}
	return runID, runName, nil
}

func splitCSVAllowlist(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if v := strings.TrimSpace(part); v != "" {
			out = append(out, v)
		}
	}
	return out
}

func createCodeTask(prompt, runner, model, mode string) (string, error) {
	cfg, err := LoadConfig()
	if err != nil || cfg.AuthToken == "" {
		return "", fmt.Errorf("not authenticated — run 'yaver auth'")
	}
	body, _ := json.Marshal(map[string]interface{}{
		"title":       prompt,
		"description": "",
		"runner":      runner,
		"model":       model,
		"mode":        mode,
		"source":      terminalLocalTaskSource,
	})
	req, _ := http.NewRequest("POST", "http://127.0.0.1:18080/tasks", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+cfg.AuthToken)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Yaver-Source", terminalLocalTaskSource)
	req.Header.Set("X-Yaver-Session-Mode", "terminal")

	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var result struct {
		OK     bool   `json:"ok"`
		TaskID string `json:"taskId"`
		Error  string `json:"error"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&result)
	if !result.OK || result.TaskID == "" {
		if result.Error != "" {
			return "", fmt.Errorf("%s", result.Error)
		}
		return "", fmt.Errorf("task creation failed (status %d)", resp.StatusCode)
	}
	return result.TaskID, nil
}

func runRemoteCodeAttach(prompt, attachTarget, username, runner, model, mode string) error {
	cfg, err := LoadConfig()
	if err != nil || cfg.AuthToken == "" {
		return fmt.Errorf("not authenticated — run 'yaver auth'")
	}
	device, err := resolveCodeAttachDevice(cfg, attachTarget, username)
	if err != nil {
		return err
	}
	baseURL := resolveDeviceURL(cfg, device.DeviceID, true)
	opts := TerminalClientOptions{
		DefaultRunner:      runner,
		DefaultModel:       model,
		DefaultMode:        mode,
		Source:             terminalRemoteTaskSource,
		AttachedDeviceID:   device.DeviceID,
		AttachedDeviceName: device.Name,
	}
	if strings.TrimSpace(prompt) == "" {
		ctx := context.Background()
		return RunClientHTTP(ctx, baseURL, cfg.AuthToken, opts)
	}
	client := &http.Client{Timeout: 30 * time.Second}
	authHeader := "Bearer " + cfg.AuthToken
	_, err = httpCreateTask(context.Background(), client, baseURL, authHeader, buildTerminalPromptPayload(prompt), opts)
	return err
}

func resolveCodeAttachDevice(cfg *Config, attachTarget, username string) (*DeviceInfo, error) {
	devices, err := listDevices(cfg.ConvexSiteURL, cfg.AuthToken)
	if err != nil {
		return nil, fmt.Errorf("list devices: %w", err)
	}
	target := strings.TrimSpace(strings.ToLower(attachTarget))
	wantEmail := strings.TrimSpace(strings.ToLower(username))
	var exact *DeviceInfo
	var partial *DeviceInfo
	for i := range devices {
		d := &devices[i]
		if !d.IsOnline {
			continue
		}
		if wantEmail != "" && strings.ToLower(strings.TrimSpace(d.HostEmail)) != wantEmail {
			continue
		}
		if strings.EqualFold(d.DeviceID, attachTarget) || strings.EqualFold(d.Name, attachTarget) ||
			(strings.TrimSpace(d.Alias) != "" && strings.EqualFold(d.Alias, attachTarget)) {
			exact = d
			break
		}
		if strings.HasPrefix(strings.ToLower(d.DeviceID), target) || strings.Contains(strings.ToLower(d.Name), target) {
			if partial == nil {
				partial = d
			}
		}
	}
	if exact != nil {
		return exact, nil
	}
	if partial != nil {
		return partial, nil
	}
	if wantEmail != "" {
		return nil, fmt.Errorf("no online device matched %q for host %q", attachTarget, username)
	}
	return nil, fmt.Errorf("no online device matched %q", attachTarget)
}

func streamCodeTask(taskID, label string) error {
	cfg, err := LoadConfig()
	if err != nil || cfg.AuthToken == "" {
		return fmt.Errorf("not authenticated — run 'yaver auth'")
	}
	req, _ := http.NewRequestWithContext(context.Background(), "GET", "http://127.0.0.1:18080/tasks/"+taskID+"/output", nil)
	req.Header.Set("Authorization", "Bearer "+cfg.AuthToken)
	client := &http.Client{Timeout: 30 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	fmt.Printf("[%s] task %s\n\n", label, taskID)
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
		case "done":
			fmt.Printf("\n[%s] %s\n", label, event.Status)
			return nil
		}
	}
	return scanner.Err()
}

func streamCodeGraph(runID string) error {
	type nodeSnapshot struct {
		Status string
		TaskID string
	}
	nodeState := map[string]nodeSnapshot{}
	taskOffsets := map[string]int{}

	for {
		run, err := fetchCodeGraph(runID)
		if err != nil {
			return err
		}
		for _, node := range run.Nodes {
			prev := nodeState[node.Spec.ID]
			if prev.Status != string(node.Status) {
				label := graphNodeLabel(node)
				fmt.Printf("[%s] %s -> %s\n", label, node.Spec.Title, node.Status)
				if node.Placement != nil && node.Placement.Reason != "" && node.Status == AgentNodeRunning {
					fmt.Printf("[%s] %s\n", label, node.Placement.Reason)
				}
			}
			if node.TaskID != "" {
				if err := streamGraphTaskDelta(node, taskOffsets); err != nil {
					return err
				}
			}
			nodeState[node.Spec.ID] = nodeSnapshot{Status: string(node.Status), TaskID: node.TaskID}
		}

		switch run.Status {
		case AgentGraphCompleted, AgentGraphFailed, AgentGraphStopped:
			fmt.Printf("\n[%s] %s\n", run.ID, run.Status)
			if strings.TrimSpace(run.Summary) != "" {
				fmt.Println(run.Summary)
			}
			return nil
		}
		time.Sleep(1200 * time.Millisecond)
	}
}

func fetchCodeGraph(runID string) (*AgentGraphRun, error) {
	resp, err := localAgentRequest("GET", "/agent/graphs/"+runID, nil)
	if err != nil {
		return nil, err
	}
	if run, ok := resp["run"]; ok {
		data, _ := json.Marshal(run)
		var parsed AgentGraphRun
		if err := json.Unmarshal(data, &parsed); err != nil {
			return nil, err
		}
		return &parsed, nil
	}
	data, _ := json.Marshal(resp)
	var parsed AgentGraphRun
	if err := json.Unmarshal(data, &parsed); err != nil {
		return nil, err
	}
	return &parsed, nil
}

func streamGraphTaskDelta(node *AgentGraphNodeState, offsets map[string]int) error {
	resp, err := localAgentRequest("GET", "/tasks/"+node.TaskID, nil)
	if err != nil {
		return err
	}
	taskMap, ok := resp["task"]
	if !ok {
		return nil
	}
	data, _ := json.Marshal(taskMap)
	var task TaskInfo
	if err := json.Unmarshal(data, &task); err != nil {
		return err
	}
	prev := offsets[task.ID]
	if len(task.Output) <= prev {
		return nil
	}
	label := graphNodeLabel(node)
	printPrefixedDelta(label, task.Output[prev:])
	offsets[task.ID] = len(task.Output)
	return nil
}

func graphNodeLabel(node *AgentGraphNodeState) string {
	parts := []string{}
	if node.Placement != nil {
		if name := strings.TrimSpace(node.Placement.DeviceNameOrID()); name != "" {
			parts = append(parts, name)
		}
		if runner := strings.TrimSpace(node.Placement.Runner); runner != "" {
			parts = append(parts, runner)
		}
	}
	if len(parts) == 0 {
		parts = append(parts, node.Spec.KindString())
	}
	return strings.Join(parts, "/")
}

func printPrefixedDelta(prefix, delta string) {
	if delta == "" {
		return
	}
	normalized := strings.ReplaceAll(delta, "\r\n", "\n")
	lines := strings.Split(normalized, "\n")
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		fmt.Printf("[%s] %s\n", prefix, line)
	}
}

func currentAgentWorkDir() (string, error) {
	resp, err := localAgentRequest("GET", "/agent/context", nil)
	if err != nil {
		return "", err
	}
	workDir, _ := resp["workDir"].(string)
	if workDir == "" {
		return "", fmt.Errorf("agent context did not include workDir")
	}
	return workDir, nil
}

func switchAgentWorkDir(workDir string) (func(), error) {
	abs, err := filepath.Abs(workDir)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%s is not a directory", abs)
	}
	current, err := currentAgentWorkDir()
	if err != nil {
		return nil, err
	}
	if _, err := localAgentRequest("POST", "/agent/workdir", map[string]interface{}{"workDir": abs}); err != nil {
		return nil, err
	}
	return func() {
		if current != "" && current != abs {
			_, _ = localAgentRequest("POST", "/agent/workdir", map[string]interface{}{"workDir": current})
		}
	}, nil
}

func enrichCodePrompt(prompt, workDir string) string {
	prompt = strings.TrimSpace(prompt)
	ctx := collectCodeRepoContext(workDir)
	if ctx == "" {
		return prompt
	}
	return ctx + "\n\nUser request:\n" + prompt
}

func collectCodeRepoContext(workDir string) string {
	workDir = strings.TrimSpace(workDir)
	if workDir == "" {
		return ""
	}
	if _, err := os.Stat(filepath.Join(workDir, ".git")); err != nil {
		return fmt.Sprintf("Project context:\n- Work dir: %s", workDir)
	}

	var lines []string
	lines = append(lines, "Yaver code context:")
	lines = append(lines, "- Work dir: "+workDir)
	if branch, err := gitOutput(workDir, "rev-parse", "--abbrev-ref", "HEAD"); err == nil && branch != "" {
		lines = append(lines, "- Branch: "+branch)
	}
	if remote, err := gitOutput(workDir, "remote", "get-url", "origin"); err == nil && remote != "" {
		lines = append(lines, "- Remote: "+remote)
	}
	if status, err := gitOutput(workDir, "status", "--short"); err == nil && status != "" {
		statusLines := strings.Split(status, "\n")
		if len(statusLines) > 8 {
			statusLines = statusLines[:8]
		}
		lines = append(lines, "- Dirty files:")
		for _, line := range statusLines {
			lines = append(lines, "  "+line)
		}
	}
	if commits, err := gitOutput(workDir, "log", "--oneline", "-3"); err == nil && commits != "" {
		lines = append(lines, "- Recent commits:")
		for _, line := range strings.Split(commits, "\n") {
			lines = append(lines, "  "+line)
		}
	}
	return strings.Join(lines, "\n")
}

func gitOutput(workDir string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", workDir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}
