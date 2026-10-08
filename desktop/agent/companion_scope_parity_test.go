package main

// companion_scope_parity_test.go — the guard for the TV scope wall.
//
// Incident (2026-07-27, docs/audits/tv-vibing-scope-wall-deep-analysis-2026-07.md):
// the shipped tvOS app called nine endpoints its own session scope forbade —
// /droid/frame, /vibing/preview/*, /dev/start, /install/* — so the Projects
// list rendered while every preview 403'd "TV-scoped token cannot access this
// endpoint" behind a Try again that could never succeed. The existing
// TestCompanionSessionAllowed asserted the allowlist against itself, which
// encodes drift instead of catching it. These tests key off the CLIENT:
//   1. an explicit method+path contract mirroring tvos/YaverTV (fails when an
//      allowlist row is removed),
//   2. a scan of AgentClient.swift's literal paths (fails when the tvOS client
//      grows an endpoint the scope gate does not admit),
//   3. a deny-list that must NEVER open for companion scopes.

import (
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// tvClientEndpoints is the method+path contract of the shipped tvOS app.
// Source of truth: tvos/YaverTV/AgentClient.swift and the Views that drive it
// (DroidStreamView, WebPreviewStreamView, ProjectsView, SessionView,
// TasksView, RuntimeDashboardView, FeedbackView, AppleTVRemoteView).
// If you remove a row from companionSessionAllowed, this list is what breaks.
var tvClientEndpoints = []struct {
	method string
	path   string
}{
	{http.MethodGet, "/health"},
	{http.MethodGet, "/info"},
	{http.MethodGet, "/agent/status"},
	{http.MethodGet, "/agent/runners"},
	{http.MethodGet, "/tasks"},
	{http.MethodGet, "/tasks/task-123"},
	{http.MethodPost, "/tasks"},
	{http.MethodPost, "/tasks/task-123/continue"},
	{http.MethodPost, "/tasks/task-123/fork"},
	{http.MethodPost, "/tasks/task-123/answer"},
	{http.MethodGet, "/projects"},
	{http.MethodGet, "/workspace/apps"},
	{http.MethodGet, "/project/preview-capabilities"},
	{http.MethodGet, "/tmux/sessions"},
	{http.MethodGet, "/tmux/stream"},
	{http.MethodPost, "/ops"},
	{http.MethodPost, "/runner/session/turn"},
	{http.MethodGet, "/remote-runtime/sessions"},
	{http.MethodGet, "/remote-runtime/capabilities"},
	{http.MethodGet, "/remote-runtime/turn-credentials"},
	{http.MethodPost, "/remote-runtime/sessions/r1/control"},
	// Read-only shared-storage browsing; credentials and mutations stay local.
	{http.MethodGet, "/shared-storage/profiles"},
	{http.MethodGet, "/shared-storage/list"},
	{http.MethodGet, "/shared-storage/search"},
	// Pixels: the whole point of a TV.
	{http.MethodGet, "/droid/frame"},
	{http.MethodGet, "/capture/frame.jpg"},
	{http.MethodPost, "/vibing/preview/start"},
	{http.MethodPost, "/vibing/preview/stop"},
	{http.MethodPost, "/vibing/preview/snapshot"},
	// tvOS "kumanda" DOM selection + DOM-mode toggle — the cursor→element lane
	// (AgentClient.selectPreviewElement / setPreviewDomMode / movePreviewCursor).
	{http.MethodPost, "/vibing/preview/select"},
	{http.MethodPost, "/vibing/preview/dom-mode"},
	{http.MethodPost, "/vibing/preview/cursor"},
	{http.MethodGet, "/vibing/preview/status"},
	{http.MethodGet, "/vibing/preview/frames/abc123"},
	{http.MethodGet, "/vibing/preview/summaries"},
	{http.MethodGet, "/vibing/preview/clips"},
	{http.MethodGet, "/vibing/preview/events"},
	// Dev-server lifecycle + narration.
	{http.MethodPost, "/dev/start"},
	{http.MethodPost, "/dev/web-preview/start"},
	{http.MethodGet, "/dev/events"},
	{http.MethodGet, "/dev/status"},
	{http.MethodGet, "/dev/target"},
	// The route-to-fix lane: a missing toolchain renders as an Install button
	// whose stream narrates itself. Gating the remedy is the same defect as
	// gating the feature.
	{http.MethodPost, "/install/flutter"},
	{http.MethodGet, "/streams/install:flutter"},
	{http.MethodPost, "/feedback"},
	// MCP-server picker (VibeTurnPanel) — read-only names for the run's MCP set.
	{http.MethodGet, "/mcp/servers"},
}

// companionDeniedEndpoints must stay closed for tv/vision/spatial: the blast
// radius of a stolen TV token includes its owner-visible task conversation and
// preview UI, never direct exec, terminal, shutdown, or secret access.
var companionDeniedEndpoints = []struct {
	method string
	path   string
}{
	{http.MethodPost, "/exec"},
	{http.MethodPost, "/vault/list"},
	{http.MethodGet, "/vault/list"},
	{http.MethodPost, "/agent/shutdown"},
	{http.MethodGet, "/ws/terminal"},
	{http.MethodPost, "/tasks/task-123/cancel"},
	{http.MethodDelete, "/tasks/task-123"},
	{http.MethodPost, "/settings/repair-relay"},
	{http.MethodPost, "/dev/reload"},
	{http.MethodGet, "/host-share/fs/read"},
	// Account removal is intentionally Convex-only for companion surfaces; a
	// stolen TV/vision token must not uninstall or stop the user's box.
	{http.MethodPost, "/machine/remove"},
	{http.MethodPost, "/projects/refresh"},
	{http.MethodPost, "/shared-storage/profiles"},
	{http.MethodPost, "/shared-storage/list"},
	{http.MethodPost, "/shared-storage/search"},
}

// watchClientEndpoints is the same contract for the watch scope. Source of
// truth: watch/YaverWatch/SessionClient.swift (+ WatchStore) and the Wear OS
// client (wear/…): /runner/session/turn plus the standalone smartwatch lane
// /watch/turn + /watch/result (watch_http.go), which the agent served while
// this scope forbade it — the TV wall's twin on the wrist.
var watchClientEndpoints = []struct {
	method string
	path   string
}{
	{http.MethodGet, "/health"},
	{http.MethodGet, "/info"},
	{http.MethodGet, "/agent/status"},
	{http.MethodPost, "/ops"},
	{http.MethodPost, "/runner/session/turn"},
	{http.MethodPost, "/watch/turn"},
	{http.MethodGet, "/watch/result"},
}

func TestCompanionScopeAdmitsShippedWatchClient(t *testing.T) {
	for _, ep := range watchClientEndpoints {
		if !companionSessionAllowed(ep.method, ep.path, "watch") {
			t.Errorf("scope \"watch\" forbids %s %s — the shipped watch/Wear clients call this endpoint", ep.method, ep.path)
		}
	}
}

func TestCompanionScopeAdmitsShippedTVClient(t *testing.T) {
	for _, ep := range tvClientEndpoints {
		scopes := []string{"tv", "vision", "spatial"}
		if tvTaskMutationAllowed(ep.method, ep.path, "tv") {
			scopes = []string{"tv"}
		}
		for _, scope := range scopes {
			if !companionSessionAllowed(ep.method, ep.path, scope) {
				t.Errorf("scope %q forbids %s %s — the shipped client calls this endpoint; widen companionSessionAllowed or remove the call", scope, ep.method, ep.path)
			}
		}
	}
}

func TestTVTaskMutationScopeIsExact(t *testing.T) {
	for _, path := range []string{"/tasks", "/tasks/t1/continue", "/tasks/t1/fork", "/tasks/t1/answer"} {
		if !tvTaskMutationAllowed(http.MethodPost, path, "tv") {
			t.Errorf("expected TV task conversation route %s to be allowed", path)
		}
	}
	for _, path := range []string{"/tasks/t1", "/tasks/t1/cancel", "/tasks/t1/delete", "/tasks/t1/question", "/tasks/t1/continue/again"} {
		if tvTaskMutationAllowed(http.MethodPost, path, "tv") {
			t.Errorf("unexpected TV task mutation route %s is allowed", path)
		}
	}
	if tvTaskMutationAllowed(http.MethodPost, "/tasks", "vision") || tvTaskMutationAllowed(http.MethodDelete, "/tasks/t1", "tv") {
		t.Error("TV task mutations leaked to another scope or method")
	}
}

func TestCompanionScopeStaysClosedWhereItMust(t *testing.T) {
	for _, scope := range []string{"tv", "vision", "spatial", "watch"} {
		for _, ep := range companionDeniedEndpoints {
			if companionSessionAllowed(ep.method, ep.path, scope) {
				t.Errorf("scope %q ADMITS %s %s — companion scopes must never reach exec/vault/terminal/mutation", scope, ep.method, ep.path)
			}
		}
	}
}

// TestCompanionScopeParityWithSwiftSource scans the tvOS client source for
// literal agent paths and asserts each is reachable under the tv scope. This
// is the leg that catches the NEXT drift: a new endpoint added to
// AgentClient.swift without a scope row fails here before it ships as a
// living-room 403. Same pattern as mobile's beaconParity.test.ts — key off
// the code, not a copy.
func TestCompanionScopeParityWithSwiftSource(t *testing.T) {
	src := filepath.Join("..", "..", "tvos", "YaverTV", "AgentClient.swift")
	data, err := os.ReadFile(src)
	if err != nil {
		t.Skipf("tvOS source not present at %s: %v", src, err)
	}
	// Literal paths, including Swift interpolation: "/install/\(tool)".
	re := regexp.MustCompile(`"(/[a-zA-Z0-9_.\-/]+(?:\\\([a-zA-Z0-9_]+\)[a-zA-Z0-9_.\-/:]*)?)"`)
	seen := map[string]bool{}
	for _, m := range re.FindAllStringSubmatch(string(data), -1) {
		p := m[1]
		// Interpolated tail → representative concrete segment so prefix rows
		// (/install/, /streams/, /vibing/preview/frames/) are exercised.
		if i := strings.Index(p, `\(`); i >= 0 {
			p = p[:i] + "x"
		}
		if p == "/" || strings.HasPrefix(p, "/d/") { // relay route prefix, not an agent endpoint
			continue
		}
		seen[p] = true
	}
	if len(seen) < 8 {
		t.Fatalf("suspiciously few endpoint literals (%d) parsed from %s — the scan regex has drifted from the Swift source", len(seen), src)
	}
	for p := range seen {
		if !companionSessionAllowed(http.MethodGet, p, "tv") && !companionSessionAllowed(http.MethodPost, p, "tv") {
			t.Errorf("tvOS AgentClient.swift calls %q but the tv scope forbids it for both GET and POST — add a row to companionSessionAllowed (or drop the call)", p)
		}
	}
}
