package main

// allowlist_test.go — tripwire for the authorization boundary.
//
// Guest grants and support sessions expose a narrow subset of the
// agent's HTTP surface. Certain prefixes MUST never appear in either
// allowlist; adding them there would let a guest or a redeemed support
// bearer read secrets that are owner-only by policy.
//
// If a future edit slips one of these prefixes into either list, this
// test fails with the offending path + list name so the reviewer can
// decide whether that's really what they meant.

import (
	"strings"
	"testing"
)

// Paths that must remain owner-only. If you're tempted to add one
// here to the support allowlist, first think: would I be okay handing
// this to a stranger who typed a 6-char code into a web form?
var forbiddenOnNonOwnerSurfaces = []string{
	"/vault",           // secrets at rest
	"/apikeys",         // SDK token registry
	"/blobs",           // user object storage
	"/agent/shutdown",  // "oops, guest turned off my machine"
	"/session/",        // AI-agent session transfer
	"/autodev/",        // long-running autonomous dev loops
	"/sdk/token",       // creates new bearer tokens
	"/auth/",           // pairing, recovery
}

func TestGuestAllowlistHasNoOwnerOnlyPrefixes(t *testing.T) {
	for _, forbidden := range forbiddenOnNonOwnerSurfaces {
		for _, allowed := range guestAllowedPrefixes {
			if allowed == forbidden || strings.HasPrefix(allowed, forbidden) {
				t.Errorf("guestAllowedPrefixes contains owner-only prefix %q (matched entry %q)", forbidden, allowed)
			}
		}
	}
}

func TestSupportAllowlistHasNoOwnerOnlyPrefixes(t *testing.T) {
	for _, forbidden := range forbiddenOnNonOwnerSurfaces {
		for _, allowed := range supportAllowedPrefixes {
			if allowed == forbidden || strings.HasPrefix(allowed, forbidden) {
				t.Errorf("supportAllowedPrefixes contains owner-only prefix %q (matched entry %q)", forbidden, allowed)
			}
		}
	}
}

// TestSupportTokenBlocksForbiddenPrefixes is the end-to-end check: an
// active support session's bearer must NOT pass isGuestAllowedPath-
// equivalent logic on any forbidden prefix, via the real
// supportTokenValidFor function.
func TestSupportTokenBlocksForbiddenPrefixes(t *testing.T) {
	resetSupport(t)
	sess := StartSupportSession("test", 0) // default TTL
	defer StopSupportSession()

	for _, forbidden := range forbiddenOnNonOwnerSurfaces {
		// Normalise so we test both "/vault" and "/vault/read" style.
		for _, path := range []string{forbidden, strings.TrimSuffix(forbidden, "/") + "/inner"} {
			if supportTokenValidFor(sess.Token, path) {
				t.Errorf("support token should not grant %q (forbidden family %q)", path, forbidden)
			}
		}
	}
}
