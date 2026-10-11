package main

// cf_autotunnel.go — zero-setup off-LAN reachability now that the shared free
// relay is gone.
//
// Policy (user directive 2026-10-11): Tailscale if the user already has it;
// otherwise a Cloudflare tunnel is the default, free-but-limited path Yaver
// provides. The agent does it all by itself — no wizard, no Cloudflare account:
//
//  1. Skip when Tailscale is Running (it is the better path), when the user
//     configured their own tunnel (cfg.CloudflareTunnels), or when opted out
//     (--no-tunnel / YAVER_NO_AUTO_TUNNEL=1).
//  2. Find cloudflared; if missing, download the official release for THIS
//     os/arch into ~/.yaver/bin/cloudflared (never a hardcoded path).
//  3. Run `cloudflared tunnel --url http://127.0.0.1:<port>` (a quick tunnel),
//     parse the https://<random>.trycloudflare.com URL, and expose it through
//     configuredPublicEndpoints so the next heartbeat publishes it to Convex.
//     Phones and the web dashboard already probe publicEndpoints.
//  4. Supervise with backoff; the URL changes on every restart, so the state
//     is cleared while down and the heartbeat stops advertising a dead URL.
//
// Honest limits (Cloudflare's, not ours): quick tunnels have no SLA, the URL is
// ephemeral, and Cloudflare documents that they do not support Server-Sent
// Events. Security is unchanged: the URL is not a credential, every agent route
// still demands the bearer token. Every wait narrates itself via
// autoTunnelStatus() so a surface can say what is happening.

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	osexec "os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"
)

var quickTunnelURLRe = regexp.MustCompile(`https://[a-z0-9][a-z0-9-]*\.trycloudflare\.com`)

// parseQuickTunnelURL extracts the quick-tunnel URL from a cloudflared log line.
func parseQuickTunnelURL(line string) string {
	// cloudflared prints its own control host (api.trycloudflare.com) while
	// starting; that is not a tunnel and must never be advertised.
	for _, m := range quickTunnelURLRe.FindAllString(line, -1) {
		if m != "https://api.trycloudflare.com" {
			return m
		}
	}
	return ""
}

// AutoTunnelStatus is the named, structured state (stable codes, not prose).
type AutoTunnelStatus struct {
	State  string    `json:"state"` // disabled|installing|starting|up|backoff
	Reason string    `json:"reason,omitempty"`
	URL    string    `json:"url,omitempty"`
	Since  time.Time `json:"since"`
	Detail string    `json:"detail,omitempty"`
}

var autoTunnel struct {
	mu sync.RWMutex
	s  AutoTunnelStatus
}

func setAutoTunnel(state, reason, url, detail string) {
	autoTunnel.mu.Lock()
	defer autoTunnel.mu.Unlock()
	autoTunnel.s = AutoTunnelStatus{State: state, Reason: reason, URL: url, Since: time.Now(), Detail: detail}
}

func autoTunnelStatus() AutoTunnelStatus {
	autoTunnel.mu.RLock()
	defer autoTunnel.mu.RUnlock()
	return autoTunnel.s
}

// autoTunnelURL is the live URL, or "" while the tunnel is not up.
func autoTunnelURL() string {
	s := autoTunnelStatus()
	if s.State != "up" {
		return ""
	}
	return s.URL
}

// autoTunnelDecision returns ("", true) to run, or a stable reason code to skip.
func autoTunnelDecision(cfg *Config, noTunnelFlag bool, tailscaleRunning bool) (string, bool) {
	switch {
	case noTunnelFlag || os.Getenv("YAVER_NO_AUTO_TUNNEL") == "1":
		return "opted_out", false
	case tailscaleRunning:
		return "tailscale_active", false
	case cfg != nil && len(cfg.CloudflareTunnels) > 0:
		return "user_tunnel_configured", false
	}
	return "", true
}

// cloudflaredAsset resolves the official release asset for an os/arch.
func cloudflaredAsset(goos, goarch string) (name string, tgz bool, err error) {
	switch goarch {
	case "amd64", "arm64":
	default:
		return "", false, fmt.Errorf("cloudflared publishes no build for %s/%s", goos, goarch)
	}
	switch goos {
	case "linux":
		return "cloudflared-linux-" + goarch, false, nil
	case "darwin":
		return "cloudflared-darwin-" + goarch + ".tgz", true, nil
	}
	return "", false, fmt.Errorf("automatic cloudflared install is not supported on %s; install it manually", goos)
}

func ensureCloudflared(ctx context.Context, progress func(string)) (string, error) {
	if p, err := osexec.LookPath("cloudflared"); err == nil {
		return p, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dst := filepath.Join(home, ".yaver", "bin", "cloudflared")
	if fi, err := os.Stat(dst); err == nil && !fi.IsDir() {
		return dst, nil
	}
	asset, tgz, err := cloudflaredAsset(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return "", err
	}
	url := "https://github.com/cloudflare/cloudflared/releases/latest/download/" + asset
	progress("downloading " + url)
	dctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	req, _ := http.NewRequestWithContext(dctx, http.MethodGet, url, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("download cloudflared: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("download cloudflared: HTTP %d", resp.StatusCode)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return "", err
	}
	tmp := dst + ".part"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return "", err
	}
	var n int64
	if tgz {
		n, err = extractCloudflaredTgz(resp.Body, f)
	} else {
		n, err = io.Copy(f, resp.Body)
	}
	f.Close()
	if err != nil || n == 0 {
		os.Remove(tmp)
		return "", fmt.Errorf("write cloudflared: %v (%d bytes)", err, n)
	}
	if err := os.Rename(tmp, dst); err != nil {
		return "", err
	}
	progress(fmt.Sprintf("installed %s (%d MB)", dst, n>>20))
	return dst, nil
}

func extractCloudflaredTgz(r io.Reader, w io.Writer) (int64, error) {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return 0, err
	}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err != nil {
			return 0, fmt.Errorf("cloudflared not found in archive: %w", err)
		}
		if filepath.Base(h.Name) == "cloudflared" && h.Typeflag == tar.TypeReg {
			return io.Copy(w, tr)
		}
	}
}

// tailscaleRunning is a var so the live test can exercise the tunnel on a
// machine that has Tailscale.
var tailscaleRunning = func() bool {
	ts := DetectTailscale()
	return ts != nil && ts.Running
}

// runAutoTunnel supervises the quick tunnel until ctx ends. Always logs one
// line first so a silent supervisor is distinguishable from a dead one.
func runAutoTunnel(ctx context.Context, cfg *Config, port int, noTunnelFlag bool) {
	if reason, ok := autoTunnelDecision(cfg, noTunnelFlag, tailscaleRunning()); !ok {
		setAutoTunnel("disabled", reason, "", "")
		log.Printf("[tunnel] auto Cloudflare tunnel not started: %s", reason)
		return
	}
	log.Printf("[tunnel] no Tailscale — starting free Cloudflare tunnel to 127.0.0.1:%d", port)
	backoff := 5 * time.Second
	for ctx.Err() == nil {
		setAutoTunnel("installing", "", "", "locating cloudflared")
		bin, err := ensureCloudflared(ctx, func(m string) {
			log.Printf("[tunnel] %s", m)
			setAutoTunnel("installing", "", "", m)
		})
		if err != nil {
			log.Printf("[tunnel] cloudflared unavailable: %v (retry in %s)", err, backoff)
			setAutoTunnel("backoff", "cloudflared_unavailable", "", err.Error())
		} else {
			started := time.Now()
			err = runQuickTunnelOnce(ctx, bin, port)
			if time.Since(started) > 2*time.Minute {
				backoff = 5 * time.Second
			}
			log.Printf("[tunnel] exited: %v (retry in %s)", err, backoff)
			setAutoTunnel("backoff", "tunnel_exited", "", fmt.Sprint(err))
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		if backoff < 5*time.Minute {
			backoff *= 2
		}
	}
}

func runQuickTunnelOnce(ctx context.Context, bin string, port int) error {
	setAutoTunnel("starting", "", "", "waiting for Cloudflare to assign a URL")
	cmd := osexec.CommandContext(ctx, bin, "tunnel", "--no-autoupdate",
		"--url", fmt.Sprintf("http://127.0.0.1:%d", port))
	cmd.WaitDelay = 3 * time.Second
	pr, pw := io.Pipe()
	cmd.Stdout, cmd.Stderr = pw, pw
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { cmd.Wait(); pw.Close() }()
	sc := bufio.NewScanner(pr)
	deadline := time.AfterFunc(60*time.Second, func() {
		if autoTunnelStatus().State != "up" {
			log.Printf("[tunnel] no URL after 60s — restarting cloudflared")
			cmd.Process.Kill()
		}
	})
	defer deadline.Stop()
	for sc.Scan() {
		if u := parseQuickTunnelURL(sc.Text()); u != "" && autoTunnelStatus().State != "up" {
			log.Printf("[tunnel] up: %s", u)
			setAutoTunnel("up", "", u, "")
		}
	}
	return fmt.Errorf("cloudflared stopped")
}

// publishable endpoint for the heartbeat; kept tiny so auth.go stays readable.
func autoTunnelEndpoint() string {
	return strings.TrimRight(autoTunnelURL(), "/")
}
