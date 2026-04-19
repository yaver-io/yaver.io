package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
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
	"strings"
	"sync"
	"time"

	"github.com/quic-go/quic-go"
)

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
	validatedPw   map[string]time.Time // password -> cache expiry

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
		exposeRoutes: make(map[string]*exposeRoute),
		exposeDomain: exposeDomain,
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
func (s *RelayServer) setPassword(pw string) {
	s.pwMu.Lock()
	defer s.pwMu.Unlock()
	s.password = pw
}

// validatePassword checks a password against the shared password or Convex backend.
// Returns true if the password is valid.
func (s *RelayServer) validatePassword(pw string) bool {
	// 1. Check shared password (self-hosted mode)
	if sharedPw := s.getPassword(); sharedPw != "" && pw == sharedPw {
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
	url := strings.TrimRight(s.convexURL, "/") + "/relay/validate"
	body, _ := json.Marshal(map[string]string{"password": pw})
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Post(url, "application/json", strings.NewReader(string(body)))
	if err != nil {
		log.Printf("[RELAY] Convex validation error: %v", err)
		return false
	}
	defer resp.Body.Close()

	var result struct {
		OK bool `json:"ok"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		log.Printf("[RELAY] Convex validation parse error: %v", err)
		return false
	}
	return result.OK
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

	var reg RegisterMsg
	if err := json.Unmarshal(data, &reg); err != nil || reg.Type != "register" {
		resp, _ := json.Marshal(RegisterResp{Type: "error", OK: false, Message: "invalid registration"})
		stream.Write(resp)
		stream.Close()
		conn.CloseWithError(1, "bad registration")
		return
	}

	if reg.DeviceID == "" || reg.Token == "" {
		resp, _ := json.Marshal(RegisterResp{Type: "error", OK: false, Message: "deviceId and token required"})
		stream.Write(resp)
		stream.Close()
		conn.CloseWithError(1, "missing fields")
		return
	}

	// Validate relay password (shared or per-user via Convex)
	if !s.validatePassword(reg.Password) {
		resp, _ := json.Marshal(RegisterResp{Type: "error", OK: false, Message: "invalid relay password"})
		stream.Write(resp)
		stream.Close()
		conn.CloseWithError(1, "invalid relay password")
		return
	}

	// Register the tunnel
	tunnel := &agentTunnel{
		deviceID: reg.DeviceID,
		conn:     conn,
		peerAddr: remoteAddr,
		connAt:   time.Now(),
	}

	s.mu.Lock()
	old, exists := s.tunnels[reg.DeviceID]
	s.tunnels[reg.DeviceID] = tunnel
	s.mu.Unlock()

	if exists {
		log.Printf("[RELAY] Replacing existing tunnel for device %s (was %s)", reg.DeviceID[:8], old.peerAddr)
		old.conn.CloseWithError(0, "replaced")
	}

	// Send success
	resp, _ := json.Marshal(RegisterResp{Type: "registered", OK: true})
	stream.Write(resp)
	stream.Close()

	log.Printf("[RELAY] Device %s registered from %s", reg.DeviceID[:8], remoteAddr)

	// Best-effort push to Convex so mobile/web pick up tunnel-up
	// within the Convex reactive latency window instead of polling
	// /presence every 30s. No-op unless CONVEX_PRESENCE_URL +
	// CONVEX_PRESENCE_SECRET env vars are set. See convex_presence.go.
	pushPresence(presencePayload{
		DeviceID:    reg.DeviceID,
		Online:      true,
		PeerAddr:    remoteAddr,
		ConnectedAt: tunnel.connAt.UnixMilli(),
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
	}

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

func (s *RelayServer) handleHealth(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	count := len(s.tunnels)
	s.mu.RUnlock()

	s.exposeMu.RLock()
	exposeCount := len(s.exposeRoutes)
	s.exposeMu.RUnlock()

	bwStats := s.bandwidth.GetStats()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"ok":                  true,
		"tunnels":             count,
		"exposeRoutes":        exposeCount,
		"version":             version,
		"activeDevices":       bwStats.ActiveDevices,
		"loadPercent":         bwStats.LoadPercent,
		"limitsRelaxed":       bwStats.LimitsRelaxed,
		"bandwidthMultiplier": bwStats.CurrentMultiplier,
	})
}

func (s *RelayServer) handleListTunnels(w http.ResponseWriter, r *http.Request) {
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
			"subdomain":   sub,
			"deviceId":    deviceID,
			"port":        route.port,
			"publicUrl":   publicURL,
			"createdAt":   route.createdAt.Format(time.RFC3339),
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
//   GET /presence?id=<deviceId>            -> single {deviceId, online, since}
//   GET /presence?ids=a,b,c,...            -> map keyed by deviceId
//
// Unknown deviceIds return online:false (indistinguishable from "exists but
// offline"), so an adversary can't enumerate our tunnel table.
// Response bodies are small; no auth required because no sensitive data
// leaves the relay — only the caller's own deviceId yields a real signal.
func (s *RelayServer) handlePresence(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	s.mu.RLock()
	defer s.mu.RUnlock()

	lookup := func(id string) map[string]interface{} {
		now := time.Now()
		if t, ok := s.tunnels[id]; ok {
			return map[string]interface{}{
				"deviceId":   id,
				"online":     true,
				"since":      t.connAt.UTC().Format(time.RFC3339),
				"uptimeSec":  int(now.Sub(t.connAt).Seconds()),
			}
		}
		return map[string]interface{}{
			"deviceId": id,
			"online":   false,
		}
	}

	if ids := r.URL.Query().Get("ids"); ids != "" {
		out := map[string]interface{}{}
		for _, raw := range strings.Split(ids, ",") {
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
func (s *RelayServer) handleSetPassword(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
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

	// If a password is currently set, require current_password to match
	if currentPw := s.getPassword(); currentPw != "" {
		if req.CurrentPassword != currentPw {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"error": "invalid current password",
			})
			return
		}
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
func (s *RelayServer) handleAdminStatus(w http.ResponseWriter, r *http.Request) {
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

	// Validate relay password (shared or per-user via Convex)
	relayPw := r.Header.Get("X-Relay-Password")
	if !s.validatePassword(relayPw) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error": "invalid relay password",
		})
		return
	}

	// Check bandwidth limit
	if err := s.bandwidth.CheckAllowed(deviceID, r.ContentLength); err != nil {
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

	// Read request body
	var body []byte
	if r.Body != nil {
		body, _ = io.ReadAll(io.LimitReader(r.Body, 10<<20)) // 10MB limit
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
		Query:   r.URL.RawQuery,
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

	// Check if this is an SSE request (task output, dev server events, blackbox subscribe)
	isSSE := r.Method == "GET" && (strings.Contains(forwardPath, "/output") ||
		strings.HasSuffix(forwardPath, "/dev/events") ||
		strings.HasSuffix(forwardPath, "/subscribe"))
	if isSSE {
		s.proxySSE(w, r, stream, tunnel.deviceID)
		return
	}

	// Read response
	respData, err := io.ReadAll(io.LimitReader(stream, 10<<20))
	if err != nil {
		log.Printf("[RELAY] read from %s failed: %v", tunnel.deviceID[:8], err)
		http.Error(w, "tunnel read error", http.StatusBadGateway)
		return
	}

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
	bytesIn := int64(len(reqData))
	if r.ContentLength > 0 {
		bytesIn += r.ContentLength
	}
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
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	buf := make([]byte, 4096)
	for {
		n, err := stream.Read(buf)
		if n > 0 {
			w.Write(buf[:n])
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
	defer stream.Close()
	data, err := io.ReadAll(io.LimitReader(stream, 1<<16))
	if err != nil {
		return
	}

	var peek struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(data, &peek); err != nil {
		return
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

	// Block reserved subdomains
	reserved := map[string]bool{"www": true, "api": true, "relay": true, "public": true, "admin": true, "mail": true, "app": true}
	if reserved[subdomain] {
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
	// Read request body
	var body []byte
	if r.Body != nil {
		body, _ = io.ReadAll(io.LimitReader(r.Body, 200<<20)) // 200MB for expose (dev assets)
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
		w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, X-Relay-Password")
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
