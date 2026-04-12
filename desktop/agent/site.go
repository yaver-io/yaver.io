package main

// site.go — AI-powered static site generator for the Yaver workspace.
// Generates landing pages, blogs, and content sites using Astro + Tailwind.
// Replaces WordPress / Webflow / Framer for developers who ship from the CLI.

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

// SitePage represents a static page in the site.
type SitePage struct {
	Slug      string    `json:"slug"`
	Title     string    `json:"title"`
	Content   string    `json:"content"`
	Template  string    `json:"template"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
	Draft     bool      `json:"draft"`
	Tags      []string  `json:"tags"`
}

// BlogPost represents a blog post with frontmatter metadata.
type BlogPost struct {
	Slug        string    `json:"slug"`
	Title       string    `json:"title"`
	Content     string    `json:"content"`
	Author      string    `json:"author"`
	PublishedAt time.Time `json:"publishedAt"`
	Tags        []string  `json:"tags"`
	Draft       bool      `json:"draft"`
	Description string    `json:"description"`
}

// SiteConfig holds the complete site configuration persisted to disk.
type SiteConfig struct {
	Name      string     `json:"name"`
	Framework string     `json:"framework"` // astro | next-static | 11ty
	Pages     []SitePage `json:"pages"`
	Posts     []BlogPost `json:"posts"`
	Domain    string     `json:"domain"`
	Theme     string     `json:"theme"`
}

// SiteManager manages a single Astro/static site inside the workspace.
type SiteManager struct {
	mu         sync.Mutex
	workDir    string
	siteDir    string
	config     *SiteConfig
	configPath string
}

// ---------------------------------------------------------------------------
// Constructor
// ---------------------------------------------------------------------------

// NewSiteManager creates a SiteManager rooted at workDir.
// The site lives at workDir/site/ and config at workDir/.yaver/site.json.
func NewSiteManager(workDir string) *SiteManager {
	return &SiteManager{
		workDir:    workDir,
		siteDir:    filepath.Join(workDir, "site"),
		configPath: filepath.Join(workDir, ".yaver", "site.json"),
		config:     &SiteConfig{Framework: "astro"},
	}
}

// ---------------------------------------------------------------------------
// Create
// ---------------------------------------------------------------------------

// Create bootstraps a new site of the given type and framework.
// siteType: "landing" | "blog" | "docs" | "changelog"
// framework: "astro" | "next-static" | "11ty" (defaults to "astro")
// Returns the absolute path to the site directory.
func (m *SiteManager) Create(siteType, name, framework string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if framework == "" {
		framework = "astro"
	}

	m.config.Name = name
	m.config.Framework = framework

	siteDir := filepath.Join(m.workDir, name)

	switch framework {
	case "astro":
		if err := m.createAstroSite(name, siteType, siteDir); err != nil {
			return "", fmt.Errorf("create astro site: %w", err)
		}
	case "next-static":
		if err := m.createNextStaticSite(name, siteDir); err != nil {
			return "", fmt.Errorf("create next-static site: %w", err)
		}
	case "11ty":
		if err := m.createEleventySite(name, siteDir); err != nil {
			return "", fmt.Errorf("create eleventy site: %w", err)
		}
	default:
		return "", fmt.Errorf("unsupported framework %q (use astro, next-static, or 11ty)", framework)
	}

	m.siteDir = siteDir
	if err := m.saveConfig(); err != nil {
		return "", fmt.Errorf("save config: %w", err)
	}

	return siteDir, nil
}

func (m *SiteManager) createAstroSite(name, siteType, siteDir string) error {
	// Bootstrap via Astro CLI
	cmd := exec.Command("npm", "create", "astro@latest", name, "--",
		"--template", "minimal", "--no-install", "--no-git")
	cmd.Dir = m.workDir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("npm create astro: %w", err)
	}

	// Add Tailwind + MDX integrations
	addCmd := exec.Command("npx", "astro", "add", "tailwind", "mdx", "--yes")
	addCmd.Dir = siteDir
	addCmd.Stdout = os.Stdout
	addCmd.Stderr = os.Stderr
	if err := addCmd.Run(); err != nil {
		// Non-fatal — integrations can be added manually
		fmt.Printf("[site] warning: failed to add integrations: %v\n", err)
	}

	// Ensure directory structure exists
	for _, dir := range []string{
		"src/pages",
		"src/content/blog",
		"src/components",
		"src/layouts",
		"public",
	} {
		if err := os.MkdirAll(filepath.Join(siteDir, dir), 0o755); err != nil {
			return fmt.Errorf("mkdir %s: %w", dir, err)
		}
	}

	// Write content collection config
	collectionsCfg := `import { defineCollection, z } from 'astro:content';

const blog = defineCollection({
  type: 'content',
  schema: z.object({
    title: z.string(),
    description: z.string().optional(),
    publishedAt: z.coerce.date(),
    tags: z.array(z.string()).default([]),
    draft: z.boolean().default(false),
    author: z.string().default(''),
  }),
});

export const collections = { blog };
`
	if err := os.WriteFile(filepath.Join(siteDir, "src/content/config.ts"), []byte(collectionsCfg), 0o644); err != nil {
		return err
	}

	// Generate base layout
	layout := m.generateLayout(name, "minimal")
	if err := os.WriteFile(filepath.Join(siteDir, "src/layouts/BaseLayout.astro"), []byte(layout), 0o644); err != nil {
		return err
	}

	// Generate header + footer
	header := m.generateHeader(name)
	if err := os.WriteFile(filepath.Join(siteDir, "src/components/Header.astro"), []byte(header), 0o644); err != nil {
		return err
	}

	footer := m.generateFooter(name)
	if err := os.WriteFile(filepath.Join(siteDir, "src/components/Footer.astro"), []byte(footer), 0o644); err != nil {
		return err
	}

	// Generate index page based on site type
	index := m.generateIndexPage(name, siteType)
	if err := os.WriteFile(filepath.Join(siteDir, "src/pages/index.astro"), []byte(index), 0o644); err != nil {
		return err
	}

	// Install dependencies
	installCmd := exec.Command("npm", "install")
	installCmd.Dir = siteDir
	installCmd.Stdout = os.Stdout
	installCmd.Stderr = os.Stderr
	if err := installCmd.Run(); err != nil {
		return fmt.Errorf("npm install: %w", err)
	}

	return nil
}

func (m *SiteManager) createNextStaticSite(name, siteDir string) error {
	cmd := exec.Command("npx", "create-next-app@latest", name,
		"--typescript", "--tailwind", "--no-eslint", "--no-app",
		"--no-src-dir", "--no-import-alias")
	cmd.Dir = m.workDir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("create-next-app: %w", err)
	}

	// Add static export config
	nextConfig := `/** @type {import('next').NextConfig} */
const nextConfig = {
  output: 'export',
  trailingSlash: true,
};

module.exports = nextConfig;
`
	return os.WriteFile(filepath.Join(siteDir, "next.config.js"), []byte(nextConfig), 0o644)
}

func (m *SiteManager) createEleventySite(name, siteDir string) error {
	if err := os.MkdirAll(siteDir, 0o755); err != nil {
		return err
	}

	// Minimal package.json
	pkg := fmt.Sprintf(`{
  "name": "%s",
  "version": "1.0.0",
  "scripts": {
    "build": "eleventy",
    "dev": "eleventy --serve"
  },
  "devDependencies": {
    "@11ty/eleventy": "^3.0.0"
  }
}
`, name)
	if err := os.WriteFile(filepath.Join(siteDir, "package.json"), []byte(pkg), 0o644); err != nil {
		return err
	}

	cmd := exec.Command("npm", "install")
	cmd.Dir = siteDir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// ---------------------------------------------------------------------------
// Pages
// ---------------------------------------------------------------------------

// Pages returns all pages registered in the site config.
func (m *SiteManager) Pages() ([]SitePage, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.loadConfig(); err != nil {
		return nil, err
	}

	// Also scan src/pages/ for any files not yet tracked in config
	known := make(map[string]bool)
	for _, p := range m.config.Pages {
		known[p.Slug] = true
	}

	pagesDir := filepath.Join(m.siteDir, "src/pages")
	entries, err := os.ReadDir(pagesDir)
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("read pages dir: %w", err)
	}

	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		base := strings.TrimSuffix(e.Name(), filepath.Ext(e.Name()))
		if base == "index" || known[base] {
			continue
		}
		info, _ := e.Info()
		m.config.Pages = append(m.config.Pages, SitePage{
			Slug:      base,
			Title:     strings.Title(strings.ReplaceAll(base, "-", " ")),
			CreatedAt: info.ModTime(),
			UpdatedAt: info.ModTime(),
		})
	}

	return m.config.Pages, nil
}

// PageAdd creates a new page at src/pages/{slug}.astro.
// If content is empty a sensible template is generated based on the slug.
func (m *SiteManager) PageAdd(slug, title, content string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.loadConfig(); err != nil {
		return "", err
	}

	slug = m.slugify(slug)
	if title == "" {
		title = strings.Title(strings.ReplaceAll(slug, "-", " "))
	}

	if content == "" {
		content = m.generatePageTemplate(slug, title)
	}

	pagePath := filepath.Join(m.siteDir, "src/pages", slug+".astro")
	if err := os.MkdirAll(filepath.Dir(pagePath), 0o755); err != nil {
		return "", fmt.Errorf("mkdir: %w", err)
	}
	if err := os.WriteFile(pagePath, []byte(content), 0o644); err != nil {
		return "", fmt.Errorf("write page: %w", err)
	}

	now := time.Now()
	m.config.Pages = append(m.config.Pages, SitePage{
		Slug:      slug,
		Title:     title,
		Content:   content,
		Template:  "default",
		CreatedAt: now,
		UpdatedAt: now,
	})

	if err := m.saveConfig(); err != nil {
		return "", err
	}

	return pagePath, nil
}

// PageEdit updates the content of an existing page.
func (m *SiteManager) PageEdit(slug, content string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	slug = m.slugify(slug)
	pagePath := filepath.Join(m.siteDir, "src/pages", slug+".astro")

	if _, err := os.Stat(pagePath); os.IsNotExist(err) {
		return "", fmt.Errorf("page %q not found", slug)
	}

	if err := os.WriteFile(pagePath, []byte(content), 0o644); err != nil {
		return "", fmt.Errorf("write page: %w", err)
	}

	// Update tracked config entry
	for i, p := range m.config.Pages {
		if p.Slug == slug {
			m.config.Pages[i].Content = content
			m.config.Pages[i].UpdatedAt = time.Now()
			break
		}
	}

	if err := m.saveConfig(); err != nil {
		return "", err
	}

	return pagePath, nil
}

// PageRemove deletes the page file and removes it from config.
func (m *SiteManager) PageRemove(slug string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	slug = m.slugify(slug)
	pagePath := filepath.Join(m.siteDir, "src/pages", slug+".astro")

	if err := os.Remove(pagePath); err != nil && !os.IsNotExist(err) {
		return "", fmt.Errorf("remove page file: %w", err)
	}

	pages := m.config.Pages[:0]
	for _, p := range m.config.Pages {
		if p.Slug != slug {
			pages = append(pages, p)
		}
	}
	m.config.Pages = pages

	if err := m.saveConfig(); err != nil {
		return "", err
	}

	return fmt.Sprintf("removed page %q", slug), nil
}

// ---------------------------------------------------------------------------
// Build / Serve / Deploy
// ---------------------------------------------------------------------------

// Build runs `npm run build` and returns a human-readable output size summary.
func (m *SiteManager) Build() (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	cmd := exec.Command("npm", "run", "build")
	cmd.Dir = m.siteDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("build failed: %w\n%s", err, out)
	}

	distDir := filepath.Join(m.siteDir, "dist")
	size := dirSize(distDir)

	return fmt.Sprintf("build succeeded — dist/ is %.1f KB\n%s", float64(size)/1024, out), nil
}

// Serve starts the dev server for local preview and returns the URL.
func (m *SiteManager) Serve(port int) (string, error) {
	if port == 0 {
		port = 4321
	}

	var cmd *exec.Cmd
	switch m.config.Framework {
	case "astro":
		cmd = exec.Command("npx", "astro", "dev", "--port", fmt.Sprintf("%d", port))
	default:
		cmd = exec.Command("npm", "run", "dev", "--", "--port", fmt.Sprintf("%d", port))
	}
	cmd.Dir = m.siteDir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("start dev server: %w", err)
	}

	return fmt.Sprintf("http://localhost:%d", port), nil
}

// Deploy builds the site and copies dist/ into the platform serving directory.
func (m *SiteManager) Deploy() (string, error) {
	result, err := m.Build()
	if err != nil {
		return "", err
	}

	distDir := filepath.Join(m.siteDir, "dist")
	serveDir := filepath.Join(m.workDir, ".yaver", "sites", m.config.Name)

	if err := os.MkdirAll(serveDir, 0o755); err != nil {
		return "", fmt.Errorf("create serve dir: %w", err)
	}

	cpCmd := exec.Command("cp", "-r", distDir+"/.", serveDir)
	if out, err := cpCmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("copy dist: %w\n%s", err, out)
	}

	return fmt.Sprintf("deployed to %s\n%s", serveDir, result), nil
}

// ---------------------------------------------------------------------------
// Generate (AI-assisted landing page)
// ---------------------------------------------------------------------------

// Generate creates a complete landing page with real Astro+Tailwind components.
// style: "minimal" | "bold" | "corporate" | "playful"
func (m *SiteManager) Generate(productName, tagline string, features []string, cta, style string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if style == "" {
		style = "minimal"
	}

	componentsDir := filepath.Join(m.siteDir, "src/components")
	if err := os.MkdirAll(componentsDir, 0o755); err != nil {
		return "", fmt.Errorf("mkdir components: %w", err)
	}

	// Write each section component
	components := map[string]string{
		"Hero.astro":         m.generateHero(productName, tagline, cta, style),
		"FeatureGrid.astro":  m.generateFeatures(features, style),
		"PricingTable.astro": m.generatePricing(style),
		"CTASection.astro":   m.generateCTA(cta, style),
		"Footer.astro":       m.generateFooter(productName),
	}

	for filename, content := range components {
		path := filepath.Join(componentsDir, filename)
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			return "", fmt.Errorf("write %s: %w", filename, err)
		}
	}

	// Write (or overwrite) BaseLayout
	layout := m.generateLayout(productName, style)
	if err := os.WriteFile(filepath.Join(m.siteDir, "src/layouts/BaseLayout.astro"), []byte(layout), 0o644); err != nil {
		return "", fmt.Errorf("write layout: %w", err)
	}

	// Write landing page index
	index := m.generateLandingIndex(productName, style)
	if err := os.WriteFile(filepath.Join(m.siteDir, "src/pages/index.astro"), []byte(index), 0o644); err != nil {
		return "", fmt.Errorf("write index: %w", err)
	}

	return fmt.Sprintf("generated landing page for %q with style %q in %s", productName, style, m.siteDir), nil
}

// ---------------------------------------------------------------------------
// Blog
// ---------------------------------------------------------------------------

// BlogPost creates a new blog post markdown file with YAML frontmatter.
func (m *SiteManager) BlogPost(title, content string, tags []string, draft bool) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.loadConfig(); err != nil {
		return "", err
	}

	slug := m.slugify(title)
	now := time.Now()

	frontmatter := map[string]interface{}{
		"title":       title,
		"description": "",
		"publishedAt": now.Format(time.RFC3339),
		"tags":        tags,
		"draft":       draft,
		"author":      "",
	}

	fm, err := yaml.Marshal(frontmatter)
	if err != nil {
		return "", fmt.Errorf("marshal frontmatter: %w", err)
	}

	fileContent := fmt.Sprintf("---\n%s---\n\n%s\n", string(fm), content)

	blogDir := filepath.Join(m.siteDir, "src/content/blog")
	if err := os.MkdirAll(blogDir, 0o755); err != nil {
		return "", fmt.Errorf("mkdir blog: %w", err)
	}

	postPath := filepath.Join(blogDir, slug+".md")
	if err := os.WriteFile(postPath, []byte(fileContent), 0o644); err != nil {
		return "", fmt.Errorf("write post: %w", err)
	}

	post := BlogPost{
		Slug:        slug,
		Title:       title,
		Content:     content,
		Tags:        tags,
		Draft:       draft,
		PublishedAt: now,
	}
	m.config.Posts = append(m.config.Posts, post)

	if err := m.saveConfig(); err != nil {
		return "", err
	}

	return postPath, nil
}

// BlogList returns all blog posts by scanning src/content/blog/.
func (m *SiteManager) BlogList() ([]BlogPost, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	blogDir := filepath.Join(m.siteDir, "src/content/blog")
	entries, err := os.ReadDir(blogDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read blog dir: %w", err)
	}

	var posts []BlogPost
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if !strings.HasSuffix(e.Name(), ".md") && !strings.HasSuffix(e.Name(), ".mdx") {
			continue
		}

		raw, err := os.ReadFile(filepath.Join(blogDir, e.Name()))
		if err != nil {
			continue
		}

		fm, body, err := m.parseFrontmatter(string(raw))
		if err != nil {
			continue
		}

		slug := strings.TrimSuffix(e.Name(), filepath.Ext(e.Name()))
		post := BlogPost{
			Slug:    slug,
			Content: body,
		}

		if v, ok := fm["title"].(string); ok {
			post.Title = v
		}
		if v, ok := fm["description"].(string); ok {
			post.Description = v
		}
		if v, ok := fm["author"].(string); ok {
			post.Author = v
		}
		if v, ok := fm["draft"].(bool); ok {
			post.Draft = v
		}
		if v, ok := fm["publishedAt"].(string); ok {
			if t, err := time.Parse(time.RFC3339, v); err == nil {
				post.PublishedAt = t
			}
		}
		if v, ok := fm["tags"].([]interface{}); ok {
			for _, tag := range v {
				if s, ok := tag.(string); ok {
					post.Tags = append(post.Tags, s)
				}
			}
		}

		posts = append(posts, post)
	}

	return posts, nil
}

// BlogPublish sets draft: false in a post's frontmatter.
func (m *SiteManager) BlogPublish(slug string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	slug = m.slugify(slug)
	postPath := filepath.Join(m.siteDir, "src/content/blog", slug+".md")

	raw, err := os.ReadFile(postPath)
	if err != nil {
		// Try .mdx
		postPath = filepath.Join(m.siteDir, "src/content/blog", slug+".mdx")
		raw, err = os.ReadFile(postPath)
		if err != nil {
			return "", fmt.Errorf("post %q not found", slug)
		}
	}

	fm, body, err := m.parseFrontmatter(string(raw))
	if err != nil {
		return "", fmt.Errorf("parse frontmatter: %w", err)
	}

	fm["draft"] = false
	fm["publishedAt"] = time.Now().Format(time.RFC3339)

	newFM, err := yaml.Marshal(fm)
	if err != nil {
		return "", fmt.Errorf("marshal frontmatter: %w", err)
	}

	fileContent := fmt.Sprintf("---\n%s---\n\n%s", string(newFM), body)
	if err := os.WriteFile(postPath, []byte(fileContent), 0o644); err != nil {
		return "", fmt.Errorf("write post: %w", err)
	}

	return fmt.Sprintf("published post %q", slug), nil
}

// ---------------------------------------------------------------------------
// Internal — component generators
// ---------------------------------------------------------------------------

// generateComponent returns a generic Astro component with Tailwind classes.
func (m *SiteManager) generateComponent(name, style, props string) string {
	wrapper := sectionWrapper(style)
	return fmt.Sprintf(`---
%s
---

%s
  <div class="max-w-6xl mx-auto px-4 sm:px-6 lg:px-8">
    <!-- %s component -->
    <slot />
  </div>
%s
`, props, wrapper.open, name, wrapper.close)
}

// generateHero returns the Hero.astro component content.
func (m *SiteManager) generateHero(productName, tagline, cta, style string) string {
	switch style {
	case "bold":
		return fmt.Sprintf(`---
interface Props {
  ctaHref?: string;
}
const { ctaHref = '#' } = Astro.props;
---

<section class="relative bg-gradient-to-br from-indigo-600 via-purple-600 to-pink-500 py-24 md:py-36 overflow-hidden">
  <div class="absolute inset-0 bg-[url('/grid.svg')] opacity-10"></div>
  <div class="relative max-w-6xl mx-auto px-4 sm:px-6 lg:px-8 text-center">
    <h1 class="text-6xl md:text-8xl font-extrabold text-white tracking-tight leading-none mb-6">
      %s
    </h1>
    <p class="text-2xl md:text-3xl text-indigo-100 font-medium max-w-3xl mx-auto mb-10">
      %s
    </p>
    <a
      href={ctaHref}
      class="inline-block bg-white text-indigo-700 font-bold text-lg px-10 py-4 rounded-2xl shadow-xl hover:shadow-2xl hover:-translate-y-1 transition-all duration-200"
    >
      %s
    </a>
  </div>
</section>
`, productName, tagline, cta)

	case "corporate":
		return fmt.Sprintf(`---
interface Props {
  ctaHref?: string;
}
const { ctaHref = '#' } = Astro.props;
---

<section class="bg-slate-50 border-b border-slate-200 py-20 md:py-28">
  <div class="max-w-6xl mx-auto px-4 sm:px-6 lg:px-8">
    <div class="max-w-3xl">
      <h1 class="text-4xl md:text-5xl font-bold text-slate-900 tracking-tight mb-5">
        %s
      </h1>
      <p class="text-xl text-slate-600 leading-relaxed mb-8">
        %s
      </p>
      <div class="flex flex-wrap gap-4">
        <a
          href={ctaHref}
          class="inline-block bg-slate-900 text-white font-semibold px-8 py-3 rounded-lg hover:bg-slate-700 transition-colors"
        >
          %s
        </a>
        <a href="#learn-more" class="inline-block text-slate-700 font-semibold px-8 py-3 rounded-lg border border-slate-300 hover:border-slate-400 transition-colors">
          Learn more
        </a>
      </div>
    </div>
  </div>
</section>
`, productName, tagline, cta)

	case "playful":
		return fmt.Sprintf(`---
interface Props {
  ctaHref?: string;
}
const { ctaHref = '#' } = Astro.props;
---

<section class="bg-gradient-to-br from-yellow-50 via-pink-50 to-purple-50 py-20 md:py-32">
  <div class="max-w-5xl mx-auto px-4 sm:px-6 lg:px-8 text-center">
    <div class="inline-block bg-white rounded-full px-5 py-2 text-sm font-semibold text-purple-600 shadow-sm mb-6 border border-purple-100">
      ✨ Hello, world!
    </div>
    <h1 class="text-5xl md:text-7xl font-extrabold text-gray-900 leading-tight mb-5">
      %s 🚀
    </h1>
    <p class="text-xl md:text-2xl text-gray-600 max-w-2xl mx-auto mb-10">
      %s
    </p>
    <a
      href={ctaHref}
      class="inline-block bg-gradient-to-r from-purple-500 to-pink-500 text-white font-bold text-lg px-10 py-4 rounded-2xl shadow-lg hover:shadow-xl hover:-translate-y-1 transition-all duration-200"
    >
      %s
    </a>
  </div>
</section>
`, productName, tagline, cta)

	default: // minimal
		return fmt.Sprintf(`---
interface Props {
  ctaHref?: string;
}
const { ctaHref = '#' } = Astro.props;
---

<section class="bg-white py-20 md:py-32">
  <div class="max-w-5xl mx-auto px-4 sm:px-6 lg:px-8 text-center">
    <h1 class="text-5xl md:text-6xl font-bold text-gray-900 tracking-tight mb-6">
      %s
    </h1>
    <p class="text-xl text-gray-500 max-w-2xl mx-auto mb-10 leading-relaxed">
      %s
    </p>
    <a
      href={ctaHref}
      class="inline-block bg-gray-900 text-white font-semibold px-8 py-3 rounded-lg hover:bg-gray-700 transition-colors"
    >
      %s
    </a>
  </div>
</section>
`, productName, tagline, cta)
	}
}

// generateFeatures returns the FeatureGrid.astro component content.
func (m *SiteManager) generateFeatures(features []string, style string) string {
	var items strings.Builder
	icons := []string{"⚡", "🔒", "📦", "🌍", "🤝", "💡", "🛠️", "📈"}
	for i, f := range features {
		icon := icons[i%len(icons)]
		switch style {
		case "bold":
			items.WriteString(fmt.Sprintf(`
    <div class="bg-white/10 backdrop-blur-sm border border-white/20 rounded-2xl p-6 hover:bg-white/20 transition-colors">
      <div class="text-4xl mb-4">%s</div>
      <h3 class="text-lg font-bold text-white mb-2">%s</h3>
    </div>
`, icon, f))
		case "corporate":
			items.WriteString(fmt.Sprintf(`
    <div class="bg-white border border-slate-200 rounded-lg p-6 hover:shadow-md transition-shadow">
      <div class="w-10 h-10 bg-slate-100 rounded-lg flex items-center justify-center mb-4 text-xl">%s</div>
      <h3 class="text-base font-semibold text-slate-900 mb-1">%s</h3>
    </div>
`, icon, f))
		case "playful":
			items.WriteString(fmt.Sprintf(`
    <div class="bg-white rounded-2xl p-6 shadow-sm border border-gray-100 hover:-translate-y-1 transition-transform">
      <div class="text-4xl mb-3">%s</div>
      <h3 class="text-base font-bold text-gray-900">%s</h3>
    </div>
`, icon, f))
		default: // minimal
			items.WriteString(fmt.Sprintf(`
    <div class="py-6">
      <div class="text-2xl mb-2">%s</div>
      <h3 class="text-base font-semibold text-gray-900">%s</h3>
    </div>
`, icon, f))
		}
	}

	sectionClasses := map[string]string{
		"bold":      "bg-gradient-to-br from-indigo-600 to-purple-700 py-20",
		"corporate": "bg-slate-50 py-20 border-b border-slate-100",
		"playful":   "bg-gray-50 py-20",
		"minimal":   "bg-white py-20 border-t border-gray-100",
	}
	gridClasses := map[string]string{
		"bold":      "grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-4",
		"corporate": "grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-6",
		"playful":   "grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-5",
		"minimal":   "grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 divide-x divide-y divide-gray-100",
	}
	headingClasses := map[string]string{
		"bold":      "text-3xl font-extrabold text-white text-center mb-12",
		"corporate": "text-2xl font-bold text-slate-900 text-center mb-12",
		"playful":   "text-3xl font-extrabold text-gray-900 text-center mb-10",
		"minimal":   "text-2xl font-bold text-gray-900 text-center mb-12",
	}

	sc := sectionClasses[style]
	gc := gridClasses[style]
	hc := headingClasses[style]
	if sc == "" {
		sc = sectionClasses["minimal"]
	}
	if gc == "" {
		gc = gridClasses["minimal"]
	}
	if hc == "" {
		hc = headingClasses["minimal"]
	}

	return fmt.Sprintf(`---
---

<section class="%s">
  <div class="max-w-6xl mx-auto px-4 sm:px-6 lg:px-8">
    <h2 class="%s">Everything you need</h2>
    <div class="%s">
%s    </div>
  </div>
</section>
`, sc, hc, gc, items.String())
}

// generatePricing returns a PricingTable.astro with a 3-tier template.
func (m *SiteManager) generatePricing(style string) string {
	switch style {
	case "bold":
		return `---
---

<section class="bg-gray-950 py-24">
  <div class="max-w-6xl mx-auto px-4 sm:px-6 lg:px-8 text-center">
    <h2 class="text-4xl font-extrabold text-white mb-4">Simple pricing</h2>
    <p class="text-indigo-300 mb-14">Start free. Scale as you grow.</p>
    <div class="grid grid-cols-1 md:grid-cols-3 gap-6">
      <!-- Starter -->
      <div class="bg-white/5 border border-white/10 rounded-2xl p-8">
        <h3 class="text-lg font-bold text-white mb-1">Starter</h3>
        <div class="text-4xl font-extrabold text-white my-4">$0<span class="text-lg text-gray-400 font-normal">/mo</span></div>
        <ul class="text-gray-300 space-y-2 text-sm mb-8">
          <li>✓ Up to 3 projects</li>
          <li>✓ Community support</li>
          <li>✓ 1 GB storage</li>
        </ul>
        <a href="#" class="block w-full py-3 rounded-xl bg-white/10 text-white font-semibold hover:bg-white/20 transition-colors">Get started</a>
      </div>
      <!-- Pro — highlighted -->
      <div class="bg-gradient-to-b from-indigo-500 to-purple-600 rounded-2xl p-8 shadow-2xl scale-105">
        <div class="text-xs font-bold text-indigo-100 uppercase tracking-widest mb-2">Most popular</div>
        <h3 class="text-lg font-bold text-white mb-1">Pro</h3>
        <div class="text-4xl font-extrabold text-white my-4">$29<span class="text-lg text-indigo-200 font-normal">/mo</span></div>
        <ul class="text-indigo-100 space-y-2 text-sm mb-8">
          <li>✓ Unlimited projects</li>
          <li>✓ Priority support</li>
          <li>✓ 50 GB storage</li>
          <li>✓ Custom domains</li>
        </ul>
        <a href="#" class="block w-full py-3 rounded-xl bg-white text-indigo-700 font-bold hover:bg-indigo-50 transition-colors">Get started</a>
      </div>
      <!-- Enterprise -->
      <div class="bg-white/5 border border-white/10 rounded-2xl p-8">
        <h3 class="text-lg font-bold text-white mb-1">Enterprise</h3>
        <div class="text-4xl font-extrabold text-white my-4">Custom</div>
        <ul class="text-gray-300 space-y-2 text-sm mb-8">
          <li>✓ Everything in Pro</li>
          <li>✓ Dedicated support</li>
          <li>✓ SLA & compliance</li>
          <li>✓ SSO / SAML</li>
        </ul>
        <a href="mailto:hello@example.com" class="block w-full py-3 rounded-xl bg-white/10 text-white font-semibold hover:bg-white/20 transition-colors">Contact us</a>
      </div>
    </div>
  </div>
</section>
`

	case "corporate":
		return `---
---

<section class="bg-white py-20 border-b border-slate-100">
  <div class="max-w-6xl mx-auto px-4 sm:px-6 lg:px-8">
    <div class="text-center mb-14">
      <h2 class="text-3xl font-bold text-slate-900 mb-3">Pricing</h2>
      <p class="text-slate-500 text-lg">Transparent pricing for every stage.</p>
    </div>
    <div class="grid grid-cols-1 md:grid-cols-3 gap-8">
      <div class="border border-slate-200 rounded-lg p-8">
        <h3 class="text-base font-semibold text-slate-500 uppercase tracking-wide mb-4">Starter</h3>
        <div class="text-4xl font-bold text-slate-900 mb-6">$0<span class="text-base font-normal text-slate-400">/mo</span></div>
        <ul class="space-y-3 text-slate-600 text-sm mb-8">
          <li class="flex items-center gap-2"><span class="text-green-500">✓</span> Up to 3 projects</li>
          <li class="flex items-center gap-2"><span class="text-green-500">✓</span> Community support</li>
          <li class="flex items-center gap-2"><span class="text-green-500">✓</span> 1 GB storage</li>
        </ul>
        <a href="#" class="block w-full text-center py-2.5 border border-slate-300 rounded-lg text-slate-700 font-medium hover:bg-slate-50 transition-colors">Get started</a>
      </div>
      <div class="border-2 border-slate-900 rounded-lg p-8 relative">
        <span class="absolute -top-3 left-1/2 -translate-x-1/2 bg-slate-900 text-white text-xs font-bold px-3 py-1 rounded-full">POPULAR</span>
        <h3 class="text-base font-semibold text-slate-500 uppercase tracking-wide mb-4">Pro</h3>
        <div class="text-4xl font-bold text-slate-900 mb-6">$29<span class="text-base font-normal text-slate-400">/mo</span></div>
        <ul class="space-y-3 text-slate-600 text-sm mb-8">
          <li class="flex items-center gap-2"><span class="text-green-500">✓</span> Unlimited projects</li>
          <li class="flex items-center gap-2"><span class="text-green-500">✓</span> Priority support</li>
          <li class="flex items-center gap-2"><span class="text-green-500">✓</span> 50 GB storage</li>
          <li class="flex items-center gap-2"><span class="text-green-500">✓</span> Custom domains</li>
        </ul>
        <a href="#" class="block w-full text-center py-2.5 bg-slate-900 text-white rounded-lg font-medium hover:bg-slate-700 transition-colors">Get started</a>
      </div>
      <div class="border border-slate-200 rounded-lg p-8">
        <h3 class="text-base font-semibold text-slate-500 uppercase tracking-wide mb-4">Enterprise</h3>
        <div class="text-4xl font-bold text-slate-900 mb-6">Custom</div>
        <ul class="space-y-3 text-slate-600 text-sm mb-8">
          <li class="flex items-center gap-2"><span class="text-green-500">✓</span> Everything in Pro</li>
          <li class="flex items-center gap-2"><span class="text-green-500">✓</span> Dedicated support</li>
          <li class="flex items-center gap-2"><span class="text-green-500">✓</span> SLA & compliance</li>
          <li class="flex items-center gap-2"><span class="text-green-500">✓</span> SSO / SAML</li>
        </ul>
        <a href="mailto:hello@example.com" class="block w-full text-center py-2.5 border border-slate-300 rounded-lg text-slate-700 font-medium hover:bg-slate-50 transition-colors">Contact sales</a>
      </div>
    </div>
  </div>
</section>
`

	case "playful":
		return `---
---

<section class="bg-white py-20">
  <div class="max-w-6xl mx-auto px-4 sm:px-6 lg:px-8 text-center">
    <h2 class="text-4xl font-extrabold text-gray-900 mb-3">Pick your plan 🎉</h2>
    <p class="text-gray-500 mb-12">No hidden fees. Cancel anytime.</p>
    <div class="grid grid-cols-1 md:grid-cols-3 gap-6">
      <div class="bg-gray-50 rounded-2xl p-8 border border-gray-100">
        <div class="text-3xl mb-3">🌱</div>
        <h3 class="text-lg font-bold text-gray-900 mb-1">Starter</h3>
        <div class="text-4xl font-extrabold text-gray-900 my-4">Free</div>
        <ul class="text-gray-600 space-y-2 text-sm mb-8 text-left">
          <li>✅ Up to 3 projects</li>
          <li>✅ Community support</li>
          <li>✅ 1 GB storage</li>
        </ul>
        <a href="#" class="block w-full py-3 rounded-2xl bg-gray-200 text-gray-700 font-bold hover:bg-gray-300 transition-colors">Start free</a>
      </div>
      <div class="bg-gradient-to-br from-purple-500 to-pink-500 rounded-2xl p-8 shadow-xl">
        <div class="text-3xl mb-3">🚀</div>
        <h3 class="text-lg font-bold text-white mb-1">Pro</h3>
        <div class="text-4xl font-extrabold text-white my-4">$29<span class="text-base font-normal text-pink-100">/mo</span></div>
        <ul class="text-pink-100 space-y-2 text-sm mb-8 text-left">
          <li>✅ Unlimited projects</li>
          <li>✅ Priority support</li>
          <li>✅ 50 GB storage</li>
          <li>✅ Custom domains</li>
        </ul>
        <a href="#" class="block w-full py-3 rounded-2xl bg-white text-purple-600 font-bold hover:bg-pink-50 transition-colors">Get Pro</a>
      </div>
      <div class="bg-gray-50 rounded-2xl p-8 border border-gray-100">
        <div class="text-3xl mb-3">🏢</div>
        <h3 class="text-lg font-bold text-gray-900 mb-1">Enterprise</h3>
        <div class="text-4xl font-extrabold text-gray-900 my-4">Custom</div>
        <ul class="text-gray-600 space-y-2 text-sm mb-8 text-left">
          <li>✅ Everything in Pro</li>
          <li>✅ Dedicated support</li>
          <li>✅ SLA & compliance</li>
          <li>✅ SSO / SAML</li>
        </ul>
        <a href="mailto:hello@example.com" class="block w-full py-3 rounded-2xl bg-gray-200 text-gray-700 font-bold hover:bg-gray-300 transition-colors">Contact us</a>
      </div>
    </div>
  </div>
</section>
`

	default: // minimal
		return `---
---

<section class="bg-gray-50 py-20">
  <div class="max-w-5xl mx-auto px-4 sm:px-6 lg:px-8 text-center">
    <h2 class="text-3xl font-bold text-gray-900 mb-3">Pricing</h2>
    <p class="text-gray-500 mb-12">Simple, transparent pricing.</p>
    <div class="grid grid-cols-1 md:grid-cols-3 gap-8">
      <div class="bg-white rounded-lg border border-gray-200 p-8">
        <h3 class="text-sm font-semibold text-gray-500 uppercase tracking-wider mb-4">Starter</h3>
        <div class="text-4xl font-bold text-gray-900 mb-6">$0<span class="text-sm font-normal text-gray-400">/mo</span></div>
        <ul class="space-y-3 text-gray-600 text-sm mb-8 text-left">
          <li>— Up to 3 projects</li>
          <li>— Community support</li>
          <li>— 1 GB storage</li>
        </ul>
        <a href="#" class="block w-full text-center py-2.5 border border-gray-200 rounded-lg text-gray-700 text-sm font-medium hover:bg-gray-50 transition-colors">Get started</a>
      </div>
      <div class="bg-gray-900 rounded-lg p-8">
        <h3 class="text-sm font-semibold text-gray-400 uppercase tracking-wider mb-4">Pro</h3>
        <div class="text-4xl font-bold text-white mb-6">$29<span class="text-sm font-normal text-gray-400">/mo</span></div>
        <ul class="space-y-3 text-gray-300 text-sm mb-8 text-left">
          <li>— Unlimited projects</li>
          <li>— Priority support</li>
          <li>— 50 GB storage</li>
          <li>— Custom domains</li>
        </ul>
        <a href="#" class="block w-full text-center py-2.5 bg-white text-gray-900 rounded-lg text-sm font-medium hover:bg-gray-100 transition-colors">Get started</a>
      </div>
      <div class="bg-white rounded-lg border border-gray-200 p-8">
        <h3 class="text-sm font-semibold text-gray-500 uppercase tracking-wider mb-4">Enterprise</h3>
        <div class="text-4xl font-bold text-gray-900 mb-6">Custom</div>
        <ul class="space-y-3 text-gray-600 text-sm mb-8 text-left">
          <li>— Everything in Pro</li>
          <li>— Dedicated support</li>
          <li>— SLA & compliance</li>
          <li>— SSO / SAML</li>
        </ul>
        <a href="mailto:hello@example.com" class="block w-full text-center py-2.5 border border-gray-200 rounded-lg text-gray-700 text-sm font-medium hover:bg-gray-50 transition-colors">Contact us</a>
      </div>
    </div>
  </div>
</section>
`
	}
}

// generateCTA returns the CTASection.astro component content.
func (m *SiteManager) generateCTA(text, style string) string {
	switch style {
	case "bold":
		return fmt.Sprintf(`---
---

<section class="bg-gradient-to-r from-indigo-600 to-purple-600 py-20">
  <div class="max-w-4xl mx-auto px-4 sm:px-6 lg:px-8 text-center">
    <h2 class="text-4xl md:text-5xl font-extrabold text-white mb-4">Ready to ship?</h2>
    <p class="text-indigo-200 text-xl mb-10">Join thousands of developers who build with Yaver.</p>
    <a href="#" class="inline-block bg-white text-indigo-700 font-bold text-lg px-10 py-4 rounded-2xl shadow-xl hover:shadow-2xl hover:-translate-y-1 transition-all duration-200">
      %s
    </a>
  </div>
</section>
`, text)

	case "corporate":
		return fmt.Sprintf(`---
---

<section class="bg-slate-900 py-20">
  <div class="max-w-4xl mx-auto px-4 sm:px-6 lg:px-8 text-center">
    <h2 class="text-3xl font-bold text-white mb-4">Ready to get started?</h2>
    <p class="text-slate-400 text-lg mb-10">See how teams are accelerating delivery with our platform.</p>
    <div class="flex flex-wrap justify-center gap-4">
      <a href="#" class="inline-block bg-white text-slate-900 font-semibold px-8 py-3 rounded-lg hover:bg-slate-100 transition-colors">
        %s
      </a>
      <a href="#" class="inline-block border border-slate-600 text-slate-300 font-semibold px-8 py-3 rounded-lg hover:border-slate-400 transition-colors">
        Schedule a demo
      </a>
    </div>
  </div>
</section>
`, text)

	case "playful":
		return fmt.Sprintf(`---
---

<section class="bg-gradient-to-br from-yellow-50 to-pink-50 py-20">
  <div class="max-w-4xl mx-auto px-4 sm:px-6 lg:px-8 text-center">
    <div class="text-5xl mb-5">🎯</div>
    <h2 class="text-4xl font-extrabold text-gray-900 mb-4">Let's build something great!</h2>
    <p class="text-gray-500 text-xl mb-10">Your next big thing starts here.</p>
    <a href="#" class="inline-block bg-gradient-to-r from-purple-500 to-pink-500 text-white font-bold text-lg px-10 py-4 rounded-2xl shadow-lg hover:shadow-xl hover:-translate-y-1 transition-all duration-200">
      %s ✨
    </a>
  </div>
</section>
`, text)

	default: // minimal
		return fmt.Sprintf(`---
---

<section class="bg-gray-900 py-20">
  <div class="max-w-3xl mx-auto px-4 sm:px-6 lg:px-8 text-center">
    <h2 class="text-3xl font-bold text-white mb-4">Start building today</h2>
    <p class="text-gray-400 mb-8">No credit card required.</p>
    <a href="#" class="inline-block bg-white text-gray-900 font-semibold px-8 py-3 rounded-lg hover:bg-gray-100 transition-colors">
      %s
    </a>
  </div>
</section>
`, text)
	}
}

// generateFooter returns the Footer.astro component content.
func (m *SiteManager) generateFooter(productName string) string {
	return fmt.Sprintf(`---
---

<footer class="bg-gray-900 text-gray-400 py-12">
  <div class="max-w-6xl mx-auto px-4 sm:px-6 lg:px-8">
    <div class="grid grid-cols-2 md:grid-cols-4 gap-8 mb-10">
      <div>
        <h4 class="text-white font-semibold mb-4">%s</h4>
        <ul class="space-y-2 text-sm">
          <li><a href="/about" class="hover:text-white transition-colors">About</a></li>
          <li><a href="/blog" class="hover:text-white transition-colors">Blog</a></li>
          <li><a href="/changelog" class="hover:text-white transition-colors">Changelog</a></li>
        </ul>
      </div>
      <div>
        <h4 class="text-white font-semibold mb-4">Product</h4>
        <ul class="space-y-2 text-sm">
          <li><a href="/pricing" class="hover:text-white transition-colors">Pricing</a></li>
          <li><a href="/docs" class="hover:text-white transition-colors">Docs</a></li>
          <li><a href="/roadmap" class="hover:text-white transition-colors">Roadmap</a></li>
        </ul>
      </div>
      <div>
        <h4 class="text-white font-semibold mb-4">Legal</h4>
        <ul class="space-y-2 text-sm">
          <li><a href="/privacy" class="hover:text-white transition-colors">Privacy</a></li>
          <li><a href="/terms" class="hover:text-white transition-colors">Terms</a></li>
        </ul>
      </div>
      <div>
        <h4 class="text-white font-semibold mb-4">Social</h4>
        <ul class="space-y-2 text-sm">
          <li><a href="https://x.com" class="hover:text-white transition-colors">X / Twitter</a></li>
          <li><a href="https://github.com" class="hover:text-white transition-colors">GitHub</a></li>
        </ul>
      </div>
    </div>
    <div class="border-t border-gray-800 pt-6 text-sm text-center">
      © {new Date().getFullYear()} %s. All rights reserved.
    </div>
  </div>
</footer>
`, productName, productName)
}

// generateLayout returns the BaseLayout.astro content.
func (m *SiteManager) generateLayout(productName, style string) string {
	fontLink := `<link rel="preconnect" href="https://fonts.googleapis.com">
  <link href="https://fonts.googleapis.com/css2?family=Inter:wght@400;500;600;700;800;900&display=swap" rel="stylesheet">`

	bodyClass := "font-sans antialiased"
	switch style {
	case "bold", "playful":
		bodyClass = "font-sans antialiased bg-white"
	case "corporate":
		bodyClass = "font-sans antialiased bg-slate-50"
	}

	return fmt.Sprintf(`---
interface Props {
  title?: string;
  description?: string;
}
const { title = '%s', description = '' } = Astro.props;
---

<!doctype html>
<html lang="en">
  <head>
    <meta charset="UTF-8" />
    <meta name="viewport" content="width=device-width, initial-scale=1.0" />
    <meta name="description" content={description} />
    <title>{title}</title>
    %s
    <link rel="icon" type="image/svg+xml" href="/favicon.svg" />
  </head>
  <body class="%s">
    <slot />
  </body>
</html>
`, productName, fontLink, bodyClass)
}

// generateHeader returns the Header.astro component.
func (m *SiteManager) generateHeader(productName string) string {
	return fmt.Sprintf(`---
const nav = [
  { label: 'Pricing', href: '/pricing' },
  { label: 'Blog', href: '/blog' },
  { label: 'Docs', href: '/docs' },
];
---

<header class="border-b border-gray-100 bg-white/80 backdrop-blur-sm sticky top-0 z-50">
  <div class="max-w-6xl mx-auto px-4 sm:px-6 lg:px-8 h-16 flex items-center justify-between">
    <a href="/" class="text-lg font-bold text-gray-900">%s</a>
    <nav class="hidden md:flex items-center gap-6">
      {nav.map(item => (
        <a href={item.href} class="text-sm text-gray-600 hover:text-gray-900 transition-colors">{item.label}</a>
      ))}
    </nav>
    <a href="#" class="text-sm font-semibold bg-gray-900 text-white px-4 py-2 rounded-lg hover:bg-gray-700 transition-colors">
      Get started
    </a>
  </div>
</header>
`, productName)
}

// generateIndexPage creates a simple index page based on the site type.
func (m *SiteManager) generateIndexPage(name, siteType string) string {
	switch siteType {
	case "blog":
		return fmt.Sprintf(`---
import BaseLayout from '../layouts/BaseLayout.astro';
import { getCollection } from 'astro:content';

const posts = (await getCollection('blog'))
  .filter(p => !p.data.draft)
  .sort((a, b) => b.data.publishedAt.valueOf() - a.data.publishedAt.valueOf());
---

<BaseLayout title="%s Blog">
  <main class="max-w-3xl mx-auto px-4 py-20">
    <h1 class="text-4xl font-bold text-gray-900 mb-12">Blog</h1>
    <ul class="space-y-8">
      {posts.map(post => (
        <li>
          <a href={'/blog/' + post.slug} class="group block">
            <h2 class="text-xl font-semibold text-gray-900 group-hover:text-indigo-600 transition-colors mb-1">{post.data.title}</h2>
            <p class="text-sm text-gray-400">{post.data.publishedAt.toLocaleDateString()}</p>
          </a>
        </li>
      ))}
    </ul>
  </main>
</BaseLayout>
`, name)

	case "docs":
		return fmt.Sprintf(`---
import BaseLayout from '../layouts/BaseLayout.astro';
---

<BaseLayout title="%s Docs">
  <main class="max-w-4xl mx-auto px-4 py-20">
    <h1 class="text-4xl font-bold text-gray-900 mb-4">Documentation</h1>
    <p class="text-lg text-gray-500 mb-12">Welcome to the %s documentation.</p>
    <div class="prose prose-gray max-w-none">
      <h2>Getting started</h2>
      <p>Edit <code>src/pages/index.astro</code> to build your docs.</p>
    </div>
  </main>
</BaseLayout>
`, name, name)

	case "changelog":
		return fmt.Sprintf(`---
import BaseLayout from '../layouts/BaseLayout.astro';
---

<BaseLayout title="%s Changelog">
  <main class="max-w-2xl mx-auto px-4 py-20">
    <h1 class="text-4xl font-bold text-gray-900 mb-12">Changelog</h1>
    <div class="space-y-12">
      <article>
        <time class="text-sm text-gray-400">%s</time>
        <h2 class="text-xl font-semibold text-gray-900 mt-1 mb-2">v1.0.0 — Initial release</h2>
        <p class="text-gray-600">First public release of %s.</p>
      </article>
    </div>
  </main>
</BaseLayout>
`, name, time.Now().Format("January 2, 2006"), name)

	default: // landing
		return fmt.Sprintf(`---
import BaseLayout from '../layouts/BaseLayout.astro';
---

<BaseLayout title="%s">
  <main class="min-h-screen bg-white flex items-center justify-center">
    <div class="text-center px-4">
      <h1 class="text-5xl font-bold text-gray-900 mb-4">%s</h1>
      <p class="text-xl text-gray-500 mb-8">Edit src/pages/index.astro to get started.</p>
      <a href="#" class="inline-block bg-gray-900 text-white px-8 py-3 rounded-lg font-semibold hover:bg-gray-700 transition-colors">
        Get started
      </a>
    </div>
  </main>
</BaseLayout>
`, name, name)
	}
}

// generateLandingIndex creates the full landing page index.astro that
// imports all generated section components.
func (m *SiteManager) generateLandingIndex(productName, style string) string {
	return fmt.Sprintf(`---
import BaseLayout from '../layouts/BaseLayout.astro';
import Hero from '../components/Hero.astro';
import FeatureGrid from '../components/FeatureGrid.astro';
import PricingTable from '../components/PricingTable.astro';
import CTASection from '../components/CTASection.astro';
import Footer from '../components/Footer.astro';
---

<BaseLayout title="%s">
  <Hero />
  <FeatureGrid />
  <PricingTable />
  <CTASection />
  <Footer />
</BaseLayout>
`, productName)
}

// generatePageTemplate creates a sensible default page based on the slug.
func (m *SiteManager) generatePageTemplate(slug, title string) string {
	switch slug {
	case "pricing":
		return fmt.Sprintf(`---
import BaseLayout from '../layouts/BaseLayout.astro';
import PricingTable from '../components/PricingTable.astro';
---

<BaseLayout title="%s">
  <main>
    <div class="py-16 text-center">
      <h1 class="text-4xl font-bold text-gray-900">%s</h1>
    </div>
    <PricingTable />
  </main>
</BaseLayout>
`, title, title)

	case "about":
		return fmt.Sprintf(`---
import BaseLayout from '../layouts/BaseLayout.astro';
---

<BaseLayout title="%s">
  <main class="max-w-3xl mx-auto px-4 py-20">
    <h1 class="text-4xl font-bold text-gray-900 mb-6">%s</h1>
    <div class="prose prose-gray max-w-none">
      <p>We're building tools that help developers ship faster.</p>
      <p>Add your story here.</p>
    </div>
  </main>
</BaseLayout>
`, title, title)

	case "contact":
		return fmt.Sprintf(`---
import BaseLayout from '../layouts/BaseLayout.astro';
---

<BaseLayout title="%s">
  <main class="max-w-xl mx-auto px-4 py-20">
    <h1 class="text-4xl font-bold text-gray-900 mb-8">%s</h1>
    <form class="space-y-6">
      <div>
        <label for="name" class="block text-sm font-medium text-gray-700 mb-1">Name</label>
        <input id="name" type="text" class="w-full border border-gray-300 rounded-lg px-4 py-2.5 focus:outline-none focus:ring-2 focus:ring-gray-900" />
      </div>
      <div>
        <label for="email" class="block text-sm font-medium text-gray-700 mb-1">Email</label>
        <input id="email" type="email" class="w-full border border-gray-300 rounded-lg px-4 py-2.5 focus:outline-none focus:ring-2 focus:ring-gray-900" />
      </div>
      <div>
        <label for="message" class="block text-sm font-medium text-gray-700 mb-1">Message</label>
        <textarea id="message" rows="4" class="w-full border border-gray-300 rounded-lg px-4 py-2.5 focus:outline-none focus:ring-2 focus:ring-gray-900"></textarea>
      </div>
      <button type="submit" class="w-full bg-gray-900 text-white font-semibold py-3 rounded-lg hover:bg-gray-700 transition-colors">
        Send message
      </button>
    </form>
  </main>
</BaseLayout>
`, title, title)

	default:
		return fmt.Sprintf(`---
import BaseLayout from '../layouts/BaseLayout.astro';
---

<BaseLayout title="%s">
  <main class="max-w-4xl mx-auto px-4 py-20">
    <h1 class="text-4xl font-bold text-gray-900 mb-6">%s</h1>
    <div class="prose prose-gray max-w-none">
      <p>Edit this page in <code>src/pages/%s.astro</code>.</p>
    </div>
  </main>
</BaseLayout>
`, title, title, m.slugify(title))
	}
}

// ---------------------------------------------------------------------------
// Internal helpers
// ---------------------------------------------------------------------------

type sectionWrap struct{ open, close string }

func sectionWrapper(style string) sectionWrap {
	switch style {
	case "bold":
		return sectionWrap{`<section class="bg-gradient-to-br from-indigo-600 to-purple-700 py-20">`, "</section>"}
	case "corporate":
		return sectionWrap{`<section class="bg-slate-50 border-b border-slate-100 py-20">`, "</section>"}
	case "playful":
		return sectionWrap{`<section class="bg-white py-20 rounded-2xl">`, "</section>"}
	default:
		return sectionWrap{`<section class="bg-white py-20">`, "</section>"}
	}
}

// slugify converts a string to a URL-safe lowercase slug.
func (m *SiteManager) slugify(s string) string {
	s = strings.ToLower(s)
	re := regexp.MustCompile(`[^a-z0-9]+`)
	s = re.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	return s
}

// parseFrontmatter extracts YAML frontmatter and the body from a markdown string.
func (m *SiteManager) parseFrontmatter(content string) (map[string]interface{}, string, error) {
	if !strings.HasPrefix(content, "---") {
		return map[string]interface{}{}, content, nil
	}

	// Find closing ---
	rest := content[3:]
	idx := strings.Index(rest, "\n---")
	if idx == -1 {
		return map[string]interface{}{}, content, nil
	}

	fmRaw := rest[:idx]
	body := strings.TrimPrefix(rest[idx+4:], "\n")

	var fm map[string]interface{}
	if err := yaml.Unmarshal([]byte(fmRaw), &fm); err != nil {
		return nil, "", fmt.Errorf("parse frontmatter yaml: %w", err)
	}
	if fm == nil {
		fm = map[string]interface{}{}
	}

	return fm, body, nil
}

// loadConfig reads the site config from disk. Call with mu already held.
func (m *SiteManager) loadConfig() error {
	data, err := os.ReadFile(m.configPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read site config: %w", err)
	}
	return json.Unmarshal(data, m.config)
}

// saveConfig writes the site config to disk. Call with mu already held.
func (m *SiteManager) saveConfig() error {
	if err := os.MkdirAll(filepath.Dir(m.configPath), 0o755); err != nil {
		return fmt.Errorf("mkdir config dir: %w", err)
	}
	data, err := json.MarshalIndent(m.config, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal site config: %w", err)
	}
	return os.WriteFile(m.configPath, data, 0o644)
}

