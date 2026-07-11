package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestConnectorElevation_ScopeHelpers locks the security property that a client
// cannot self-elevate: the marker is stripped from any client-supplied scope and
// only ever re-added on the consent form (handleOauthLogin).
func TestConnectorElevation_ScopeHelpers(t *testing.T) {
	if got := stripConnectorElevation("mcp " + mcpElevatedConnectorScope + " profile"); got != "mcp profile" {
		t.Fatalf("marker not stripped from client scope: %q", got)
	}
	if got := stripConnectorElevation("mcp"); got != "mcp" {
		t.Fatalf("plain scope altered: %q", got)
	}
	if !connectorScopeElevated(map[string]interface{}{"scope": "mcp " + mcpElevatedConnectorScope}) {
		t.Fatal("elevated scope not detected")
	}
	if connectorScopeElevated(map[string]interface{}{"scope": "mcp"}) {
		t.Fatal("plain scope wrongly read as elevated")
	}
	if connectorScopeElevated(map[string]interface{}{}) {
		t.Fatal("missing scope claim wrongly read as elevated")
	}
}

// TestAuthMCP_ElevatedConnectorGetsOpsNotExec verifies an owner-elevated
// connector token is stamped the ops surface (breadth via the grand-tool) but
// still cannot reach dangerous STANDALONE tools directly.
func TestAuthMCP_ElevatedConnectorGetsOpsNotExec(t *testing.T) {
	setTestOauthKey(t)
	s := &HTTPServer{}
	var sawAllowed, sawElevated string
	next := func(w http.ResponseWriter, r *http.Request) {
		sawAllowed = r.Header.Get("X-Yaver-AllowedTools")
		sawElevated = r.Header.Get("X-Yaver-Connector-Elevated")
		w.WriteHeader(200)
	}
	h := s.authMCP(next)

	at, _ := mintAccessToken("user1", "https://host/mcp", "mcp "+mcpElevatedConnectorScope, time.Hour)
	req := httptest.NewRequest("POST", "/mcp", nil)
	req.Header.Set("Authorization", "Bearer "+at)
	// A spoofed inbound allowlist must still be overwritten.
	req.Header.Set("X-Yaver-AllowedTools", "exec_command")
	rec := httptest.NewRecorder()
	h(rec, req)

	if rec.Code != 200 {
		t.Fatalf("elevated connector rejected: %d", rec.Code)
	}
	if sawElevated != "true" {
		t.Fatal("elevated flag not stamped")
	}
	if !strings.Contains(sawAllowed, "ops") {
		t.Fatalf("elevated connector should be granted the ops surface; allowed=%q", sawAllowed)
	}
	if strings.Contains(sawAllowed, "exec_command") || strings.Contains(sawAllowed, "write_file") {
		t.Fatalf("SECURITY: elevated connector exposes dangerous standalone tools: %q", sawAllowed)
	}

	// The stamped scope admits ops at dispatch but still denies a raw exec tool.
	if d := mcpToolDeniedByScope(reqWithAllowed(sawAllowed), "ops"); d != nil {
		t.Fatalf("ops wrongly denied for elevated connector: %v", d.Reason)
	}
	if d := mcpToolDeniedByScope(reqWithAllowed(sawAllowed), "exec_command"); d == nil {
		t.Fatal("SECURITY: exec_command not denied for elevated connector")
	}
}

// TestAuthMCP_DefaultConnectorStaysToys guards against a regression where the
// elevation change accidentally widens the DEFAULT (unelevated) connector.
func TestAuthMCP_DefaultConnectorStaysToys(t *testing.T) {
	setTestOauthKey(t)
	s := &HTTPServer{}
	var sawAllowed, sawElevated string
	next := func(w http.ResponseWriter, r *http.Request) {
		sawAllowed = r.Header.Get("X-Yaver-AllowedTools")
		sawElevated = r.Header.Get("X-Yaver-Connector-Elevated")
		w.WriteHeader(200)
	}
	h := s.authMCP(next)

	at, _ := mintAccessToken("user1", "https://host/mcp", "mcp", time.Hour)
	req := httptest.NewRequest("POST", "/mcp", nil)
	req.Header.Set("Authorization", "Bearer "+at)
	rec := httptest.NewRecorder()
	h(rec, req)

	if rec.Code != 200 {
		t.Fatalf("default connector rejected: %d", rec.Code)
	}
	if sawElevated == "true" {
		t.Fatal("SECURITY: unelevated connector marked elevated")
	}
	if !strings.Contains(sawAllowed, "calculate") {
		t.Fatalf("default connector lost its toy allowlist: %q", sawAllowed)
	}
	if strings.Contains(sawAllowed, "ops") {
		t.Fatalf("SECURITY: default connector was granted ops: %q", sawAllowed)
	}
}
