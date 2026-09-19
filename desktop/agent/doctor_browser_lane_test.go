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

	for _, pair := range []struct {
		tsConst string
		goSide  string
	}{
		{"PREVIEW_READY_PREDICATE", browserLaneReadyPredicateJS},
		// The predicate calls yaverPreviewProbeState, so the probe injects BOTH —
		// each half can drift independently and each drift is its own false green.
		{"PREVIEW_PROBE_STATE_FUNCTION", browserLaneProbeStateJS},
	} {
		re := regexp.MustCompile("(?s)export const " + pair.tsConst + " = `(.*?)`;")
		m := re.FindSubmatch(raw)
		if m == nil {
			t.Fatalf("could not find %s in %s — if it was renamed, update this test AND doctor_browser_lane.go together", pair.tsConst, tsPath)
		}
		mobile := strings.TrimSpace(string(m[1]))
		goSide := strings.TrimSpace(pair.goSide)

		if mobile != goSide {
			t.Fatalf("%s drifted between the phone and the doctor probe.\n"+
				"They must be byte-identical or the probe can pass while the phone stays blank.\n\n"+
				"--- mobile (%s) ---\n%s\n\n--- go (doctor_browser_lane.go) ---\n%s",
				pair.tsConst, tsPath, mobile, goSide)
		}
	}
}

// The predicate is the thing that was wrong. Pin its actual behavior, not just
// its text, so a future "simplification" cannot quietly restore the bug.
func TestBrowserLaneReadyPredicateRejectsUnmountedExpoShell(t *testing.T) {
	// Expo Web's real index.html body, verified by exporting sfmg and
	// talos/mobile: noscript + div#root + script = 3 element children at
	// document-end, BEFORE react mounts. The old predicate accepted this.
	combined := browserLaneProbeStateJS + browserLaneReadyPredicateJS
	if !strings.Contains(combined, "getElementById") {
		t.Fatal("predicate no longer consults a mount point — an Expo shell with an empty #root will read as rendered again")
	}
	if !strings.Contains(browserLaneReadyPredicateJS, "s.mountChildren > 0") {
		t.Fatal("predicate must require the mount point to have CHILDREN; 'exists' is what produced the blank screen")
	}
	// Flutter's markers must stay ahead of the mount-point branch — that lane
	// is owned elsewhere and its behavior must not change.
	flutterAt := strings.Index(browserLaneReadyPredicateJS, "s.flutterMarker")
	mountAt := strings.Index(browserLaneReadyPredicateJS, "s.mountId")
	if flutterAt < 0 || mountAt < 0 || flutterAt > mountAt {
		t.Fatal("the flutter marker check must come before the SPA mount check")
	}
	// The still-compiling 503 body must never read as rendered.
	if !strings.Contains(browserLaneProbeStateJS, `"status":"starting"`) {
		t.Fatal("probe state must reject the agent's structured 'starting' 503 body")
	}
	if !strings.Contains(browserLaneReadyPredicateJS, "s.startingText") {
		t.Fatal("predicate must consult startingText so the 503 body never reads as rendered")
	}
	if !strings.Contains(browserLaneProbeStateJS, "404 page not found") ||
		!strings.Contains(browserLaneReadyPredicateJS, "s.httpErrorDocument") {
		t.Fatal("the agent's bare 404 document must never read as rendered")
	}
}

func TestBrowserLaneDoctorRequiresReloadURLCapability(t *testing.T) {
	raw, err := os.ReadFile("doctor_browser_lane.go")
	if err != nil {
		t.Fatal(err)
	}
	s := string(raw)
	for _, want := range []string{
		`fetch(location.href`,
		`credentials:"include"`,
		`WithAwaitPromise(true)`,
		`visible reload URL returned HTTP`,
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("browser-lane doctor can return rendered without proving refresh persistence; missing %q", want)
		}
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

func TestBrowserLaneViewportKeepsARealClientSurface(t *testing.T) {
	got := normalizedBrowserLaneViewport(BrowserLaneViewport{
		Width: 393, Height: 852, DeviceScaleFactor: 3,
		Mobile: true, Touch: true, Surface: "yaver-mobile-app",
	})
	if got.Width != 393 || got.Height != 852 || got.DeviceScaleFactor != 3 || !got.Mobile || !got.Touch {
		t.Fatalf("client surface was not preserved: %#v", got)
	}
	if !strings.Contains(browserLaneUserAgent(got), "Mobile") {
		t.Fatalf("mobile surface received a desktop user agent: %q", browserLaneUserAgent(got))
	}
}

func TestBrowserLaneViewportRejectsUnboundedDimensions(t *testing.T) {
	got := normalizedBrowserLaneViewport(BrowserLaneViewport{Width: 100000, Height: 1, DeviceScaleFactor: 99})
	if got.Width != 430 || got.Height != 932 || got.DeviceScaleFactor != 1 {
		t.Fatalf("unbounded viewport did not fall back safely: %#v", got)
	}
}
