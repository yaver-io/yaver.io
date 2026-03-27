package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
)

// handleDevServerStatus returns the current dev server status.
// GET /dev/status
func (s *HTTPServer) handleDevServerStatus(w http.ResponseWriter, r *http.Request) {
	if s.devServerMgr == nil {
		jsonReply(w, http.StatusServiceUnavailable, map[string]string{"error": "dev server not available"})
		return
	}

	status := s.devServerMgr.Status()
	if status == nil {
		jsonReply(w, http.StatusOK, map[string]interface{}{
			"running": false,
		})
		return
	}

	jsonReply(w, http.StatusOK, status)
}

// handleDevServerStart starts a dev server.
// POST /dev/start { "framework": "expo", "workDir": "/path", "platform": "ios", "port": 8081 }
func (s *HTTPServer) handleDevServerStart(w http.ResponseWriter, r *http.Request) {
	if s.devServerMgr == nil {
		jsonReply(w, http.StatusServiceUnavailable, map[string]string{"error": "dev server not available"})
		return
	}

	if r.Method != http.MethodPost {
		jsonReply(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	var req struct {
		Framework string `json:"framework"` // "expo", "flutter", "vite", "nextjs", "" (auto-detect)
		WorkDir   string `json:"workDir"`
		Platform  string `json:"platform"` // "ios", "android", "web"
		Port      int    `json:"port"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonReply(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}

	if req.WorkDir == "" {
		// Default to task manager's work dir
		if s.taskMgr != nil {
			req.WorkDir = s.taskMgr.workDir
		}
	}

	if err := s.devServerMgr.Start(req.Framework, req.WorkDir, req.Platform, req.Port); err != nil {
		jsonReply(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	// Return immediately — server starts in background, mobile polls /dev/status
	status := s.devServerMgr.Status()
	if status == nil {
		jsonReply(w, http.StatusOK, map[string]interface{}{
			"running":   false,
			"framework": req.Framework,
			"workDir":   req.WorkDir,
			"starting":  true,
		})
		return
	}

	jsonReply(w, http.StatusOK, status)
}

// handleDevServerStop stops the active dev server.
// POST /dev/stop
func (s *HTTPServer) handleDevServerStop(w http.ResponseWriter, r *http.Request) {
	if s.devServerMgr == nil {
		jsonReply(w, http.StatusServiceUnavailable, map[string]string{"error": "dev server not available"})
		return
	}

	if err := s.devServerMgr.Stop(); err != nil {
		jsonReply(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	jsonReply(w, http.StatusOK, map[string]string{"ok": "true"})
}

// handleDevServerReload triggers a hot reload on the active dev server.
// POST /dev/reload
func (s *HTTPServer) handleDevServerReload(w http.ResponseWriter, r *http.Request) {
	if s.devServerMgr == nil {
		jsonReply(w, http.StatusServiceUnavailable, map[string]string{"error": "dev server not available"})
		return
	}

	if err := s.devServerMgr.Reload(); err != nil {
		jsonReply(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	// Emit control signal for hot reload
	if s.taskMgr != nil {
		s.taskMgr.BroadcastControlSignal(`{"yaver_control":"hot_reload"}`)
	}

	jsonReply(w, http.StatusOK, map[string]string{"ok": "true"})
}

// handleDevServerEvents streams dev server events via SSE.
// GET /dev/events
func (s *HTTPServer) handleDevServerEvents(w http.ResponseWriter, r *http.Request) {
	if s.devServerMgr == nil {
		jsonReply(w, http.StatusServiceUnavailable, map[string]string{"error": "dev server not available"})
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		jsonReply(w, http.StatusInternalServerError, map[string]string{"error": "streaming not supported"})
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	ch := s.devServerMgr.Subscribe()
	defer s.devServerMgr.Unsubscribe(ch)

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-ch:
			if !ok {
				return
			}
			data, _ := json.Marshal(event)
			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
		}
	}
}

// handleDevServerProxy reverse-proxies requests to the local dev server.
// /dev/* → http://127.0.0.1:{devServerPort}/*
// Supports both HTTP and WebSocket (needed for Metro HMR hot reload).
func (s *HTTPServer) handleDevServerProxy(w http.ResponseWriter, r *http.Request) {
	if s.devServerMgr == nil {
		jsonReply(w, http.StatusServiceUnavailable, map[string]string{"error": "dev server not available"})
		return
	}

	proxy := s.devServerMgr.Proxy()
	if proxy == nil {
		jsonReply(w, http.StatusServiceUnavailable, map[string]string{"error": "no dev server running"})
		return
	}

	// Strip the /dev prefix before forwarding
	r.URL.Path = strings.TrimPrefix(r.URL.Path, "/dev")
	if r.URL.Path == "" {
		r.URL.Path = "/"
	}

	// WebSocket upgrade — Metro uses WS for HMR (/hot) and debugger (/debugger-proxy)
	if isWebSocketUpgrade(r) {
		port := s.devServerMgr.DevServerPort()
		s.proxyWebSocket(w, r, fmt.Sprintf("127.0.0.1:%d", port))
		return
	}

	proxy.ServeHTTP(w, r)
}

func isWebSocketUpgrade(r *http.Request) bool {
	return strings.EqualFold(r.Header.Get("Upgrade"), "websocket")
}

// proxyWebSocket tunnels a WebSocket connection to the target.
func (s *HTTPServer) proxyWebSocket(w http.ResponseWriter, r *http.Request, target string) {
	// Connect to the backend
	backendConn, err := net.Dial("tcp", target)
	if err != nil {
		http.Error(w, "backend unavailable", http.StatusBadGateway)
		return
	}
	defer backendConn.Close()

	// Hijack the client connection
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "websocket not supported", http.StatusInternalServerError)
		return
	}
	clientConn, clientBuf, err := hijacker.Hijack()
	if err != nil {
		http.Error(w, "hijack failed", http.StatusInternalServerError)
		return
	}
	defer clientConn.Close()

	// Forward the original HTTP upgrade request to backend
	if err := r.Write(backendConn); err != nil {
		return
	}

	// Flush any buffered data from the client
	if clientBuf.Reader.Buffered() > 0 {
		buffered := make([]byte, clientBuf.Reader.Buffered())
		clientBuf.Read(buffered)
		backendConn.Write(buffered)
	}

	// Bidirectional copy
	done := make(chan struct{}, 2)
	go func() { io.Copy(clientConn, backendConn); done <- struct{}{} }()
	go func() { io.Copy(backendConn, clientConn); done <- struct{}{} }()
	<-done
}
