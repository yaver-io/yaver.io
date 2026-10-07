// Package plainssh implements the account-independent phone SSH transport.
// The same implementation is bound into iOS and Android with gomobile.
// No Yaver bearer, relay, device registry or runner credentials enter this lane.
package plainssh

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
)

type request struct {
	Identity    string `json:"identity"`
	Op          string `json:"op"`
	ID          string `json:"id"`
	Host        string `json:"host"`
	Port        int    `json:"port"`
	User        string `json:"user"`
	Fingerprint string `json:"fingerprint"`
	Password    string `json:"password"`
	PrivateKey  string `json:"privateKey"`
	Passphrase  string `json:"passphrase"`
	Pane        string `json:"pane"`
	Session     string `json:"session"`
	Data        string `json:"data"`
}

type failure struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}
type response struct {
	OK    bool     `json:"ok"`
	Value any      `json:"value,omitempty"`
	Error *failure `json:"error,omitempty"`
}
type codedError struct{ code, message string }

func (e codedError) Error() string    { return e.message }
func fail(code, message string) error { return codedError{code, message} }

type connection struct {
	enrollment *enrollment
	client     *ssh.Client
	mu         sync.Mutex
	terminal   *ssh.Session
	stdin      io.WriteCloser
	pane       string
	output     chan []byte
	done       chan struct{}
	closeOnce  sync.Once
	err        error
	lastUsed   time.Time
}

var connections = struct {
	sync.Mutex
	items map[string]*connection
}{items: map[string]*connection{}}
var paneID = regexp.MustCompile(`^%[0-9]+$`)
var sessionID = regexp.MustCompile(`^\$[0-9]+$`)

// Invoke is the deliberately small native bridge. Secrets are accepted only
// for a connection attempt, never persisted or included in responses/logs.
func Invoke(input string) string {
	var req request
	err := json.Unmarshal([]byte(input), &req)
	var value any
	if err == nil {
		value, err = dispatch(req)
	}
	result := response{OK: err == nil, Value: value}
	if err != nil {
		code := "SSH_FAILED"
		var named codedError
		if errors.As(err, &named) {
			code = named.code
		}
		result.Error = &failure{Code: code, Message: err.Error()}
	}
	b, _ := json.Marshal(result)
	return string(b)
}

func dispatch(req request) (any, error) {
	if req.Op == "closeAll" {
		connections.Lock()
		items := connections.items
		connections.items = map[string]*connection{}
		connections.Unlock()
		for _, c := range items {
			c.close()
		}
		return nil, nil
	}
	if req.Op == "probe" || req.Op == "connect" {
		return connect(req)
	}
	connections.Lock()
	c := connections.items[req.ID]
	connections.Unlock()
	if c == nil {
		return nil, fail("SSH_DISCONNECTED", "Connection ended. Reconnect to the saved host.")
	}
	c.mu.Lock()
	c.lastUsed = time.Now()
	c.mu.Unlock()
	switch req.Op {
	case "enroll":
		return nil, c.enroll(false)
	case "installYaver":
		return nil, c.enroll(true)
	case "enrollmentRead":
		return c.enrollmentRead()
	case "close":
		connections.Lock()
		delete(connections.items, req.ID)
		connections.Unlock()
		c.close()
		return nil, nil
	case "panes":
		return c.listPanes()
	case "open":
		return nil, c.open(req.Session, req.Pane, req.Identity)
	case "submit":
		data, err := base64.StdEncoding.DecodeString(req.Data)
		if err != nil || len(data) == 0 || len(data) > 32768 {
			return nil, fail("SSH_INPUT_INVALID", "Message must contain 1–32768 bytes.")
		}
		c.mu.Lock()
		defer c.mu.Unlock()
		if c.stdin == nil {
			return nil, fail("SSH_NO_PANE", "Select a pane before sending input.")
		}
		var random [16]byte
		if _, err = rand.Read(random[:]); err != nil {
			return nil, err
		}
		name := "yaver-phone-" + hex.EncodeToString(random[:])
		s, err := c.client.NewSession()
		if err != nil {
			return nil, err
		}
		defer s.Close()
		s.Stdin = bytes.NewReader(data)
		timer := time.AfterFunc(8*time.Second, func() { s.Close() })
		defer timer.Stop()
		// tmux knows the CURRENT application's paste mode even when it was
		// enabled before this phone attached. Never modify the user's buffer.
		command := tmuxPath + "tmux load-buffer -b " + name + " - && tmux paste-buffer -d -p -b " + name + " -t " + c.pane + " && tmux send-keys -t " + c.pane + " Enter"
		err = s.Run(command)
		if err != nil {
			return nil, fail("TMUX_INPUT_UNCERTAIN", "Message delivery is uncertain. Inspect the pane before retrying; nothing was replayed.")
		}
		return nil, nil
	case "write":
		data, err := base64.StdEncoding.DecodeString(req.Data)
		if err != nil || len(data) > 32768 {
			return nil, fail("SSH_INPUT_INVALID", "Input must be valid base64, at most 32 KB.")
		}
		c.mu.Lock()
		defer c.mu.Unlock()
		if c.stdin == nil {
			return nil, fail("SSH_NO_PANE", "Select a pane before sending input.")
		}
		// Hex keys target a stable pane ID. Text cannot become a tmux command,
		// and changing the desktop's active pane cannot redirect phone input.
		var command strings.Builder
		fmt.Fprintf(&command, "send-keys -t %s -H", c.pane)
		for _, b := range data {
			fmt.Fprintf(&command, " %02x", b)
		}
		command.WriteByte('\n')
		if len(data) > 0 {
			_, err = io.WriteString(c.stdin, command.String())
		}
		return nil, err
	case "snapshot":
		c.mu.Lock()
		target := c.pane
		c.mu.Unlock()
		if !paneID.MatchString(target) {
			return nil, fail("SSH_NO_PANE", "Select a pane first.")
		}
		data, err := c.command(tmuxPath + "tmux capture-pane -p -t " + target)
		return string(data), err
	case "read":
		select {
		case data := <-c.output:
			return map[string]any{"data": base64.StdEncoding.EncodeToString(data)}, nil
		case <-c.done:
			c.mu.Lock()
			err := c.err
			c.mu.Unlock()
			if err != nil {
				return nil, err
			}
			return nil, fail("SSH_DISCONNECTED", "SSH disconnected. Reconnect; the tmux pane keeps running.")
		case <-time.After(time.Second):
			return map[string]any{"data": ""}, nil
		}
	}
	return nil, fail("SSH_OPERATION_INVALID", "Unknown SSH operation.")
}

func connect(req request) (any, error) {
	if req.Port == 0 {
		req.Port = 22
	}
	if strings.TrimSpace(req.Host) == "" || strings.ContainsAny(req.Host, "\r\n\x00/") || req.Port < 1 || req.Port > 65535 || strings.TrimSpace(req.User) == "" {
		return nil, fail("SSH_HOST_INVALID", "Enter an SSH hostname, port and username.")
	}
	var auth []ssh.AuthMethod
	if req.Op == "connect" {
		if req.Fingerprint == "" {
			return nil, fail("SSH_HOST_UNTRUSTED", "Verify and trust this host's fingerprint first.")
		}
		if req.PrivateKey != "" {
			var signer ssh.Signer
			var err error
			if req.Passphrase != "" {
				signer, err = ssh.ParsePrivateKeyWithPassphrase([]byte(req.PrivateKey), []byte(req.Passphrase))
			} else {
				signer, err = ssh.ParsePrivateKey([]byte(req.PrivateKey))
			}
			if err != nil {
				return nil, fail("SSH_KEY_INVALID", "Cannot unlock this SSH private key. Check its format and passphrase.")
			}
			auth = append(auth, ssh.PublicKeys(signer))
		}
		if req.Password != "" {
			auth = append(auth, ssh.Password(req.Password))
		}
	}
	observed := ""
	config := &ssh.ClientConfig{User: req.User, Auth: auth, Timeout: 12 * time.Second,
		HostKeyCallback: func(_ string, _ net.Addr, key ssh.PublicKey) error {
			observed = ssh.FingerprintSHA256(key)
			if req.Op == "probe" {
				return fail("SSH_HOST_UNTRUSTED", "Host key observed; authentication was not attempted.")
			}
			if observed != req.Fingerprint {
				return fail("SSH_HOST_CHANGED", "SSH host key changed. Verify the new fingerprint on your machine before replacing the saved host.")
			}
			return nil
		},
	}
	address := net.JoinHostPort(req.Host, strconv.Itoa(req.Port))
	socket, err := net.DialTimeout("tcp", address, 12*time.Second)
	if err != nil {
		return nil, fail("SSH_UNREACHABLE", "Cannot reach SSH. Check the hostname, Remote Login/sshd and your Tailscale connection, then retry.")
	}
	_ = socket.SetDeadline(time.Now().Add(15 * time.Second))
	cc, channels, requests, err := ssh.NewClientConn(socket, address, config)
	if err != nil {
		socket.Close()
		if req.Op == "probe" && observed != "" {
			return map[string]string{"fingerprint": observed}, nil
		}
		if observed != "" && observed != req.Fingerprint {
			return nil, fail("SSH_HOST_CHANGED", "SSH host key does not match the saved fingerprint. Verify it on the host; connection refused.")
		}
		return nil, fail("SSH_AUTH_FAILED", "SSH handshake or authentication failed. Check your SSH username, key/password or Tailscale SSH policy.")
	}
	_ = socket.SetDeadline(time.Time{})
	c := &connection{client: ssh.NewClient(cc, channels, requests), output: make(chan []byte, 32), done: make(chan struct{}), lastUsed: time.Now()}
	var idBytes [16]byte
	if _, err := rand.Read(idBytes[:]); err != nil {
		c.close()
		return nil, err
	}
	id := hex.EncodeToString(idBytes[:])
	connections.Lock()
	connections.items[id] = c
	connections.Unlock()
	go func() {
		ticker := time.NewTicker(20 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-c.done:
				return
			case <-ticker.C:
				c.mu.Lock()
				idle := time.Since(c.lastUsed) > 5*time.Minute
				c.mu.Unlock()
				if idle {
					c.close()
					return
				}
				_ = socket.SetDeadline(time.Now().Add(12 * time.Second))
				_, _, err := c.client.SendRequest("keepalive@openssh.com", true, nil)
				if err != nil {
					c.close()
					return
				}
				_ = socket.SetDeadline(time.Time{})
			}
		}
	}()
	return map[string]string{"id": id}, nil
}

func (c *connection) close() {
	c.closeOnce.Do(func() {
		close(c.done)
		c.client.Close()
		connections.Lock()
		for id, item := range connections.items {
			if item == c {
				delete(connections.items, id)
			}
		}
		connections.Unlock()
	})
}

// No unbounded command read. A remote tmux hook or shell startup script may
// hang, and a large pane listing must not allocate without a limit.
func (c *connection) command(command string) ([]byte, error) {
	s, err := c.client.NewSession()
	if err != nil {
		return nil, err
	}
	defer s.Close()
	var out limitedBuffer
	s.Stdout = &out
	s.Stderr = &out
	timer := time.AfterFunc(8*time.Second, func() { s.Close() })
	defer timer.Stop()
	err = s.Run(command)
	return out.Bytes(), err
}

type limitedBuffer struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (b *limitedBuffer) Bytes() []byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]byte(nil), b.buffer.Bytes()...)
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.buffer.Len()+len(p) > 1024*1024 {
		return 0, errors.New("SSH command output exceeded 1 MB")
	}
	return b.buffer.Write(p)
}

type pane struct {
	Identity  string `json:"identity"`
	ID        string `json:"id"`
	SessionID string `json:"sessionId"`
	Session   string `json:"session"`
	Window    string `json:"window"`
	Command   string `json:"command"`
	Width     int    `json:"width"`
	Height    int    `json:"height"`
}

const tmuxPath = `PATH="$PATH:/opt/homebrew/bin:/usr/local/bin"; export PATH; `

func (c *connection) listPanes() ([]pane, error) {
	b, err := c.command(tmuxPath + `command -v tmux >/dev/null || exit 127; tmux list-panes -a -F '#{pane_id}	#{session_id}	#{session_name}	#{window_index}	#{pane_current_command}	#{pane_width}	#{pane_height}	#{pid}:#{pane_pid}:#{session_created}'`)
	if err != nil {
		var exit *ssh.ExitError
		if errors.As(err, &exit) && exit.ExitStatus() == 127 {
			return nil, fail("TMUX_MISSING", "Install tmux on this host (brew install tmux on macOS), then create a session with tmux new -s work.")
		}
		if strings.Contains(string(b), "no server running") || strings.Contains(string(b), "no sessions") || strings.Contains(string(b), "No such file or directory") {
			return []pane{}, nil
		}
		return nil, fail("TMUX_LIST_FAILED", "Cannot list tmux panes. Check tmux in your SSH login shell, then retry.")
	}
	panes := parsePanes(string(b))
	if len(panes) == 0 && strings.TrimSpace(string(b)) != "" {
		return nil, fail("TMUX_LIST_FORMAT", "The host returned an unreadable pane list. Check tmux and your SSH shell startup output.")
	}
	return panes, nil
}
func parsePanes(raw string) []pane {
	result := []pane{}
	for _, line := range strings.Split(strings.TrimSpace(raw), "\n") {
		f := strings.Split(line, "\t")
		if len(f) != 8 || !paneID.MatchString(f[0]) || !sessionID.MatchString(f[1]) {
			continue
		}
		w, _ := strconv.Atoi(f[5])
		h, _ := strconv.Atoi(f[6])
		result = append(result, pane{f[7], f[0], f[1], f[2], f[3], f[4], w, h})
	}
	return result
}

func (c *connection) open(session, target, identity string) error {
	if !sessionID.MatchString(session) || !paneID.MatchString(target) {
		return fail("TMUX_TARGET_INVALID", "Choose an existing session and pane from the list.")
	}
	panes, err := c.listPanes()
	if err != nil {
		return err
	}
	found := false
	for _, p := range panes {
		if p.SessionID == session && p.ID == target && identity != "" && p.Identity == identity {
			found = true
		}
	}
	if !found {
		return fail("TMUX_PANE_GONE", "This pane has exited. Refresh the pane list and select a running pane.")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.terminal != nil {
		return fail("TMUX_ALREADY_OPEN", "Disconnect this pane before opening another.")
	}
	s, err := c.client.NewSession()
	if err != nil {
		return err
	}
	stdin, err := s.StdinPipe()
	if err != nil {
		s.Close()
		return err
	}
	stdout, err := s.StdoutPipe()
	if err != nil {
		s.Close()
		return err
	}
	// Control mode gives raw output AND exact-pane input without touching the
	// desktop's active pane, window, dimensions or runner. No PTY is needed.
	if err = s.Start(tmuxPath + "exec tmux -C attach-session -f ignore-size -t '" + session + "'"); err != nil {
		s.Close()
		return err
	}
	c.terminal, c.stdin, c.pane = s, stdin, target
	go c.stream(stdout, target)
	// capture is ordered on the same control connection as subsequent output.
	_, err = io.WriteString(stdin, "capture-pane -p -e -t "+target+"\ndisplay-message -p -t "+target+" '#{cursor_x};#{cursor_y}'\n")
	return err
}

func (c *connection) emit(data []byte) bool {
	select {
	case c.output <- data:
		return true
	case <-c.done:
		return false
	}
}
func (c *connection) stream(reader io.Reader, target string) {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 4096), 1024*1024)
	block := 0
	capturing := false
	frameEnd := ""
	var capture bytes.Buffer
	for scanner.Scan() {
		line := scanner.Text()
		if !capturing && strings.HasPrefix(line, "%begin ") {
			frameEnd = "%end " + strings.TrimPrefix(line, "%begin ")
			block++
			capturing = block == 2
			continue
		}
		if !capturing && strings.HasPrefix(line, "%error ") {
			c.mu.Lock()
			c.err = fail("TMUX_COMMAND_FAILED", "The selected tmux pane rejected an operation. Refresh panes and reconnect.")
			c.mu.Unlock()
			break
		}
		if line == frameEnd {
			if capturing {
				if !c.emit(append([]byte("\x1b[2J\x1b[H"), bytes.TrimSuffix(capture.Bytes(), []byte("\r\n"))...)) {
					return
				}
				capturing = false
			}
			continue
		}
		if capturing {
			if capture.Len()+len(line) > 1024*1024 {
				c.mu.Lock()
				c.err = fail("TMUX_OUTPUT_LIMIT", "Pane snapshot exceeded 1 MB. Reconnect after reducing pane dimensions.")
				c.mu.Unlock()
				break
			}
			capture.WriteString(line)
			capture.WriteString("\r\n")
			continue
		}
		if block == 3 && !strings.HasPrefix(line, "%") {
			xy := strings.Split(line, ";")
			if len(xy) == 2 {
				x, xe := strconv.Atoi(xy[0])
				y, ye := strconv.Atoi(xy[1])
				if xe == nil && ye == nil && x >= 0 && y >= 0 && x < 10000 && y < 10000 {
					if !c.emit([]byte(fmt.Sprintf("\x1b[%d;%dH", y+1, x+1))) {
						return
					}
				}
			}
		}
		if strings.HasPrefix(line, "%layout-change ") || strings.HasPrefix(line, "%window-close ") {
			panes, err := c.listPanes()
			found := false
			if err == nil {
				for _, p := range panes {
					if p.ID == target {
						found = true
					}
				}
			}
			if !found {
				c.mu.Lock()
				c.err = fail("TMUX_PANE_GONE", "The selected pane exited. Refresh panes to choose another.")
				c.mu.Unlock()
				break
			}
		}
		prefix := "%output " + target + " "
		if strings.HasPrefix(line, prefix) {
			if !c.emit(unescapeControl(strings.TrimPrefix(line, prefix))) {
				return
			}
		}
	}
	c.close()
}
func unescapeControl(raw string) []byte {
	result := make([]byte, 0, len(raw))
	for i := 0; i < len(raw); i++ {
		if raw[i] == '\\' && i+3 < len(raw) {
			if n, err := strconv.ParseUint(raw[i+1:i+4], 8, 8); err == nil {
				result = append(result, byte(n))
				i += 3
				continue
			}
		}
		result = append(result, raw[i])
	}
	return result
}
