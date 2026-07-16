package main

// remote_runtime_mcp.go — MCP → local /remote-runtime/* proxy. P1 of
// the n2n plan (docs/architecture/N2N_IMPLEMENTATION_PLAN.md).
//
// The WebRTC remote-runtime lane (create / attach / control / frame)
// is HTTP-only today, so a runner can drive the *code* but cannot
// drive the *app*. This shim mirrors feedbackHttpMCP: MCP verbs hit
// the local agent HTTP on 127.0.0.1:18080 with the owner's bearer
// token so a runner can compose those HTTP calls into `runtime_*`
// tool responses without duplicating the manager access layer.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// remoteRuntimeHTTPMCP issues a JSON request against
// http://127.0.0.1:18080<path> with the owner's session bearer.
// Returns the raw body, HTTP status, and any transport error.
//
// GET calls should pass body == nil. POST/DELETE calls accept any
// json.Marshaler-able value (or nil).
func remoteRuntimeHTTPMCP(method, path string, body any) ([]byte, int, error) {
	cfg, err := LoadConfig()
	if err != nil || cfg == nil || cfg.AuthToken == "" {
		return nil, 0, fmt.Errorf("not signed in — run 'yaver auth'")
	}
	var reader io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return nil, 0, fmt.Errorf("marshal request body: %w", err)
		}
		reader = bytes.NewReader(buf)
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	req, err := http.NewRequest(method, "http://127.0.0.1:18080"+path, reader)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Authorization", "Bearer "+cfg.AuthToken)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	return out, resp.StatusCode, nil
}

// remoteRuntimeFrameJPEG fetches the current frame as a raw JPEG
// blob. Same as remoteRuntimeHTTPMCP for /frame but without the
// json Content-Type/decoding indirection — the frame handler
// returns image/jpeg directly.
func remoteRuntimeFrameJPEG(sessionID string) ([]byte, int, error) {
	if strings.TrimSpace(sessionID) == "" {
		return nil, 0, fmt.Errorf("sessionId is required")
	}
	return remoteRuntimeHTTPMCP(http.MethodGet, "/remote-runtime/sessions/"+sessionID+"/frame", nil)
}
