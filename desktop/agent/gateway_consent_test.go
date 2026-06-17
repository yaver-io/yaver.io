package main

import "testing"

func TestConsentDefaultsAllOff(t *testing.T) {
	isolateHome(t)
	c, err := loadGatewayConsent()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if c.ShareAppInventory || c.AutoRelayOtp || c.ReadDeviceSms {
		t.Fatalf("fresh consent must be all-off, got %+v", c)
	}
	// consentAllows fail-closed on a fresh (missing) record.
	for _, f := range []string{consentShareAppInventory, consentAutoRelayOtp, consentReadDeviceSms} {
		if consentAllows(f) {
			t.Fatalf("feature %q should be off by default", f)
		}
	}
}

func TestConsentSetRoundTripAndAudit(t *testing.T) {
	isolateHome(t)

	res := mcpGatewayConsentSet(consentAutoRelayOtp, true)
	if m, _ := res.(map[string]interface{}); m["ok"] != true {
		t.Fatalf("set failed: %+v", res)
	}
	if !consentAllows(consentAutoRelayOtp) {
		t.Fatal("auto_relay_otp should be granted after set")
	}
	// Other features stay off (independent grants).
	if consentAllows(consentReadDeviceSms) {
		t.Fatal("read_device_sms must remain off")
	}

	// Revoke.
	mcpGatewayConsentSet(consentAutoRelayOtp, false)
	if consentAllows(consentAutoRelayOtp) {
		t.Fatal("auto_relay_otp should be revoked")
	}

	// The change is audited locally (grant + revoke = 2 entries for _consent).
	entries, err := listGatewayAudit(10)
	if err != nil {
		t.Fatalf("list audit: %v", err)
	}
	n := 0
	for _, e := range entries {
		if e.Connector == "_consent" {
			n++
		}
	}
	if n < 2 {
		t.Fatalf("expected >=2 consent audit entries, got %d", n)
	}
}

func TestConsentSetUnknownFeature(t *testing.T) {
	isolateHome(t)
	res := mcpGatewayConsentSet("not_a_feature", true)
	if m, _ := res.(map[string]interface{}); m["error"] == nil {
		t.Fatalf("expected error for unknown feature, got %+v", res)
	}
}

func TestConsentGetSurfacesAllFeatures(t *testing.T) {
	isolateHome(t)
	res := mcpGatewayConsent()
	m, _ := res.(map[string]interface{})
	feats, _ := m["features"].([]consentFeatureDoc)
	if len(feats) != 3 {
		t.Fatalf("expected 3 documented features, got %d", len(feats))
	}
}
