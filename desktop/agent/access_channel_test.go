package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func accessEvent(t *testing.T, now time.Time, kind, action, requestID, nonce string) BusEvent {
	t.Helper()
	payload, err := json.Marshal(accessSignal{
		Version:        1,
		Kind:           kind,
		RequestID:      requestID,
		TargetDeviceID: "target-1",
		IssuerDeviceID: "phone-1",
		IssuedAt:       now.UnixMilli(),
		ExpiresAt:      now.Add(5 * time.Minute).UnixMilli(),
		Nonce:          nonce,
		Provider:       "yaver",
	})
	if err != nil {
		t.Fatal(err)
	}
	return BusEvent{
		ID:          requestID,
		Topic:       "access/v1/device/target-1/" + action,
		Publisher:   "phone-1",
		PublishedAt: now.UnixMilli(),
		QoS:         1,
		Payload:     payload,
	}
}

func TestAccessSignalGuardAcceptsOneBoundedYaverAuthRequest(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	g := newAccessSignalGuard()
	g.now = func() time.Time { return now }
	evt := accessEvent(t, now, "requests.auth", "requests/auth", "req-1", "nonce-1")
	if err := g.Accept(evt); err != nil {
		t.Fatalf("valid auth request rejected: %v", err)
	}
}

func TestAccessSignalGuardCoalescesOAuthSpam(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	g := newAccessSignalGuard()
	g.now = func() time.Time { return now }
	if err := g.Accept(accessEvent(t, now, "requests.auth", "requests/auth", "req-1", "nonce-1")); err != nil {
		t.Fatal(err)
	}
	err := g.Accept(accessEvent(t, now, "requests.auth", "requests/auth", "req-2", "nonce-2"))
	if err == nil || !strings.Contains(err.Error(), "already active") {
		t.Fatalf("second OAuth trigger must coalesce, got %v", err)
	}
}

func TestAccessSignalGuardRejectsReplayExpiredAndPrivateFields(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	g := newAccessSignalGuard()
	g.now = func() time.Time { return now }
	evt := accessEvent(t, now, "requests.access", "requests/access", "req-1", "nonce-1")
	if err := g.Accept(evt); err != nil {
		t.Fatal(err)
	}
	if err := g.Accept(evt); err == nil || !strings.Contains(err.Error(), "replay") {
		t.Fatalf("replay must fail, got %v", err)
	}

	expired := accessEvent(t, now.Add(-20*time.Minute), "requests.access", "requests/access", "req-2", "nonce-2")
	if err := g.Accept(expired); err == nil || !strings.Contains(err.Error(), "expired") {
		t.Fatalf("expired signal must fail, got %v", err)
	}

	privatePayload := []byte(`{"version":1,"kind":"requests.access","requestId":"req-3","targetDeviceId":"target-1","issuerDeviceId":"phone-1","issuedAt":1800000000000,"expiresAt":1800000300000,"nonce":"nonce-3","token":"secret"}`)
	private := evt
	private.ID = "req-3"
	private.Payload = privatePayload
	if err := g.Accept(private); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("arbitrary/private field must fail closed, got %v", err)
	}
}

func TestAccessSignalGuardRejectsTopicPayloadMismatch(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	g := newAccessSignalGuard()
	g.now = func() time.Time { return now }
	evt := accessEvent(t, now, "requests.wake", "requests/auth", "req-1", "nonce-1")
	if err := g.Accept(evt); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("topic/payload mismatch must fail, got %v", err)
	}
}

func TestCanonicalAccessDeviceProof(t *testing.T) {
	got := canonicalAccessDeviceProof("mac-mini", 1234, "0123456789abcdef0123456789abcdef")
	want := "yaver-access-device-v1\nmac-mini\n1234\n0123456789abcdef0123456789abcdef"
	if got != want {
		t.Fatalf("canonical proof changed:\n got %q\nwant %q", got, want)
	}
}

func TestGenericBusTransportCannotInjectAccessSignal(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	b := NewBus("target-1", "owner-1")
	b.Receive(accessEvent(t, now, "requests.auth", "requests/auth", "req-1", "nonce-1"))
	if got := b.Status().Rejected; got != 1 {
		t.Fatalf("generic transport rejection count = %d, want 1", got)
	}
	if got := len(b.inbox); got != 0 {
		t.Fatalf("generic transport injected %d access signals", got)
	}
}
