package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOwnerClaimRejectsFreshBootstrapWithoutDeviceID(t *testing.T) {
	withTempHome(t)
	if err := SaveConfig(&Config{ConvexSiteURL: "https://example.convex.cloud"}); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}

	oldList := listDevicesForOwnerClaimFn
	listDevicesForOwnerClaimFn = func(baseURL, token string) ([]DeviceInfo, error) {
		t.Fatalf("listDevices should not be called when device_id is missing")
		return nil, nil
	}
	defer func() { listDevicesForOwnerClaimFn = oldList }()

	req := httptest.NewRequest(http.MethodPost, "/auth/pair/owner-claim", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer owner-token")
	rec := httptest.NewRecorder()

	(&bootstrapHTTPServer{}).handleOwnerClaim(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "device_id") {
		t.Fatalf("expected missing device_id error, got %s", rec.Body.String())
	}
}

func TestOwnerClaimRejectsUserWhoDoesNotOwnDevice(t *testing.T) {
	withTempHome(t)
	if err := SaveConfig(&Config{
		ConvexSiteURL: "https://example.convex.cloud",
		DeviceID:      "device-123",
	}); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}

	oldList := listDevicesForOwnerClaimFn
	listDevicesForOwnerClaimFn = func(baseURL, token string) ([]DeviceInfo, error) {
		return []DeviceInfo{{DeviceID: "other-device", AccessScope: "owner"}}, nil
	}
	defer func() { listDevicesForOwnerClaimFn = oldList }()

	req := httptest.NewRequest(http.MethodPost, "/auth/pair/owner-claim", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer owner-token")
	rec := httptest.NewRecorder()

	(&bootstrapHTTPServer{}).handleOwnerClaim(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestOwnerClaimRequiresActivePairSession(t *testing.T) {
	withTempHome(t)
	EndPairingSession()
	if err := SaveConfig(&Config{
		ConvexSiteURL: "https://example.convex.cloud",
		DeviceID:      "device-123",
	}); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}

	oldList := listDevicesForOwnerClaimFn
	listDevicesForOwnerClaimFn = func(baseURL, token string) ([]DeviceInfo, error) {
		return []DeviceInfo{{DeviceID: "device-123", AccessScope: "owner", HostName: "owner-host"}}, nil
	}
	defer func() { listDevicesForOwnerClaimFn = oldList }()

	req := httptest.NewRequest(http.MethodPost, "/auth/pair/owner-claim", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer owner-token")
	rec := httptest.NewRecorder()

	(&bootstrapHTTPServer{}).handleOwnerClaim(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestOwnerClaimSubmitsBearerIntoActivePairSession(t *testing.T) {
	withTempHome(t)
	EndPairingSession()
	if err := SaveConfig(&Config{
		ConvexSiteURL: "https://example.convex.cloud",
		DeviceID:      "device-123",
	}); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}
	session, err := StartPairingSession(bootstrapPairingTTL)
	if err != nil {
		t.Fatalf("StartPairingSession: %v", err)
	}
	t.Cleanup(EndPairingSession)

	oldList := listDevicesForOwnerClaimFn
	listDevicesForOwnerClaimFn = func(baseURL, token string) ([]DeviceInfo, error) {
		return []DeviceInfo{{DeviceID: "device-123", AccessScope: "owner", HostName: "owner-host"}}, nil
	}
	defer func() { listDevicesForOwnerClaimFn = oldList }()

	req := httptest.NewRequest(http.MethodPost, "/auth/pair/owner-claim", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer owner-token")
	rec := httptest.NewRecorder()

	(&bootstrapHTTPServer{}).handleOwnerClaim(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	snap := activePairingSnapshot()
	if snap == nil {
		t.Fatalf("expected active pairing session snapshot")
	}
	if snap.Code != session.Code {
		t.Fatalf("pair code mismatch: got %q want %q", snap.Code, session.Code)
	}
	if snap.ReceivedToken != "owner-token" {
		t.Fatalf("expected received token to be stored, got %q", snap.ReceivedToken)
	}
	if snap.ReceivedURL != "https://example.convex.cloud" {
		t.Fatalf("expected convex URL to be stored, got %q", snap.ReceivedURL)
	}
}
