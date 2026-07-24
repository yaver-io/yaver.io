package main

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// The relay is MULTI-TENANT and its code is public, so this cookie has to hold
// up to a reader who knows exactly how it is built. These pin the properties
// that make it safe to hand a browser.

const testSecret = "relay-secret-under-test"

func TestWebviewCookieRoundTrips(t *testing.T) {
	now := time.Now()
	v := mintWebviewCookieValue("device-abc", testSecret, now.Add(5*time.Minute))
	if !verifyWebviewCookieValue(v, "device-abc", testSecret, now) {
		t.Fatal("a freshly minted cookie must verify for its own device")
	}
}

// THE tenant boundary. A cookie minted for one device must never authorize
// another — that is the same-owner rule the relay already enforces, expressed
// in the cookie's own signature.
func TestWebviewCookieCannotBeReplayedAtAnotherDevice(t *testing.T) {
	now := time.Now()
	v := mintWebviewCookieValue("device-mine", testSecret, now.Add(5*time.Minute))
	if verifyWebviewCookieValue(v, "device-someone-else", testSecret, now) {
		t.Fatal("a cookie for one device authorized a DIFFERENT device — cross-tenant break")
	}
}

func TestWebviewCookieRejectsForgedSignature(t *testing.T) {
	now := time.Now()
	// Attacker knows the exact format and picks their own payload.
	forged := "device-abc." + "9999999999" + ".not-a-real-signature"
	if verifyWebviewCookieValue(forged, "device-abc", testSecret, now) {
		t.Fatal("a forged signature was accepted")
	}
	// And cannot mint one with the wrong secret.
	wrong := mintWebviewCookieValue("device-abc", "wrong-secret", now.Add(time.Minute))
	if verifyWebviewCookieValue(wrong, "device-abc", testSecret, now) {
		t.Fatal("a cookie signed with the wrong secret was accepted")
	}
}

// The expiry is inside the signed payload, so it cannot be extended by editing.
func TestWebviewCookieExpiryIsSignedAndEnforced(t *testing.T) {
	now := time.Now()
	expired := mintWebviewCookieValue("device-abc", testSecret, now.Add(-time.Second))
	if verifyWebviewCookieValue(expired, "device-abc", testSecret, now) {
		t.Fatal("an expired cookie was accepted")
	}
	// Tamper the expiry upward; the signature must now fail.
	valid := mintWebviewCookieValue("device-abc", testSecret, now.Add(time.Minute))
	parts := strings.Split(valid, ".")
	if len(parts) < 3 {
		t.Fatalf("unexpected cookie shape %q", valid)
	}
	tampered := parts[0] + ".9999999999." + parts[len(parts)-1]
	if verifyWebviewCookieValue(tampered, "device-abc", testSecret, now) {
		t.Fatal("an extended expiry was accepted — the expiry is not covered by the signature")
	}
}

func TestWebviewCookieRejectsJunk(t *testing.T) {
	now := time.Now()
	for _, v := range []string{"", "no-dots", ".", "a.b", "device-abc.notanumber.sig"} {
		if verifyWebviewCookieValue(v, "device-abc", testSecret, now) {
			t.Fatalf("junk cookie %q was accepted", v)
		}
	}
	// No secret configured => never authorize. An unsigned cookie must not be
	// a skeleton key on a password-less self-hosted relay.
	v := mintWebviewCookieValue("device-abc", testSecret, now.Add(time.Minute))
	if verifyWebviewCookieValue(v, "device-abc", "", now) {
		t.Fatal("verification succeeded with an empty secret")
	}
}

// The cookie must be scoped so a browser never even OFFERS it to another
// tenant's path, and never exposes it to page JS.
func TestWebviewCookieIsScopedAndHardened(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "https://relay.example/d/device-abc/dev/", nil)
	req.TLS = &tls.ConnectionState{}
	setWebviewAuthCookie(rec, req, "device-abc", testSecret)

	cookies := rec.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("expected exactly one cookie, got %d", len(cookies))
	}
	c := cookies[0]
	if c.Path != "/d/device-abc/" {
		t.Fatalf("cookie path %q is not scoped to the device subtree", c.Path)
	}
	if !c.HttpOnly {
		t.Fatal("cookie must be HttpOnly — a hostile previewed app could otherwise read it")
	}
	if !c.Secure {
		t.Fatal("cookie must be Secure on a TLS request")
	}
	if c.SameSite != http.SameSiteLaxMode {
		t.Fatal("cookie must set SameSite")
	}
	if c.MaxAge <= 0 || time.Duration(c.MaxAge)*time.Second > webviewCookieTTL {
		t.Fatalf("cookie MaxAge %d must be positive and within the TTL", c.MaxAge)
	}
	// The secret must never appear in what we hand the browser.
	if strings.Contains(c.Value, testSecret) {
		t.Fatal("the relay secret leaked into the cookie value")
	}
}

func TestWebviewCookieNotMintedWithoutSecret(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "http://relay.example/d/device-abc/dev/", nil)
	setWebviewAuthCookie(rec, req, "device-abc", "")
	if len(rec.Result().Cookies()) != 0 {
		t.Fatal("minted an unsigned cookie on a relay with no secret")
	}
}

func TestWebviewCookieAuthorizesOnlyWithMatchingRequest(t *testing.T) {
	now := time.Now()
	good := mintWebviewCookieValue("device-abc", testSecret, now.Add(time.Minute))

	req := httptest.NewRequest("GET", "http://relay.example/d/device-abc/dev/flutter.js", nil)
	req.AddCookie(&http.Cookie{Name: webviewCookieName, Value: good})
	if !webviewCookieAuthorizes(req, "device-abc", testSecret) {
		t.Fatal("a valid cookie did not authorize its own device")
	}
	if webviewCookieAuthorizes(req, "device-other", testSecret) {
		t.Fatal("cookie authorized a different device")
	}
	// No cookie at all.
	bare := httptest.NewRequest("GET", "http://relay.example/d/device-abc/dev/flutter.js", nil)
	if webviewCookieAuthorizes(bare, "device-abc", testSecret) {
		t.Fatal("a request with no cookie was authorized")
	}
}
