package main

import "testing"

// Parity test with backend/convex/http.ts::verifyLemonSqueezySignature.
// LS signs the raw webhook body with the shared secret using HMAC-SHA256
// and sends the hex digest in X-Signature. Both verifiers must accept
// the same digest and reject tampered bodies / wrong secrets.
func TestLSVerifySignature(t *testing.T) {
	secret := "test-secret-do-not-use"
	body := []byte(`{"meta":{"event_name":"subscription_created"}}`)
	// Precomputed: echo -n $body | openssl dgst -sha256 -hmac $secret
	good := "879c0e83c9908cd52c9cb96d8dafe47b610c18336f104ce60aad7c8f7e00b6d4"

	if !lsVerifySignature(body, good, secret) {
		t.Fatalf("valid signature rejected")
	}
	if lsVerifySignature(body, "deadbeef", secret) {
		t.Fatalf("invalid hex signature accepted")
	}
	if lsVerifySignature(body, good, "wrong-secret") {
		t.Fatalf("wrong secret accepted")
	}
	tampered := []byte(`{"meta":{"event_name":"subscription_cancelled"}}`)
	if lsVerifySignature(tampered, good, secret) {
		t.Fatalf("tampered body accepted")
	}
}
