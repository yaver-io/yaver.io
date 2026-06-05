// Package mesh holds the Yaver Mesh data plane — the optional WireGuard overlay
// that lets Yaver act as a Tailscale alternative. It is built in layers across
// phases:
//
//   - keys.go   (Phase 0) — WireGuard-compatible Curve25519 keypair generation.
//   - device.go (Phase 1) — wireguard-go userspace device + TUN bring-up.
//   - peers.go  (Phase 1) — peer reconciliation from the control plane.
//
// Phase 0 ships ONLY keys.go: pure crypto with no TUN, no privileged calls, and
// no dependency on wireguard-go yet, so it compiles and tests everywhere. The
// agent (package main) wires these keys to the vault and the Convex control
// plane; this package never touches the vault or the network itself.
package mesh

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"

	"golang.org/x/crypto/curve25519"
)

// KeyPair is a WireGuard-compatible Curve25519 keypair. Both halves are stored
// base64-encoded (std encoding), exactly as `wg genkey` / `wg pubkey` emit them,
// so the keys interoperate with any WireGuard implementation.
type KeyPair struct {
	// PrivateKey MUST stay on the device (vault only) — never synced to Convex.
	PrivateKey string
	// PublicKey is safe to publish to the control plane.
	PublicKey string
}

// GenerateKeyPair creates a fresh WireGuard keypair: 32 random bytes clamped per
// the Curve25519 spec for the private scalar, with the public key derived via
// scalar-base multiplication.
func GenerateKeyPair() (KeyPair, error) {
	var priv [32]byte
	if _, err := rand.Read(priv[:]); err != nil {
		return KeyPair{}, fmt.Errorf("mesh: read random: %w", err)
	}
	clamp(&priv)

	pub, err := curve25519.X25519(priv[:], curve25519.Basepoint)
	if err != nil {
		return KeyPair{}, fmt.Errorf("mesh: derive public key: %w", err)
	}

	return KeyPair{
		PrivateKey: base64.StdEncoding.EncodeToString(priv[:]),
		PublicKey:  base64.StdEncoding.EncodeToString(pub),
	}, nil
}

// PublicFromPrivate recomputes the public key for an existing base64 private
// key — used after loading the private half back from the vault so we never
// need to persist the public key separately.
func PublicFromPrivate(privB64 string) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(privB64)
	if err != nil {
		return "", fmt.Errorf("mesh: decode private key: %w", err)
	}
	if len(raw) != 32 {
		return "", fmt.Errorf("mesh: private key must be 32 bytes, got %d", len(raw))
	}
	pub, err := curve25519.X25519(raw, curve25519.Basepoint)
	if err != nil {
		return "", fmt.Errorf("mesh: derive public key: %w", err)
	}
	return base64.StdEncoding.EncodeToString(pub), nil
}

// clamp applies the standard Curve25519 private-scalar clamping that WireGuard
// uses, so generated keys are valid WireGuard private keys.
func clamp(k *[32]byte) {
	k[0] &= 248
	k[31] &= 127
	k[31] |= 64
}
