package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// ─── Types ────────────────────────────────────────────────────────────────────

// CMSStatus describes the current state of the CMS process.
type CMSStatus struct {
	Running    bool   `json:"running"`
	Engine     string `json:"engine"`     // keystatic | tina | decap | pocketbase
	URL        string `json:"url"`        // content preview URL
	AdminURL   string `json:"admin_url"`  // CMS admin UI URL
	Port       int    `json:"port"`
	ContentDir string `json:"content_dir"`
	Collections int   `json:"collections"`
}

// CMSContent represents a single content entry inside a collection.
type CMSContent struct {
	Collection string    `json:"collection"`
	Slug       string    `json:"slug"`
	Title      string    `json:"title"`
	Draft      bool      `json:"draft"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// cmsPersistedConfig is the on-disk config for the CMS subsystem.
type cmsPersistedConfig struct {
	Engine string `json:"engine"`
	Port   int    `json:"port"`
}

// CMSManager manages the lifecycle of a headless CMS dev server.
type CMSManager struct {
	mu      sync.Mutex
	cmd     *exec.Cmd
	engine  string
	port    int
	workDir string
}

// ─── Constructor ──────────────────────────────────────────────────────────────

// NewCMSManager creates a CMSManager for the given project directory.
// It restores the last-used engine/port from the persisted config file.
func NewCMSManager(workDir string) *CMSManager {
	m := &CMSManager{workDir: workDir}
	m.loadConfig()
	return m
}

// ─── Public API ───────────────────────────────────────────────────────────────

// Start launches the CMS dev server for the given engine on the given port.
// Returns the admin URL on success. If engine is empty, it is auto-detected
// from the project; falling back to "keystatic".
func (m *CMSManager) Start(engine string, port int) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.cmd != nil {
		return "", fmt.Errorf("cms already running (engine=%s port=%d); call stop first", m.engine, m.port)
	}

	if engine == "" {
		engine = detectCMS(m.workDir)
	}
	if port == 0 {
		port = 4001
	}

	if err := m.installCMS(engine); err != nil {
		return "", fmt.Errorf("cms install: %w", err)
	}

	var args []string
	var adminPath string

	switch engine {
	case "keystatic":
		args = []string{"npx", "keystatic", "dev", "--port", fmt.Sprintf("%d", port)}
		adminPath = "/keystatic"
	case "tina":
		args = []string{"npx", "tinacms", "dev", "--port", fmt.Sprintf("%d", port)}
		adminPath = "/admin"
	case "decap":
		// Decap is served as a static file; spin up a simple http-server.
		args = []string{"npx", "http-server", "static/admin", "-p", fmt.Sprintf("%d", port), "--cors"}
		adminPath = "/"
	case "pocketbase":
		// Delegate to PocketBase binary if present.
		pb := filepath.Join(m.workDir, "pocketbase")
		if _, err := os.Stat(pb); err != nil {
			return "", fmt.Errorf("pocketbase binary not found at %s", pb)
		}
		args = []string{pb, "serve", "--http", fmt.Sprintf("127.0.0.1:%d", port)}
		adminPath = "/_/"
	default:
		return "", fmt.Errorf("unknown cms engine %q; supported: keystatic, tina, decap, pocketbase", engine)
	}

	cmd := exec.Command(args[0], args[1:]...) //nolint:gosec
	cmd.Dir = m.workDir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("start cms %s: %w", engine, err)
	}

	m.cmd = cmd
	m.engine = engine
	m.port = port
	m.saveConfig()

	adminURL := fmt.Sprintf("http://localhost:%d%s", port, adminPath)
	return adminURL, nil
}

// Stop terminates the running CMS process.
func (m *CMSManager) Stop() (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.cmd == nil {
		return "cms is not running", nil
	}

	engine := m.engine
	if err := m.cmd.Process.Kill(); err != nil {
		return "", fmt.Errorf("kill cms %s: %w", engine, err)
	}
	_ = m.cmd.Wait()
	m.cmd = nil
	return fmt.Sprintf("cms %s stopped", engine), nil
}

// Status returns the current CMS status including content statistics.
func (m *CMSManager) Status() (*CMSStatus, error) {
	m.mu.Lock()
	running := m.cmd != nil
	engine := m.engine
	port := m.port
	workDir := m.workDir
	m.mu.Unlock()

	if !running && engine == "" {
		engine = detectCMS(workDir)
	}

	status := &CMSStatus{
		Running: running,
		Engine:  engine,
		Port:    port,
	}

	if running {
		status.URL = fmt.Sprintf("http://localhost:%d", port)
		switch engine {
		case "keystatic":
			status.AdminURL = fmt.Sprintf("http://localhost:%d/keystatic", port)
		case "tina":
			status.AdminURL = fmt.Sprintf("http://localhost:%d/admin", port)
		case "decap":
			status.AdminURL = fmt.Sprintf("http://localhost:%d/", port)
		case "pocketbase":
			status.AdminURL = fmt.Sprintf("http://localhost:%d/_/", port)
		}

		// Verify process is actually responsive.
		client := &http.Client{Timeout: 2 * time.Second}
		if resp, err := client.Get(status.URL); err == nil {
			resp.Body.Close()
		} else {
			status.Running = false
		}
	}

	// Scan content directory for collections count.
	contentDir := filepath.Join(workDir, "src", "content")
	if info, err := os.Stat(contentDir); err == nil && info.IsDir() {
		status.ContentDir = contentDir
		entries, _ := os.ReadDir(contentDir)
		status.Collections = len(entries)
	}

	return status, nil
}

// Content lists all entries in a content collection.
// It looks for markdown/MDX files under src/content/{collection}/.
func (m *CMSManager) Content(collection string) ([]CMSContent, error) {
	m.mu.Lock()
	workDir := m.workDir
	m.mu.Unlock()

	collDir := filepath.Join(workDir, "src", "content", collection)
	if _, err := os.Stat(collDir); err != nil {
		return nil, fmt.Errorf("collection %q not found at %s", collection, collDir)
	}

	return scanContentDir(collDir, collection)
}

// Setup generates CMS configuration files and prints integration instructions
// for the given engine and framework combination.
func (m *CMSManager) Setup(engine, framework string) (string, error) {
	if engine == "" {
		engine = "keystatic"
	}
	if framework == "" {
		framework = "astro"
	}

	switch engine {
	case "keystatic":
		config := generateKeystatic(framework)
		dest := filepath.Join(m.workDir, "keystatic.config.ts")
		if err := os.WriteFile(dest, []byte(config), 0o644); err != nil {
			return "", fmt.Errorf("write keystatic config: %w", err)
		}
		return fmt.Sprintf(
			"keystatic.config.ts written to %s\n\nNext steps:\n"+
				"  1. npm add @keystatic/core @keystatic/astro\n"+
				"  2. Add the Keystatic integration to astro.config.mjs\n"+
				"  3. Run: yaver cms start keystatic",
			dest,
		), nil

	case "tina":
		steps := "Tina CMS setup:\n" +
			"  1. npx @tinacms/cli@latest init\n" +
			"  2. Follow the prompts to configure your schema\n" +
			"  3. Run: yaver cms start tina"
		return steps, nil

	case "decap":
		configDir := filepath.Join(m.workDir, "static", "admin")
		if err := os.MkdirAll(configDir, 0o755); err != nil {
			return "", fmt.Errorf("create decap admin dir: %w", err)
		}
		cfg := decapDefaultConfig(framework)
		dest := filepath.Join(configDir, "config.yml")
		if err := os.WriteFile(dest, []byte(cfg), 0o644); err != nil {
			return "", fmt.Errorf("write decap config: %w", err)
		}
		return fmt.Sprintf(
			"Decap CMS config written to %s\n\nNext steps:\n"+
				"  1. Add an index.html to static/admin/ that loads the Decap CMS widget\n"+
				"  2. Configure your Git gateway / OAuth backend\n"+
				"  3. Run: yaver cms start decap",
			dest,
		), nil

	default:
		return "", fmt.Errorf("setup not supported for engine %q", engine)
	}
}

// ─── Internal helpers ─────────────────────────────────────────────────────────

// detectCMS infers the CMS engine from well-known config files in workDir.
func detectCMS(workDir string) string {
	probes := []struct {
		file   string
		engine string
	}{
		{"keystatic.config.ts", "keystatic"},
		{"keystatic.config.js", "keystatic"},
		{filepath.Join("tina", "config.ts"), "tina"},
		{filepath.Join("tina", "config.js"), "tina"},
		{filepath.Join("static", "admin", "config.yml"), "decap"},
		{"pocketbase", "pocketbase"},
	}
	for _, p := range probes {
		if _, err := os.Stat(filepath.Join(workDir, p.file)); err == nil {
			return p.engine
		}
	}
	return "keystatic"
}

// scanContentDir walks dir for *.md / *.mdx files and parses their frontmatter.
func scanContentDir(dir, collection string) ([]CMSContent, error) {
	var entries []CMSContent

	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(path))
		if ext != ".md" && ext != ".mdx" {
			return nil
		}

		slug := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
		fm, err := parseFrontmatter(path)
		if err != nil {
			// Include the entry even if frontmatter is unparseable.
			fm = map[string]string{}
		}

		info, _ := d.Info()
		var updatedAt time.Time
		if info != nil {
			updatedAt = info.ModTime()
		}

		title := fm["title"]
		if title == "" {
			title = slug
		}
		draft := strings.EqualFold(fm["draft"], "true")

		entries = append(entries, CMSContent{
			Collection: collection,
			Slug:       slug,
			Title:      title,
			Draft:      draft,
			UpdatedAt:  updatedAt,
		})
		return nil
	})
	return entries, err
}

// parseFrontmatter reads a markdown file and extracts YAML frontmatter key/value
// pairs. Only simple string values are supported (no nesting).
func parseFrontmatter(path string) (map[string]string, error) {
	f, err := os.Open(path) //nolint:gosec
	if err != nil {
		return nil, err
	}
	defer f.Close()

	result := make(map[string]string)
	scanner := bufio.NewScanner(f)

	// Expect opening "---"
	if !scanner.Scan() {
		return result, nil
	}
	if strings.TrimSpace(scanner.Text()) != "---" {
		return result, nil
	}

	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) == "---" {
			break
		}
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		val := strings.Trim(strings.TrimSpace(parts[1]), `"'`)
		result[key] = val
	}
	return result, scanner.Err()
}

// installCMS ensures the required npm packages are installed for the engine.
func (m *CMSManager) installCMS(engine string) error {
	switch engine {
	case "keystatic":
		return m.npmInstallIfMissing("@keystatic/core", "@keystatic/core")
	case "tina":
		return m.npmInstallIfMissing("tinacms", "tinacms")
	case "decap":
		return m.npmInstallIfMissing("netlify-cms-app", "netlify-cms-app")
	case "pocketbase":
		// PocketBase is a standalone binary; nothing to npm-install.
		return nil
	default:
		return nil
	}
}

// npmInstallIfMissing runs `npm add pkg` only when the module is absent from node_modules.
func (m *CMSManager) npmInstallIfMissing(module, pkg string) error {
	modPath := filepath.Join(m.workDir, "node_modules", module)
	if _, err := os.Stat(modPath); err == nil {
		return nil // already installed
	}
	cmd := exec.Command("npm", "add", pkg) //nolint:gosec
	cmd.Dir = m.workDir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("npm add %s: %w", pkg, err)
	}
	return nil
}

// generateKeystatic returns a minimal keystatic.config.ts for the given framework.
func generateKeystatic(framework string) string {
	var imports, integration string
	switch framework {
	case "astro":
		imports = `import { config, collection, fields } from '@keystatic/core';
import { withAstroKeystatic } from '@keystatic/astro';`
		integration = "// Add withAstroKeystatic() to your astro.config.mjs integrations array."
	case "nextjs", "next":
		imports = `import { config, collection, fields } from '@keystatic/core';`
		integration = "// Wrap your Next.js app with the Keystatic handler in app/keystatic/[...params]/route.ts"
	default:
		imports = `import { config, collection, fields } from '@keystatic/core';`
		integration = ""
	}

	return fmt.Sprintf(`%s

%s

export default config({
  storage: {
    kind: 'local',
  },
  collections: {
    posts: collection({
      label: 'Posts',
      slugField: 'title',
      path: 'src/content/posts/*',
      format: { contentField: 'content' },
      schema: {
        title: fields.slug({ name: { label: 'Title' } }),
        publishedAt: fields.date({ label: 'Published At' }),
        draft: fields.checkbox({ label: 'Draft', defaultValue: true }),
        content: fields.markdoc({
          label: 'Content',
        }),
      },
    }),
  },
});
`, imports, integration)
}

// decapDefaultConfig returns a minimal Decap CMS config.yml.
func decapDefaultConfig(framework string) string {
	mediaFolder := "public/uploads"
	if framework == "astro" {
		mediaFolder = "public/uploads"
	}
	return fmt.Sprintf(`backend:
  name: git-gateway
  branch: main

media_folder: %s
public_folder: /uploads

collections:
  - name: posts
    label: Posts
    folder: src/content/posts
    create: true
    slug: "{{slug}}"
    fields:
      - { label: Title, name: title, widget: string }
      - { label: Draft, name: draft, widget: boolean, default: true }
      - { label: Published At, name: publishedAt, widget: datetime, required: false }
      - { label: Body, name: body, widget: markdown }
`, mediaFolder)
}

// ─── Config persistence ───────────────────────────────────────────────────────

func (m *CMSManager) configPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, configDirName, "cms.json")
}

func (m *CMSManager) loadConfig() {
	data, err := os.ReadFile(m.configPath())
	if err != nil {
		return
	}
	var cfg cmsPersistedConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return
	}
	if m.engine == "" && cfg.Engine != "" {
		m.engine = cfg.Engine
	}
	if m.port == 0 && cfg.Port != 0 {
		m.port = cfg.Port
	}
}

func (m *CMSManager) saveConfig() {
	cfg := cmsPersistedConfig{Engine: m.engine, Port: m.port}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return
	}
	path := m.configPath()
	_ = os.MkdirAll(filepath.Dir(path), 0o700)
	_ = os.WriteFile(path, data, 0o600)
}
