package main

// support.go — in-memory "remote support" session.
//
// Think: TeamViewer, but inside the Yaver agent. The host opens a
// short-lived window with `yaver support start`; the returned
// 6-char code + bearer token + shareable URL let a trusted second
// party reach a narrow subset of this agent's HTTP API — terminal,
// exec, file browse, browser sessions, system status — until the
// window closes. Nothing is persisted. Nothing leaves the host.
//
// Deliberately distinct from the Convex-backed "guest" grants in
// guests.ts: those are long-lived, email-tied, team-shaped. A
// support session is the opposite — owner-initiated, lives only in
// the host's agent memory, dies on `stop` / TTL / process restart.

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"os"
	"strings"
	"sync"
	"time"
)

// supportSession is the active, in-memory grant. A host has at most
// one at a time; calling start again replaces the previous one.
type supportSession struct {
	Code            string    // human-typeable 6-char invite code
	Token           string    // long random bearer token (clients use this)
	Label           string    // optional tag e.g. "cousin"
	Hostname        string    // host machine name, copied at create time
	AllowedPrefixes []string  // URL prefixes this session may access
	Shell           bool      // host opted into RCE-class endpoints (/exec, /ws/terminal, /browser/*)
	CreatedAt       time.Time
	ExpiresAt       time.Time
}

var (
	supportMu     sync.RWMutex
	activeSupport *supportSession

	// Same safe alphabet as auth_pair.go — no 0/O/1/I.
	supportCodeAlphabet = []byte("ABCDEFGHJKLMNPQRSTUVWXYZ23456789")
)

// supportAllowedPrefixes is the default scope granted to a support
// session. Read-only / status surface only. Every entry matches
// segment-aware: a path matches when it equals the entry or starts
// with entry + "/".
//
// /exec, /ws/terminal, and /browser/* are NOT in this default list —
// those are RCE / SSRF surfaces. A host who actually wants a remote
// shell handed out via 6-char code must opt in explicitly with
// `yaver support start --shell`, which appends supportShellPrefixes.
// The opt-in lives on the session struct (Shell bool) and is rebuilt
// fresh every time auth() runs, so revoking the session immediately
// also revokes shell access.
var supportAllowedPrefixes = []string{
	"/support/",
	"/health",
	"/info",
	"/agent/status",
	"/agent/capabilities",
	"/agent/runners",
	"/files/roots",
	"/files/list",
	"/files/read",
	"/files/raw",
	"/streams",
	"/streams/",
}

// supportShellPrefixes are the RCE-class endpoints added on top of the
// default allowlist when a session is opened with --shell. Combined
// with rate-limiting + constant-time code compare, the brute-force
// path on /support/redeem is closed; the worst case is "the host
// deliberately handed someone a shell" instead of "anyone scanning
// the public internet got one."
var supportShellPrefixes = []string{
	"/exec",
	"/exec/",
	"/ws/terminal",
	"/browser/",
}

const defaultSupportTTL = 30 * time.Minute

func generateSupportCode() string {
	buf := make([]byte, 6)
	_, _ = rand.Read(buf)
	for i, b := range buf {
		buf[i] = supportCodeAlphabet[int(b)%len(supportCodeAlphabet)]
	}
	return string(buf)
}

func generateSupportToken() string {
	buf := make([]byte, 24)
	_, _ = rand.Read(buf)
	return "yv_supp_" + base64.RawURLEncoding.EncodeToString(buf)
}

// SupportStartOptions configures the new session. Defaults are
// strict — Shell is false, scope is read-only.
type SupportStartOptions struct {
	Label string
	TTL   time.Duration
	Shell bool // grants /exec, /ws/terminal, /browser/* on top of the default scope
}

// StartSupportSession creates (or replaces) the active session. A
// ttl <= 0 falls back to defaultSupportTTL. Strict-by-default: pass
// opts.Shell=true to grant the RCE-class scope (the host must opt in
// at session-creation time; the redeeming party can never escalate).
func StartSupportSession(opts SupportStartOptions) *supportSession {
	ttl := opts.TTL
	if ttl <= 0 {
		ttl = defaultSupportTTL
	}
	hostname, _ := os.Hostname()
	prefixes := append([]string(nil), supportAllowedPrefixes...)
	if opts.Shell {
		prefixes = append(prefixes, supportShellPrefixes...)
	}
	sess := &supportSession{
		Code:            generateSupportCode(),
		Token:           generateSupportToken(),
		Label:           strings.TrimSpace(opts.Label),
		Hostname:        hostname,
		AllowedPrefixes: prefixes,
		Shell:           opts.Shell,
		CreatedAt:       time.Now(),
		ExpiresAt:       time.Now().Add(ttl),
	}
	supportMu.Lock()
	activeSupport = sess
	supportMu.Unlock()
	return sess
}

// StopSupportSession revokes the active session. Returns true if
// there was one to stop.
func StopSupportSession() bool {
	supportMu.Lock()
	defer supportMu.Unlock()
	if activeSupport == nil {
		return false
	}
	activeSupport = nil
	return true
}

// activeSupportSnapshot returns the live session or nil if none is
// active / it has expired. Expired sessions are cleared on read so
// the next start doesn't stack on a ghost.
func activeSupportSnapshot() *supportSession {
	supportMu.RLock()
	sess := activeSupport
	supportMu.RUnlock()
	if sess == nil {
		return nil
	}
	if time.Now().After(sess.ExpiresAt) {
		supportMu.Lock()
		if activeSupport != nil && time.Now().After(activeSupport.ExpiresAt) {
			activeSupport = nil
		}
		supportMu.Unlock()
		return nil
	}
	return sess
}

// supportTokenValidFor reports whether token is the active support
// session's bearer and path is inside its allowlist. Called from the
// auth() middleware as a third fast path before the Convex lookup.
//
// Compares the bearer with subtle.ConstantTimeCompare so a network
// attacker can't time-leak the token byte-by-byte. Path match is
// segment-aware: an entry "/X" matches "/X" exactly and "/X/..." but
// NOT "/Xevil" — closes the prefix-collision class (e.g. /agent/runners
// vs /agent/runners/test).
func supportTokenValidFor(token, path string) bool {
	sess := activeSupportSnapshot()
	if sess == nil || token == "" {
		return false
	}
	if subtle.ConstantTimeCompare([]byte(token), []byte(sess.Token)) != 1 {
		return false
	}
	return pathMatchesSupportAllowlist(path, sess.AllowedPrefixes)
}

// pathMatchesSupportAllowlist matches segment-aware: an entry matches
// when path equals it OR path is a sub-path (entry + "/..."). Trailing
// slashes in entries are tolerated by stripping before compare.
func pathMatchesSupportAllowlist(path string, allowed []string) bool {
	if path == "" {
		path = "/"
	}
	for _, raw := range allowed {
		entry := strings.TrimSuffix(raw, "/")
		if entry == "" {
			continue
		}
		if path == entry || strings.HasPrefix(path, entry+"/") {
			return true
		}
	}
	return false
}

// supportSessionRedeem looks up the active session by human code.
// Used by the unauthenticated /support/redeem endpoint — a guest's
// browser/agent POSTs the 6-char code and receives the bearer token.
// Case-insensitive to survive mobile autocapitalization, then compared
// in constant time to neutralize the timing-oracle brute-force surface.
func supportSessionRedeem(code string) *supportSession {
	sess := activeSupportSnapshot()
	if sess == nil {
		return nil
	}
	got := strings.ToUpper(strings.TrimSpace(code))
	want := strings.ToUpper(sess.Code)
	if len(got) != len(want) {
		return nil
	}
	if subtle.ConstantTimeCompare([]byte(got), []byte(want)) != 1 {
		return nil
	}
	return sess
}
