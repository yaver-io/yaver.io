package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// OrchestrateResult is the full report for a multi-region bootstrap run.
type OrchestrateResult struct {
	Servers     []OrchestratedServer `json:"servers"`
	CaddyConfig string               `json:"caddyConfig,omitempty"`
	Error       string               `json:"error,omitempty"`
}

type OrchestratedServer struct {
	IP        string            `json:"ip"`
	Region    string            `json:"region"`
	Role      string            `json:"role"` // app, router
	Status    string            `json:"status"`
	Steps     []string          `json:"steps"`
	Details   map[string]string `json:"details,omitempty"`
	Error     string            `json:"error,omitempty"`
}

// OrchestrateMultiRegion picks up where DeployMultiRegion leaves off: for each
// provisioned VPS, it runs the bootstrap sequence (SSH → install yaver agent
// → clone project repo → yaver serve → start services). Then it designates
// one as the router and writes a Caddy load-balancer config onto it.
//
// Requires the Hetzner provisioner to have captured root_password in Details
// (which it does in cloud_provisioners.go).
func OrchestrateMultiRegion(projectDir, domain string, servers []ProvisionResult, gitRepo string) *OrchestrateResult {
	res := &OrchestrateResult{}
	var upstreams []string

	for i, srv := range servers {
		ip := srv.Details["ipv4"]
		if ip == "" {
			res.Servers = append(res.Servers, OrchestratedServer{
				Region: firstRegion(srv), Status: "skipped", Error: "no ipv4 in provisioner output",
			})
			continue
		}
		role := "app"
		if i == 0 {
			role = "router+app"
		}
		os := OrchestratedServer{
			IP: ip, Region: firstRegion(srv), Role: role, Status: "running",
			Details: srv.Details,
		}
		steps, err := bootstrapRemoteHost(ip, srv.Details["root_password"], projectDir, gitRepo)
		os.Steps = steps
		if err != nil {
			os.Error = err.Error()
			os.Status = "failed"
		} else {
			os.Status = "ready"
			upstreams = append(upstreams, ip+":80")
		}
		res.Servers = append(res.Servers, os)
	}

	// Generate and deploy the Caddy router on the first healthy server.
	if domain != "" && len(upstreams) > 0 {
		var sb strings.Builder
		sb.WriteString(domain + " {\n  reverse_proxy ")
		sb.WriteString(strings.Join(upstreams, " "))
		sb.WriteString(" {\n    lb_policy round_robin\n    health_uri /\n    health_interval 10s\n    fail_duration 30s\n  }\n  encode gzip zstd\n}\n")
		res.CaddyConfig = sb.String()

		// Write it onto the first server (the router).
		for _, srv := range res.Servers {
			if srv.Status == "ready" {
				if err := deployCaddyConfig(srv.IP, srv.Details["root_password"], res.CaddyConfig); err != nil {
					res.Error = "caddy deploy failed on " + srv.IP + ": " + err.Error()
				}
				break
			}
		}
	}
	return res
}

func firstRegion(p ProvisionResult) string {
	if r, ok := p.Details["region"]; ok {
		return r
	}
	return "unknown"
}

// bootstrapRemoteHost SSHes into a fresh VPS as root, installs essentials,
// clones the project, and starts `yaver serve`. Uses `sshpass` + root password
// (Hetzner supplies one on provision). When bootstrap succeeds the remote host
// is part of the user's Yaver mesh and can be managed from any other agent.
func bootstrapRemoteHost(ip, rootPassword, projectDir, gitRepo string) ([]string, error) {
	var steps []string
	if rootPassword == "" {
		return steps, fmt.Errorf("no root password (provisioner didn't capture it)")
	}

	// Wait for SSH to come up.
	if err := waitSSH(ip, 90*time.Second); err != nil {
		return steps, err
	}
	steps = append(steps, "ssh reachable")

	// Install Docker + dependencies (apt-get).
	bootstrap := `
		export DEBIAN_FRONTEND=noninteractive
		apt-get update -qq
		apt-get install -y -qq ca-certificates curl git
		curl -fsSL https://get.docker.com | sh
		systemctl enable docker && systemctl start docker
		curl -fsSL https://yaver.io/install.sh | bash
		echo 'BOOTSTRAP_OK'
	`
	if out, err := runSSH(ip, rootPassword, bootstrap); err != nil {
		return append(steps, "bootstrap failed: "+out), fmt.Errorf("bootstrap: %w", err)
	}
	steps = append(steps, "docker + yaver-agent installed")

	// Clone the project repo to ~/project so `yaver serve` has something to work on.
	if gitRepo != "" {
		cmd := fmt.Sprintf("cd /root && rm -rf project && git clone %s project", bashSingleQuote(gitRepo))
		if out, err := runSSH(ip, rootPassword, cmd); err != nil {
			return append(steps, "git clone failed: "+out), fmt.Errorf("clone: %w", err)
		}
		steps = append(steps, "cloned "+gitRepo)
	} else if projectDir != "" {
		// No git repo — rsync the local project tree over.
		rsync := exec.Command("sshpass", "-p", rootPassword,
			"rsync", "-az", "-e", "ssh -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null",
			"--exclude=node_modules", "--exclude=.next", "--exclude=.yaver/snapshots",
			projectDir+"/", fmt.Sprintf("root@%s:/root/project/", ip))
		if out, err := rsync.CombinedOutput(); err != nil {
			return append(steps, "rsync failed: "+string(out)), fmt.Errorf("rsync: %w", err)
		}
		steps = append(steps, "rsynced project tree")
	}

	// Start services via the yaver agent that was just installed.
	startCmd := `cd /root/project && yaver services start || true`
	if out, err := runSSH(ip, rootPassword, startCmd); err != nil {
		steps = append(steps, "services start returned: "+out)
	} else {
		steps = append(steps, "services started")
	}
	return steps, nil
}

// deployCaddyConfig drops a Caddyfile onto the remote host and runs caddy reload.
func deployCaddyConfig(ip, rootPassword, caddyConfig string) error {
	writeCmd := fmt.Sprintf(`cat > /etc/caddy/Caddyfile <<'CADDY_EOF'
%s
CADDY_EOF
mkdir -p /etc/caddy
systemctl enable caddy && systemctl restart caddy || caddy reload --config /etc/caddy/Caddyfile`, caddyConfig)
	if out, err := runSSH(ip, rootPassword, writeCmd); err != nil {
		return fmt.Errorf("%w: %s", err, out)
	}
	return nil
}

func waitSSH(ip string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		conn, err := net_Dial("tcp", ip+":22", 3*time.Second)
		if err == nil {
			_ = conn.Close()
			// Give sshd a moment to fully initialize auth.
			time.Sleep(3 * time.Second)
			return nil
		}
		time.Sleep(3 * time.Second)
	}
	return fmt.Errorf("ssh never came up on %s within %s", ip, timeout)
}

// runSSH runs a shell script on the remote host via sshpass + ssh and returns
// combined output. Strict-host-key-check is disabled (fresh VPS has no known
// key yet).
func runSSH(ip, rootPassword, script string) (string, error) {
	if _, err := exec.LookPath("sshpass"); err != nil {
		return "", fmt.Errorf("sshpass not installed (brew install hudochenkov/sshpass/sshpass on macOS, apt install sshpass on linux)")
	}
	cmd := exec.Command("sshpass", "-p", rootPassword,
		"ssh", "-o", "StrictHostKeyChecking=no", "-o", "UserKnownHostsFile=/dev/null", "-o", "ConnectTimeout=10",
		"root@"+ip, "bash", "-s")
	cmd.Stdin = bytes.NewReader([]byte(script))
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// bashSingleQuote quotes a string for safe interpolation into a bash
// command. Renamed from shellEscape to avoid colliding with the
// runner-sandbox helper of the same name (which has slightly different
// escape rules — `'\''` vs the `'"'"'` form here, and an empty-string
// special-case). Both are valid POSIX-shell escapes; we keep both to
// preserve callers' existing tested output.
func bashSingleQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'"'"'`) + "'"
}

// Upgrade DeployMultiRegion to ALSO orchestrate and return the richer result.
func DeployMultiRegionWithOrchestration(projectDir, name string, regions []string, domain, gitRepo string) (map[string]interface{}, error) {
	base, err := DeployMultiRegion(name, regions, domain)
	if err != nil {
		return nil, err
	}
	orch := OrchestrateMultiRegion(projectDir, domain, base.Servers, gitRepo)
	return map[string]interface{}{
		"provision":    base,
		"orchestrate":  orch,
	}, nil
}

// ---- HTTP ----

func (s *HTTPServer) handleMultiRegionOrchestrate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	var b struct {
		Name    string   `json:"name"`
		Regions []string `json:"regions"`
		Domain  string   `json:"domain"`
		GitRepo string   `json:"gitRepo"`
	}
	_ = json.NewDecoder(r.Body).Decode(&b)
	res, err := DeployMultiRegionWithOrchestration(s.dirParam(r), b.Name, b.Regions, b.Domain, b.GitRepo)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{"error": err.Error()})
		return
	}
	AuditLog("", "multiregion_deploy", filepath.Base(s.dirParam(r)), b.Name, "success", "", "")
	writeJSON(w, http.StatusOK, res)
}
