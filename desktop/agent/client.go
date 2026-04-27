package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/quic-go/quic-go"
)

type TerminalClientOptions struct {
	DefaultRunner      string
	DefaultModel       string
	Source             string
	AttachedDeviceID   string
	AttachedDeviceName string
}

// RunClient connects to a remote Yaver agent over QUIC and provides an
// interactive terminal to submit tasks and stream output.
func RunClient(ctx context.Context, host string, port int, token string, opts TerminalClientOptions) error {
	addr := fmt.Sprintf("%s:%d", host, port)
	log.Printf("Connecting to %s...", addr)

	tlsCfg := &tls.Config{
		InsecureSkipVerify: true, // Self-signed cert on agent
		NextProtos:         []string{"yaver-p2p"},
	}

	conn, err := quic.DialAddr(ctx, addr, tlsCfg, &quic.Config{
		MaxIdleTimeout:  60 * time.Second,
		KeepAlivePeriod: 15 * time.Second,
	})
	if err != nil {
		return fmt.Errorf("connect to %s: %w", addr, err)
	}
	defer conn.CloseWithError(0, "bye")

	// Authenticate
	deviceName, err := clientAuth(ctx, conn, token)
	if err != nil {
		return fmt.Errorf("authentication failed: %w", err)
	}
	printAttachWelcome(&attachInfo{Hostname: deviceName, Runner: attachRunnerInfo{ID: opts.DefaultRunner, Model: opts.DefaultModel}})

	// Load speech config for voice commands
	clientCfg, _ := LoadConfig()
	var speechCfg *SpeechConfig
	if clientCfg != nil {
		speechCfg = clientCfg.Speech
	}

	// Interactive loop
	reader := bufio.NewReader(os.Stdin)

	for {
		fmt.Print("yaver> ")
		line, err := reader.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				fmt.Println()
				return nil
			}
			return fmt.Errorf("read input: %w", err)
		}

		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		if result, err := handleInteractiveCodeCommand(line, strings.TrimSpace(opts.AttachedDeviceID), firstNonEmpty(strings.TrimSpace(opts.AttachedDeviceName), deviceName), opts.DefaultRunner, opts.DefaultModel); result.Handled {
			if err != nil {
				fmt.Printf("error: %v\n", err)
				continue
			}
			if result.ShouldExit {
				return nil
			}
			if cfg, profile, loadErr := loadCodeConfig(); loadErr == nil && cfg != nil && profile != nil {
				opts.DefaultRunner = strings.TrimSpace(profile.Runner)
				opts.DefaultModel = strings.TrimSpace(profile.Model)
			}
			continue
		}

		// Built-in commands
		if cmd, ok := parseTerminalCommand(line); ok {
			switch cmd.Kind {
			case "detach":
				return nil
			case "help":
				printAttachHelp(&attachInfo{Hostname: deviceName, Runner: attachRunnerInfo{ID: opts.DefaultRunner, Model: opts.DefaultModel}})
				continue
			case "tasks":
				if err := clientListTasks(ctx, conn); err != nil {
					fmt.Printf("error: %v\n", err)
				}
				continue
			case "agent":
				if strings.TrimSpace(opts.DefaultRunner) != "" || strings.TrimSpace(opts.DefaultModel) != "" {
					fmt.Printf("Current coding agent: %s\n", attachRunnerLine(&attachInfo{Runner: attachRunnerInfo{ID: opts.DefaultRunner, Model: opts.DefaultModel}}))
					fmt.Println()
				} else {
					fmt.Println("Current coding agent: remote default")
					fmt.Println()
				}
				continue
			case "set-agent":
				opts.DefaultRunner = strings.TrimSpace(cmd.Runner)
				opts.DefaultModel = strings.TrimSpace(cmd.Model)
				info := &attachInfo{Runner: attachRunnerInfo{ID: opts.DefaultRunner, Name: opts.DefaultRunner, Model: opts.DefaultModel}}
				fmt.Printf("Default coding agent set to: %s\n\n", attachRunnerLine(info))
				continue
			case "clear":
				fmt.Print("\033[2J\033[H")
				printAttachWelcome(&attachInfo{Hostname: deviceName, Runner: attachRunnerInfo{ID: opts.DefaultRunner, Model: opts.DefaultModel}})
				continue
			case "stop-task":
				if err := clientStopTask(ctx, conn, cmd.TaskID); err != nil {
					fmt.Printf("error: %v\n", err)
				}
				continue
			case "continue-task":
				if err := clientContinueTask(ctx, conn, cmd.TaskID, cmd.Input); err != nil {
					fmt.Printf("error: %v\n", err)
				}
				continue
			case "exit-task":
				fmt.Println("graceful task exit is only available over the HTTP terminal path")
				continue
			}
		}
		switch {
		case line == "voice" || line == "/voice":
			// Record and transcribe voice input
			if speechCfg == nil || speechCfg.Provider == "" {
				fmt.Println("Speech not configured. Run: yaver config set speech.provider <whisper|openai|deepgram|assemblyai>")
				continue
			}
			audioPath, err := RecordAudio("")
			if err != nil {
				fmt.Printf("Recording error: %v\n", err)
				continue
			}
			defer os.Remove(audioPath)
			fmt.Print("Transcribing... ")
			text, err := TranscribeAudio(audioPath, speechCfg)
			if err != nil {
				fmt.Printf("\nTranscription error: %v\n", err)
				continue
			}
			fmt.Printf("\n> %s\n", text)
			os.Remove(audioPath)
			if text == "" {
				fmt.Println("(empty transcription, skipping)")
				continue
			}
			if err := clientCreateTask(ctx, conn, text, opts); err != nil {
				fmt.Printf("error: %v\n", err)
			}
			continue
		}

		// Default: create a new task
		fmt.Printf("⟩ %s\n\n", summarizeTerminalInputEcho(line))
		if err := clientCreateTask(ctx, conn, line, opts); err != nil {
			fmt.Printf("error: %v\n", err)
		}
	}
}

// clientAuth sends an auth message and waits for auth_ok.
func clientAuth(ctx context.Context, conn quic.Connection, token string) (string, error) {
	msg := IncomingMessage{Type: "auth", Token: token}
	resp, err := clientRPC(ctx, conn, msg)
	if err != nil {
		return "", err
	}
	if resp.Type == "error" {
		return "", fmt.Errorf("%s", resp.Message)
	}
	return resp.DeviceName, nil
}

// clientCreateTask sends a task and streams the output.
func clientCreateTask(ctx context.Context, conn quic.Connection, prompt string, opts TerminalClientOptions) error {
	stream, err := conn.OpenStreamSync(ctx)
	if err != nil {
		return fmt.Errorf("open stream: %w", err)
	}

	msg := IncomingMessage{
		Type:        "task_create",
		Title:       prompt,
		Description: prompt,
		Source:      firstNonEmpty(opts.Source, terminalRemoteTaskSource),
		Runner:      opts.DefaultRunner,
		Model:       opts.DefaultModel,
	}

	data, _ := json.Marshal(msg)
	stream.Write(data)
	stream.Close() // signal we're done writing

	// Read streamed output
	scanner := bufio.NewScanner(stream)
	scanner.Buffer(make([]byte, 1<<20), 1<<20)

	for scanner.Scan() {
		var resp OutgoingMessage
		if err := json.Unmarshal(scanner.Bytes(), &resp); err != nil {
			continue
		}

		switch resp.Type {
		case "task_created":
			fmt.Printf("[task %s] created\n", resp.TaskID)
		case "task_output":
			if resp.Text != "" {
				fmt.Print(resp.Text)
			}
			if resp.Final {
				fmt.Println()
				return nil
			}
		case "error":
			return fmt.Errorf("%s", resp.Message)
		}
	}

	return scanner.Err()
}

// clientListTasks lists all tasks on the remote agent.
func clientListTasks(ctx context.Context, conn quic.Connection) error {
	resp, err := clientRPC(ctx, conn, IncomingMessage{Type: "task_list"})
	if err != nil {
		return err
	}
	if resp.Type == "error" {
		return fmt.Errorf("%s", resp.Message)
	}
	if len(resp.Tasks) == 0 {
		fmt.Println("No tasks.")
		return nil
	}
	for _, t := range resp.Tasks {
		fmt.Printf("  %s  %-10s  %s\n", t.ID, t.Status, t.Title)
	}
	return nil
}

// clientStopTask stops a task by ID.
func clientStopTask(ctx context.Context, conn quic.Connection, taskID string) error {
	resp, err := clientRPC(ctx, conn, IncomingMessage{Type: "task_stop", TaskID: taskID})
	if err != nil {
		return err
	}
	if resp.Type == "error" {
		return fmt.Errorf("%s", resp.Message)
	}
	fmt.Printf("Task %s stopped.\n", taskID)
	return nil
}

// clientContinueTask continues a task with follow-up input.
func clientContinueTask(ctx context.Context, conn quic.Connection, taskID, input string) error {
	stream, err := conn.OpenStreamSync(ctx)
	if err != nil {
		return fmt.Errorf("open stream: %w", err)
	}

	msg := IncomingMessage{
		Type:   "task_continue",
		TaskID: taskID,
		Input:  input,
	}

	data, _ := json.Marshal(msg)
	stream.Write(data)
	stream.Close()

	scanner := bufio.NewScanner(stream)
	scanner.Buffer(make([]byte, 1<<20), 1<<20)

	for scanner.Scan() {
		var resp OutgoingMessage
		if err := json.Unmarshal(scanner.Bytes(), &resp); err != nil {
			continue
		}

		switch resp.Type {
		case "task_created":
			fmt.Printf("[task %s] resumed\n", resp.TaskID)
		case "task_output":
			if resp.Text != "" {
				fmt.Print(resp.Text)
			}
			if resp.Final {
				fmt.Println()
				return nil
			}
		case "error":
			return fmt.Errorf("%s", resp.Message)
		}
	}

	return scanner.Err()
}

// RunClientHTTP connects to a remote Yaver agent over HTTP (via relay or direct)
// and provides the same interactive terminal as RunClient.
func RunClientHTTP(ctx context.Context, baseURL string, token string, opts TerminalClientOptions) error {
	log.Printf("Connecting via HTTP to %s...", baseURL)

	client := &http.Client{Timeout: 30 * time.Second}
	authHeader := "Bearer " + token
	infoSnapshot := &attachInfo{Runner: attachRunnerInfo{ID: opts.DefaultRunner, Model: opts.DefaultModel}}

	// Health check to verify connectivity
	req, err := http.NewRequestWithContext(ctx, "GET", baseURL+"/health", nil)
	if err != nil {
		return fmt.Errorf("build health request: %w", err)
	}
	req.Header.Set("Authorization", authHeader)
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("agent unreachable at %s: %w", baseURL, err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("agent health check failed: HTTP %d", resp.StatusCode)
	}

	// Get agent info
	req, _ = http.NewRequestWithContext(ctx, "GET", baseURL+"/info", nil)
	req.Header.Set("Authorization", authHeader)
	resp, err = client.Do(req)
	if err == nil && resp.StatusCode == 200 {
		var info attachInfo
		json.NewDecoder(resp.Body).Decode(&info)
		resp.Body.Close()
		if strings.TrimSpace(opts.DefaultRunner) != "" && strings.TrimSpace(info.Runner.ID) == "" {
			info.Runner.ID = opts.DefaultRunner
		}
		if strings.TrimSpace(opts.DefaultModel) != "" && strings.TrimSpace(info.Runner.Model) == "" {
			info.Runner.Model = opts.DefaultModel
		}
		infoSnapshot = &info
		printAttachWelcome(infoSnapshot)
	} else {
		if resp != nil {
			resp.Body.Close()
		}
		printAttachWelcome(infoSnapshot)
	}

	// Interactive loop
	reader := bufio.NewReader(os.Stdin)

	for {
		fmt.Print("yaver> ")
		line, err := reader.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				fmt.Println()
				return nil
			}
			return fmt.Errorf("read input: %w", err)
		}

		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		currentHost := ""
		if infoSnapshot != nil {
			currentHost = strings.TrimSpace(infoSnapshot.Hostname)
		}
		if result, err := handleInteractiveCodeCommand(line, strings.TrimSpace(opts.AttachedDeviceID), firstNonEmpty(strings.TrimSpace(opts.AttachedDeviceName), currentHost), opts.DefaultRunner, opts.DefaultModel); result.Handled {
			if err != nil {
				fmt.Printf("error: %v\n", err)
				continue
			}
			if result.ShouldExit {
				return nil
			}
			if cfg, profile, loadErr := loadCodeConfig(); loadErr == nil && cfg != nil && profile != nil {
				opts.DefaultRunner = strings.TrimSpace(profile.Runner)
				opts.DefaultModel = strings.TrimSpace(profile.Model)
			}
			continue
		}

		if cmd, ok := parseTerminalCommand(line); ok {
			switch cmd.Kind {
			case "detach":
				return nil
			case "help":
				printAttachHelp(infoSnapshot)
				continue
			case "tasks":
				if err := httpListTasks(ctx, client, baseURL, authHeader); err != nil {
					fmt.Printf("error: %v\n", err)
				}
				continue
			case "agent":
				if runnerLine := attachRunnerLine(infoSnapshot); runnerLine != "" {
					fmt.Printf("Current coding agent: %s\n", runnerLine)
					fmt.Println()
				} else {
					fmt.Println("Current coding agent: remote default")
					fmt.Println()
				}
				continue
			case "set-agent":
				opts.DefaultRunner = strings.TrimSpace(cmd.Runner)
				opts.DefaultModel = strings.TrimSpace(cmd.Model)
				if infoSnapshot == nil {
					infoSnapshot = &attachInfo{}
				}
				infoSnapshot.Runner.ID = opts.DefaultRunner
				infoSnapshot.Runner.Name = opts.DefaultRunner
				infoSnapshot.Runner.Model = opts.DefaultModel
				fmt.Printf("Default coding agent set to: %s\n\n", attachRunnerLine(infoSnapshot))
				continue
			case "clear":
				fmt.Print("\033[2J\033[H")
				printAttachWelcome(infoSnapshot)
				continue
			case "stop-task":
				if err := httpStopTask(ctx, client, baseURL, authHeader, cmd.TaskID); err != nil {
					fmt.Printf("error: %v\n", err)
				}
				continue
			case "exit-task":
				if err := httpExitTask(ctx, client, baseURL, authHeader, cmd.TaskID); err != nil {
					fmt.Printf("error: %v\n", err)
				}
				continue
			case "continue-task":
				if err := httpContinueTask(ctx, client, baseURL, authHeader, cmd.TaskID, cmd.Input); err != nil {
					fmt.Printf("error: %v\n", err)
				}
				continue
			}
		}

		// Default: create a new task
		fmt.Printf("⟩ %s\n\n", summarizeTerminalInputEcho(line))
		if err := httpCreateTask(ctx, client, baseURL, authHeader, line, opts); err != nil {
			fmt.Printf("error: %v\n", err)
		}
	}
}

func httpCreateTask(ctx context.Context, client *http.Client, baseURL, authHeader, prompt string, opts TerminalClientOptions) error {
	body, _ := json.Marshal(map[string]string{
		"title":       prompt,
		"description": prompt,
		"source":      firstNonEmpty(opts.Source, terminalRemoteTaskSource),
		"runner":      opts.DefaultRunner,
		"model":       opts.DefaultModel,
	})
	req, _ := http.NewRequestWithContext(ctx, "POST", baseURL+"/tasks", bytes.NewReader(body))
	req.Header.Set("Authorization", authHeader)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Yaver-Source", firstNonEmpty(opts.Source, terminalRemoteTaskSource))
	req.Header.Set("X-Yaver-Session-Mode", "terminal")

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("create task: %w", err)
	}
	defer resp.Body.Close()

	var result struct {
		OK     bool   `json:"ok"`
		TaskID string `json:"taskId"`
		Error  string `json:"error"`
	}
	json.NewDecoder(resp.Body).Decode(&result)
	if !result.OK {
		return fmt.Errorf("create task: %s", result.Error)
	}

	fmt.Printf("[task %s] created\n", result.TaskID)

	// Stream output via SSE
	sseClient := &http.Client{Timeout: 10 * time.Minute}
	sseReq, _ := http.NewRequestWithContext(ctx, "GET", baseURL+"/tasks/"+result.TaskID+"/output", nil)
	sseReq.Header.Set("Authorization", authHeader)

	sseResp, err := sseClient.Do(sseReq)
	if err != nil {
		return fmt.Errorf("stream output: %w", err)
	}
	defer sseResp.Body.Close()

	scanner := bufio.NewScanner(sseResp.Body)
	scanner.Buffer(make([]byte, 1<<20), 1<<20)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		var event struct {
			Type   string `json:"type"`
			Text   string `json:"text"`
			Status string `json:"status"`
		}
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			continue
		}
		switch event.Type {
		case "output":
			fmt.Print(event.Text)
		case "done":
			fmt.Println()
			return nil
		}
	}
	return scanner.Err()
}

func httpListTasks(ctx context.Context, client *http.Client, baseURL, authHeader string) error {
	req, _ := http.NewRequestWithContext(ctx, "GET", baseURL+"/tasks", nil)
	req.Header.Set("Authorization", authHeader)

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	var result struct {
		Tasks []struct {
			ID     string `json:"id"`
			Title  string `json:"title"`
			Status string `json:"status"`
		} `json:"tasks"`
	}
	json.NewDecoder(resp.Body).Decode(&result)
	if len(result.Tasks) == 0 {
		fmt.Println("No tasks.")
		return nil
	}
	for _, t := range result.Tasks {
		fmt.Printf("  %s  %-10s  %s\n", t.ID, t.Status, t.Title)
	}
	return nil
}

func httpStopTask(ctx context.Context, client *http.Client, baseURL, authHeader, taskID string) error {
	req, _ := http.NewRequestWithContext(ctx, "POST", baseURL+"/tasks/"+taskID+"/stop", nil)
	req.Header.Set("Authorization", authHeader)

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("stop failed: HTTP %d", resp.StatusCode)
	}
	fmt.Printf("Task %s stopped.\n", taskID)
	return nil
}

func httpExitTask(ctx context.Context, client *http.Client, baseURL, authHeader, taskID string) error {
	req, _ := http.NewRequestWithContext(ctx, "POST", baseURL+"/tasks/"+taskID+"/exit", nil)
	req.Header.Set("Authorization", authHeader)

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("graceful exit failed: HTTP %d", resp.StatusCode)
	}
	fmt.Printf("Task %s exited gracefully.\n", taskID)
	return nil
}

func httpContinueTask(ctx context.Context, client *http.Client, baseURL, authHeader, taskID, input string) error {
	body, _ := json.Marshal(map[string]string{"input": input})
	req, _ := http.NewRequestWithContext(ctx, "POST", baseURL+"/tasks/"+taskID+"/continue", bytes.NewReader(body))
	req.Header.Set("Authorization", authHeader)
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	var result struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	json.NewDecoder(resp.Body).Decode(&result)
	if !result.OK {
		return fmt.Errorf("continue: %s", result.Error)
	}

	fmt.Printf("[task %s] resumed\n", taskID)

	// Stream output
	sseClient := &http.Client{Timeout: 10 * time.Minute}
	sseReq, _ := http.NewRequestWithContext(ctx, "GET", baseURL+"/tasks/"+taskID+"/output", nil)
	sseReq.Header.Set("Authorization", authHeader)

	sseResp, err := sseClient.Do(sseReq)
	if err != nil {
		return fmt.Errorf("stream output: %w", err)
	}
	defer sseResp.Body.Close()

	scanner := bufio.NewScanner(sseResp.Body)
	scanner.Buffer(make([]byte, 1<<20), 1<<20)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		var event struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			continue
		}
		switch event.Type {
		case "output":
			fmt.Print(event.Text)
		case "done":
			fmt.Println()
			return nil
		}
	}
	return scanner.Err()
}

// clientRPC sends a single message and reads one response (non-streaming).
func clientRPC(ctx context.Context, conn quic.Connection, msg IncomingMessage) (OutgoingMessage, error) {
	stream, err := conn.OpenStreamSync(ctx)
	if err != nil {
		return OutgoingMessage{}, fmt.Errorf("open stream: %w", err)
	}
	defer stream.Close()

	data, _ := json.Marshal(msg)
	if _, err := stream.Write(data); err != nil {
		return OutgoingMessage{}, fmt.Errorf("write: %w", err)
	}
	// Close write side to signal we're done
	stream.Close()

	respData, err := io.ReadAll(io.LimitReader(stream, 1<<20))
	if err != nil {
		return OutgoingMessage{}, fmt.Errorf("read response: %w", err)
	}

	// Response may contain multiple newline-delimited JSON objects; take the first
	lines := strings.SplitN(string(respData), "\n", 2)
	if len(lines) == 0 || lines[0] == "" {
		return OutgoingMessage{}, fmt.Errorf("empty response")
	}

	var resp OutgoingMessage
	if err := json.Unmarshal([]byte(lines[0]), &resp); err != nil {
		return OutgoingMessage{}, fmt.Errorf("parse response: %w", err)
	}
	return resp, nil
}
