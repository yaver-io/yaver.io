package e2ee

import (
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/rand"
	"testing"
	"time"
)

func TestDeviceAuthorizationRequiresExistingTrustedSignature(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	authorizerPublic, authorizerPrivate, _ := ed25519.GenerateKey(rand.Reader)
	subjectIdentity, _, _ := ed25519.GenerateKey(rand.Reader)
	subjectExchange, _ := ecdh.X25519().GenerateKey(rand.Reader)
	nonce := make([]byte, 24)
	_, _ = rand.Read(nonce)

	cert, err := AuthorizeDevice(
		authorizerPrivate,
		"opaque-account", "trusted-device-1", "new-device-2",
		subjectIdentity, subjectExchange.PublicKey().Bytes(), nonce, now,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyDeviceAuthorization(cert, authorizerPublic, "opaque-account", now); err != nil {
		t.Fatal(err)
	}

	serverPublic, _, _ := ed25519.GenerateKey(rand.Reader)
	if err := VerifyDeviceAuthorization(cert, serverPublic, "opaque-account", now); err == nil {
		t.Fatal("server-controlled key was able to authorize a device")
	}
	tampered := cert
	tampered.SubjectDeviceID = "server-inserted-device"
	if err := VerifyDeviceAuthorization(tampered, authorizerPublic, "opaque-account", now); err == nil {
		t.Fatal("tampered membership certificate accepted")
	}
	if err := VerifyDeviceAuthorization(cert, authorizerPublic, "different-account", now); err == nil {
		t.Fatal("cross-account membership certificate accepted")
	}
}

func TestSafetyFingerprintBindsBothKeys(t *testing.T) {
	identityA, _, _ := ed25519.GenerateKey(rand.Reader)
	identityB, _, _ := ed25519.GenerateKey(rand.Reader)
	exchange, _ := ecdh.X25519().GenerateKey(rand.Reader)
	if SafetyFingerprint(identityA, exchange.PublicKey().Bytes()) == SafetyFingerprint(identityB, exchange.PublicKey().Bytes()) {
		t.Fatal("fingerprint did not bind identity public key")
	}
}
