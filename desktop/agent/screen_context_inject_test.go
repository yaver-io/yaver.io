package main

import (
	"os"
	"strings"
	"testing"
)

// readRepoFile reads a file from this package's own directory. Tests here read
// SOURCE (not a copy) so an anti-drift assertion cannot itself drift.
func readRepoFile(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(name)
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return string(b)
}

const sampleIndexHTML = `<!doctype html><html><head><title>sfmg</title></head>` +
	`<body><div id="root"></div><script src="/_expo/static/js/web/entry.js" defer></script></body></html>`

func TestInjectScreenContextProbe_LandsBeforeBodyClose(t *testing.T) {
	got := injectScreenContextProbe(sampleIndexHTML)
	if !strings.Contains(got, screenProbeMarker) {
		t.Fatal("probe was not injected")
	}
	probeAt := strings.Index(got, screenProbeMarker)
	bodyAt := strings.LastIndex(strings.ToLower(got), "</body>")
	if probeAt < 0 || bodyAt < 0 || probeAt > bodyAt {
		t.Fatalf("probe is not inside <body> (probe=%d bodyClose=%d)", probeAt, bodyAt)
	}
	// The entry bundle must still be ahead of the probe: the probe reads
	// RENDERED dom, so it has to run after the app's script tag is in the
	// document, not before it.
	if strings.Index(got, "entry.js") > probeAt {
		t.Error("probe was placed before the app bundle")
	}
}

func TestInjectScreenContextProbe_IsIdempotent(t *testing.T) {
	once := injectScreenContextProbe(sampleIndexHTML)
	twice := injectScreenContextProbe(once)
	if once != twice {
		t.Fatal("second injection changed the document — two probes would double every post")
	}
	if n := strings.Count(twice, screenProbeMarker); n != 1 {
		t.Fatalf("expected exactly 1 probe, found %d", n)
	}
}

func TestInjectScreenContextProbe_LeavesNonHTMLAlone(t *testing.T) {
	// The proxy hands this function whatever came back with a text/html content
	// type. A JSON error body or a JS chunk must survive byte-for-byte — a
	// preview missing its probe is recoverable, a corrupted bundle is not.
	for _, in := range []string{
		``,
		`{"status":"starting","detail":"metro is booting"}`,
		`export default function App(){return null}`,
		`just some text`,
	} {
		if got := injectScreenContextProbe(in); got != in {
			t.Errorf("non-HTML input was modified.\n in: %q\nout: %q", in, got)
		}
	}
}

func TestInjectScreenContextProbe_FallsBackToHeadThenAppend(t *testing.T) {
	head := `<html><head><title>x</title></head>`
	got := injectScreenContextProbe(head)
	if !strings.Contains(got, screenProbeMarker) {
		t.Fatal("no injection when </body> is absent")
	}
	if strings.Index(got, screenProbeMarker) > strings.LastIndex(strings.ToLower(got), "</head>") {
		t.Error("probe was not placed before </head> in the fallback path")
	}
}

// TestScreenProbe_BothPreviewLanesInjectIt is the anti-drift guard.
//
// The repo has shipped a broken heartbeat, dropped SSE frames and a dead shake
// gesture by fixing ONE of two implementations of the same surface. There are
// exactly two HTML preview lanes; this asserts the call exists in both. Prove it
// by deleting either call and watching this fail.
func TestScreenProbe_BothPreviewLanesInjectIt(t *testing.T) {
	for _, lane := range []struct{ file, why string }{
		{"build_web.go", "static Expo/RN web bundle — the lane the sfmg incident happened on"},
		{"devserver_basehref.go", "live dev-server reverse proxy — Vite / Next.js / Flutter web"},
	} {
		src := readRepoFile(t, lane.file)
		if !strings.Contains(src, "injectScreenContextProbe(") {
			t.Errorf("%s never calls injectScreenContextProbe — %s has no screen context, so a prompt from that preview still sends the runner grepping", lane.file, lane.why)
		}
	}
}

// stripJSLineComments removes `//` comments so the contract assertions below
// test CODE rather than prose. Without this the guard trips on its own
// documentation — the probe's comments say "NEVER read .value", which is the
// opposite of a violation.
func stripJSLineComments(js string) string {
	var b strings.Builder
	for _, line := range strings.Split(js, "\n") {
		if i := strings.Index(line, "//"); i >= 0 {
			line = line[:i]
		}
		b.WriteString(line)
		b.WriteString("\n")
	}
	return b.String()
}

func TestScreenProbeJS_ContractsThatMustHold(t *testing.T) {
	js := stripJSLineComments(screenContextProbeJS)
	if strings.TrimSpace(js) == "" {
		t.Fatal("embedded probe is empty")
	}
	// It must NEVER read an input's value. On the very screen that motivated
	// this feature the user is typing their own name into the field.
	if strings.Contains(js, ".value") {
		t.Error("probe reads .value — user-entered text must never be captured")
	}
	// It must not reach the network itself; it reports to its host surface,
	// which forwards over an authenticated channel.
	for _, banned := range []string{"XMLHttpRequest", "fetch(", "navigator.sendBeacon"} {
		if strings.Contains(js, banned) {
			t.Errorf("probe uses %s — it must post to its host surface, never write to the agent directly", banned)
		}
	}
	// Both host surfaces must be addressed, or one lane goes silent.
	for _, need := range []string{"ReactNativeWebView", "window.parent"} {
		if !strings.Contains(js, need) {
			t.Errorf("probe never posts to %s", need)
		}
	}
	// The query string carries a bundle `sig` token; the route must be built
	// from pathname + hash only.
	if strings.Contains(js, "location.search") {
		t.Error("probe includes location.search — the preview URL carries an auth token")
	}
	if strings.Contains(js, "location.href") {
		t.Error("probe includes location.href — the preview URL carries an auth token")
	}
}

func TestScreenProbeJS_PostsClientPaintWithoutAcceptingEmptyExpoRoot(t *testing.T) {
	js := stripJSLineComments(screenContextProbeJS)
	for _, required := range []string{
		`t: "yaver-rendered"`,
		`mount ? mount.children.length > 0`,
		`window.parent.postMessage(msg, "*")`,
	} {
		if !strings.Contains(js, required) {
			t.Fatalf("cross-origin client-paint bridge missing %q", required)
		}
	}
	if strings.Contains(js, `mount ? true`) {
		t.Fatal("an empty Expo #root must not count as rendered")
	}
	if !strings.Contains(js, `^404 page not found$`) {
		t.Fatal("the independent screen probe must reject the agent's bare 404 document")
	}
}

func TestScreenProbeTag_IsWellFormed(t *testing.T) {
	tag := screenContextProbeTag()
	if !strings.HasPrefix(tag, "<script") || !strings.HasSuffix(tag, "</script>") {
		t.Fatal("probe tag is not a script element")
	}
	// A literal "</script>" inside the JS would terminate the tag early and
	// spill JavaScript into the page as text.
	if strings.Count(tag, "</script>") != 1 {
		t.Fatal("probe body contains a </script> sequence — it would break out of its own tag")
	}
}
