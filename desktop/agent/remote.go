package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// RemoteMachine represents a headless dev machine managed by Yaver.
// It may be a VPS, an old laptop, a Raspberry Pi, or any SSH-accessible host.
type RemoteMachine struct {
	ID          string    `json:"id"`
	Host        string    `json:"host"`
	User        string    `json:"user"`
	Label       string    `json:"label"`
	Platform    string    `json:"platform"` // "linux" or "darwin"
	Arch        string    `json:"arch"`     // "amd64", "arm64"
	Online      bool      `json:"online"`
	CPU         string    `json:"cpu,omitempty"`
	Memory      string    `json:"memory,omitempty"`
	Disk        string    `json:"disk,omitempty"`
	DiskUsed    string    `json:"diskUsed,omitempty"`
	Projects    []string  `json:"projects,omitempty"`
	Containers  int       `json:"containers"`
	CostEstimate float64  `json:"costEstimate,omitempty"` // USD/month
	Provider    string    `json:"provider,omitempty"`     // "hetzner", "digitalocean", "unknown"
	LastSeen    time.Time `json:"lastSeen"`

	// Provider-specific instance ID for API operations (destroy, snapshot).
	ProviderInstanceID string `json:"providerInstanceId,omitempty"`
}

// RemoteManager manages a fleet of headless dev machines.
// Machines are persisted to ~/.yaver/remotes.json between runs.
type RemoteManager struct {
	mu         sync.Mutex
	machines   map[string]*RemoteMachine
	configPath string // ~/.yaver/remotes.json
}

// NewRemoteManager creates a RemoteManager backed by ~/.yaver/remotes.json.
// Existing machines are loaded from disk; a missing config file is not an error.
func NewRemoteManager() *RemoteManager {
	home, _ := os.UserHomeDir()
	configPath := filepath.Join(home, configDirName, "remotes.json")

	m := &RemoteManager{
		machines:   make(map[string]*RemoteMachine),
		configPath: configPath,
	}
	// Best-effort load; ignore errors on first run.
	_ = m.loadConfig()
	return m
}

// Setup performs first-time configuration of a remote machine via SSH.
// It installs Docker, Node.js (via nvm), Git, and the Yaver agent binary,
// configures UFW firewall rules, sets timezone to UTC, and optionally
// creates swap space when the host has less than 4 GB of RAM.
// Returns a human-readable setup summary on success.
func (m *RemoteManager) Setup(host, user string) (string, error) {
	if host == "" || user == "" {
		return "", fmt.Errorf("host and user are required")
	}

	var summary strings.Builder
	summary.WriteString(fmt.Sprintf("Setting up remote machine %s@%s\n\n", user, host))

	// --- 1. Detect OS and architecture ---
	out, err := sshRun(host, user, "uname -sm")
	if err != nil {
		return "", fmt.Errorf("cannot connect to %s@%s: %w", user, host, err)
	}
	parts := strings.Fields(strings.TrimSpace(out))
	platform := "linux"
	arch := "amd64"
	if len(parts) >= 1 {
		switch strings.ToLower(parts[0]) {
		case "darwin":
			platform = "darwin"
		default:
			platform = "linux"
		}
	}
	if len(parts) >= 2 {
		switch strings.ToLower(parts[1]) {
		case "arm64", "aarch64":
			arch = "arm64"
		default:
			arch = "amd64"
		}
	}
	summary.WriteString(fmt.Sprintf("  Platform: %s/%s\n", platform, arch))

	// --- 2. Detect RAM for swap decision ---
	ramOut, _ := sshRun(host, user,
		`awk '/MemTotal/{printf "%d", $2/1024}' /proc/meminfo 2>/dev/null || sysctl -n hw.memsize 2>/dev/null | awk '{printf "%d", $1/1048576}'`)
	ramMB := 0
	fmt.Sscanf(strings.TrimSpace(ramOut), "%d", &ramMB)

	// --- 3. Build setup script ---
	var script strings.Builder

	// Error on first failure (except optional installs).
	script.WriteString("set -e\n")

	// Docker
	script.WriteString(`
# Install Docker if missing
if ! command -v docker &>/dev/null; then
  echo "[setup] Installing Docker..."
  if [ "$(uname)" = "Linux" ]; then
    curl -fsSL https://get.docker.com | sh
    sudo usermod -aG docker "$USER" || true
    sudo systemctl enable --now docker || true
  fi
fi
`)

	// Node.js via nvm
	script.WriteString(`
# Install Node.js via nvm if missing
if ! command -v node &>/dev/null; then
  echo "[setup] Installing Node.js via nvm..."
  curl -o- https://raw.githubusercontent.com/nvm-sh/nvm/v0.39.7/install.sh | bash
  export NVM_DIR="$HOME/.nvm"
  [ -s "$NVM_DIR/nvm.sh" ] && . "$NVM_DIR/nvm.sh"
  nvm install --lts
  nvm use --lts
fi
`)

	// Git
	script.WriteString(`
# Install Git if missing
if ! command -v git &>/dev/null; then
  echo "[setup] Installing Git..."
  if command -v apt-get &>/dev/null; then
    sudo apt-get install -y git
  elif command -v yum &>/dev/null; then
    sudo yum install -y git
  elif command -v dnf &>/dev/null; then
    sudo dnf install -y git
  fi
fi
`)

	// Yaver agent binary
	agentArch := arch
	if agentArch == "arm64" {
		agentArch = "arm64"
	} else {
		agentArch = "amd64"
	}
	agentURL := fmt.Sprintf(
		"https://github.com/kivanccakmak/yaver.io/releases/latest/download/yaver-%s-%s",
		platform, agentArch,
	)
	script.WriteString(fmt.Sprintf(`
# Install Yaver agent if missing or outdated
echo "[setup] Installing Yaver agent..."
curl -fsSL "%s" -o /tmp/yaver-new
chmod +x /tmp/yaver-new
sudo mv /tmp/yaver-new /usr/local/bin/yaver
echo "[setup] Yaver agent installed: $(yaver --version 2>/dev/null || echo unknown)"
`, agentURL))

	// Coding runners (claude / codex / opencode) + the codex Linux sandbox
	// sysctls. Install-only and non-fatal: auth never lands here at
	// provision time — it arrives via runner_auth_mirror / credentials_import
	// or the relayed device-auth flow. Without the sysctls, every codex task
	// on a stock Ubuntu box (apparmor_restrict_unprivileged_userns=1) fails
	// "runner not ready" with no auto-remediation.
	script.WriteString(`
# Coding runners (install-only; auth is mirrored later)
if command -v npm &>/dev/null; then
  command -v claude &>/dev/null || npm install -g @anthropic-ai/claude-code || echo "[setup] WARN: claude-code install failed"
  command -v codex  &>/dev/null || npm install -g @openai/codex || echo "[setup] WARN: codex install failed"
  command -v opencode &>/dev/null || npm install -g opencode-ai || echo "[setup] WARN: opencode install failed"
fi
`)
	if platform == "linux" {
		script.WriteString(`
# Codex sandbox prerequisites (unprivileged user namespaces)
if [ "$(uname)" = "Linux" ]; then
  sudo tee /etc/sysctl.d/99-yaver-runner-sandbox.conf >/dev/null <<'SYSCTL'
kernel.unprivileged_userns_clone=1
user.max_user_namespaces=1048576
SYSCTL
  if [ -f /proc/sys/kernel/apparmor_restrict_unprivileged_userns ]; then
    echo "kernel.apparmor_restrict_unprivileged_userns=0" | sudo tee -a /etc/sysctl.d/99-yaver-runner-sandbox.conf >/dev/null
  fi
  sudo sysctl --system >/dev/null 2>&1 || true
fi
`)
	}

	// Firewall (Linux only)
	if platform == "linux" {
		script.WriteString(`
# Configure UFW firewall
if command -v ufw &>/dev/null; then
  echo "[setup] Configuring firewall..."
  sudo ufw allow 22/tcp   || true
  sudo ufw allow 80/tcp   || true
  sudo ufw allow 443/tcp  || true
  sudo ufw allow 18080/tcp || true
  sudo ufw --force enable  || true
fi
`)
	}

	// Timezone
	script.WriteString(`
# Set timezone to UTC
echo "[setup] Setting timezone to UTC..."
if command -v timedatectl &>/dev/null; then
  sudo timedatectl set-timezone UTC || true
elif [ -f /usr/share/zoneinfo/UTC ]; then
  sudo ln -sf /usr/share/zoneinfo/UTC /etc/localtime || true
fi
`)

	// Swap (only if RAM < 4096 MB)
	if ramMB > 0 && ramMB < 4096 {
		script.WriteString(`
# Add 2 GB swap (low-RAM machine)
if [ ! -f /swapfile ]; then
  echo "[setup] Creating 2 GB swap..."
  sudo fallocate -l 2G /swapfile 2>/dev/null || sudo dd if=/dev/zero of=/swapfile bs=1M count=2048
  sudo chmod 600 /swapfile
  sudo mkswap /swapfile
  sudo swapon /swapfile
  echo '/swapfile none swap sw 0 0' | sudo tee -a /etc/fstab > /dev/null
fi
`)
		summary.WriteString(fmt.Sprintf("  RAM: %d MB — swap will be configured\n", ramMB))
	}

	script.WriteString(`echo "[setup] Done."`)

	// --- 4. Execute setup script ---
	setupOut, err := sshRun(host, user, script.String())
	if err != nil {
		return "", fmt.Errorf("setup script failed on %s@%s: %w\nOutput:\n%s", user, host, err, setupOut)
	}
	summary.WriteString("\nSetup output:\n")
	summary.WriteString(setupOut)

	// --- 5. Persist machine to config ---
	id := machineID(host, user)
	label := host
	if idx := strings.IndexByte(host, '.'); idx > 0 {
		label = host[:idx]
	}

	m.mu.Lock()
	m.machines[id] = &RemoteMachine{
		ID:       id,
		Host:     host,
		User:     user,
		Label:    label,
		Platform: platform,
		Arch:     arch,
		Online:   true,
		Provider: detectProvider(host),
		LastSeen: time.Now(),
	}
	m.mu.Unlock()

	if err := m.saveConfig(); err != nil {
		summary.WriteString(fmt.Sprintf("\nWarning: could not save config: %v\n", err))
	}

	summary.WriteString(fmt.Sprintf("\nMachine %q (%s) saved to %s\n", label, id, m.configPath))
	return summary.String(), nil
}

// Status returns a live dashboard of all managed machines.
// It probes each machine in parallel via SSH and populates CPU, memory, disk,
// container count, active projects, and uptime. Unreachable machines are
// marked Online=false but still included in the result.
// Results are sorted by Label.
func (m *RemoteManager) Status() ([]RemoteMachine, error) {
	m.mu.Lock()
	ids := make([]string, 0, len(m.machines))
	for id := range m.machines {
		ids = append(ids, id)
	}
	m.mu.Unlock()

	if len(ids) == 0 {
		return nil, nil
	}

	type result struct {
		id string
		rm RemoteMachine
	}

	results := make(chan result, len(ids))
	var wg sync.WaitGroup

	for _, id := range ids {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()

			m.mu.Lock()
			src := *m.machines[id]
			m.mu.Unlock()

			rm := src
			rm.Online = false

			statsScript := `
CPU=$(top -bn1 2>/dev/null | grep "Cpu(s)" | awk '{print $2+$4"%"}' 2>/dev/null || echo "n/a")
MEM=$(free -h 2>/dev/null | awk '/^Mem/{print $3"/"$2}' || echo "n/a")
DISK_TOTAL=$(df -h / 2>/dev/null | awk 'NR==2{print $2}' || echo "n/a")
DISK_USED=$(df -h / 2>/dev/null | awk 'NR==2{print $3}' || echo "n/a")
CONTAINERS=$(docker ps -q 2>/dev/null | wc -l | tr -d ' ' || echo "0")
PROJECTS=$(find ~ /workspace /srv 2>/dev/null -maxdepth 3 -name "package.json" -o -name "go.mod" -o -name "pubspec.yaml" 2>/dev/null | grep -v node_modules | grep -v vendor | head -20 | xargs -I{} dirname {} 2>/dev/null | tr '\n' '|' || echo "")
UPTIME=$(uptime -p 2>/dev/null || uptime | awk '{print $3,$4}')
printf 'CPU=%s\nMEM=%s\nDISK_TOTAL=%s\nDISK_USED=%s\nCONTAINERS=%s\nPROJECTS=%s\nUPTIME=%s\n' \
  "$CPU" "$MEM" "$DISK_TOTAL" "$DISK_USED" "$CONTAINERS" "$PROJECTS" "$UPTIME"
`
			out, err := sshRun(rm.Host, rm.User, statsScript)
			if err != nil {
				results <- result{id: id, rm: rm}
				return
			}

			rm.Online = true
			rm.LastSeen = time.Now()

			for _, line := range strings.Split(out, "\n") {
				kv := strings.SplitN(strings.TrimSpace(line), "=", 2)
				if len(kv) != 2 {
					continue
				}
				val := strings.TrimSpace(kv[1])
				switch kv[0] {
				case "CPU":
					rm.CPU = val
				case "MEM":
					rm.Memory = val
				case "DISK_TOTAL":
					rm.Disk = val
				case "DISK_USED":
					rm.DiskUsed = val
				case "CONTAINERS":
					fmt.Sscanf(val, "%d", &rm.Containers)
				case "PROJECTS":
					if val != "" && val != "|" {
						var projs []string
						for _, p := range strings.Split(val, "|") {
							p = strings.TrimSpace(p)
							if p != "" {
								projs = append(projs, p)
							}
						}
						rm.Projects = projs
					}
				}
			}

			results <- result{id: id, rm: rm}
		}(id)
	}

	wg.Wait()
	close(results)

	machines := make([]RemoteMachine, 0, len(ids))
	m.mu.Lock()
	for r := range results {
		m.machines[r.id] = &r.rm
		machines = append(machines, r.rm)
	}
	m.mu.Unlock()

	_ = m.saveConfig()

	sort.Slice(machines, func(i, j int) bool {
		return machines[i].Label < machines[j].Label
	})
	return machines, nil
}

// SSHKey manages SSH keys for a remote machine.
// Actions:
//   - "generate": create a new ed25519 key pair at ~/.ssh/yaver_<host>
//   - "add":      copy the default public key to the remote's authorized_keys
//   - "list":     list authorized keys on the remote host
//   - "remove":   remove the public key matching the local default key
func (m *RemoteManager) SSHKey(action, host string) (string, error) {
	switch action {
	case "generate":
		home, _ := os.UserHomeDir()
		keyPath := filepath.Join(home, ".ssh", "yaver_"+strings.ReplaceAll(host, ".", "_"))
		if _, err := os.Stat(keyPath); err == nil {
			return fmt.Sprintf("Key already exists at %s\n", keyPath), nil
		}
		cmd := exec.Command("ssh-keygen", "-t", "ed25519", "-C", "yaver@"+host,
			"-f", keyPath, "-N", "")
		out, err := cmd.CombinedOutput()
		if err != nil {
			return "", fmt.Errorf("ssh-keygen failed: %w\n%s", err, out)
		}
		return fmt.Sprintf("Generated key pair:\n  Private: %s\n  Public:  %s.pub\n", keyPath, keyPath), nil

	case "add":
		pubKey, err := defaultPublicKey()
		if err != nil {
			return "", fmt.Errorf("no SSH public key found: %w", err)
		}
		user, err := m.userForHost(host)
		if err != nil {
			return "", err
		}
		script := fmt.Sprintf(`
mkdir -p ~/.ssh && chmod 700 ~/.ssh
echo %q >> ~/.ssh/authorized_keys
chmod 600 ~/.ssh/authorized_keys
echo "Key added."
`, strings.TrimSpace(pubKey))
		out, err := sshRun(host, user, script)
		if err != nil {
			return "", fmt.Errorf("failed to add key to %s@%s: %w", user, host, err)
		}
		return out, nil

	case "list":
		user, err := m.userForHost(host)
		if err != nil {
			return "", err
		}
		out, err := sshRun(host, user, "cat ~/.ssh/authorized_keys 2>/dev/null || echo '(no authorized_keys)'")
		if err != nil {
			return "", fmt.Errorf("failed to list keys on %s@%s: %w", user, host, err)
		}
		return out, nil

	case "remove":
		pubKey, err := defaultPublicKey()
		if err != nil {
			return "", fmt.Errorf("no SSH public key found: %w", err)
		}
		user, err := m.userForHost(host)
		if err != nil {
			return "", err
		}
		// Escape special characters for grep
		keyLine := strings.TrimSpace(pubKey)
		script := fmt.Sprintf(`
if [ -f ~/.ssh/authorized_keys ]; then
  grep -vF %q ~/.ssh/authorized_keys > /tmp/.ak.tmp && mv /tmp/.ak.tmp ~/.ssh/authorized_keys
  echo "Key removed (if it was present)."
else
  echo "No authorized_keys file."
fi
`, keyLine)
		out, err := sshRun(host, user, script)
		if err != nil {
			return "", fmt.Errorf("failed to remove key from %s@%s: %w", user, host, err)
		}
		return out, nil

	default:
		return "", fmt.Errorf("unknown SSH key action %q; valid: generate, add, list, remove", action)
	}
}

// Provision spins up a new VPS from a cloud provider and runs Setup on it.
// If the required API token is not set in the environment, it returns a
// manual setup script and instructions instead of erroring.
//
// Supported providers: "hetzner", "digitalocean"
// Sizes:   small | medium | large
// Regions: eu | us | asia
func (m *RemoteManager) Provision(provider, size, region string) (string, error) {
	switch strings.ToLower(provider) {
	case "hetzner":
		return m.provisionHetzner(size, region)
	case "digitalocean", "do":
		return m.provisionDigitalOcean(size, region)
	default:
		return "", fmt.Errorf("unknown provider %q; supported: hetzner, digitalocean", provider)
	}
}

func (m *RemoteManager) provisionHetzner(size, region string) (string, error) {
	token := os.Getenv("HETZNER_API_TOKEN")
	if token == "" {
		return hetznerManualInstructions(size, region), nil
	}

	serverType := map[string]string{
		"small": "cx22", "medium": "cx32", "large": "cx42",
	}[strings.ToLower(size)]
	if serverType == "" {
		serverType = "cx22"
	}

	locationMap := map[string]string{
		"eu": "nbg1", "us": "ash", "asia": "sin",
	}
	location := locationMap[strings.ToLower(region)]
	if location == "" {
		location = "nbg1"
	}

	// Fetch default SSH key from Hetzner account to embed in new server.
	sshKeyIDs, _ := m.hetznerSSHKeyIDs(token)

	body := map[string]interface{}{
		"name":        fmt.Sprintf("yaver-%d", time.Now().Unix()),
		"server_type": serverType,
		"location":    location,
		"image":       "ubuntu-22.04",
		"ssh_keys":    sshKeyIDs,
		"user_data":   "#!/bin/bash\napt-get update -y && apt-get install -y curl git\n",
	}

	resp, err := hetznerAPI(token, "POST", "/v1/servers", body)
	if err != nil {
		return "", fmt.Errorf("Hetzner create server: %w", err)
	}

	var result struct {
		Server struct {
			ID         int    `json:"id"`
			Name       string `json:"name"`
			PublicNet  struct {
				IPv4 struct {
					IP string `json:"ip"`
				} `json:"ipv4"`
			} `json:"public_net"`
		} `json:"server"`
	}
	if err := json.Unmarshal(resp, &result); err != nil {
		return "", fmt.Errorf("parse Hetzner response: %w\nBody: %s", err, resp)
	}

	ip := result.Server.PublicNet.IPv4.IP
	name := result.Server.Name

	// Wait for SSH to become available (up to 2 min).
	if err := waitForSSH(ip, "root", 120*time.Second); err != nil {
		return "", fmt.Errorf("server %s (%s) did not become reachable: %w", name, ip, err)
	}

	summary, err := m.Setup(ip, "root")
	if err != nil {
		return "", fmt.Errorf("Setup failed on %s: %w", ip, err)
	}

	// Record provider instance ID for future API calls.
	id := machineID(ip, "root")
	m.mu.Lock()
	if rm := m.machines[id]; rm != nil {
		rm.ProviderInstanceID = fmt.Sprintf("%d", result.Server.ID)
		rm.CostEstimate = hetznerCostEstimate(serverType)
	}
	m.mu.Unlock()
	_ = m.saveConfig()

	return fmt.Sprintf("Hetzner server %s provisioned at %s\n\n%s", name, ip, summary), nil
}

func (m *RemoteManager) provisionDigitalOcean(size, region string) (string, error) {
	token := os.Getenv("DIGITALOCEAN_TOKEN")
	if token == "" {
		return doManualInstructions(size, region), nil
	}

	slug := map[string]string{
		"small": "s-1vcpu-2gb", "medium": "s-2vcpu-4gb", "large": "s-4vcpu-8gb",
	}[strings.ToLower(size)]
	if slug == "" {
		slug = "s-1vcpu-2gb"
	}

	regionSlug := map[string]string{
		"eu": "fra1", "us": "nyc3", "asia": "sgp1",
	}[strings.ToLower(region)]
	if regionSlug == "" {
		regionSlug = "nyc3"
	}

	body := map[string]interface{}{
		"name":   fmt.Sprintf("yaver-%d", time.Now().Unix()),
		"region": regionSlug,
		"size":   slug,
		"image":  "ubuntu-22-04-x64",
	}

	resp, err := digitaloceanAPI(token, "POST", "/v2/droplets", body)
	if err != nil {
		return "", fmt.Errorf("DigitalOcean create droplet: %w", err)
	}

	var result struct {
		Droplet struct {
			ID      int    `json:"id"`
			Name    string `json:"name"`
			Networks struct {
				V4 []struct {
					IPAddress string `json:"ip_address"`
					Type      string `json:"type"`
				} `json:"v4"`
			} `json:"networks"`
		} `json:"droplet"`
	}
	if err := json.Unmarshal(resp, &result); err != nil {
		return "", fmt.Errorf("parse DO response: %w\nBody: %s", err, resp)
	}

	ip := ""
	for _, n := range result.Droplet.Networks.V4 {
		if n.Type == "public" {
			ip = n.IPAddress
			break
		}
	}
	if ip == "" {
		// DO provisioning is async; wait a bit then re-fetch.
		time.Sleep(15 * time.Second)
		ip, _ = m.doDropletIP(token, result.Droplet.ID)
	}
	if ip == "" {
		return "", fmt.Errorf("could not determine droplet IP for ID %d", result.Droplet.ID)
	}

	if err := waitForSSH(ip, "root", 120*time.Second); err != nil {
		return "", fmt.Errorf("droplet %s (%s) did not become reachable: %w", result.Droplet.Name, ip, err)
	}

	summary, err := m.Setup(ip, "root")
	if err != nil {
		return "", fmt.Errorf("Setup failed on %s: %w", ip, err)
	}

	id := machineID(ip, "root")
	m.mu.Lock()
	if rm := m.machines[id]; rm != nil {
		rm.ProviderInstanceID = fmt.Sprintf("%d", result.Droplet.ID)
		rm.CostEstimate = doCostEstimate(slug)
	}
	m.mu.Unlock()
	_ = m.saveConfig()

	return fmt.Sprintf("DigitalOcean droplet %s provisioned at %s\n\n%s", result.Droplet.Name, ip, summary), nil
}

// Destroy tears down a VPS via its provider API and removes it from config.
// confirm must be true to prevent accidental deletion.
func (m *RemoteManager) Destroy(machineID string, confirm bool) (string, error) {
	if !confirm {
		return "", fmt.Errorf("pass confirm=true to destroy machine %q", machineID)
	}

	m.mu.Lock()
	rm, ok := m.machines[machineID]
	if !ok {
		m.mu.Unlock()
		return "", fmt.Errorf("machine %q not found", machineID)
	}
	provider := rm.Provider
	instanceID := rm.ProviderInstanceID
	label := rm.Label
	m.mu.Unlock()

	var detail string
	switch provider {
	case "hetzner":
		token := os.Getenv("HETZNER_API_TOKEN")
		if token == "" {
			detail = "HETZNER_API_TOKEN not set — remove server manually from https://console.hetzner.cloud"
		} else if instanceID == "" {
			detail = "no provider instance ID recorded — remove server manually"
		} else {
			_, err := hetznerAPI(token, "DELETE", "/v1/servers/"+instanceID, nil)
			if err != nil {
				return "", fmt.Errorf("Hetzner delete server %s: %w", instanceID, err)
			}
			detail = fmt.Sprintf("Hetzner server %s deleted", instanceID)
		}

	case "digitalocean":
		token := os.Getenv("DIGITALOCEAN_TOKEN")
		if token == "" {
			detail = "DIGITALOCEAN_TOKEN not set — remove droplet manually from https://cloud.digitalocean.com"
		} else if instanceID == "" {
			detail = "no provider instance ID recorded — remove droplet manually"
		} else {
			_, err := digitaloceanAPI(token, "DELETE", "/v2/droplets/"+instanceID, nil)
			if err != nil {
				return "", fmt.Errorf("DigitalOcean delete droplet %s: %w", instanceID, err)
			}
			detail = fmt.Sprintf("DigitalOcean droplet %s deleted", instanceID)
		}

	default:
		detail = fmt.Sprintf("provider %q not supported for automated destroy — remove manually", provider)
	}

	m.mu.Lock()
	delete(m.machines, machineID)
	m.mu.Unlock()
	_ = m.saveConfig()

	return fmt.Sprintf("Machine %q (%s) removed from config.\n%s\n", label, machineID, detail), nil
}

// Cost returns a formatted monthly cost breakdown across all managed machines.
func (m *RemoteManager) Cost() (string, error) {
	m.mu.Lock()
	machines := make([]RemoteMachine, 0, len(m.machines))
	for _, rm := range m.machines {
		machines = append(machines, *rm)
	}
	m.mu.Unlock()

	if len(machines) == 0 {
		return "No machines configured.\n", nil
	}

	sort.Slice(machines, func(i, j int) bool {
		return machines[i].Label < machines[j].Label
	})

	var total float64
	var sb strings.Builder
	sb.WriteString("Monthly cost estimate:\n\n")
	sb.WriteString(fmt.Sprintf("  %-24s %-14s %-12s %s\n", "Label", "Host", "Provider", "Cost/mo"))
	sb.WriteString("  " + strings.Repeat("-", 60) + "\n")

	for _, rm := range machines {
		cost := "$?.??"
		if rm.CostEstimate > 0 {
			cost = fmt.Sprintf("$%.2f", rm.CostEstimate)
			total += rm.CostEstimate
		}
		sb.WriteString(fmt.Sprintf("  %-24s %-14s %-12s %s\n",
			truncateLabel(rm.Label, 22), truncateLabel(rm.Host, 14), rm.Provider, cost))
	}

	sb.WriteString("  " + strings.Repeat("-", 60) + "\n")
	if total > 0 {
		sb.WriteString(fmt.Sprintf("  %-52s $%.2f/mo\n", "Total", total))
	} else {
		sb.WriteString("  (no cost data available)\n")
	}
	return sb.String(), nil
}

// Snapshot creates a provider-level backup of a remote machine.
// For Hetzner it creates a server image; for DigitalOcean a droplet snapshot.
func (m *RemoteManager) Snapshot(machineID string) (string, error) {
	m.mu.Lock()
	rm, ok := m.machines[machineID]
	if !ok {
		m.mu.Unlock()
		return "", fmt.Errorf("machine %q not found", machineID)
	}
	provider := rm.Provider
	instanceID := rm.ProviderInstanceID
	label := rm.Label
	m.mu.Unlock()

	snapName := fmt.Sprintf("yaver-%s-%s", strings.ReplaceAll(label, " ", "-"),
		time.Now().Format("2006-01-02T150405"))

	switch provider {
	case "hetzner":
		token := os.Getenv("HETZNER_API_TOKEN")
		if token == "" {
			return "", fmt.Errorf("HETZNER_API_TOKEN not set")
		}
		if instanceID == "" {
			return "", fmt.Errorf("no provider instance ID for machine %q", machineID)
		}
		body := map[string]interface{}{
			"description": snapName,
			"type":        "snapshot",
		}
		resp, err := hetznerAPI(token, "POST", "/v1/servers/"+instanceID+"/actions/create_image", body)
		if err != nil {
			return "", fmt.Errorf("Hetzner create image: %w", err)
		}
		var result struct {
			Image struct {
				ID          int    `json:"id"`
				Description string `json:"description"`
			} `json:"image"`
		}
		_ = json.Unmarshal(resp, &result)
		return fmt.Sprintf("Hetzner snapshot created: %s (image ID %d)\n", snapName, result.Image.ID), nil

	case "digitalocean":
		token := os.Getenv("DIGITALOCEAN_TOKEN")
		if token == "" {
			return "", fmt.Errorf("DIGITALOCEAN_TOKEN not set")
		}
		if instanceID == "" {
			return "", fmt.Errorf("no provider instance ID for machine %q", machineID)
		}
		body := map[string]interface{}{
			"type": "snapshot",
			"name": snapName,
		}
		_, err := digitaloceanAPI(token, "POST", "/v2/droplets/"+instanceID+"/actions", body)
		if err != nil {
			return "", fmt.Errorf("DigitalOcean snapshot: %w", err)
		}
		return fmt.Sprintf("DigitalOcean snapshot requested: %s\n(Snapshots are async — check https://cloud.digitalocean.com in a few minutes)\n", snapName), nil

	default:
		// Fallback: tar home directory to a local archive via SSH.
		rm2, _ := m.machines[machineID]
		if rm2 == nil {
			return "", fmt.Errorf("machine %q not found", machineID)
		}
		archiveName := snapName + ".tar.gz"
		script := fmt.Sprintf("tar -czf /tmp/%s ~ 2>/dev/null && echo 'Snapshot: /tmp/%s'", archiveName, archiveName)
		out, err := sshRun(rm2.Host, rm2.User, script)
		if err != nil {
			return "", fmt.Errorf("snapshot via tar failed: %w", err)
		}
		return out, nil
	}
}

// Exec runs a command on a remote machine via SSH and returns combined output.
func (m *RemoteManager) Exec(machineID, command string) (string, error) {
	if command == "" {
		return "", fmt.Errorf("command is required")
	}
	m.mu.Lock()
	rm, ok := m.machines[machineID]
	if !ok {
		m.mu.Unlock()
		return "", fmt.Errorf("machine %q not found", machineID)
	}
	host, user := rm.Host, rm.User
	m.mu.Unlock()

	out, err := sshRun(host, user, command)
	if err != nil {
		return out, fmt.Errorf("exec on %s@%s: %w", user, host, err)
	}
	return out, nil
}

// Deploy rsyncs a local project to a remote machine, installs dependencies,
// builds the project, and starts or restarts it.
func (m *RemoteManager) Deploy(machineID, projectPath string) (string, error) {
	if projectPath == "" {
		return "", fmt.Errorf("projectPath is required")
	}

	abs := projectPath
	if !filepath.IsAbs(abs) {
		cwd, _ := os.Getwd()
		abs = filepath.Join(cwd, projectPath)
	}

	info, err := os.Stat(abs)
	if err != nil {
		return "", fmt.Errorf("project path %q: %w", abs, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("project path %q is not a directory", abs)
	}

	m.mu.Lock()
	rm, ok := m.machines[machineID]
	if !ok {
		m.mu.Unlock()
		return "", fmt.Errorf("machine %q not found", machineID)
	}
	host, user := rm.Host, rm.User
	m.mu.Unlock()

	projectName := filepath.Base(abs)
	remoteDir := fmt.Sprintf("~/projects/%s", projectName)

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Deploying %s → %s@%s:%s\n\n", abs, user, host, remoteDir))

	// --- rsync ---
	rsyncArgs := []string{
		"-az", "--delete",
		"--exclude=node_modules",
		"--exclude=.git",
		"--exclude=dist",
		"--exclude=build",
		"--exclude=.next",
		"--exclude=__pycache__",
		"--exclude=*.pyc",
		abs + "/",
		fmt.Sprintf("%s@%s:%s/", user, host, remoteDir),
	}
	rsyncCmd := exec.Command("rsync", rsyncArgs...)
	rsyncOut, err := rsyncCmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("rsync failed: %w\n%s", err, rsyncOut)
	}
	sb.WriteString("Files synced.\n")

	// --- Detect project type and build ---
	buildScript := fmt.Sprintf(`
set -e
cd %s

# Detect and install dependencies
if [ -f package.json ]; then
  echo "[deploy] Installing npm dependencies..."
  export NVM_DIR="$HOME/.nvm"
  [ -s "$NVM_DIR/nvm.sh" ] && . "$NVM_DIR/nvm.sh"
  npm install --production --silent

  # Build
  if grep -q '"build"' package.json 2>/dev/null; then
    echo "[deploy] Building..."
    npm run build
  fi

  # Start/restart with PM2 if available, otherwise background
  if command -v pm2 &>/dev/null; then
    pm2 restart %s 2>/dev/null || pm2 start npm --name %s -- start
    pm2 save
    echo "[deploy] Started with PM2."
  else
    pkill -f "node.*%s" 2>/dev/null || true
    nohup npm start > /tmp/%s.log 2>&1 &
    echo "[deploy] Started (PID $!)."
  fi

elif [ -f go.mod ]; then
  echo "[deploy] Building Go project..."
  go build -o /tmp/%s-bin .
  pkill -f "/tmp/%s-bin" 2>/dev/null || true
  nohup /tmp/%s-bin > /tmp/%s.log 2>&1 &
  echo "[deploy] Started Go binary (PID $!)."

elif [ -f requirements.txt ]; then
  echo "[deploy] Installing Python dependencies..."
  pip3 install -r requirements.txt -q
  pkill -f "python.*%s" 2>/dev/null || true
  nohup python3 main.py > /tmp/%s.log 2>&1 &
  echo "[deploy] Started Python app (PID $!)."

else
  echo "[deploy] Unknown project type — files synced but not started."
fi
`, remoteDir,
		projectName, projectName, projectName, projectName,
		projectName, projectName, projectName, projectName,
		projectName, projectName)

	deployOut, err := sshRun(host, user, buildScript)
	if err != nil {
		return "", fmt.Errorf("deploy script failed: %w\nOutput:\n%s", err, deployOut)
	}
	sb.WriteString(deployOut)

	// Record project in machine config.
	m.mu.Lock()
	if rm2 := m.machines[machineID]; rm2 != nil {
		found := false
		for _, p := range rm2.Projects {
			if p == remoteDir {
				found = true
				break
			}
		}
		if !found {
			rm2.Projects = append(rm2.Projects, remoteDir)
		}
	}
	m.mu.Unlock()
	_ = m.saveConfig()

	return sb.String(), nil
}

// ---------------------------------------------------------------------------
// Internal helpers
// ---------------------------------------------------------------------------

// sshRun executes a shell command on a remote host via SSH and returns
// the combined stdout+stderr output.
func sshRun(host, user, command string) (string, error) {
	return sshRunMulti(host, user, []string{command})
}

// sshRunMulti executes a sequence of commands on a remote host via SSH,
// joining them with newlines into a single SSH session.
func sshRunMulti(host, user string, commands []string) (string, error) {
	script := strings.Join(commands, "\n")
	sshTarget := fmt.Sprintf("%s@%s", user, host)

	// Build SSH args: disable host key checking for automation,
	// prefer keys, never prompt for a password.
	args := []string{
		"-o", "StrictHostKeyChecking=no",
		"-o", "BatchMode=yes",
		"-o", "ConnectTimeout=10",
		"-o", "ServerAliveInterval=30",
		sshTarget,
		"/bin/bash", "-s",
	}

	// Try ed25519 first, then rsa.
	home, _ := os.UserHomeDir()
	for _, keyName := range []string{"id_ed25519", "id_rsa", "id_ecdsa"} {
		keyPath := filepath.Join(home, ".ssh", keyName)
		if _, err := os.Stat(keyPath); err == nil {
			args = append([]string{"-i", keyPath}, args...)
			break
		}
	}

	cmd := exec.Command("ssh", args...)
	cmd.Stdin = strings.NewReader(script)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// loadConfig reads machines from the JSON config file.
// Missing file → no-op (returns nil).
func (m *RemoteManager) loadConfig() error {
	data, err := os.ReadFile(m.configPath)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read %s: %w", m.configPath, err)
	}

	var machines []*RemoteMachine
	if err := json.Unmarshal(data, &machines); err != nil {
		return fmt.Errorf("parse %s: %w", m.configPath, err)
	}
	for _, rm := range machines {
		m.machines[rm.ID] = rm
	}
	return nil
}

// saveConfig persists the current machine list to the JSON config file,
// creating the directory if it does not exist.
func (m *RemoteManager) saveConfig() error {
	m.mu.Lock()
	machines := make([]*RemoteMachine, 0, len(m.machines))
	for _, rm := range m.machines {
		machines = append(machines, rm)
	}
	m.mu.Unlock()

	sort.Slice(machines, func(i, j int) bool {
		return machines[i].ID < machines[j].ID
	})

	data, err := json.MarshalIndent(machines, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal machines: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(m.configPath), 0700); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}
	if err := os.WriteFile(m.configPath, data, 0600); err != nil {
		return fmt.Errorf("write %s: %w", m.configPath, err)
	}
	return nil
}

// detectProvider guesses the VPS provider from IP address or hostname.
// Returns "unknown" when detection fails.
func detectProvider(host string) string {
	if strings.Contains(host, "hetzner") || strings.Contains(host, "hc-")  {
		return "hetzner"
	}
	if strings.Contains(host, "digitalocean") || strings.Contains(host, "droplet") {
		return "digitalocean"
	}
	// IP-based heuristics for common providers.
	// (Ranges are approximate; full ASN lookup would be more accurate.)
	// Hetzner: 135.181.0.0/16, 65.108.0.0/16, 116.202.0.0/16, etc.
	// DigitalOcean: 104.131.0.0/18, 165.227.0.0/16, etc.
	if strings.HasPrefix(host, "135.181.") ||
		strings.HasPrefix(host, "65.108.") ||
		strings.HasPrefix(host, "116.202.") ||
		strings.HasPrefix(host, "5.161.") {
		return "hetzner"
	}
	if strings.HasPrefix(host, "104.131.") ||
		strings.HasPrefix(host, "165.227.") ||
		strings.HasPrefix(host, "159.89.") ||
		strings.HasPrefix(host, "68.183.") {
		return "digitalocean"
	}
	return "unknown"
}

// hetznerAPI makes an authenticated request to the Hetzner Cloud API.
func hetznerAPI(token, method, path string, body interface{}) ([]byte, error) {
	const base = "https://api.hetzner.cloud"
	return cloudAPIRequest(base+path, method, "Bearer "+token, body)
}

// digitaloceanAPI makes an authenticated request to the DigitalOcean v2 API.
func digitaloceanAPI(token, method, path string, body interface{}) ([]byte, error) {
	const base = "https://api.digitalocean.com"
	return cloudAPIRequest(base+path, method, "Bearer "+token, body)
}

// cloudAPIRequest is the shared HTTP helper for provider API calls.
func cloudAPIRequest(url, method, authorization string, body interface{}) ([]byte, error) {
	var bodyReader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal request body: %w", err)
		}
		bodyReader = bytes.NewReader(b)
	}

	req, err := http.NewRequest(method, url, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Authorization", authorization)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("HTTP %s %s: %w", method, url, err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response body: %w", err)
	}

	if resp.StatusCode >= 400 {
		return data, fmt.Errorf("API error %d: %s", resp.StatusCode, string(data))
	}
	return data, nil
}

// hetznerSSHKeyIDs returns the IDs of all SSH keys registered in the Hetzner account.
func (m *RemoteManager) hetznerSSHKeyIDs(token string) ([]int, error) {
	data, err := hetznerAPI(token, "GET", "/v1/ssh_keys", nil)
	if err != nil {
		return nil, err
	}
	var result struct {
		SSHKeys []struct {
			ID int `json:"id"`
		} `json:"ssh_keys"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, err
	}
	ids := make([]int, 0, len(result.SSHKeys))
	for _, k := range result.SSHKeys {
		ids = append(ids, k.ID)
	}
	return ids, nil
}

// doDropletIP polls DigitalOcean for the public IPv4 of a droplet.
// Used when the creation response doesn't immediately include the IP.
func (m *RemoteManager) doDropletIP(token string, dropletID int) (string, error) {
	data, err := digitaloceanAPI(token, "GET", fmt.Sprintf("/v2/droplets/%d", dropletID), nil)
	if err != nil {
		return "", err
	}
	var result struct {
		Droplet struct {
			Networks struct {
				V4 []struct {
					IPAddress string `json:"ip_address"`
					Type      string `json:"type"`
				} `json:"v4"`
			} `json:"networks"`
		} `json:"droplet"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return "", err
	}
	for _, n := range result.Droplet.Networks.V4 {
		if n.Type == "public" {
			return n.IPAddress, nil
		}
	}
	return "", fmt.Errorf("no public IPv4 found for droplet %d", dropletID)
}

// waitForSSH polls an SSH port until it accepts connections or the deadline passes.
func waitForSSH(host, user string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		out, err := sshRun(host, user, "echo ok")
		if err == nil && strings.Contains(out, "ok") {
			return nil
		}
		time.Sleep(5 * time.Second)
	}
	return fmt.Errorf("SSH on %s@%s not reachable after %s", user, host, timeout)
}

// userForHost looks up the SSH user for a known host, or returns an error.
func (m *RemoteManager) userForHost(host string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, rm := range m.machines {
		if rm.Host == host {
			return rm.User, nil
		}
	}
	return "", fmt.Errorf("host %q not found in managed machines — run `yaver remote setup %s` first", host, host)
}

// defaultPublicKey reads the default SSH public key from ~/.ssh/id_ed25519.pub
// or ~/.ssh/id_rsa.pub, whichever exists first.
func defaultPublicKey() (string, error) {
	home, _ := os.UserHomeDir()
	for _, name := range []string{"id_ed25519.pub", "id_rsa.pub", "id_ecdsa.pub"} {
		path := filepath.Join(home, ".ssh", name)
		data, err := os.ReadFile(path)
		if err == nil && len(data) > 0 {
			return string(data), nil
		}
	}
	return "", fmt.Errorf("no public key found in ~/.ssh (id_ed25519.pub, id_rsa.pub, id_ecdsa.pub)")
}

// machineID creates a stable, unique ID from host and user.
func machineID(host, user string) string {
	return strings.ReplaceAll(user+"@"+host, ".", "-")
}

// truncateLabel shortens s to at most n runes, adding "…" if truncated.
func truncateLabel(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n-1]) + "…"
}

// ---------------------------------------------------------------------------
// Manual-provision instruction helpers
// ---------------------------------------------------------------------------

func hetznerManualInstructions(size, region string) string {
	return fmt.Sprintf(`HETZNER_API_TOKEN not set.

To provision a Hetzner server manually:
  1. Log in to https://console.hetzner.cloud
  2. Create a new server:
     - Type: %s (%s)
     - Location: %s
     - Image: Ubuntu 22.04
     - Add your SSH key
  3. Once the server is running, note its IP address.
  4. Run: yaver remote setup <ip> root

Or set HETZNER_API_TOKEN to enable automated provisioning.
`, hetznerSizeName(size), hetznerTypeForSize(size), hetznerRegionName(region))
}

func doManualInstructions(size, region string) string {
	return fmt.Sprintf(`DIGITALOCEAN_TOKEN not set.

To provision a DigitalOcean Droplet manually:
  1. Log in to https://cloud.digitalocean.com
  2. Create a new Droplet:
     - Size: %s (%s)
     - Region: %s
     - Image: Ubuntu 22.04 LTS x64
     - Add your SSH key
  3. Once the Droplet is running, note its IP address.
  4. Run: yaver remote setup <ip> root

Or set DIGITALOCEAN_TOKEN to enable automated provisioning.
`, doSizeName(size), doSlugForSize(size), doRegionName(region))
}

func hetznerSizeName(size string) string {
	return map[string]string{"small": "CX22 (2 vCPU, 4 GB)", "medium": "CX32 (4 vCPU, 8 GB)", "large": "CX42 (8 vCPU, 16 GB)"}[strings.ToLower(size)]
}
func hetznerTypeForSize(size string) string {
	return map[string]string{"small": "cx22", "medium": "cx32", "large": "cx42"}[strings.ToLower(size)]
}
func hetznerRegionName(region string) string {
	return map[string]string{"eu": "Nuremberg (nbg1)", "us": "Ashburn (ash)", "asia": "Singapore (sin)"}[strings.ToLower(region)]
}
func hetznerCostEstimate(serverType string) float64 {
	return map[string]float64{"cx22": 4.85, "cx32": 9.31, "cx42": 18.56}[strings.ToLower(serverType)]
}

func doSizeName(size string) string {
	return map[string]string{"small": "s-1vcpu-2gb (1 vCPU, 2 GB)", "medium": "s-2vcpu-4gb (2 vCPU, 4 GB)", "large": "s-4vcpu-8gb (4 vCPU, 8 GB)"}[strings.ToLower(size)]
}
func doSlugForSize(size string) string {
	return map[string]string{"small": "s-1vcpu-2gb", "medium": "s-2vcpu-4gb", "large": "s-4vcpu-8gb"}[strings.ToLower(size)]
}
func doRegionName(region string) string {
	return map[string]string{"eu": "Frankfurt (fra1)", "us": "New York 3 (nyc3)", "asia": "Singapore (sgp1)"}[strings.ToLower(region)]
}
func doCostEstimate(slug string) float64 {
	return map[string]float64{"s-1vcpu-2gb": 12.0, "s-2vcpu-4gb": 24.0, "s-4vcpu-8gb": 48.0}[strings.ToLower(slug)]
}
