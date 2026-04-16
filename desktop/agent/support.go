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
// session. Every entry is a URL-prefix match evaluated by auth().
// Kept narrow: no /vault, no /agent/shutdown, no /session/*, no
// /autodev/*, no /tasks (those belong to the owner).
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
	"/exec",
	"/exec/",
	"/ws/terminal",
	"/browser/",
	"/streams",
	"/streams/",
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

// StartSupportSession creates (or replaces) the active session. A
// ttl <= 0 falls back to defaultSupportTTL.
func StartSupportSession(label string, ttl time.Duration) *supportSession {
	if ttl <= 0 {
		ttl = defaultSupportTTL
	}
	hostname, _ := os.Hostname()
	sess := &supportSession{
		Code:            generateSupportCode(),
		Token:           generateSupportToken(),
		Label:           strings.TrimSpace(label),
		Hostname:        hostname,
		AllowedPrefixes: append([]string(nil), supportAllowedPrefixes...),
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
func supportTokenValidFor(token, path string) bool {
	sess := activeSupportSnapshot()
	if sess == nil || token == "" || token != sess.Token {
		return false
	}
	for _, prefix := range sess.AllowedPrefixes {
		if strings.HasPrefix(path, prefix) {
			return true
		}
	}
	return false
}

// supportSessionRedeem looks up the active session by human code.
// Used by the unauthenticated /support/redeem endpoint — a guest's
// browser/agent POSTs the 6-char code and receives the bearer token.
// Case-insensitive to survive mobile autocapitalization.
func supportSessionRedeem(code string) *supportSession {
	sess := activeSupportSnapshot()
	if sess == nil {
		return nil
	}
	if strings.EqualFold(strings.TrimSpace(code), sess.Code) {
		return sess
	}
	return nil
}
