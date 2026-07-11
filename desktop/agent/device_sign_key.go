package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// device_sign_key.go — the device's ed25519 SIGNING keypair, used to prove
// device identity to the relay (and anywhere else) WITHOUT a shared secret. It
// is distinct from the X25519 box keypair (device_keys.go), which is for
// pairing encryption and cannot sign.
//
// The public half is published to Convex; the relay verifies request
// signatures holding only public material — so breaching the (open-source,
// self-hostable) relay yields nothing reusable. See
// docs/yaver-relay-asymmetric-auth.md.
//
// IMPORTANT: canonicalRelaySigString below MUST stay byte-for-byte identical to
// relay/sigauth.go::canonicalSigString — they are the two halves of one wire
// contract. Change them together.

const signKeyFileName = "device_sign.key"

// DeviceSigningKey is a device's ed25519 signing keypair.
type DeviceSigningKey struct {
	Public  ed25519.PublicKey
	Private ed25519.PrivateKey
}

// LoadOrGenerateSigningKey loads ~/.yaver/device_sign.key or creates it. The
// file stores the 64-byte ed25519 private key, base64-encoded, mode 0600.
func LoadOrGenerateSigningKey() (*DeviceSigningKey, error) {
	dir, err := ConfigDir()
	if err != nil {
		return nil, err
	}
	path := filepath.Join(dir, signKeyFileName)

	if data, err := os.ReadFile(path); err == nil {
		if raw, decErr := base64.StdEncoding.DecodeString(strings.TrimSpace(string(data))); decErr == nil && len(raw) == ed25519.PrivateKeySize {
			priv := ed25519.PrivateKey(raw)
			return &DeviceSigningKey{Public: priv.Public().(ed25519.PublicKey), Private: priv}, nil
		}
	}

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate ed25519 signing key: %w", err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, []byte(base64.StdEncoding.EncodeToString(priv)), 0o600); err != nil {
		return nil, fmt.Errorf("save signing key: %w", err)
	}
	return &DeviceSigningKey{Public: pub, Private: priv}, nil
}

// SignPublicKeyBase64 is the base64 public key to publish to Convex.
func (sk *DeviceSigningKey) SignPublicKeyBase64() string {
	return base64.StdEncoding.EncodeToString(sk.Public)
}

// deviceSignPublicKey returns this device's ed25519 signing public key as
// base64, or "" if it can't be loaded. Best-effort: registration proceeds
// without it and the relay simply falls back to the password path.
func deviceSignPublicKey() string {
	sk, err := LoadOrGenerateSigningKey()
	if err != nil {
		return ""
	}
	return sk.SignPublicKeyBase64()
}

// Sign returns a base64 ed25519 signature over msg.
func (sk *DeviceSigningKey) Sign(msg []byte) string {
	return base64.StdEncoding.EncodeToString(ed25519.Sign(sk.Private, msg))
}

// newSigNonce returns a random 128-bit hex nonce for a signed relay request
// (replay protection at the relay). Falls back to a timestamp-only nonce if the
// RNG is unavailable — the signature + timestamp window still hold.
func newSigNonce() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "t" + strconv.FormatInt(int64(len(b)), 10)
	}
	return hex.EncodeToString(b[:])
}

// canonicalRelaySigString is the exact bytes a relay request is signed over.
// MUST match relay/sigauth.go::canonicalSigString.
func canonicalRelaySigString(method, path, deviceID, ts, nonce string, body []byte) string {
	sum := sha256.Sum256(body)
	return strings.Join([]string{method, path, deviceID, ts, nonce, hex.EncodeToString(sum[:])}, "\n")
}

// SignRelayRequest returns the headers that authenticate a relay request with a
// per-device signature — no shared password, nothing to leak in the URL.
func (sk *DeviceSigningKey) SignRelayRequest(deviceID, method, path string, body []byte, unixMillis int64, nonce string) map[string]string {
	ts := strconv.FormatInt(unixMillis, 10)
	canonical := canonicalRelaySigString(method, path, deviceID, ts, nonce, body)
	// A dedicated header (NOT Authorization) so the Bearer token that
	// authenticates the end agent rides through untouched.
	return map[string]string{
		"X-Yaver-Sig":       "v1",
		"X-Yaver-Device":    deviceID,
		"X-Yaver-Timestamp": ts,
		"X-Yaver-Nonce":     nonce,
		"X-Yaver-Signature": sk.Sign([]byte(canonical)),
	}
}
