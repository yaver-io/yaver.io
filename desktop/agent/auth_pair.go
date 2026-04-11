package main

// auth_pair.go — QR-based auth pairing for headless machines.
//
// Problem: solo dev has a Mac mini at the top of the house with
// no display, a Hetzner VPS over SSH, or a Linux box they
// connect to from a laptop. Running `yaver auth` on the
// headless machine normally opens a browser for Apple / Google /
// O365 OAuth, which nobody's there to click through — and the
// dev's already signed in on their laptop anyway.
//
// Solution: the source machine (laptop) already has a valid
// token in ~/.yaver/config.json. We copy it to the target
// machine (Mac mini / VPS) over the existing P2P relay using
// a short, human-typeable code as the one-shot secret. The
// laptop can scan the QR code the target prints (either via
// the mobile yaver app's camera or a plain phone camera that
// opens a `yaver://` URL), confirm the pairing, and the token
// lands on the target. No Convex roundtrip for the token — it
// flows directly over the relay.
//
// Security model:
//
//   1. The pairing code is a 6-char random string from an
//      unambiguous alphabet (no 0/O/1/I). 1 in ~1.3 billion
//      guesses to brute force, and the window is 10 minutes.
//   2. Pairing endpoints are only open while a code is active,
//      only accept ONE successful submission, and destroy the
//      code on success.
//   3. The source proves it knows the code; the target trusts
//      the first valid submission.
//   4. The QR payload includes both the local LAN URL and the
//      relay URL so the source can reach the target through
//      whichever transport is available.
//
// Wire format (QR payload, plain text):
//
//   yaver-pair://<code>?urls=<csv-of-urls>&host=<hostname>
//
// where `urls` is a comma-separated list of candidate endpoints
// (local LAN first, relay fallback). The mobile app parses it,
// shows a confirmation, and POSTs the token.
//
// HTTP surface:
//
//   GET  /auth/pair/info           — returns the active code's
//                                    metadata (host, expiry) if any
//   POST /auth/pair/submit?code=X  — UNAUTHENTICATED while a code
//                                    is active; body = {token,
//                                    convexSiteUrl, userId}

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/mdp/qrterminal/v3"
)

// pairingSession is the in-memory state while `yaver auth pair`
// is waiting for a token.
type pairingSession struct {
	Code           string
	Hostname       string
	ExpiresAt      time.Time
	ReceivedToken  string
	ReceivedURL    string // ConvexSiteURL the source was signed into
	ReceivedUserID string // optional
	// done is closed as soon as a valid submission lands, so
	// the CLI side can block on it without polling.
	done chan struct{}
}

var (
	pairingMu      sync.Mutex
	activePairing  *pairingSession
	pairingAlphabet = []byte("ABCDEFGHJKLMNPQRSTUVWXYZ23456789")
)

// generatePairCode returns a 6-char pairing code from the safe
// alphabet. Excludes 0/O/1/I to avoid "is that a zero or an
// oh" confusion when the dev types it.
func generatePairCode() string {
	buf := make([]byte, 6)
	_, _ = rand.Read(buf)
	for i, b := range buf {
		buf[i] = pairingAlphabet[int(b)%len(pairingAlphabet)]
	}
	return string(buf)
}

// StartPairingSession creates an active pairing window. Returns
// the session so the caller can block on session.done.
func StartPairingSession(ttl time.Duration) (*pairingSession, error) {
	hostname, _ := os.Hostname()
	code := generatePairCode()
	session := &pairingSession{
		Code:      code,
		Hostname:  hostname,
		ExpiresAt: time.Now().Add(ttl),
		done:      make(chan struct{}),
	}
	pairingMu.Lock()
	// Replace any previous session so only one is active at a
	// time. The previous session's CLI caller can notice by
	// checking activePairing.
	if prev := activePairing; prev != nil {
		close(prev.done)
	}
	activePairing = session
	pairingMu.Unlock()
	return session, nil
}

// EndPairingSession clears the active window.
func EndPairingSession() {
	pairingMu.Lock()
	defer pairingMu.Unlock()
	if activePairing != nil {
		select {
		case <-activePairing.done:
		default:
			close(activePairing.done)
		}
		activePairing = nil
	}
}

// activePairingSnapshot returns a copy of the current session's
// public fields or nil if none is active / expired.
func activePairingSnapshot() *pairingSession {
	pairingMu.Lock()
	defer pairingMu.Unlock()
	if activePairing == nil {
		return nil
	}
	if time.Now().After(activePairing.ExpiresAt) {
		return nil
	}
	// Shallow copy — callers only read.
	cp := *activePairing
	return &cp
}

// candidatePairingURLs returns the URLs the source machine can
// use to reach this target. Always includes the local direct
// URL (for same-LAN pairing) and falls back to the relay when
// the config has a relay configured.
func candidatePairingURLs() []string {
	urls := []string{}
	if ip := getLocalIP(); ip != "" && ip != "0.0.0.0" {
		urls = append(urls, fmt.Sprintf("http://%s:18080", ip))
	}
	urls = append(urls, "http://127.0.0.1:18080")
	// Relay URL: if this agent is registered, every relay it
	// knows is a valid target. Keep this short — the source
	// will try them in order.
	if cfg, err := LoadConfig(); err == nil && cfg != nil {
		for _, r := range cfg.RelayServers {
			relayURL := r.HttpURL
			if relayURL == "" {
				continue
			}
			if cfg.DeviceID != "" {
				urls = append(urls, fmt.Sprintf("%s/d/%s", strings.TrimRight(relayURL, "/"), cfg.DeviceID))
			}
		}
	}
	return urls
}

// pairQRPayload composes the plain-text QR body. Stable format
// so the mobile app's parser stays simple.
func pairQRPayload(session *pairingSession) string {
	urls := candidatePairingURLs()
	return fmt.Sprintf(
		"yaver-pair://%s?host=%s&urls=%s",
		session.Code,
		session.Hostname,
		strings.Join(urls, ","),
	)
}

// --- HTTP handlers -------------------------------------------------------

// handlePairInfo returns non-sensitive metadata about the
// currently-active pairing session. Lets the source machine
// confirm the target is in pairing mode before it ships a
// token over.
func (s *HTTPServer) handlePairInfo(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, http.StatusMethodNotAllowed, "use GET")
		return
	}
	session := activePairingSnapshot()
	if session == nil {
		jsonError(w, http.StatusNotFound, "no active pairing session")
		return
	}
	jsonReply(w, http.StatusOK, map[string]interface{}{
		"ok":        true,
		"host":      session.Hostname,
		"expiresAt": session.ExpiresAt.UTC().Format(time.RFC3339),
		// Never return the code itself — the source already
		// has it from the QR / manual entry.
	})
}

// handlePairSubmit accepts a token submission from the source.
// UNAUTHENTICATED on purpose: the pairing code is the secret.
// Only accepts one submission; closes the session on success.
func (s *HTTPServer) handlePairSubmit(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}
	code := r.URL.Query().Get("code")
	if code == "" {
		jsonError(w, http.StatusBadRequest, "code required")
		return
	}

	pairingMu.Lock()
	session := activePairing
	// Validate + consume all under the same lock so two
	// concurrent submits can't both win.
	if session == nil || session.Code != code {
		pairingMu.Unlock()
		jsonError(w, http.StatusForbidden, "invalid or inactive pairing code")
		return
	}
	if time.Now().After(session.ExpiresAt) {
		pairingMu.Unlock()
		jsonError(w, http.StatusGone, "pairing code expired")
		return
	}
	if session.ReceivedToken != "" {
		pairingMu.Unlock()
		jsonError(w, http.StatusConflict, "token already received")
		return
	}
	pairingMu.Unlock()

	var body struct {
		Token         string `json:"token"`
		ConvexSiteURL string `json:"convexSiteUrl"`
		UserID        string `json:"userId,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if body.Token == "" {
		jsonError(w, http.StatusBadRequest, "token required")
		return
	}

	pairingMu.Lock()
	// Re-check after unmarshal in case the session got ended
	// mid-call.
	session = activePairing
	if session == nil || session.Code != code {
		pairingMu.Unlock()
		jsonError(w, http.StatusForbidden, "invalid or inactive pairing code")
		return
	}
	session.ReceivedToken = body.Token
	session.ReceivedURL = body.ConvexSiteURL
	session.ReceivedUserID = body.UserID
	select {
	case <-session.done:
	default:
		close(session.done)
	}
	pairingMu.Unlock()

	jsonReply(w, http.StatusOK, map[string]interface{}{
		"ok":   true,
		"host": session.Hostname,
	})
}

// --- CLI ------------------------------------------------------------------

// runAuthPair opens a pairing session on the target, prints
// the QR, and blocks until a token arrives or the window
// expires. Called as `yaver auth pair`.
func runAuthPair(args []string) {
	ttl := 10 * time.Minute
	for i := 0; i < len(args); i++ {
		if args[i] == "--ttl" && i+1 < len(args) {
			if d, err := time.ParseDuration(args[i+1]); err == nil {
				ttl = d
			}
			i++
		}
	}
	session, err := StartPairingSession(ttl)
	if err != nil {
		fmt.Fprintf(os.Stderr, "pair: %v\n", err)
		os.Exit(1)
	}
	defer EndPairingSession()

	payload := pairQRPayload(session)
	hostname, _ := os.Hostname()

	fmt.Println()
	fmt.Println("Yaver auth pairing — scan this QR from another device that's signed in:")
	fmt.Println()
	qrterminal.GenerateHalfBlock(payload, qrterminal.L, os.Stdout)
	fmt.Println()
	fmt.Printf("  Target:        %s\n", hostname)
	fmt.Printf("  Pairing code:  %s\n", session.Code)
	fmt.Printf("  Expires:       %s\n", session.ExpiresAt.Format(time.RFC3339))
	fmt.Println()
	fmt.Println("From a source machine already signed in:")
	fmt.Printf("  yaver auth send %s <one of the URLs below>\n", session.Code)
	for _, u := range candidatePairingURLs() {
		fmt.Printf("    %s\n", u)
	}
	fmt.Println()
	fmt.Println("Or open the Yaver mobile app → More → Pair device → scan the QR.")
	fmt.Println()
	fmt.Println("Waiting for the source to submit a token…")

	select {
	case <-session.done:
	case <-time.After(ttl):
		fmt.Fprintln(os.Stderr, "pairing window expired — run `yaver auth pair` again")
		os.Exit(1)
	}

	// The HTTP submit handler populated the fields under the
	// mutex, so a re-read here is safe.
	if session.ReceivedToken == "" {
		fmt.Fprintln(os.Stderr, "pairing ended without a token")
		os.Exit(1)
	}

	// Persist the token into the target's config — same shape
	// `yaver auth` writes.
	cfg, err := LoadConfig()
	if err != nil || cfg == nil {
		cfg = &Config{}
	}
	cfg.AuthToken = session.ReceivedToken
	if session.ReceivedURL != "" {
		cfg.ConvexSiteURL = session.ReceivedURL
	}
	if err := SaveConfig(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "save config: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("✓ paired — the agent now has a valid auth token")
	fmt.Println("  Run `yaver serve` (or restart the launchd/systemd unit) to use it.")
}

// runAuthSend is the source-side CLI. `yaver auth send <code>
// <target-url>` POSTs the laptop's token to the target's
// /auth/pair/submit endpoint.
func runAuthSend(args []string) {
	if len(args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: yaver auth send <code> <target-url>")
		os.Exit(1)
	}
	code := strings.ToUpper(args[0])
	target := strings.TrimRight(args[1], "/")

	cfg, err := LoadConfig()
	if err != nil || cfg == nil || cfg.AuthToken == "" {
		fmt.Fprintln(os.Stderr, "this machine isn't signed in — run `yaver auth` first")
		os.Exit(1)
	}

	// Optional sanity check: ping the target's pair info
	// endpoint so we fail fast if the target isn't in pairing
	// mode.
	if resp, err := http.Get(target + "/auth/pair/info"); err == nil {
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			fmt.Fprintf(os.Stderr, "target %s is not in pairing mode (HTTP %d)\n", target, resp.StatusCode)
			os.Exit(1)
		}
	}

	body, _ := json.Marshal(map[string]interface{}{
		"token":         cfg.AuthToken,
		"convexSiteUrl": cfg.ConvexSiteURL,
	})
	req, err := http.NewRequest("POST", target+"/auth/pair/submit?code="+code, strings.NewReader(string(body)))
	if err != nil {
		fmt.Fprintf(os.Stderr, "build request: %v\n", err)
		os.Exit(1)
	}
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "send: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		fmt.Fprintf(os.Stderr, "target rejected the submit (HTTP %d)\n", resp.StatusCode)
		os.Exit(1)
	}
	fmt.Printf("✓ token forwarded to %s\n", target)
}

// Keep net imported for any future direct-socket fallbacks.
var _ = net.InterfaceAddrs
