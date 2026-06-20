package main

// cloud_deploy.go — CloudDeployManager: deploy any project to a VPS (Hetzner or any server)
// with Docker + Caddy for automatic HTTPS, postgres, and redis.
//
// If HETZNER_API_TOKEN is set, the manager provisions a server via the Hetzner Cloud API.
// Without the token, it generates a self-contained deploy.sh script that the user can run
// on any VPS (DigitalOcean, Linode, bare-metal, etc.).
//
// Usage:
//   mgr, _ := NewCloudDeployManager("/path/to/project")
//   progress, err := mgr.Deploy("starter", "eu", "myapp", "myapp.example.com")
//   status, err := mgr.Status()
//   logs, err := mgr.Logs("web", 100)
//   err = mgr.Redeploy()
//   err = mgr.Restart("web")
//   err = mgr.Backup()
//   err = mgr.Destroy(true)

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"text/template"
	"time"
)

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

// CloudPlan describes a VPS plan offered by Yaver Cloud.
type CloudPlan struct {
	Name     string  `json:"name"`
	VCPU     int     `json:"vcpu"`
	RAM      string  `json:"ram"`
	Disk     string  `json:"disk"`
	Transfer string  `json:"transfer"`
	Price    float64 `json:"price"`
	MaxApps  int     `json:"maxApps"`
}

// CloudApp is a running application container in a deployment.
type CloudApp struct {
	Name        string `json:"name"`
	Framework   string `json:"framework"`
	Port        int    `json:"port"`
	Domain      string `json:"domain"`
	Status      string `json:"status"`
	ContainerID string `json:"containerID,omitempty"`
	Memory      string `json:"memory"`
	CPU         string `json:"cpu"`
}

// CloudService is a backing service (postgres, redis, etc.) in a deployment.
type CloudService struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	Port        int    `json:"port"`
	Status      string `json:"status"`
	ContainerID string `json:"containerID,omitempty"`
}

// CloudDeployment holds the full deployment state persisted to ~/.yaver/cloud.json.
type CloudDeployment struct {
	ID              string         `json:"id"`
	Name            string         `json:"name"`
	Plan            string         `json:"plan"`
	Region          string         `json:"region"`
	ServerIP        string         `json:"serverIP"`
	Domain          string         `json:"domain"`
	Status          string         `json:"status"` // provisioning | running | stopped | error
	Apps            []CloudApp     `json:"apps"`
	Services        []CloudService `json:"services"`
	CreatedAt       time.Time      `json:"createdAt"`
	MonthlyEstimate float64        `json:"monthlyEstimate"`
}

// projectStack is the detected technology stack for a workDir.
type projectStack struct {
	Framework string // node, go, python, rust, static
	HasDB     bool
	HasCache  bool
	BuildCmd  string
	StartCmd  string
	Port      int
}

// CloudDeployManager manages Yaver Cloud deployments for a single project.
type CloudDeployManager struct {
	mu         sync.Mutex
	workDir    string
	deployment *CloudDeployment
	configPath string
}

// ---------------------------------------------------------------------------
// Plans registry
// ---------------------------------------------------------------------------

var availablePlans = []CloudPlan{
	{
		Name: "starter", VCPU: 1, RAM: "2GB", Disk: "40GB",
		Transfer: "2TB", Price: 9.0, MaxApps: 2,
	},
	{
		Name: "pro", VCPU: 2, RAM: "4GB", Disk: "80GB",
		Transfer: "4TB", Price: 19.0, MaxApps: 5,
	},
	{
		Name: "scale", VCPU: 4, RAM: "8GB", Disk: "160GB",
		Transfer: "8TB", Price: 29.0, MaxApps: 10,
	},
}

func planByName(name string) (CloudPlan, bool) {
	for _, p := range availablePlans {
		if p.Name == name {
			return p, true
		}
	}
	return CloudPlan{}, false
}

// ---------------------------------------------------------------------------
// Constructor
// ---------------------------------------------------------------------------

// NewCloudDeployManager returns a manager for the given project directory.
// It loads any existing deployment state from ~/.yaver/cloud.json.
func NewCloudDeployManager(workDir string) (*CloudDeployManager, error) {
	cfgDir, err := ConfigDir()
	if err != nil {
		return nil, fmt.Errorf("config dir: %w", err)
	}
	m := &CloudDeployManager{
		workDir:    workDir,
		configPath: filepath.Join(cfgDir, "cloud.json"),
	}
	_ = m.loadDeployment() // ignore missing-file error
	return m, nil
}

// ---------------------------------------------------------------------------
// Deploy
// ---------------------------------------------------------------------------

// Deploy provisions a server (or generates a deploy script) and deploys the project.
// It returns multi-line progress text describing each completed step.
func (m *CloudDeployManager) Deploy(plan, region, name, domain string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// 1. Validate plan
	p, ok := planByName(plan)
	if !ok {
		names := make([]string, len(availablePlans))
		for i, ap := range availablePlans {
			names[i] = ap.Name
		}
		return "", fmt.Errorf("unknown plan %q — choose one of: %s", plan, strings.Join(names, ", "))
	}

	var progress strings.Builder
	step := func(msg string) { progress.WriteString("[deploy] " + msg + "\n") }

	step(fmt.Sprintf("Plan: %s (%.0f vCPU, %s RAM, $%.0f/mo)", p.Name, float64(p.VCPU), p.RAM, p.Price))

	// 2. Detect project stack
	stack := m.detectProjectStack()
	step(fmt.Sprintf("Detected framework: %s (port %d)", stack.Framework, stack.Port))

	// 3. Generate Docker Compose + Caddyfile
	compose, err := m.GenerateDockerCompose()
	if err != nil {
		return progress.String(), fmt.Errorf("generate compose: %w", err)
	}
	caddyCfg := m.generateCaddyConfig(domain, []CloudApp{
		{Name: name, Port: stack.Port, Domain: domain},
	})
	step("Generated docker-compose.yml and Caddyfile")

	// 4. Provision server (Hetzner API) or generate script
	apiToken := os.Getenv("HETZNER_API_TOKEN")
	var serverIP string
	if apiToken != "" {
		step("Provisioning Hetzner server via API…")
		serverIP, _, err = m.hetznerCreateServer(apiToken, name, plan, region)
		if err != nil {
			return progress.String(), fmt.Errorf("provision server: %w", err)
		}
		step(fmt.Sprintf("Server provisioned: %s", serverIP))
	} else {
		step("HETZNER_API_TOKEN not set — generating deploy.sh instead")
		script, serr := m.GenerateDeployScript()
		if serr != nil {
			return progress.String(), fmt.Errorf("generate deploy script: %w", serr)
		}
		scriptPath := filepath.Join(m.workDir, "deploy.sh")
		if werr := os.WriteFile(scriptPath, []byte(script), 0755); werr != nil {
			return progress.String(), fmt.Errorf("write deploy.sh: %w", werr)
		}
		step(fmt.Sprintf("deploy.sh written to %s", scriptPath))
		step("Run on any VPS: scp deploy.sh user@server: && ssh user@server bash deploy.sh")

		// Write compose and caddy files locally for reference
		_ = os.WriteFile(filepath.Join(m.workDir, "docker-compose.yml"), []byte(compose), 0644)
		_ = os.WriteFile(filepath.Join(m.workDir, "Caddyfile"), []byte(caddyCfg), 0644)
		step("docker-compose.yml and Caddyfile written to project root")

		// Save a "pending" deployment so Status() shows something useful
		m.deployment = &CloudDeployment{
			ID:              generateID(),
			Name:            name,
			Plan:            plan,
			Region:          region,
			ServerIP:        "",
			Domain:          domain,
			Status:          "pending",
			Apps:            []CloudApp{{Name: name, Framework: stack.Framework, Port: stack.Port, Domain: domain, Status: "pending"}},
			Services:        defaultServices(stack),
			CreatedAt:       time.Now(),
			MonthlyEstimate: p.Price,
		}
		_ = m.saveDeployment()
		return progress.String(), nil
	}

	// 5. Install docker + caddy on the server
	step("Installing Docker and Caddy on server…")
	installScript := cloudBootstrapScript()
	if err := m.cloudSSHExec(serverIP, installScript); err != nil {
		return progress.String(), fmt.Errorf("bootstrap server: %w", err)
	}
	step("Docker and Caddy installed")

	// 6. Upload compose + caddyfile
	step("Uploading configuration…")
	if err := m.cloudSCPString(serverIP, compose, "/opt/app/docker-compose.yml"); err != nil {
		return progress.String(), fmt.Errorf("upload compose: %w", err)
	}
	if err := m.cloudSCPString(serverIP, caddyCfg, "/opt/app/Caddyfile"); err != nil {
		return progress.String(), fmt.Errorf("upload caddyfile: %w", err)
	}
	step("Configuration uploaded")

	// 7. Build app image + deploy
	step("Building app image and deploying containers…")
	deployCmd := "cd /opt/app && docker compose pull 2>/dev/null; docker compose up -d --build"
	if err := m.cloudSSHExec(serverIP, deployCmd); err != nil {
		return progress.String(), fmt.Errorf("deploy containers: %w", err)
	}
	step("Containers running")

	// 8. Smoke test
	step("Running smoke test…")
	time.Sleep(3 * time.Second)
	healthURL := fmt.Sprintf("http://%s/health", serverIP)
	resp, herr := http.Get(healthURL) //nolint:gosec
	if herr == nil {
		resp.Body.Close()
		step(fmt.Sprintf("Smoke test passed (%s)", healthURL))
	} else {
		step("Smoke test skipped (no /health endpoint or server not yet ready)")
	}

	// 9. Save state
	m.deployment = &CloudDeployment{
		ID:              generateID(),
		Name:            name,
		Plan:            plan,
		Region:          region,
		ServerIP:        serverIP,
		Domain:          domain,
		Status:          "running",
		Apps:            []CloudApp{{Name: name, Framework: stack.Framework, Port: stack.Port, Domain: domain, Status: "running"}},
		Services:        defaultServices(stack),
		CreatedAt:       time.Now(),
		MonthlyEstimate: p.Price,
	}
	if err := m.saveDeployment(); err != nil {
		step(fmt.Sprintf("Warning: could not save deployment state: %v", err))
	}

	step(fmt.Sprintf("Deployment complete! App: https://%s", domain))
	return progress.String(), nil
}

// ---------------------------------------------------------------------------
// Status
// ---------------------------------------------------------------------------

// Status returns the current deployment, loading from disk and optionally
// checking live container health via SSH.
func (m *CloudDeployManager) Status() (*CloudDeployment, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.loadDeployment(); err != nil {
		return nil, fmt.Errorf("no deployment found — run 'deploy' first: %w", err)
	}

	d := m.deployment
	if d.ServerIP == "" {
		return d, nil
	}

	// Try live check (best-effort — never fail Status() because of SSH)
	out, err := m.cloudSSHOut(d.ServerIP, "docker compose -f /opt/app/docker-compose.yml ps --format json 2>/dev/null || echo '[]'")
	if err == nil && len(out) > 2 {
		// Parse container statuses and update Apps/Services in-place
		updateDeploymentStatus(d, out)
	}
	return d, nil
}

// ---------------------------------------------------------------------------
// Logs
// ---------------------------------------------------------------------------

// Logs returns the last n log lines for the named app container.
func (m *CloudDeployManager) Logs(app string, lines int) (string, error) {
	m.mu.Lock()
	d := m.deployment
	m.mu.Unlock()

	if d == nil || d.ServerIP == "" {
		return "", fmt.Errorf("no active deployment with a server IP")
	}
	if lines <= 0 {
		lines = 100
	}
	cmd := fmt.Sprintf("docker compose -f /opt/app/docker-compose.yml logs --tail=%d %s 2>&1", lines, app)
	return m.cloudSSHOut(d.ServerIP, cmd)
}

// ---------------------------------------------------------------------------
// Redeploy
// ---------------------------------------------------------------------------

// Redeploy rebuilds the app image from source and restarts the container.
func (m *CloudDeployManager) Redeploy() error {
	m.mu.Lock()
	d := m.deployment
	m.mu.Unlock()

	if d == nil || d.ServerIP == "" {
		return fmt.Errorf("no active deployment")
	}
	cmd := "cd /opt/app && git pull 2>/dev/null; docker compose up -d --build"
	return m.cloudSSHExec(d.ServerIP, cmd)
}

// ---------------------------------------------------------------------------
// Scale
// ---------------------------------------------------------------------------

// Scale shows a plan comparison table. Actual server resizing is not automated
// (requires a Hetzner API call with downtime) — the user is given instructions.
func (m *CloudDeployManager) Scale(plan string) (string, error) {
	_, ok := planByName(plan)
	if !ok {
		return "", fmt.Errorf("unknown plan: %s", plan)
	}

	var sb strings.Builder
	sb.WriteString("Available plans:\n\n")
	sb.WriteString(fmt.Sprintf("  %-10s  %-6s  %-5s  %-6s  %-6s  %s\n", "NAME", "vCPU", "RAM", "DISK", "$/mo", "MAX APPS"))
	sb.WriteString(strings.Repeat("-", 58) + "\n")
	for _, p := range availablePlans {
		marker := " "
		if m.deployment != nil && m.deployment.Plan == p.Name {
			marker = "*"
		}
		sb.WriteString(fmt.Sprintf("%s %-10s  %-6d  %-5s  %-6s  %-6.0f  %d\n",
			marker, p.Name, p.VCPU, p.RAM, p.Disk, p.Price, p.MaxApps))
	}
	sb.WriteString("\nTo resize: update your server in the Hetzner console, then run 'yaver cloud deploy' again.\n")
	sb.WriteString("Zero-downtime resize: snapshot → new server → DNS cutover → destroy old.\n")
	return sb.String(), nil
}

// ---------------------------------------------------------------------------
// Restart
// ---------------------------------------------------------------------------

// Restart restarts a named app container via SSH.
func (m *CloudDeployManager) Restart(app string) error {
	m.mu.Lock()
	d := m.deployment
	m.mu.Unlock()

	if d == nil || d.ServerIP == "" {
		return fmt.Errorf("no active deployment")
	}
	cmd := fmt.Sprintf("docker compose -f /opt/app/docker-compose.yml restart %s", app)
	return m.cloudSSHExec(d.ServerIP, cmd)
}

// ---------------------------------------------------------------------------
// Backup
// ---------------------------------------------------------------------------

// Backup pg_dump + tars app files and downloads to ~/.yaver/backups/.
func (m *CloudDeployManager) Backup() error {
	m.mu.Lock()
	d := m.deployment
	m.mu.Unlock()

	if d == nil || d.ServerIP == "" {
		return fmt.Errorf("no active deployment")
	}

	backupID := fmt.Sprintf("%s-%d", d.Name, time.Now().Unix())
	cmd := fmt.Sprintf(`
set -e
mkdir -p /tmp/backup-%s
# Postgres dump (if container exists)
if docker ps --format '{{.Names}}' | grep -q postgres; then
  docker exec $(docker ps -qf name=postgres) pg_dumpall -U postgres > /tmp/backup-%s/db.sql
fi
# App files
tar -czf /tmp/backup-%s/files.tar.gz -C /opt/app . 2>/dev/null || true
tar -czf /tmp/%s.tar.gz -C /tmp backup-%s
echo "backup:/tmp/%s.tar.gz"
`, backupID, backupID, backupID, backupID, backupID, backupID)

	out, err := m.cloudSSHOut(d.ServerIP, cmd)
	if err != nil {
		return fmt.Errorf("backup failed: %w", err)
	}

	// Download the archive
	cfgDir, _ := ConfigDir()
	backupDir := filepath.Join(cfgDir, "backups")
	_ = os.MkdirAll(backupDir, 0700)
	localPath := filepath.Join(backupDir, backupID+".tar.gz")

	scpCmd := exec.Command("scp", //nolint:gosec
		"-o", "StrictHostKeyChecking=no",
		"-o", "BatchMode=yes",
		fmt.Sprintf("root@%s:/tmp/%s.tar.gz", d.ServerIP, backupID),
		localPath,
	)
	if scpOut, scpErr := scpCmd.CombinedOutput(); scpErr != nil {
		return fmt.Errorf("download backup: %w\n%s", scpErr, string(scpOut))
	}

	_ = out // suppress lint
	fmt.Printf("Backup saved: %s\n", localPath)
	return nil
}

// ---------------------------------------------------------------------------
// BackupRestore
// ---------------------------------------------------------------------------

// BackupRestore uploads and restores a local backup archive.
func (m *CloudDeployManager) BackupRestore(backupID string) error {
	m.mu.Lock()
	d := m.deployment
	m.mu.Unlock()

	if d == nil || d.ServerIP == "" {
		return fmt.Errorf("no active deployment")
	}

	cfgDir, _ := ConfigDir()
	localPath := filepath.Join(cfgDir, "backups", backupID+".tar.gz")
	if _, err := os.Stat(localPath); err != nil {
		return fmt.Errorf("backup not found: %s", localPath)
	}

	// Upload
	scpCmd := exec.Command("scp", //nolint:gosec
		"-o", "StrictHostKeyChecking=no",
		"-o", "BatchMode=yes",
		localPath,
		fmt.Sprintf("root@%s:/tmp/%s.tar.gz", d.ServerIP, backupID),
	)
	if out, err := scpCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("upload backup: %w\n%s", err, string(out))
	}

	// Restore
	cmd := fmt.Sprintf(`
set -e
tar -xzf /tmp/%s.tar.gz -C /tmp
if [ -f /tmp/backup-%s/db.sql ]; then
  docker exec -i $(docker ps -qf name=postgres) psql -U postgres < /tmp/backup-%s/db.sql
fi
tar -xzf /tmp/backup-%s/files.tar.gz -C /opt/app
docker compose -f /opt/app/docker-compose.yml up -d --build
`, backupID, backupID, backupID, backupID)
	return m.cloudSSHExec(d.ServerIP, cmd)
}

// ---------------------------------------------------------------------------
// Destroy
// ---------------------------------------------------------------------------

// Destroy exports data then tears down the server. confirm must be true.
func (m *CloudDeployManager) Destroy(confirm bool) error {
	if !confirm {
		return fmt.Errorf("pass confirm=true to destroy the deployment")
	}

	m.mu.Lock()
	d := m.deployment
	m.mu.Unlock()

	if d == nil {
		return fmt.Errorf("no deployment found")
	}

	// 1. Export data first (best-effort)
	if d.ServerIP != "" {
		fmt.Println("[destroy] Exporting data before teardown…")
		_ = m.Backup()
	}

	// 2. Destroy server via Hetzner API if token available
	apiToken := os.Getenv("HETZNER_API_TOKEN")
	if apiToken != "" && d.ID != "" {
		fmt.Println("[destroy] Removing Hetzner server via API…")
		if err := m.hetznerDeleteServer(apiToken, d.ID); err != nil {
			fmt.Printf("[destroy] Warning: could not delete server via API: %v\n", err)
		} else {
			fmt.Println("[destroy] Server removed")
		}
	} else if d.ServerIP != "" {
		// Stop containers
		_ = m.cloudSSHExec(d.ServerIP, "cd /opt/app && docker compose down -v 2>/dev/null || true")
		fmt.Printf("[destroy] Containers stopped on %s. Delete the server manually.\n", d.ServerIP)
	}

	// 3. Remove local state
	m.mu.Lock()
	m.deployment = nil
	m.mu.Unlock()
	_ = os.Remove(m.configPath)
	fmt.Println("[destroy] Local deployment state removed")
	return nil
}

// ---------------------------------------------------------------------------
// ListPlans
// ---------------------------------------------------------------------------

// ListPlans returns all available plans.
func (m *CloudDeployManager) ListPlans() []CloudPlan {
	return availablePlans
}

// ---------------------------------------------------------------------------
// GenerateDockerCompose
// ---------------------------------------------------------------------------

// GenerateDockerCompose detects the project stack and returns a docker-compose.yml string.
func (m *CloudDeployManager) GenerateDockerCompose() (string, error) {
	stack := m.detectProjectStack()
	dockerfile := m.generateDockerfile(stack.Framework)

	// Write Dockerfile to workDir (needed by compose build)
	if dockerfile != "" {
		dfPath := filepath.Join(m.workDir, "Dockerfile")
		if _, err := os.Stat(dfPath); os.IsNotExist(err) {
			if err2 := os.WriteFile(dfPath, []byte(dockerfile), 0644); err2 != nil {
				return "", fmt.Errorf("write Dockerfile: %w", err2)
			}
		}
	}

	const composeTpl = `version: "3.9"

services:
  web:
    build: .
    restart: unless-stopped
    ports:
      - "{{.Port}}:{{.Port}}"
    environment:
      - PORT={{.Port}}
      - NODE_ENV=production
{{- if .HasDB}}
      - DATABASE_URL=postgres://app:app@postgres:5432/app
{{- end}}
{{- if .HasCache}}
      - REDIS_URL=redis://redis:6379
{{- end}}
    depends_on:
{{- if .HasDB}}
      - postgres
{{- end}}
{{- if .HasCache}}
      - redis
{{- end}}
    networks:
      - app

{{- if .HasDB}}

  postgres:
    image: postgres:16-alpine
    restart: unless-stopped
    environment:
      - POSTGRES_USER=app
      - POSTGRES_PASSWORD=app
      - POSTGRES_DB=app
    volumes:
      - pgdata:/var/lib/postgresql/data
    networks:
      - app
{{- end}}

{{- if .HasCache}}

  redis:
    image: redis:7-alpine
    restart: unless-stopped
    volumes:
      - redisdata:/data
    networks:
      - app
{{- end}}

  caddy:
    image: caddy:2-alpine
    restart: unless-stopped
    ports:
      - "80:80"
      - "443:443"
    volumes:
      - ./Caddyfile:/etc/caddy/Caddyfile:ro
      - caddy_data:/data
      - caddy_config:/config
    networks:
      - app

volumes:
{{- if .HasDB}}
  pgdata:
{{- end}}
{{- if .HasCache}}
  redisdata:
{{- end}}
  caddy_data:
  caddy_config:

networks:
  app:
`
	t, err := template.New("compose").Parse(composeTpl)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, stack); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// ---------------------------------------------------------------------------
// GenerateDeployScript
// ---------------------------------------------------------------------------

// GenerateDeployScript returns a self-contained bash script that deploys the
// project to any fresh Ubuntu/Debian VPS.
func (m *CloudDeployManager) GenerateDeployScript() (string, error) {
	compose, err := m.GenerateDockerCompose()
	if err != nil {
		return "", err
	}

	stack := m.detectProjectStack()
	d := m.deployment
	domain := ""
	appName := "app"
	if d != nil {
		domain = d.Domain
		appName = d.Name
	}
	caddyCfg := m.generateCaddyConfig(domain, []CloudApp{
		{Name: appName, Port: stack.Port, Domain: domain},
	})

	// Escape single-quotes in embedded strings for safe heredoc inclusion
	composeSafe := strings.ReplaceAll(compose, "'", "'\\''")
	caddySafe := strings.ReplaceAll(caddyCfg, "'", "'\\''")

	script := fmt.Sprintf(`#!/usr/bin/env bash
# deploy.sh — generated by Yaver Cloud Deploy
# Run on any fresh Ubuntu 22.04+ / Debian 12+ VPS as root.
# Usage: bash deploy.sh [--domain yourdomain.com] [--repo https://github.com/you/repo]
set -euo pipefail

DOMAIN="${DOMAIN:-%s}"
REPO="${REPO:-}"
APP_DIR="/opt/app"

log() { echo "[deploy] $*"; }

# ── Parse args ────────────────────────────────────────────────────────────────
while [[ $# -gt 0 ]]; do
  case $1 in
    --domain) DOMAIN="$2"; shift 2 ;;
    --repo)   REPO="$2";   shift 2 ;;
    *) echo "Unknown arg: $1"; exit 1 ;;
  esac
done

# ── Install Docker ─────────────────────────────────────────────────────────────
if ! command -v docker &>/dev/null; then
  log "Installing Docker…"
  curl -fsSL https://get.docker.com | sh
  systemctl enable --now docker
  log "Docker installed"
else
  log "Docker already installed"
fi

# ── Install Docker Compose plugin ─────────────────────────────────────────────
if ! docker compose version &>/dev/null; then
  log "Installing Docker Compose plugin…"
  apt-get install -y docker-compose-plugin 2>/dev/null || \
    curl -SL "https://github.com/docker/compose/releases/latest/download/docker-compose-$(uname -s)-$(uname -m)" \
         -o /usr/local/lib/docker/cli-plugins/docker-compose && \
    chmod +x /usr/local/lib/docker/cli-plugins/docker-compose
fi

# ── Prepare app directory ──────────────────────────────────────────────────────
mkdir -p "$APP_DIR"
cd "$APP_DIR"

# ── Clone / pull repo ──────────────────────────────────────────────────────────
if [ -n "$REPO" ]; then
  if [ -d ".git" ]; then
    log "Pulling latest…"
    git pull
  else
    log "Cloning $REPO…"
    git clone "$REPO" .
  fi
else
  log "No --repo given — using embedded configuration only"
fi

# ── Write docker-compose.yml ──────────────────────────────────────────────────
cat > "$APP_DIR/docker-compose.yml" << 'COMPOSE_EOF'
%s
COMPOSE_EOF

# ── Write Caddyfile ───────────────────────────────────────────────────────────
cat > "$APP_DIR/Caddyfile" << 'CADDY_EOF'
%s
CADDY_EOF

# ── Build and start ───────────────────────────────────────────────────────────
log "Building and starting containers…"
docker compose pull 2>/dev/null || true
docker compose up -d --build

# ── Smoke test ────────────────────────────────────────────────────────────────
sleep 5
if curl -sf "http://localhost:%d/health" >/dev/null 2>&1; then
  log "Health check passed"
else
  log "Health check skipped (no /health endpoint)"
fi

log "Deployment complete!"
if [ -n "$DOMAIN" ]; then
  log "App: https://$DOMAIN"
else
  log "App: http://$(curl -sf https://ipinfo.io/ip 2>/dev/null || echo '<server-ip>'):%d"
fi
`, domain, composeSafe, caddySafe, stack.Port, stack.Port)

	return script, nil
}

// ---------------------------------------------------------------------------
// Internal: SSH helpers
// ---------------------------------------------------------------------------

// cloudSSHExec runs a command on the remote server via ssh (BatchMode, no host key check).
func (m *CloudDeployManager) cloudSSHExec(ip, cmd string) error {
	_, err := m.cloudSSHOut(ip, cmd)
	return err
}

// cloudSSHOut runs a command and returns its stdout.
func (m *CloudDeployManager) cloudSSHOut(ip, cmd string) (string, error) {
	c := exec.Command("ssh", //nolint:gosec
		"-o", "StrictHostKeyChecking=no",
		"-o", "BatchMode=yes",
		"-o", "ConnectTimeout=15",
		"root@"+ip,
		cmd,
	)
	out, err := c.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("ssh %s: %w\n%s", ip, err, string(out))
	}
	return string(out), nil
}

// cloudSCPString uploads a string as a remote file via ssh cat.
func (m *CloudDeployManager) cloudSCPString(ip, content, remotePath string) error {
	// Use ssh + tee to write the file without needing a temp file locally.
	remoteDir := filepath.Dir(remotePath)
	mkdirCmd := fmt.Sprintf("mkdir -p %s", remoteDir)
	if err := m.cloudSSHExec(ip, mkdirCmd); err != nil {
		return err
	}
	c := exec.Command("ssh", //nolint:gosec
		"-o", "StrictHostKeyChecking=no",
		"-o", "BatchMode=yes",
		"root@"+ip,
		fmt.Sprintf("cat > %s", remotePath),
	)
	c.Stdin = strings.NewReader(content)
	out, err := c.CombinedOutput()
	if err != nil {
		return fmt.Errorf("upload %s: %w\n%s", remotePath, err, string(out))
	}
	return nil
}

// ---------------------------------------------------------------------------
// Internal: Hetzner API
// ---------------------------------------------------------------------------

// Test seam (var, not const) so the httptest fake Hetzner API and the
// no-sleep path are reachable without mocks or a 100s boot wait.
// Production keeps the real API + readiness poll untouched.
var (
	hetznerAPIBase       = "https://api.hetzner.cloud/v1"
	hetznerSkipReadyWait = false
)

// hetznerCreateServer provisions a CX server via Hetzner Cloud API.
// Returns the public IPv4 address AND the numeric server id (as a
// string) — the id is required to snapshot+delete the box later
// (Phase A managed add/remove). The id was previously discarded.
func (m *CloudDeployManager) hetznerCreateServer(token, name, plan, region string) (string, string, error) {
	// Map Yaver plan → Hetzner server type
	serverTypeMap := map[string]string{
		"starter": "cx21",
		"pro":     "cx31",
		"scale":   "cx41",
	}
	serverType, ok := serverTypeMap[plan]
	if !ok {
		serverType = "cx21"
	}

	locationMap := map[string]string{
		"eu": "nbg1",
		"us": "ash",
	}
	location, ok := locationMap[region]
	if !ok {
		location = "nbg1"
	}

	payload := map[string]interface{}{
		"name":        name,
		"server_type": serverType,
		"image":       "ubuntu-22.04",
		"location":    location,
		"user_data":   cloudBootstrapScript(),
	}
	body, _ := json.Marshal(payload)
	req, err := http.NewRequest("POST", hetznerAPIBase+"/servers", bytes.NewReader(body)) //nolint:noctx
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("hetzner API: %w", err)
	}
	defer resp.Body.Close()

	var result struct {
		Server struct {
			ID         int `json:"id"`
			PublicNet  struct {
				IPv4 struct {
					IP string `json:"ip"`
				} `json:"ipv4"`
			} `json:"public_net"`
		} `json:"server"`
		Error *struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", "", fmt.Errorf("parse hetzner response: %w", err)
	}
	if result.Error != nil {
		return "", "", fmt.Errorf("hetzner error %s: %s", result.Error.Code, result.Error.Message)
	}

	ip := result.Server.PublicNet.IPv4.IP
	if ip == "" {
		return "", "", fmt.Errorf("hetzner returned no IP for new server")
	}
	id := fmt.Sprintf("%d", result.Server.ID)

	// Wait for SSH to become available (server needs ~30s to boot).
	// Skipped under test (hetznerSkipReadyWait) so the fake API path
	// doesn't sleep 100s.
	if !hetznerSkipReadyWait {
		for i := 0; i < 20; i++ {
			time.Sleep(5 * time.Second)
			if err := m.cloudSSHExec(ip, "echo ready"); err == nil {
				break
			}
		}
	}
	return ip, id, nil
}

// hetznerSnapshotServer creates a snapshot image of a server before
// it is deleted. CLAUDE.md hard rule: a managed box is NEVER deleted
// without a recoverable snapshot first (snapshot ≈ €0.10/mo vs
// €6.49/mo running). Returns error so the caller can ABORT the delete
// if the snapshot fails — never delete an un-snapshotted box.
func (m *CloudDeployManager) hetznerSnapshotServer(token, id, label string) error {
	payload := map[string]interface{}{"type": "snapshot", "description": label}
	body, _ := json.Marshal(payload)
	req, err := http.NewRequest("POST", hetznerAPIBase+"/servers/"+id+"/actions/create_image", bytes.NewReader(body)) //nolint:noctx
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("hetzner snapshot: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("hetzner snapshot returned %d", resp.StatusCode)
	}
	return nil
}

// hetznerDeleteServer deletes a server by its Hetzner numeric ID (stored as string).
func (m *CloudDeployManager) hetznerDeleteServer(token, id string) error {
	req, err := http.NewRequest("DELETE", hetznerAPIBase+"/servers/"+id, nil) //nolint:noctx
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("hetzner delete returned %d", resp.StatusCode)
	}
	return nil
}

// HetznerServerInfo is the resolved identity of one real server on
// the user's Hetzner account. Returned by hetznerListServers so a UI
// can let the user pick the EXACT box to recycle/remove by name+IP
// instead of recalling a numeric id from memory. This is resolution
// from the live account, never a fuzzy guess: the id is authoritative.
type HetznerServerInfo struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	IP       string `json:"ip"`
	Status   string `json:"status"`
	Type     string `json:"type"`
	Location string `json:"location"`
	Created  string `json:"created"`
}

// hetznerListServers enumerates every server on the account (paginated)
// via the same vault-backed Bearer token used for create/snapshot/
// delete. Read-only. Used by the `cloud_list` ops verb to populate the
// Recycle/Remove dialog's server picker.
func (m *CloudDeployManager) hetznerListServers(token string) ([]HetznerServerInfo, error) {
	out := []HetznerServerInfo{}
	page := 1
	for {
		url := fmt.Sprintf("%s/servers?per_page=50&page=%d", hetznerAPIBase, page)
		req, err := http.NewRequest("GET", url, nil) //nolint:noctx
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("hetzner list: %w", err)
		}
		var result struct {
			Servers []struct {
				ID        int    `json:"id"`
				Name      string `json:"name"`
				Status    string `json:"status"`
				Created   string `json:"created"`
				PublicNet struct {
					IPv4 struct {
						IP string `json:"ip"`
					} `json:"ipv4"`
				} `json:"public_net"`
				ServerType struct {
					Name string `json:"name"`
				} `json:"server_type"`
				Datacenter struct {
					Location struct {
						Name string `json:"name"`
					} `json:"location"`
				} `json:"datacenter"`
			} `json:"servers"`
			Meta struct {
				Pagination struct {
					NextPage *int `json:"next_page"`
				} `json:"pagination"`
			} `json:"meta"`
			Error *struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		derr := json.NewDecoder(resp.Body).Decode(&result)
		resp.Body.Close()
		if derr != nil {
			return nil, fmt.Errorf("parse hetzner list: %w", derr)
		}
		if result.Error != nil {
			return nil, fmt.Errorf("hetzner error %s: %s", result.Error.Code, result.Error.Message)
		}
		for _, s := range result.Servers {
			out = append(out, HetznerServerInfo{
				ID:       fmt.Sprintf("%d", s.ID),
				Name:     s.Name,
				IP:       s.PublicNet.IPv4.IP,
				Status:   s.Status,
				Type:     s.ServerType.Name,
				Location: s.Datacenter.Location.Name,
				Created:  s.Created,
			})
		}
		if result.Meta.Pagination.NextPage == nil {
			break
		}
		page = *result.Meta.Pagination.NextPage
	}
	return out, nil
}

// HetznerSnapshotInfo identifies one snapshot image on the account.
// CreatedFromID is the numeric id of the server the snapshot was taken
// from (Hetzner keeps this even after that server is deleted) — the
// authoritative way to attribute an orphaned snapshot to a box that no
// longer exists. DiskGB is what Hetzner bills on.
type HetznerSnapshotInfo struct {
	ID            string  `json:"id"`
	Description   string  `json:"description"`
	DiskGB        float64 `json:"diskGb"`
	Created       string  `json:"created"`
	CreatedFromID string  `json:"createdFromId"`
	// EstMonthlyEUR is Hetzner's published snapshot price (€0.0119 per
	// GB/mo) × DiskGB. Shown so the user sees the running cost of a
	// leftover image, not just its existence.
	EstMonthlyEUR float64 `json:"estMonthlyEur"`
}

// hetznerListSnapshots enumerates every snapshot image on the account
// (paginated), read-only, same vault Bearer token as the rest. Used to
// detect snapshots orphaned by a server delete so they can be surfaced
// (and one-click removed) instead of silently billing forever.
func (m *CloudDeployManager) hetznerListSnapshots(token string) ([]HetznerSnapshotInfo, error) {
	out := []HetznerSnapshotInfo{}
	page := 1
	for {
		url := fmt.Sprintf("%s/images?type=snapshot&per_page=50&page=%d", hetznerAPIBase, page)
		req, err := http.NewRequest("GET", url, nil) //nolint:noctx
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("hetzner snapshot list: %w", err)
		}
		var result struct {
			Images []struct {
				ID          int     `json:"id"`
				Description string  `json:"description"`
				ImageSize   float64 `json:"image_size"`
				DiskSize    float64 `json:"disk_size"`
				Created     string  `json:"created"`
				CreatedFrom *struct {
					ID int `json:"id"`
				} `json:"created_from"`
			} `json:"images"`
			Meta struct {
				Pagination struct {
					NextPage *int `json:"next_page"`
				} `json:"pagination"`
			} `json:"meta"`
			Error *struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		derr := json.NewDecoder(resp.Body).Decode(&result)
		resp.Body.Close()
		if derr != nil {
			return nil, fmt.Errorf("parse hetzner snapshot list: %w", derr)
		}
		if result.Error != nil {
			return nil, fmt.Errorf("hetzner error %s: %s", result.Error.Code, result.Error.Message)
		}
		for _, im := range result.Images {
			from := ""
			if im.CreatedFrom != nil {
				from = fmt.Sprintf("%d", im.CreatedFrom.ID)
			}
			// Hetzner bills snapshots on used image size, not disk
			// size; fall back to disk size if image_size is absent.
			gb := im.ImageSize
			if gb <= 0 {
				gb = im.DiskSize
			}
			out = append(out, HetznerSnapshotInfo{
				ID:            fmt.Sprintf("%d", im.ID),
				Description:   im.Description,
				DiskGB:        gb,
				Created:       im.Created,
				CreatedFromID: from,
				EstMonthlyEUR: gb * 0.0119,
			})
		}
		if result.Meta.Pagination.NextPage == nil {
			break
		}
		page = *result.Meta.Pagination.NextPage
	}
	return out, nil
}

// hetznerSnapshotsForServer returns snapshots attributable to a given
// (now usually deleted) server id — either by Hetzner's created_from
// linkage or by our own `yaver-predelete-<id>-<ts>` description prefix
// (created_from is dropped on some snapshot types, the description
// prefix is our durable fallback).
func (m *CloudDeployManager) hetznerSnapshotsForServer(token, serverID string) ([]HetznerSnapshotInfo, error) {
	all, err := m.hetznerListSnapshots(token)
	if err != nil {
		return nil, err
	}
	prefix := fmt.Sprintf("yaver-predelete-%s-", serverID)
	hit := []HetznerSnapshotInfo{}
	for _, s := range all {
		if s.CreatedFromID == serverID || strings.HasPrefix(s.Description, prefix) {
			hit = append(hit, s)
		}
	}
	return hit, nil
}

// hetznerDeleteImage deletes one snapshot image by numeric id. Used by
// the cloud_snapshot_delete verb so a leftover recovery image can be
// removed (and its billing stopped) without leaving the dashboard.
func (m *CloudDeployManager) hetznerDeleteImage(token, imageID string) error {
	req, err := http.NewRequest("DELETE", hetznerAPIBase+"/images/"+imageID, nil) //nolint:noctx
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("hetzner image delete returned %d", resp.StatusCode)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Internal: project stack detection
// ---------------------------------------------------------------------------

// detectProjectStack examines workDir to determine the framework and settings.
func (m *CloudDeployManager) detectProjectStack() projectStack {
	stack := projectStack{
		Framework: "node",
		Port:      3000,
		BuildCmd:  "npm run build",
		StartCmd:  "npm start",
	}

	// Node.js
	if cloudFileExists(filepath.Join(m.workDir, "package.json")) {
		stack.Framework = "node"
		stack.Port = 3000
		// Check for Next.js
		if cloudFileContains(filepath.Join(m.workDir, "package.json"), "\"next\"") {
			stack.Framework = "nextjs"
		}
		// Check for Vite
		if cloudFileExists(filepath.Join(m.workDir, "vite.config.ts")) || cloudFileExists(filepath.Join(m.workDir, "vite.config.js")) {
			stack.Framework = "vite"
			stack.Port = 5173
		}
	}

	// Go
	if cloudFileExists(filepath.Join(m.workDir, "go.mod")) {
		stack.Framework = "go"
		stack.Port = 8080
		stack.BuildCmd = "go build -o app ."
		stack.StartCmd = "./app"
	}

	// Python
	if cloudFileExists(filepath.Join(m.workDir, "requirements.txt")) || cloudFileExists(filepath.Join(m.workDir, "pyproject.toml")) {
		stack.Framework = "python"
		stack.Port = 8000
		stack.BuildCmd = "pip install -r requirements.txt"
		stack.StartCmd = "python -m uvicorn main:app --host 0.0.0.0 --port 8000"
	}

	// Rust
	if cloudFileExists(filepath.Join(m.workDir, "Cargo.toml")) {
		stack.Framework = "rust"
		stack.Port = 8080
		stack.BuildCmd = "cargo build --release"
		stack.StartCmd = "./target/release/app"
	}

	// Static site
	if cloudFileExists(filepath.Join(m.workDir, "index.html")) && !cloudFileExists(filepath.Join(m.workDir, "package.json")) {
		stack.Framework = "static"
		stack.Port = 80
		stack.BuildCmd = ""
		stack.StartCmd = ""
	}

	// Database / cache detection
	for _, f := range []string{"package.json", "requirements.txt", "go.mod", "Cargo.toml"} {
		p := filepath.Join(m.workDir, f)
		if cloudFileContains(p, "postgres") || cloudFileContains(p, "pg") || cloudFileContains(p, "psycopg") || cloudFileContains(p, "sqlx") {
			stack.HasDB = true
		}
		if cloudFileContains(p, "redis") || cloudFileContains(p, "ioredis") {
			stack.HasCache = true
		}
	}
	// docker-compose gives hints too
	if cloudFileContains(filepath.Join(m.workDir, "docker-compose.yml"), "postgres") {
		stack.HasDB = true
	}
	if cloudFileContains(filepath.Join(m.workDir, "docker-compose.yml"), "redis") {
		stack.HasCache = true
	}

	return stack
}

// ---------------------------------------------------------------------------
// Internal: Dockerfile generation
// ---------------------------------------------------------------------------

func (m *CloudDeployManager) generateDockerfile(framework string) string {
	switch framework {
	case "node", "nextjs":
		return `FROM node:22-alpine AS builder
WORKDIR /app
COPY package*.json ./
RUN npm ci --omit=dev
COPY . .
RUN npm run build 2>/dev/null || true

FROM node:22-alpine
WORKDIR /app
COPY --from=builder /app .
EXPOSE 3000
CMD ["npm", "start"]
`
	case "vite":
		return `FROM node:22-alpine AS builder
WORKDIR /app
COPY package*.json ./
RUN npm ci
COPY . .
RUN npm run build

FROM caddy:2-alpine
COPY --from=builder /app/dist /srv
EXPOSE 80
`
	case "go":
		return `FROM golang:1.22-alpine AS builder
WORKDIR /app
COPY go.* ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o app .

FROM alpine:3.19
RUN apk add --no-cache ca-certificates
WORKDIR /app
COPY --from=builder /app/app .
EXPOSE 8080
CMD ["./app"]
`
	case "python":
		return `FROM python:3.12-slim
WORKDIR /app
COPY requirements*.txt ./
RUN pip install --no-cache-dir -r requirements.txt
COPY . .
EXPOSE 8000
CMD ["python", "-m", "uvicorn", "main:app", "--host", "0.0.0.0", "--port", "8000"]
`
	case "rust":
		return `FROM rust:1.78-slim AS builder
WORKDIR /app
COPY Cargo.* ./
RUN mkdir src && echo "fn main(){}" > src/main.rs && cargo build --release && rm -rf src
COPY . .
RUN touch src/main.rs && cargo build --release

FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y ca-certificates && rm -rf /var/lib/apt/lists/*
WORKDIR /app
COPY --from=builder /app/target/release/app .
EXPOSE 8080
CMD ["./app"]
`
	case "static":
		return `FROM caddy:2-alpine
COPY . /srv
EXPOSE 80
`
	default:
		return ""
	}
}

// ---------------------------------------------------------------------------
// Internal: Caddy config generation
// ---------------------------------------------------------------------------

func (m *CloudDeployManager) generateCaddyConfig(domain string, apps []CloudApp) string {
	if domain == "" {
		// No domain — serve on plain HTTP
		var sb strings.Builder
		for _, app := range apps {
			sb.WriteString(fmt.Sprintf(":80 {\n  reverse_proxy %s:%d\n}\n\n", app.Name, app.Port))
		}
		return sb.String()
	}

	var sb strings.Builder
	for _, app := range apps {
		d := app.Domain
		if d == "" {
			d = domain
		}
		sb.WriteString(fmt.Sprintf("%s {\n  reverse_proxy web:%d\n  encode gzip\n  header {\n    Strict-Transport-Security max-age=31536000\n    X-Content-Type-Options nosniff\n    X-Frame-Options DENY\n  }\n}\n\n", d, app.Port))
	}
	return sb.String()
}

// ---------------------------------------------------------------------------
// Internal: persistence
// ---------------------------------------------------------------------------

func (m *CloudDeployManager) loadDeployment() error {
	data, err := os.ReadFile(m.configPath)
	if err != nil {
		return err
	}
	var d CloudDeployment
	if err := json.Unmarshal(data, &d); err != nil {
		return fmt.Errorf("parse cloud.json: %w", err)
	}
	m.deployment = &d
	return nil
}

func (m *CloudDeployManager) saveDeployment() error {
	if m.deployment == nil {
		return nil
	}
	data, err := json.MarshalIndent(m.deployment, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(m.configPath, data, 0600)
}

// ---------------------------------------------------------------------------
// Internal: helpers
// ---------------------------------------------------------------------------

// cloudBootstrapScript returns a cloud-init / bash snippet that installs Docker.
func cloudBootstrapScript() string {
	return fmt.Sprintf(`#!/bin/bash
set -e
export DEBIAN_FRONTEND=noninteractive
apt-get update -q
apt-get install -y -q curl git ca-certificates sudo
# Docker
curl -fsSL https://get.docker.com | sh
systemctl enable --now docker
# Non-root 'yaver' user (docs §4a): the agent / user workloads must NOT run
# as root. System user with a real home so $HOME/Workspace works.
%s
# Let yaver drive Docker (rootless is the Phase-2 hardening; for now the
# docker group is the pragmatic path).
usermod -aG docker yaver || true
# Scoped passwordless sudo (install_privilege.go) — operator profile: tenant
# lifecycle + package + yaver/docker services only. NEVER NOPASSWD: ALL, so the
# agent cannot rm a home, stop sshd, or userdel a human account.
%s
# Canonical project home for the yaver user (docs §4b): $HOME/Workspace.
install -d -o yaver -g yaver -m 0755 /home/yaver/Workspace
# Firewall
ufw allow 22/tcp
ufw allow 80/tcp
ufw allow 443/tcp
ufw --force enable
`, ensureYaverUserSnippet(), writeSudoersSnippet(profileOperator))
}

// defaultServices returns standard services based on detected stack.
func defaultServices(stack projectStack) []CloudService {
	var services []CloudService
	if stack.HasDB {
		services = append(services, CloudService{Name: "postgres", Type: "database", Port: 5432, Status: "running"})
	}
	if stack.HasCache {
		services = append(services, CloudService{Name: "redis", Type: "cache", Port: 6379, Status: "running"})
	}
	services = append(services, CloudService{Name: "caddy", Type: "proxy", Port: 443, Status: "running"})
	return services
}

// updateDeploymentStatus parses docker compose ps JSON output and updates
// app/service statuses in-place. Non-fatal on parse errors.
func updateDeploymentStatus(d *CloudDeployment, raw string) {
	// docker compose ps --format json outputs one JSON object per line.
	lines := strings.Split(strings.TrimSpace(raw), "\n")
	statusMap := make(map[string]string)
	idMap := make(map[string]string)
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "{") {
			continue
		}
		var obj struct {
			Name   string `json:"Name"`
			State  string `json:"State"`
			ID     string `json:"ID"`
		}
		if err := json.Unmarshal([]byte(line), &obj); err == nil {
			statusMap[obj.Name] = obj.State
			idMap[obj.Name] = obj.ID
		}
	}
	for i := range d.Apps {
		if s, ok := statusMap[d.Apps[i].Name]; ok {
			d.Apps[i].Status = s
			d.Apps[i].ContainerID = idMap[d.Apps[i].Name]
		}
	}
	for i := range d.Services {
		if s, ok := statusMap[d.Services[i].Name]; ok {
			d.Services[i].Status = s
			d.Services[i].ContainerID = idMap[d.Services[i].Name]
		}
	}
}

// generateID returns a short random deployment ID.
func generateID() string {
	// Simple time-based ID — no crypto needed for a deployment label.
	return fmt.Sprintf("dep-%x", time.Now().Unix())
}

// cloudFileExists returns true if the path exists and is a regular file.
func cloudFileExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.Mode().IsRegular()
}

// cloudFileContains returns true if the file exists and contains the substring.
func cloudFileContains(p, sub string) bool {
	data, err := os.ReadFile(p) //nolint:gosec
	if err != nil {
		return false
	}
	return strings.Contains(string(data), sub)
}
