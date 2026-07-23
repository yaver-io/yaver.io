package main

import (
	"os"
	"regexp"
	"strings"
	"testing"
	"time"
)

// The probe and the phone must agree, byte for byte, on what "rendered" means.
//
// If they drift, the probe becomes a SECOND false green: it would report the
// browser lane healthy while the phone still shows a blank screen — strictly
// worse than having no probe, because now there is a green check standing
// between the user and the bug. The 2026-07-24 incident was exactly one
// predicate being wrong; two predicates being different is that incident with
// a witness who lies.
func TestBrowserLaneReadyPredicateMatchesMobile(t *testing.T) {
	const tsPath = "../../mobile/src/lib/previewReadyScript.ts"
	raw, err := os.ReadFile(tsPath)
	if err != nil {
		t.Fatalf("cannot read %s: %v — the mobile predicate is the source of truth for this probe", tsPath, err)
	}

	re := regexp.MustCompile("(?s)export const PREVIEW_READY_PREDICATE = `(.*?)`;")
	m := re.FindSubmatch(raw)
	if m == nil {
		t.Fatalf("could not find PREVIEW_READY_PREDICATE in %s — if it was renamed, update this test AND doctor_browser_lane.go together", tsPath)
	}
	mobile := strings.TrimSpace(string(m[1]))
	goSide := strings.TrimSpace(browserLaneReadyPredicateJS)

	if mobile != goSide {
		t.Fatalf("readiness predicate drifted between the phone and the doctor probe.\n"+
			"They must be byte-identical or the probe can pass while the phone stays blank.\n\n"+
			"--- mobile (%s) ---\n%s\n\n--- go (doctor_browser_lane.go) ---\n%s",
			tsPath, mobile, goSide)
	}
}

// The predicate is the thing that was wrong. Pin its actual behavior, not just
// its text, so a future "simplification" cannot quietly restore the bug.
func TestBrowserLaneReadyPredicateRejectsUnmountedExpoShell(t *testing.T) {
	// Expo Web's real index.html body, verified by exporting sfmg and
	// talos/mobile: noscript + div#root + script = 3 element children at
	// document-end, BEFORE react mounts. The old predicate accepted this.
	if !strings.Contains(browserLaneReadyPredicateJS, "getElementById") {
		t.Fatal("predicate no longer consults a mount point — an Expo shell with an empty #root will read as rendered again")
	}
	if !strings.Contains(browserLaneReadyPredicateJS, "mount.children.length > 0") {
		t.Fatal("predicate must require the mount point to have CHILDREN; 'exists' is what produced the blank screen")
	}
	// Flutter's markers must stay ahead of the mount-point branch — that lane
	// is owned elsewhere and its behavior must not change.
	flutterAt := strings.Index(browserLaneReadyPredicateJS, "flutter-view")
	mountAt := strings.Index(browserLaneReadyPredicateJS, "getElementById")
	if flutterAt < 0 || mountAt < 0 || flutterAt > mountAt {
		t.Fatal("the flutter marker check must come before the SPA mount check")
	}
	// The still-compiling 503 body must never read as rendered.
	if !strings.Contains(browserLaneReadyPredicateJS, `"status":"starting"`) {
		t.Fatal("predicate must reject the agent's structured 'starting' 503 body")
	}
}

func TestProbeBrowserLaneRefusesEmptyURLWithARemedy(t *testing.T) {
	res := ProbeBrowserLane(t.Context(), "   ", time.Second)
	if res.OK {
		t.Fatal("an empty URL must never be reported as a working browser lane")
	}
	if res.Stage != BrowserLaneStageNoURL {
		t.Fatalf("stage = %q, want %q", res.Stage, BrowserLaneStageNoURL)
	}
	if res.Remedy == "" {
		t.Fatal("every failure stage must carry a remedy — a bare refusal is what costs sessions")
	}
	if !strings.Contains(res.Remedy, "/dev/start") {
		t.Fatalf("remedy should name the actual next command, got %q", res.Remedy)
	}
}

func TestBrowserLaneRemedyNamesTheAuthCause(t *testing.T) {
	// 401/403 on the WebView URL is the relay-password case, and it is invisible
	// to any header-authenticated status check. The remedy has to say so.
	r := browserLaneRemedy(BrowserLaneStageHTTP, 401)
	if !strings.Contains(r, "__rp") {
		t.Fatalf("401 remedy must name the relay password param, got %q", r)
	}
	r404 := browserLaneRemedy(BrowserLaneStageHTTP, 404)
	if !strings.Contains(r404, "web") {
		t.Fatalf("404 remedy should point at the missing web target, got %q", r404)
	}
	if browserLaneRemedy(BrowserLaneStageBlank, 200) == "" {
		t.Fatal("the blank stage — the whole reason this probe exists — must carry a remedy")
	}
}

func TestBrowserLaneStagesAreAllDistinct(t *testing.T) {
	// "Blank screen" is the shared symptom of every one of these. If two stages
	// collapse to the same string the probe stops distinguishing them, which is
	// the exact ambiguity it was built to remove.
	seen := map[BrowserLaneStage]bool{}
	for _, s := range []BrowserLaneStage{
		BrowserLaneStageNoURL, BrowserLaneStageNoBrowser, BrowserLaneStageNavigate,
		BrowserLaneStageHTTP, BrowserLaneStageCompiling, BrowserLaneStageBlank,
		BrowserLaneStageRendered,
	} {
		if seen[s] {
			t.Fatalf("duplicate stage value %q", s)
		}
		seen[s] = true
	}
}
