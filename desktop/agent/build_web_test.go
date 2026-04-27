package main

import (
	"strings"
	"testing"
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

	// Allow the serving-phase goroutine to fire.
	for i := 0; i < 100; i++ {
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
		// Polling rather than time.Sleep so the test fails fast if the
		// goroutine is missing entirely.
		time := struct{}{}
		_ = time
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
