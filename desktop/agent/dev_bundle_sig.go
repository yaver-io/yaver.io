package main

// dev_bundle_sig.go — HMAC-signed URLs for /dev/native-bundle,
// /dev/native-assets, /dev/web-bundle/. C-4 in security_audit.md.
//
// Pre-fix the three handlers above were unauthenticated by design
// ("the iframe + the phone need to fetch them, neither carries an
// agent bearer"). On a public-IP agent this leaked the compiled
// Hermes bundle and the built web bundle (full transpiled source) to
// any scanner.
//
// The owner now mints a short-lived signed URL via /dev/build-native
// (or /dev/web-bundle/info) — both auth'd. The signature covers
// (build-id | kind | exp), kind ∈ {native, assets, web}, exp is unix
// seconds. Default TTL: 10 min — long enough for a phone or iframe
// to fetch the bundle, short enough that a leaked URL becomes useless
// before the next build cycle.
//
// Reuses the same persisted secret as /blobs/public (blobSecret()) —
// the threat profile is identical (offline-capable HMAC, agent-local).

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const defaultDevBundleTTL = 10 * time.Minute

// signDevBundleURL returns a query-string suffix the owner can append
// to /dev/native-bundle, /dev/native-assets, or /dev/web-bundle/ so an
// unauthenticated phone or iframe can fetch the bundle.
//
// The signature covers (build|kind|exp). For /dev/web-bundle/ pass
// build=""; the URL is bundle-wide rather than per-build because the
// iframe fetches index.html + N sibling assets and signing each
// would mean N round-trips.
func signDevBundleURL(build, kind string, ttl time.Duration) (string, error) {
	if kind != "native" && kind != "assets" && kind != "web" {
		return "", fmt.Errorf("unknown bundle kind %q", kind)
	}
	if ttl <= 0 {
		ttl = defaultDevBundleTTL
	}
	secret, err := blobSecret()
	if err != nil {
		return "", err
	}
	exp := time.Now().Add(ttl).Unix()
	payload := fmt.Sprintf("%s|%s|%d", build, kind, exp)
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(payload))
	sig := hex.EncodeToString(mac.Sum(nil))
	q := fmt.Sprintf("exp=%d&sig=%s", exp, sig)
	if build != "" {
		q = fmt.Sprintf("build=%s&%s", build, q)
	}
	return q, nil
}

// verifyDevBundleSig checks the signature on a /dev/{native-bundle,
// native-assets,web-bundle/...} request. Returns nil on success, an
// error to render as 403 on failure.
//
// On loopback (127.0.0.1 / ::1) we skip the check — the owner's
// localhost iframe + the local SDK previewer load these endpoints via
// IPC which never crosses the public network. This is the same trust
// boundary used for /blobs/public.
//
// For web bundles, accepts either a ?sig=…&exp=… query string OR a
// per-bundle "yaver-dev-web-sig" cookie set by /dev/web-bundle/info.
// The cookie path is required for asset loads inside the iframe —
// browsers don't propagate query strings through <base href> rewrites
// of relative URLs.
//
// Authorization fallback: a valid Authorization: Bearer <token> from
// owner / paired / SDK / guest is also accepted. The Feedback SDK
// already sends Bearer headers when fetching the bundle, so a
// signed-URL miss isn't fatal as long as the request is auth'd.
func verifyDevBundleSig(build, kind string, r *http.Request) error {
	if r != nil && isLoopbackRequest(r) {
		return nil
	}
	if r != nil && hasValidYaverBearer(r) {
		return nil
	}
	q := r.URL.Query()
	sig := strings.TrimSpace(q.Get("sig"))
	expStr := strings.TrimSpace(q.Get("exp"))
	urlBuild := strings.TrimSpace(q.Get("build"))

	// Cookie fallback for web-bundle asset fetches.
	if kind == "web" && (sig == "" || expStr == "") {
		if c, err := r.Cookie("yaver-dev-web-sig"); err == nil && c != nil {
			parts := strings.SplitN(c.Value, ":", 2)
			if len(parts) == 2 {
				expStr = parts[0]
				sig = parts[1]
			}
		}
	}

	if sig == "" || expStr == "" {
		return errors.New("dev bundle URL must be signed; mint via /dev/build-native or /dev/web-bundle/info")
	}
	exp, err := strconv.ParseInt(expStr, 10, 64)
	if err != nil {
		return errors.New("invalid exp")
	}
	if time.Now().Unix() > exp {
		return errors.New("signed URL expired")
	}
	secret, err := blobSecret()
	if err != nil {
		return err
	}
	// For native bundles, the URL must carry the same build id the caller
	// asked us to serve. Web bundles use a build-less signature (the
	// build dir is whatever the most recent web export produced).
	if kind == "native" || kind == "assets" {
		if urlBuild != build {
			return errors.New("build mismatch")
		}
	}
	payload := fmt.Sprintf("%s|%s|%d", urlBuild, kind, exp)
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(payload))
	want := hex.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(want), []byte(sig)) {
		return errors.New("bad signature")
	}
	return nil
}

// setDevWebBundleCookie attaches a cookie carrying the same web-bundle
// signature so subsequent /dev/web-bundle/<asset> fetches inside the
// iframe authenticate without needing query strings (which <base href>
// drops on relative-URL resolution). Called from /dev/web-bundle/info
// + the index.html serve path.
func setDevWebBundleCookie(w http.ResponseWriter, sigQuery string) {
	parts := strings.Split(sigQuery, "&")
	var exp, sig string
	for _, p := range parts {
		switch {
		case strings.HasPrefix(p, "exp="):
			exp = strings.TrimPrefix(p, "exp=")
		case strings.HasPrefix(p, "sig="):
			sig = strings.TrimPrefix(p, "sig=")
		}
	}
	if exp == "" || sig == "" {
		return
	}
	// Path="/" rather than "/dev/web-bundle/" because requests routed
	// through the public relay arrive at the agent stripped of their
	// "/d/<deviceId>" prefix. The browser, however, sees the path
	// "/d/<deviceId>/dev/web-bundle/..." — a cookie scoped to
	// "/dev/web-bundle/" would not match because the actual request
	// path starts with "/d/...". HttpOnly + SameSite=Lax keeps the
	// blast radius small (passive read-only signature, browser-only
	// transport).
	http.SetCookie(w, &http.Cookie{
		Name:     "yaver-dev-web-sig",
		Value:    exp + ":" + sig,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(defaultDevBundleTTL.Seconds()),
	})
}

// signedWebBundleURL is the helper used by every place that emits a
// /dev/web-bundle/ URL into a JSON response. Sets the cookie on the
// outbound response writer so subsequent asset fetches authenticate
// AND returns the URL so the client (mobile, web dashboard) can
// embed it directly in iframe.src or fetch().
//
// Returns "/dev/web-bundle/" (unsigned) when the secret can't be
// derived — the serve handler will then 403, which is the safer
// fallback than silently rendering an unsigned URL that the iframe
// would then fail to load.
func signedWebBundleURL(w http.ResponseWriter) string {
	sig, err := signDevBundleURL("", "web", defaultDevBundleTTL)
	if err != nil || sig == "" {
		return "/dev/web-bundle/"
	}
	setDevWebBundleCookie(w, sig)
	return "/dev/web-bundle/?" + sig
}

// signedNativeBundleURLs returns (bundleURL, assetsURL) for the most
// recently built native bundle, both with HMAC sigs covering the
// current buildID. Used by reload_bundle commands that need to point
// the SDK at the existing bundle without forcing a fresh build.
//
// When no build is recorded (cold start), returns the unsigned paths
// — the SDK will get a 403 on fetch and the user will be prompted to
// rebuild via /dev/build-native, which is the right UX.
func signedNativeBundleURLs(s *HTTPServer) (string, string) {
	if s == nil || s.devServerMgr == nil {
		return "/dev/native-bundle", "/dev/native-assets"
	}
	info := s.devServerMgr.GetNativeBundleInfo("")
	if info.BuildID == "" {
		return "/dev/native-bundle", "/dev/native-assets"
	}
	bundleSig, _ := signDevBundleURL(info.BuildID, "native", 30*time.Minute)
	assetsSig, _ := signDevBundleURL(info.BuildID, "assets", 30*time.Minute)
	bundleURL := "/dev/native-bundle"
	if bundleSig != "" {
		bundleURL += "?" + bundleSig
	}
	assetsURL := "/dev/native-assets"
	if assetsSig != "" {
		assetsURL += "?" + assetsSig
	}
	return bundleURL, assetsURL
}

// hasValidYaverBearer reports whether the request carries an
// Authorization: Bearer header that the agent recognizes as a
// known token (owner, paired, support, or — pragmatically —
// "starts with the yv_supp_ prefix and validates"). Used as an
// alternative authorisation path for /dev/native-bundle so the
// Feedback SDK + Yaver mobile app can keep their existing auth'd
// fetch flow without needing the signed-URL dance.
//
// We deliberately do NOT validate against Convex here (that would
// re-block when the agent is offline). The check matches the same
// fast paths used by httpserver.go::auth(): exact owner-token
// equality (constant-time), paired-token presence, and support
// bearer presence. SDK tokens use authSDK and arrive with the
// same Bearer prefix; they are accepted at the lighter "the agent
// has SEEN this token before" level by the cache. For an
// unauthenticated public scanner with no token at all, all three
// paths fail and we fall through to the signed-URL check.
func hasValidYaverBearer(r *http.Request) bool {
	auth := strings.TrimSpace(r.Header.Get("Authorization"))
	if !strings.HasPrefix(auth, "Bearer ") {
		return false
	}
	tok := strings.TrimSpace(strings.TrimPrefix(auth, "Bearer "))
	if tok == "" {
		return false
	}
	cfg, _ := LoadConfig()
	if cfg != nil && cfg.AuthToken != "" && secretEqual(tok, cfg.AuthToken) {
		return true
	}
	if IsPairedToken(tok) {
		return true
	}
	if strings.HasPrefix(tok, "yv_supp_") && supportTokenValidFor(tok, "/info") {
		// Support sessions are scope-limited; here we only confirm the
		// token is currently active. The serve handler's path-level
		// scope check is what actually decides; we just want to admit
		// "the request has a real bearer" so the signed URL is not
		// strictly required.
		return true
	}
	// SDK tokens — the Feedback SDK ships its bearer on every fetch.
	// We accept any token shape that's valid against the agent's
	// existing in-process cache (populated by authSDK on prior calls).
	// Population happens via the Convex-side validate path; if the
	// cache misses we fall back to signed-URL only, which is the safe
	// default for unknown tokens.
	if devBundleBearerCacheHit(tok) {
		return true
	}
	return false
}

// devBundleBearerCacheHit consults the running HTTPServer's token
// cache (populated by authSDK / auth) for the given bearer. We use a
// package-level pointer to avoid threading a *HTTPServer through
// every call site; the pointer is set on agent startup.
func devBundleBearerCacheHit(tok string) bool {
	srv := devBundleServerRef
	if srv == nil {
		return false
	}
	if v, ok := srv.tokenCache.Load(tok); ok && v != nil {
		return true
	}
	return false
}

// devBundleServerRef is set by HTTPServer.Start so the unauth dev
// bundle handlers (which don't get a *HTTPServer threaded through)
// can consult the same token cache the auth() middleware uses.
var devBundleServerRef *HTTPServer

// SetDevBundleServerRef wires the running HTTPServer pointer into the
// dev-bundle bearer fallback. Called once during HTTPServer.Start.
func SetDevBundleServerRef(s *HTTPServer) { devBundleServerRef = s }

// isLoopbackRequest reports whether the request originated on the
// local machine. The dashboard's owner-side iframe + the dev preview
// hit these endpoints from 127.0.0.1, which is already trust-bounded
// to the agent's owner via OS-level isolation.
func isLoopbackRequest(r *http.Request) bool {
	if r == nil {
		return false
	}
	host := r.Host
	if i := strings.IndexByte(host, ':'); i >= 0 {
		host = host[:i]
	}
	if host == "" {
		host = clientIP(r)
	}
	if host == "127.0.0.1" || host == "::1" || host == "localhost" {
		return true
	}
	if rip := clientIP(r); rip == "127.0.0.1" || rip == "::1" {
		return true
	}
	return false
}
