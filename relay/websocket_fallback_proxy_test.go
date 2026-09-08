package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/websocket"
)

// Regression for the 1.99.302 false fallback: the agent successfully
// registered through WebSocket, but /d/<device>/health only consulted the QUIC
// lane and returned 502. This test installs NO QUIC connection. If handleProxy
// ever stops routing websocket-only tunnels, the operation-level health probe
// fails here exactly as it would in the mobile app on a UDP-blocking network.
func TestProxyHealthRoutesThroughWebSocketOnlyTunnel(t *testing.T) {
	srv := NewRelayServer(0, 0, "pw", "", "")
	done := make(chan struct{})
	wst := &wsAgentTunnel{
		pending: make(map[string]chan WSTunnelFrame),
		done:    done,
	}
	wst.sendHook = func(frame WSTunnelFrame) error {
		if frame.Type != "request" || frame.Request == nil {
			t.Fatalf("websocket frame = %+v, want request", frame)
		}
		if frame.Request.Path != "/health" || frame.Request.Method != http.MethodGet {
			t.Fatalf("proxied operation = %s %s, want GET /health", frame.Request.Method, frame.Request.Path)
		}

		wst.pendingMu.Lock()
		responseCh := wst.pending[frame.ID]
		wst.pendingMu.Unlock()
		responseCh <- WSTunnelFrame{
			Type: "response",
			ID:   frame.ID,
			Response: &TunnelResponse{
				ID:         frame.Request.ID,
				StatusCode: http.StatusOK,
				Headers:    map[string]string{"Content-Type": "application/json"},
				Body:       []byte(`{"ok":true,"transport":"websocket"}`),
			},
		}
		return nil
	}

	srv.tunnels["device-ws-only"] = &agentTunnel{
		deviceID: "device-ws-only",
		ws:       wst,
	}

	req := httptest.NewRequest(http.MethodGet, "/d/device-ws-only/health", nil)
	req.Header.Set("X-Relay-Password", "pw")
	rr := httptest.NewRecorder()
	srv.handleProxy(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	if got := rr.Body.String(); got != `{"ok":true,"transport":"websocket"}` {
		t.Fatalf("body = %q", got)
	}
}

// The WebSocket recovery lane must match QUIC's collision boundary: a
// Convex-proven same-owner reconnect may replace a half-open tunnel, while a
// different owner may not claim the device even with otherwise valid relay
// credentials.
func TestWebSocketFallbackReconnectIsSameOwnerOnly(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]string
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("decode relay validation: %v", err)
			return
		}
		userID := "user-a"
		if payload["token"] == "token-b" {
			userID = "user-b"
		}
		_, _ = io.WriteString(w, `{"ok":true,"userId":"`+userID+`"}`)
	}))
	defer backend.Close()

	relay := NewRelayServer(0, 0, "", backend.URL, "")
	mux := http.NewServeMux()
	mux.Handle("/agent/tunnel/ws", websocket.Handler(relay.handleAgentWebSocket))
	proxy := httptest.NewServer(mux)
	defer proxy.Close()
	wsURL := "ws" + strings.TrimPrefix(proxy.URL, "http") + "/agent/tunnel/ws"

	dialAndRegister := func(token, password string) (*websocket.Conn, WSTunnelFrame) {
		t.Helper()
		conn, err := websocket.Dial(wsURL, "", proxy.URL)
		if err != nil {
			t.Fatalf("dial websocket fallback: %v", err)
		}
		reg := &RegisterMsg{Type: "register", DeviceID: "device-ws-reconnect", Token: token, Password: password}
		if err := websocket.JSON.Send(conn, WSTunnelFrame{Type: "register", Register: reg}); err != nil {
			_ = conn.Close()
			t.Fatalf("send registration: %v", err)
		}
		var response WSTunnelFrame
		if err := websocket.JSON.Receive(conn, &response); err != nil {
			_ = conn.Close()
			t.Fatalf("receive registration response: %v", err)
		}
		return conn, response
	}

	first, firstResp := dialAndRegister("token-a", "password-a")
	defer first.Close()
	if !firstResp.OK || firstResp.Type != "registered" {
		t.Fatalf("first registration = %+v, want registered", firstResp)
	}

	second, secondResp := dialAndRegister("token-a", "password-a")
	defer second.Close()
	if !secondResp.OK || secondResp.Type != "registered" {
		t.Fatalf("same-owner reconnect = %+v, want registered", secondResp)
	}
	_ = first.SetReadDeadline(time.Now().Add(time.Second))
	var closedFrame WSTunnelFrame
	if err := websocket.JSON.Receive(first, &closedFrame); err == nil {
		t.Fatalf("superseded same-owner tunnel remained readable: %+v", closedFrame)
	}

	other, otherResp := dialAndRegister("token-b", "password-b")
	defer other.Close()
	if otherResp.OK || otherResp.Type != "error" || otherResp.Message != "deviceId already registered" {
		t.Fatalf("different-owner collision = %+v, want deviceId already registered", otherResp)
	}
}
