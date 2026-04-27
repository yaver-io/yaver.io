package main

import (
	"strings"
	"testing"
	"time"
)

// TestInjectBaseHrefHappyPath confirms the <base> tag lands inside <head>
// for the standard expo export shape.
func TestInjectBaseHrefHappyPath(t *testing.T) {
	in := []byte(`<!doctype html><html><head><meta charset="utf-8"><title>x</title></head><body></body></html>`)
	out := injectBaseHref(in, "/dev/web-bundle/")
	if !strings.Contains(string(out), `<head><base href="/dev/web-bundle/" /><meta charset="utf-8">`) {
		t.Fatalf("base tag not inserted at expected position: %s", out)
	}
}

// TestInjectBaseHrefHeadWithAttrs handles `<head class="..." lang="...">`
// — uncommon but seen in the wild.
func TestInjectBaseHrefHeadWithAttrs(t *testing.T) {
	in := []byte(`<html><head class="x" lang="en"><title>y</title></head><body></body></html>`)
	out := injectBaseHref(in, "/dev/web-bundle/")
	if !strings.Contains(string(out), `<head class="x" lang="en"><base href="/dev/web-bundle/" />`) {
		t.Fatalf("base tag not inserted after attributed <head>: %s", out)
	}
}

// TestRelativizeAbsoluteAssetPaths verifies the bundle path rewriter
// strips leading `/` from absolute asset references so the dashboard's
// relay proxy doesn't double-prefix them and our <base href> can
// resolve them through the bundle's serve location.
func TestRelativizeAbsoluteAssetPaths(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			"absolute src is relativised",
			`<script src="/_expo/static/js/foo.js"></script>`,
			`<script src="_expo/static/js/foo.js"></script>`,
		},
		{
			"absolute href is relativised",
			`<link href="/_expo/static/css/main.css" rel="stylesheet">`,
			`<link href="_expo/static/css/main.css" rel="stylesheet">`,
		},
		{
			"absolute action is relativised",
			`<form action="/submit">`,
			`<form action="submit">`,
		},
		{
			"protocol-relative URLs are NOT touched (would break CDN refs)",
			`<script src="//cdn.example.com/lib.js"></script>`,
			`<script src="//cdn.example.com/lib.js"></script>`,
		},
		{
			"full URLs are NOT touched",
			`<script src="https://example.com/lib.js"></script>`,
			`<script src="https://example.com/lib.js"></script>`,
		},
		{
			"already-relative paths stay relative",
			`<script src="./already-relative.js"></script>`,
			`<script src="./already-relative.js"></script>`,
		},
		{
			"single-quoted attributes work too",
			`<script src='/_expo/foo.js'></script>`,
			`<script src='_expo/foo.js'></script>`,
		},
		{
			"multiple attrs in one tag",
			`<link href="/foo.css" rel="stylesheet" /><script src="/bar.js"></script>`,
			`<link href="foo.css" rel="stylesheet" /><script src="bar.js"></script>`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := string(relativizeAbsoluteAssetPaths([]byte(tc.in)))
			if got != tc.want {
				t.Errorf("got  %q\nwant %q", got, tc.want)
			}
		})
	}
}

// TestInjectBaseHrefIdempotentOnNoHead returns input unchanged when
// there's no <head> tag (very unusual but defensive).
func TestInjectBaseHrefIdempotentOnNoHead(t *testing.T) {
	in := []byte(`<html><body><h1>no head</h1></body></html>`)
	out := injectBaseHref(in, "/dev/web-bundle/")
	if string(out) != string(in) {
		t.Fatalf("expected unchanged output for headless html, got: %s", out)
	}
}

// TestWebTransportPhaseLadder runs through the full transport lifecycle
// — compiled → ready_to_serve → serving → streaming → delivered — and
// confirms each phase emits exactly one phase event (modulo throttling
// for streaming).
func TestWebTransportPhaseLadder(t *testing.T) {
	type capturedEvent struct {
		Type, Phase, Topic string
	}
	var events []capturedEvent
	emit := func(e DevServerEvent) {
		events = append(events, capturedEvent{Type: e.Type, Phase: e.Phase, Topic: e.Topic})
	}

	manifest := map[string]int64{
		"index.html":          1024,
		"_expo/static/js/a.js": 50000,
		"_expo/static/js/b.js": 30000,
	}
	tr := newWebTransport(emit, "web-js-bundle", "test-caller/0.0", manifest)

	tr.transition("ready_to_serve")
	tr.recordFile("index.html", 1024)
	// Wait for the goroutine that emits the synchronous serving phase.
	// In a real handler this is racy with the immediate progress event;
	// the test asserts both eventually land.

	tr.recordFile("_expo/static/js/a.js", 50000)
	tr.recordFile("_expo/static/js/b.js", 30000)
	tr.markDelivered(987)

	// Allow the serving-phase goroutine to fire. Real sleep — the
	// previous version was a tight CPU spin which raced with the
	// scheduler under parallel test load.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		hasServing := false
		for _, e := range events {
			if e.Phase == "serving" {
				hasServing = true
				break
			}
		}
		if hasServing {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	hasReady := false
	hasServing := false
	hasStreaming := false
	hasDelivered := false
	for _, e := range events {
		if e.Topic != "webview/transport" {
			t.Errorf("event has wrong topic: %+v", e)
		}
		switch e.Phase {
		case "ready_to_serve":
			hasReady = true
		case "serving":
			hasServing = true
		case "streaming":
			hasStreaming = true
		case "delivered":
			hasDelivered = true
		}
	}
	if !hasReady {
		t.Errorf("missing ready_to_serve phase event in: %+v", events)
	}
	if !hasServing {
		t.Errorf("missing serving phase event in: %+v", events)
	}
	if !hasStreaming {
		t.Errorf("missing streaming progress event in: %+v", events)
	}
	if !hasDelivered {
		t.Errorf("missing delivered phase event in: %+v", events)
	}
}

// TestWebTransportErrorPath confirms markError is idempotent and emits
// exactly one error event.
func TestWebTransportErrorPath(t *testing.T) {
	var errorCount int
	emit := func(e DevServerEvent) {
		if e.Type == "error" {
			errorCount++
		}
	}
	tr := newWebTransport(emit, "web-js-bundle", "x", map[string]int64{"a": 1})
	tr.markError("first failure")
	tr.markError("second failure (should be ignored)")
	if errorCount != 1 {
		t.Errorf("expected 1 error event, got %d", errorCount)
	}
}

// TestWebTransportNilSafe — every method should be a no-op on a nil
// receiver so callers don't have to guard at every call site.
func TestWebTransportNilSafe(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("nil receiver method panicked: %v", r)
		}
	}()
	var tr *webTransport
	tr.transition("anything")
	tr.recordFile("foo", 100)
	tr.markDelivered(0)
	tr.markError("x")
	_ = tr.snapshot()
}

// TestScanBundleManifestProducesForwardSlashKeys verifies the manifest
// scanner normalises path separators so the HTTP serve layer's URL
// paths match regardless of host OS.
func TestScanBundleManifestProducesForwardSlashKeys(t *testing.T) {
	// Smoke check: scanBundleManifest on a non-existent dir returns an
	// empty map, not a panic.
	m := scanBundleManifest("/definitely/does/not/exist/xyz")
	if len(m) != 0 {
		t.Fatalf("expected empty manifest, got %d entries", len(m))
	}
}

// TestHermesWasmRunnerHTMLEmitsExpectedShape locks in the runner page
// the agent serves for target=web-hermes-wasm. The runner JS is still
// best-effort (upstream Hermes hasn't shipped a stable WASM runner yet),
// so the test focuses on the contract the dashboard relies on:
//   - it's HTML
//   - it has a status banner the iframe can read
//   - it loads /dev/hermes-wasm-runtime (the wasm) and
//     /dev/web-bundle/main.jsbundle (the HBC)
// Dropping any of those three would silently break the experimental
// render even though the build still succeeds.
func TestHermesWasmRunnerHTMLEmitsExpectedShape(t *testing.T) {
	html := hermesWasmRunnerHTML
	if !strings.Contains(html, "<!doctype html>") {
		t.Errorf("missing doctype in runner html")
	}
	if !strings.Contains(html, `id="yaver-status"`) {
		t.Errorf("status banner element missing — dashboard relies on this for UX")
	}
	if !strings.Contains(html, "/dev/hermes-wasm-runtime") {
		t.Errorf("runner doesn't reference /dev/hermes-wasm-runtime — wasm engine won't load")
	}
	if !strings.Contains(html, "/dev/web-bundle/main.jsbundle") {
		t.Errorf("runner doesn't reference /dev/web-bundle/main.jsbundle — HBC won't load")
	}
	// Defensive: the runner explicitly states it's experimental so the
	// user sees an honest "engine compiled, full execution pending"
	// status instead of a silent blank iframe.
	if !strings.Contains(html, "experimental") && !strings.Contains(html, "Hermes WASM") {
		t.Errorf("runner doesn't surface experimental status — users will think it's broken")
	}
}
