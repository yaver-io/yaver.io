package main

import (
	"strings"
	"testing"
)

func billingToolText(t *testing.T, result interface{}) string {
	t.Helper()
	m, ok := result.(map[string]interface{})
	if !ok {
		t.Fatalf("result not a map: %T", result)
	}
	content, ok := m["content"].([]map[string]interface{})
	if !ok || len(content) == 0 {
		t.Fatalf("no content in result: %v", m)
	}
	text, _ := content[0]["text"].(string)
	return text
}

func TestBillingToolsRegistered(t *testing.T) {
	wrapper, ok := (&HTTPServer{}).getMCPToolsList().(map[string]interface{})
	if !ok {
		t.Fatal("getMCPToolsList did not return a map wrapper")
	}
	tools, ok := wrapper["tools"].([]map[string]interface{})
	if !ok {
		t.Fatal("tools key is not []map[string]interface{}")
	}
	have := map[string]bool{}
	for _, tl := range tools {
		if name, _ := tl["name"].(string); name != "" {
			have[name] = true
		}
	}
	for _, name := range []string{"yaver_billing_status", "yaver_billing_checkout", "yaver_billing_manage"} {
		if !have[name] {
			t.Errorf("billing tool %q not registered in getMCPToolsList", name)
		}
	}
}

// The $19 Agent plan must NOT mint a real checkout until the server-side hosted
// variant/tier wiring lands — otherwise it silently grants byok ($9)
// entitlements. The tool must gate it (available:false) and never return a
// live LemonSqueezy checkout link.
func TestBillingCheckoutAgentGated(t *testing.T) {
	txt := billingToolText(t, mcpYaverBillingCheckout("agent"))
	if !strings.Contains(txt, "\"available\": false") {
		t.Errorf("agent checkout should be gated (available:false), got: %s", txt)
	}
	if strings.Contains(txt, "lemonsqueezy.com/buy") || strings.Contains(txt, "/checkout/") {
		t.Errorf("agent checkout must NOT mint a live LS checkout URL (mis-bill guard), got: %s", txt)
	}
}

func TestBillingCheckoutInvalidPlan(t *testing.T) {
	txt := billingToolText(t, mcpYaverBillingCheckout("frontier"))
	if !strings.Contains(strings.ToLower(txt), "plan must be") {
		t.Errorf("invalid plan should error, got: %s", txt)
	}
}

func TestBillingManageReturnsDashboard(t *testing.T) {
	txt := billingToolText(t, mcpYaverBillingManage())
	if !strings.Contains(txt, yaverDashboardURL) {
		t.Errorf("manage should return the dashboard URL, got: %s", txt)
	}
}
