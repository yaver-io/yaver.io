package main

// shell_cmd.go — `yaver shell [device]`: an interactive PTY on a remote
// (or local) Yaver machine over the agent's existing owner-gated
// /ws/terminal WebSocket. This is the NAT-friendly "ssh" path: it rides
// the same transport resolution as the rest of the agent (LAN → mesh →
// public endpoint → relay), so it reaches a headless box behind CGNAT
// that real OpenSSH cannot — exactly the case a zero-touch provisioned
// Raspberry Pi lands in (relay-only, no inbound port).
//
// It is the literal-terminal counterpart to the web xterm and mobile
// terminal, which already attach to /ws/terminal. Combined with the
// provisioning flow, "connect from terminal / web / mobile, all such
// cases" works the moment you own the box — no sshd, no port-forward.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	"golang.org/x/term"
)

// runShellCmd is the `yaver shell` CLI entry. With no device it attaches
// to the local agent; with a device hint it resolves a remote agent and
// attaches over the best reachable transport.
func runShellCmd(args []string) {
	var deviceHint, shell string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--shell":
			i++
			if i < len(args) {
				shell = args[i]
			}
		case "-h", "--help":
			fmt.Println(`yaver shell [device] [--shell <sh>]

  Open an interactive shell on a Yaver machine over the relay-friendly
  /ws/terminal transport (works through NAT — no sshd or port-forward).

  yaver shell                 Shell on this machine's local agent
  yaver shell <device>        Shell on a remote device (alias/id/name)
  yaver shell <device> --shell /bin/bash`)
			return
		default:
			if !strings.HasPrefix(args[i], "-") && deviceHint == "" {
				deviceHint = args[i]
			}
		}
	}

	if err := runShellOverRelay(deviceHint, shell); err != nil {
		fmt.Fprintf(os.Stderr, "shell: %v\n", err)
		os.Exit(1)
	}
}

// runShellOverRelay resolves a reachable base URL for deviceHint (empty =
// local agent), opens /ws/terminal, and bridges the local terminal to the
// remote PTY. Also used as the `yaver ssh` fallback when no direct SSH
// host can be resolved (relay-only boxes).
func runShellOverRelay(deviceHint, shell string) error {
	cfg, err := LoadConfig()
	if err != nil || cfg == nil || strings.TrimSpace(cfg.AuthToken) == "" {
		return fmt.Errorf("not signed in — run `yaver auth` first")
	}

	baseURL := ""
	token := cfg.AuthToken
	headers := http.Header{}
	label := "local agent"

	if strings.TrimSpace(deviceHint) == "" {
		// Local agent on this machine.
		baseURL = "http://127.0.0.1:18080"
		if _, running := isAgentRunning(); !running {
			return fmt.Errorf("local agent is not running — run `yaver serve` first")
		}
	} else {
		candidates, _, rerr := resolveRemoteAgentCandidates(deviceHint)
		if rerr != nil {
			return fmt.Errorf("resolve %q: %w", deviceHint, rerr)
		}
		if len(candidates) == 0 {
			return fmt.Errorf("no reachable transport to %q — is the device online?", deviceHint)
		}
		// Candidates are pre-ordered + probed (LAN → mesh → public → relay).
		c := candidates[0]
		baseURL = strings.TrimRight(c.BaseURL, "/")
		label = fmt.Sprintf("%s via %s", c.DeviceID, c.Kind)
		for k, v := range c.Headers {
			headers.Set(k, v)
			if strings.EqualFold(k, "Authorization") {
				token = strings.TrimSpace(strings.TrimPrefix(v, "Bearer "))
			}
		}
	}

	wsURL, err := terminalWSURL(baseURL, token, shell)
	if err != nil {
		return err
	}

	dialer := &websocket.Dialer{HandshakeTimeout: 15 * time.Second}
	conn, resp, err := dialer.Dial(wsURL, headers)
	if err != nil {
		if resp != nil {
			return fmt.Errorf("connect to %s: %s (%d)", label, err, resp.StatusCode)
		}
		return fmt.Errorf("connect to %s: %w", label, err)
	}
	defer conn.Close()

	fmt.Fprintf(os.Stderr, "→ connected to %s — Ctrl-D / `exit` to leave\r\n", label)
	return bridgeTerminal(conn)
}

// terminalWSURL converts an http(s) agent base URL into the ws(s)
// /ws/terminal URL with the bearer as ?token= (WS clients can't set the
// Authorization header in browsers; the agent's auth() promotes ?token=).
// Mirrors the SDK's terminalWsUrl (sdk/js/src/fleet.ts).
func terminalWSURL(baseURL, token, shell string) (string, error) {
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	switch {
	case strings.HasPrefix(base, "https://"):
		base = "wss://" + strings.TrimPrefix(base, "https://")
	case strings.HasPrefix(base, "http://"):
		base = "ws://" + strings.TrimPrefix(base, "http://")
	default:
		return "", fmt.Errorf("unsupported base URL scheme: %s", baseURL)
	}
	u := base + "/ws/terminal?token=" + token
	if strings.TrimSpace(shell) != "" {
		u += "&shell=" + shell
	}
	return u, nil
}

// bridgeTerminal puts the local terminal into raw mode and pumps bytes
// both ways: stdin → binary frames, server binary frames → stdout. It
// polls the terminal size once a second and sends a resize control frame
// when it changes (cross-platform; avoids SIGWINCH build tags). Server
// text frames (the session-id announcement) are ignored.
func bridgeTerminal(conn *websocket.Conn) error {
	stdinFd := int(os.Stdin.Fd())
	var restore func()
	if term.IsTerminal(stdinFd) {
		oldState, err := term.MakeRaw(stdinFd)
		if err == nil {
			restore = func() { _ = term.Restore(stdinFd, oldState) }
		}
	}
	if restore != nil {
		defer restore()
	}

	sendResize := func(cols, rows int) {
		msg, _ := json.Marshal(map[string]map[string]int{
			"resize": {"cols": cols, "rows": rows},
		})
		_ = conn.WriteMessage(websocket.TextMessage, msg)
	}
	// Initial size + a poll loop for live resizes.
	lastCols, lastRows := 0, 0
	if c, r, err := term.GetSize(stdinFd); err == nil {
		lastCols, lastRows = c, r
		sendResize(c, r)
	}
	stopResize := make(chan struct{})
	go func() {
		t := time.NewTicker(time.Second)
		defer t.Stop()
		for {
			select {
			case <-stopResize:
				return
			case <-t.C:
				if c, r, err := term.GetSize(stdinFd); err == nil && (c != lastCols || r != lastRows) {
					lastCols, lastRows = c, r
					sendResize(c, r)
				}
			}
		}
	}()

	// stdin → ws (binary frames).
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := os.Stdin.Read(buf)
			if n > 0 {
				if werr := conn.WriteMessage(websocket.BinaryMessage, buf[:n]); werr != nil {
					return
				}
			}
			if err != nil {
				// EOF (Ctrl-D) closes the write side; let the remote shell exit.
				_ = conn.WriteMessage(websocket.CloseMessage,
					websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
				return
			}
		}
	}()

	// ws → stdout (binary), until the connection closes.
	for {
		mt, data, err := conn.ReadMessage()
		if err != nil {
			close(stopResize)
			if websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
				return nil
			}
			// A clean remote `exit` typically surfaces as an abnormal close;
			// don't treat that as a user-facing error.
			return nil
		}
		switch mt {
		case websocket.BinaryMessage:
			_, _ = os.Stdout.Write(data)
		case websocket.TextMessage:
			// Control frame (e.g. {"type":"terminal_session",...}); ignore.
		}
	}
}
