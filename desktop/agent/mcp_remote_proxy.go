package main

// mcp_remote_proxy.go — forwards an MCP tool call to another Yaver agent
// owned by the same user, identified by device_id. Reuses the existing
// remoteAgentBaseAndToken / remoteAgentJSON plumbing so auth, relay-password
// resolution, and direct-vs-relay selection all go through one code path.
//
// Layer contract (matches REMOTE_WORKER.md §E):
//
//   - Empty device_id → returns errProxyLocal; caller runs the local handler.
//   - device_id matches the current device → same; local-handler path.
//   - device_id set to another device → issues the HTTP call on the caller's
//     behalf. Response body is returned verbatim so MCP callers can decode
//     the payload their tool expects.
//
// Layer-4 secrets tools (vault_*, sdk_token_*, env_*) must reject a
// non-empty device_id before ever calling proxyToDevice — see
// refuseRemoteLayer4 below. We still enforce the rule here as a belt +
// braces check.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// errProxyLocal is returned when the caller should execute the local
// handler for a tool — either device_id was empty or it named the current
// device. MCP tool dispatchers branch on errors.Is(err, errProxyLocal) to
// fall through to local execution.
var errProxyLocal = errors.New("run local handler")

// errLayer4Remote is returned when a Layer-4 (secrets/vault/token) tool is
// called with a non-empty device_id. This is a hard rule — secrets never
// cross machines.
var errLayer4Remote = errors.New("this tool is local-only and cannot be proxied (device_id not allowed)")

// layer4Tools enumerates every MCP tool whose implementation reads or
// writes secrets on the local machine and therefore must refuse device_id.
// Kept as a map for O(1) lookup in hot paths.
var layer4Tools = map[string]bool{
	// Vault
	"vault_set":    true,
	"vault_get":    true,
	"vault_list":   true,
	"vault_delete": true,
	// SDK tokens
	"sdk_token_create": true,
	"sdk_token_list":   true,
	"sdk_token_rotate": true,
	"sdk_token_revoke": true,
	// Env / deploy creds
	"env_import":       true,
	"env_inject":       true,
	"deploy_cred_set":  true,
	"deploy_cred_list": true,
}

// refuseRemoteLayer4 reports whether toolName belongs to Layer 4 AND was
// called with a non-empty device_id. Callers should bail out with
// errLayer4Remote in that case.
func refuseRemoteLayer4(toolName, deviceID string) error {
	if strings.TrimSpace(deviceID) == "" {
		return nil
	}
	if layer4Tools[toolName] {
		return errLayer4Remote
	}
	return nil
}

// localDeviceID returns the current machine's deviceId from config, or ""
// if config is unreadable. Used for local-handler short-circuiting so a
// tool call with device_id matching ourselves never hits the network.
func localDeviceID() string {
	cfg, err := LoadConfig()
	if err != nil || cfg == nil {
		return ""
	}
	return strings.TrimSpace(cfg.DeviceID)
}

// proxyToDevice forwards an HTTP call to the Yaver agent identified by
// deviceID. method is typically "GET" or "POST"; path is the agent-local
// HTTP path (e.g. "/dev/start"); bodyJSON is the raw JSON request body
// (may be nil). Returns (statusCode, responseBody, err).
//
// On success the response bytes are returned verbatim so the MCP tool
// dispatcher can decode whichever shape the original handler returned.
//
// Callers must check the returned error first:
//
//   - errors.Is(err, errProxyLocal)   → run the local handler
//   - errors.Is(err, errLayer4Remote) → surface as MCP error
//   - other non-nil err               → network / auth failure, surface as-is
func proxyToDevice(ctx context.Context, toolName, deviceID, method, path string, bodyJSON []byte) (int, []byte, error) {
	return proxyToDeviceAs(ctx, toolName, deviceID, method, path, bodyJSON, "")
}

// proxyToDeviceAs is proxyToDevice but forwards bearerOverride (the ORIGINAL
// caller's user bearer) as the Authorization to the target, instead of this
// agent's own device token. This is the correct mesh identity model: the target
// validates the REAL user, so cross-device control works through ANY gateway
// regardless of the gateway's own token/userId (e.g. after an account merge left
// the gateway's token resolving to a stale id). It's also strictly more secure —
// a forwarded user bearer is only accepted by devices that user actually owns,
// so a gateway can't act as a different identity. The relay-layer auth (the
// __rp password baked into the candidate) is unchanged; only the agent-level
// Bearer is overridden. bearerOverride == "" keeps the legacy gateway-token
// behaviour (MCP/CLI callers that have no inbound user bearer).
func proxyToDeviceAs(ctx context.Context, toolName, deviceID, method, path string, bodyJSON []byte, bearerOverride string) (int, []byte, error) {
	deviceID = strings.TrimSpace(deviceID)
	if deviceID == "" {
		return 0, nil, errProxyLocal
	}
	if err := refuseRemoteLayer4(toolName, deviceID); err != nil {
		return 0, nil, err
	}
	if local := localDeviceID(); local != "" && deviceID == local {
		return 0, nil, errProxyLocal
	}

	candidates, token, err := resolveRemoteAgentCandidates(deviceID)
	if err != nil {
		return 0, nil, fmt.Errorf("resolve device: %w", err)
	}
	if b := strings.TrimSpace(bearerOverride); b != "" {
		token = b // forward the real user's bearer, not this gateway's token
	}

	if method == "" {
		method = http.MethodPost
	}
	for i := range candidates {
		if candidates[i].Headers == nil {
			candidates[i].Headers = map[string]string{}
		}
		candidates[i].Headers["X-Yaver-Proxied-Tool"] = toolName
	}
	_, status, raw, err := doRemoteAgentRequest(ctx, candidates, token, method, path, bodyJSON, 120*time.Second)
	if err != nil {
		return 0, nil, err
	}
	return status, raw, nil
}

// proxyToDeviceJSON is a convenience helper for MCP handlers that want the
// response decoded as a JSON object. Returns errProxyLocal when local
// execution is appropriate.
func proxyToDeviceJSON(ctx context.Context, toolName, deviceID, method, path string, body any) (map[string]any, error) {
	var raw []byte
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		raw = b
	}
	status, respBytes, err := proxyToDevice(ctx, toolName, deviceID, method, path, raw)
	if err != nil {
		return nil, err
	}
	if status >= 400 {
		msg := strings.TrimSpace(string(respBytes))
		if msg == "" {
			msg = http.StatusText(status)
		}
		return nil, fmt.Errorf("remote %s %s: HTTP %d: %s", method, path, status, msg)
	}
	if len(respBytes) == 0 {
		return map[string]any{}, nil
	}
	var out map[string]any
	if err := json.Unmarshal(respBytes, &out); err != nil {
		// Some agent endpoints return raw text; expose it under "raw".
		return map[string]any{"raw": string(respBytes)}, nil
	}
	return out, nil
}
