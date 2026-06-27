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

// billingPlanMap is the pure plan→variant mapping. The $19/$9 split is
// security-relevant (wrong planId = wrong charge), so pin it exactly.
func TestBillingPlanMap(t *testing.T) {
	cases := []struct {
		in, planID, short string
	}{
		{"", "cloud-workspace", "workspace"},
		{"workspace", "cloud-workspace", "workspace"},
		{"byok", "cloud-workspace", "workspace"},
		{"Workspace", "cloud-workspace", "workspace"},
		{"agent", "cloud-agent", "agent"},
		{"hosted", "cloud-agent", "agent"},
		{"AGENT", "cloud-agent", "agent"},
	}
	for _, c := range cases {
		planID, short, _, _, ok := billingPlanMap(c.in)
		if !ok || planID != c.planID || short != c.short {
			t.Errorf("billingPlanMap(%q) = (%q,%q,ok=%v), want (%q,%q,true)", c.in, planID, short, ok, c.planID, c.short)
		}
	}
	if _, _, _, _, ok := billingPlanMap("frontier"); ok {
		t.Error("unknown plan should not be ok")
	}
}

// An unrecognized plan must error BEFORE any network call.
func TestBillingCheckoutInvalidPlan(t *testing.T) {
	txt := billingToolText(t, mcpYaverBillingCheckout("frontier"))
	if !strings.Contains(strings.ToLower(txt), "plan must be") {
		t.Errorf("invalid plan should error, got: %s", txt)
	}
}
