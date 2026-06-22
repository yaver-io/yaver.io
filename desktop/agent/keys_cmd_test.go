package main

import (
	"crypto/x509"
	"encoding/pem"
	"strings"
	"testing"
)

func TestGenerateECKeyCSR(t *testing.T) {
	keyPEM, csrPEM, err := generateECKeyCSR("Receipt Scanner")
	if err != nil {
		t.Fatalf("generateECKeyCSR: %v", err)
	}
	// Private key parses as PKCS#8 EC.
	kb, _ := pem.Decode([]byte(keyPEM))
	if kb == nil {
		t.Fatal("key not PEM")
	}
	if _, err := x509.ParsePKCS8PrivateKey(kb.Bytes); err != nil {
		t.Fatalf("private key parse: %v", err)
	}
	// CSR parses, has the right CN, and a valid signature.
	cb, _ := pem.Decode([]byte(csrPEM))
	if cb == nil {
		t.Fatal("csr not PEM")
	}
	csr, err := x509.ParseCertificateRequest(cb.Bytes)
	if err != nil {
		t.Fatalf("csr parse: %v", err)
	}
	if csr.Subject.CommonName != "Receipt Scanner" {
		t.Errorf("CN = %q", csr.Subject.CommonName)
	}
	if err := csr.CheckSignature(); err != nil {
		t.Errorf("csr signature invalid: %v", err)
	}
}

func TestExtractSHA1(t *testing.T) {
	out := `Certificate fingerprints:
	 SHA1: AB:CD:EF:01:23:45:67:89:AB:CD:EF:01:23:45:67:89:AB:CD:EF:01
	 SHA256: ...`
	got := extractSHA1(out)
	if got != "AB:CD:EF:01:23:45:67:89:AB:CD:EF:01:23:45:67:89:AB:CD:EF:01" {
		t.Errorf("SHA1 = %q", got)
	}
	// Hyphenated label variant.
	if extractSHA1("SHA-1: 11:22:33:44:55:66:77:88:99:00:AA:BB:CC:DD:EE:FF:11:22:33:44") == "" {
		t.Error("should parse SHA-1: variant")
	}
	if extractSHA1("no fingerprint here") != "" {
		t.Error("no match should return empty")
	}
}

func TestGenPassword(t *testing.T) {
	a, err := genPassword(24)
	if err != nil {
		t.Fatal(err)
	}
	if len(a) < 24 {
		t.Errorf("password too short: %d", len(a))
	}
	b, _ := genPassword(24)
	if a == b {
		t.Error("two generated passwords must differ")
	}
	if strings.ContainsAny(a, "+/=") {
		t.Error("password should be URL-safe (no +/=)")
	}
}
