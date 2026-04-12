package main

import (
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// SEOIssue represents a single SEO problem found on a page.
type SEOIssue struct {
	Type        string // error | warning | info
	Category    string // meta | images | headings | sitemap | schema | speed | mobile
	Page        string
	Message     string
	Fix         string
	AutoFixable bool
}

// SEOPageScore holds the audit result for a single page.
type SEOPageScore struct {
	Page            string
	Score           int
	Issues          []SEOIssue
	MetaTitle       string
	MetaDescription string
	HasCanonical    bool
	HasOG           bool
	HasSchema       bool
}

// SEOReport is the full site-wide SEO audit output.
type SEOReport struct {
	OverallScore     int
	PageScores       []SEOPageScore
	TotalIssues      int
	Errors           int
	Warnings         int
	Infos            int
	SitemapPresent   bool
	RobotsTxtPresent bool
	AuditedAt        time.Time
}

// SEOManager manages SEO auditing and fixing for a workspace.
type SEOManager struct {
	mu      sync.Mutex
	workDir string
	siteDir string
}

// NewSEOManager creates a new SEOManager for the given workspace directory.
func NewSEOManager(workDir string) *SEOManager {
	return &SEOManager{
		workDir: workDir,
		siteDir: detectBuildDir(workDir),
	}
}

// Audit performs a full SEO audit of the build output.
func (m *SEOManager) Audit() (*SEOReport, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	siteDir := m.siteDir
	if siteDir == "" {
		return nil, fmt.Errorf("no build output directory found (expected dist/, .next/, out/, or build/)")
	}

	htmlFiles, err := scanHTMLFiles(siteDir)
	if err != nil {
		return nil, fmt.Errorf("scanning HTML files: %w", err)
	}

	report := &SEOReport{
		AuditedAt: time.Now(),
	}

	for _, path := range htmlFiles {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		content := string(data)
		rel, _ := filepath.Rel(siteDir, path)
		page := "/" + filepath.ToSlash(rel)
		if page == "/index.html" {
			page = "/"
		} else {
			page = strings.TrimSuffix(page, "/index.html")
			page = strings.TrimSuffix(page, ".html")
		}

		ps := auditPage(page, content)
		report.PageScores = append(report.PageScores, ps)

		for _, issue := range ps.Issues {
			report.TotalIssues++
			switch issue.Type {
			case "error":
				report.Errors++
			case "warning":
				report.Warnings++
			case "info":
				report.Infos++
			}
		}
	}

	// Root-level checks
	sitemapPath := filepath.Join(m.workDir, "public", "sitemap.xml")
	if _, err := os.Stat(sitemapPath); err != nil {
		sitemapPath = filepath.Join(siteDir, "sitemap.xml")
	}
	if _, err := os.Stat(sitemapPath); err == nil {
		report.SitemapPresent = true
	}

	robotsPath := filepath.Join(m.workDir, "public", "robots.txt")
	if _, err := os.Stat(robotsPath); err != nil {
		robotsPath = filepath.Join(siteDir, "robots.txt")
	}
	if _, err := os.Stat(robotsPath); err == nil {
		report.RobotsTxtPresent = true
	}

	// Overall score: average of page scores, penalised for missing sitemap/robots
	if len(report.PageScores) > 0 {
		total := 0
		for _, ps := range report.PageScores {
			total += ps.Score
		}
		report.OverallScore = total / len(report.PageScores)
	} else {
		report.OverallScore = 100
	}
	if !report.SitemapPresent {
		report.OverallScore -= 5
	}
	if !report.RobotsTxtPresent {
		report.OverallScore -= 3
	}
	if report.OverallScore < 0 {
		report.OverallScore = 0
	}

	return report, nil
}

// auditPage checks a single HTML page and returns its score.
func auditPage(page, content string) SEOPageScore {
	metas := parseHTMLHead(content)
	var issues []SEOIssue

	// Meta title
	title := metas["title"]
	if title == "" {
		issues = append(issues, SEOIssue{
			Type: "error", Category: "meta", Page: page,
			Message:     "Missing meta title",
			Fix:         "Add a <title> tag between 30 and 60 characters",
			AutoFixable: true,
		})
	} else {
		n := utf8.RuneCountInString(title)
		if n < 30 {
			issues = append(issues, SEOIssue{
				Type: "warning", Category: "meta", Page: page,
				Message:     fmt.Sprintf("Meta title too short (%d chars, min 30)", n),
				Fix:         "Expand the title to at least 30 characters",
				AutoFixable: false,
			})
		} else if n > 60 {
			issues = append(issues, SEOIssue{
				Type: "warning", Category: "meta", Page: page,
				Message:     fmt.Sprintf("Meta title too long (%d chars, max 60)", n),
				Fix:         "Shorten the title to at most 60 characters",
				AutoFixable: false,
			})
		}
	}

	// Meta description
	desc := metas["description"]
	if desc == "" {
		issues = append(issues, SEOIssue{
			Type: "error", Category: "meta", Page: page,
			Message:     "Missing meta description",
			Fix:         "Add <meta name=\"description\"> between 120 and 160 characters",
			AutoFixable: true,
		})
	} else {
		n := utf8.RuneCountInString(desc)
		if n < 120 {
			issues = append(issues, SEOIssue{
				Type: "warning", Category: "meta", Page: page,
				Message:     fmt.Sprintf("Meta description too short (%d chars, min 120)", n),
				Fix:         "Expand the description to at least 120 characters",
				AutoFixable: false,
			})
		} else if n > 160 {
			issues = append(issues, SEOIssue{
				Type: "warning", Category: "meta", Page: page,
				Message:     fmt.Sprintf("Meta description too long (%d chars, max 160)", n),
				Fix:         "Shorten the description to at most 160 characters",
				AutoFixable: false,
			})
		}
	}

	// H1 check
	h1Re := regexp.MustCompile(`(?i)<h1[\s>]`)
	h1Matches := h1Re.FindAllString(content, -1)
	if len(h1Matches) == 0 {
		issues = append(issues, SEOIssue{
			Type: "error", Category: "headings", Page: page,
			Message:     "No H1 heading found",
			Fix:         "Add exactly one <h1> tag per page",
			AutoFixable: false,
		})
	} else if len(h1Matches) > 1 {
		issues = append(issues, SEOIssue{
			Type: "warning", Category: "headings", Page: page,
			Message:     fmt.Sprintf("Multiple H1 headings (%d found, expected 1)", len(h1Matches)),
			Fix:         "Keep only one <h1> per page",
			AutoFixable: false,
		})
	}

	// Images without alt text
	imgRe := regexp.MustCompile(`(?i)<img[^>]+>`)
	altRe := regexp.MustCompile(`(?i)\balt\s*=`)
	imgs := imgRe.FindAllString(content, -1)
	missingAlt := 0
	for _, img := range imgs {
		if !altRe.MatchString(img) {
			missingAlt++
		}
	}
	if missingAlt > 0 {
		issues = append(issues, SEOIssue{
			Type: "error", Category: "images", Page: page,
			Message:     fmt.Sprintf("%d image(s) missing alt text", missingAlt),
			Fix:         "Add descriptive alt attributes to all <img> tags",
			AutoFixable: true,
		})
	}

	// Canonical URL
	hasCanonical := strings.Contains(strings.ToLower(content), `rel="canonical"`) ||
		strings.Contains(strings.ToLower(content), `rel='canonical'`)
	if !hasCanonical {
		issues = append(issues, SEOIssue{
			Type: "warning", Category: "meta", Page: page,
			Message:     "Missing canonical URL",
			Fix:         "Add <link rel=\"canonical\" href=\"...\"> in the <head>",
			AutoFixable: true,
		})
	}

	// OpenGraph tags
	hasOGTitle := metas["og:title"] != ""
	hasOGDesc := metas["og:description"] != ""
	hasOGImage := metas["og:image"] != ""
	hasOG := hasOGTitle && hasOGDesc && hasOGImage
	if !hasOGTitle {
		issues = append(issues, SEOIssue{
			Type: "warning", Category: "meta", Page: page,
			Message:     "Missing og:title",
			Fix:         "Add <meta property=\"og:title\" content=\"...\">",
			AutoFixable: true,
		})
	}
	if !hasOGDesc {
		issues = append(issues, SEOIssue{
			Type: "warning", Category: "meta", Page: page,
			Message:     "Missing og:description",
			Fix:         "Add <meta property=\"og:description\" content=\"...\">",
			AutoFixable: true,
		})
	}
	if !hasOGImage {
		issues = append(issues, SEOIssue{
			Type: "warning", Category: "meta", Page: page,
			Message:     "Missing og:image",
			Fix:         "Add <meta property=\"og:image\" content=\"...\">",
			AutoFixable: true,
		})
	}

	// Twitter Card
	if metas["twitter:card"] == "" {
		issues = append(issues, SEOIssue{
			Type: "info", Category: "meta", Page: page,
			Message:     "Missing Twitter Card tags",
			Fix:         "Add <meta name=\"twitter:card\" content=\"summary_large_image\">",
			AutoFixable: true,
		})
	}

	// Page size check
	sizeKB := len(content) / 1024
	if sizeKB > 3072 {
		issues = append(issues, SEOIssue{
			Type: "warning", Category: "speed", Page: page,
			Message:     fmt.Sprintf("Page size is large (%d KB, warn threshold 3072 KB)", sizeKB),
			Fix:         "Split the page or lazy-load heavy resources",
			AutoFixable: false,
		})
	}

	// Structured data (JSON-LD)
	hasSchema := strings.Contains(content, `application/ld+json`)

	if !hasSchema {
		issues = append(issues, SEOIssue{
			Type: "info", Category: "schema", Page: page,
			Message:     "No structured data (JSON-LD) found",
			Fix:         "Add JSON-LD schema markup appropriate for this page",
			AutoFixable: true,
		})
	}

	return SEOPageScore{
		Page:            page,
		Score:           calculatePageScore(issues),
		Issues:          issues,
		MetaTitle:       title,
		MetaDescription: desc,
		HasCanonical:    hasCanonical,
		HasOG:           hasOG,
		HasSchema:       hasSchema,
	}
}

// Fix auto-fixes SEO issues in the build output.
// what: "all", "meta", "images", "sitemap", "schema", "robots"
func (m *SEOManager) Fix(what string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	var summary []string
	apply := func(category string) bool {
		return what == "all" || what == category
	}

	siteDir := m.siteDir
	if siteDir == "" {
		return "", fmt.Errorf("no build output directory found")
	}

	if apply("meta") || apply("images") || apply("schema") {
		htmlFiles, err := scanHTMLFiles(siteDir)
		if err != nil {
			return "", err
		}
		for _, path := range htmlFiles {
			data, err := os.ReadFile(path)
			if err != nil {
				continue
			}
			content := string(data)
			changed := false

			if apply("meta") {
				if !strings.Contains(strings.ToLower(content), "<title") {
					content = injectMetaTag(content, "title", "Untitled Page")
					changed = true
				}
				if !strings.Contains(content, `name="description"`) && !strings.Contains(content, `name='description'`) {
					content = injectMetaTag(content, "description", "Description for this page.")
					changed = true
				}
				if !strings.Contains(strings.ToLower(content), `rel="canonical"`) {
					content = injectMetaTag(content, "canonical", "")
					changed = true
				}
			}

			if apply("images") {
				imgRe := regexp.MustCompile(`(?i)(<img)([^>]*?)(/?>)`)
				altRe := regexp.MustCompile(`(?i)\balt\s*=`)
				srcRe := regexp.MustCompile(`(?i)\bsrc\s*=\s*["']([^"']+)["']`)
				content = imgRe.ReplaceAllStringFunc(content, func(tag string) string {
					if altRe.MatchString(tag) {
						return tag
					}
					altText := "image"
					if m := srcRe.FindStringSubmatch(tag); len(m) > 1 {
						base := filepath.Base(m[1])
						base = strings.TrimSuffix(base, filepath.Ext(base))
						base = regexp.MustCompile(`[-_]+`).ReplaceAllString(base, " ")
						if base != "" {
							altText = base
						}
					}
					// Insert alt before closing bracket
					return imgRe.ReplaceAllString(tag, `$1$2 alt="`+altText+`"$3`)
				})
				changed = true
			}

			if apply("schema") && !strings.Contains(content, `application/ld+json`) {
				jsonld := generateJSONLD("organization", map[string]string{
					"name": "My Organization",
					"url":  "https://example.com",
				})
				content = injectJSONLD(content, jsonld)
				changed = true
			}

			if changed {
				if err := os.WriteFile(path, []byte(content), 0644); err != nil {
					return "", fmt.Errorf("writing %s: %w", path, err)
				}
				rel, _ := filepath.Rel(siteDir, path)
				summary = append(summary, "fixed: "+rel)
			}
		}
	}

	if apply("sitemap") {
		xml, err := m.generateSitemapXMLFromDir()
		if err != nil {
			return "", err
		}
		dest := filepath.Join(siteDir, "sitemap.xml")
		if err := os.WriteFile(dest, []byte(xml), 0644); err != nil {
			return "", fmt.Errorf("writing sitemap.xml: %w", err)
		}
		summary = append(summary, "generated sitemap.xml")
	}

	if apply("robots") {
		txt, err := m.generateRobotsTxtContent("")
		if err != nil {
			return "", err
		}
		dest := filepath.Join(m.workDir, "public", "robots.txt")
		_ = os.MkdirAll(filepath.Dir(dest), 0755)
		if err := os.WriteFile(dest, []byte(txt), 0644); err != nil {
			return "", fmt.Errorf("writing robots.txt: %w", err)
		}
		summary = append(summary, "generated robots.txt")
	}

	if len(summary) == 0 {
		return "No fixable issues found.", nil
	}
	return strings.Join(summary, "\n"), nil
}

// Meta sets meta tags for a specific page file.
func (m *SEOManager) Meta(page, title, description, keywords, ogImage string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	path, err := m.resolvePagePath(page)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("reading %s: %w", path, err)
	}
	content := string(data)

	if title != "" {
		// Replace existing <title> or inject new one
		titleRe := regexp.MustCompile(`(?i)<title>[^<]*</title>`)
		if titleRe.MatchString(content) {
			content = titleRe.ReplaceAllString(content, "<title>"+title+"</title>")
		} else {
			content = injectMetaTag(content, "title", title)
		}
	}
	if description != "" {
		content = injectMetaTag(content, "description", description)
	}
	if keywords != "" {
		content = injectMetaTag(content, "keywords", keywords)
	}
	if ogImage != "" {
		content = injectMetaTag(content, "og:image", ogImage)
	}

	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		return "", fmt.Errorf("writing %s: %w", path, err)
	}
	return fmt.Sprintf("Updated meta tags for %s", page), nil
}

// Sitemap generates sitemap.xml from the build output.
func (m *SEOManager) Sitemap() (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	xml, err := m.generateSitemapXMLFromDir()
	if err != nil {
		return "", err
	}
	dest := filepath.Join(m.siteDir, "sitemap.xml")
	if err := os.WriteFile(dest, []byte(xml), 0644); err != nil {
		return "", fmt.Errorf("writing sitemap.xml: %w", err)
	}
	return fmt.Sprintf("sitemap.xml written to %s", dest), nil
}

// Schema adds JSON-LD structured data to a page.
// schemaType: "article", "product", "faq", "organization", "local-business", "breadcrumb"
func (m *SEOManager) Schema(page, schemaType string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	path, err := m.resolvePagePath(page)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("reading %s: %w", path, err)
	}
	content := string(data)

	jsonld := generateJSONLD(schemaType, map[string]string{
		"name": "My Site",
		"url":  "https://example.com",
	})
	content = injectJSONLD(content, jsonld)

	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		return "", fmt.Errorf("writing %s: %w", path, err)
	}
	return fmt.Sprintf("Added %s schema to %s", schemaType, page), nil
}

// Report returns a formatted human-readable SEO report.
func (m *SEOManager) Report() (string, error) {
	report, err := m.Audit()
	if err != nil {
		return "", err
	}

	var sb strings.Builder
	sb.WriteString("SEO Report\n")
	sb.WriteString("══════════════════════════════\n")
	sb.WriteString(fmt.Sprintf("  Overall Score: %d/100\n\n", report.OverallScore))
	sb.WriteString("  Pages:\n")

	for _, ps := range report.PageScores {
		label := ps.Page
		if label == "/" {
			label = "/ (index)"
		}
		// Count page-level errors/warnings
		errs, warns := 0, 0
		for _, iss := range ps.Issues {
			switch iss.Type {
			case "error":
				errs++
			case "warning":
				warns++
			}
		}
		status := "✓"
		if errs > 0 || warns > 0 {
			parts := []string{}
			if errs > 0 {
				parts = append(parts, fmt.Sprintf("%d error", errs))
				if errs > 1 {
					parts[len(parts)-1] += "s"
				}
			}
			if warns > 0 {
				parts = append(parts, fmt.Sprintf("%d warning", warns))
				if warns > 1 {
					parts[len(parts)-1] += "s"
				}
			}
			status = strings.Join(parts, ", ")
		}
		sb.WriteString(fmt.Sprintf("    %-24s %d/100  %s\n", label, ps.Score, status))
	}

	sb.WriteString("\n  Issues:\n")
	for _, ps := range report.PageScores {
		for _, iss := range ps.Issues {
			prefix := "✓"
			switch iss.Type {
			case "error":
				prefix = "✗"
			case "warning":
				prefix = "!"
			}
			sb.WriteString(fmt.Sprintf("    %s %s: %s\n", prefix, iss.Page, iss.Message))
		}
	}

	if report.SitemapPresent {
		sb.WriteString("    ✓ sitemap.xml present\n")
	} else {
		sb.WriteString("    ✗ sitemap.xml missing\n")
	}
	if report.RobotsTxtPresent {
		sb.WriteString("    ✓ robots.txt present\n")
	} else {
		sb.WriteString("    ✗ robots.txt missing\n")
	}

	// Aggregate image alt status
	totalMissingAlt := 0
	for _, ps := range report.PageScores {
		for _, iss := range ps.Issues {
			if iss.Category == "images" && iss.Type == "error" {
				totalMissingAlt++
			}
		}
	}
	if totalMissingAlt == 0 {
		sb.WriteString("    ✓ All images have alt text\n")
	} else {
		sb.WriteString(fmt.Sprintf("    ✗ %d page(s) have images missing alt text\n", totalMissingAlt))
	}

	// Schema summary
	schemaCount := 0
	for _, ps := range report.PageScores {
		if ps.HasSchema {
			schemaCount++
		}
	}
	sb.WriteString(fmt.Sprintf("    ✓ Structured data on %d page(s)\n", schemaCount))

	return sb.String(), nil
}

// RobotsTxt generates and writes robots.txt.
func (m *SEOManager) RobotsTxt(customRules string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	content, err := m.generateRobotsTxtContent(customRules)
	if err != nil {
		return "", err
	}
	dest := filepath.Join(m.workDir, "public", "robots.txt")
	_ = os.MkdirAll(filepath.Dir(dest), 0755)
	if err := os.WriteFile(dest, []byte(content), 0644); err != nil {
		return "", fmt.Errorf("writing robots.txt: %w", err)
	}
	return fmt.Sprintf("robots.txt written to %s", dest), nil
}

// ── Internal helpers ──────────────────────────────────────────────────────────

func scanHTMLFiles(dir string) ([]string, error) {
	var files []string
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // skip unreadable entries
		}
		if !info.IsDir() && strings.EqualFold(filepath.Ext(path), ".html") {
			files = append(files, path)
		}
		return nil
	})
	return files, err
}

// parseHTMLHead extracts meta tag values from an HTML document.
// Recognised keys: "title", "description", "keywords", "og:title", "og:description",
// "og:image", "twitter:card", plus any property/name meta content.
func parseHTMLHead(content string) map[string]string {
	result := make(map[string]string)

	// <title>...</title>
	titleRe := regexp.MustCompile(`(?i)<title[^>]*>([^<]*)</title>`)
	if m := titleRe.FindStringSubmatch(content); len(m) > 1 {
		result["title"] = strings.TrimSpace(m[1])
	}

	// <meta name="..." content="...">
	metaNameRe := regexp.MustCompile(`(?i)<meta[^>]+name\s*=\s*["']([^"']+)["'][^>]*content\s*=\s*["']([^"']*)["'][^>]*>|<meta[^>]+content\s*=\s*["']([^"']*)["'][^>]*name\s*=\s*["']([^"']+)["'][^>]*>`)
	for _, m := range metaNameRe.FindAllStringSubmatch(content, -1) {
		if m[1] != "" {
			result[strings.ToLower(m[1])] = m[2]
		} else if m[4] != "" {
			result[strings.ToLower(m[4])] = m[3]
		}
	}

	// <meta property="og:..." content="...">
	metaPropRe := regexp.MustCompile(`(?i)<meta[^>]+property\s*=\s*["']([^"']+)["'][^>]*content\s*=\s*["']([^"']*)["'][^>]*>|<meta[^>]+content\s*=\s*["']([^"']*)["'][^>]*property\s*=\s*["']([^"']+)["'][^>]*>`)
	for _, m := range metaPropRe.FindAllStringSubmatch(content, -1) {
		if m[1] != "" {
			result[strings.ToLower(m[1])] = m[2]
		} else if m[4] != "" {
			result[strings.ToLower(m[4])] = m[3]
		}
	}

	return result
}

// injectMetaTag adds or updates a meta tag in the HTML <head>.
func injectMetaTag(content, name, value string) string {
	var tag string
	switch strings.ToLower(name) {
	case "title":
		tag = fmt.Sprintf("<title>%s</title>", value)
	case "canonical":
		tag = `<link rel="canonical" href="">`
	default:
		if strings.HasPrefix(name, "og:") || strings.HasPrefix(name, "twitter:") {
			attr := "property"
			if strings.HasPrefix(name, "twitter:") {
				attr = "name"
			}
			tag = fmt.Sprintf(`<meta %s="%s" content="%s">`, attr, name, value)
		} else {
			tag = fmt.Sprintf(`<meta name="%s" content="%s">`, name, value)
		}
	}

	// Remove existing tag first (best-effort)
	if name == "title" {
		old := regexp.MustCompile(`(?i)<title>[^<]*</title>`)
		content = old.ReplaceAllString(content, "")
	} else {
		// Remove existing meta with same name/property
		old := regexp.MustCompile(`(?i)<meta\s[^>]*(name|property)\s*=\s*["']` + regexp.QuoteMeta(name) + `["'][^>]*>`)
		content = old.ReplaceAllString(content, "")
	}

	// Inject before </head>
	headCloseRe := regexp.MustCompile(`(?i)</head>`)
	if headCloseRe.MatchString(content) {
		return headCloseRe.ReplaceAllString(content, "  "+tag+"\n</head>")
	}
	// Fallback: prepend
	return tag + "\n" + content
}

// injectJSONLD inserts a JSON-LD <script> block before </head>.
func injectJSONLD(content, jsonld string) string {
	script := fmt.Sprintf(`<script type="application/ld+json">%s</script>`, jsonld)
	headCloseRe := regexp.MustCompile(`(?i)</head>`)
	if headCloseRe.MatchString(content) {
		return headCloseRe.ReplaceAllString(content, "  "+script+"\n</head>")
	}
	return content + "\n" + script
}

type sitemapURL struct {
	Loc     string `xml:"loc"`
	LastMod string `xml:"lastmod,omitempty"`
}

type sitemapURLSet struct {
	XMLName xml.Name     `xml:"urlset"`
	XMLNS   string       `xml:"xmlns,attr"`
	URLs    []sitemapURL `xml:"url"`
}

func generateSitemapXML(pages []string, domain string) string {
	urlset := sitemapURLSet{
		XMLNS: "http://www.sitemaps.org/schemas/sitemap/0.9",
	}
	now := time.Now().Format("2006-01-02")
	for _, p := range pages {
		loc := strings.TrimRight(domain, "/") + p
		urlset.URLs = append(urlset.URLs, sitemapURL{Loc: loc, LastMod: now})
	}
	out, err := xml.MarshalIndent(urlset, "", "  ")
	if err != nil {
		return ""
	}
	return xml.Header + string(out)
}

func generateJSONLD(schemaType string, data map[string]string) string {
	name := data["name"]
	if name == "" {
		name = "My Site"
	}
	url := data["url"]
	if url == "" {
		url = "https://example.com"
	}

	switch schemaType {
	case "article":
		return fmt.Sprintf(`{"@context":"https://schema.org","@type":"Article","headline":"%s","url":"%s","datePublished":"%s"}`,
			name, url, time.Now().Format("2006-01-02"))
	case "product":
		return fmt.Sprintf(`{"@context":"https://schema.org","@type":"Product","name":"%s","url":"%s"}`, name, url)
	case "faq":
		return `{"@context":"https://schema.org","@type":"FAQPage","mainEntity":[]}`
	case "local-business":
		return fmt.Sprintf(`{"@context":"https://schema.org","@type":"LocalBusiness","name":"%s","url":"%s"}`, name, url)
	case "breadcrumb":
		return fmt.Sprintf(`{"@context":"https://schema.org","@type":"BreadcrumbList","itemListElement":[{"@type":"ListItem","position":1,"name":"Home","item":"%s"}]}`, url)
	default: // organization
		return fmt.Sprintf(`{"@context":"https://schema.org","@type":"Organization","name":"%s","url":"%s"}`, name, url)
	}
}

func calculatePageScore(issues []SEOIssue) int {
	score := 100
	for _, iss := range issues {
		switch iss.Type {
		case "error":
			score -= 10
		case "warning":
			score -= 5
		case "info":
			score -= 1
		}
	}
	if score < 0 {
		return 0
	}
	return score
}

func detectBuildDir(workDir string) string {
	candidates := []string{
		filepath.Join(workDir, "dist"),
		filepath.Join(workDir, ".next", "server", "pages"),
		filepath.Join(workDir, "out"),
		filepath.Join(workDir, "build"),
	}
	for _, dir := range candidates {
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			return dir
		}
	}
	return ""
}

// resolvePagePath maps a URL path (e.g. "/pricing") to an HTML file path.
func (m *SEOManager) resolvePagePath(page string) (string, error) {
	siteDir := m.siteDir
	if siteDir == "" {
		return "", fmt.Errorf("no build output directory found")
	}
	page = strings.TrimPrefix(page, "/")
	if page == "" {
		page = "index"
	}
	candidates := []string{
		filepath.Join(siteDir, page+".html"),
		filepath.Join(siteDir, page, "index.html"),
	}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c, nil
		}
	}
	return "", fmt.Errorf("page file not found for path %q", page)
}

// generateSitemapXMLFromDir collects pages and calls generateSitemapXML.
func (m *SEOManager) generateSitemapXMLFromDir() (string, error) {
	siteDir := m.siteDir
	if siteDir == "" {
		return "", fmt.Errorf("no build output directory found")
	}
	htmlFiles, err := scanHTMLFiles(siteDir)
	if err != nil {
		return "", err
	}
	var pages []string
	for _, path := range htmlFiles {
		rel, _ := filepath.Rel(siteDir, path)
		page := "/" + filepath.ToSlash(rel)
		if strings.HasSuffix(page, "/index.html") {
			page = strings.TrimSuffix(page, "index.html")
		} else {
			page = strings.TrimSuffix(page, ".html")
		}
		pages = append(pages, page)
	}
	return generateSitemapXML(pages, "https://example.com"), nil
}

// generateRobotsTxtContent builds robots.txt content.
func (m *SEOManager) generateRobotsTxtContent(customRules string) (string, error) {
	sitemapURL := "https://example.com/sitemap.xml"
	var sb strings.Builder
	sb.WriteString("User-agent: *\n")
	sb.WriteString("Allow: /\n")
	sb.WriteString("\n# Block common admin/private paths\n")
	sb.WriteString("Disallow: /admin/\n")
	sb.WriteString("Disallow: /api/\n")
	sb.WriteString("Disallow: /_next/\n")
	sb.WriteString("Disallow: /static/chunks/\n")
	if customRules != "" {
		sb.WriteString("\n# Custom rules\n")
		sb.WriteString(strings.TrimSpace(customRules))
		sb.WriteString("\n")
	}
	sb.WriteString("\n")
	sb.WriteString(fmt.Sprintf("Sitemap: %s\n", sitemapURL))
	return sb.String(), nil
}
