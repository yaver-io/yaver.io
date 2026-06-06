package main

// recovery_transport_marshal_test.go — guards the device-registration
// regression caught by a freshness test: a fresh box's recoveryPosture
// dropped its (empty) transport arrays from JSON via `omitempty`, but Convex's
// recoveryPostureValidator requires `mobileApprovedTransports` /
// `webApprovedTransports` as non-optional arrays — so every registerDevice
// call 500'd with "Object is missing the required field
// `mobileApprovedTransports`". The fix: always emit those arrays as `[]`.

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRecoveryPostureAlwaysCarriesTransportArrays(t *testing.T) {
	// &Config{} models a fresh box with no approved recovery transports —
	// the exact case that produced nil/empty arrays.
	p := computeRecoveryTransportPosture(&Config{})
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal recoveryPosture: %v", err)
	}
	s := string(b)

	// The two arrays Convex requires must be PRESENT (not omitted).
	for _, key := range []string{`"mobileApprovedTransports":`, `"webApprovedTransports":`} {
		if !strings.Contains(s, key) {
			t.Fatalf("recoveryPosture JSON missing required key %s — Convex validator 500s registration:\n%s", key, s)
		}
	}
	// ...and must serialize as an array, never `null` (a nil slice marshals to
	// null, which v.array() rejects exactly like a missing field).
	if strings.Contains(s, `"mobileApprovedTransports":null`) || strings.Contains(s, `"webApprovedTransports":null`) {
		t.Fatalf("transport arrays must be [] not null:\n%s", s)
	}
}
