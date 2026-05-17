package main

import (
	"testing"
)

// TestGuestScopeAllowList is the lock-in for the security contract:
// a feedback-only guest CANNOT reach any of the surfaces that would let
// them execute arbitrary AI prompts, proxy dev servers, enumerate
// projects, or trigger builds. A full-scope guest CAN reach the
// historical allow-list. Both scopes are blocked from the owner-only
// surface (vault, exec, sessions, tmux, …).
func TestGuestScopeAllowList(t *testing.T) {
	// Paths that are specifically valuable to block for end-users invited
	// via Feedback SDK — these are the arbitrary-code-execution and
	// data-exfil vectors the scope is designed to close.
	mustBlockFeedbackOnly := []string{
		"/tasks",
		"/tasks/",
		"/tasks/abc-123",
		"/vibing",
		"/vibing/execute",
		"/dev/",
		"/dev/start",
		"/dev/native-bundle",
		"/builds",
		"/projects",
		"/projects/refresh",
		"/todolist",
		"/agent/status",
		"/agent/runners",
		"/shared-storage/",
		"/shared-storage/read",
	}
	for _, path := range mustBlockFeedbackOnly {
		if isGuestAllowedPathForScope(path, GuestScopeFeedbackOnly) {
			t.Errorf("feedback-only guest should NOT reach %q — that surface allows code exec / enumeration", path)
		}
		if !isGuestAllowedPathForScope(path, GuestScopeFull) {
			t.Errorf("full-scope guest SHOULD reach %q (backward-compat)", path)
		}
	}

	// /repos/* used to be on the full-scope allow-list but the security
	// audit (H-5) reclassified it as owner-only — clone/credentials/delete
	// are too dangerous to hand to a teammate-tier guest. Both scopes
	// must now reject the prefix.
	// /repos/clone, /repos/credentials, /repos/delete are owner-only.
	// /repos/list (read-only enumeration) is allowed for full-scope
	// guests so the mobile UI can show "what repos are on the dev
	// machine"; feedback-only guests still cannot reach it.
	mustBlockBothRepos := []string{
		"/repos",
		"/repos/clone",
		"/repos/credentials",
		"/repos/delete",
		"/repos/pull",
	}
	for _, path := range mustBlockBothRepos {
		for _, scope := range []string{GuestScopeFeedbackOnly, GuestScopeFull} {
			if isGuestAllowedPathForScope(path, scope) {
				t.Errorf("%s scope must NOT reach %q — owner-only per H-5", scope, path)
			}
		}
	}
	if !isGuestAllowedPathForScope("/repos/list", GuestScopeFull) {
		t.Error("/repos/list must remain reachable for full-scope guests (mobile UI)")
	}
	if isGuestAllowedPathForScope("/repos/list", GuestScopeFeedbackOnly) {
		t.Error("/repos/list must NOT be reachable for feedback-only guests")
	}

	// /agent/runners is read-only loadout enumeration. Exact-match-only
	// in the allow-list (see guestExactOnlyEntries): the only sibling
	// route registered today is /agent/runners/test which spawns AI
	// runners (and thus burns the host's API budget) — must be blocked
	// for guests.
	if !isGuestAllowedPathForScope("/agent/runners", GuestScopeFull) {
		t.Error("/agent/runners must be reachable by full-scope guests")
	}
	if isGuestAllowedPathForScope("/agent/runners/test", GuestScopeFull) {
		t.Error("/agent/runners/test must be BLOCKED (it spawns runners → API budget burn)")
	}
	if isGuestAllowedPathForScope("/agent/runners-debug", GuestScopeFull) {
		t.Error("/agent/runners-debug must be BLOCKED (prefix-collision class)")
	}

	// Paths both scopes must allow — the minimum Feedback SDK surface.
	mustAllowBoth := []string{
		"/feedback",
		"/feedback/abc",
		"/feedback/abc/fix",
		"/blackbox/events",
		"/blackbox/subscribe",
		"/voice/status",
		"/voice/transcribe",
		"/health",
		"/info",
	}
	for _, path := range mustAllowBoth {
		if !isGuestAllowedPathForScope(path, GuestScopeFeedbackOnly) {
			t.Errorf("feedback-only guest MUST reach %q", path)
		}
		if !isGuestAllowedPathForScope(path, GuestScopeFull) {
			t.Errorf("full-scope guest MUST reach %q", path)
		}
	}

	// Owner-only surface — neither scope may reach these (defense in depth;
	// the canonical check lives in TestGuestAllowlistHasNoOwnerOnlyPrefixes
	// but this is the positive end-to-end spot-check).
	mustBlockAll := []string{
		"/exec",
		"/exec/run",
		"/vault",
		"/vault/read",
		"/session/export",
		"/tmux/new",
		"/agent/shutdown",
		"/apikeys",
		"/sdk/token",
	}
	for _, path := range mustBlockAll {
		for _, scope := range []string{GuestScopeFeedbackOnly, GuestScopeFull} {
			if isGuestAllowedPathForScope(path, scope) {
				t.Errorf("scope %q must not reach owner-only path %q", scope, path)
			}
		}
	}
}

// TestGuestScopeDefaults covers the "what if Convex returns nothing / weird"
// case. Legacy rows pre-scope come back with an empty Scope field and must
// be treated as the old "full" behavior so bumping the agent in place
// doesn't silently downgrade teammates. Unknown values also collapse to
// "full" — safer to widen than to block an unknown tier outright.
func TestGuestScopeDefaults(t *testing.T) {
	cases := map[string]string{
		"":              GuestScopeFull,
		"full":          GuestScopeFull,
		"feedback-only": GuestScopeFeedbackOnly,
		"FEEDBACK-ONLY": GuestScopeFull, // case-sensitive: unknown → default
		"readonly":      GuestScopeFull, // unknown → default
		"  full  ":      GuestScopeFull,
	}
	for in, want := range cases {
		if got := guestScopeOrDefault(in); got != want {
			t.Errorf("guestScopeOrDefault(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestGuestConfigManagerScope exercises the cache-layer lookup path that
// the auth middleware uses to decide which allow-list to apply. Feeds
// in a fake sync from Convex and asserts the two scopes round-trip
// correctly, including the "legacy row with no scope" fallback.
func TestGuestConfigManagerScope(t *testing.T) {
	mgr := NewGuestConfigManager(t.TempDir())
	mgr.UpdateConfigs([]GuestConfig{
		{GuestUserID: "user_feedback", Scope: GuestScopeFeedbackOnly, GuestEmail: "a@example.com"},
		{GuestUserID: "user_full", Scope: GuestScopeFull, GuestEmail: "b@example.com"},
		{GuestUserID: "user_legacy", Scope: "", GuestEmail: "c@example.com"},
	})

	cases := []struct {
		uid            string
		wantScope      string
		wantFeedbackFn bool
	}{
		{"user_feedback", GuestScopeFeedbackOnly, true},
		{"user_full", GuestScopeFull, false},
		{"user_legacy", GuestScopeFull, false}, // pre-scope rows → full
		{"user_unknown", GuestScopeFull, false}, // never-seen guests → full (auth middleware also gates on isApprovedGuest)
	}
	for _, tc := range cases {
		if got := mgr.GetScope(tc.uid); got != tc.wantScope {
			t.Errorf("GetScope(%q) = %q, want %q", tc.uid, got, tc.wantScope)
		}
		if got := mgr.IsFeedbackOnly(tc.uid); got != tc.wantFeedbackFn {
			t.Errorf("IsFeedbackOnly(%q) = %v, want %v", tc.uid, got, tc.wantFeedbackFn)
		}
	}

	// Nil-manager safety — used in call sites where guestConfigMgr may not be
	// wired up yet (e.g. very first seconds after `yaver serve` boot).
	var nilMgr *GuestConfigManager
	if got := nilMgr.GetScope("anything"); got != GuestScopeFull {
		t.Errorf("nil manager GetScope must default to %q, got %q", GuestScopeFull, got)
	}
	if nilMgr.IsFeedbackOnly("anything") {
		t.Errorf("nil manager IsFeedbackOnly must be false")
	}
}

// TestGuestProjectScoping locks in the per-guest project allow-list. The host
// can invite someone with --projects=SFMG, and the agent must:
//   - accept feedback-fix / task creation when the target project matches;
//   - reject when the target is a different project (even one the host also owns);
//   - reject when the request has no project identity (a "which project?" gap is
//     treated as denial — prevents an unrestricted fix sneaking through);
//   - treat an empty allow-list as "all projects" (backward-compat for existing grants).
func TestGuestProjectScoping(t *testing.T) {
	mgr := NewGuestConfigManager(t.TempDir())
	mgr.UpdateConfigs([]GuestConfig{
		{GuestUserID: "narrow", Scope: GuestScopeFeedbackOnly, AllowedProjects: []string{"SFMG", "Talos"}},
		{GuestUserID: "wide", Scope: GuestScopeFull, AllowedProjects: nil},
		{GuestUserID: "empty", Scope: GuestScopeFeedbackOnly, AllowedProjects: []string{}},
	})

	cases := []struct {
		uid     string
		project string
		want    bool
	}{
		// Narrow guest — exact match case-insensitive.
		{"narrow", "SFMG", true},
		{"narrow", "sfmg", true},
		{"narrow", "Talos", true},
		{"narrow", "yaver", false},
		{"narrow", "", false},      // no project identity → reject
		{"narrow", "unknown", false},

		// Wide guest — no project narrowing, everything allowed.
		{"wide", "SFMG", true},
		{"wide", "anything", true},
		{"wide", "", true},

		// Empty list — same as wide (all projects).
		{"empty", "SFMG", true},
		{"empty", "whatever", true},
	}
	for _, tc := range cases {
		if got := mgr.GuestCanAccessProject(tc.uid, tc.project); got != tc.want {
			t.Errorf("GuestCanAccessProject(%q, %q) = %v, want %v", tc.uid, tc.project, got, tc.want)
		}
	}

	// cleanProjectList trims + dedupes + drops empties.
	out := cleanProjectList([]string{"  SFMG ", "sfmg", "", "Talos", "SFMG", "  "})
	if len(out) != 2 {
		t.Fatalf("expected 2 unique entries after clean, got %v", out)
	}
	if out[0] != "SFMG" || out[1] != "Talos" {
		t.Errorf("cleanProjectList dropped the wrong entries: %v", out)
	}
}
