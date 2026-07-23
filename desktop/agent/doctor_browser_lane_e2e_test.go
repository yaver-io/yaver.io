package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

// End-to-end probe tests: a REAL headless browser against a REAL server.
//
// The shells below are not invented. They are the actual body Expo Web emits,
// captured by running `expo export -p web` against sfmg and talos/mobile
// (Expo SDK 54 / RN 0.81): noscript + div#root + script — three element
// children at document-end, before react mounts anything. That shape is the
// entire 2026-07-24 blank-screen incident, so it is what the probe is pinned
// against.
//
// Skipped when no Chrome is installed: this suite must degrade to "not run",
// never to a green that means nothing.

const expoShellHead = `<!DOCTYPE html><html><head><title>t</title>
<style id="expo-reset">html,body{height:100%}#root{display:flex;height:100%}</style></head>
<body>
<noscript>You need to enable JavaScript to run this app.</noscript>
<div id="root"></div>`

func skipWithoutChrome(t *testing.T) {
	t.Helper()
	if findChromePath() == "" && !chromeLikelyOnPath() {
		t.Skip("no Chrome/Chromium on this box — browser-lane probe cannot run")
	}
	if os.Getenv("YAVER_SKIP_BROWSER_TESTS") != "" {
		t.Skip("YAVER_SKIP_BROWSER_TESTS set")
	}
}

// The regression itself: an Expo shell whose bundle never mounts. Three body
// children, empty #root. The OLD predicate called this "rendered"; the probe
// must call it blank.
func TestProbeBrowserLaneCatchesUnmountedExpoShell(t *testing.T) {
	skipWithoutChrome(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, expoShellHead+`<script>/* bundle that never mounts */</script></body></html>`)
	}))
	defer srv.Close()

	res := ProbeBrowserLane(t.Context(), srv.URL, 3*time.Second)
	if res.OK {
		t.Fatal("an Expo shell with an empty #root must NOT be reported as rendered — this is the blank-screen bug")
	}
	if res.Stage != BrowserLaneStageBlank {
		t.Fatalf("stage = %q, want %q", res.Stage, BrowserLaneStageBlank)
	}
	if res.Remedy == "" {
		t.Fatal("the blank verdict must carry a remedy")
	}
}

// The positive case: same shell, but the bundle mounts into #root shortly
// after load — exactly what a real RN web app does once its entry executes.
func TestProbeBrowserLaneSeesRealMount(t *testing.T) {
	skipWithoutChrome(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, expoShellHead+`<script>
setTimeout(function(){
  var d=document.createElement('div');
  d.textContent='hello from the app';
  document.getElementById('root').appendChild(d);
}, 300);
</script></body></html>`)
	}))
	defer srv.Close()

	res := ProbeBrowserLane(t.Context(), srv.URL, 15*time.Second)
	if !res.OK {
		t.Fatalf("a mounted app must be reported as rendered, got stage=%q detail=%q preview=%q",
			res.Stage, res.Detail, res.BodyPreview)
	}
	if res.Stage != BrowserLaneStageRendered {
		t.Fatalf("stage = %q, want %q", res.Stage, BrowserLaneStageRendered)
	}
}

// The agent's own structured 503 while a web dev server is still binding must
// read as "compiling", never as rendered and never as a hard failure — telling
// a user their app is broken while it is merely still building is how a healthy
// slow start gets debugged for an hour.
func TestProbeBrowserLaneRecognisesStillCompiling(t *testing.T) {
	skipWithoutChrome(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Retry-After", "2")
		w.WriteHeader(http.StatusServiceUnavailable)
		fmt.Fprint(w, `{"status":"starting","framework":"expo","port":19006,"message":"still starting"}`)
	}))
	defer srv.Close()

	res := ProbeBrowserLane(t.Context(), srv.URL, 3*time.Second)
	if res.Stage != BrowserLaneStageCompiling {
		t.Fatalf("stage = %q, want %q (detail=%q)", res.Stage, BrowserLaneStageCompiling, res.Detail)
	}
	if res.OK {
		t.Fatal("still-compiling is not OK — but it must be distinguishable from blank")
	}
	if !strings.Contains(res.Remedy, "re-probe") {
		t.Fatalf("compiling remedy should tell the caller to retry, got %q", res.Remedy)
	}
}

// Point this at a real `expo export -p web` output to verify the probe against
// an actual project rather than a synthetic shell:
//
//	YAVER_BROWSER_LANE_EXPORT_DIR=/path/to/dist go test -run RealExport ./...
//
// Env-gated because the export is a build artifact, not a fixture — but kept in
// the tree because "does MY project render in the browser lane?" is exactly the
// question a user needs answered, and this is the shortest path to it.
func TestProbeBrowserLaneAgainstRealExport(t *testing.T) {
	dir := os.Getenv("YAVER_BROWSER_LANE_EXPORT_DIR")
	if dir == "" {
		t.Skip("set YAVER_BROWSER_LANE_EXPORT_DIR to a web export directory to run this")
	}
	skipWithoutChrome(t)
	srv := httptest.NewServer(http.FileServer(http.Dir(dir)))
	defer srv.Close()

	res := ProbeBrowserLane(t.Context(), srv.URL, 60*time.Second)
	t.Logf("stage=%s ok=%v elapsed=%dms detail=%s preview=%q",
		res.Stage, res.OK, res.ElapsedM, res.Detail, res.BodyPreview)
	if !res.OK {
		t.Fatalf("%s did not render in the browser lane: %s — %s", dir, res.Detail, res.Remedy)
	}
}

// A dead port must be diagnosed as a transport failure, not as "blank". They
// have completely different remedies and both look identical on the phone.
func TestProbeBrowserLaneDistinguishesDeadServerFromBlankPage(t *testing.T) {
	skipWithoutChrome(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := srv.URL
	srv.Close() // nothing is listening now

	res := ProbeBrowserLane(t.Context(), url, 3*time.Second)
	if res.Stage == BrowserLaneStageRendered {
		t.Fatal("a dead server must never report rendered")
	}
	if res.Stage == BrowserLaneStageBlank {
		t.Fatalf("a refused connection must be reported as %q, not %q — the remedies are different",
			BrowserLaneStageNavigate, BrowserLaneStageBlank)
	}
}
