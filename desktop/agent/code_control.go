package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"golang.org/x/term"
)

const (
	codeWorkModeLocal    = "local"
	codeWorkModeAttached = "attached"
)

type codeRunnerRow struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Ready   bool   `json:"ready"`
	Default bool   `json:"isDefault"`
	Error   string `json:"error,omitempty"`
	Warning string `json:"warning,omitempty"`
}

type codeProjectRow struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

type interactiveCodeResult struct {
	Handled    bool
	ShouldExit bool
}

func runCodeControl(args []string) (bool, error) {
	if len(args) == 0 {
		return false, nil
	}
	switch args[0] {
	case "attach":
		return true, runCodeAttachControl(args[1:])
	case "detach":
		return true, runCodeDetachControl(args[1:])
	case "auth":
		return true, runCodeAuthControl(args[1:])
	case "continue":
		return true, runCodeContinueControl(args[1:])
	case "fork":
		return true, runCodeForkControl(args[1:])
	case "sessions":
		return true, runCodeSessionsControl(args[1:])
	case "get":
		return true, runCodeGetControl(args[1:])
	case "set":
		return true, runCodeSetControl(args[1:])
	case "user":
		return true, runCodeUserControl(args[1:])
	case "repo":
		return true, runCodeRepoControl(args[1:])
	case "clone":
		if len(args) > 1 && args[1] == "repo" {
			return true, runCodeRepoClone(args[2:])
		}
	case "dev":
		return true, runCodeDevControl(args[1:])
	case "deploy":
		return true, runCodeDeployControl(args[1:])
	case "status":
		return true, runCodeStatus()
	case "help":
		printCodeControlUsage()
		return true, nil
	}
	return false, nil
}

func parseInteractiveCodeArgs(line string) ([]string, bool) {
	fields := strings.Fields(strings.TrimSpace(line))
	if len(fields) == 0 {
		return nil, false
	}
	if fields[0] == "/" {
		return []string{"/"}, true
	}
	if strings.HasPrefix(fields[0], "/") && len(fields[0]) > 1 {
		fields[0] = strings.TrimPrefix(fields[0], "/")
	}
	switch fields[0] {
	case "attach", "detach", "auth", "continue", "fork", "sessions", "get", "set", "repo", "clone", "dev", "deploy", "user", "status", "help":
		return fields, true
	default:
		return nil, false
	}
}

func slashMenuOptions(attached bool) []string {
	options := []string{
		"/auth codex",
		"/auth claude",
		"/sessions",
		"/set orchestration manual",
		"/fork <task-id> --agent opencode <prompt>",
		"/get agent",
		"/set agent codex",
		"/get model",
		"/set model gpt-5.4",
		"/get repo",
		"/repo list",
		"/repo refresh",
		"/dev status",
		"/dev reload",
		"/status",
		"/version",
		"/about",
		"/machine",
		"/help",
	}
	if attached {
		options = append([]string{"/detach pc", "/get pc"}, options...)
	} else {
		options = append([]string{"/attach pc select", "/get pc"}, options...)
	}
	return options
}

func interactiveSlashMenu(attached bool) (string, error) {
	options := slashMenuOptions(attached)
	if !term.IsTerminal(int(os.Stdin.Fd())) || !term.IsTerminal(int(os.Stdout.Fd())) {
		fmt.Println("Slash commands:")
		for _, option := range options {
			fmt.Printf("  %s\n", option)
		}
		return "", nil
	}
	oldState, err := term.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		return "", err
	}
	defer term.Restore(int(os.Stdin.Fd()), oldState)

	selected := 0
	reader := bufio.NewReader(os.Stdin)
	render := func() {
		fmt.Print("\033[2J\033[H")
		fmt.Println("Slash commands")
		fmt.Println("Use ↑/↓ and Enter. Esc cancels.")
		fmt.Println()
		for i, option := range options {
			prefix := "  "
			if i == selected {
				prefix = "> "
			}
			fmt.Printf("%s%s\n", prefix, option)
		}
	}
	render()

	for {
		b, err := reader.ReadByte()
		if err != nil {
			return "", err
		}
		switch b {
		case 13, 10:
			fmt.Print("\033[2J\033[H")
			return options[selected], nil
		case 3, 27:
			// ESC may be a standalone cancel or the start of an arrow sequence.
			if b == 27 {
				next, _ := reader.Peek(2)
				if len(next) == 2 && next[0] == '[' {
					_, _ = reader.ReadByte()
					dir, _ := reader.ReadByte()
					switch dir {
					case 'A':
						if selected > 0 {
							selected--
						}
						render()
						continue
					case 'B':
						if selected < len(options)-1 {
							selected++
						}
						render()
						continue
					}
				}
			}
			fmt.Print("\033[2J\033[H")
			return "", nil
		case 'k':
			if selected > 0 {
				selected--
			}
			render()
		case 'j':
			if selected < len(options)-1 {
				selected++
			}
			render()
		}
	}
}

func primeCodeConfigForInteractive(attachedDeviceID, attachedDeviceName string, runner, model string) error {
	cfg, profile, err := loadCodeConfig()
	if err != nil {
		return err
	}
	if strings.TrimSpace(attachedDeviceID) != "" {
		profile.WorkMode = codeWorkModeAttached
		profile.AttachedDeviceID = strings.TrimSpace(attachedDeviceID)
		profile.AttachedDeviceName = strings.TrimSpace(attachedDeviceName)
	}
	if strings.TrimSpace(runner) != "" {
		profile.Runner = strings.TrimSpace(runner)
	}
	if strings.TrimSpace(model) != "" {
		profile.Model = strings.TrimSpace(model)
	}
	return saveCodeConfig(cfg)
}

func execInteractiveCodeCommand(args []string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := exec.Command(exe, append([]string{"code"}, args...)...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	return cmd.Run()
}

func handleInteractiveCodeCommand(line, attachedDeviceID, attachedDeviceName string, runner, model string) (interactiveCodeResult, error) {
	args, ok := parseInteractiveCodeArgs(line)
	if !ok {
		return interactiveCodeResult{}, nil
	}
	if len(args) == 1 && args[0] == "/" {
		selection, err := interactiveSlashMenu(strings.TrimSpace(attachedDeviceID) != "")
		if err != nil {
			return interactiveCodeResult{Handled: true}, err
		}
		if strings.TrimSpace(selection) == "" {
			return interactiveCodeResult{Handled: true}, nil
		}
		args, ok = parseInteractiveCodeArgs(selection)
		if !ok {
			return interactiveCodeResult{Handled: true}, nil
		}
	}
	if err := primeCodeConfigForInteractive(attachedDeviceID, attachedDeviceName, runner, model); err != nil {
		return interactiveCodeResult{Handled: true}, err
	}
	switch {
	case len(args) >= 2 && args[0] == "attach" && args[1] == "pc":
		if _, err := runCodeControl(args); err != nil {
			return interactiveCodeResult{Handled: true}, err
		}
		cfg, profile, err := loadCodeConfig()
		if err != nil {
			return interactiveCodeResult{Handled: true}, err
		}
		_ = cfg
		if strings.TrimSpace(profile.AttachedDeviceID) == "" {
			return interactiveCodeResult{Handled: true}, nil
		}
		if err := runRemoteCodeAttach("", profile.AttachedDeviceID, "", runner, model); err != nil {
			return interactiveCodeResult{Handled: true}, err
		}
		return interactiveCodeResult{Handled: true, ShouldExit: true}, nil
	case len(args) >= 2 && args[0] == "detach" && args[1] == "pc":
		if _, err := runCodeControl(args); err != nil {
			return interactiveCodeResult{Handled: true}, err
		}
		return interactiveCodeResult{Handled: true, ShouldExit: strings.TrimSpace(attachedDeviceID) != ""}, nil
	default:
		return interactiveCodeResult{Handled: true}, execInteractiveCodeCommand(args)
	}
}

func printCodeControlUsage() {
	fmt.Print(`yaver code control plane

Machine:
  yaver code attach pc [<deviceId|deviceName>|select]
  yaver code detach pc
  yaver code get pc

Auth:
  yaver code auth claude
  yaver code auth codex
  yaver code auth status <session-id>
  yaver code auth submit <session-id> <code>
  yaver code auth cancel <session-id>

Sessions:
  yaver code sessions
  yaver code continue <task-id> [--agent <runner>] [--model <model>] <message>
  yaver code fork <task-id> --agent <runner> [--model <model>] <message>

Orchestration:
  yaver code set orchestration <manual|auto>
  yaver code get orchestration

Agent + models:
  yaver code set agent <runner>
  yaver code get agent
  yaver code set model <model>
  yaver code get model
  yaver code set plan-model <model>
  yaver code get plan-model
  yaver code set build-model <model>
  yaver code get build-model

Repo:
  yaver code repo clone <git-url> [--dir <path>] [--branch <branch>]
  yaver code repo list
  yaver code repo refresh
  yaver code set repo <path|name>
  yaver code get repo

Sharing:
  yaver code user invite <email> [--scope full|feedback-only|sdk-project]
  yaver code user remove <email|user-id>
  yaver code user access <email|user-id> <scope>
  yaver code user list

Dev:
  yaver code dev start [flags]
  yaver code dev stop
  yaver code dev reload
  yaver code dev status

Deploy:
  yaver code deploy mobile
  yaver code deploy backend
  yaver code deploy frontend
  yaver code deploy all

Status:
  yaver code status
`)
}

func loadCodeConfig() (*Config, *CodeCLIConfig, error) {
	cfg, err := LoadConfig()
	if err != nil {
		return nil, nil, err
	}
	if cfg.Code == nil {
		cfg.Code = &CodeCLIConfig{WorkMode: codeWorkModeLocal}
	}
	if strings.TrimSpace(cfg.Code.WorkMode) == "" {
		cfg.Code.WorkMode = codeWorkModeLocal
	}
	if strings.TrimSpace(cfg.Code.OrchestrationMode) == "" {
		cfg.Code.OrchestrationMode = "manual"
	}
	return cfg, cfg.Code, nil
}

func saveCodeConfig(cfg *Config) error {
	if cfg == nil {
		return errors.New("config required")
	}
	if cfg.Code == nil {
		cfg.Code = &CodeCLIConfig{WorkMode: codeWorkModeLocal}
	}
	return SaveConfig(cfg)
}

func codeAttachedDevice(profile *CodeCLIConfig) string {
	if profile == nil {
		return ""
	}
	if strings.TrimSpace(profile.WorkMode) == codeWorkModeAttached {
		return strings.TrimSpace(profile.AttachedDeviceID)
	}
	return ""
}

type codeBrowserAuthResponse struct {
	OK      bool                     `json:"ok"`
	Session runnerBrowserAuthSession `json:"session"`
	Error   string                   `json:"error,omitempty"`
}

func runCodeAuthControl(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: yaver code auth <claude|codex|status|submit|cancel> ...")
	}
	_, profile, err := loadCodeConfig()
	if err != nil {
		return err
	}
	deviceID := codeAttachedDevice(profile)
	switch normalizeRunnerAuthName(args[0]) {
	case "claude", "codex":
		return runCodeBrowserAuthFlow(deviceID, normalizeRunnerAuthName(args[0]))
	}
	switch args[0] {
	case "status":
		if len(args) < 2 {
			return fmt.Errorf("usage: yaver code auth status <session-id>")
		}
		sess, err := codeRunnerBrowserAuthStatus(deviceID, args[1])
		if err != nil {
			return err
		}
		printCodeBrowserAuthSession(sess)
		return nil
	case "submit":
		if len(args) < 3 {
			return fmt.Errorf("usage: yaver code auth submit <session-id> <code>")
		}
		code := strings.TrimSpace(strings.Join(args[2:], " "))
		sess, err := codeRunnerBrowserAuthSubmit(deviceID, args[1], code)
		if err != nil {
			return err
		}
		printCodeBrowserAuthSession(sess)
		return nil
	case "cancel":
		if len(args) < 2 {
			return fmt.Errorf("usage: yaver code auth cancel <session-id>")
		}
		sess, err := codeRunnerBrowserAuthCancel(deviceID, args[1])
		if err != nil {
			return err
		}
		printCodeBrowserAuthSession(sess)
		return nil
	default:
		return fmt.Errorf("usage: yaver code auth <claude|codex|status|submit|cancel> ...")
	}
}

func runCodeBrowserAuthFlow(deviceID, runner string) error {
	sess, err := codeRunnerBrowserAuthStart(deviceID, runner)
	if err != nil {
		return err
	}
	printCodeBrowserAuthSession(sess)
	if strings.EqualFold(sess.Runner, "claude") && sess.Status != "completed" {
		fmt.Println("Open the link above, finish Claude sign-in, then paste the returned code/token here.")
		fmt.Print("Auth code (leave empty to skip): ")
		reader := bufio.NewReader(os.Stdin)
		line, readErr := reader.ReadString('\n')
		if readErr != nil {
			return readErr
		}
		code := strings.TrimSpace(line)
		if code != "" {
			sess, err = codeRunnerBrowserAuthSubmit(deviceID, sess.ID, code)
			if err != nil {
				return err
			}
			printCodeBrowserAuthSession(sess)
		}
	}
	return pollCodeBrowserAuth(deviceID, sess.ID)
}

func pollCodeBrowserAuth(deviceID, sessionID string) error {
	for {
		time.Sleep(1200 * time.Millisecond)
		sess, err := codeRunnerBrowserAuthStatus(deviceID, sessionID)
		if err != nil {
			return err
		}
		printCodeBrowserAuthSession(sess)
		switch sess.Status {
		case "completed":
			return nil
		case "failed":
			return errors.New(firstNonEmpty(strings.TrimSpace(sess.Error), strings.TrimSpace(sess.Detail), "runner auth failed"))
		case "cancelled":
			return errors.New(firstNonEmpty(strings.TrimSpace(sess.Detail), "runner auth cancelled"))
		}
	}
}

func printCodeBrowserAuthSession(sess *runnerBrowserAuthSession) {
	if sess == nil {
		return
	}
	fmt.Printf("Runner auth: %s [%s] session=%s\n", sess.Runner, sess.Status, sess.ID)
	if strings.TrimSpace(sess.OpenURL) != "" {
		fmt.Printf("Open: %s\n", sess.OpenURL)
	}
	if strings.TrimSpace(sess.Code) != "" {
		fmt.Printf("Code: %s\n", sess.Code)
	}
	if strings.TrimSpace(sess.Detail) != "" {
		fmt.Printf("Detail: %s\n", sess.Detail)
	}
	if sess.AuthConfigured && strings.TrimSpace(sess.AuthSource) != "" {
		fmt.Printf("Auth: %s\n", sess.AuthSource)
	}
	fmt.Println()
}

func codeRunnerBrowserAuthStart(deviceID, runner string) (*runnerBrowserAuthSession, error) {
	var resp codeBrowserAuthResponse
	body := map[string]interface{}{"runner": runner}
	var err error
	if deviceID == "" {
		raw, reqErr := localAgentRequest("POST", "/runner-auth/browser/start", body)
		if reqErr != nil {
			return nil, reqErr
		}
		data, _ := json.Marshal(raw)
		err = json.Unmarshal(data, &resp)
	} else {
		err = remoteAgentJSONForDevice(context.Background(), deviceID, "POST", "/runner-auth/browser/start", body, &resp)
	}
	if err != nil {
		return nil, err
	}
	return &resp.Session, nil
}

func codeRunnerBrowserAuthStatus(deviceID, sessionID string) (*runnerBrowserAuthSession, error) {
	var resp codeBrowserAuthResponse
	path := "/runner-auth/browser/status?id=" + urlQueryEscape(strings.TrimSpace(sessionID))
	var err error
	if deviceID == "" {
		raw, reqErr := localAgentRequest("GET", path, nil)
		if reqErr != nil {
			return nil, reqErr
		}
		data, _ := json.Marshal(raw)
		err = json.Unmarshal(data, &resp)
	} else {
		err = remoteAgentJSONForDevice(context.Background(), deviceID, "GET", path, nil, &resp)
	}
	if err != nil {
		return nil, err
	}
	return &resp.Session, nil
}

func codeRunnerBrowserAuthSubmit(deviceID, sessionID, code string) (*runnerBrowserAuthSession, error) {
	var resp codeBrowserAuthResponse
	path := "/runner-auth/browser/submit-code?id=" + urlQueryEscape(strings.TrimSpace(sessionID))
	body := map[string]interface{}{"code": strings.TrimSpace(code)}
	var err error
	if deviceID == "" {
		raw, reqErr := localAgentRequest("POST", path, body)
		if reqErr != nil {
			return nil, reqErr
		}
		data, _ := json.Marshal(raw)
		err = json.Unmarshal(data, &resp)
	} else {
		err = remoteAgentJSONForDevice(context.Background(), deviceID, "POST", path, body, &resp)
	}
	if err != nil {
		return nil, err
	}
	return &resp.Session, nil
}

func codeRunnerBrowserAuthCancel(deviceID, sessionID string) (*runnerBrowserAuthSession, error) {
	var resp codeBrowserAuthResponse
	path := "/runner-auth/browser/cancel?id=" + urlQueryEscape(strings.TrimSpace(sessionID))
	var err error
	if deviceID == "" {
		raw, reqErr := localAgentRequest("POST", path, map[string]interface{}{})
		if reqErr != nil {
			return nil, reqErr
		}
		data, _ := json.Marshal(raw)
		err = json.Unmarshal(data, &resp)
	} else {
		err = remoteAgentJSONForDevice(context.Background(), deviceID, "POST", path, map[string]interface{}{}, &resp)
	}
	if err != nil {
		return nil, err
	}
	return &resp.Session, nil
}

func runCodeSessionsControl(args []string) error {
	if len(args) != 0 {
		return fmt.Errorf("usage: yaver code sessions")
	}
	_, profile, err := loadCodeConfig()
	if err != nil {
		return err
	}
	tasks, err := codeListTasks(codeAttachedDevice(profile))
	if err != nil {
		return err
	}
	if len(tasks) == 0 {
		fmt.Println("No sessions yet.")
		return nil
	}
	sort.Slice(tasks, func(i, j int) bool {
		return tasks[i].CreatedAt.After(tasks[j].CreatedAt)
	})
	for _, task := range tasks {
		fmt.Printf("%s  %-10s  runner=%s  session=%s  %s\n",
			task.ID,
			task.Status,
			firstNonEmpty(strings.TrimSpace(task.RunnerID), "-"),
			firstNonEmpty(strings.TrimSpace(task.SessionID), "-"),
			task.Title)
	}
	return nil
}

func runCodeContinueControl(args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("usage: yaver code continue <task-id> [--agent <runner>] [--model <model>] <message>")
	}
	taskID := strings.TrimSpace(args[0])
	runner := ""
	model := ""
	var messageParts []string
	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "--agent":
			if i+1 >= len(args) {
				return fmt.Errorf("missing value for --agent")
			}
			runner = strings.TrimSpace(args[i+1])
			i++
		case "--model":
			if i+1 >= len(args) {
				return fmt.Errorf("missing value for --model")
			}
			model = strings.TrimSpace(args[i+1])
			i++
		default:
			messageParts = append(messageParts, args[i])
		}
	}
	message := strings.TrimSpace(strings.Join(messageParts, " "))
	if taskID == "" || message == "" {
		return fmt.Errorf("usage: yaver code continue <task-id> [--agent <runner>] [--model <model>] <message>")
	}
	_, profile, err := loadCodeConfig()
	if err != nil {
		return err
	}
	payload := buildTerminalPromptPayload(message)
	task, err := codeContinueTask(codeAttachedDevice(profile), taskID, payload, runner, model)
	if err != nil {
		return err
	}
	fmt.Printf("Continued %s (runner=%s, session=%s, status=%s)\n",
		task.ID,
		firstNonEmpty(strings.TrimSpace(task.RunnerID), firstNonEmpty(strings.TrimSpace(runner), "-")),
		firstNonEmpty(strings.TrimSpace(task.SessionID), "-"),
		task.Status)
	return nil
}

func runCodeForkControl(args []string) error {
	if len(args) < 3 {
		return fmt.Errorf("usage: yaver code fork <task-id> --agent <runner> [--model <model>] <message>")
	}
	parentTaskID := strings.TrimSpace(args[0])
	runner := ""
	model := ""
	var messageParts []string
	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "--agent":
			if i+1 >= len(args) {
				return fmt.Errorf("missing value for --agent")
			}
			runner = normalizeRunnerID(args[i+1])
			i++
		case "--model":
			if i+1 >= len(args) {
				return fmt.Errorf("missing value for --model")
			}
			model = strings.TrimSpace(args[i+1])
			i++
		default:
			messageParts = append(messageParts, args[i])
		}
	}
	if parentTaskID == "" || runner == "" || len(messageParts) == 0 {
		return fmt.Errorf("usage: yaver code fork <task-id> --agent <runner> [--model <model>] <message>")
	}
	_, profile, err := loadCodeConfig()
	if err != nil {
		return err
	}
	childPrompt := strings.TrimSpace(strings.Join(messageParts, " "))
	task, err := codeForkTask(codeAttachedDevice(profile), parentTaskID, runner, model, childPrompt)
	if err != nil {
		return err
	}
	fmt.Printf("Forked child task %s from %s using %s\n", task.ID, parentTaskID, firstNonEmpty(task.RunnerID, runner))
	return nil
}

func codeListTasks(deviceID string) ([]TaskInfo, error) {
	var resp struct {
		Tasks []TaskInfo `json:"tasks"`
	}
	if deviceID == "" {
		raw, err := localAgentRequest("GET", "/tasks", nil)
		if err != nil {
			return nil, err
		}
		data, _ := json.Marshal(raw)
		if err := json.Unmarshal(data, &resp); err != nil {
			return nil, err
		}
		return resp.Tasks, nil
	}
	if err := remoteAgentJSONForDevice(context.Background(), deviceID, "GET", "/tasks", nil, &resp); err != nil {
		return nil, err
	}
	return resp.Tasks, nil
}

func codeContinueTask(deviceID, taskID string, payload terminalPromptPayload, runner, model string) (*TaskInfo, error) {
	var resp struct {
		OK     bool     `json:"ok"`
		TaskID string   `json:"taskId"`
		Status string   `json:"status"`
		Task   TaskInfo `json:"task"`
	}
	body := map[string]interface{}{
		"input":  payload.Prompt,
		"images": payload.Images,
		"runner": strings.TrimSpace(runner),
		"model":  strings.TrimSpace(model),
	}
	path := "/tasks/" + strings.TrimSpace(taskID) + "/continue"
	if deviceID == "" {
		raw, err := localAgentRequest("POST", path, body)
		if err != nil {
			return nil, err
		}
		data, _ := json.Marshal(raw)
		if err := json.Unmarshal(data, &resp); err != nil {
			return nil, err
		}
		return &TaskInfo{ID: resp.TaskID, Status: TaskStatus(resp.Status), RunnerID: strings.TrimSpace(runner)}, nil
	}
	if err := remoteAgentJSONForDevice(context.Background(), deviceID, "POST", path, body, &resp); err != nil {
		return nil, err
	}
	return &TaskInfo{ID: resp.TaskID, Status: TaskStatus(resp.Status), RunnerID: strings.TrimSpace(runner)}, nil
}

func codeForkTask(deviceID, parentTaskID, runner, model, childPrompt string) (*TaskInfo, error) {
	parent, err := codeFetchTask(deviceID, parentTaskID)
	if err != nil {
		return nil, err
	}
	ctxPrompt := renderCodeCompactContextPrompt(buildCodeCompactContext(taskFromTaskInfo(parent), 6))
	fullPrompt := strings.TrimSpace(ctxPrompt + "\n\n[Delegated child task]\n" + strings.TrimSpace(childPrompt))
	return codeCreateTask(deviceID, terminalPromptPayload{
		Prompt:       fullPrompt,
		UserEcho:     childPrompt,
		OriginalText: childPrompt,
	}, runner, model)
}

func codeFetchTask(deviceID, taskID string) (*TaskInfo, error) {
	var resp struct {
		OK   bool     `json:"ok"`
		Task TaskInfo `json:"task"`
	}
	path := "/tasks/" + strings.TrimSpace(taskID)
	if deviceID == "" {
		raw, err := localAgentRequest("GET", path, nil)
		if err != nil {
			return nil, err
		}
		data, _ := json.Marshal(raw)
		if err := json.Unmarshal(data, &resp); err != nil {
			return nil, err
		}
		return &resp.Task, nil
	}
	if err := remoteAgentJSONForDevice(context.Background(), deviceID, "GET", path, nil, &resp); err != nil {
		return nil, err
	}
	return &resp.Task, nil
}

func codeCreateTask(deviceID string, payload terminalPromptPayload, runner, model string) (*TaskInfo, error) {
	var resp struct {
		OK       bool       `json:"ok"`
		TaskID   string     `json:"taskId"`
		Status   TaskStatus `json:"status"`
		RunnerID string     `json:"runnerId"`
	}
	body := map[string]interface{}{
		"title":       payload.Prompt,
		"description": payload.Prompt,
		"userPrompt":  payload.OriginalText,
		"images":      payload.Images,
		"source":      terminalLocalTaskSource,
		"runner":      strings.TrimSpace(runner),
		"model":       strings.TrimSpace(model),
	}
	if deviceID == "" {
		raw, err := localAgentRequest("POST", "/tasks", body)
		if err != nil {
			return nil, err
		}
		data, _ := json.Marshal(raw)
		if err := json.Unmarshal(data, &resp); err != nil {
			return nil, err
		}
		return &TaskInfo{ID: resp.TaskID, Status: resp.Status, RunnerID: resp.RunnerID}, nil
	}
	if err := remoteAgentJSONForDevice(context.Background(), deviceID, "POST", "/tasks", body, &resp); err != nil {
		return nil, err
	}
	return &TaskInfo{ID: resp.TaskID, Status: resp.Status, RunnerID: resp.RunnerID}, nil
}

func taskFromTaskInfo(info *TaskInfo) *Task {
	if info == nil {
		return nil
	}
	return &Task{
		ID:          info.ID,
		Title:       info.Title,
		Description: info.Description,
		Status:      info.Status,
		RunnerID:    info.RunnerID,
		SessionID:   info.SessionID,
		ResultText:  info.ResultText,
		Turns:       info.Turns,
	}
}

func runCodeAttachControl(args []string) error {
	if len(args) == 0 || args[0] != "pc" {
		return fmt.Errorf("usage: yaver code attach pc [<deviceId|deviceName>|select]")
	}
	cfg, profile, err := loadCodeConfig()
	if err != nil {
		return err
	}
	target := ""
	if len(args) > 1 {
		target = strings.TrimSpace(args[1])
	}
	devices, err := listDevices(cfg.ConvexSiteURL, cfg.AuthToken)
	if err != nil {
		return fmt.Errorf("list devices: %w", err)
	}
	online := filterOnlineDevices(devices)
	if len(online) == 0 {
		return fmt.Errorf("no online devices found")
	}
	if target == "" || strings.EqualFold(target, "select") {
		chosen, pickErr := chooseAttachDevice(cfg, online)
		if pickErr != nil {
			return pickErr
		}
		target = chosen.DeviceID
	}
	device, err := resolveCodeAttachDevice(cfg, target, "")
	if err != nil {
		return err
	}
	profile.WorkMode = codeWorkModeAttached
	profile.AttachedDeviceID = device.DeviceID
	profile.AttachedDeviceName = device.Name
	if profile.RepoRemote {
		// keep remote repo selection
	} else {
		profile.RepoPath = ""
	}
	if err := saveCodeConfig(cfg); err != nil {
		return err
	}
	fmt.Printf("Attached code context to %s (%s)\n", device.Name, device.DeviceID[:min(8, len(device.DeviceID))])
	if summary, err := codeRunnerSummaryForDevice(device.DeviceID); err == nil && summary != "" {
		fmt.Printf("Runners: %s\n", summary)
	}
	return nil
}

func chooseAttachDevice(cfg *Config, devices []DeviceInfo) (*DeviceInfo, error) {
	printAttachDeviceList(cfg, devices)
	if !stdoutIsTTY() {
		return nil, fmt.Errorf("select a device explicitly: yaver code attach pc <deviceId>")
	}
	fmt.Print("Select machine number: ")
	reader := bufio.NewReader(os.Stdin)
	line, err := reader.ReadString('\n')
	if err != nil {
		return nil, err
	}
	line = strings.TrimSpace(line)
	if line == "" {
		return nil, fmt.Errorf("selection cancelled")
	}
	var idx int
	if _, err := fmt.Sscanf(line, "%d", &idx); err != nil || idx < 1 || idx > len(devices) {
		return nil, fmt.Errorf("invalid selection %q", line)
	}
	return &devices[idx-1], nil
}

func filterOnlineDevices(devices []DeviceInfo) []DeviceInfo {
	out := make([]DeviceInfo, 0, len(devices))
	for _, d := range devices {
		if d.IsOnline {
			out = append(out, d)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		return strings.ToLower(firstNonEmpty(out[i].Name, out[i].DeviceID)) < strings.ToLower(firstNonEmpty(out[j].Name, out[j].DeviceID))
	})
	return out
}

func printAttachDeviceList(cfg *Config, devices []DeviceInfo) {
	fmt.Println("Available machines:")
	for i, d := range devices {
		runnerSummary, err := codeRunnerSummaryForDevice(d.DeviceID)
		if err != nil || runnerSummary == "" {
			runnerSummary = "runner info unavailable"
		}
		owner := ""
		if d.HostEmail != "" {
			owner = " host=" + d.HostEmail
		}
		fmt.Printf("  %d. %s (%s, %s%s)\n", i+1, d.Name, d.DeviceID[:min(8, len(d.DeviceID))], d.Platform, owner)
		fmt.Printf("     %s\n", runnerSummary)
	}
}

func runCodeDetachControl(args []string) error {
	if len(args) == 0 || args[0] != "pc" {
		return fmt.Errorf("usage: yaver code detach pc")
	}
	cfg, profile, err := loadCodeConfig()
	if err != nil {
		return err
	}
	profile.WorkMode = codeWorkModeLocal
	profile.AttachedDeviceID = ""
	profile.AttachedDeviceName = ""
	if profile.RepoRemote {
		profile.RepoPath = ""
		profile.RepoRemote = false
	}
	if err := saveCodeConfig(cfg); err != nil {
		return err
	}
	fmt.Println("Detached from remote machine. `yaver code` is local again.")
	return nil
}

func runCodeGetControl(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: yaver code get <pc|agent|model|plan-model|build-model|repo|work-mode|orchestration>")
	}
	switch args[0] {
	case "pc":
		return runCodeGetPC()
	case "agent":
		return runCodeGetAgent()
	case "model":
		return runCodeGetModel()
	case "plan-model":
		return runCodeGetOpenCodeModel("plan")
	case "build-model":
		return runCodeGetOpenCodeModel("build")
	case "repo":
		return runCodeGetRepo()
	case "work-mode":
		return runCodeGetWorkMode()
	case "orchestration":
		return runCodeGetOrchestration()
	default:
		return fmt.Errorf("unknown get target %q", args[0])
	}
}

func runCodeSetControl(args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("usage: yaver code set <agent|model|plan-model|build-model|repo|work-mode|orchestration> ...")
	}
	switch args[0] {
	case "agent":
		return runCodeSetAgent(args[1:])
	case "model":
		return runCodeSetModel(args[1:])
	case "plan-model":
		return runCodeSetOpenCodeModel("plan", args[1:])
	case "build-model":
		return runCodeSetOpenCodeModel("build", args[1:])
	case "repo":
		return runCodeSetRepo(args[1:])
	case "work-mode":
		return runCodeSetWorkMode(args[1:])
	case "orchestration":
		return runCodeSetOrchestration(args[1:])
	default:
		return fmt.Errorf("unknown set target %q", args[0])
	}
}

func runCodeGetPC() error {
	cfg, profile, err := loadCodeConfig()
	if err != nil {
		return err
	}
	if id := codeAttachedDevice(profile); id != "" {
		fmt.Printf("Attached machine: %s (%s)\n", firstNonEmpty(profile.AttachedDeviceName, id), id)
		if summary, err := codeRunnerSummaryForDevice(id); err == nil && summary != "" {
			fmt.Printf("Runners: %s\n", summary)
		}
	} else {
		fmt.Println("Attached machine: local")
	}
	devices, err := listDevices(cfg.ConvexSiteURL, cfg.AuthToken)
	if err != nil {
		return fmt.Errorf("list devices: %w", err)
	}
	printAttachDeviceList(cfg, filterOnlineDevices(devices))
	return nil
}

func runCodeSetAgent(args []string) error {
	runner := normalizeRunnerID(strings.TrimSpace(args[0]))
	if runner == "" {
		return fmt.Errorf("runner is required")
	}
	cfg, profile, err := loadCodeConfig()
	if err != nil {
		return err
	}
	if err := codeSwitchRunner(codeAttachedDevice(profile), runner); err != nil {
		return err
	}
	profile.Runner = runner
	if err := saveCodeConfig(cfg); err != nil {
		return err
	}
	fmt.Printf("Code agent set to %s\n", runner)
	return nil
}

func runCodeGetAgent() error {
	info, _, err := codeTargetInfo()
	if err != nil {
		return err
	}
	runner, _ := info["runner"].(map[string]interface{})
	fmt.Printf("Agent: %s\n", firstNonEmpty(fmt.Sprint(runner["id"]), fmt.Sprint(runner["name"])))
	return nil
}

func runCodeSetModel(args []string) error {
	model := strings.TrimSpace(strings.Join(args, " "))
	if model == "" {
		return fmt.Errorf("model is required")
	}
	cfg, profile, err := loadCodeConfig()
	if err != nil {
		return err
	}
	profile.Model = model
	if strings.EqualFold(profile.Runner, "opencode") {
		if err := codePatchOpenCode(codeAttachedDevice(profile), map[string]string{"model": model}); err != nil {
			return err
		}
	}
	if err := saveCodeConfig(cfg); err != nil {
		return err
	}
	fmt.Printf("Model set to %s\n", model)
	return nil
}

func runCodeGetModel() error {
	_, profile, err := loadCodeConfig()
	if err != nil {
		return err
	}
	if strings.EqualFold(profile.Runner, "opencode") {
		summary, err := codeGetOpenCodeConfig(codeAttachedDevice(profile))
		if err != nil {
			return err
		}
		fmt.Printf("Model: %s\n", firstNonEmpty(summary.Model, profile.Model))
		return nil
	}
	fmt.Printf("Model: %s\n", firstNonEmpty(profile.Model, "(default)"))
	return nil
}

func runCodeSetOpenCodeModel(kind string, args []string) error {
	model := strings.TrimSpace(strings.Join(args, " "))
	if model == "" {
		return fmt.Errorf("%s-model is required", kind)
	}
	cfg, profile, err := loadCodeConfig()
	if err != nil {
		return err
	}
	if normalizeRunnerID(profile.Runner) != "opencode" {
		return fmt.Errorf("%s-model is only valid when agent=opencode", kind)
	}
	field := kind + "Model"
	if err := codePatchOpenCode(codeAttachedDevice(profile), map[string]string{field: model}); err != nil {
		return err
	}
	_ = saveCodeConfig(cfg)
	fmt.Printf("%s-model set to %s\n", kind, model)
	return nil
}

func runCodeGetOpenCodeModel(kind string) error {
	_, profile, err := loadCodeConfig()
	if err != nil {
		return err
	}
	if normalizeRunnerID(profile.Runner) != "opencode" {
		return fmt.Errorf("%s-model is only valid when agent=opencode", kind)
	}
	summary, err := codeGetOpenCodeConfig(codeAttachedDevice(profile))
	if err != nil {
		return err
	}
	value := summary.BuildModel
	if kind == "plan" {
		value = summary.PlanModel
	}
	fmt.Printf("%s-model: %s\n", kind, firstNonEmpty(value, "(default)"))
	return nil
}

func runCodeSetRepo(args []string) error {
	query := strings.TrimSpace(strings.Join(args, " "))
	if query == "" {
		return fmt.Errorf("repo query is required")
	}
	cfg, profile, err := loadCodeConfig()
	if err != nil {
		return err
	}
	deviceID := codeAttachedDevice(profile)
	var repoPath string
	if deviceID == "" {
		if abs, statErr := filepath.Abs(query); statErr == nil {
			if info, err := os.Stat(abs); err == nil && info.IsDir() {
				repoPath = abs
			}
		}
		if repoPath == "" {
			match, err := findProject(query)
			if err != nil {
				return err
			}
			repoPath = match
		}
		if err := codeSetLocalWorkDir(repoPath); err != nil {
			fmt.Printf("Local agent workdir not updated: %v\n", err)
		}
		profile.RepoPath = repoPath
		profile.RepoRemote = false
	} else {
		projects, err := codeListRemoteProjects(deviceID)
		if err != nil {
			return err
		}
		match, err := matchCodeProject(projects, query)
		if err != nil {
			return err
		}
		repoPath = match.Path
		if err := codeSetRemoteWorkDir(deviceID, repoPath); err != nil {
			return err
		}
		profile.RepoPath = repoPath
		profile.RepoRemote = true
	}
	if err := saveCodeConfig(cfg); err != nil {
		return err
	}
	fmt.Printf("Repo set to %s\n", repoPath)
	return nil
}

func runCodeGetRepo() error {
	_, profile, err := loadCodeConfig()
	if err != nil {
		return err
	}
	ctx, err := codeCurrentContext(codeAttachedDevice(profile))
	if err != nil {
		return err
	}
	workDir, _ := ctx["workDir"].(string)
	branch, _ := ctx["branch"].(string)
	location := "local"
	if profile.RepoRemote {
		location = "remote"
	}
	fmt.Printf("Repo: %s\n", firstNonEmpty(workDir, profile.RepoPath, "(none)"))
	if branch != "" {
		fmt.Printf("Branch: %s\n", branch)
	}
	fmt.Printf("Location: %s\n", location)
	return nil
}

func runCodeGetWorkMode() error {
	_, profile, err := loadCodeConfig()
	if err != nil {
		return err
	}
	fmt.Printf("Work mode: %s\n", firstNonEmpty(profile.WorkMode, codeWorkModeLocal))
	if codeAttachedDevice(profile) != "" {
		fmt.Printf("Attached machine: %s (%s)\n", firstNonEmpty(profile.AttachedDeviceName, profile.AttachedDeviceID), profile.AttachedDeviceID)
	}
	if profile.RepoPath != "" {
		location := "local"
		if profile.RepoRemote {
			location = "remote"
		}
		fmt.Printf("Repo: %s (%s)\n", profile.RepoPath, location)
	}
	return nil
}

func runCodeSetWorkMode(args []string) error {
	mode := strings.TrimSpace(args[0])
	cfg, profile, err := loadCodeConfig()
	if err != nil {
		return err
	}
	switch mode {
	case codeWorkModeLocal:
		profile.WorkMode = codeWorkModeLocal
	case codeWorkModeAttached:
		if strings.TrimSpace(profile.AttachedDeviceID) == "" {
			return fmt.Errorf("no attached machine configured; run `yaver code attach pc <device>` first")
		}
		profile.WorkMode = codeWorkModeAttached
	default:
		return fmt.Errorf("unsupported work-mode %q (supported: local, attached)", mode)
	}
	return saveCodeConfig(cfg)
}

func runCodeGetOrchestration() error {
	_, profile, err := loadCodeConfig()
	if err != nil {
		return err
	}
	fmt.Printf("Orchestration: %s\n", firstNonEmpty(strings.TrimSpace(profile.OrchestrationMode), "manual"))
	return nil
}

func runCodeSetOrchestration(args []string) error {
	mode := strings.ToLower(strings.TrimSpace(args[0]))
	if mode != "manual" && mode != "auto" {
		return fmt.Errorf("unsupported orchestration mode %q (supported: manual, auto)", mode)
	}
	cfg, profile, err := loadCodeConfig()
	if err != nil {
		return err
	}
	profile.OrchestrationMode = mode
	if err := saveCodeConfig(cfg); err != nil {
		return err
	}
	fmt.Printf("Orchestration set to %s\n", mode)
	return nil
}

func runCodeUserControl(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: yaver code user <invite|remove|access|list> ...")
	}
	switch args[0] {
	case "invite":
		runGuestsInvite(args[1:])
	case "remove":
		runGuestsRemove(args[1:])
	case "list":
		runGuestsList()
	case "access":
		if len(args) < 3 {
			return fmt.Errorf("usage: yaver code user access <email|user-id> <scope>")
		}
		runGuestsConfig([]string{args[1], "scope=" + args[2]})
	default:
		return fmt.Errorf("unknown user command %q", args[0])
	}
	return nil
}

func runCodeRepoControl(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: yaver code repo <clone|list|refresh>")
	}
	switch args[0] {
	case "clone":
		return runCodeRepoClone(args[1:])
	case "list":
		return runCodeRepoList()
	case "refresh":
		return runCodeRepoRefresh()
	default:
		return fmt.Errorf("unknown repo command %q", args[0])
	}
}

func runCodeRepoClone(args []string) error {
	fs := flag.NewFlagSet("yaver code repo clone", flag.ContinueOnError)
	dir := fs.String("dir", "", "clone parent directory")
	branch := fs.String("branch", "", "branch")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() < 1 {
		return fmt.Errorf("usage: yaver code repo clone <git-url> [--dir <path>] [--branch <branch>]")
	}
	url := strings.TrimSpace(fs.Arg(0))
	cfg, profile, err := loadCodeConfig()
	if err != nil {
		return err
	}
	deviceID := codeAttachedDevice(profile)
	body := map[string]any{"url": url}
	if strings.TrimSpace(*dir) != "" {
		body["dir"] = strings.TrimSpace(*dir)
	}
	if strings.TrimSpace(*branch) != "" {
		body["branch"] = strings.TrimSpace(*branch)
	}
	var resp map[string]any
	if deviceID == "" {
		resp, err = localAgentRequest("POST", "/repos/clone", body)
	} else {
		err = remoteAgentJSONForDevice(context.Background(), deviceID, "POST", "/repos/clone", body, &resp)
	}
	if err != nil {
		return err
	}
	path := fmt.Sprint(resp["path"])
	profile.RepoPath = strings.TrimSpace(path)
	profile.RepoRemote = deviceID != ""
	if err := saveCodeConfig(cfg); err != nil {
		return err
	}
	fmt.Printf("Cloned to %s\n", path)
	return nil
}

func runCodeRepoList() error {
	_, profile, err := loadCodeConfig()
	if err != nil {
		return err
	}
	deviceID := codeAttachedDevice(profile)
	if deviceID == "" {
		projects := listDiscoveredProjects()
		if len(projects) == 0 {
			fmt.Println("No projects found. Run `yaver code repo refresh`.")
			return nil
		}
		for _, p := range projects {
			fmt.Printf("%s\t%s\n", filepath.Base(p.Path), p.Path)
		}
		return nil
	}
	projects, err := codeListRemoteProjects(deviceID)
	if err != nil {
		return err
	}
	for _, p := range projects {
		fmt.Printf("%s\t%s\n", p.Name, p.Path)
	}
	return nil
}

func runCodeRepoRefresh() error {
	_, profile, err := loadCodeConfig()
	if err != nil {
		return err
	}
	deviceID := codeAttachedDevice(profile)
	if deviceID == "" {
		runRepoRefresh()
		return nil
	}
	var resp map[string]any
	if err := remoteAgentJSONForDevice(context.Background(), deviceID, "POST", "/projects/refresh", nil, &resp); err != nil {
		return err
	}
	fmt.Println("Remote project discovery started.")
	return nil
}

func runCodeDevControl(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: yaver code dev <start|stop|reload|status>")
	}
	_, profile, err := loadCodeConfig()
	if err != nil {
		return err
	}
	deviceID := codeAttachedDevice(profile)
	if deviceID == "" {
		runDev(args)
		return nil
	}
	switch args[0] {
	case "start":
		return runCodeRemoteDevStart(deviceID, profile, args[1:])
	case "stop":
		return remoteAgentJSONForDevice(context.Background(), deviceID, "POST", "/dev/stop", nil, nil)
	case "reload":
		return remoteAgentJSONForDevice(context.Background(), deviceID, "POST", "/dev/reload", nil, nil)
	case "status":
		var out map[string]any
		if err := remoteAgentJSONForDevice(context.Background(), deviceID, "GET", "/dev/status", nil, &out); err != nil {
			return err
		}
		pretty, _ := json.MarshalIndent(out, "", "  ")
		fmt.Println(string(pretty))
		return nil
	default:
		return fmt.Errorf("unknown dev command %q", args[0])
	}
}

func runCodeRemoteDevStart(deviceID string, profile *CodeCLIConfig, args []string) error {
	fs := flag.NewFlagSet("yaver code dev start", flag.ContinueOnError)
	framework := fs.String("framework", "", "framework")
	port := fs.Int("port", 0, "port")
	platform := fs.String("platform", "ios", "platform")
	workDir := fs.String("dir", strings.TrimSpace(profile.RepoPath), "project directory")
	if err := fs.Parse(args); err != nil {
		return err
	}
	body := map[string]any{
		"framework": *framework,
		"workDir":   *workDir,
		"platform":  *platform,
		"port":      *port,
	}
	var out map[string]any
	if err := remoteAgentJSONForDevice(context.Background(), deviceID, "POST", "/dev/start", body, &out); err != nil {
		return err
	}
	pretty, _ := json.MarshalIndent(out, "", "  ")
	fmt.Println(string(pretty))
	return nil
}

func runCodeDeployControl(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: yaver code deploy <mobile|backend|frontend|all>")
	}
	_, profile, err := loadCodeConfig()
	if err != nil {
		return err
	}
	var targets []string
	switch args[0] {
	case "mobile":
		targets = []string{"testflight", "playstore"}
	case "backend":
		targets = []string{"convex"}
	case "frontend":
		targets = []string{"cloudflare"}
	case "all":
		targets = []string{"testflight", "playstore", "convex", "cloudflare"}
	default:
		return fmt.Errorf("unknown deploy surface %q", args[0])
	}
	app, err := codeCurrentAppName(codeAttachedDevice(profile), profile)
	if err != nil {
		return err
	}
	cfg, err := LoadConfig()
	if err != nil {
		return err
	}
	exitCode := shipToAgent(cfg, app, targets, "", "", 0, codeAttachedDevice(profile))
	if exitCode != 0 {
		return fmt.Errorf("deploy failed with exit %d", exitCode)
	}
	return nil
}

func runCodeStatus() error {
	if err := runCodeGetWorkMode(); err != nil {
		return err
	}
	if err := runCodeGetAgent(); err != nil {
		return err
	}
	if err := runCodeGetModel(); err != nil {
		return err
	}
	if err := runCodeGetRepo(); err != nil {
		return err
	}
	return nil
}

func codeSwitchRunner(deviceID, runner string) error {
	body := map[string]interface{}{"runnerId": runner}
	if deviceID == "" {
		_, err := localAgentRequest("POST", "/agent/runner/switch", body)
		return err
	}
	return remoteAgentJSONForDevice(context.Background(), deviceID, "POST", "/agent/runner/switch", body, nil)
}

func codeTargetInfo() (map[string]interface{}, string, error) {
	_, profile, err := loadCodeConfig()
	if err != nil {
		return nil, "", err
	}
	if deviceID := codeAttachedDevice(profile); deviceID != "" {
		candidates, token, err := resolveRemoteAgentCandidates(deviceID)
		if err != nil {
			return nil, "", err
		}
		_, _, raw, err := doRemoteAgentRequest(context.Background(), candidates, token, "GET", "/info", nil, 20*time.Second)
		if err != nil {
			return nil, "", err
		}
		var out map[string]interface{}
		if err := json.Unmarshal(raw, &out); err != nil {
			return nil, "", err
		}
		return out, deviceID, nil
	}
	resp, err := localAgentRequest("GET", "/info", nil)
	return resp, "", err
}

func codeCurrentContext(deviceID string) (map[string]interface{}, error) {
	var out map[string]interface{}
	if deviceID == "" {
		return localAgentRequest("GET", "/agent/context", nil)
	}
	err := remoteAgentJSONForDevice(context.Background(), deviceID, "GET", "/agent/context", nil, &out)
	return out, err
}

func codeSetLocalWorkDir(path string) error {
	_, err := localAgentRequest("POST", "/agent/workdir", map[string]interface{}{"workDir": path})
	return err
}

func codeSetRemoteWorkDir(deviceID, path string) error {
	return remoteAgentJSONForDevice(context.Background(), deviceID, "POST", "/agent/workdir", map[string]interface{}{"workDir": path}, nil)
}

func codeListRemoteProjects(deviceID string) ([]codeProjectRow, error) {
	var resp struct {
		Projects []codeProjectRow `json:"projects"`
	}
	if err := remoteAgentJSONForDevice(context.Background(), deviceID, "GET", "/projects", nil, &resp); err != nil {
		return nil, err
	}
	return resp.Projects, nil
}

func matchCodeProject(projects []codeProjectRow, query string) (*codeProjectRow, error) {
	query = strings.ToLower(strings.TrimSpace(query))
	var partial *codeProjectRow
	for i := range projects {
		p := &projects[i]
		if strings.EqualFold(p.Path, query) || strings.EqualFold(p.Name, query) {
			return p, nil
		}
		if strings.Contains(strings.ToLower(p.Path), query) || strings.Contains(strings.ToLower(p.Name), query) {
			if partial == nil {
				partial = p
			}
		}
	}
	if partial != nil {
		return partial, nil
	}
	return nil, fmt.Errorf("no project matched %q", query)
}

func codeRunnerSummaryForDevice(deviceID string) (string, error) {
	var runners struct {
		Runners []codeRunnerRow `json:"runners"`
	}
	if err := remoteAgentJSONForDevice(context.Background(), deviceID, "GET", "/agent/runners", nil, &runners); err != nil {
		return "", err
	}
	parts := make([]string, 0, len(runners.Runners))
	for _, row := range runners.Runners {
		state := "not-ready"
		if row.Ready {
			state = "ready"
		}
		label := normalizeRunnerID(row.ID) + " " + state
		if row.Default {
			label += " [default]"
		}
		parts = append(parts, label)
	}
	return strings.Join(parts, ", "), nil
}

func codeGetOpenCodeConfig(deviceID string) (*OpenCodeConfigSummary, error) {
	var resp struct {
		Config OpenCodeConfigSummary `json:"config"`
	}
	if deviceID == "" {
		raw, err := localAgentRequest("GET", "/runner/opencode/config", nil)
		if err != nil {
			return nil, err
		}
		data, _ := json.Marshal(raw)
		if err := json.Unmarshal(data, &resp); err != nil {
			return nil, err
		}
		return &resp.Config, nil
	}
	if err := remoteAgentJSONForDevice(context.Background(), deviceID, "GET", "/runner/opencode/config", nil, &resp); err != nil {
		return nil, err
	}
	return &resp.Config, nil
}

func codePatchOpenCode(deviceID string, patch map[string]string) error {
	body := map[string]interface{}{}
	for k, v := range patch {
		body[k] = v
	}
	if deviceID == "" {
		_, err := localAgentRequest("POST", "/runner/opencode/config", body)
		return err
	}
	return remoteAgentJSONForDevice(context.Background(), deviceID, "POST", "/runner/opencode/config", body, nil)
}

func codeCurrentAppName(deviceID string, profile *CodeCLIConfig) (string, error) {
	ctx, err := codeCurrentContext(deviceID)
	if err == nil {
		if workDir, _ := ctx["workDir"].(string); workDir != "" {
			return filepath.Base(workDir), nil
		}
	}
	if profile != nil && strings.TrimSpace(profile.RepoPath) != "" {
		return filepath.Base(profile.RepoPath), nil
	}
	return "", fmt.Errorf("could not determine current app name; set repo first")
}
