package main

import (
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
)

// The incident: Flutter's index.html ships <base href="/">, served under the
// /dev/ proxy, so flutter.js resolves to the agent root and 404s. These pin the
// rewrite that makes assets resolve through the proxy — the difference between
// "the app renders on the phone" and "the overlay waits forever".

func TestRewriteFlutterBaseHref(t *testing.T) {
	// The real shape Flutter emits (verified against e-mobile).
	in := `<!DOCTYPE html><html><head>
  <base href="/">
  <script src="flutter.js" defer></script></head>
  <body><picture id="splash"></picture></body></html>`
	out := rewriteDevIndexBaseHrefHTML(in)
	if !strings.Contains(out, `<base href="./">`) {
		t.Fatalf("base href not rewritten to ./:\n%s", out)
	}
	if strings.Contains(out, `<base href="/">`) {
		t.Fatal("the root base href must be gone — assets would still 404")
	}
	// A relative script tag must be left untouched: it now resolves under the
	// rewritten base, which is the whole point.
	if !strings.Contains(out, `src="flutter.js"`) {
		t.Fatal("relative asset paths must not be altered")
	}
}

func TestRewriteBaseHrefQuoteAndSpacingVariants(t *testing.T) {
	for _, in := range []string{
		`<base href="/">`,
		`<base href='/'>`,
		`<base   href = "/" >`,
		`<base href="">`, // Flutter's build sometimes emits an empty base
		`<BASE HREF="/">`,
	} {
		out := rewriteDevIndexBaseHrefHTML(in)
		if !strings.Contains(out, `"./"`) {
			t.Fatalf("variant not rewritten: %q -> %q", in, out)
		}
	}
}

func TestRewriteLeavesExplicitBaseAlone(t *testing.T) {
	// A dev server that already sets a real base must NOT be clobbered — that
	// would break a project that intentionally serves from a subpath.
	in := `<head><base href="/myapp/"><script src="a.js"></script></head>`
	out := rewriteDevIndexBaseHrefHTML(in)
	if out != in {
		t.Fatalf("explicit non-root base was altered:\n  in:  %s\n  out: %s", in, out)
	}
}

func TestRewriteNoBaseIsNoOp(t *testing.T) {
	// A page with no base and only RELATIVE assets is unchanged.
	in := `<html><head><script src="foo.js"></script></head></html>`
	if rewriteDevIndexBaseHrefHTML(in) != in {
		t.Fatal("a document with no base tag and only relative assets must be unchanged")
	}
	// But a root-absolute asset is made relative even without a base tag —
	// otherwise it 404s through the /dev/ proxy (the whole point).
	got := rewriteDevIndexBaseHrefHTML(`<script src="/foo.js"></script>`)
	if !strings.Contains(got, `src="foo.js"`) {
		t.Fatalf("root-absolute asset not made relative: %s", got)
	}
}

// The ModifyResponse hook must only touch HTML — a rewrite that corrupted a JS
// bundle or an image would be far worse than the bug it fixes.
func TestModifyResponseOnlyTouchesHTML(t *testing.T) {
	jsBody := `var x = '<base href="/">';` // a string that LOOKS like a base tag
	resp := &http.Response{
		Header: http.Header{"Content-Type": []string{"application/javascript"}},
		Body:   io.NopCloser(strings.NewReader(jsBody)),
	}
	if err := rewriteDevIndexBaseHref(resp); err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(resp.Body)
	if string(got) != jsBody {
		t.Fatalf("JS body was modified — the rewrite must be HTML-only:\n%s", got)
	}
	// The one header EVERY dev-proxied response must carry, bundles included:
	// Metro serves changed bundles under the same URL, so a cached 200 makes
	// every edit→reload cycle serve a stale preview that looks healthy
	// (measured 2026-07-27: direct fetch fresh, Chromium cache stale).
	if resp.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("dev-proxied response missing Cache-Control: no-store (got %q)", resp.Header.Get("Cache-Control"))
	}
}

func TestModifyResponseSetsNoStoreOnHTMLToo(t *testing.T) {
	resp := &http.Response{
		Header: http.Header{"Content-Type": []string{"text/html"}},
		Body:   io.NopCloser(strings.NewReader(`<head></head>`)),
	}
	if err := rewriteDevIndexBaseHref(resp); err != nil {
		t.Fatal(err)
	}
	if resp.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("HTML dev response missing Cache-Control: no-store (got %q)", resp.Header.Get("Cache-Control"))
	}
}

func TestModifyResponseRewritesHTMLBody(t *testing.T) {
	html := `<head><base href="/"><script src="flutter.js"></script></head>`
	resp := &http.Response{
		Header: http.Header{"Content-Type": []string{"text/html; charset=utf-8"}},
		Body:   io.NopCloser(strings.NewReader(html)),
	}
	if err := rewriteDevIndexBaseHref(resp); err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(got), `<base href="./">`) {
		t.Fatalf("HTML body was not rewritten:\n%s", got)
	}
	// Content-Length must match the new body or the client truncates/hangs.
	if resp.Header.Get("Content-Length") != strconv.Itoa(len(got)) {
		t.Fatalf("Content-Length %q does not match rewritten body length %d",
			resp.Header.Get("Content-Length"), len(got))
	}
}

func TestModifyResponseNilSafe(t *testing.T) {
	if err := rewriteDevIndexBaseHref(nil); err != nil {
		t.Fatal("nil response must be a no-op, not an error")
	}
	resp := &http.Response{Header: http.Header{"Content-Type": []string{"text/html"}}}
	if err := rewriteDevIndexBaseHref(resp); err != nil {
		t.Fatal("nil body must be a no-op")
	}
}

// Live Metro (Expo web) serves its entry as a ROOT-ABSOLUTE path, which ignores
// <base href> and 404s through the /dev/ proxy. Static `expo export` uses
// relative paths, which is why fixtures missed this. Pin the fix.
func TestRewriteMakesAbsoluteMetroBundleRelative(t *testing.T) {
	in := `<head><base href="/"><script src="/node_modules/expo/AppEntry.bundle?platform=web&dev=true"></script></head>`
	out := rewriteDevIndexBaseHrefHTML(in)
	if strings.Contains(out, `src="/node_modules`) {
		t.Fatalf("absolute bundle path not made relative — it will 404 through the proxy:\n%s", out)
	}
	if !strings.Contains(out, `src="node_modules/expo/AppEntry.bundle?platform=web&dev=true"`) {
		t.Fatalf("expected base-relative bundle path:\n%s", out)
	}
}

func TestRewriteLeavesProtocolRelativeAndAbsoluteURLsAlone(t *testing.T) {
	for _, in := range []string{
		`<script src="//cdn.example.com/x.js"></script>`,
		`<script src="https://cdn.example.com/x.js"></script>`,
		`<link href="https://fonts.example/x.css">`,
	} {
		if got := rewriteDevIndexBaseHrefHTML(in); got != in {
			t.Fatalf("external/protocol-relative URL was altered:\n  in:  %s\n  out: %s", in, got)
		}
	}
}

// The relay authenticates EVERY proxied request, and a browser cannot put
// ?token/&__rp on the sub-resource requests the HTML parser issues. The agent
// grew applyPreviewRelayAuth for exactly this — and it was never called from
// the proxy hook, so over the relay the page loaded and every script 401'd
// (measured live 2026-07-26: /d/<id>/dev-web/...entry.bundle → 401 while the
// page showed its empty #root). Pin the wiring, not just the helper.
func TestModifyResponsePropagatesAuthQueryOntoAssets(t *testing.T) {
	html := `<html><head></head><body><script src="node_modules/expo-router/entry.bundle?platform=web&dev=true"></script></body></html>`
	req, _ := http.NewRequest("GET", "http://127.0.0.1:18080/dev-web/?token=tok123&__rp=pw456", nil)
	resp := &http.Response{
		Header:  http.Header{"Content-Type": []string{"text/html; charset=utf-8"}},
		Body:    io.NopCloser(strings.NewReader(html)),
		Request: req,
	}
	if err := rewriteDevIndexBaseHref(resp); err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(resp.Body)
	s := string(got)
	if !strings.Contains(s, "entry.bundle?platform=web&dev=true&__rp=pw456&token=tok123") {
		t.Fatalf("static script src did not gain the page's auth query — over the relay it will 401:\n%s", s)
	}
	if !strings.Contains(s, "yaver-preview-auth-shim") {
		t.Fatalf("dynamic-loader auth shim was not injected:\n%s", s)
	}
}

// The shared relay validates and strips __rp before the request reaches the
// agent. In that live shape the agent sees only token=, while location.search
// in the browser still contains both. A static parser-created script must be
// deferred through the runtime URL helper or it requests with token alone and
// receives 401 before any app code can run.
func TestModifyResponseDefersStaticScriptWhenRelayPasswordWasStripped(t *testing.T) {
	html := `<html><head></head><body><script src="node_modules/expo-router/entry.bundle?platform=web&dev=true" defer></script></body></html>`
	req, _ := http.NewRequest("GET", "http://127.0.0.1:18080/dev-web/?token=tok123", nil)
	resp := &http.Response{
		Header:  http.Header{"Content-Type": []string{"text/html; charset=utf-8"}},
		Body:    io.NopCloser(strings.NewReader(html)),
		Request: req,
	}
	if err := rewriteDevIndexBaseHref(resp); err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(resp.Body)
	s := string(got)
	if !strings.Contains(s, `data-yaver="preview-static-loader"`) {
		t.Fatalf("parser-created entry script was not deferred through runtime relay auth:\n%s", s)
	}
	if !strings.Contains(s, "window.__yaverPreviewURL") {
		t.Fatalf("runtime URL helper was not exposed to the static loader:\n%s", s)
	}
	if strings.Contains(s, `<script src="node_modules/expo-router/entry.bundle`) {
		t.Fatalf("original parser-created script can still race the runtime auth shim:\n%s", s)
	}
	if !strings.Contains(s, `if(!url.searchParams.has(k))url.searchParams.set(k,v)`) {
		t.Fatalf("runtime shim does not add a missing __rp beside an existing token:\n%s", s)
	}
}

// No auth query on the page request → no auth material may appear in the
// output. The transport shim still has to pin dynamic requests to /dev/ after
// the guest router sees "/". LAN direct traffic is header-authenticated and
// must never gain query creds.
func TestModifyResponseLeavesUnauthedPagesWithoutAuth(t *testing.T) {
	html := `<html><head></head><body><script src="app.js"></script></body></html>`
	req, _ := http.NewRequest("GET", "http://127.0.0.1:18080/dev/", nil)
	resp := &http.Response{
		Header:  http.Header{"Content-Type": []string{"text/html; charset=utf-8"}},
		Body:    io.NopCloser(strings.NewReader(html)),
		Request: req,
	}
	if err := rewriteDevIndexBaseHref(resp); err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(resp.Body)
	s := string(got)
	if !strings.Contains(s, "yaver-preview-auth-shim") {
		t.Fatalf("transport shim missing on a page with no auth query:\n%s", s)
	}
	if strings.Contains(s, "tok123") || strings.Contains(s, "pw456") {
		t.Fatalf("auth material injected on a page with no auth query:\n%s", s)
	}
	if !strings.Contains(s, `src="app.js"`) {
		t.Fatalf("asset src was altered without an auth query to carry:\n%s", s)
	}
}

// Expo Router must see "/" or it renders its own Unmatched Route screen, so
// the preview bootstrap replaces /d/<device>/dev-web/ with "/" before the
// guest bundle starts. That visible-route rewrite must not also retarget
// Metro's later fetch/XHR/script requests to the relay root. On 2026-08-22 the
// phone therefore fetched https://public.yaver.io/node_modules/... and parsed
// the relay's HTML response as JavaScript (SyntaxError: Unexpected token '<').
//
// Pin both halves of the transport shim: it captures the scoped lane before
// history.replaceState changes location, then resolves root-relative AND
// ordinary relative dynamic resources against that captured lane.
func TestPreviewTransportShimKeepsDynamicRequestsInsideScopedLane(t *testing.T) {
	for _, want := range []string{
		`var lane=p.match(/^(.*\/dev(?:-web)?)(?:\/.*)?$/),base=lane?lane[1]+"/":"";`,
		`url=new URL(base+s.slice(1),location.origin);`,
		`url=new URL(s,base?new URL(base,location.origin):location.href);`,
	} {
		if !strings.Contains(previewAuthShimJS, want) {
			t.Fatalf("preview transport shim does not pin dynamic resources to the scoped lane; missing %q", want)
		}
	}
}

// Expo resolves a lazy import after the router bootstrap has changed the
// visible path to `/`. Its getDevServer() therefore turns /src/x.bundle into
// https://relay/src/x.bundle before fetch sees it. That absolute same-origin
// shape must be rebased just like a root-relative resource.
func TestPreviewTransportShimRebasesExpoSplitBundles(t *testing.T) {
	for _, want := range []string{
		`url.pathname.indexOf(base)!==0`,
		`/\.bundle$/i.test(url.pathname)`,
		`base+url.pathname.replace(/^\/+/,"")+url.search+url.hash`,
	} {
		if !strings.Contains(previewAuthShimJS, want) {
			t.Fatalf("preview transport shim does not rebase Expo split bundles; missing %q", want)
		}
	}
}

// Expo Web derives its HMR entry point from document.currentScript.src. The
// proxy mount is therefore present in the URL even though Metro must resolve
// the project-relative path. Measured live on Dogfood: first paint succeeded,
// then Metro received ./dev/node_modules/expo-router/entry and crashed, leaving
// the visible reload URL on HTTP 502. The early shim must sanitize only the
// register-entrypoints message before it crosses the HMR WebSocket.
func TestPreviewTransportShimStripsProxyMountFromExpoHMREntryPoint(t *testing.T) {
	for _, want := range []string{
		`var wp=window.WebSocket&&window.WebSocket.prototype;`,
		`m.type==="register-entrypoints"`,
		`u.pathname.indexOf(base)===0`,
		`u.pathname="/"+u.pathname.slice(base.length).replace(/^\/+/,"")`,
		`return os.call(this,d);`,
	} {
		if !strings.Contains(previewAuthShimJS, want) {
			t.Fatalf("preview transport shim does not normalize Expo HMR entry points; missing %q", want)
		}
	}
	if strings.Index(previewAuthShimJS, `var wp=window.WebSocket`) > strings.Index(previewAuthShimJS, `var of=window.fetch`) {
		t.Fatal("HMR WebSocket shim must be installed before the entry bundle can initialize Expo HMR")
	}
}

// Expo Router renders navigation controls as dynamically-created anchors.
// Rebasing fetch/XHR/scripts but not <a href> lets an authenticated preview
// mount successfully and then escape /d/<id>/dev/ (or /peer/<id>/dev/) on its
// first route change. The browser subsequently reloads the relay/agent root
// and shows a 404, which made Fast Reload look broken even though Metro stayed
// healthy. Keep anchors on the same scoped lane as every other resource.
func TestPreviewTransportShimRebasesDynamicAnchorNavigation(t *testing.T) {
	if !strings.Contains(previewAuthShimJS, `(n==="link"||n==="a")?"href"`) {
		t.Fatal("preview transport shim does not rebase dynamically-created anchor hrefs")
	}
}

func TestNormalizePreviewViewportForNativeHost(t *testing.T) {
	for _, in := range []string{
		`<html><head></head><body></body></html>`,
		`<html><head><meta content="width=980" name="viewport"></head></html>`,
		`<html><head><meta name='viewport' content='width=device-width'></head></html>`,
	} {
		got := normalizePreviewViewportHTML(in)
		if strings.Count(got, `name="viewport"`) != 1 {
			t.Fatalf("preview must have exactly one canonical viewport:\n%s", got)
		}
		for _, part := range []string{"width=device-width", "initial-scale=1", "maximum-scale=1", "user-scalable=no", "viewport-fit=cover"} {
			if !strings.Contains(got, part) {
				t.Fatalf("canonical viewport missing %q:\n%s", part, got)
			}
		}
	}
}
