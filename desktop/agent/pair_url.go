package main

// pair_url.go — canonical Yaver pairing URL.
//
// The QR / pair-URL layer is purely additive on top of the existing
// passkey + /auth/pair/submit flow. The 6-character code remains the
// real submit secret; this URL is just a locator that gets a phone to
// the right pair session faster.
//
// Format:
//
//   https://yaver.io/pair
//     ?sid=<session id>          # locator; for now == code
//     &mode=pair|bootstrap|recovery
//     &host=<display hostname>
//     &exp=<unix seconds>
//     &target=<preferred reach URL>   # optional
//     &code=<short passkey>           # optional fallback
//
// This URL is consumable by:
//   - the Yaver mobile app (deep-link / paste / future QR scan)
//   - a browser (Slice B will add yaver.io/pair web route)
//   - any CLI that wants to forward the same locator without re-rolling
//
// Constraints:
//   - the URL is NEVER the trust anchor. The code stays the secret.
//   - the URL is OPTIONAL. Every existing flow (manual passkey,
//     `yaver auth send`, bootstrap LAN beacon, recovery) must keep
//     working when the URL/QR is ignored entirely.

import (
	"net/url"
	"strconv"
	"strings"
)

// defaultPairBaseURL is the canonical hosted pair landing page. Falls
// back to this when no Config.WebBaseURL has been configured. Self-
// hosters can point at their own web build via `yaver config set
// web-base-url`.
const defaultPairBaseURL = "https://yaver.io/pair"

// PairURLOptions feeds buildPairURL. All fields except Session are
// optional; the URL is still meaningful with just a session.
type PairURLOptions struct {
	Mode      string // "pair" | "bootstrap" | "recovery"
	Target    string // first-choice reach URL for the agent
	BaseURL   string // override canonical base (rarely used)
	OmitCode  bool   // suppress ?code=... — useful when the URL is shared in writing
}

// pairBaseURLFromConfig returns the configured web base URL with /pair
// appended, or the canonical default. Treats an empty / whitespace-only
// override as "use default" so a stray `web_base_url=""` in config
// doesn't degrade the link.
func pairBaseURLFromConfig() string {
	cfg, err := LoadConfig()
	if err != nil || cfg == nil {
		return defaultPairBaseURL
	}
	base := strings.TrimSpace(cfg.WebBaseURL)
	if base == "" {
		return defaultPairBaseURL
	}
	return strings.TrimRight(base, "/") + "/pair"
}

// buildPairURL assembles the canonical pair URL from the active
// session. Returns "" if session is nil — callers should fall back to
// the existing passkey-only output in that case.
func buildPairURL(session *pairingSession, opts PairURLOptions) string {
	if session == nil || session.Code == "" {
		return ""
	}
	base := strings.TrimSpace(opts.BaseURL)
	if base == "" {
		base = pairBaseURLFromConfig()
	}
	q := url.Values{}
	// sid is a locator. For Slice A we have one global active pairing
	// session, so sid==code. When we later move to multi-session, sid
	// becomes a separate identifier and code stays the secret.
	q.Set("sid", session.Code)
	mode := strings.TrimSpace(opts.Mode)
	if mode == "" {
		mode = "pair"
	}
	q.Set("mode", mode)
	if session.Hostname != "" {
		q.Set("host", session.Hostname)
	}
	if !session.ExpiresAt.IsZero() {
		q.Set("exp", strconv.FormatInt(session.ExpiresAt.Unix(), 10))
	}
	if t := strings.TrimSpace(opts.Target); t != "" {
		q.Set("target", t)
	}
	if !opts.OmitCode {
		// Including the code makes the URL one-tap pairable for the
		// "I already trust this LAN" case and keeps the link self-
		// contained for headless boxes nobody can reach to type the
		// code separately. Suppressed only when the URL is going on
		// a wiki, in chat, or anywhere it could be re-read out of
		// context.
		q.Set("code", session.Code)
	}
	return base + "?" + q.Encode()
}

// preferredPairTarget picks the most user-friendly reach URL out of
// the candidate list — LAN IP if present, then Tailscale, then
// loopback last. This is the one we put in the URL and recommend in
// the console.
func preferredPairTarget(urls []string) string {
	if len(urls) == 0 {
		return ""
	}
	// Skip loopback; prefer non-tailscale LAN IPs first since those
	// usually work without any extra setup on the source machine.
	for _, u := range urls {
		if strings.Contains(u, "127.0.0.1") || strings.Contains(u, "localhost") {
			continue
		}
		if !strings.Contains(u, "100.") { // Tailscale CGNAT range hint
			return u
		}
	}
	for _, u := range urls {
		if strings.Contains(u, "127.0.0.1") || strings.Contains(u, "localhost") {
			continue
		}
		return u
	}
	return urls[0]
}
