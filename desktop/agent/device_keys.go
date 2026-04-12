package main

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/crypto/nacl/box"
)

const keyFileName = "device.key"

// DeviceKeys holds the X25519 keypair for encrypted pairing.
type DeviceKeys struct {
	PublicKey  [32]byte
	PrivateKey [32]byte
}

// LoadOrGenerateKeys loads the device keypair from ~/.yaver/device.key,
// or generates a new one if it doesn't exist. The file stores 64 bytes:
// 32-byte public key + 32-byte private key, base64-encoded.
func LoadOrGenerateKeys() (*DeviceKeys, error) {
	dir, err := ConfigDir()
	if err != nil {
		return nil, err
	}
	path := filepath.Join(dir, keyFileName)

	data, err := os.ReadFile(path)
	if err == nil {
		raw, decErr := base64.StdEncoding.DecodeString(string(data))
		if decErr == nil && len(raw) == 64 {
			var dk DeviceKeys
			copy(dk.PublicKey[:], raw[:32])
			copy(dk.PrivateKey[:], raw[32:])
			return &dk, nil
		}
	}

	pub, priv, err := box.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate X25519 keypair: %w", err)
	}
	dk := &DeviceKeys{PublicKey: *pub, PrivateKey: *priv}

	os.MkdirAll(dir, 0o700)
	encoded := base64.StdEncoding.EncodeToString(append(dk.PublicKey[:], dk.PrivateKey[:]...))
	if err := os.WriteFile(path, []byte(encoded), 0o600); err != nil {
		return nil, fmt.Errorf("save device key: %w", err)
	}
	return dk, nil
}

// PublicKeyBase64 returns the public key as a base64 string for Convex storage.
func (dk *DeviceKeys) PublicKeyBase64() string {
	return base64.StdEncoding.EncodeToString(dk.PublicKey[:])
}

// DecryptPairPayload decrypts a NaCl box sealed by the phone's private key
// to this device's public key. senderPub is the phone's public key (from
// Convex). Returns the plaintext (the OAuth token).
func (dk *DeviceKeys) DecryptPairPayload(encrypted []byte, senderPub [32]byte) ([]byte, error) {
	if len(encrypted) < 24 {
		return nil, fmt.Errorf("encrypted payload too short")
	}
	var nonce [24]byte
	copy(nonce[:], encrypted[:24])
	plaintext, ok := box.Open(nil, encrypted[24:], &nonce, &senderPub, &dk.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("decryption failed — wrong key or corrupted payload")
	}
	return plaintext, nil
}
