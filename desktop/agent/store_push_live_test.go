package main

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"strings"
	"testing"
)

func TestBuildGoogleJWTGrant(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	der, _ := x509.MarshalPKCS8PrivateKey(key)
	keyPEM := string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))

	grant, err := buildGoogleJWTGrant(
		"svc@proj.iam.gserviceaccount.com", keyPEM,
		"https://www.googleapis.com/auth/androidpublisher",
		"https://oauth2.googleapis.com/token", 1_700_000_000,
	)
	if err != nil {
		t.Fatalf("buildGoogleJWTGrant: %v", err)
	}
	parts := strings.Split(grant, ".")
	if len(parts) != 3 {
		t.Fatalf("grant should have 3 segments, got %d", len(parts))
	}

	var hdr map[string]string
	hb, _ := base64.RawURLEncoding.DecodeString(parts[0])
	_ = json.Unmarshal(hb, &hdr)
	if hdr["alg"] != "RS256" {
		t.Errorf("alg = %q, want RS256", hdr["alg"])
	}

	var claims map[string]interface{}
	cb, _ := base64.RawURLEncoding.DecodeString(parts[1])
	_ = json.Unmarshal(cb, &claims)
	if claims["iss"] != "svc@proj.iam.gserviceaccount.com" {
		t.Errorf("iss = %v", claims["iss"])
	}
	if claims["scope"] != "https://www.googleapis.com/auth/androidpublisher" {
		t.Errorf("scope = %v", claims["scope"])
	}

	// Signature must verify against the public key (RS256 over header.payload).
	sig, _ := base64.RawURLEncoding.DecodeString(parts[2])
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if err := rsa.VerifyPKCS1v15(&key.PublicKey, crypto.SHA256, digest[:], sig); err != nil {
		t.Errorf("grant signature failed to verify: %v", err)
	}
}

func TestBuildGoogleJWTGrantRejectsBadKey(t *testing.T) {
	if _, err := buildGoogleJWTGrant("svc", "not a pem", "scope", "aud", 1); err == nil {
		t.Error("expected error on non-PEM key")
	}
}
