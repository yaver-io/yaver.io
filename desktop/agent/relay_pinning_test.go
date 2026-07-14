package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"testing"
	"time"
)

// makeSelfSignedCert builds a self-signed cert exactly the way the relay does
// (relay/server.go: ecdsa P-256, self-signed), so the pin computed here matches
// what the agent would compute against a real relay.
func makeSelfSignedCert(t *testing.T) *x509.Certificate {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "yaver-relay"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &priv.PublicKey, priv)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return cert
}

func TestSpkiPinIsStableAndKeyBound(t *testing.T) {
	cert := makeSelfSignedCert(t)
	pin1 := spkiPinOfCert(cert)
	if pin1 == "" {
		t.Fatal("empty pin")
	}
	// Same cert -> same pin (deterministic).
	if pin2 := spkiPinOfCert(cert); pin1 != pin2 {
		t.Fatalf("pin not deterministic: %s vs %s", pin1, pin2)
	}
	// A DIFFERENT key -> a different pin. This is the security property: a MITM
	// with its own key cannot match the pin.
	other := makeSelfSignedCert(t)
	if spkiPinOfCert(other) == pin1 {
		t.Fatal("two independent keys produced the same pin — pin is not key-bound")
	}
}

func TestNormalizePinToleratesOperatorPaste(t *testing.T) {
	want := "abc123=="
	for _, in := range []string{
		"abc123==",
		"  abc123==  ",
		"sha256/abc123==",
		"sha256:abc123==",
		"\tsha256/abc123==\n",
	} {
		if got := normalizePin(in); got != want {
			t.Errorf("normalizePin(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRelayTLSConfigWithoutPinIsPermissive(t *testing.T) {
	// No pin: encrypted but unverified — must NOT install a verifier (that would
	// break every relay that hasn't published a pin yet).
	cfg := relayTLSConfig("relay.example:4433", "")
	if !cfg.InsecureSkipVerify {
		t.Error("expected InsecureSkipVerify without a pin")
	}
	if cfg.VerifyPeerCertificate != nil {
		t.Error("no pin configured but a peer verifier was installed — rollout would break unpinned relays")
	}
	if len(cfg.NextProtos) == 0 || cfg.NextProtos[0] != "yaver-relay" {
		t.Errorf("wrong ALPN: %v", cfg.NextProtos)
	}
}

func TestRelayTLSConfigPinAcceptsMatchRejectsMitm(t *testing.T) {
	real := makeSelfSignedCert(t)
	pin := spkiPinOfCert(real)

	cfg := relayTLSConfig("relay.example:4433", pin)
	if cfg.VerifyPeerCertificate == nil {
		t.Fatal("pin configured but no verifier installed")
	}

	// The genuine relay's cert must pass.
	if err := cfg.VerifyPeerCertificate([][]byte{real.Raw}, nil); err != nil {
		t.Errorf("genuine relay cert rejected: %v", err)
	}

	// A MITM presenting a different key must be refused — no fallback to accept.
	mitm := makeSelfSignedCert(t)
	if err := cfg.VerifyPeerCertificate([][]byte{mitm.Raw}, nil); err == nil {
		t.Fatal("MITM cert ACCEPTED under a pin — confidentiality is not protected")
	}

	// An empty presentation must be refused, not treated as a match.
	if err := cfg.VerifyPeerCertificate(nil, nil); err == nil {
		t.Error("empty cert chain accepted under a pin")
	}
}

// The pin survives cert reissue as long as the KEY is reused — the whole reason
// we pin SPKI and not the cert. Relay rotating its self-signed cert (new serial,
// new validity) must not break pinned agents.
func TestPinSurvivesCertReissueWithSameKey(t *testing.T) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	mkCert := func(serial int64) *x509.Certificate {
		tmpl := &x509.Certificate{
			SerialNumber: big.NewInt(serial),
			Subject:      pkix.Name{CommonName: "yaver-relay"},
			NotBefore:    time.Now().Add(-time.Hour),
			NotAfter:     time.Now().Add(24 * time.Hour),
		}
		der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &priv.PublicKey, priv)
		if err != nil {
			t.Fatal(err)
		}
		c, err := x509.ParseCertificate(der)
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	first := mkCert(1)
	reissued := mkCert(2) // same key, different cert
	if spkiPinOfCert(first) != spkiPinOfCert(reissued) {
		t.Fatal("pin changed on cert reissue with the same key — would break pinned agents on every relay restart")
	}
}
