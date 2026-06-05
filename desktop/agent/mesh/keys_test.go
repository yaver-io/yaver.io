package mesh

import (
	"encoding/base64"
	"testing"
)

func TestGenerateKeyPair_validAndDistinct(t *testing.T) {
	a, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair: %v", err)
	}
	b, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair: %v", err)
	}
	if a.PrivateKey == b.PrivateKey {
		t.Fatal("two keypairs share a private key — randomness broken")
	}
	for _, k := range []string{a.PrivateKey, a.PublicKey} {
		raw, err := base64.StdEncoding.DecodeString(k)
		if err != nil {
			t.Fatalf("key %q not valid base64: %v", k, err)
		}
		if len(raw) != 32 {
			t.Fatalf("key must decode to 32 bytes, got %d", len(raw))
		}
	}
}

func TestGenerateKeyPair_privateIsClamped(t *testing.T) {
	kp, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair: %v", err)
	}
	raw, _ := base64.StdEncoding.DecodeString(kp.PrivateKey)
	if raw[0]&7 != 0 {
		t.Errorf("low 3 bits of byte 0 must be cleared, got %08b", raw[0])
	}
	if raw[31]&0x80 != 0 {
		t.Errorf("high bit of byte 31 must be cleared, got %08b", raw[31])
	}
	if raw[31]&0x40 == 0 {
		t.Errorf("bit 6 of byte 31 must be set, got %08b", raw[31])
	}
}

func TestPublicFromPrivate_matchesGenerated(t *testing.T) {
	kp, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair: %v", err)
	}
	got, err := PublicFromPrivate(kp.PrivateKey)
	if err != nil {
		t.Fatalf("PublicFromPrivate: %v", err)
	}
	if got != kp.PublicKey {
		t.Fatalf("recomputed public key %q != generated %q", got, kp.PublicKey)
	}
}

func TestPublicFromPrivate_rejectsBadInput(t *testing.T) {
	if _, err := PublicFromPrivate("not-base64!!!"); err == nil {
		t.Error("expected error on non-base64 input")
	}
	if _, err := PublicFromPrivate(base64.StdEncoding.EncodeToString([]byte("short"))); err == nil {
		t.Error("expected error on wrong-length key")
	}
}
