package main

import (
	"os"
	"path/filepath"
	"sort"
	"testing"
)

func TestBetaScrubMatcher(t *testing.T) {
	secret := []string{
		".env", ".env.local", ".env.production", "env.local",
		"config/.env", "apps/web/.env.development",
		"keys/yaver-upload.keystore", "keys/AuthKey_X.p8",
		"keystore.properties", "android/keystore.properties",
		"AuthKey_77Z6B543D5.p8", "cert.pem", "server.key", "id.p12",
		"google-play-service-account.json", "my-service-account.json",
		".npmrc", ".netrc", "credentials.json",
		"secrets/db.txt", ".ssh/id_rsa", "id_rsa", "id_ed25519",
		".aws/credentials", ".yaver/vault.enc", ".git/config",
		"deep/nested/.git/objects/x", "GoogleService-Info.plist",
		"app.mobileprovision", "terraform.tfstate",
	}
	for _, p := range secret {
		if !isBetaSecretPath(p) {
			t.Errorf("expected SECRET (withheld): %q", p)
		}
	}

	safe := []string{
		"package.json", "src/index.ts", "README.md", "app.json",
		".env.example", ".env.sample", ".env.template", ".env.dist",
		"id_rsa.pub", "id_ed25519.pub", "tsconfig.json",
		"components/Button.tsx", "public/logo.png", "go.mod",
		"data.json", "keymap.ts", // "key" substring must not over-match
		"monkey/index.js", // "key" inside a word/segment must not match
	}
	for _, p := range safe {
		if isBetaSecretPath(p) {
			t.Errorf("expected SAFE (copied): %q", p)
		}
	}
}

func TestSeedScrubbedCopy(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()
	mk := func(rel, body string) {
		p := filepath.Join(src, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mk("package.json", "{}")
	mk("src/app.ts", "x")
	mk(".env", "SECRET=1")
	mk("keys/upload.keystore", "binary")
	mk(".env.example", "SECRET=")

	withheld, err := seedScrubbedCopy(src, dst)
	if err != nil {
		t.Fatal(err)
	}

	// secrets must be absent in dst
	for _, rel := range []string{".env", "keys/upload.keystore"} {
		if _, err := os.Stat(filepath.Join(dst, rel)); !os.IsNotExist(err) {
			t.Errorf("secret leaked into tenant copy: %q", rel)
		}
	}
	// safe files must be present
	for _, rel := range []string{"package.json", "src/app.ts", ".env.example"} {
		if _, err := os.Stat(filepath.Join(dst, rel)); err != nil {
			t.Errorf("safe file missing from tenant copy: %q (%v)", rel, err)
		}
	}
	sort.Strings(withheld)
	if len(withheld) < 2 {
		t.Errorf("expected ≥2 withheld, got %v", withheld)
	}
}
