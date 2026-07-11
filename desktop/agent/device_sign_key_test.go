package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"testing"
)

func TestSignRelayRequest_Verifies(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	sk := &DeviceSigningKey{Public: pub, Private: priv}
	body := []byte(`{"x":1}`)

	h := sk.SignRelayRequest("dev1", "POST", "/d/dev1/ops", body, 1700000000000, "n1")
	if h["Authorization"] != "Yaver-Sig v1" || h["X-Yaver-Device"] != "dev1" {
		t.Fatalf("unexpected headers: %v", h)
	}

	canonical := canonicalRelaySigString("POST", "/d/dev1/ops", "dev1", "1700000000000", "n1", body)
	sig, err := base64.StdEncoding.DecodeString(h["X-Yaver-Signature"])
	if err != nil {
		t.Fatal(err)
	}
	if !ed25519.Verify(pub, []byte(canonical), sig) {
		t.Fatal("signed relay request does not verify against its own public key")
	}
}

// TestCanonicalRelaySigString_Golden must match relay/sigauth_test.go's golden
// so the two packages stay wire-compatible.
func TestCanonicalRelaySigString_Golden(t *testing.T) {
	got := canonicalRelaySigString("POST", "/d/dev1/ops", "dev1", "1700000000000", "n1", []byte("hi"))
	sum := sha256.Sum256([]byte("hi"))
	want := "POST\n/d/dev1/ops\ndev1\n1700000000000\nn1\n" + hex.EncodeToString(sum[:])
	if got != want {
		t.Fatalf("canonical drift: %q", got)
	}
}
