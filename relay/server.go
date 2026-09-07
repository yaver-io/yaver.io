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
	"errors"
	"fmt"
	"io"
	"log"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/quic-go/quic-go"
	"golang.org/x/net/websocket"
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

// relaySSHControlSentinelPath: a /d/<deviceId>/_yaver_ssh_control request is a raw
// bidirectional tunnel to the box's out-of-band SSH control server (spliced like a
// WebSocket). The relay never authorizes SSH — it only pipes bytes; the box does
// public-key auth + the forced-command cage. KEEP IN SYNC with
// desktop/agent/ssh_relay_bridge.go's relaySSHControlSentinelPath.
const relaySSHControlSentinelPath = "/_yaver_ssh_control"

// RelayServer accepts QUIC tunnels from agents and proxies HTTP requests
// from mobile clients through those tunnels.
type RelayServer struct {
	quicPort int // QUIC port for agent tunnels
	httpPort int // HTTP port for mobile clients

	// password is protected by pwMu for runtime updates
	pwMu     sync.RWMutex
	password string // shared password for relay authentication (empty = no auth)
	// webviewCookieSecret is process-local signing material for browser-preview
	// cookies. It must exist even when the official relay uses only Convex
	// per-user authentication and therefore has no shared password.
	webviewCookieSecret string

	// Convex backend URL for per-user password validation (empty = use shared password only)
	convexURL string

	// Cache of validated per-user passwords (password -> expiry time)
	validatedPwMu       sync.RWMutex
	validatedPw         map[string]time.Time           // password/access cache key -> cache expiry
	validatedAccessMeta map[string]validatedAccessMeta // access cache key -> entitlement metadata
	// userEntitlements caches the last RESOLVED plan per owner account. The
	// exemption belongs to the OWNER, so it must reach every device that
	// owner has — including requests that resolve nothing themselves
	// (webview-cookie subresources), which are looked up by the tunnel's
	// registered userID rather than left to default to free tier.
	userEntitlements  map[string]deviceEntitlement
	userEntitlementMu sync.RWMutex

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
	// Auth-mix telemetry: how many proxy auths used a device signature vs the
	// shared password. The password cutover (removing password auth) must be
	// data-driven — flip it off only once authViaSig ≈ total. Exposed at
	// /authmix (admin-authed).
	authViaSig atomic.Uint64
	authViaPw  atomic.Uint64
	// Subset of authViaSig that crossed accounts — a guest / host-share /
	// support peer reaching a device its own account does not own, allowed by
	// an active infraAccessGrant (backend/convex/devices.ts::resolveDeviceSig).
	// Counted separately because these are the sessions the cutover is most
	// likely to break: until 2026-07-23 the signature path denied them outright
	// and they were carried ONLY by the password, invisibly. A cutover reading
	// "100% signature" while this counter is 0 and cross-account sessions exist
	// means they are still riding the password — check before flipping.
	authViaSigGrant atomic.Uint64
	// Sub-resource requests authorized by the WebView cookie. Tracked so the
	// password-cutover metric can tell browser asset traffic apart from clients
	// that simply never migrated.
	authViaCookie atomic.Uint64
	// Attributable signature failures, by reason. authViaPw alone cannot tell
	// "never migrated" from "migrated but silently failing" — both fall back to
	// the password. Without this split the cutover is a guess. See sigFailReason.
	sigFailMu sync.Mutex
	sigFails  map[sigFailReason]uint64

	// turnAuthSecret is used only to mint short-lived TURN REST credentials
	// from GET /ice. It never crosses the HTTP boundary. The official relay
	// loads it from a systemd credential file; self-hosters may still use the
	// TURN_AUTH_SECRET environment variable.
	turnAuthSecret string
}

type validatedAccessMeta struct {
	UserID string
	IsPaid bool
	Plan   string
	Expiry time.Time
}

type exposeRoute struct {
	deviceID  string
	port      int
	createdAt time.Time
}

type agentTunnel struct {
	deviceID string
	conn     quic.Connection
	ws       *wsAgentTunnel
	peerAddr string // observed public address
	connAt   time.Time
	userID   string // owner resolved at registration; scopes mesh forwarding
}

type wsAgentTunnel struct {
	conn    *websocket.Conn
	writeMu sync.Mutex

	pendingMu sync.Mutex
	pending   map[string]chan WSTunnelFrame

	done chan struct{}
}

func newWSAgentTunnel(conn *websocket.Conn) *wsAgentTunnel {
	return &wsAgentTunnel{
		conn:    conn,
		pending: make(map[string]chan WSTunnelFrame),
		done:    make(chan struct{}),
	}
}

func (wst *wsAgentTunnel) send(frame WSTunnelFrame) error {
	wst.writeMu.Lock()
	defer wst.writeMu.Unlock()
	return websocket.JSON.Send(wst.conn, frame)
}

func (wst *wsAgentTunnel) request(ctx context.Context, req TunnelRequest) (*TunnelResponse, error) {
	ch := make(chan WSTunnelFrame, 1)
	wst.pendingMu.Lock()
	wst.pending[req.ID] = ch
	wst.pendingMu.Unlock()
	defer func() {
		wst.pendingMu.Lock()
		delete(wst.pending, req.ID)
		wst.pendingMu.Unlock()
	}()

	if err := wst.send(WSTunnelFrame{Type: "request", ID: req.ID, Request: &req}); err != nil {
		return nil, err
	}
	select {
	case frame := <-ch:
		if frame.Type == "error" {
			msg := strings.TrimSpace(frame.Message)
			if msg == "" {
				msg = "websocket tunnel error"
			}
			return nil, errors.New(msg)
		}
		if frame.Response == nil {
			return nil, errors.New("websocket tunnel returned no response")
		}
		return frame.Response, nil
	case <-wst.done:
		return nil, errors.New("websocket tunnel closed")
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (wst *wsAgentTunnel) readLoop() {
	defer close(wst.done)
	for {
		var frame WSTunnelFrame
		if err := websocket.JSON.Receive(wst.conn, &frame); err != nil {
			return
		}
		switch frame.Type {
		case "response", "error":
			wst.pendingMu.Lock()
			ch := wst.pending[frame.ID]
			wst.pendingMu.Unlock()
			if ch != nil {
				select {
				case ch <- frame:
				default:
				}
			}
		case "ping":
			_ = wst.send(WSTunnelFrame{Type: "pong", ID: frame.ID})
		}
	}
}

func NewRelayServer(quicPort, httpPort int, password, convexURL, exposeDomain string) *RelayServer {
	s := &RelayServer{
		quicPort:            quicPort,
		httpPort:            httpPort,
		password:            password,
		webviewCookieSecret: newWebviewCookieSecret(),
		convexURL:           convexURL,
		validatedPw:         make(map[string]time.Time),
		validatedAccessMeta: make(map[string]validatedAccessMeta),
		userEntitlements:    make(map[string]deviceEntitlement),
		startedAt:           time.Now(),
		tunnels:             make(map[string]*agentTunnel),
		meshStreams:         make(map[string]*meshStreamHandle),
		exposeRoutes:        make(map[string]*exposeRoute),
		exposeDomain:        exposeDomain,
		busHub:              newBusHub(),
		pwUserIDs:           make(map[string]string),
		pwUserIDExp:         make(map[string]time.Time),
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
	s.validatedAccessMeta = make(map[string]validatedAccessMeta)
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
	wsConns := make([]*websocket.Conn, 0, len(s.tunnels))
	for _, t := range s.tunnels {
		if t.conn != nil {
			conns = append(conns, t.conn)
		}
		if t.ws != nil && t.ws.conn != nil {
			wsConns = append(wsConns, t.ws.conn)
		}
	}
	s.mu.Unlock()
	for _, c := range conns {
		c.CloseWithError(0, "password rotated")
	}
	for _, c := range wsConns {
		_ = c.Close()
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

	// 2. Relay password via X-Relay-Password header — but ONLY the relay's
	//    OWN shared admin password, NEVER a per-user Convex password.
	//    validatePassword() falls back to validatePasswordViaConvex, which
	//    accepts ANY tenant's per-user relay password — so every free signup
	//    could read /tunnels, /admin/bandwidth, every tenant's deviceId
	//    prefixes + peer IPs (and, chained with the prefix hole, reach their
	//    boxes). Admin tier is the shared secret or the bearer token, full
	//    stop. We DON'T accept the ?__rp= query fallback here either — these
	//    are admin endpoints, not iframe-served content.
	relayPw := r.Header.Get("X-Relay-Password")
	if sp := s.getPassword(); sp != "" && relayPw != "" && secretEqual(relayPw, sp) {
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

// handleAuthMix reports the sig-vs-password auth mix so the password cutover is
// data-driven: flip password auth off only once device signatures are ≈ 100% of
// proxy auths. Admin-authed, read-only.
func (s *RelayServer) handleAuthMix(w http.ResponseWriter, r *http.Request) {
	if !s.authorizeAdmin(w, r) {
		return
	}
	sig := s.authViaSig.Load()
	sigGrant := s.authViaSigGrant.Load()
	pw := s.authViaPw.Load()
	total := sig + pw
	sigPct := 0.0
	if total > 0 {
		sigPct = float64(sig) / float64(total) * 100
	}

	s.sigFailMu.Lock()
	fails := make(map[string]uint64, len(s.sigFails))
	var failTotal uint64
	for reason, n := range s.sigFails {
		fails[string(reason)] = n
		failTotal += n
	}
	s.sigFailMu.Unlock()

	// sigPercent alone is NOT a safe cutover signal. A client whose signature is
	// broken falls back to the password and lands in authViaPassword, looking
	// exactly like one that never migrated. So a fleet could read 100% "migrated"
	// while a chunk of it is actually failing signature auth every request — and
	// turning the password off would lock precisely those clients out.
	//
	// The honest gate is BOTH: essentially all auths via signature, AND
	// essentially no signature failures. sigFailures is the number that has to go
	// to zero; the breakdown says what to fix first.
	safe := failTotal == 0 && pw == 0 && sig > 0
	note := "SAFE TO CUT OVER: every auth used a signature and none failed"
	switch {
	case sig == 0:
		note = "no signature auths seen yet — do not cut over"
	case failTotal > 0:
		note = "signature auths are FAILING and being masked by the password fallback — fix sigFailures before cutting over, or these clients get locked out"
	case pw > 0:
		note = "clients are still authenticating by password — not migrated yet"
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"authViaSig": sig,
		// Cross-account reaches (guest / host-share / project-share / support)
		// carried by a signature rather than the password. Before 2026-07-23 the
		// signature path denied these outright, so they were carried invisibly by
		// the password and this number would have been 0 forever — a cutover
		// would have taken every one of them down. Read it alongside sigPercent:
		// if you know cross-account sessions exist and this is 0, they are still
		// on the password and the cutover is NOT safe regardless of sigPercent.
		"authViaSigCrossAccount": sigGrant,
		"authViaPassword":        pw,
		"total":                  total,
		"sigPercent":             sigPct,
		"sigFailures":            failTotal,
		"sigFailByReason":        fails,
		"safeToCutOver":          safe,
		"note":                   note,
	})
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
	_, ok, _ := s.validateAndResolveViaConvex(pw, "", "", "")
	return ok
}

func (s *RelayServer) validateRelayAccess(pw, action, deviceID, token string) (string, bool) {
	uid, ok, _ := s.validateRelayAccessE(pw, action, deviceID, token)
	return uid, ok
}

func relayAccessCacheKey(action, deviceID, pw, token string) string {
	return strings.Join([]string{"access", action, deviceID, pw, token}, "\x00")
}

func (s *RelayServer) relayAccessIsPaid(action, deviceID, pw, token string) bool {
	isPaid, _ := s.relayAccessEntitlement(action, deviceID, pw, token)
	return isPaid
}

// relayAccessEntitlement surfaces the Convex-verified (isPaid, plan) cached by
// validateRelayAccessWithReason for this access key. Expired or absent reads
// as unentitled — fail closed. The plan string is what grants the owner-dev
// bandwidth exemption; it comes exclusively from Convex's verdict about the
// AUTHENTICATED caller, never from anything the client sent.
func (s *RelayServer) relayAccessEntitlement(action, deviceID, pw, token string) (bool, string) {
	key := relayAccessCacheKey(strings.TrimSpace(action), strings.TrimSpace(deviceID), strings.TrimSpace(pw), strings.TrimSpace(token))
	s.validatedPwMu.RLock()
	meta, ok := s.validatedAccessMeta[key]
	s.validatedPwMu.RUnlock()
	if !ok || !time.Now().Before(meta.Expiry) {
		return false, ""
	}
	return meta.IsPaid, meta.Plan
}

// planBandwidthExempt reports whether a Convex plan is exempt from the relay
// bandwidth cap AND the per-user proxy rate limit. Only owner-dev (the
// Convex-env owner allowlist) qualifies: the paid tiers buy a bigger
// allowance, not the absence of one. Tamper-proofness rests on where the plan
// comes from (ownerAllowlist env vars + subscriptions table, read through
// authenticated /relay/validate and /relay/resolve-sig) — no client-writable
// row or header participates.
func planBandwidthExempt(plan string) bool {
	return plan == "owner-dev"
}

// rememberUserEntitlement records a RESOLVED plan for an account so sibling
// devices and unknowing requests inherit it. Unresolved verdicts are ignored:
// silence is not a free-tier verdict.
func (s *RelayServer) rememberUserEntitlement(userID string, ent deviceEntitlement) {
	if strings.TrimSpace(userID) == "" || !ent.Known {
		return
	}
	s.userEntitlementMu.Lock()
	if s.userEntitlements == nil {
		s.userEntitlements = make(map[string]deviceEntitlement)
	}
	s.userEntitlements[userID] = ent
	s.userEntitlementMu.Unlock()
}

// entitlementForUser returns the account's last resolved plan, or unknown.
func (s *RelayServer) entitlementForUser(userID string) deviceEntitlement {
	if strings.TrimSpace(userID) == "" {
		return entitlementUnknown
	}
	s.userEntitlementMu.RLock()
	ent, ok := s.userEntitlements[userID]
	s.userEntitlementMu.RUnlock()
	if !ok {
		return entitlementUnknown
	}
	return ent
}

// ownerOfTunnel reports the account that registered a device's tunnel — the
// only identity available on a request that authenticated by cookie.
func (s *RelayServer) ownerOfTunnel(deviceID string) string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if t, ok := s.tunnels[deviceID]; ok && t != nil {
		return t.userID
	}
	return ""
}

// validateRelayAccessE is validateRelayAccess plus an honest reason.
// A non-nil error is always errAuthBackendUnavailable — "we could not check",
// never "you are wrong". Access is still denied (fail closed); the difference
// is what we tell the caller and whether it earns an invalid-auth strike.
func (s *RelayServer) validateRelayAccessE(pw, action, deviceID, token string) (string, bool, error) {
	uid, ok, _, err := s.validateRelayAccessWithReason(pw, action, deviceID, token)
	return uid, ok, err
}

// validateRelayAccessWithReason additionally reports the RelayDenyReason so
// the caller can render distinct wire messages. Audit §3 (2026-07-19).
// The empty reason value (RelayDenyReason("")) means "not applicable" —
// either ok=true, or err is non-nil (backend unavailable), or self-hosted
// shared-password mode (no reason to distinguish).
func (s *RelayServer) validateRelayAccessWithReason(pw, action, deviceID, token string) (string, bool, RelayDenyReason, error) {
	pw = strings.TrimSpace(pw)
	action = strings.TrimSpace(action)
	deviceID = strings.TrimSpace(deviceID)
	token = strings.TrimSpace(token)
	if pw == "" {
		return "", false, RelayDenyBadPassword, nil
	}

	// Self-hosted shared-password mode remains supported. The official free
	// relay sets CONVEX_URL, so register/proxy authorization goes through the
	// backend ownership/quota path instead of accepting a universal shared key.
	if s.convexURL == "" {
		if sharedPw := s.getPassword(); sharedPw != "" {
			if secretEqual(pw, sharedPw) {
				return "", true, "", nil
			}
			return "", false, RelayDenyBadPassword, nil
		}
		return "", true, "", nil
	}

	cacheKey := relayAccessCacheKey(action, deviceID, pw, token)
	s.validatedPwMu.RLock()
	if expiry, ok := s.validatedPw[cacheKey]; ok && time.Now().Before(expiry) {
		s.validatedPwMu.RUnlock()
		return s.resolveUserIDFromPassword(pw), true, "", nil
	}
	s.validatedPwMu.RUnlock()

	userID, ok, isPaid, plan, reason, err := s.validateAndResolveViaConvexE(pw, action, deviceID, token)
	if ok {
		expiry := time.Now().Add(5 * time.Minute)
		s.validatedPwMu.Lock()
		s.validatedPw[cacheKey] = expiry
		s.validatedAccessMeta[cacheKey] = validatedAccessMeta{
			UserID: userID,
			IsPaid: isPaid,
			Plan:   plan,
			Expiry: expiry,
		}
		s.validatedPwMu.Unlock()
		if userID != "" {
			s.pwUserIDMu.Lock()
			s.pwUserIDs[pw] = userID
			s.pwUserIDExp[pw] = expiry
			s.pwUserIDMu.Unlock()
		}
	}
	return userID, ok, reason, err
}

// validateAndResolveViaConvex returns both the validity and the
// resolved userId. Same 5-minute cache as validatePassword. Used by
// the bus to scope fanout per-user without a second Convex round-trip.
// errAuthBackendUnavailable means we could not REACH a verdict — the auth
// backend errored, timed out, or returned something unparseable. It is NOT a
// rejection.
//
// Collapsing this into "invalid relay password" (the old behavior) is what
// turns a transient Convex hiccup into a fleet-wide outage: every agent is
// told its password is wrong, every client is told the same, the invalid-auth
// limiter starts throttling them, and agents that snapshot their credentials
// never recover. An unreachable backend must fail SOFT — say so honestly, do
// not blame the caller's credential, and let them retry.
var errAuthBackendUnavailable = errors.New("relay auth backend unavailable")

func (s *RelayServer) validateAndResolveViaConvex(pw, action, deviceID, token string) (string, bool, bool) {
	uid, ok, isPaid, _, _, _ := s.validateAndResolveViaConvexE(pw, action, deviceID, token)
	return uid, ok, isPaid
}

// RelayDenyReason names why access was denied. See audit §3 (2026-07-19).
// The relay maps these to distinct client-facing rejection strings so the
// desktop agent can route recovery to the RIGHT remedy: bad-password → refetch
// the password, dead-token → re-auth the whole session, device-mismatch →
// user error. Collapsing these into one wire string (the pre-fix behaviour)
// is how the mini went dark for hours while the agent looped refetching a
// password that was never wrong.
type RelayDenyReason string

const (
	RelayDenyBadPassword    RelayDenyReason = "bad_password"
	RelayDenyDeadToken      RelayDenyReason = "dead_token"
	RelayDenyDeviceMismatch RelayDenyReason = "device_mismatch"
)

// validateAndResolveViaConvexE additionally reports errAuthBackendUnavailable
// so callers can distinguish "your credential is wrong" from "we could not
// check". Everything security-relevant still fails CLOSED: a non-nil error
// grants no access. It only changes what we TELL the caller and whether we
// hold it against them.
//
// Return: (userId, ok, isPaid, plan, denyReason, err). When ok is true, deny
// is empty. When ok is false and err is nil, deny is one of the constants
// above (or empty when the backend was too old to emit one — treated as
// bad-password by callers, which matches the pre-fix behaviour).
func (s *RelayServer) validateAndResolveViaConvexE(pw, action, deviceID, token string) (string, bool, bool, string, RelayDenyReason, error) {
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
		return "", false, false, "", "", errAuthBackendUnavailable
	}
	defer resp.Body.Close()

	// 5xx is the backend failing, not the caller. 401 IS a real verdict.
	if resp.StatusCode >= 500 {
		log.Printf("[RELAY] Convex validation backend error: HTTP %d", resp.StatusCode)
		return "", false, false, "", "", errAuthBackendUnavailable
	}

	var result struct {
		OK     bool   `json:"ok"`
		UserID string `json:"userId"`
		IsPaid bool   `json:"isPaid"`
		Plan   string `json:"plan"`
		Reason string `json:"reason"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		log.Printf("[RELAY] Convex validation parse error: %v", err)
		return "", false, false, "", "", errAuthBackendUnavailable
	}
	reason := RelayDenyReason("")
	if !result.OK {
		switch result.Reason {
		case string(RelayDenyBadPassword), string(RelayDenyDeadToken), string(RelayDenyDeviceMismatch):
			reason = RelayDenyReason(result.Reason)
		}
	}
	return result.UserID, result.OK, result.IsPaid, result.Plan, reason, nil
}

// resolveSigViaConvex fetches the SIGNER device's ed25519 signing public key
// and confirms the signer may address the TARGET device — either because the
// same account owns both, or because an active cross-account access grant
// links them (viaGrant). Returns (userId, signerPubKeyBase64, ok, viaGrant).
// The relay holds no secret — it receives only public material and verifies
// the signature itself (verifyDeviceSig). Authorization to actually DO
// anything still happens at the agent; this only decides whether the relay
// will carry the bytes.
// sigResolution is Convex's verdict about a signer→target reach, including
// the SIGNER's billing entitlement (plan/isPaid). Before the entitlement
// rode along here, the signature path left every caller on the free-tier
// bandwidth cap — the password path resolved isPaid but the (preferred) sig
// path never did, so the owner's own phone was metered as an anonymous free
// user. The plan is Convex's statement about the authenticated signer; the
// relay never widens it from client input.
type sigResolution struct {
	UserID          string
	SignerPublicKey string
	ViaGrant        bool
	IsPaid          bool
	Plan            string
}

func (s *RelayServer) resolveSigViaConvex(signerDeviceID, targetDeviceID string) (sigResolution, bool) {
	if s.convexURL == "" {
		return sigResolution{}, false
	}
	url := strings.TrimRight(s.convexURL, "/") + "/relay/resolve-sig"
	body, _ := json.Marshal(map[string]string{
		"signerDeviceId": signerDeviceID,
		"targetDeviceId": targetDeviceID,
	})
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Post(url, "application/json", strings.NewReader(string(body)))
	if err != nil {
		return sigResolution{}, false
	}
	defer resp.Body.Close()
	var result struct {
		OK              bool   `json:"ok"`
		UserID          string `json:"userId"`
		SignerPublicKey string `json:"signerPublicKey"`
		ViaGrant        bool   `json:"viaGrant"`
		IsPaid          bool   `json:"isPaid"`
		Plan            string `json:"plan"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return sigResolution{}, false
	}
	if !result.OK {
		return sigResolution{}, false
	}
	return sigResolution{
		UserID:          result.UserID,
		SignerPublicKey: result.SignerPublicKey,
		ViaGrant:        result.ViaGrant,
		IsPaid:          result.IsPaid,
		Plan:            result.Plan,
	}, true
}

// sigFailReason names WHY a device-signature auth attempt failed. Every one of
// these used to collapse into a bare `false`, which is how the password cutover
// metric came to lie: a client whose signature was BROKEN (skewed clock, stale
// key, Convex blip, replayed nonce) fell through to the password path and was
// counted as authViaPw — indistinguishable from a client that had simply never
// migrated. /authmix said "not migrated yet"; the truth was "migrated, and
// failing silently". Flip the password off on that reading and those clients are
// locked out with no warning, because the fallback that was meant to protect
// them is exactly what hid them.
//
// The fallback stays (a bad signature must never be fatal while the password is
// still live) — but it is now attributable.
type sigFailReason string

const (
	sigFailNoSigner       sigFailReason = "no_signer_device"
	sigFailBodyRead       sigFailReason = "body_read"
	sigFailUnresolved     sigFailReason = "unresolved_signer" // Convex down, unknown device, or signer≠owner of target
	sigFailBadPubKey      sigFailReason = "bad_public_key"    // Convex returned a key we cannot decode
	sigFailBadSignature   sigFailReason = "bad_signature"     // wrong key, skewed clock, tampered body, or replayed nonce
	sigFailDeviceMismatch sigFailReason = "signer_mismatch"   // signature is valid but signed a different device id
)

// noteSigFail records an attributable signature failure. Unknown reasons are
// dropped rather than allocating an unbounded map from attacker-controlled
// input — the reason set is closed and defined above.
func (s *RelayServer) noteSigFail(reason sigFailReason) {
	s.sigFailMu.Lock()
	if s.sigFails == nil {
		s.sigFails = make(map[sigFailReason]uint64, 6)
	}
	s.sigFails[reason]++
	s.sigFailMu.Unlock()
}

// authorizeProxyViaSig tries the asymmetric per-device signature path for a
// /d/<targetDeviceID>/ proxy request. On success it returns the resolved userId,
// true, and whether the reach crossed accounts via an access grant; on ANY
// failure it returns false so the caller falls back to the password path — this
// can never lock out a client that hasn't migrated. It buffers the request body
// (only when a signature is present) so it can hash it for verification AND
// still forward it downstream.
//
// Every failure path is counted by reason (see sigFailReason) so the fallback
// stays non-fatal WITHOUT being invisible.
func (s *RelayServer) authorizeProxyViaSig(r *http.Request, targetDeviceID string) (sigResolution, bool) {
	signerDeviceID := strings.TrimSpace(r.Header.Get("X-Yaver-Device"))
	if signerDeviceID == "" {
		s.noteSigFail(sigFailNoSigner)
		return sigResolution{}, false
	}
	var body []byte
	if r.Body != nil {
		b, err := readBodyForSignature(r, s.abuseGuard.cfg.MaxRequestBodyBytes)
		if err != nil {
			s.noteSigFail(sigFailBodyRead)
			return sigResolution{}, false
		}
		body = b
		r.Body = io.NopCloser(bytes.NewReader(body)) // let the downstream proxy re-read it
	}
	res, ok := s.resolveSigViaConvex(signerDeviceID, targetDeviceID)
	if !ok {
		s.noteSigFail(sigFailUnresolved)
		return sigResolution{}, false
	}
	pub := decodeSignPubKey(res.SignerPublicKey)
	if pub == nil {
		s.noteSigFail(sigFailBadPubKey)
		return sigResolution{}, false
	}
	signed, ok := verifyDeviceSig(r, body, pub, s.sigNonces)
	if !ok {
		s.noteSigFail(sigFailBadSignature)
		return sigResolution{}, false
	}
	if !sigDeviceMatches(signed, signerDeviceID) {
		s.noteSigFail(sigFailDeviceMismatch)
		return sigResolution{}, false
	}
	return res, true
}

func readBodyForSignature(r *http.Request, limit int64) ([]byte, error) {
	if r.Body == nil {
		return nil, nil
	}
	if limit > 0 && r.ContentLength > limit {
		return nil, fmt.Errorf("request body exceeds %d bytes", limit)
	}
	if limit <= 0 {
		return io.ReadAll(r.Body)
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, limit+1))
	if err != nil {
		return nil, err
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	if int64(len(body)) > limit {
		return nil, fmt.Errorf("request body exceeds %d bytes", limit)
	}
	return body, nil
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

	uid, ok, _ := s.validateAndResolveViaConvex(pw, "", "", "")
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
	// Persistent key so the SPKI is stable across restarts and agents can pin it.
	// Falls back to an ephemeral key (logged) if the key path is unwritable.
	tlsCfg, err := generatePersistentRelayTLS()
	if err != nil {
		return fmt.Errorf("TLS setup: %w", err)
	}

	// A nil IP is the wildcard for both address families. The old literal
	// 0.0.0.0 made an otherwise healthy IPv6-only client unable to establish
	// the agent tunnel even when the relay host had a routed IPv6 prefix.
	addr := fmt.Sprintf(":%d", s.quicPort)
	udpAddr := &net.UDPAddr{Port: s.quicPort}

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
	regUserID, ok, denyReason, authErr := s.validateRelayAccessWithReason(reg.Password, "register", reg.DeviceID, reg.Token)
	if !ok {
		// We could not REACH a verdict. Do not tell the agent its password is
		// wrong (it will "self-heal" a credential that was never broken) and do
		// not hold it against the IP. Say so, and let it retry.
		if authErr != nil {
			log.Printf("[RELAY] register %s: auth backend unavailable — telling the agent to retry", reg.DeviceID[:min(8, len(reg.DeviceID))])
			rejectRegistration("relay auth backend unavailable — retry", "auth backend unavailable")
			return
		}
		if strings.TrimSpace(reg.Password) == "" {
			rejectRegistration("relay password missing", "no relay password")
			return
		}
		// dead_token means the PASSWORD validated (Convex matched the row) but
		// the session token lapsed — a legitimate client, not a brute-force
		// source. Counting it as an invalid-auth strike lets an owner's own
		// box lock itself out: the 429s never reach Convex again, so
		// clearInvalidAuth can never fire (2026-08-10, ubuntu-4gb-hel1-1).
		// See the websocket register path for the full reasoning.
		if denyReason != RelayDenyDeadToken {
			if !s.abuseGuard.allowInvalidAuth(remoteAddr) {
				rejectRegistration("too many invalid relay password attempts", "invalid password rate limited")
				return
			}
		} else {
			s.abuseGuard.clearInvalidAuth(remoteAddr)
		}
		// Audit §3 (2026-07-19): return the distinct reason so the agent's
		// looksLikeStaleRelayPassword can route dead-token to re-auth instead
		// of a hopeless password refetch. The string form embeds the code so
		// older agents that pattern-match on "invalid" continue to work.
		switch denyReason {
		case RelayDenyDeadToken:
			rejectRegistration(
				"relay session expired — sign in again on this device (reason=dead_token)",
				"session token expired",
			)
		case RelayDenyDeviceMismatch:
			rejectRegistration(
				"relay password owner does not own this deviceId (reason=device_mismatch)",
				"device mismatch",
			)
		default:
			rejectRegistration(
				"invalid relay password (reason=bad_password)",
				"invalid relay password",
			)
		}
		return
	}
	// Valid credential from this IP — it is not a brute-force source. Clear any
	// strikes so a broken client behind the same NAT can't lock this one out.
	s.abuseGuard.clearInvalidAuth(remoteAddr)

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
		deadNow := false
		if existing.conn != nil {
			select {
			case <-existing.conn.Context().Done():
				deadNow = true
			default:
			}
		}
		switch {
		case deadNow:
			// Previous tunnel is dead but cleanup hasn't run yet —
			// remove it now so this registration can take its place.
			delete(s.tunnels, reg.DeviceID)
		case regUserID != "" && existing.userID == regUserID:
			// SAME authenticated user reconnecting → last-writer-wins.
			//
			// C-1 (2026-05-02) refuses collisions to stop a DIFFERENT peer who
			// merely holds the shared password from hijacking a tunnel. But when
			// the NEW registration is validated to the SAME userID as the tunnel
			// it's colliding with, this is that user's own device reconnecting
			// after an unclean drop — and refusing it created an up-to-120s black
			// hole: the relay kept the (already dead) tunnel "alive" until QUIC's
			// MaxIdleTimeout fired, so the agent's fast redial was rejected and
			// the phone got "online but no transport answered" for a full minute
			// (2026-07-21 incident). Evict the stale tunnel and accept the new
			// one. This is NOT a hijack (same owner, and the owner can always
			// disconnect their own device); anti-hijack still holds because a
			// different userID — or an unauthenticated/self-hosted shared-password
			// relay where userID=="" — falls through to the refuse below.
			log.Printf("[RELAY] Same-user reconnect for device %s (user %s) — evicting stale tunnel from %s, accepting %s",
				reg.DeviceID[:min(8, len(reg.DeviceID))], regUserID[:min(8, len(regUserID))], existing.peerAddr, remoteAddr)
			if existing.conn != nil {
				_ = existing.conn.CloseWithError(0, "superseded by same-user reconnect")
			}
			delete(s.tunnels, reg.DeviceID)
		default:
			s.mu.Unlock()
			log.Printf("[RELAY] Refusing duplicate registration for device %s (existing tunnel from %s, new from %s — different/unauthenticated owner)",
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
		userID:   regUserID,
	}
	s.tunnels[reg.DeviceID] = tunnel
	s.mu.Unlock()

	// Stamp the device's bandwidth tier NOW, from the registration verdict —
	// not lazily on the first authenticated proxy request. Registration is the
	// one moment every lane shares: QUIC bridges (phone native, `yaver code
	// --attach` CLI-to-CLI), expose subdomains, the SSH bridge and webview
	// subresources all move bytes for this device without ever carrying a
	// password themselves, so before this stamp an owner-dev box was unmetered
	// on the dashboard lane and free-tier on every other one. Same trust
	// boundary as everything else here: the plan is Convex's cached verdict
	// about the AUTHENTICATED registrant ("register" action), never a client
	// claim.
	if regPaid, regPlan := s.relayAccessEntitlement("register", reg.DeviceID, reg.Password, reg.Token); regPlan != "" || regPaid {
		ent := deviceEntitlement{Known: true, IsPaid: regPaid, Unmetered: planBandwidthExempt(regPlan)}
		if regUserID != "" {
			s.rememberUserEntitlement(regUserID, ent)
		}
		s.bandwidth.SetDeviceTier(reg.DeviceID, regPaid, ent.Unmetered)
	}

	// A registered tunnel is not the same thing as a WORKING one. Watch it.
	go s.watchTunnelLiveness(tunnel)

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
	go s.handleAgentControlStreams(conn, reg.DeviceID, regUserID)

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

func (s *RelayServer) handleAgentWebSocket(ws *websocket.Conn) {
	req := ws.Request()
	remoteAddr := ""
	if req != nil {
		remoteAddr = req.RemoteAddr
	}
	if remoteAddr == "" {
		remoteAddr = "websocket"
	}
	log.Printf("[RELAY] Agent websocket connected from %s", remoteAddr)

	if !s.abuseGuard.allowQUICRegister(remoteAddr) {
		_ = websocket.JSON.Send(ws, WSTunnelFrame{Type: "error", OK: false, Message: "registration rate limited"})
		_ = ws.Close()
		return
	}

	reject := func(message string) {
		_ = websocket.JSON.Send(ws, WSTunnelFrame{Type: "error", OK: false, Message: message})
		_ = ws.Close()
	}

	var frame WSTunnelFrame
	if err := websocket.JSON.Receive(ws, &frame); err != nil {
		reject("invalid registration")
		return
	}
	reg := frame.Register
	if frame.Type == "register" && reg == nil {
		// Allow the frame itself to carry the register fields in future without
		// changing the first deployed agent fallback. Today agents send
		// {type:"register", register:{...}}.
		reject("invalid registration")
		return
	}
	if reg == nil || reg.Type != "register" {
		reject("invalid registration")
		return
	}
	if reg.DeviceID == "" || reg.Token == "" {
		reject("deviceId and token required")
		return
	}
	if !deviceIDShapePattern.MatchString(reg.DeviceID) {
		reject("invalid deviceId shape")
		return
	}
	if s.exposeDomain != "" && reservedSubdomains[strings.ToLower(reg.DeviceID)] {
		reject("deviceId reserved")
		return
	}

	regUserID, ok, denyReason, authErr := s.validateRelayAccessWithReason(reg.Password, "register", reg.DeviceID, reg.Token)
	if !ok {
		if authErr != nil {
			log.Printf("[RELAY] websocket register %s: auth backend unavailable", reg.DeviceID[:min(8, len(reg.DeviceID))])
			reject("relay auth backend unavailable — retry")
			return
		}
		if strings.TrimSpace(reg.Password) == "" {
			reject("relay password missing")
			return
		}
		// dead_token means the PASSWORD is correct (Convex matched the
		// userSettings row) but the session token is expired/foreign — a
		// legitimate client whose session lapsed, NOT a brute-force source.
		// Counting it as an invalid-auth strike lets an owner's own box lock
		// itself out: every retry earns a strike, the bucket drains, and the
		// 429s never reach Convex again so clearInvalidAuth can never fire —
		// a self-sustaining lockout (2026-08-10, ubuntu-4gb-hel1-1). The
		// strike exists to stop unknown passwords being guessed; a password
		// that was JUST verified correct proves this source is not guessing.
		if denyReason != RelayDenyDeadToken {
			if !s.abuseGuard.allowInvalidAuth(remoteAddr) {
				reject("too many invalid relay password attempts")
				return
			}
		} else {
			// The password validated — clear this IP's prior strikes so a
			// misconfigured client that retried before re-auth does not keep
			// paying for those attempts after it fixes the session.
			s.abuseGuard.clearInvalidAuth(remoteAddr)
		}
		// Audit §3 (2026-07-19) — see the QUIC register path above for why
		// these three cases must be distinct on the wire.
		switch denyReason {
		case RelayDenyDeadToken:
			reject("relay session expired — sign in again on this device (reason=dead_token)")
		case RelayDenyDeviceMismatch:
			reject("relay password owner does not own this deviceId (reason=device_mismatch)")
		default:
			reject("invalid relay password (reason=bad_password)")
		}
		return
	}
	s.abuseGuard.clearInvalidAuth(remoteAddr)

	wst := newWSAgentTunnel(ws)

	s.mu.Lock()
	if existing, exists := s.tunnels[reg.DeviceID]; exists {
		alive := false
		if existing.conn != nil {
			select {
			case <-existing.conn.Context().Done():
				delete(s.tunnels, reg.DeviceID)
			default:
				alive = true
			}
		} else if existing.ws != nil {
			select {
			case <-existing.ws.done:
				delete(s.tunnels, reg.DeviceID)
			default:
				alive = true
			}
		}
		if alive {
			s.mu.Unlock()
			log.Printf("[RELAY] Refusing duplicate websocket registration for device %s from %s", reg.DeviceID[:min(8, len(reg.DeviceID))], remoteAddr)
			reject("deviceId already registered")
			return
		}
	}

	tunnel := &agentTunnel{
		deviceID: reg.DeviceID,
		ws:       wst,
		peerAddr: remoteAddr,
		connAt:   time.Now(),
		userID:   regUserID,
	}
	s.tunnels[reg.DeviceID] = tunnel
	s.mu.Unlock()

	// Same registration-time tier stamp as the QUIC path above — the WS
	// fallback is still a registration, and a box that fell back to
	// websocket must not silently lose its owner's exemption.
	if regPaid, regPlan := s.relayAccessEntitlement("register", reg.DeviceID, reg.Password, reg.Token); regPlan != "" || regPaid {
		ent := deviceEntitlement{Known: true, IsPaid: regPaid, Unmetered: planBandwidthExempt(regPlan)}
		if regUserID != "" {
			s.rememberUserEntitlement(regUserID, ent)
		}
		s.bandwidth.SetDeviceTier(reg.DeviceID, regPaid, ent.Unmetered)
	}

	resp := WSTunnelFrame{Type: "registered", OK: true}
	if err := wst.send(resp); err != nil {
		_ = ws.Close()
		return
	}

	log.Printf("[RELAY] Device %s registered from %s via websocket fallback", reg.DeviceID[:8], remoteAddr)
	pushPresence(presencePayload{
		DeviceID:    reg.DeviceID,
		Online:      true,
		PeerAddr:    remoteAddr,
		ConnectedAt: tunnel.connAt.UnixMilli(),
	})

	wst.readLoop()

	s.mu.Lock()
	if cur, ok := s.tunnels[reg.DeviceID]; ok && cur.ws == wst {
		delete(s.tunnels, reg.DeviceID)
	}
	s.mu.Unlock()
	s.dropMeshStream(reg.DeviceID)
	pushPresence(presencePayload{
		DeviceID:    reg.DeviceID,
		Online:      false,
		ConnectedAt: tunnel.connAt.UnixMilli(),
		DurationSec: int(time.Since(tunnel.connAt).Seconds()),
	})
	log.Printf("[RELAY] Device %s websocket fallback disconnected (%s)", reg.DeviceID[:8], remoteAddr)
}

// --- HTTP Proxy (mobile clients connect here) ---

func (s *RelayServer) runHTTPProxy(ctx context.Context) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", s.handleHealth)
	mux.HandleFunc("/ice", s.handleICECredentials)
	mux.HandleFunc("/tunnels", s.handleListTunnels)
	mux.HandleFunc("/presence", s.handlePresence)
	mux.HandleFunc("/admin/set-password", s.handleSetPassword)
	mux.HandleFunc("/admin/status", s.handleAdminStatus)
	mux.HandleFunc("/admin/selftest", s.handleAdminSelftest)
	mux.HandleFunc("/authmix", s.handleAuthMix)
	mux.HandleFunc("/admin/bandwidth", s.handleBandwidthStats)
	mux.HandleFunc("/my/bandwidth", s.handleMyBandwidth)
	// P2P bus — per-user fanout (see relay/bus.go). Not a broker;
	// relay holds no topic state, just forwards events.
	mux.HandleFunc("/bus/publish", s.handleBusPublish)
	mux.HandleFunc("/bus/subscribe", s.handleBusSubscribe)
	mux.HandleFunc("/bus/status", s.handleBusStatus)
	mux.Handle("/agent/tunnel/ws", websocket.Handler(s.handleAgentWebSocket))
	mux.HandleFunc("/d/", s.handleProxy) // /d/{deviceId}/...

	srv := &http.Server{
		// Empty host asks Go for a dual-stack wildcard listener. nginx normally
		// reaches this over loopback, while direct/self-hosted relay installs may
		// use either IPv4 or IPv6.
		Addr: fmt.Sprintf(":%d", s.httpPort),
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

	log.Printf("[RELAY] HTTP proxy on [::]:%d (dual-stack)", s.httpPort)
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
			"transport":   map[bool]string{true: "websocket", false: "quic"}[t.conn == nil && t.ws != nil],
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
	var relayPaid bool
	var relayPlan string
	if hasDeviceSig(r) {
		if sig, sigOK := s.authorizeProxyViaSig(r, deviceID); sigOK {
			userID, authed = sig.UserID, true
			// The signer's Convex-verified entitlement rides along with the
			// sig resolution — without it, sig-authenticated callers (the
			// preferred path) were all metered as free tier.
			relayPaid, relayPlan = sig.IsPaid, sig.Plan
			s.authViaSig.Add(1)
			if sig.ViaGrant {
				s.authViaSigGrant.Add(1)
			}
		}
	}
	// WebView sub-resource path: a browser cannot set a header, and a relative
	// asset URL carries no query string, so an already-authorized page's assets
	// arrive bare. A signature-valid, device-scoped, expiring cookie (minted
	// below on the authorized parent request) authorizes exactly those.
	// Carries no secret — see webview_cookie.go.
	if !authed && webviewCookieAuthorizes(r, deviceID, s.webviewCookieSecret) {
		authed = true
		s.authViaCookie.Add(1)
	}
	if !authed {
		uid, ok, denyReason, authErr := s.validateRelayAccessWithReason(relayPw, "proxy", deviceID, "")
		if !ok {
			// Could not reach a verdict — 503, not 401. Telling a client its
			// password is invalid when the auth backend is merely down sends it
			// hunting a credential bug that does not exist (and, in the mobile
			// client, into a retry loop that trips the limiter below).
			// Every refusal below carries a STABLE `code` (abuse_guard.go) so a
			// client can tell "the RELAY refused MY credential" from "the AGENT
			// refused your token" without regexing English. The prose is
			// unchanged on purpose — shipped clients still match it, and this
			// relay is redeployed by hand.
			if authErr != nil {
				writeRelayErrorCode(w, http.StatusServiceUnavailable, RelayCodeAuthBackendUnavailable, "relay auth backend unavailable — retry")
				return
			}
			if strings.TrimSpace(relayPw) == "" {
				writeRelayErrorCode(w, http.StatusUnauthorized, RelayCodePasswordMissing, "relay password missing — sign in again to fetch it")
				return
			}
			// Throttle invalid-auth attempts so the account-wide relay password
			// isn't brute-forcible over HTTP (relay security audit, finding #4).
			// Keyed on the real client IP (trusted-proxy-aware clientIP).
			//
			// dead_token means the PASSWORD validated but the session token
			// lapsed — a legitimate client, not a brute-force source. Counting
			// it as an invalid-auth strike lets an owner's own box lock itself
			// out: the 429s never reach Convex again, so clearInvalidAuth can
			// never fire (2026-08-10, ubuntu-4gb-hel1-1). Same rule as the
			// register paths above; the reason must be read (not collapsed) for
			// this to work, which is why this path uses
			// validateRelayAccessWithReason rather than validateRelayAccessE.
			if denyReason != RelayDenyDeadToken {
				if !s.abuseGuard.allowInvalidAuth(s.abuseGuard.clientIP(r)) {
					writeRelayErrorCode(w, http.StatusTooManyRequests, RelayCodePasswordRateLimited, "too many invalid relay password attempts")
					return
				}
			} else {
				s.abuseGuard.clearInvalidAuth(s.abuseGuard.clientIP(r))
			}
			// Was a hand-rolled `{"error": ...}` with no ok/code/message at all,
			// so the ONE deny a credential refresh actually repairs arrived on
			// the wire less structured than every other refusal. Same prose,
			// now the standard envelope.
			writeRelayErrorCode(w, http.StatusUnauthorized, RelayCodePasswordInvalid, "invalid relay password")
			return
		}
		userID = uid
		relayPaid, relayPlan = s.relayAccessEntitlement("proxy", deviceID, relayPw, "")
		s.authViaPw.Add(1)
		s.abuseGuard.clearInvalidAuth(s.abuseGuard.clientIP(r))
	}
	// Hand the browser a scoped cookie so the assets this page is about to
	// request can authenticate themselves. Only ever on an ALREADY-authorized
	// request, so this widens nothing.
	setWebviewAuthCookie(w, r, deviceID, s.webviewCookieSecret)

	// owner-dev (Convex owner allowlist) is exempt from the per-user rate
	// limit and the bandwidth cap below. The verdict came from Convex about
	// the AUTHENTICATED caller — nothing client-sent can claim it.
	// Entitlement is the ACCOUNT's, not this request's. A request that
	// resolved a plan teaches the account cache; one that could not (the
	// webview-cookie path carries no password and no signature — and preview
	// subresources ARE that traffic) inherits the owner's, resolved via the
	// tunnel's registered userID. Before this, an unknowing request wrote
	// "free tier" over a verified exemption, so the owner's own browser lane
	// refused itself at 1911MB while the store said unmetered (2026-07-27).
	entitlement := entitlementUnknown
	if relayPlan != "" || relayPaid {
		entitlement = deviceEntitlement{Known: true, IsPaid: relayPaid, Unmetered: planBandwidthExempt(relayPlan)}
		s.rememberUserEntitlement(userID, entitlement)
	} else {
		owner := userID
		if owner == "" {
			owner = s.ownerOfTunnel(deviceID)
		}
		entitlement = s.entitlementForUser(owner)
	}
	relayUnmetered := entitlement.Known && entitlement.Unmetered
	// The middleware defers the per-IP proxy verdict to HERE, where the
	// ACCOUNT is known: an over-budget request survives only when the
	// authenticated account's Convex-verified plan is bandwidth-exempt.
	// The whitelist is the account, never the IP.
	if proxyOverBudget(r) && !relayUnmetered {
		s.abuseGuard.logLimited("http-proxy", s.abuseGuard.clientIP(r))
		writeRelayError(w, http.StatusTooManyRequests, "rate limit exceeded")
		return
	}
	if userID != "" && !relayUnmetered && !s.abuseGuard.allow("proxy-user:"+userID, s.abuseGuard.cfg.ProxyPerUserPerMin, s.abuseGuard.cfg.ProxyBurstPerUser) {
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
	s.bandwidth.ApplyEntitlement(deviceID, entitlement)

	// Check bandwidth limit
	if err := s.bandwidth.CheckAllowed(deviceID, bytesRequested); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error": err.Error(),
		})
		return
	}

	// Find the tunnel. EXACT match only — a prefix match here was a
	// cross-tenant hole: authorization at :1886 validates the exact URL
	// string against the caller's OWN devices row, but a prefix fallback
	// could then resolve that authorized 8-char string to a DIFFERENT
	// tenant's full-UUID tunnel and deliver the request there. Every real
	// client sends the full deviceId; short IDs are not a supported input.
	s.mu.RLock()
	tunnel, ok := s.tunnels[deviceID]
	s.mu.RUnlock()

	// Ownership backstop: even on an exact match, the tunnel's registered
	// owner MUST equal the authorized caller. This is the same same-owner
	// check mesh.go already enforces; the /d/ proxy path was missing it, so
	// any future resolution mismatch can never bridge tenants. Empty userID
	// (self-hosted shared-password relay with no Convex) skips the check —
	// there is no access graph to scope to in that deployment.
	if ok && userID != "" && tunnel.userID != "" && tunnel.userID != userID {
		// Deliberately the SAME prose as the genuine-absence 502 below — a
		// caller must not be able to probe whether a deviceId belongs to
		// someone else. The `code` differs so OUR OWN operators (logs, support,
		// `yaver doctor`) can tell the two apart; the body a stranger sees is
		// otherwise identical.
		writeRelayErrorCode(w, http.StatusBadGateway, RelayCodeDeviceOwnerMismatch, "device not connected to relay")
		return
	}

	if !ok {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadGateway)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"ok":         false,
			"code":       RelayCodeDeviceNotConnected,
			"reasonCode": "connectivity.relay.device_not_connected",
			"error":      "device not connected to relay",
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

	// Cloudflare-friendly fallback tunnel. It carries normal request/response
	// HTTP traffic over WebSocket when QUIC/UDP is unavailable. Raw upgraded
	// streams and SSE remain QUIC-only until the websocket frame protocol grows
	// streaming multiplex support.
	if tunnel.conn == nil && tunnel.ws != nil {
		isWebSocket := strings.EqualFold(r.Header.Get("Upgrade"), "websocket")
		isSSE := r.Method == "GET" &&
			(strings.Contains(r.Header.Get("Accept"), "text/event-stream") ||
				strings.Contains(forwardPath, "/output") ||
				strings.HasSuffix(forwardPath, "/dev/events") ||
				strings.HasSuffix(forwardPath, "/subscribe") ||
				strings.HasSuffix(forwardPath, "/blackbox/command-stream") ||
				strings.HasSuffix(forwardPath, "/blackbox/stream") ||
				strings.HasSuffix(forwardPath, "/feedback/stream") ||
				strings.Contains(forwardPath, "/streams/"))
		if isWebSocket || isSSE {
			writeRelayError(w, http.StatusBadGateway, "device is connected through websocket fallback; streaming endpoints require QUIC relay")
			return
		}
		reqCtx, cancel := context.WithTimeout(r.Context(), 15*time.Minute)
		defer cancel()
		tunnelResp, err := tunnel.ws.request(reqCtx, tunnelReq)
		if err != nil {
			log.Printf("[RELAY] websocket fallback request to %s failed: %v", tunnel.deviceID[:8], err)
			writeRelayError(w, http.StatusBadGateway, "agent websocket fallback tunnel broken")
			return
		}
		for k, v := range tunnelResp.Headers {
			w.Header().Set(k, v)
		}
		w.WriteHeader(tunnelResp.StatusCode)
		_, _ = w.Write(tunnelResp.Body)

		reqData, _ := json.Marshal(tunnelReq)
		bytesIn := int64(len(reqData))
		if r.ContentLength > 0 {
			bytesIn += r.ContentLength
		}
		s.bandwidth.RecordBytes(deviceID, bytesIn, int64(len(tunnelResp.Body)), relayPaid)
		return
	}
	if tunnel.conn == nil {
		writeRelayError(w, http.StatusBadGateway, "agent tunnel has no usable transport")
		return
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
	// Out-of-band SSH control channel: a request to the SSH sentinel path is a
	// raw bidirectional tunnel to the box's SSH control server — treat it exactly
	// like a WebSocket (hijack + splice raw bytes), no buffering. The agent side
	// splices to its local SSH port; auth happens THERE. Additive: no real route
	// is this path. KEEP relaySSHControlSentinelPath IN SYNC with
	// desktop/agent/ssh_relay_bridge.go.
	isSSHControl := strings.HasSuffix(forwardPath, relaySSHControlSentinelPath)

	// Send request
	reqData, _ := json.Marshal(tunnelReq)
	if _, err := stream.Write(reqData); err != nil {
		log.Printf("[RELAY] write to %s failed: %v", tunnel.deviceID[:8], err)
		stream.Close()
		http.Error(w, "tunnel write error", http.StatusBadGateway)
		return
	}

	// WebSocket OR SSH control: keep stream open for raw bidirectional proxy.
	if isWebSocket || isSSHControl {
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
	first, err := s.readTunnelFirstByte(tunnel, stream, forwardPath)
	if err != nil {
		log.Printf("[RELAY] read first byte from %s failed: %v", tunnel.deviceID[:8], err)
		http.Error(w, "tunnel read error", http.StatusBadGateway)
		return
	}

	bytesIn := int64(len(reqData))
	if r.ContentLength > 0 {
		bytesIn += r.ContentLength
	}

	if first == streamWireMagic {
		// New streaming wire format. Don't buffer; let the reader
		// flush chunks straight to the client.
		//
		// Outbound bytes are now counted exactly (countingResponseWriter)
		// rather than recorded as 0. Streaming is the most expensive traffic
		// the relay carries — continuous media over the WebRTC/TURN fallback
		// and desktop streams — and reporting 0 made it invisible to
		// BandwidthManager.CheckAllowed, i.e. uncapped free egress. Wrapping
		// the writer is precisely the fix the old comment here deferred.
		//
		// The budget is the device's remaining daily allowance. Without it a
		// single stream could never be stopped: CheckAllowed above ran once
		// against ContentLength (0 for a streaming GET), so it always passed,
		// and nothing re-checked until the tunnel's 15-minute timeout.
		cw := &countingResponseWriter{
			ResponseWriter: w,
			budget:         s.bandwidth.RemainingBytes(deviceID),
			// Report incrementally so a long stream is visible to concurrent
			// requests while it runs, instead of landing in one lump at the end.
			// bytesIn is billed once, below, to avoid double-counting it.
			report: func(delta int64) {
				s.bandwidth.RecordBytes(deviceID, 0, delta, relayPaid)
			},
		}
		err := readStreamingResponse(cw, stream)
		cw.Close() // flush the final partial increment
		if err != nil && !errors.Is(err, errBandwidthBudgetExhausted) {
			// Headers were already written by the time most errors
			// fire — log and bail, the client sees a truncated body.
			log.Printf("[RELAY] streaming response from %s failed: %v", tunnel.deviceID[:8], err)
		}
		if cw.Exhausted() {
			log.Printf("[RELAY] device %s hit its bandwidth limit mid-stream after %d bytes; stream cut",
				tunnel.deviceID[:8], cw.BytesWritten())
		}
		// Outbound was billed incrementally by the reporter above; only the
		// request side remains. Bytes already sent are billed even when the
		// stream was cut — they cost the same as a clean transfer.
		s.bandwidth.RecordBytes(deviceID, bytesIn, 0, relayPaid)
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
	respData := append([]byte{first}, rest...)

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
	s.bandwidth.RecordBytes(deviceID, bytesIn, bytesOut, relayPaid)
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

func (s *RelayServer) handleAgentControlStreams(conn quic.Connection, deviceID, userID string) {
	for {
		stream, err := conn.AcceptStream(conn.Context())
		if err != nil {
			return // connection closed
		}
		go s.handleControlMsg(stream, deviceID, userID)
	}
}

func (s *RelayServer) handleControlMsg(stream quic.Stream, deviceID, userID string) {
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
		s.handleMeshStream(stream, br, deviceID, userID)
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

const maxExposeSubdomainsPerDevice = 32

func exposeRouteLimitReached(routes map[string]*exposeRoute, deviceID, subdomain string) bool {
	if existing, ok := routes[subdomain]; ok && existing.deviceID == deviceID {
		return false // idempotent update never consumes another slot
	}
	count := 0
	for _, route := range routes {
		if route.deviceID == deviceID {
			count++
		}
	}
	return count >= maxExposeSubdomainsPerDevice
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
	// Browser-shortcut isolation consumes one hostname per exported app. Keep
	// the device quota bounded, but large enough for a real project catalog;
	// the old limit of three made the fourth exported app fail by design.
	if exposeRouteLimitReached(s.exposeRoutes, deviceID, subdomain) {
		s.exposeMu.Unlock()
		resp, _ := json.Marshal(ExposeRegisterResp{Type: "error", Message: fmt.Sprintf("max %d subdomains per device", maxExposeSubdomainsPerDevice)})
		stream.Write(resp)
		return
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
	// Check X-Forwarded-Host — but ONLY from the trusted front proxy. nginx
	// never clears a client-supplied X-Forwarded-Host, so honoring it from
	// anyone let a raw internet request set `<victimDeviceId>.<exposeDomain>`
	// and reach that agent's control port with zero relay auth. The
	// trusted-proxy allowlist is the same one clientIP uses.
	if s.abuseGuard.isTrustedProxy(net.ParseIP(s.abuseGuard.remoteIP(r.RemoteAddr))) {
		if fh := r.Header.Get("X-Forwarded-Host"); fh != "" {
			host = fh
		}
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
	// Canonical relay hosts own these paths. A registered app/device subdomain,
	// however, must receive its root and health paths too; otherwise an exported
	// PWA's install URL is a guaranteed 404 despite a successful registration.
	switch {
	case !reservedSubdomains[subdomain]:
		// continue into registered expose routing below
	case strings.HasPrefix(r.URL.Path, "/d/"),
		strings.HasPrefix(r.URL.Path, "/bus/"),
		strings.HasPrefix(r.URL.Path, "/agent/"),
		strings.HasPrefix(r.URL.Path, "/admin/"),
		strings.HasPrefix(r.URL.Path, "/my/"),
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
	if tunnel.conn == nil {
		writeRelayError(w, http.StatusBadGateway, "subdomain expose requires QUIC relay; device is on websocket fallback")
		return
	}
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

// handleMyBandwidth is the PER-TENANT usage view the dashboard settings page
// renders: the caller's Convex-verified plan plus usage rows for exactly the
// devices whose tunnels the caller's account registered. /admin/bandwidth
// stays admin-tier and returns every tenant's rows; this endpoint exists so
// the product surface never needs that reach. Owner-dev accounts still get
// their real usage numbers — "no limit" and "how much am I moving" are both
// things the owner asked to see (2026-07-27).
func (s *RelayServer) handleMyBandwidth(w http.ResponseWriter, r *http.Request) {
	// Same stable codes as the /d/ proxy ladder: a credential refusal is a
	// credential refusal whichever relay endpoint produced it, so one client
	// classifier covers both. Prose unchanged.
	pw := strings.TrimSpace(r.Header.Get("X-Relay-Password"))
	if pw == "" {
		writeRelayErrorCode(w, http.StatusUnauthorized, RelayCodePasswordMissing, "relay password required")
		return
	}
	userID, ok, err := s.validateRelayAccessE(pw, "", "", "")
	if err != nil {
		writeRelayErrorCode(w, http.StatusServiceUnavailable, RelayCodeAuthBackendUnavailable, "auth backend unavailable — retry")
		return
	}
	if !ok || userID == "" {
		if !s.abuseGuard.allowInvalidAuth(s.abuseGuard.clientIP(r)) {
			writeRelayErrorCode(w, http.StatusTooManyRequests, RelayCodePasswordRateLimited, "too many invalid auth attempts")
			return
		}
		writeRelayErrorCode(w, http.StatusUnauthorized, RelayCodePasswordInvalid, "invalid relay password")
		return
	}
	isPaid, plan := s.relayAccessEntitlement("", "", pw, "")

	s.mu.RLock()
	var mine []string
	for id, t := range s.tunnels {
		if t.userID == userID {
			mine = append(mine, id)
		}
	}
	s.mu.RUnlock()
	sort.Strings(mine)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"ok":        true,
		"plan":      plan,
		"isPaid":    isPaid,
		"unmetered": planBandwidthExempt(plan),
		"devices":   s.bandwidth.SummaryFor(mine),
	})
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
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, X-Relay-Password, X-Yaver-Caller, X-Yaver-Surface, X-Client-Platform")
		origin := strings.TrimSpace(r.Header.Get("Origin"))
		if origin == "" {
			w.Header().Set("Access-Control-Allow-Origin", "*")
		} else if relayCORSOriginAllowed(origin) {
			// Credentialed preview bootstrap must use an exact origin; wildcard
			// CORS makes browsers discard Set-Cookie. Never reflect an arbitrary
			// hostile origin on this multi-tenant relay.
			w.Header().Set("Access-Control-Allow-Origin", canonicalRelayOrigin(origin))
			w.Header().Set("Access-Control-Allow-Credentials", "true")
			w.Header().Set("Vary", "Origin")
		} else if r.Method == http.MethodOptions {
			http.Error(w, "CORS origin denied", http.StatusForbidden)
			return
		}
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

func relayCORSOriginAllowed(origin string) bool {
	parsed, err := url.Parse(origin)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return false
	}
	host := strings.ToLower(parsed.Hostname())
	if parsed.Scheme == "https" && (host == "yaver.io" || strings.HasSuffix(host, ".yaver.io")) {
		return true
	}
	if host == "localhost" || host == "127.0.0.1" || host == "::1" {
		return true
	}
	for _, raw := range strings.Split(os.Getenv("YAVER_RELAY_CORS_ORIGINS"), ",") {
		if canonicalRelayOrigin(raw) == canonicalRelayOrigin(origin) && canonicalRelayOrigin(raw) != "" {
			return true
		}
	}
	return false
}

func canonicalRelayOrigin(origin string) string {
	parsed, err := url.Parse(strings.TrimSpace(origin))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return ""
	}
	return strings.ToLower(parsed.Scheme) + "://" + strings.ToLower(parsed.Host)
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
