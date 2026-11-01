package main

// acp_runner.go — per-runner ACP launch + subscription-auth mapping.
//
// ACP support per runner, verified live 2026-08-11/12:
//
//	opencode  → NATIVE. `opencode acp --pure --cwd <dir>` speaks ACP v1 over
//	            stdio directly. Auth method: `opencode-login`.
//	claude    → ADAPTER. Claude Code has no native ACP flag (verified
//	            `claude --help`); the ACP server is the Claude Agent SDK
//	            adapter `@agentclientprotocol/claude-agent-acp` (npm). It
//	            reads the SAME subscription OAuth as Claude Code
//	            (~/.claude/.credentials.json / Keychain). Auth methods:
//	            `claude-ai-login` (TERMINAL type — subscription, args
//	            `--cli auth login --claudeai`, which the adapter forwards to
//	            the real `claude` CLI) and `console-login` (API-key billing).
//	            NOTE: on the user's account the Agent SDK OAuth client is
//	            org-blocked (`oauth_org_not_allowed` on prompt) even though
//	            `claude auth status` shows subscription max — an Anthropic
//	            org policy, not a Yaver defect; the terminal login path
//	            (`--cli auth login --claudeai`) is the workaround and is the
//	            ACP-sanctioned terminal-auth method.
//	codex     → ADAPTER. Codex CLI has no native ACP (verified `codex --help`);
//	            the ACP server is `@agentclientprotocol/codex-acp` (npm). It
//	            reads the SAME ChatGPT subscription as the Codex CLI
//	            (~/.codex/auth.json). Auth methods: `chat-gpt` (subscription)
//	            and `api-key`. Set NO_BROWSER=1 to hide the browser-based
//	            chat-gpt method on headless boxes.
//
// Security invariants: these adapters run ON the box and read the user's OWN
// subscription credentials there — nothing is copied off-machine, matching
// the plan's §6 rule "subscription tokens never leave the machine". The ACP
// layer is additive: every caller falls back to the existing probe path
// (runner_auth.go) when the ACP server cannot be reached or the adapter is
// not installed.

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
	"time"
)

// acpRunnerSpec describes how to launch one runner's ACP server.
type acpRunnerSpec struct {
	RunnerID string
	// Command is the resolved binary: opencode, claude-agent-acp, codex-acp.
	Command string
	// BaseArgs are the fixed argv before --cwd-style flags ("acp" for
	// opencode; empty for the adapters, which take the ACP connection
	// directly from stdio).
	BaseArgs []string
	// ExtraArgs are appended per launch (e.g. --pure for opencode).
	ExtraArgs []string
	// Env are ADDITIONAL environment pairs (NAME=value) merged over the
	// inherited environment. NO_BROWSER=1 for codex hides the browser
	// chat-gpt method.
	Env []string
	// SubscriptionAuthMethod is the ACP auth method id that represents the
	// SUBSCRIPTION login (not API key) for this runner.
	SubscriptionAuthMethod string
	// APIAuthMethod is the ACP auth method id for API-key auth, "" if the
	// runner does not advertise one.
	APIAuthMethod string
	// DisplayName is what surfaces should show, e.g. "Claude Agent (via ACP)".
	DisplayName string
}

// acpAdapterCommand is the npm binary name for each adapter-backed runner.
const (
	acpCodexAdapterBin = "codex-acp"
)

// acpRunnerSpecFor resolves the ACP launch spec for a runner. ok=false means
// this runner has no ACP server available on this machine (binary missing).
func acpRunnerSpecFor(runnerID string) (acpRunnerSpec, bool) {
	switch normalizeRunnerID(runnerID) {
	case "opencode":
		bin := resolveRunnerBinary("opencode")
		if bin == "" {
			return acpRunnerSpec{}, false
		}
		return acpRunnerSpec{
			RunnerID:               "opencode",
			Command:                bin,
			BaseArgs:               []string{"acp"},
			ExtraArgs:              []string{"--pure"},
			SubscriptionAuthMethod: "opencode-login",
			DisplayName:            "OpenCode",
		}, true
	case "codex":
		bin := resolveRunnerBinary(acpCodexAdapterBin)
		if bin == "" {
			return acpRunnerSpec{}, false
		}
		spec := acpRunnerSpec{
			RunnerID:               "codex",
			Command:                bin,
			SubscriptionAuthMethod: "chat-gpt",
			APIAuthMethod:          "api-key",
			DisplayName:            "Codex (via ACP)",
		}
		// Headless boxes cannot complete a browser chat-gpt login; hiding
		// the method makes the surface tell the truth ("API key only")
		// instead of advertising a flow that cannot finish.
		if os.Getenv("NO_BROWSER") == "1" {
			spec.SubscriptionAuthMethod = ""
		}
		return spec, true
	default:
		return acpRunnerSpec{}, false
	}
}

// acpRunnerInstalled reports whether this runner's ACP server binary exists.
func acpRunnerInstalled(runnerID string) bool {
	_, ok := acpRunnerSpecFor(runnerID)
	return ok
}

// newACPClientForRunner spawns the runner's ACP server over stdio.
func newACPClientForRunner(runnerID, workDir string, opts acpClientOptions) (*acpClient, error) {
	spec, ok := acpRunnerSpecFor(runnerID)
	if !ok {
		return nil, fmt.Errorf("acp: no %s ACP server (adapter not installed)", normalizeRunnerID(runnerID))
	}
	if opts.Command == "" {
		opts.Command = spec.Command
	}
	if opts.BaseArgs == nil {
		opts.BaseArgs = append([]string{}, spec.BaseArgs...)
	}
	if len(opts.ExtraArgs) == 0 {
		opts.ExtraArgs = append([]string{}, spec.ExtraArgs...)
	}
	// The adapter binaries must see the runner CLIs they wrap on PATH
	// (claude-agent-acp forwards `--cli …` to the real `claude`; codex-acp
	// spawns the bundled codex app server). They also inherit the user's
	// HOME so the subscription OAuth stores resolve.
	opts.Env = append(opts.Env, spec.Env...)
	if workDir == "" {
		workDir = "."
	}
	if opts.Cwd == "" {
		opts.Cwd = workDir
	}
	return newACPClient(opts)
}

// acpSubscriptionMethodID returns the ACP auth-method id that represents the
// subscription login for this runner ("" when the runner's ACP server hides
// it, e.g. codex under NO_BROWSER).
func acpSubscriptionMethodID(runnerID string) string {
	// Method identity is a protocol property, not an installation probe. The
	// previous implementation asked acpRunnerSpecFor, which returns ok=false
	// when an optional adapter binary is absent. That made every surface forget
	// which advertised method was subscription-backed on a fresh machine—the
	// exact machine where it needs to explain what can be installed or signed
	// in. Keep availability in acpRunnerInstalled/newACPClientForRunner and keep
	// this classifier deterministic.
	switch normalizeRunnerID(runnerID) {
	case "opencode":
		return "opencode-login"
	case "codex":
		if os.Getenv("NO_BROWSER") == "1" {
			return ""
		}
		return "chat-gpt"
	default:
		return ""
	}
}

// acpAuthMethodIsSubscription reports whether an advertised ACP auth method
// is the subscription login for this runner.
func acpAuthMethodIsSubscription(runnerID string, m acpAuthMethod) bool {
	want := acpSubscriptionMethodID(runnerID)
	return want != "" && m.ID == want
}

// findACPAuthMethod returns the advertised method with the given id, or nil.
func findACPAuthMethod(methods []acpAuthMethod, id string) *acpAuthMethod {
	for i := range methods {
		if methods[i].ID == id {
			return &methods[i]
		}
	}
	return nil
}

// findACPSubscriptionMethod returns the advertised subscription auth method
// for the runner, or nil. This is what surfaces use to render "claude.ai ·
// max · via ACP" and to decide whether the terminal-login button is shown.
func findACPSubscriptionMethod(runnerID string, methods []acpAuthMethod) *acpAuthMethod {
	return findACPAuthMethod(methods, acpSubscriptionMethodID(runnerID))
}

// ---------------------------------------------------------------------------
// Cached ACP probe for /runner-auth/status (and anything else that reads it).
//
// The status poll loop (runner_auth_health_loop.go) runs every 6h, and
// /runner-auth/status is hit on every dashboard load — spawning a node
// adapter per call would be wasteful and slow. The probe result is cached
// with a TTL; callers that need fresh truth (login completion, ?live=1) call
// invalidateACPProbeCache first.

type acpProbeCacheEntry struct {
	At   time.Time
	Auth acpAuthState
}

var acpProbeCache = struct {
	sync.Mutex
	m map[string]acpProbeCacheEntry
}{m: make(map[string]acpProbeCacheEntry)}

// acpProbeCacheTTL bounds how stale a cached ACP auth state may be before the
// next /runner-auth/status re-probes. 60s: fast enough that a completed
// terminal login shows up on the next surface poll, slow enough that the
// status endpoint never spawns an adapter more than once a minute.
const acpProbeCacheTTL = 60 * time.Second

// acpProbeTimeout bounds a single ACP initialize probe (spawn + handshake).
// node adapters cold-start slowly (1-3s); give them room but never let the
// status endpoint hang on one.
const acpProbeTimeout = 20 * time.Second

// probeACPAuthState returns the runner's ACP auth state, cached per
// acpProbeCacheTTL. force=true bypasses the cache (post-login verification).
// The result is purely additive — Reachable=false must never be interpreted
// as "not signed in", only as "ACP is not available, use the probe".
func probeACPAuthState(runnerID string, force bool) acpAuthState {
	runnerID = normalizeRunnerID(runnerID)
	acpProbeCache.Lock()
	if !force {
		if e, ok := acpProbeCache.m[runnerID]; ok && time.Since(e.At) < acpProbeCacheTTL {
			acpProbeCache.Unlock()
			return e.Auth
		}
	}
	acpProbeCache.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), acpProbeTimeout)
	defer cancel()

	client, err := newACPClientForRunner(runnerID, ".", acpClientOptions{})
	if err != nil {
		st := acpAuthState{Error: err.Error()}
		acpProbeCache.Lock()
		acpProbeCache.m[runnerID] = acpProbeCacheEntry{At: time.Now(), Auth: st}
		acpProbeCache.Unlock()
		return st
	}
	defer client.Close()

	st := client.AuthState(ctx)
	if st.Reachable {
		// Mark which advertised method is the subscription login so callers
		// don't each re-derive it.
		st.HasLoginMethod = findACPSubscriptionMethod(runnerID, st.AuthMethods) != nil
	}
	acpProbeCache.Lock()
	acpProbeCache.m[runnerID] = acpProbeCacheEntry{At: time.Now(), Auth: st}
	acpProbeCache.Unlock()
	return st
}

// invalidateACPProbeCache forces the next probeACPAuthState call to re-spawn
// the ACP server. Called after a terminal/agent login flow completes so the
// next status poll reflects the new credential immediately.
func invalidateACPProbeCache(runnerID string) {
	acpProbeCache.Lock()
	delete(acpProbeCache.m, normalizeRunnerID(runnerID))
	acpProbeCache.Unlock()
}

// acpSubscriptionAuthMethodForRunner is a convenience for callers that want
// the advertised subscription method without a full probe lifecycle.
func acpSubscriptionAuthMethodForRunner(runnerID string, force bool) (*acpAuthMethod, acpAuthState) {
	st := probeACPAuthState(runnerID, force)
	if !st.Reachable {
		return nil, st
	}
	return findACPSubscriptionMethod(runnerID, st.AuthMethods), st
}

// acpTerminalLoginCommand builds the interactive login command for a
// terminal-type ACP auth method, per the auth-methods RFD: launch the SAME
// configured agent program (the runner's ACP server binary) with the
// descriptor's args + env. For claude that is
// `claude-agent-acp --cli auth login --claudeai`, which the adapter forwards
// to the real Claude CLI — the ACP-sanctioned way to run the subscription
// login flow interactively.
func acpTerminalLoginCommand(runnerID string, m *acpAuthMethod) ([]string, []string, bool) {
	if m == nil || m.Type != "terminal" {
		return nil, nil, false
	}
	spec, ok := acpRunnerSpecFor(runnerID)
	if !ok {
		return nil, nil, false
	}
	args := append([]string{}, spec.BaseArgs...)
	args = append(args, m.Args...)
	env := append([]string{}, spec.Env...)
	for name, val := range m.Env {
		env = append(env, name+"="+val)
	}
	return args, env, true
}

// acpAgentLoginMethodID returns the method id to pass to `authenticate` when
// the subscription method is agent-type (codex chat-gpt / opencode-login),
// or "" when the subscription login is terminal-type (claude-ai-login) and
// must go through acpTerminalLoginCommand instead.
func acpAgentLoginMethodID(runnerID string, m *acpAuthMethod) string {
	if m == nil || m.Type == "terminal" {
		return ""
	}
	if !acpAuthMethodIsSubscription(runnerID, *m) {
		return ""
	}
	return m.ID
}

// logACPFallback logs a single-line note when the ACP layer for a runner is
// unavailable and the probe path is taking over. Kept as one place so the
// fallback story is greppable.
func logACPFallback(runnerID, reason string) {
	if strings.TrimSpace(reason) == "" {
		reason = "ACP server unavailable"
	}
	log.Printf("[acp] %s: %s — falling back to probe-based auth", normalizeRunnerID(runnerID), reason)
}
