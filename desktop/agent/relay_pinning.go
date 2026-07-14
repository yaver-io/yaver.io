package main

import (
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"fmt"
	"log"
	"strings"
)

// Relay identity pinning.
//
// The agent↔relay leg is QUIC, so it is always TLS 1.3 encrypted — a PASSIVE
// eavesdropper sees ciphertext. But the relay presents a self-signed cert and
// the agent has historically dialled with InsecureSkipVerify:true, accepting ANY
// certificate. That leaves an ACTIVE attacker (DNS spoof, hostile hosting/ISP,
// BGP hijack) free to impersonate the relay, terminate the QUIC session, and
// read the plaintext inside — task content, source, and the relay password on
// its way in. Encryption without peer authentication protects you from nobody
// who can get on the path.
//
// The fix is to pin the relay's public key. The pin (an SPKI SHA-256, base64)
// travels to the agent inside RelayServerInfo, which comes from Convex
// platformConfig over Convex's own authenticated TLS — a channel the agent
// already trusts for its auth token. So there is no trust-on-first-use window
// and no new secret: an SPKI hash is derived from a PUBLIC key and is safe to
// publish.
//
// Rollout is deliberately fail-safe: when a relay has no pin configured the
// agent keeps working exactly as before and logs a one-line warning. Pinning
// activates per-relay the moment a pin is published, so this can ship dark and
// be turned on after the pin is verified against a specific relay — never a
// flag day that could sever every remote connection at once.

// spkiPinOfCert returns the base64 SHA-256 of a certificate's
// SubjectPublicKeyInfo — the standard, rotation-friendly pin. It binds the KEY,
// not the cert, so the relay can reissue its self-signed cert (new serial, new
// dates) without invalidating the pin, as long as the keypair is persisted.
func spkiPinOfCert(cert *x509.Certificate) string {
	sum := sha256.Sum256(cert.RawSubjectPublicKeyInfo)
	return base64.StdEncoding.EncodeToString(sum[:])
}

// normalizePin tolerates the cosmetic variations an operator will inevitably
// paste (whitespace, a leading "sha256/" scheme label) so a correct pin is never
// rejected for a formatting reason.
func normalizePin(pin string) string {
	pin = strings.TrimSpace(pin)
	pin = strings.TrimPrefix(pin, "sha256/")
	pin = strings.TrimPrefix(pin, "sha256:")
	return strings.TrimSpace(pin)
}

// relayTLSConfig builds the TLS config for dialling a relay's QUIC listener.
//
//   - pin == ""  → current behaviour: encrypted, relay identity NOT verified.
//     Logs a warning so the gap is visible in logs, never silent.
//   - pin set    → InsecureSkipVerify still disables the CA-chain check (the
//     relay cert is self-signed and there is no CA), but VerifyPeerCertificate
//     enforces the SPKI pin instead. This is the standard Go idiom for
//     authenticating a self-signed peer: skip the PKI you don't have, verify the
//     key you do.
func relayTLSConfig(relayAddr, pin string) *tls.Config {
	pin = normalizePin(pin)
	cfg := &tls.Config{
		InsecureSkipVerify: true, // self-signed relay cert; identity handled below
		NextProtos:         []string{"yaver-relay"},
	}
	if pin == "" {
		log.Printf("[RELAY] %s: no SPKI pin configured — transport is encrypted but the relay's identity is UNVERIFIED (set spkiPin in platformConfig to close this)", relayAddr)
		return cfg
	}
	cfg.VerifyPeerCertificate = func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
		if len(rawCerts) == 0 {
			return fmt.Errorf("relay %s presented no certificate", relayAddr)
		}
		leaf, err := x509.ParseCertificate(rawCerts[0])
		if err != nil {
			return fmt.Errorf("relay %s: parse leaf cert: %w", relayAddr, err)
		}
		got := spkiPinOfCert(leaf)
		if got != pin {
			// The one error that must be loud and unambiguous: this is what a
			// MITM looks like. Do NOT fall back to accepting the connection.
			return fmt.Errorf("relay %s SPKI pin mismatch: expected %s, got %s — refusing (possible MITM)", relayAddr, pin, got)
		}
		return nil
	}
	return cfg
}
