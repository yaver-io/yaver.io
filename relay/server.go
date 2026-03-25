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
}

type agentTunnel struct {
	deviceID string
	conn     quic.Connection
	peerAddr string // observed public address
	connAt   time.Time
}

func NewRelayServer(quicPort, httpPort int, password, convexURL string) *RelayServer {
	s := &RelayServer{
		quicPort:    quicPort,
		httpPort:    httpPort,
		password:    password,
		convexURL:   convexURL,
		validatedPw: make(map[string]time.Time),
		startedAt:   time.Now(),
		tunnels:     make(map[string]*agentTunnel),
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

	// Keep connection alive — block until it dies
	<-conn.Context().Done()

	s.mu.Lock()
	if cur, ok := s.tunnels[reg.DeviceID]; ok && cur.conn == conn {
		delete(s.tunnels, reg.DeviceID)
	}
	s.mu.Unlock()

	log.Printf("[RELAY] Device %s disconnected (%s)", reg.DeviceID[:8], remoteAddr)
}

// --- HTTP Proxy (mobile clients connect here) ---

func (s *RelayServer) runHTTPProxy(ctx context.Context) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", s.handleHealth)
	mux.HandleFunc("/tunnels", s.handleListTunnels)
	mux.HandleFunc("/admin/set-password", s.handleSetPassword)
	mux.HandleFunc("/admin/status", s.handleAdminStatus)
	mux.HandleFunc("/admin/bandwidth", s.handleBandwidthStats)
	mux.HandleFunc("/d/", s.handleProxy) // /d/{deviceId}/...

	srv := &http.Server{
		Addr:    fmt.Sprintf("0.0.0.0:%d", s.httpPort),
		Handler: withRelayCORS(mux),
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

	bwStats := s.bandwidth.GetStats()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"ok":              true,
		"tunnels":         count,
		"version":         version,
		"activeDevices":   bwStats.ActiveDevices,
		"loadPercent":     bwStats.LoadPercent,
		"limitsRelaxed":   bwStats.LimitsRelaxed,
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

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"ok":      true,
		"tunnels": list,
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

	// Send request
	reqData, _ := json.Marshal(tunnelReq)
	if _, err := stream.Write(reqData); err != nil {
		log.Printf("[RELAY] write to %s failed: %v", tunnel.deviceID[:8], err)
		stream.Close()
		http.Error(w, "tunnel write error", http.StatusBadGateway)
		return
	}
	stream.Close() // signal done writing

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
