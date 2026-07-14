package main

import (
	"os"
	"path/filepath"
	"testing"
)

// The env fallback exists so a locked vault degrades instead of stranding every
// store verb. It must NEVER apply to a named (third-party) project: doing so
// would submit another developer's app with the operator's App Store key.
func TestASCCredsEnvFallbackIsOwnProjectOnly(t *testing.T) {
	dir := t.TempDir()
	key := filepath.Join(dir, "AuthKey_TEST.p8")
	if err := os.WriteFile(key, []byte("-----BEGIN PRIVATE KEY-----\ntest\n-----END PRIVATE KEY-----\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("APP_STORE_KEY_PATH", key)
	t.Setenv("APP_STORE_KEY_ID", "OPERATORKEY")
	t.Setenv("APP_STORE_KEY_ISSUER", "operator-issuer")

	// Own project: the fallback is allowed to rescue a locked/absent vault.
	for _, own := range []string{"", "mobile"} {
		if c, err := resolveAppleASCCreds(own); err != nil || c == nil || c.KeyID != "OPERATORKEY" {
			t.Fatalf("own project %q: expected env fallback, got creds=%v err=%v", own, c, err)
		}
	}

	// Third-party project: MUST NOT silently pick up the operator's key.
	c, err := resolveAppleASCCreds("acme-thirdparty")
	if err == nil && c != nil && c.KeyID == "OPERATORKEY" {
		t.Fatal("CROSS-TENANT LEAK: third-party project resolved to the operator's ASC key from env")
	}
}

// Absent env, the fallback must return nil rather than a half-built cred.
func TestASCCredsFromEnvRequiresAllThree(t *testing.T) {
	t.Setenv("APP_STORE_KEY_PATH", "")
	t.Setenv("APP_STORE_KEY_ID", "X")
	t.Setenv("APP_STORE_KEY_ISSUER", "Y")
	if c := ascCredsFromEnv(); c != nil {
		t.Fatalf("expected nil with missing key path, got %+v", c)
	}
}
