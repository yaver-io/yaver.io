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
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
)

// browserLaneProbeStateJS — keep byte-identical to
// mobile/src/lib/previewReadyScript.ts::PREVIEW_PROBE_STATE_FUNCTION.
// The predicate below calls it, so the probe must define both, exactly as the
// phone injects them.
const browserLaneProbeStateJS = `function yaverPreviewProbeState(doc){
  var out = {
    hasBody:false,
    href:"",
    title:"",
    bodyChildren:0,
    bodyTextLen:0,
    mountId:"",
    mountChildren:-1,
    mountTextLen:0,
    visibleBoxCount:0,
    mediaCount:0,
    flutterMarker:false,
    flutterBooting:false,
    startingText:false,
    httpErrorDocument:false,
    reason:"document_not_ready"
  };
  try {
    out.href = String((doc && doc.location && doc.location.href) || "");
    out.title = String((doc && doc.title) || "");
    var b = doc && doc.body;
    if (!b) return out;
    out.hasBody = true;
    out.bodyChildren = b.children ? b.children.length : 0;
    var bt = (b.innerText || "").trim();
    out.bodyTextLen = bt.length;
    out.startingText = bt.indexOf('"status":"starting"') >= 0 || bt.indexOf("did not become ready") >= 0;
    // The response status is not exposed to injected JS. Recognize the exact
    // default Go 404 body that reached Dogfood on 2026-09-19.
    out.httpErrorDocument = /^404 page not found$/i.test(bt);
    out.flutterMarker = !!doc.querySelector("flutter-view,flt-glass-pane,flt-scene-host");
    out.flutterBooting = !!(doc.getElementById("splash") || doc.querySelector('script[src*="flutter"]'));
    var mount = doc.getElementById ? (doc.getElementById("root") || doc.getElementById("app")) : null;
    if (mount) {
      out.mountId = mount.id || "";
      out.mountChildren = mount.children ? mount.children.length : 0;
      out.mountTextLen = ((mount.innerText || "").trim()).length;
      out.mediaCount = mount.querySelectorAll ? mount.querySelectorAll("canvas,svg,img,video,picture").length : 0;
      var nodes = mount.querySelectorAll ? mount.querySelectorAll("*") : [];
      for (var i = 0; i < nodes.length && out.visibleBoxCount < 20; i++) {
        var el = nodes[i];
        var tag = (el.tagName || "").toUpperCase();
        if (tag === "SCRIPT" || tag === "STYLE" || tag === "NOSCRIPT") continue;
        var r = el.getBoundingClientRect ? el.getBoundingClientRect() : null;
        if (!r || r.width < 2 || r.height < 2) continue;
        var cs = doc.defaultView && doc.defaultView.getComputedStyle ? doc.defaultView.getComputedStyle(el) : null;
        if (cs && (cs.display === "none" || cs.visibility === "hidden" || cs.opacity === "0")) continue;
        out.visibleBoxCount++;
      }
    }
    if (out.httpErrorDocument) out.reason = "http_error_document";
    else if (out.startingText) out.reason = "agent_starting_response";
    else if (out.flutterMarker) out.reason = "flutter_engine_attached";
    else if (out.flutterBooting) out.reason = "flutter_booting";
    else if (out.mountId && out.mountChildren <= 0) out.reason = "empty_mount";
    else if (out.mountId && out.visibleBoxCount <= 1 && out.mountTextLen <= 0 && out.mediaCount <= 0) out.reason = "mount_without_visible_content";
    else if (out.mountId) out.reason = "mount_has_visible_content";
    else if (out.bodyChildren > 1 || out.bodyTextLen > 0) out.reason = "plain_body_content";
    else out.reason = "empty_body";
  } catch (e) {
    out.reason = "probe_exception";
  }
  return out;
}`

// browserLaneReadyPredicateJS — keep byte-identical to
// mobile/src/lib/previewReadyScript.ts::PREVIEW_READY_PREDICATE.
const browserLaneReadyPredicateJS = `function yaverPreviewReady(doc){
  try {
    var s = yaverPreviewProbeState(doc);
    if (!s.hasBody) return false;
    // The agent answers a still-compiling dev server with a structured 503
    // carrying this JSON body (devserver.go). It is text in the DOM, so
    // without this guard the error page itself reads as "rendered".
    if (s.startingText) return false;
    // Go's default ServeMux 404 is also plain body text. It must never lift the
    // loading surface or announce that the app rendered.
    if (s.httpErrorDocument) return false;
    // 1. Flutter — the engine has attached. Unchanged from the original probe.
    if (s.flutterMarker) return true;
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
    if (s.flutterBooting) return false;
    // 2. SPA mount point: present is not the same as painted.
    // Backward-compatible contract: once React/Vue/etc. has committed a child
    // into the known mount point, the app is rendered. The richer visible-box
    // facts are posted as diagnostics only; using them as a hard gate can break
    // valid apps whose first screen is a canvas, blank stage, or intentional
    // dark splash.
    if (s.mountId) return s.mountChildren > 0;
    // 3. Plain web: original heuristic, unchanged.
    return s.bodyChildren > 1 || s.bodyTextLen > 0;
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
	BodyPreview string               `json:"bodyPreview,omitempty"`
	Viewport    *BrowserLaneViewport `json:"viewport,omitempty"`
}

// BrowserLaneViewport is the client surface the browser operation must prove.
// A phone asking Dogfood to render must not be tested in a merely narrow
// desktop Chrome window: RN-web also branches on mobile mode, touch support,
// device scale and user agent. The caller sends its measured layout viewport;
// the agent bounds it before applying it to Chrome.
type BrowserLaneViewport struct {
	Width             int     `json:"width"`
	Height            int     `json:"height"`
	DeviceScaleFactor float64 `json:"deviceScaleFactor"`
	Mobile            bool    `json:"mobile"`
	Touch             bool    `json:"touch"`
	Surface           string  `json:"surface,omitempty"`
}

func normalizedBrowserLaneViewport(v BrowserLaneViewport) BrowserLaneViewport {
	if v.Width < 200 || v.Width > 3840 || v.Height < 200 || v.Height > 2160 {
		v.Width, v.Height = 430, 932
	}
	if v.DeviceScaleFactor < 0.5 || v.DeviceScaleFactor > 4 {
		v.DeviceScaleFactor = 1
	}
	v.Surface = strings.TrimSpace(v.Surface)
	if len(v.Surface) > 64 {
		v.Surface = v.Surface[:64]
	}
	return v
}

func browserLaneUserAgent(v BrowserLaneViewport) string {
	if v.Mobile {
		return "Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.0 Mobile/15E148 Safari/604.1"
	}
	return "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"
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
	return ProbeBrowserLaneWithViewport(ctx, previewURL, wait, BrowserLaneViewport{})
}

// ProbeBrowserLaneWithViewport performs the same operation at the requesting
// client's real surface profile. It starts navigation without waiting for the
// page load event: on a cold Metro build that event is held by the entry bundle,
// and the old synchronous Navigate consumed the phone's entire 65-second HTTP
// allowance before the doctor could return a structured stage.
func ProbeBrowserLaneWithViewport(ctx context.Context, previewURL string, wait time.Duration, requestedViewport BrowserLaneViewport) BrowserLaneProbeResult {
	start := time.Now()
	viewport := normalizedBrowserLaneViewport(requestedViewport)
	res := BrowserLaneProbeResult{URL: previewURL, Stage: BrowserLaneStageNoURL, Viewport: &viewport}
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

	// PIN THE BINARY, like every other launcher in this tree.
	//
	// This allocator used DefaultExecAllocatorOptions and never set ExecPath, so
	// chromedp did its OWN search — and on the owner's box that finds
	// /usr/bin/chromium-browser, which is the SNAP REDIRECTOR: it cannot create
	// its temp dir under a daemon and dies with "cannot create temporary
	// directory for the root file system", while /usr/bin/google-chrome sits
	// right there and works (measured 2026-08-05: version=FAIL/headless=FAIL for
	// both snap paths, OK/OK for google-chrome).
	//
	// browser.go already fixed exactly this and pins resolveLaunchableChrome's
	// answer. The fix landed in ONE of two launchers, which is the drift rule by
	// name — and the consequence was that the phone's browser lane could never
	// render sfmg on this box while the dashboard's could.
	allocOpts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Flag("headless", true),
		chromedp.Flag("disable-gpu", true),
		chromedp.Flag("no-sandbox", true),
		chromedp.Flag("mute-audio", true),
		// The preview is served over the agent's self-signed LAN TLS in some
		// paths; a cert refusal would otherwise present as a blank page.
		chromedp.Flag("ignore-certificate-errors", true),
		chromedp.WindowSize(viewport.Width, viewport.Height),
	)

	allocCtx, allocCancel := newPinnedChromeAllocator(ctx, allocOpts...)
	defer allocCancel()
	browserCtx, browserCancel := chromedp.NewContext(allocCtx)
	defer browserCancel()

	runCtx, runCancel := context.WithTimeout(browserCtx, wait+30*time.Second)
	defer runCancel()

	// Apply the complete device context before navigation. WindowSize alone is
	// inventory; device metrics + touch + a matching UA are what the page
	// actually observes.
	navigate := chromedp.ActionFunc(func(actionCtx context.Context) error {
		_, _, errorText, _, err := page.Navigate(previewURL).Do(actionCtx)
		if err != nil {
			return err
		}
		if errorText != "" {
			return fmt.Errorf("page load error %s", errorText)
		}
		return nil
	})
	touchPoints := int64(0)
	if viewport.Touch {
		touchPoints = 5
	}
	if err := chromedp.Run(runCtx,
		emulation.SetDeviceMetricsOverride(int64(viewport.Width), int64(viewport.Height), viewport.DeviceScaleFactor, viewport.Mobile),
		emulation.SetTouchEmulationEnabled(viewport.Touch).WithMaxTouchPoints(touchPoints),
		emulation.SetUserAgentOverride(browserLaneUserAgent(viewport)),
		navigate,
	); err != nil {
		// CLASSIFY A LAUNCH FAILURE AS A LAUNCH FAILURE.
		//
		// This used to look only for "exec"/"executable" in the message. The
		// snap-confined Chrome dies with
		//
		//   chrome failed to start: cannot create temporary directory for the
		//   root file system: No such file or directory
		//
		// which contains NEITHER word, so it fell through to StageNavigate and
		// the phone rendered "Browser lane stopped at navigate" with the remedy
		// "the preview URL refused the connection — the dev server bound a
		// different port … check /dev/status". Measured on the owner's box
		// 2026-08-05: a Chrome that never started, reported as a dev-server port
		// problem. The user is sent to inspect a healthy dev server while the
		// actual cause — which binary was launched — goes unmentioned.
		//
		// browserWindowLaunchErrorReason already knows every one of these
		// signatures and returns the browser_window.* code for it, so the
		// classification is shared rather than re-derived here. One vocabulary,
		// or the two lanes disagree about the same failure.
		if reason := browserWindowLaunchErrorReason(err); reason != "" {
			res.Stage = BrowserLaneStageNoBrowser
			res.Detail = "could not launch Chrome (" + reason + "): " + err.Error()
			return finish()
		}
		res.Stage = BrowserLaneStageNavigate
		res.Detail = "navigation failed: " + err.Error()
		return finish()
	}

	// Poll the SAME predicate the phone uses until it passes or the single
	// end-to-end allowance expires. This keeps a cold entry-bundle request alive
	// without repeatedly navigating and restarting Metro's compilation.
	deadline := start.Add(wait)
	var bodyText string
	for time.Now().Before(deadline) && runCtx.Err() == nil {
		var ready bool
		evalErr := chromedp.Run(runCtx,
			chromedp.Evaluate("(function(){"+browserLaneProbeStateJS+";"+browserLaneReadyPredicateJS+" return yaverPreviewReady(document);})()", &ready),
			chromedp.Evaluate(`(document.body && document.body.innerText || '').trim().slice(0,400)`, &bodyText),
		)
		if evalErr == nil && ready {
			// First paint is not enough. Expo Router is intentionally shown a
			// logical path such as "/" even though it entered through /dev-web/.
			// On 2026-09-05 Dogfood painted successfully, then HMR/full reload
			// fetched that visible URL from the agent mux and got its bare 404.
			// Probe the exact URL a reload would request before declaring green.
			var refreshStatus int
			refreshErr := chromedp.Run(runCtx, chromedp.Evaluate(`(async function(){
try { var r=await fetch(location.href,{method:"GET",cache:"no-store",credentials:"include"}); return r.status; }
catch(e) { return 0; }
})()`, &refreshStatus, func(params *runtime.EvaluateParams) *runtime.EvaluateParams {
				// chromedp.Evaluate does not await Promises by default. Without
				// this, it tries to decode the Promise object into an int and a
				// healthy reload is reported as an unreachable URL every time.
				return params.WithAwaitPromise(true)
			}))
			if refreshErr != nil || refreshStatus == 0 {
				res.Stage = BrowserLaneStageNavigate
				res.Detail = "the project painted once, but its visible URL could not be fetched for reload"
				return finish()
			}
			if refreshStatus < 200 || refreshStatus >= 400 {
				res.Stage = BrowserLaneStageHTTP
				res.Status = refreshStatus
				res.Detail = fmt.Sprintf("the project painted once, but its visible reload URL returned HTTP %d", refreshStatus)
				return finish()
			}
			res.Stage = BrowserLaneStageRendered
			res.Detail = fmt.Sprintf("the project painted and its reload URL returned HTTP %d at %dx%d @%gx (%s)", refreshStatus, viewport.Width, viewport.Height, viewport.DeviceScaleFactor, viewport.Surface)
			return finish()
		}
		select {
		case <-runCtx.Done():
			continue
		case <-time.After(500 * time.Millisecond):
		}
	}

	if strings.Contains(bodyText, `"status":"starting"`) {
		res.Stage = BrowserLaneStageCompiling
		res.Status = 503
		res.Detail = fmt.Sprintf("dev server was still compiling after %s", wait)
		res.BodyPreview = truncateForPreview(bodyText)
		return finish()
	}
	res.Stage = BrowserLaneStageBlank
	res.Detail = fmt.Sprintf("the page loaded at %dx%d but nothing painted within %s", viewport.Width, viewport.Height, wait)
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
	viewport := BrowserLaneViewport{
		Width:             queryInt(r, "viewportWidth"),
		Height:            queryInt(r, "viewportHeight"),
		DeviceScaleFactor: queryFloat(r, "deviceScaleFactor"),
		Mobile:            queryBool(r, "mobile"),
		Touch:             queryBool(r, "touch"),
		Surface:           r.URL.Query().Get("surface"),
	}
	res := ProbeBrowserLaneWithViewport(r.Context(), target, wait, viewport)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(res)
}

func queryInt(r *http.Request, name string) int {
	n, _ := strconv.Atoi(strings.TrimSpace(r.URL.Query().Get(name)))
	return n
}

func queryFloat(r *http.Request, name string) float64 {
	n, _ := strconv.ParseFloat(strings.TrimSpace(r.URL.Query().Get(name)), 64)
	return n
}

func queryBool(r *http.Request, name string) bool {
	n, _ := strconv.ParseBool(strings.TrimSpace(r.URL.Query().Get(name)))
	return n
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
		if _, err := exec.LookPath(n); err == nil {
			return true
		}
	}
	return false
}
