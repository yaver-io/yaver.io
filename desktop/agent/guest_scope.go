package main

import (
	"strings"
)

// Guest scope model.
//
// Every guest grant lives in Convex's `guestAccess.scope` field and comes
// back on the config sync as `GuestConfig.Scope`. The agent enforces
// per-scope allow-lists at the auth-middleware layer so a malicious end-user
// of a third-party app (who signed in to the Feedback SDK and got invited as
// a guest) physically cannot reach the dev-machine surface that would let
// them exfiltrate code or execute arbitrary AI-agent prompts.
//
// Two tiers today:
//
//   - GuestScopeFull — classic teammate: tasks, vibing, dev server proxy,
//     builds, project enumeration, plus the safe feedback/voice/blackbox
//     surface. Equivalent to the pre-scope behavior. Opt in with
//     `yaver guests invite --scope=full`.
//
//   - GuestScopeFeedbackOnly (default for new invites) — untrusted end-user:
//     feedback upload, blackbox telemetry, voice transcription, plus the
//     minimal health/info/guests surface needed for discovery. No way to
//     read code, enumerate projects, trigger AI tasks, or proxy dev servers.
//     Additionally: /info is redacted to strip project metadata, /projects
//     is 403, and any task spawned by this guest's feedback is force-
//     containerized regardless of the agent's `containerize_guests` setting.
//
// Legacy rows (pre-scope) come back from Convex without a scope field. The
// runtime treats them as "full" so bumping the agent in place doesn't
// silently downgrade an existing teammate. New invitations default to
// "feedback-only" on the Convex side.

const (
	GuestScopeFull         = "full"
	GuestScopeFeedbackOnly = "feedback-only"
	GuestScopeSDKProject   = "sdk-project"
	// GuestScopeDeploy — narrow tier for shared-machine deploys: the
	// guest can trigger yaver-managed deploy scripts for projects in
	// their allowedProjects list, but cannot read code, run tasks, or
	// touch the vault directly. The script body + vault values stay on
	// the host; the guest sees only stdout/stderr + exit code.
	GuestScopeDeploy = "deploy"
	// GuestScopeSupport — least-privilege tier for support links
	// (docs/mesh-support-link.md). A supporter the friend let in at the
	// default "view + files" consent level can read status + browse/read
	// files + watch streams, but CANNOT run tasks, exec, deploy, or touch
	// the vault. If the friend opts UP to terminal at consent time, the
	// grant is recorded as scope "full" instead (AI tasks); desktop control
	// is a separate allowDesktopControl flag. So this tier is read-only.
	GuestScopeSupport = "support"
	// GuestScopeCircuit — capability credential for the hosted circuit
	// simulator service. An external product (Talos, OCPP) gets a token at
	// this scope so it can drive ONE resource — the circuit cell — over /ops,
	// and reach NOTHING else: no code, no tasks, no exec, no vault, no other
	// verb family. The path allow-list below only opens /ops (+ info/health);
	// the verb-level gate in ops.go (capabilityScopeVerbPrefix) then narrows
	// /ops to circuit_* verbs only. This is how Yaver exposes a resource as an
	// isolated service to other apps without lending them the owner's box.
	GuestScopeCircuit       = "circuit"
	guestScopeDefaultLegacy = GuestScopeFull
)

// guestCircuitAllowedPrefixes is the entire HTTP surface a circuit-scoped
// service credential can touch. Just the ops endpoint (verb-gated to circuit_*
// downstream) plus the discovery probes. No /tasks, /dev, /files, /deploy,
// /vault, /repos — none of it.
var guestCircuitAllowedPrefixes = []string{
	"/ops",
	"/ops/plan",
	"/info",
	"/health",
}

// guestFullAllowedPrefixes is the classic teammate access surface.
// Kept in sync with the documented table in CLAUDE.md. Host-only endpoints
// (exec, vault, sessions, tmux, git, shutdown, …) are NOT listed here —
// they fall through to the owner-only path.
// H-4 / H-5 (security_audit.md):
//   - Removed "/repos/" from the full-tier allow-list. /repos/* exposes
//     git clone, credential reads/writes, and delete on the host's repo
//     ledger; CLAUDE.md's documented guest table already says owner-only.
//   - "/agent/runners" stays as an entry, but the matcher below is now
//     segment-aware so /agent/runners (exact, the read-only loadout
//     list) is allowed while /agent/runners/test (spawns AI runners,
//     burns the host's API budget) is NOT. Adding the entry without the
//     segment-aware match was the prefix-collision bug.
var guestFullAllowedPrefixes = []string{
	"/tasks",
	"/feedback",
	"/dev/",
	"/blackbox/",
	"/voice/",
	"/info",
	"/agent/status",
	"/agent/runners",
	"/projects",
	// /repos/list (read-only enumeration: name, path, branch, remote,
	// lastCommit, dirty) is the ONLY repo endpoint full-scope guests
	// reach — they cannot clone, delete, or read credentials. Listed
	// in guestExactOnlyEntries below so /repos/clone & friends do not
	// silently inherit access via prefix-matching.
	"/repos/list",
	"/todolist",
	"/builds",
	"/health",
	"/vibing",
	"/guest/testable",
	"/shared-storage/",
	// Shared-machine deploy surface (doctor + script templates +
	// actual run + history). Scoped by allowedProjects in the handler;
	// run history is guest-filtered so one guest cannot see another
	// guest's deploys.
	"/deploy/ship",
	"/deploy/runs",
	"/deploy/templates",
	"/deploy/generate",
	"/deploy/diagnose",
	"/doctor/build",
	// Unified Runner surface (RUNNER_DEV.md). Full-tier guests reach
	// every read + manual-trigger path. Job authoring (POST
	// /runner/jobs) is owner-only and refused inside the handler
	// regardless of allow-list. Run history is guest-filtered by
	// TriggeredBy so a guest only sees their own runs. Agent
	// sessions and sandboxes stay owner-only — too broad to scope
	// safely in Phase 2.
	"/runner/jobs",
	"/runner/runs",
	"/runner/pools",
	// screenlog (local screen black box). A full-tier teammate may
	// monitor + analyze (the "watch my dad's box / a shared machine"
	// case). Deliberately NOT in the feedback-only or support
	// (read-only) tiers — screen-history access is high-trust. The
	// recorded machine's ScreenlogPolicy (allowedPeers / kill-switch)
	// is a second gate on top of this scope check.
	"/screenlog/",
}

// guestFeedbackOnlyAllowedPrefixes is the hardened end-user surface.
// Intentionally tight: only what the Feedback SDK needs to file reports and
// stream telemetry, plus the minimum health/info probes for discovery.
//
// Notable exclusions vs. the full tier:
//
//	/tasks, /vibing           — no AI-agent prompts (arbitrary code exec)
//	/dev/*                    — no dev-server proxy (could hit sensitive local services)
//	/builds                   — no triggering builds
//	/projects, /todolist      — no project-metadata enumeration
//	/agent/runners, /status   — surface the host's loadout of AI runners
//	/shared-storage/          — no blob pull-through
var guestFeedbackOnlyAllowedPrefixes = []string{
	"/feedback",
	"/blackbox/",
	"/voice/",
	"/info",
	"/health",
}

// guestSDKProjectAllowedPrefixes is the "tester" tier: an invited friend who
// can RUN the dev's pre-release app (load the host-built guest-safe Hermes
// bundle + hot-reload it) and file feedback, but cannot read code, enumerate
// projects, exec, deploy, or reach the vault. It is the feedback-only surface
// PLUS the narrow reload/status/events endpoints — deliberately NOT the "/dev/"
// subtree, which would expose the full dev-server proxy (arbitrary local ports).
//
// The AI-improve surface ("/vibing") is intentionally NOT in this static list.
// It is unlocked per-grant by the canVibe flag (isGuestAllowedPathForScopeVibe).
// A vibing task from a tester is always force-isolated AND routed to a GLM/BYO
// runner, never the owner's subscription (see handleVibingExecute) — so opting a
// tester into vibe never lends them code-read, host filesystem, or the owner's
// Claude/Codex plan.
var guestSDKProjectAllowedPrefixes = []string{
	"/feedback",
	"/blackbox/",
	"/voice/",
	"/info",
	"/health",
	"/dev/reload",
	"/dev/reload-app",
	"/dev/status",
	"/dev/events",
	// Read-only discovery: which of the host's projects this tester may run.
	// Returns only the tester's allowedProjects (handler-enforced).
	"/guest/testable",
	// Save a vibe straight onto the project branch. Project-gated + canVibe-gated
	// in the handler; a non-vibe tester is refused there.
	"/guest/vibe-save",
}

// guestDeployAllowedPrefixes is the tight shared-machine-deploy surface.
// Enough to run a scripted deploy for a scoped project, nothing more.
// The handler enforces allowedProjects on top of this allow-list.
var guestDeployAllowedPrefixes = []string{
	"/ops",
	"/ops/plan",
	"/deploy/ship",
	"/deploy/runs", // list + detail; filtered to the guest's own runs
	"/deploy/templates",
	"/deploy/generate", // read-only preview
	"/doctor/build",
	"/info",
	"/health",
	// Same shape as /deploy/runs: deploy-tier guests can list/inspect
	// their own runner runs but cannot author jobs (handler refuses
	// the POST regardless of the allow-list). Sandboxes + agent
	// sessions stay owner-only.
	"/runner/jobs",
	"/runner/runs",
	"/runner/pools",
}

// guestSupportAllowedPrefixes is the read-only support tier: status + file
// browse/read + streams + the agent's support endpoints. No /tasks, /exec,
// /dev, /deploy, /vault. Mirrors the read-only set of support sessions.
var guestSupportAllowedPrefixes = []string{
	"/info",
	"/health",
	"/agent/status",
	"/agent/capabilities",
	"/agent/runners",
	"/files/roots",
	"/files/list",
	"/files/read",
	"/files/raw",
	"/streams",
	"/streams/",
	"/support/",
}

// guestScopeOrDefault normalizes a cached scope string to one of the
// known tiers. An empty or unknown scope maps to the legacy default ("full").
func guestScopeOrDefault(s string) string {
	switch strings.TrimSpace(s) {
	case GuestScopeFeedbackOnly:
		return GuestScopeFeedbackOnly
	case GuestScopeSDKProject:
		return GuestScopeSDKProject
	case GuestScopeDeploy:
		return GuestScopeDeploy
	case GuestScopeSupport:
		return GuestScopeSupport
	case GuestScopeCircuit:
		return GuestScopeCircuit
	case GuestScopeFull:
		return GuestScopeFull
	default:
		return guestScopeDefaultLegacy
	}
}

// isGuestAllowedPathForScope returns true if `path` is inside the allow-list
// for the given scope. Unknown scopes collapse to "full" (legacy default).
//
// Match semantics: an entry that ends in "/" (e.g. "/dev/", "/blackbox/")
// is a SUBTREE match — any path beginning with that string passes. An
// entry without a trailing slash (e.g. "/tasks", "/agent/runners") is
// a SEGMENT-AWARE match: it allows the entry exactly OR entry + "/..."
// (sub-paths beneath it) but does NOT allow entry + "anything-else"
// (so "/agent/runners-debug" stays blocked even though it shares the
// /agent/runners prefix). This closes the H-4 collision class.
func isGuestAllowedPathForScope(path, scope string) bool {
	return isGuestAllowedPathForScopeVibe(path, scope, false)
}

// isGuestAllowedPathForScopeVibe is the canVibe-aware form. canVibe unlocks the
// AI-improve surface ("/vibing" and its sub-paths) for the sdk-project tester
// tier ONLY. It is ignored for every other scope: "full" already lists /vibing,
// and feedback-only / deploy / support / circuit must never reach it regardless
// of the flag. The 2-arg isGuestAllowedPathForScope delegates here with
// canVibe=false so callers that don't carry the flag keep the tight default.
func isGuestAllowedPathForScopeVibe(path, scope string, canVibe bool) bool {
	normScope := guestScopeOrDefault(scope)
	list := guestFullAllowedPrefixes
	switch normScope {
	case GuestScopeFeedbackOnly:
		list = guestFeedbackOnlyAllowedPrefixes
	case GuestScopeSDKProject:
		list = guestSDKProjectAllowedPrefixes
	case GuestScopeDeploy:
		list = guestDeployAllowedPrefixes
	case GuestScopeSupport:
		list = guestSupportAllowedPrefixes
	case GuestScopeCircuit:
		list = guestCircuitAllowedPrefixes
	}
	if path == "" {
		path = "/"
	}
	// canVibe is a per-grant opt-in, not part of the static scope list, so it
	// is gated here rather than baked into guestSDKProjectAllowedPrefixes.
	if canVibe && normScope == GuestScopeSDKProject && matchGuestAllowEntry(path, "/vibing") {
		return true
	}
	for _, prefix := range list {
		if matchGuestAllowEntry(path, prefix) {
			return true
		}
	}
	return false
}

// guestExactOnlyEntries is the set of allow-list entries whose match
// must be EXACT — no sub-path traversal. /agent/runners is in this set
// because /agent/runners/test exists as a write-side endpoint that
// spawns AI runners (and burns the host's API budget); we want guests
// to read the loadout list but not trigger runner spawns.
//
// Add new entries here whenever a new endpoint shares a prefix with a
// safe-to-read base path but its sub-paths grant write/exec privilege.
var guestExactOnlyEntries = map[string]struct{}{
	"/agent/runners": {},
	"/info":          {},
	"/health":        {},
	"/agent/status":  {},
	// /repos/list is the read-only enumeration; siblings clone/pull/
	// credentials/delete share the /repos/ prefix and must NOT be
	// reachable, so /repos/list is matched exact-only.
	"/repos/list": {},
}

// matchGuestAllowEntry implements the segment-aware match described on
// isGuestAllowedPathForScope. Trailing-slash entries match the prefix
// directly (subtree). Entries listed in guestExactOnlyEntries match
// only the exact path. Other slashless entries match the exact path
// or any path starting with entry + "/".
func matchGuestAllowEntry(path, entry string) bool {
	if entry == "" {
		return false
	}
	if strings.HasSuffix(entry, "/") {
		return strings.HasPrefix(path, entry)
	}
	if _, exactOnly := guestExactOnlyEntries[entry]; exactOnly {
		return path == entry
	}
	return path == entry || strings.HasPrefix(path, entry+"/")
}

// GetScope returns the scope for a guest, defaulting to "full" when no
// config has been synced yet (e.g. a fresh grant before the 10s config
// refresh fires). Returning "full" in the unknown case matches the legacy
// behavior for in-flight rows; the allow-list itself is what actually
// blocks dangerous paths once the config arrives.
func (m *GuestConfigManager) GetScope(guestUserID string) string {
	if m == nil {
		return guestScopeDefaultLegacy
	}
	cfg := m.GetConfig(guestUserID)
	if cfg == nil {
		return guestScopeDefaultLegacy
	}
	return guestScopeOrDefault(cfg.Scope)
}

// IsFeedbackOnly is a convenience for the task-spawn + endpoint-redaction
// paths that need a yes/no answer.
func (m *GuestConfigManager) IsFeedbackOnly(guestUserID string) bool {
	return m.GetScope(guestUserID) == GuestScopeFeedbackOnly
}

// GuestCanVibe reports whether this guest's grant opted into the AI-improve
// (vibe) capability. Default false — vibe is explicit opt-in at invite time,
// and only meaningful for the sdk-project tester tier (the /vibing gate in
// isGuestAllowedPathForScopeVibe ignores it for every other scope).
func (m *GuestConfigManager) GuestCanVibe(guestUserID string) bool {
	if m == nil {
		return false
	}
	cfg := m.GetConfig(guestUserID)
	if cfg == nil || cfg.CanVibe == nil {
		return false
	}
	return *cfg.CanVibe
}

// AllowedProjects returns the list of project slugs this guest may touch.
// Empty slice means "all projects" (current legacy behavior). Callers should
// treat a non-empty return as an allowlist and reject anything outside.
func (m *GuestConfigManager) AllowedProjects(guestUserID string) []string {
	if m == nil {
		return nil
	}
	cfg := m.GetConfig(guestUserID)
	if cfg == nil {
		return nil
	}
	return cleanProjectList(cfg.AllowedProjects)
}

// GuestCanAccessProject answers whether a guest may touch `project`. Returns
// true when the guest has no project narrowing (empty list = all projects)
// OR when the given project is in the list. Case-insensitive — matches how
// MobileProject.Name comparisons happen elsewhere in the agent.
func (m *GuestConfigManager) GuestCanAccessProject(guestUserID, project string) bool {
	project = strings.TrimSpace(project)
	allowed := m.AllowedProjects(guestUserID)
	if len(allowed) == 0 {
		return true
	}
	if project == "" {
		// No project identity attached to this request but the guest is
		// restricted — refuse. Forces callers to always tag the project.
		return false
	}
	for _, p := range allowed {
		if strings.EqualFold(strings.TrimSpace(p), project) {
			return true
		}
	}
	return false
}

// cleanProjectList trims whitespace, drops empty entries, and de-duplicates.
// Used on both the sending (CLI → Convex) and receiving (agent config refresh)
// sides so the list the agent compares against is always canonical.
func cleanProjectList(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		key := strings.ToLower(s)
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, s)
	}
	return out
}
