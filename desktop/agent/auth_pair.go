package main

// auth_pair.go — passkey-based auth pairing for headless machines.
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
// a short, human-typeable passkey as the one-shot secret. The
// target prints the passkey in big block letters; on a signed-in
// machine the dev runs `yaver auth send <passkey> <target-url>`
// and the token lands on the target.
//
// We used to print a `yaver-pair://...` QR here, but iOS Camera
// refuses to open custom URI schemes ("kullanılabilir veri
// bulunamadı" / "no usable data found"), so it added zero value.
// The passkey + one CLI command is simpler and actually works.
//
// Security model:
//
//   1. The passkey is a 6-char random string from an
//      unambiguous alphabet (no 0/O/1/I). 1 in ~1.3 billion
//      guesses to brute force, and the window is 10 minutes.
//   2. Pairing endpoints are only open while a passkey is
//      active, only accept ONE successful submission, and
//      destroy the passkey on success.
//   3. The source proves it knows the passkey; the target
//      trusts the first valid submission.
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
	"encoding/base64"
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
	pairingMu       sync.Mutex
	activePairing   *pairingSession
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
// use to reach this target. Prefers Tailscale IPs (when
// detected) because two nodes on the same Tailnet can skip
// the relay entirely; falls back to the local LAN address;
// finally lists the registered relay URLs so the source can
// reach the target from anywhere.
func candidatePairingURLs() []string {
	urls := []string{}
	// Tailscale first — zero-config VPN for the solo dev.
	urls = append(urls, tailscaleIPCandidates(18080)...)
	if ip := getLocalIP(); ip != "" && ip != "0.0.0.0" {
		urls = append(urls, fmt.Sprintf("http://%s:18080", ip))
	}
	urls = append(urls, "http://127.0.0.1:18080")
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

// bigPasskey renders the 6-char passkey as an ASCII block so
// it reads clearly from across the room when the dev is glancing
// at the headless machine's terminal. Keeps the output pure
// ASCII — no fancy box-drawing — so it works over SSH, inside
// `screen`, on Windows conhost, and in CI log scrapers.
func bigPasskey(code string) string {
	var b strings.Builder
	b.WriteString("    +")
	b.WriteString(strings.Repeat("-", len(code)*4+3))
	b.WriteString("+\n")
	b.WriteString("    |  ")
	for i, r := range code {
		if i > 0 {
			b.WriteString("   ")
		}
		b.WriteRune(r)
	}
	b.WriteString("  |\n")
	b.WriteString("    +")
	b.WriteString(strings.Repeat("-", len(code)*4+3))
	b.WriteString("+\n")
	return b.String()
}

// pairQROptOut returns true when the QR code should be suppressed.
// QR is a convenience layer — every flow that prints it must also print
// the plain URL + passkey + reachable hosts, so suppressing the QR
// never breaks pairing.
//
// Suppressed when:
//   - YAVER_NO_QR=1 (script-friendly opt-out)
//   - YAVER_QR=0 (alternate spelling some users reach for first)
//   - stdout is not a TTY (block-art garbage in pipes/log files)
func pairQROptOut() bool {
	if envTruthy(os.Getenv("YAVER_NO_QR")) {
		return true
	}
	switch strings.ToLower(strings.TrimSpace(os.Getenv("YAVER_QR"))) {
	case "0", "false", "off", "no":
		return true
	}
	if !isStdoutTTY() {
		return true
	}
	return false
}

// printPairURLAndQR prints the canonical pair URL and (unless opted
// out) a terminal QR for it. Always called *after* the existing
// passkey + reachable-URL block so the additive contract is obvious
// to readers (QR is extra; passkey + URLs already got them paired).
//
// Safe to call with an empty url — prints nothing in that case.
func printPairURLAndQR(pairURL string) {
	if strings.TrimSpace(pairURL) == "" {
		return
	}
	fmt.Println("Or scan / open this URL on a phone that's already signed in:")
	fmt.Println()
	fmt.Printf("    %s\n", pairURL)
	fmt.Println()
	if pairQROptOut() {
		return
	}
	qrterminal.GenerateWithConfig(pairURL, qrterminal.Config{
		Level:     qrterminal.L,
		Writer:    os.Stdout,
		BlackChar: qrterminal.BLACK,
		WhiteChar: qrterminal.WHITE,
		QuietZone: 2,
	})
	fmt.Println()
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

// handlePairSession returns normalized pairing-session metadata for a
// scanning client (mobile app, web pair page, MCP). Additive over
// /auth/pair/info — that endpoint stays unchanged so existing callers
// keep working.
//
// Lookup keys (any one accepted, in priority order):
//   - sid query param (canonical going forward; for Slice A sid==code)
//   - code query param (back-compat with the manual-passkey flow)
//   - none (returns the active session if any — same as /info)
//
// Response shape is intentionally a superset of /auth/pair/info so a
// client can use one endpoint instead of two. Never returns the trust
// secret itself; the scanner already has it (typed in or carried in
// the URL it scanned).
func (s *HTTPServer) handlePairSession(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, http.StatusMethodNotAllowed, "use GET")
		return
	}
	wantSid := strings.TrimSpace(r.URL.Query().Get("sid"))
	wantCode := strings.TrimSpace(r.URL.Query().Get("code"))
	session := activePairingSnapshot()
	if session == nil {
		jsonError(w, http.StatusNotFound, "no active pairing session")
		return
	}
	// For Slice A there's exactly one in-memory session and sid==code.
	// We accept either form so a client built against the future
	// session-id model works against today's agent.
	if wantSid != "" && !secretEqualFold(wantSid, session.Code) {
		jsonError(w, http.StatusNotFound, "no pairing session matches sid")
		return
	}
	if wantCode != "" && !secretEqualFold(wantCode, session.Code) {
		jsonError(w, http.StatusNotFound, "no pairing session matches code")
		return
	}
	resp := map[string]interface{}{
		"ok":              true,
		"sessionId":       session.Code,
		"hostname":        session.Hostname,
		"expiresAt":       session.ExpiresAt.UTC().Format(time.RFC3339),
		"canDirectSubmit": true,
	}
	// targetUrls help a client that has a sid but not a reachable URL
	// pick something that works. Same set we hand to the CLI so the
	// surface is consistent.
	if urls := candidatePairingURLs(); len(urls) > 0 {
		resp["targetUrls"] = urls
	}
	jsonReply(w, http.StatusOK, resp)
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
	if session == nil || !secretEqualFold(session.Code, code) {
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
	if session == nil || !secretEqualFold(session.Code, code) {
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

	// If this pair flow is being used to recover a running daemon,
	// clear the degraded auth-expired flag immediately instead of
	// waiting for the next heartbeat tick.
	if s != nil {
		s.authExpired.Store(false)
	}

	jsonReply(w, http.StatusOK, map[string]interface{}{
		"ok":   true,
		"host": session.Hostname,
	})
}

// handlePairEncrypted accepts an encrypted token submission from a source
// client that already knows the active pairing code. This keeps the raw
// bearer off the wire on direct/LAN recovery paths.
func (s *HTTPServer) handlePairEncrypted(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		Code            string `json:"code"`
		Encrypted       string `json:"encrypted"`
		SenderPublicKey string `json:"senderPublicKey"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonReply(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}
	if strings.TrimSpace(body.Code) == "" {
		jsonReply(w, http.StatusBadRequest, map[string]string{"error": "pair code required"})
		return
	}

	pairingMu.Lock()
	session := activePairing
	if session == nil || !secretEqualFold(session.Code, body.Code) {
		pairingMu.Unlock()
		jsonReply(w, http.StatusForbidden, map[string]string{"error": "invalid or inactive pairing code"})
		return
	}
	if time.Now().After(session.ExpiresAt) {
		pairingMu.Unlock()
		jsonReply(w, http.StatusGone, map[string]string{"error": "pairing code expired"})
		return
	}
	if session.ReceivedToken != "" {
		pairingMu.Unlock()
		jsonReply(w, http.StatusConflict, map[string]string{"error": "token already received"})
		return
	}
	pairingMu.Unlock()

	if body.Encrypted == "" || body.SenderPublicKey == "" {
		jsonReply(w, http.StatusBadRequest, map[string]string{"error": "encrypted and senderPublicKey required"})
		return
	}
	dk, err := LoadOrGenerateKeys()
	if err != nil {
		jsonReply(w, http.StatusInternalServerError, map[string]string{"error": "cannot load device keys"})
		return
	}
	senderPubBytes, err := base64.StdEncoding.DecodeString(body.SenderPublicKey)
	if err != nil || len(senderPubBytes) != 32 {
		jsonReply(w, http.StatusBadRequest, map[string]string{"error": "invalid senderPublicKey"})
		return
	}
	var senderPub [32]byte
	copy(senderPub[:], senderPubBytes)

	encBytes, err := base64.StdEncoding.DecodeString(body.Encrypted)
	if err != nil {
		jsonReply(w, http.StatusBadRequest, map[string]string{"error": "invalid base64 in encrypted"})
		return
	}
	plaintext, err := dk.DecryptPairPayload(encBytes, senderPub)
	if err != nil {
		jsonReply(w, http.StatusForbidden, map[string]string{"error": "decryption failed: " + err.Error()})
		return
	}

	token := strings.TrimSpace(string(plaintext))
	if token == "" {
		jsonReply(w, http.StatusBadRequest, map[string]string{"error": "empty token after decryption"})
		return
	}

	pairingMu.Lock()
	session = activePairing
	if session == nil || !secretEqualFold(session.Code, body.Code) {
		pairingMu.Unlock()
		jsonReply(w, http.StatusForbidden, map[string]string{"error": "invalid or inactive pairing code"})
		return
	}
	session.ReceivedToken = token
	if cfg, err := LoadConfig(); err == nil && cfg != nil && strings.TrimSpace(cfg.ConvexSiteURL) != "" {
		session.ReceivedURL = cfg.ConvexSiteURL
	}
	select {
	case <-session.done:
	default:
		close(session.done)
	}
	pairingMu.Unlock()

	if s != nil {
		s.authExpired.Store(false)
	}

	jsonReply(w, http.StatusOK, map[string]interface{}{
		"ok":   true,
		"host": session.Hostname,
	})
}

// --- CLI ------------------------------------------------------------------

// runAuthPair opens a pairing session on the target, prints
// the QR, and blocks until a token arrives or the window
// expires. Called as `yaver auth pair`.
//
// Subcommands:
//
//	list               — print every paired token (masked)
//	revoke <id|label>  — remove a paired token
//	(no subcommand)    — start a new pairing session
//
// Flags on the default path:
//
//	--replace   — overwrite cfg.AuthToken instead of appending
//	              to the paired-tokens ledger. Legacy single-
//	              user behavior.
//	--label NAME — tag the incoming token for easy revoke.
//	--ttl 10m    — pairing window.
func runAuthPair(args []string) {
	// Sub-subcommands: list + revoke.
	if len(args) > 0 {
		switch args[0] {
		case "list":
			listPairedTokensCmd()
			return
		case "revoke":
			if len(args) < 2 {
				fmt.Fprintln(os.Stderr, "usage: yaver auth pair revoke <label|hash-prefix>")
				os.Exit(1)
			}
			n := RevokePairedToken(args[1])
			if n == 0 {
				fmt.Fprintf(os.Stderr, "no paired token matched %q\n", args[1])
				os.Exit(2)
			}
			fmt.Printf("✓ revoked %d paired token(s)\n", n)
			return
		}
	}

	ttl := 10 * time.Minute
	replace := false
	label := ""
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--ttl":
			if i+1 < len(args) {
				if d, err := time.ParseDuration(args[i+1]); err == nil {
					ttl = d
				}
				i++
			}
		case "--replace":
			replace = true
		case "--label":
			if i+1 < len(args) {
				label = args[i+1]
				i++
			}
		case "--no-qr":
			// Per-invocation QR opt-out. Same effect as
			// YAVER_NO_QR=1 for this process. Useful for the
			// "I'm pairing over SSH and don't want block-art
			// in my scrollback" case.
			_ = os.Setenv("YAVER_NO_QR", "1")
		}
	}
	_ = replace
	_ = label
	session, err := StartPairingSession(ttl)
	if err != nil {
		fmt.Fprintf(os.Stderr, "pair: %v\n", err)
		os.Exit(1)
	}
	defer EndPairingSession()

	hostname, _ := os.Hostname()
	urls := candidatePairingURLs()
	// Pick a reasonable default URL for the copy-paste hint: LAN
	// first (most likely to work), falling back to the first
	// candidate (usually Tailscale) if there's no LAN address.
	defaultURL := ""
	for _, u := range urls {
		if strings.Contains(u, "127.0.0.1") {
			continue
		}
		defaultURL = u
		break
	}
	if defaultURL == "" && len(urls) > 0 {
		defaultURL = urls[0]
	}

	fmt.Println()
	fmt.Printf("Yaver auth pairing — target: %s\n", hostname)
	fmt.Println()
	fmt.Println("Passkey:")
	fmt.Println()
	fmt.Print(bigPasskey(session.Code))
	fmt.Println()
	fmt.Printf("  Expires in:  %s\n", time.Until(session.ExpiresAt).Round(time.Second))
	fmt.Println()
	fmt.Println("On a machine that's already signed in, run:")
	fmt.Println()
	if defaultURL != "" {
		fmt.Printf("    yaver auth send %s %s\n", session.Code, defaultURL)
	} else {
		fmt.Printf("    yaver auth send %s <target-url>\n", session.Code)
	}
	fmt.Println()
	if len(urls) > 1 {
		fmt.Println("Other reachable URLs for this target:")
		for _, u := range urls {
			if u == defaultURL {
				continue
			}
			fmt.Printf("    %s\n", u)
		}
		fmt.Println()
	}
	// Additive QR / pair-URL on-ramp. Always after the existing
	// passkey + reachable-URLs block so the no-regression contract
	// is obvious — every char above is enough to pair without ever
	// looking at the URL or QR.
	pairURL := buildPairURL(session, PairURLOptions{
		Mode:   "pair",
		Target: defaultURL,
	})
	printPairURLAndQR(pairURL)
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

	// Persist the token. Two modes:
	//
	// - --replace (legacy / single-user): overwrite
	//   cfg.AuthToken so the target's primary session is this
	//   one. Use when a solo dev wants to migrate their token
	//   across machines.
	//
	// - default (multi-user): append to the paired-tokens
	//   ledger. The target keeps its existing primary token
	//   AND accepts this new one from the HTTP auth middleware.
	//   Multiple phones / accounts can stack up on the same
	//   remote Mac mini this way.
	cfg, err := LoadConfig()
	if err != nil || cfg == nil {
		cfg = &Config{}
	}
	if replace || cfg.AuthToken == "" {
		if session.ReceivedURL != "" {
			cfg.ConvexSiteURL = session.ReceivedURL
		}
		if err := SetAuthToken(cfg, session.ReceivedToken); err != nil {
			fmt.Fprintf(os.Stderr, "save config: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("✓ paired — primary auth token installed")
		fmt.Println()
		startServeIfStopped()
		return
	}

	sourceHost := ""
	if session.ReceivedUserID != "" {
		sourceHost = session.ReceivedUserID
	}
	if err := AddPairedToken(session.ReceivedToken, label, session.ReceivedURL, sourceHost); err != nil {
		fmt.Fprintf(os.Stderr, "add paired token: %v\n", err)
		os.Exit(1)
	}
	labelMsg := ""
	if label != "" {
		labelMsg = " (label: " + label + ")"
	}
	fmt.Printf("✓ paired%s — added to paired-tokens ledger (%d total)\n",
		labelMsg, len(ListPairedTokens()))
	fmt.Println("  The primary owner's token is still active; this added user can hit")
	fmt.Println("  the agent alongside them. Revoke with `yaver auth pair revoke <label>`.")
	fmt.Println()
	startServeIfStopped()
}

// listPairedTokensCmd prints every accepted paired token in a
// terminal-friendly form. Tokens are masked — the stored
// ledger has the real bearer, but `list` only shows the
// fingerprint so a shoulder-surfer at a coffee shop can't
// copy one off the screen.
func listPairedTokensCmd() {
	tokens := ListPairedTokens()
	if len(tokens) == 0 {
		fmt.Println("(no paired tokens yet)")
		return
	}
	for _, t := range tokens {
		lastUse := "never"
		if t.LastUsedAt != "" {
			lastUse = t.LastUsedAt
		}
		source := t.SourceHost
		if source == "" {
			source = "(unknown)"
		}
		label := t.Label
		if label == "" {
			label = "(no label)"
		}
		fmt.Printf("  %s  %-20s  source=%s  added=%s  last-used=%s\n",
			t.TokenHash[:8], label, source, t.AddedAt, lastUse)
	}
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
