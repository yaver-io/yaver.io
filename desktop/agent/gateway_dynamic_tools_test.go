package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// writeRawConnector writes a connector manifest straight to the registry dir,
// bypassing Store's read-only validation. This lets a test seed an ACT/write
// capability (which Store would reject) to prove it is NOT surfaced as a tool.
func writeRawConnector(t *testing.T, dir string, c Connector) {
	t.Helper()
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		t.Fatalf("marshal connector %s: %v", c.ID, err)
	}
	if err := os.WriteFile(filepath.Join(dir, c.ID+".json"), data, 0600); err != nil {
		t.Fatalf("write connector %s: %v", c.ID, err)
	}
}

// readCap is a tiny helper to build a read capability with an api flow.
func readCap(id, title, path string) Capability {
	return Capability{
		ID:    id,
		Verb:  "get",
		Risk:  "read",
		Title: title,
		Flow:  CapabilityFlow{Type: "api", Method: "GET", Path: path},
	}
}

func toolByName(tools []map[string]interface{}, name string) (map[string]interface{}, bool) {
	for _, td := range tools {
		if n, _ := td["name"].(string); n == name {
			return td, true
		}
	}
	return nil, false
}

func schemaProps(t *testing.T, td map[string]interface{}) map[string]interface{} {
	t.Helper()
	sch, ok := td["inputSchema"].(map[string]interface{})
	if !ok {
		t.Fatalf("tool %v missing inputSchema", td["name"])
	}
	if typ, _ := sch["type"].(string); typ != "object" {
		t.Fatalf("inputSchema type = %v, want object", sch["type"])
	}
	props, _ := sch["properties"].(map[string]interface{})
	return props
}

// TestGatewayDynamicToolsPerReadCapability verifies one gw_* tool is emitted per
// read capability, with an inputSchema built from the capability's flow params.
func TestGatewayDynamicToolsPerReadCapability(t *testing.T) {
	dir := t.TempDir()
	reg, err := newConnectorRegistryAt(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := reg.Store(Connector{
		ID: "google", Engine: "api", Surface: "https://www.googleapis.com",
		Capabilities: []Capability{
			readCap("next_event", "Next calendar event", "calendar/v3/events?timeMin={now}&maxResults=1"),
			readCap("inbox_unread", "Unread inbox count", "gmail/v1/users/me/labels/{label}"),
		},
	}); err != nil {
		t.Fatalf("store google: %v", err)
	}
	if err := reg.Store(Connector{
		ID: "broker", Engine: "api", Surface: "https://api.broker.example",
		Capabilities: []Capability{
			readCap("balance", "Account balance", "v1/accounts/{account}/balance"),
		},
	}); err != nil {
		t.Fatalf("store broker: %v", err)
	}

	tools := dynamicGatewayTools(reg)
	if len(tools) != 3 {
		t.Fatalf("expected 3 dynamic tools, got %d: %v", len(tools), tools)
	}

	// Each expected tool exists with the gw_ prefix.
	for _, name := range []string{"gw_broker_balance", "gw_google_inbox_unread", "gw_google_next_event"} {
		if _, ok := toolByName(tools, name); !ok {
			names := make([]string, 0, len(tools))
			for _, td := range tools {
				names = append(names, td["name"].(string))
			}
			t.Fatalf("missing tool %q; got %v", name, names)
		}
	}

	// inputSchema for next_event has exactly the {label}? no — only non-builtin
	// placeholders; {now} is omitted by flowParams. next_event has only {now} ⇒ 0 props.
	if td, _ := toolByName(tools, "gw_google_next_event"); td != nil {
		props := schemaProps(t, td)
		if len(props) != 0 {
			t.Fatalf("gw_google_next_event props = %v, want none ({now} is builtin)", props)
		}
	}
	// balance has {account} ⇒ one string prop.
	if td, _ := toolByName(tools, "gw_broker_balance"); td != nil {
		props := schemaProps(t, td)
		acct, ok := props["account"].(map[string]interface{})
		if !ok {
			t.Fatalf("gw_broker_balance missing 'account' prop, got %v", props)
		}
		if typ, _ := acct["type"].(string); typ != "string" {
			t.Fatalf("account prop type = %v, want string", acct["type"])
		}
	}
	// inbox_unread has {label} ⇒ one string prop.
	if td, _ := toolByName(tools, "gw_google_inbox_unread"); td != nil {
		props := schemaProps(t, td)
		if _, ok := props["label"]; !ok {
			t.Fatalf("gw_google_inbox_unread missing 'label' prop, got %v", props)
		}
	}
}

// TestGatewayDynamicToolsSkipWrite verifies an ACT/write capability is NOT
// surfaced as a tool (read-only slice).
func TestGatewayDynamicToolsSkipWrite(t *testing.T) {
	dir := t.TempDir()
	reg, err := newConnectorRegistryAt(dir)
	if err != nil {
		t.Fatal(err)
	}
	// Hand-write a manifest mixing a read and a write capability — Store would
	// reject the write, so we bypass it.
	writeRawConnector(t, dir, Connector{
		ID: "mixed", Engine: "api", Surface: "https://api.example",
		Capabilities: []Capability{
			readCap("status", "Read status", "v1/status"),
			{
				ID:   "pay",
				Verb: "add", // write/ACT — must be skipped
				Risk: "write",
				Flow: CapabilityFlow{Type: "api", Method: "GET", Path: "v1/pay/{amount}"},
			},
		},
	})

	tools := dynamicGatewayTools(reg)
	if _, ok := toolByName(tools, "gw_mixed_status"); !ok {
		t.Fatalf("read capability gw_mixed_status should be surfaced; got %v", tools)
	}
	if _, ok := toolByName(tools, "gw_mixed_pay"); ok {
		t.Fatalf("write capability gw_mixed_pay must NOT be surfaced")
	}
	if len(tools) != 1 {
		t.Fatalf("expected exactly 1 tool (read only), got %d", len(tools))
	}
}

// TestGatewaySanitizeToolName checks the name sanitizer reduces to a valid MCP
// tool name and preserves the reserved gw_ prefix.
func TestGatewaySanitizeToolName(t *testing.T) {
	cases := map[string]string{
		"gw_google_next_event":   "gw_google_next_event",
		"gw_My.Connector_Cap-Id": "gw_my_connector_cap_id",
		"gw_a__b":                "gw_a_b",
		"gw_a/b/c":               "gw_a_b_c",
		"gw_a   b":               "gw_a_b",
		"gw_Über_café":           "gw_ber_caf", // non-ascii collapses to a separator
	}
	for in, want := range cases {
		if got := sanitizeGatewayToolName(in); got != want {
			t.Errorf("sanitize(%q) = %q, want %q", in, got, want)
		}
	}
	// Every output is valid: lowercase [a-z0-9_], no leading/trailing underscore.
	for in := range cases {
		got := sanitizeGatewayToolName(in)
		for _, r := range got {
			if !((r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_') {
				t.Errorf("sanitize(%q) = %q has invalid rune %q", in, got, r)
			}
		}
	}
}

// TestGatewayDynamicToolsCollision verifies two distinct (connector, capability)
// pairs that sanitize to the same name are disambiguated (never silently shadowed)
// and both remain reverse-resolvable.
func TestGatewayDynamicToolsCollision(t *testing.T) {
	dir := t.TempDir()
	reg, err := newConnectorRegistryAt(dir)
	if err != nil {
		t.Fatal(err)
	}
	// Cap ids "ab.c" and "ab-c" both sanitize to "ab_c" → tool base "gw_x_ab_c".
	// (Store accepts these cap ids; only connector ids are charset-validated.)
	if err := reg.Store(Connector{
		ID: "x", Engine: "api", Surface: "https://api.x.example",
		Capabilities: []Capability{
			readCap("ab.c", "dot variant", "v1/a"),
			readCap("ab-c", "dash variant", "v1/b"),
		},
	}); err != nil {
		t.Fatalf("store x: %v", err)
	}

	tools := dynamicGatewayTools(reg)
	if len(tools) != 2 {
		t.Fatalf("expected 2 tools, got %d: %v", len(tools), tools)
	}
	names := map[string]bool{}
	for _, td := range tools {
		n := td["name"].(string)
		if names[n] {
			t.Fatalf("duplicate tool name %q — a pair was silently shadowed", n)
		}
		names[n] = true
	}
	// The first (manifest order: ab.c) keeps the base name; the second is hashed.
	if !names["gw_x_ab_c"] {
		t.Fatalf("expected base name gw_x_ab_c to be present, got %v", names)
	}
	disambiguated := ""
	for n := range names {
		if n != "gw_x_ab_c" {
			disambiguated = n
		}
	}
	if disambiguated == "" || len(disambiguated) <= len("gw_x_ab_c") {
		t.Fatalf("expected a disambiguated (longer) second name, got %v", names)
	}

	// Reverse lookup recovers the raw ids for BOTH names.
	c1, p1, ok1 := resolveGatewayDynamicTool(reg, "gw_x_ab_c")
	if !ok1 || c1 != "x" || p1 != "ab.c" {
		t.Fatalf("resolve gw_x_ab_c = (%q,%q,%v), want (x, ab.c, true)", c1, p1, ok1)
	}
	c2, p2, ok2 := resolveGatewayDynamicTool(reg, disambiguated)
	if !ok2 || c2 != "x" || p2 != "ab-c" {
		t.Fatalf("resolve %q = (%q,%q,%v), want (x, ab-c, true)", disambiguated, c2, p2, ok2)
	}
}

// TestGatewayResolveDynamicTool checks the reverse lookup for ordinary names and
// rejects non-gw / unknown names.
func TestGatewayResolveDynamicTool(t *testing.T) {
	dir := t.TempDir()
	reg, err := newConnectorRegistryAt(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := reg.Store(Connector{
		ID: "google", Engine: "api", Surface: "https://www.googleapis.com",
		Capabilities: []Capability{
			readCap("next_event", "Next event", "calendar/v3/events?maxResults=1"),
		},
	}); err != nil {
		t.Fatalf("store: %v", err)
	}

	conn, capID, ok := resolveGatewayDynamicTool(reg, "gw_google_next_event")
	if !ok || conn != "google" || capID != "next_event" {
		t.Fatalf("resolve gw_google_next_event = (%q,%q,%v), want (google, next_event, true)", conn, capID, ok)
	}
	if _, _, ok := resolveGatewayDynamicTool(reg, "gateway_query"); ok {
		t.Fatalf("non-gw_ name should not resolve")
	}
	if _, _, ok := resolveGatewayDynamicTool(reg, "gw_google_does_not_exist"); ok {
		t.Fatalf("unknown gw_ name should not resolve")
	}
}
