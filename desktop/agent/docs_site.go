package main

// docs_site.go — serve a markdown folder as a static docs site.
// Replaces Mintlify / Gitbook for the solo dev who keeps docs in
// their repo already.
//
// Layout on disk (defaults to ~/.yaver/docs/ but any path can be
// pointed at via config):
//
//   docs/
//   ├── index.md
//   ├── getting-started.md
//   ├── api/
//   │   ├── index.md
//   │   └── auth.md
//   └── _config.yaml   (optional — title, theme, logo URL)
//
// Public endpoints:
//
//   GET  /docs                       — serves index.md
//   GET  /docs/<slug>                — serves <slug>.md
//   GET  /docs/<folder>/<slug>       — nested
//   GET  /docs/_search?q=...         — simple in-memory search
//   GET  /docs/_json                 — the full tree + metadata
//
// No external Go deps — markdown rendering is intentionally a
// tiny parser for the subset solo devs actually use:
// headings, code blocks, links, lists, bold/italic. Good enough
// for a changelog + API reference; the dev keeps their real
// writing in the repo.
//
// Owner endpoints:
//
//   POST /docs/config    { path, title, theme }  — point at the docs folder
//   GET  /docs/config    — return current config

import (
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
)

// DocsConfig controls where the docs site pulls from + branding.
type DocsConfig struct {
	Path    string `json:"path"`
	Title   string `json:"title,omitempty"`
	Theme   string `json:"theme,omitempty"`   // "light" | "dark"
	LogoURL string `json:"logoUrl,omitempty"`
}

var (
	docsMu     sync.Mutex
	docsCfg    *DocsConfig
	docsIndex  map[string]string // slug → absolute path
	docsTree   []docsNode
	docsCache  map[string]string // slug → rendered HTML
)

type docsNode struct {
	Slug     string     `json:"slug"`
	Title    string     `json:"title"`
	Children []docsNode `json:"children,omitempty"`
}

func docsConfigFile() (string, error) {
	dir, err := ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "docs-config.json"), nil
}

func loadDocsConfig() *DocsConfig {
	docsMu.Lock()
	defer docsMu.Unlock()
	if docsCfg != nil {
		return docsCfg
	}
	p, _ := docsConfigFile()
	data, err := os.ReadFile(p)
	if err != nil {
		dir, _ := ConfigDir()
		docsCfg = &DocsConfig{Path: filepath.Join(dir, "docs"), Title: "Docs", Theme: "light"}
		return docsCfg
	}
	var cfg DocsConfig
	_ = json.Unmarshal(data, &cfg)
	docsCfg = &cfg
	return docsCfg
}

func saveDocsConfig(cfg *DocsConfig) error {
	p, _ := docsConfigFile()
	data, _ := json.MarshalIndent(cfg, "", "  ")
	if err := os.WriteFile(p, data, 0o600); err != nil {
		return err
	}
	docsMu.Lock()
	docsCfg = cfg
	docsIndex = nil
	docsTree = nil
	docsCache = nil
	docsMu.Unlock()
	return nil
}

// scanDocs walks the docs folder, populating the slug index
// and the sidebar tree. Cheap — docs folders are tiny.
func scanDocs() {
	cfg := loadDocsConfig()
	index := map[string]string{}
	var tree []docsNode
	root := cfg.Path
	if root == "" {
		return
	}
	_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".md") && !strings.HasSuffix(path, ".markdown") {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		slug := strings.TrimSuffix(rel, filepath.Ext(rel))
		slug = filepath.ToSlash(slug)
		if slug == "index" {
			slug = ""
		}
		index[slug] = path
		return nil
	})

	// Build the sidebar tree from the slug list.
	slugs := make([]string, 0, len(index))
	for k := range index {
		slugs = append(slugs, k)
	}
	sort.Strings(slugs)
	for _, s := range slugs {
		tree = append(tree, docsNode{Slug: s, Title: prettyTitle(s, index[s])})
	}

	docsMu.Lock()
	docsIndex = index
	docsTree = tree
	docsCache = map[string]string{}
	docsMu.Unlock()
}

// prettyTitle tries to read the first # heading of a markdown
// file, falls back to the slug.
func prettyTitle(slug, path string) string {
	data, err := os.ReadFile(path)
	if err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			if strings.HasPrefix(line, "# ") {
				return strings.TrimSpace(strings.TrimPrefix(line, "# "))
			}
		}
	}
	if slug == "" {
		return "Home"
	}
	parts := strings.Split(slug, "/")
	name := parts[len(parts)-1]
	return strings.Title(strings.ReplaceAll(name, "-", " "))
}

// --- tiny markdown renderer ------------------------------------------------

// renderMarkdown converts a small markdown subset to HTML. Not a
// full CommonMark implementation — it handles what docs pages
// actually use (headings, code fences, inline code, bold/italic,
// links, unordered lists). Anything else gets html-escaped and
// emitted as a paragraph.
func renderMarkdown(src string) string {
	var b strings.Builder
	lines := strings.Split(src, "\n")
	inCode := false
	inList := false

	flushList := func() {
		if inList {
			b.WriteString("</ul>\n")
			inList = false
		}
	}

	for _, raw := range lines {
		line := raw

		// Code fence.
		if strings.HasPrefix(line, "```") {
			if inCode {
				b.WriteString("</code></pre>\n")
				inCode = false
			} else {
				flushList()
				lang := strings.TrimPrefix(line, "```")
				_ = lang
				b.WriteString("<pre><code>")
				inCode = true
			}
			continue
		}
		if inCode {
			b.WriteString(html.EscapeString(line))
			b.WriteString("\n")
			continue
		}

		// Heading.
		if strings.HasPrefix(line, "# ") {
			flushList()
			fmt.Fprintf(&b, "<h1>%s</h1>\n", html.EscapeString(strings.TrimPrefix(line, "# ")))
			continue
		}
		if strings.HasPrefix(line, "## ") {
			flushList()
			fmt.Fprintf(&b, "<h2>%s</h2>\n", html.EscapeString(strings.TrimPrefix(line, "## ")))
			continue
		}
		if strings.HasPrefix(line, "### ") {
			flushList()
			fmt.Fprintf(&b, "<h3>%s</h3>\n", html.EscapeString(strings.TrimPrefix(line, "### ")))
			continue
		}

		// List.
		if strings.HasPrefix(line, "- ") || strings.HasPrefix(line, "* ") {
			if !inList {
				b.WriteString("<ul>\n")
				inList = true
			}
			b.WriteString("<li>")
			b.WriteString(renderInline(strings.TrimSpace(line[2:])))
			b.WriteString("</li>\n")
			continue
		}

		if strings.TrimSpace(line) == "" {
			flushList()
			continue
		}

		flushList()
		b.WriteString("<p>")
		b.WriteString(renderInline(line))
		b.WriteString("</p>\n")
	}
	flushList()
	if inCode {
		b.WriteString("</code></pre>\n")
	}
	return b.String()
}

var (
	reBold   = regexp.MustCompile(`\*\*([^*]+)\*\*`)
	reItalic = regexp.MustCompile(`\*([^*]+)\*`)
	reCode   = regexp.MustCompile("`([^`]+)`")
	reLink   = regexp.MustCompile(`\[([^\]]+)\]\(([^)]+)\)`)
)

func renderInline(s string) string {
	s = html.EscapeString(s)
	s = reCode.ReplaceAllString(s, "<code>$1</code>")
	s = reLink.ReplaceAllString(s, `<a href="$2">$1</a>`)
	s = reBold.ReplaceAllString(s, "<strong>$1</strong>")
	s = reItalic.ReplaceAllString(s, "<em>$1</em>")
	return s
}

// --- HTTP ------------------------------------------------------------------

func (s *HTTPServer) handleDocsSite(w http.ResponseWriter, r *http.Request) {
	cfg := loadDocsConfig()
	if docsIndex == nil {
		scanDocs()
	}

	slug := strings.TrimPrefix(r.URL.Path, "/docs")
	slug = strings.TrimPrefix(slug, "/")
	// Special sub-routes.
	switch slug {
	case "_search":
		s.handleDocsSearch(w, r)
		return
	case "_json":
		jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true, "tree": docsTree, "config": cfg})
		return
	case "config":
		s.handleDocsConfig(w, r)
		return
	}

	path, ok := docsIndex[slug]
	if !ok {
		// Empty slug → try index
		if slug == "" {
			if p, ok2 := docsIndex[""]; ok2 {
				path = p
			} else {
				http.NotFound(w, r)
				return
			}
		} else {
			http.NotFound(w, r)
			return
		}
	}

	docsMu.Lock()
	cached, hit := docsCache[slug]
	docsMu.Unlock()
	var htmlBody string
	if hit {
		htmlBody = cached
	} else {
		data, err := os.ReadFile(path)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		htmlBody = renderMarkdown(string(data))
		docsMu.Lock()
		docsCache[slug] = htmlBody
		docsMu.Unlock()
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, renderDocsShell(cfg, slug, htmlBody))
}

// renderDocsShell wraps the rendered markdown in a minimal
// sidebar layout. Intentionally stylesheet-free HTML with
// inline CSS so the docs site has zero asset dependencies.
func renderDocsShell(cfg *DocsConfig, activeSlug, body string) string {
	var nav strings.Builder
	for _, node := range docsTree {
		active := ""
		if node.Slug == activeSlug {
			active = ` style="font-weight:700"`
		}
		slugPath := "/docs"
		if node.Slug != "" {
			slugPath += "/" + node.Slug
		}
		fmt.Fprintf(&nav, `<li><a href="%s"%s>%s</a></li>`, slugPath, active, html.EscapeString(node.Title))
	}
	theme := cfg.Theme
	bg := "#fff"
	fg := "#111"
	sidebarBg := "#fafafa"
	if theme == "dark" {
		bg = "#0b0b0b"
		fg = "#eee"
		sidebarBg = "#111"
	}
	title := cfg.Title
	if title == "" {
		title = "Docs"
	}
	return fmt.Sprintf(`<!doctype html><html><head><meta charset="utf-8">
<title>%s</title>
<meta name="viewport" content="width=device-width,initial-scale=1">
<style>
body { margin: 0; font-family: system-ui, sans-serif; background: %s; color: %s; display: grid; grid-template-columns: 260px 1fr; min-height: 100vh; }
aside { background: %s; border-right: 1px solid #e5e5e5; padding: 24px 16px; }
aside h2 { margin: 0 0 16px; font-size: 14px; text-transform: uppercase; color: #888; }
aside ul { list-style: none; padding: 0; margin: 0; }
aside li { padding: 6px 0; }
aside a { color: inherit; text-decoration: none; }
main { padding: 48px 64px; max-width: 760px; }
pre { background: #0b0b0b; color: #eee; padding: 16px; border-radius: 8px; overflow-x: auto; }
code { font-family: "SF Mono", Menlo, monospace; font-size: 13px; }
h1 { margin-top: 0; }
a { color: #4F46E5; }
@media (max-width: 720px) { body { grid-template-columns: 1fr; } aside { border-right: 0; border-bottom: 1px solid #e5e5e5; } main { padding: 24px; } }
</style></head>
<body>
<aside><h2>%s</h2><ul>%s</ul></aside>
<main>%s</main>
</body></html>`, html.EscapeString(title), bg, fg, sidebarBg, html.EscapeString(title), nav.String(), body)
}

// handleDocsSearch does a lazy substring match over all known
// docs — good enough for small doc sites. The expensive inverted
// index lives in search.go; if the dev puts thousands of doc
// pages we'll switch over, but today substring is fine.
func (s *HTTPServer) handleDocsSearch(w http.ResponseWriter, r *http.Request) {
	q := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
	if q == "" {
		jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true, "hits": []interface{}{}})
		return
	}
	hits := []map[string]interface{}{}
	for slug, path := range docsIndex {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		lower := strings.ToLower(string(data))
		if idx := strings.Index(lower, q); idx >= 0 {
			start := idx - 40
			end := idx + 80
			if start < 0 {
				start = 0
			}
			if end > len(lower) {
				end = len(lower)
			}
			hits = append(hits, map[string]interface{}{
				"slug":    slug,
				"title":   prettyTitle(slug, path),
				"snippet": "..." + lower[start:end] + "...",
			})
		}
	}
	jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true, "hits": hits})
}

func (s *HTTPServer) handleDocsConfig(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true, "config": loadDocsConfig()})
	case http.MethodPost:
		var cfg DocsConfig
		if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		if err := saveDocsConfig(&cfg); err != nil {
			jsonError(w, http.StatusInternalServerError, err.Error())
			return
		}
		scanDocs()
		jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true, "config": &cfg})
	default:
		jsonError(w, http.StatusMethodNotAllowed, "use GET/POST")
	}
}
