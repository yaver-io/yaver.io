package main

// secret_compare.go — single home for constant-time secret comparison.
//
// The audit found `==` / `!=` / `EqualFold` used to compare every kind
// of bearer / passkey / pair code / webhook secret in this codebase.
// `==` on a Go string is short-circuiting byte-wise comparison: a
// network attacker can time-leak the secret one byte at a time. Most
// of these surfaces are remote-reachable (some unauthenticated, some
// behind a low-entropy code) so timing oracles are a real concern.
//
// Use secretEqual for any string-vs-string comparison where both sides
// are intended to remain confidential. The variadic upperFold variant
// covers the case-insensitive alphabet codes (pair / support) without
// reintroducing strings.EqualFold's per-byte branching.

import (
	"crypto/subtle"
	"strings"
)

// secretEqual reports whether got matches want in constant time.
// Both arguments are compared as raw bytes; differing lengths short-
// circuit to false but still walk both inputs (subtle.ConstantTimeCompare
// returns 0 immediately for differing lengths, which is acceptable —
// length itself is rarely the secret).
//
// Returns false when either side is empty: an empty stored secret is
// never a valid match (defends against "config not loaded yet" bugs).
func secretEqual(got, want string) bool {
	if got == "" || want == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}

// secretEqualFold is the case-insensitive variant for human-typeable
// codes (the 6-char support / pair alphabets). Both sides are upper-
// cased before compare so the timing footprint is the same regardless
// of whether the input was already uppercase.
func secretEqualFold(got, want string) bool {
	got = strings.ToUpper(strings.TrimSpace(got))
	want = strings.ToUpper(strings.TrimSpace(want))
	return secretEqual(got, want)
}
