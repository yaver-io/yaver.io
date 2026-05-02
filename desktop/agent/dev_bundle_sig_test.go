package main

// dev_bundle_sig_test.go — locks in the C-4 contract: every dev bundle
// URL must be signed with an HMAC + expiry; loopback callers + cookie
// callers are special-cased so the iframe + the local SDK keep working.

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestSignDevBundleURLNative(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	q, err := signDevBundleURL("build-abc", "native", 30*time.Second)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	if !strings.Contains(q, "build=build-abc") {
		t.Errorf("expected build= in %q", q)
	}
	if !strings.Contains(q, "exp=") || !strings.Contains(q, "sig=") {
		t.Errorf("expected exp=/sig= in %q", q)
	}
}

func TestVerifyDevBundleSigAcceptsValid(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	q, err := signDevBundleURL("build-1", "native", 30*time.Second)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	r := httptest.NewRequest(http.MethodGet, "/dev/native-bundle?"+q, nil)
	r.RemoteAddr = "203.0.113.5:55555" // public IP — must use sig
	if err := verifyDevBundleSig("build-1", "native", r); err != nil {
		t.Fatalf("verify rejected valid URL: %v", err)
	}
}

func TestVerifyDevBundleSigRejectsMissing(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	r := httptest.NewRequest(http.MethodGet, "/dev/native-bundle", nil)
	r.RemoteAddr = "203.0.113.5:55555"
	if err := verifyDevBundleSig("build-1", "native", r); err == nil {
		t.Error("verify accepted unsigned request from public IP")
	}
}

func TestVerifyDevBundleSigRejectsExpired(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	// Mint a valid sig, then craft a URL that swaps in an expired exp.
	// We can't ask signDevBundleURL for a negative TTL because it
	// clamps to the default; we rebuild the URL by hand using a known
	// exp the verifier will reject.
	q, err := signDevBundleURL("build-1", "native", 30*time.Second)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	// Replace exp with one in the past — the sig won't match either,
	// so this also exercises the "bad signature" branch. Both errors
	// produce a 403; we just want to confirm the request is rejected.
	q = strings.Replace(q, "exp=", "exp=1&_old=", 1) // garbage exp value
	r := httptest.NewRequest(http.MethodGet, "/dev/native-bundle?"+q, nil)
	r.RemoteAddr = "203.0.113.5:55555"
	if err := verifyDevBundleSig("build-1", "native", r); err == nil {
		t.Error("verify accepted expired URL")
	}
}

func TestVerifyDevBundleSigRejectsBuildMismatch(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	q, err := signDevBundleURL("build-A", "native", 30*time.Second)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	r := httptest.NewRequest(http.MethodGet, "/dev/native-bundle?"+q, nil)
	r.RemoteAddr = "203.0.113.5:55555"
	// Caller asks us to serve build-B but URL signed for build-A.
	if err := verifyDevBundleSig("build-B", "native", r); err == nil {
		t.Error("verify accepted cross-build URL")
	}
}

func TestVerifyDevBundleSigRejectsKindMismatch(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	// Sign a "native" URL but try to use it as "assets".
	q, err := signDevBundleURL("build-1", "native", 30*time.Second)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	r := httptest.NewRequest(http.MethodGet, "/dev/native-assets?"+q, nil)
	r.RemoteAddr = "203.0.113.5:55555"
	if err := verifyDevBundleSig("build-1", "assets", r); err == nil {
		t.Error("verify accepted kind-mismatched URL (native sig used for assets)")
	}
}

func TestVerifyDevBundleSigSkipsLoopback(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	r := httptest.NewRequest(http.MethodGet, "/dev/native-bundle", nil)
	r.RemoteAddr = "127.0.0.1:55555"
	r.Host = "127.0.0.1:18080"
	if err := verifyDevBundleSig("build-1", "native", r); err != nil {
		t.Errorf("loopback request must pass without sig: %v", err)
	}
}

func TestVerifyDevBundleSigAcceptsCookieForWeb(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	// Sign a web-bundle URL, then use the cookie path (no query).
	q, err := signDevBundleURL("", "web", 30*time.Second)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	// Build the cookie value from the signed query.
	var exp, sig string
	for _, p := range strings.Split(q, "&") {
		switch {
		case strings.HasPrefix(p, "exp="):
			exp = strings.TrimPrefix(p, "exp=")
		case strings.HasPrefix(p, "sig="):
			sig = strings.TrimPrefix(p, "sig=")
		}
	}
	r := httptest.NewRequest(http.MethodGet, "/dev/web-bundle/foo.js", nil)
	r.RemoteAddr = "203.0.113.5:55555"
	r.AddCookie(&http.Cookie{Name: "yaver-dev-web-sig", Value: exp + ":" + sig})
	if err := verifyDevBundleSig("", "web", r); err != nil {
		t.Errorf("verify rejected cookie-authed web bundle fetch: %v", err)
	}
}
