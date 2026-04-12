package main

import (
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"time"
)

func osStatFile(p string) (os.FileInfo, error) { return os.Stat(p) }

// autoInstallCaddy tries homebrew on macOS or apt/snap on Linux. On Windows
// we can't install silently so we return an error with install guidance.
func autoInstallCaddy() error {
	switch runtime.GOOS {
	case "darwin":
		if _, err := exec.LookPath("brew"); err != nil {
			return fmt.Errorf("Homebrew not found; install from https://brew.sh then rerun")
		}
		out, err := exec.Command("brew", "install", "caddy").CombinedOutput()
		if err != nil {
			return fmt.Errorf("brew install caddy: %w (%s)", err, string(out))
		}
		return nil
	case "linux":
		// Prefer apt; fall back to snap.
		if _, err := exec.LookPath("apt"); err == nil {
			// Install the official caddy package. Requires root.
			cmds := [][]string{
				{"sudo", "apt", "update"},
				{"sudo", "apt", "install", "-y", "debian-keyring", "debian-archive-keyring", "apt-transport-https", "curl"},
				{"sh", "-c", "curl -1sLf 'https://dl.cloudsmith.io/public/caddy/stable/gpg.key' | sudo gpg --dearmor -o /usr/share/keyrings/caddy-stable-archive-keyring.gpg"},
				{"sh", "-c", "curl -1sLf 'https://dl.cloudsmith.io/public/caddy/stable/debian.deb.txt' | sudo tee /etc/apt/sources.list.d/caddy-stable.list"},
				{"sudo", "apt", "update"},
				{"sudo", "apt", "install", "-y", "caddy"},
			}
			for _, c := range cmds {
				if out, err := exec.Command(c[0], c[1:]...).CombinedOutput(); err != nil {
					return fmt.Errorf("%s: %w (%s)", c[0], err, string(out))
				}
			}
			return nil
		}
		if _, err := exec.LookPath("snap"); err == nil {
			out, err := exec.Command("sudo", "snap", "install", "caddy").CombinedOutput()
			if err != nil {
				return fmt.Errorf("snap install caddy: %w (%s)", err, string(out))
			}
			return nil
		}
		return fmt.Errorf("no known package manager; install manually from https://caddyserver.com/docs/install")
	default:
		return fmt.Errorf("auto-install not supported on %s; install from https://caddyserver.com/docs/install", runtime.GOOS)
	}
}

// autoInferHealthcheck probes common health endpoints and returns the first
// that returns <500 within 2s. Used by the deploy pipeline when the user didn't
// set one explicitly.
func autoInferHealthcheck(baseURL string) string {
	if baseURL == "" {
		return ""
	}
	client := &http.Client{Timeout: 2 * time.Second}
	for _, path := range []string{"/api/health", "/healthz", "/health", "/api/ping", "/"} {
		url := baseURL + path
		res, err := client.Get(url)
		if err != nil {
			continue
		}
		res.Body.Close()
		if res.StatusCode < 500 {
			return url
		}
	}
	return ""
}

// detectMigrator sniffs the project for Drizzle or Prisma and returns the
// migration command to run before swapping services in a deploy.
func detectMigrator(projectDir string) (string, string) {
	if fileExists(projectDir + "/drizzle.config.ts") || fileExists(projectDir + "/drizzle.config.js") {
		return "drizzle", "npx drizzle-kit migrate"
	}
	if fileExists(projectDir + "/prisma/schema.prisma") {
		return "prisma", "npx prisma migrate deploy"
	}
	return "", ""
}


// autoInferDeployHealthcheck walks services.yaml ports and picks the first HTTP
// service to probe. Called by RunDeploy when cfg.Healthcheck is empty.
func autoInferDeployHealthcheck(projectDir string) string {
	sm := NewServicesManager(projectDir)
	cfg, err := sm.LoadConfig()
	if err != nil {
		return ""
	}
	for _, svc := range cfg.Services {
		if svc.Port == 0 {
			continue
		}
		// Skip infra ports we know aren't HTTP (5432/6379/5672/9000/etc. may or may not be).
		switch svc.Port {
		case 5432, 6379, 3306, 5672, 11211, 27017:
			continue
		}
		if hc := autoInferHealthcheck(fmt.Sprintf("http://127.0.0.1:%d", svc.Port)); hc != "" {
			return hc
		}
	}
	return ""
}
