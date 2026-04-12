package main

// mail_dev.go — MailDevManager wraps mailpit, a local SMTP catch-all server
// used during development to inspect outbound email without relaying it to
// real recipients.
//
// Mailpit provides:
//   - SMTP listener (port 1025) that accepts any mail addressed to any recipient
//   - Web UI (port 8025) + REST API for reading/searching messages
//
// Install: brew install mailpit
// Releases: https://github.com/axllent/mailpit/releases

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ─── Types ────────────────────────────────────────────────────────────────────

type MailDevStatus struct {
	Running      bool   `json:"running"`
	SMTPPort     int    `json:"smtpPort"`
	WebUIPort    int    `json:"webUiPort"`
	MessageCount int    `json:"messageCount"`
	WebUIURL     string `json:"webUiUrl"`
}

// MailpitMessage is a summary of a single message stored in mailpit.
// Field names use PascalCase to match mailpit's JSON API.
type MailpitMessage struct {
	ID      string   `json:"ID"`
	From    string   `json:"From"`
	To      []string `json:"To"`
	Subject string   `json:"Subject"`
	Created string   `json:"Created"`
	Size    int      `json:"Size"`
}

// MailpitMessageFull extends MailpitMessage with body content.
type MailpitMessageFull struct {
	MailpitMessage
	HTML        string `json:"HTML"`
	Text        string `json:"Text"`
	Attachments int    `json:"Attachments"`
}

// ─── Manager ─────────────────────────────────────────────────────────────────

type MailDevManager struct {
	mu       sync.Mutex
	cmd      *exec.Cmd
	smtpPort int
	webPort  int
	pidFile  string
	client   *http.Client
}

func NewMailDevManager() *MailDevManager {
	home, _ := os.UserHomeDir()
	return &MailDevManager{
		smtpPort: 1025,
		webPort:  8025,
		pidFile:  filepath.Join(home, ".yaver", "mailpit.pid"),
		client:   &http.Client{Timeout: 10 * time.Second},
	}
}

// Start starts mailpit. It verifies the binary is installed, checks that port
// 1025 is free, then runs mailpit in the background and records its PID.
func (m *MailDevManager) Start() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Already running in this process?
	if m.cmd != nil && m.cmd.Process != nil {
		return nil
	}

	// Locate mailpit binary.
	binPath, err := exec.LookPath("mailpit")
	if err != nil {
		return fmt.Errorf(
			"mailpit not found in PATH\n" +
				"Install with:\n" +
				"  brew install mailpit\n" +
				"or download from https://github.com/axllent/mailpit/releases",
		)
	}

	// Guard against a port-1025 conflict.
	if portInUseMailDev(m.smtpPort) {
		return fmt.Errorf(
			"port %d is already in use — is mailpit already running?\n"+
				"Run `yaver maildev stop` first, or check for another process on that port.",
			m.smtpPort,
		)
	}

	// Ensure ~/.yaver exists.
	if err := os.MkdirAll(filepath.Dir(m.pidFile), 0755); err != nil {
		return fmt.Errorf("create yaver dir: %w", err)
	}

	cmd := exec.Command(binPath,
		"--smtp", fmt.Sprintf("0.0.0.0:%d", m.smtpPort),
		"--listen", fmt.Sprintf("0.0.0.0:%d", m.webPort),
	)
	// Discard mailpit's own stdout/stderr; errors surface through status checks.
	cmd.Stdout = nil
	cmd.Stderr = nil

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start mailpit: %w", err)
	}
	m.cmd = cmd

	// Persist PID so Stop() works even after a process restart.
	_ = os.WriteFile(m.pidFile, []byte(strconv.Itoa(cmd.Process.Pid)), 0644)

	// Block until the web UI is accepting connections (up to 5 s).
	if err := waitForPortMailDev(m.webPort, 5*time.Second); err != nil {
		return fmt.Errorf("mailpit did not start in time: %w", err)
	}
	return nil
}

// Stop kills the mailpit process and removes the PID file.
func (m *MailDevManager) Stop() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.cmd != nil && m.cmd.Process != nil {
		if err := m.cmd.Process.Kill(); err != nil {
			return fmt.Errorf("kill mailpit: %w", err)
		}
		_ = m.cmd.Wait()
		m.cmd = nil
		_ = os.Remove(m.pidFile)
		return nil
	}

	// No in-process handle — try the PID file.
	return m.killFromPIDFile()
}

// Status returns the current state of mailpit, including message count when
// the process is running.
func (m *MailDevManager) Status() (*MailDevStatus, error) {
	running := m.isRunning()
	status := &MailDevStatus{
		Running:   running,
		SMTPPort:  m.smtpPort,
		WebUIPort: m.webPort,
		WebUIURL:  fmt.Sprintf("http://localhost:%d", m.webPort),
	}
	if !running {
		return status, nil
	}

	resp, err := m.client.Get(fmt.Sprintf("http://localhost:%d/api/v1/messages", m.webPort))
	if err != nil {
		return status, nil // running but API not yet reachable
	}
	defer resp.Body.Close()

	var result struct {
		Total int `json:"total"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err == nil {
		status.MessageCount = result.Total
	}
	return status, nil
}

// Inbox fetches messages from mailpit's REST API. Pass empty strings to skip
// filtering; limit ≤ 0 defaults to 50.
func (m *MailDevManager) Inbox(to, subject string, limit int) ([]MailpitMessage, error) {
	if !m.isRunning() {
		return nil, fmt.Errorf("mailpit is not running — call Start() first")
	}
	if limit <= 0 {
		limit = 50
	}

	u := fmt.Sprintf("http://localhost:%d/api/v1/messages?limit=%d", m.webPort, limit)
	if search := buildMailpitSearch(to, subject); search != "" {
		u += "&search=" + search
	}

	resp, err := m.client.Get(u)
	if err != nil {
		return nil, fmt.Errorf("fetch messages: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("mailpit API %d: %s", resp.StatusCode, string(body))
	}

	// Mailpit's list response: { messages: [...], total: N }
	var result struct {
		Messages []struct {
			ID   string `json:"ID"`
			From struct {
				Address string `json:"Address"`
			} `json:"From"`
			To []struct {
				Address string `json:"Address"`
			} `json:"To"`
			Subject string `json:"Subject"`
			Created string `json:"Created"`
			Size    int    `json:"Size"`
		} `json:"messages"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode messages: %w", err)
	}

	msgs := make([]MailpitMessage, 0, len(result.Messages))
	for _, raw := range result.Messages {
		toAddrs := make([]string, 0, len(raw.To))
		for _, t := range raw.To {
			toAddrs = append(toAddrs, t.Address)
		}
		msgs = append(msgs, MailpitMessage{
			ID:      raw.ID,
			From:    raw.From.Address,
			To:      toAddrs,
			Subject: raw.Subject,
			Created: raw.Created,
			Size:    raw.Size,
		})
	}
	return msgs, nil
}

// Read fetches the full content of a single message by its mailpit ID.
func (m *MailDevManager) Read(messageID string) (*MailpitMessageFull, error) {
	if !m.isRunning() {
		return nil, fmt.Errorf("mailpit is not running — call Start() first")
	}

	u := fmt.Sprintf("http://localhost:%d/api/v1/message/%s", m.webPort, messageID)
	resp, err := m.client.Get(u)
	if err != nil {
		return nil, fmt.Errorf("get message: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("mailpit API %d: %s", resp.StatusCode, string(body))
	}

	var raw struct {
		ID   string `json:"ID"`
		From struct {
			Address string `json:"Address"`
		} `json:"From"`
		To []struct {
			Address string `json:"Address"`
		} `json:"To"`
		Subject     string `json:"Subject"`
		Created     string `json:"Date"` // mailpit uses "Date" in the detail view
		Size        int    `json:"Size"`
		HTML        string `json:"HTML"`
		Text        string `json:"Text"`
		Attachments []any  `json:"Attachments"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("decode message: %w", err)
	}

	toAddrs := make([]string, 0, len(raw.To))
	for _, t := range raw.To {
		toAddrs = append(toAddrs, t.Address)
	}

	return &MailpitMessageFull{
		MailpitMessage: MailpitMessage{
			ID:      raw.ID,
			From:    raw.From.Address,
			To:      toAddrs,
			Subject: raw.Subject,
			Created: raw.Created,
			Size:    raw.Size,
		},
		HTML:        raw.HTML,
		Text:        raw.Text,
		Attachments: len(raw.Attachments),
	}, nil
}

// Clear deletes all messages from mailpit's inbox.
func (m *MailDevManager) Clear() error {
	if !m.isRunning() {
		return fmt.Errorf("mailpit is not running — call Start() first")
	}

	req, err := http.NewRequest(http.MethodDelete,
		fmt.Sprintf("http://localhost:%d/api/v1/messages", m.webPort), nil)
	if err != nil {
		return fmt.Errorf("build delete request: %w", err)
	}

	resp, err := m.client.Do(req)
	if err != nil {
		return fmt.Errorf("clear messages: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("mailpit API %d: %s", resp.StatusCode, string(body))
	}
	return nil
}

// Config returns the SMTP connection parameters to inject into application
// environment variables during local development.
func (m *MailDevManager) Config() map[string]string {
	return map[string]string{
		"SMTP_HOST": "localhost",
		"SMTP_PORT": strconv.Itoa(m.smtpPort),
		"SMTP_USER": "",
		"SMTP_PASS": "",
		"SMTP_TLS":  "false",
	}
}

// ─── Internal helpers ─────────────────────────────────────────────────────────

// isRunning checks whether mailpit is alive either via the in-process Cmd or
// by looking up the PID stored on disk.
func (m *MailDevManager) isRunning() bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.cmd != nil && m.cmd.Process != nil {
		if processAlive(m.cmd.Process.Pid) {
			return true
		}
	}

	data, err := os.ReadFile(m.pidFile)
	if err != nil {
		return false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return false
	}
	return processAlive(pid)
}

// killFromPIDFile reads the stored PID and kills that process.
func (m *MailDevManager) killFromPIDFile() error {
	data, err := os.ReadFile(m.pidFile)
	if err != nil {
		return nil // nothing to kill
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return nil
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return nil
	}
	_ = proc.Kill()
	_ = os.Remove(m.pidFile)
	return nil
}

// processAlive returns true if a process with the given PID exists and is
// running (signal 0 does not kill but does probe for existence).
func processAlive(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	// On Unix, FindProcess always succeeds; we must send signal 0 to probe.
	return proc.Signal(os.Signal(nil)) == nil
}

// portInUseMailDev reports whether the given TCP port is already bound locally.
func portInUseMailDev(port int) bool {
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), time.Second)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

// waitForPortMailDev blocks until the given TCP port accepts a connection or
// the deadline elapses.
func waitForPortMailDev(port int, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 200*time.Millisecond)
		if err == nil {
			conn.Close()
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("port %d not available after %s", port, timeout)
}

// buildMailpitSearch assembles a mailpit search expression from optional
// recipient and subject filters.
func buildMailpitSearch(to, subject string) string {
	var parts []string
	if to != "" {
		parts = append(parts, "to:"+to)
	}
	if subject != "" {
		parts = append(parts, "subject:"+subject)
	}
	return strings.Join(parts, " ")
}
