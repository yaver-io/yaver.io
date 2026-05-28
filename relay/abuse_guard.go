package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

type abuseGuardConfig struct {
	HTTPPerIPPerMin         int
	HTTPBurstPerIP          int
	ProxyPerIPPerMin        int
	ProxyBurstPerIP         int
	BusPerIPPerMin          int
	BusBurstPerIP           int
	AdminPerIPPerMin        int
	AdminBurstPerIP         int
	QUICRegisterPerIPPerMin int
	QUICRegisterBurstPerIP  int
	InvalidAuthPerIPPerMin  int
	InvalidAuthBurstPerIP   int
	MaxConcurrentHTTP       int
	MaxConcurrentPerDevice  int
	MaxRequestBodyBytes     int64
	MaxExposeBodyBytes      int64
	CleanupInterval         time.Duration
	IdleEntryTTL            time.Duration
}

func defaultAbuseGuardConfig() abuseGuardConfig {
	return abuseGuardConfig{
		HTTPPerIPPerMin:         600,
		HTTPBurstPerIP:          120,
		ProxyPerIPPerMin:        240,
		ProxyBurstPerIP:         80,
		BusPerIPPerMin:          120,
		BusBurstPerIP:           40,
		AdminPerIPPerMin:        60,
		AdminBurstPerIP:         20,
		QUICRegisterPerIPPerMin: 60,
		QUICRegisterBurstPerIP:  20,
		InvalidAuthPerIPPerMin:  12,
		InvalidAuthBurstPerIP:   6,
		MaxConcurrentHTTP:       2048,
		MaxConcurrentPerDevice:  64,
		MaxRequestBodyBytes:     64 << 20,
		MaxExposeBodyBytes:      200 << 20,
		CleanupInterval:         2 * time.Minute,
		IdleEntryTTL:            10 * time.Minute,
	}
}

func abuseGuardConfigFromEnv() abuseGuardConfig {
	cfg := defaultAbuseGuardConfig()
	cfg.HTTPPerIPPerMin = envInt("RELAY_HTTP_RATE_PER_IP_PER_MIN", cfg.HTTPPerIPPerMin)
	cfg.HTTPBurstPerIP = envInt("RELAY_HTTP_BURST_PER_IP", cfg.HTTPBurstPerIP)
	cfg.ProxyPerIPPerMin = envInt("RELAY_PROXY_RATE_PER_IP_PER_MIN", cfg.ProxyPerIPPerMin)
	cfg.ProxyBurstPerIP = envInt("RELAY_PROXY_BURST_PER_IP", cfg.ProxyBurstPerIP)
	cfg.BusPerIPPerMin = envInt("RELAY_BUS_RATE_PER_IP_PER_MIN", cfg.BusPerIPPerMin)
	cfg.BusBurstPerIP = envInt("RELAY_BUS_BURST_PER_IP", cfg.BusBurstPerIP)
	cfg.AdminPerIPPerMin = envInt("RELAY_ADMIN_RATE_PER_IP_PER_MIN", cfg.AdminPerIPPerMin)
	cfg.AdminBurstPerIP = envInt("RELAY_ADMIN_BURST_PER_IP", cfg.AdminBurstPerIP)
	cfg.QUICRegisterPerIPPerMin = envInt("RELAY_QUIC_REGISTER_RATE_PER_IP_PER_MIN", cfg.QUICRegisterPerIPPerMin)
	cfg.QUICRegisterBurstPerIP = envInt("RELAY_QUIC_REGISTER_BURST_PER_IP", cfg.QUICRegisterBurstPerIP)
	cfg.InvalidAuthPerIPPerMin = envInt("RELAY_INVALID_AUTH_RATE_PER_IP_PER_MIN", cfg.InvalidAuthPerIPPerMin)
	cfg.InvalidAuthBurstPerIP = envInt("RELAY_INVALID_AUTH_BURST_PER_IP", cfg.InvalidAuthBurstPerIP)
	cfg.MaxConcurrentHTTP = envInt("RELAY_MAX_CONCURRENT_HTTP", cfg.MaxConcurrentHTTP)
	cfg.MaxConcurrentPerDevice = envInt("RELAY_MAX_CONCURRENT_PER_DEVICE", cfg.MaxConcurrentPerDevice)
	cfg.MaxRequestBodyBytes = envInt64("RELAY_MAX_REQUEST_BODY_BYTES", cfg.MaxRequestBodyBytes)
	cfg.MaxExposeBodyBytes = envInt64("RELAY_MAX_EXPOSE_BODY_BYTES", cfg.MaxExposeBodyBytes)
	return cfg
}

func envInt(name string, fallback int) int {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback
	}
	v, err := strconv.Atoi(raw)
	if err != nil || v < 0 {
		log.Printf("[RELAY] ignoring invalid %s=%q", name, raw)
		return fallback
	}
	return v
}

func envInt64(name string, fallback int64) int64 {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback
	}
	v, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || v < 0 {
		log.Printf("[RELAY] ignoring invalid %s=%q", name, raw)
		return fallback
	}
	return v
}

type tokenBucket struct {
	tokens     float64
	lastRefill time.Time
	lastSeen   time.Time
}

type abuseGuard struct {
	mu            sync.Mutex
	cfg           abuseGuardConfig
	buckets       map[string]*tokenBucket
	httpSem       chan struct{}
	deviceActive  map[string]int
	deniedLogLast map[string]time.Time
}

func newAbuseGuard(cfg abuseGuardConfig) *abuseGuard {
	g := &abuseGuard{
		cfg:           cfg,
		buckets:       make(map[string]*tokenBucket),
		deviceActive:  make(map[string]int),
		deniedLogLast: make(map[string]time.Time),
	}
	if cfg.MaxConcurrentHTTP > 0 {
		g.httpSem = make(chan struct{}, cfg.MaxConcurrentHTTP)
	}
	go g.cleanupLoop()
	return g
}

func (g *abuseGuard) cleanupLoop() {
	ticker := time.NewTicker(g.cfg.CleanupInterval)
	defer ticker.Stop()
	for range ticker.C {
		cutoff := time.Now().Add(-g.cfg.IdleEntryTTL)
		g.mu.Lock()
		for k, b := range g.buckets {
			if b.lastSeen.Before(cutoff) {
				delete(g.buckets, k)
			}
		}
		for k, t := range g.deniedLogLast {
			if t.Before(cutoff) {
				delete(g.deniedLogLast, k)
			}
		}
		g.mu.Unlock()
	}
}

func (g *abuseGuard) allow(key string, perMinute, burst int) bool {
	if perMinute <= 0 || burst <= 0 {
		return true
	}
	now := time.Now()
	ratePerSec := float64(perMinute) / 60.0

	g.mu.Lock()
	defer g.mu.Unlock()

	b := g.buckets[key]
	if b == nil {
		b = &tokenBucket{tokens: float64(burst), lastRefill: now, lastSeen: now}
		g.buckets[key] = b
	}
	elapsed := now.Sub(b.lastRefill).Seconds()
	if elapsed > 0 {
		b.tokens += elapsed * ratePerSec
		if b.tokens > float64(burst) {
			b.tokens = float64(burst)
		}
		b.lastRefill = now
	}
	b.lastSeen = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

func (g *abuseGuard) tryEnterHTTP() bool {
	if g.httpSem == nil {
		return true
	}
	select {
	case g.httpSem <- struct{}{}:
		return true
	default:
		return false
	}
}

func (g *abuseGuard) leaveHTTP() {
	if g.httpSem != nil {
		<-g.httpSem
	}
}

func (g *abuseGuard) tryEnterDevice(deviceID string) bool {
	if g.cfg.MaxConcurrentPerDevice <= 0 {
		return true
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.deviceActive[deviceID] >= g.cfg.MaxConcurrentPerDevice {
		return false
	}
	g.deviceActive[deviceID]++
	return true
}

func (g *abuseGuard) leaveDevice(deviceID string) {
	if g.cfg.MaxConcurrentPerDevice <= 0 {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.deviceActive[deviceID] <= 1 {
		delete(g.deviceActive, deviceID)
		return
	}
	g.deviceActive[deviceID]--
}

func (g *abuseGuard) logLimited(reason, key string) {
	now := time.Now()
	logKey := reason + ":" + key
	g.mu.Lock()
	last := g.deniedLogLast[logKey]
	if now.Sub(last) >= 30*time.Second {
		g.deniedLogLast[logKey] = now
		g.mu.Unlock()
		log.Printf("[RELAY] abuse guard denied %s for %s", reason, key)
		return
	}
	g.mu.Unlock()
}

func (g *abuseGuard) clientIP(r *http.Request) string {
	for _, h := range []string{"CF-Connecting-IP", "X-Real-IP"} {
		if ip := strings.TrimSpace(r.Header.Get(h)); ip != "" {
			return ip
		}
	}
	if xff := strings.TrimSpace(r.Header.Get("X-Forwarded-For")); xff != "" {
		if first := strings.TrimSpace(strings.Split(xff, ",")[0]); first != "" {
			return first
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil && host != "" {
		return host
	}
	return r.RemoteAddr
}

func (g *abuseGuard) remoteIP(addr string) string {
	host, _, err := net.SplitHostPort(addr)
	if err == nil && host != "" {
		return host
	}
	return addr
}

func (g *abuseGuard) classifyHTTPPath(path string) (name string, perMinute int, burst int) {
	switch {
	case strings.HasPrefix(path, "/admin/"), path == "/tunnels", path == "/presence":
		return "admin", g.cfg.AdminPerIPPerMin, g.cfg.AdminBurstPerIP
	case strings.HasPrefix(path, "/d/"):
		return "proxy", g.cfg.ProxyPerIPPerMin, g.cfg.ProxyBurstPerIP
	case strings.HasPrefix(path, "/bus/"):
		return "bus", g.cfg.BusPerIPPerMin, g.cfg.BusBurstPerIP
	default:
		return "http", g.cfg.HTTPPerIPPerMin, g.cfg.HTTPBurstPerIP
	}
}

func (g *abuseGuard) httpMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodOptions {
			next.ServeHTTP(w, r)
			return
		}
		ip := g.clientIP(r)
		name, perMinute, burst := g.classifyHTTPPath(r.URL.Path)
		if !g.allow("http:"+name+":"+ip, perMinute, burst) {
			g.logLimited("http-"+name, ip)
			writeRelayError(w, http.StatusTooManyRequests, "rate limit exceeded")
			return
		}
		if !g.tryEnterHTTP() {
			g.logLimited("http-concurrency", ip)
			writeRelayError(w, http.StatusServiceUnavailable, "relay overloaded")
			return
		}
		defer g.leaveHTTP()
		next.ServeHTTP(w, r)
	})
}

func (g *abuseGuard) allowQUICRegister(remoteAddr string) bool {
	ip := g.remoteIP(remoteAddr)
	ok := g.allow("quic-register:"+ip, g.cfg.QUICRegisterPerIPPerMin, g.cfg.QUICRegisterBurstPerIP)
	if !ok {
		g.logLimited("quic-register", ip)
	}
	return ok
}

func (g *abuseGuard) allowInvalidAuth(remoteAddr string) bool {
	ip := g.remoteIP(remoteAddr)
	ok := g.allow("invalid-auth:"+ip, g.cfg.InvalidAuthPerIPPerMin, g.cfg.InvalidAuthBurstPerIP)
	if !ok {
		g.logLimited("invalid-auth", ip)
	}
	return ok
}

func writeRelayError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"ok":      false,
		"code":    http.StatusText(status),
		"error":   message,
		"message": message,
	})
}

func readCappedBody(w http.ResponseWriter, r *http.Request, limit int64) ([]byte, bool) {
	if r.Body == nil {
		return nil, true
	}
	if limit <= 0 {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			writeRelayError(w, http.StatusBadRequest, "could not read request body")
			return nil, false
		}
		return body, true
	}
	if r.ContentLength > limit {
		writeRelayError(w, http.StatusRequestEntityTooLarge, fmt.Sprintf("request body exceeds %d bytes", limit))
		return nil, false
	}
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeRelayError(w, http.StatusRequestEntityTooLarge, fmt.Sprintf("request body exceeds %d bytes", limit))
		return nil, false
	}
	return body, true
}
