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
	for _, name := range []string{"yaver_billing_topup", "yaver_billing_credits_checkout"} {
		if have[name] {
			t.Errorf("retired billing tool %q must not be registered in getMCPToolsList", name)
		}
	}
}

func TestBillingToolDescriptionsStayFlatPlanOnly(t *testing.T) {
	wrapper, ok := (&HTTPServer{}).getMCPToolsList().(map[string]interface{})
	if !ok {
		t.Fatal("getMCPToolsList did not return a map wrapper")
	}
	tools, ok := wrapper["tools"].([]map[string]interface{})
	if !ok {
		t.Fatal("tools key is not []map[string]interface{}")
	}
	for _, tl := range tools {
		name, _ := tl["name"].(string)
		if !strings.HasPrefix(name, "yaver_billing_") {
			continue
		}
		desc, _ := tl["description"].(string)
		lower := strings.ToLower(desc)
		for _, forbidden := range []string{"top-up", "topup", "credit pack", "prepaid wallet", "cloud agent", "$19"} {
			if strings.Contains(lower, forbidden) {
				t.Errorf("%s description exposes retired billing language %q: %s", name, forbidden, desc)
			}
		}
	}
}

// billingPlanMap is the pure plan→variant mapping. The $9/$29 split is
// security-relevant (wrong productId = wrong charge), so pin it exactly.
func TestBillingPlanMap(t *testing.T) {
	cases := []struct {
		in, planID, short string
	}{
		{"", "relay-pro", "relay"},
		{"relay", "relay-pro", "relay"},
		{"relay-pro", "relay-pro", "relay"},
		{"managed-relay", "relay-pro", "relay"},
		{"workspace", "cloud-workspace", "workspace"},
		{"cloud-workspace", "cloud-workspace", "workspace"},
		{"compute", "cloud-workspace", "workspace"},
		{"Workspace", "cloud-workspace", "workspace"},
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
	if _, _, _, _, ok := billingPlanMap("agent"); ok {
		t.Error("retired Cloud Agent plan should not be ok")
	}
}

// An unrecognized plan must error BEFORE any network call.
func TestBillingCheckoutInvalidPlan(t *testing.T) {
	txt := billingToolText(t, mcpYaverBillingCheckout("frontier"))
	if !strings.Contains(strings.ToLower(txt), "plan must be") {
		t.Errorf("invalid plan should error, got: %s", txt)
	}
}
