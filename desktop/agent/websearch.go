package main

// websearch.go — provider-agnostic web search for Yaver. Exposed as the
// `web_search` MCP tool so any AI agent connected to Yaver (Claude Code,
// Codex, Aider, etc.) can ground its output in current information
// without each agent needing to ship its own search integration.
//
// Providers:
//   - "duckduckgo" (default) — free, no API key, scrapes the HTML
//     endpoint at html.duckduckgo.com. Good enough for most coding /
//     market-research queries; may rate-limit a single client to a few
//     RPS, which is fine for an interactive AI workflow.
//   - "google"  — Google Programmable Search Engine. Requires
//                 GOOGLE_CSE_KEY + GOOGLE_CSE_CX (engine id) env vars.
//   - "bing"    — Microsoft Bing Web Search v7. Requires BING_API_KEY.
//
// The provider list is intentionally easy to extend — add a new case to
// RunWebSearch and a dispatcher entry. No DI framework, no plugin
// system; this is one file and ~300 lines.

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"
)

// WebSearchResult is a single hit returned by any provider. Fields are
// the lowest-common-denominator across DDG / Google / Bing — anything
// provider-specific is dropped here so the AI consumer sees uniform
// output regardless of backend.
type WebSearchResult struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Snippet string `json:"snippet,omitempty"`
}

// WebSearchResponse is the shape returned to callers (CLI / MCP / HTTP).
type WebSearchResponse struct {
	Provider string            `json:"provider"`
	Query    string            `json:"query"`
	Results  []WebSearchResult `json:"results"`
}

// RunWebSearch dispatches to the chosen provider. provider="" or "auto"
// picks the first provider whose credentials are available, falling
// back to DuckDuckGo (no creds needed) so the tool always works.
func RunWebSearch(query, provider string, limit int) (*WebSearchResponse, error) {
	q := strings.TrimSpace(query)
	if q == "" {
		return nil, fmt.Errorf("query is required")
	}
	if limit <= 0 || limit > 25 {
		limit = 10
	}

	p := strings.ToLower(strings.TrimSpace(provider))
	if p == "" || p == "auto" {
		p = pickAvailableProvider()
	}

	switch p {
	case "duckduckgo", "ddg":
		results, err := searchDuckDuckGo(q, limit)
		return &WebSearchResponse{Provider: "duckduckgo", Query: q, Results: results}, err
	case "google":
		results, err := searchGoogle(q, limit)
		return &WebSearchResponse{Provider: "google", Query: q, Results: results}, err
	case "bing":
		results, err := searchBing(q, limit)
		return &WebSearchResponse{Provider: "bing", Query: q, Results: results}, err
	default:
		return nil, fmt.Errorf("unknown web-search provider: %q (want duckduckgo|google|bing|auto)", provider)
	}
}

// pickAvailableProvider returns the best provider whose creds are set,
// preferring Google → Bing → DuckDuckGo. The order is "if you paid for
// it, use it; otherwise fall back to the free one".
func pickAvailableProvider() string {
	if os.Getenv("GOOGLE_CSE_KEY") != "" && os.Getenv("GOOGLE_CSE_CX") != "" {
		return "google"
	}
	if os.Getenv("BING_API_KEY") != "" {
		return "bing"
	}
	return "duckduckgo"
}

// searchDuckDuckGo hits the html.duckduckgo.com endpoint and parses
// results out of the rendered HTML. There is no public DDG search API;
// the HTML endpoint is the documented anonymous interface and is what
// every other "free DDG search" library targets.
func searchDuckDuckGo(query string, limit int) ([]WebSearchResult, error) {
	endpoint := "https://html.duckduckgo.com/html/?q=" + url.QueryEscape(query)
	req, _ := http.NewRequest("GET", endpoint, nil)
	// Browser-like UA — DDG returns an empty body to default Go UA.
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 14_0) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.0 Safari/605.1.15")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("duckduckgo: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("duckduckgo: status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("duckduckgo read: %w", err)
	}
	return parseDDGHTML(string(body), limit), nil
}

// parseDDGHTML extracts (title, href, snippet) triples from the
// html.duckduckgo.com markup. Two-pass: split on <div class="result …">
// to get per-result blocks, then pull link + snippet inside each block.
// More robust than one mega-regex because DDG occasionally reorders the
// title/snippet pair or wraps either one in different tags.
var (
	ddgLinkRe    = regexp.MustCompile(`(?s)<a[^>]+class="result__a"[^>]+href="([^"]+)"[^>]*>(.*?)</a>`)
	ddgSnippetRe = regexp.MustCompile(`(?s)class="result__snippet"[^>]*>(.*?)</(?:a|div)>`)
)

func parseDDGHTML(html string, limit int) []WebSearchResult {
	// Walk every result__a anchor; for each match, look ahead in a
	// bounded window for the next result__snippet block (which may be
	// either an <a> or a <div>). Bounded window prevents one result's
	// snippet from being claimed by the previous result on malformed
	// pages where DDG omits a snippet entirely.
	const lookahead = 2048
	matches := ddgLinkRe.FindAllStringSubmatchIndex(html, -1)
	var out []WebSearchResult
	seen := map[string]bool{}
	for _, idx := range matches {
		if len(out) >= limit {
			break
		}
		rawURL := html[idx[2]:idx[3]]
		title := html[idx[4]:idx[5]]
		if u := extractDDGRedirect(rawURL); u != "" {
			rawURL = u
		}
		if rawURL == "" || seen[rawURL] {
			continue
		}
		seen[rawURL] = true

		end := idx[1] + lookahead
		if end > len(html) {
			end = len(html)
		}
		window := html[idx[1]:end]
		snippet := ""
		if sm := ddgSnippetRe.FindStringSubmatch(window); len(sm) >= 2 {
			snippet = sm[1]
		}
		out = append(out, WebSearchResult{
			Title:   stripTags(title),
			URL:     rawURL,
			Snippet: stripTags(snippet),
		})
	}
	return out
}

// extractDDGRedirect unwraps DDG's tracking redirect:
//   //duckduckgo.com/l/?uddg=https%3A%2F%2Fexample.com%2Fpath&rut=…
// → https://example.com/path
func extractDDGRedirect(href string) string {
	if !strings.Contains(href, "duckduckgo.com/l/") {
		return ""
	}
	if !strings.HasPrefix(href, "http") {
		href = "https:" + href
	}
	u, err := url.Parse(href)
	if err != nil {
		return ""
	}
	if v := u.Query().Get("uddg"); v != "" {
		if dec, err := url.QueryUnescape(v); err == nil {
			return dec
		}
	}
	return ""
}

var tagRe = regexp.MustCompile(`<[^>]+>`)

func stripTags(s string) string {
	s = tagRe.ReplaceAllString(s, "")
	s = strings.ReplaceAll(s, "&amp;", "&")
	s = strings.ReplaceAll(s, "&quot;", `"`)
	s = strings.ReplaceAll(s, "&#39;", "'")
	s = strings.ReplaceAll(s, "&lt;", "<")
	s = strings.ReplaceAll(s, "&gt;", ">")
	return strings.TrimSpace(s)
}

// searchGoogle uses Google Programmable Search Engine (Custom Search
// JSON API). Free tier is 100 queries/day; paid is $5/1000 queries.
// The AI runner picks Google when the user has paid for higher recall
// or Indonesian/Turkish/etc. queries that DDG handles less well.
func searchGoogle(query string, limit int) ([]WebSearchResult, error) {
	key := os.Getenv("GOOGLE_CSE_KEY")
	cx := os.Getenv("GOOGLE_CSE_CX")
	if key == "" || cx == "" {
		return nil, fmt.Errorf("google: set GOOGLE_CSE_KEY and GOOGLE_CSE_CX env vars (Programmable Search Engine)")
	}
	if limit > 10 {
		limit = 10 // CSE max per request
	}
	endpoint := fmt.Sprintf("https://www.googleapis.com/customsearch/v1?key=%s&cx=%s&q=%s&num=%d",
		url.QueryEscape(key), url.QueryEscape(cx), url.QueryEscape(query), limit)

	resp, err := (&http.Client{Timeout: 15 * time.Second}).Get(endpoint)
	if err != nil {
		return nil, fmt.Errorf("google: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("google: status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var raw struct {
		Items []struct {
			Title, Link, Snippet string
		} `json:"items"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("google decode: %w", err)
	}
	var out []WebSearchResult
	for _, it := range raw.Items {
		out = append(out, WebSearchResult{Title: it.Title, URL: it.Link, Snippet: it.Snippet})
	}
	return out, nil
}

// searchBing uses Bing Web Search v7. As of 2024 Bing also requires a
// Microsoft Azure subscription; the API key is an Azure resource key.
func searchBing(query string, limit int) ([]WebSearchResult, error) {
	key := os.Getenv("BING_API_KEY")
	if key == "" {
		return nil, fmt.Errorf("bing: set BING_API_KEY env var (Azure Bing Search v7 resource key)")
	}
	if limit > 50 {
		limit = 50
	}
	endpoint := fmt.Sprintf("https://api.bing.microsoft.com/v7.0/search?q=%s&count=%d", url.QueryEscape(query), limit)
	req, _ := http.NewRequest("GET", endpoint, nil)
	req.Header.Set("Ocp-Apim-Subscription-Key", key)

	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		return nil, fmt.Errorf("bing: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("bing: status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var raw struct {
		WebPages struct {
			Value []struct {
				Name, URL, Snippet string
			} `json:"value"`
		} `json:"webPages"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("bing decode: %w", err)
	}
	var out []WebSearchResult
	for _, it := range raw.WebPages.Value {
		out = append(out, WebSearchResult{Title: it.Name, URL: it.URL, Snippet: it.Snippet})
	}
	return out, nil
}
