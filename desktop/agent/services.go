package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

// DevServiceConfig represents a single service in .yaver/services.yaml
type DevServiceConfig struct {
	Image       string            `yaml:"image" json:"image"`
	Binary      string            `yaml:"binary,omitempty" json:"binary,omitempty"` // non-Docker binary (e.g. mailpit)
	Port        int               `yaml:"port" json:"port"`
	ConsolePort int               `yaml:"console_port,omitempty" json:"consolePort,omitempty"`
	SMTPPort    int               `yaml:"smtp_port,omitempty" json:"smtpPort,omitempty"`
	WebPort     int               `yaml:"web_port,omitempty" json:"webPort,omitempty"`
	Env         map[string]string `yaml:"env,omitempty" json:"env,omitempty"`
	Volume      string            `yaml:"volume,omitempty" json:"volume,omitempty"`
	Engine      string            `yaml:"engine,omitempty" json:"engine,omitempty"` // e.g. better-auth, umami
	Command     string            `yaml:"command,omitempty" json:"command,omitempty"`
	Args        []string          `yaml:"args,omitempty" json:"args,omitempty"` // explicit argv for binary services (companion workers)
	WorkDir     string            `yaml:"workdir,omitempty" json:"workDir,omitempty"`
	HealthCheck string            `yaml:"healthcheck,omitempty" json:"healthcheck,omitempty"`
}

// DevServicesConfig is the top-level .yaver/services.yaml structure
type DevServicesConfig struct {
	Services map[string]*DevServiceConfig `yaml:"services" json:"services"`
}

// ServiceStatus is the runtime status of a service
type ServiceStatus struct {
	Name      string `json:"name"`
	Running   bool   `json:"running"`
	Port      int    `json:"port"`
	Image     string `json:"image,omitempty"`
	Container string `json:"container,omitempty"`
	Health    string `json:"health"` // healthy, unhealthy, starting, stopped
	Uptime    string `json:"uptime,omitempty"`
	Memory    string `json:"memory,omitempty"`
}

// ServicesManager manages the unified local service stack
type ServicesManager struct {
	mu       sync.Mutex
	workDir  string
	yamlPath string
	config   *DevServicesConfig
}

// NewServicesManager creates a new services manager for the given work directory
func NewServicesManager(workDir string) *ServicesManager {
	return &ServicesManager{
		workDir:  workDir,
		yamlPath: filepath.Join(workDir, ".yaver", "services.yaml"),
	}
}

// dotYaverDir returns the .yaver directory path and ensures it exists.
func (sm *ServicesManager) dotYaverDir() (string, error) {
	dir := filepath.Join(sm.workDir, ".yaver")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", fmt.Errorf("create .yaver dir: %w", err)
	}
	return dir, nil
}

// composePath returns the path to the generated docker-compose.yml.
func (sm *ServicesManager) composePath() string {
	return filepath.Join(sm.workDir, ".yaver", "docker-compose.yml")
}

// LoadConfig loads .yaver/services.yaml, creating a default file if it does not exist.
func (sm *ServicesManager) LoadConfig() (*DevServicesConfig, error) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	if sm.config != nil {
		return sm.config, nil
	}

	if _, err := os.Stat(sm.yamlPath); os.IsNotExist(err) {
		cfg := &DevServicesConfig{Services: map[string]*DevServiceConfig{}}
		if err := sm.saveConfigLocked(cfg); err != nil {
			return nil, err
		}
		sm.config = cfg
		return cfg, nil
	}

	data, err := os.ReadFile(sm.yamlPath)
	if err != nil {
		return nil, fmt.Errorf("read services.yaml: %w", err)
	}

	var cfg DevServicesConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse services.yaml: %w", err)
	}
	if cfg.Services == nil {
		cfg.Services = map[string]*DevServiceConfig{}
	}

	sm.config = &cfg
	return sm.config, nil
}

// SaveConfig writes the config to .yaver/services.yaml.
func (sm *ServicesManager) SaveConfig(cfg *DevServicesConfig) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	return sm.saveConfigLocked(cfg)
}

// saveConfigLocked writes without acquiring the lock (caller must hold it).
func (sm *ServicesManager) saveConfigLocked(cfg *DevServicesConfig) error {
	if _, err := sm.dotYaverDir(); err != nil {
		return err
	}

	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal services.yaml: %w", err)
	}

	if err := os.WriteFile(sm.yamlPath, data, 0644); err != nil {
		return fmt.Errorf("write services.yaml: %w", err)
	}

	sm.config = cfg
	return nil
}

// presets returns the default DevServiceConfig for known service names.
func presets() map[string]*DevServiceConfig {
	return map[string]*DevServiceConfig{
		"postgres": {
			Image:  "postgres:16",
			Port:   5432,
			Volume: "yaver-pg-data",
			Env: map[string]string{
				"POSTGRES_DB":       "myapp",
				"POSTGRES_USER":     "postgres",
				"POSTGRES_PASSWORD": "dev",
			},
			HealthCheck: "pg_isready -U postgres",
		},
		"redis": {
			Image: "redis:7-alpine",
			Port:  6379,
		},
		"minio": {
			Image:       "minio/minio",
			Port:        9000,
			ConsolePort: 9001,
			Command:     "server /data --console-address :9001",
			Volume:      "yaver-minio-data",
			Env: map[string]string{
				"MINIO_ROOT_USER":     "minioadmin",
				"MINIO_ROOT_PASSWORD": "minioadmin",
			},
		},
		"mailpit": {
			Binary:   "mailpit",
			Port:     8025,
			SMTPPort: 1025,
			WebPort:  8025,
		},
		"umami": {
			Image:  "ghcr.io/umami-software/umami:postgresql-latest",
			Port:   3333,
			Engine: "umami",
			Env: map[string]string{
				"DATABASE_URL": "postgresql://umami:umami@postgres:5432/umami",
				"DATABASE_TYPE": "postgresql",
			},
		},
		"posthog": {
			Image: "posthog/posthog:latest",
			Port:  8000,
		},
		"logto": {
			Image:  "svhd/logto:latest",
			Port:   3001,
			Engine: "logto",
			Env: map[string]string{
				"DB_URL":    "postgresql://logto:logto@postgres:5432/logto",
				"ENDPOINT": "http://localhost:3001",
			},
		},
		"meili": {
			Image:  "getmeili/meilisearch:latest",
			Port:   7700,
			Volume: "yaver-meili-data",
			Env: map[string]string{
				"MEILI_MASTER_KEY": "dev-key",
			},
		},
		"pocketbase": {
			Image:  "ghcr.io/muchobien/pocketbase:latest",
			Port:   8090,
			Volume: "yaver-pb-data",
		},
		"postgres-replica": {
			Image:  "postgres:16",
			Port:   5433,
			Volume: "yaver-pg-replica-data",
			Env: map[string]string{
				"POSTGRES_USER":       "postgres",
				"POSTGRES_PASSWORD":   "dev",
				"PGUSER":              "postgres",
				"POSTGRES_DB":         "myapp",
			},
			// Command: run base_backup → recovery, then start as replica.
			// Concrete setup handled by ConfigureReplication below.
		},
		"dynamodb-local": {
			Image:   "amazon/dynamodb-local:latest",
			Port:    8000,
			Command: "-jar DynamoDBLocal.jar -sharedDb -dbPath /home/dynamodblocal/data",
			Volume:  "yaver-ddb-data",
		},
		"elasticmq": {
			Image: "softwaremill/elasticmq-native:latest",
			Port:  9324,
		},
		"azurite": {
			Image:       "mcr.microsoft.com/azure-storage/azurite:latest",
			Port:        10000, // Blob
			ConsolePort: 10001, // Queue
			SMTPPort:    10002, // Table (reusing SMTPPort slot for multi-port mapping)
			Volume:      "yaver-azurite-data",
		},
		"code-server": {
			Image:  "codercom/code-server:latest",
			Port:   8787,
			Volume: "yaver-codeserver-data",
			Env: map[string]string{
				"PASSWORD": "yaver",
			},
			Command: "--bind-addr 0.0.0.0:8787 /home/coder/project",
		},
		"firebase-emulator": {
			Binary: "firebase",
			Port:   4000, // Emulator UI
		},
		"convex": {
			Image:       "ghcr.io/get-convex/convex-backend:latest",
			Port:        3210,
			ConsolePort: 3211, // HTTP Actions
			Volume:      "yaver-convex-data",
			Env: map[string]string{
				"INSTANCE_NAME":   "yaver-local",
				"CONVEX_SITE_URL": "http://127.0.0.1:3211",
			},
		},
		"convex-dashboard": {
			Image: "ghcr.io/get-convex/convex-dashboard:latest",
			Port:  6791,
			Env: map[string]string{
				"NEXT_PUBLIC_DEPLOYMENT_URL": "http://127.0.0.1:3210",
			},
		},
		"typesense": {
			Image:  "typesense/typesense:27.1",
			Port:   8108,
			Volume: "yaver-typesense-data",
			Command: "--data-dir /data --api-key=dev-key",
			Env: map[string]string{
				"TYPESENSE_API_KEY": "dev-key",
			},
		},
	}
}

// Add adds a service to the config, applying preset defaults when available.
// If cfg is nil, the preset for name is used. An error is returned if neither
// a preset nor a cfg is provided.
func (sm *ServicesManager) Add(name string, cfg *DevServiceConfig) (string, error) {
	current, err := sm.LoadConfig()
	if err != nil {
		return "", err
	}

	if cfg == nil {
		p, ok := presets()[name]
		if !ok {
			return "", fmt.Errorf("no preset for %q and no config provided; known presets: %s",
				name, strings.Join(presetNames(), ", "))
		}
		copy := *p
		cfg = &copy
	} else {
		// Fill in missing fields from preset if one exists
		if p, ok := presets()[name]; ok {
			if cfg.Image == "" {
				cfg.Image = p.Image
			}
			if cfg.Port == 0 {
				cfg.Port = p.Port
			}
			if cfg.Volume == "" {
				cfg.Volume = p.Volume
			}
			if len(cfg.Env) == 0 && len(p.Env) > 0 {
				cfg.Env = p.Env
			}
		}
	}

	sm.mu.Lock()
	current.Services[name] = cfg
	if err := sm.saveConfigLocked(current); err != nil {
		sm.mu.Unlock()
		return "", err
	}
	sm.mu.Unlock()

	return fmt.Sprintf("Added service %q (port %d). Run `yaver services start %s` to launch it.", name, cfg.Port, name), nil
}

// Remove stops and removes a service from the config.
func (sm *ServicesManager) Remove(name string) (string, error) {
	current, err := sm.LoadConfig()
	if err != nil {
		return "", err
	}

	cfg, ok := current.Services[name]
	if !ok {
		return "", fmt.Errorf("service %q not found in config", name)
	}

	// Stop the service first (best-effort; ignore errors)
	if cfg.Binary != "" {
		_ = sm.killBinaryService(name)
	} else {
		_ = sm.composeDown(name)
	}

	sm.mu.Lock()
	delete(current.Services, name)
	if err := sm.saveConfigLocked(current); err != nil {
		sm.mu.Unlock()
		return "", err
	}
	sm.mu.Unlock()

	return fmt.Sprintf("Removed service %q from config.", name), nil
}

// Start starts all or the specified services.
// It regenerates the docker-compose.yml, then calls `docker compose up -d` for
// Docker-based services and starts binaries directly for non-Docker services.
func (sm *ServicesManager) Start(names ...string) (string, error) {
	cfg, err := sm.LoadConfig()
	if err != nil {
		return "", err
	}
	if len(cfg.Services) == 0 {
		return "No services configured. Add one with `yaver services add <name>`.", nil
	}

	// Determine which services to start
	targets, err := sm.resolveTargets(cfg, names)
	if err != nil {
		return "", err
	}

	// Separate Docker services from binary services
	var dockerNames []string
	var binaryNames []string
	for _, n := range targets {
		if cfg.Services[n].Binary != "" {
			binaryNames = append(binaryNames, n)
		} else {
			dockerNames = append(dockerNames, n)
		}
	}

	var lines []string

	// Regenerate compose file for Docker services
	if len(dockerNames) > 0 {
		if err := sm.writeComposeFile(cfg, dockerNames); err != nil {
			return "", fmt.Errorf("generate docker-compose.yml: %w", err)
		}

		args := []string{"compose", "-p", "yaver-services",
			"-f", sm.composePath(), "up", "-d", "--remove-orphans"}
		args = append(args, dockerNames...)

		out, err := sm.runDocker(args...)
		if err != nil {
			return "", fmt.Errorf("docker compose up: %w\n%s", err, out)
		}
		for _, n := range dockerNames {
			lines = append(lines, fmt.Sprintf("  started (docker): %s", n))
		}
	}

	// Start binary services
	for _, n := range binaryNames {
		svc := cfg.Services[n]
		msg, err := sm.startBinaryService(n, svc)
		if err != nil {
			lines = append(lines, fmt.Sprintf("  failed: %s — %v", n, err))
		} else {
			lines = append(lines, fmt.Sprintf("  %s", msg))
		}
	}

	// If any of the started services back a known dashboard (Convex, Supabase,
	// PocketBase, Drizzle, etc.), auto-spin up the per-project tunnel so
	// users don't have to manually POST /dashboard/start.
	go func() {
		needsDashboard := false
		for _, n := range dockerNames {
			switch n {
			case "convex", "pocketbase", "supabase":
				needsDashboard = true
			}
		}
		if needsDashboard {
			_, _ = StartDashboard(sm.workDir)
		}
	}()

	return "Services started:\n" + strings.Join(lines, "\n"), nil
}

// Stop stops all or the specified services.
func (sm *ServicesManager) Stop(names ...string) (string, error) {
	cfg, err := sm.LoadConfig()
	if err != nil {
		return "", err
	}
	if len(cfg.Services) == 0 {
		return "No services configured.", nil
	}

	targets, err := sm.resolveTargets(cfg, names)
	if err != nil {
		return "", err
	}

	var dockerNames []string
	var lines []string

	for _, n := range targets {
		svc := cfg.Services[n]
		if svc.Binary != "" {
			if err := sm.killBinaryService(n); err != nil {
				lines = append(lines, fmt.Sprintf("  failed: %s — %v", n, err))
			} else {
				lines = append(lines, fmt.Sprintf("  stopped (binary): %s", n))
			}
		} else {
			dockerNames = append(dockerNames, n)
		}
	}

	if len(dockerNames) > 0 {
		args := []string{"compose", "-p", "yaver-services",
			"-f", sm.composePath(), "stop"}
		args = append(args, dockerNames...)

		out, err := sm.runDocker(args...)
		if err != nil {
			return "", fmt.Errorf("docker compose stop: %w\n%s", err, out)
		}
		for _, n := range dockerNames {
			lines = append(lines, fmt.Sprintf("  stopped (docker): %s", n))
		}
	}

	return "Services stopped:\n" + strings.Join(lines, "\n"), nil
}

// Status returns the runtime status of all configured services.
func (sm *ServicesManager) Status() ([]ServiceStatus, error) {
	cfg, err := sm.LoadConfig()
	if err != nil {
		return nil, err
	}

	// Collect live docker compose ps data
	type composePSEntry struct {
		Name    string `json:"Name"`
		Service string `json:"Service"`
		State   string `json:"State"`
		Health  string `json:"Health"`
		Image   string `json:"Image"`
	}

	liveContainers := map[string]*composePSEntry{}

	psJSON, err := sm.runDocker("compose", "-p", "yaver-services",
		"-f", sm.composePath(), "ps", "--format", "json")
	if err == nil && strings.TrimSpace(psJSON) != "" {
		// docker compose ps --format json can emit one JSON object per line or a JSON array
		lines := strings.Split(strings.TrimSpace(psJSON), "\n")
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if line == "" || line == "[]" {
				continue
			}
			// Strip leading/trailing brackets if it is an array
			if strings.HasPrefix(line, "[") {
				var arr []composePSEntry
				if err := json.Unmarshal([]byte(line), &arr); err == nil {
					for i := range arr {
						liveContainers[arr[i].Service] = &arr[i]
					}
				}
				continue
			}
			var entry composePSEntry
			if err := json.Unmarshal([]byte(line), &entry); err == nil {
				liveContainers[entry.Service] = &entry
			}
		}
	}

	// Collect docker stats for memory usage
	memUsage := map[string]string{} // container name → memory
	statsJSON, err := sm.runDocker("stats", "--no-stream", "--format",
		"{{json .}}")
	if err == nil && strings.TrimSpace(statsJSON) != "" {
		for _, line := range strings.Split(strings.TrimSpace(statsJSON), "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			var s struct {
				Name      string `json:"Name"`
				MemUsage  string `json:"MemUsage"`
			}
			if err := json.Unmarshal([]byte(line), &s); err == nil {
				parts := strings.SplitN(s.MemUsage, " / ", 2)
				memUsage[s.Name] = parts[0]
			}
		}
	}

	var statuses []ServiceStatus
	for name, svc := range cfg.Services {
		st := ServiceStatus{
			Name:   name,
			Port:   svc.Port,
			Image:  svc.Image,
			Health: "stopped",
		}

		if svc.Binary != "" {
			// Check if binary process is running
			running, pid := sm.isBinaryRunning(name)
			if running {
				st.Running = true
				st.Health = "healthy"
				st.Container = fmt.Sprintf("pid:%d", pid)
			}
		} else {
			if entry, ok := liveContainers[name]; ok {
				state := strings.ToLower(entry.State)
				st.Running = state == "running"
				st.Container = entry.Name
				st.Image = entry.Image

				switch {
				case entry.Health == "healthy":
					st.Health = "healthy"
				case entry.Health == "unhealthy":
					st.Health = "unhealthy"
				case state == "running" && entry.Health == "":
					st.Health = "healthy"
				case state == "starting":
					st.Health = "starting"
				default:
					st.Health = "stopped"
				}

				if mem, ok := memUsage[entry.Name]; ok {
					st.Memory = mem
				}
			}
		}

		statuses = append(statuses, st)
	}

	return statuses, nil
}

// Logs tails logs from a named service.
func (sm *ServicesManager) Logs(name string, lines int) (string, error) {
	cfg, err := sm.LoadConfig()
	if err != nil {
		return "", err
	}

	svc, ok := cfg.Services[name]
	if !ok {
		return "", fmt.Errorf("service %q not found", name)
	}

	if lines <= 0 {
		lines = 100
	}

	if svc.Binary != "" {
		// Binary services write to a log file in .yaver/logs/<name>.log
		logPath := filepath.Join(sm.workDir, ".yaver", "logs", name+".log")
		data, err := os.ReadFile(logPath)
		if err != nil {
			if os.IsNotExist(err) {
				return fmt.Sprintf("No log file found for %s at %s", name, logPath), nil
			}
			return "", fmt.Errorf("read log file: %w", err)
		}
		logLines := strings.Split(string(data), "\n")
		if len(logLines) > lines {
			logLines = logLines[len(logLines)-lines:]
		}
		return strings.Join(logLines, "\n"), nil
	}

	out, err := sm.runDocker("compose", "-p", "yaver-services",
		"-f", sm.composePath(), "logs",
		"--tail", strconv.Itoa(lines),
		"--no-color",
		name)
	if err != nil {
		return "", fmt.Errorf("docker compose logs: %w\n%s", err, out)
	}
	return out, nil
}

// generateComposeFile returns the docker-compose.yml content for the given services.
// If serviceFilter is non-empty, only those services are included.
func (sm *ServicesManager) generateComposeFile(cfg *DevServicesConfig, serviceFilter []string) (string, error) {
	filterSet := map[string]bool{}
	for _, n := range serviceFilter {
		filterSet[n] = true
	}

	var buf bytes.Buffer
	buf.WriteString("# Auto-generated by yaver services — do not edit manually\n")
	buf.WriteString("# Regenerated on: " + time.Now().Format(time.RFC3339) + "\n\n")
	buf.WriteString("name: yaver-services\n\n")
	buf.WriteString("networks:\n")
	buf.WriteString("  yaver-net:\n")
	buf.WriteString("    driver: bridge\n\n")

	// Collect volumes
	volumeSet := map[string]bool{}
	for name, svc := range cfg.Services {
		if len(filterSet) > 0 && !filterSet[name] {
			continue
		}
		if svc.Binary != "" {
			continue
		}
		if svc.Volume != "" {
			volumeSet[svc.Volume] = true
		}
	}

	if len(volumeSet) > 0 {
		buf.WriteString("volumes:\n")
		for vol := range volumeSet {
			buf.WriteString(fmt.Sprintf("  %s:\n", vol))
		}
		buf.WriteString("\n")
	}

	buf.WriteString("services:\n")

	for name, svc := range cfg.Services {
		if len(filterSet) > 0 && !filterSet[name] {
			continue
		}
		if svc.Binary != "" {
			// Binary services are not included in the compose file
			continue
		}

		buf.WriteString(fmt.Sprintf("  %s:\n", name))
		if svc.Image != "" {
			buf.WriteString(fmt.Sprintf("    image: %s\n", svc.Image))
		}
		buf.WriteString("    restart: unless-stopped\n")
		buf.WriteString("    networks:\n      - yaver-net\n")

		// Ports
		ports := []string{}
		if svc.Port > 0 {
			ports = append(ports, fmt.Sprintf("%d:%d", svc.Port, svc.Port))
		}
		if svc.ConsolePort > 0 {
			ports = append(ports, fmt.Sprintf("%d:%d", svc.ConsolePort, svc.ConsolePort))
		}
		if svc.SMTPPort > 0 {
			ports = append(ports, fmt.Sprintf("%d:%d", svc.SMTPPort, svc.SMTPPort))
		}
		if svc.WebPort > 0 && svc.WebPort != svc.Port {
			ports = append(ports, fmt.Sprintf("%d:%d", svc.WebPort, svc.WebPort))
		}
		if len(ports) > 0 {
			buf.WriteString("    ports:\n")
			for _, p := range ports {
				buf.WriteString(fmt.Sprintf("      - \"%s\"\n", p))
			}
		}

		// Environment
		if len(svc.Env) > 0 {
			buf.WriteString("    environment:\n")
			for k, v := range svc.Env {
				buf.WriteString(fmt.Sprintf("      %s: %q\n", k, v))
			}
		}

		// Volume
		if svc.Volume != "" {
			buf.WriteString("    volumes:\n")
			// Determine the container data path from convention
			dataPath := "/data"
			switch name {
			case "postgres":
				dataPath = "/var/lib/postgresql/data"
			case "redis":
				dataPath = "/data"
			case "meili":
				dataPath = "/meili_data"
			case "typesense":
				dataPath = "/data"
			case "convex":
				dataPath = "/convex/data"
			case "dynamodb-local":
				dataPath = "/home/dynamodblocal/data"
			case "pocketbase":
				dataPath = "/pb/pb_data"
			case "azurite":
				dataPath = "/data"
			case "code-server":
				dataPath = "/home/coder"
			case "postgres-replica":
				dataPath = "/var/lib/postgresql/data"
			}
			buf.WriteString(fmt.Sprintf("      - %s:%s\n", svc.Volume, dataPath))
		}

		// Command
		if svc.Command != "" {
			buf.WriteString(fmt.Sprintf("    command: %s\n", svc.Command))
		}

		// Health check
		if svc.HealthCheck != "" {
			buf.WriteString("    healthcheck:\n")
			buf.WriteString(fmt.Sprintf("      test: [\"CMD-SHELL\", \"%s\"]\n", svc.HealthCheck))
			buf.WriteString("      interval: 10s\n")
			buf.WriteString("      timeout: 5s\n")
			buf.WriteString("      retries: 5\n")
		}
	}

	return buf.String(), nil
}

// writeComposeFile generates and writes docker-compose.yml to .yaver/.
func (sm *ServicesManager) writeComposeFile(cfg *DevServicesConfig, filter []string) error {
	if _, err := sm.dotYaverDir(); err != nil {
		return err
	}

	content, err := sm.generateComposeFile(cfg, filter)
	if err != nil {
		return err
	}

	if err := os.WriteFile(sm.composePath(), []byte(content), 0644); err != nil {
		return fmt.Errorf("write docker-compose.yml: %w", err)
	}
	return nil
}

// startBinaryService launches a non-Docker binary service and captures its output
// to .yaver/logs/<name>.log.
func (sm *ServicesManager) startBinaryService(name string, svc *DevServiceConfig) (string, error) {
	// Companion workers set Command (the executable) explicitly; classic dev
	// presets (mailpit, …) set Binary. Command wins when present.
	binName := svc.Binary
	if strings.TrimSpace(svc.Command) != "" {
		binName = svc.Command
	}
	binPath, err := exec.LookPath(binName)
	if err != nil {
		return "", fmt.Errorf("binary %q not found in PATH: %w", binName, err)
	}

	// Check if already running
	if running, _ := sm.isBinaryRunning(name); running {
		return fmt.Sprintf("started (binary, already running): %s", name), nil
	}

	logDir := filepath.Join(sm.workDir, ".yaver", "logs")
	if err := os.MkdirAll(logDir, 0755); err != nil {
		return "", fmt.Errorf("create log dir: %w", err)
	}
	logFile, err := os.OpenFile(filepath.Join(logDir, name+".log"),
		os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return "", fmt.Errorf("open log file: %w", err)
	}

	var args []string
	switch {
	case len(svc.Args) > 0:
		// Explicit argv (companion workers).
		args = append(args, svc.Args...)
	case name == "mailpit":
		args = []string{
			"--smtp", fmt.Sprintf("0.0.0.0:%d", svc.SMTPPort),
			"--listen", fmt.Sprintf("0.0.0.0:%d", svc.WebPort),
		}
	}

	cmd := exec.Command(binPath, args...)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	if strings.TrimSpace(svc.WorkDir) != "" {
		cmd.Dir = svc.WorkDir
	}
	if len(svc.Env) > 0 {
		cmd.Env = os.Environ()
		for k, v := range svc.Env {
			cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", k, v))
		}
	}

	if err := cmd.Start(); err != nil {
		_ = logFile.Close()
		return "", fmt.Errorf("start %s: %w", svc.Binary, err)
	}

	// Write PID file so we can kill it later
	pidPath := sm.pidFilePath(name)
	_ = os.WriteFile(pidPath, []byte(strconv.Itoa(cmd.Process.Pid)), 0644)

	// Detach — do not wait, let it run in the background
	go func() {
		_ = cmd.Wait()
		_ = logFile.Close()
		_ = os.Remove(pidPath)
	}()

	return fmt.Sprintf("started (binary): %s (pid %d, port %d)", name, cmd.Process.Pid, svc.WebPort), nil
}

// killBinaryService sends SIGTERM to a running binary service via its PID file.
func (sm *ServicesManager) killBinaryService(name string) error {
	pidPath := sm.pidFilePath(name)
	data, err := os.ReadFile(pidPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // not running
		}
		return fmt.Errorf("read pid file: %w", err)
	}

	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return fmt.Errorf("invalid pid file: %w", err)
	}

	proc, err := os.FindProcess(pid)
	if err != nil {
		_ = os.Remove(pidPath)
		return nil
	}

	if err := proc.Signal(os.Interrupt); err != nil {
		// Process might already be gone
		_ = os.Remove(pidPath)
		return nil
	}

	_ = os.Remove(pidPath)
	return nil
}

// isBinaryRunning checks whether a binary service is still running via its PID file.
// Returns (running, pid).
func (sm *ServicesManager) isBinaryRunning(name string) (bool, int) {
	pidPath := sm.pidFilePath(name)
	data, err := os.ReadFile(pidPath)
	if err != nil {
		return false, 0
	}

	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return false, 0
	}

	proc, err := os.FindProcess(pid)
	if err != nil {
		return false, 0
	}

	// On Unix, FindProcess always succeeds; send signal 0 to check liveness
	if err := proc.Signal(os.Signal(nil)); err != nil {
		_ = os.Remove(pidPath)
		return false, 0
	}

	return true, pid
}

// pidFilePath returns the path to the PID file for a binary service.
func (sm *ServicesManager) pidFilePath(name string) string {
	return filepath.Join(sm.workDir, ".yaver", name+".pid")
}

// composeDown stops one or more docker compose services (not removal).
func (sm *ServicesManager) composeDown(names ...string) error {
	args := []string{"compose", "-p", "yaver-services",
		"-f", sm.composePath(), "stop"}
	args = append(args, names...)
	_, err := sm.runDocker(args...)
	return err
}

// resolveTargets returns the service names to operate on, validating each against
// the config.  If names is empty, all configured services are returned.
func (sm *ServicesManager) resolveTargets(cfg *DevServicesConfig, names []string) ([]string, error) {
	if len(names) == 0 {
		targets := make([]string, 0, len(cfg.Services))
		for n := range cfg.Services {
			targets = append(targets, n)
		}
		return targets, nil
	}

	for _, n := range names {
		if _, ok := cfg.Services[n]; !ok {
			return nil, fmt.Errorf("service %q not found in config; configured services: %s",
				n, strings.Join(configuredNames(cfg), ", "))
		}
	}
	return names, nil
}

// runDocker executes a docker command and returns combined stdout+stderr output.
func (sm *ServicesManager) runDocker(args ...string) (string, error) {
	dockerPath, err := exec.LookPath("docker")
	if err != nil {
		return "", fmt.Errorf("docker not found in PATH: %w", err)
	}

	cmd := exec.Command(dockerPath, args...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// configuredNames returns sorted service names from a config.
func configuredNames(cfg *DevServicesConfig) []string {
	names := make([]string, 0, len(cfg.Services))
	for n := range cfg.Services {
		names = append(names, n)
	}
	return names
}

// presetNames returns the list of known preset names.
func presetNames() []string {
	p := presets()
	names := make([]string, 0, len(p))
	for k := range p {
		names = append(names, k)
	}
	return names
}
