package main

// gateway_redroid_test.go — tests for the password_totp AuthMethod handler
// (M-G4). No real redroid, no keychain: a fakeDeviceDriver double drives the
// handler, an in-memory CredStore holds creds/seeds, and an in-memory gate store
// stands in for the human gate.
//
// Coverage:
//   - RFC-6238 TOTP test vectors against the reused gen (deterministic, no device).
//   - totp_seed mechanism auto-fills a code (fully remote, no human).
//   - golden snapshot fast-path skips login.
//   - push_to_app routes to a human gate and NEVER auto-approves.
//   - passkey fails clean (non-relayable).
//
// Run scoped: go test -run TestGateway -count=1 -vet=off .

import (
	"context"
	"encoding/base32"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"
)

// ── device driver double ─────────────────────────────────────────────────────

type fakeDeviceDriver struct {
	mu sync.Mutex

	launched  string
	typed     []string
	taps      []string
	uiTexts   []uiNode
	smsCode   string
	snapErr   error
	restoreOK bool // when true, RestoreSnapshot succeeds (golden snapshot valid)

	snapInstance string
	snapID       string
}

func (d *fakeDeviceDriver) Launch(pkg string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.launched = pkg
	return nil
}

func (d *fakeDeviceDriver) Type(text string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.typed = append(d.typed, text)
	return nil
}

func (d *fakeDeviceDriver) Frame() ([]byte, error) { return []byte("PNG"), nil }

func (d *fakeDeviceDriver) Tap(target string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.taps = append(d.taps, target)
	return nil
}

func (d *fakeDeviceDriver) UiTexts() ([]uiNode, error) { return d.uiTexts, nil }

func (d *fakeDeviceDriver) Snapshot() (string, string, error) {
	if d.snapErr != nil {
		return "", "", d.snapErr
	}
	inst := d.snapInstance
	if inst == "" {
		inst = "inst-1"
	}
	id := d.snapID
	if id == "" {
		id = "snap-1"
	}
	return inst, id, nil
}

func (d *fakeDeviceDriver) RestoreSnapshot(instanceID, snapshotID string) error {
	if d.restoreOK {
		return nil
	}
	return errTestNoSnapshot
}

func (d *fakeDeviceDriver) ReadSMS() (string, error) { return d.smsCode, nil }

var errTestNoSnapshot = &testErr{"no snapshot"}

type testErr struct{ s string }

func (e *testErr) Error() string { return e.s }

func (d *fakeDeviceDriver) typedContains(s string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, t := range d.typed {
		if t == s {
			return true
		}
	}
	return false
}

// seedTestSecret is "12345678901234567890" base32-encoded — the RFC-6238 SHA1
// test key.
func seedTestSecret(t *testing.T) string {
	t.Helper()
	return base32.StdEncoding.EncodeToString([]byte("12345678901234567890"))
}

func putLoginCreds(t *testing.T, store CredStore, ref, user, pass string) {
	t.Helper()
	project, name, err := credNameFromRef(ref)
	if err != nil {
		t.Fatalf("credNameFromRef: %v", err)
	}
	blob, _ := json.Marshal(loginCreds{Username: user, Password: pass})
	if err := store.SetCreds(project, name, blob); err != nil {
		t.Fatalf("set login creds: %v", err)
	}
}

func putRawCred(t *testing.T, store CredStore, ref, value string) {
	t.Helper()
	project, name, err := credNameFromRef(ref)
	if err != nil {
		t.Fatalf("credNameFromRef: %v", err)
	}
	if err := store.SetCreds(project, name, []byte(value)); err != nil {
		t.Fatalf("set cred: %v", err)
	}
}

// ── RFC-6238 test vectors ─────────────────────────────────────────────────────

func TestGatewayTOTPRFC6238Vectors(t *testing.T) {
	// Official RFC-6238 Appendix B vectors for the SHA1 suite. Secret is the
	// ASCII string "12345678901234567890". Each row is (unix time, expected
	// 8-digit code); we truncate to our 6-digit default and compare the last 6.
	secret := seedTestSecret(t)
	cases := []struct {
		unix     int64
		expected string // RFC 8-digit value
	}{
		{59, "94287082"},
		{1111111109, "07081804"},
		{1111111111, "14050471"},
		{1234567890, "89005924"},
		{2000000000, "69279037"},
		{20000000000, "65353130"},
	}
	for _, c := range cases {
		code, err := totpAt(secret, time.Unix(c.unix, 0))
		if err != nil {
			t.Fatalf("totpAt(%d): %v", c.unix, err)
		}
		want := c.expected[len(c.expected)-totpDigits:] // last 6 of the RFC 8-digit code
		if code != want {
			t.Errorf("totp at %d = %q, want %q (RFC-6238 vector %q truncated)", c.unix, code, want, c.expected)
		}
	}
}

func TestGatewayTOTPDecodesPaddedAndSpacedSeed(t *testing.T) {
	secret := seedTestSecret(t)
	// Add spaces + lowercase + strip padding — must still decode + match.
	mangled := strings.ToLower(strings.TrimRight(secret, "="))
	mangled = mangled[:4] + " " + mangled[4:]
	a, err := totpAt(secret, time.Unix(59, 0))
	if err != nil {
		t.Fatal(err)
	}
	b, err := totpAt(mangled, time.Unix(59, 0))
	if err != nil {
		t.Fatalf("mangled seed should still decode: %v", err)
	}
	if a != b {
		t.Fatalf("mangled seed produced %q, want %q", b, a)
	}
}

// ── handler behavior ──────────────────────────────────────────────────────────

func redroidConnector(mechanism string) *Connector {
	return &Connector{
		ID:      "example-app",
		Engine:  "redroid",
		Surface: "com.example.app",
		Auth: ConnectorAuth{
			Method:    "password_totp",
			Mechanism: mechanism,
			LoginRef:  "gateway/example-app/login",
			TotpRef:   "gateway/example-app/totp_seed",
			DeviceRef: "gateway/example-app/redroid",
		},
	}
}

func TestGatewayRedroidTotpSeedNoHuman(t *testing.T) {
	store := newMemCredStore()
	driver := &fakeDeviceDriver{}
	gate := newGateStore(&recordingNotifier{})
	h := newPasswordTotpHandler(store, driver, gate)

	c := redroidConnector("totp_seed")
	putLoginCreds(t, store, c.Auth.LoginRef, "alice", "s3cret")
	putRawCred(t, store, c.Auth.TotpRef, seedTestSecret(t))

	sess, err := h.Ensure(context.Background(), c)
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	if sess.Kind != SessionDevice || sess.DeviceID == "" {
		t.Fatalf("session = %+v, want device session with id", sess)
	}
	if driver.launched != "com.example.app" {
		t.Fatalf("launched %q, want com.example.app", driver.launched)
	}
	if !driver.typedContains("alice") || !driver.typedContains("s3cret") {
		t.Fatalf("did not type username+password; typed=%v", driver.typed)
	}
	// A 6-digit TOTP code must have been typed.
	gotCode := false
	for _, tt := range driver.typed {
		if extractSixDigitCode(tt) == tt && len(tt) == 6 {
			gotCode = true
		}
	}
	if !gotCode {
		t.Fatalf("no 6-digit TOTP code was typed; typed=%v", driver.typed)
	}
	// Snapshot ref was persisted to the vault.
	if ref, err := h.loadSnapshotRef(c); err != nil || ref.SnapshotID == "" {
		t.Fatalf("snapshot ref not persisted: ref=%+v err=%v", ref, err)
	}
	if h.NeedsHuman(c) {
		t.Fatal("totp_seed NeedsHuman should be false")
	}
}

func TestGatewayRedroidGoldenSnapshotFastPath(t *testing.T) {
	store := newMemCredStore()
	driver := &fakeDeviceDriver{restoreOK: true}
	h := newPasswordTotpHandler(store, driver, newGateStore(&recordingNotifier{}))

	c := redroidConnector("totp_seed")
	// Pre-seed a valid snapshot ref → Ensure must restore + skip login entirely.
	blob, _ := json.Marshal(snapshotRef{InstanceID: "inst-x", SnapshotID: "snap-x"})
	putRawCred(t, store, c.Auth.DeviceRef, string(blob))

	sess, err := h.Ensure(context.Background(), c)
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	if sess.DeviceID != "inst-x" {
		t.Fatalf("restored device = %q, want inst-x", sess.DeviceID)
	}
	if driver.launched != "" {
		t.Fatal("login ran despite a valid golden snapshot (fast path broken)")
	}
}

func TestGatewayRedroidPushRoutesToHumanGate(t *testing.T) {
	store := newMemCredStore()
	driver := &fakeDeviceDriver{}
	gate := newGateStore(&recordingNotifier{})
	h := newPasswordTotpHandler(store, driver, gate)

	c := redroidConnector("push_to_app")
	putLoginCreds(t, store, c.Auth.LoginRef, "bob", "pw")

	// push_to_app must surface as a possible human gate.
	if !h.NeedsHuman(c) {
		t.Fatal("push_to_app NeedsHuman should be true")
	}

	type res struct {
		sess Session
		err  error
	}
	done := make(chan res, 1)
	go func() {
		s, e := h.Ensure(context.Background(), c)
		done <- res{s, e}
	}()

	// A gate must appear (the handler suspended on a human approval) — it must
	// NOT have auto-approved.
	var gateID string
	deadline := time.After(2 * time.Second)
	for {
		gates := gate.List()
		if len(gates) == 1 {
			gateID = gates[0].ID
			if gates[0].Kind != GateApprovePush {
				t.Fatalf("gate kind = %q, want approve_push", gates[0].Kind)
			}
			break
		}
		select {
		case <-deadline:
			t.Fatal("push_to_app did not create a human gate (auto-approved?)")
		case <-time.After(5 * time.Millisecond):
		}
	}

	// User approves → login completes + snapshots.
	if err := gate.Resolve(gateID, Resolution{Approved: true}); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	select {
	case r := <-done:
		if r.err != nil {
			t.Fatalf("Ensure after approval: %v", r.err)
		}
		if r.sess.Kind != SessionDevice {
			t.Fatalf("session = %+v, want device", r.sess)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Ensure did not complete after push approval")
	}
}

func TestGatewayRedroidPushDeniedAborts(t *testing.T) {
	store := newMemCredStore()
	driver := &fakeDeviceDriver{}
	gate := newGateStore(&recordingNotifier{})
	h := newPasswordTotpHandler(store, driver, gate)

	c := redroidConnector("push_to_app")
	putLoginCreds(t, store, c.Auth.LoginRef, "bob", "pw")

	done := make(chan error, 1)
	go func() {
		_, e := h.Ensure(context.Background(), c)
		done <- e
	}()
	var gateID string
	for {
		gates := gate.List()
		if len(gates) == 1 {
			gateID = gates[0].ID
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	// Deny → clean abort, never a session.
	if err := gate.Resolve(gateID, Resolution{Approved: false}); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("denied push must abort login with an error, not grant a session")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Ensure did not abort after push denial")
	}
}

func TestGatewayRedroidPasskeyFailsClean(t *testing.T) {
	store := newMemCredStore()
	h := newPasswordTotpHandler(store, &fakeDeviceDriver{}, newGateStore(&recordingNotifier{}))
	c := redroidConnector("passkey")
	putLoginCreds(t, store, c.Auth.LoginRef, "carol", "pw")

	_, err := h.Ensure(context.Background(), c)
	if err == nil {
		t.Fatal("passkey connector must fail clean (non-relayable), got nil error")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "passkey") {
		t.Fatalf("passkey error should mention passkey/non-relayable: %v", err)
	}
}

func TestGatewayRedroidAuthenticatorReadsRotatingCode(t *testing.T) {
	store := newMemCredStore()
	driver := &fakeDeviceDriver{uiTexts: []uiNode{{Text: "Your code"}, {Text: "428913"}, {Text: "30s"}}}
	h := newPasswordTotpHandler(store, driver, newGateStore(&recordingNotifier{}))
	c := redroidConnector("authenticator_app")
	putLoginCreds(t, store, c.Auth.LoginRef, "dan", "pw")

	if _, err := h.Ensure(context.Background(), c); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	if !driver.typedContains("428913") {
		t.Fatalf("authenticator code not typed; typed=%v", driver.typed)
	}
}
