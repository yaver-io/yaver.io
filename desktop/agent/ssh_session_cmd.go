package main

// ssh_session_cmd.go — the forced-command endpoint for the out-of-band SSH
// control/task channel (see docs/architecture/ROBUST_TRANSPORT_SSH_QUIC.md).
//
// A paired device's SSH key is installed with:
//
//   command="yaver ssh-session --session <id>",no-pty,no-agent-forwarding,\
//   no-port-forwarding,no-user-rc ssh-ed25519 AAAA... yaver-managed-<deviceId>
//
// so an SSH connection on that key can ONLY invoke `yaver ssh-session` — never a
// shell, never arbitrary forwarding. The verb the client asked for arrives in
// $SSH_ORIGINAL_COMMAND. We map it through a strict WHITELIST to a single
// loopback call against this box's own agent HTTP (127.0.0.1:18080), reusing
// every existing handler + its auth. The channel therefore exposes exactly the
// Yaver verbs (health/status/tasks/projects/tmux/doctor), nothing else:
//
//   - unknown verb            → rejected (no passthrough, no shell)
//   - path/arg with traversal → rejected
//   - only loopback           → the SSH pipe never reaches anything but 127.0.0.1
//
// This is the "SSH is the pipe, Yaver identity is the authority, forced-command
// is the cage" design. The whitelist (sshSessionRoute) is a pure function so the
// security contract is unit-tested without a live SSH server or agent.

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// sshSessionRoute maps an out-of-band verb to the single loopback agent-HTTP
// call it is allowed to make. ok=false means "reject" — the caller must NOT fall
// back to a shell or any other behavior. This is the entire security surface of
// the forced-command channel: if a verb is not listed here, it cannot be reached
// over SSH.
//
// method/path are fixed per verb; the client's JSON body (for POSTs) is passed
// through to the handler, which re-validates it exactly as it does for the HTTP
// path. `taskScoped` verbs require a taskId path segment, validated as an opaque
// id (no slashes) so a verb can never be used to reach an arbitrary path.
func sshSessionRoute(verb string) (method, path string, taskScoped, ok bool) {
	switch strings.TrimSpace(verb) {
	// Read-only control plane — the liveness/status/eventing surface.
	case "health":
		return http.MethodGet, "/health", false, true
	case "status":
		return http.MethodGet, "/agent/status", false, true
	case "info":
		return http.MethodGet, "/info", false, true
	case "list-projects":
		return http.MethodGet, "/projects/mobile", false, true
	case "list-tasks":
		return http.MethodGet, "/tasks", false, true
	// Connectivity self-heal verbs — the whole point of the out-of-band channel:
	// diagnose and repair the transport agentically while the data path is down.
	case "doctor-transport":
		return http.MethodGet, "/doctor/transport", false, true
	case "repair-relay":
		return http.MethodPost, "/settings/repair-relay", false, true
	// Task plane — tasks can ride SSH too (drives the SAME tmux/runner session).
	case "run-task":
		return http.MethodPost, "/tasks", false, true
	case "continue-task":
		return http.MethodPost, "/tasks/{id}/continue", true, true
	case "stop-task":
		return http.MethodPost, "/tasks/{id}/stop", true, true
	}
	return "", "", false, false
}

// sshSessionRequest is the tiny wire shape the client sends as
// $SSH_ORIGINAL_COMMAND: a verb plus, for scoped/POST verbs, a taskId and/or a
// raw JSON body. Keeping it a single JSON object (rather than shell-style argv)
// removes any shell-quoting/injection surface.
type sshSessionRequest struct {
	Verb   string          `json:"verb"`
	TaskID string          `json:"taskId,omitempty"`
	Body   json.RawMessage `json:"body,omitempty"`
}

// isSafeTaskID rejects anything that could turn a task-scoped verb into an
// arbitrary path (slashes, dot-dot, whitespace, control chars). Task ids are
// opaque tokens; this is defense-in-depth on top of the fixed path template.
func isSafeTaskID(id string) bool {
	if id == "" || len(id) > 128 {
		return false
	}
	for _, r := range id {
		ok := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.'
		if !ok {
			return false
		}
	}
	return !strings.Contains(id, "..")
}

// runSSHSession is the `yaver ssh-session` subcommand: the forced command. It
// parses $SSH_ORIGINAL_COMMAND, enforces the whitelist, and proxies exactly one
// request to the local agent over loopback, streaming the reply to stdout.
func runSSHSession(args []string) int {
	raw := strings.TrimSpace(os.Getenv("SSH_ORIGINAL_COMMAND"))
	if raw == "" {
		fmt.Fprintln(os.Stderr, "ssh-session: no command (this endpoint is forced-command only)")
		return 2
	}
	method, path, body, errMsg := parseAndRouteSSHCommand(raw)
	if errMsg != "" {
		fmt.Fprintln(os.Stderr, "ssh-session: "+errMsg)
		return 2
	}
	out, exit := dispatchLocalAgent(method, path, body)
	os.Stdout.Write(out)
	return exit
}

// parseAndRouteSSHCommand is the shared parse+whitelist+validate step used by
// BOTH the forced-command CLI (runSSHSession) and the embedded SSH control server
// (ssh_control_server.go). It turns the client's $SSH_ORIGINAL_COMMAND / exec
// payload into the single loopback call it is allowed to make, or a non-empty
// errMsg to refuse. Keeping it in one place means the security whitelist can
// never drift between the two entry points.
func parseAndRouteSSHCommand(raw string) (method, path string, body []byte, errMsg string) {
	var req sshSessionRequest
	if err := json.Unmarshal([]byte(strings.TrimSpace(raw)), &req); err != nil {
		return "", "", nil, "command must be a JSON {verb,...} object"
	}
	m, p, taskScoped, ok := sshSessionRoute(req.Verb)
	if !ok {
		return "", "", nil, fmt.Sprintf("verb %q is not permitted on the out-of-band channel", req.Verb)
	}
	if taskScoped {
		if !isSafeTaskID(req.TaskID) {
			return "", "", nil, "this verb requires a valid taskId"
		}
		p = strings.Replace(p, "{id}", req.TaskID, 1)
	}
	return m, p, req.Body, ""
}

// proxySSHSessionToLocalAgent performs the single whitelisted loopback call. It
// reuses the agent's own HTTP auth (Bearer of the local auth token) so no new
// trust path is created — the SSH channel authenticated the DEVICE; the loopback
// call authenticates as this box's own agent, and the handler does its normal
// validation.
// dispatchLocalAgent performs the single whitelisted loopback call and returns
// its body + an exit code. Used by BOTH the forced-command CLI and the embedded
// SSH control server. It reuses the agent's own HTTP auth (Bearer of the local
// auth token) so no new trust path is created — the SSH channel authenticated the
// DEVICE; the loopback call authenticates as this box's own agent, and the
// handler does its normal validation.
func dispatchLocalAgent(method, path string, body []byte) (out []byte, exit int) {
	token := ""
	if cfg, err := LoadConfig(); err == nil && cfg != nil {
		token = strings.TrimSpace(cfg.AuthToken)
	}
	base := localAgentBaseURL()
	var rdr io.Reader
	if len(body) > 0 {
		rdr = strings.NewReader(string(body))
	}
	httpReq, err := http.NewRequest(method, base+path, rdr)
	if err != nil {
		return []byte("ssh-session: bad request: " + err.Error() + "\n"), 1
	}
	if token != "" {
		httpReq.Header.Set("Authorization", "Bearer "+token)
	}
	if len(body) > 0 {
		httpReq.Header.Set("Content-Type", "application/json")
	}
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(httpReq)
	if err != nil {
		// The out-of-band channel reached us, but our own daemon didn't answer —
		// name it so the agentic self-heal on the other end knows the AGENT is
		// down, not the transport.
		return []byte("ssh-session: local agent did not answer (is `yaver serve` running?): " + err.Error() + "\n"), 1
	}
	defer resp.Body.Close()
	payload, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return payload, 1
	}
	return payload, 0
}
