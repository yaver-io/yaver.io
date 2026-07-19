package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

// runAttach connects to the running yaver agent and provides an interactive
// terminal UI. Shows task output from all tasks (mobile or local), and
// accepts keyboard input to create new tasks.
func runAttach(args []string) {
	fs := flag.NewFlagSet("attach", flag.ExitOnError)
	runner := fs.String("runner", "", "runner ID override for new terminal tasks")
	agent := fs.String("agent", "", "alias for --runner")
	model := fs.String("model", "", "model override for new terminal tasks")
	mode := fs.String("mode", "", "runner mode override for new terminal tasks")
	machine := fs.String("machine", "", "reattach to a live runner session on a remote device (alias/id/name)")
	chrome := fs.Bool("chrome", false, "with --machine: keep the remote tmux status bar visible")
	_ = fs.Parse(args)
	if strings.TrimSpace(*agent) != "" && strings.TrimSpace(*runner) == "" {
		*runner = normalizeRunnerID(*agent)
	}

	// Remote reattach: `yaver attach --machine=<dev> [--agent=codex]` finds
	// live runner PTY sessions on the box. One match → reattach; several →
	// picker; none → hint. Mirrors `yaver codex --machine` transport.
	if strings.TrimSpace(*machine) != "" {
		if err := runRemoteAttachPicker(*machine, normalizeRunnerID(strings.TrimSpace(*runner)), *chrome); err != nil {
			fmt.Fprintf(os.Stderr, "attach: %v\n", err)
			os.Exit(1)
		}
		return
	}
	opts := attachSessionOptions{
		Source:        terminalLocalTaskSource,
		DefaultRunner: strings.TrimSpace(*runner),
		DefaultModel:  strings.TrimSpace(*model),
		DefaultMode:   strings.TrimSpace(*mode),
	}

	cfg, err := LoadConfig()
	if err != nil || cfg.AuthToken == "" {
		fmt.Fprintln(os.Stderr, "Not signed in. Run 'yaver auth' first.")
		os.Exit(1)
	}

	// Check if agent is running
	pid, running := isAgentRunning()
	if !running {
		fmt.Fprintln(os.Stderr, "Agent is not running. Run 'yaver serve' or 'yaver auth' first.")
		os.Exit(1)
	}

	baseURL := "http://127.0.0.1:18080"

	// Verify connection
	info, err := attachGetInfo(baseURL, cfg.AuthToken)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Cannot connect to agent (PID %d): %v\n", pid, err)
		os.Exit(1)
	}

	printAttachWelcome(info)

	// Track known tasks to detect new ones
	knownTasks := make(map[string]bool)
	lastOutputLen := make(map[string]int)
	taskBases := make(map[string]string)
	taskHeaders := make(map[string]map[string]string)

	// Initial task fetch — populate known tasks
	if tasks, err := attachListTasks(baseURL, cfg.AuthToken); err == nil {
		for _, t := range tasks {
			knownTasks[t.ID] = true
			lastOutputLen[t.ID] = len(t.Output)
		}
	}

	// Signal handler
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	// Input channel — read lines from stdin
	inputCh := make(chan string)
	go func() {
		scanner := bufio.NewScanner(os.Stdin)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line != "" {
				inputCh <- line
			}
		}
	}()

	// Poll ticker
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	// Track which task we're actively streaming
	activeTask := ""
	sessionTask := ""

	printPrompt := func() {
		if activeTask == "" {
			workDir := ""
			if info != nil {
				workDir = strings.TrimSpace(info.WorkDir)
			}
			printInteractivePrompt(workDir, opts.DefaultRunner, opts.DefaultModel, opts.DefaultMode)
		}
	}

	printPrompt()

	for {
		select {
		case <-sigCh:
			fmt.Println("\n\nDetached from agent. Agent continues running in background.")
			return

		case input := <-inputCh:
			if result, err := handleInteractiveCodeCommand(input, "", "", opts.DefaultRunner, opts.DefaultModel); result.Handled {
				if err != nil {
					fmt.Printf("Error: %v\n\n", err)
					printPrompt()
					continue
				}
				if result.ShouldExit {
					fmt.Println("\nDetached from agent. Agent continues running in background.")
					return
				}
				if cfg, profile, loadErr := loadCodeConfig(); loadErr == nil && cfg != nil && profile != nil {
					opts.DefaultRunner = strings.TrimSpace(profile.Runner)
					opts.DefaultModel = strings.TrimSpace(profile.Model)
					opts.DefaultMode = strings.TrimSpace(profile.Mode)
					if info != nil {
						info.Runner.ID = opts.DefaultRunner
						info.Runner.Name = opts.DefaultRunner
						info.Runner.Model = opts.DefaultModel
						info.Runner.Mode = opts.DefaultMode
					}
				}
				printPrompt()
				continue
			}
			if handled, shouldExit := runAttachBuiltin(input, info, baseURL, cfg.AuthToken, &opts); handled {
				if shouldExit {
					fmt.Println("\nDetached from agent. Agent continues running in background.")
					return
				}
				printPrompt()
				continue
			}
			payload := buildTerminalPromptPayload(input)
			// Create a new task from keyboard input
			printTerminalUserInput(payload)
			taskID := ""
			if sessionTask != "" && activeTask == "" {
				task, contErr := codeContinueTask("", sessionTask, payload, opts.DefaultRunner, opts.DefaultModel, opts.DefaultMode)
				if contErr != nil {
					fmt.Printf("\033[31mError: %v\033[0m\n", contErr)
					printPrompt()
					continue
				}
				taskID = task.ID
			} else {
				ref, createErr := attachCreateTask(baseURL, cfg.AuthToken, payload, opts)
				err = createErr
				if err != nil {
					fmt.Printf("\033[31mError: %v\033[0m\n", err)
					printPrompt()
					continue
				}
				taskID = ref.ID
				if ref.BaseURL != "" && ref.BaseURL != baseURL {
					taskBases[taskID] = ref.BaseURL
					taskHeaders[taskID] = ref.Headers
				}
			}
			knownTasks[taskID] = true
			lastOutputLen[taskID] = 0
			activeTask = taskID
			sessionTask = taskID

		case <-ticker.C:
			// Poll for task updates
			tasks, err := attachListTasks(baseURL, cfg.AuthToken)
			if err != nil {
				continue
			}
			if activeTask != "" {
				if remoteBase := strings.TrimSpace(taskBases[activeTask]); remoteBase != "" {
					if remoteTask, remoteErr := attachGetTask(remoteBase, cfg.AuthToken, activeTask, taskHeaders[activeTask]); remoteErr == nil {
						tasks = append(tasks, remoteTask)
					}
				}
			}

			for _, t := range tasks {
				// Detect new tasks from mobile
				if !knownTasks[t.ID] {
					knownTasks[t.ID] = true
					lastOutputLen[t.ID] = 0
					fmt.Printf("\n\033[1;33m📱 [mobile] %s\033[0m\n\n", t.Title)
					activeTask = t.ID
					sessionTask = t.ID
				}

				// Stream new output
				prevLen := lastOutputLen[t.ID]
				if len(t.Output) > prevLen {
					newOutput := t.Output[prevLen:]
					fmt.Print(newOutput)
					lastOutputLen[t.ID] = len(t.Output)
				}

				// Task finished
				if (t.Status == "completed" || t.Status == "failed" || t.Status == "stopped") && activeTask == t.ID {
					// Show result if we haven't already via output
					if t.ResultText != "" && len(t.Output) == 0 {
						fmt.Printf("\n%s\n", t.ResultText)
					}
					if t.Status == "failed" {
						fmt.Printf("\n\033[31m✗ Task failed\033[0m\n")
					} else if t.Status == "completed" {
						if t.CostUSD > 0 {
							fmt.Printf("\n\033[2m($%.4f)\033[0m\n", t.CostUSD)
						}
					}
					fmt.Println()
					activeTask = ""
					if strings.TrimSpace(taskBases[t.ID]) != "" {
						sessionTask = ""
					}
					printPrompt()
				}
			}
		}
	}
}

// --- HTTP helpers for attach mode ---

type attachRunnerInfo struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Model string `json:"model,omitempty"`
	Mode  string `json:"mode,omitempty"`
}

type attachInfo struct {
	Hostname string           `json:"hostname"`
	Version  string           `json:"version"`
	WorkDir  string           `json:"workDir"`
	Runner   attachRunnerInfo `json:"runner"`
}

type attachSessionOptions struct {
	Source        string
	DefaultRunner string
	DefaultModel  string
	DefaultMode   string
}

type attachTaskRef struct {
	ID      string
	BaseURL string
	Headers map[string]string
}

func attachGetInfo(baseURL, token string) (*attachInfo, error) {
	req, _ := http.NewRequest("GET", baseURL+"/info", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("status %d", resp.StatusCode)
	}
	var info attachInfo
	json.NewDecoder(resp.Body).Decode(&info)
	return &info, nil
}

func attachListTasks(baseURL, token string) ([]TaskInfo, error) {
	req, _ := http.NewRequest("GET", baseURL+"/tasks", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("status %d", resp.StatusCode)
	}
	var data struct {
		Tasks []TaskInfo `json:"tasks"`
	}
	body, _ := io.ReadAll(resp.Body)
	json.Unmarshal(body, &data)
	return data.Tasks, nil
}

func attachGetTask(baseURL, token, taskID string, headers map[string]string) (TaskInfo, error) {
	req, _ := http.NewRequest("GET", strings.TrimRight(baseURL, "/")+"/tasks/"+taskID, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	for k, v := range headers {
		if strings.TrimSpace(v) != "" {
			req.Header.Set(k, v)
		}
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return TaskInfo{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return TaskInfo{}, fmt.Errorf("status %d", resp.StatusCode)
	}
	var data struct {
		Task TaskInfo `json:"task"`
	}
	body, _ := io.ReadAll(resp.Body)
	if err := json.Unmarshal(body, &data); err != nil {
		return TaskInfo{}, err
	}
	return data.Task, nil
}

func attachCreateTask(baseURL, token string, prompt terminalPromptPayload, opts attachSessionOptions) (*attachTaskRef, error) {
	source := strings.TrimSpace(opts.Source)
	if source == "" {
		source = terminalLocalTaskSource
	}
	bodyPayload := map[string]interface{}{
		"title":       prompt.Prompt,
		"description": prompt.Prompt,
		"userPrompt":  prompt.OriginalText,
		"source":      source,
	}
	if strings.TrimSpace(opts.DefaultRunner) != "" {
		bodyPayload["runner"] = opts.DefaultRunner
	}
	if strings.TrimSpace(opts.DefaultModel) != "" {
		bodyPayload["model"] = opts.DefaultModel
	}
	if strings.TrimSpace(opts.DefaultMode) != "" {
		bodyPayload["mode"] = opts.DefaultMode
	}
	if len(prompt.Images) > 0 {
		bodyPayload["images"] = prompt.Images
	}
	body, _ := json.Marshal(bodyPayload)
	req, _ := http.NewRequest("POST", baseURL+"/tasks", strings.NewReader(string(body)))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Yaver-Source", source)
	req.Header.Set("X-Yaver-Session-Mode", "terminal")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if err := decodeCloudWorkspaceRequiredError(resp.StatusCode, respBody); err != nil {
		if cloudErr, ok := err.(*CloudWorkspaceRequiredError); ok {
			fmt.Printf("[cloud] workspace selected; waiting for %s (pending %s)\n", attachCloudTargetLabel(cloudErr), firstNonEmpty(cloudErr.PendingTaskID, "n/a"))
			chosen, result, handoffErr := createTaskOnCloudWorkspace(context.Background(), cloudErr, "Bearer "+token, body, 60*time.Second, newTerminalCloudHandoffProgressPrinter())
			if handoffErr != nil {
				return nil, handoffErr
			}
			fmt.Printf("[task %s] created on %s\n", result.TaskID, firstNonEmpty(chosen.Label, chosen.DeviceID, "cloud workspace"))
			return &attachTaskRef{ID: result.TaskID, BaseURL: strings.TrimRight(chosen.BaseURL, "/"), Headers: chosen.Headers}, nil
		}
		return nil, err
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		var errData struct {
			Error string `json:"error"`
		}
		json.Unmarshal(respBody, &errData)
		if errData.Error != "" {
			return nil, fmt.Errorf("%s", errData.Error)
		}
		return nil, fmt.Errorf("status %d", resp.StatusCode)
	}
	var data struct {
		TaskID string `json:"taskId"`
	}
	json.Unmarshal(respBody, &data)
	return &attachTaskRef{ID: data.TaskID, BaseURL: baseURL}, nil
}

func attachCloudTargetLabel(err *CloudWorkspaceRequiredError) string {
	if err != nil && err.Placement != nil && strings.TrimSpace(err.Placement.TargetDeviceID) != "" {
		return strings.TrimSpace(err.Placement.TargetDeviceID)
	}
	return "assigned workspace"
}

func truncateStr(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return "..." + s[len(s)-max+3:]
}

func runAttachBuiltin(input string, info *attachInfo, baseURL, token string, opts *attachSessionOptions) (handled bool, shouldExit bool) {
	cmd, ok := parseTerminalCommand(input)
	if !ok {
		return false, false
	}

	switch cmd.Kind {
	case "help":
		printAttachHelp(info)
		return true, false
	case "tasks":
		tasks, err := attachListTasks(baseURL, token)
		if err != nil {
			fmt.Printf("Error: %v\n\n", err)
			return true, false
		}
		if len(tasks) == 0 {
			fmt.Println("No tasks yet.")
			fmt.Println()
			return true, false
		}
		for _, t := range tasks {
			runner := t.RunnerID
			if runner == "" {
				runner = "-"
			}
			fmt.Printf("%-8s  %-10s  %-10s  %s\n", t.ID, t.Status, runner, t.Title)
		}
		fmt.Println()
		return true, false
	case "cloud-pending":
		fmt.Println(renderPendingCloudTaskDispatchStatus(time.Now()))
		fmt.Println()
		return true, false
	case "agent":
		if runnerLine := attachRunnerLine(info); runnerLine != "" {
			fmt.Printf("Current coding agent: %s\n", runnerLine)
			fmt.Println()
		} else {
			fmt.Println("Current coding agent: unknown")
			fmt.Println()
		}
		return true, false
	case "set-agent":
		if opts != nil {
			opts.DefaultRunner = strings.TrimSpace(cmd.Runner)
			opts.DefaultModel = strings.TrimSpace(cmd.Model)
		}
		if info != nil {
			info.Runner.ID = strings.TrimSpace(cmd.Runner)
			info.Runner.Name = strings.TrimSpace(cmd.Runner)
			info.Runner.Model = strings.TrimSpace(cmd.Model)
		}
		if runnerLine := attachRunnerLine(info); runnerLine != "" {
			fmt.Printf("Default coding agent set to: %s\n", runnerLine)
		} else {
			fmt.Printf("Default coding agent set to: %s\n", strings.TrimSpace(cmd.Runner))
		}
		fmt.Println()
		return true, false
	case "clear":
		fmt.Print("\033[2J\033[H")
		printAttachWelcome(info)
		return true, false
	case "stop-task":
		if err := attachPostTaskAction(baseURL, token, cmd.TaskID, "stop", nil); err != nil {
			fmt.Printf("Error: %v\n\n", err)
			return true, false
		}
		fmt.Printf("Stopped task %s.\n", cmd.TaskID)
		fmt.Println()
		return true, false
	case "exit-task":
		if err := attachPostTaskAction(baseURL, token, cmd.TaskID, "exit", nil); err != nil {
			fmt.Printf("Error: %v\n\n", err)
			return true, false
		}
		fmt.Printf("Gracefully exited task %s.\n", cmd.TaskID)
		fmt.Println()
		return true, false
	case "continue-task":
		body := fmt.Sprintf(`{"input":%q}`, cmd.Input)
		if err := attachPostTaskAction(baseURL, token, cmd.TaskID, "continue", strings.NewReader(body)); err != nil {
			fmt.Printf("Error: %v\n\n", err)
			return true, false
		}
		fmt.Printf("Resumed task %s.\n", cmd.TaskID)
		fmt.Println()
		return true, false
	case "phone-status":
		// Resolve workdir: prefer attachInfo.WorkDir (the remote agent
		// reports the cwd it was started in), fall back to local cwd
		// so /phone status still works in the local-only path.
		workDir := ""
		if info != nil {
			workDir = strings.TrimSpace(info.WorkDir)
		}
		out, err := renderPhoneStatus(context.Background(), workDir)
		if err != nil {
			fmt.Printf("phone status error: %v\n\n", err)
			return true, false
		}
		fmt.Println(out)
		fmt.Println()
		return true, false
	case "detach":
		return true, true
	default:
		return false, false
	}
}

func attachPostTaskAction(baseURL, token, taskID, action string, body io.Reader) error {
	req, _ := http.NewRequest("POST", baseURL+"/tasks/"+taskID+"/"+action, body)
	req.Header.Set("Authorization", "Bearer "+token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		var errData struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(respBody, &errData)
		if strings.TrimSpace(errData.Error) != "" {
			return fmt.Errorf("%s", errData.Error)
		}
		return fmt.Errorf("status %d", resp.StatusCode)
	}
	return nil
}
