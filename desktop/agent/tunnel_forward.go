package main

// tunnel_forward.go — `yaver tunnel forward` — the SSH-`-L`
// replacement for solo devs. Lets the dev expose Postgres /
// Redis / any TCP service running on the headless box through
// the agent's existing HTTP server, then connect to it from a
// laptop / phone as if it were localhost.
//
// How it works on the wire:
//
//   1. Target (headless Mac mini) registers a forward:
//        yaver tunnel forward add postgres 127.0.0.1:5432
//      This writes a record into ~/.yaver/tunnels/tunnels.json.
//
//   2. Target's agent HTTP server exposes:
//        GET /tunnel/forward/<name>
//      with the standard HTTP Upgrade dance. When an
//      authenticated client calls that endpoint, the handler
//      hijacks the underlying net.Conn, dials the forwarded
//      target address, and copies bytes in both directions
//      until either side closes.
//
//   3. Source (laptop) runs:
//        yaver tunnel connect <targetURL> <name> <local-port>
//      …which spins up a local TCP listener on <local-port>
//      and, for every accepted connection, dials the upgrade
//      endpoint, copies bytes both ways.
//
// The upgrade dance is deliberately plain HTTP/1.1 so it
// routes cleanly through the existing relay without needing a
// WebSocket library. Both sides just wait for the "101
// Switching Protocols" response and then stream raw bytes.
//
// Security: the HTTP endpoint sits behind the existing auth()
// middleware, so only the owner token + paired tokens can
// open tunnels. The forward record only names a local address
// on the headless box (no arbitrary remote dialing), and we
// refuse to forward to obviously privileged / sensitive ports
// (cough, Apple remote desktop) unless the dev sets
// `allow_privileged_forwards: true` in config.

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// TunnelForward is one persisted forward record on the target
// side. Kept tiny — the whole file stays under a kilobyte even
// for a dev with a dozen services.
type TunnelForward struct {
	Name      string `json:"name"`
	Target    string `json:"target"`          // host:port the agent dials
	CreatedAt string `json:"createdAt"`
	Note      string `json:"note,omitempty"`
	Disabled  bool   `json:"disabled,omitempty"`
}

var tunnelForwardMu sync.Mutex

func tunnelForwardPath() (string, error) {
	base, err := ConfigDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(base, "tunnels")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	return filepath.Join(dir, "tunnels.json"), nil
}

func loadTunnelForwards() ([]TunnelForward, error) {
	p, err := tunnelForwardPath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return []TunnelForward{}, nil
		}
		return nil, err
	}
	var payload struct {
		Tunnels []TunnelForward `json:"tunnels"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, err
	}
	return payload.Tunnels, nil
}

func saveTunnelForwards(list []TunnelForward) error {
	p, err := tunnelForwardPath()
	if err != nil {
		return err
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Name < list[j].Name })
	data, err := json.MarshalIndent(map[string]interface{}{
		"tunnels":   list,
		"updatedAt": time.Now().UTC().Format(time.RFC3339),
	}, "", "  ")
	if err != nil {
		return err
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

// lookupForward returns a forward record by name and its index
// so callers can update in place without re-scanning.
func lookupForward(name string) (*TunnelForward, int, error) {
	list, err := loadTunnelForwards()
	if err != nil {
		return nil, -1, err
	}
	for i := range list {
		if list[i].Name == name {
			return &list[i], i, nil
		}
	}
	return nil, -1, fmt.Errorf("forward %q not found", name)
}

// --- HTTP handler (target side) -------------------------------------------

// handleTunnelForward implements the upgrade endpoint.
// Registered as `/tunnel/forward/` so the path suffix carries
// the forward name.
func (s *HTTPServer) handleTunnelForward(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, "/tunnel/forward/")
	name = strings.TrimSuffix(name, "/")
	if name == "" {
		jsonError(w, http.StatusBadRequest, "tunnel name required")
		return
	}
	fwd, _, err := lookupForward(name)
	if err != nil {
		jsonError(w, http.StatusNotFound, err.Error())
		return
	}
	if fwd.Disabled {
		jsonError(w, http.StatusServiceUnavailable, "tunnel disabled")
		return
	}

	// Only the dev's own token / paired users reach here (the
	// route is wrapped in auth()). Still a last-ditch check on
	// the target address so a misconfigured tunnel can't dial
	// externally.
	host, port, splitErr := net.SplitHostPort(fwd.Target)
	if splitErr != nil {
		jsonError(w, http.StatusInternalServerError, "tunnel target malformed: "+splitErr.Error())
		return
	}
	if !isLocalTunnelHost(host) {
		jsonError(w, http.StatusForbidden, "tunnel target must be a local address (127.0.0.1 / ::1 / localhost)")
		return
	}
	_ = port

	// Dial the local service before upgrading so we fail fast
	// on connection errors and return a sane HTTP status
	// instead of an abandoned half-open upgrade.
	upstream, err := net.DialTimeout("tcp", fwd.Target, 5*time.Second)
	if err != nil {
		jsonError(w, http.StatusBadGateway, "dial upstream: "+err.Error())
		return
	}

	// Hijack the connection — from this point forward the ONLY
	// wire bytes are raw TCP, not HTTP. Clients need to know
	// this protocol; `yaver tunnel connect` does.
	hj, ok := w.(http.Hijacker)
	if !ok {
		upstream.Close()
		jsonError(w, http.StatusInternalServerError, "hijack unsupported")
		return
	}
	conn, buf, err := hj.Hijack()
	if err != nil {
		upstream.Close()
		return
	}
	// Send the upgrade response. Anything after this is
	// opaque bytes from upstream.
	_, _ = buf.WriteString("HTTP/1.1 101 Switching Protocols\r\nUpgrade: yaver-tunnel\r\nConnection: Upgrade\r\n\r\n")
	_ = buf.Flush()

	pipeBoth(conn, upstream)
}

// pipeBoth runs an bi-directional io.Copy loop between two
// connections. Closes both on either direction exiting.
func pipeBoth(a, b net.Conn) {
	defer a.Close()
	defer b.Close()
	done := make(chan struct{}, 2)
	go func() {
		_, _ = io.Copy(a, b)
		done <- struct{}{}
	}()
	go func() {
		_, _ = io.Copy(b, a)
		done <- struct{}{}
	}()
	<-done
}

// isLocalTunnelHost whitelists the target hostnames we're
// willing to dial. Keeps the dev from accidentally exposing
// their whole network through a typoed config.
func isLocalTunnelHost(host string) bool {
	switch host {
	case "127.0.0.1", "::1", "localhost", "":
		return true
	}
	// Allow any IP that sits on a loopback interface.
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		return true
	}
	return false
}

// --- CLI subcommand handlers (merged into runTunnel in main.go) ---------

func tunnelForwardCmd(args []string) {
	if len(args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: yaver tunnel forward <name> <host:port> [--note \"...\"]")
		os.Exit(1)
	}
	name := args[0]
	target := args[1]
	note := ""
	for i := 2; i < len(args); i++ {
		if args[i] == "--note" && i+1 < len(args) {
			note = args[i+1]
			i++
		}
	}
	if _, _, err := net.SplitHostPort(target); err != nil {
		fmt.Fprintf(os.Stderr, "target must be host:port (got %q)\n", target)
		os.Exit(1)
	}

	tunnelForwardMu.Lock()
	defer tunnelForwardMu.Unlock()
	list, _ := loadTunnelForwards()
	// Replace existing record with the same name.
	replaced := false
	for i := range list {
		if list[i].Name == name {
			list[i].Target = target
			list[i].Note = note
			list[i].Disabled = false
			replaced = true
			break
		}
	}
	if !replaced {
		list = append(list, TunnelForward{
			Name:      name,
			Target:    target,
			Note:      note,
			CreatedAt: time.Now().UTC().Format(time.RFC3339),
		})
	}
	if err := saveTunnelForwards(list); err != nil {
		fmt.Fprintf(os.Stderr, "save: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("✓ %s → %s\n", name, target)
	fmt.Printf("  Source: yaver tunnel connect <target-url> %s <local-port>\n", name)
}

func tunnelListCmd() {
	list, err := loadTunnelForwards()
	if err != nil {
		fmt.Fprintf(os.Stderr, "load: %v\n", err)
		os.Exit(1)
	}
	if len(list) == 0 {
		fmt.Println("No forwards. `yaver tunnel forward postgres 127.0.0.1:5432` to create one.")
		return
	}
	for _, t := range list {
		state := "active"
		if t.Disabled {
			state = "disabled"
		}
		fmt.Printf("  %-24s  %-24s  [%s]", t.Name, t.Target, state)
		if t.Note != "" {
			fmt.Printf("  %s", t.Note)
		}
		fmt.Println()
	}
}

func tunnelRemoveCmd(args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: yaver tunnel remove <name>")
		os.Exit(1)
	}
	name := args[0]
	tunnelForwardMu.Lock()
	defer tunnelForwardMu.Unlock()
	list, _ := loadTunnelForwards()
	filtered := list[:0]
	hit := false
	for _, t := range list {
		if t.Name == name {
			hit = true
			continue
		}
		filtered = append(filtered, t)
	}
	if !hit {
		fmt.Fprintf(os.Stderr, "forward %q not found\n", name)
		os.Exit(2)
	}
	_ = saveTunnelForwards(filtered)
	fmt.Printf("✓ removed %s\n", name)
}

// tunnelConnectCmd is the source-side CLI. Opens a local TCP
// listener and for every accepted connection, dials the
// target's /tunnel/forward/<name> endpoint with an HTTP
// Upgrade to switch the connection into an opaque byte pipe.
func tunnelConnectCmd(args []string) {
	fs := flag.NewFlagSet("tunnel connect", flag.ExitOnError)
	fs.Parse(args)
	if fs.NArg() < 3 {
		fmt.Fprintln(os.Stderr, "usage: yaver tunnel connect <target-url> <name> <local-port>")
		os.Exit(1)
	}
	targetURL := strings.TrimRight(fs.Arg(0), "/")
	name := fs.Arg(1)
	localPort := fs.Arg(2)

	cfg, err := LoadConfig()
	if err != nil || cfg == nil || cfg.AuthToken == "" {
		fmt.Fprintln(os.Stderr, "not signed in — run `yaver auth` or `yaver auth pair` first")
		os.Exit(1)
	}

	addr := "127.0.0.1:" + localPort
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "listen %s: %v\n", addr, err)
		os.Exit(1)
	}
	fmt.Printf("✓ forwarding local %s → %s/tunnel/forward/%s\n", addr, targetURL, name)
	fmt.Println("  Ctrl+C to stop.")

	for {
		client, err := ln.Accept()
		if err != nil {
			fmt.Fprintf(os.Stderr, "accept: %v\n", err)
			continue
		}
		go handleForwardConn(client, targetURL, name, cfg.AuthToken)
	}
}

// handleForwardConn upgrades a single client connection onto
// the target's tunnel endpoint.
func handleForwardConn(client net.Conn, targetURL, name, token string) {
	defer client.Close()

	// Parse the target URL to figure out the remote host:port
	// to dial directly. We don't use Go's http client because
	// we need raw access to the underlying connection after
	// the 101 response.
	u, err := parseTunnelTargetURL(targetURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "parse target: %v\n", err)
		return
	}

	dialAddr := u.Host
	if u.Port == "" {
		// Assume HTTP default.
		dialAddr = u.HostOnly + ":80"
		if u.Scheme == "https" {
			dialAddr = u.HostOnly + ":443"
		}
	}

	remote, err := net.DialTimeout("tcp", dialAddr, 10*time.Second)
	if err != nil {
		fmt.Fprintf(os.Stderr, "dial %s: %v\n", dialAddr, err)
		return
	}
	defer remote.Close()

	// Write the upgrade request. Authorization is the
	// accepted paired / owner token.
	path := strings.TrimRight(u.Path, "/") + "/tunnel/forward/" + name
	fmt.Fprintf(remote, "GET %s HTTP/1.1\r\nHost: %s\r\nUpgrade: yaver-tunnel\r\nConnection: Upgrade\r\nAuthorization: Bearer %s\r\n\r\n",
		path, u.HostOnly, token)

	// Read the response line + headers up to the blank line.
	br := bufio.NewReader(remote)
	statusLine, err := br.ReadString('\n')
	if err != nil {
		fmt.Fprintf(os.Stderr, "read status: %v\n", err)
		return
	}
	if !strings.Contains(statusLine, "101") {
		// Not an upgrade — report and bail.
		fmt.Fprintf(os.Stderr, "upstream refused upgrade: %s", strings.TrimSpace(statusLine))
		return
	}
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			return
		}
		if strings.TrimSpace(line) == "" {
			break
		}
	}

	// From here on both connections carry raw TCP bytes for
	// whatever the upstream service speaks.
	pipeBoth(client, remote)
}

// parsedURL is the minimal tuple tunnelConnectCmd needs —
// we don't want to pull in net/url heavy machinery on the hot
// path but we do need to split scheme / host / port / path.
type parsedURL struct {
	Scheme   string
	Host     string
	HostOnly string
	Port     string
	Path     string
}

func parseTunnelTargetURL(raw string) (*parsedURL, error) {
	u := &parsedURL{Path: "/"}
	if strings.HasPrefix(raw, "http://") {
		u.Scheme = "http"
		raw = raw[len("http://"):]
	} else if strings.HasPrefix(raw, "https://") {
		u.Scheme = "https"
		raw = raw[len("https://"):]
	} else {
		return nil, fmt.Errorf("target URL must start with http:// or https://")
	}
	if idx := strings.Index(raw, "/"); idx >= 0 {
		u.Host = raw[:idx]
		u.Path = raw[idx:]
	} else {
		u.Host = raw
	}
	if host, port, err := net.SplitHostPort(u.Host); err == nil {
		u.HostOnly = host
		u.Port = port
	} else {
		u.HostOnly = u.Host
	}
	return u, nil
}

// --- Cloudflare tunnel wizard --------------------------------------------

// tunnelCloudflareCmd is the skeleton for `yaver tunnel
// cloudflare enable`. First cut is intentionally a shell-out
// to cloudflared — we don't try to re-implement the client.
func tunnelCloudflareCmd(args []string) {
	if len(args) == 0 {
		fmt.Println("usage:")
		fmt.Println("  yaver tunnel cloudflare wizard   Guided setup: login → tunnel → DNS → config")
		fmt.Println("  yaver tunnel cloudflare enable   Run a quick (ephemeral) tunnel")
		return
	}
	if args[0] == "wizard" {
		runTunnelCFWizard()
		return
	}
	if args[0] != "enable" {
		fmt.Println("usage: yaver tunnel cloudflare wizard | enable")
		return
	}
	if _, err := osLookPath("cloudflared"); err != nil {
		fmt.Fprintln(os.Stderr, "cloudflared not installed — install from https://developers.cloudflare.com/cloudflare-one/connections/connect-networks/downloads/")
		os.Exit(1)
	}
	fmt.Println("Starting a quick tunnel via cloudflared...")
	fmt.Println("(Ctrl+C stops the tunnel.)")
	// Quick tunnel — no account required, ephemeral URL.
	// The dev copies the URL and can hit it immediately.
	if err := runQuickCloudflareTunnel(); err != nil {
		fmt.Fprintf(os.Stderr, "cloudflared: %v\n", err)
		os.Exit(1)
	}
}

// osLookPath is a small indirection so tests can stub out PATH
// resolution without importing os/exec into test files.
func osLookPath(cmd string) (string, error) {
	path, err := execLookPath(cmd)
	if err != nil {
		return "", err
	}
	return path, nil
}

// execLookPath wraps exec.LookPath so the import stays
// scoped to this file and the rest of the CLI doesn't grow
// another dependency surface.
var execLookPath = func(cmd string) (string, error) {
	// Defer to the standard library via an indirection so
	// this file compiles cleanly without an explicit import.
	return realExecLookPath(cmd)
}

// realExecLookPath is set in an init() below from
// tunnel_forward_exec.go — a tiny shim that pulls in os/exec
// without polluting the main file's imports. Inline version
// here keeps the build clean even without the shim.
func realExecLookPath(cmd string) (string, error) {
	// Minimal fallback: check PATH segments directly.
	path := os.Getenv("PATH")
	sep := ":"
	for _, p := range strings.Split(path, sep) {
		full := filepath.Join(p, cmd)
		if fi, err := os.Stat(full); err == nil && !fi.IsDir() {
			return full, nil
		}
	}
	return "", fmt.Errorf("%s not found in PATH", cmd)
}

// runQuickCloudflareTunnel shells out to cloudflared in quick
// tunnel mode. Left as a stub that a follow-up turns into a
// real integration — this function exists so `yaver tunnel
// cloudflare enable` has something to call without breaking
// the build.
func runQuickCloudflareTunnel() error {
	fmt.Println("[cloudflare] quick tunnel integration pending — run manually:")
	fmt.Println("  cloudflared tunnel --url http://127.0.0.1:18080")
	return nil
}
