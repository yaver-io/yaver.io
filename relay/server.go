package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/quic-go/quic-go"
)

// reservedSubdomains is consulted on every path that can claim a
// public.<exposeDomain> subdomain — the agent-driven /expose/register
// flow AND the auto-provisioned <deviceId>.<exposeDomain> route fired
// at registration time. Pre-fix only the former checked it, so an
// attacker registering deviceId="admin" got https://admin.<domain>
// for free (M-6).
//
// Keep in sync with the inline list in handleExposeRegister.
var reservedSubdomains = map[string]bool{
	"www":    true,
	"api":    true,
	"relay":  true,
	"public": true,
	"admin":  true,
	"mail":   true,
	"app":    true,
}

// deviceIDShapePattern enforces a sane, URL-safe shape for incoming
// deviceIds. Matches the Convex-side validator we want (M-12) and
// blocks pathological inputs like empty strings, /-separated paths,
// shell metacharacters, and absurdly long ids that would blow up
// downstream URL building. M-6 + M-12.
var deviceIDShapePattern = regexp.MustCompile(`^[a-zA-Z0-9._-]{8,128}$`)

// RelayServer accepts QUIC tunnels from agents and proxies HTTP requests
// from mobile clients through those tunnels.
type RelayServer struct {
	quicPort int // QUIC port for agent tunnels
	httpPort int // HTTP port for mobile clients

	// password is protected by pwMu for runtime updates
	pwMu     sync.RWMutex
	password string // shared password for relay authentication (empty = no auth)

	// Convex backend URL for per-user password validation (empty = use shared password only)
	convexURL string

	// Cache of validated per-user passwords (password -> expiry time)
	validatedPwMu sync.RWMutex
	validatedPw   map[string]time.Time // password/access cache key -> cache expiry

	startedAt time.Time // server start time for uptime tracking

	// deviceID -> active agent tunnel
	mu      sync.RWMutex
	tunnels map[string]*agentTunnel

	// Bandwidth management
	bandwidth *BandwidthManager

	// Subdomain expose routing
	exposeMu     sync.RWMutex
	exposeRoutes map[string]*exposeRoute // subdomain -> route
	exposeDomain string                  // base domain (e.g. "yaver.io")

	// Per-user bus fanout (see bus.go). Events published by any
	// agent under userId X are dispatched to every other subscriber
	// under the same userId. Namespaced per-user — NEVER crosses
	// user boundaries.
	busHub      *busHub
	pwUserIDMu  sync.RWMutex
	pwUserIDs   map[string]string // password -> userId (short cache)
	pwUserIDExp map[string]time.Time

	// Yaver Mesh DERP relay — persistent per-device frame streams that forward
	// WireGuard packets between peers that can't reach each other directly
	// (symmetric NAT). Pass-through: the relay never decrypts payloads.
	meshMu      sync.RWMutex
	meshStreams map[string]*meshStreamHandle // deviceId -> its mesh frame stream

	// adminToken gates /admin/* and the auth-required diagnostic
	// endpoints (/tunnels, /presence, /admin/bandwidth, /admin/status).
	// Read from RELAY_ADMIN_TOKEN at process start. Empty disables the
	// admin-token path; password / Convex auth still applies. C-9 + H-14.
	adminToken string

	// abuseGuard enforces coarse public-relay protections: per-IP request
	// buckets, registration throttles, global HTTP concurrency, and
	// per-device active stream caps. Defaults are generous and configurable
	// with RELAY_* env vars so existing clients keep working.
	abuseGuard *abuseGuard
	sigNonces  *sigNonceCache
}

type exposeRoute struct {
	deviceID  string
	port      int
	createdAt time.Time
}

type agentTunnel struct {
	deviceID string
	conn     quic.Connection
	peerAddr string // observed public address
	connAt   time.Time
}

func NewRelayServer(quicPort, httpPort int, password, convexURL, exposeDomain string) *RelayServer {
	s := &RelayServer{
		quicPort:     quicPort,
		httpPort:     httpPort,
		password:     password,
		convexURL:    convexURL,
		validatedPw:  make(map[string]time.Time),
		startedAt:    time.Now(),
		tunnels:      make(map[string]*agentTunnel),
		meshStreams:  make(map[string]*meshStreamHandle),
		exposeRoutes: make(map[string]*exposeRoute),
		exposeDomain: exposeDomain,
		busHub:       newBusHub(),
		pwUserIDs:    make(map[string]string),
		pwUserIDExp:  make(map[string]time.Time),
		// RELAY_ADMIN_TOKEN gates /admin/* + diagnostic endpoints
		// regardless of relay password. Empty = no admin-token path
		// available (callers must use the relay password instead).
		adminToken: strings.TrimSpace(os.Getenv("RELAY_ADMIN_TOKEN")),
		abuseGuard: newAbuseGuard(abuseGuardConfigFromEnv()),
		sigNonces:  newSigNonceCache(),
	}
	// Initialize bandwidth manager
	dataDir := os.Getenv("RELAY_DATA_DIR")
	if dataDir == "" {
		dataDir = "/var/lib/yaver-relay"
		if home, err := os.UserHomeDir(); err == nil {
			dataDir = filepath.Join(home, ".yaver-relay")
		}
	}
	s.bandwidth = NewBandwidthManager(nil, dataDir)

	// Log bandwidth stats every 5 minutes
	go func() {
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()
		for range ticker.C {
			s.bandwidth.LogUsage()
		}
	}()

	return s
}

// getPassword returns the current relay password (thread-safe).
func (s *RelayServer) getPassword() string {
	s.pwMu.RLock()
	defer s.pwMu.RUnlock()
	return s.password
}

// setPassword updates the relay password in memory (thread-safe).
//
// M-9 (audit 2026-05-02): on rotation, invalidate the per-password
// cache (validatedPw + pwUserIDs) and force-disconnect every existing
// tunnel. Without this, any peer holding the OLD password retains
// validated-cache hits for up to 5 minutes after rotation, and any
// already-registered agent keeps its tunnel forever — defeating the
// point of "rotate the relay password to evict a compromised peer".
func (s *RelayServer) setPassword(pw string) {
	s.pwMu.Lock()
	s.password = pw
	s.pwMu.Unlock()

	// Drop every cached "yes, this password is OK" entry. Some of those
	// were validated against a Convex per-user record and are still
	// good (Convex does its own per-user rotation), but we'd rather
	// pay one extra round-trip per active client than risk leaving a
	// stale shared-password hit.
	s.validatedPwMu.Lock()
	s.validatedPw = make(map[string]time.Time)
	s.validatedPwMu.Unlock()

	s.pwUserIDMu.Lock()
	s.pwUserIDs = make(map[string]string)
	s.pwUserIDExp = make(map[string]time.Time)
	s.pwUserIDMu.Unlock()

	// Snapshot and close all tunnels under the lock — an agent
	// holding the old password will reconnect via the normal backoff
	// path in relay/tunnel.go and re-handshake against the new
	// password. Tunnel cleanup happens in handleAgentConnection's
	// <-conn.Context().Done() path.
	s.mu.Lock()
	conns := make([]quic.Connection, 0, len(s.tunnels))
	for _, t := range s.tunnels {
		conns = append(conns, t.conn)
	}
	s.mu.Unlock()
	for _, c := range conns {
		c.CloseWithError(0, "password rotated")
	}
}

// authorizeAdmin enforces auth for the diagnostic + admin endpoints
// (/tunnels, /presence, /admin/*). Accepts either:
//
//   - Authorization: Bearer <RELAY_ADMIN_TOKEN>  (preferred)
//   - X-Relay-Password: <relay password>          (compat with existing dashboards)
//
// On success returns true; on failure writes a 401 and returns false.
//
// H-14 / C-9 (audit 2026-05-02): pre-fix, every one of these endpoints
// returned without auth, allowing the public to enumerate connected
// devices, peer IPs, expose routes, bandwidth-per-device, and
// "is a password configured?" reconnaissance.
func (s *RelayServer) authorizeAdmin(w http.ResponseWriter, r *http.Request) bool {
	// 1. Admin token via Authorization: Bearer header.
	if s.adminToken != "" {
		hdr := r.Header.Get("Authorization")
		if strings.HasPrefix(hdr, "Bearer ") {
			tok := strings.TrimPrefix(hdr, "Bearer ")
			if secretEqual(tok, s.adminToken) {
				return true
			}
		}
	}

	// 2. Relay password via X-Relay-Password header (or per-user via Convex).
	//    We DON'T accept the ?__rp= query fallback here on purpose —
	//    these are admin-tier endpoints, not iframe-served content.
	relayPw := r.Header.Get("X-Relay-Password")
	if relayPw != "" && s.validatePassword(relayPw) {
		return true
	}

	// Throttle invalid admin-auth attempts (relay security audit, finding #4).
	if !s.abuseGuard.allowInvalidAuth(s.abuseGuard.clientIP(r)) {
		writeRelayError(w, http.StatusTooManyRequests, "too many invalid admin auth attempts")
		return false
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"error": "unauthorized: provide Authorization: Bearer <RELAY_ADMIN_TOKEN> or X-Relay-Password",
	})
	return false
}

// secretEqual is a constant-time string comparison for secrets (passwords,
// admin tokens) so a remote attacker can't recover them a byte at a time via
// response-timing (relay security audit, finding #5).
func secretEqual(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

// validatePassword checks a password against the shared password or Convex backend.
// Returns true if the password is valid. This is intentionally action-less and
// used for admin/bus compatibility. Device tunnel registration and /d/ proxy
// traffic use validateRelayAccess so the official free relay can enforce
// account/device ownership in Convex.
func (s *RelayServer) validatePassword(pw string) bool {
	// 1. Check shared password (self-hosted mode)
	if sharedPw := s.getPassword(); sharedPw != "" && secretEqual(pw, sharedPw) {
		return true
	}

	// 2. If no Convex URL configured and no shared password, allow all
	if s.convexURL == "" && s.getPassword() == "" {
		return true
	}

	// 3. If no Convex URL configured but shared password didn't match, reject
	if s.convexURL == "" {
		return false
	}

	// 4. Check cache first
	s.validatedPwMu.RLock()
	if expiry, ok := s.validatedPw[pw]; ok && time.Now().Before(expiry) {
		s.validatedPwMu.RUnlock()
		return true
	}
	s.validatedPwMu.RUnlock()

	// 5. Validate against Convex backend
	ok := s.validatePasswordViaConvex(pw)
	if ok {
		s.validatedPwMu.Lock()
		s.validatedPw[pw] = time.Now().Add(5 * time.Minute)
		s.validatedPwMu.Unlock()
	}
	return ok
}

// validatePasswordViaConvex calls the Convex backend to check a per-user relay password.
func (s *RelayServer) validatePasswordViaConvex(pw string) bool {
	_, ok := s.validateAndResolveViaConvex(pw, "", "", "")
	return ok
}

func (s *RelayServer) validateRelayAccess(pw, action, deviceID, token string) (string, bool) {
	pw = strings.TrimSpace(pw)
	action = strings.TrimSpace(action)
	deviceID = strings.TrimSpace(deviceID)
	token = strings.TrimSpace(token)
	if pw == "" {
		return "", false
	}

	// Self-hosted shared-password mode remains supported. The official free
	// relay sets CONVEX_URL, so register/proxy authorization goes through the
	// backend ownership/quota path instead of accepting a universal shared key.
	if s.convexURL == "" {
		if sharedPw := s.getPassword(); sharedPw != "" {
			return "", secretEqual(pw, sharedPw)
		}
		return "", true
	}

	cacheKey := strings.Join([]string{"access", action, deviceID, pw, token}, "\x00")
	s.validatedPwMu.RLock()
	if expiry, ok := s.validatedPw[cacheKey]; ok && time.Now().Before(expiry) {
		s.validatedPwMu.RUnlock()
		return s.resolveUserIDFromPassword(pw), true
	}
	s.validatedPwMu.RUnlock()

	userID, ok := s.validateAndResolveViaConvex(pw, action, deviceID, token)
	if ok {
		s.validatedPwMu.Lock()
		s.validatedPw[cacheKey] = time.Now().Add(5 * time.Minute)
		s.validatedPwMu.Unlock()
		if userID != "" {
			s.pwUserIDMu.Lock()
			s.pwUserIDs[pw] = userID
			s.pwUserIDExp[pw] = time.Now().Add(5 * time.Minute)
			s.pwUserIDMu.Unlock()
		}
	}
	return userID, ok
}

// validateAndResolveViaConvex returns both the validity and the
// resolved userId. Same 5-minute cache as validatePassword. Used by
// the bus to scope fanout per-user without a second Convex round-trip.
func (s *RelayServer) validateAndResolveViaConvex(pw, action, deviceID, token string) (string, bool) {
	url := strings.TrimRight(s.convexURL, "/") + "/relay/validate"
	payload := map[string]string{"password": pw}
	if strings.TrimSpace(action) != "" {
		payload["action"] = strings.TrimSpace(action)
	}
	if strings.TrimSpace(deviceID) != "" {
		payload["deviceId"] = strings.TrimSpace(deviceID)
	}
	if strings.TrimSpace(token) != "" {
		payload["token"] = strings.TrimSpace(token)
	}
	body, _ := json.Marshal(payload)
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Post(url, "application/json", strings.NewReader(string(body)))
	if err != nil {
		log.Printf("[RELAY] Convex validation error: %v", err)
		return "", false
	}
	defer resp.Body.Close()

	var result struct {
		OK     bool   `json:"ok"`
		UserID string `json:"userId"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		log.Printf("[RELAY] Convex validation parse error: %v", err)
		return "", false
	}
	return result.UserID, result.OK
}

// resolveSigViaConvex fetches the SIGNER device's ed25519 signing public key
// and confirms its owner also owns the TARGET device. Returns
// (userId, signerPubKeyBase64, ok). The relay holds no secret — it receives
// only public material and verifies the signature itself (verifyDeviceSig).
func (s *RelayServer) resolveSigViaConvex(signerDeviceID, targetDeviceID string) (string, string, bool) {
	if s.convexURL == "" {
		return "", "", false
	}
	url := strings.TrimRight(s.convexURL, "/") + "/relay/resolve-sig"
	body, _ := json.Marshal(map[string]string{
		"signerDeviceId": signerDeviceID,
		"targetDeviceId": targetDeviceID,
	})
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Post(url, "application/json", strings.NewReader(string(body)))
	if err != nil {
		return "", "", false
	}
	defer resp.Body.Close()
	var result struct {
		OK              bool   `json:"ok"`
		UserID          string `json:"userId"`
		SignerPublicKey string `json:"signerPublicKey"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", "", false
	}
	return result.UserID, result.SignerPublicKey, result.OK
}

// authorizeProxyViaSig tries the asymmetric per-device signature path for a
// /d/<targetDeviceID>/ proxy request. On success it returns the resolved userId
// and true; on ANY failure it returns false so the caller falls back to the
// password path — this can never lock out a client that hasn't migrated. It
// buffers the request body (only when a signature is present) so it can hash it
// for verification AND still forward it downstream.
func (s *RelayServer) authorizeProxyViaSig(r *http.Request, targetDeviceID string) (string, bool) {
	signerDeviceID := strings.TrimSpace(r.Header.Get("X-Yaver-Device"))
	if signerDeviceID == "" {
		return "", false
	}
	var body []byte
	if r.Body != nil {
		b, err := io.ReadAll(io.LimitReader(r.Body, s.abuseGuard.cfg.MaxRequestBodyBytes))
		if err != nil {
			return "", false
		}
		body = b
		r.Body = io.NopCloser(bytes.NewReader(body)) // let the downstream proxy re-read it
	}
	userID, pubB64, ok := s.resolveSigViaConvex(signerDeviceID, targetDeviceID)
	if !ok {
		return "", false
	}
	pub := decodeSignPubKey(pubB64)
	if pub == nil {
		return "", false
	}
	signed, ok := verifyDeviceSig(r, body, pub, s.sigNonces)
	if !ok || !sigDeviceMatches(signed, signerDeviceID) {
		return "", false
	}
	return userID, true
}

// resolveUserIDFromPassword is the cache-aware variant used by bus
// handlers. Returns "" when Convex is not configured or the password
// doesn't map to a user (shared-password mode).
func (s *RelayServer) resolveUserIDFromPassword(pw string) string {
	if pw == "" || s.convexURL == "" {
		return ""
	}
	s.pwUserIDMu.RLock()
	if uid, ok := s.pwUserIDs[pw]; ok {
		if exp, hasExp := s.pwUserIDExp[pw]; hasExp && time.Now().Before(exp) {
			s.pwUserIDMu.RUnlock()
			return uid
		}
	}
	s.pwUserIDMu.RUnlock()

	uid, ok := s.validateAndResolveViaConvex(pw, "", "", "")
	if !ok || uid == "" {
		return ""
	}
	s.pwUserIDMu.Lock()
	s.pwUserIDs[pw] = uid
	s.pwUserIDExp[pw] = time.Now().Add(5 * time.Minute)
	s.pwUserIDMu.Unlock()
	return uid
}

// Start runs both the QUIC tunnel listener and the HTTP proxy.
func (s *RelayServer) Start(ctx context.Context) error {
	errCh := make(chan error, 2)

	go func() { errCh <- s.runQUICListener(ctx) }()
	go func() { errCh <- s.runHTTPProxy(ctx) }()

	// Log connected tunnels periodically
	go s.logTunnels(ctx)

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		return nil
	}
}

// --- QUIC Tunnel Listener (agents connect here) ---

func (s *RelayServer) runQUICListener(ctx context.Context) error {
	tlsCfg, err := generateRelayTLS()
	if err != nil {
		return fmt.Errorf("TLS setup: %w", err)
	}

	addr := fmt.Sprintf("0.0.0.0:%d", s.quicPort)
	udpAddr, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		return fmt.Errorf("resolve: %w", err)
	}

	conn, err := net.ListenUDP("udp", udpAddr)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}

	tr := &quic.Transport{Conn: conn}
	listener, err := tr.Listen(tlsCfg, &quic.Config{
		MaxIdleTimeout:  120 * time.Second,
		KeepAlivePeriod: 20 * time.Second,
	})
	if err != nil {
		return fmt.Errorf("quic listen: %w", err)
	}
	defer listener.Close()

	log.Printf("[RELAY] QUIC tunnel listener on %s", addr)

	go func() {
		<-ctx.Done()
		listener.Close()
	}()

	for {
		session, err := listener.Accept(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			log.Printf("[RELAY] accept error: %v", err)
			continue
		}
		go s.handleAgentConnection(ctx, session)
	}
}

func (s *RelayServer) handleAgentConnection(ctx context.Context, conn quic.Connection) {
	remoteAddr := conn.RemoteAddr().String()
	log.Printf("[RELAY] Agent connected from %s", remoteAddr)

	if !s.abuseGuard.allowQUICRegister(remoteAddr) {
		conn.CloseWithError(1, "registration rate limited")
		return
	}

	// Wait for registration stream
	stream, err := conn.AcceptStream(ctx)
	if err != nil {
		log.Printf("[RELAY] accept registration stream from %s: %v", remoteAddr, err)
		conn.CloseWithError(1, "no registration")
		return
	}

	data, err := io.ReadAll(io.LimitReader(stream, 1<<16)) // 64KB limit
	if err != nil {
		log.Printf("[RELAY] read registration from %s: %v", remoteAddr, err)
		conn.CloseWithError(1, "read error")
		return
	}

	// rejectRegistration writes the error response, closes the stream,
	// and tears the connection down on a short delay so the response
	// has time to flush. Synchronous CloseWithError races with the
	// client's read on loopback (and on slow links) and surfaces as
	// an empty response on the agent side — the C-1 fix learned this
	// the hard way; same pattern applies to every register-time
	// rejection. M-6 / C-1.
	rejectRegistration := func(message, closeReason string) {
		resp, _ := json.Marshal(RegisterResp{Type: "error", OK: false, Message: message})
		stream.Write(resp)
		stream.Close()
		time.AfterFunc(100*time.Millisecond, func() {
			conn.CloseWithError(1, closeReason)
		})
	}

	var reg RegisterMsg
	if err := json.Unmarshal(data, &reg); err != nil || reg.Type != "register" {
		rejectRegistration("invalid registration", "bad registration")
		return
	}

	if reg.DeviceID == "" || reg.Token == "" {
		rejectRegistration("deviceId and token required", "missing fields")
		return
	}

	// M-6 (audit 2026-05-02): enforce a strict shape on deviceId.
	// Pre-fix the relay accepted any non-empty string, which blew the
	// door open for path-traversal-style ids (`../foo`), shell-metachar
	// ids that would later become URL parts, and zero-padded short ids
	// that collided easily under the 8-char prefix-match in handleProxy.
	if !deviceIDShapePattern.MatchString(reg.DeviceID) {
		rejectRegistration("invalid deviceId shape", "invalid deviceId shape")
		return
	}

	// M-6: refuse deviceIds that would auto-claim a reserved subdomain
	// (admin/api/www/...). Without this, the auto-provision code below
	// happily wired admin.<exposeDomain> to whoever connected first.
	// Skip the check when no expose-domain is configured (self-hosted
	// relay without wildcard DNS); in that mode no auto-subdomain is
	// ever provisioned, so the reservation is moot.
	if s.exposeDomain != "" {
		if reservedSubdomains[strings.ToLower(reg.DeviceID)] {
			rejectRegistration("deviceId reserved", "deviceId reserved")
			return
		}
	}

	// Validate relay password for this registration. On the official free
	// relay this proves the password belongs to the same signed-in user as
	// the agent token and, when the device row already exists, that the user
	// owns the deviceId being registered.
	if _, ok := s.validateRelayAccess(reg.Password, "register", reg.DeviceID, reg.Token); !ok {
		if !s.abuseGuard.allowInvalidAuth(remoteAddr) {
			rejectRegistration("too many invalid relay password attempts", "invalid password rate limited")
			return
		}
		rejectRegistration("invalid relay password", "invalid relay password")
		return
	}

	// C-1 (audit 2026-05-02): refuse-on-collision instead of replace-on-collision.
	//
	// The previous behavior unconditionally swapped the tunnel for any
	// connecting client that presented a valid relay password. Since the
	// shared-password model treats every authenticated peer as
	// indistinguishable, that gave anyone holding the password the ability
	// to take over any other device's tunnel just by sending its DeviceID
	// — full mobile-client traffic redirection (auth headers, /vault, etc.).
	//
	// The legitimate "agent reconnects after a process restart" path is
	// preserved by QUIC keepalive: MaxIdleTimeout=120s drops the dead
	// tunnel, after which the new registration succeeds. Crashed agents
	// just retry with their normal exponential backoff (relay/tunnel.go),
	// so this change costs at most ~2 minutes of reconnect latency for an
	// uncleanly-killed agent — and in exchange closes the hijack vector.
	//
	// Reject with Type:"error" — relay/tunnel.go::register() already
	// surfaces RegisterResp.OK==false as "registration rejected" and the
	// client retries on backoff.
	s.mu.Lock()
	if existing, exists := s.tunnels[reg.DeviceID]; exists {
		// Check if the existing tunnel's QUIC connection is actually
		// still alive. A dead conn whose <-Done() goroutine just hasn't
		// finished cleanup yet should not block a legitimate reconnect.
		select {
		case <-existing.conn.Context().Done():
			// Previous tunnel is dead but cleanup hasn't run yet —
			// remove it now so this registration can take its place.
			delete(s.tunnels, reg.DeviceID)
		default:
			s.mu.Unlock()
			log.Printf("[RELAY] Refusing duplicate registration for device %s (existing tunnel from %s, new from %s)",
				reg.DeviceID[:min(8, len(reg.DeviceID))], existing.peerAddr, remoteAddr)
			rejectRegistration("deviceId already registered", "deviceId already registered")
			return
		}
	}

	tunnel := &agentTunnel{
		deviceID: reg.DeviceID,
		conn:     conn,
		peerAddr: remoteAddr,
		connAt:   time.Now(),
	}
	s.tunnels[reg.DeviceID] = tunnel
	s.mu.Unlock()

	// Auto-provision a `<deviceId>.<exposeDomain>` subdomain for
	// every connected tunnel. This gives every device a clean
	// HTTPS-direct origin (e.g. https://abc1234.dev.yaver.io)
	// that the dashboard / mobile app can hit without going
	// through the /d/<id>/ path or hitting mixed-content blocks
	// on direct LAN probes. Wildcard cert covers all subdomains;
	// no per-box certbot.
	//
	// Idempotent: replacing a tunnel for the same deviceId also
	// replaces the route. Cleared in the deferred handler below
	// when the tunnel goes away. Skip when expose-domain isn't
	// configured (self-hosted relay without a wildcard DNS).
	autoSub := ""
	if s.exposeDomain != "" {
		autoSub = strings.ToLower(reg.DeviceID)
		s.exposeMu.Lock()
		s.exposeRoutes[autoSub] = &exposeRoute{
			deviceID: reg.DeviceID,
			port:     18080,
		}
		s.exposeMu.Unlock()
		log.Printf("[RELAY] auto-registered https://%s.%s for device %s",
			autoSub, s.exposeDomain, reg.DeviceID[:8])
	}

	// Send success — include the auto-provisioned subdomain URL
	// so the agent can publish it as publicUrl in its heartbeat.
	respMsg := RegisterResp{Type: "registered", OK: true}
	if autoSub != "" {
		respMsg.AssignedURL = "https://" + autoSub + "." + s.exposeDomain
	}
	resp, _ := json.Marshal(respMsg)
	stream.Write(resp)
	stream.Close()

	log.Printf("[RELAY] Device %s registered from %s", reg.DeviceID[:8], remoteAddr)

	// Best-effort push to Convex so mobile/web pick up tunnel-up
	// within the Convex reactive latency window instead of polling
	// /presence every 30s. Includes AssignedURL when we just auto-
	// provisioned one — Convex stores it under device.publicEndpoints
	// so the dashboard's transport classifier picks it up instantly.
	// No-op unless CONVEX_PRESENCE_URL + CONVEX_PRESENCE_SECRET env
	// vars are set. See convex_presence.go.
	assignedFullURL := ""
	if autoSub != "" {
		assignedFullURL = "https://" + autoSub + "." + s.exposeDomain
	}
	pushPresence(presencePayload{
		DeviceID:    reg.DeviceID,
		Online:      true,
		PeerAddr:    remoteAddr,
		ConnectedAt: tunnel.connAt.UnixMilli(),
		AssignedURL: assignedFullURL,
	})

	// Accept control streams (expose register/unregister) from agent
	go s.handleAgentControlStreams(conn, reg.DeviceID)

	// Keep connection alive — block until it dies
	<-conn.Context().Done()

	s.mu.Lock()
	if cur, ok := s.tunnels[reg.DeviceID]; ok && cur.conn == conn {
		delete(s.tunnels, reg.DeviceID)
	}
	s.mu.Unlock()
	s.dropMeshStream(reg.DeviceID)

	// Mirror the disconnect to Convex. duration lets the reactive UI
	// show "last seen X ago" without waiting for the next heartbeat.
	pushPresence(presencePayload{
		DeviceID:    reg.DeviceID,
		Online:      false,
		ConnectedAt: tunnel.connAt.UnixMilli(),
		DurationSec: int(time.Since(tunnel.connAt).Seconds()),
	})

	// Clean up expose routes for this device
	s.exposeMu.Lock()
	for sub, route := range s.exposeRoutes {
		if route.deviceID == reg.DeviceID {
			delete(s.exposeRoutes, sub)
			log.Printf("[EXPOSE] Removed %s.%s (device disconnected)", sub, s.exposeDomain)
		}
	}
	s.exposeMu.Unlock()

	log.Printf("[RELAY] Device %s disconnected (%s)", reg.DeviceID[:8], remoteAddr)
}

// --- HTTP Proxy (mobile clients connect here) ---

func (s *RelayServer) runHTTPProxy(ctx context.Context) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", s.handleHealth)
	mux.HandleFunc("/tunnels", s.handleListTunnels)
	mux.HandleFunc("/presence", s.handlePresence)
	mux.HandleFunc("/admin/set-password", s.handleSetPassword)
	mux.HandleFunc("/admin/status", s.handleAdminStatus)
	mux.HandleFunc("/admin/bandwidth", s.handleBandwidthStats)
	// P2P bus — per-user fanout (see relay/bus.go). Not a broker;
	// relay holds no topic state, just forwards events.
	mux.HandleFunc("/bus/publish", s.handleBusPublish)
	mux.HandleFunc("/bus/subscribe", s.handleBusSubscribe)
	mux.HandleFunc("/bus/status", s.handleBusStatus)
	mux.HandleFunc("/d/", s.handleProxy) // /d/{deviceId}/...

	srv := &http.Server{
		Addr: fmt.Sprintf("0.0.0.0:%d", s.httpPort),
		Handler: withRelayCORS(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Check for subdomain-based expose routing first
			if s.exposeDomain != "" && s.tryExposeProxy(w, r) {
				return
			}
			// Fall through to normal mux routing
			mux.ServeHTTP(w, r)
		})),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	srv.Handler = s.abuseGuard.httpMiddleware(srv.Handler)

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(shutdownCtx)
	}()

	log.Printf("[RELAY] HTTP proxy on 0.0.0.0:%d", s.httpPort)
	err := srv.ListenAndServe()
	if err == http.ErrServerClosed {
		return nil
	}
	return err
}

// handleHealth returns a slim, public liveness probe.
//
// H-14 (audit 2026-05-02): pre-fix, /health returned tunnel count,
// activeDevices, load percent, and bandwidth stats — usable for
// public reconnaissance ("is the relay loaded? are there many users?").
// All counts are now behind admin auth; /health stays public so load
// balancers and uptime monitors can reach it without credentials.
func (s *RelayServer) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"ok":      true,
		"version": version,
	})
}

func (s *RelayServer) handleListTunnels(w http.ResponseWriter, r *http.Request) {
	// H-14: enumerating connected devices + peer addresses is a
	// reconnaissance vector; gate behind admin auth.
	if !s.authorizeAdmin(w, r) {
		return
	}
	s.mu.RLock()
	list := make([]map[string]interface{}, 0, len(s.tunnels))
	for _, t := range s.tunnels {
		id := t.deviceID
		if len(id) > 8 {
			id = id[:8] + "..."
		}
		list = append(list, map[string]interface{}{
			"deviceId":    id,
			"peerAddr":    t.peerAddr,
			"connectedAt": t.connAt.Format(time.RFC3339),
			"uptime":      time.Since(t.connAt).Round(time.Second).String(),
		})
	}
	s.mu.RUnlock()

	s.exposeMu.RLock()
	exposeList := make([]map[string]interface{}, 0, len(s.exposeRoutes))
	for sub, route := range s.exposeRoutes {
		deviceID := route.deviceID
		if len(deviceID) > 8 {
			deviceID = deviceID[:8] + "..."
		}
		publicURL := ""
		if s.exposeDomain != "" {
			publicURL = fmt.Sprintf("https://%s.%s", sub, s.exposeDomain)
		}
		exposeList = append(exposeList, map[string]interface{}{
			"subdomain": sub,
			"deviceId":  deviceID,
			"port":      route.port,
			"publicUrl": publicURL,
			"createdAt": route.createdAt.Format(time.RFC3339),
		})
	}
	s.exposeMu.RUnlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"ok":           true,
		"tunnels":      list,
		"exposeRoutes": exposeList,
	})
}

// handlePresence gives clients a real-time answer to "is this device connected
// to the relay right now?" without depending on Convex heartbeat lag (30-90 s).
// Supports two shapes:
//
//	GET /presence?id=<deviceId>            -> single {deviceId, online, since}
//	GET /presence?ids=a,b,c,...            -> map keyed by deviceId
//
// Unknown deviceIds return online:false (indistinguishable from "exists but
// offline"), so an adversary can't enumerate our tunnel table.
// Response bodies are small; no auth required because no sensitive data
// leaves the relay — only the caller's own deviceId yields a real signal.
// handlePresence returns the tunnel-online state for one or more deviceIds.
//
// H-14 (audit 2026-05-02): now requires admin auth. Pre-fix it ran
// unauth, allowing arbitrary callers to enumerate "is this deviceId
// currently connected?" against the public relay — useful for traffic-
// analysis correlation and for confirming a guess at a target
// deviceId before pivoting via C-1's hijack.
//
// Also caps the comma-separated `ids` list at 50 entries to bound the
// per-request work and prevent /presence from being abused as a tunnel-
// table dump via massively-batched queries.
func (s *RelayServer) handlePresence(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	if !s.authorizeAdmin(w, r) {
		return
	}
	w.Header().Set("Content-Type", "application/json")
	s.mu.RLock()
	defer s.mu.RUnlock()

	lookup := func(id string) map[string]interface{} {
		now := time.Now()
		if t, ok := s.tunnels[id]; ok {
			return map[string]interface{}{
				"deviceId":  id,
				"online":    true,
				"since":     t.connAt.UTC().Format(time.RFC3339),
				"uptimeSec": int(now.Sub(t.connAt).Seconds()),
			}
		}
		return map[string]interface{}{
			"deviceId": id,
			"online":   false,
		}
	}

	if ids := r.URL.Query().Get("ids"); ids != "" {
		raws := strings.Split(ids, ",")
		// H-14: cap the batch size. Returning 400 (not just truncating)
		// makes the limit visible to clients so they batch correctly
		// instead of silently dropping queries.
		if len(raws) > 50 {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"error": "ids list capped at 50 entries per request",
			})
			return
		}
		out := map[string]interface{}{}
		for _, raw := range raws {
			id := strings.TrimSpace(raw)
			if id == "" {
				continue
			}
			out[id] = lookup(id)
		}
		json.NewEncoder(w).Encode(map[string]interface{}{
			"ok":      true,
			"devices": out,
		})
		return
	}

	id := strings.TrimSpace(r.URL.Query().Get("id"))
	if id == "" {
		http.Error(w, `{"error":"id or ids query param required"}`, http.StatusBadRequest)
		return
	}
	json.NewEncoder(w).Encode(map[string]interface{}{
		"ok":     true,
		"device": lookup(id),
	})
}

// handleSetPassword allows runtime password changes via POST /admin/set-password.
//
// C-9 (audit 2026-05-02): when RELAY_ADMIN_TOKEN is set, every call must
// carry that token regardless of whether a password is currently
// configured. Pre-fix, a relay launched without an initial password
// allowed any internet caller to do "first write wins" and seize
// permanent admin control.
func (s *RelayServer) handleSetPassword(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	// If an admin token is configured, it MUST be present. The admin
	// token gate is unconditional: it doesn't matter whether a password
	// is currently set.
	if s.adminToken != "" {
		hdr := r.Header.Get("Authorization")
		if !strings.HasPrefix(hdr, "Bearer ") || !secretEqual(strings.TrimPrefix(hdr, "Bearer "), s.adminToken) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"error": "unauthorized: Authorization: Bearer <RELAY_ADMIN_TOKEN> required",
			})
			return
		}
	}

	var req struct {
		Password        string `json:"password"`
		CurrentPassword string `json:"current_password"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&req); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error": "invalid request body",
		})
		return
	}

	if req.Password == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error": "password is required",
		})
		return
	}

	// If a password is currently set, require current_password to match.
	// (When admin token also gates this endpoint, this is belt-and-
	// suspenders; we keep it for the path where adminToken is empty.)
	if currentPw := s.getPassword(); currentPw != "" {
		if !secretEqual(req.CurrentPassword, currentPw) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"error": "invalid current password",
			})
			return
		}
	} else if s.adminToken == "" {
		// No password set AND no admin token — refuse rather than allow
		// "first write wins". An operator who legitimately needs to set
		// the very first password should configure RELAY_ADMIN_TOKEN
		// before exposing the relay, or set the password via the env
		// var before startup.
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error": "no password is set; configure RELAY_ADMIN_TOKEN to allow setting the initial password via API, or set RELAY_PASSWORD before startup",
		})
		return
	}

	// Update password in memory
	s.setPassword(req.Password)

	// Persist to .relay-password file
	if err := os.WriteFile(".relay-password", []byte(req.Password), 0600); err != nil {
		log.Printf("[RELAY] Warning: could not write .relay-password file: %v", err)
	}

	log.Printf("[RELAY] Password updated via API")

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"ok":      true,
		"message": "Password updated",
	})
}

// handleAdminStatus returns relay status info via GET /admin/status.
//
// H-14 (audit 2026-05-02): "is a password set?" + tunnel count + uptime
// is reconnaissance for an attacker probing whether the relay is
// reachable in open mode (C-9). Auth-gated.
func (s *RelayServer) handleAdminStatus(w http.ResponseWriter, r *http.Request) {
	if !s.authorizeAdmin(w, r) {
		return
	}

	s.mu.RLock()
	tunnelCount := len(s.tunnels)
	s.mu.RUnlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"ok":           true,
		"password_set": s.getPassword() != "",
		"tunnels":      tunnelCount,
		"uptime":       time.Since(s.startedAt).Round(time.Second).String(),
	})
}

// handleProxy proxies HTTP requests to agents via QUIC tunnel.
// URL format: /d/{deviceId}/... -> forwarded as /... to the agent
func (s *RelayServer) handleProxy(w http.ResponseWriter, r *http.Request) {
	// Parse: /d/{deviceId}/rest/of/path
	path := strings.TrimPrefix(r.URL.Path, "/d/")
	parts := strings.SplitN(path, "/", 2)
	if len(parts) == 0 || parts[0] == "" {
		http.Error(w, `{"ok":false,"error":"device ID required in path: /d/{deviceId}/..."}`, http.StatusBadRequest)
		return
	}

	deviceID := parts[0]
	forwardPath := "/"
	if len(parts) > 1 {
		forwardPath = "/" + parts[1]
	}

	// Validate relay password (shared or per-user via Convex).
	// Iframes can't set custom headers, so accept `?__rp=<password>` as a
	// fallback for the web dashboard's dev-server preview. yaver.io is
	// HTTPS end-to-end so the URL is TLS-protected in transit, and the
	// relay password is a per-user shared secret — not a user credential.
	relayPw := r.Header.Get("X-Relay-Password")
	if relayPw == "" {
		relayPw = r.URL.Query().Get("__rp")
	}
	// Asymmetric per-device signature path (preferred; no shared secret in the
	// URL/logs). It is tried first and, on any failure, falls through to the
	// password path — so a client that hasn't migrated is never locked out.
	var userID string
	var authed bool
	if hasDeviceSig(r) {
		if uid, sigOK := s.authorizeProxyViaSig(r, deviceID); sigOK {
			userID, authed = uid, true
		}
	}
	if !authed {
		uid, ok := s.validateRelayAccess(relayPw, "proxy", deviceID, "")
		if !ok {
			// Throttle invalid-auth attempts so the account-wide relay password
			// isn't brute-forcible over HTTP (relay security audit, finding #4).
			// Keyed on the real client IP (trusted-proxy-aware clientIP).
			if !s.abuseGuard.allowInvalidAuth(s.abuseGuard.clientIP(r)) {
				writeRelayError(w, http.StatusTooManyRequests, "too many invalid relay password attempts")
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"error": "invalid relay password",
			})
			return
		}
		userID = uid
	}
	if userID != "" && !s.abuseGuard.allow("proxy-user:"+userID, s.abuseGuard.cfg.ProxyPerUserPerMin, s.abuseGuard.cfg.ProxyBurstPerUser) {
		s.abuseGuard.logLimited("proxy-user", userID)
		writeRelayError(w, http.StatusTooManyRequests, "free relay user rate limit exceeded")
		return
	}

	// Don't leak the password into the agent-side query string.
	// Also promote ?token=<jwt> to Authorization: Bearer <jwt> so
	// EventSource clients (which can't set custom headers) work
	// through the relay. The agent's auth middleware already does
	// the same promotion when the request hits it locally, but
	// going via the tunnel the request body is reconstructed by
	// the agent's tunnel-client and r.URL.Query() returns empty
	// for a reason I haven't pinned down — dropping events: 0
	// for every dashboard SSE subscription. Promoting at the
	// relay layer is robust and matches what nginx/Cloudflare do
	// for similar header-stripping scenarios.
	forwardQuery := r.URL.RawQuery
	tokenInQuery := r.URL.Query().Get("token")
	if strings.Contains(forwardQuery, "__rp=") || tokenInQuery != "" {
		q := r.URL.Query()
		q.Del("__rp")
		forwardQuery = q.Encode()
	}
	// If the caller passed ?token= and didn't already set an
	// Authorization header, promote it. The relay never strips
	// `token=` from the query because some agent endpoints look
	// at it (and removing might break those), but we DO inject
	// the header so the agent's auth fast path succeeds.
	if tokenInQuery != "" && r.Header.Get("Authorization") == "" {
		r.Header.Set("Authorization", "Bearer "+tokenInQuery)
	}

	bytesRequested := r.ContentLength
	if bytesRequested < 0 {
		bytesRequested = 0
	}

	// Check bandwidth limit
	if err := s.bandwidth.CheckAllowed(deviceID, bytesRequested); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error": err.Error(),
		})
		return
	}

	// Find the tunnel
	s.mu.RLock()
	tunnel, ok := s.tunnels[deviceID]
	s.mu.RUnlock()

	// Try prefix match if exact match fails (mobile might send short ID)
	if !ok && len(deviceID) >= 8 {
		s.mu.RLock()
		for id, t := range s.tunnels {
			if strings.HasPrefix(id, deviceID) {
				tunnel = t
				ok = true
				break
			}
		}
		s.mu.RUnlock()
	}

	if !ok {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadGateway)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"ok":    false,
			"error": "device not connected to relay",
		})
		return
	}

	if !s.abuseGuard.tryEnterDevice(tunnel.deviceID) {
		s.abuseGuard.logLimited("device-concurrency", tunnel.deviceID[:min(8, len(tunnel.deviceID))])
		writeRelayError(w, http.StatusTooManyRequests, "too many concurrent requests for device")
		return
	}
	defer s.abuseGuard.leaveDevice(tunnel.deviceID)

	// Read request body. 64 MiB cap is chosen to comfortably handle
	// static web bundles (Expo's main entry chunk is ~5–15 MB; the
	// JSON envelope is ~33 % bigger after base64). Bigger than this
	// now returns 413 instead of silently truncating; a future protocol
	// revision should stream large request bodies through QUIC instead
	// of buffering JSON.
	var body []byte
	if r.Body != nil {
		var ok bool
		body, ok = readCappedBody(w, r, s.abuseGuard.cfg.MaxRequestBodyBytes)
		if !ok {
			return
		}
	}

	// Build tunnel request
	headers := make(map[string]string)
	for k, v := range r.Header {
		if len(v) > 0 {
			headers[k] = v[0]
		}
	}

	tunnelReq := TunnelRequest{
		ID:      fmt.Sprintf("%d", time.Now().UnixNano()),
		Method:  r.Method,
		Path:    forwardPath,
		Query:   forwardQuery,
		Headers: headers,
		Body:    body,
	}

	// Open a QUIC stream to the agent
	streamCtx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	stream, err := tunnel.conn.OpenStreamSync(streamCtx)
	if err != nil {
		log.Printf("[RELAY] open stream to %s failed: %v", tunnel.deviceID[:8], err)

		// Clean up dead tunnel
		s.mu.Lock()
		if cur, exists := s.tunnels[tunnel.deviceID]; exists && cur.conn == tunnel.conn {
			delete(s.tunnels, tunnel.deviceID)
		}
		s.mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadGateway)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"ok":    false,
			"error": "agent tunnel broken, reconnecting...",
		})
		return
	}

	// Check if this is a WebSocket upgrade (Metro HMR, debugger)
	isWebSocket := strings.EqualFold(r.Header.Get("Upgrade"), "websocket")

	// Send request
	reqData, _ := json.Marshal(tunnelReq)
	if _, err := stream.Write(reqData); err != nil {
		log.Printf("[RELAY] write to %s failed: %v", tunnel.deviceID[:8], err)
		stream.Close()
		http.Error(w, "tunnel write error", http.StatusBadGateway)
		return
	}

	// WebSocket: keep stream open for bidirectional proxy
	if isWebSocket {
		s.proxyWebSocket(w, r, stream, tunnel.deviceID)
		return
	}

	stream.Close() // signal done writing (non-WS only)

	// Check if this is an SSE request. We use a hybrid detector:
	//   1. Accept: text/event-stream header — the canonical signal
	//      from any compliant SSE client (EventSource, fetch, curl
	//      with -H "Accept: text/event-stream").
	//   2. Path-suffix allowlist — for clients that forget Accept,
	//      and as a defense-in-depth catch.
	// KEEP THE PATH LIST IN SYNC with relay/tunnel.go:230 and
	// desktop/agent/main.go:7581. Hitting an SSE endpoint with
	// neither signal causes the relay to ReadAll the response
	// body, which never EOFs for SSE → hang until 10MB limit
	// (~30+ min), curl exits with status=000 / exit=28.
	isSSE := r.Method == "GET" &&
		(strings.Contains(r.Header.Get("Accept"), "text/event-stream") ||
			strings.Contains(forwardPath, "/output") ||
			strings.HasSuffix(forwardPath, "/dev/events") ||
			strings.HasSuffix(forwardPath, "/subscribe") ||
			strings.HasSuffix(forwardPath, "/blackbox/command-stream") ||
			strings.HasSuffix(forwardPath, "/blackbox/stream") ||
			strings.HasSuffix(forwardPath, "/feedback/stream") ||
			strings.Contains(forwardPath, "/streams/"))
	if isSSE {
		s.proxySSE(w, r, stream, tunnel.deviceID)
		return
	}

	// Peek the first byte to detect wire format:
	//   0xFE → new streaming wire (relay_stream_wire.go) — body streams
	//          chunk-by-chunk so iOS/browsers see bytes immediately and
	//          don't trigger Data stall on big (8 MB+) responses.
	//   '{'  → legacy JSON envelope (TunnelResponse). Old agents only
	//          know this shape; backwards compat keeps them working.
	var first [1]byte
	if _, err := io.ReadFull(stream, first[:]); err != nil {
		log.Printf("[RELAY] read first byte from %s failed: %v", tunnel.deviceID[:8], err)
		http.Error(w, "tunnel read error", http.StatusBadGateway)
		return
	}

	bytesIn := int64(len(reqData))
	if r.ContentLength > 0 {
		bytesIn += r.ContentLength
	}

	if first[0] == streamWireMagic {
		// New streaming wire format. Don't buffer; let the reader
		// flush chunks straight to the client.
		if err := readStreamingResponse(w, stream); err != nil {
			// Headers were already written by the time most errors
			// fire — log and bail, the client sees a truncated body.
			log.Printf("[RELAY] streaming response from %s failed: %v", tunnel.deviceID[:8], err)
		}
		// Bandwidth bookkeeping for streaming responses isn't byte-
		// exact (we'd need to wrap the io.Copy with a counter); for
		// now record the request side accurately and treat streaming
		// outbound as best-effort. The bandwidth dashboard already
		// flags very off-base numbers.
		s.bandwidth.RecordBytes(deviceID, bytesIn, 0, false)
		return
	}

	// Legacy JSON envelope path — re-prepend the byte we peeked,
	// then read the rest.
	rest, err := io.ReadAll(io.LimitReader(stream, 64<<20))
	if err != nil {
		log.Printf("[RELAY] read from %s failed: %v", tunnel.deviceID[:8], err)
		http.Error(w, "tunnel read error", http.StatusBadGateway)
		return
	}
	respData := append(first[:1:1], rest...)

	var tunnelResp TunnelResponse
	if err := json.Unmarshal(respData, &tunnelResp); err != nil {
		log.Printf("[RELAY] parse response from %s failed: %v", tunnel.deviceID[:8], err)
		http.Error(w, "tunnel response parse error", http.StatusBadGateway)
		return
	}

	// Write response headers
	for k, v := range tunnelResp.Headers {
		w.Header().Set(k, v)
	}
	w.WriteHeader(tunnelResp.StatusCode)
	w.Write(tunnelResp.Body)

	// Record bandwidth usage
	bytesOut := int64(len(tunnelResp.Body))
	s.bandwidth.RecordBytes(deviceID, bytesIn, bytesOut, false)
}

// proxySSE handles Server-Sent Events by streaming from the QUIC stream.
func (s *RelayServer) proxySSE(w http.ResponseWriter, r *http.Request, stream quic.Stream, deviceID string) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	// Tell intermediaries (nginx in front of public.yaver.io,
	// Cloudflare's edge) NOT to buffer this response. Without this
	// nginx defaults to buffering chunked responses end-to-end and
	// SSE bytes never reach the browser until the connection
	// closes — symptom: dashboard sees "sse: open" but events: 0.
	// Cloudflare honors the same header and so does Vercel/Fly.
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	// Send an immediate priming byte so any proxy in the chain
	// flushes its initial buffer (HTTP/1.1 response headers + first
	// chunk). Without this nginx still holds the headers until the
	// upstream sends the first body byte, and the dashboard never
	// transitions to "open" until the agent sends an event.
	fmt.Fprintf(w, ":relay-hello %d\n\n", time.Now().Unix())
	flusher.Flush()

	buf := make([]byte, 4096)
	for {
		n, err := stream.Read(buf)
		if n > 0 {
			if _, werr := w.Write(buf[:n]); werr != nil {
				return
			}
			flusher.Flush()
		}
		if err != nil {
			return
		}
	}
}

// proxyWebSocket hijacks the HTTP connection and bidirectionally proxies
// between the client and the QUIC stream to the agent. This enables Metro HMR
// WebSocket connections to work through the relay.
func (s *RelayServer) proxyWebSocket(w http.ResponseWriter, r *http.Request, stream quic.Stream, deviceID string) {
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "websocket proxy not supported", http.StatusInternalServerError)
		stream.Close()
		return
	}

	// Read the initial response from the agent (WebSocket upgrade response)
	// The agent sends raw HTTP response bytes for WS upgrades
	clientConn, clientBuf, err := hijacker.Hijack()
	if err != nil {
		log.Printf("[RELAY] hijack failed for WS to %s: %v", deviceID[:8], err)
		stream.Close()
		return
	}
	defer clientConn.Close()
	defer stream.Close()

	// Flush any buffered data from the client to the stream
	if clientBuf.Reader.Buffered() > 0 {
		buffered := make([]byte, clientBuf.Reader.Buffered())
		clientBuf.Read(buffered)
		stream.Write(buffered)
	}

	// Bidirectional copy between client TCP and QUIC stream
	done := make(chan struct{}, 2)
	go func() { io.Copy(clientConn, stream); done <- struct{}{} }()
	go func() { io.Copy(stream, clientConn); done <- struct{}{} }()
	<-done
}

// --- Expose (subdomain routing) ---

func (s *RelayServer) handleAgentControlStreams(conn quic.Connection, deviceID string) {
	for {
		stream, err := conn.AcceptStream(conn.Context())
		if err != nil {
			return // connection closed
		}
		go s.handleControlMsg(stream, deviceID)
	}
}

func (s *RelayServer) handleControlMsg(stream quic.Stream, deviceID string) {
	// Read a single header. Mesh streams send a newline-terminated header and
	// then keep the stream open for binary frames; legacy one-shot control
	// messages (expose_*) send a whole-stream JSON blob with no newline, so
	// ReadBytes returns it with io.EOF.
	br := bufio.NewReader(stream)
	header, rerr := br.ReadBytes('\n')
	if rerr != nil && rerr != io.EOF {
		stream.Close()
		return
	}

	var peek struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(header, &peek); err != nil {
		stream.Close()
		return
	}

	// Persistent mesh frame stream — do NOT close until the loop ends.
	if peek.Type == "mesh_relay" {
		s.handleMeshStream(stream, br, deviceID)
		return
	}

	defer stream.Close()
	// Legacy one-shot: the header IS the message (read any remainder for safety).
	data := header
	if rerr != io.EOF {
		rest, _ := io.ReadAll(io.LimitReader(br, 1<<16))
		data = append(data, rest...)
	}

	switch peek.Type {
	case "expose_register":
		var msg ExposeRegisterMsg
		if err := json.Unmarshal(data, &msg); err != nil {
			resp, _ := json.Marshal(ExposeRegisterResp{Type: "error", Message: "invalid message"})
			stream.Write(resp)
			return
		}
		s.handleExposeRegister(stream, msg, deviceID)
	case "expose_unregister":
		var msg ExposeUnregisterMsg
		if err := json.Unmarshal(data, &msg); err != nil {
			return
		}
		s.handleExposeUnregister(msg, deviceID)
	}
}

func (s *RelayServer) handleExposeRegister(stream quic.Stream, msg ExposeRegisterMsg, deviceID string) {
	subdomain := strings.ToLower(msg.Subdomain)

	// Validate subdomain format
	if len(subdomain) < 3 || len(subdomain) > 32 {
		resp, _ := json.Marshal(ExposeRegisterResp{Type: "error", Message: "subdomain must be 3-32 characters"})
		stream.Write(resp)
		return
	}
	for _, c := range subdomain {
		if !((c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-') {
			resp, _ := json.Marshal(ExposeRegisterResp{Type: "error", Message: "subdomain must be alphanumeric and hyphens only"})
			stream.Write(resp)
			return
		}
	}
	if subdomain[0] == '-' || subdomain[len(subdomain)-1] == '-' {
		resp, _ := json.Marshal(ExposeRegisterResp{Type: "error", Message: "subdomain cannot start or end with hyphen"})
		stream.Write(resp)
		return
	}

	// Block reserved subdomains. Single source of truth shared with
	// the auto-provision path at registration time (M-6).
	if reservedSubdomains[subdomain] {
		resp, _ := json.Marshal(ExposeRegisterResp{Type: "error", Message: "subdomain is reserved"})
		stream.Write(resp)
		return
	}

	if msg.Port <= 0 || msg.Port > 65535 {
		resp, _ := json.Marshal(ExposeRegisterResp{Type: "error", Message: "invalid port"})
		stream.Write(resp)
		return
	}

	s.exposeMu.Lock()
	// Check if subdomain taken by another device
	if existing, ok := s.exposeRoutes[subdomain]; ok && existing.deviceID != deviceID {
		s.exposeMu.Unlock()
		resp, _ := json.Marshal(ExposeRegisterResp{Type: "error", Message: "subdomain already taken"})
		stream.Write(resp)
		return
	}
	// Enforce max 3 per device
	count := 0
	for _, r := range s.exposeRoutes {
		if r.deviceID == deviceID {
			count++
		}
	}
	if count >= 3 {
		// Check if this is an update (same subdomain)
		if _, isUpdate := s.exposeRoutes[subdomain]; !isUpdate {
			s.exposeMu.Unlock()
			resp, _ := json.Marshal(ExposeRegisterResp{Type: "error", Message: "max 3 subdomains per device"})
			stream.Write(resp)
			return
		}
	}
	s.exposeRoutes[subdomain] = &exposeRoute{
		deviceID:  deviceID,
		port:      msg.Port,
		createdAt: time.Now(),
	}
	s.exposeMu.Unlock()

	publicURL := fmt.Sprintf("https://%s.%s", subdomain, s.exposeDomain)
	log.Printf("[EXPOSE] %s.%s → device %s port %d", subdomain, s.exposeDomain, deviceID[:8], msg.Port)

	resp, _ := json.Marshal(ExposeRegisterResp{
		Type:      "expose_registered",
		OK:        true,
		PublicURL: publicURL,
	})
	stream.Write(resp)
}

func (s *RelayServer) handleExposeUnregister(msg ExposeUnregisterMsg, deviceID string) {
	subdomain := strings.ToLower(msg.Subdomain)
	s.exposeMu.Lock()
	if route, ok := s.exposeRoutes[subdomain]; ok && route.deviceID == deviceID {
		delete(s.exposeRoutes, subdomain)
		log.Printf("[EXPOSE] Removed %s.%s", subdomain, s.exposeDomain)
	}
	s.exposeMu.Unlock()
}

// tryExposeProxy checks if the request is for a registered subdomain.
// Returns true if handled, false to fall through to normal routing.
func (s *RelayServer) tryExposeProxy(w http.ResponseWriter, r *http.Request) bool {
	host := r.Host
	// Check X-Forwarded-Host (when behind nginx/Caddy)
	if fh := r.Header.Get("X-Forwarded-Host"); fh != "" {
		host = fh
	}
	// Strip port
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}

	suffix := "." + s.exposeDomain
	if !strings.HasSuffix(host, suffix) {
		return false
	}
	subdomain := strings.TrimSuffix(host, suffix)
	if subdomain == "" {
		return false
	}

	// Path-based relay routes that must NOT be eaten by the subdomain
	// expose handler. The dashboard hits public.yaver.io/<path>
	// directly from the browser (which always carries that Host
	// header) for every relay-owned endpoint. Without this skip,
	// /presence, /tunnels, and admin paths all return 404 with
	// "subdomain 'public' not registered" before the path mux
	// ever sees them — surfaced as "Failed to load resource: 404"
	// floods in the browser console + breaking presence-driven UI.
	// Keep this list in sync with mux.HandleFunc registrations
	// above (server.go:412+).
	switch {
	case strings.HasPrefix(r.URL.Path, "/d/"),
		strings.HasPrefix(r.URL.Path, "/bus/"),
		strings.HasPrefix(r.URL.Path, "/admin/"),
		r.URL.Path == "/health",
		r.URL.Path == "/presence",
		r.URL.Path == "/tunnels",
		r.URL.Path == "/":
		return false
	}

	s.exposeMu.RLock()
	route, ok := s.exposeRoutes[subdomain]
	s.exposeMu.RUnlock()
	if !ok {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error": fmt.Sprintf("subdomain '%s' not registered", subdomain),
		})
		return true
	}

	// Find tunnel
	s.mu.RLock()
	tunnel, tunnelOK := s.tunnels[route.deviceID]
	s.mu.RUnlock()
	if !tunnelOK {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadGateway)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error": "device not connected",
		})
		return true
	}

	// Proxy the request through QUIC tunnel with TargetPort set
	s.proxyExposeRequest(w, r, tunnel, route)
	return true
}

func (s *RelayServer) proxyExposeRequest(w http.ResponseWriter, r *http.Request, tunnel *agentTunnel, route *exposeRoute) {
	if !s.abuseGuard.tryEnterDevice(tunnel.deviceID) {
		s.abuseGuard.logLimited("device-concurrency", tunnel.deviceID[:min(8, len(tunnel.deviceID))])
		writeRelayError(w, http.StatusTooManyRequests, "too many concurrent requests for device")
		return
	}
	defer s.abuseGuard.leaveDevice(tunnel.deviceID)

	// Read request body
	var body []byte
	if r.Body != nil {
		var ok bool
		body, ok = readCappedBody(w, r, s.abuseGuard.cfg.MaxExposeBodyBytes)
		if !ok {
			return
		}
	}

	// Build headers
	headers := make(map[string]string)
	for k, v := range r.Header {
		if len(v) > 0 {
			headers[k] = v[0]
		}
	}

	tunnelReq := TunnelRequest{
		ID:         fmt.Sprintf("%d", time.Now().UnixNano()),
		Method:     r.Method,
		Path:       r.URL.Path,
		Query:      r.URL.RawQuery,
		Headers:    headers,
		Body:       body,
		TargetPort: route.port,
	}

	streamCtx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()

	stream, err := tunnel.conn.OpenStreamSync(streamCtx)
	if err != nil {
		log.Printf("[EXPOSE] open stream to %s failed: %v", tunnel.deviceID[:8], err)
		http.Error(w, "device tunnel broken", http.StatusBadGateway)
		return
	}

	// Check for SSE or WebSocket
	isWebSocket := strings.EqualFold(r.Header.Get("Upgrade"), "websocket")
	isSSE := r.Method == "GET" && strings.Contains(r.Header.Get("Accept"), "text/event-stream")

	reqData, _ := json.Marshal(tunnelReq)
	if _, err := stream.Write(reqData); err != nil {
		stream.Close()
		http.Error(w, "tunnel write error", http.StatusBadGateway)
		return
	}

	if isWebSocket {
		s.proxyWebSocket(w, r, stream, tunnel.deviceID)
		return
	}

	stream.Close() // signal done writing

	if isSSE {
		s.proxySSE(w, r, stream, tunnel.deviceID)
		return
	}

	// Read response
	respData, err := io.ReadAll(io.LimitReader(stream, 200<<20))
	if err != nil {
		http.Error(w, "tunnel read error", http.StatusBadGateway)
		return
	}

	var tunnelResp TunnelResponse
	if err := json.Unmarshal(respData, &tunnelResp); err != nil {
		http.Error(w, "tunnel response parse error", http.StatusBadGateway)
		return
	}

	for k, v := range tunnelResp.Headers {
		w.Header().Set(k, v)
	}
	w.WriteHeader(tunnelResp.StatusCode)
	w.Write(tunnelResp.Body)

	// Record bandwidth
	s.bandwidth.RecordBytes(tunnel.deviceID, int64(len(reqData)), int64(len(tunnelResp.Body)), false)
}

func (s *RelayServer) logTunnels(ctx context.Context) {
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.mu.RLock()
			count := len(s.tunnels)
			for _, t := range s.tunnels {
				id := t.deviceID
				if len(id) > 8 {
					id = id[:8]
				}
				log.Printf("[RELAY] Tunnel: %s from %s (up %s)", id, t.peerAddr, time.Since(t.connAt).Round(time.Second))
			}
			s.mu.RUnlock()
			if count == 0 {
				log.Printf("[RELAY] No active tunnels")
			}
		}
	}
}

func (s *RelayServer) handleBandwidthStats(w http.ResponseWriter, r *http.Request) {
	// H-14 (audit 2026-05-02): per-device bandwidth breakdowns are
	// per-tenant and must not be public.
	if !s.authorizeAdmin(w, r) {
		return
	}
	stats := s.bandwidth.GetStats()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"ok":    true,
		"stats": stats,
	})
}

// --- CORS ---

func withRelayCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, X-Relay-Password, X-Yaver-Caller")
		// A proxied dev-server page may carry the account-wide password in its URL
		// (?__rp=), which would otherwise leak via the Referer header to every
		// third-party subresource it loads. Suppress it (relay security audit,
		// finding #3). Full fix = get the password out of the URL entirely
		// (asymmetric per-device tokens — see the relay auth design doc).
		w.Header().Set("Referrer-Policy", "no-referrer")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// --- TLS ---

func generateRelayTLS() (*tls.Config, error) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}

	serial, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	tmpl := x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{Organization: []string{"Yaver Relay"}},
		NotBefore:    time.Now(),
		NotAfter:     time.Now().Add(10 * 365 * 24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}

	certDER, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &priv.PublicKey, priv)
	if err != nil {
		return nil, err
	}

	return &tls.Config{
		Certificates: []tls.Certificate{{
			Certificate: [][]byte{certDER},
			PrivateKey:  priv,
		}},
		NextProtos: []string{"yaver-relay"},
	}, nil
}
