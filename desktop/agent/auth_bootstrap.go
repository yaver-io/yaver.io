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
	"io"
	"log"
	"net"
	"net/http"
	"os"
	osexec "os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/google/uuid"
)

// runtimeGOOS returns the platform name in the shape the Convex
// devices schema accepts. Convex's `platform` enum is "macos" not
// "darwin"; everything else is verbatim.
func runtimeGOOS() string {
	if runtime.GOOS == "darwin" {
		return "macos"
	}
	return runtime.GOOS
}

var tailscaleCGNAT = mustCIDR("100.64.0.0/10")

func mustCIDR(raw string) *net.IPNet {
	_, cidr, err := net.ParseCIDR(raw)
	if err != nil {
		panic(err)
	}
	return cidr
}

func bootstrapPasskeyVisible(r *http.Request) bool {
	if os.Getenv("YAVER_BOOTSTRAP_NO_BEACON_PK") == "1" {
		return false
	}
	if r.Header.Get("X-Forwarded-For") != "" || r.Header.Get("X-Relay-Password") != "" {
		return false
	}
	ip := net.ParseIP(clientIP(r))
	if ip == nil {
		return false
	}
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() {
		return true
	}
	return tailscaleCGNAT.Contains(ip)
}

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
	if shouldTrackPrimaryAgent(httpPort) {
		if err := os.WriteFile(pidFilePath(), []byte(fmt.Sprintf("%d", cmd.Process.Pid)), 0644); err != nil {
			log.Printf("bootstrap: warning: could not write PID file: %v", err)
		}
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

	// Zero-touch provisioning seed (DPP-style). If this box was flashed
	// with a provision seed, pin its deviceId + convex URL into config
	// NOW — before the relay/notify code below reads cfg.DeviceID — so the
	// tunnel registers under the provisioned id rather than a fresh random
	// one. The background attest loop is started further down.
	provisionSeed, provisionErr := LoadProvisionSeed()
	if provisionErr != nil {
		log.Printf("[provision] ignoring unreadable provision seed: %v", provisionErr)
	}
	if provisionSeed != nil {
		applyProvisionSeedToConfig(provisionSeed)
		fmt.Println("Zero-touch provisioning seed found — this machine will")
		fmt.Println("self-credential once its QR is claimed in the Yaver app.")
		fmt.Println()
	}

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
	// Additive QR / pair-URL on-ramp. The LAN beacon + passkey above
	// already cover every existing flow (mobile beacon detection,
	// `yaver auth send`); the URL/QR is purely a faster on-ramp.
	bootstrapTarget := ""
	if ip := getLocalIP(); ip != "" && ip != "0.0.0.0" {
		bootstrapTarget = fmt.Sprintf("http://%s:%d", ip, httpPort)
	}
	printPairURLAndQR(buildPairURL(session, PairURLOptions{
		Mode:   "bootstrap",
		Target: bootstrapTarget,
	}))
	fmt.Println("Waiting for a token…")
	fmt.Println()

	// Minimal HTTP surface. Deliberately not using the full
	// HTTPServer — most handlers would NPE without config.
	bs := &bootstrapHTTPServer{}
	mux := http.NewServeMux()
	mux.HandleFunc("/health", bs.handleHealth)
	mux.HandleFunc("/auth/pair/info", bs.handlePairInfo)
	mux.HandleFunc("/auth/pair/session", bs.handlePairSession)
	mux.HandleFunc("/auth/pair/submit", bs.handlePairSubmit)
	mux.HandleFunc("/auth/pair/encrypted", bs.handlePairEncrypted)
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
	mux.HandleFunc("/auth/recover/session", bs.handleAuthRecoverSession)
	// One-click owner claim for the dashboard / mobile UI. Caller's
	// bearer is verified against Convex /devices/list (must be
	// owner). On success we splice it into the active pair session.
	// See auth_owner_claim.go.
	mux.HandleFunc("/auth/pair/owner-claim", bs.handleOwnerClaim)

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

	// Relay tunnel: in bootstrap mode the agent should still be
	// reachable from off-LAN so the dashboard / mobile app can
	// pair it without anyone SSH'ing the box. The relay only
	// validates the password — it does NOT call Convex on the
	// register path — so a token-less agent can register a tunnel
	// as long as it has the relay password + a device_id.
	//
	// device_id: persisted on first authed `yaver auth`. Factory-
	// reset wipes it. Without one we can't register a tunnel
	// (relay routes by deviceId). Fix: if config has no
	// device_id but DOES have relay creds, mint a fresh UUID and
	// save it before opening the tunnel. Pairing will overwrite
	// it later if Convex assigns a different one — that's fine,
	// the bootstrap window is short.
	relayCtx, relayCancel := context.WithCancel(context.Background())
	defer relayCancel()
	if cfg, loadErr := LoadConfig(); loadErr == nil && cfg != nil {
		// Auto-mint a device_id when missing so a freshly-reset box
		// can still come up reachable on the relay.
		//
		// A MANAGED box must recover its OWN identity rather than invent a new
		// one. The control plane registers it as a deterministic
		// `cloud-<shortId>` (cloudMachines.ts), and /etc/yaver/machine.json
		// carries that shortId — so the id is always derivable. Minting a random
		// UUID here orphans the box: it re-registers as a brand-new device while
		// every pointer at the old one (primary, aliases, ACLs) quietly breaks,
		// and the operator sees "no device responded" for a machine that is
		// plainly running. That is exactly what happened on 2026-07-13.
		if cfg.DeviceID == "" {
			if derived := managedDeviceIDFromMachineIdentity(); derived != "" {
				cfg.DeviceID = derived
				log.Printf("[BOOTSTRAP-RELAY] recovered managed device_id %s from %s", derived, machineIdentityPath)
			} else {
				cfg.DeviceID = uuid.New().String()
			}
			if saveErr := SaveConfig(cfg); saveErr != nil {
				log.Printf("[BOOTSTRAP-RELAY] could not persist device_id: %v (continuing with in-memory id)", saveErr)
			} else {
				log.Printf("[BOOTSTRAP-RELAY] using device_id %s for bootstrap-mode tunnel", cfg.DeviceID[:min(8, len(cfg.DeviceID))])
			}
		}
		agentAddr := fmt.Sprintf("127.0.0.1:%d", httpPort)
		relayCfg := cfg.RelayServers
		globalPw := cfg.RelayPassword
		if len(relayCfg) == 0 && len(cfg.CachedRelayServers) > 0 {
			relayCfg = cfg.CachedRelayServers
			if globalPw == "" {
				globalPw = cfg.CachedRelayPassword
			}
		}
		started := 0
		for _, rs := range relayCfg {
			pw := rs.Password
			if pw == "" {
				pw = globalPw
			}
			if pw == "" {
				continue
			}
			log.Printf("[BOOTSTRAP-RELAY] Starting tunnel to %s (device %s)…", rs.QuicAddr, cfg.DeviceID[:8])
			go runRelayTunnel(relayCtx, rs.QuicAddr, agentAddr, cfg.DeviceID, "bootstrap-pending", pw, nil, nil)
			started++
		}
		if started > 0 {
			log.Printf("[BOOTSTRAP-RELAY] %d relay tunnel(s) started — agent reachable remotely", started)
		}

		// Tell Convex we're in bootstrap mode so the device list shows
		// us as online + needsAuth=true. Mobile/web clients can then
		// auto-pair by pushing an encrypted token. This does NOT require
		// an auth token — authenticated via (deviceId, hardwareId, pubKey)
		// triple which is already in Convex from the initial `yaver auth`.
		go notifyConvexBootstrap(cfg, httpPort)
	}

	// Zero-touch attest loop: drive a provisioned box from "fresh" to
	// "credentialed" the instant its owner claims the QR. Runs alongside
	// the manual pairing server above — whichever credentials the box
	// first wins, and on success the loop completes the active pairing
	// session so the shared save-token + re-exec handoff below fires.
	if provisionSeed != nil {
		provisionCtx, provisionCancel := context.WithCancel(context.Background())
		defer provisionCancel()
		go runProvisionAttestLoop(provisionCtx, provisionSeed)
	}

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
			relayCancel()
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
	if session.ReceivedURL != "" {
		cfg.ConvexSiteURL = session.ReceivedURL
	}
	if cfg.ConvexSiteURL == "" {
		cfg.ConvexSiteURL = defaultConvexSiteURL
	}
	if cfg.DeviceID == "" {
		cfg.DeviceID = uuid.New().String()
	}
	return SetAuthToken(cfg, session.ReceivedToken)
}

// reexecAsServe replaces the current bootstrap process with a
// fresh `yaver serve`. This takes the normal auth path now that
// the config file has a token.
//
// Critical: on Linux/macOS this uses syscall.Exec to REPLACE the
// running process image instead of forking. systemd's Type=simple
// tracks the ExecStart pid; if we forked a child and exited, systemd
// would see the unit's main pid die and Deactivate the unit even
// though the child is alive — which is exactly the bug the previous
// fork+wait+exit implementation caused (every fresh install hit this
// the moment a phone claimed the box). Replacing the process image
// keeps the pid stable so systemd doesn't notice a transition and
// the unit stays Active across the bootstrap → authenticated handoff.
// See bootstrap_reexec_{unix,windows}.go for the platform impl.
func reexecAsServe() {
	execPath, err := os.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "bootstrap: cannot find yaver binary: %v\n", err)
		os.Exit(1)
	}
	if err := reexecReplaceProcess(execPath, []string{execPath, "serve"}); err != nil {
		fmt.Fprintf(os.Stderr, "bootstrap: failed to relaunch serve: %v\n", err)
		os.Exit(1)
	}
}

// --- Minimal HTTP handler set ---------------------------------------------

type bootstrapHTTPServer struct{}

func (bs *bootstrapHTTPServer) handleHealth(w http.ResponseWriter, r *http.Request) {
	cfg, _ := LoadConfig()
	lifecycle := bootstrapLifecycleInfo(cfg)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"ok":             true,
		"mode":           "bootstrap",
		"needsAuth":      true,
		"lifecycleState": lifecycle.State,
		"lifecycle":      lifecycle,
	})
}

// handleInfo lets the mobile app verify "yes, this is a yaver
// box waiting for auth" before submitting a token. Shape keeps
// the fields the mobile app already reads so it can safely
// display something in the Pair Device modal.
func (bs *bootstrapHTTPServer) handleInfo(w http.ResponseWriter, r *http.Request) {
	hostname, _ := os.Hostname()
	cfg, _ := LoadConfig()
	lifecycle := bootstrapLifecycleInfo(cfg)
	// Include the passkey + public key so the mobile app can pair
	// over a direct HTTP connection without needing to receive the
	// UDP beacon. Do NOT reveal the passkey on proxied requests:
	// relays and reverse proxies make the endpoint reachable from
	// wider networks, so returning the active bootstrap secret there
	// turns discovery into takeover.
	resp := map[string]interface{}{
		"ok":             true,
		"mode":           "bootstrap",
		"needsAuth":      true,
		"hostname":       hostname,
		"version":        version,
		"lifecycleState": lifecycle.State,
		"lifecycle":      lifecycle,
	}
	// Current passkey (only on direct requests, and only if not suppressed).
	if sess := activePairingSnapshot(); sess != nil && bootstrapPasskeyVisible(r) {
		resp["bootstrapPasskey"] = sess.Code
	}
	// Device's public key so mobile can do NaCl box encryption
	if dk, err := LoadOrGenerateKeys(); err == nil {
		resp["devicePublicKey"] = dk.PublicKeyBase64()
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func (bs *bootstrapHTTPServer) handlePairInfo(w http.ResponseWriter, r *http.Request) {
	// Reuse the existing handler by wrapping it — it only
	// touches package-level pairing state, not HTTPServer fields.
	(&HTTPServer{}).handlePairInfo(w, r)
}

func (bs *bootstrapHTTPServer) handlePairSession(w http.ResponseWriter, r *http.Request) {
	(&HTTPServer{}).handlePairSession(w, r)
}

func (bs *bootstrapHTTPServer) handlePairSubmit(w http.ResponseWriter, r *http.Request) {
	(&HTTPServer{}).handlePairSubmit(w, r)
}

// handlePairEncrypted accepts a NaCl box-encrypted token from the
// mobile app. The phone looked up this device's public key in Convex
// and encrypted the payload with nacl.box(msg, nonce, devicePubKey,
// phonePriKey). Only this device can decrypt it because only this
// device has the matching private key in ~/.yaver/device.key.
//
// Body: { "encrypted": "<base64(nonce24 + ciphertext)>", "senderPublicKey": "<base64(32)>" }
// Response: { "ok": true, "host": "..." }
func (bs *bootstrapHTTPServer) handlePairEncrypted(w http.ResponseWriter, r *http.Request) {
	(&HTTPServer{}).handlePairEncrypted(w, r)
}

// handleAuthRecover delegates to the same handler used in
// normal serve mode. It's safe because handleAuthRecover only
// touches package-level state (recoveryLimiter, the bootstrap
// secret hash in config.json, the pair session map) — none of
// the HTTPServer fields it would otherwise read are needed.
func (bs *bootstrapHTTPServer) handleAuthRecover(w http.ResponseWriter, r *http.Request) {
	(&HTTPServer{}).handleAuthRecover(w, r)
}

func (bs *bootstrapHTTPServer) handleAuthRecoverSession(w http.ResponseWriter, r *http.Request) {
	(&HTTPServer{}).handleAuthRecoverSession(w, r)
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
//     uses `na:true` to decide whether to show it)
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
	// Include the device's current public key so the phone can
	// verify it against Convex before encrypting. Safe to broadcast
	// — it's a PUBLIC key, knowing it doesn't help an attacker.
	var devicePubKey string
	if dk, err := LoadOrGenerateKeys(); err == nil {
		devicePubKey = dk.PublicKeyBase64()
	}

	// Use the REAL deviceId from config (if available) so the phone
	// can match it against its Convex device list for the public key
	// lookup. Fall back to ephemeral boot-ID if no config exists.
	realDeviceID := shortID
	if cfg, cfgErr := LoadConfig(); cfgErr == nil && cfg != nil && cfg.DeviceID != "" {
		realDeviceID = cfg.DeviceID
		if len(realDeviceID) > 8 {
			realDeviceID = realDeviceID[:8]
		}
	}

	payload := beaconPayload{
		Version:          beaconVersion,
		DeviceID:         realDeviceID,
		Port:             httpPort,
		Name:             hostname,
		TokenFingerprint: "",
		HardwareID:       HardwareID(),
		NeedsAuth:        true,
		BootstrapPasskey: broadcastPasskey,
		DevicePublicKey:  devicePubKey,
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

// notifyConvexAuthExpired is the one-shot variant of notifyConvexBootstrap.
// Called from the heartbeat loop when the agent discovers mid-session that
// its session token has been revoked / expired (heartbeat 401 + refresh
// failure). It re-uses the bootstrap mutation, which authenticates on the
// (deviceId, hardwareId, publicKey) identity triple — exactly the proof we
// still hold even though the session token is dead — and flips
// needsAuth=true on the device row so web/mobile clients can surface a
// re-auth UI without waiting for the next reboot's bootstrap dance.
//
// One-shot: heartbeat already runs on its own ticker, so we don't need a
// background re-register loop here. Repeated 401s will trigger this again
// naturally, and the mutation is idempotent.
func notifyConvexAuthExpired(cfg *Config, httpPort int) {
	if cfg == nil || cfg.ConvexSiteURL == "" || cfg.DeviceID == "" {
		return
	}
	dk, err := LoadOrGenerateKeys()
	if err != nil {
		log.Printf("[auth-expired-convex] cannot load keys: %v", err)
		return
	}
	body := map[string]interface{}{
		"deviceId":   cfg.DeviceID,
		"hardwareId": HardwareID(),
		"publicKey":  dk.PublicKeyBase64(),
		"quicHost":   getLocalIP(),
		"quicPort":   httpPort,
	}
	data, _ := json.Marshal(body)
	url := strings.TrimRight(cfg.ConvexSiteURL, "/") + "/devices/bootstrap"
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Post(url, "application/json", strings.NewReader(string(data)))
	if err != nil {
		log.Printf("[auth-expired-convex] request failed: %v", err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		log.Printf("[auth-expired-convex] Convex returned %d: %s", resp.StatusCode, strings.TrimSpace(string(snippet)))
		return
	}
	log.Printf("[auth-expired-convex] Marked device %s as needs-auth so clients can show re-auth UI", cfg.DeviceID[:8])
}

// notifyConvexBootstrap tells the Convex backend this device is
// running in bootstrap mode (no token). Convex updates the device
// record with needsAuth=true + isOnline=true so the mobile app and
// web dashboard surface the device with a "NEEDS AUTH" badge.
// Auth is by (deviceId, hardwareId, publicKey) triple — Convex
// verifies they match the existing record from the first yaver auth.
// Retries every 30s to keep the "online" status fresh.
//
// Truly-fresh boxes (never paired before, no Convex devices row) get
// a 404 from /devices/bootstrap. We fall back to /devices/bootstrap-
// pending, which uses the agent's relay password as the per-user
// signal so the rightful user's dashboard can list and claim the
// box. Without this fallback a clean install is invisible from
// anywhere except the LAN beacon.
func notifyConvexBootstrap(cfg *Config, httpPort int) {
	if cfg == nil || cfg.ConvexSiteURL == "" || cfg.DeviceID == "" {
		return
	}
	dk, err := LoadOrGenerateKeys()
	if err != nil {
		log.Printf("[bootstrap-convex] cannot load keys: %v", err)
		return
	}
	pubKey := dk.PublicKeyBase64()
	hwid := HardwareID()
	host := getLocalIP()
	hostname, _ := os.Hostname()
	bootstrapURL := strings.TrimRight(cfg.ConvexSiteURL, "/") + "/devices/bootstrap"
	pendingURL := strings.TrimRight(cfg.ConvexSiteURL, "/") + "/devices/bootstrap-pending"

	// Pick the relay password the agent is actually using. Per-relay
	// password wins over global; cached creds win over nothing.
	pickRelayPassword := func() (string, string) {
		relays := cfg.RelayServers
		if len(relays) == 0 {
			relays = cfg.CachedRelayServers
		}
		globalPw := cfg.RelayPassword
		if globalPw == "" {
			globalPw = cfg.CachedRelayPassword
		}
		for _, r := range relays {
			pw := r.Password
			if pw == "" {
				pw = globalPw
			}
			if pw != "" {
				return pw, r.QuicAddr
			}
		}
		return "", ""
	}

	registerPending := func(client *http.Client) {
		pw, label := pickRelayPassword()
		if pw == "" {
			// No relay password configured. The pending-claim flow
			// can't help — the box is LAN-only until the user pairs
			// it via beacon. Don't spam the endpoint without proof.
			log.Printf("[bootstrap-convex] no Convex row and no relay password — pending-claim flow skipped (LAN beacon pairing required)")
			return
		}
		body := map[string]interface{}{
			"deviceId":      cfg.DeviceID,
			"hardwareId":    hwid,
			"publicKey":     pubKey,
			"relayPassword": pw,
			"name":          hostname,
			"platform":      runtimeGOOS(),
			"quicHost":      host,
			"quicPort":      httpPort,
			"relayLabel":    label,
		}
		data, _ := json.Marshal(body)
		resp, err := client.Post(pendingURL, "application/json", strings.NewReader(string(data)))
		if err != nil {
			log.Printf("[bootstrap-pending] request failed: %v", err)
			return
		}
		defer resp.Body.Close()
		if resp.StatusCode >= 400 {
			snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
			log.Printf("[bootstrap-pending] Convex returned %d: %s", resp.StatusCode, strings.TrimSpace(string(snippet)))
			return
		}
		log.Printf("[bootstrap-pending] Registered as pending claim (device %s) — visit dashboard to claim", cfg.DeviceID[:8])
	}

	register := func() {
		body := map[string]interface{}{
			"deviceId":   cfg.DeviceID,
			"hardwareId": hwid,
			"publicKey":  pubKey,
			"quicHost":   host,
			"quicPort":   httpPort,
		}
		data, _ := json.Marshal(body)
		client := &http.Client{Timeout: 10 * time.Second}
		resp, err := client.Post(bootstrapURL, "application/json", strings.NewReader(string(data)))
		if err != nil {
			log.Printf("[bootstrap-convex] request failed: %v", err)
			return
		}
		defer resp.Body.Close()
		if resp.StatusCode == http.StatusNotFound {
			// Truly-fresh box — no Convex row matches this triple.
			// Fall through to the pending-claim path so the user can
			// adopt the box from their dashboard.
			log.Printf("[bootstrap-convex] no Convex row for device %s — registering as pending claim", cfg.DeviceID[:8])
			registerPending(client)
			return
		}
		if resp.StatusCode >= 400 {
			snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
			log.Printf("[bootstrap-convex] Convex returned %d: %s", resp.StatusCode, strings.TrimSpace(string(snippet)))
			return
		}
		log.Printf("[bootstrap-convex] Registered as needs-auth (device %s)", cfg.DeviceID[:8])
	}
	register()
	ticker := time.NewTicker(30 * time.Second)
	go func() {
		for range ticker.C {
			register()
		}
	}()
}
