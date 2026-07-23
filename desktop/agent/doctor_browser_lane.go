package main

// doctor_browser_lane.go — does the browser lane ACTUALLY render this project?
//
// ── Why this exists (2026-07-24 incident) ─────────────────────────────────────
//
// The browser lane shipped a blank screen for every React-Native/Expo project
// and nothing in the product could see it. Every inventory-style check was
// green: `/dev/status` said running, the dev server was bound, the HTTP proxy
// returned 200, the bundle built. The phone still showed nothing, because the
// WebView's paint probe accepted a page as "rendered" when body.children > 1 —
// and Expo Web's index.html ships three body children (noscript + div#root +
// script) BEFORE React mounts. The overlay lifted onto an empty #root.
//
// That is the whole false-green class this repo keeps re-learning: the
// inventory says yes, the operation says no. A probe that asks "is the dev
// server running?" cannot ever catch it. The only check that can is one that
// loads the page in a real browser and asks whether anything was actually
// PAINTED.
//
// So this probe attempts the real operation. It drives the agent's own headless
// Chrome (chromedp, already a dependency) against the exact URL the phone's
// WebView would load, waits for the exact readiness predicate the phone uses,
// and reports which stage failed and what to do about it.
//
// ── The anti-drift rule ───────────────────────────────────────────────────────
//
// browserLaneReadyPredicateJS below MUST stay byte-identical to
// PREVIEW_READY_PREDICATE in mobile/src/lib/previewReadyScript.ts. If the phone
// and the probe disagree about what "rendered" means, this probe becomes a
// second false green — it would pass while the phone stays blank, which is
// strictly worse than having no probe at all. doctor_browser_lane_test.go reads
// the TypeScript file and fails on any difference. Do not "tidy" one side.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/chromedp/chromedp"
)

// browserLaneReadyPredicateJS — keep byte-identical to
// mobile/src/lib/previewReadyScript.ts::PREVIEW_READY_PREDICATE.
const browserLaneReadyPredicateJS = `function yaverPreviewReady(doc){
  try {
    var b = doc && doc.body;
    if (!b) return false;
    var bt = (b.innerText || '').trim();
    // The agent answers a still-compiling dev server with a structured 503
    // carrying this JSON body (devserver.go). It is text in the DOM, so
    // without this guard the error page itself reads as "rendered".
    if (bt.indexOf('"status":"starting"') >= 0) return false;
    if (bt.indexOf('did not become ready') >= 0) return false;
    // 1. Flutter — the engine has attached. Unchanged from the original probe.
    if (doc.querySelector('flutter-view,flt-glass-pane,flt-scene-host')) return true;
    // 1b. Flutter is BOOTING: its bootstrap page is up but no engine marker yet.
    // Measured against a live "flutter run -d web-server" (e-mobile, 2026-07-24):
    // NOTE: this text is mirrored verbatim into a Go raw string
    // (doctor_browser_lane.go) — never use a backtick in this block.
    // the body is <picture id="splash"> + <script>, i.e. children.length === 2,
    // so branch 3 below returned TRUE and the overlay lifted onto a page the
    // engine had not touched. Flutter never has a #root, so the SPA branch
    // could not catch it either. Nobody reported it because a splash IS visible
    // content — the failure looks like a loading state instead of a blank void,
    // and a Flutter app that never boots then sits on its splash forever with
    // the overlay already gone and no error shown.
    if (doc.getElementById('splash') || doc.querySelector('script[src*="flutter"]')) return false;
    // 2. SPA mount point: present is not the same as painted.
    var mount = doc.getElementById ? (doc.getElementById('root') || doc.getElementById('app')) : null;
    if (mount) return mount.children.length > 0;
    // 3. Plain web: original heuristic, unchanged.
    return b.children.length > 1 || bt.length > 0;
  } catch (e) { return false; }
}`

// BrowserLaneStage names how far the lane got before it stopped working. The
// stage IS the diagnosis — "blank screen" is a symptom shared by every one of
// these, and telling them apart by hand is what cost a session.
type BrowserLaneStage string

const (
	BrowserLaneStageNoURL     BrowserLaneStage = "no-url"     // nothing to load
	BrowserLaneStageNoBrowser BrowserLaneStage = "no-browser" // Chrome missing on this box
	BrowserLaneStageNavigate  BrowserLaneStage = "navigate"   // connection refused / DNS / TLS
	BrowserLaneStageHTTP      BrowserLaneStage = "http"       // reached, non-2xx
	BrowserLaneStageCompiling BrowserLaneStage = "compiling"  // agent's structured 503 "starting"
	BrowserLaneStageBlank     BrowserLaneStage = "blank"      // 200 + document, nothing painted
	BrowserLaneStageRendered  BrowserLaneStage = "rendered"   // the good one
)

// BrowserLaneProbeResult is what every surface renders.
type BrowserLaneProbeResult struct {
	OK       bool             `json:"ok"`
	Stage    BrowserLaneStage `json:"stage"`
	URL      string           `json:"url"`
	Status   int              `json:"httpStatus,omitempty"`
	Detail   string           `json:"detail"`
	Remedy   string           `json:"remedy,omitempty"`
	ElapsedM int64            `json:"elapsedMs"`
	// BodyPreview is the first ~200 chars of visible text when the page loaded
	// but painted nothing. It is the difference between "blank" and "blank
	// showing a stack trace".
	BodyPreview string `json:"bodyPreview,omitempty"`
}

// browserLaneRemedy carries the WHY into the error text. A vague remedy costs
// whole sessions (see errSecInternalComponent, 2026-07-19), so each of these
// names the specific next action rather than "check your configuration".
func browserLaneRemedy(stage BrowserLaneStage, status int) string {
	switch stage {
	case BrowserLaneStageNoURL:
		return "the dev server reported no URL — POST /dev/start with {web:true, workDir} first, then re-probe"
	case BrowserLaneStageNoBrowser:
		return "install Chrome or Chromium on this box; the browser-lane probe drives a real browser because nothing else can prove a page painted"
	case BrowserLaneStageNavigate:
		return "the preview URL refused the connection — the dev server bound a different port, or died after /dev/start returned; check /dev/status and the dev server log"
	case BrowserLaneStageHTTP:
		if status == 401 || status == 403 {
			return "auth was rejected — the WebView URL carries ?token= and &__rp=; a missing relay password (__rp) is the usual cause, sign in again to refetch it"
		}
		if status == 404 {
			return "the preview path 404'd — the dev server is up but is not serving a web target at this path; confirm the project has a web build (expo: react-native-web + react-dom)"
		}
		return fmt.Sprintf("the preview URL returned HTTP %d", status)
	case BrowserLaneStageCompiling:
		return "the dev server is still compiling — a first web build can take up to a minute; re-probe, and only treat it as failed if it never leaves this stage"
	case BrowserLaneStageBlank:
		return "the page loaded but painted nothing: the JS bundle did not mount. Check the browser console for a runtime error, confirm the web deps match the SDK (expo install --check), and confirm the entry bundle finished downloading"
	}
	return ""
}

// ProbeBrowserLane loads previewURL in a real headless browser and reports
// whether the project actually painted.
//
// previewURL must be the EXACT url the phone would load, query string and all.
// Probing a hand-built URL without ?token=/&__rp= is how a probe passes while
// the phone gets a 401 — the status endpoint is header-authenticated and the
// WebView URL is query-authenticated, so they can fail independently.
func ProbeBrowserLane(ctx context.Context, previewURL string, wait time.Duration) BrowserLaneProbeResult {
	start := time.Now()
	res := BrowserLaneProbeResult{URL: previewURL, Stage: BrowserLaneStageNoURL}
	finish := func() BrowserLaneProbeResult {
		res.ElapsedM = time.Since(start).Milliseconds()
		res.Remedy = browserLaneRemedy(res.Stage, res.Status)
		res.OK = res.Stage == BrowserLaneStageRendered
		return res
	}

	if strings.TrimSpace(previewURL) == "" {
		res.Detail = "no preview URL to load"
		return finish()
	}
	if findChromePath() == "" && !chromeLikelyOnPath() {
		res.Stage = BrowserLaneStageNoBrowser
		res.Detail = "no Chrome/Chromium found on this machine"
		return finish()
	}
	if wait <= 0 {
		wait = 90 * time.Second
	}

	allocCtx, allocCancel := chromedp.NewExecAllocator(ctx,
		append(chromedp.DefaultExecAllocatorOptions[:],
			chromedp.Flag("headless", true),
			chromedp.Flag("disable-gpu", true),
			chromedp.Flag("no-sandbox", true),
			chromedp.Flag("mute-audio", true),
			// The preview is served over the agent's self-signed LAN TLS in some
			// paths; a cert refusal would otherwise present as a blank page.
			chromedp.Flag("ignore-certificate-errors", true),
			chromedp.WindowSize(430, 932), // phone-shaped: layout bugs show up here
		)...)
	defer allocCancel()
	browserCtx, browserCancel := chromedp.NewContext(allocCtx)
	defer browserCancel()

	runCtx, runCancel := context.WithTimeout(browserCtx, wait+30*time.Second)
	defer runCancel()

	// Navigate. chromedp surfaces transport failures here; HTTP status is read
	// separately below because a 4xx still "navigates" successfully.
	if err := chromedp.Run(runCtx, chromedp.Navigate(previewURL)); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "exec") ||
			strings.Contains(strings.ToLower(err.Error()), "executable") {
			res.Stage = BrowserLaneStageNoBrowser
			res.Detail = "could not launch Chrome: " + err.Error()
			return finish()
		}
		res.Stage = BrowserLaneStageNavigate
		res.Detail = "navigation failed: " + err.Error()
		return finish()
	}

	// Read the document's own view of what happened. Done in-page rather than
	// via network events so a redirect chain collapses to the final answer.
	var bodyText string
	_ = chromedp.Run(runCtx, chromedp.Evaluate(
		`(document.body && document.body.innerText || '').trim().slice(0,400)`, &bodyText))

	// The agent's structured 503 while a web dev server is still binding.
	if strings.Contains(bodyText, `"status":"starting"`) {
		res.Stage = BrowserLaneStageCompiling
		res.Status = 503
		res.Detail = "dev server is still compiling its first web build"
		res.BodyPreview = truncateForPreview(bodyText)
		return finish()
	}

	// Poll the SAME predicate the phone uses until it passes or we run out.
	deadline := time.Now().Add(wait)
	for {
		var ready bool
		evalErr := chromedp.Run(runCtx, chromedp.Evaluate(
			"(function(){"+browserLaneReadyPredicateJS+" return yaverPreviewReady(document);})()", &ready))
		if evalErr == nil && ready {
			res.Stage = BrowserLaneStageRendered
			res.Detail = "the project painted real content in the browser lane"
			return finish()
		}
		if time.Now().After(deadline) {
			break
		}
		select {
		case <-runCtx.Done():
			break
		case <-time.After(500 * time.Millisecond):
		}
		if runCtx.Err() != nil {
			break
		}
	}

	_ = chromedp.Run(runCtx, chromedp.Evaluate(
		`(document.body && document.body.innerText || '').trim().slice(0,400)`, &bodyText))
	res.Stage = BrowserLaneStageBlank
	res.Detail = fmt.Sprintf("the page loaded but nothing painted within %s", wait)
	res.BodyPreview = truncateForPreview(bodyText)
	return finish()
}

// handleDoctorBrowserLane serves GET/POST /doctor/browser-lane.
//
// With no `url` it probes the CURRENTLY RUNNING dev server, building the exact
// URL the phone's WebView would load — including the ?token= / &__rp= query
// auth. Probing a hand-built URL without those is how a probe goes green while
// the phone gets a 401: /dev/status is header-authenticated, the WebView URL is
// query-authenticated, and they fail independently.
func (s *HTTPServer) handleDoctorBrowserLane(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "use GET or POST")
		return
	}
	target := strings.TrimSpace(r.URL.Query().Get("url"))
	wait := 60 * time.Second
	if v := strings.TrimSpace(r.URL.Query().Get("waitSeconds")); v != "" {
		if n, err := time.ParseDuration(v + "s"); err == nil && n > 0 && n <= 5*time.Minute {
			wait = n
		}
	}
	if target == "" {
		target = s.currentBrowserLaneURL()
	}
	res := ProbeBrowserLane(r.Context(), target, wait)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(res)
}

// currentBrowserLaneURL reconstructs the phone's preview URL for the active dev
// server, or "" when nothing is serving. Mirrors
// mobile/src/lib/quic.ts::getDevServerBundleUrl — same path, same query auth.
func (s *HTTPServer) currentBrowserLaneURL() string {
	if s == nil || s.devServerMgr == nil {
		return ""
	}
	st := s.devServerMgr.Status()
	if st == nil || !(st.Running || st.Building) {
		return ""
	}
	path := strings.TrimSpace(st.BundleURL)
	if path == "" {
		path = "/dev/"
	}
	base := fmt.Sprintf("http://127.0.0.1:%d", s.port)
	sep := "?"
	if strings.Contains(path, "?") {
		sep = "&"
	}
	return base + path + sep + "token=" + url.QueryEscape(s.token)
}

func truncateForPreview(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 200 {
		return s[:200] + "…"
	}
	return s
}

// chromeLikelyOnPath is a cheap secondary check so a box where Chrome is on
// PATH but not at a well-known location is not reported as browser-less.
func chromeLikelyOnPath() bool {
	for _, n := range []string{"google-chrome", "chromium", "chromium-browser", "chrome"} {
		if hostShareCommandExists(n) {
			return true
		}
	}
	return false
}
