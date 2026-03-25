package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/quic-go/quic-go"
)

// TunnelClient connects the local agent to the relay server.
// It maintains a persistent QUIC connection and proxies incoming
// requests to the local HTTP server.
type TunnelClient struct {
	relayAddr  string // relay QUIC address (e.g. "relay.yaver.io:4433")
	agentAddr  string // local agent HTTP address (e.g. "127.0.0.1:18080")
	deviceID   string
	token      string
	password   string // shared relay password
	httpClient *http.Client

	mu   sync.Mutex
	conn quic.Connection
}

func NewTunnelClient(relayAddr, agentAddr, deviceID, token, password string) *TunnelClient {
	return &TunnelClient{
		relayAddr: relayAddr,
		agentAddr: agentAddr,
		deviceID:  deviceID,
		token:     token,
		password:  password,
		httpClient: &http.Client{
			Timeout: 60 * time.Second,
		},
	}
}

// Run connects to the relay and handles incoming requests.
// It automatically reconnects on failure with exponential backoff.
func (t *TunnelClient) Run(ctx context.Context) error {
	backoff := time.Second

	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}

		log.Printf("[TUNNEL] Connecting to relay %s...", t.relayAddr)
		err := t.connectAndServe(ctx)
		if err != nil {
			log.Printf("[TUNNEL] Connection lost: %v", err)
		}

		if ctx.Err() != nil {
			return nil
		}

		log.Printf("[TUNNEL] Reconnecting in %s...", backoff)
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(backoff):
		}

		// Exponential backoff: 1s, 2s, 4s, 8s, max 30s
		backoff *= 2
		if backoff > 30*time.Second {
			backoff = 30 * time.Second
		}
	}
}

func (t *TunnelClient) connectAndServe(ctx context.Context) error {
	tlsCfg := &tls.Config{
		InsecureSkipVerify: true, // relay uses self-signed cert
		NextProtos:         []string{"yaver-relay"},
	}

	conn, err := quic.DialAddr(ctx, t.relayAddr, tlsCfg, &quic.Config{
		MaxIdleTimeout:  120 * time.Second,
		KeepAlivePeriod: 20 * time.Second,
	})
	if err != nil {
		return fmt.Errorf("dial relay: %w", err)
	}
	defer conn.CloseWithError(0, "shutdown")

	t.mu.Lock()
	t.conn = conn
	t.mu.Unlock()

	// Register with relay
	if err := t.register(ctx, conn); err != nil {
		return fmt.Errorf("register: %w", err)
	}

	log.Printf("[TUNNEL] Registered with relay as device %s", t.deviceID[:8])

	// Accept incoming streams (requests from relay)
	for {
		stream, err := conn.AcceptStream(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("accept stream: %w", err)
		}
		go t.handleRequest(stream)
	}
}

func (t *TunnelClient) register(ctx context.Context, conn quic.Connection) error {
	stream, err := conn.OpenStreamSync(ctx)
	if err != nil {
		return fmt.Errorf("open stream: %w", err)
	}

	msg := RegisterMsg{
		Type:     "register",
		DeviceID: t.deviceID,
		Token:    t.token,
		Password: t.password,
	}

	data, _ := json.Marshal(msg)
	if _, err := stream.Write(data); err != nil {
		stream.Close()
		return fmt.Errorf("write: %w", err)
	}
	stream.Close() // signal done writing

	respData, err := io.ReadAll(io.LimitReader(stream, 1<<16))
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}

	var resp RegisterResp
	if err := json.Unmarshal(respData, &resp); err != nil {
		return fmt.Errorf("parse response: %w", err)
	}

	if !resp.OK {
		return fmt.Errorf("registration rejected: %s", resp.Message)
	}

	return nil
}

// handleRequest reads a TunnelRequest from the relay, proxies it to
// the local agent HTTP server, and writes the TunnelResponse back.
func (t *TunnelClient) handleRequest(stream quic.Stream) {
	defer stream.Close()

	// Read tunnel request
	data, err := io.ReadAll(io.LimitReader(stream, 10<<20)) // 10MB
	if err != nil {
		log.Printf("[TUNNEL] read request: %v", err)
		return
	}

	var req TunnelRequest
	if err := json.Unmarshal(data, &req); err != nil {
		log.Printf("[TUNNEL] parse request: %v", err)
		t.sendErrorResponse(stream, req.ID, 400, "invalid tunnel request")
		return
	}

	// Build local HTTP request
	url := fmt.Sprintf("http://%s%s", t.agentAddr, req.Path)
	if req.Query != "" {
		url += "?" + req.Query
	}

	httpReq, err := http.NewRequest(req.Method, url, bytes.NewReader(req.Body))
	if err != nil {
		log.Printf("[TUNNEL] build request: %v", err)
		t.sendErrorResponse(stream, req.ID, 500, "failed to build request")
		return
	}

	for k, v := range req.Headers {
		httpReq.Header.Set(k, v)
	}

	// Check if this is an SSE request (task output, dev server events, blackbox subscribe)
	isSSE := req.Method == "GET" && req.Path != "" &&
		(strings.HasSuffix(req.Path, "/output") ||
			strings.HasSuffix(req.Path, "/dev/events") ||
			strings.HasSuffix(req.Path, "/subscribe"))

	if isSSE {
		t.handleSSERequest(stream, req, httpReq)
		return
	}

	// Execute request against local agent
	resp, err := t.httpClient.Do(httpReq)
	if err != nil {
		log.Printf("[TUNNEL] local request failed: %v", err)
		t.sendErrorResponse(stream, req.ID, 502, fmt.Sprintf("agent error: %v", err))
		return
	}
	defer resp.Body.Close()

	// Dev server bundles can be large (RN bundles ~20MB, Flutter ~50MB+); raise limit for /dev/ paths
	maxRespSize := int64(10 << 20) // 10MB default
	if strings.HasPrefix(req.Path, "/dev/") {
		maxRespSize = 200 << 20 // 200MB for dev server
	}
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, maxRespSize))

	headers := make(map[string]string)
	for k, v := range resp.Header {
		if len(v) > 0 {
			headers[k] = v[0]
		}
	}

	tunnelResp := TunnelResponse{
		ID:         req.ID,
		StatusCode: resp.StatusCode,
		Headers:    headers,
		Body:       respBody,
	}

	respData, _ := json.Marshal(tunnelResp)
	stream.Write(respData)
}

// handleSSERequest proxies Server-Sent Events from the agent to the relay stream.
func (t *TunnelClient) handleSSERequest(stream quic.Stream, req TunnelRequest, httpReq *http.Request) {
	// Use a long timeout for SSE
	client := &http.Client{Timeout: 10 * time.Minute}
	resp, err := client.Do(httpReq)
	if err != nil {
		log.Printf("[TUNNEL] SSE request failed: %v", err)
		t.sendErrorResponse(stream, req.ID, 502, fmt.Sprintf("agent error: %v", err))
		return
	}
	defer resp.Body.Close()

	// Stream the SSE data directly
	buf := make([]byte, 4096)
	for {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := stream.Write(buf[:n]); werr != nil {
				return
			}
		}
		if err != nil {
			return
		}
	}
}

func (t *TunnelClient) sendErrorResponse(stream quic.Stream, id string, code int, msg string) {
	resp := TunnelResponse{
		ID:         id,
		StatusCode: code,
		Headers:    map[string]string{"Content-Type": "application/json"},
		Body:       []byte(fmt.Sprintf(`{"ok":false,"error":"%s"}`, msg)),
	}
	data, _ := json.Marshal(resp)
	stream.Write(data)
}
