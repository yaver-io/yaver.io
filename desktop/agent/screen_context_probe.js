// screen_context_probe.js — the in-page probe that tells Yaver WHICH SCREEN the
// user is looking at.
//
// The agent injects this into every HTML document it serves for a preview
// (screen_context_inject.go), which is why there is exactly ONE copy of it. The
// mobile WebView and the web dashboard's iframe both load the agent-proxied
// document, so both get this same file — there is no `.web.ts` twin to drift,
// and no hand-mirrored second implementation to fall out of sync.
//
// It reports LABELS, never VALUES. On the sfmg onboarding screen that motivated
// this ("Adın ne?" — "what's your name?") the user is typing their own name into
// the field on screen. `input.value` is never read, here or anywhere below.
//
// Constraints this file must keep:
//   * ES5 only. It runs inside whatever the guest app's bundle targets, and a
//     syntax error here would break the PREVIEW, not just the probe.
//   * Never throw. Every entry point is wrapped; a probe that can break a
//     customer's app is worse than no probe.
//   * Never touch the network. It posts to its host surface, which forwards it
//     over the surface's own authenticated channel. An unauthenticated write
//     straight into the agent would let anyone on the LAN dictate text into
//     somebody else's AI prompt.
(function () {
  if (window.__yaverScreenProbe) return;
  window.__yaverScreenProbe = true;

  var MAX_CONTROLS = 25;
  var MAX_LABEL = 80;
  var POLL_MS = 2500;
  // Re-post an unchanged screen this often so the agent-side freshness window
  // (screenContextTTL) never expires while the user is still sitting on it.
  var HEARTBEAT_MS = 45000;

  function text(el) {
    try {
      var t = (el.innerText || el.textContent || "");
      return t.replace(/\s+/g, " ").replace(/^ | $/g, "");
    } catch (e) {
      return "";
    }
  }

  function clamp(s) {
    if (!s) return "";
    s = String(s).replace(/\s+/g, " ").replace(/^ | $/g, "");
    if (s.length > MAX_LABEL) s = s.slice(0, MAX_LABEL - 1) + "…";
    return s;
  }

  function visible(el) {
    try {
      var r = el.getBoundingClientRect ? el.getBoundingClientRect() : null;
      if (!r || r.width < 2 || r.height < 2) return false;
      var cs = window.getComputedStyle ? window.getComputedStyle(el) : null;
      if (cs && (cs.display === "none" || cs.visibility === "hidden" || cs.opacity === "0")) return false;
      return true;
    } catch (e) {
      return false;
    }
  }

  // route — the in-app path, with the agent's own proxy prefixes and the query
  // string removed.
  //
  // The query string is dropped rather than truncated because the preview URL
  // carries a `sig` bundle token (dev_bundle_sig.go). A route is for orienting
  // the runner; it is not worth putting a credential one hop from a prompt.
  function route() {
    try {
      var p = String(location.pathname || "");
      p = p.replace(/^.*?\/dev(?:-web)?\//, "/");
      p = p.replace(/^\/web-bundle\//, "/");
      if (!p) p = "/";
      return p + String(location.hash || "");
    } catch (e) {
      return "";
    }
  }

  // heading — the human name of the screen, in decreasing order of confidence.
  function heading() {
    try {
      var sel = "h1,h2,h3,[role=heading],[aria-level]";
      var nodes = document.querySelectorAll(sel);
      for (var i = 0; i < nodes.length && i < 60; i++) {
        if (!visible(nodes[i])) continue;
        var t = clamp(text(nodes[i]));
        if (t) return t;
      }
      // React Native Web renders <Text> as a plain div — no <h1> anywhere. Fall
      // back to the first visible short text run inside the mount, which on a
      // titled screen is the title. Bounded so a paragraph never wins.
      var mount = document.getElementById("root") || document.getElementById("app");
      if (mount) {
        var all = mount.querySelectorAll("*");
        for (var j = 0; j < all.length && j < 400; j++) {
          var el = all[j];
          if (el.children && el.children.length > 0) continue; // leaf text only
          if (!visible(el)) continue;
          var s = clamp(text(el));
          if (s && s.length >= 2 && s.length <= 60) return s;
        }
      }
    } catch (e) {}
    return "";
  }

  function fieldLabel(el) {
    try {
      var l = el.getAttribute("aria-label") || el.getAttribute("placeholder") || "";
      if (!l && el.id && document.querySelector) {
        var lab = document.querySelector('label[for="' + el.id + '"]');
        if (lab) l = text(lab);
      }
      if (!l) l = el.getAttribute("name") || "";
      return clamp(l);
    } catch (e) {
      return "";
    }
  }

  // controls — visible interactive labels, innermost-first.
  //
  // The selector deliberately includes `[tabindex]` and role-less RN-web
  // pressables, because on the screen that motivated this file the "İleri →"
  // button is a <div>, not a <button>. The innermost-wins pass below is what
  // stops a tabbable page wrapper from swallowing the whole screen as one
  // "control".
  function controls() {
    var out = [];
    try {
      var sel =
        'button,a[href],summary,select,[role="button"],[role="tab"],[role="link"],' +
        '[role="menuitem"],[role="switch"],[role="checkbox"],input[type="submit"],' +
        'input[type="button"],[tabindex]:not([tabindex="-1"])';
      var nodes = document.querySelectorAll(sel);
      var cands = [];
      for (var i = 0; i < nodes.length && i < 400; i++) {
        var el = nodes[i];
        if (!visible(el)) continue;
        var label = clamp(text(el)) || clamp(el.getAttribute("aria-label") || "") || clamp(el.getAttribute("title") || "");
        if (!label) continue;
        cands.push({ el: el, label: label });
      }
      // Innermost wins: drop any candidate that CONTAINS another candidate.
      for (var a = 0; a < cands.length; a++) {
        var swallows = false;
        for (var b = 0; b < cands.length; b++) {
          if (a === b) continue;
          if (cands[a].el.contains && cands[a].el.contains(cands[b].el)) {
            swallows = true;
            break;
          }
        }
        if (!swallows) out.push(cands[a].label);
      }

      // Fields: label / placeholder only. NEVER `.value`.
      var fields = document.querySelectorAll("input,textarea");
      for (var f = 0; f < fields.length && f < 60; f++) {
        var fl = fields[f];
        var ty = (fl.getAttribute("type") || "").toLowerCase();
        if (ty === "hidden" || ty === "submit" || ty === "button") continue;
        if (!visible(fl)) continue;
        var name = fieldLabel(fl);
        out.push(name ? "field: " + name : "field");
      }
    } catch (e) {}

    var seen = {};
    var dedup = [];
    for (var k = 0; k < out.length && dedup.length < MAX_CONTROLS; k++) {
      var key = out[k].toLowerCase();
      if (seen[key]) continue;
      seen[key] = 1;
      dedup.push(out[k]);
    }
    return dedup;
  }

  // component — a screen identifier, only when the app happens to publish one.
  // Never synthesised: a guessed component name would send the runner to a file
  // that does not exist, which is worse than sending it nowhere.
  function component() {
    try {
      var mount = document.getElementById("root") || document.getElementById("app") || document.body;
      if (!mount) return "";
      var el = mount.querySelector("[data-screen],[data-component],[data-testid]");
      if (!el) return "";
      return clamp(
        el.getAttribute("data-screen") || el.getAttribute("data-component") || el.getAttribute("data-testid") || ""
      );
    } catch (e) {
      return "";
    }
  }

  function collect() {
    return {
      route: route(),
      title: clamp(document.title || ""),
      heading: heading(),
      controls: controls(),
      component: component(),
      lane: window.ReactNativeWebView ? "webview" : "browser"
    };
  }

  var lastJSON = "";
  var lastPostAt = 0;
  var renderedPosted = false;

  // The RN-web host cannot inspect a cross-origin iframe, but this probe is
  // injected by the agent inside that iframe. Report paint from the operation
  // that actually matters — the phone's guest document — rather than asking a
  // second box-local browser and assuming its pixels match. Keep the predicate
  // aligned with the native ready probe: an SPA mount must have a committed
  // child; a bare Expo shell with an empty #root is not rendered.
  function maybePostRendered() {
    if (renderedPosted) return;
    try {
      var body = document.body;
      if (!body) return;
      var text = (body.innerText || "").trim();
      if (text.indexOf('"status":"starting"') >= 0 || text.indexOf("did not become ready") >= 0) return;
      // Go's default ServeMux 404 is body text, not application paint. The
      // independent mobile probe rejects it too; both producers must agree or
      // this injected probe can still emit a false "Yaver rendered" verdict.
      if (/^404 page not found$/i.test(text)) return;
      var mount = document.getElementById("root") || document.getElementById("app");
      var flutter = document.querySelector("flutter-view,flt-glass-pane,flt-scene-host");
      var ready = flutter || (mount ? mount.children.length > 0 : (body.children.length > 1 || text.length > 0));
      if (!ready) return;
      renderedPosted = true;
      var state = {
        // The scoped preview query carries authentication. The host needs the
        // route for diagnostics, never the credential-bearing query string.
        href: String((document.location && (document.location.pathname + document.location.hash)) || ""),
        reason: flutter ? "flutter_engine_attached" : (mount ? "mount_has_visible_content" : "plain_body_content"),
        mountId: mount ? (mount.id || "") : "",
        mountChildren: mount ? mount.children.length : -1,
        bodyChildren: body.children.length,
        bodyTextLen: text.length
      };
      var msg = { source: "yaver-preview", t: "yaver-rendered", state: state, ts: Date.now() };
      if (window.ReactNativeWebView && window.ReactNativeWebView.postMessage) {
        window.ReactNativeWebView.postMessage(JSON.stringify(msg));
      }
      if (window.parent && window.parent !== window) window.parent.postMessage(msg, "*");
    } catch (e) {}
  }

  function send() {
    try {
      maybePostRendered();
      var ctx = collect();
      if (!ctx.route && !ctx.title && !ctx.heading && (!ctx.controls || !ctx.controls.length)) return;
      var json = JSON.stringify(ctx);
      var now = Date.now();
      // Post on change, or on the heartbeat. Anything else is chatter on a
      // channel the user is paying relay bytes for.
      if (json === lastJSON && now - lastPostAt < HEARTBEAT_MS) return;
      lastJSON = json;
      lastPostAt = now;
      var msg = { source: "yaver-screen", t: "yaver-screen-context", v: 1, ctx: ctx };
      if (window.ReactNativeWebView && window.ReactNativeWebView.postMessage) {
        window.ReactNativeWebView.postMessage(JSON.stringify(msg));
      }
      // targetOrigin "*" is correct here and not a leak: the only recipient of a
      // frame's parent-post is its embedder, which by construction is the Yaver
      // surface that loaded this preview. The payload is app-authored UI text.
      if (window.parent && window.parent !== window) {
        window.parent.postMessage(msg, "*");
      }
    } catch (e) {}
  }

  function hook(name) {
    try {
      var orig = history[name];
      if (typeof orig !== "function") return;
      history[name] = function () {
        var r = orig.apply(this, arguments);
        setTimeout(send, 120);
        return r;
      };
    } catch (e) {}
  }

  try {
    hook("pushState");
    hook("replaceState");
    window.addEventListener("popstate", function () { setTimeout(send, 120); });
    window.addEventListener("hashchange", function () { setTimeout(send, 120); });
    // A client-side router swap paints no navigation event at all in some
    // frameworks, so the poll is the backstop. It only POSTS on change.
    setInterval(send, POLL_MS);
    setTimeout(send, 400);
    if (document.readyState === "complete" || document.readyState === "interactive") setTimeout(send, 50);
    else window.addEventListener("DOMContentLoaded", function () { setTimeout(send, 50); });
  } catch (e) {}
})();
