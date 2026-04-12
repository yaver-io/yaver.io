package main

// analytics_selfhost.go — AnalyticsManager for self-hosted analytics stacks.
//
// Manages PostHog, Plausible CE, or Umami via Docker Compose so a solo dev
// can spin up a full analytics backend with a single command:
//
//   yaver analytics start              # starts Plausible (default, lightest)
//   yaver analytics start posthog      # PostHog (most features, needs ~4 GB RAM)
//   yaver analytics start umami        # Umami (simplest, lightest JS footprint)
//   yaver analytics stop
//   yaver analytics status
//   yaver analytics setup next         # emit Next.js snippet for copy-paste
//
// All state lives in ~/.yaver/analytics/{engine}/.
// Docker Compose files are written on first start and left in place so the
// user can customise them.

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// ---------------------------------------------------------------------------
// Public types
// ---------------------------------------------------------------------------

// AnalyticsStatus describes the current state of the self-hosted stack.
type AnalyticsStatus struct {
	Running bool   `json:"running"`
	Engine  string `json:"engine"`
	Port    int    `json:"port"`
	URL     string `json:"url"`
	APIKey  string `json:"apiKey,omitempty"`
	Memory  string `json:"memory"`
}

// AnalyticsEvent is a single captured event returned by Events().
type AnalyticsEvent struct {
	Event      string                 `json:"event"`
	Timestamp  string                 `json:"timestamp"`
	PersonID   string                 `json:"personId,omitempty"`
	Properties map[string]interface{} `json:"properties,omitempty"`
}

// AnalyticsPerson is a tracked user record.
type AnalyticsPerson struct {
	ID         string                 `json:"id"`
	Properties map[string]interface{} `json:"properties,omitempty"`
	CreatedAt  string                 `json:"createdAt"`
}

// AnalyticsDashboard holds key aggregate metrics.
type AnalyticsDashboard struct {
	Engine    string `json:"engine"`
	Pageviews int    `json:"pageviews"`
	Visitors  int    `json:"visitors"`
	TopPages  []struct {
		Path  string `json:"path"`
		Views int    `json:"views"`
	} `json:"topPages"`
	TopEvents []struct {
		Name  string `json:"name"`
		Count int    `json:"count"`
	} `json:"topEvents"`
}

// ---------------------------------------------------------------------------
// Internal config persisted to disk
// ---------------------------------------------------------------------------

type analyticsConfig struct {
	Engine   string `json:"engine"`
	Port     int    `json:"port"`
	AdminKey string `json:"adminKey,omitempty"` // PostHog personal API key
	// Plausible/Umami credentials
	AdminUser string `json:"adminUser,omitempty"`
	AdminPass string `json:"adminPass,omitempty"`
	// Umami website ID (needed for stats API)
	UmamiSiteID string `json:"umamiSiteId,omitempty"`
}

// ---------------------------------------------------------------------------
// AnalyticsManager
// ---------------------------------------------------------------------------

// AnalyticsManager manages a self-hosted analytics stack (PostHog, Plausible, Umami)
// running via Docker Compose inside ~/.yaver/analytics/{engine}/.
type AnalyticsManager struct {
	baseDir string // ~/.yaver/analytics
}

// NewAnalyticsManager returns an AnalyticsManager.  baseDir is created lazily.
func NewAnalyticsManager() *AnalyticsManager {
	dir, _ := ConfigDir()
	return &AnalyticsManager{
		baseDir: filepath.Join(dir, "analytics"),
	}
}

// ---------------------------------------------------------------------------
// Start / Stop
// ---------------------------------------------------------------------------

// Start launches the analytics stack for the given engine via Docker Compose.
// Supported engines: "posthog", "plausible", "umami". Default: "plausible".
func (m *AnalyticsManager) Start(engine string) error {
	if engine == "" {
		engine = "plausible"
	}
	engine = strings.ToLower(engine)
	switch engine {
	case "posthog", "plausible", "umami":
	default:
		return fmt.Errorf("unknown analytics engine %q (supported: posthog, plausible, umami)", engine)
	}

	// Check Docker is available.
	if _, err := exec.LookPath("docker"); err != nil {
		return fmt.Errorf("docker not found in PATH — install Docker Desktop or Docker Engine first")
	}

	// Warn for heavy stacks.
	if engine == "posthog" {
		fmt.Println("Warning: PostHog requires approximately 4 GB of RAM. Make sure Docker Desktop has enough memory allocated.")
	}

	engineDir := filepath.Join(m.baseDir, engine)
	if err := os.MkdirAll(engineDir, 0700); err != nil {
		return fmt.Errorf("create engine dir: %w", err)
	}

	// Generate / load config.
	cfg, err := m.loadOrInitConfig(engine, engineDir)
	if err != nil {
		return fmt.Errorf("init config: %w", err)
	}

	// Write docker-compose.yml if it doesn't exist.
	composeFile := filepath.Join(engineDir, "docker-compose.yml")
	if _, err := os.Stat(composeFile); os.IsNotExist(err) {
		compose, err := generateCompose(engine, cfg)
		if err != nil {
			return fmt.Errorf("generate compose: %w", err)
		}
		if err := os.WriteFile(composeFile, []byte(compose), 0600); err != nil {
			return fmt.Errorf("write compose: %w", err)
		}
	}

	// docker compose up -d
	cmd := exec.Command("docker", "compose", "-f", composeFile, "up", "-d", "--pull", "missing")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("docker compose up: %w", err)
	}

	// Save active engine choice.
	globalCfg := filepath.Join(m.baseDir, "config.json")
	data, _ := json.MarshalIndent(map[string]string{"engine": engine}, "", "  ")
	_ = os.WriteFile(globalCfg, data, 0600)

	fmt.Printf("\nAnalytics stack (%s) started.\n", engine)
	fmt.Printf("Web UI: http://localhost:%d\n", cfg.Port)
	if cfg.AdminUser != "" {
		fmt.Printf("Username: %s\n", cfg.AdminUser)
		fmt.Printf("Password: %s\n", cfg.AdminPass)
	}
	return nil
}

// Stop shuts down the active analytics stack.
func (m *AnalyticsManager) Stop() error {
	engine, err := m.activeEngine()
	if err != nil {
		return err
	}
	composeFile := filepath.Join(m.baseDir, engine, "docker-compose.yml")
	if _, err := os.Stat(composeFile); os.IsNotExist(err) {
		return fmt.Errorf("no compose file found for engine %q", engine)
	}
	cmd := exec.Command("docker", "compose", "-f", composeFile, "down")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// ---------------------------------------------------------------------------
// Status
// ---------------------------------------------------------------------------

// Status returns the current state of the active analytics stack.
func (m *AnalyticsManager) Status() (*AnalyticsStatus, error) {
	engine, _ := m.activeEngine()
	if engine == "" {
		return &AnalyticsStatus{Running: false}, nil
	}

	cfg, err := m.loadConfig(engine)
	if err != nil {
		return &AnalyticsStatus{Running: false, Engine: engine}, nil
	}

	// Check if container is actually running via docker compose ps.
	composeFile := filepath.Join(m.baseDir, engine, "docker-compose.yml")
	running := false
	if _, err := os.Stat(composeFile); err == nil {
		out, err := exec.Command("docker", "compose", "-f", composeFile, "ps", "--services", "--filter", "status=running").Output()
		if err == nil && len(strings.TrimSpace(string(out))) > 0 {
			running = true
		}
	}

	mem := m.memoryUsage(engine)

	return &AnalyticsStatus{
		Running: running,
		Engine:  engine,
		Port:    cfg.Port,
		URL:     fmt.Sprintf("http://localhost:%d", cfg.Port),
		APIKey:  cfg.AdminKey,
		Memory:  mem,
	}, nil
}

// memoryUsage returns a human-readable memory usage string for the stack.
func (m *AnalyticsManager) memoryUsage(engine string) string {
	// docker stats --no-stream --format "{{.MemUsage}}" for containers whose
	// names contain the engine name.
	out, err := exec.Command("docker", "stats", "--no-stream", "--format", "{{.Name}}: {{.MemUsage}}").Output()
	if err != nil {
		return "unknown"
	}
	var lines []string
	for _, line := range strings.Split(string(out), "\n") {
		if strings.Contains(strings.ToLower(line), engine) {
			lines = append(lines, strings.TrimSpace(line))
		}
	}
	if len(lines) == 0 {
		return "not running"
	}
	return strings.Join(lines, "; ")
}

// ---------------------------------------------------------------------------
// Events
// ---------------------------------------------------------------------------

// Events queries captured events from the analytics engine.
// personID and last are optional filters (last e.g. "7d", "24h").
func (m *AnalyticsManager) Events(eventName, personID string, last string) ([]AnalyticsEvent, error) {
	engine, err := m.activeEngine()
	if err != nil {
		return nil, err
	}
	cfg, err := m.loadConfig(engine)
	if err != nil {
		return nil, err
	}

	switch engine {
	case "plausible":
		return m.plausibleEvents(cfg, eventName, last)
	case "posthog":
		return m.posthogEvents(cfg, eventName, personID, last)
	case "umami":
		return m.umamiEvents(cfg, eventName, last)
	}
	return nil, fmt.Errorf("unsupported engine %q", engine)
}

// ---------------------------------------------------------------------------
// Persons
// ---------------------------------------------------------------------------

// Persons lists tracked users (PostHog and Umami only; Plausible is cookieless).
func (m *AnalyticsManager) Persons(limit int) ([]AnalyticsPerson, error) {
	engine, err := m.activeEngine()
	if err != nil {
		return nil, err
	}
	cfg, err := m.loadConfig(engine)
	if err != nil {
		return nil, err
	}

	if limit <= 0 {
		limit = 100
	}

	switch engine {
	case "posthog":
		return m.posthogPersons(cfg, limit)
	case "umami":
		// Umami exposes sessions, not named persons.
		return m.umamiSessions(cfg, limit)
	default:
		return nil, fmt.Errorf("persons API is not supported for %q (it is cookieless)", engine)
	}
}

// ---------------------------------------------------------------------------
// Feature flags (PostHog only)
// ---------------------------------------------------------------------------

// Flags manages PostHog feature flags.
// action: "list", "create", "enable", "disable".
func (m *AnalyticsManager) Flags(action, name string, enabled bool) (interface{}, error) {
	engine, err := m.activeEngine()
	if err != nil {
		return nil, err
	}
	if engine != "posthog" {
		return nil, fmt.Errorf("feature flags are only supported with the posthog engine (current: %q)", engine)
	}
	cfg, err := m.loadConfig(engine)
	if err != nil {
		return nil, err
	}
	return m.posthogFlags(cfg, action, name, enabled)
}

// ---------------------------------------------------------------------------
// Dashboard
// ---------------------------------------------------------------------------

// Dashboard returns key aggregate metrics from the active analytics engine.
func (m *AnalyticsManager) Dashboard() (*AnalyticsDashboard, error) {
	engine, err := m.activeEngine()
	if err != nil {
		return nil, err
	}
	cfg, err := m.loadConfig(engine)
	if err != nil {
		return nil, err
	}

	switch engine {
	case "plausible":
		return m.plausibleDashboard(cfg)
	case "posthog":
		return m.posthogDashboard(cfg)
	case "umami":
		return m.umamiDashboard(cfg)
	}
	return nil, fmt.Errorf("unsupported engine %q", engine)
}

// ---------------------------------------------------------------------------
// Setup snippet
// ---------------------------------------------------------------------------

// Setup returns a copy-pasteable integration snippet for the given frontend
// framework ("next", "react", "vue", "html"). If framework is empty it
// defaults to "next".
func (m *AnalyticsManager) Setup(framework string) string {
	if framework == "" {
		framework = "next"
	}
	framework = strings.ToLower(framework)

	engine, _ := m.activeEngine()
	if engine == "" {
		engine = "plausible"
	}
	cfg, _ := m.loadConfig(engine)

	port := 8000
	apiKey := ""
	if cfg != nil {
		port = cfg.Port
		apiKey = cfg.AdminKey
	}
	host := fmt.Sprintf("http://localhost:%d", port)

	switch engine {
	case "plausible":
		return m.plausibleSnippet(framework, host)
	case "posthog":
		return m.posthogSnippet(framework, host, apiKey)
	case "umami":
		return m.umamiSnippet(framework, host, cfg)
	}
	return "// Unsupported engine"
}

// ---------------------------------------------------------------------------
// Snippet generators
// ---------------------------------------------------------------------------

func (m *AnalyticsManager) plausibleSnippet(framework, host string) string {
	domain := "localhost"
	script := fmt.Sprintf(`<script defer data-domain="%s" src="%s/js/script.js"></script>`, domain, host)

	switch framework {
	case "next":
		return fmt.Sprintf(`// app/layout.tsx — add inside <head>
// Install: no npm package needed, just a <script> tag.

import Script from 'next/script'

export default function RootLayout({ children }) {
  return (
    <html>
      <head>
        <Script
          defer
          data-domain="%s"
          src="%s/js/script.js"
          strategy="afterInteractive"
        />
      </head>
      <body>{children}</body>
    </html>
  )
}`, domain, host)
	case "react":
		return fmt.Sprintf(`// index.html — add to <head>
%s

// Optional: track custom events from JS
window.plausible = window.plausible || function() {
  (window.plausible.q = window.plausible.q || []).push(arguments)
}
plausible('purchase', { props: { plan: 'pro' } })`, script)
	case "vue":
		return fmt.Sprintf(`// main.ts
import { createApp } from 'vue'
import App from './App.vue'

const app = createApp(App)
// Add to index.html <head>:
// %s
app.mount('#app')`, script)
	default:
		return fmt.Sprintf(`<!-- Add to your <head> tag -->
%s`, script)
	}
}

func (m *AnalyticsManager) posthogSnippet(framework, host, apiKey string) string {
	if apiKey == "" {
		apiKey = "YOUR_POSTHOG_API_KEY"
	}
	switch framework {
	case "next":
		return fmt.Sprintf(`// lib/posthog.ts
import posthog from 'posthog-js'

export function initPostHog() {
  posthog.init('%s', {
    api_host: '%s',
    loaded: (ph) => {
      if (process.env.NODE_ENV === 'development') ph.opt_out_capturing()
    }
  })
}

// app/layout.tsx
'use client'
import { useEffect } from 'react'
import { initPostHog } from '@/lib/posthog'

export default function RootLayout({ children }) {
  useEffect(() => { initPostHog() }, [])
  return <html><body>{children}</body></html>
}

// Capture events anywhere:
import posthog from 'posthog-js'
posthog.capture('my_event', { property: 'value' })`, apiKey, host)
	case "react":
		return fmt.Sprintf(`// Install: npm install posthog-js
import posthog from 'posthog-js'

posthog.init('%s', { api_host: '%s' })

// Capture events:
posthog.capture('my_event', { property: 'value' })
posthog.identify('user_123', { email: 'user@example.com' })`, apiKey, host)
	default:
		return fmt.Sprintf(`<script>
  !function(t,e){/* PostHog snippet */}(window, document)
  posthog.init('%s', { api_host: '%s' })
</script>`, apiKey, host)
	}
}

func (m *AnalyticsManager) umamiSnippet(framework, host string, cfg *analyticsConfig) string {
	siteID := "YOUR_UMAMI_WEBSITE_ID"
	if cfg != nil && cfg.UmamiSiteID != "" {
		siteID = cfg.UmamiSiteID
	}
	script := fmt.Sprintf(`<script async defer data-website-id="%s" src="%s/script.js"></script>`, siteID, host)
	switch framework {
	case "next":
		return fmt.Sprintf(`// app/layout.tsx
import Script from 'next/script'

export default function RootLayout({ children }) {
  return (
    <html>
      <head>
        <Script
          async
          defer
          data-website-id="%s"
          src="%s/script.js"
          strategy="afterInteractive"
        />
      </head>
      <body>{children}</body>
    </html>
  )
}

// Track custom events:
import { track } from '@umami/react'
track('signup', { plan: 'pro' })`, siteID, host)
	default:
		return fmt.Sprintf(`<!-- Add to <head> -->
%s

<!-- Track events -->
<script>
  umami.track('signup', { plan: 'pro' })
</script>`, script)
	}
}

// ---------------------------------------------------------------------------
// Docker Compose YAML generators
// ---------------------------------------------------------------------------

func generateCompose(engine string, cfg *analyticsConfig) (string, error) {
	switch engine {
	case "plausible":
		return generatePlausibleCompose(cfg), nil
	case "umami":
		return generateUmamiCompose(cfg), nil
	case "posthog":
		return generatePostHogCompose(cfg), nil
	}
	return "", fmt.Errorf("unknown engine: %s", engine)
}

func generatePlausibleCompose(cfg *analyticsConfig) string {
	secretKeyBase := randomHex(64)
	return fmt.Sprintf(`version: "3"

services:
  mail:
    image: bytemark/smtp
    restart: always

  plausible_db:
    image: postgres:16-alpine
    restart: always
    volumes:
      - db-data:/var/lib/postgresql/data
    environment:
      POSTGRES_PASSWORD: plausible

  plausible_events_db:
    image: clickhouse/clickhouse-server:24.3.3.102-alpine
    restart: always
    volumes:
      - event-data:/var/lib/clickhouse
      - ./clickhouse/clickhouse-config.xml:/etc/clickhouse-server/config.d/logging.xml:ro
      - ./clickhouse/clickhouse-user-config.xml:/etc/clickhouse-server/users.d/logging.xml:ro
    ulimits:
      nofile:
        soft: 262144
        hard: 262144

  plausible:
    image: ghcr.io/plausible/community-edition:v2.1.1
    restart: always
    command: sh -c "sleep 10 && /entrypoint.sh db createdb && /entrypoint.sh db migrate && /entrypoint.sh run"
    depends_on:
      - plausible_db
      - plausible_events_db
      - mail
    ports:
      - "%d:8000"
    environment:
      BASE_URL: http://localhost:%d
      SECRET_KEY_BASE: %s
      DATABASE_URL: postgres://postgres:plausible@plausible_db:5432/plausible_db
      CLICKHOUSE_DATABASE_URL: http://plausible_events_db:8123/plausible_events
      MAILER_EMAIL: admin@localhost
      SMTP_HOST_ADDR: mail
      SMTP_HOST_PORT: 25
      DISABLE_REGISTRATION: invite_only

volumes:
  db-data:
  event-data:
`, cfg.Port, cfg.Port, secretKeyBase)
}

func generateUmamiCompose(cfg *analyticsConfig) string {
	dbPass := randomHex(16)
	return fmt.Sprintf(`version: "3"

services:
  umami:
    image: ghcr.io/umami-software/umami:postgresql-latest
    restart: always
    ports:
      - "%d:3000"
    environment:
      DATABASE_URL: postgresql://umami:%s@umami_db:5432/umami
      DATABASE_TYPE: postgresql
      APP_SECRET: %s
    depends_on:
      - umami_db

  umami_db:
    image: postgres:15-alpine
    restart: always
    environment:
      POSTGRES_DB: umami
      POSTGRES_USER: umami
      POSTGRES_PASSWORD: %s
    volumes:
      - umami-db-data:/var/lib/postgresql/data

volumes:
  umami-db-data:
`, cfg.Port, dbPass, randomHex(32), dbPass)
}

func generatePostHogCompose(cfg *analyticsConfig) string {
	// PostHog's official all-in-one compose is heavy. We generate a minimal
	// self-contained compose pointing at the official PostHog image with Postgres
	// and Redis. For production use, users should pull the official compose from
	// https://github.com/PostHog/posthog.
	secretKey := randomHex(32)
	return fmt.Sprintf(`version: "3"

# NOTE: This is a minimal dev-only PostHog setup.
# For production, use https://posthog.com/docs/self-host
services:
  db:
    image: postgres:15-alpine
    environment:
      POSTGRES_USER: posthog
      POSTGRES_PASSWORD: posthog
      POSTGRES_DB: posthog
    volumes:
      - posthog-db:/var/lib/postgresql/data

  redis:
    image: redis:7-alpine
    restart: always

  posthog:
    image: posthog/posthog:latest
    restart: always
    depends_on:
      - db
      - redis
    ports:
      - "%d:8000"
    environment:
      DATABASE_URL: postgres://posthog:posthog@db:5432/posthog
      REDIS_URL: redis://redis:6379/
      SECRET_KEY: %s
      DISABLE_SECURE_SSL_REDIRECT: "true"
      IS_BEHIND_PROXY: "true"
      TRUST_ALL_PROXIES: "true"
      SITE_URL: http://localhost:%d
      JS_URL: http://localhost:%d

  posthog_worker:
    image: posthog/posthog:latest
    restart: always
    depends_on:
      - db
      - redis
    command: ./bin/plugin-server --no-restart-loop
    environment:
      DATABASE_URL: postgres://posthog:posthog@db:5432/posthog
      REDIS_URL: redis://redis:6379/
      SECRET_KEY: %s

volumes:
  posthog-db:
`, cfg.Port, secretKey, cfg.Port, cfg.Port, secretKey)
}

// ---------------------------------------------------------------------------
// Config helpers
// ---------------------------------------------------------------------------

func (m *AnalyticsManager) loadOrInitConfig(engine, engineDir string) (*analyticsConfig, error) {
	cfgPath := filepath.Join(engineDir, "config.json")
	if data, err := os.ReadFile(cfgPath); err == nil {
		var cfg analyticsConfig
		if err := json.Unmarshal(data, &cfg); err == nil && cfg.Port > 0 {
			return &cfg, nil
		}
	}

	// Generate new config.
	cfg := &analyticsConfig{Engine: engine}
	switch engine {
	case "plausible":
		cfg.Port = 8000
		cfg.AdminUser = "admin@localhost"
		cfg.AdminPass = randomHex(8)
	case "umami":
		cfg.Port = 3100
		cfg.AdminUser = "admin"
		cfg.AdminPass = randomHex(8)
	case "posthog":
		cfg.Port = 8000
		cfg.AdminUser = "admin@localhost"
		cfg.AdminPass = randomHex(8)
	}

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(cfgPath, data, 0600); err != nil {
		return nil, err
	}
	return cfg, nil
}

func (m *AnalyticsManager) loadConfig(engine string) (*analyticsConfig, error) {
	cfgPath := filepath.Join(m.baseDir, engine, "config.json")
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		return nil, fmt.Errorf("config not found for engine %q — run `yaver analytics start %s` first", engine, engine)
	}
	var cfg analyticsConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	return &cfg, nil
}

func (m *AnalyticsManager) activeEngine() (string, error) {
	globalCfg := filepath.Join(m.baseDir, "config.json")
	data, err := os.ReadFile(globalCfg)
	if err != nil {
		return "", fmt.Errorf("no analytics stack started yet — run `yaver analytics start`")
	}
	var cfg map[string]string
	if err := json.Unmarshal(data, &cfg); err != nil {
		return "", fmt.Errorf("parse global config: %w", err)
	}
	engine := cfg["engine"]
	if engine == "" {
		return "", fmt.Errorf("no active engine found in config")
	}
	return engine, nil
}

// ---------------------------------------------------------------------------
// Plausible API calls
// ---------------------------------------------------------------------------

func (m *AnalyticsManager) plausibleEvents(cfg *analyticsConfig, eventName, period string) ([]AnalyticsEvent, error) {
	// Plausible Stats API: GET /api/v1/stats/breakdown
	if period == "" {
		period = "30d"
	}
	url := fmt.Sprintf("http://localhost:%d/api/v1/stats/breakdown?site_id=localhost&period=%s&property=event:name", cfg.Port, period)
	if eventName != "" {
		url += "&filters=event:name==" + eventName
	}
	body, err := plausibleRequest("GET", url, cfg.AdminKey, nil)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Results []struct {
			Name    string `json:"event:name"`
			Visitors int   `json:"visitors"`
		} `json:"results"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	events := make([]AnalyticsEvent, 0, len(resp.Results))
	for _, r := range resp.Results {
		events = append(events, AnalyticsEvent{
			Event:     r.Name,
			Timestamp: time.Now().Format(time.RFC3339),
			Properties: map[string]interface{}{
				"visitors": r.Visitors,
			},
		})
	}
	return events, nil
}

func (m *AnalyticsManager) plausibleDashboard(cfg *analyticsConfig) (*AnalyticsDashboard, error) {
	period := "30d"
	base := fmt.Sprintf("http://localhost:%d/api/v1/stats", cfg.Port)

	// Aggregate stats.
	aggURL := fmt.Sprintf("%s/aggregate?site_id=localhost&period=%s&metrics=pageviews,visitors", base, period)
	aggBody, err := plausibleRequest("GET", aggURL, cfg.AdminKey, nil)
	if err != nil {
		return nil, err
	}
	var aggResp struct {
		Results struct {
			Pageviews struct{ Value int } `json:"pageviews"`
			Visitors  struct{ Value int } `json:"visitors"`
		} `json:"results"`
	}
	_ = json.Unmarshal(aggBody, &aggResp)

	// Top pages.
	pagesURL := fmt.Sprintf("%s/breakdown?site_id=localhost&period=%s&property=event:page&limit=5", base, period)
	pagesBody, err := plausibleRequest("GET", pagesURL, cfg.AdminKey, nil)
	dash := &AnalyticsDashboard{
		Engine:    "plausible",
		Pageviews: aggResp.Results.Pageviews.Value,
		Visitors:  aggResp.Results.Visitors.Value,
	}
	if err == nil {
		var pResp struct {
			Results []struct {
				Page     string `json:"event:page"`
				Visitors int    `json:"visitors"`
			} `json:"results"`
		}
		if json.Unmarshal(pagesBody, &pResp) == nil {
			for _, p := range pResp.Results {
				dash.TopPages = append(dash.TopPages, struct {
					Path  string `json:"path"`
					Views int    `json:"views"`
				}{Path: p.Page, Views: p.Visitors})
			}
		}
	}
	return dash, nil
}

func plausibleRequest(method, url, apiKey string, body []byte) ([]byte, error) {
	var reqBody io.Reader
	if body != nil {
		reqBody = bytes.NewReader(body)
	}
	req, err := http.NewRequest(method, url, reqBody)
	if err != nil {
		return nil, err
	}
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("API error %d: %s", resp.StatusCode, string(data))
	}
	return data, nil
}

// ---------------------------------------------------------------------------
// PostHog API calls
// ---------------------------------------------------------------------------

func (m *AnalyticsManager) posthogEvents(cfg *analyticsConfig, eventName, personID, after string) ([]AnalyticsEvent, error) {
	url := fmt.Sprintf("http://localhost:%d/api/event/?limit=100", cfg.Port)
	if eventName != "" {
		url += "&event=" + eventName
	}
	if personID != "" {
		url += "&person_id=" + personID
	}
	body, err := posthogRequest("GET", url, cfg.AdminKey, nil)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Results []struct {
			Event      string                 `json:"event"`
			Timestamp  string                 `json:"timestamp"`
			DistinctID string                 `json:"distinct_id"`
			Properties map[string]interface{} `json:"properties"`
		} `json:"results"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	events := make([]AnalyticsEvent, 0, len(resp.Results))
	for _, r := range resp.Results {
		events = append(events, AnalyticsEvent{
			Event:      r.Event,
			Timestamp:  r.Timestamp,
			PersonID:   r.DistinctID,
			Properties: r.Properties,
		})
	}
	return events, nil
}

func (m *AnalyticsManager) posthogPersons(cfg *analyticsConfig, limit int) ([]AnalyticsPerson, error) {
	url := fmt.Sprintf("http://localhost:%d/api/person/?limit=%d", cfg.Port, limit)
	body, err := posthogRequest("GET", url, cfg.AdminKey, nil)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Results []struct {
			ID         string                 `json:"id"`
			Properties map[string]interface{} `json:"properties"`
			CreatedAt  string                 `json:"created_at"`
		} `json:"results"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	persons := make([]AnalyticsPerson, 0, len(resp.Results))
	for _, r := range resp.Results {
		persons = append(persons, AnalyticsPerson{
			ID:         r.ID,
			Properties: r.Properties,
			CreatedAt:  r.CreatedAt,
		})
	}
	return persons, nil
}

func (m *AnalyticsManager) posthogFlags(cfg *analyticsConfig, action, name string, enabled bool) (interface{}, error) {
	base := fmt.Sprintf("http://localhost:%d/api/feature_flag/", cfg.Port)
	switch action {
	case "list", "":
		body, err := posthogRequest("GET", base, cfg.AdminKey, nil)
		if err != nil {
			return nil, err
		}
		var result interface{}
		json.Unmarshal(body, &result)
		return result, nil
	case "create":
		if name == "" {
			return nil, fmt.Errorf("flag name is required for create")
		}
		payload := map[string]interface{}{
			"key":     name,
			"name":    name,
			"active":  enabled,
			"filters": map[string]interface{}{"groups": []interface{}{map[string]interface{}{"rollout_percentage": 100}}},
		}
		data, _ := json.Marshal(payload)
		body, err := posthogRequest("POST", base, cfg.AdminKey, data)
		if err != nil {
			return nil, err
		}
		var result interface{}
		json.Unmarshal(body, &result)
		return result, nil
	case "enable", "disable":
		if name == "" {
			return nil, fmt.Errorf("flag name is required for %s", action)
		}
		// Find flag by key first.
		listBody, err := posthogRequest("GET", base, cfg.AdminKey, nil)
		if err != nil {
			return nil, err
		}
		var listResp struct {
			Results []struct {
				ID  int    `json:"id"`
				Key string `json:"key"`
			} `json:"results"`
		}
		json.Unmarshal(listBody, &listResp)
		flagID := 0
		for _, f := range listResp.Results {
			if f.Key == name {
				flagID = f.ID
				break
			}
		}
		if flagID == 0 {
			return nil, fmt.Errorf("flag %q not found", name)
		}
		active := action == "enable"
		payload := map[string]interface{}{"active": active}
		data, _ := json.Marshal(payload)
		url := fmt.Sprintf("%s%d/", base, flagID)
		body, err := posthogRequest("PATCH", url, cfg.AdminKey, data)
		if err != nil {
			return nil, err
		}
		var result interface{}
		json.Unmarshal(body, &result)
		return result, nil
	default:
		return nil, fmt.Errorf("unknown action %q (supported: list, create, enable, disable)", action)
	}
}

func (m *AnalyticsManager) posthogDashboard(cfg *analyticsConfig) (*AnalyticsDashboard, error) {
	// /api/insight/trend/ for pageview count.
	url := fmt.Sprintf("http://localhost:%d/api/insight/trend/?events=[{\"id\":\"$pageview\"}]", cfg.Port)
	body, err := posthogRequest("GET", url, cfg.AdminKey, nil)
	dash := &AnalyticsDashboard{Engine: "posthog"}
	if err == nil {
		var resp struct {
			Result []struct {
				Data []int `json:"data"`
			} `json:"result"`
		}
		if json.Unmarshal(body, &resp) == nil && len(resp.Result) > 0 {
			for _, v := range resp.Result[0].Data {
				dash.Pageviews += v
			}
		}
	}
	return dash, nil
}

func posthogRequest(method, url, apiKey string, body []byte) ([]byte, error) {
	var reqBody io.Reader
	if body != nil {
		reqBody = bytes.NewReader(body)
	}
	req, err := http.NewRequest(method, url, reqBody)
	if err != nil {
		return nil, err
	}
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("API error %d: %s", resp.StatusCode, string(data))
	}
	return data, nil
}

// ---------------------------------------------------------------------------
// Umami API calls
// ---------------------------------------------------------------------------

func (m *AnalyticsManager) umamiToken(cfg *analyticsConfig) (string, error) {
	url := fmt.Sprintf("http://localhost:%d/api/auth/login", cfg.Port)
	payload := map[string]string{"username": cfg.AdminUser, "password": cfg.AdminPass}
	data, _ := json.Marshal(payload)
	req, err := http.NewRequest("POST", url, bytes.NewReader(data))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var result struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(body, &result); err != nil || result.Token == "" {
		return "", fmt.Errorf("umami auth failed: %s", string(body))
	}
	return result.Token, nil
}

func (m *AnalyticsManager) umamiWebsiteID(cfg *analyticsConfig, token string) (string, error) {
	if cfg.UmamiSiteID != "" {
		return cfg.UmamiSiteID, nil
	}
	url := fmt.Sprintf("http://localhost:%d/api/websites", cfg.Port)
	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var sites []struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &sites); err != nil || len(sites) == 0 {
		return "", fmt.Errorf("no websites found in Umami — create one at http://localhost:%d", cfg.Port)
	}
	// Cache it.
	cfg.UmamiSiteID = sites[0].ID
	cfgPath := filepath.Join(m.baseDir, "umami", "config.json")
	if data, err := json.MarshalIndent(cfg, "", "  "); err == nil {
		_ = os.WriteFile(cfgPath, data, 0600)
	}
	return cfg.UmamiSiteID, nil
}

func (m *AnalyticsManager) umamiEvents(cfg *analyticsConfig, eventName, period string) ([]AnalyticsEvent, error) {
	token, err := m.umamiToken(cfg)
	if err != nil {
		return nil, err
	}
	siteID, err := m.umamiWebsiteID(cfg, token)
	if err != nil {
		return nil, err
	}

	now := time.Now()
	startAt := now.AddDate(0, 0, -30).UnixMilli()
	endAt := now.UnixMilli()
	url := fmt.Sprintf("http://localhost:%d/api/websites/%s/events?startAt=%d&endAt=%d&limit=100",
		cfg.Port, siteID, startAt, endAt)

	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	var result struct {
		Data []struct {
			EventName string `json:"eventName"`
			CreatedAt string `json:"createdAt"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	events := make([]AnalyticsEvent, 0)
	for _, e := range result.Data {
		if eventName != "" && e.EventName != eventName {
			continue
		}
		events = append(events, AnalyticsEvent{
			Event:     e.EventName,
			Timestamp: e.CreatedAt,
		})
	}
	return events, nil
}

func (m *AnalyticsManager) umamiSessions(cfg *analyticsConfig, limit int) ([]AnalyticsPerson, error) {
	token, err := m.umamiToken(cfg)
	if err != nil {
		return nil, err
	}
	siteID, err := m.umamiWebsiteID(cfg, token)
	if err != nil {
		return nil, err
	}

	now := time.Now()
	startAt := now.AddDate(0, 0, -30).UnixMilli()
	endAt := now.UnixMilli()
	url := fmt.Sprintf("http://localhost:%d/api/websites/%s/sessions?startAt=%d&endAt=%d&limit=%d",
		cfg.Port, siteID, startAt, endAt, limit)

	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	var result struct {
		Data []struct {
			ID        string `json:"id"`
			CreatedAt string `json:"createdAt"`
			Browser   string `json:"browser"`
			OS        string `json:"os"`
			Country   string `json:"country"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	persons := make([]AnalyticsPerson, 0, len(result.Data))
	for _, s := range result.Data {
		persons = append(persons, AnalyticsPerson{
			ID:        s.ID,
			CreatedAt: s.CreatedAt,
			Properties: map[string]interface{}{
				"browser": s.Browser,
				"os":      s.OS,
				"country": s.Country,
			},
		})
	}
	return persons, nil
}

func (m *AnalyticsManager) umamiDashboard(cfg *analyticsConfig) (*AnalyticsDashboard, error) {
	token, err := m.umamiToken(cfg)
	if err != nil {
		return nil, err
	}
	siteID, err := m.umamiWebsiteID(cfg, token)
	if err != nil {
		return nil, err
	}

	now := time.Now()
	startAt := now.AddDate(0, 0, -30).UnixMilli()
	endAt := now.UnixMilli()
	statsURL := fmt.Sprintf("http://localhost:%d/api/websites/%s/stats?startAt=%d&endAt=%d",
		cfg.Port, siteID, startAt, endAt)

	req, _ := http.NewRequest("GET", statsURL, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	dash := &AnalyticsDashboard{Engine: "umami"}
	var stats struct {
		Pageviews struct{ Value int } `json:"pageviews"`
		Visitors  struct{ Value int } `json:"uniques"`
	}
	if json.Unmarshal(body, &stats) == nil {
		dash.Pageviews = stats.Pageviews.Value
		dash.Visitors = stats.Visitors.Value
	}

	// Top pages.
	pagesURL := fmt.Sprintf("http://localhost:%d/api/websites/%s/metrics?startAt=%d&endAt=%d&type=url&limit=5",
		cfg.Port, siteID, startAt, endAt)
	req2, _ := http.NewRequest("GET", pagesURL, nil)
	req2.Header.Set("Authorization", "Bearer "+token)
	resp2, err := client.Do(req2)
	if err == nil {
		defer resp2.Body.Close()
		body2, _ := io.ReadAll(resp2.Body)
		var pages []struct {
			X string `json:"x"`
			Y int    `json:"y"`
		}
		if json.Unmarshal(body2, &pages) == nil {
			for _, p := range pages {
				dash.TopPages = append(dash.TopPages, struct {
					Path  string `json:"path"`
					Views int    `json:"views"`
				}{Path: p.X, Views: p.Y})
			}
		}
	}
	return dash, nil
}

// ---------------------------------------------------------------------------
// Utilities
// ---------------------------------------------------------------------------

func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		// Fallback to timestamp-based string — not cryptographically strong
		// but sufficient for local dev secrets.
		return fmt.Sprintf("%x", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)[:n]
}
