package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

type sessionFrame struct {
	Type      string `json:"type"`
	SessionID string `json:"sessionId"`
	Resumed   bool   `json:"resumed"`
	Prompt    string `json:"prompt"`
}

func main() {
	baseURL := flag.String("base-url", "", "Host agent base URL, e.g. https://relay.example/d/device or http://127.0.0.1:18080")
	token := flag.String("token", "", "Guest bearer token")
	command := flag.String("command", "", "Shell command to send to the terminal")
	expect := flag.String("expect", "", "Substring that must appear in terminal output")
	timeout := flag.Duration("timeout", 45*time.Second, "Overall timeout")
	flag.Parse()

	if strings.TrimSpace(*baseURL) == "" || strings.TrimSpace(*token) == "" || strings.TrimSpace(*command) == "" || strings.TrimSpace(*expect) == "" {
		fmt.Fprintln(os.Stderr, "usage: hostshare-terminal-smoke --base-url <url> --token <bearer> --command <cmd> --expect <substring>")
		os.Exit(2)
	}

	wsURL, err := websocketURL(strings.TrimSpace(*baseURL), strings.TrimSpace(*token))
	if err != nil {
		fmt.Fprintf(os.Stderr, "build websocket URL: %v\n", err)
		os.Exit(1)
	}

	conn, resp, err := websocket.DefaultDialer.Dial(wsURL, http.Header{})
	if err != nil {
		if resp != nil {
			fmt.Fprintf(os.Stderr, "dial websocket %s: %v (http %d)\n", wsURL, err, resp.StatusCode)
		} else {
			fmt.Fprintf(os.Stderr, "dial websocket %s: %v\n", wsURL, err)
		}
		os.Exit(1)
	}
	defer conn.Close()

	var (
		mu        sync.Mutex
		output    strings.Builder
		sessionID string
	)

	done := make(chan error, 1)
	go func() {
		for {
			mt, payload, err := conn.ReadMessage()
			if err != nil {
				done <- err
				return
			}
			switch mt {
			case websocket.TextMessage:
				var frame sessionFrame
				if err := json.Unmarshal(payload, &frame); err == nil {
					switch frame.Type {
					case "terminal_session":
						mu.Lock()
						sessionID = frame.SessionID
						mu.Unlock()
						continue
					case "sudo_prompt":
						mu.Lock()
						output.WriteString("\n[sudo] ")
						output.WriteString(frame.Prompt)
						output.WriteByte('\n')
						mu.Unlock()
						continue
					}
				}
				mu.Lock()
				output.Write(payload)
				mu.Unlock()
			case websocket.BinaryMessage:
				mu.Lock()
				output.Write(payload)
				mu.Unlock()
			}
		}
	}()

	deadline := time.Now().Add(*timeout)
	for time.Now().Before(deadline) {
		mu.Lock()
		ready := sessionID != ""
		mu.Unlock()
		if ready {
			break
		}
		select {
		case err := <-done:
			fmt.Fprintf(os.Stderr, "terminal closed before session ready: %v\n", err)
			os.Exit(1)
		case <-time.After(100 * time.Millisecond):
		}
	}

	mu.Lock()
	currentSessionID := sessionID
	mu.Unlock()
	if currentSessionID == "" {
		fmt.Fprintln(os.Stderr, "timed out waiting for terminal session metadata")
		os.Exit(1)
	}

	if err := conn.WriteMessage(websocket.BinaryMessage, []byte(strings.TrimSpace(*command)+"\n")); err != nil {
		fmt.Fprintf(os.Stderr, "write terminal command: %v\n", err)
		os.Exit(1)
	}

	expectNeedle := strings.TrimSpace(*expect)
	for time.Now().Before(deadline) {
		mu.Lock()
		out := output.String()
		mu.Unlock()
		if strings.Contains(out, expectNeedle) {
			fmt.Printf("terminal session %s matched expected output\n", currentSessionID)
			fmt.Println("---- begin terminal output ----")
			fmt.Print(out)
			if !strings.HasSuffix(out, "\n") {
				fmt.Println()
			}
			fmt.Println("---- end terminal output ----")
			_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"terminate_session"}`))
			return
		}
		select {
		case err := <-done:
			mu.Lock()
			out := output.String()
			mu.Unlock()
			if strings.Contains(out, expectNeedle) {
				fmt.Printf("terminal session %s matched expected output before close\n", currentSessionID)
				fmt.Println("---- begin terminal output ----")
				fmt.Print(out)
				if !strings.HasSuffix(out, "\n") {
					fmt.Println()
				}
				fmt.Println("---- end terminal output ----")
				return
			}
			fmt.Fprintf(os.Stderr, "terminal closed before expected output: %v\n", err)
			fmt.Fprintf(os.Stderr, "captured output:\n%s\n", out)
			os.Exit(1)
		case <-time.After(150 * time.Millisecond):
		}
	}

	mu.Lock()
	out := output.String()
	mu.Unlock()
	fmt.Fprintf(os.Stderr, "timed out waiting for %q in terminal output\n", expectNeedle)
	fmt.Fprintf(os.Stderr, "captured output:\n%s\n", out)
	os.Exit(1)
}

func websocketURL(baseURL, token string) (string, error) {
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return "", err
	}
	switch parsed.Scheme {
	case "http":
		parsed.Scheme = "ws"
	case "https":
		parsed.Scheme = "wss"
	case "ws", "wss":
	default:
		return "", fmt.Errorf("unsupported scheme %q", parsed.Scheme)
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/") + "/ws/terminal"
	query := parsed.Query()
	query.Set("token", token)
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}
