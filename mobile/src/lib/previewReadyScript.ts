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
}`;

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
    ${PREVIEW_READY_PREDICATE}
    var signalled = false;
    function check(){
      if (signalled) return true;
      if (!yaverPreviewReady(document)) return false;
      signalled = true;
      if (window.ReactNativeWebView) {
        window.ReactNativeWebView.postMessage(JSON.stringify({t:'yaver-rendered'}));
      }
      return true;
    }
    if (!check()) {
      var n = 0;
      var iv = setInterval(function(){
        n++;
        if (check() || n > ${PREVIEW_READY_MAX_TICKS}) clearInterval(iv);
      }, ${PREVIEW_READY_TICK_MS});
    }
  } catch (e) {}
  return true;
})();`;
