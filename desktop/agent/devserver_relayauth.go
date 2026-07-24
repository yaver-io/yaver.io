package main

// devserver_relayauth.go — carry the preview's auth onto its SUB-RESOURCES.
//
// ── The bug, measured against the live public relay (2026-07-24) ─────────────
//
//	GET /d/<device>/dev/?__rp=<pw>      -> 200   the WebView's first load
//	GET /d/<device>/dev/flutter.js      -> 401   every asset that page needs
//	GET /d/<device>/dev/flutter.js?__rp -> 200
//
// The relay authenticates every proxied request. A browser cannot comply for
// sub-resources: relative URLs do not inherit a query string, and a WebView
// cannot set a header. So the document loads and everything it needs 401s —
// Flutter's engine never boots, Metro's bundle never arrives, and the preview
// overlay waits forever on a page that can never paint. This broke the browser
// lane over relay for EVERY framework, not just Flutter.
//
// The clean fix is an HttpOnly cookie minted by the relay (relay/webview_cookie.go,
// already written and tested). It cannot ship yet — the release is blocked on a
// tag-protection ruleset — and this is agent-side, so it works TODAY against the
// relay as deployed.
//
// ── What this does ───────────────────────────────────────────────────────────
//
// When the proxied page was itself loaded with an auth query, the agent:
//
//  1. rewrites STATIC relative src/href in the HTML to carry that same query.
//     The HTML parser creates those elements directly, so no runtime hook can
//     reach them.
//  2. injects a small shim that patches fetch / XMLHttpRequest / createElement
//     so DYNAMIC loads carry it too. Flutter fetches main.dart.js and canvaskit
//     from inside flutter.js at runtime; without this they still 401.
//
// ── Security ─────────────────────────────────────────────────────────────────
//
// This adds NO exposure. The page is already loaded at a URL containing the
// query, so `location.search` hands the same value to page JS with or without
// this shim — which is exactly why the shim can read it from location rather
// than having the agent bake a credential into the HTML. Nothing secret is
// written into the markup; the shim only propagates what the browser already
// has, and only to SAME-ORIGIN requests.
//
// The relay cookie remains the better answer precisely because HttpOnly removes
// that pre-existing exposure. When it ships, the query can drop out of the URL
// entirely and this becomes a no-op (no auth query -> nothing injected).

import (
	"net/url"
	"regexp"
	"strings"
)

// relayAuthQueryKeys are the params that authenticate a proxied preview request.
// `__rp` is the relay password; `token` is the agent bearer promoted from query.
var relayAuthQueryKeys = []string{"__rp", "token"}

// extractPreviewAuthQuery returns the auth-carrying params of a request query,
// in a stable order, or "" when there are none. Only these keys are propagated —
// never the whole query, so a page's own params are not duplicated onto assets.
func extractPreviewAuthQuery(rawQuery string) string {
	if strings.TrimSpace(rawQuery) == "" {
		return ""
	}
	vals, err := url.ParseQuery(rawQuery)
	if err != nil {
		return ""
	}
	out := url.Values{}
	for _, k := range relayAuthQueryKeys {
		if v := vals.Get(k); v != "" {
			out.Set(k, v)
		}
	}
	if len(out) == 0 {
		return ""
	}
	return out.Encode()
}

// staticAssetAttrRe matches a relative src=/href= in served HTML. Absolute URLs
// (scheme, //host) and data:/blob:/# are left alone — only same-document
// relative references need the auth appended.
var staticAssetAttrRe = regexp.MustCompile(`(?i)\b(src|href)\s*=\s*"([^"]+)"`)

// appendAuthToStaticAssets rewrites relative src/href values to carry authQuery.
func appendAuthToStaticAssets(html, authQuery string) string {
	if authQuery == "" {
		return html
	}
	return staticAssetAttrRe.ReplaceAllStringFunc(html, func(m string) string {
		sub := staticAssetAttrRe.FindStringSubmatch(m)
		if len(sub) != 3 {
			return m
		}
		attr, val := sub[1], sub[2]
		if !isRelativeAssetRef(val) {
			return m
		}
		sep := "?"
		if strings.Contains(val, "?") {
			sep = "&"
		}
		return attr + `="` + val + sep + authQuery + `"`
	})
}

// isRelativeAssetRef reports whether a src/href value is a same-origin relative
// reference worth rewriting.
func isRelativeAssetRef(v string) bool {
	v = strings.TrimSpace(v)
	if v == "" {
		return false
	}
	lower := strings.ToLower(v)
	for _, skip := range []string{"http://", "https://", "//", "data:", "blob:", "mailto:", "javascript:", "#", "about:"} {
		if strings.HasPrefix(lower, skip) {
			return false
		}
	}
	// Already carries auth (e.g. a second pass) — leave it.
	for _, k := range relayAuthQueryKeys {
		if strings.Contains(v, k+"=") {
			return false
		}
	}
	return true
}

// previewAuthShimJS propagates the page's own auth query onto same-origin
// dynamic requests. It reads location.search at runtime, so no credential is
// ever written into the HTML the agent serves.
const previewAuthShimJS = `<script>(function(){try{
var raw=location.search;if(!raw||raw.length<2)return;
var src=new URLSearchParams(raw),keep=new URLSearchParams();
["__rp","token"].forEach(function(k){var v=src.get(k);if(v)keep.set(k,v);});
var q=keep.toString();if(!q)return;
function A(u){try{
 if(u==null)return u;
 var s=String(u);
 if(/^(data:|blob:|about:|javascript:|#)/i.test(s))return u;
 var url=new URL(s,location.href);
 if(url.origin!==location.origin)return u;
 if(url.searchParams.has("__rp")||url.searchParams.has("token"))return u;
 keep.forEach(function(v,k){url.searchParams.set(k,v);});
 return url.toString();
}catch(e){return u;}}
var of=window.fetch;
if(of)window.fetch=function(i,init){try{
 if(typeof i==="string")return of(A(i),init);
 if(i&&i.url)return of(new Request(A(i.url),i),init);
}catch(e){}return of(i,init);};
var xo=XMLHttpRequest.prototype.open;
XMLHttpRequest.prototype.open=function(m,u){try{arguments[1]=A(u);}catch(e){}return xo.apply(this,arguments);};
var ce=document.createElement.bind(document);
document.createElement=function(t){var el=ce(t);try{
 var n=String(t).toLowerCase(),a=(n==="link")?"href":(n==="script"||n==="img")?"src":null;
 if(a){Object.defineProperty(el,a,{configurable:true,
  set:function(v){el.setAttribute(a,A(v));},get:function(){return el.getAttribute(a);}});}
}catch(e){}return el;};
}catch(e){}})();</script>`

// injectPreviewAuthShim places the shim as early as possible — right after
// <head> — so it is installed before any dynamic loader runs.
func injectPreviewAuthShim(html, authQuery string) string {
	if authQuery == "" || strings.Contains(html, "yaver-preview-auth-shim") {
		return html
	}
	marked := strings.Replace(previewAuthShimJS, "<script>", `<script data-yaver="yaver-preview-auth-shim">`, 1)
	lower := strings.ToLower(html)
	if i := strings.Index(lower, "<head>"); i >= 0 {
		at := i + len("<head>")
		return html[:at] + marked + html[at:]
	}
	if i := strings.Index(lower, "<html"); i >= 0 {
		if j := strings.IndexByte(html[i:], '>'); j >= 0 {
			at := i + j + 1
			return html[:at] + marked + html[at:]
		}
	}
	return marked + html
}

// applyPreviewRelayAuth is the whole transform: static rewrite + runtime shim.
func applyPreviewRelayAuth(html, rawQuery string) string {
	authQuery := extractPreviewAuthQuery(rawQuery)
	if authQuery == "" {
		return html
	}
	return injectPreviewAuthShim(appendAuthToStaticAssets(html, authQuery), authQuery)
}
