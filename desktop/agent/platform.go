package main

import (
	"bufio"
	"bytes"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

// ─── Types ────────────────────────────────────────────────────────────────────

// PlatformApp represents a deployed application managed by the platform.
type PlatformApp struct {
	Name             string    `yaml:"name"             json:"name"`
	Framework        string    `yaml:"framework"        json:"framework"`
	Directory        string    `yaml:"directory"        json:"directory"`
	Port             int       `yaml:"port"             json:"port"`
	Domain           string    `yaml:"domain"           json:"domain"`
	BuildCmd         string    `yaml:"buildCmd"         json:"buildCmd"`
	StartCmd         string    `yaml:"startCmd"         json:"startCmd"`
	EnvFile          string    `yaml:"envFile"          json:"envFile"`
	Status           string    `yaml:"status"           json:"status"` // building/running/stopped/failed
	ContainerID      string    `yaml:"containerId"      json:"containerId"`
	PID              int       `yaml:"pid"              json:"pid"`
	DeployedAt       time.Time `yaml:"deployedAt"       json:"deployedAt"`
	Version          int       `yaml:"version"          json:"version"`
	PreviousVersions []string  `yaml:"previousVersions" json:"previousVersions"`
}

// PlatformPaaSConfig holds the platform-level configuration, persisted to YAML.
// Named PlatformPaaSConfig to avoid collision with the Convex PlatformConfig in auth.go.
type PlatformPaaSConfig struct {
	Mode          string        `yaml:"mode"`          // local/relay/vps
	Domain        string        `yaml:"domain"`
	SSL           string        `yaml:"ssl"`           // auto/manual/off
	Containerized bool          `yaml:"containerized"`
	Apps          []PlatformApp `yaml:"apps"`

	// configPath is set at load time; not persisted.
	configPath string `yaml:"-"`
}

// PlatformPreview is a temporary preview deploy for a git branch.
type PlatformPreview struct {
	ID        string    `json:"id"`
	Branch    string    `json:"branch"`
	AppName   string    `json:"appName"`
	Port      int       `json:"port"`
	URL       string    `json:"url"`
	Status    string    `json:"status"` // building/running/stopped/failed
	CreatedAt time.Time `json:"createdAt"`
}

// PlatformWebhook stores the configuration for a push-to-deploy webhook.
type PlatformWebhook struct {
	ID            string    `json:"id"`
	Repo          string    `json:"repo"`
	Branch        string    `json:"branch"`
	Secret        string    `json:"secret"`
	AppName       string    `json:"appName"`
	LastTriggered time.Time `json:"lastTriggered"`
}

// PlatformStatus is a point-in-time health summary of the platform.
type PlatformStatus struct {
	Mode       string        `json:"mode"`
	Domain     string        `json:"domain"`
	SSL        bool          `json:"ssl"`
	RunningApps int          `json:"runningApps"`
	TotalApps  int           `json:"totalApps"`
	Previews   int           `json:"previews"`
	Uptime     time.Duration `json:"uptime"`
	Memory     string        `json:"memory"`
	CPU        string        `json:"cpu"`
}

// PlatformManager is the central coordinator for the self-hosted PaaS.
// It manages app lifecycle, Caddy routing, preview deploys, and webhooks.
type PlatformManager struct {
	mu         sync.Mutex
	workDir    string
	config     *PlatformPaaSConfig
	apps       map[string]*PlatformApp
	previews   map[string]*PlatformPreview
	webhooks   map[string]*PlatformWebhook
	cmds       map[string]*exec.Cmd
	configPath string // ~/.yaver/platform.yaml
	startedAt  time.Time
}

// ─── Constructor ──────────────────────────────────────────────────────────────

// NewPlatformManager creates a PlatformManager rooted at workDir.
// The YAML config is stored at ~/.yaver/platform.yaml and app data under
// ~/.yaver/platform/apps/{name}/.
func NewPlatformManager(workDir string) *PlatformManager {
	home, err := os.UserHomeDir()
	if err != nil {
		home = os.Getenv("HOME")
	}

	pm := &PlatformManager{
		workDir:    workDir,
		apps:       make(map[string]*PlatformApp),
		previews:   make(map[string]*PlatformPreview),
		webhooks:   make(map[string]*PlatformWebhook),
		cmds:       make(map[string]*exec.Cmd),
		configPath: filepath.Join(home, ".yaver", "platform.yaml"),
		startedAt:  time.Now(),
	}

	// Load persisted config (ignore error on first run).
	if err := pm.loadConfig(); err != nil {
		pm.config = &PlatformPaaSConfig{
			Mode:   "local",
			SSL:    "off",
			Domain: "localhost",
		}
	}

	// Populate in-memory app map from config.
	for i := range pm.config.Apps {
		a := pm.config.Apps[i]
		pm.apps[a.Name] = &a
	}

	return pm
}

// ─── Init ─────────────────────────────────────────────────────────────────────

// Init sets up the platform for the first time (or re-initialises it).
// mode: "local" | "relay" | "vps"
// domain: public-facing domain or "localhost"
// Returns a human-readable setup summary.
func (pm *PlatformManager) Init(mode, domain string) (string, error) {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	if mode != "local" && mode != "relay" && mode != "vps" {
		return "", fmt.Errorf("unknown mode %q: must be local, relay, or vps", mode)
	}

	pm.config.Mode = mode
	pm.config.Domain = domain

	// Auto-pick SSL strategy.
	switch {
	case domain == "localhost" || strings.HasPrefix(domain, "127."):
		pm.config.SSL = "off"
	case mode == "vps":
		pm.config.SSL = "auto" // Caddy ACME
	default:
		pm.config.SSL = "manual"
	}

	// Create required directories.
	dirs := []string{
		pm.appsDataRoot(),
		pm.caddyDir(),
		pm.logsDir(),
	}
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return "", fmt.Errorf("mkdir %s: %w", d, err)
		}
	}

	// Write initial Caddy config.
	if err := pm.updateCaddyRoutes(); err != nil {
		// Non-fatal: Caddy may not be installed yet.
		log.Printf("platform init: caddy config write warning: %v", err)
	}

	if err := pm.saveConfig(); err != nil {
		return "", fmt.Errorf("save config: %w", err)
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "Platform initialised\n")
	fmt.Fprintf(&sb, "  mode:   %s\n", mode)
	fmt.Fprintf(&sb, "  domain: %s\n", domain)
	fmt.Fprintf(&sb, "  ssl:    %s\n", pm.config.SSL)
	fmt.Fprintf(&sb, "  data:   %s\n", pm.appsDataRoot())
	fmt.Fprintf(&sb, "  config: %s\n", pm.configPath)

	if mode == "local" {
		fmt.Fprintf(&sb, "\nDeploy apps with:  yaver platform deploy <dir> --name <app> --domain <host>\n")
	} else if mode == "vps" {
		fmt.Fprintf(&sb, "\nEnsure Caddy is running:  sudo caddy run --config %s\n", pm.caddyfilePath())
	}

	return sb.String(), nil
}

// ─── Deploy ───────────────────────────────────────────────────────────────────

// Deploy builds and starts an app from directory, serving it under domain.
// Returns the public URL of the deployed app.
func (pm *PlatformManager) Deploy(directory, name, domain string) (string, error) {
	directory, err := filepath.Abs(directory)
	if err != nil {
		return "", fmt.Errorf("resolve directory: %w", err)
	}
	if _, err := os.Stat(directory); err != nil {
		return "", fmt.Errorf("directory not found: %s", directory)
	}

	pm.mu.Lock()

	// Recycle an existing entry or create new.
	app, exists := pm.apps[name]
	if !exists {
		app = &PlatformApp{
			Name:      name,
			Directory: directory,
			Domain:    domain,
			Version:   1,
		}
		pm.apps[name] = app
	} else {
		// Bump version, archive previous build snapshot path.
		snapshot := pm.snapshotPath(name, app.Version)
		app.PreviousVersions = append(app.PreviousVersions, snapshot)
		if len(app.PreviousVersions) > 5 {
			app.PreviousVersions = app.PreviousVersions[len(app.PreviousVersions)-5:]
		}
		app.Version++
		app.Directory = directory
		app.Domain = domain
	}

	app.Framework = pm.detectFramework(directory)
	app.Status = "building"

	// Derive build/start commands if not already customised.
	if app.BuildCmd == "" {
		app.BuildCmd = defaultBuildCmd(app.Framework)
	}
	if app.StartCmd == "" {
		app.StartCmd = defaultStartCmd(app.Framework)
	}

	// Allocate port (keep existing port on redeploy).
	if app.Port == 0 {
		app.Port = pm.assignPort()
	}

	pm.mu.Unlock()

	// Stop any previous instance before rebuilding.
	_ = pm.stopApp(name)

	// Build.
	if err := pm.buildApp(app); err != nil {
		pm.mu.Lock()
		app.Status = "failed"
		_ = pm.saveConfigLocked()
		pm.mu.Unlock()
		return "", fmt.Errorf("build failed: %w", err)
	}

	// Start.
	if err := pm.startApp(app); err != nil {
		pm.mu.Lock()
		app.Status = "failed"
		_ = pm.saveConfigLocked()
		pm.mu.Unlock()
		return "", fmt.Errorf("start failed: %w", err)
	}

	pm.mu.Lock()
	app.Status = "running"
	app.DeployedAt = time.Now()
	if err := pm.updateCaddyRoutes(); err != nil {
		log.Printf("platform deploy: caddy update warning: %v", err)
	}
	_ = pm.saveConfigLocked()
	pm.mu.Unlock()

	url := pm.appURL(app)
	return url, nil
}

// ─── Redeploy ─────────────────────────────────────────────────────────────────

// Redeploy rebuilds and restarts an existing app, preserving a rollback slot.
func (pm *PlatformManager) Redeploy(appName string) (string, error) {
	pm.mu.Lock()
	app, ok := pm.apps[appName]
	if !ok {
		pm.mu.Unlock()
		return "", fmt.Errorf("app %q not found", appName)
	}
	dir := app.Directory
	domain := app.Domain
	pm.mu.Unlock()

	return pm.Deploy(dir, appName, domain)
}

// ─── Rollback ─────────────────────────────────────────────────────────────────

// Rollback re-deploys a specific previous version by index (1 = last, 2 = one before, ...).
// If version is 0 the most recent previous version is used.
func (pm *PlatformManager) Rollback(appName string, version int) (string, error) {
	pm.mu.Lock()
	app, ok := pm.apps[appName]
	if !ok {
		pm.mu.Unlock()
		return "", fmt.Errorf("app %q not found", appName)
	}

	if len(app.PreviousVersions) == 0 {
		pm.mu.Unlock()
		return "", fmt.Errorf("no previous versions available for %q", appName)
	}

	idx := len(app.PreviousVersions) - 1
	if version > 0 && version-1 < len(app.PreviousVersions) {
		idx = version - 1
	}
	snapshotPath := app.PreviousVersions[idx]
	domain := app.Domain
	pm.mu.Unlock()

	// Snapshot path doubles as the source directory for the old build.
	if _, err := os.Stat(snapshotPath); err != nil {
		return "", fmt.Errorf("snapshot not available at %s: %w", snapshotPath, err)
	}

	return pm.Deploy(snapshotPath, appName, domain)
}

// ─── Apps ─────────────────────────────────────────────────────────────────────

// Apps returns all known apps with live status.
func (pm *PlatformManager) Apps() ([]PlatformApp, error) {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	result := make([]PlatformApp, 0, len(pm.apps))
	for _, app := range pm.apps {
		// Refresh process/container liveness.
		pm.refreshAppStatus(app)
		result = append(result, *app)
	}
	return result, nil
}

// ─── Logs ─────────────────────────────────────────────────────────────────────

// AppLogs returns the last `lines` log lines for an app.
// Uses `docker logs` for containerised apps, otherwise reads the log file.
func (pm *PlatformManager) AppLogs(appName string, lines int) (string, error) {
	pm.mu.Lock()
	app, ok := pm.apps[appName]
	if !ok {
		pm.mu.Unlock()
		return "", fmt.Errorf("app %q not found", appName)
	}
	containerID := app.ContainerID
	pm.mu.Unlock()

	if lines <= 0 {
		lines = 100
	}

	if containerID != "" {
		out, err := exec.Command("docker", "logs", "--tail", strconv.Itoa(lines), containerID).CombinedOutput()
		if err != nil {
			return "", fmt.Errorf("docker logs: %w\n%s", err, out)
		}
		return string(out), nil
	}

	// Process: read from log file.
	logPath := pm.appLogPath(appName)
	f, err := os.Open(logPath)
	if err != nil {
		if os.IsNotExist(err) {
			return "(no logs yet)", nil
		}
		return "", fmt.Errorf("open log file: %w", err)
	}
	defer f.Close()

	return tailFile(f, lines), nil
}

// ─── Remove ───────────────────────────────────────────────────────────────────

// Remove stops the app and removes it from the platform entirely.
func (pm *PlatformManager) Remove(appName string) (string, error) {
	pm.mu.Lock()
	_, ok := pm.apps[appName]
	if !ok {
		pm.mu.Unlock()
		return "", fmt.Errorf("app %q not found", appName)
	}
	pm.mu.Unlock()

	if err := pm.stopApp(appName); err != nil {
		log.Printf("platform remove: stop warning: %v", err)
	}

	pm.mu.Lock()
	delete(pm.apps, appName)
	if err := pm.updateCaddyRoutes(); err != nil {
		log.Printf("platform remove: caddy update warning: %v", err)
	}
	_ = pm.saveConfigLocked()
	pm.mu.Unlock()

	return fmt.Sprintf("app %q removed", appName), nil
}

// ─── Preview ──────────────────────────────────────────────────────────────────

// Preview creates a temporary preview deploy for a specific git branch.
// The preview is served on a unique port and a subdomain like
// {branch}.preview.{domain} is configured in Caddy.
func (pm *PlatformManager) Preview(branch, appName string) (*PlatformPreview, error) {
	pm.mu.Lock()
	app, ok := pm.apps[appName]
	if !ok {
		pm.mu.Unlock()
		return nil, fmt.Errorf("app %q not found", appName)
	}
	srcDir := app.Directory
	domain := pm.config.Domain
	pm.mu.Unlock()

	// Checkout the branch into a temp workspace.
	previewDir, err := os.MkdirTemp("", "yaver-preview-*")
	if err != nil {
		return nil, fmt.Errorf("create preview dir: %w", err)
	}

	// Clone/copy source and checkout branch.
	if err := gitCloneAndCheckout(srcDir, previewDir, branch); err != nil {
		_ = os.RemoveAll(previewDir)
		return nil, fmt.Errorf("git checkout %q: %w", branch, err)
	}

	// Allocate a port and build/start the preview.
	pm.mu.Lock()
	port := pm.assignPort()
	pm.mu.Unlock()

	fw := pm.detectFramework(previewDir)
	previewApp := &PlatformApp{
		Name:      fmt.Sprintf("preview-%s-%s", appName, sanitizeBranch(branch)),
		Framework: fw,
		Directory: previewDir,
		Port:      port,
		BuildCmd:  defaultBuildCmd(fw),
		StartCmd:  defaultStartCmd(fw),
		Status:    "building",
	}

	if err := pm.buildApp(previewApp); err != nil {
		_ = os.RemoveAll(previewDir)
		return nil, fmt.Errorf("preview build: %w", err)
	}
	if err := pm.startApp(previewApp); err != nil {
		_ = os.RemoveAll(previewDir)
		return nil, fmt.Errorf("preview start: %w", err)
	}

	// Sub-domain: sanitized-branch.preview.domain
	safeBranch := sanitizeBranch(branch)
	previewURL := fmt.Sprintf("http://%s.preview.%s", safeBranch, domain)
	if pm.config.SSL != "off" {
		previewURL = fmt.Sprintf("https://%s.preview.%s", safeBranch, domain)
	}

	previewID := fmt.Sprintf("%s-%s-%d", appName, safeBranch, time.Now().Unix())

	p := &PlatformPreview{
		ID:        previewID,
		Branch:    branch,
		AppName:   appName,
		Port:      port,
		URL:       previewURL,
		Status:    "running",
		CreatedAt: time.Now(),
	}

	pm.mu.Lock()
	pm.previews[previewID] = p
	// Register as a virtual app so Caddy can route to it.
	previewApp.Domain = fmt.Sprintf("%s.preview.%s", safeBranch, domain)
	previewApp.Status = "running"
	previewApp.DeployedAt = time.Now()
	pm.apps[previewApp.Name] = previewApp
	if err := pm.updateCaddyRoutes(); err != nil {
		log.Printf("platform preview: caddy update warning: %v", err)
	}
	pm.mu.Unlock()

	return p, nil
}

// ─── Previews ─────────────────────────────────────────────────────────────────

// Previews returns all active preview deploys.
func (pm *PlatformManager) Previews() ([]*PlatformPreview, error) {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	result := make([]*PlatformPreview, 0, len(pm.previews))
	for _, p := range pm.previews {
		result = append(result, p)
	}
	return result, nil
}

// ─── Webhooks ─────────────────────────────────────────────────────────────────

// WebhookSetup creates a push-to-deploy webhook for a repo/branch/app combination.
// Returns the webhook configuration including the secret and the URL to configure
// in GitHub/GitLab.
func (pm *PlatformManager) WebhookSetup(repo, branch, appName string) (*PlatformWebhook, error) {
	pm.mu.Lock()
	if _, ok := pm.apps[appName]; !ok {
		pm.mu.Unlock()
		return nil, fmt.Errorf("app %q not found", appName)
	}
	pm.mu.Unlock()

	secret, err := generateSecret(32)
	if err != nil {
		return nil, fmt.Errorf("generate secret: %w", err)
	}

	id := fmt.Sprintf("wh-%s-%d", sanitizeBranch(branch), time.Now().UnixNano())
	wh := &PlatformWebhook{
		ID:      id,
		Repo:    repo,
		Branch:  branch,
		Secret:  secret,
		AppName: appName,
	}

	pm.mu.Lock()
	pm.webhooks[id] = wh
	pm.mu.Unlock()

	return wh, nil
}

// WebhookHandler processes an incoming git push webhook payload.
// It verifies the HMAC-SHA256 signature, checks the pushed branch,
// and triggers a redeploy on match.
func (pm *PlatformManager) WebhookHandler(webhookID string, payload []byte) (string, error) {
	pm.mu.Lock()
	wh, ok := pm.webhooks[webhookID]
	if !ok {
		pm.mu.Unlock()
		return "", fmt.Errorf("webhook %q not found", webhookID)
	}
	secret := wh.Secret
	appName := wh.AppName
	expectedBranch := wh.Branch
	pm.mu.Unlock()

	// Verify HMAC-SHA256 signature embedded in the payload's "signature" field.
	if !verifyWebhookSignature(payload, secret) {
		return "", fmt.Errorf("invalid webhook signature")
	}

	// Parse the pushed branch from a minimal GitHub-style payload.
	branch := extractPushedBranch(payload)
	if branch == "" {
		return "", fmt.Errorf("could not determine pushed branch from payload")
	}
	if branch != expectedBranch {
		return fmt.Sprintf("ignored push to %q (watching %q)", branch, expectedBranch), nil
	}

	pm.mu.Lock()
	wh.LastTriggered = time.Now()
	pm.mu.Unlock()

	url, err := pm.Redeploy(appName)
	if err != nil {
		return "", fmt.Errorf("redeploy after webhook: %w", err)
	}
	return fmt.Sprintf("redeployed %s → %s", appName, url), nil
}

// ─── Status ───────────────────────────────────────────────────────────────────

// Status returns a health summary of the platform.
func (pm *PlatformManager) Status() (*PlatformStatus, error) {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	running := 0
	for _, app := range pm.apps {
		pm.refreshAppStatus(app)
		if app.Status == "running" {
			running++
		}
	}

	memStr, cpuStr := hostResources()

	return &PlatformStatus{
		Mode:        pm.config.Mode,
		Domain:      pm.config.Domain,
		SSL:         pm.config.SSL != "off",
		RunningApps: running,
		TotalApps:   len(pm.apps),
		Previews:    len(pm.previews),
		Uptime:      time.Since(pm.startedAt),
		Memory:      memStr,
		CPU:         cpuStr,
	}, nil
}

// ─── Config ───────────────────────────────────────────────────────────────────

// Config returns the current platform configuration.
func (pm *PlatformManager) Config() (*PlatformPaaSConfig, error) {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	cfg := *pm.config
	return &cfg, nil
}

// SetConfig updates a single top-level config key and persists it.
// Supported keys: mode, domain, ssl, containerized
func (pm *PlatformManager) SetConfig(key, value string) (string, error) {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	switch key {
	case "mode":
		if value != "local" && value != "relay" && value != "vps" {
			return "", fmt.Errorf("invalid mode %q: must be local, relay, or vps", value)
		}
		pm.config.Mode = value
	case "domain":
		pm.config.Domain = value
	case "ssl":
		if value != "auto" && value != "manual" && value != "off" {
			return "", fmt.Errorf("invalid ssl value %q: must be auto, manual, or off", value)
		}
		pm.config.SSL = value
	case "containerized":
		switch value {
		case "true", "1", "yes":
			pm.config.Containerized = true
		case "false", "0", "no":
			pm.config.Containerized = false
		default:
			return "", fmt.Errorf("invalid boolean value %q", value)
		}
	default:
		return "", fmt.Errorf("unknown config key %q", key)
	}

	if err := pm.saveConfigLocked(); err != nil {
		return "", fmt.Errorf("save config: %w", err)
	}
	return fmt.Sprintf("set %s = %s", key, value), nil
}

// ─── Internal helpers ─────────────────────────────────────────────────────────

// detectFramework inspects dir and returns a framework identifier.
// Recognised values: nextjs, vite, astro, go, python, node, static
func (pm *PlatformManager) detectFramework(dir string) string {
	// Go project.
	if fileExists(filepath.Join(dir, "go.mod")) {
		return "go"
	}
	// Python project.
	if fileExists(filepath.Join(dir, "requirements.txt")) || fileExists(filepath.Join(dir, "pyproject.toml")) {
		return "python"
	}
	// Node/JS projects — inspect package.json.
	pkgPath := filepath.Join(dir, "package.json")
	if data, err := os.ReadFile(pkgPath); err == nil {
		var pkg map[string]interface{}
		if json.Unmarshal(data, &pkg) == nil {
			deps := mergedDeps(pkg)
			switch {
			case deps["next"] != "":
				return "nextjs"
			case deps["astro"] != "":
				return "astro"
			case deps["vite"] != "":
				return "vite"
			}
		}
	}
	// Static site (index.html only).
	if fileExists(filepath.Join(dir, "index.html")) {
		return "static"
	}
	return "node"
}

// buildApp runs the build command for the app, capturing output to a log file.
func (pm *PlatformManager) buildApp(app *PlatformApp) error {
	if app.BuildCmd == "" {
		return nil // Nothing to build (e.g. pre-compiled Go binary, static site).
	}

	logPath := pm.appLogPath(app.Name)
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		return fmt.Errorf("mkdir log dir: %w", err)
	}
	lf, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return fmt.Errorf("open log file: %w", err)
	}
	defer lf.Close()

	env := os.Environ()
	if app.EnvFile != "" {
		extraEnv, _ := loadDotEnv(app.EnvFile)
		env = append(env, extraEnv...)
	}
	env = append(env, fmt.Sprintf("PORT=%d", app.Port))

	cmd := shellCommand(app.BuildCmd)
	cmd.Dir = app.Directory
	cmd.Env = env
	cmd.Stdout = lf
	cmd.Stderr = lf

	return cmd.Run()
}

// startApp launches the app process (or container) and records its PID/ContainerID.
// The caller must hold no locks when calling this.
func (pm *PlatformManager) startApp(app *PlatformApp) error {
	if pm.config.Containerized && app.Framework != "static" {
		return pm.startAppContainer(app)
	}
	return pm.startAppProcess(app)
}

// startAppProcess starts the app as a plain OS process.
func (pm *PlatformManager) startAppProcess(app *PlatformApp) error {
	if app.StartCmd == "" {
		// Static sites don't need a process; Caddy serves files directly.
		return nil
	}

	logPath := pm.appLogPath(app.Name)
	_ = os.MkdirAll(filepath.Dir(logPath), 0o755)
	lf, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("open log file: %w", err)
	}

	env := os.Environ()
	if app.EnvFile != "" {
		extraEnv, _ := loadDotEnv(app.EnvFile)
		env = append(env, extraEnv...)
	}
	env = append(env, fmt.Sprintf("PORT=%d", app.Port))

	cmd := shellCommand(app.StartCmd)
	cmd.Dir = app.Directory
	cmd.Env = env
	cmd.Stdout = lf
	cmd.Stderr = lf

	if err := cmd.Start(); err != nil {
		lf.Close()
		return fmt.Errorf("start process: %w", err)
	}

	app.PID = cmd.Process.Pid
	app.ContainerID = ""

	pm.mu.Lock()
	pm.cmds[app.Name] = cmd
	pm.mu.Unlock()

	// Reap the process in the background so it doesn't become a zombie.
	go func() {
		_ = cmd.Wait()
		lf.Close()
	}()

	return nil
}

// startAppContainer runs the app inside a Docker container.
func (pm *PlatformManager) startAppContainer(app *PlatformApp) error {
	imageName := fmt.Sprintf("yaver-platform-%s", strings.ToLower(app.Name))

	// Build the Docker image.
	buildArgs := []string{"build", "-t", imageName, "."}
	buildCmd := exec.Command("docker", buildArgs...)
	buildCmd.Dir = app.Directory
	if out, err := buildCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("docker build: %w\n%s", err, out)
	}

	// Assemble run arguments.
	runArgs := []string{
		"run", "-d",
		"--name", containerName(app.Name),
		"-p", fmt.Sprintf("%d:%d", app.Port, app.Port),
		"-e", fmt.Sprintf("PORT=%d", app.Port),
	}
	if app.EnvFile != "" {
		runArgs = append(runArgs, "--env-file", app.EnvFile)
	}
	runArgs = append(runArgs, imageName)

	out, err := exec.Command("docker", runArgs...).Output()
	if err != nil {
		return fmt.Errorf("docker run: %w", err)
	}

	app.ContainerID = strings.TrimSpace(string(out))
	app.PID = 0
	return nil
}

// stopApp stops the app's process or container.
func (pm *PlatformManager) stopApp(appName string) error {
	pm.mu.Lock()
	app, ok := pm.apps[appName]
	if !ok {
		pm.mu.Unlock()
		return nil
	}
	containerID := app.ContainerID
	cmd := pm.cmds[appName]
	pm.mu.Unlock()

	var stopErr error

	if containerID != "" {
		out, err := exec.Command("docker", "rm", "-f", containerID).CombinedOutput()
		if err != nil {
			stopErr = fmt.Errorf("docker rm: %w\n%s", err, out)
		}
		pm.mu.Lock()
		app.ContainerID = ""
		pm.mu.Unlock()
	} else if cmd != nil && cmd.Process != nil {
		if err := cmd.Process.Kill(); err != nil {
			stopErr = fmt.Errorf("kill process: %w", err)
		}
		pm.mu.Lock()
		delete(pm.cmds, appName)
		app.PID = 0
		pm.mu.Unlock()
	}

	pm.mu.Lock()
	app.Status = "stopped"
	pm.mu.Unlock()

	return stopErr
}

// updateCaddyRoutes regenerates the Caddyfile and signals Caddy to reload.
// Must be called with pm.mu held or after acquiring it.
func (pm *PlatformManager) updateCaddyRoutes() error {
	caddyfile := pm.generateCaddyConfig()
	path := pm.caddyfilePath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("mkdir caddy dir: %w", err)
	}
	if err := os.WriteFile(path, []byte(caddyfile), 0o644); err != nil {
		return fmt.Errorf("write caddyfile: %w", err)
	}
	// Signal Caddy to reload config if it is running.
	_ = exec.Command("caddy", "reload", "--config", path).Run()
	return nil
}

// generateCaddyConfig produces a Caddyfile that routes each app's domain to its port.
func (pm *PlatformManager) generateCaddyConfig() string {
	var sb strings.Builder

	for _, app := range pm.apps {
		if app.Status != "running" || app.Domain == "" {
			continue
		}

		domain := app.Domain
		if pm.config.SSL == "off" {
			// HTTP-only for local / no-SSL mode.
			fmt.Fprintf(&sb, "http://%s {\n", domain)
		} else {
			fmt.Fprintf(&sb, "%s {\n", domain)
		}

		if app.Framework == "static" {
			fmt.Fprintf(&sb, "\troot * %s\n", filepath.Join(app.Directory, "dist"))
			fmt.Fprintf(&sb, "\tfile_server\n")
		} else {
			fmt.Fprintf(&sb, "\treverse_proxy localhost:%d\n", app.Port)
		}

		if pm.config.SSL == "auto" {
			fmt.Fprintf(&sb, "\ttls {\n\t\ton_demand\n\t}\n")
		}

		fmt.Fprintf(&sb, "}\n\n")
	}

	return sb.String()
}

// loadConfig reads the YAML config file from configPath.
func (pm *PlatformManager) loadConfig() error {
	data, err := os.ReadFile(pm.configPath)
	if err != nil {
		return err
	}
	cfg := &PlatformPaaSConfig{}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return fmt.Errorf("parse config: %w", err)
	}
	cfg.configPath = pm.configPath
	pm.config = cfg
	return nil
}

// saveConfig persists the current config to YAML. Acquires the lock.
func (pm *PlatformManager) saveConfig() error {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	return pm.saveConfigLocked()
}

// saveConfigLocked persists config. Must be called with pm.mu held.
func (pm *PlatformManager) saveConfigLocked() error {
	// Sync in-memory apps map back into the config slice.
	apps := make([]PlatformApp, 0, len(pm.apps))
	for _, a := range pm.apps {
		apps = append(apps, *a)
	}
	pm.config.Apps = apps

	if err := os.MkdirAll(filepath.Dir(pm.configPath), 0o755); err != nil {
		return fmt.Errorf("mkdir config dir: %w", err)
	}
	data, err := yaml.Marshal(pm.config)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	return os.WriteFile(pm.configPath, data, 0o644)
}

// assignPort returns the next available TCP port starting from 3001.
// Must be called with pm.mu held.
func (pm *PlatformManager) assignPort() int {
	used := make(map[int]bool)
	for _, a := range pm.apps {
		if a.Port > 0 {
			used[a.Port] = true
		}
	}
	for _, p := range pm.previews {
		if p.Port > 0 {
			used[p.Port] = true
		}
	}

	for port := 3001; port < 65535; port++ {
		if used[port] {
			continue
		}
		if portAvailable(port) {
			return port
		}
	}
	return 0
}

// refreshAppStatus checks whether the process or container is still alive
// and updates app.Status accordingly. Must be called with pm.mu held.
func (pm *PlatformManager) refreshAppStatus(app *PlatformApp) {
	if app.Status != "running" {
		return
	}

	if app.ContainerID != "" {
		out, err := exec.Command("docker", "inspect", "-f", "{{.State.Running}}", app.ContainerID).Output()
		if err != nil || strings.TrimSpace(string(out)) != "true" {
			app.Status = "stopped"
		}
		return
	}

	if app.PID > 0 {
		proc, err := os.FindProcess(app.PID)
		if err != nil {
			app.Status = "stopped"
			return
		}
		// On Unix, signal 0 tests process existence without sending a signal.
		if err := proc.Signal(os.Signal(nil)); err != nil {
			app.Status = "stopped"
		}
	}
}

// ─── Path helpers ─────────────────────────────────────────────────────────────

func (pm *PlatformManager) appsDataRoot() string {
	return filepath.Join(filepath.Dir(pm.configPath), "platform", "apps")
}

func (pm *PlatformManager) caddyDir() string {
	return filepath.Join(filepath.Dir(pm.configPath), "platform", "caddy")
}

func (pm *PlatformManager) logsDir() string {
	return filepath.Join(filepath.Dir(pm.configPath), "platform", "logs")
}

func (pm *PlatformManager) caddyfilePath() string {
	return filepath.Join(pm.caddyDir(), "Caddyfile")
}

func (pm *PlatformManager) appLogPath(appName string) string {
	return filepath.Join(pm.logsDir(), appName+".log")
}

func (pm *PlatformManager) snapshotPath(appName string, version int) string {
	return filepath.Join(pm.appsDataRoot(), appName, fmt.Sprintf("v%d", version))
}

func (pm *PlatformManager) appURL(app *PlatformApp) string {
	if app.Domain == "" {
		return fmt.Sprintf("http://localhost:%d", app.Port)
	}
	scheme := "http"
	if pm.config.SSL != "off" {
		scheme = "https"
	}
	return fmt.Sprintf("%s://%s", scheme, app.Domain)
}

// ─── Package-level utilities ──────────────────────────────────────────────────

// defaultBuildCmd returns the conventional build command for a framework.
func defaultBuildCmd(framework string) string {
	switch framework {
	case "nextjs":
		return "npm run build"
	case "vite":
		return "npm run build"
	case "astro":
		return "npm run build"
	case "go":
		return "go build -o app ."
	case "python":
		return "" // No build step for Python.
	case "static":
		return ""
	default:
		return "npm run build"
	}
}

// defaultStartCmd returns the conventional start command for a framework.
func defaultStartCmd(framework string) string {
	switch framework {
	case "nextjs":
		return "npm run start"
	case "vite":
		return "npx vite preview --port $PORT"
	case "astro":
		return "npx astro preview --port $PORT"
	case "go":
		return "./app"
	case "python":
		if fileExists("gunicorn") {
			return "gunicorn -b 0.0.0.0:$PORT app:app"
		}
		return "python3 -m uvicorn app.main:app --host 0.0.0.0 --port $PORT"
	case "static":
		return "" // Caddy handles file serving.
	default:
		return "node server.js"
	}
}

// shellCommand wraps a command string for execution via the OS shell.
func shellCommand(cmdStr string) *exec.Cmd {
	if runtime.GOOS == "windows" {
		return exec.Command("cmd", "/C", cmdStr)
	}
	return exec.Command("sh", "-c", cmdStr)
}

// mergedDeps combines "dependencies" and "devDependencies" from a package.json map.
func mergedDeps(pkg map[string]interface{}) map[string]string {
	result := make(map[string]string)
	for _, key := range []string{"dependencies", "devDependencies"} {
		if raw, ok := pkg[key]; ok {
			if m, ok := raw.(map[string]interface{}); ok {
				for k, v := range m {
					if s, ok := v.(string); ok {
						result[k] = s
					} else {
						result[k] = ""
					}
				}
			}
		}
	}
	return result
}

// portAvailable checks whether a TCP port is free on localhost.
func portAvailable(port int) bool {
	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		return false
	}
	_ = ln.Close()
	return true
}

// containerName derives a stable Docker container name from an app name.
func containerName(appName string) string {
	return "yaver-platform-" + strings.ToLower(strings.ReplaceAll(appName, " ", "-"))
}

// sanitizeBranch converts a git branch name into a DNS-label-safe string.
func sanitizeBranch(branch string) string {
	replacer := strings.NewReplacer("/", "-", "_", "-", ".", "-")
	s := replacer.Replace(strings.ToLower(branch))
	// Strip any character that isn't alphanumeric or a hyphen.
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			b.WriteRune(r)
		}
	}
	result := strings.Trim(b.String(), "-")
	if result == "" {
		return "branch"
	}
	return result
}

// generateSecret produces a hex-encoded random secret of `n` bytes.
func generateSecret(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// verifyWebhookSignature checks HMAC-SHA256 of payload against secret.
// Expects the payload to be a JSON object with a "signature" field containing
// "sha256=<hex>", consistent with GitHub's X-Hub-Signature-256 header convention.
func verifyWebhookSignature(payload []byte, secret string) bool {
	// Parse the signature out of the payload for the simplified wire format used
	// by the Yaver webhook endpoint (header-less).
	var envelope struct {
		Signature string          `json:"signature"`
		Body      json.RawMessage `json:"body"`
	}
	if json.Unmarshal(payload, &envelope) != nil {
		return false
	}
	expected := "sha256=" + hmacHex(envelope.Body, secret)
	return hmac.Equal([]byte(envelope.Signature), []byte(expected))
}

// hmacHex returns the hex-encoded HMAC-SHA256 of data with key.
func hmacHex(data []byte, key string) string {
	mac := hmac.New(sha256.New, []byte(key))
	mac.Write(data)
	return hex.EncodeToString(mac.Sum(nil))
}

// extractPushedBranch parses a GitHub-style push webhook body to find the branch name.
func extractPushedBranch(payload []byte) string {
	// Try the envelope format used by verifyWebhookSignature first.
	var envelope struct {
		Body json.RawMessage `json:"body"`
	}
	body := payload
	if json.Unmarshal(payload, &envelope) == nil && len(envelope.Body) > 0 {
		body = envelope.Body
	}

	var push struct {
		Ref string `json:"ref"` // e.g. "refs/heads/main"
	}
	if err := json.Unmarshal(body, &push); err != nil || push.Ref == "" {
		return ""
	}
	// "refs/heads/main" → "main"
	parts := strings.SplitN(push.Ref, "/", 3)
	if len(parts) == 3 {
		return parts[2]
	}
	return push.Ref
}

// gitCloneAndCheckout copies a local repo to destDir and checks out branch.
func gitCloneAndCheckout(srcDir, destDir, branch string) error {
	// Local clone is fast (hardlinks).
	cloneCmd := exec.Command("git", "clone", "--local", "--branch", branch, srcDir, destDir)
	if out, err := cloneCmd.CombinedOutput(); err != nil {
		// If the branch isn't found via --branch, try a plain clone + checkout.
		_ = os.RemoveAll(destDir)
		_ = os.MkdirAll(destDir, 0o755)
		if out2, err2 := exec.Command("git", "clone", "--local", srcDir, destDir).CombinedOutput(); err2 != nil {
			return fmt.Errorf("git clone: %w\n%s\n%s", err, out, out2)
		}
		if out3, err3 := exec.Command("git", "-C", destDir, "checkout", branch).CombinedOutput(); err3 != nil {
			return fmt.Errorf("git checkout %s: %w\n%s", branch, err3, out3)
		}
	}
	return nil
}

// loadDotEnv parses a .env file and returns a slice of "KEY=VALUE" strings.
func loadDotEnv(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var result []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// Strip surrounding quotes from the value part.
		if idx := strings.IndexByte(line, '='); idx > 0 {
			key := line[:idx]
			val := line[idx+1:]
			val = strings.Trim(val, `"'`)
			result = append(result, key+"="+val)
		}
	}
	return result, scanner.Err()
}

// tailFile returns the last n lines of an open file as a single string.
func tailFile(f *os.File, n int) string {
	scanner := bufio.NewScanner(f)
	lines := make([]string, 0, n)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
		if len(lines) > n {
			lines = lines[1:]
		}
	}
	return strings.Join(lines, "\n")
}

// hostResources returns rough memory and CPU usage strings for the Status call.
// These are best-effort; callers should treat them as informational.
func hostResources() (mem string, cpu string) {
	// Memory: read /proc/meminfo on Linux; use a placeholder on other platforms.
	if runtime.GOOS == "linux" {
		if data, err := os.ReadFile("/proc/meminfo"); err == nil {
			var total, available uint64
			sc := bufio.NewScanner(bytes.NewReader(data))
			for sc.Scan() {
				line := sc.Text()
				if strings.HasPrefix(line, "MemTotal:") {
					fmt.Sscanf(line, "MemTotal: %d kB", &total)
				} else if strings.HasPrefix(line, "MemAvailable:") {
					fmt.Sscanf(line, "MemAvailable: %d kB", &available)
				}
			}
			if total > 0 {
				used := (total - available) / 1024
				totalMB := total / 1024
				mem = fmt.Sprintf("%d/%d MB", used, totalMB)
			}
		}
	}
	if mem == "" {
		mem = "n/a"
	}

	// CPU: try a quick top/uptime parse; fall back gracefully.
	if out, err := exec.Command("uptime").Output(); err == nil {
		line := strings.TrimSpace(string(out))
		// "... load averages: 1.23 1.45 1.67" (BSD) or "load average: 1.23, 1.45, 1.67" (Linux)
		idx := strings.Index(line, "load")
		if idx >= 0 {
			cpu = strings.TrimSpace(line[idx:])
		}
	}
	if cpu == "" {
		cpu = "n/a"
	}

	return mem, cpu
}
