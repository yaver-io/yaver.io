package main

// acp_client.go — Agent Client Protocol (ACP) v1 stdio client.
//
// ACP (https://agentclientprotocol.com) standardizes how an editor/agent-host
// talks to a coding agent over JSON-RPC 2.0. Yaver uses it as an ADDITIVE layer
// over the probe-based runner auth (runner_auth.go): when the runner exposes an
// ACP server (opencode does natively via `opencode acp`), we can drive
// subscription-login visibility and task turns over a standard protocol instead
// of scraping PTY output; when ACP is unavailable we fall back to the probes.
//
// Verified 2026-08-11 against opencode 1.18.15 (`opencode acp --pure`):
//   - Transport: newline-delimited JSON-RPC 2.0 over stdio. Each message MUST be
//     a single line, no embedded newlines, UTF-8.
//   - initialize   → agentInfo + agentCapabilities + authMethods.
//   - session/new  → REQUIRES `mcpServers` (an array; omitting it returns
//     -32602 "Invalid params"). This is the MCP-injection seam: pass the yaver
//     MCP (+ allowed external MCPs) here instead of OPENCODE_CONFIG.
//   - The prompt method is `session/prompt`, NOT `prompt` (which is
//     -32601 Method not found). `prompt` is an ARRAY of content blocks:
//     {type:"text",text} and {type:"image",data:<base64>,mimeType} both work —
//     a screenshot attachment is exactly this, which is the plan's core use
//     case. `auth/status` is NOT implemented by opencode yet (draft RFD) —
//     auth state still comes from the probe path; ACP contributes the
//     auth-methods surface and the turn transport.
//   - Server→client notifications: session/update (agent_message_chunk,
//     user_message_chunk, tool_call, usage_update, ...), terminal/*, fs/*,
//     elicitation/*. A client must read the stream continuously or the agent
//     blocks on a full stdout pipe.
//
// Security invariants (see the plan §6): the client spawns the runner locally
// and inherits Yaver's existing Authorization + relay boundaries; it never
// copies subscription tokens off-machine (terminal auth is a remote
// re-login flow, not a token copy).

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	osexec "os/exec"
	"strings"
	"sync"
	"time"
)

// acpProtocolVersion is the ACP v1 protocol version we speak. opencode 1.18.15
// negotiates 1; the v2 draft exists but is not what opencode acp serves yet.
const acpProtocolVersion = 1

// ACP bootstrap calls are bounded because they run before a prompt and may
// safely fall back to CLI. A started task prompt inherits the task context and
// has no artificial token/provider deadline; the explicit Stop action owns
// cancellation.
const (
	acpCallTimeoutDefault = 30 * time.Second
	acpInitTimeout        = 60 * time.Second
)

// acpJSONRPCError mirrors the JSON-RPC 2.0 error object.
type acpJSONRPCError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *acpJSONRPCError) Error() string {
	if e == nil {
		return "nil acp error"
	}
	return fmt.Sprintf("acp error %d: %s", e.Code, e.Message)
}

// acpRPCRequest / acpRPCResponse are the wire shapes. Params/Result are
// handled as raw JSON so each method can decode its own typed payload. ACP
// permits numeric *or string* JSON-RPC ids, so inbound ids stay raw until the
// response dispatcher proves it is one of Yaver's numeric outbound ids.
type acpRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int64           `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type acpRPCResponse struct {
	JSONRPC string           `json:"jsonrpc"`
	ID      json.RawMessage  `json:"id,omitempty"`
	Result  json.RawMessage  `json:"result,omitempty"`
	Error   *acpJSONRPCError `json:"error,omitempty"`
	// Notifications carry Method+Params and no ID. Server-initiated requests
	// carry Method+Params+ID and are dispatched to onRequest.
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
}

// acpRPCServerResponse is the client half of a server-initiated JSON-RPC
// request. Keep the raw id verbatim: an ACP server is allowed to use a string
// id even though Yaver's own outbound calls use monotonic numeric ids.
type acpRPCServerResponse struct {
	JSONRPC string           `json:"jsonrpc"`
	ID      json.RawMessage  `json:"id"`
	Result  json.RawMessage  `json:"result,omitempty"`
	Error   *acpJSONRPCError `json:"error,omitempty"`
}

// acpInitializeResult is the initialize response: what the agent advertises.
type acpInitializeResult struct {
	ProtocolVersion     int             `json:"protocolVersion"`
	AgentInfo           acpAgentInfo    `json:"agentInfo"`
	AgentCapabilities   acpCapabilities `json:"agentCapabilities"`
	AuthMethods         []acpAuthMethod `json:"authMethods"`
	SessionCapabilities map[string]any  `json:"-"`
}

// acpAgentInfo identifies the agent (e.g. OpenCode 1.18.15).
type acpAgentInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// acpCapabilities is the agent-side capability set from initialize.
type acpCapabilities struct {
	LoadSession         bool           `json:"loadSession"`
	Auth                map[string]any `json:"auth"`
	MCPCapabilities     map[string]any `json:"mcpCapabilities"`
	PromptCapabilities  map[string]any `json:"promptCapabilities"`
	SessionCapabilities map[string]any `json:"sessionCapabilities"`
}

// acpAuthMethod is one advertised authentication method. The `agent` type is
// the baseline (id → authenticate); `terminal` (auth-methods RFD) tells a
// capable client to launch the agent program interactively for login, then
// reconnect — the remote-login flow the plan wants for box-side subscriptions.
type acpAuthMethod struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Description string            `json:"description,omitempty"`
	Type        string            `json:"type,omitempty"` // "", "agent", "terminal", "env_var"
	Args        []string          `json:"args,omitempty"`
	Env         map[string]string `json:"env,omitempty"` // v1 wire shape: {name:value}
}

// acpAuthState answers "can this runner authenticate right now?" It is
// intentionally narrower than the probe's AuthConfigured/AuthVerified split:
// ACP's auth/status RFD is still draft and opencode does not implement it, so
// this is derived from initialize (server reachable + auth methods advertised)
// and the caller layers the probe's verified/proof logic on top.
type acpAuthState struct {
	Reachable    bool   `json:"reachable"`
	AgentName    string `json:"agentName,omitempty"`
	AgentVersion string `json:"agentVersion,omitempty"`
	// AuthMethods present means the agent advertises at least one way to sign
	// in (terminal login, agent login, env vars...). Absence means "no login
	// path advertised" — the runner may still work via env/API keys.
	AuthMethods    []acpAuthMethod `json:"authMethods,omitempty"`
	HasLoginMethod bool            `json:"hasLoginMethod"`
	Error          string          `json:"error,omitempty"`
}

// acpSessionNewParams is the session/new request. mcpServers is REQUIRED by
// opencode (verified: omitting it returns -32602). Each entry is an MCP server
// descriptor — this is how yaver MCP + allowed external MCPs ride into the
// runner in ACP mode (the screenshot-Read-tool path).
type acpSessionNewParams struct {
	Cwd                   string         `json:"cwd"`
	MCPServers            []acpMCPServer `json:"mcpServers"`
	AdditionalDirectories []string       `json:"additionalDirectories,omitempty"`
}

// acpMCPServer mirrors opencode's MCP server descriptor union:
//
//	http:  {type:"http",  name, url, headers:[{name,value}]}
//	sse:   {type:"sse",   name, url, headers:[{name,value}]}
//	stdio: {name, command, args:[], env:[{name,value}]}   (no "type" field)
//
// The stdio form deliberately omits "type" — opencode's schema union rejects
// nothing extra but the descriptor is defined without it.
type acpMCPServer struct {
	Type    string         `json:"type,omitempty"` // "http" | "sse"; stdio omits
	Name    string         `json:"name"`
	URL     string         `json:"url,omitempty"`
	Headers []acpMCPHeader `json:"headers,omitempty"`
	Command string         `json:"command,omitempty"`
	Args    []string       `json:"args,omitempty"`
	Env     []acpMCPEnvVar `json:"env,omitempty"`
}

// MarshalJSON guarantees the array fields serialize as [] rather than being
// omitted when empty. opencode's session/new schema is a strict zod union and
// REJECTS a stdio descriptor whose env is missing — verified live: omitting
// env returns -32602 "expected array, received undefined", with env:[] the
// same descriptor is accepted. codex-acp is lenient, but [] everywhere is
// valid for both, so normalize to the strictest consumer.
//
// NOTE: the arrays are declared WITHOUT omitempty here deliberately. Under
// omitempty Go drops a len-0 slice from the JSON entirely, which is exactly
// the -32602 failure we are fixing.
func (m acpMCPServer) MarshalJSON() ([]byte, error) {
	type flat struct {
		Type    string         `json:"type,omitempty"`
		Name    string         `json:"name"`
		URL     string         `json:"url,omitempty"`
		Headers []acpMCPHeader `json:"headers"`
		Command string         `json:"command,omitempty"`
		Args    []string       `json:"args"`
		Env     []acpMCPEnvVar `json:"env"`
	}
	out := flat{
		Type:    m.Type,
		Name:    m.Name,
		URL:     m.URL,
		Headers: m.Headers,
		Command: m.Command,
		Args:    m.Args,
		Env:     m.Env,
	}
	if out.Args == nil {
		out.Args = []string{}
	}
	if out.Env == nil {
		out.Env = []acpMCPEnvVar{}
	}
	if out.Headers == nil {
		out.Headers = []acpMCPHeader{}
	}
	return json.Marshal(out)
}

type acpMCPHeader struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type acpMCPEnvVar struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// acpSessionNewResult carries the created session id + config options.
type acpSessionNewResult struct {
	SessionID     string            `json:"sessionId"`
	ConfigOptions []acpConfigOption `json:"configOptions,omitempty"`
}

// acpConfigOption is a session configuration selector (model / mode / effort).
type acpConfigOption struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Category     string `json:"category,omitempty"`
	Type         string `json:"type"`
	CurrentValue string `json:"currentValue"`
}

// acpSetConfigOptionParams is the stable ACP session-level configuration
// request. Agents advertise the available selectors from session/new, then
// the client applies only the selections it needs before the first prompt.
// This keeps model/effort selection out of a runner-specific CLI argv.
type acpSetConfigOptionParams struct {
	SessionID string `json:"sessionId"`
	ConfigID  string `json:"configId"`
	Value     string `json:"value"`
}

// acpContentBlock is one element of the session/prompt `prompt` ARRAY.
// text + image are the ones Yaver needs (task prose + screenshot attachment);
// audio / resource / resource_link exist in the spec but are not used here.
type acpContentBlock struct {
	Type     string `json:"type"` // "text" | "image"
	Text     string `json:"text,omitempty"`
	Data     string `json:"data,omitempty"`     // base64 (image)
	MimeType string `json:"mimeType,omitempty"` // e.g. image/png (image)
}

// acpTextBlock / acpImageBlock are convenience constructors.
func acpTextBlock(text string) acpContentBlock {
	return acpContentBlock{Type: "text", Text: text}
}

func acpImageBlock(base64Data, mimeType string) acpContentBlock {
	return acpContentBlock{Type: "image", Data: base64Data, MimeType: mimeType}
}

// acpSessionPromptParams is the session/prompt request. Note the method is
// `session/prompt` and `prompt` is an ARRAY (both verified against opencode).
type acpSessionPromptParams struct {
	SessionID string            `json:"sessionId"`
	Prompt    []acpContentBlock `json:"prompt"`
}

// acpPromptResult is the terminal state of a prompt turn.
type acpPromptResult struct {
	StopReason string    `json:"stopReason"`
	Usage      *acpUsage `json:"usage,omitempty"`
	MessageID  string    `json:"messageId,omitempty"`
}

type acpUsage struct {
	InputTokens  int `json:"inputTokens"`
	OutputTokens int `json:"outputTokens"`
	TotalTokens  int `json:"totalTokens"`
}

// acpSessionSummary is one entry from session/list.
type acpSessionSummary struct {
	SessionID string `json:"sessionId"`
	Cwd       string `json:"cwd,omitempty"`
	Title     string `json:"title,omitempty"`
	UpdatedAt string `json:"updatedAt,omitempty"`
}

// acpSessionUpdate is the payload of a server→client `session/update`
// notification. `content` is deliberately raw JSON: ACP uses a *single*
// ContentBlock object for agent_message_chunk, but uses an array of
// ToolCallContent for tool_call(_update). Treating both as []ContentBlock was
// a false green: Codex completed the edit while Yaver discarded every update.
// Keep the wire union at this boundary and project it into Yaver's stable
// presentation/raw lanes in task_acp.go.
type acpSessionUpdate struct {
	SessionID string `json:"sessionId"`
	Update    struct {
		SessionUpdate string          `json:"sessionUpdate"`
		MessageID     string          `json:"messageId,omitempty"`
		ToolCallID    string          `json:"toolCallId,omitempty"`
		Title         string          `json:"title,omitempty"`
		Status        string          `json:"status,omitempty"`
		Kind          string          `json:"kind,omitempty"`
		Content       json.RawMessage `json:"content,omitempty"`
		RawInput      json.RawMessage `json:"rawInput,omitempty"`
		RawOutput     json.RawMessage `json:"rawOutput,omitempty"`
		Meta          json.RawMessage `json:"_meta,omitempty"`
	} `json:"update"`
}

// acpNotifyHandler receives server→client notifications as they arrive.
// The handler must be cheap — it runs on the reader goroutine and blocks the
// whole stream while it executes.
type acpNotifyHandler func(method string, params json.RawMessage)

// acpRequestHandler answers an agent -> client request such as ACP form
// elicitation. It may wait for the human, so readLoop invokes it in its own
// goroutine; the ACP reader must continue draining notifications while a
// phone is considering the question.
type acpRequestHandler func(ctx context.Context, method string, params json.RawMessage) (json.RawMessage, *acpJSONRPCError)

// acpClient is a newline-delimited JSON-RPC 2.0 client over a runner's ACP
// stdio server. One client owns one subprocess. All calls are safe for
// concurrent use; responses are matched to callers by id.
type acpClient struct {
	cmd    *osexec.Cmd
	stdin  io.WriteCloser
	sc     *bufio.Scanner
	cancel context.CancelFunc
	done   chan struct{}

	mu        sync.Mutex
	writeMu   sync.Mutex
	nextID    int64
	pending   map[int64]chan acpRPCResponse
	onNotify  acpNotifyHandler
	onRequest acpRequestHandler

	initOnce sync.Once
	initRes  *acpInitializeResult
	initErr  error
}

// acpClientOptions configures the spawned ACP server process.
type acpClientOptions struct {
	// Command is the runner binary. Default: resolved `opencode` binary.
	Command string
	// BaseArgs are the fixed command arguments before ACP transport options.
	// Native OpenCode is invoked as `opencode acp --pure`; adapter binaries
	// already are ACP servers and deliberately have no `acp` subcommand.
	BaseArgs []string
	// Cwd is the working directory the runner boots in (project root).
	Cwd string
	// ExtraArgs are appended after the `acp` subcommand (e.g. --pure).
	ExtraArgs []string
	// Env overrides/extends the process environment (e.g. OPENCODE_CONFIG
	// for scoped provider config when not passing mcpServers per-session).
	Env []string
	// OnNotify receives server→client notifications. Optional.
	OnNotify acpNotifyHandler
	// OnRequest handles server→client requests. It is deliberately separate
	// from OnNotify: requests require a JSON-RPC response and can wait for a
	// human answer without blocking the reader loop.
	OnRequest acpRequestHandler
}

// newACPClient spawns the runner's ACP server over stdio and returns a client
// ready for initialize. It does not block on the handshake.
func newACPClient(opts acpClientOptions) (*acpClient, error) {
	command := strings.TrimSpace(opts.Command)
	if command == "" {
		command = resolveRunnerBinary("opencode")
	}
	if command == "" {
		return nil, fmt.Errorf("acp: no %s binary on PATH", "opencode")
	}
	baseArgs := opts.BaseArgs
	if baseArgs == nil {
		// Preserve the direct newACPClient default for native OpenCode callers.
		baseArgs = []string{"acp"}
	}
	args := append(append([]string{}, baseArgs...), opts.ExtraArgs...)
	ctx, cancel := context.WithCancel(context.Background())
	cmd := osexec.CommandContext(ctx, command, args...)
	if cwd := strings.TrimSpace(opts.Cwd); cwd != "" {
		cmd.Dir = cwd
	}
	cmd.Env = append(os.Environ(), opts.Env...)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("acp stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("acp stdout pipe: %w", err)
	}
	cmd.Stderr = nil // ACP allows logs on stderr; let them flow to /dev/null-ish

	if err := cmd.Start(); err != nil {
		cancel()
		return nil, fmt.Errorf("acp start %s: %w", command, err)
	}

	c := &acpClient{
		cmd:     cmd,
		stdin:   stdin,
		done:    make(chan struct{}),
		cancel:  cancel,
		pending: make(map[int64]chan acpRPCResponse),
	}
	if opts.OnNotify != nil {
		c.onNotify = opts.OnNotify
	}
	if opts.OnRequest != nil {
		c.onRequest = opts.OnRequest
	}
	go c.readLoop(stdout)
	go func() {
		<-ctx.Done()
		_ = cmd.Wait()
		close(c.done)
	}()
	return c, nil
}

// readLoop consumes the newline-delimited stream, dispatching responses by id
// and notifications to the handler. It exits when the process dies or the
// context is cancelled; any callers still waiting are answered with an error.
func (c *acpClient) readLoop(stdout io.Reader) {
	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		var msg acpRPCResponse
		if err := json.Unmarshal(line, &msg); err != nil {
			// A line that is not JSON is a protocol violation; the spec says
			// stdout carries ONLY valid ACP messages. Log and skip rather than
			// killing the session — but surface it so a bad agent is visible.
			log.Printf("[acp] non-JSON line on stdout: %.120q", string(line))
			continue
		}
		if msg.Method != "" {
			if !acpInboundRequestID(msg.ID) {
				// Server→client notification.
				if c.onNotify != nil {
					c.onNotify(msg.Method, msg.Params)
				}
				continue
			}
			// Server→client request. Do not make readLoop wait for a phone;
			// the agent can continue sending session/update notifications while
			// the request is pending.
			requestID := append(json.RawMessage(nil), msg.ID...)
			go c.answerServerRequest(requestID, msg.Method, msg.Params)
			continue
		}
		id, ok := acpNumericResponseID(msg.ID)
		if !ok {
			log.Printf("[acp] response without a numeric request id")
			continue
		}
		c.mu.Lock()
		ch, ok := c.pending[id]
		if ok {
			delete(c.pending, id)
		}
		c.mu.Unlock()
		if ok {
			ch <- msg
		}
	}
	// Stream ended (process exited / pipe closed). Fail every still-pending call.
	c.mu.Lock()
	for id, ch := range c.pending {
		close(ch)
		delete(c.pending, id)
	}
	c.mu.Unlock()
}

func acpInboundRequestID(id json.RawMessage) bool {
	trimmed := strings.TrimSpace(string(id))
	return trimmed != "" && trimmed != "null"
}

func acpNumericResponseID(id json.RawMessage) (int64, bool) {
	if !acpInboundRequestID(id) {
		return 0, false
	}
	var out int64
	if err := json.Unmarshal(id, &out); err != nil {
		return 0, false
	}
	return out, true
}

func (c *acpClient) answerServerRequest(id json.RawMessage, method string, params json.RawMessage) {
	result := json.RawMessage(`{}`)
	var rpcErr *acpJSONRPCError
	if c.onRequest == nil {
		rpcErr = &acpJSONRPCError{Code: -32601, Message: "Yaver does not support this ACP client request"}
	} else {
		result, rpcErr = c.onRequest(context.Background(), method, params)
		if len(result) == 0 && rpcErr == nil {
			result = json.RawMessage(`{}`)
		}
	}
	if err := c.writeJSONL(acpRPCServerResponse{JSONRPC: "2.0", ID: id, Result: result, Error: rpcErr}); err != nil {
		log.Printf("[acp] reply to %s request: %v", method, err)
	}
}

// call performs one JSON-RPC request and waits for its response. It returns
// an *acpJSONRPCError for protocol-level errors.
func (c *acpClient) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	raw, err := json.Marshal(params)
	if err != nil {
		return nil, fmt.Errorf("acp marshal %s: %w", method, err)
	}
	c.mu.Lock()
	if c.done != nil && isClosed(c.done) {
		c.mu.Unlock()
		return nil, fmt.Errorf("acp: %s: process is not running", method)
	}
	id := c.nextID
	c.nextID++
	ch := make(chan acpRPCResponse, 1)
	c.pending[id] = ch
	c.mu.Unlock()

	req := acpRPCRequest{JSONRPC: "2.0", ID: id, Method: method}
	if len(raw) > 0 && string(raw) != "null" {
		req.Params = raw
	}
	if err := c.writeJSONL(req); err != nil {
		c.dropPending(id, ch)
		return nil, fmt.Errorf("acp write %s: %w", method, err)
	}

	select {
	case <-ctx.Done():
		c.dropPending(id, ch)
		return nil, ctx.Err()
	case msg, ok := <-ch:
		if !ok {
			return nil, fmt.Errorf("acp %s: stream closed before response", method)
		}
		if msg.Error != nil {
			return nil, msg.Error
		}
		return msg.Result, nil
	}
}

// writeJSONL serializes all client requests and responses. An ACP task can
// receive an elicitation while another goroutine submits a follow-up; a pipe
// write is not a message boundary, so concurrent JSON writes must never be
// allowed to interleave.
func (c *acpClient) writeJSONL(value any) error {
	body, err := json.Marshal(value)
	if err != nil {
		return err
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	_, err = c.stdin.Write(append(body, '\n'))
	return err
}

func (c *acpClient) dropPending(id int64, ch chan acpRPCResponse) {
	c.mu.Lock()
	if c.pending[id] == ch {
		delete(c.pending, id)
	}
	c.mu.Unlock()
}

// Initialize performs the ACP handshake. Results are cached for the client's
// lifetime; concurrent callers share one handshake.
func (c *acpClient) Initialize(ctx context.Context) (*acpInitializeResult, error) {
	var res *acpInitializeResult
	var err error
	c.initOnce.Do(func() {
		capabilities := map[string]any{
			// auth.terminal opts us in to terminal-type auth methods
			// (auth-methods RFD). Without this, agents like
			// claude-agent-acp HIDE their terminal subscription login
			// (claude-ai-login) — verified live: methods=[] with an
			// empty capability set, [claude-ai-login, console-login]
			// with it. opencode's own capability set has no such gate,
			// but advertising it is harmless and correct everywhere.
			"auth": map[string]any{"terminal": true},
			// codex-acp's optional terminal-output extension streams only new
			// command bytes through `_meta.terminal_output`. Without this opt-in
			// it buffers command output and sends a single final snapshot, which
			// is both less useful on a phone and can grow quadratically.
			"_meta": map[string]any{"terminal_output": true},
		}
		// Do not claim form elicitation on a probe-only client. A task client
		// with an interaction handler maps it to Yaver's cross-surface
		// AgentQuestion flow and can actually answer it.
		if c.onRequest != nil {
			capabilities["elicitation"] = map[string]any{"form": map[string]any{}}
		}
		raw, cerr := c.call(ctx, "initialize", map[string]any{
			"protocolVersion":    acpProtocolVersion,
			"clientInfo":         map[string]any{"name": "yaver-agent", "version": version},
			"clientCapabilities": capabilities,
		})
		if cerr != nil {
			err = cerr
			return
		}
		var out acpInitializeResult
		if derr := json.Unmarshal(raw, &out); derr != nil {
			err = fmt.Errorf("acp initialize decode: %w", derr)
			return
		}
		res = &out
	})
	if err != nil {
		return nil, err
	}
	return res, nil
}

// AuthState is the ACP contribution to runner auth status: reachable + what
// the agent advertises. It deliberately does NOT claim verified credentials —
// the probe path owns AuthConfigured/AuthVerified (see runner_auth.go).
func (c *acpClient) AuthState(ctx context.Context) acpAuthState {
	st := acpAuthState{}
	init, err := c.Initialize(ctx)
	if err != nil {
		st.Error = err.Error()
		return st
	}
	st.Reachable = true
	if init.AgentInfo.Name != "" {
		st.AgentName = init.AgentInfo.Name
		st.AgentVersion = init.AgentInfo.Version
	}
	st.AuthMethods = init.AuthMethods
	for _, m := range init.AuthMethods {
		if m.Type == "terminal" || m.Type == "agent" {
			st.HasLoginMethod = true
		}
	}
	return st
}

// NewSession creates a session with the given MCP servers attached (yaver mcp
// + allowed external servers). Returns the session id and config options.
func (c *acpClient) NewSession(ctx context.Context, cwd string, mcpServers []acpMCPServer) (string, []acpConfigOption, error) {
	if cwd == "" {
		cwd = "."
	}
	if mcpServers == nil {
		mcpServers = []acpMCPServer{} // required by opencode; never omit
	}
	raw, err := c.call(ctx, "session/new", acpSessionNewParams{Cwd: cwd, MCPServers: mcpServers})
	if err != nil {
		return "", nil, err
	}
	var out acpSessionNewResult
	if derr := json.Unmarshal(raw, &out); derr != nil {
		return "", nil, fmt.Errorf("acp session/new decode: %w", derr)
	}
	if out.SessionID == "" {
		return "", nil, errors.New("acp session/new returned no sessionId")
	}
	return out.SessionID, out.ConfigOptions, nil
}

// SetSessionConfigOption applies one option advertised by session/new. The
// response contains the refreshed option set because changing a model can
// change the compatible reasoning and mode choices.
func (c *acpClient) SetSessionConfigOption(ctx context.Context, sessionID, configID, value string) ([]acpConfigOption, error) {
	if strings.TrimSpace(sessionID) == "" || strings.TrimSpace(configID) == "" {
		return nil, errors.New("acp session/set_config_option: sessionId and configId are required")
	}
	raw, err := c.call(ctx, "session/set_config_option", acpSetConfigOptionParams{
		SessionID: sessionID,
		ConfigID:  configID,
		Value:     value,
	})
	if err != nil {
		return nil, err
	}
	var out struct {
		ConfigOptions []acpConfigOption `json:"configOptions,omitempty"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("acp session/set_config_option decode: %w", err)
	}
	return out.ConfigOptions, nil
}

// Prompt sends one turn. content is the array of content blocks (text and/or
// image base64). The caller must read notifications via OnNotify to observe
// streaming chunks while this blocks.
func (c *acpClient) Prompt(ctx context.Context, sessionID string, content []acpContentBlock) (*acpPromptResult, error) {
	if len(content) == 0 {
		return nil, errors.New("acp prompt: empty content")
	}
	raw, err := c.call(ctx, "session/prompt", acpSessionPromptParams{SessionID: sessionID, Prompt: content})
	if err != nil {
		return nil, err
	}
	var out acpPromptResult
	if derr := json.Unmarshal(raw, &out); derr != nil {
		return nil, fmt.Errorf("acp session/prompt decode: %w", derr)
	}
	return &out, nil
}

// ListSessions returns the agent's session history.
func (c *acpClient) ListSessions(ctx context.Context) ([]acpSessionSummary, error) {
	raw, err := c.call(ctx, "session/list", map[string]any{})
	if err != nil {
		return nil, err
	}
	var out struct {
		Sessions []acpSessionSummary `json:"sessions"`
	}
	if derr := json.Unmarshal(raw, &out); derr != nil {
		return nil, fmt.Errorf("acp session/list decode: %w", derr)
	}
	return out.Sessions, nil
}

// CloseSession ends a session on the agent side. A no-op error (Method not
// found / unknown session) is tolerated so callers can close defensively.
func (c *acpClient) CloseSession(ctx context.Context, sessionID string) error {
	_, err := c.call(ctx, "session/close", map[string]any{"sessionId": sessionID})
	if err != nil {
		var rpcErr *acpJSONRPCError
		if errors.As(err, &rpcErr) && (rpcErr.Code == -32601 || rpcErr.Code == -32602) {
			return nil
		}
		return err
	}
	return nil
}

// Authenticate drives an agent-type ACP auth method (codex `chat-gpt`,
// opencode `opencode-login`). The adapter starts its own login flow — for
// codex that is the ChatGPT device/browser flow, whose URL/code the caller
// must surface to the user (the response or the adapter's stderr carries it).
// Terminal-type methods (claude-ai-login) must NOT go through authenticate;
// use acpTerminalLoginCommand (acp_runner.go) and run the login interactively
// per the auth-methods RFD.
func (c *acpClient) Authenticate(ctx context.Context, methodID string) error {
	if strings.TrimSpace(methodID) == "" {
		return errors.New("acp authenticate: empty methodId")
	}
	_, err := c.call(ctx, "authenticate", map[string]any{"methodId": methodID})
	if err != nil {
		return err
	}
	return nil
}

// Logout ends the runner's ACP session-level auth (ACP v1 `logout`).
// No-op on Method-not-found so callers can call it defensively.
func (c *acpClient) Logout(ctx context.Context) error {
	_, err := c.call(ctx, "logout", map[string]any{})
	if err != nil {
		var rpcErr *acpJSONRPCError
		if errors.As(err, &rpcErr) && rpcErr.Code == -32601 {
			return nil
		}
		return err
	}
	return nil
}

// ---------------------------------------------------------------------------
// MCP server descriptors for session/new — the injection seam.
//
// All three ACP servers (opencode native + both adapters) accept a
// `mcpServers` ARRAY in session/new (verified: omitting it errors -32602).
// The yaver MCP is a stdio descriptor (command `yaver mcp`); external MCPs
// are http descriptors. This is what makes the screenshot → Read tool path
// work in ACP mode: the yaver MCP arrives as a first-class tool.
// ---------------------------------------------------------------------------

// acpYaverMCPServer builds the stdio descriptor for the yaver MCP doorway.
func acpYaverMCPServer(yaverPath string) acpMCPServer {
	return acpMCPServer{
		Name:    "yaver",
		Command: yaverPath,
		Args:    []string{"mcp"},
	}
}

// acpExternalMCPServer builds an http descriptor for an external MCP.
func acpExternalMCPServer(srv ExternalMCPServer) acpMCPServer {
	desc := acpMCPServer{
		Type: "http",
		Name: srv.Name,
		URL:  srv.URL,
	}
	if srv.AuthToken != "" {
		desc.Headers = []acpMCPHeader{{Name: "Authorization", Value: "Bearer " + srv.AuthToken}}
	}
	return desc
}

// acpMCPServersForTask assembles the mcpServers array for a task session:
// the yaver MCP (unless explicitly deselected) plus the allowed external
// servers. Mirrors prepareRunnerMCPScope's includeYaverMcp sentinel semantics.
func acpMCPServersForTask(yaverPath string, servers []ExternalMCPServer, includeYaverMcp bool, task *Task) []acpMCPServer {
	var out []acpMCPServer
	if includeYaverMcp {
		yaver := acpYaverMCPServer(yaverPath)
		// 2026-09-10: an ACP Codex task completed its UI fix, then its
		// yaver_request_render failed with YAVER_TASK_ID missing. Adapters
		// need not pass their own environment to MCP children. Bind the
		// descriptor to this task explicitly; never inherit the daemon's
		// possibly unrelated task ID or copy host secrets to external MCPs.
		if task != nil {
			yaver.Env = []acpMCPEnvVar{
				{Name: "YAVER_TASK_ID", Value: strings.TrimSpace(task.ID)},
				{Name: "YAVER_TASK_SOURCE", Value: strings.TrimSpace(task.Source)},
			}
		}
		out = append(out, yaver)
	}
	for _, srv := range servers {
		out = append(out, acpExternalMCPServer(srv))
	}
	if out == nil {
		out = []acpMCPServer{}
	}
	return out
}

// Close shuts the subprocess down: close stdin (agents exit cleanly on EOF),
// then wait for the process with a grace period, then force-kill.
func (c *acpClient) Close() {
	c.cancel()
	_ = c.stdin.Close()
	select {
	case <-c.done:
	case <-time.After(3 * time.Second):
		if c.cmd.Process != nil {
			_ = c.cmd.Process.Kill()
		}
	}
}

func isClosed(ch chan struct{}) bool {
	select {
	case <-ch:
		return true
	default:
		return false
	}
}
