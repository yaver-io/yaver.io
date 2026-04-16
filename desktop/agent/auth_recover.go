package main

// auth_recover.go — remote-triggered auth recovery for the
// "agent is up but unauthenticated, dev is outside the LAN"
// case. The scenario:
//
//   1. Mac mini loses auth (session expired, reboot, whatever).
//   2. Dev is at a cafe, can't SSH in.
//   3. Mobile app notices every request returns 401.
//   4. Mobile app hits an UNAUTHENTICATED "recover" endpoint
//      with a pre-shared bootstrap secret.
//   5. Agent either starts a pair window (mobile forwards its
//      own token) or a device-code flow (mobile opens the
//      OAuth URL for the dev to sign in fresh).
//   6. Within minutes the agent has a valid token again,
//      over the existing relay, no SSH.
//
// The bootstrap secret is set once during install via
// `yaver config set bootstrap-secret <value>`. The hash sits
// in config.json. Raw secret is stored only in the mobile
// app's keychain (or in the dev's password manager). If the
// dev loses both, they're back to SSH — which is the correct
// failure mode.
//
// Rate-limited to 1 attempt per 5 seconds per IP so an
// attacker who knows the URL can't brute-force the secret.
//
// Connectivity requirement: this endpoint is auth-free, but
// the mobile app still has to REACH it. The three supported
// transports — Tailscale overlay, Cloudflare Tunnel, or the
// yaver relay — all run independently of the agent's Convex
// auth token and keep working while the token is stale. The
// mobile side caches the relay URL + tunnel host + Tailscale
// IPs in its device registry, so "agent up, auth down" still
// has a live path as long as one of those transports is wired.

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

// RecoveryRequest is the POST body for /auth/recover.
type RecoveryRequest struct {
	// Secret is the optional pre-shared bootstrap secret. If
	// set, compared against the hash stored in
	// Config.BootstrapSecretHash. Either this OR a Bearer
	// Authorization header (host-token mode) must be present.
	Secret string `json:"secret"`
	// Mode picks the recovery path:
	//   "pair"        — start a 10-minute pair window and
	//                   return the pair code. Follow-up: the
	//                   caller POSTs their already-authed
	//                   token to /auth/pair/submit?code=...
	//   "device-code" — start a fresh Convex device-code flow
	//                   and return the user-code + URL for a
	//                   browser OAuth roundtrip.
	Mode string `json:"mode"`
}

// RecoveryResponse is the per-mode return shape.
type RecoveryResponse struct {
	OK            bool   `json:"ok"`
	Mode          string `json:"mode"`
	PairCode      string `json:"pairCode,omitempty"`
	PairSubmitURL string `json:"pairSubmitUrl,omitempty"`
	DeviceCodeURL string `json:"deviceCodeUrl,omitempty"`
	UserCode      string `json:"userCode,omitempty"`
	ExpiresAt     string `json:"expiresAt,omitempty"`
}

// applyRecoveredAuthToken persists a newly recovered auth token and updates
// in-memory state so the running daemon can recover without requiring manual
// intervention from the user.
func applyRecoveredAuthToken(token, convexURL string, s *HTTPServer) {
	cfg, _ := LoadConfig()
	if cfg == nil {
		cfg = &Config{}
	}
	cfg.AuthToken = token
	if convexURL != "" {
		cfg.ConvexSiteURL = convexURL
	}
	if cfg.ConvexSiteURL == "" {
		cfg.ConvexSiteURL = defaultConvexSiteURL
	}
	_ = SaveConfig(cfg)

	if s != nil {
		s.token = token
		s.authExpired.Store(false)
		if s.taskMgr != nil {
			s.taskMgr.AuthToken = token
			s.taskMgr.ConvexURL = cfg.ConvexSiteURL
		}
	}
}

// completePairRecoveryInBackground waits for a recovery-initiated pairing
// session to receive a token, then persists it so the daemon actually exits
// the degraded auth-expired state.
func completePairRecoveryInBackground(session *pairingSession, s *HTTPServer) {
	if session == nil {
		return
	}
	select {
	case <-session.done:
		if strings.TrimSpace(session.ReceivedToken) == "" {
			return
		}
		applyRecoveredAuthToken(session.ReceivedToken, session.ReceivedURL, s)
	case <-time.After(time.Until(session.ExpiresAt) + 5*time.Second):
		return
	}
}

// recoveryRateLimiter tracks last-attempt timestamps per
// client IP so a rapid attacker can't hammer the endpoint.
type recoveryRateLimiter struct {
	mu       sync.Mutex
	attempts map[string]time.Time
}

var recoveryLimiter = &recoveryRateLimiter{attempts: map[string]time.Time{}}
var verifyHostTokenFn = verifyHostToken
var requestDeviceCodeFn = requestDeviceCode

// SetBootstrapSecret stores the hash of a newly-minted
// bootstrap secret into config.json. Called from
// `yaver config set bootstrap-secret <value>`.
func SetBootstrapSecret(secret string) error {
	cfg, err := LoadConfig()
	if err != nil || cfg == nil {
		cfg = &Config{}
	}
	if secret == "" {
		cfg.BootstrapSecretHash = ""
		return SaveConfig(cfg)
	}
	sum := sha256.Sum256([]byte(secret))
	cfg.BootstrapSecretHash = hex.EncodeToString(sum[:])
	return SaveConfig(cfg)
}

// verifyBootstrapSecret returns true if the supplied plaintext
// matches the stored hash. Constant-time comparison so a
// timing attack can't leak byte positions.
func verifyBootstrapSecret(plaintext string) bool {
	cfg, err := LoadConfig()
	if err != nil || cfg == nil || cfg.BootstrapSecretHash == "" {
		return false
	}
	sum := sha256.Sum256([]byte(plaintext))
	want := cfg.BootstrapSecretHash
	got := hex.EncodeToString(sum[:])
	return subtle.ConstantTimeCompare([]byte(want), []byte(got)) == 1
}

// rateLimitRecover checks whether a given IP is allowed to
// make another recovery attempt. 5-second cooldown.
func (r *recoveryRateLimiter) allow(ip string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now()
	if last, ok := r.attempts[ip]; ok && now.Sub(last) < 5*time.Second {
		return false
	}
	r.attempts[ip] = now
	// Garbage-collect old entries opportunistically so the
	// map doesn't grow unbounded under real attack.
	if len(r.attempts) > 512 {
		cutoff := now.Add(-10 * time.Minute)
		for k, v := range r.attempts {
			if v.Before(cutoff) {
				delete(r.attempts, k)
			}
		}
	}
	return true
}

func (r *recoveryRateLimiter) reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.attempts = map[string]time.Time{}
}

// --- HTTP ---------------------------------------------------------------

// handleAuthRecover is the unauthenticated recovery endpoint.
// Registered in httpserver.go at /auth/recover WITHOUT the
// auth() middleware — that's the whole point.
func (s *HTTPServer) handleAuthRecover(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}
	if s != nil && strings.TrimSpace(s.token) != "" && !s.authExpired.Load() {
		jsonError(w, http.StatusConflict, "agent auth is healthy; recovery is not allowed")
		return
	}
	ip := clientIP(r)
	if !recoveryLimiter.allow(ip) {
		jsonError(w, http.StatusTooManyRequests, "too many recovery attempts — wait 5 seconds")
		return
	}

	var body RecoveryRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	// Two ways to authenticate this call:
	//   1. host-token mode (preferred): the caller presents their
	//      own Convex Bearer token. We ask Convex who owns the
	//      hardware fingerprint of *this* machine and only allow
	//      the request if the caller IS that owner. Means there
	//      is nothing to remember — only the original host can
	//      remote-reauth their own box.
	//   2. shared-secret mode (legacy / no-prior-pairing): the
	//      caller presents the pre-shared bootstrap secret that
	//      was set up at install time.
	authedAsHost := false
	if bearer := extractBearerToken(r); bearer != "" {
		if ok, hostErr := verifyHostTokenFn(bearer); hostErr == nil && ok {
			authedAsHost = true
		}
	}
	if !authedAsHost {
		if body.Secret == "" {
			jsonError(w, http.StatusUnauthorized, "host token or bootstrap secret required")
			return
		}
		if !verifyBootstrapSecret(body.Secret) {
			jsonError(w, http.StatusForbidden, "invalid bootstrap secret")
			return
		}
	}
	if body.Mode == "" {
		body.Mode = "pair"
	}
	if body.Mode == "device-code" && !authedAsHost {
		jsonError(w, http.StatusForbidden, "device-code recovery requires verified host authentication")
		return
	}

	switch body.Mode {
	case "pair":
		session, err := StartPairingSession(10 * time.Minute)
		if err != nil {
			jsonError(w, http.StatusInternalServerError, err.Error())
			return
		}
		go completePairRecoveryInBackground(session, s)
		resp := RecoveryResponse{
			OK:            true,
			Mode:          "pair",
			PairCode:      session.Code,
			PairSubmitURL: fmt.Sprintf("/auth/pair/submit?code=%s", session.Code),
			ExpiresAt:     session.ExpiresAt.UTC().Format(time.RFC3339),
		}
		jsonReply(w, http.StatusOK, resp)

	case "device-code":
		// Kick off a Convex device-code flow and return the
		// user-code + URL so the caller can complete OAuth
		// in a browser with whichever provider they prefer
		// (Apple / Google / Microsoft — all live behind the
		// same yaver.io/auth/device page).
		cfg, err := LoadConfig()
		if err != nil || cfg == nil || cfg.ConvexSiteURL == "" {
			jsonError(w, http.StatusInternalServerError, "no convex URL configured")
			return
		}
		dc, err := requestDeviceCodeFn(cfg.ConvexSiteURL)
		if err != nil {
			jsonError(w, http.StatusBadGateway, "device-code request failed: "+err.Error())
			return
		}
		// Start a background goroutine that polls Convex for
		// the token and writes it to config on success. The
		// caller doesn't need to hang — it can poll
		// /auth/pair/info or the existing /agent/status to
		// know when auth is live again.
		go completeDeviceCodeInBackground(cfg.ConvexSiteURL, dc.DeviceCode)

		resp := RecoveryResponse{
			OK:            true,
			Mode:          "device-code",
			DeviceCodeURL: "https://yaver.io/auth/device?code=" + dc.UserCode,
			UserCode:      dc.UserCode,
			ExpiresAt:     time.UnixMilli(dc.ExpiresAt).UTC().Format(time.RFC3339),
		}
		jsonReply(w, http.StatusOK, resp)

	default:
		jsonError(w, http.StatusBadRequest, "mode must be 'pair' or 'device-code'")
	}
}

// extractBearerToken pulls a Bearer token out of the
// Authorization header, returning "" if absent or malformed.
func extractBearerToken(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if !strings.HasPrefix(h, "Bearer ") {
		return ""
	}
	return strings.TrimSpace(h[7:])
}

// verifyHostToken asks Convex whether the bearer token belongs
// to the original host of this machine. The check is:
//
//	convex /devices/owner-by-hardware
//	  Bearer <caller token>
//	  body  {hardwareId: <our hardware fingerprint>}
//
// Convex authenticates the bearer to a userId and looks up the
// device by stable hardware fingerprint. The reply is a simple
// {isOwner: bool}. We allow the recovery action only when the
// caller is the registered owner. The agent might be in
// bootstrap mode (no token of its own) — that's fine, the
// hardware ID is local and Convex doesn't need our token to
// answer the lookup.
func verifyHostToken(bearer string) (bool, error) {
	cfg, _ := LoadConfig()
	convexURL := defaultConvexSiteURL
	if cfg != nil && cfg.ConvexSiteURL != "" {
		convexURL = cfg.ConvexSiteURL
	}
	hwid := HardwareID()
	if hwid == "" {
		return false, fmt.Errorf("no hardware id")
	}
	payload := fmt.Sprintf(`{"hardwareId":%q}`, hwid)
	req, err := http.NewRequest("POST", convexURL+"/devices/owner-by-hardware", strings.NewReader(payload))
	if err != nil {
		return false, err
	}
	req.Header.Set("Authorization", "Bearer "+bearer)
	req.Header.Set("Content-Type", "application/json")
	resp, err := httpClient.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	var result struct {
		OK      bool `json:"ok"`
		IsOwner bool `json:"isOwner"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return false, err
	}
	return result.OK && result.IsOwner, nil
}

// requestDeviceCode is a thin wrapper around the existing
// /auth/device-code Convex endpoint. Shared with runDeviceCodeAuth
// via the same payload shape.
func requestDeviceCode(convexURL string) (*deviceCodeResponse, error) {
	resp, err := httpClient.Post(convexURL+"/auth/device-code", "application/json", strings.NewReader("{}"))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	var dc deviceCodeResponse
	if err := json.NewDecoder(resp.Body).Decode(&dc); err != nil {
		return nil, err
	}
	return &dc, nil
}

// completeDeviceCodeInBackground polls Convex until the user
// finishes the browser OAuth flow, then writes the token to
// config. On success, the agent picks up the new token on the
// next request through the auth cache.
func completeDeviceCodeInBackground(convexURL, deviceCode string) {
	deadline := time.Now().Add(15 * time.Minute)
	for time.Now().Before(deadline) {
		token, done, err := pollDeviceCode(convexURL, deviceCode)
		if err != nil {
			time.Sleep(5 * time.Second)
			continue
		}
		if done && token != "" {
			applyRecoveredAuthToken(token, convexURL, nil)
			return
		}
		if done {
			return // expired
		}
		time.Sleep(5 * time.Second)
	}
}

// --- CLI ---------------------------------------------------------------

// runConfigBootstrapSecret is the `yaver config set
// bootstrap-secret <value>` path. Wired from config_cmd.go
// (or runConfig in main.go if that's where the dispatcher
// lives).
func runConfigBootstrapSecret(args []string) {
	if len(args) == 0 {
		cfg, _ := LoadConfig()
		if cfg == nil || cfg.BootstrapSecretHash == "" {
			fmt.Println("(no bootstrap secret set — `yaver config set bootstrap-secret <value>` to create one)")
			return
		}
		fmt.Printf("bootstrap-secret: configured (hash=%s…)\n", cfg.BootstrapSecretHash[:12])
		return
	}
	if args[0] == "clear" || args[0] == "unset" {
		_ = SetBootstrapSecret("")
		fmt.Println("✓ bootstrap secret cleared")
		return
	}
	if err := SetBootstrapSecret(args[0]); err != nil {
		fmt.Println("error:", err)
		return
	}
	fmt.Println("✓ bootstrap secret saved")
	fmt.Println("  Store this somewhere safe (password manager, mobile keychain).")
	fmt.Println("  If the agent ever loses auth, POST to /auth/recover with this secret")
	fmt.Println("  from the mobile app to trigger a new pair or device-code flow.")
}
