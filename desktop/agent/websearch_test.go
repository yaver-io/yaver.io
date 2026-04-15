package main

import (
	"strings"
	"testing"
)

// TestParseDDGHTML_ExtractsResults verifies the DDG HTML scraper pulls
// title/url/snippet triples and unwraps the duckduckgo.com/l/?uddg=
// tracking redirect to the real destination URL. The fixture is the
// minimal markup html.duckduckgo.com returns; if DDG ships a layout
// change this test will go red and we'll patch the regexps.
func TestParseDDGHTML_ExtractsResults(t *testing.T) {
	html := `
<div class="result results_links result-default">
  <a class="result__a" href="//duckduckgo.com/l/?uddg=https%3A%2F%2Fexample.com%2Fa&rut=x">Example A</a>
  <a class="result__snippet" href="x">Snippet for example A</a>
</div>
<div class="result">
  <a class="result__a" href="//duckduckgo.com/l/?uddg=https%3A%2F%2Fexample.com%2Fb">Example B &amp; friends</a>
  <div class="result__snippet">Snippet B</div>
</div>
`
	got := parseDDGHTML(html, 10)
	if len(got) != 2 {
		t.Fatalf("want 2 results, got %d: %+v", len(got), got)
	}
	if got[0].URL != "https://example.com/a" {
		t.Errorf("uddg redirect not unwrapped: %q", got[0].URL)
	}
	if got[0].Snippet != "Snippet for example A" {
		t.Errorf("snippet[0]: %q", got[0].Snippet)
	}
	// HTML entity decode must run so the AI sees clean titles, not "&amp;".
	if !strings.Contains(got[1].Title, "&") || strings.Contains(got[1].Title, "amp;") {
		t.Errorf("title entities not decoded: %q", got[1].Title)
	}
}

// TestRunWebSearch_RejectsBadProvider locks the dispatcher's behavior
// for an unknown provider name so callers get a useful error instead
// of a silent fall-through to the default.
func TestRunWebSearch_RejectsBadProvider(t *testing.T) {
	if _, err := RunWebSearch("anything", "altavista", 5); err == nil {
		t.Fatal("expected error for unknown provider")
	}
}

// TestRunWebSearch_RejectsEmptyQuery — guard against an empty MCP call
// hitting the network with q="".
func TestRunWebSearch_RejectsEmptyQuery(t *testing.T) {
	if _, err := RunWebSearch("   ", "duckduckgo", 5); err == nil {
		t.Fatal("expected error for empty query")
	}
}

// TestPickAvailableProvider verifies the auto-selection priority:
// Google (paid) → Bing (paid) → DuckDuckGo (free fallback). The test
// drives via env vars so we don't depend on the host environment.
func TestPickAvailableProvider(t *testing.T) {
	t.Setenv("GOOGLE_CSE_KEY", "")
	t.Setenv("GOOGLE_CSE_CX", "")
	t.Setenv("BING_API_KEY", "")
	if p := pickAvailableProvider(); p != "duckduckgo" {
		t.Errorf("no creds → want duckduckgo, got %q", p)
	}
	t.Setenv("BING_API_KEY", "fake")
	if p := pickAvailableProvider(); p != "bing" {
		t.Errorf("bing creds → want bing, got %q", p)
	}
	t.Setenv("GOOGLE_CSE_KEY", "fake")
	t.Setenv("GOOGLE_CSE_CX", "fake")
	if p := pickAvailableProvider(); p != "google" {
		t.Errorf("google creds → want google, got %q", p)
	}
}
