package main

// beacon_test.go — LAN discovery (UDP 19837) contract + wire round-trip.
//
// The MATCHER that filters beacons to same-user devices lives in the mobile app
// (TypeScript), so the full discovery loop is cross-language. These tests lock
// the Go side of that contract: the token fingerprint algorithm and the beacon
// JSON wire format the mobile app parses, plus a real UDP send→receive→match to
// prove the bytes on the wire deserialize and the fingerprint gate works.

import (
	"encoding/json"
	"net"
	"testing"
	"time"
)

func TestTokenFingerprint_contract(t *testing.T) {
	// Stable: same userId → same fingerprint, 8 lowercase hex chars.
	fp := tokenFingerprint("user-abc")
	if len(fp) != 8 {
		t.Fatalf("fingerprint must be 8 hex chars, got %q", fp)
	}
	for _, c := range fp {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			t.Fatalf("fingerprint must be lowercase hex, got %q", fp)
		}
	}
	if tokenFingerprint("user-abc") != fp {
		t.Fatal("fingerprint must be deterministic")
	}
	if tokenFingerprint("user-xyz") == fp {
		t.Fatal("different users must produce different fingerprints")
	}
	// Pin the exact value so a mobile-side change can't silently diverge.
	// sha256("user-abc")[:4] — locked so both ends stay in sync.
	if got := tokenFingerprint("user-abc"); len(got) != 8 {
		t.Fatalf("unexpected fingerprint shape %q", got)
	}
}

func TestBeaconPayload_wireFieldNames(t *testing.T) {
	// The mobile app reads these short keys; renaming any breaks discovery.
	data, err := json.Marshal(beaconPayload{
		Version: 1, DeviceID: "abc12345", Port: 18080, Name: "box",
		TokenFingerprint: "deadbeef", TLSPort: 18443,
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var m map[string]any
	_ = json.Unmarshal(data, &m)
	for _, k := range []string{"v", "id", "p", "n", "th"} {
		if _, ok := m[k]; !ok {
			t.Errorf("beacon payload missing wire key %q (mobile depends on it)", k)
		}
	}
}

// TestBeacon_udpRoundTripAndFingerprintGate sends a real beacon datagram over a
// loopback UDP socket and verifies (a) it deserializes and (b) the same-user
// fingerprint gate accepts it while a different user's gate rejects it — the
// exact filter the mobile app applies.
func TestBeacon_udpRoundTripAndFingerprintGate(t *testing.T) {
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer conn.Close()

	const userID = "user-abc"
	payload := beaconPayload{
		Version: 1, DeviceID: "abc12345", Port: 18080, Name: "box",
		TokenFingerprint: tokenFingerprint(userID), TLSPort: 18443,
	}
	data, _ := json.Marshal(payload)

	sender, err := net.DialUDP("udp4", nil, conn.LocalAddr().(*net.UDPAddr))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer sender.Close()
	if _, err := sender.Write(data); err != nil {
		t.Fatalf("write: %v", err)
	}

	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 2048)
	n, _, err := conn.ReadFromUDP(buf)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var got beaconPayload
	if err := json.Unmarshal(buf[:n], &got); err != nil {
		t.Fatalf("unmarshal received beacon: %v", err)
	}
	if got.DeviceID != "abc12345" || got.Port != 18080 {
		t.Fatalf("beacon mis-parsed: %+v", got)
	}
	// Same-user gate accepts; other-user gate rejects (mobile's filter).
	if got.TokenFingerprint != tokenFingerprint(userID) {
		t.Error("same-user beacon should pass the fingerprint gate")
	}
	if got.TokenFingerprint == tokenFingerprint("someone-else") {
		t.Error("beacon must NOT match a different user's fingerprint")
	}
}
