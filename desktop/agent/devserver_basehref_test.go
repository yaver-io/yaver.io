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
	if !strings.Contains(out, `<base href="/dev/">`) {
		t.Fatalf("base href not rewritten to /dev/:\n%s", out)
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
		if !strings.Contains(out, `/dev/`) {
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
	in := `<html><head><script src="/foo.js"></script></head></html>`
	if rewriteDevIndexBaseHrefHTML(in) != in {
		t.Fatal("a document with no base tag must be returned unchanged")
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
	if !strings.Contains(string(got), `<base href="/dev/">`) {
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
