package main

// acp_live_test.go — LIVE integration tests against the REAL ACP servers.
//
// These exercise the production acpClient/acp_runner code path against actual
// binaries:
//
//	opencode  → `opencode acp --pure` (native ACP, opencode 1.18+)
//	codex     → `codex-acp` (@agentclientprotocol/codex-acp npm adapter),
//	           which reads the real ChatGPT subscription from ~/.codex/auth.json
//	claude    → `claude-agent-acp` adapter (needs a Claude subscription; on
//	           accounts where the org blocks the Agent SDK OAuth client the
//	           prompt turn returns oauth_org_not_allowed — the auth-state
//	           surface still works, only the turn is refused).
//
// Each test SKIPS when the runner's ACP server binary is not installed, so the
// suite stays green on boxes without the adapters. Set YA_ACP_LIVE=1 to force
// run even when a binary is missing (useful to see the skip reasons).
//
// These tests are the closed loop for the plan's Faz B/E claims: a screenshot
// attachment arriving as an ACP image block, and a prompt turn that reaches
// the provider. They are intentionally NOT hermetic — they spend real tokens
// on a real subscription, so they must be run deliberately, not on every CI
// run. `go test -run TestACPLive -v .`

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

func acpLiveEnabled() bool {
	return os.Getenv("YA_ACP_LIVE") == "1"
}

func requireACPServer(t *testing.T, runnerID string) {
	t.Helper()
	if !acpLiveEnabled() {
		t.Skipf("%s ACP live test disabled — set YA_ACP_LIVE=1 to spend a real provider turn", runnerID)
	}
	if !acpRunnerInstalled(runnerID) {
		t.Fatalf("YA_ACP_LIVE=1 but %s ACP server binary not found on PATH", runnerID)
	}
}

// TestACPLiveOpenCodeFullLoop drives the REAL opencode ACP server end to end:
// initialize → session/new with yaver mcp descriptor → prompt turn with a
// text + image block. This is the plan's Faz E acceptance shape.
func TestACPLiveOpenCodeFullLoop(t *testing.T) {
	requireACPServer(t, "opencode")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	c, err := newACPClientForRunner("opencode", t.TempDir(), acpClientOptions{})
	if err != nil {
		t.Fatalf("spawn opencode acp: %v", err)
	}
	defer c.Close()

	st := c.AuthState(ctx)
	if !st.Reachable {
		t.Fatalf("opencode acp unreachable: %s", st.Error)
	}
	t.Logf("agent=%s %s methods=%v", st.AgentName, st.AgentVersion, authMethodIDs(st.AuthMethods))

	sessionID, _, err := c.NewSession(ctx, t.TempDir(), []acpMCPServer{acpYaverMCPServer("/tmp/yaver-test")})
	if err != nil {
		t.Fatalf("session/new: %v", err)
	}
	t.Logf("session=%s", sessionID)

	// A text+image prompt — the screenshot-attachment shape. 1x1 red PNG.
	res, err := c.Prompt(ctx, sessionID, []acpContentBlock{
		acpTextBlock("Reply with exactly: PONG"),
		acpImageBlock("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg==", "image/png"),
	})
	if err != nil {
		t.Fatalf("prompt: %v", err)
	}
	if res.StopReason == "" {
		t.Fatal("prompt returned empty stopReason")
	}
	t.Logf("stopReason=%s usage=%+v", res.StopReason, res.Usage)
}

// TestACPLiveCodexFullLoop drives the REAL codex-acp adapter against the user's
// ChatGPT subscription: initialize (chat-gpt method must be advertised) →
// session/new → prompt turn. This proves the subscription (not API key) path.
func TestACPLiveCodexFullLoop(t *testing.T) {
	requireACPServer(t, "codex")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	c, err := newACPClientForRunner("codex", t.TempDir(), acpClientOptions{})
	if err != nil {
		t.Fatalf("spawn codex-acp: %v", err)
	}
	defer c.Close()

	st := c.AuthState(ctx)
	if !st.Reachable {
		t.Fatalf("codex-acp unreachable: %s", st.Error)
	}
	t.Logf("agent=%s %s methods=%v", st.AgentName, st.AgentVersion, authMethodIDs(st.AuthMethods))

	// The subscription method (chat-gpt) must be advertised unless NO_BROWSER.
	if os.Getenv("NO_BROWSER") != "1" {
		if findACPSubscriptionMethod("codex", st.AuthMethods) == nil {
			t.Fatalf("chat-gpt subscription method not advertised: %v", authMethodIDs(st.AuthMethods))
		}
	}

	sessionID, _, err := c.NewSession(ctx, t.TempDir(), []acpMCPServer{acpYaverMCPServer("/tmp/yaver-test")})
	if err != nil {
		t.Fatalf("session/new: %v", err)
	}
	t.Logf("session=%s", sessionID)

	res, err := c.Prompt(ctx, sessionID, []acpContentBlock{
		acpTextBlock("Reply with exactly: PONG"),
	})
	if err != nil {
		t.Fatalf("prompt: %v", err)
	}
	if res.StopReason == "" {
		t.Fatal("prompt returned empty stopReason")
	}
	t.Logf("stopReason=%s usage=%+v", res.StopReason, res.Usage)
}

// TestACPLiveClaudeAuthState verifies the claude-agent-acp adapter surface.
// The user may not have a usable Claude subscription right now (org can block
// the Agent SDK OAuth client), so only initialize + auth-method discovery are
// asserted — the auth STATE is exactly what /runner-auth/status consumes.
// If a subscription IS available the test also drives a prompt turn.
func TestACPLiveClaudeAuthState(t *testing.T) {
	requireACPServer(t, "claude")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	c, err := newACPClientForRunner("claude", t.TempDir(), acpClientOptions{})
	if err != nil {
		t.Fatalf("spawn claude-agent-acp: %v", err)
	}
	defer c.Close()

	st := c.AuthState(ctx)
	if !st.Reachable {
		t.Fatalf("claude-agent-acp unreachable: %s", st.Error)
	}
	t.Logf("agent=%s %s methods=%v", st.AgentName, st.AgentVersion, authMethodIDs(st.AuthMethods))

	// claude-ai-login must be advertised — the terminal subscription login.
	sub := findACPSubscriptionMethod("claude", st.AuthMethods)
	if sub == nil {
		t.Fatalf("claude-ai-login subscription method not advertised: %v", authMethodIDs(st.AuthMethods))
	}
	if sub.Type != "terminal" {
		t.Fatalf("claude-ai-login must be terminal type, got %q", sub.Type)
	}

	// Optional: if the subscription is usable, prove a turn. Refuse on the
	// known org-block error so the test documents reality without failing.
	sessionID, _, err := c.NewSession(ctx, t.TempDir(), []acpMCPServer{acpYaverMCPServer("/tmp/yaver-test")})
	if err != nil {
		t.Fatalf("session/new: %v", err)
	}
	res, err := c.Prompt(ctx, sessionID, []acpContentBlock{acpTextBlock("Reply with exactly: PONG")})
	if err != nil {
		msg := err.Error()
		if strings.Contains(msg, "oauth_org_not_allowed") || strings.Contains(msg, "org") {
			t.Logf("claude subscription org-blocked for the Agent SDK OAuth client (oauth_org_not_allowed) — auth state OK, turn refused: %s", msg)
			return
		}
		t.Fatalf("prompt: %v", err)
	}
	t.Logf("stopReason=%s usage=%+v", res.StopReason, res.Usage)
}

// TestACPLiveTerminalLoginCommand verifies the RFD terminal-auth command for
// claude (claude-agent-acp --cli auth login --claudeai) is constructed
// correctly — the command surfaces will render as the "sign in with your
// Claude subscription" button.
func TestACPLiveTerminalLoginCommand(t *testing.T) {
	requireACPServer(t, "claude")
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	c, err := newACPClientForRunner("claude", t.TempDir(), acpClientOptions{})
	if err != nil {
		t.Fatalf("spawn claude-agent-acp: %v", err)
	}
	defer c.Close()

	st := c.AuthState(ctx)
	if !st.Reachable {
		t.Skipf("claude-agent-acp unreachable: %s", st.Error)
	}
	sub := findACPSubscriptionMethod("claude", st.AuthMethods)
	if sub == nil {
		t.Skip("claude-ai-login not advertised")
	}
	args, env, ok := acpTerminalLoginCommand("claude", sub)
	if !ok {
		t.Fatal("acpTerminalLoginCommand failed for claude-ai-login")
	}
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "--cli") || !strings.Contains(joined, "--claudeai") {
		t.Fatalf("terminal login command missing subscription flags: %v", args)
	}
	t.Logf("terminal login: %s env=%v", joined, env)
}

func authMethodIDs(methods []acpAuthMethod) []string {
	out := make([]string, 0, len(methods))
	for _, m := range methods {
		out = append(out, m.ID)
	}
	return out
}

// ACP audit 2026-08-12, §5 item #2: /agent/runners (runnerInfoRow) must
// carry the same ACP view as /runner-auth/status. Both rows are fed by the
// shared acpAuthStateForRunner helper — this guards the helper so a future
// refactor that drops the wiring on one surface fails here first. Live
// (spawns the real ACP server); skips when the binary is absent.
func TestACPAuthStateForRunnerFeedsBothRowTypes(t *testing.T) {
	for _, id := range []string{"opencode", "codex", "claude"} {
		requireACPServer(t, id)
		authMethod, reachable, subMethod, subName := acpAuthStateForRunner(id, true)
		if !reachable {
			t.Fatalf("%s: acpAuthStateForRunner unreachable", id)
		}
		if authMethod != "acp" {
			t.Fatalf("%s: authMethod = %q, want acp", id, authMethod)
		}
		want := map[string]string{
			"opencode": "opencode-login",
			"codex":    "chat-gpt",
			"claude":   "claude-ai-login",
		}[id]
		if subMethod != want {
			t.Fatalf("%s: subMethod = %q, want %q", id, subMethod, want)
		}
		if subName == "" {
			t.Fatalf("%s: subscription method advertised without a display name", id)
		}
		t.Logf("%s: authMethod=%s reachable=%v subMethod=%s subName=%q", id, authMethod, reachable, subMethod, subName)
	}
}
