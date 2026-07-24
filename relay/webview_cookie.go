package main

// webview_cookie.go — let a WebView's SUB-RESOURCES authenticate to the relay.
//
// ── The problem (2026-07-24) ──────────────────────────────────────────────────
//
// The relay authenticates EVERY proxied request (X-Relay-Password header, or
// ?__rp= in the query). That is fine for an API client, and impossible for a
// browser rendering a page:
//
//	1. the phone's WebView loads /d/<deviceId>/dev/?token=…&__rp=…   -> 200 OK
//	2. that HTML says <script src="flutter.js">
//	3. the browser resolves it to /d/<deviceId>/dev/flutter.js — with NO query
//	   string, because relative URLs do not inherit one, and no header, because
//	   a WebView cannot set one
//	4. the relay 401s it
//
// So the document loads and every asset it needs dies. Flutter's engine never
// boots, the mobile preview overlay waits forever on a page that can never
// paint. This broke web preview over relay for EVERY framework, not just
// Flutter — anything whose page pulls a second file.
//
// Static URL rewriting cannot fix it: Flutter fetches main.dart.js and canvaskit
// from inside JS at runtime, so there is no markup to rewrite.
//
// ── The fix, and why it is not a weakening ────────────────────────────────────
//
// On a request that ALREADY authenticated by the normal means, the relay hands
// back a cookie that authenticates only that device's subtree. The browser then
// attaches it to sub-resource requests automatically.
//
// The cookie carries NO secret. It is `<deviceId>.<expiryUnix>.<hmac>` where the
// HMAC is over that same tuple keyed by the relay's existing secret. The relay
// verifies the signature; it can be forged only by someone who already holds the
// secret, in which case they did not need a cookie. Compared to the status quo —
// the relay password sitting in a query string, where it lands in access logs,
// browser history and Referer headers — this is strictly BETTER for the
// credential.
//
// Scoping, all deliberate:
//   - Path=/d/<deviceId>/  — the browser only ever sends it to THAT device's
//     subtree, so it cannot be replayed against another tenant's box. This is
//     the same-owner boundary the relay already enforces, expressed in the
//     cookie's own scope.
//   - HttpOnly             — page JS cannot read it, so a hostile guest app
//     rendered in the preview cannot exfiltrate it.
//   - SameSite=Lax         — not attached to cross-site form posts.
//   - Secure when the request arrived over TLS.
//   - Short TTL            — minutes, not a session. Long enough to load a page
//     and its assets, short enough that a leaked cookie is quickly worthless.
//
// What this does NOT change: the relay still authorizes nothing beyond "may I
// carry these bytes". The AGENT still authenticates the caller's bearer token
// and enforces its own scope allow-lists, exactly as before. A cookie gets an
// asset through the tunnel; it does not get anyone into a box.

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// webviewCookieName is the cookie the relay sets and accepts.
const webviewCookieName = "yaver_rp"

// webviewCookieTTL bounds how long a minted cookie stays valid. Long enough for
// a slow first page + its assets over a phone connection; short enough that a
// leaked value expires before it is useful.
const webviewCookieTTL = 15 * time.Minute

// mintWebviewCookieValue builds `<deviceId>.<expiryUnix>.<sig>`.
//
// The signature covers the device id AND the expiry, so neither can be edited
// without invalidating it — a cookie for device A cannot be re-pointed at
// device B, and an expiry cannot be pushed into the future.
func mintWebviewCookieValue(deviceID string, secret string, expiry time.Time) string {
	payload := deviceID + "." + strconv.FormatInt(expiry.Unix(), 10)
	return payload + "." + signWebviewPayload(payload, secret)
}

func signWebviewPayload(payload, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// verifyWebviewCookieValue reports whether `value` is a signature-valid,
// unexpired cookie for `deviceID`.
//
// Every failure returns false with no detail — a caller learns "no", never
// which part was wrong, so the endpoint cannot be used as an oracle. The
// signature comparison is constant-time.
func verifyWebviewCookieValue(value, deviceID, secret string, now time.Time) bool {
	if value == "" || deviceID == "" || secret == "" {
		return false
	}
	// SplitN with 3: the signature is base64url and contains no '.', while a
	// deviceId might, so bind the split to the KNOWN shape from the right.
	lastDot := strings.LastIndex(value, ".")
	if lastDot <= 0 {
		return false
	}
	payload, sig := value[:lastDot], value[lastDot+1:]
	if !hmac.Equal([]byte(sig), []byte(signWebviewPayload(payload, secret))) {
		return false
	}
	// Payload must be exactly <deviceId>.<expiry> for THIS device.
	expDot := strings.LastIndex(payload, ".")
	if expDot <= 0 {
		return false
	}
	gotDevice, expStr := payload[:expDot], payload[expDot+1:]
	if !hmac.Equal([]byte(gotDevice), []byte(deviceID)) {
		return false
	}
	exp, err := strconv.ParseInt(expStr, 10, 64)
	if err != nil {
		return false
	}
	return now.Unix() < exp
}

// webviewCookiePath scopes the cookie to one device's proxy subtree, so the
// browser never offers it to another tenant's path.
func webviewCookiePath(deviceID string) string {
	return "/d/" + deviceID + "/"
}

// setWebviewAuthCookie attaches a freshly-minted cookie to an already-authorized
// response. No-op when there is no secret to sign with (a self-hosted relay
// running without a password): minting an unsigned cookie would be worse than
// having none.
func setWebviewAuthCookie(w http.ResponseWriter, r *http.Request, deviceID, secret string) {
	if deviceID == "" || secret == "" {
		return
	}
	expiry := time.Now().Add(webviewCookieTTL)
	http.SetCookie(w, &http.Cookie{
		Name:     webviewCookieName,
		Value:    mintWebviewCookieValue(deviceID, secret, expiry),
		Path:     webviewCookiePath(deviceID),
		Expires:  expiry,
		MaxAge:   int(webviewCookieTTL / time.Second),
		HttpOnly: true,
		Secure:   requestIsTLS(r),
		SameSite: http.SameSiteLaxMode,
	})
}

// webviewCookieAuthorizes reports whether the request carries a valid cookie for
// this device.
func webviewCookieAuthorizes(r *http.Request, deviceID, secret string) bool {
	if secret == "" {
		return false
	}
	c, err := r.Cookie(webviewCookieName)
	if err != nil || c == nil {
		return false
	}
	return verifyWebviewCookieValue(c.Value, deviceID, secret, time.Now())
}

// requestIsTLS reports whether the ORIGINAL client request was https, honouring
// the standard proxy header so a relay behind a TLS terminator still marks the
// cookie Secure.
func requestIsTLS(r *http.Request) bool {
	if r == nil {
		return false
	}
	if r.TLS != nil {
		return true
	}
	return strings.EqualFold(strings.TrimSpace(firstHeaderValue(r.Header.Get("X-Forwarded-Proto"))), "https")
}

func firstHeaderValue(v string) string {
	if i := strings.IndexByte(v, ','); i >= 0 {
		return v[:i]
	}
	return v
}

var _ = fmt.Sprintf
