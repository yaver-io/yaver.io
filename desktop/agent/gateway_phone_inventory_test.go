package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// isolateHome points ConfigDir() at a temp dir so no real ~/.yaver is touched
// (mirrors gateway_appsync_test.go). No keychain/network involved.
func isolateHome(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home) // windows equivalent (harmless on unix)
}

func TestPhoneInventoryRoundTrip(t *testing.T) {
	isolateHome(t)

	// Missing file ⇒ empty inventory, not an error.
	empty, err := loadPhoneInventory("phone-1")
	if err != nil {
		t.Fatalf("load missing: %v", err)
	}
	if empty.DeviceID != "phone-1" || len(empty.Apps) != 0 {
		t.Fatalf("expected empty inventory for fresh phone, got %+v", empty)
	}

	inv := PhoneInventory{
		DeviceID:   "phone-1",
		CapturedAt: 1234,
		Apps: []PhoneApp{
			{PackageID: "com.bank.app", Label: "Bank"},
			{PackageID: "com.android.settings", Label: "Settings", System: true},
		},
	}
	if err := savePhoneInventory(inv); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := loadPhoneInventory("phone-1")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(got.Apps) != 2 || got.Apps[0].PackageID != "com.bank.app" {
		t.Fatalf("round-trip mismatch: %+v", got)
	}
	if !got.Apps[1].System {
		t.Fatalf("system flag lost in round-trip: %+v", got.Apps[1])
	}
}

func TestSavePhoneInventoryRequiresDevice(t *testing.T) {
	isolateHome(t)
	if err := savePhoneInventory(PhoneInventory{}); err == nil {
		t.Fatal("expected error saving inventory with empty deviceId")
	}
}

func TestNormalizePhoneAppsAcceptsBothKeysAndDedups(t *testing.T) {
	raw := []phoneAppWire{
		{PackageName: "com.a", Label: "A"},     // mobile native shape
		{PackageID: "com.b", Label: "B"},       // our shape
		{PackageName: "com.a", Label: "A dup"}, // duplicate → dropped
		{Label: "no package"},                  // no id → dropped
	}
	got := normalizePhoneApps(raw)
	if len(got) != 2 {
		t.Fatalf("expected 2 normalized apps, got %d: %+v", len(got), got)
	}
	if got[0].PackageID != "com.a" || got[1].PackageID != "com.b" {
		t.Fatalf("normalization/order wrong: %+v", got)
	}
}

func TestDeriveCloneAppSet(t *testing.T) {
	inv := PhoneInventory{Apps: []PhoneApp{
		{PackageID: "com.bank.app"},
		{PackageID: "com.android.settings", System: true}, // system → never mirrored
		{PackageID: "com.social.app"},
		{PackageID: "com.bank.app"}, // dup → dropped
	}}

	// connectorOnly=false: every non-system app, deduped.
	all := deriveCloneAppSet(inv, false, nil)
	if len(all) != 2 || all[0].PackageID != "com.bank.app" || all[1].PackageID != "com.social.app" {
		t.Fatalf("mirror-all wrong: %+v", all)
	}

	// connectorOnly=true: only apps in the connector set.
	connPkgs := map[string]bool{"com.bank.app": true}
	only := deriveCloneAppSet(inv, true, connPkgs)
	if len(only) != 1 || only[0].PackageID != "com.bank.app" {
		t.Fatalf("connector-only filter wrong: %+v", only)
	}
}

func TestCloneFromPhoneWritesDesiredSet(t *testing.T) {
	isolateHome(t)
	if err := saveGatewayConsent(GatewayConsent{ShareAppInventory: true}); err != nil {
		t.Fatalf("grant consent: %v", err)
	}

	// Report an inventory, then mirror it (no sync ⇒ no device touched).
	rep := mcpGatewayPhoneInventoryReport("phone-1", []phoneAppWire{
		{PackageName: "com.bank.app", Label: "Bank"},
		{PackageName: "com.android.settings", Label: "Settings", System: true},
	})
	if m, _ := rep.(map[string]interface{}); m["error"] != nil {
		t.Fatalf("report errored: %v", m["error"])
	}

	res := mcpGatewayCloneFromPhone("phone-1", "redroid-7", false, "", false)
	m, ok := res.(map[string]interface{})
	if !ok || m["error"] != nil {
		t.Fatalf("clone_from_phone failed: %+v", res)
	}
	if m["mirrored"].(int) != 1 {
		t.Fatalf("expected 1 mirrored app (system dropped), got %v", m["mirrored"])
	}

	// The clone's desired set should now hold exactly the non-system app.
	set, err := loadNodeAppSet("redroid-7")
	if err != nil {
		t.Fatalf("load clone set: %v", err)
	}
	if len(set.Apps) != 1 || set.Apps[0].PackageID != "com.bank.app" {
		t.Fatalf("desired set wrong: %+v", set.Apps)
	}
}

func TestCloneFromPhoneNoInventory(t *testing.T) {
	isolateHome(t)
	res := mcpGatewayCloneFromPhone("phone-unknown", "redroid-7", false, "", false)
	m, _ := res.(map[string]interface{})
	if m["error"] == nil {
		t.Fatalf("expected error when phone has no inventory, got %+v", res)
	}
}

func TestPhoneInventoryReportRequiresConsent(t *testing.T) {
	isolateHome(t)
	// No consent granted ⇒ report is refused (clean, not stored).
	res := mcpGatewayPhoneInventoryReport("phone-x", []phoneAppWire{{PackageName: "com.x"}})
	m, _ := res.(map[string]interface{})
	if nc, _ := m["needsConsent"].(bool); !nc {
		t.Fatalf("expected needsConsent without a grant, got %+v", res)
	}
	inv, _ := loadPhoneInventory("phone-x")
	if len(inv.Apps) != 0 {
		t.Fatalf("nothing should be stored without consent, got %+v", inv)
	}
}

func TestHandleGatewayPhoneInventoryHTTP(t *testing.T) {
	isolateHome(t)
	if err := saveGatewayConsent(GatewayConsent{ShareAppInventory: true}); err != nil {
		t.Fatalf("grant consent: %v", err)
	}
	s := &HTTPServer{}

	// POST a report (deviceId in body).
	body, _ := json.Marshal(map[string]interface{}{
		"deviceId": "phone-9",
		"apps": []map[string]interface{}{
			{"packageName": "com.bank.app", "label": "Bank"},
			{"packageName": "com.social.app", "label": "Social"},
		},
	})
	postReq := httptest.NewRequest(http.MethodPost, "/gateway/phone-inventory", bytes.NewReader(body))
	postRec := httptest.NewRecorder()
	s.handleGatewayPhoneInventory(postRec, postReq)
	if postRec.Code != http.StatusOK {
		t.Fatalf("POST status = %d, body=%s", postRec.Code, postRec.Body.String())
	}

	// GET it back.
	getReq := httptest.NewRequest(http.MethodGet, "/gateway/phone-inventory?device=phone-9", nil)
	getRec := httptest.NewRecorder()
	s.handleGatewayPhoneInventory(getRec, getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("GET status = %d", getRec.Code)
	}
	var out struct {
		Device string `json:"device"`
		Count  int    `json:"count"`
	}
	if err := json.Unmarshal(getRec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode GET: %v", err)
	}
	if out.Device != "phone-9" || out.Count != 2 {
		t.Fatalf("GET round-trip wrong: %+v", out)
	}
}

func TestHandleGatewayPhoneInventoryUsesHeaderDeviceID(t *testing.T) {
	isolateHome(t)
	if err := saveGatewayConsent(GatewayConsent{ShareAppInventory: true}); err != nil {
		t.Fatalf("grant consent: %v", err)
	}
	s := &HTTPServer{}
	body, _ := json.Marshal(map[string]interface{}{
		"apps": []map[string]interface{}{{"packageName": "com.x"}},
	})
	req := httptest.NewRequest(http.MethodPost, "/gateway/phone-inventory", bytes.NewReader(body))
	req.Header.Set("X-Device-ID", "phone-hdr")
	rec := httptest.NewRecorder()
	s.handleGatewayPhoneInventory(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST status = %d, body=%s", rec.Code, rec.Body.String())
	}
	inv, err := loadPhoneInventory("phone-hdr")
	if err != nil || len(inv.Apps) != 1 {
		t.Fatalf("header-keyed inventory not stored: inv=%+v err=%v", inv, err)
	}
}

func TestHandleGatewayPhoneInventoryRejectsMissingDevice(t *testing.T) {
	isolateHome(t)
	s := &HTTPServer{}
	body, _ := json.Marshal(map[string]interface{}{
		"apps": []map[string]interface{}{{"packageName": "com.x"}},
	})
	req := httptest.NewRequest(http.MethodPost, "/gateway/phone-inventory", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	s.handleGatewayPhoneInventory(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 with no deviceId, got %d", rec.Code)
	}
}
