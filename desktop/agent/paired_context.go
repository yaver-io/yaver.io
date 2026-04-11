package main

// paired_context.go — helpers for propagating the resolved
// userId of a paired-token request through the request
// context. Used by the MultiUserManager-aware handlers so a
// paired user's tasks / workspaces / sessions land in their
// own isolated slot instead of sharing the primary owner's.

import "context"

type pairedUserKey struct{}

// contextWithPairedUser attaches a userId to the request
// context. Returns the original context when userID is empty.
func contextWithPairedUser(ctx context.Context, userID string) context.Context {
	if userID == "" {
		return ctx
	}
	return context.WithValue(ctx, pairedUserKey{}, userID)
}

// PairedUserFromContext pulls out the userId set by
// contextWithPairedUser. Returns "" if no paired user is
// attached (which is the common case — the request is from
// the primary owner).
func PairedUserFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	v := ctx.Value(pairedUserKey{})
	if v == nil {
		return ""
	}
	s, _ := v.(string)
	return s
}
