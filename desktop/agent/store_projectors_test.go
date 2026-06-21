package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"strings"
	"testing"
)

func sampleListing() StoreListing {
	return StoreListing{
		AppName:     "Receipt Scanner",
		Subtitle:    "Snap & track expenses",
		Description: "desc",
		Keywords:    []string{"receipt", "expense"},
		WhatsNew:    "First release",
		Privacy: []DataCollection{
			{Category: "Location"}, {Category: "UsageData"},
		},
		Screenshots: append([]ScreenshotSlot(nil), requiredScreenshotSlots...),
	}
}

func TestApplePushPlanIsFullyAutomatable(t *testing.T) {
	p := buildApplePushPlan(sampleListing())
	if p.ConsoleCount != 0 {
		t.Errorf("Apple should be fully API-driven, got %d console actions", p.ConsoleCount)
	}
	if p.AutomatableCount == 0 {
		t.Error("expected API actions")
	}
	// Privacy must be pushed via the appDataUsages endpoint.
	if !hasAction(p, "appPrivacy", true) {
		t.Error("appPrivacy should be an automatable ASC action")
	}
}

func TestGooglePushPlanSplitsConsoleForms(t *testing.T) {
	p := buildGooglePushPlan(sampleListing())
	// Listing text/images push via API…
	if !hasAction(p, "fullDescription", true) {
		t.Error("fullDescription should be API-automatable on Play")
	}
	// …but Data Safety + content rating are Console-only.
	if !hasAction(p, "dataSafety", false) {
		t.Error("dataSafety must be flagged Console-only on Play")
	}
	if !hasAction(p, "contentRating", false) {
		t.Error("contentRating must be flagged Console-only on Play")
	}
	if p.ConsoleCount < 2 {
		t.Errorf("expected ≥2 console actions, got %d", p.ConsoleCount)
	}
}

func TestBuildPushPlanUnknownStore(t *testing.T) {
	if _, err := buildPushPlan("windows", sampleListing()); err == nil {
		t.Error("unknown store should error")
	}
}

func TestMintASCJWT(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	keyPEM := string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))

	tok, err := mintASCJWT(keyPEM, "ABC123KEY", "issuer-uuid", 1_700_000_000)
	if err != nil {
		t.Fatalf("mintASCJWT: %v", err)
	}
	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		t.Fatalf("JWT should have 3 segments, got %d", len(parts))
	}
	// Header carries ES256 + the key id.
	hb, _ := base64.RawURLEncoding.DecodeString(parts[0])
	var hdr map[string]string
	_ = json.Unmarshal(hb, &hdr)
	if hdr["alg"] != "ES256" || hdr["kid"] != "ABC123KEY" {
		t.Errorf("bad header: %v", hdr)
	}
	// Claims carry the issuer + the ASC audience.
	cb, _ := base64.RawURLEncoding.DecodeString(parts[1])
	var claims map[string]interface{}
	_ = json.Unmarshal(cb, &claims)
	if claims["iss"] != "issuer-uuid" || claims["aud"] != "appstoreconnect-v1" {
		t.Errorf("bad claims: %v", claims)
	}
	// Signature must verify against the public key.
	if !verifyASCJWTForTest(tok, &key.PublicKey) {
		t.Error("ASC JWT signature failed to verify")
	}
}

func TestMintASCJWTRejectsBadKey(t *testing.T) {
	if _, err := mintASCJWT("not a pem", "k", "i", 1); err == nil {
		t.Error("expected error on non-PEM key")
	}
}

func hasAction(p PushPlan, field string, automatable bool) bool {
	for _, a := range p.Actions {
		if a.Field == field && a.Automatable == automatable {
			return true
		}
	}
	return false
}
