package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// resolveCompanyAIRuntime calls the Convex /company-ai/resolve route with the
// agent's owner token and returns the raw JSON resolution. The resolver checks
// team membership server-side and returns NO secrets — only the policy-resolved
// runtime (device + runner + model + provider + approvals + nextActions). This
// is the headless/MCP entry point to the same resolver the web/mobile/desktop
// surfaces use, so an agent (or an MCP-driven coding runner) can ask "what
// runtime am I allowed to use for this work kind" before dispatching.
func resolveCompanyAIRuntime(convexURL, token string, payload map[string]interface{}) (json.RawMessage, error) {
	convexURL = strings.TrimRight(strings.TrimSpace(convexURL), "/")
	if convexURL == "" {
		return nil, fmt.Errorf("convex URL not configured")
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	req, err := newBearerRequest("POST", convexURL+"/company-ai/resolve", token, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("/company-ai/resolve -> HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}
	return json.RawMessage(data), nil
}
