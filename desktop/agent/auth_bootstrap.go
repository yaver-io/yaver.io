package main

// auth_bootstrap.go — zero-auth bootstrap mode for `yaver serve`.
//
// The user story this enables:
//
//   I only have my phone. The remote dev box is racked in the
//   basement / running on a VPS / sitting on a Mac mini at the
//   top of the house. I installed the yaver binary there but I
//   can't SSH in to run `yaver auth`. I need the box to come up
//   far enough that my phone can reach it and push my account's
//   token over — all from the couch.
//
// Flow:
//
//   1. The yaver binary starts with no token in config.json.
//      Normally `yaver serve` would bail with "Not signed in".
//   2. Instead, it calls runBootstrapServe() — a stripped-down
//      HTTP surface that only mounts /health + /auth/pair/*.
//   3. It auto-opens a pairing session (same mechanism as
//      `yaver auth pair`) with a freshly generated 6-char
//      passkey.
//   4. It broadcasts a LAN beacon that carries two extra hints:
//      `na:true` (needs auth) and `pk:<passkey>`. The mobile
//      app's beacon listener recognises this and offers a
//      one-tap "adopt this machine" button.
//   5. The phone POSTs its Convex token to /auth/pair/submit.
//      We save it to config.json and re-exec ourselves, which
//      now takes the normal serve path with a real token.
//
// Security model for bootstrap pairing:
//
//   - The passkey is still the secret. Putting it in the beacon
//     is a conscious choice: on the LAN any attacker can read
//     the UDP broadcast anyway, and this is a one-shot, 10-min
//     window on a machine that by definition holds no secrets
//     yet. The trade-off is "anyone on the local network can
//     claim a fresh yaver install" — that's the same trust
//     model as Chromecast, Apple TV pairing, smart-TV Wi-Fi
//     setup, and it matches the home-LAN case the user cares
//     about.
//   - Once a submit lands, the pairing window closes and the
//     process re-execs into a normal (authenticated) serve.
//     Subsequent bootstrap beacons stop, so there is no
//     ambient "claim me" surface after the first success.
//   - If you really don't want beacon-broadcast passkeys, set
//     YAVER_BOOTSTRAP_NO_BEACON_PK=1 — the beacon still says
//     `na:true` so the mobile app can discover the box, but
//     the passkey stays in the terminal and must be typed by
//     hand (the normal `yaver auth pair` flow).
//
// This file is intentionally standalone — it does not import
// the full HTTPServer struct (tasks, vault, exec, tmux, …)
// because none of those mean anything before auth. The only
// dependencies are StartPairingSession / EndPairingSession /
// handlePairInfo / handlePairSubmit, all of which already live
// in auth_pair.go and work without a configured HTTPServer.

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	osexec "os/exec"
	"strings"
	"time"

	"github.com/google/uuid"
)

// bootstrapPairingTTL is how long a bootstrap pairing window
// stays open before the passkey expires and we regenerate.
const bootstrapPairingTTL = 10 * time.Minute

// BootstrapInfo is the JSON shape returned by /info when the
// agent is in bootstrap mode. Exported (lowercase fields, but
// the type itself) so other code in this package can decode it.
type bootstrapInfoResponse struct {
	OK        bool   `json:"ok"`
	Mode      string `json:"mode"`
	NeedsAuth bool   `json:"needsAuth"`
	Hostname  string `json:"hostname"`
	Version   string `json:"version"`
}

// probeBootstrapInfo hits http://127.0.0.1:<port>/info with a
// 1-second timeout. Returns the parsed response or nil on any
// failure (port closed, timeout, non-bootstrap response). Used
// by `yaver status` to detect a running bootstrap-mode agent
// without needing an auth token.
func probeBootstrapInfo(port int) *bootstrapInfoResponse {
	client := &http.Client{Timeout: 1 * time.Second}
	resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/info", port))
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil
	}
	var info bootstrapInfoResponse
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return nil
	}
	return &info
}

// forkBootstrapToBackground mirrors the authed serve fork-to-bg
// dance for the no-token case. Re-execs `yaver serve --debug
// --port=N --work-dir=W` with stdout/stderr piped to the agent
// log file and detached from the parent. Returns true on success.
func forkBootstrapToBackground(httpPort int, workDir string) bool {
	execPath, err := os.Executable()
	if err != nil {
		log.Printf("bootstrap: cannot find yaver binary: %v", err)
		return false
	}
	logFile := logFilePath()
	lf, err := os.OpenFile(logFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		log.Printf("bootstrap: cannot open log file: %v", err)
		return false
	}
	cmd := osexec.Command(execPath, "serve", "--debug",
		fmt.Sprintf("--port=%d", httpPort),
		fmt.Sprintf("--work-dir=%s", workDir))
	cmd.Stdout = lf
	cmd.Stderr = lf
	cmd.Dir = workDir
	detachProcess(cmd)
	if err := cmd.Start(); err != nil {
		log.Printf("bootstrap: failed to fork: %v", err)
		lf.Close()
		return false
	}
	if err := os.WriteFile(pidFilePath(), []byte(fmt.Sprintf("%d", cmd.Process.Pid)), 0644); err != nil {
		log.Printf("bootstrap: warning: could not write PID file: %v", err)
	}
	lf.Close()
	fmt.Printf("Yaver agent started in bootstrap mode (PID %d, port %d).\n", cmd.Process.Pid, httpPort)
	fmt.Println()
	fmt.Println("This machine has no auth token yet. The agent is up and waiting.")
	fmt.Println("Open the Yaver mobile app (already signed in) on the same Wi-Fi —")
	fmt.Println("the box will appear as 'needs auth', tap it to pair.")
	fmt.Println()
	fmt.Println("  yaver status    Check pairing state")
	fmt.Println("  yaver logs      Watch the bootstrap server")
	return true
}

// runBootstrapServe is entered when `yaver serve` is invoked on
// a machine that has no auth token. It blocks until a token
// lands via /auth/pair/submit, saves it, then re-execs the
// caller as a regular `yaver serve`.
func runBootstrapServe(httpPort int) {
	fmt.Println()
	fmt.Println("Yaver — bootstrap mode (no auth token yet)")
	fmt.Println("------------------------------------------")
	fmt.Println("Running a minimal pairing server on :", httpPort)
	fmt.Println("The mobile Yaver app (on the same network) will detect this")
	fmt.Println("machine automatically and offer a one-tap pairing option.")
	fmt.Println()

	hostname, _ := os.Hostname()
	session, err := StartPairingSession(bootstrapPairingTTL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "bootstrap: could not start pairing session: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("  Host:        %s\n", hostname)
	fmt.Println("  Passkey:")
	fmt.Println()
	fmt.Print(bigPasskey(session.Code))
	fmt.Println()
	fmt.Printf("  Passkey expires in: %s\n", time.Until(session.ExpiresAt).Round(time.Second))
	fmt.Println()
	fmt.Println("Ways to pair this machine:")
	fmt.Println()
	fmt.Println("  1. Open the Yaver mobile app on the same Wi-Fi network.")
	fmt.Println("     This machine will appear as 'needs auth' — tap to adopt.")
	fmt.Println()
	fmt.Println("  2. From any already-signed-in machine:")
	fmt.Printf("       yaver auth send %s http://<this-host-or-ip>:%d\n", session.Code, httpPort)
	fmt.Println()
	fmt.Println("Waiting for a token…")
	fmt.Println()

	// Minimal HTTP surface. Deliberately not using the full
	// HTTPServer — most handlers would NPE without config.
	bs := &bootstrapHTTPServer{}
	mux := http.NewServeMux()
	mux.HandleFunc("/health", bs.handleHealth)
	mux.HandleFunc("/auth/pair/info", bs.handlePairInfo)
	mux.HandleFunc("/auth/pair/submit", bs.handlePairSubmit)
	// /info is a cheap liveness check the mobile app pings to
	// verify the URL before sending the token.
	mux.HandleFunc("/info", bs.handleInfo)
	// /auth/recover is the unauth recovery endpoint normally
	// mounted on the full HTTPServer. Mount it here too so a
	// box that has lost its token but is still reachable via
	// relay/Tailscale can be remotely re-authed by an already-
	// signed-in mobile client. The handler is the same one used
	// in normal serve mode (auth_recover.go).
	mux.HandleFunc("/auth/recover", bs.handleAuthRecover)

	srv := &http.Server{
		Addr:              fmt.Sprintf("0.0.0.0:%d", httpPort),
		Handler:           corsWrap(mux),
		ReadHeaderTimeout: 10 * time.Second,
	}

	// Beacon: broadcast "this box needs auth". The mobile app's
	// beacon listener will surface it in the Pair Device modal.
	beaconCtx, beaconCancel := context.WithCancel(context.Background())
	defer beaconCancel()
	go startBootstrapBeacon(beaconCtx, httpPort, hostname, session.Code)

	// Start HTTP in a goroutine so we can block on session.done.
	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("bootstrap http: %v", err)
		}
	}()

	// Block until either a token lands or the pairing window
	// expires. We give the user a chance to start a fresh
	// session (new passkey) if they miss the window.
	for {
		select {
		case <-session.done:
			// Pair submit handler fires session.done on success.
			if session.ReceivedToken == "" {
				// Window was closed without a token (unusual —
				// e.g. another session replaced this one). Start
				// a fresh one and keep going.
				log.Printf("bootstrap: pairing window closed without a token, restarting")
				var err error
				session, err = StartPairingSession(bootstrapPairingTTL)
				if err != nil {
					fmt.Fprintf(os.Stderr, "bootstrap: restart pairing: %v\n", err)
					os.Exit(1)
				}
				fmt.Println("  New passkey:")
				fmt.Print(bigPasskey(session.Code))
				continue
			}
			// Token arrived — save it and re-exec.
			fmt.Println()
			fmt.Println("✓ Token received from mobile. Saving and starting full serve…")
			if err := saveBootstrapToken(session); err != nil {
				fmt.Fprintf(os.Stderr, "bootstrap: save token: %v\n", err)
				os.Exit(1)
			}
			// Shut down the bootstrap HTTP server cleanly so the
			// real serve can bind :18080.
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			_ = srv.Shutdown(shutdownCtx)
			cancel()
			beaconCancel()
			EndPairingSession()
			// Re-exec ourselves as `yaver serve`. The real serve
			// path picks up the freshly saved token and the
			// normal runServe flow takes over.
			reexecAsServe()
			return
		case <-time.After(bootstrapPairingTTL + 5*time.Second):
			// Expired and nobody came. Rotate to a new passkey
			// and keep waiting — a remote box with nobody home
			// should stay reachable across multiple windows.
			log.Printf("bootstrap: passkey window expired, rotating")
			var err error
			session, err = StartPairingSession(bootstrapPairingTTL)
			if err != nil {
				fmt.Fprintf(os.Stderr, "bootstrap: rotate pairing: %v\n", err)
				os.Exit(1)
			}
			fmt.Println("  New passkey:")
			fmt.Print(bigPasskey(session.Code))
		}
	}
}

// saveBootstrapToken writes the received token, convex URL and
// a fresh device ID into ~/.yaver/config.json.
func saveBootstrapToken(session *pairingSession) error {
	cfg, err := LoadConfig()
	if err != nil || cfg == nil {
		cfg = &Config{}
	}
	cfg.AuthToken = session.ReceivedToken
	if session.ReceivedURL != "" {
		cfg.ConvexSiteURL = session.ReceivedURL
	}
	if cfg.ConvexSiteURL == "" {
		cfg.ConvexSiteURL = defaultConvexSiteURL
	}
	if cfg.DeviceID == "" {
		cfg.DeviceID = uuid.New().String()
	}
	return SaveConfig(cfg)
}

// reexecAsServe replaces the current bootstrap process with a
// fresh `yaver serve`. This takes the normal auth path now that
// the config file has a token.
func reexecAsServe() {
	execPath, err := os.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "bootstrap: cannot find yaver binary: %v\n", err)
		os.Exit(1)
	}
	cmd := osexec.Command(execPath, "serve")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "bootstrap: failed to relaunch serve: %v\n", err)
		os.Exit(1)
	}
	os.Exit(0)
}

// --- Minimal HTTP handler set ---------------------------------------------

type bootstrapHTTPServer struct{}

func (bs *bootstrapHTTPServer) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"ok":true,"mode":"bootstrap"}`))
}

// handleInfo lets the mobile app verify "yes, this is a yaver
// box waiting for auth" before submitting a token. Shape keeps
// the fields the mobile app already reads so it can safely
// display something in the Pair Device modal.
func (bs *bootstrapHTTPServer) handleInfo(w http.ResponseWriter, r *http.Request) {
	hostname, _ := os.Hostname()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"ok":        true,
		"mode":      "bootstrap",
		"needsAuth": true,
		"hostname":  hostname,
		"version":   version,
	})
}

func (bs *bootstrapHTTPServer) handlePairInfo(w http.ResponseWriter, r *http.Request) {
	// Reuse the existing handler by wrapping it — it only
	// touches package-level pairing state, not HTTPServer fields.
	(&HTTPServer{}).handlePairInfo(w, r)
}

func (bs *bootstrapHTTPServer) handlePairSubmit(w http.ResponseWriter, r *http.Request) {
	(&HTTPServer{}).handlePairSubmit(w, r)
}

// handleAuthRecover delegates to the same handler used in
// normal serve mode. It's safe because handleAuthRecover only
// touches package-level state (recoveryLimiter, the bootstrap
// secret hash in config.json, the pair session map) — none of
// the HTTPServer fields it would otherwise read are needed.
func (bs *bootstrapHTTPServer) handleAuthRecover(w http.ResponseWriter, r *http.Request) {
	(&HTTPServer{}).handleAuthRecover(w, r)
}

// corsWrap lets the mobile app and browsers on the LAN hit the
// bootstrap endpoints without running into preflight failures.
func corsWrap(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		h.ServeHTTP(w, r)
	})
}

// --- Bootstrap beacon ------------------------------------------------------

// startBootstrapBeacon is a cousin of startBeacon() used before
// auth exists. It broadcasts the same protocol but with:
//
//   - `th` = "" (we don't have a userId yet — the mobile app
//              uses `na:true` to decide whether to show it)
//   - `na` = true (needs auth)
//   - `pk` = passkey (unless YAVER_BOOTSTRAP_NO_BEACON_PK=1)
//
// Once the token lands, the caller cancels the context and this
// goroutine returns.
func startBootstrapBeacon(ctx context.Context, httpPort int, hostname, passkey string) {
	if hostname == "" {
		hostname = "yaver"
	}
	// Bootstrap device IDs are ephemeral random 8-char tags so
	// multiple unpaired boxes on the same LAN don't collide.
	shortID := fmt.Sprintf("boot%04x", os.Getpid()&0xFFFF)
	broadcastPasskey := passkey
	if os.Getenv("YAVER_BOOTSTRAP_NO_BEACON_PK") == "1" {
		broadcastPasskey = ""
	}
	payload := beaconPayload{
		Version:          beaconVersion,
		DeviceID:         shortID,
		Port:             httpPort,
		Name:             hostname,
		TokenFingerprint: "",
		HardwareID:       HardwareID(),
		NeedsAuth:        true,
		BootstrapPasskey: broadcastPasskey,
	}
	data, err := json.Marshal(payload)
	if err != nil {
		log.Printf("[bootstrap-beacon] marshal: %v", err)
		return
	}
	addr := &net.UDPAddr{IP: net.IPv4bcast, Port: beaconPort}
	log.Printf("[bootstrap-beacon] broadcasting on UDP %d (needs-auth host %s)", beaconPort, hostname)

	ticker := time.NewTicker(beaconInterval)
	defer ticker.Stop()

	dial := func() *net.UDPConn {
		c, err := net.DialUDP("udp4", nil, addr)
		if err != nil {
			return nil
		}
		return c
	}
	conn := dial()
	defer func() {
		if conn != nil {
			_ = conn.Close()
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if conn == nil {
				conn = dial()
				if conn == nil {
					continue
				}
			}
			if _, err := conn.Write(data); err != nil {
				// Silent rebind on failure — networks come and go.
				_ = conn.Close()
				conn = nil
			}
		}
	}
}

// needsBootstrap returns true when runServe should drop into
// bootstrap mode instead of exiting with "Not signed in". A
// real user can still force the old behaviour with
// YAVER_NO_BOOTSTRAP=1 to keep scripts that expect the fail-fast
// exit code working.
func needsBootstrap(cfg *Config, loadErr error) bool {
	if os.Getenv("YAVER_NO_BOOTSTRAP") == "1" {
		return false
	}
	if loadErr != nil {
		return true
	}
	if cfg == nil {
		return true
	}
	if strings.TrimSpace(cfg.AuthToken) == "" {
		return true
	}
	if strings.TrimSpace(cfg.ConvexSiteURL) == "" {
		return true
	}
	return false
}
