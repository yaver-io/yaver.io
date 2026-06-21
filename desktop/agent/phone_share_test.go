package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// The friends-preview share/join contract: a host mints a code for a project;
// a friend resolves it to {slug, runtime, dataUrl, bundleUrl} and Hermes-loads
// against the host's Yaver Serverless Lite backend. Codes are P2P (on-agent
// files), case-insensitive, and self-expire.
func TestPhoneShare_CreateResolveExpire(t *testing.T) {
	setupPhoneTestHome(t)
	p, err := CreatePhoneProject(PhoneCreateSpec{Name: "Share Me", Template: "todos"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	sh, err := CreatePhoneShare(p.Slug, time.Hour)
	if err != nil {
		t.Fatalf("share: %v", err)
	}
	if len(sh.Code) < 4 {
		t.Errorf("code too short: %q", sh.Code)
	}
	if sh.Slug != p.Slug {
		t.Errorf("slug = %q, want %q", sh.Slug, p.Slug)
	}
	if sh.Runtime != "yaver-serverless-lite" {
		t.Errorf("runtime = %q, want yaver-serverless-lite", sh.Runtime)
	}
	if sh.DataURL != "/data/"+p.Slug {
		t.Errorf("dataUrl = %q, want /data/%s", sh.DataURL, p.Slug)
	}
	if sh.HostedConvexURL != "" {
		t.Errorf("legacy hostedConvexUrl should be empty for serverless shares: %q", sh.HostedConvexURL)
	}
	if sh.BundleURL == "" || sh.BundleURL[:1] != "/" {
		t.Errorf("bundleUrl should be a relative path: %q", sh.BundleURL)
	}

	// Resolve is case-insensitive (friend types it lowercased).
	got, err := ResolvePhoneShare("  " + lower(sh.Code) + "  ")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got.Slug != p.Slug || got.Runtime != sh.Runtime || got.DataURL != sh.DataURL {
		t.Errorf("resolved share mismatch: %+v", got)
	}

	// Unknown code → typed not-found.
	if _, err := ResolvePhoneShare("ZZZZZZ"); !errors.Is(err, ErrPhoneShareNotFound) {
		t.Errorf("unknown code err = %v, want ErrPhoneShareNotFound", err)
	}

	// Expired code is rejected AND self-cleaned from disk.
	dir, _ := phoneSharesDir()
	f := filepath.Join(dir, sh.Code+".json")
	expired := *sh
	expired.ExpiresAt = time.Now().Add(-time.Minute).UTC().Format(time.RFC3339)
	b, _ := json.Marshal(expired)
	if err := os.WriteFile(f, b, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ResolvePhoneShare(sh.Code); !errors.Is(err, ErrPhoneShareNotFound) {
		t.Errorf("expired code err = %v, want ErrPhoneShareNotFound", err)
	}
	if _, statErr := os.Stat(f); !os.IsNotExist(statErr) {
		t.Error("expired share file should have been removed")
	}
}

func lower(s string) string {
	out := []byte(s)
	for i, c := range out {
		if c >= 'A' && c <= 'Z' {
			out[i] = c + 32
		}
	}
	return string(out)
}
