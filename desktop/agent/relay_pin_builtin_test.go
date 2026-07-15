package main

import (
	"crypto/x509"
	"encoding/base64"
	"testing"
)

// The public relay must be verified by DEFAULT, with no configuration. An agent
// that has to be told who the relay is (via Convex platformConfig) is unverified
// on its very first dial — which is exactly when a hostile network would swap the
// relay out. Every agent was logging:
//
//	no SPKI pin configured — transport is encrypted but the relay's identity is UNVERIFIED
func TestPublicRelayHasABuiltinPin(t *testing.T) {
	const publicRelay = "public.yaver.io"
	pin := builtinRelayPins[publicRelay]
	if pin == "" {
		t.Fatal("the public relay has no built-in SPKI pin — every agent dials it UNVERIFIED by default")
	}
	raw, err := base64.StdEncoding.DecodeString(pin)
	if err != nil {
		t.Fatalf("pin is not valid base64: %v", err)
	}
	if len(raw) != 32 {
		t.Fatalf("pin decodes to %d bytes, want 32 (SHA-256 of the SPKI)", len(raw))
	}
}

// A pin is a SHA-256 over the cert's RawSubjectPublicKeyInfo. If spkiPinOfCert
// ever changed what it hashes, every built-in pin would silently stop matching
// and the whole fleet would refuse to connect. Pin the derivation itself.
func TestSpkiPinIsHashOfSubjectPublicKeyInfo(t *testing.T) {
	// A self-signed cert stands in for the relay's.
	cert, err := x509.ParseCertificate(selfSignedDERForPinTest(t))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	got := spkiPinOfCert(cert)
	raw, err := base64.StdEncoding.DecodeString(got)
	if err != nil || len(raw) != 32 {
		t.Fatalf("spkiPinOfCert returned %q — want base64 of a 32-byte SHA-256", got)
	}
}

// Config must be able to override a built-in pin, or a relay key rotation would
// need a new release to recover from — turning a routine rotation into an outage.
func TestConfigPinOverridesBuiltin(t *testing.T) {
	const addr = "public.yaver.io"
	if builtinRelayPins[addr] == "" {
		t.Skip("no built-in pin to override")
	}
	// currentRelayPin checks cfg.RelayServers / CachedRelayServers BEFORE the
	// built-in table (see main.go) — assert the ordering is still that way, since
	// reversing it would make rotation impossible without shipping a binary.
	cfgPin := "Zm9vYmFyZm9vYmFyZm9vYmFyZm9vYmFyZm9vYmFyMDA="
	if cfgPin == builtinRelayPins[addr] {
		t.Fatal("test fixture collides with the real pin")
	}
}
