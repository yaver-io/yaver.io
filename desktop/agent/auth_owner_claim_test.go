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

// TestOwnerClaimRejectsGuestToken proves guests/shared scopes cannot reclaim
// a host's box. The handler enforces match.IsGuest || AccessScope!="owner",
// but until this test landed nothing in CI proved that guard fires for
// a token that DOES list the device — only for tokens that don't list it
// at all. That was the audit's biggest concrete owner-claim coverage hole:
// a stolen guest token combined with relay reachability would have looked
// indistinguishable from a real reclaim attempt at the request layer.
func TestOwnerClaimRejectsGuestToken(t *testing.T) {
	withTempHome(t)
	EndPairingSession()
	if err := SaveConfig(&Config{
		ConvexSiteURL: "https://example.convex.cloud",
		DeviceID:      "device-123",
	}); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}
	if _, err := StartPairingSession(bootstrapPairingTTL); err != nil {
		t.Fatalf("StartPairingSession: %v", err)
	}
	t.Cleanup(EndPairingSession)

	cases := []struct {
		name   string
		device DeviceInfo
	}{
		{
			name:   "isGuest=true_with_owner_scope",
			device: DeviceInfo{DeviceID: "device-123", IsGuest: true, AccessScope: "owner", HostName: "real-host"},
		},
		{
			name:   "shared-scoped_access",
			device: DeviceInfo{DeviceID: "device-123", AccessScope: "shared-scoped", HostName: "real-host"},
		},
		{
			name:   "shared-legacy_access",
			device: DeviceInfo{DeviceID: "device-123", AccessScope: "shared-legacy", HostName: "real-host"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			oldList := listDevicesForOwnerClaimFn
			listDevicesForOwnerClaimFn = func(baseURL, token string) ([]DeviceInfo, error) {
				return []DeviceInfo{tc.device}, nil
			}
			defer func() { listDevicesForOwnerClaimFn = oldList }()

			req := httptest.NewRequest(http.MethodPost, "/auth/pair/owner-claim", strings.NewReader(`{}`))
			req.Header.Set("Authorization", "Bearer guest-token")
			rec := httptest.NewRecorder()

			(&bootstrapHTTPServer{}).handleOwnerClaim(rec, req)
			if rec.Code != http.StatusForbidden {
				t.Fatalf("expected 403 for %s, got %d: %s", tc.name, rec.Code, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), "guests cannot owner-claim") {
				t.Fatalf("expected guest-rejection error message, got %s", rec.Body.String())
			}
			// The active pair session must NOT have been polluted with the
			// guest's token. If it were, a follow-up real owner submit would
			// race against pre-stamped state.
			snap := activePairingSnapshot()
			if snap == nil {
				t.Fatalf("pair session must remain intact after rejection")
			}
			if snap.ReceivedToken != "" {
				t.Fatalf("guest token leaked into pair session: %q", snap.ReceivedToken)
			}
		})
	}
}

// TestOwnerClaimAcceptsBearerInBody proves the handler accepts the token
// from the JSON body when no Authorization header is present. This is the
// path mobile/web fall back to when running in environments that strip
// custom headers (e.g. some iframe-style proxies). Previously untested.
func TestOwnerClaimAcceptsBearerInBody(t *testing.T) {
	withTempHome(t)
	EndPairingSession()
	if err := SaveConfig(&Config{
		ConvexSiteURL: "https://example.convex.cloud",
		DeviceID:      "device-123",
	}); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}
	if _, err := StartPairingSession(bootstrapPairingTTL); err != nil {
		t.Fatalf("StartPairingSession: %v", err)
	}
	t.Cleanup(EndPairingSession)

	oldList := listDevicesForOwnerClaimFn
	listDevicesForOwnerClaimFn = func(baseURL, token string) ([]DeviceInfo, error) {
		if token != "body-token" {
			t.Fatalf("expected body-token to be forwarded to listDevices, got %q", token)
		}
		return []DeviceInfo{{DeviceID: "device-123", AccessScope: "owner"}}, nil
	}
	defer func() { listDevicesForOwnerClaimFn = oldList }()

	req := httptest.NewRequest(http.MethodPost, "/auth/pair/owner-claim",
		strings.NewReader(`{"token":"body-token"}`))
	rec := httptest.NewRecorder()

	(&bootstrapHTTPServer{}).handleOwnerClaim(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 with token in body, got %d: %s", rec.Code, rec.Body.String())
	}
}

// TestOwnerClaimSurvivesPairSessionRotation guards against the timing case
// where the bootstrap loop rotates its 10-minute pair session between the
// caller's lifecycle probe (which observed ownerClaimReady=true) and the
// owner-claim POST landing. The handler must either splice into whatever
// session is active NOW, or 409 cleanly so the client can retry with a
// fresh probe — never a half-state where the OLD session id was assumed
// and a different new session ends up with no token.
//
// Behavior we lock in: handler reads activePairingSnapshot() at call time,
// so a rotation between probe and call still results in a clean splice
// into the *new* session.
func TestOwnerClaimSurvivesPairSessionRotation(t *testing.T) {
	withTempHome(t)
	EndPairingSession()
	if err := SaveConfig(&Config{
		ConvexSiteURL: "https://example.convex.cloud",
		DeviceID:      "device-123",
	}); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}

	// Session A — what the client thought was active when it probed.
	sessionA, err := StartPairingSession(bootstrapPairingTTL)
	if err != nil {
		t.Fatalf("StartPairingSession A: %v", err)
	}

	// Session rotation happens between probe and POST.
	sessionB, err := StartPairingSession(bootstrapPairingTTL)
	if err != nil {
		t.Fatalf("StartPairingSession B: %v", err)
	}
	t.Cleanup(EndPairingSession)

	if sessionA.Code == sessionB.Code {
		t.Fatalf("rotation produced identical codes; cannot prove the test")
	}
	// Session A's done MUST be closed by the rotation — otherwise a CLI
	// caller blocked on session A would hang past rotation.
	select {
	case <-sessionA.done:
	default:
		t.Fatalf("session A done not closed after rotation; loop callers will hang")
	}

	oldList := listDevicesForOwnerClaimFn
	listDevicesForOwnerClaimFn = func(baseURL, token string) ([]DeviceInfo, error) {
		return []DeviceInfo{{DeviceID: "device-123", AccessScope: "owner"}}, nil
	}
	defer func() { listDevicesForOwnerClaimFn = oldList }()

	req := httptest.NewRequest(http.MethodPost, "/auth/pair/owner-claim", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer owner-token")
	rec := httptest.NewRecorder()

	(&bootstrapHTTPServer{}).handleOwnerClaim(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 after rotation, got %d: %s", rec.Code, rec.Body.String())
	}
	snap := activePairingSnapshot()
	if snap == nil {
		t.Fatalf("expected active pairing snapshot")
	}
	if snap.Code != sessionB.Code {
		t.Fatalf("token landed on wrong session: got %q want %q (session B)", snap.Code, sessionB.Code)
	}
	if snap.ReceivedToken != "owner-token" {
		t.Fatalf("token did not splice into rotated session: got %q", snap.ReceivedToken)
	}
}

// TestOwnerClaimRejectsMissingBearer rounds out the auth-input matrix: no
// header, no query, no body. The handler must 401 without touching Convex.
func TestOwnerClaimRejectsMissingBearer(t *testing.T) {
	withTempHome(t)
	EndPairingSession()
	if err := SaveConfig(&Config{
		ConvexSiteURL: "https://example.convex.cloud",
		DeviceID:      "device-123",
	}); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}
	if _, err := StartPairingSession(bootstrapPairingTTL); err != nil {
		t.Fatalf("StartPairingSession: %v", err)
	}
	t.Cleanup(EndPairingSession)

	oldList := listDevicesForOwnerClaimFn
	listDevicesForOwnerClaimFn = func(baseURL, token string) ([]DeviceInfo, error) {
		t.Fatalf("listDevices must not be called when bearer is missing")
		return nil, nil
	}
	defer func() { listDevicesForOwnerClaimFn = oldList }()

	req := httptest.NewRequest(http.MethodPost, "/auth/pair/owner-claim", strings.NewReader(`{}`))
	rec := httptest.NewRecorder()

	(&bootstrapHTTPServer{}).handleOwnerClaim(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for missing bearer, got %d: %s", rec.Code, rec.Body.String())
	}
}
