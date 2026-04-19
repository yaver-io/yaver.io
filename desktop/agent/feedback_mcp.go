package main

// feedback_mcp.go — tiny shim used by the feedback_* MCP cases so
// they hit the existing /feedback HTTP handlers without duplicating
// the DB access layer. Same pattern the CLI uses (feedback_cmd.go),
// just called in-process so auth is the session bearer from
// LoadConfig rather than a cross-host round-trip.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// feedbackHttpMCP issues a request against 127.0.0.1:18080 with the
// owner's auth token loaded from ~/.yaver/config.json. Returns the
// raw body, HTTP status, and any transport error. When the daemon
// isn't running (MCP is hit from a one-shot `yaver mcp` invocation),
// we still try — the same retry-with-ensureDaemonAlive path that
// session_cmd.go uses could be added here if a need arises.
func (s *HTTPServer) feedbackHttpMCP(method, path string, body interface{}) ([]byte, int, error) {
	cfg, err := LoadConfig()
	if err != nil || cfg == nil || cfg.AuthToken == "" {
		return nil, 0, fmt.Errorf("not signed in — run 'yaver auth'")
	}
	var reader io.Reader
	if body != nil {
		buf, _ := json.Marshal(body)
		reader = bytes.NewReader(buf)
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
