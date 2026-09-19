// previewReadyScript.ts — the "has the previewed app actually painted?" probe
// that the browser-lane WebView injects.
//
// Why this is its own module: it used to be an inline template literal in
// apps.tsx, which meant the single most failure-prone piece of the browser lane
// was the one piece with no test. The string exported here is the EXACT string
// injected in production, and previewReadyScript.test.ts evaluates it — not a
// hand-copied mirror, which would drift.
//
// ── The RN incident this fixes (2026-07-24) ────────────────────────────────
//
// The old predicate accepted a page as "rendered" when:
//
//     body.children.length > 1 || body.innerText.trim().length > 0
//
// Expo Web's index.html — verified by exporting BOTH sfmg and talos/mobile with
// `expo export -p web` (Expo SDK 54, RN 0.81) — ships this body:
//
//     <noscript>You need to enable JavaScript…</noscript>
//     <div id="root"></div>
//     <script src="/_expo/static/js/web/entry-….js" defer></script>
//
// That is THREE element children at document-end, before React has mounted
// anything at all. So `children.length > 1` was true immediately, the probe
// posted "yaver-rendered" on the first tick, and the loading overlay lifted to
// reveal an EMPTY #root — a blank screen.
//
// It then got worse, because the old probe latched (`if(s)return true`) and
// cleared its interval. Having declared success it could never retract, so when
// the entry bundle — 6.83 MB for sfmg, 7.58 MB for talos — was still in flight,
// or failed to execute, the user was left on a permanently blank page with no
// overlay, no error, no retry. "Rendered" had already been asserted.
//
// This is the exact INVERSE of the Flutter failure mode (a page that has really
// painted but is never recognised). One predicate has to serve both, so the fix
// is ordered specific→general rather than made stricter across the board:
//
//   1. Flutter's own markers, checked FIRST and left exactly as they were, so
//      the Flutter lane's behavior is bit-for-bit unchanged.
//   2. A known SPA mount point (#root / #app): ready only once it has a child.
//      This is the RN-web / React / Vue answer, and it is the actual signal —
//      react-dom having committed something into the container.
//   3. Otherwise the original heuristic, unchanged, for plain-web dev servers
//      that render straight into <body>.
//
// Do not "simplify" 2 into 3. The whole defect was treating a mount point that
// EXISTS as a mount point that has RENDERED.

/**
 * Source of the readiness predicate, as text.
 *
 * Kept as a string (rather than a real function passed through `.toString()`)
 * because release-mode minification is free to rewrite a function body, and a
 * probe that silently changes shape in production builds only is precisely the
 * class of bug this file documents.
 */
export const PREVIEW_READY_PREDICATE = `function yaverPreviewReady(doc){
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
}`;

export const PREVIEW_PROBE_STATE_FUNCTION = `function yaverPreviewProbeState(doc){
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
}`;

/**
 * Lane signal, injected via `injectedJavaScriptBeforeContentLoaded` so it is
 * set BEFORE the guest app's own scripts run. A lane-aware yaver-feedback SDK
 * reads `window.__yaverLane` in init() and, seeing "browser", self-hosts a
 * DRAGGABLE DOM floating icon — the only occlusion-proof feedback affordance in
 * the browser lane (the container's RN overlay is behind the fullScreen WebView
 * modal on iOS). See docs/audits/feedback-sdk-lanes-audit-2026-07-28.md and
 * sdk/feedback/web/src/YaverFeedback.ts::detectLane.
 *
 * Ends with `true;` — injected scripts must not evaluate to a bridge-interpreted
 * value. Idempotent: re-injection on reload just re-affirms the same lane.
 */
export const PREVIEW_LANE_SCRIPT = `(function(){try{window.__yaverLane='browser';}catch(e){}})(); true;`;

/** Report failed browser sub-resources before the guest boots. A main-document
 *  200 does not fire WebView.onHttpError when Metro's entry bundle fails, so
 *  the old first open stayed on an empty #root even after compilation finished.
 *  Both preview implementations consume this exact structured event and can
 *  retry the document once the bundle becomes available. Credentials are
 *  stripped before the URL crosses the WebView bridge. */
export const PREVIEW_RESOURCE_ERROR_SCRIPT = `(function(){try{
  if(window.__yaverPreviewResourceWatchInstalled)return true;
  window.__yaverPreviewResourceWatchInstalled=true;
  function clean(u){try{
    var x=new URL(String(u||''),location.href);
    ['token','__rp','access_token','password','secret','key'].forEach(function(k){if(x.searchParams.has(k))x.searchParams.set(k,'[redacted]');});
    return x.toString();
  }catch(e){return String(u||'').replace(/([?&](?:token|__rp|access_token|password|secret|key)=)[^\\s&]+/gi,'$1[redacted]');}}
  function safePath(u){try{
    var x=new URL(String(u||''),location.href);
    ['token','__rp','access_token','password','secret','key'].forEach(function(k){x.searchParams.delete(k);});
    return x.pathname+(x.searchParams.toString()?'?'+x.searchParams.toString():'');
  }catch(e){return '';}}
  window.addEventListener('error',function(event){try{
    var target=event&&event.target;
    if(!target||target===window||!(target.src||target.href))return;
    window.ReactNativeWebView&&window.ReactNativeWebView.postMessage(JSON.stringify({
      t:'yaver-preview-resource-error',
      tag:String(target.tagName||'node').toUpperCase(),
      url:clean(target.src||target.href),
      path:safePath(target.src||target.href),
      ts:Date.now()
    }));
  }catch(e){}},true);
}catch(e){}return true;})(); true;`;

/**
 * How long to keep asking. The old probe gave up after 120 ticks × 500 ms =
 * 60 s and then could never report readiness at all, which for a 7 MB RN web
 * bundle fetched over the relay is well inside the normal range. Raised to
 * cover the agent's own readiness budget (`devserver.go` waits up to 180 s
 * before declaring a dev server dead) — the phone giving up before the box does
 * turns a healthy slow start into a permanent blank.
 */
export const PREVIEW_READY_MAX_TICKS = 400; // 400 × 500ms ≈ 200s
export const PREVIEW_READY_TICK_MS = 500;

/**
 * The full injected script. Posts `{t:'yaver-rendered'}` once the predicate
 * passes, then stops. Ends with `true;` because injectedJavaScript must not
 * evaluate to a value the WebView bridge tries to interpret.
 */
export const PREVIEW_READY_SCRIPT = `(function(){
  try {
    ${PREVIEW_PROBE_STATE_FUNCTION}
    ${PREVIEW_READY_PREDICATE}
    var signalled = false;
    var lastProbeAt = 0;
    function post(t, state){
      if (!window.ReactNativeWebView) return;
      window.ReactNativeWebView.postMessage(JSON.stringify({t:t,state:state,ts:Date.now()}));
    }
    function check(){
      if (signalled) return true;
      var state = yaverPreviewProbeState(document);
      var now = Date.now();
      if (now - lastProbeAt > 2000) {
        lastProbeAt = now;
        post('yaver-preview-probe', state);
      }
      if (!yaverPreviewReady(document)) return false;
      signalled = true;
      post('yaver-rendered', state);
      return true;
    }
    if (!check()) {
      var n = 0;
      var iv = setInterval(function(){
        n++;
        if (check()) clearInterval(iv);
        else if (n > ${PREVIEW_READY_MAX_TICKS}) {
          post('yaver-preview-timeout', yaverPreviewProbeState(document));
          clearInterval(iv);
        }
      }, ${PREVIEW_READY_TICK_MS});
    }
  } catch (e) {}
  return true;
})();`;
