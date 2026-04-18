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
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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

	base, token, err := remoteAgentBaseAndToken(deviceID)
	if err != nil {
		return 0, nil, fmt.Errorf("resolve device: %w", err)
	}

	if method == "" {
		method = http.MethodPost
	}
	var reader io.Reader
	if len(bodyJSON) > 0 {
		reader = bytes.NewReader(bodyJSON)
	}

	url := strings.TrimRight(base, "/") + path
	req, err := http.NewRequestWithContext(ctx, method, url, reader)
	if err != nil {
		return 0, nil, err
	}
	if strings.TrimSpace(token) != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	pw, err := relayPasswordForBase(base)
	if err != nil {
		return 0, nil, err
	}
	if pw != "" {
		req.Header.Set("X-Relay-Password", pw)
	}
	if len(bodyJSON) > 0 {
		req.Header.Set("Content-Type", "application/json")
	}
	// Audit trail: the target's activity log should record which machine
	// initiated this proxied call, not just the original user.
	req.Header.Set("X-Yaver-Proxied-By", localDeviceID())
	req.Header.Set("X-Yaver-Proxied-Tool", toolName)

	client := &http.Client{Timeout: 120 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()

	raw, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		return resp.StatusCode, nil, readErr
	}
	return resp.StatusCode, raw, nil
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
