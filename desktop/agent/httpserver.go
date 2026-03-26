package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	osexec "os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// HTTPServer serves the V1 HTTP API for mobile clients over Tailscale.
type HTTPServer struct {
	port        int
	token       string
	ownerUserID string
	convexURL   string
	hostname    string
	taskMgr     *TaskManager
	execMgr     *ExecManager
	scheduler   *Scheduler
	analytics   *Analytics
	aclMgr      *ACLManager
	emailMgr    *EmailManager
	notifyMgr   *NotificationManager
	vaultStore  *VaultStore
	buildMgr    *BuildManager
	tunnelMgr   *TunnelManager
	testMgr     *TestManager
	feedbackMgr *FeedbackManager
	blackboxMgr   *BlackBoxManager
	devServerMgr  *DevServerManager
	todolistMgr   *TodoListManager
	multiUserMgr  *MultiUserManager // nil in single-user mode
	server       *http.Server
	tlsServer    *http.Server
	onShutdown   func() // called when mobile requests agent shutdown

	// Cache validated tokens (token -> cachedTokenInfo) to avoid repeated Convex calls
	tokenCache sync.Map

	// IP allowlist — if non-empty, only these CIDRs can access the agent
	allowedCIDRs []*net.IPNet

	// Track seen IPs per token prefix for new-device notifications
	seenIPs sync.Map // "tokenPrefix_IP" -> true

	// TLS config for HTTPS on LAN
	tlsPort int
	tlsCert tls.Certificate
	tlsFingerprint string
}

// NewHTTPServer creates a new HTTP server bound to the given port.
func NewHTTPServer(port int, token, ownerUserID, convexURL, hostname string, taskMgr *TaskManager) *HTTPServer {
	return &HTTPServer{
		port:        port,
		token:       token,
		ownerUserID: ownerUserID,
		convexURL:   convexURL,
		hostname:    hostname,
		taskMgr:     taskMgr,
	}
}

// Start starts the HTTP server and blocks until the context is cancelled.
func (s *HTTPServer) Start(ctx context.Context) error {
	mux := http.NewServeMux()

	// Public
	mux.HandleFunc("/health", s.handleHealth)

	// Authenticated
	mux.HandleFunc("/tasks", s.auth(s.handleTasks))
	mux.HandleFunc("/tasks/", s.auth(s.handleTaskByID))
	mux.HandleFunc("/info", s.auth(s.handleInfo))
	mux.HandleFunc("/agent/status", s.auth(s.handleAgentStatus))
	mux.HandleFunc("/agent/runners", s.auth(s.handleRunners))
	mux.HandleFunc("/agent/runner/restart", s.auth(s.handleRunnerRestart))
	mux.HandleFunc("/agent/runner/switch", s.auth(s.handleRunnerSwitch))
	mux.HandleFunc("/agent/shutdown", s.auth(s.handleShutdown))
	mux.HandleFunc("/agent/clean", s.auth(s.handleClean))
	mux.HandleFunc("/agent/doctor", s.auth(s.handleDoctor))
	mux.HandleFunc("/agent/tools", s.auth(s.handleTools))
	mux.HandleFunc("/schedules", s.auth(s.handleSchedules))
	mux.HandleFunc("/schedules/", s.auth(s.handleScheduleByID))
	mux.HandleFunc("/analytics", s.auth(s.handleAnalytics))
	mux.HandleFunc("/session/list", s.auth(s.handleSessionList))
	mux.HandleFunc("/session/export", s.auth(s.handleSessionExport))
	mux.HandleFunc("/session/import", s.auth(s.handleSessionImport))
	mux.HandleFunc("/tmux/sessions", s.auth(s.handleTmuxSessions))
	mux.HandleFunc("/tmux/adopt", s.auth(s.handleTmuxAdopt))
	mux.HandleFunc("/tmux/detach", s.auth(s.handleTmuxDetach))
	mux.HandleFunc("/tmux/input", s.auth(s.handleTmuxInput))

	// Notifications
	mux.HandleFunc("/notifications/config", s.auth(s.handleNotificationsConfig))
	mux.HandleFunc("/notifications/test", s.auth(s.handleNotificationsTest))

	// Webhooks (public — uses webhook secret instead of auth)
	mux.HandleFunc("/webhooks/trigger", s.handleWebhookTrigger)

	// Exec (remote command execution)
	mux.HandleFunc("/exec", s.auth(s.handleExec))
	mux.HandleFunc("/exec/", s.auth(s.handleExecByID))

	// Tunnels (TCP port tunneling for hot reload)
	mux.HandleFunc("/tunnels", s.auth(s.handleTunnels))
	mux.HandleFunc("/tunnels/", s.auth(s.handleTunnelByID))

	// Tests (automated test sessions)
	mux.HandleFunc("/tests", s.auth(s.handleTests))
	mux.HandleFunc("/tests/", s.auth(s.handleTestByID))

	// Feedback (visual bug reports from device testing) — SDK-accessible
	mux.HandleFunc("/feedback", s.authSDK(s.handleFeedback))
	mux.HandleFunc("/feedback/stream", s.authSDK(s.handleFeedbackStream))
	mux.HandleFunc("/feedback/", s.authSDK(s.handleFeedbackByID))

	// Black box (flight-recorder streaming from device SDKs) — SDK-accessible
	mux.HandleFunc("/blackbox/stream", s.authSDK(s.handleBlackBoxStream))
	mux.HandleFunc("/blackbox/events", s.authSDK(s.handleBlackBoxEvents))
	mux.HandleFunc("/blackbox/logs", s.authSDK(s.handleBlackBoxLogs))
	mux.HandleFunc("/blackbox/subscribe", s.authSDK(s.handleBlackBoxSubscribe))
	mux.HandleFunc("/blackbox/context", s.authSDK(s.handleBlackBoxContext))

	// Dev server (reverse proxy to local Metro/Vite/Flutter dev server)
	mux.HandleFunc("/dev/status", s.authSDK(s.handleDevServerStatus))
	mux.HandleFunc("/dev/start", s.authOrLocalhost(s.handleDevServerStart))
	mux.HandleFunc("/dev/stop", s.authOrLocalhost(s.handleDevServerStop))
	mux.HandleFunc("/dev/reload", s.authSDK(s.handleDevServerReload))
	mux.HandleFunc("/dev/events", s.authSDK(s.handleDevServerEvents))
	mux.HandleFunc("/dev/", s.handleDevServerProxy) // No auth — serves app bundle in WebView (not sensitive)

	// Projects (discovery + workdir switching + actions)
	mux.HandleFunc("/projects", s.auth(s.handleProjects))
	mux.HandleFunc("/projects/switch", s.auth(s.handleProjectSwitch))
	mux.HandleFunc("/projects/actions", s.auth(s.handleProjectActions))
	mux.HandleFunc("/vibing", s.auth(s.handleVibing))
	mux.HandleFunc("/vibing/execute", s.auth(s.handleVibingExecute))
	mux.HandleFunc("/vibing/surprise", s.auth(s.handleVibingSurprise))

	// Todo list (queued bug reports for batch implementation) — SDK-accessible for add/list/count
	mux.HandleFunc("/todolist", s.authSDK(s.handleTodoList))
	mux.HandleFunc("/todolist/count", s.authSDK(s.handleTodoListCount))
	mux.HandleFunc("/todolist/classify", s.authSDK(s.handleTodoListClassify))
	mux.HandleFunc("/todolist/auto-consume", s.auth(s.handleTodoListAutoConsume))
	mux.HandleFunc("/todolist/implement-all", s.auth(s.handleTodoListImplementAll))
	mux.HandleFunc("/todolist/", s.authSDK(s.handleTodoListByID))

	// Multi-user management (shared machines)
	mux.HandleFunc("/users", s.auth(s.handleMultiUserList))
	mux.HandleFunc("/users/me", s.auth(s.handleMultiUserMe))
	mux.HandleFunc("/users/", s.auth(s.handleMultiUserRemove))
	mux.HandleFunc("/sessions", s.auth(s.handleMultiUserSessions))

	// Voice (real-time speech-to-speech & transcription) — SDK-accessible
	mux.HandleFunc("/voice/status", s.authSDK(s.handleVoiceStatus))
	mux.HandleFunc("/voice/transcribe", s.authSDK(s.handleVoiceTranscribe))
	mux.HandleFunc("/voice/providers", s.authSDK(s.handleVoiceProviders))
	mux.HandleFunc("/voice/config", s.authSDK(s.handleVoiceConfig))

	// Agent context (repo switching)
	mux.HandleFunc("/agent/workdir", s.auth(s.handleAgentWorkdir))
	mux.HandleFunc("/agent/context", s.auth(s.handleAgentContext))

	// Builds (remote build & artifact transfer) — SDK-accessible
	mux.HandleFunc("/builds", s.authSDK(s.handleBuilds))
	mux.HandleFunc("/builds/register", s.authSDK(s.handleBuildRegister))
	mux.HandleFunc("/builds/", s.authSDK(s.handleBuildByID))

	// Vault (P2P encrypted key sync)
	mux.HandleFunc("/vault/list", s.auth(s.handleVaultList))
	mux.HandleFunc("/vault/get", s.auth(s.handleVaultGet))
	mux.HandleFunc("/vault/set", s.auth(s.handleVaultSet))
	mux.HandleFunc("/vault/delete", s.auth(s.handleVaultDelete))

	// MCP (Model Context Protocol) endpoint — JSON-RPC 2.0 over HTTP
	mux.HandleFunc("/mcp", s.handleMCP)

	handler := s.ipAllowlist(withCORS(mux))

	s.server = &http.Server{
		Addr:    fmt.Sprintf("0.0.0.0:%d", s.port),
		Handler: handler,
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		s.server.Shutdown(shutdownCtx)
		if s.tlsServer != nil {
			s.tlsServer.Shutdown(shutdownCtx)
		}
	}()

	// Start TLS server alongside HTTP if configured
	if s.tlsPort > 0 && s.tlsFingerprint != "" {
		tlsCfg := &tls.Config{
			Certificates: []tls.Certificate{s.tlsCert},
			MinVersion:   tls.VersionTLS12,
		}
		s.tlsServer = &http.Server{
			Addr:      fmt.Sprintf("0.0.0.0:%d", s.tlsPort),
			Handler:   handler,
			TLSConfig: tlsCfg,
		}
		go func() {
			fpPreview := s.tlsFingerprint
			if len(fpPreview) > 16 {
				fpPreview = fpPreview[:16] + "..."
			}
			log.Printf("HTTPS server listening on 0.0.0.0:%d (fingerprint: %s)", s.tlsPort, fpPreview)
			if err := s.tlsServer.ListenAndServeTLS("", ""); err != nil && err != http.ErrServerClosed {
				log.Printf("[TLS] HTTPS server error: %v", err)
			}
		}()
	}

	log.Printf("HTTP server listening on 0.0.0.0:%d", s.port)
	if len(s.allowedCIDRs) > 0 {
		cidrs := make([]string, len(s.allowedCIDRs))
		for i, c := range s.allowedCIDRs {
			cidrs[i] = c.String()
		}
		log.Printf("IP allowlist: %s", strings.Join(cidrs, ", "))
	}
	err := s.server.ListenAndServe()
	if err == http.ErrServerClosed {
		return nil
	}
	return err
}

// ---------------------------------------------------------------------------
// Cached token info
// ---------------------------------------------------------------------------

type cachedTokenInfo struct {
	userID       string
	isSdk        bool
	scopes       []string
	allowedCIDRs []string
}

// Scope-to-path mapping: which URL paths each scope grants access to.
var scopePathPrefixes = map[string][]string{
	"feedback": {"/feedback"},
	"blackbox": {"/blackbox/"},
	"voice":    {"/voice/"},
	"builds":   {"/builds"},
	"health":   {"/health"},
	"todolist": {"/todolist"},
}

func pathAllowedByScopes(path string, scopes []string) bool {
	for _, scope := range scopes {
		prefixes, ok := scopePathPrefixes[scope]
		if !ok {
			continue
		}
		for _, prefix := range prefixes {
			if strings.HasPrefix(path, prefix) {
				return true
			}
		}
	}
	return false
}

// clientIP extracts the remote IP from the request (strips port).
func clientIP(r *http.Request) string {
	// Check X-Forwarded-For for proxy/relay
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.Split(xff, ",")
		return strings.TrimSpace(parts[0])
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// ipMatchesCIDRs checks if an IP is within any of the given CIDRs.
func ipMatchesCIDRs(ipStr string, cidrs []*net.IPNet) bool {
	ip := net.ParseIP(ipStr)
	if ip == nil {
		return false
	}
	for _, cidr := range cidrs {
		if cidr.Contains(ip) {
			return true
		}
	}
	return false
}

// parseCIDRs parses a list of CIDR strings (also accepts plain IPs).
func parseCIDRs(strs []string) []*net.IPNet {
	var nets []*net.IPNet
	for _, s := range strs {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		// If no /, treat as single IP
		if !strings.Contains(s, "/") {
			if strings.Contains(s, ":") {
				s += "/128"
			} else {
				s += "/32"
			}
		}
		_, cidr, err := net.ParseCIDR(s)
		if err != nil {
			log.Printf("[IP] Warning: invalid CIDR %q: %v", s, err)
			continue
		}
		nets = append(nets, cidr)
	}
	return nets
}

// ---------------------------------------------------------------------------
// Middleware
// ---------------------------------------------------------------------------

// ipAllowlist rejects requests from IPs not in the allowlist.
// If the allowlist is empty, all IPs are allowed.
func (s *HTTPServer) ipAllowlist(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if len(s.allowedCIDRs) > 0 {
			ip := clientIP(r)
			if !ipMatchesCIDRs(ip, s.allowedCIDRs) {
				log.Printf("[IP] %s %s — blocked IP %s (not in allowlist)", r.Method, r.URL.Path, ip)
				jsonError(w, http.StatusForbidden, "IP not allowed")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// auth is for full-access endpoints (tasks, exec, agent commands, vault, etc.).
// Accepts the agent's own token and CLI session tokens. Rejects SDK tokens.
// authOrLocalhost allows requests from localhost without auth (for Claude Code tasks running locally).
func (s *HTTPServer) authOrLocalhost(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Allow localhost without auth
		remoteIP := r.RemoteAddr
		if idx := strings.LastIndex(remoteIP, ":"); idx >= 0 {
			remoteIP = remoteIP[:idx]
		}
		remoteIP = strings.Trim(remoteIP, "[]")
		if remoteIP == "127.0.0.1" || remoteIP == "::1" || remoteIP == "localhost" {
			next(w, r)
			return
		}
		// Otherwise require auth
		s.auth(next)(w, r)
	}
}

func (s *HTTPServer) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		authHeader := r.Header.Get("Authorization")
		if !strings.HasPrefix(authHeader, "Bearer ") {
			log.Printf("[AUTH] %s %s — missing Authorization header", r.Method, r.URL.Path)
			jsonError(w, http.StatusUnauthorized, "missing or invalid Authorization header")
			return
		}
		token := strings.TrimPrefix(authHeader, "Bearer ")

		// Fast path: exact match with the agent's own token
		if token == s.token {
			next(w, r)
			return
		}

		// Check token cache
		if cached, ok := s.tokenCache.Load(token); ok {
			info := cached.(*cachedTokenInfo)
			if info.isSdk {
				// SDK tokens not allowed for full-access endpoints
				jsonError(w, http.StatusForbidden, "SDK tokens cannot access this endpoint")
				return
			}
			if info.userID == s.ownerUserID {
				next(w, r)
				return
			}
			jsonError(w, http.StatusForbidden, "token belongs to a different user")
			return
		}

		// Validate session token via Convex
		log.Printf("[AUTH] %s %s — validating token against Convex...", r.Method, r.URL.Path)
		uid, err := ValidateTokenUser(s.convexURL, token)
		if err != nil {
			log.Printf("[AUTH] %s %s — token validation failed: %v", r.Method, r.URL.Path, err)
			jsonError(w, http.StatusForbidden, "invalid token")
			return
		}
		s.tokenCache.Store(token, &cachedTokenInfo{userID: uid, isSdk: false})

		if uid != s.ownerUserID {
			jsonError(w, http.StatusForbidden, "token belongs to a different user")
			return
		}
		next(w, r)
	}
}

// authSDK is for SDK-accessible endpoints (feedback, blackbox, voice, builds).
// Accepts all token types: agent's own, CLI session, and SDK tokens (with scope check).
func (s *HTTPServer) authSDK(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		authHeader := r.Header.Get("Authorization")
		if !strings.HasPrefix(authHeader, "Bearer ") {
			jsonError(w, http.StatusUnauthorized, "missing or invalid Authorization header")
			return
		}
		token := strings.TrimPrefix(authHeader, "Bearer ")

		// Fast path: agent's own token (full access)
		if token == s.token {
			next(w, r)
			return
		}

		// Check cache
		if cached, ok := s.tokenCache.Load(token); ok {
			info := cached.(*cachedTokenInfo)
			if info.userID != s.ownerUserID {
				jsonError(w, http.StatusForbidden, "token belongs to a different user")
				return
			}
			if info.isSdk {
				// Check scope
				if !pathAllowedByScopes(r.URL.Path, info.scopes) {
					jsonError(w, http.StatusForbidden, "SDK token scope does not allow this endpoint")
					return
				}
				// Check IP binding
				if len(info.allowedCIDRs) > 0 {
					cidrs := parseCIDRs(info.allowedCIDRs)
					if !ipMatchesCIDRs(clientIP(r), cidrs) {
						jsonError(w, http.StatusForbidden, "SDK token not allowed from this IP")
						return
					}
				}
				// Track new device IPs
				s.trackNewIP(token, r)
			}
			next(w, r)
			return
		}

		// Try session token first
		uid, err := ValidateTokenUser(s.convexURL, token)
		if err == nil {
			s.tokenCache.Store(token, &cachedTokenInfo{userID: uid, isSdk: false})
			if uid != s.ownerUserID {
				jsonError(w, http.StatusForbidden, "token belongs to a different user")
				return
			}
			next(w, r)
			return
		}

		// Try SDK token
		sdkInfo, sdkErr := ValidateSdkTokenFull(s.convexURL, token)
		if sdkErr != nil {
			log.Printf("[AUTH] %s %s — all token validation failed", r.Method, r.URL.Path)
			jsonError(w, http.StatusForbidden, "invalid token")
			return
		}

		info := &cachedTokenInfo{
			userID:       sdkInfo.UserID,
			isSdk:        true,
			scopes:       sdkInfo.Scopes,
			allowedCIDRs: sdkInfo.AllowedCIDRs,
		}
		s.tokenCache.Store(token, info)

		if sdkInfo.UserID != s.ownerUserID {
			jsonError(w, http.StatusForbidden, "token belongs to a different user")
			return
		}

		// Check scope
		if !pathAllowedByScopes(r.URL.Path, sdkInfo.Scopes) {
			jsonError(w, http.StatusForbidden, "SDK token scope does not allow this endpoint")
			return
		}

		// Check IP binding
		if len(sdkInfo.AllowedCIDRs) > 0 {
			cidrs := parseCIDRs(sdkInfo.AllowedCIDRs)
			if !ipMatchesCIDRs(clientIP(r), cidrs) {
				jsonError(w, http.StatusForbidden, "SDK token not allowed from this IP")
				return
			}
		}

		// Track new device IPs
		s.trackNewIP(token, r)

		next(w, r)
	}
}

// trackNewIP records the first time an SDK token is used from a new IP.
func (s *HTTPServer) trackNewIP(token string, r *http.Request) {
	ip := clientIP(r)
	prefix := token
	if len(prefix) > 8 {
		prefix = prefix[:8]
	}
	seenKey := prefix + "_" + ip
	if _, loaded := s.seenIPs.LoadOrStore(seenKey, true); !loaded {
		log.Printf("[SECURITY] New IP %s for SDK token %s...", ip, prefix)
		go ReportSecurityEvent(s.convexURL, s.token, "new_ip", map[string]interface{}{
			"ip":          ip,
			"tokenPrefix": prefix,
			"path":        r.URL.Path,
		})
	}
}

func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// ---------------------------------------------------------------------------
// Handlers
// ---------------------------------------------------------------------------

func (s *HTTPServer) handleHealth(w http.ResponseWriter, r *http.Request) {
	resp := map[string]interface{}{
		"ok":       true,
		"hostname": s.hostname,
		"version":  version,
	}
	if s.tlsFingerprint != "" {
		resp["tlsFingerprint"] = s.tlsFingerprint
		resp["tlsPort"] = s.tlsPort
	}
	jsonReply(w, http.StatusOK, resp)
}

func (s *HTTPServer) handleInfo(w http.ResponseWriter, r *http.Request) {
	hostname, _ := os.Hostname()
	info := map[string]interface{}{
		"ok":       true,
		"hostname": hostname,
		"version":  version,
		"workDir":  s.taskMgr.workDir,
	}

	// Project metadata
	project := DetectProjectInfo(s.taskMgr.workDir)
	info["project"] = project

	// Dev server status (for hot-reload awareness)
	if s.devServerMgr != nil {
		if devStatus := s.devServerMgr.Status(); devStatus != nil {
			info["devServer"] = devStatus
		}
	}

	// Todo list count + stats
	if s.todolistMgr != nil {
		items := s.todolistMgr.ListItems()
		pending, implementing, done, failed := 0, 0, 0, 0
		for _, item := range items {
			switch item.Status {
			case TodoStatusPending:
				pending++
			case TodoStatusImplementing:
				implementing++
			case TodoStatusDone:
				done++
			case TodoStatusFailed:
				failed++
			}
		}
		info["todoCount"] = pending
		info["todoTotal"] = len(items)
		info["todoDone"] = done
		info["todoFailed"] = failed
		info["todoImplementing"] = implementing
		info["autoConsume"] = s.todolistMgr.IsAutoConsume()
	}

	// Session task stats
	if s.taskMgr != nil {
		tasks := s.taskMgr.ListTasks()
		taskTotal := len(tasks)
		taskDone := 0
		taskRunning := 0
		taskFailed := 0
		for _, t := range tasks {
			switch t.Status {
			case TaskStatusFinished:
				taskDone++
			case TaskStatusRunning:
				taskRunning++
			case TaskStatusFailed:
				taskFailed++
			}
		}
		info["taskStats"] = map[string]int{
			"total":   taskTotal,
			"done":    taskDone,
			"running": taskRunning,
			"failed":  taskFailed,
		}
	}

	// Auto-start / systemd status
	autoStart := map[string]interface{}{"configured": false}
	home, _ := os.UserHomeDir()
	// Check systemd
	if _, err := os.Stat(filepath.Join(home, ".config", "systemd", "user", "yaver.service")); err == nil {
		autoStart["configured"] = true
		autoStart["type"] = "systemd"
	}
	// Check macOS LaunchAgent
	if _, err := os.Stat(filepath.Join(home, "Library", "LaunchAgents", "io.yaver.agent.plist")); err == nil {
		autoStart["configured"] = true
		autoStart["type"] = "launchd"
	}
	info["autoStart"] = autoStart

	// Voice capability — always true (mobile can always send audio)
	info["voiceInputEnabled"] = true

	// S2S provider status
	cfg, _ := LoadConfig()
	if cfg != nil && cfg.Voice != nil && cfg.Voice.S2SProvider != "" {
		if p, ok := GetVoiceProvider(cfg.Voice.S2SProvider); ok {
			status := p.Status()
			info["voiceProvider"] = status.Provider
			info["voiceReady"] = status.Ready
		}
	}
	// STT available
	if cfg != nil && cfg.Speech != nil && cfg.Speech.Provider != "" {
		info["sttProvider"] = cfg.Speech.Provider
	}

	jsonReply(w, http.StatusOK, info)
}

// handleAgentStatus returns detailed agent and runner health status.
// handleProjects lists discovered projects on this machine.
func (s *HTTPServer) handleProjects(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, http.StatusMethodNotAllowed, "use GET")
		return
	}

	projects := listDiscoveredProjects()
	type projectResp struct {
		Name      string   `json:"name"`
		Path      string   `json:"path"`
		Branch    string   `json:"branch,omitempty"`
		Framework string   `json:"framework,omitempty"`
		GitRemote string   `json:"gitRemote,omitempty"`
		Tags      []string `json:"tags,omitempty"`
	}
	// Filter out non-deployable projects: dotfiles, editor configs, vim plugins, etc.
	// Only show things a solo dev would actually deploy: apps, websites, backends, APIs.
	skipPrefixes := []string{".", "_"}
	skipNames := map[string]bool{
		// System / OS dirs
		"node_modules": true, "vendor": true, "dist": true, "build": true,
		"Library": true, "Applications": true, "Music": true, "Movies": true,
		"Pictures": true, "Documents": true, "Public": true, "Downloads": true,
		"Desktop": true, "Trash": true,
		// Editor / shell configs
		"vim": true, "nvim": true, "neovim": true, "emacs": true,
		"tmux": true, "zsh": true, "bash": true, "fish": true,
		"oh-my-zsh": true, "powerlevel10k": true, "spacemacs": true,
		// Vim plugin managers / bundles
		"bundle": true, "plugged": true, "pack": true,
	}
	// Skip paths containing these segments (vim bundles, dotfile managers, etc.)
	skipPathSegments := []string{
		"/vim/bundle/", "/vim/plugged/", "/vim/pack/",
		"/.vim/", "/.config/nvim/", "/.tmux/", "/.oh-my-zsh/",
		"/.local/share/", "/.cache/", "/.cargo/",
		"/homebrew/", "/Cellar/", "/Caskroom/",
	}

	result := make([]projectResp, 0, len(projects))
	for _, p := range projects {
		name := filepath.Base(p.Path)
		// Skip dotfiles and config dirs
		skip := false
		for _, prefix := range skipPrefixes {
			if strings.HasPrefix(name, prefix) {
				skip = true
				break
			}
		}
		if skip || skipNames[name] {
			continue
		}
		// Skip paths inside editor/config directories
		for _, seg := range skipPathSegments {
			if strings.Contains(p.Path, seg) {
				skip = true
				break
			}
		}
		if skip {
			continue
		}
		// Only show projects that look deployable
		// Check root AND immediate subdirs for build system files (monorepo support)
		hasDeployable := false
		dirsToCheck := []string{p.Path}
		if subs, err := os.ReadDir(p.Path); err == nil {
			for _, sub := range subs {
				if sub.IsDir() && !strings.HasPrefix(sub.Name(), ".") && sub.Name() != "node_modules" {
					dirsToCheck = append(dirsToCheck, filepath.Join(p.Path, sub.Name()))
				}
			}
		}
		for _, dir := range dirsToCheck {
			if fileExists(filepath.Join(dir, "package.json")) || fileExists(filepath.Join(dir, "pubspec.yaml")) ||
				fileExists(filepath.Join(dir, "go.mod")) || fileExists(filepath.Join(dir, "Cargo.toml")) ||
				fileExists(filepath.Join(dir, "Dockerfile")) || fileExists(filepath.Join(dir, "docker-compose.yml")) ||
				fileExists(filepath.Join(dir, "docker-compose.yaml")) || fileExists(filepath.Join(dir, "pyproject.toml")) ||
				fileExists(filepath.Join(dir, "requirements.txt")) || fileExists(filepath.Join(dir, "Makefile")) {
				hasDeployable = true
				break
			}
		}
		if !hasDeployable {
			continue
		}
		info := DetectProjectInfo(p.Path)
		// Derive tags from actions — covers monorepos (mobile/ + web/ + backend/)
		actions := DetectProjectActions(p.Path)
		tagSet := map[string]bool{}
		for _, a := range actions {
			if a.Framework != "" {
				tagSet[a.Framework] = true
			}
			if a.Platform != "" {
				tagSet[a.Platform] = true
			}
			// Infer high-level tags
			switch a.Type {
			case "dev-server":
				if a.Framework == "expo" || a.Framework == "flutter" {
					tagSet["mobile"] = true
				} else {
					tagSet["web"] = true
				}
			case "deploy":
				if a.Platform == "testflight" || a.Platform == "playstore" {
					tagSet["mobile"] = true
				}
			}
		}
		// Also add language-level tags
		for _, t := range DetectProjectTags(p.Path) {
			tagSet[t] = true
		}
		tags := make([]string, 0, len(tagSet))
		for t := range tagSet {
			tags = append(tags, t)
		}
		result = append(result, projectResp{
			Name:      name,
			Path:      p.Path,
			Branch:    p.Branch,
			Framework: info.Framework,
			GitRemote: info.GitRemote,
			Tags:      tags,
		})
	}

	jsonReply(w, http.StatusOK, map[string]interface{}{
		"ok":         true,
		"projects":   result,
		"currentDir": s.taskMgr.workDir,
	})
}

// handleProjectSwitch changes the agent's working directory + optionally starts dev server.
func (s *HTTPServer) handleProjectSwitch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}

	var req struct {
		Query    string `json:"query"`    // fuzzy project name
		Path     string `json:"path"`     // or explicit path
		StartDev bool   `json:"startDev"` // also start dev server
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	projectPath := req.Path
	if projectPath == "" && req.Query != "" {
		found, err := findProject(req.Query)
		if err != nil {
			jsonError(w, http.StatusNotFound, fmt.Sprintf("project not found: %v", err))
			return
		}
		projectPath = found
	}

	if projectPath == "" {
		jsonError(w, http.StatusBadRequest, "query or path required")
		return
	}

	// Switch workdir
	s.taskMgr.mu.Lock()
	s.taskMgr.workDir = projectPath
	s.taskMgr.mu.Unlock()
	log.Printf("[projects] Switched to %s", projectPath)

	resp := map[string]interface{}{
		"ok":      true,
		"path":    projectPath,
		"project": DetectProjectInfo(projectPath),
	}

	// Optionally start dev server
	if req.StartDev && s.devServerMgr != nil {
		if status := s.devServerMgr.Status(); status != nil {
			// Already running — stop first
			s.devServerMgr.Stop()
		}
		framework := DetectProjectInfo(projectPath).Framework
		if err := s.devServerMgr.Start(framework, projectPath, "", 0); err != nil {
			resp["devServerError"] = err.Error()
		} else {
			resp["devServer"] = s.devServerMgr.Status()
		}
	}

	jsonReply(w, http.StatusOK, resp)
}

// handleProjectActions returns available actions for a project (deploy targets, dev servers, builds).
func (s *HTTPServer) handleProjectActions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, http.StatusMethodNotAllowed, "use GET")
		return
	}

	path := r.URL.Query().Get("path")
	query := r.URL.Query().Get("query")

	if path == "" && query != "" {
		found, err := findProject(query)
		if err != nil {
			jsonError(w, http.StatusNotFound, "project not found: "+err.Error())
			return
		}
		path = found
	}
	if path == "" {
		path = s.taskMgr.workDir
	}

	actions := DetectProjectActions(path)
	jsonReply(w, http.StatusOK, map[string]interface{}{
		"ok":      true,
		"project": filepath.Base(path),
		"path":    path,
		"actions": actions,
	})
}

func (s *HTTPServer) handleAgentStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, http.StatusMethodNotAllowed, "use GET")
		return
	}
	status := s.taskMgr.GetAgentStatus()
	jsonReply(w, http.StatusOK, map[string]interface{}{
		"ok":     true,
		"status": status,
	})
}

// handleRunnerRestart checks if the runner is healthy and clears the runnerDown flag.
// Mobile can call this to "restart" the runner after all retries were exhausted.
// handleRunners returns all available runners with their install status and models.
func (s *HTTPServer) handleRunners(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, http.StatusMethodNotAllowed, "use GET")
		return
	}

	type modelInfo struct {
		ID          string `json:"id"`
		Name        string `json:"name"`
		Description string `json:"description,omitempty"`
		IsDefault   bool   `json:"isDefault,omitempty"`
	}

	type runnerInfo struct {
		ID        string      `json:"id"`
		Name      string      `json:"name"`
		Command   string      `json:"command"`
		Installed bool        `json:"installed"`
		IsDefault bool        `json:"isDefault"`
		Models    []modelInfo `json:"models"`
	}

	// Build models index by runner
	modelsByRunner := make(map[string][]modelInfo)
	for _, m := range GetCachedModels() {
		modelsByRunner[m.RunnerID] = append(modelsByRunner[m.RunnerID], modelInfo{
			ID:          m.ModelID,
			Name:        m.Name,
			Description: m.Description,
			IsDefault:   m.IsDefault,
		})
	}

	var runners []runnerInfo
	seenIDs := make(map[string]bool)

	// Add default runner first, then others sorted by ID
	defaultID := s.taskMgr.runner.RunnerID
	addRunner := func(r RunnerConfig) {
		if seenIDs[r.RunnerID] {
			return
		}
		_, err := osexec.LookPath(r.Command)
		runners = append(runners, runnerInfo{
			ID:        r.RunnerID,
			Name:      r.Name,
			Command:   r.Command,
			Installed: err == nil,
			IsDefault: r.RunnerID == defaultID,
			Models:    modelsByRunner[r.RunnerID],
		})
		seenIDs[r.RunnerID] = true
	}

	// Default runner first
	if r, ok := builtinRunners[defaultID]; ok {
		addRunner(r)
	}
	// Then rest in stable order
	for _, id := range []string{"claude", "codex", "aider", "goose", "ollama", "amp", "opencode"} {
		if r, ok := builtinRunners[id]; ok {
			addRunner(r)
		}
	}
	// Any remaining runners from Convex
	for _, r := range builtinRunners {
		addRunner(r)
	}

	// Include the active runner if it's custom (not in builtinRunners)
	if !seenIDs[s.taskMgr.runner.RunnerID] {
		runners = append(runners, runnerInfo{
			ID:        s.taskMgr.runner.RunnerID,
			Name:      s.taskMgr.runner.Name,
			Command:   s.taskMgr.runner.Command,
			Installed: true,
			IsDefault: true,
			Models:    nil, // custom runners have no predefined models
		})
	}

	jsonReply(w, http.StatusOK, map[string]interface{}{
		"ok":      true,
		"runners": runners,
		"default": s.taskMgr.runner.RunnerID,
	})
}

func (s *HTTPServer) handleRunnerRestart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}

	// Check runner health
	if err := s.taskMgr.CheckRunner(); err != nil {
		jsonError(w, http.StatusServiceUnavailable, fmt.Sprintf("runner not available: %v", err))
		return
	}

	// Clear runnerDown flag in Convex
	if s.taskMgr.ConvexURL != "" {
		go func() {
			_ = SetRunnerDown(s.taskMgr.ConvexURL, s.taskMgr.AuthToken, s.taskMgr.DeviceID, false)
			_ = ReportDeviceEvent(s.taskMgr.ConvexURL, s.taskMgr.AuthToken, s.taskMgr.DeviceID, "restart", "manual restart from mobile")
		}()
	}

	log.Printf("[HTTP] Runner restart triggered from mobile — runner is healthy")
	jsonReply(w, http.StatusOK, map[string]interface{}{
		"ok":      true,
		"message": "Runner is healthy, runnerDown flag cleared",
	})
}

// handleRunnerSwitch switches the active runner. Validates the binary exists first.
func (s *HTTPServer) handleRunnerSwitch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}

	var body struct {
		RunnerID string `json:"runnerId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if body.RunnerID == "" {
		jsonError(w, http.StatusBadRequest, "runnerId is required")
		return
	}

	// Map runner IDs to commands
	runnerCommands := map[string]string{
		"claude": "claude",
		"codex":  "codex",
		"aider":  "aider",
	}

	cmd, known := runnerCommands[body.RunnerID]
	if !known {
		jsonError(w, http.StatusBadRequest, fmt.Sprintf("unknown runner: %s (available: claude, codex, aider)", body.RunnerID))
		return
	}

	// Check if binary exists on this machine
	path, err := osexec.LookPath(cmd)
	if err != nil {
		log.Printf("[HTTP] Runner switch failed: %s not found on machine", cmd)
		jsonError(w, http.StatusNotFound, fmt.Sprintf("%s is not installed on this machine", cmd))
		return
	}

	// Build new runner config
	var newRunner RunnerConfig
	switch body.RunnerID {
	case "claude":
		newRunner = defaultRunner
	case "codex":
		newRunner = RunnerConfig{
			RunnerID: "codex",
			Name:     "OpenAI Codex",
			Command:  "codex",
			Args:     []string{"--quiet", "--full-auto", "{prompt}"},
			OutputMode: "raw",
		}
	case "aider":
		newRunner = RunnerConfig{
			RunnerID: "aider",
			Name:     "Aider",
			Command:  "aider",
			Args:     []string{"--yes-always", "--no-git", "--message", "{prompt}"},
			OutputMode:  "raw",
			ExitCommand: "/quit",
		}
	}

	// Update the task manager's runner
	s.taskMgr.mu.Lock()
	s.taskMgr.runner = newRunner
	s.taskMgr.mu.Unlock()

	log.Printf("[HTTP] Runner switched to %s (%s) at %s", newRunner.Name, body.RunnerID, path)

	// Also save to Convex user settings (non-blocking)
	if s.taskMgr.ConvexURL != "" {
		go func() {
			payload, _ := json.Marshal(map[string]string{"runnerId": body.RunnerID})
			req, err := newBearerRequest("POST", s.taskMgr.ConvexURL+"/settings", s.taskMgr.AuthToken, bytes.NewReader(payload))
			if err == nil {
				resp, err := http.DefaultClient.Do(req)
				if err == nil {
					resp.Body.Close()
				}
			}
		}()
	}

	jsonReply(w, http.StatusOK, map[string]interface{}{
		"ok":       true,
		"runner":   newRunner.Name,
		"runnerId": body.RunnerID,
		"path":     path,
	})
}

// handleShutdown gracefully shuts down the yaver agent. Called from mobile.
func (s *HTTPServer) handleShutdown(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}

	log.Printf("[HTTP] Shutdown requested from mobile")

	// Stop all running tasks first
	stopped := s.taskMgr.StopAllTasks()
	log.Printf("[HTTP] Stopped %d tasks before shutdown", stopped)

	// Report event to Convex
	if s.taskMgr.ConvexURL != "" {
		go func() {
			_ = ReportDeviceEvent(s.taskMgr.ConvexURL, s.taskMgr.AuthToken, s.taskMgr.DeviceID, "stopped", "shutdown from mobile")
			_ = MarkOffline(s.taskMgr.ConvexURL, s.taskMgr.AuthToken, s.taskMgr.DeviceID)
		}()
	}

	jsonReply(w, http.StatusOK, map[string]interface{}{
		"ok":      true,
		"message": "Agent shutting down",
		"stopped": stopped,
	})

	// Trigger shutdown after response is sent
	if s.onShutdown != nil {
		go func() {
			time.Sleep(500 * time.Millisecond) // let response flush
			s.onShutdown()
		}()
	}
}

// handleClean removes old tasks, images, and logs. Called from mobile settings.
func (s *HTTPServer) handleClean(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}
	var body struct {
		Days int  `json:"days"`
		All  bool `json:"all"`
	}
	json.NewDecoder(r.Body).Decode(&body)
	if body.Days == 0 {
		body.Days = 30
	}

	result := performClean(body.Days, body.All, false)
	log.Printf("[HTTP] Clean: removed %d tasks, %d image dirs, freed %s", result.TasksRemoved, result.ImagesRemoved, formatBytes(result.BytesFreed))
	jsonReply(w, http.StatusOK, map[string]interface{}{
		"ok":     true,
		"result": result,
	})
}

// handleTasks handles GET /tasks (list) and POST /tasks (create).
func (s *HTTPServer) handleTasks(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.listTasks(w, r)
	case http.MethodPost:
		s.createTask(w, r)
	case http.MethodDelete:
		count := s.taskMgr.DeleteAllTasks()
		jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true, "deleted": count})
	default:
		jsonError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *HTTPServer) listTasks(w http.ResponseWriter, r *http.Request) {
	tasks := s.taskMgr.ListTasks()
	resp := map[string]interface{}{
		"ok":    true,
		"tasks": tasks,
	}
	// Include project context so mobile can display project chips
	project := DetectProjectInfo(s.taskMgr.workDir)
	resp["project"] = project
	// Include todo stats
	if s.todolistMgr != nil {
		resp["todoCount"] = s.todolistMgr.Count()
		resp["todoTotal"] = len(s.todolistMgr.ListItems())
	}
	jsonReply(w, http.StatusOK, resp)
}

func (s *HTTPServer) createTask(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Title         string            `json:"title"`
		Description   string            `json:"description"`
		Model         string            `json:"model"`
		Runner        string            `json:"runner"`        // runner ID: "claude", "codex", "aider" — empty uses default
		CustomCommand string            `json:"customCommand"` // arbitrary command — runs via sh -c
		Source        string            `json:"source"`        // client type: "mobile", "desktop-app", "web", "cli"
		SpeechContext *SpeechContext     `json:"speechContext"` // voice input/output preferences
		Images        []ImageAttachment `json:"images,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if body.Title == "" {
		jsonError(w, http.StatusBadRequest, "title is required")
		return
	}

	source := body.Source
	if source == "" {
		// Fall back to header, then default
		source = r.Header.Get("X-Yaver-Source")
	}
	if source == "" {
		source = "mobile"
	}

	task, err := s.taskMgr.CreateTask(body.Title, body.Description, body.Model, source, body.Runner, body.CustomCommand, body.Images, body.SpeechContext)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, fmt.Sprintf("failed to create task: %v", err))
		return
	}

	log.Printf("[HTTP] Task created: %s — %s (status: %s, model: %s, runner: %s)", task.ID, task.Title, task.Status, body.Model, task.RunnerID)
	project := DetectProjectInfo(s.taskMgr.workDir)
	resp := map[string]interface{}{
		"ok":       true,
		"taskId":   task.ID,
		"status":   task.Status,
		"runnerId": task.RunnerID,
		"project":  project.Name,
	}
	log.Printf("[HTTP] Sending create response for task %s", task.ID)
	jsonReply(w, http.StatusCreated, resp)
	log.Printf("[HTTP] Response sent for task %s", task.ID)
}

// handleTaskByID routes /tasks/{id}, /tasks/{id}/output, /tasks/{id}/stop, /tasks/{id}/continue
func (s *HTTPServer) handleTaskByID(w http.ResponseWriter, r *http.Request) {
	// Parse path: /tasks/{id}[/action]
	path := strings.TrimPrefix(r.URL.Path, "/tasks/")
	parts := strings.SplitN(path, "/", 2)
	taskID := parts[0]
	action := ""
	if len(parts) > 1 {
		action = parts[1]
	}

	if taskID == "" {
		jsonError(w, http.StatusBadRequest, "task ID required")
		return
	}

	if taskID == "stop-all" {
		s.handleStopAll(w, r)
		return
	}
	if taskID == "delete-all" {
		s.handleDeleteAll(w, r)
		return
	}

	switch action {
	case "":
		if r.Method == http.MethodDelete {
			s.deleteTask(w, r, taskID)
		} else {
			s.getTask(w, r, taskID)
		}
	case "output":
		s.streamOutput(w, r, taskID)
	case "stop":
		s.stopTask(w, r, taskID)
	case "exit":
		s.exitTask(w, r, taskID)
	case "continue":
		s.continueTask(w, r, taskID)
	default:
		jsonError(w, http.StatusNotFound, "unknown action")
	}
}

func (s *HTTPServer) getTask(w http.ResponseWriter, r *http.Request, id string) {
	log.Printf("[HTTP] GET task %s", id)
	task, ok := s.taskMgr.GetTask(id)
	if !ok {
		log.Printf("[HTTP] Task %s not found", id)
		jsonError(w, http.StatusNotFound, "task not found")
		return
	}

	s.taskMgr.mu.RLock()
	output := task.Output
	if len(output) > 10000 {
		output = output[len(output)-10000:]
	}
	info := TaskInfo{
		ID:          task.ID,
		Title:       task.Title,
		Description: task.Description,
		Status:      task.Status,
		RunnerID:    task.RunnerID,
		SessionID:   task.SessionID,
		Output:      output,
		ResultText:  task.ResultText,
		CostUSD:     task.CostUSD,
		Turns:       task.Turns,
		Source:      task.Source,
		TmuxSession: task.TmuxSession,
		IsAdopted:   task.IsAdopted,
		CreatedAt:   task.CreatedAt,
		StartedAt:   task.StartedAt,
		FinishedAt:  task.FinishedAt,
	}
	s.taskMgr.mu.RUnlock()

	log.Printf("[HTTP] Task %s status=%s output_len=%d", id, info.Status, len(info.Output))
	jsonReply(w, http.StatusOK, map[string]interface{}{
		"ok":   true,
		"task": info,
	})
}

// streamOutput streams task output as Server-Sent Events (SSE).
func (s *HTTPServer) streamOutput(w http.ResponseWriter, r *http.Request, id string) {
	log.Printf("[HTTP] SSE stream requested for task %s", id)
	task, ok := s.taskMgr.GetTask(id)
	if !ok {
		log.Printf("[HTTP] SSE task %s not found", id)
		jsonError(w, http.StatusNotFound, "task not found")
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		jsonError(w, http.StatusInternalServerError, "streaming not supported")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	ctx := r.Context()

	// First send any existing output as initial data.
	s.taskMgr.mu.RLock()
	existingOutput := task.Output
	currentStatus := task.Status
	s.taskMgr.mu.RUnlock()

	if existingOutput != "" {
		fmt.Fprintf(w, "data: %s\n\n", jsonString(map[string]interface{}{
			"type": "output",
			"text": existingOutput,
		}))
		flusher.Flush()
	}

	// If already finished, send done event and return.
	if currentStatus == TaskStatusFinished || currentStatus == TaskStatusFailed || currentStatus == TaskStatusStopped {
		fmt.Fprintf(w, "data: %s\n\n", jsonString(map[string]interface{}{
			"type":   "done",
			"status": currentStatus,
		}))
		flusher.Flush()
		return
	}

	// Stream live output from the channel.
	for {
		select {
		case <-ctx.Done():
			return
		case text, ok := <-task.outputCh:
			if !ok {
				// Channel closed — task finished.
				s.taskMgr.mu.RLock()
				finalStatus := task.Status
				s.taskMgr.mu.RUnlock()
				fmt.Fprintf(w, "data: %s\n\n", jsonString(map[string]interface{}{
					"type":   "done",
					"status": finalStatus,
				}))
				flusher.Flush()
				return
			}
			fmt.Fprintf(w, "data: %s\n\n", jsonString(map[string]interface{}{
				"type": "output",
				"text": text,
			}))
			flusher.Flush()
		}
	}
}

func (s *HTTPServer) stopTask(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}

	if err := s.taskMgr.StopTask(id); err != nil {
		jsonError(w, http.StatusNotFound, err.Error())
		return
	}

	log.Printf("[HTTP] Task stopped: %s", id)
	jsonReply(w, http.StatusOK, map[string]interface{}{
		"ok":     true,
		"taskId": id,
		"status": TaskStatusStopped,
	})
}

func (s *HTTPServer) exitTask(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}

	if err := s.taskMgr.GracefulStopTask(id); err != nil {
		jsonError(w, http.StatusNotFound, err.Error())
		return
	}

	log.Printf("[HTTP] Task gracefully exited: %s", id)
	jsonReply(w, http.StatusOK, map[string]interface{}{
		"ok":     true,
		"taskId": id,
		"status": TaskStatusStopped,
	})
}

func (s *HTTPServer) deleteTask(w http.ResponseWriter, r *http.Request, id string) {
	if err := s.taskMgr.DeleteTask(id); err != nil {
		jsonError(w, http.StatusBadRequest, err.Error())
		return
	}
	log.Printf("[HTTP] Task deleted: %s", id)
	jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true})
}

func (s *HTTPServer) handleStopAll(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}
	count := s.taskMgr.StopAllTasks()
	log.Printf("[HTTP] Stopped all tasks: %d", count)
	jsonReply(w, http.StatusOK, map[string]interface{}{
		"ok":      true,
		"stopped": count,
	})
}

func (s *HTTPServer) handleDeleteAll(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		jsonError(w, http.StatusMethodNotAllowed, "use DELETE")
		return
	}
	count := s.taskMgr.DeleteAllTasks()
	log.Printf("[HTTP] Deleted all tasks: %d", count)
	jsonReply(w, http.StatusOK, map[string]interface{}{
		"ok":      true,
		"deleted": count,
	})
}

func (s *HTTPServer) continueTask(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}

	var body struct {
		Input  string            `json:"input"`
		Images []ImageAttachment `json:"images,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if body.Input == "" {
		jsonError(w, http.StatusBadRequest, "input is required")
		return
	}

	task, err := s.taskMgr.ResumeTask(id, body.Input, body.Images)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, fmt.Sprintf("resume failed: %v", err))
		return
	}

	log.Printf("[HTTP] Task resumed: %s (session=%s)", id, task.SessionID)
	jsonReply(w, http.StatusOK, map[string]interface{}{
		"ok":     true,
		"taskId": task.ID,
		"status": task.Status,
	})
}

// ---------------------------------------------------------------------------
// Doctor & Tools handlers
// ---------------------------------------------------------------------------

// handleDoctor runs system diagnostics and returns results as JSON.
func (s *HTTPServer) handleDoctor(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, http.StatusMethodNotAllowed, "use GET")
		return
	}

	type checkResult struct {
		Name    string `json:"name"`
		Status  string `json:"status"` // "pass", "warn", "fail"
		Detail  string `json:"detail"`
		Section string `json:"section"`
	}

	var checks []checkResult

	addCheck := func(section, name, status, detail string) {
		checks = append(checks, checkResult{Name: name, Status: status, Detail: detail, Section: section})
	}

	// Config
	cfg, err := LoadConfig()
	if err != nil {
		addCheck("config", "Config file", "fail", fmt.Sprintf("Error: %v", err))
	} else {
		p, _ := ConfigPath()
		addCheck("config", "Config file", "pass", p)
	}
	addCheck("config", "Version", "pass", version)

	// Auth
	if cfg == nil || cfg.AuthToken == "" {
		addCheck("auth", "Auth token", "fail", "Not signed in")
	} else {
		addCheck("auth", "Auth token", "pass", "Present")
		if cfg.DeviceID != "" {
			addCheck("auth", "Device ID", "pass", cfg.DeviceID[:8]+"...")
		} else {
			addCheck("auth", "Device ID", "fail", "Not set")
		}
	}

	// Agent
	agentPID, agentRunning := isAgentRunning()
	if agentRunning {
		addCheck("agent", "Agent process", "pass", fmt.Sprintf("Running (PID %d)", agentPID))
	} else {
		addCheck("agent", "Agent process", "warn", "Not running")
	}

	if tmuxAvailable() {
		addCheck("agent", "Tmux", "pass", "available")
	} else {
		addCheck("agent", "Tmux", "warn", "not installed")
	}

	// HTTP server
	statusClient := &http.Client{Timeout: 3 * time.Second}
	if resp, err := statusClient.Get("http://127.0.0.1:18080/health"); err == nil {
		resp.Body.Close()
		addCheck("agent", "HTTP server", "pass", "Listening on :18080")
	} else {
		addCheck("agent", "HTTP server", "warn", "Not reachable on port 18080")
	}

	// AI Runners
	runners := []struct{ id, name, cmd, install string }{
		{"claude", "Claude Code", "claude", "npm install -g @anthropic-ai/claude-code"},
		{"codex", "OpenAI Codex", "codex", "npm install -g @openai/codex"},
		{"aider", "Aider", "aider", "pip install aider-chat"},
		{"ollama", "Ollama", "ollama", "brew install ollama"},
		{"goose", "Goose", "goose", "pip install goose-ai"},
		{"amp", "Amp", "amp", "npm install -g @anthropic/amp"},
		{"opencode", "OpenCode", "opencode", "go install github.com/mbreithecker/opencode@latest"},
	}
	for _, r := range runners {
		p, err := osexec.LookPath(r.cmd)
		if err != nil {
			addCheck("runners", r.name, "warn", "Not installed — "+r.install)
		} else {
			out, verr := osexec.Command(r.cmd, "--version").CombinedOutput()
			if verr == nil {
				ver := strings.TrimSpace(strings.Split(string(out), "\n")[0])
				if len(ver) > 60 {
					ver = ver[:60]
				}
				addCheck("runners", r.name, "pass", fmt.Sprintf("%s (%s)", p, ver))
			} else {
				addCheck("runners", r.name, "pass", p)
			}
		}
	}

	// Relay servers
	if cfg != nil && len(cfg.RelayServers) > 0 {
		relayClient := &http.Client{Timeout: 5 * time.Second}
		for _, rs := range cfg.RelayServers {
			label := rs.Label
			if label == "" {
				label = rs.ID
			}
			start := time.Now()
			resp, err := relayClient.Get(rs.HttpURL + "/health")
			rtt := time.Since(start)
			if err != nil {
				addCheck("relay", "Relay: "+label, "fail", "Unreachable")
			} else {
				resp.Body.Close()
				addCheck("relay", "Relay: "+label, "pass", fmt.Sprintf("OK (%dms)", rtt.Milliseconds()))
			}
		}
	} else {
		addCheck("relay", "Relay servers", "warn", "None configured")
	}

	// Network
	ip := getLocalIP()
	if ip != "" {
		addCheck("network", "Local IP", "pass", ip)
	} else {
		addCheck("network", "Local IP", "warn", "Could not determine")
	}

	// Voice
	if cfg != nil && cfg.Speech != nil && cfg.Speech.Provider != "" {
		addCheck("voice", "Speech provider", "pass", cfg.Speech.Provider)
		if cfg.Speech.TTSEnabled {
			addCheck("voice", "TTS", "pass", "Enabled")
		} else {
			addCheck("voice", "TTS", "pass", "Disabled")
		}
	} else {
		addCheck("voice", "Speech provider", "warn", "Not configured")
	}

	jsonReply(w, http.StatusOK, map[string]interface{}{
		"ok":     true,
		"checks": checks,
	})
}

// handleTools scans for installed AI tools and returns their info.
func (s *HTTPServer) handleTools(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, http.StatusMethodNotAllowed, "use GET")
		return
	}

	type toolInfo struct {
		ID        string `json:"id"`
		Name      string `json:"name"`
		Command   string `json:"command"`
		Installed bool   `json:"installed"`
		Path      string `json:"path,omitempty"`
		Version   string `json:"version,omitempty"`
		Install   string `json:"installCmd"`
	}

	tools := []struct{ id, name, cmd, install string }{
		{"claude", "Claude Code", "claude", "npm install -g @anthropic-ai/claude-code"},
		{"codex", "OpenAI Codex", "codex", "npm install -g @openai/codex"},
		{"aider", "Aider", "aider", "pip install aider-chat"},
		{"ollama", "Ollama", "ollama", "brew install ollama"},
		{"goose", "Goose", "goose", "pip install goose-ai"},
		{"amp", "Amp", "amp", "npm install -g @anthropic/amp"},
		{"opencode", "OpenCode", "opencode", "go install github.com/mbreithecker/opencode@latest"},
		{"qwen", "Qwen", "qwen", "pip install qwen-agent"},
		{"cursor", "Cursor", "cursor", "https://cursor.com"},
	}

	var result []toolInfo
	for _, t := range tools {
		ti := toolInfo{ID: t.id, Name: t.name, Command: t.cmd, Install: t.install}
		p, err := osexec.LookPath(t.cmd)
		if err == nil {
			ti.Installed = true
			ti.Path = p
			out, verr := osexec.Command(t.cmd, "--version").CombinedOutput()
			if verr == nil {
				ver := strings.TrimSpace(strings.Split(string(out), "\n")[0])
				if len(ver) > 60 {
					ver = ver[:60]
				}
				ti.Version = ver
			}
		}
		result = append(result, ti)
	}

	// Also check supporting tools
	type supportTool struct {
		Name      string `json:"name"`
		Command   string `json:"command"`
		Installed bool   `json:"installed"`
		Purpose   string `json:"purpose"`
	}
	var support []supportTool
	supportChecks := []struct{ name, cmd, purpose string }{
		{"tmux", "tmux", "Session management"},
		{"Node.js", "node", "JS toolchain"},
		{"Python", "python3", "Python toolchain"},
		{"Go", "go", "Go toolchain"},
		{"Git", "git", "Version control"},
		{"sox", "sox", "Audio recording"},
		{"ffmpeg", "ffmpeg", "Media processing"},
		{"whisper", "whisper-cpp", "On-device STT"},
		{"Docker", "docker", "Container runtime"},
		{"cloudflared", "cloudflared", "Cloudflare Tunnel"},
	}
	for _, s := range supportChecks {
		st := supportTool{Name: s.name, Command: s.cmd, Purpose: s.purpose}
		if _, err := osexec.LookPath(s.cmd); err == nil {
			st.Installed = true
		}
		support = append(support, st)
	}

	jsonReply(w, http.StatusOK, map[string]interface{}{
		"ok":      true,
		"tools":   result,
		"support": support,
	})
}

// ---------------------------------------------------------------------------
// Scheduler handlers
// ---------------------------------------------------------------------------

func (s *HTTPServer) handleSchedules(w http.ResponseWriter, r *http.Request) {
	if s.scheduler == nil {
		jsonError(w, http.StatusServiceUnavailable, "scheduler not enabled")
		return
	}
	switch r.Method {
	case http.MethodGet:
		schedules := s.scheduler.ListSchedules()
		jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true, "schedules": schedules})
	case http.MethodPost:
		var st ScheduledTask
		if err := json.NewDecoder(r.Body).Decode(&st); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		if err := s.scheduler.AddSchedule(&st); err != nil {
			jsonError(w, http.StatusBadRequest, err.Error())
			return
		}
		jsonReply(w, http.StatusCreated, map[string]interface{}{"ok": true, "schedule": st})
	default:
		jsonError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *HTTPServer) handleScheduleByID(w http.ResponseWriter, r *http.Request) {
	if s.scheduler == nil {
		jsonError(w, http.StatusServiceUnavailable, "scheduler not enabled")
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/schedules/")
	parts := strings.SplitN(path, "/", 2)
	id := parts[0]
	action := ""
	if len(parts) > 1 {
		action = parts[1]
	}

	switch action {
	case "":
		if r.Method == http.MethodDelete {
			if err := s.scheduler.RemoveSchedule(id); err != nil {
				jsonError(w, http.StatusNotFound, err.Error())
				return
			}
			jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true})
		} else {
			st, ok := s.scheduler.GetSchedule(id)
			if !ok {
				jsonError(w, http.StatusNotFound, "schedule not found")
				return
			}
			jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true, "schedule": st})
		}
	case "pause":
		if err := s.scheduler.PauseSchedule(id); err != nil {
			jsonError(w, http.StatusNotFound, err.Error())
			return
		}
		jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true})
	case "resume":
		if err := s.scheduler.ResumeSchedule(id); err != nil {
			jsonError(w, http.StatusNotFound, err.Error())
			return
		}
		jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true})
	default:
		jsonError(w, http.StatusNotFound, "unknown action")
	}
}

// ---------------------------------------------------------------------------
// Analytics handler
// ---------------------------------------------------------------------------

func (s *HTTPServer) handleAnalytics(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, http.StatusMethodNotAllowed, "use GET")
		return
	}
	if s.analytics == nil {
		jsonError(w, http.StatusServiceUnavailable, "analytics not available")
		return
	}
	stats := s.analytics.GetStats()
	jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true, "analytics": stats})
}

// ---------------------------------------------------------------------------
// Session transfer handlers
// ---------------------------------------------------------------------------

func (s *HTTPServer) handleSessionList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, http.StatusMethodNotAllowed, "use GET")
		return
	}
	sessions := ListTransferableSessions(s.taskMgr)
	jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true, "sessions": sessions})
}

func (s *HTTPServer) handleSessionExport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}
	var body struct {
		TaskID           string `json:"taskId"`
		IncludeWorkspace bool   `json:"includeWorkspace"`
		WorkspaceMode    string `json:"workspaceMode"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if body.TaskID == "" {
		jsonError(w, http.StatusBadRequest, "taskId is required")
		return
	}
	bundle, err := ExportSession(s.taskMgr, body.TaskID, ExportOptions{
		IncludeWorkspace: body.IncludeWorkspace,
		WorkspaceMode:    body.WorkspaceMode,
	})
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	log.Printf("[HTTP] Session exported: task=%s agent=%s turns=%d", body.TaskID, bundle.AgentType, len(bundle.Task.Turns))
	jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true, "bundle": bundle})
}

func (s *HTTPServer) handleSessionImport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}
	var body struct {
		Bundle         TransferBundle `json:"bundle"`
		WorkDir        string         `json:"workDir,omitempty"`
		ResumeOnImport bool           `json:"resumeOnImport"`
		GitClone       bool           `json:"gitClone"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	taskID, warnings, err := ImportSession(s.taskMgr, &body.Bundle, ImportOptions{
		WorkDir:        body.WorkDir,
		ResumeOnImport: body.ResumeOnImport,
		GitClone:       body.GitClone,
	})
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	log.Printf("[HTTP] Session imported: task=%s warnings=%d", taskID, len(warnings))
	jsonReply(w, http.StatusOK, map[string]interface{}{
		"ok":       true,
		"taskId":   taskID,
		"warnings": warnings,
	})
}

// ---------------------------------------------------------------------------
// Notifications handlers
// ---------------------------------------------------------------------------

func (s *HTTPServer) handleNotificationsConfig(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		cfg, _ := LoadConfig()
		if cfg != nil && cfg.Notifications != nil {
			jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true, "config": cfg.Notifications})
		} else {
			jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true, "config": NotificationConfig{}})
		}
	case http.MethodPost:
		var notifConfig NotificationConfig
		if err := json.NewDecoder(r.Body).Decode(&notifConfig); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		cfg, _ := LoadConfig()
		if cfg == nil {
			cfg = &Config{}
		}
		cfg.Notifications = &notifConfig
		if err := SaveConfig(cfg); err != nil {
			jsonError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if s.notifyMgr != nil {
			s.notifyMgr.UpdateConfig(&notifConfig)
		}
		jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true})
	default:
		jsonError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *HTTPServer) handleNotificationsTest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}
	var body struct {
		Channel string `json:"channel"` // "telegram", "discord", "slack", or "" for all
	}
	json.NewDecoder(r.Body).Decode(&body)
	if s.notifyMgr == nil {
		jsonError(w, http.StatusServiceUnavailable, "notifications not available")
		return
	}
	result := s.notifyMgr.TestNotification(body.Channel)
	jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true, "result": result})
}

// ---------------------------------------------------------------------------
// Webhook trigger (public — uses webhook secret)
// ---------------------------------------------------------------------------

func (s *HTTPServer) handleWebhookTrigger(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}

	// Validate webhook secret
	secret := r.Header.Get("X-Webhook-Secret")
	cfg, _ := LoadConfig()
	if cfg == nil || cfg.WebhookSecret == "" {
		jsonError(w, http.StatusServiceUnavailable, "webhook secret not configured — set via: yaver config set webhook-secret <secret>")
		return
	}
	if secret != cfg.WebhookSecret {
		jsonError(w, http.StatusUnauthorized, "invalid webhook secret")
		return
	}

	var body struct {
		Title       string `json:"title"`
		Description string `json:"description,omitempty"`
		Runner      string `json:"runner,omitempty"`
		Model       string `json:"model,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if body.Title == "" {
		jsonError(w, http.StatusBadRequest, "title is required")
		return
	}

	task, err := s.taskMgr.CreateTask(body.Title, body.Description, body.Model, "webhook", body.Runner, "", nil, nil)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}

	log.Printf("[HTTP] Webhook task created: %s — %s", task.ID, body.Title)
	jsonReply(w, http.StatusCreated, map[string]interface{}{
		"ok":     true,
		"taskId": task.ID,
		"status": task.Status,
	})
}

// ---------------------------------------------------------------------------
// Exec handlers (remote command execution)
// ---------------------------------------------------------------------------

func (s *HTTPServer) handleExec(w http.ResponseWriter, r *http.Request) {
	if s.execMgr == nil {
		jsonError(w, http.StatusServiceUnavailable, "exec is not enabled")
		return
	}
	switch r.Method {
	case http.MethodGet:
		sessions := s.execMgr.ListExecs()
		execs := make([]map[string]interface{}, 0, len(sessions))
		for _, sess := range sessions {
			execs = append(execs, sess.Snapshot())
		}
		jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true, "execs": execs})
	case http.MethodPost:
		var body struct {
			Command string            `json:"command"`
			WorkDir string            `json:"workDir,omitempty"`
			Shell   string            `json:"shell,omitempty"`
			Timeout int               `json:"timeout,omitempty"`
			Env     map[string]string `json:"env,omitempty"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid JSON body")
			return
		}
		if body.Command == "" {
			jsonError(w, http.StatusBadRequest, "command is required")
			return
		}
		sess, err := s.execMgr.StartExec(body.Command, body.WorkDir, body.Shell, body.Env, body.Timeout)
		if err != nil {
			code := http.StatusInternalServerError
			if strings.Contains(err.Error(), "blocked") {
				code = http.StatusBadRequest
			} else if strings.Contains(err.Error(), "too many") {
				code = http.StatusTooManyRequests
			}
			jsonError(w, code, err.Error())
			return
		}
		log.Printf("[HTTP] Exec started: %s — %s (pid=%d)", sess.ID, body.Command, sess.PID)
		jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true, "execId": sess.ID, "pid": sess.PID})
	default:
		jsonError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *HTTPServer) handleExecByID(w http.ResponseWriter, r *http.Request) {
	if s.execMgr == nil {
		jsonError(w, http.StatusServiceUnavailable, "exec is not enabled")
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/exec/")
	parts := strings.SplitN(path, "/", 2)
	execID := parts[0]
	action := ""
	if len(parts) > 1 {
		action = parts[1]
	}
	if execID == "" {
		jsonError(w, http.StatusBadRequest, "exec ID required")
		return
	}

	switch action {
	case "":
		if r.Method == http.MethodDelete {
			if err := s.execMgr.KillExec(execID); err != nil {
				jsonError(w, http.StatusNotFound, err.Error())
				return
			}
			jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true})
		} else {
			sess, ok := s.execMgr.GetExec(execID)
			if !ok {
				jsonError(w, http.StatusNotFound, "exec session not found")
				return
			}
			jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true, "exec": sess.Snapshot()})
		}
	case "input":
		if r.Method != http.MethodPost {
			jsonError(w, http.StatusMethodNotAllowed, "use POST")
			return
		}
		var body struct {
			Input string `json:"input"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		if err := s.execMgr.SendInput(execID, body.Input); err != nil {
			jsonError(w, http.StatusBadRequest, err.Error())
			return
		}
		jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true})
	case "signal":
		if r.Method != http.MethodPost {
			jsonError(w, http.StatusMethodNotAllowed, "use POST")
			return
		}
		var body struct {
			Signal string `json:"signal"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		if err := s.execMgr.SignalExec(execID, body.Signal); err != nil {
			jsonError(w, http.StatusBadRequest, err.Error())
			return
		}
		jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true})
	case "stream":
		s.streamExecOutput(w, r, execID)
	default:
		jsonError(w, http.StatusNotFound, "unknown action")
	}
}

func (s *HTTPServer) streamExecOutput(w http.ResponseWriter, r *http.Request, execID string) {
	ch, err := s.execMgr.Subscribe(execID)
	if err != nil {
		jsonError(w, http.StatusNotFound, err.Error())
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		jsonError(w, http.StatusInternalServerError, "streaming not supported")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	for evt := range ch {
		data, _ := json.Marshal(evt)
		fmt.Fprintf(w, "data: %s\n\n", data)
		flusher.Flush()
	}
}

// ---------------------------------------------------------------------------
// JSON helpers
// ---------------------------------------------------------------------------

func jsonReply(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

func jsonError(w http.ResponseWriter, status int, msg string) {
	jsonReply(w, status, map[string]interface{}{
		"ok":    false,
		"error": msg,
	})
}

func jsonString(v interface{}) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// ---------------------------------------------------------------------------
// MCP (Model Context Protocol) — JSON-RPC 2.0 over HTTP
// Allows AI agents like Claude to use Yaver as an MCP server.
// ---------------------------------------------------------------------------

type mcpRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      interface{}     `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type mcpResponse struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      interface{} `json:"id"`
	Result  interface{} `json:"result,omitempty"`
	Error   *mcpError   `json:"error,omitempty"`
}

type mcpError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (s *HTTPServer) handleMCP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}

	var req mcpRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(mcpResponse{
			JSONRPC: "2.0",
			ID:      nil,
			Error:   &mcpError{Code: -32700, Message: "Parse error"},
		})
		return
	}

	var resp mcpResponse
	resp.JSONRPC = "2.0"
	resp.ID = req.ID

	switch req.Method {
	case "initialize":
		resp.Result = map[string]interface{}{
			"protocolVersion": "2024-11-05",
			"capabilities": map[string]interface{}{
				"tools": map[string]interface{}{},
			},
			"serverInfo": map[string]interface{}{
				"name":    "yaver",
				"version": version,
			},
		}

	case "tools/list":
		resp.Result = s.getMCPToolsList()

	case "tools/call":
		resp.Result = s.handleMCPToolCall(req.Params)

	case "notifications/initialized":
		// Client notification, no response needed but we return empty result
		resp.Result = map[string]interface{}{}

	default:
		resp.Error = &mcpError{Code: -32601, Message: "Method not found: " + req.Method}
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(resp)
}

func (s *HTTPServer) handleMCPToolCall(params json.RawMessage) interface{} {
	var call struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(params, &call); err != nil {
		return map[string]interface{}{
			"content": []map[string]interface{}{
				{"type": "text", "text": "Invalid tool call parameters"},
			},
			"isError": true,
		}
	}

	switch call.Name {
	case "create_task":
		var args struct {
			Prompt    string `json:"prompt"`
			Verbosity *int   `json:"verbosity"`
		}
		json.Unmarshal(call.Arguments, &args)
		if args.Prompt == "" {
			return mcpToolError("prompt is required")
		}
		var sc *SpeechContext
		if args.Verbosity != nil {
			sc = &SpeechContext{Verbosity: args.Verbosity}
		}
		task, err := s.taskMgr.CreateTask(args.Prompt, "", "", "mcp", "", "", nil, sc)
		if err != nil {
			return mcpToolError(fmt.Sprintf("failed to create task: %v", err))
		}
		log.Printf("[MCP] Task created: %s", task.ID)
		return mcpToolResult(fmt.Sprintf("Task created successfully.\nTask ID: %s\nStatus: %s", task.ID, task.Status))

	case "list_tasks":
		tasks := s.taskMgr.ListTasks()
		if len(tasks) == 0 {
			return mcpToolResult("No tasks found.")
		}
		var sb strings.Builder
		for _, t := range tasks {
			sb.WriteString(fmt.Sprintf("- [%s] %s — %s", t.Status, t.ID, t.Title))
			if t.Status == "running" {
				sb.WriteString(" (running)")
			}
			sb.WriteString("\n")
		}
		return mcpToolResult(sb.String())

	case "get_task":
		var args struct {
			TaskID string `json:"task_id"`
		}
		json.Unmarshal(call.Arguments, &args)
		task, ok := s.taskMgr.GetTask(args.TaskID)
		if !ok {
			return mcpToolError("task not found: " + args.TaskID)
		}
		s.taskMgr.mu.RLock()
		output := task.Output
		status := task.Status
		title := task.Title
		s.taskMgr.mu.RUnlock()
		return mcpToolResult(fmt.Sprintf("Task: %s\nStatus: %s\nTitle: %s\n\nOutput:\n%s", args.TaskID, status, title, output))

	case "stop_task":
		var args struct {
			TaskID string `json:"task_id"`
		}
		json.Unmarshal(call.Arguments, &args)
		if err := s.taskMgr.StopTask(args.TaskID); err != nil {
			return mcpToolError(err.Error())
		}
		log.Printf("[MCP] Task stopped: %s", args.TaskID)
		return mcpToolResult("Task stopped: " + args.TaskID)

	case "continue_task":
		var args struct {
			TaskID string `json:"task_id"`
			Input  string `json:"input"`
		}
		json.Unmarshal(call.Arguments, &args)
		task, err := s.taskMgr.ResumeTask(args.TaskID, args.Input, nil)
		if err != nil {
			return mcpToolError(fmt.Sprintf("resume failed: %v", err))
		}
		log.Printf("[MCP] Task resumed: %s (session=%s)", args.TaskID, task.SessionID)
		return mcpToolResult(fmt.Sprintf("Task resumed. Task ID: %s", task.ID))

	case "get_info":
		hostname, _ := os.Hostname()
		return mcpToolResult(fmt.Sprintf("Hostname: %s\nVersion: %s\nWork Dir: %s", hostname, version, s.taskMgr.workDir))

	// --- Runner Management ---
	case "list_runners":
		var sb strings.Builder
		sb.WriteString("Available runners:\n")
		defaultID := s.taskMgr.runner.RunnerID
		for id, r := range builtinRunners {
			_, err := osexec.LookPath(r.Command)
			installed := "not installed"
			if err == nil {
				installed = "installed"
			}
			def := ""
			if id == defaultID {
				def = " (active)"
			}
			sb.WriteString(fmt.Sprintf("- %s: %s [%s]%s\n", id, r.Name, installed, def))
		}
		return mcpToolResult(sb.String())

	case "switch_runner":
		var args struct {
			RunnerID string `json:"runner_id"`
		}
		json.Unmarshal(call.Arguments, &args)
		if args.RunnerID == "" {
			return mcpToolError("runner_id is required")
		}
		r, ok := builtinRunners[args.RunnerID]
		if !ok {
			return mcpToolError(fmt.Sprintf("unknown runner: %s", args.RunnerID))
		}
		if _, err := osexec.LookPath(r.Command); err != nil {
			return mcpToolError(fmt.Sprintf("%s is not installed on this machine", r.Command))
		}
		s.taskMgr.mu.Lock()
		s.taskMgr.runner = r
		s.taskMgr.mu.Unlock()
		log.Printf("[MCP] Runner switched to %s", args.RunnerID)
		return mcpToolResult(fmt.Sprintf("Runner switched to %s (%s)", r.Name, args.RunnerID))

	// --- System & Config ---
	case "get_system_info":
		status := s.taskMgr.GetAgentStatus()
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("Hostname: %s\n", status.System.Hostname))
		sb.WriteString(fmt.Sprintf("OS: %s/%s\n", status.System.OS, status.System.Arch))
		if status.System.MemoryMB > 0 {
			sb.WriteString(fmt.Sprintf("Memory: %d MB\n", status.System.MemoryMB))
		}
		sb.WriteString(fmt.Sprintf("Runner: %s (%s) — %s\n", status.Runner.Name, status.Runner.ID, func() string {
			if status.Runner.Installed {
				return "installed"
			}
			return "not installed"
		}()))
		sb.WriteString(fmt.Sprintf("Running tasks: %d / %d total\n", status.RunningTasks, status.TotalTasks))
		sb.WriteString(fmt.Sprintf("Work dir: %s\n", s.taskMgr.workDir))
		sb.WriteString(fmt.Sprintf("Version: %s\n", version))
		return mcpToolResult(sb.String())

	case "get_config":
		cfg, err := LoadConfig()
		if err != nil {
			return mcpToolError(fmt.Sprintf("load config: %v", err))
		}
		// Redact sensitive fields
		safeCfg := map[string]interface{}{
			"auto_start":   cfg.AutoStart,
			"auto_update":  cfg.AutoUpdate,
			"relay_count":  len(cfg.RelayServers),
			"acl_peers":    len(cfg.ACLPeers),
			"email_configured": cfg.Email != nil && cfg.Email.Provider != "",
		}
		if cfg.Sandbox != nil {
			safeCfg["sandbox"] = map[string]interface{}{
				"enabled":     cfg.Sandbox.Enabled,
				"allow_sudo":  cfg.Sandbox.AllowSudo,
			}
		} else {
			safeCfg["sandbox"] = "default (enabled, no sudo)"
		}
		data, _ := json.MarshalIndent(safeCfg, "", "  ")
		return mcpToolResult(string(data))

	case "set_work_dir":
		var args struct {
			Path string `json:"path"`
		}
		json.Unmarshal(call.Arguments, &args)
		if args.Path == "" {
			return mcpToolError("path is required")
		}
		info, err := os.Stat(args.Path)
		if err != nil {
			return mcpToolError(fmt.Sprintf("path not accessible: %v", err))
		}
		if !info.IsDir() {
			return mcpToolError("path is not a directory")
		}
		if err := ValidateWorkDir(args.Path, s.taskMgr.Sandbox); err != nil {
			return mcpToolError(err.Error())
		}
		s.taskMgr.mu.Lock()
		s.taskMgr.workDir = args.Path
		s.taskMgr.mu.Unlock()
		log.Printf("[MCP] Work dir changed to %s", args.Path)
		return mcpToolResult(fmt.Sprintf("Working directory changed to: %s", args.Path))

	case "list_projects":
		fp, err := projectsFilePath()
		if err != nil {
			return mcpToolError(fmt.Sprintf("projects file: %v", err))
		}
		data, err := os.ReadFile(fp)
		if err != nil {
			if os.IsNotExist(err) {
				return mcpToolResult("No projects discovered yet. Run 'yaver discover' to scan.")
			}
			return mcpToolError(fmt.Sprintf("read projects: %v", err))
		}
		content := string(data)
		if len(content) > 5000 {
			content = content[:5000] + "\n... (truncated)"
		}
		return mcpToolResult(content)

	// --- Relay Management ---
	case "get_relay_config":
		cfg, err := LoadConfig()
		if err != nil {
			return mcpToolError(fmt.Sprintf("load config: %v", err))
		}
		if len(cfg.RelayServers) == 0 {
			return mcpToolResult("No relay servers configured. Use add_relay_server to add one.")
		}
		var sb strings.Builder
		for _, rs := range cfg.RelayServers {
			sb.WriteString(fmt.Sprintf("- [%s] %s", rs.ID, rs.QuicAddr))
			if rs.Label != "" {
				sb.WriteString(fmt.Sprintf(" (%s)", rs.Label))
			}
			if rs.Region != "" {
				sb.WriteString(fmt.Sprintf(" region=%s", rs.Region))
			}
			sb.WriteString("\n")
		}
		return mcpToolResult(sb.String())

	case "add_relay_server":
		var args struct {
			QuicAddr string `json:"quic_addr"`
			HttpURL  string `json:"http_url"`
			Password string `json:"password"`
			Label    string `json:"label"`
		}
		json.Unmarshal(call.Arguments, &args)
		if args.QuicAddr == "" {
			return mcpToolError("quic_addr is required")
		}
		cfg, err := LoadConfig()
		if err != nil {
			return mcpToolError(fmt.Sprintf("load config: %v", err))
		}
		newRelay := RelayServerConfig{
			ID:       fmt.Sprintf("relay-%d", len(cfg.RelayServers)+1),
			QuicAddr: args.QuicAddr,
			HttpURL:  args.HttpURL,
			Password: args.Password,
			Label:    args.Label,
		}
		cfg.RelayServers = append(cfg.RelayServers, newRelay)
		if err := SaveConfig(cfg); err != nil {
			return mcpToolError(fmt.Sprintf("save config: %v", err))
		}
		log.Printf("[MCP] Relay server added: %s", args.QuicAddr)
		return mcpToolResult(fmt.Sprintf("Relay server added: %s (ID: %s). Restart agent to connect.", args.QuicAddr, newRelay.ID))

	case "remove_relay_server":
		var args struct {
			RelayID string `json:"relay_id"`
		}
		json.Unmarshal(call.Arguments, &args)
		if args.RelayID == "" {
			return mcpToolError("relay_id is required")
		}
		cfg, err := LoadConfig()
		if err != nil {
			return mcpToolError(fmt.Sprintf("load config: %v", err))
		}
		found := false
		var remaining []RelayServerConfig
		for _, rs := range cfg.RelayServers {
			if rs.ID == args.RelayID {
				found = true
				continue
			}
			remaining = append(remaining, rs)
		}
		if !found {
			return mcpToolError("relay server not found: " + args.RelayID)
		}
		cfg.RelayServers = remaining
		if err := SaveConfig(cfg); err != nil {
			return mcpToolError(fmt.Sprintf("save config: %v", err))
		}
		return mcpToolResult(fmt.Sprintf("Relay server %s removed. Restart agent to apply.", args.RelayID))

	// --- Filesystem ---
	case "read_file":
		var args struct {
			Path string `json:"path"`
		}
		json.Unmarshal(call.Arguments, &args)
		if args.Path == "" {
			return mcpToolError("path is required")
		}
		filePath := s.resolveFilePath(args.Path)
		data, err := os.ReadFile(filePath)
		if err != nil {
			return mcpToolError(fmt.Sprintf("read file: %v", err))
		}
		content := string(data)
		if len(content) > 100*1024 {
			content = content[:100*1024] + "\n... (truncated at 100KB)"
		}
		return mcpToolResult(content)

	case "write_file":
		var args struct {
			Path    string `json:"path"`
			Content string `json:"content"`
		}
		json.Unmarshal(call.Arguments, &args)
		if args.Path == "" || args.Content == "" {
			return mcpToolError("path and content are required")
		}
		filePath := s.resolveFilePath(args.Path)
		dir := filepath.Dir(filePath)
		if err := os.MkdirAll(dir, 0755); err != nil {
			return mcpToolError(fmt.Sprintf("create directory: %v", err))
		}
		if err := os.WriteFile(filePath, []byte(args.Content), 0644); err != nil {
			return mcpToolError(fmt.Sprintf("write file: %v", err))
		}
		return mcpToolResult(fmt.Sprintf("File written: %s (%d bytes)", filePath, len(args.Content)))

	case "list_directory":
		var args struct {
			Path string `json:"path"`
		}
		json.Unmarshal(call.Arguments, &args)
		dirPath := s.taskMgr.workDir
		if args.Path != "" {
			dirPath = s.resolveFilePath(args.Path)
		}
		entries, err := os.ReadDir(dirPath)
		if err != nil {
			return mcpToolError(fmt.Sprintf("list directory: %v", err))
		}
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("Directory: %s\n\n", dirPath))
		for _, e := range entries {
			info, _ := e.Info()
			if info != nil {
				if info.IsDir() {
					sb.WriteString(fmt.Sprintf("  %s/\n", e.Name()))
				} else {
					sb.WriteString(fmt.Sprintf("  %s (%d bytes)\n", e.Name(), info.Size()))
				}
			}
		}
		return mcpToolResult(sb.String())

	case "search_files":
		var args struct {
			Pattern    string `json:"pattern"`
			Directory  string `json:"directory"`
			MaxResults int    `json:"max_results"`
		}
		json.Unmarshal(call.Arguments, &args)
		if args.Pattern == "" {
			return mcpToolError("pattern is required")
		}
		dir := args.Directory
		if dir == "" {
			dir = s.taskMgr.workDir
		}
		return mcpToolResult(searchFiles(dir, args.Pattern, args.MaxResults))

	case "search_content":
		var args struct {
			Query      string `json:"query"`
			Directory  string `json:"directory"`
			MaxResults int    `json:"max_results"`
		}
		json.Unmarshal(call.Arguments, &args)
		if args.Query == "" {
			return mcpToolError("query is required")
		}
		dir := args.Directory
		if dir == "" {
			dir = s.taskMgr.workDir
		}
		return mcpToolResult(searchFileContent(dir, args.Query, args.MaxResults))

	case "screenshot":
		img, err := captureScreen()
		if err != nil {
			return mcpToolError(err.Error())
		}
		// Return as text with base64 — MCP clients can render images
		return map[string]interface{}{
			"content": []map[string]interface{}{
				{"type": "image", "data": img, "mimeType": "image/png"},
			},
		}

	case "system_info":
		return mcpToolResult(getSystemInfo())

	case "git_info":
		var args struct {
			Operation string `json:"operation"`
			Directory string `json:"directory"`
		}
		json.Unmarshal(call.Arguments, &args)
		if args.Operation == "" {
			return mcpToolError("operation is required (status, diff, log, branch, remote)")
		}
		dir := args.Directory
		if dir == "" {
			dir = s.taskMgr.workDir
		}
		return mcpToolResult(gitInfo(dir, args.Operation))

	// --- Email ---
	case "email_list_inbox":
		if s.emailMgr == nil {
			return mcpToolError("Email not configured. Run 'yaver email setup' first.")
		}
		var args struct {
			Folder string `json:"folder"`
			Search string `json:"search"`
			Limit  int    `json:"limit"`
		}
		json.Unmarshal(call.Arguments, &args)
		if args.Limit <= 0 {
			args.Limit = 20
		}
		if args.Folder == "" {
			args.Folder = "inbox"
		}
		emails, err := s.emailMgr.ListInbox(args.Folder, args.Search, args.Limit)
		if err != nil {
			return mcpToolError(fmt.Sprintf("list inbox: %v", err))
		}
		if len(emails) == 0 {
			return mcpToolResult("No emails found.")
		}
		data, _ := json.MarshalIndent(emails, "", "  ")
		return mcpToolResult(string(data))

	case "email_get":
		if s.emailMgr == nil {
			return mcpToolError("Email not configured. Run 'yaver email setup' first.")
		}
		var args struct {
			EmailID string `json:"email_id"`
		}
		json.Unmarshal(call.Arguments, &args)
		if args.EmailID == "" {
			return mcpToolError("email_id is required")
		}
		email, err := s.emailMgr.GetEmail(args.EmailID)
		if err != nil {
			return mcpToolError(fmt.Sprintf("get email: %v", err))
		}
		data, _ := json.MarshalIndent(email, "", "  ")
		return mcpToolResult(string(data))

	case "email_send":
		if s.emailMgr == nil {
			return mcpToolError("Email not configured. Run 'yaver email setup' first.")
		}
		var args struct {
			To      string `json:"to"`
			Subject string `json:"subject"`
			Body    string `json:"body"`
			CC      string `json:"cc"`
		}
		json.Unmarshal(call.Arguments, &args)
		if args.To == "" || args.Subject == "" || args.Body == "" {
			return mcpToolError("to, subject, and body are required")
		}
		if err := s.emailMgr.SendEmail(args.To, args.Subject, args.Body, args.CC); err != nil {
			return mcpToolError(fmt.Sprintf("send email: %v", err))
		}
		return mcpToolResult(fmt.Sprintf("Email sent to %s: %s", args.To, args.Subject))

	case "email_sync":
		if s.emailMgr == nil {
			return mcpToolError("Email not configured. Run 'yaver email setup' first.")
		}
		count, err := s.emailMgr.SyncEmails()
		if err != nil {
			return mcpToolError(fmt.Sprintf("sync failed: %v", err))
		}
		return mcpToolResult(fmt.Sprintf("Synced %d emails to local database.", count))

	case "email_search":
		if s.emailMgr == nil {
			return mcpToolError("Email not configured. Run 'yaver email setup' first.")
		}
		var args struct {
			Query string `json:"query"`
			Limit int    `json:"limit"`
		}
		json.Unmarshal(call.Arguments, &args)
		if args.Query == "" {
			return mcpToolError("query is required")
		}
		if args.Limit <= 0 {
			args.Limit = 20
		}
		emails, err := s.emailMgr.SearchEmails(args.Query, args.Limit)
		if err != nil {
			return mcpToolError(fmt.Sprintf("search: %v", err))
		}
		if len(emails) == 0 {
			return mcpToolResult("No emails found matching query.")
		}
		data, _ := json.MarshalIndent(emails, "", "  ")
		return mcpToolResult(string(data))

	// --- ACL (Agent Communication Layer) ---
	case "acl_list_peers":
		if s.aclMgr == nil {
			return mcpToolResult("ACL not initialized. No peers configured.")
		}
		peers := s.aclMgr.ListPeers()
		if len(peers) == 0 {
			return mcpToolResult("No MCP peers connected. Use acl_add_peer to connect to another MCP server.")
		}
		data, _ := json.MarshalIndent(peers, "", "  ")
		return mcpToolResult(string(data))

	case "acl_add_peer":
		if s.aclMgr == nil {
			return mcpToolError("ACL not initialized")
		}
		var args struct {
			Name string `json:"name"`
			URL  string `json:"url"`
			Auth string `json:"auth"`
		}
		json.Unmarshal(call.Arguments, &args)
		if args.Name == "" || args.URL == "" {
			return mcpToolError("name and url are required")
		}
		peer := ACLPeerConfig{
			ID:   strings.ToLower(strings.ReplaceAll(args.Name, " ", "-")),
			Name: args.Name,
			URL:  args.URL,
			Type: "http",
			Auth: args.Auth,
		}
		if err := s.aclMgr.AddPeer(peer); err != nil {
			return mcpToolError(fmt.Sprintf("add peer: %v", err))
		}
		// Persist to config
		cfg, _ := LoadConfig()
		if cfg != nil {
			cfg.ACLPeers = append(cfg.ACLPeers, peer)
			SaveConfig(cfg)
		}
		return mcpToolResult(fmt.Sprintf("Connected to MCP peer: %s (%s)", args.Name, args.URL))

	case "acl_remove_peer":
		if s.aclMgr == nil {
			return mcpToolError("ACL not initialized")
		}
		var args struct {
			PeerID string `json:"peer_id"`
		}
		json.Unmarshal(call.Arguments, &args)
		if args.PeerID == "" {
			return mcpToolError("peer_id is required")
		}
		if err := s.aclMgr.RemovePeer(args.PeerID); err != nil {
			return mcpToolError(err.Error())
		}
		// Persist removal to config
		cfg, _ := LoadConfig()
		if cfg != nil {
			var remaining []ACLPeerConfig
			for _, p := range cfg.ACLPeers {
				if p.ID != args.PeerID {
					remaining = append(remaining, p)
				}
			}
			cfg.ACLPeers = remaining
			SaveConfig(cfg)
		}
		return mcpToolResult(fmt.Sprintf("Disconnected from peer: %s", args.PeerID))

	case "acl_list_peer_tools":
		if s.aclMgr == nil {
			return mcpToolError("ACL not initialized")
		}
		var args struct {
			PeerID string `json:"peer_id"`
		}
		json.Unmarshal(call.Arguments, &args)
		if args.PeerID == "" {
			return mcpToolError("peer_id is required")
		}
		tools, err := s.aclMgr.ListPeerTools(args.PeerID)
		if err != nil {
			return mcpToolError(fmt.Sprintf("list tools: %v", err))
		}
		data, _ := json.MarshalIndent(tools, "", "  ")
		return mcpToolResult(string(data))

	case "acl_call_peer_tool":
		if s.aclMgr == nil {
			return mcpToolError("ACL not initialized")
		}
		var args struct {
			PeerID   string          `json:"peer_id"`
			ToolName string          `json:"tool_name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		json.Unmarshal(call.Arguments, &args)
		if args.PeerID == "" || args.ToolName == "" {
			return mcpToolError("peer_id and tool_name are required")
		}
		result, err := s.aclMgr.CallPeerTool(args.PeerID, args.ToolName, args.Arguments)
		if err != nil {
			return mcpToolError(fmt.Sprintf("call tool: %v", err))
		}
		data, _ := json.MarshalIndent(result, "", "  ")
		return mcpToolResult(string(data))

	case "acl_health":
		if s.aclMgr == nil {
			return mcpToolResult("ACL not initialized. No peers configured.")
		}
		health := s.aclMgr.HealthCheck()
		var sb strings.Builder
		for id, ok := range health {
			status := "healthy"
			if !ok {
				status = "unreachable"
			}
			sb.WriteString(fmt.Sprintf("- %s: %s\n", id, status))
		}
		return mcpToolResult(sb.String())

	// --- Tmux Session Management ---
	case "tmux_list_sessions":
		tmuxMgr := s.taskMgr.TmuxMgr
		if tmuxMgr == nil {
			return mcpToolResult("Tmux is not available on this machine. Install tmux to use session adoption.")
		}
		sessions, err := tmuxMgr.ListTmuxSessions()
		if err != nil {
			return mcpToolError(fmt.Sprintf("list sessions: %v", err))
		}
		if len(sessions) == 0 {
			return mcpToolResult("No tmux sessions found.")
		}
		var sb strings.Builder
		sb.WriteString("Tmux sessions:\n")
		for _, s := range sessions {
			agent := s.AgentType
			if agent == "" {
				agent = "shell"
			}
			sb.WriteString(fmt.Sprintf("- %s [%s] %s, %d window(s)", s.Name, s.Relationship, agent, s.Windows))
			if s.TaskID != "" {
				sb.WriteString(fmt.Sprintf(", task=%s", s.TaskID))
			}
			if s.Attached {
				sb.WriteString(" (attached)")
			}
			sb.WriteString("\n")
		}
		return mcpToolResult(sb.String())

	case "tmux_adopt_session":
		var args struct {
			SessionName string `json:"session_name"`
		}
		json.Unmarshal(call.Arguments, &args)
		if args.SessionName == "" {
			return mcpToolError("session_name is required")
		}
		tmuxMgr := s.taskMgr.TmuxMgr
		if tmuxMgr == nil {
			return mcpToolError("tmux is not available on this machine")
		}
		task, err := tmuxMgr.AdoptSession(args.SessionName)
		if err != nil {
			return mcpToolError(fmt.Sprintf("adopt failed: %v", err))
		}
		log.Printf("[MCP] Adopted tmux session %q as task %s", args.SessionName, task.ID)
		return mcpToolResult(fmt.Sprintf("Adopted tmux session %q as task %s.\nStatus: %s\nRunner: %s", args.SessionName, task.ID, task.Status, task.RunnerID))

	case "tmux_detach_session":
		var args struct {
			TaskID string `json:"task_id"`
		}
		json.Unmarshal(call.Arguments, &args)
		if args.TaskID == "" {
			return mcpToolError("task_id is required")
		}
		tmuxMgr := s.taskMgr.TmuxMgr
		if tmuxMgr == nil {
			return mcpToolError("tmux is not available on this machine")
		}
		if err := tmuxMgr.DetachSession(args.TaskID); err != nil {
			return mcpToolError(fmt.Sprintf("detach failed: %v", err))
		}
		log.Printf("[MCP] Detached tmux session (task %s)", args.TaskID)
		return mcpToolResult(fmt.Sprintf("Detached task %s. The tmux session continues running.", args.TaskID))

	case "tmux_send_input":
		var args struct {
			TaskID string `json:"task_id"`
			Input  string `json:"input"`
		}
		json.Unmarshal(call.Arguments, &args)
		if args.TaskID == "" {
			return mcpToolError("task_id is required")
		}
		tmuxMgr := s.taskMgr.TmuxMgr
		if tmuxMgr == nil {
			return mcpToolError("tmux is not available on this machine")
		}
		if err := tmuxMgr.SendTmuxInput(args.TaskID, args.Input); err != nil {
			return mcpToolError(fmt.Sprintf("send input failed: %v", err))
		}
		return mcpToolResult("Input sent to tmux session.")

	// --- Diagnostics & Status ---
	case "yaver_doctor":
		return s.mcpDoctor()

	case "yaver_status":
		return s.mcpStatus()

	case "yaver_devices":
		cfg, err := LoadConfig()
		if err != nil || cfg.AuthToken == "" || cfg.ConvexSiteURL == "" {
			return mcpToolError("Not signed in. Run 'yaver auth' first.")
		}
		devices, err := listDevices(cfg.ConvexSiteURL, cfg.AuthToken)
		if err != nil {
			return mcpToolError(fmt.Sprintf("list devices: %v", err))
		}
		if len(devices) == 0 {
			return mcpToolResult("No devices registered. Run 'yaver serve' on your dev machine to register it.")
		}
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("%-10s  %-20s  %-8s  %-8s  %s\n", "ID", "NAME", "PLATFORM", "STATUS", "ADDRESS"))
		for _, d := range devices {
			status := "offline"
			if d.IsOnline {
				status = "online"
			}
			id := d.DeviceID
			if len(id) > 8 {
				id = id[:8] + "..."
			}
			sb.WriteString(fmt.Sprintf("%-10s  %-20s  %-8s  %-8s  %s:%d\n",
				id, d.Name, d.Platform, status, d.QuicHost, d.QuicPort))
		}
		return mcpToolResult(sb.String())

	case "yaver_logs":
		var args struct {
			Lines int `json:"lines"`
		}
		json.Unmarshal(call.Arguments, &args)
		if args.Lines <= 0 {
			args.Lines = 50
		}
		if args.Lines > 500 {
			args.Lines = 500
		}
		lp := logFilePath()
		if lp == "" {
			return mcpToolError("Could not determine log file path")
		}
		out, err := osexec.Command("tail", "-n", fmt.Sprintf("%d", args.Lines), lp).CombinedOutput()
		if err != nil {
			if strings.Contains(string(out), "No such file") {
				return mcpToolResult("No logs found. Start the agent with 'yaver serve'.")
			}
			return mcpToolError(fmt.Sprintf("read logs: %v: %s", err, string(out)))
		}
		return mcpToolResult(string(out))

	case "yaver_clear_logs":
		lp := logFilePath()
		if lp == "" {
			return mcpToolError("Could not determine log file path")
		}
		if err := os.Truncate(lp, 0); err != nil {
			if os.IsNotExist(err) {
				return mcpToolResult("No log file to clear.")
			}
			return mcpToolError(fmt.Sprintf("clear logs: %v", err))
		}
		log.Printf("[MCP] Logs cleared")
		return mcpToolResult("Agent logs cleared.")

	case "yaver_help":
		var args struct {
			Topic string `json:"topic"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolResult(yaverHelpText(args.Topic))

	case "yaver_ping":
		hostname, _ := os.Hostname()
		return mcpToolResult(fmt.Sprintf("Pong! Agent is alive.\nHostname: %s\nVersion: %s\nWork Dir: %s", hostname, version, s.taskMgr.workDir))

	case "agent_shutdown":
		var args struct {
			Confirm bool `json:"confirm"`
		}
		json.Unmarshal(call.Arguments, &args)
		if !args.Confirm {
			return mcpToolError("You must pass confirm: true to shut down the agent.")
		}
		log.Printf("[MCP] Shutdown requested")
		// Trigger shutdown after returning the response
		go func() {
			time.Sleep(500 * time.Millisecond)
			if s.onShutdown != nil {
				s.onShutdown()
			}
		}()
		return mcpToolResult("Agent shutdown initiated. All running tasks will be stopped.")

	// --- Config Management ---
	case "config_set":
		var args struct {
			Key   string `json:"key"`
			Value string `json:"value"`
		}
		json.Unmarshal(call.Arguments, &args)
		if args.Key == "" || args.Value == "" {
			return mcpToolError("key and value are required")
		}
		cfg, err := LoadConfig()
		if err != nil {
			return mcpToolError(fmt.Sprintf("load config: %v", err))
		}
		switch args.Key {
		case "auto-start":
			cfg.AutoStart = args.Value == "true" || args.Value == "1" || args.Value == "yes"
			if err := SaveConfig(cfg); err != nil {
				return mcpToolError(fmt.Sprintf("save config: %v", err))
			}
			return mcpToolResult(fmt.Sprintf("auto-start set to %v", cfg.AutoStart))
		case "auto-update":
			cfg.AutoUpdate = args.Value == "true" || args.Value == "1" || args.Value == "yes"
			if err := SaveConfig(cfg); err != nil {
				return mcpToolError(fmt.Sprintf("save config: %v", err))
			}
			return mcpToolResult(fmt.Sprintf("auto-update set to %v", cfg.AutoUpdate))
		default:
			return mcpToolError(fmt.Sprintf("Unknown config key: %s. Supported: auto-start, auto-update", args.Key))
		}

	case "relay_test":
		var args struct {
			URL string `json:"url"`
		}
		json.Unmarshal(call.Arguments, &args)
		var urls []string
		if args.URL != "" {
			urls = []string{strings.TrimRight(args.URL, "/")}
		} else {
			cfg, err := LoadConfig()
			if err != nil {
				return mcpToolError(fmt.Sprintf("load config: %v", err))
			}
			for _, rs := range cfg.RelayServers {
				urls = append(urls, rs.HttpURL)
			}
			if len(urls) == 0 {
				return mcpToolResult("No relay servers configured. Use add_relay_server or pass a URL.")
			}
		}
		client := &http.Client{Timeout: 10 * time.Second}
		var sb strings.Builder
		for _, u := range urls {
			start := time.Now()
			resp, err := client.Get(u + "/health")
			rtt := time.Since(start)
			if err != nil {
				sb.WriteString(fmt.Sprintf("FAIL  %s  error: %v\n", u, err))
				continue
			}
			resp.Body.Close()
			if resp.StatusCode == 200 {
				sb.WriteString(fmt.Sprintf("OK    %s  %dms\n", u, rtt.Milliseconds()))
			} else {
				sb.WriteString(fmt.Sprintf("FAIL  %s  status: %d\n", u, resp.StatusCode))
			}
		}
		return mcpToolResult(sb.String())

	case "relay_set_password":
		var args struct {
			Password string `json:"password"`
		}
		json.Unmarshal(call.Arguments, &args)
		if args.Password == "" {
			return mcpToolError("password is required")
		}
		cfg, err := LoadConfig()
		if err != nil {
			return mcpToolError(fmt.Sprintf("load config: %v", err))
		}
		cfg.RelayPassword = args.Password
		if err := SaveConfig(cfg); err != nil {
			return mcpToolError(fmt.Sprintf("save config: %v", err))
		}
		signalRunningAgent()
		log.Printf("[MCP] Relay password set")
		return mcpToolResult("Relay password saved. Agent notified.")

	case "relay_clear_password":
		cfg, err := LoadConfig()
		if err != nil {
			return mcpToolError(fmt.Sprintf("load config: %v", err))
		}
		if cfg.RelayPassword == "" {
			return mcpToolResult("No relay password was set.")
		}
		cfg.RelayPassword = ""
		if err := SaveConfig(cfg); err != nil {
			return mcpToolError(fmt.Sprintf("save config: %v", err))
		}
		signalRunningAgent()
		log.Printf("[MCP] Relay password cleared")
		return mcpToolResult("Relay password cleared. Agent notified.")

	// --- Tunnel Management ---
	case "tunnel_list":
		cfg, err := LoadConfig()
		if err != nil {
			return mcpToolError(fmt.Sprintf("load config: %v", err))
		}
		if len(cfg.CloudflareTunnels) == 0 {
			return mcpToolResult("No Cloudflare Tunnels configured.\nAdd one with: yaver tunnel add <url>")
		}
		var sb strings.Builder
		sb.WriteString("Cloudflare Tunnels:\n")
		for _, t := range cfg.CloudflareTunnels {
			cfAccess := "no"
			if t.CFAccessClientId != "" {
				cfAccess = "yes"
			}
			label := t.Label
			if label == "" {
				label = "-"
			}
			sb.WriteString(fmt.Sprintf("- %s  %s  (CF Access: %s, label: %s)\n", t.ID, t.URL, cfAccess, label))
		}
		return mcpToolResult(sb.String())

	case "tunnel_add":
		var args struct {
			URL            string `json:"url"`
			CFClientId     string `json:"cf_client_id"`
			CFClientSecret string `json:"cf_client_secret"`
			Label          string `json:"label"`
		}
		json.Unmarshal(call.Arguments, &args)
		if args.URL == "" {
			return mcpToolError("url is required")
		}
		rawURL := strings.TrimRight(args.URL, "/")
		id := fmt.Sprintf("%x", func() uint32 {
			var h uint32
			for _, c := range rawURL {
				h = h*31 + uint32(c)
			}
			return h
		}())
		if len(id) > 8 {
			id = id[:8]
		}
		cfg, err := LoadConfig()
		if err != nil {
			return mcpToolError(fmt.Sprintf("load config: %v", err))
		}
		for _, t := range cfg.CloudflareTunnels {
			if t.URL == rawURL {
				return mcpToolError(fmt.Sprintf("Tunnel already configured: %s (id: %s)", rawURL, t.ID))
			}
		}
		tunnel := CloudflareTunnelConfig{
			ID:                   id,
			URL:                  rawURL,
			CFAccessClientId:     args.CFClientId,
			CFAccessClientSecret: args.CFClientSecret,
			Label:                args.Label,
			Priority:             len(cfg.CloudflareTunnels) + 1,
		}
		cfg.CloudflareTunnels = append(cfg.CloudflareTunnels, tunnel)
		if err := SaveConfig(cfg); err != nil {
			return mcpToolError(fmt.Sprintf("save config: %v", err))
		}
		log.Printf("[MCP] Added Cloudflare Tunnel: %s", rawURL)
		return mcpToolResult(fmt.Sprintf("Added Cloudflare Tunnel:\n  ID: %s\n  URL: %s\n  CF Access: %v", id, rawURL, args.CFClientId != ""))

	case "tunnel_remove":
		var args struct {
			TunnelID string `json:"tunnel_id"`
		}
		json.Unmarshal(call.Arguments, &args)
		if args.TunnelID == "" {
			return mcpToolError("tunnel_id is required")
		}
		cfg, err := LoadConfig()
		if err != nil {
			return mcpToolError(fmt.Sprintf("load config: %v", err))
		}
		found := false
		var remaining []CloudflareTunnelConfig
		for _, t := range cfg.CloudflareTunnels {
			if t.ID == args.TunnelID || t.URL == args.TunnelID {
				found = true
				log.Printf("[MCP] Removed Cloudflare Tunnel: %s (%s)", t.URL, t.ID)
			} else {
				remaining = append(remaining, t)
			}
		}
		if !found {
			return mcpToolError(fmt.Sprintf("Tunnel not found: %s", args.TunnelID))
		}
		cfg.CloudflareTunnels = remaining
		if err := SaveConfig(cfg); err != nil {
			return mcpToolError(fmt.Sprintf("save config: %v", err))
		}
		return mcpToolResult(fmt.Sprintf("Removed tunnel: %s", args.TunnelID))

	case "tunnel_test":
		var args struct {
			URL string `json:"url"`
		}
		json.Unmarshal(call.Arguments, &args)
		var tunnels []CloudflareTunnelConfig
		if args.URL != "" {
			tunnels = []CloudflareTunnelConfig{{URL: strings.TrimRight(args.URL, "/")}}
		} else {
			cfg, err := LoadConfig()
			if err != nil {
				return mcpToolError(fmt.Sprintf("load config: %v", err))
			}
			tunnels = cfg.CloudflareTunnels
			if len(tunnels) == 0 {
				return mcpToolResult("No tunnels configured. Pass a URL or add with tunnel_add.")
			}
		}
		client := &http.Client{Timeout: 10 * time.Second}
		var sb strings.Builder
		for _, t := range tunnels {
			req, _ := http.NewRequest("GET", t.URL+"/health", nil)
			if t.CFAccessClientId != "" {
				req.Header.Set("CF-Access-Client-Id", t.CFAccessClientId)
				req.Header.Set("CF-Access-Client-Secret", t.CFAccessClientSecret)
			}
			start := time.Now()
			resp, err := client.Do(req)
			rtt := time.Since(start)
			if err != nil {
				sb.WriteString(fmt.Sprintf("FAIL  %s  error: %v\n", t.URL, err))
				continue
			}
			resp.Body.Close()
			if resp.StatusCode == 200 {
				sb.WriteString(fmt.Sprintf("OK    %s  %dms\n", t.URL, rtt.Milliseconds()))
			} else {
				sb.WriteString(fmt.Sprintf("FAIL  %s  status: %d\n", t.URL, resp.StatusCode))
			}
		}
		return mcpToolResult(sb.String())

	// --- Session Transfer ---
	case "session_list":
		sessions := ListTransferableSessions(s.taskMgr)
		if len(sessions) == 0 {
			return mcpToolResult("No transferable sessions found.")
		}
		var sb strings.Builder
		sb.WriteString("Transferable sessions:\n\n")
		for _, sess := range sessions {
			resumable := ""
			if sess.Resumable {
				resumable = " [resumable]"
			}
			sb.WriteString(fmt.Sprintf("- %s (%s) — \"%s\" [%s, %d turns]%s\n",
				sess.TaskID, sess.AgentType, sess.Title, sess.Status, sess.Turns, resumable))
			if sess.GitRemote != "" {
				sb.WriteString(fmt.Sprintf("  Git: %s @ %s\n", sess.GitRemote, sess.GitBranch))
			}
		}
		return mcpToolResult(sb.String())

	case "session_export":
		var args struct {
			TaskID           string `json:"task_id"`
			IncludeWorkspace bool   `json:"include_workspace"`
			WorkspaceMode    string `json:"workspace_mode"`
		}
		json.Unmarshal(call.Arguments, &args)
		if args.TaskID == "" {
			return mcpToolError("task_id is required")
		}
		bundle, err := ExportSession(s.taskMgr, args.TaskID, ExportOptions{
			IncludeWorkspace: args.IncludeWorkspace,
			WorkspaceMode:    args.WorkspaceMode,
		})
		if err != nil {
			return mcpToolError(err.Error())
		}
		bundleJSON, _ := json.MarshalIndent(bundle, "", "  ")
		return mcpToolResult(fmt.Sprintf("Session exported (%d bytes, %d turns, agent=%s).\n\nBundle JSON:\n%s",
			len(bundleJSON), len(bundle.Task.Turns), bundle.AgentType, string(bundleJSON)))

	case "session_import":
		var args struct {
			BundleJSON string `json:"bundle_json"`
			WorkDir    string `json:"work_dir"`
			GitClone   bool   `json:"git_clone"`
		}
		json.Unmarshal(call.Arguments, &args)
		if args.BundleJSON == "" {
			return mcpToolError("bundle_json is required")
		}
		var bundle TransferBundle
		if err := json.Unmarshal([]byte(args.BundleJSON), &bundle); err != nil {
			return mcpToolError(fmt.Sprintf("invalid bundle JSON: %v", err))
		}
		taskID, warnings, err := ImportSession(s.taskMgr, &bundle, ImportOptions{
			WorkDir:  args.WorkDir,
			GitClone: args.GitClone,
		})
		if err != nil {
			return mcpToolError(err.Error())
		}
		result := fmt.Sprintf("Session imported! Task ID: %s", taskID)
		if len(warnings) > 0 {
			result += "\n\nWarnings:\n"
			for _, w := range warnings {
				result += "- " + w + "\n"
			}
		}
		return mcpToolResult(result)

	case "session_transfer":
		var args struct {
			TaskID           string `json:"task_id"`
			TargetDevice     string `json:"target_device"`
			IncludeWorkspace bool   `json:"include_workspace"`
			WorkspaceMode    string `json:"workspace_mode"`
		}
		json.Unmarshal(call.Arguments, &args)
		if args.TaskID == "" || args.TargetDevice == "" {
			return mcpToolError("task_id and target_device are required")
		}
		// Export
		bundle, err := ExportSession(s.taskMgr, args.TaskID, ExportOptions{
			IncludeWorkspace: args.IncludeWorkspace,
			WorkspaceMode:    args.WorkspaceMode,
		})
		if err != nil {
			return mcpToolError(fmt.Sprintf("export failed: %v", err))
		}
		// Find target device
		cfg, err := LoadConfig()
		if err != nil {
			return mcpToolError(fmt.Sprintf("load config: %v", err))
		}
		targetURL := resolveDeviceURL(cfg, args.TargetDevice, true)
		// Send to target
		importBody, _ := json.Marshal(map[string]interface{}{
			"bundle":   bundle,
			"gitClone": args.WorkspaceMode == "git",
		})
		req, _ := http.NewRequest("POST", targetURL+"/session/import", bytes.NewReader(importBody))
		req.Header.Set("Authorization", "Bearer "+cfg.AuthToken)
		req.Header.Set("Content-Type", "application/json")
		client := &http.Client{Timeout: 120 * time.Second}
		resp, err := client.Do(req)
		if err != nil {
			return mcpToolError(fmt.Sprintf("transfer failed: %v", err))
		}
		defer resp.Body.Close()
		var importResp struct {
			OK       bool     `json:"ok"`
			TaskID   string   `json:"taskId"`
			Warnings []string `json:"warnings"`
			Error    string   `json:"error"`
		}
		json.NewDecoder(resp.Body).Decode(&importResp)
		if !importResp.OK {
			return mcpToolError(fmt.Sprintf("import failed on target: %s", importResp.Error))
		}
		result := fmt.Sprintf("Session transferred successfully!\nSource: %s (task %s)\nTarget: %s (task %s)",
			s.hostname, args.TaskID, args.TargetDevice, importResp.TaskID)
		if len(importResp.Warnings) > 0 {
			result += "\n\nWarnings:\n"
			for _, w := range importResp.Warnings {
				result += "- " + w + "\n"
			}
		}
		log.Printf("[MCP] Session transferred: %s -> %s (task %s -> %s)", s.hostname, args.TargetDevice, args.TaskID, importResp.TaskID)
		return mcpToolResult(result)

	// --- Exec ---
	case "exec_command":
		var args struct {
			Command string `json:"command"`
			WorkDir string `json:"work_dir"`
			Timeout int    `json:"timeout"`
		}
		json.Unmarshal(call.Arguments, &args)
		if args.Command == "" {
			return mcpToolError("command is required")
		}
		if s.execMgr == nil {
			return mcpToolError("exec is not enabled on this agent")
		}
		sess, err := s.execMgr.StartExec(args.Command, args.WorkDir, "", nil, args.Timeout)
		if err != nil {
			return mcpToolError(err.Error())
		}
		// Wait for completion (up to timeout)
		select {
		case <-sess.doneCh:
		case <-time.After(time.Duration(args.Timeout) * time.Second):
			if args.Timeout == 0 {
				<-sess.doneCh // wait forever if no timeout
			}
		}
		snapshot := sess.Snapshot()
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("Exit code: %v\n", snapshot["exitCode"]))
		if stdout, ok := snapshot["stdout"].(string); ok && stdout != "" {
			sb.WriteString("\n--- stdout ---\n")
			sb.WriteString(stdout)
		}
		if stderr, ok := snapshot["stderr"].(string); ok && stderr != "" {
			sb.WriteString("\n--- stderr ---\n")
			sb.WriteString(stderr)
		}
		return mcpToolResult(sb.String())

	case "schedule_task":
		var args struct {
			Title          string `json:"title"`
			RunAt          string `json:"run_at"`
			RepeatInterval int    `json:"repeat_interval"`
			Cron           string `json:"cron"`
			MaxRuns        int    `json:"max_runs"`
			Runner         string `json:"runner"`
		}
		json.Unmarshal(call.Arguments, &args)
		if args.Title == "" {
			return mcpToolError("title is required")
		}
		if s.scheduler == nil {
			return mcpToolError("scheduler not available")
		}
		st := &ScheduledTask{
			Title:          args.Title,
			RunAt:          args.RunAt,
			RepeatInterval: args.RepeatInterval,
			Cron:           args.Cron,
			MaxRuns:        args.MaxRuns,
			Runner:         args.Runner,
		}
		if err := s.scheduler.AddSchedule(st); err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolResult(fmt.Sprintf("Scheduled! ID: %s, Next run: %s", st.ID, st.NextRunAt))

	case "list_schedules":
		if s.scheduler == nil {
			return mcpToolError("scheduler not available")
		}
		schedules := s.scheduler.ListSchedules()
		if len(schedules) == 0 {
			return mcpToolResult("No scheduled tasks.")
		}
		var sb strings.Builder
		for _, st := range schedules {
			sb.WriteString(fmt.Sprintf("- %s [%s] \"%s\" next:%s runs:%d\n",
				st.ID, st.Status, st.Title, st.NextRunAt, st.RunCount))
		}
		return mcpToolResult(sb.String())

	case "cancel_schedule":
		var args struct {
			ScheduleID string `json:"schedule_id"`
		}
		json.Unmarshal(call.Arguments, &args)
		if s.scheduler == nil {
			return mcpToolError("scheduler not available")
		}
		if err := s.scheduler.RemoveSchedule(args.ScheduleID); err != nil {
			return mcpToolError(err.Error())
		}
		return mcpToolResult("Schedule cancelled: " + args.ScheduleID)

	case "notify":
		var args struct {
			Message string `json:"message"`
			Channel string `json:"channel"`
		}
		json.Unmarshal(call.Arguments, &args)
		if args.Message == "" {
			return mcpToolError("message is required")
		}
		if s.notifyMgr == nil {
			return mcpToolError("notifications not configured")
		}
		result := s.notifyMgr.TestNotification(args.Channel)
		// Actually send the custom message
		if args.Channel == "" {
			s.notifyMgr.sendAll(args.Message)
		} else {
			switch strings.ToLower(args.Channel) {
			case "telegram":
				s.notifyMgr.sendTelegram(args.Message)
			case "discord":
				s.notifyMgr.sendDiscord(args.Message)
			case "slack":
				s.notifyMgr.sendSlack(args.Message)
			case "teams":
				s.notifyMgr.sendTeams(args.Message)
			}
		}
		return mcpToolResult("Notification sent: " + result)

	case "integrations_list":
		cfg, _ := LoadConfig()
		if cfg == nil || cfg.Notifications == nil {
			return mcpToolResult("No integrations configured.\n\nAvailable channels: telegram, discord, slack, teams, linear, jira, pagerduty, opsgenie, email\n\nUse 'integrations_set' to configure.")
		}
		nc := cfg.Notifications
		var sb strings.Builder
		sb.WriteString("Configured integrations:\n\n")
		if nc.Telegram != nil {
			sb.WriteString(fmt.Sprintf("- Telegram: %s (chatId: %s)\n", boolStr(nc.Telegram.Enabled), nc.Telegram.ChatID))
		}
		if nc.Discord != nil {
			sb.WriteString(fmt.Sprintf("- Discord: %s\n", boolStr(nc.Discord.Enabled)))
		}
		if nc.Slack != nil {
			sb.WriteString(fmt.Sprintf("- Slack: %s\n", boolStr(nc.Slack.Enabled)))
		}
		if nc.Teams != nil {
			sb.WriteString(fmt.Sprintf("- Teams: %s\n", boolStr(nc.Teams.Enabled)))
		}
		if nc.Linear != nil {
			sb.WriteString(fmt.Sprintf("- Linear: %s (team: %s)\n", boolStr(nc.Linear.Enabled), nc.Linear.TeamID))
		}
		if nc.Jira != nil {
			sb.WriteString(fmt.Sprintf("- Jira: %s (project: %s)\n", boolStr(nc.Jira.Enabled), nc.Jira.ProjectKey))
		}
		if nc.PagerDuty != nil {
			sb.WriteString(fmt.Sprintf("- PagerDuty: %s (failOnly: %v)\n", boolStr(nc.PagerDuty.Enabled), nc.PagerDuty.OnFailOnly))
		}
		if nc.Opsgenie != nil {
			sb.WriteString(fmt.Sprintf("- Opsgenie: %s (failOnly: %v)\n", boolStr(nc.Opsgenie.Enabled), nc.Opsgenie.OnFailOnly))
		}
		if nc.Email != nil {
			sb.WriteString(fmt.Sprintf("- Email: %s (to: %s)\n", boolStr(nc.Email.Enabled), nc.Email.To))
		}
		if sb.Len() == len("Configured integrations:\n\n") {
			sb.WriteString("(none configured)\n")
		}
		sb.WriteString("\nAvailable: telegram, discord, slack, teams, linear, jira, pagerduty, opsgenie, email")
		return mcpToolResult(sb.String())

	case "integrations_set":
		var args struct {
			Channel string          `json:"channel"`
			Config  json.RawMessage `json:"config"`
		}
		json.Unmarshal(call.Arguments, &args)
		if args.Channel == "" {
			return mcpToolError("channel is required (telegram, discord, slack, teams, linear, jira, pagerduty, opsgenie, email)")
		}
		cfg, _ := LoadConfig()
		if cfg == nil {
			cfg = &Config{}
		}
		if cfg.Notifications == nil {
			cfg.Notifications = &NotificationConfig{}
		}
		nc := cfg.Notifications
		ch := strings.ToLower(args.Channel)
		switch ch {
		case "telegram":
			var c TelegramConfig
			json.Unmarshal(args.Config, &c)
			nc.Telegram = &c
		case "discord":
			var c DiscordConfig
			json.Unmarshal(args.Config, &c)
			nc.Discord = &c
		case "slack":
			var c SlackConfig
			json.Unmarshal(args.Config, &c)
			nc.Slack = &c
		case "teams":
			var c TeamsConfig
			json.Unmarshal(args.Config, &c)
			nc.Teams = &c
		case "linear":
			var c LinearConfig
			json.Unmarshal(args.Config, &c)
			nc.Linear = &c
		case "jira":
			var c JiraConfig
			json.Unmarshal(args.Config, &c)
			nc.Jira = &c
		case "pagerduty":
			var c PagerDutyConfig
			json.Unmarshal(args.Config, &c)
			nc.PagerDuty = &c
		case "opsgenie":
			var c OpsgenieConfig
			json.Unmarshal(args.Config, &c)
			nc.Opsgenie = &c
		case "email":
			var c EmailNotifyConfig
			json.Unmarshal(args.Config, &c)
			nc.Email = &c
		default:
			return mcpToolError("unknown channel: " + ch)
		}
		if err := SaveConfig(cfg); err != nil {
			return mcpToolError("failed to save config: " + err.Error())
		}
		if s.notifyMgr != nil {
			s.notifyMgr.UpdateConfig(nc)
		}
		return mcpToolResult(fmt.Sprintf("Integration '%s' configured and saved.", ch))

	case "integrations_test":
		var args struct {
			Channel string `json:"channel"`
		}
		json.Unmarshal(call.Arguments, &args)
		if s.notifyMgr == nil {
			return mcpToolError("notifications not configured")
		}
		result := s.notifyMgr.TestNotification(args.Channel)
		return mcpToolResult(result)

	// --- Docker ---
	case "docker_ps":
		return mcpToolJSON(mcpDockerPS())
	case "docker_logs":
		var args struct {
			Container string `json:"container"`
			Tail      int    `json:"tail"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpDockerLogs(args.Container, args.Tail))
	case "docker_exec":
		var args struct {
			Container string `json:"container"`
			Command   string `json:"command"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpDockerExec(args.Container, args.Command))
	case "docker_images":
		return mcpToolJSON(mcpDockerImages())
	case "docker_compose":
		var args struct {
			Action    string `json:"action"`
			Directory string `json:"directory"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpDockerCompose(args.Action, args.Directory))

	// --- Test Runner ---
	case "run_tests":
		var args struct {
			Command   string `json:"command"`
			Directory string `json:"directory"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpRunTests(args.Command, args.Directory))

	// --- HTTP Client ---
	case "http_request":
		var args struct {
			URL     string            `json:"url"`
			Method  string            `json:"method"`
			Headers map[string]string `json:"headers"`
			Body    string            `json:"body"`
		}
		json.Unmarshal(call.Arguments, &args)
		if args.Method == "" {
			args.Method = "GET"
		}
		return mcpToolJSON(mcpHTTPRequest(args.Method, args.URL, args.Headers, args.Body))

	// --- Log Tail ---
	case "tail_logs":
		var args struct {
			Path  string `json:"path"`
			Lines int    `json:"lines"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpTailLogs(args.Path, args.Lines))

	// --- Clipboard ---
	case "clipboard_read":
		return mcpToolJSON(mcpClipboardRead())
	case "clipboard_write":
		var args struct {
			Content string `json:"content"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpClipboardWrite(args.Content))

	// --- Process Management ---
	case "process_list":
		var args struct {
			Filter string `json:"filter"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpProcessList(args.Filter))
	case "process_kill":
		var args struct {
			PID    int    `json:"pid"`
			Signal string `json:"signal"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpProcessKill(args.PID, args.Signal))
	case "port_check":
		var args struct {
			Port int `json:"port"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpPortCheck(args.Port))

	// --- Code Quality ---
	case "lint":
		var args struct {
			Directory string `json:"directory"`
			Tool      string `json:"tool"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpLint(args.Directory, args.Tool))
	case "format_code":
		var args struct {
			Directory string `json:"directory"`
			Tool      string `json:"tool"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpFormat(args.Directory, args.Tool))
	case "type_check":
		var args struct {
			Directory string `json:"directory"`
			Tool      string `json:"tool"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpTypeCheck(args.Directory, args.Tool))

	// --- Package Dependencies ---
	case "deps_outdated":
		var args struct {
			Directory string `json:"directory"`
			Manager   string `json:"manager"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpDepsOutdated(args.Directory, args.Manager))
	case "deps_audit":
		var args struct {
			Directory string `json:"directory"`
			Manager   string `json:"manager"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpDepsAudit(args.Directory, args.Manager))
	case "deps_list":
		var args struct {
			Directory string `json:"directory"`
			Manager   string `json:"manager"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpDepsList(args.Directory, args.Manager))

	// --- GitHub ---
	case "github_prs":
		var args struct {
			Directory string `json:"directory"`
			State     string `json:"state"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpGitHubPRs(args.Directory, args.State))
	case "github_issues":
		var args struct {
			Directory string `json:"directory"`
			State     string `json:"state"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpGitHubIssues(args.Directory, args.State))
	case "github_ci_status":
		var args struct {
			Directory string `json:"directory"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpGitHubCIStatus(args.Directory))

	// --- Database ---
	case "db_query":
		var args struct {
			Driver string `json:"driver"`
			DSN    string `json:"dsn"`
			Query  string `json:"query"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpDBQuery(args.Driver, args.DSN, args.Query))
	case "db_schema":
		var args struct {
			Driver string `json:"driver"`
			DSN    string `json:"dsn"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpDBSchemas(args.Driver, args.DSN))

	// --- Network Diagnostics ---
	case "dns_lookup":
		var args struct {
			Host string `json:"host"`
			Type string `json:"type"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpDNSLookup(args.Host, args.Type))
	case "ping":
		var args struct {
			Host  string `json:"host"`
			Count int    `json:"count"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpPing(args.Host, args.Count))
	case "ssl_check":
		var args struct {
			Host string `json:"host"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpSSLCheck(args.Host))
	case "http_timing":
		var args struct {
			URL string `json:"url"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpHTTPTiming(args.URL))

	// --- Data Tools ---
	case "base64":
		var args struct {
			Action string `json:"action"`
			Input  string `json:"input"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpBase64(args.Action, args.Input))
	case "hash":
		var args struct {
			Input     string `json:"input"`
			Algorithm string `json:"algorithm"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpHash(args.Algorithm, args.Input))
	case "uuid":
		return mcpToolJSON(mcpUUID())
	case "jq":
		var args struct {
			Expression string `json:"expression"`
			Input      string `json:"input"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpJQ(args.Expression, args.Input))
	case "regex_test":
		var args struct {
			Pattern string `json:"pattern"`
			Input   string `json:"input"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpRegexTest(args.Pattern, args.Input))

	// --- Archive ---
	case "archive_create":
		var args struct {
			Source string `json:"source"`
			Output string `json:"output"`
			Format string `json:"format"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpArchiveCreate(args.Format, args.Source, args.Output))
	case "archive_extract":
		var args struct {
			Path        string `json:"path"`
			Destination string `json:"destination"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpArchiveExtract(args.Path, args.Destination))

	// --- System Services ---
	case "service_status":
		var args struct {
			Name string `json:"name"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpServiceStatus(args.Name))
	case "service_action":
		var args struct {
			Name   string `json:"name"`
			Action string `json:"action"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpServiceAction(args.Name, args.Action))
	case "service_list":
		return mcpToolJSON(mcpServiceList())

	// --- Benchmark ---
	case "benchmark":
		var args struct {
			Command   string `json:"command"`
			Directory string `json:"directory"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpBenchmark(args.Command, args.Directory))

	// --- Diff ---
	case "diff":
		var args struct {
			PathA string `json:"path_a"`
			PathB string `json:"path_b"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpDiff(args.PathA, args.PathB))

	// --- Environment ---
	case "env_list":
		var args struct {
			Filter string `json:"filter"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpEnvList(args.Filter))
	case "env_read":
		var args struct {
			Path string `json:"path"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpEnvRead(args.Path))

	// --- Crontab ---
	case "crontab":
		var args struct {
			Action string `json:"action"`
			Entry  string `json:"entry"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpCrontab(args.Action, args.Entry))

	// --- Cloud CLI ---
	case "cloud_cli":
		var args struct {
			Provider string   `json:"provider"`
			Args     []string `json:"args"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpCloudCmd(args.Provider, args.Args))

	// --- Home Assistant ---
	case "ha_states":
		var args struct {
			Filter string `json:"filter"`
			URL    string `json:"url"`
			Token  string `json:"token"`
		}
		json.Unmarshal(call.Arguments, &args)
		if args.Token == "" {
			cfg, _ := LoadConfig()
			if cfg.HAToken != "" {
				args.Token = cfg.HAToken
			}
			if cfg.HAURL != "" && args.URL == "" {
				args.URL = cfg.HAURL
			}
		}
		return mcpToolJSON(mcpHAStates(args.URL, args.Token, args.Filter))
	case "ha_service":
		var args struct {
			Domain  string                 `json:"domain"`
			Service string                 `json:"service"`
			Data    map[string]interface{} `json:"data"`
			URL     string                 `json:"url"`
			Token   string                 `json:"token"`
		}
		json.Unmarshal(call.Arguments, &args)
		if args.Token == "" {
			cfg, _ := LoadConfig()
			if cfg.HAToken != "" {
				args.Token = cfg.HAToken
			}
			if cfg.HAURL != "" && args.URL == "" {
				args.URL = cfg.HAURL
			}
		}
		return mcpToolJSON(mcpHAService(args.URL, args.Token, args.Domain, args.Service, args.Data))
	case "ha_toggle":
		var args struct {
			EntityID string `json:"entity_id"`
			URL      string `json:"url"`
			Token    string `json:"token"`
		}
		json.Unmarshal(call.Arguments, &args)
		if args.Token == "" {
			cfg, _ := LoadConfig()
			if cfg.HAToken != "" {
				args.Token = cfg.HAToken
			}
			if cfg.HAURL != "" && args.URL == "" {
				args.URL = cfg.HAURL
			}
		}
		return mcpToolJSON(mcpHAToggle(args.URL, args.Token, args.EntityID))
	case "mqtt_publish":
		var args struct {
			Topic   string `json:"topic"`
			Message string `json:"message"`
			Broker  string `json:"broker"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpMQTTPublish(args.Broker, args.Topic, args.Message))

	// --- Desktop Control ---
	case "desktop_notify":
		var args struct {
			Title   string `json:"title"`
			Message string `json:"message"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpDesktopNotify(args.Title, args.Message))
	case "open_url":
		var args struct {
			URL string `json:"url"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpOpenURL(args.URL))
	case "volume":
		var args struct {
			Action string `json:"action"`
			Level  int    `json:"level"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpVolume(args.Action, args.Level))
	case "screen_lock":
		return mcpToolJSON(mcpScreenLock())
	case "say":
		var args struct {
			Text string `json:"text"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpSay(args.Text))
	case "brightness":
		var args struct {
			Action string `json:"action"`
			Level  int    `json:"level"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpBrightness(args.Action, args.Level))

	// --- Music ---
	case "music":
		var args struct {
			Action string `json:"action"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpMusicControl(args.Action))

	// --- Weather ---
	case "weather":
		var args struct {
			Location string `json:"location"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpWeather(args.Location))

	// --- System Extras ---
	case "battery":
		return mcpToolJSON(mcpBattery())
	case "disk_usage":
		var args struct {
			Path string `json:"path"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpDiskUsage(args.Path))
	case "wifi_info":
		return mcpToolJSON(mcpWiFiInfo())
	case "public_ip":
		return mcpToolJSON(mcpPublicIP())
	case "uptime":
		return mcpToolJSON(mcpUptime())
	case "speed_test":
		return mcpToolJSON(mcpSpeedTest())
	case "site_check":
		var args struct {
			URL string `json:"url"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpSiteCheck(args.URL))

	// --- Utilities ---
	case "password_gen":
		var args struct {
			Length    int  `json:"length"`
			NoSymbols bool `json:"no_symbols"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpPasswordGen(args.Length, args.NoSymbols))
	case "qr_code":
		var args struct {
			Text string `json:"text"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpQRCode(args.Text))
	case "timer":
		var args struct {
			Seconds int    `json:"seconds"`
			Label   string `json:"label"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpTimer(args.Seconds, args.Label))
	case "calculate":
		var args struct {
			Expression string `json:"expression"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpCalculate(args.Expression))
	case "world_clock":
		var args struct {
			Timezones []string `json:"timezones"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpWorldClock(args.Timezones))
	case "countdown":
		var args struct {
			Date string `json:"date"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpCountdown(args.Date))
	case "convert_units":
		var args struct {
			Value float64 `json:"value"`
			From  string  `json:"from"`
			To    string  `json:"to"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpConvertUnits(args.Value, args.From, args.To))

	// --- Philips Hue ---
	case "hue_lights":
		var args struct {
			BridgeIP string `json:"bridge_ip"`
			APIKey   string `json:"api_key"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpHueLights(args.BridgeIP, args.APIKey))
	case "hue_control":
		var args struct {
			BridgeIP   string `json:"bridge_ip"`
			APIKey     string `json:"api_key"`
			LightID    string `json:"light_id"`
			Action     string `json:"action"`
			Brightness int    `json:"brightness"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpHueControl(args.BridgeIP, args.APIKey, args.LightID, args.Action, args.Brightness))
	case "hue_scenes":
		var args struct {
			BridgeIP string `json:"bridge_ip"`
			APIKey   string `json:"api_key"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpHueScenes(args.BridgeIP, args.APIKey))

	// --- Shelly ---
	case "shelly_status":
		var args struct {
			IP string `json:"ip"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpShellyStatus(args.IP))
	case "shelly_control":
		var args struct {
			IP      string `json:"ip"`
			Action  string `json:"action"`
			Channel int    `json:"channel"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpShellyControl(args.IP, args.Channel, args.Action))
	case "shelly_power":
		var args struct {
			IP string `json:"ip"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpShellyPower(args.IP))

	// --- Elgato Key Light ---
	case "elgato_status":
		var args struct {
			IP string `json:"ip"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpElgatoStatus(args.IP))
	case "elgato_control":
		var args struct {
			IP          string `json:"ip"`
			On          bool   `json:"on"`
			Brightness  int    `json:"brightness"`
			Temperature int    `json:"temperature"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpElgatoControl(args.IP, args.On, args.Brightness, args.Temperature))

	// --- Nanoleaf ---
	case "nanoleaf":
		var args struct {
			IP         string `json:"ip"`
			Token      string `json:"token"`
			Action     string `json:"action"`
			Brightness int    `json:"brightness"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpNanoleafControl(args.IP, args.Token, args.Action, args.Brightness))

	// --- Tasmota ---
	case "tasmota":
		var args struct {
			IP      string `json:"ip"`
			Command string `json:"command"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpTasmotaControl(args.IP, args.Command))

	// --- Govee ---
	case "govee_devices":
		var args struct {
			APIKey string `json:"api_key"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpGoveeDevices(args.APIKey))
	case "govee_control":
		var args struct {
			APIKey     string         `json:"api_key"`
			Device     string         `json:"device"`
			Model      string         `json:"model"`
			Action     string         `json:"action"`
			Brightness int            `json:"brightness"`
			Color      map[string]int `json:"color"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpGoveeControl(args.APIKey, args.Device, args.Model, args.Action, args.Brightness, args.Color))

	// --- Wake on LAN ---
	case "wake_on_lan":
		var args struct {
			MAC string `json:"mac"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpWakeOnLAN(args.MAC))

	// --- Apple Shortcuts ---
	case "run_shortcut":
		var args struct {
			Name  string `json:"name"`
			Input string `json:"input"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpRunShortcut(args.Name, args.Input))
	case "list_shortcuts":
		return mcpToolJSON(mcpListShortcuts())

	// --- ADB ---
	case "adb_devices":
		return mcpToolJSON(mcpADBDevices())
	case "adb_command":
		var args struct {
			Command string `json:"command"`
			Device  string `json:"device"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpADBCommand(args.Device, args.Command))
	case "adb_screenshot":
		var args struct {
			Device string `json:"device"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpADBScreenshot(args.Device))

	// --- Sonos ---
	case "sonos_discover":
		return mcpToolJSON(mcpSonosDiscover())
	case "sonos_control":
		var args struct {
			IP     string `json:"ip"`
			Action string `json:"action"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpSonosControl(args.IP, args.Action))

	// --- Productivity & Sharing ---
	case "standup":
		var args struct {
			Directory string `json:"directory"`
			Days      int    `json:"days"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpStandup(args.Directory, args.Days))
	case "create_gist":
		var args struct {
			Content     string `json:"content"`
			Filename    string `json:"filename"`
			Description string `json:"description"`
			Public      bool   `json:"public"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpCreateGist(args.Filename, args.Content, args.Description, args.Public))
	case "changelog":
		var args struct {
			Directory string `json:"directory"`
			From      string `json:"from"`
			To        string `json:"to"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpChangelog(args.Directory, args.From, args.To))
	case "commit_message":
		var args struct {
			Directory string `json:"directory"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpCommitMessage(args.Directory))
	case "gitignore":
		var args struct {
			Languages []string `json:"languages"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpGitignore(args.Languages))
	case "license":
		var args struct {
			Type   string `json:"type"`
			Author string `json:"author"`
			Year   int    `json:"year"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpLicense(args.Type, args.Author, args.Year))
	case "color":
		var args struct {
			Input string `json:"input"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpColor(args.Input))
	case "figlet":
		var args struct {
			Text string `json:"text"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpFiglet(args.Text))
	case "lorem_ipsum":
		var args struct {
			Paragraphs int `json:"paragraphs"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpLoremIpsum(args.Paragraphs))
	case "tldr":
		var args struct {
			Command string `json:"command"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpTldr(args.Command))
	case "github_badge":
		var args struct {
			Directory string `json:"directory"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpGitHubBadge(args.Directory))
	case "invite":
		var args struct {
			Method    string `json:"method"`
			Recipient string `json:"recipient"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpInvite(args.Method, args.Recipient))
	case "git_stats":
		var args struct {
			Directory string `json:"directory"`
			Days      int    `json:"days"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpGitStats(args.Directory, args.Days))

	// --- Location & Lifestyle ---
	case "ev_charging":
		var args struct {
			Lat           float64 `json:"lat"`
			Lon           float64 `json:"lon"`
			Radius        int     `json:"radius"`
			ConnectorType string  `json:"connector_type"`
			Network       string  `json:"network"`
			Country       string  `json:"country"`
			MinPowerKW    int     `json:"min_power_kw"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpEVCharging(args.Lat, args.Lon, args.Radius, args.ConnectorType, args.Network, args.Country, args.MinPowerKW))
	case "ev_networks":
		var args struct { Country string `json:"country"` }
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpEVNetworks(args.Country))
	case "ev_connector_types":
		return mcpToolJSON(mcpEVConnectorTypes())
	case "nobetci_eczane":
		var args struct { City string `json:"city"`; District string `json:"district"` }
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpNobetciEczane(args.City, args.District))
	case "eczane_nearby":
		var args struct { Lat float64 `json:"lat"`; Lon float64 `json:"lon"`; Radius int `json:"radius"` }
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpEczaneSearch(args.Lat, args.Lon, args.Radius))
	case "places_search":
		var args struct {
			Query string  `json:"query"`
			Lat   float64 `json:"lat"`
			Lon   float64 `json:"lon"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpPlacesSearch(args.Query, args.Lat, args.Lon))
	case "restaurants":
		var args struct {
			Lat     float64 `json:"lat"`
			Lon     float64 `json:"lon"`
			Radius  int     `json:"radius"`
			Cuisine string  `json:"cuisine"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpRestaurants(args.Lat, args.Lon, args.Radius, args.Cuisine))
	case "hotels":
		var args struct {
			Lat    float64 `json:"lat"`
			Lon    float64 `json:"lon"`
			Radius int     `json:"radius"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpHotels(args.Lat, args.Lon, args.Radius))
	case "geocode":
		var args struct {
			Address string `json:"address"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpGeocode(args.Address))
	case "directions":
		var args struct {
			FromLat float64 `json:"from_lat"`
			FromLon float64 `json:"from_lon"`
			ToLat   float64 `json:"to_lat"`
			ToLon   float64 `json:"to_lon"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpDirections(args.FromLat, args.FromLon, args.ToLat, args.ToLon))
	case "news":
		var args struct {
			Source string `json:"source"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpNews(args.Source))
	case "stock_price":
		var args struct {
			Symbol string `json:"symbol"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpStockPrice(args.Symbol))
	case "translate":
		var args struct {
			Text   string `json:"text"`
			From   string `json:"from"`
			To     string `json:"to"`
			APIURL string `json:"api_url"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpTranslate(args.Text, args.From, args.To, args.APIURL))

	// --- Daily Dev Tools ---
	case "crypto_price":
		var args struct {
			Coins []string `json:"coins"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpCryptoPrice(args.Coins))
	case "currency_exchange":
		var args struct {
			Amount float64 `json:"amount"`
			From   string  `json:"from"`
			To     string  `json:"to"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpCurrencyExchange(args.Amount, args.From, args.To))
	case "npm_info":
		var args struct {
			Package string `json:"package"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpNPMInfo(args.Package))
	case "github_trending":
		var args struct {
			Language string `json:"language"`
			Since    string `json:"since"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpGitHubTrending(args.Language, args.Since))
	case "jwt_decode":
		var args struct {
			Token string `json:"token"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpJWTDecode(args.Token))
	case "epoch":
		var args struct {
			Input string `json:"input"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpEpoch(args.Input))
	case "cron_explain":
		var args struct {
			Expression string `json:"expression"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpCronExplain(args.Expression))
	case "http_status":
		var args struct {
			Code int `json:"code"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpHTTPStatusLookup(args.Code))
	case "whois":
		var args struct {
			Domain string `json:"domain"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpWhois(args.Domain))
	case "ip_geo":
		var args struct {
			IP string `json:"ip"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpIPGeo(args.IP))
	case "subnet_calc":
		var args struct {
			CIDR string `json:"cidr"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpSubnet(args.CIDR))
	case "fake_data":
		var args struct {
			Type  string `json:"type"`
			Count int    `json:"count"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpFakeData(args.Type, args.Count))
	case "domain_check":
		var args struct {
			Domain string `json:"domain"`
		}
		json.Unmarshal(call.Arguments, &args)
		return mcpToolJSON(mcpDomainCheck(args.Domain))

	// --- Kubernetes ---
	case "k8s_pods":
		var a struct { NS string `json:"namespace"`; Ctx string `json:"context"` }
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpK8sPods(a.NS, a.Ctx))
	case "k8s_logs":
		var a struct { Pod string `json:"pod"`; NS string `json:"namespace"`; Ctx string `json:"context"`; Container string `json:"container"`; Tail int `json:"tail"` }
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpK8sLogs(a.Pod, a.NS, a.Ctx, a.Container, a.Tail))
	case "k8s_describe":
		var a struct { Resource string `json:"resource"`; Name string `json:"name"`; NS string `json:"namespace"`; Ctx string `json:"context"` }
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpK8sDescribe(a.Resource, a.Name, a.NS, a.Ctx))
	case "k8s_get":
		var a struct { Resource string `json:"resource"`; NS string `json:"namespace"`; Ctx string `json:"context"` }
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpK8sGet(a.Resource, a.NS, a.Ctx))
	case "k8s_apply":
		var a struct { File string `json:"file"`; NS string `json:"namespace"`; Ctx string `json:"context"` }
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpK8sApply(a.File, a.NS, a.Ctx))
	case "k8s_exec":
		var a struct { Pod string `json:"pod"`; Command string `json:"command"`; NS string `json:"namespace"`; Ctx string `json:"context"`; Container string `json:"container"` }
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpK8sExec(a.Pod, a.NS, a.Ctx, a.Command, a.Container))
	case "k8s_contexts":
		return mcpToolJSON(mcpK8sContexts())
	case "k8s_namespaces":
		var a struct { Ctx string `json:"context"` }
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpK8sNamespaces(a.Ctx))
	case "k8s_top":
		var a struct { Resource string `json:"resource"`; NS string `json:"namespace"`; Ctx string `json:"context"` }
		json.Unmarshal(call.Arguments, &a)
		if a.Resource == "nodes" {
			return mcpToolJSON(mcpK8sTopNodes(a.Ctx))
		}
		return mcpToolJSON(mcpK8sTopPods(a.NS, a.Ctx))
	case "k8s_events":
		var a struct { NS string `json:"namespace"`; Ctx string `json:"context"` }
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpK8sEvents(a.NS, a.Ctx))

	// --- Terraform ---
	case "tf_plan":
		var a struct { Dir string `json:"directory"` }
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpTerraformPlan(a.Dir))
	case "tf_apply":
		var a struct { Dir string `json:"directory"`; Auto bool `json:"auto_approve"` }
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpTerraformApply(a.Dir, a.Auto))
	case "tf_state":
		var a struct { Dir string `json:"directory"` }
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpTerraformState(a.Dir))
	case "tf_output":
		var a struct { Dir string `json:"directory"` }
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpTerraformOutput(a.Dir))
	case "tf_init":
		var a struct { Dir string `json:"directory"` }
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpTerraformInit(a.Dir))
	case "tf_validate":
		var a struct { Dir string `json:"directory"` }
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpTerraformValidate(a.Dir))

	// --- Serverless ---
	case "lambda_list":
		return mcpToolJSON(mcpLambdaList())
	case "lambda_invoke":
		var a struct { Name string `json:"name"`; Payload string `json:"payload"` }
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpLambdaInvoke(a.Name, a.Payload))
	case "lambda_logs":
		var a struct { Name string `json:"name"`; Minutes int `json:"minutes"` }
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpLambdaLogs(a.Name, a.Minutes))

	// --- Vercel ---
	case "vercel_status":
		var a struct { Dir string `json:"directory"` }
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpVercelStatus(a.Dir))
	case "vercel_logs":
		var a struct { URL string `json:"url"` }
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpVercelLogs(a.URL))
	case "vercel_env":
		var a struct { Dir string `json:"directory"` }
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpVercelEnv(a.Dir))

	// --- Netlify ---
	case "netlify_status":
		var a struct { Dir string `json:"directory"` }
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpNetlifyStatus(a.Dir))

	// --- Sentry ---
	case "sentry_issues":
		var a struct { Org string `json:"org"`; Project string `json:"project"` }
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpSentryIssues(a.Org, a.Project))

	// --- Linear ---
	case "linear_issues":
		var a struct { APIKey string `json:"api_key"`; Team string `json:"team"` }
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpLinearIssues(a.APIKey, a.Team))

	// --- Notion ---
	case "notion_search":
		var a struct { APIKey string `json:"api_key"`; Query string `json:"query"` }
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpNotionSearch(a.APIKey, a.Query))

	// --- 1Password ---
	case "op_get":
		var a struct { Item string `json:"item"`; Vault string `json:"vault"` }
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpOnePasswordGet(a.Item, a.Vault))
	case "op_list":
		var a struct { Vault string `json:"vault"` }
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpOnePasswordList(a.Vault))

	// --- Raycast ---
	case "raycast":
		var a struct { Command string `json:"command"` }
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpRaycastTrigger(a.Command))

	// --- App Store / iOS ---
	case "appstore_status":
		var a struct { BundleID string `json:"bundle_id"` }
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpAppStoreStatus(a.BundleID))
	case "testflight_builds":
		var a struct { BundleID string `json:"bundle_id"` }
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpAppStoreTestFlight(a.BundleID))
	case "xcode_build":
		var a struct { Dir string `json:"directory"`; Scheme string `json:"scheme"`; Dest string `json:"destination"` }
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpXcodeBuild(a.Dir, a.Scheme, a.Dest))
	case "xcode_test":
		var a struct { Dir string `json:"directory"`; Scheme string `json:"scheme"`; Dest string `json:"destination"` }
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpXcodeTest(a.Dir, a.Scheme, a.Dest))
	case "simulators":
		return mcpToolJSON(mcpSimulators())
	case "simulator_boot":
		var a struct { Device string `json:"device"` }
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpSimulatorBoot(a.Device))
	case "simulator_screenshot":
		var a struct { Device string `json:"device"` }
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpSimulatorScreenshot(a.Device))

	// --- Google Play / Android ---
	case "playstore_status":
		var a struct { Package string `json:"package"` }
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpPlayStoreStatus(a.Package))
	case "playstore_track":
		var a struct { Package string `json:"package"`; Track string `json:"track"` }
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpPlayStoreTrack(a.Package, a.Track))
	case "gradle_build":
		var a struct { Dir string `json:"directory"`; Task string `json:"task"` }
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpGradleBuild(a.Dir, a.Task))
	case "gradle_test":
		var a struct { Dir string `json:"directory"` }
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpGradleTest(a.Dir))
	case "android_lint":
		var a struct { Dir string `json:"directory"` }
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpAndroidLint(a.Dir))
	case "emulators":
		return mcpToolJSON(mcpEmulators())

	// --- Firebase ---
	case "firebase_projects":
		return mcpToolJSON(mcpFirebaseProjects())
	case "firebase_deploy":
		var a struct { Dir string `json:"directory"`; Only string `json:"only"` }
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpFirebaseDeploy(a.Dir, a.Only))
	case "firebase_crashlytics":
		var a struct { ProjectID string `json:"project_id"` }
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpFirebaseCrashlytics(a.ProjectID))

	// --- React Native / Expo ---
	case "expo_status":
		var a struct { Dir string `json:"directory"` }
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpExpoStatus(a.Dir))
	case "eas_build":
		var a struct { Dir string `json:"directory"`; Platform string `json:"platform"` }
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpExpoBuild(a.Dir, a.Platform))
	case "eas_submit":
		var a struct { Dir string `json:"directory"`; Platform string `json:"platform"` }
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpEASSubmit(a.Dir, a.Platform))

	// --- Flutter ---
	case "flutter_doctor":
		return mcpToolJSON(mcpFlutterDoctor())
	case "flutter_build":
		var a struct { Dir string `json:"directory"`; Platform string `json:"platform"` }
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpFlutterBuild(a.Dir, a.Platform))
	case "flutter_test":
		var a struct { Dir string `json:"directory"` }
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpFlutterTest(a.Dir))

	// --- CocoaPods ---
	case "pod_install":
		var a struct { Dir string `json:"directory"` }
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpPodInstall(a.Dir))
	case "pod_outdated":
		var a struct { Dir string `json:"directory"` }
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpPodOutdated(a.Dir))

	// --- App Review ---
	case "app_review_check":
		var a struct { Platform string `json:"platform"` }
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpAppReviewCheck(a.Platform))

	// --- Package Registries ---
	case "dockerhub_search":
		var a struct { Query string `json:"query"`; Limit int `json:"limit"` }
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpDockerHubSearch(a.Query, a.Limit))
	case "dockerhub_tags":
		var a struct { Image string `json:"image"`; Limit int `json:"limit"` }
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpDockerHubTags(a.Image, a.Limit))
	case "pypi_info":
		var a struct { Pkg string `json:"package"` }
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpPyPIInfo(a.Pkg))
	case "pypi_versions":
		var a struct { Pkg string `json:"package"` }
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpPyPIVersions(a.Pkg))
	case "npm_search":
		var a struct { Query string `json:"query"`; Limit int `json:"limit"` }
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpNPMSearch(a.Query, a.Limit))
	case "npm_versions":
		var a struct { Pkg string `json:"package"` }
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpNPMVersions(a.Pkg))
	case "crates_info":
		var a struct { Crate string `json:"crate"` }
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpCratesInfo(a.Crate))
	case "crates_search":
		var a struct { Query string `json:"query"`; Limit int `json:"limit"` }
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpCratesSearch(a.Query, a.Limit))
	case "go_module_info":
		var a struct { Module string `json:"module"` }
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpGoModuleInfo(a.Module))
	case "go_module_versions":
		var a struct { Module string `json:"module"` }
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpGoModuleVersions(a.Module))
	case "pubdev_info":
		var a struct { Pkg string `json:"package"` }
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpPubDevInfo(a.Pkg))
	case "pubdev_search":
		var a struct { Query string `json:"query"` }
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpPubDevSearch(a.Query))
	case "brew_info":
		var a struct { Formula string `json:"formula"` }
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpBrewInfo(a.Formula))
	case "brew_search":
		var a struct { Query string `json:"query"` }
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpBrewSearch(a.Query))
	case "gem_info":
		var a struct { Gem string `json:"gem"` }
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpGemInfo(a.Gem))
	case "gem_search":
		var a struct { Query string `json:"query"` }
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpGemSearch(a.Query))
	case "maven_search":
		var a struct { Query string `json:"query"` }
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpMavenSearch(a.Query))
	case "nuget_search":
		var a struct { Query string `json:"query"` }
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpNuGetSearch(a.Query))
	case "apt_search":
		var a struct { Query string `json:"query"` }
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpAptSearch(a.Query))
	case "apt_show":
		var a struct { Pkg string `json:"package"` }
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpAptShow(a.Pkg))
	case "pip_show":
		var a struct { Pkg string `json:"package"` }
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpPipShow(a.Pkg))
	case "pip_list":
		return mcpToolJSON(mcpPipList())
	case "cargo_search":
		var a struct { Query string `json:"query"` }
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpCargoSearch(a.Query))
	case "pkg_install":
		var a struct { Manager string `json:"manager"`; Pkg string `json:"package"`; Global bool `json:"global"` }
		json.Unmarshal(call.Arguments, &a)
		return mcpToolJSON(mcpPkgInstall(a.Manager, a.Pkg, a.Global))

	// --- Supabase ---
	case "supabase_status":
		var a struct { Dir string `json:"directory"` }; json.Unmarshal(call.Arguments, &a); return mcpToolJSON(mcpSupabaseStatus(a.Dir))
	case "supabase_db":
		var a struct { Dir string `json:"directory"`; Query string `json:"query"` }; json.Unmarshal(call.Arguments, &a); return mcpToolJSON(mcpSupabaseDB(a.Dir, a.Query))
	case "supabase_migrations":
		var a struct { Dir string `json:"directory"` }; json.Unmarshal(call.Arguments, &a); return mcpToolJSON(mcpSupabaseMigrations(a.Dir))
	case "supabase_functions":
		var a struct { Dir string `json:"directory"` }; json.Unmarshal(call.Arguments, &a); return mcpToolJSON(mcpSupabaseFunctions(a.Dir))
	case "supabase_deploy":
		var a struct { Dir string `json:"directory"`; Function string `json:"function"` }; json.Unmarshal(call.Arguments, &a); return mcpToolJSON(mcpSupabaseDeploy(a.Dir, a.Function))
	// --- Convex ---
	case "convex_deploy":
		var a struct { Dir string `json:"directory"` }; json.Unmarshal(call.Arguments, &a); return mcpToolJSON(mcpConvexDeploy(a.Dir))
	case "convex_logs":
		var a struct { Dir string `json:"directory"` }; json.Unmarshal(call.Arguments, &a); return mcpToolJSON(mcpConvexLogs(a.Dir))
	case "convex_run":
		var a struct { Dir string `json:"directory"`; Function string `json:"function"`; Args string `json:"args"` }; json.Unmarshal(call.Arguments, &a); return mcpToolJSON(mcpConvexRun(a.Dir, a.Function, a.Args))
	// --- Cloudflare ---
	case "cf_workers":
		var a struct { Dir string `json:"directory"` }; json.Unmarshal(call.Arguments, &a); return mcpToolJSON(mcpCFWorkers(a.Dir))
	case "cf_deploy":
		var a struct { Dir string `json:"directory"` }; json.Unmarshal(call.Arguments, &a); return mcpToolJSON(mcpCFDeploy(a.Dir))
	case "cf_pages":
		var a struct { Dir string `json:"directory"` }; json.Unmarshal(call.Arguments, &a); return mcpToolJSON(mcpCFPages(a.Dir))
	case "cf_r2":
		var a struct { Action string `json:"action"`; Bucket string `json:"bucket"`; Key string `json:"key"` }; json.Unmarshal(call.Arguments, &a); return mcpToolJSON(mcpCFR2(a.Action, a.Bucket, a.Key))
	case "cf_d1":
		var a struct { Action string `json:"action"`; DB string `json:"database"`; Query string `json:"query"` }; json.Unmarshal(call.Arguments, &a); return mcpToolJSON(mcpCFD1(a.Action, a.DB, a.Query))
	case "cf_kv":
		var a struct { Action string `json:"action"`; NS string `json:"namespace"`; Key string `json:"key"`; Value string `json:"value"` }; json.Unmarshal(call.Arguments, &a); return mcpToolJSON(mcpCFKV(a.Action, a.NS, a.Key, a.Value))
	// --- GitLab ---
	case "gitlab_mrs":
		var a struct { Dir string `json:"directory"`; State string `json:"state"` }; json.Unmarshal(call.Arguments, &a); return mcpToolJSON(mcpGitLabMRs(a.Dir, a.State))
	case "gitlab_issues":
		var a struct { Dir string `json:"directory"`; State string `json:"state"` }; json.Unmarshal(call.Arguments, &a); return mcpToolJSON(mcpGitLabIssues(a.Dir, a.State))
	case "gitlab_pipelines":
		var a struct { Dir string `json:"directory"` }; json.Unmarshal(call.Arguments, &a); return mcpToolJSON(mcpGitLabPipelines(a.Dir))
	case "gitlab_ci":
		var a struct { Dir string `json:"directory"` }; json.Unmarshal(call.Arguments, &a); return mcpToolJSON(mcpGitLabCI(a.Dir))
	// --- GitHub extras ---
	case "github_repo_info":
		var a struct { Dir string `json:"directory"` }; json.Unmarshal(call.Arguments, &a); return mcpToolJSON(mcpGitHubRepoInfo(a.Dir))
	case "github_releases":
		var a struct { Dir string `json:"directory"` }; json.Unmarshal(call.Arguments, &a); return mcpToolJSON(mcpGitHubReleases(a.Dir))
	case "github_stars":
		var a struct { Repo string `json:"repo"` }; json.Unmarshal(call.Arguments, &a); return mcpToolJSON(mcpGitHubStargazers(a.Repo))
	// --- PlanetScale ---
	case "pscale_branches":
		var a struct { DB string `json:"database"` }; json.Unmarshal(call.Arguments, &a); return mcpToolJSON(mcpPlanetScaleBranches(a.DB))
	case "pscale_deploy":
		var a struct { DB string `json:"database"`; Branch string `json:"branch"` }; json.Unmarshal(call.Arguments, &a); return mcpToolJSON(mcpPlanetScaleDeploy(a.DB, a.Branch))
	// --- Prisma ---
	case "prisma_status":
		var a struct { Dir string `json:"directory"` }; json.Unmarshal(call.Arguments, &a); return mcpToolJSON(mcpPrismaStatus(a.Dir))
	case "prisma_generate":
		var a struct { Dir string `json:"directory"` }; json.Unmarshal(call.Arguments, &a); return mcpToolJSON(mcpPrismaGenerate(a.Dir))
	case "prisma_push":
		var a struct { Dir string `json:"directory"` }; json.Unmarshal(call.Arguments, &a); return mcpToolJSON(mcpPrismaPush(a.Dir))
	// --- Drizzle ---
	case "drizzle_push":
		var a struct { Dir string `json:"directory"` }; json.Unmarshal(call.Arguments, &a); return mcpToolJSON(mcpDrizzlePush(a.Dir))
	case "drizzle_generate":
		var a struct { Dir string `json:"directory"` }; json.Unmarshal(call.Arguments, &a); return mcpToolJSON(mcpDrizzleGenerate(a.Dir))
	// --- Fly.io ---
	case "fly_status":
		var a struct { Dir string `json:"directory"` }; json.Unmarshal(call.Arguments, &a); return mcpToolJSON(mcpFlyStatus(a.Dir))
	case "fly_deploy":
		var a struct { Dir string `json:"directory"` }; json.Unmarshal(call.Arguments, &a); return mcpToolJSON(mcpFlyDeploy(a.Dir))
	case "fly_logs":
		var a struct { App string `json:"app"` }; json.Unmarshal(call.Arguments, &a); return mcpToolJSON(mcpFlyLogs(a.App))
	// --- Railway ---
	case "railway_status":
		var a struct { Dir string `json:"directory"` }; json.Unmarshal(call.Arguments, &a); return mcpToolJSON(mcpRailwayStatus(a.Dir))
	case "railway_deploy":
		var a struct { Dir string `json:"directory"` }; json.Unmarshal(call.Arguments, &a); return mcpToolJSON(mcpRailwayDeploy(a.Dir))

	// --- Docker Extended ---
	case "docker_prune":
		var a struct { What string `json:"what"` }; json.Unmarshal(call.Arguments, &a); return mcpToolJSON(mcpDockerPrune(a.What))
	case "docker_disk_usage":
		return mcpToolJSON(mcpDockerDiskUsage())
	case "docker_networks":
		return mcpToolJSON(mcpDockerNetworks())
	case "docker_volumes":
		return mcpToolJSON(mcpDockerVolumes())
	case "docker_inspect":
		var a struct { Target string `json:"target"` }; json.Unmarshal(call.Arguments, &a); return mcpToolJSON(mcpDockerInspect(a.Target))
	case "docker_stats":
		return mcpToolJSON(mcpDockerStats())
	case "docker_build":
		var a struct { Dir string `json:"directory"`; Tag string `json:"tag"`; Dockerfile string `json:"dockerfile"` }; json.Unmarshal(call.Arguments, &a); return mcpToolJSON(mcpDockerBuild(a.Dir, a.Tag, a.Dockerfile))
	case "docker_pull":
		var a struct { Image string `json:"image"` }; json.Unmarshal(call.Arguments, &a); return mcpToolJSON(mcpDockerPull(a.Image))
	case "docker_push":
		var a struct { Image string `json:"image"` }; json.Unmarshal(call.Arguments, &a); return mcpToolJSON(mcpDockerPush(a.Image))
	case "docker_stop":
		var a struct { Container string `json:"container"` }; json.Unmarshal(call.Arguments, &a); return mcpToolJSON(mcpDockerStop(a.Container))
	case "docker_start":
		var a struct { Container string `json:"container"` }; json.Unmarshal(call.Arguments, &a); return mcpToolJSON(mcpDockerStart(a.Container))
	case "docker_restart":
		var a struct { Container string `json:"container"` }; json.Unmarshal(call.Arguments, &a); return mcpToolJSON(mcpDockerRestart(a.Container))
	case "docker_rm":
		var a struct { Container string `json:"container"`; Force bool `json:"force"` }; json.Unmarshal(call.Arguments, &a); return mcpToolJSON(mcpDockerRm(a.Container, a.Force))
	case "docker_rmi":
		var a struct { Image string `json:"image"`; Force bool `json:"force"` }; json.Unmarshal(call.Arguments, &a); return mcpToolJSON(mcpDockerRmi(a.Image, a.Force))
	case "docker_top":
		var a struct { Container string `json:"container"` }; json.Unmarshal(call.Arguments, &a); return mcpToolJSON(mcpDockerTop(a.Container))
	case "docker_port":
		var a struct { Container string `json:"container"` }; json.Unmarshal(call.Arguments, &a); return mcpToolJSON(mcpDockerPort(a.Container))
	case "docker_cp":
		var a struct { Src string `json:"source"`; Dst string `json:"destination"` }; json.Unmarshal(call.Arguments, &a); return mcpToolJSON(mcpDockerCp(a.Src, a.Dst))
	case "docker_history":
		var a struct { Image string `json:"image"` }; json.Unmarshal(call.Arguments, &a); return mcpToolJSON(mcpDockerHistory(a.Image))

	// --- Git Extended ---
	case "git_stash":
		var a struct { Action string `json:"action"`; Message string `json:"message"` }; json.Unmarshal(call.Arguments, &a); return mcpToolJSON(mcpGitStash(a.Action, a.Message))
	case "git_blame_file":
		var a struct { File string `json:"file"`; Lines string `json:"lines"` }; json.Unmarshal(call.Arguments, &a); return mcpToolJSON(mcpGitBlame(a.File, a.Lines))
	case "git_log_advanced":
		var a struct { Dir string `json:"directory"`; Author string `json:"author"`; Since string `json:"since"`; Until string `json:"until"`; Path string `json:"path"`; Count int `json:"count"` }; json.Unmarshal(call.Arguments, &a); return mcpToolJSON(mcpGitLogAdvanced(a.Dir, a.Author, a.Since, a.Until, a.Path, a.Count))
	case "git_branches":
		var a struct { Dir string `json:"directory"` }; json.Unmarshal(call.Arguments, &a); return mcpToolJSON(mcpGitBranches(a.Dir))
	case "git_tags":
		var a struct { Dir string `json:"directory"` }; json.Unmarshal(call.Arguments, &a); return mcpToolJSON(mcpGitTags(a.Dir))
	case "git_remotes":
		var a struct { Dir string `json:"directory"` }; json.Unmarshal(call.Arguments, &a); return mcpToolJSON(mcpGitRemotes(a.Dir))
	case "git_reflog":
		var a struct { Dir string `json:"directory"`; Count int `json:"count"` }; json.Unmarshal(call.Arguments, &a); return mcpToolJSON(mcpGitReflog(a.Dir, a.Count))
	case "git_shortlog":
		var a struct { Dir string `json:"directory"` }; json.Unmarshal(call.Arguments, &a); return mcpToolJSON(mcpGitShortlog(a.Dir))

	// --- Helm ---
	case "helm_list":
		var a struct { NS string `json:"namespace"` }; json.Unmarshal(call.Arguments, &a); return mcpToolJSON(mcpHelmList(a.NS))
	case "helm_status":
		var a struct { Release string `json:"release"`; NS string `json:"namespace"` }; json.Unmarshal(call.Arguments, &a); return mcpToolJSON(mcpHelmStatus(a.Release, a.NS))
	case "helm_values":
		var a struct { Release string `json:"release"`; NS string `json:"namespace"` }; json.Unmarshal(call.Arguments, &a); return mcpToolJSON(mcpHelmValues(a.Release, a.NS))
	case "helm_search":
		var a struct { Query string `json:"query"` }; json.Unmarshal(call.Arguments, &a); return mcpToolJSON(mcpHelmSearch(a.Query))
	case "helm_repos":
		return mcpToolJSON(mcpHelmRepos())
	case "helm_history":
		var a struct { Release string `json:"release"`; NS string `json:"namespace"` }; json.Unmarshal(call.Arguments, &a); return mcpToolJSON(mcpHelmHistory(a.Release, a.NS))

	// --- System Extended ---
	case "free_memory":
		return mcpToolJSON(mcpFreeMemory())
	case "listen_ports":
		return mcpToolJSON(mcpListenPorts())
	case "find_large_files":
		var a struct { Dir string `json:"directory"`; SizeMB int `json:"size_mb"` }; json.Unmarshal(call.Arguments, &a); return mcpToolJSON(mcpFindLargeFiles(a.Dir, a.SizeMB))
	case "tree_dir":
		var a struct { Dir string `json:"directory"`; Depth int `json:"depth"` }; json.Unmarshal(call.Arguments, &a); return mcpToolJSON(mcpTreeDir(a.Dir, a.Depth))
	case "lines_of_code":
		var a struct { Dir string `json:"directory"` }; json.Unmarshal(call.Arguments, &a); return mcpToolJSON(mcpLinesOfCode(a.Dir))

	// --- Network & Packet Capture ---
	case "tcpdump":
		var a struct { Iface string `json:"interface"`; Count int `json:"count"`; Filter string `json:"filter"` }; json.Unmarshal(call.Arguments, &a); return mcpToolJSON(mcpTcpdump(a.Iface, a.Count, a.Filter))
	case "tcpdump_http":
		var a struct { Iface string `json:"interface"`; Count int `json:"count"` }; json.Unmarshal(call.Arguments, &a); return mcpToolJSON(mcpTcpdumpHTTP(a.Iface, a.Count))
	case "tcpdump_dns":
		var a struct { Iface string `json:"interface"`; Count int `json:"count"` }; json.Unmarshal(call.Arguments, &a); return mcpToolJSON(mcpTcpdumpDNS(a.Iface, a.Count))
	case "tshark":
		var a struct { Iface string `json:"interface"`; Count int `json:"count"`; Filter string `json:"filter"`; Fields string `json:"fields"` }; json.Unmarshal(call.Arguments, &a); return mcpToolJSON(mcpTshark(a.Iface, a.Count, a.Filter, a.Fields))
	case "pcap_analyze":
		var a struct { File string `json:"file"`; Filter string `json:"filter"` }; json.Unmarshal(call.Arguments, &a); return mcpToolJSON(mcpPcapAnalyze(a.File, a.Filter))
	case "pcap_stats":
		var a struct { File string `json:"file"` }; json.Unmarshal(call.Arguments, &a); return mcpToolJSON(mcpPcapStats(a.File))
	case "netcat":
		var a struct { Host string `json:"host"`; Port int `json:"port"`; Data string `json:"data"` }; json.Unmarshal(call.Arguments, &a); return mcpToolJSON(mcpNetcat(a.Host, a.Port, a.Data))
	case "port_scan":
		var a struct { Host string `json:"host"`; Ports string `json:"ports"` }; json.Unmarshal(call.Arguments, &a); return mcpToolJSON(mcpPortScan(a.Host, a.Ports))
	case "arp_table":
		return mcpToolJSON(mcpArpTable())
	case "arp_scan":
		var a struct { Subnet string `json:"subnet"` }; json.Unmarshal(call.Arguments, &a); return mcpToolJSON(mcpArpScan(a.Subnet))
	case "nmap_scan":
		var a struct { Target string `json:"target"`; Type string `json:"type"`; Ports string `json:"ports"` }; json.Unmarshal(call.Arguments, &a); return mcpToolJSON(mcpNmapScan(a.Target, a.Type, a.Ports))
	case "traceroute_host":
		var a struct { Host string `json:"host"`; MaxHops int `json:"max_hops"` }; json.Unmarshal(call.Arguments, &a); return mcpToolJSON(mcpTraceroute(a.Host, a.MaxHops))
	case "mtr_report":
		var a struct { Host string `json:"host"`; Count int `json:"count"` }; json.Unmarshal(call.Arguments, &a); return mcpToolJSON(mcpMtr(a.Host, a.Count))
	case "network_interfaces":
		return mcpToolJSON(mcpNetworkInterfaces())
	case "ip_route":
		return mcpToolJSON(mcpIPRoute())
	case "network_connections":
		var a struct { State string `json:"state"` }; json.Unmarshal(call.Arguments, &a); return mcpToolJSON(mcpNetworkConnections(a.State))
	case "bandwidth_test":
		var a struct { Host string `json:"host"` }; json.Unmarshal(call.Arguments, &a); return mcpToolJSON(mcpBandwidthTest(a.Host))
	case "curl_timings":
		var a struct { URL string `json:"url"` }; json.Unmarshal(call.Arguments, &a); return mcpToolJSON(mcpCurlTimings(a.URL))

	// --- Linux System ---
	case "dmesg":
		var a struct { Level string `json:"level"`; Lines int `json:"lines"` }; json.Unmarshal(call.Arguments, &a); return mcpToolJSON(mcpDmesg(a.Level, a.Lines))
	case "lsmod":
		return mcpToolJSON(mcpLsmod())
	case "modinfo":
		var a struct { Module string `json:"module"` }; json.Unmarshal(call.Arguments, &a); return mcpToolJSON(mcpModinfo(a.Module))
	case "insmod":
		var a struct { Module string `json:"module"` }; json.Unmarshal(call.Arguments, &a); return mcpToolJSON(mcpInsmod(a.Module))
	case "rmmod":
		var a struct { Module string `json:"module"` }; json.Unmarshal(call.Arguments, &a); return mcpToolJSON(mcpRmmod(a.Module))
	case "uname":
		return mcpToolJSON(mcpUname())
	case "sysctl":
		var a struct { Key string `json:"key"` }; json.Unmarshal(call.Arguments, &a); return mcpToolJSON(mcpSysctl(a.Key))
	case "top_snapshot":
		return mcpToolJSON(mcpTopSnapshot())
	case "ps_aux":
		var a struct { Sort string `json:"sort"`; Filter string `json:"filter"` }; json.Unmarshal(call.Arguments, &a); return mcpToolJSON(mcpPsAux(a.Sort, a.Filter))
	case "ps_tree":
		return mcpToolJSON(mcpPsTree())
	case "load_average":
		return mcpToolJSON(mcpLoadAverage())
	case "vmstat":
		var a struct { Count int `json:"count"` }; json.Unmarshal(call.Arguments, &a); return mcpToolJSON(mcpVmstat(a.Count))
	case "swap_info":
		return mcpToolJSON(mcpSwap())
	case "df":
		var a struct { Path string `json:"path"` }; json.Unmarshal(call.Arguments, &a); return mcpToolJSON(mcpDf(a.Path))
	case "du":
		var a struct { Path string `json:"path"`; Depth int `json:"depth"` }; json.Unmarshal(call.Arguments, &a); return mcpToolJSON(mcpDu(a.Path, a.Depth))
	case "lsblk":
		return mcpToolJSON(mcpLsblk())
	case "fdisk_list":
		return mcpToolJSON(mcpFdisk())
	case "mounts":
		return mcpToolJSON(mcpMounts())
	case "iostat":
		return mcpToolJSON(mcpIostat())
	case "tree":
		var a struct { Path string `json:"path"`; Depth int `json:"depth"`; All bool `json:"all"`; DirsOnly bool `json:"dirs_only"` }; json.Unmarshal(call.Arguments, &a); return mcpToolJSON(mcpTree(a.Path, a.Depth, a.All, a.DirsOnly))
	case "cpu_info":
		return mcpToolJSON(mcpCpuInfo())
	case "lspci":
		return mcpToolJSON(mcpLspci())
	case "lsusb":
		return mcpToolJSON(mcpLsusb())
	case "sensors":
		return mcpToolJSON(mcpSensors())
	case "ufw":
		var a struct { Action string `json:"action"`; Rule string `json:"rule"` }; json.Unmarshal(call.Arguments, &a); return mcpToolJSON(mcpUfw(a.Action, a.Rule))
	case "iptables_list":
		return mcpToolJSON(mcpIptables())
	case "who_is_logged_in":
		return mcpToolJSON(mcpWho())
	case "last_logins":
		var a struct { Count int `json:"count"` }; json.Unmarshal(call.Arguments, &a); return mcpToolJSON(mcpLastLogins(a.Count))
	case "timedate_info":
		return mcpToolJSON(mcpTimeDateInfo())
	case "hostname_info":
		return mcpToolJSON(mcpHostnameInfo())

	// --- Compilers & Language Suites ---
	case "make_targets":
		var a struct { Dir string `json:"directory"` }; json.Unmarshal(call.Arguments, &a); return mcpToolJSON(mcpMakeTargets(a.Dir))
	case "make_run":
		var a struct { Dir string `json:"directory"`; Target string `json:"target"` }; json.Unmarshal(call.Arguments, &a); return mcpToolJSON(mcpMakeRun(a.Dir, a.Target))
	case "make_clean":
		var a struct { Dir string `json:"directory"` }; json.Unmarshal(call.Arguments, &a); return mcpToolJSON(mcpMakeClean(a.Dir))
	case "cmake_configure":
		var a struct { Dir string `json:"directory"`; BuildDir string `json:"build_dir"`; Gen string `json:"generator"` }; json.Unmarshal(call.Arguments, &a); return mcpToolJSON(mcpCMakeConfigure(a.Dir, a.BuildDir, a.Gen))
	case "cmake_build":
		var a struct { Dir string `json:"directory"`; BuildDir string `json:"build_dir"`; Parallel int `json:"parallel"` }; json.Unmarshal(call.Arguments, &a); return mcpToolJSON(mcpCMakeBuild(a.Dir, a.BuildDir, a.Parallel))
	case "cmake_test":
		var a struct { Dir string `json:"directory"`; BuildDir string `json:"build_dir"` }; json.Unmarshal(call.Arguments, &a); return mcpToolJSON(mcpCMakeTest(a.Dir, a.BuildDir))
	case "cmake_install":
		var a struct { Dir string `json:"directory"`; BuildDir string `json:"build_dir"` }; json.Unmarshal(call.Arguments, &a); return mcpToolJSON(mcpCMakeInstall(a.Dir, a.BuildDir))
	case "gcc_compile":
		var a struct { File string `json:"file"`; Output string `json:"output"`; Flags []string `json:"flags"` }; json.Unmarshal(call.Arguments, &a); return mcpToolJSON(mcpGCCCompile(a.File, a.Output, a.Flags))
	case "clang_compile":
		var a struct { File string `json:"file"`; Output string `json:"output"`; Flags []string `json:"flags"` }; json.Unmarshal(call.Arguments, &a); return mcpToolJSON(mcpClangCompile(a.File, a.Output, a.Flags))
	case "clang_tidy_check":
		var a struct { File string `json:"file"`; Dir string `json:"directory"` }; json.Unmarshal(call.Arguments, &a); return mcpToolJSON(mcpClangTidy(a.File, a.Dir))
	case "clang_format_file":
		var a struct { File string `json:"file"`; InPlace bool `json:"in_place"` }; json.Unmarshal(call.Arguments, &a); return mcpToolJSON(mcpClangFormat(a.File, a.InPlace))
	case "objdump":
		var a struct { File string `json:"file"` }; json.Unmarshal(call.Arguments, &a); return mcpToolJSON(mcpLLVMObjdump(a.File))
	case "binary_size":
		var a struct { File string `json:"file"` }; json.Unmarshal(call.Arguments, &a); return mcpToolJSON(mcpLLVMSize(a.File))
	case "nm_symbols":
		var a struct { File string `json:"file"` }; json.Unmarshal(call.Arguments, &a); return mcpToolJSON(mcpLLVMNM(a.File))
	case "compiler_version":
		var a struct { Compiler string `json:"compiler"` }; json.Unmarshal(call.Arguments, &a); return mcpToolJSON(mcpCompilerVersion(a.Compiler))
	// Cargo (Rust)
	case "cargo_build":
		var a struct { Dir string `json:"directory"`; Release bool `json:"release"` }; json.Unmarshal(call.Arguments, &a); return mcpToolJSON(mcpCargoBuild(a.Dir, a.Release))
	case "cargo_test_suite":
		var a struct { Dir string `json:"directory"`; TestName string `json:"test_name"` }; json.Unmarshal(call.Arguments, &a); return mcpToolJSON(mcpCargoTest(a.Dir, a.TestName))
	case "cargo_clippy":
		var a struct { Dir string `json:"directory"` }; json.Unmarshal(call.Arguments, &a); return mcpToolJSON(mcpCargoClippy(a.Dir))
	case "cargo_fmt":
		var a struct { Dir string `json:"directory"`; Check bool `json:"check"` }; json.Unmarshal(call.Arguments, &a); return mcpToolJSON(mcpCargoFmt(a.Dir, a.Check))
	case "cargo_doc":
		var a struct { Dir string `json:"directory"`; Open bool `json:"open"` }; json.Unmarshal(call.Arguments, &a); return mcpToolJSON(mcpCargoDoc(a.Dir, a.Open))
	case "cargo_bench_suite":
		var a struct { Dir string `json:"directory"`; Bench string `json:"bench"` }; json.Unmarshal(call.Arguments, &a); return mcpToolJSON(mcpCargoBench(a.Dir, a.Bench))
	case "cargo_tree_deps":
		var a struct { Dir string `json:"directory"`; Depth int `json:"depth"` }; json.Unmarshal(call.Arguments, &a); return mcpToolJSON(mcpCargoTree(a.Dir, a.Depth))
	case "cargo_update_deps":
		var a struct { Dir string `json:"directory"` }; json.Unmarshal(call.Arguments, &a); return mcpToolJSON(mcpCargoUpdate(a.Dir))
	case "cargo_audit_deps":
		var a struct { Dir string `json:"directory"` }; json.Unmarshal(call.Arguments, &a); return mcpToolJSON(mcpCargoAudit(a.Dir))
	case "cargo_check_only":
		var a struct { Dir string `json:"directory"` }; json.Unmarshal(call.Arguments, &a); return mcpToolJSON(mcpCargoCheck(a.Dir))
	case "cargo_clean":
		var a struct { Dir string `json:"directory"` }; json.Unmarshal(call.Arguments, &a); return mcpToolJSON(mcpCargoClean(a.Dir))
	case "cargo_add_crate":
		var a struct { Dir string `json:"directory"`; Crate string `json:"crate"` }; json.Unmarshal(call.Arguments, &a); return mcpToolJSON(mcpCargoAdd(a.Dir, a.Crate))
	case "cargo_remove_crate":
		var a struct { Dir string `json:"directory"`; Crate string `json:"crate"` }; json.Unmarshal(call.Arguments, &a); return mcpToolJSON(mcpCargoRemove(a.Dir, a.Crate))
	// Go
	case "go_build":
		var a struct { Dir string `json:"directory"`; Output string `json:"output"` }; json.Unmarshal(call.Arguments, &a); return mcpToolJSON(mcpGoBuild(a.Dir, a.Output))
	case "go_test_suite":
		var a struct { Dir string `json:"directory"`; Verbose bool `json:"verbose"`; Race bool `json:"race"`; Cover bool `json:"cover"` }; json.Unmarshal(call.Arguments, &a); return mcpToolJSON(mcpGoTest(a.Dir, a.Verbose, a.Race, a.Cover))
	case "go_vet_check":
		var a struct { Dir string `json:"directory"` }; json.Unmarshal(call.Arguments, &a); return mcpToolJSON(mcpGoVet(a.Dir))
	case "go_mod_tidy":
		var a struct { Dir string `json:"directory"` }; json.Unmarshal(call.Arguments, &a); return mcpToolJSON(mcpGoModTidy(a.Dir))
	case "go_mod_graph":
		var a struct { Dir string `json:"directory"` }; json.Unmarshal(call.Arguments, &a); return mcpToolJSON(mcpGoModGraph(a.Dir))
	case "go_mod_why":
		var a struct { Dir string `json:"directory"`; Module string `json:"module"` }; json.Unmarshal(call.Arguments, &a); return mcpToolJSON(mcpGoModWhy(a.Dir, a.Module))
	case "go_generate":
		var a struct { Dir string `json:"directory"` }; json.Unmarshal(call.Arguments, &a); return mcpToolJSON(mcpGoGenerate(a.Dir))
	case "go_fmt_check":
		var a struct { Dir string `json:"directory"` }; json.Unmarshal(call.Arguments, &a); return mcpToolJSON(mcpGoFmt(a.Dir))
	case "go_staticcheck":
		var a struct { Dir string `json:"directory"` }; json.Unmarshal(call.Arguments, &a); return mcpToolJSON(mcpGoStaticcheck(a.Dir))
	case "go_vulncheck":
		var a struct { Dir string `json:"directory"` }; json.Unmarshal(call.Arguments, &a); return mcpToolJSON(mcpGoVulncheck(a.Dir))
	// Python
	case "pytest_suite":
		var a struct { Dir string `json:"directory"`; Verbose bool `json:"verbose"`; Coverage bool `json:"coverage"`; Marker string `json:"marker"` }; json.Unmarshal(call.Arguments, &a); return mcpToolJSON(mcpPytest(a.Dir, a.Verbose, a.Coverage, a.Marker))
	case "ruff_suite":
		var a struct { Dir string `json:"directory"`; Action string `json:"action"` }; json.Unmarshal(call.Arguments, &a); return mcpToolJSON(mcpRuff(a.Dir, a.Action))
	case "mypy_check":
		var a struct { Dir string `json:"directory"` }; json.Unmarshal(call.Arguments, &a); return mcpToolJSON(mcpMypy(a.Dir))
	case "black_format":
		var a struct { Dir string `json:"directory"`; Check bool `json:"check"` }; json.Unmarshal(call.Arguments, &a); return mcpToolJSON(mcpBlack(a.Dir, a.Check))
	case "pip_compile":
		var a struct { Dir string `json:"directory"` }; json.Unmarshal(call.Arguments, &a); return mcpToolJSON(mcpPipCompile(a.Dir))
	case "uv_install":
		var a struct { Dir string `json:"directory"` }; json.Unmarshal(call.Arguments, &a); return mcpToolJSON(mcpUVInstall(a.Dir))
	// Node.js/TypeScript
	case "npm_run_script":
		var a struct { Dir string `json:"directory"`; Script string `json:"script"` }; json.Unmarshal(call.Arguments, &a); return mcpToolJSON(mcpNPMRun(a.Dir, a.Script))
	case "tsc_check":
		var a struct { Dir string `json:"directory"`; NoEmit bool `json:"no_emit"` }; json.Unmarshal(call.Arguments, &a); return mcpToolJSON(mcpTSC(a.Dir, a.NoEmit))
	case "eslint_check":
		var a struct { Dir string `json:"directory"`; Fix bool `json:"fix"` }; json.Unmarshal(call.Arguments, &a); return mcpToolJSON(mcpESLint(a.Dir, a.Fix))
	case "prettier_check":
		var a struct { Dir string `json:"directory"`; Check bool `json:"check"` }; json.Unmarshal(call.Arguments, &a); return mcpToolJSON(mcpPrettier(a.Dir, a.Check))
	case "biome_suite":
		var a struct { Dir string `json:"directory"`; Action string `json:"action"` }; json.Unmarshal(call.Arguments, &a); return mcpToolJSON(mcpBiome(a.Dir, a.Action))

	// --- Static Analysis ---
	case "cppcheck":
		var a struct { Dir string `json:"directory"`; File string `json:"file"`; Severity string `json:"severity"`; EnableAll bool `json:"enable_all"` }; json.Unmarshal(call.Arguments, &a); return mcpToolJSON(mcpCppcheck(a.Dir, a.File, a.Severity, a.EnableAll))
	case "shellcheck":
		var a struct { File string `json:"file"`; Shell string `json:"shell"`; Severity string `json:"severity"` }; json.Unmarshal(call.Arguments, &a); return mcpToolJSON(mcpShellcheck(a.File, a.Shell, a.Severity))
	case "hadolint":
		var a struct { File string `json:"file"`; TrustedRegistries []string `json:"trusted_registries"` }; json.Unmarshal(call.Arguments, &a); return mcpToolJSON(mcpHadolint(a.File, a.TrustedRegistries))
	case "semgrep":
		var a struct { Dir string `json:"directory"`; Config string `json:"config"`; AutoConfig bool `json:"auto_config"` }; json.Unmarshal(call.Arguments, &a); return mcpToolJSON(mcpSemgrep(a.Dir, a.Config, a.AutoConfig))
	case "sonarscanner":
		var a struct { Dir string `json:"directory"`; ProjectKey string `json:"project_key"`; HostURL string `json:"host_url"`; Token string `json:"token"` }; json.Unmarshal(call.Arguments, &a); return mcpToolJSON(mcpSonarScanner(a.Dir, a.ProjectKey, a.HostURL, a.Token))
	case "bandit":
		var a struct { Dir string `json:"directory"`; File string `json:"file"`; Severity string `json:"severity"` }; json.Unmarshal(call.Arguments, &a); return mcpToolJSON(mcpBandit(a.Dir, a.File, a.Severity))
	case "gosec":
		var a struct { Dir string `json:"directory"`; NoFail bool `json:"no_fail"` }; json.Unmarshal(call.Arguments, &a); return mcpToolJSON(mcpGosec(a.Dir, a.NoFail))
	case "brakeman":
		var a struct { Dir string `json:"directory"`; Confidence int `json:"confidence"` }; json.Unmarshal(call.Arguments, &a); return mcpToolJSON(mcpBrakeman(a.Dir, a.Confidence))
	case "safety_check":
		var a struct { Dir string `json:"directory"`; File string `json:"file"` }; json.Unmarshal(call.Arguments, &a); return mcpToolJSON(mcpSafetyCheck(a.Dir, a.File))
	case "trivy_fs_scan":
		var a struct { Dir string `json:"directory"`; Severity string `json:"severity"` }; json.Unmarshal(call.Arguments, &a); return mcpToolJSON(mcpTrivyFSScan(a.Dir, a.Severity))
	// --- Profiling & Debugging ---
	case "valgrind_memcheck":
		var a struct { Binary string `json:"binary"`; Args []string `json:"args"` }; json.Unmarshal(call.Arguments, &a); return mcpToolJSON(mcpValgrindMemcheck(a.Binary, a.Args))
	case "valgrind_callgrind":
		var a struct { Binary string `json:"binary"`; Args []string `json:"args"`; OutputFile string `json:"output_file"` }; json.Unmarshal(call.Arguments, &a); return mcpToolJSON(mcpValgrindCallgrind(a.Binary, a.Args, a.OutputFile))
	case "valgrind_massif":
		var a struct { Binary string `json:"binary"`; Args []string `json:"args"`; OutputFile string `json:"output_file"` }; json.Unmarshal(call.Arguments, &a); return mcpToolJSON(mcpValgrindMassif(a.Binary, a.Args, a.OutputFile))
	case "gdb_backtrace":
		var a struct { PID int `json:"pid"`; Binary string `json:"binary"` }; json.Unmarshal(call.Arguments, &a); return mcpToolJSON(mcpGDBBacktrace(a.PID, a.Binary))
	case "lldb_backtrace":
		var a struct { PID int `json:"pid"`; Binary string `json:"binary"` }; json.Unmarshal(call.Arguments, &a); return mcpToolJSON(mcpLLDBBacktrace(a.PID, a.Binary))
	case "strace_trace":
		var a struct { PID int `json:"pid"`; Binary string `json:"binary"`; SyscallFilter string `json:"syscall_filter"`; Args []string `json:"args"` }; json.Unmarshal(call.Arguments, &a); return mcpToolJSON(mcpStraceTrace(a.PID, a.Binary, a.SyscallFilter, a.Args))
	case "ltrace_trace":
		var a struct { PID int `json:"pid"`; Binary string `json:"binary"`; Args []string `json:"args"` }; json.Unmarshal(call.Arguments, &a); return mcpToolJSON(mcpLtraceTrace(a.PID, a.Binary, a.Args))
	case "perf_record":
		var a struct { Binary string `json:"binary"`; Args []string `json:"args"`; Duration int `json:"duration"`; OutputFile string `json:"output_file"` }; json.Unmarshal(call.Arguments, &a); return mcpToolJSON(mcpPerfRecord(a.Binary, a.Args, a.Duration, a.OutputFile))
	case "perf_stat":
		var a struct { Binary string `json:"binary"`; Args []string `json:"args"`; Events string `json:"events"` }; json.Unmarshal(call.Arguments, &a); return mcpToolJSON(mcpPerfStat(a.Binary, a.Args, a.Events))
	case "go_pprof_cpu":
		var a struct { Dir string `json:"directory"`; Duration int `json:"duration"`; Binary string `json:"binary"` }; json.Unmarshal(call.Arguments, &a); return mcpToolJSON(mcpGoPprofCPU(a.Dir, a.Duration, a.Binary))
	case "go_pprof_heap":
		var a struct { Dir string `json:"directory"`; URL string `json:"url"` }; json.Unmarshal(call.Arguments, &a); return mcpToolJSON(mcpGoPprofHeap(a.Dir, a.URL))
	case "heaptrack":
		var a struct { Binary string `json:"binary"`; Args []string `json:"args"` }; json.Unmarshal(call.Arguments, &a); return mcpToolJSON(mcpHeaptrack(a.Binary, a.Args))
	// --- Code Metrics ---
	case "cyclomatic_complexity":
		var a struct { Dir string `json:"directory"`; Language string `json:"language"` }; json.Unmarshal(call.Arguments, &a); return mcpToolJSON(mcpCyclomaticComplexity(a.Dir, a.Language))
	case "lizard":
		var a struct { Dir string `json:"directory"`; Threshold int `json:"threshold"`; Languages string `json:"languages"` }; json.Unmarshal(call.Arguments, &a); return mcpToolJSON(mcpLizard(a.Dir, a.Threshold, a.Languages))
	case "loc_count":
		var a struct { Dir string `json:"directory"`; Tool string `json:"tool"` }; json.Unmarshal(call.Arguments, &a); return mcpToolJSON(mcpLOCCount(a.Dir, a.Tool))

	// --- System Logs & Debugging ---
	case "journalctl":
		var a struct { Unit string `json:"unit"`; Priority string `json:"priority"`; Lines int `json:"lines"`; Boot bool `json:"boot"`; Since string `json:"since"` }; json.Unmarshal(call.Arguments, &a); return mcpToolJSON(mcpJournalctl(a.Unit, a.Priority, a.Lines, a.Boot, a.Since))
	case "journalctl_errors":
		return mcpToolJSON(mcpJournalctlErrors())
	case "journalctl_disk_usage":
		return mcpToolJSON(mcpJournalctlDiskUsage())
	case "systemctl":
		var a struct { Action string `json:"action"`; Unit string `json:"unit"` }; json.Unmarshal(call.Arguments, &a); return mcpToolJSON(mcpSystemctl(a.Action, a.Unit))
	case "gdb_attach":
		var a struct { PID int `json:"pid"`; Commands string `json:"commands"` }; json.Unmarshal(call.Arguments, &a); return mcpToolJSON(mcpGDBAttach(a.PID, a.Commands))
	case "gdb_core_dump":
		var a struct { Binary string `json:"binary"`; Core string `json:"corefile"` }; json.Unmarshal(call.Arguments, &a); return mcpToolJSON(mcpGDBCoreDump(a.Binary, a.Core))
	case "lldb_attach":
		var a struct { PID int `json:"pid"`; Commands string `json:"commands"` }; json.Unmarshal(call.Arguments, &a); return mcpToolJSON(mcpLLDBAttach(a.PID, a.Commands))
	case "coredump_list":
		return mcpToolJSON(mcpCoredumpList())
	case "coredump_info":
		var a struct { PID string `json:"pid"` }; json.Unmarshal(call.Arguments, &a); return mcpToolJSON(mcpCoredumpInfo(a.PID))
	case "syslog":
		var a struct { File string `json:"file"`; Lines int `json:"lines"`; Filter string `json:"filter"` }; json.Unmarshal(call.Arguments, &a); return mcpToolJSON(mcpSyslog(a.File, a.Lines, a.Filter))
	case "auth_log":
		var a struct { Lines int `json:"lines"` }; json.Unmarshal(call.Arguments, &a); return mcpToolJSON(mcpAuthLog(a.Lines))

	default:
		return mcpToolError("unknown tool: " + call.Name)
	}
}

// mcpDoctor runs a doctor-like health check and returns results as text.
func (s *HTTPServer) mcpDoctor() interface{} {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Yaver Doctor (v%s)\n\n", version))

	ok, warn, fail := 0, 0, 0
	check := func(name, status, detail string) {
		icon := "✓"
		switch status {
		case "warn":
			icon = "!"
			warn++
		case "fail":
			icon = "✗"
			fail++
		default:
			ok++
		}
		sb.WriteString(fmt.Sprintf("  %-30s %s %s\n", name, icon, detail))
	}

	// Config
	sb.WriteString("── Configuration ──\n")
	cfg, err := LoadConfig()
	if err != nil {
		check("Config file", "fail", fmt.Sprintf("Error: %v", err))
	} else {
		p, _ := ConfigPath()
		check("Config file", "ok", p)
	}

	// Auth
	sb.WriteString("\n── Authentication ──\n")
	if cfg == nil || cfg.AuthToken == "" {
		check("Auth token", "fail", "Not signed in — run 'yaver auth'")
	} else {
		check("Auth token", "ok", "Present")
		if cfg.DeviceID != "" {
			check("Device ID", "ok", cfg.DeviceID[:8]+"...")
		} else {
			check("Device ID", "fail", "Not set — run 'yaver serve'")
		}
		if cfg.ConvexSiteURL != "" {
			check("Backend", "ok", cfg.ConvexSiteURL)
		} else {
			check("Backend", "fail", "Not configured")
		}
	}

	// Agent
	sb.WriteString("\n── Agent ──\n")
	agentPID, agentRunning := isAgentRunning()
	if agentRunning {
		check("Agent process", "ok", fmt.Sprintf("Running (PID %d)", agentPID))
	} else {
		check("Agent process", "warn", "Not running — start with 'yaver serve'")
	}

	if tmuxAvailable() {
		check("Tmux", "ok", "available")
	} else {
		check("Tmux", "warn", "not installed — session adoption requires tmux")
	}

	// Tasks
	status := s.taskMgr.GetAgentStatus()
	check("Tasks", "ok", fmt.Sprintf("%d running, %d total", status.RunningTasks, status.TotalTasks))

	// Runners
	sb.WriteString("\n── AI Runners ──\n")
	runners := []struct{ id, name, cmd string }{
		{"claude", "Claude Code", "claude"},
		{"codex", "OpenAI Codex", "codex"},
		{"aider", "Aider", "aider"},
		{"ollama", "Ollama", "ollama"},
		{"goose", "Goose", "goose"},
		{"amp", "Amp", "amp"},
		{"opencode", "OpenCode", "opencode"},
	}
	for _, r := range runners {
		path, err := osexec.LookPath(r.cmd)
		if err != nil {
			check(r.name, "warn", "Not installed")
		} else {
			check(r.name, "ok", path)
		}
	}

	// Relay
	sb.WriteString("\n── Relay Servers ──\n")
	if cfg != nil && len(cfg.RelayServers) > 0 {
		client := &http.Client{Timeout: 5 * time.Second}
		for _, rs := range cfg.RelayServers {
			label := rs.Label
			if label == "" {
				label = rs.ID
			}
			start := time.Now()
			resp, err := client.Get(rs.HttpURL + "/health")
			rtt := time.Since(start)
			if err != nil {
				check("Relay: "+label, "fail", "Unreachable")
			} else {
				resp.Body.Close()
				if resp.StatusCode == 200 {
					check("Relay: "+label, "ok", fmt.Sprintf("OK (%dms)", rtt.Milliseconds()))
				} else {
					check("Relay: "+label, "fail", fmt.Sprintf("HTTP %d", resp.StatusCode))
				}
			}
		}
	} else {
		check("Relay servers", "warn", "None configured")
	}

	// Tunnels
	if cfg != nil && len(cfg.CloudflareTunnels) > 0 {
		sb.WriteString("\n── Cloudflare Tunnels ──\n")
		for _, t := range cfg.CloudflareTunnels {
			label := t.Label
			if label == "" {
				label = t.ID
			}
			cf := ""
			if t.CFAccessClientId != "" {
				cf = " (CF Access)"
			}
			check("Tunnel: "+label, "ok", t.URL+cf)
		}
	}

	// Network
	sb.WriteString("\n── Network ──\n")
	ip := getLocalIP()
	if ip != "" && ip != "0.0.0.0" {
		check("Local IP", "ok", ip)
	} else {
		check("Local IP", "warn", "Could not determine")
	}

	sb.WriteString(fmt.Sprintf("\nSummary: %d passed, %d warnings, %d failures\n", ok, warn, fail))
	return mcpToolResult(sb.String())
}

// mcpStatus returns auth/agent/relay status information.
func (s *HTTPServer) mcpStatus() interface{} {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Yaver v%s\n\n", version))

	cfg, err := LoadConfig()
	if err != nil {
		return mcpToolError(fmt.Sprintf("load config: %v", err))
	}

	// Agent
	agentPID, running := isAgentRunning()
	if running {
		sb.WriteString(fmt.Sprintf("Agent: running (PID %d)\n", agentPID))
	} else {
		sb.WriteString("Agent: stopped\n")
	}

	// Auth
	if cfg.AuthToken != "" {
		sb.WriteString("Auth: signed in\n")
		if cfg.DeviceID != "" {
			sb.WriteString(fmt.Sprintf("Device: %s\n", cfg.DeviceID[:8]+"..."))
		}
	} else {
		sb.WriteString("Auth: not signed in\n")
	}

	// Runner
	s.taskMgr.mu.RLock()
	runner := s.taskMgr.runner
	s.taskMgr.mu.RUnlock()
	sb.WriteString(fmt.Sprintf("Runner: %s (%s)\n", runner.Name, runner.RunnerID))

	// Work dir
	sb.WriteString(fmt.Sprintf("Work dir: %s\n", s.taskMgr.workDir))

	// Relay
	if len(cfg.RelayServers) > 0 {
		sb.WriteString(fmt.Sprintf("\nRelay servers: %d configured\n", len(cfg.RelayServers)))
		for _, rs := range cfg.RelayServers {
			label := rs.Label
			if label == "" {
				label = rs.ID
			}
			pw := "no password"
			if rs.Password != "" || cfg.RelayPassword != "" {
				pw = "password set"
			}
			sb.WriteString(fmt.Sprintf("  - %s: %s (%s)\n", label, rs.HttpURL, pw))
		}
	} else {
		sb.WriteString("\nRelay servers: none configured\n")
	}

	// Tunnels
	if len(cfg.CloudflareTunnels) > 0 {
		sb.WriteString(fmt.Sprintf("\nCloudflare Tunnels: %d configured\n", len(cfg.CloudflareTunnels)))
		for _, t := range cfg.CloudflareTunnels {
			label := t.Label
			if label == "" {
				label = t.ID
			}
			sb.WriteString(fmt.Sprintf("  - %s: %s\n", label, t.URL))
		}
	}

	// Tasks
	status := s.taskMgr.GetAgentStatus()
	sb.WriteString(fmt.Sprintf("\nTasks: %d running, %d total\n", status.RunningTasks, status.TotalTasks))

	return mcpToolResult(sb.String())
}

// yaverHelpText returns help documentation for the given topic.
func yaverHelpText(topic string) string {
	switch strings.ToLower(topic) {
	case "tmux":
		return `Tmux Session Adoption
═══════════════════

Yaver can discover and adopt existing tmux sessions, making them visible and
controllable from the mobile app. This is useful when you start an AI agent
(Claude Code, Aider, Codex, etc.) in tmux and want to monitor/interact with
it from your phone.

How it works:
1. Start a tmux session: tmux new -s my-agent
2. Run an AI agent inside it (e.g., claude, aider, codex)
3. Yaver detects it: yaver tmux list (or tmux_list_sessions MCP tool)
4. Adopt it: yaver tmux adopt my-agent (or tmux_adopt_session MCP tool)
5. The session now appears as a task in the mobile app
6. You can send input from mobile — it goes to tmux via send-keys
7. Output is polled every 500ms and streamed to mobile

MCP Tools:
- tmux_list_sessions: List all sessions with agent detection
- tmux_adopt_session: Adopt a session as a Yaver task
- tmux_detach_session: Stop monitoring (session keeps running)
- tmux_send_input: Send keyboard input to an adopted session

Agent detection: Yaver inspects the process tree in each pane to identify
running agents (claude, codex, aider, ollama, goose, amp, opencode).`

	case "relay":
		return `Relay Servers
═════════════

Relay servers enable NAT traversal — your mobile can reach your dev machine
even when it's behind a firewall or on a different network.

How it works:
- Desktop agent connects outbound to relay via QUIC tunnel on startup
- Mobile makes short-lived HTTP requests to relay
- Relay is pass-through — no data stored
- Password-protected for security

Setup:
  yaver relay add https://relay.example.com --password secret --label "My Relay"
  yaver relay test   # Test connectivity
  yaver relay list   # View configured relays

MCP Tools: get_relay_config, add_relay_server, remove_relay_server, relay_test,
relay_set_password, relay_clear_password

Self-hosting: cd relay && RELAY_PASSWORD=secret docker compose up -d`

	case "tunnel":
		return `Cloudflare Tunnels
══════════════════

Cloudflare Tunnel creates a secure HTTPS path from Cloudflare's edge to your
machine. No port forwarding, works through any firewall.

Setup:
  1. Install cloudflared: brew install cloudflared
  2. Create a tunnel: cloudflared tunnel create yaver
  3. Route traffic: cloudflared tunnel route dns yaver yaver.example.com
  4. Run tunnel: cloudflared tunnel --url http://localhost:18080 run yaver
  5. Add to Yaver: yaver tunnel add https://yaver.example.com

MCP Tools: tunnel_list, tunnel_add, tunnel_remove, tunnel_test

For CF Access (zero-trust):
  yaver tunnel add https://yaver.example.com --cf-client-id ID --cf-client-secret SECRET`

	case "mobile":
		return `Mobile App
══════════

The Yaver mobile app (iOS/Android) lets you control AI coding agents from your phone.

Features:
- Create tasks: send prompts to Claude Code, Codex, Aider, etc.
- Live streaming: see agent output in real-time
- Follow-up: send additional instructions to running tasks
- Tmux adoption: discover and control pre-existing tmux sessions
- Multi-device: connect to any registered machine
- Connection modes: LAN (direct), relay (NAT traversal), Cloudflare tunnel

Connection priority:
  1. LAN beacon (UDP broadcast, ~5ms) — same WiFi
  2. Convex IP (direct HTTP, ~5ms) — known IP
  3. QUIC relay (proxied, ~50ms) — roaming/NAT
  4. Cloudflare tunnel — zero-trust

Network changes (WiFi ↔ cellular) are handled silently.`

	case "mcp":
		return `MCP (Model Context Protocol)
════════════════════════════

Yaver exposes an MCP server so AI agents can interact with it programmatically.

Start MCP server:
  yaver mcp              # stdio mode (for Claude Code, etc.)
  yaver mcp --http 8080  # HTTP mode (for remote tools)

Available tool categories:
- Tasks: create_task, list_tasks, get_task, stop_task, continue_task
- Runners: list_runners, switch_runner
- System: get_info, get_system_info, get_config, set_work_dir, list_projects
- Files: read_file, write_file, list_directory, search_files
- Relay: get_relay_config, add_relay_server, remove_relay_server, relay_test
- Tunnels: tunnel_list, tunnel_add, tunnel_remove, tunnel_test
- Tmux: tmux_list_sessions, tmux_adopt_session, tmux_detach_session, tmux_send_input
- Email: email_list_inbox, email_get, email_send, email_sync, email_search
- ACL: acl_list_peers, acl_add_peer, acl_remove_peer, acl_call_peer_tool
- Diagnostics: yaver_doctor, yaver_status, yaver_devices, yaver_logs, yaver_ping
- Config: config_set, relay_set_password, relay_clear_password

Use yaver_help with a topic for details on any category.`

	case "runners":
		return `AI Runners
══════════

Yaver supports multiple AI coding agents. You can switch between them per-task
or set a default.

Built-in runners:
- claude: Claude Code (default) — npm i -g @anthropic-ai/claude-code
- codex: OpenAI Codex — npm i -g @openai/codex
- aider: Aider — pip install aider-chat
- ollama: Ollama — brew install ollama
- goose: Goose — pip install goose-ai
- amp: Amp — npm i -g @anthropic/amp
- opencode: OpenCode — go install github.com/mbreithecker/opencode@latest

Custom runners:
  yaver set-runner custom "my-tool --auto {prompt}"

MCP Tools: list_runners, switch_runner

The runner is also selectable per-task from the mobile app.`

	case "tasks":
		return `Task Management
═══════════════

Tasks are the core abstraction — each task is an AI agent session.

Lifecycle: queued → running → completed/failed/stopped

From mobile: tap + to create, tap task to view, input bar for follow-ups
From MCP: create_task, list_tasks, get_task, stop_task, continue_task
From CLI: yaver attach (interactive REPL)

Adopted tmux sessions also appear as tasks with source="tmux-adopted".
They support input via tmux send-keys and output via pane polling.

Tasks are persisted to ~/.yaver/tasks.json and survive agent restarts.
Adopted tasks are automatically re-adopted if the tmux session still exists.`

	case "auth":
		return `Authentication
══════════════

Yaver uses OAuth via the web app for authentication.

  yaver auth          # Opens browser for sign-in (Apple/Google/Microsoft)
  yaver auth --headless  # Device code flow for SSH/headless servers
  yaver signout       # Clear credentials
  yaver status        # Check auth status

The auth flow:
1. CLI opens https://yaver.io/auth?client=desktop
2. User signs in via Apple/Google/Microsoft
3. Web redirects to http://127.0.0.1:19836/callback?token=<token>
4. CLI saves token to ~/.config/yaver/config.json

The token is used for all API calls and is refreshed automatically.`

	default:
		return `Yaver — AI Coding Agent on Your Phone
═════════════════════════════════════

Yaver is an open-source P2P tool that lets you control any AI coding agent
(Claude Code, Codex, Aider, Ollama, etc.) from your mobile device.

Key features:
- Tasks: Create and manage AI agent sessions from mobile
- Tmux adoption: Discover and control existing tmux sessions
- Multi-runner: Switch between Claude, Codex, Aider, and custom agents
- P2P: Task data flows directly between devices (no server storage)
- Multiple transports: LAN direct, QUIC relay, Cloudflare tunnel
- MCP: Full programmatic access for AI-to-AI workflows

Use yaver_help with a topic for details:
  tmux, relay, tunnel, mobile, mcp, runners, tasks, auth

Quick start:
  1. Install: brew install kivanccakmak/yaver/yaver
  2. Sign in: yaver auth
  3. That's it — the mobile app discovers your machine automatically

CLI commands: auth, serve, status, devices, tmux, relay, tunnel, config,
set-runner, mcp, email, acl, doctor, logs, ping, attach, connect`
	}
}

// resolveFilePath resolves a path relative to the work directory.
func (s *HTTPServer) resolveFilePath(path string) string {
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	return filepath.Join(s.taskMgr.workDir, path)
}

func boolStr(b bool) string {
	if b {
		return "enabled"
	}
	return "disabled"
}

func mcpToolJSON(data interface{}) interface{} {
	out, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return mcpToolError("json marshal error: " + err.Error())
	}
	return mcpToolResult(string(out))
}

func mcpToolResult(text string) interface{} {
	return map[string]interface{}{
		"content": []map[string]interface{}{
			{"type": "text", "text": text},
		},
	}
}

func mcpToolError(text string) interface{} {
	return map[string]interface{}{
		"content": []map[string]interface{}{
			{"type": "text", "text": text},
		},
		"isError": true,
	}
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

// ---------------------------------------------------------------------------
// tmux session management endpoints
// ---------------------------------------------------------------------------

// GET /tmux/sessions — list all tmux sessions with relationship info
func (s *HTTPServer) handleTmuxSessions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	tmuxMgr := s.taskMgr.TmuxMgr
	if tmuxMgr == nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{"sessions": []TmuxSession{}})
		return
	}
	sessions, err := tmuxMgr.ListTmuxSessions()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if sessions == nil {
		sessions = []TmuxSession{}
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"sessions": sessions})
}

// POST /tmux/adopt — adopt an existing tmux session as a yaver task
func (s *HTTPServer) handleTmuxAdopt(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	tmuxMgr := s.taskMgr.TmuxMgr
	if tmuxMgr == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "tmux not available"})
		return
	}
	var body struct {
		Session string `json:"session"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Session == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing session name"})
		return
	}
	task, err := tmuxMgr.AdoptSession(body.Session)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"taskId":  task.ID,
		"session": body.Session,
	})
}

// POST /tmux/detach — detach an adopted tmux session (stop monitoring, keep session alive)
func (s *HTTPServer) handleTmuxDetach(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	tmuxMgr := s.taskMgr.TmuxMgr
	if tmuxMgr == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "tmux not available"})
		return
	}
	var body struct {
		TaskID string `json:"taskId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.TaskID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing taskId"})
		return
	}
	if err := tmuxMgr.DetachSession(body.TaskID); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "detached"})
}

// POST /tmux/input — send keyboard input to an adopted tmux session
func (s *HTTPServer) handleTmuxInput(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	tmuxMgr := s.taskMgr.TmuxMgr
	if tmuxMgr == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "tmux not available"})
		return
	}
	var body struct {
		TaskID string `json:"taskId"`
		Input  string `json:"input"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.TaskID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing taskId or input"})
		return
	}
	if err := tmuxMgr.SendTmuxInput(body.TaskID, body.Input); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "sent"})
}
