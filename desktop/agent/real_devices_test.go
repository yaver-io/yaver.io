package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandleRealDevicesReturnsPublicInventoryOnly(t *testing.T) {
	stubAdb(t, usbAndroidLine)
	req := httptest.NewRequest(http.MethodGet, "/real-devices", nil)
	rec := httptest.NewRecorder()
	(&HTTPServer{}).handleRealDevices(rec, req)
	if rec.Code != http.StatusOK { t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String()) }
	if strings.Contains(rec.Body.String(), "R52W60BEDXD") || strings.Contains(rec.Body.String(), "udid") {
		t.Fatalf("real-device endpoint leaked local ADB identity: %s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"id":"rd_`) {
		t.Fatalf("real-device endpoint omitted opaque registration id: %s", rec.Body.String())
	}
}

func TestRegisteredRealDevicesUsesOpaqueIDsAndNeverSerializesADBSerial(t *testing.T) {
	stubAdb(t, usbAndroidLine)
	devices := registeredRealDevices(context.Background())
	if len(devices) != 1 {
		t.Fatalf("devices = %+v, want one attached device", devices)
	}
	device := devices[0]
	if !strings.HasPrefix(device.ID, "rd_") || device.ID == "R52W60BEDXD" {
		t.Fatalf("device id must be opaque, got %q", device.ID)
	}
	body, err := json.Marshal(device)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "R52W60BEDXD") || strings.Contains(string(body), "serial") {
		t.Fatalf("public device JSON leaked the ADB serial: %s", body)
	}
}

func TestResolveRegisteredRealDeviceSelectsExactAttachedDevice(t *testing.T) {
	stubAdb(t, usbAndroidLine+"\n"+wifiAndroidLine)
	wanted := realDeviceIDFor("192.168.1.50:5555")
	serial, err := resolveRegisteredRealDevice(context.Background(), wanted)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if serial != "192.168.1.50:5555" {
		t.Fatalf("serial = %q, want exact selected wireless device", serial)
	}
}

func TestMCPPublishesRealDeviceProbeAndSelector(t *testing.T) {
	tools := (&HTTPServer{}).getMCPToolsList().(map[string]interface{})["tools"].([]map[string]interface{})
	var probeFound, selectorFound bool
	for _, tool := range tools {
		name, _ := tool["name"].(string)
		if name == "real_device_probe" {
			probeFound = true
		}
		if name != "runtime_create" {
			continue
		}
		schema := tool["inputSchema"].(map[string]interface{})
		props := schema["properties"].(map[string]interface{})
		_, selectorFound = props["realDeviceId"]
	}
	if !probeFound || !selectorFound {
		t.Fatalf("MCP contract missing probe=%v selector=%v", probeFound, selectorFound)
	}
}
