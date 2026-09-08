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
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"ok":true,"userId":"user-a"}`)
	}))
	defer backend.Close()

	relay := NewRelayServer(0, 0, "", backend.URL, "")
	mux := http.NewServeMux()
	mux.Handle("/agent/tunnel/ws", websocket.Handler(relay.handleAgentWebSocket))
	mux.HandleFunc("/d/", relay.handleProxy)
	proxy := httptest.NewServer(mux)
	defer proxy.Close()

	wsURL := "ws" + strings.TrimPrefix(proxy.URL, "http") + "/agent/tunnel/ws"
	agent, err := websocket.Dial(wsURL, "", proxy.URL)
	if err != nil {
		t.Fatalf("dial websocket fallback: %v", err)
	}
	defer agent.Close()
	reg := &RegisterMsg{Type: "register", DeviceID: "device-ws-only", Token: "token-a", Password: "password-a"}
	if err := websocket.JSON.Send(agent, WSTunnelFrame{Type: "register", Register: reg}); err != nil {
		t.Fatalf("send registration: %v", err)
	}
	var registered WSTunnelFrame
	if err := websocket.JSON.Receive(agent, &registered); err != nil {
		t.Fatalf("receive registration response: %v", err)
	}
	if !registered.OK || registered.Type != "registered" {
		t.Fatalf("registration = %+v, want registered", registered)
	}

	agentErr := make(chan error, 1)
	go func() {
		var frame WSTunnelFrame
		if err := websocket.JSON.Receive(agent, &frame); err != nil {
			agentErr <- err
			return
		}
		if frame.Type != "request" || frame.Request == nil {
			agentErr <- &fallbackTestError{"websocket frame was not a request"}
			return
		}
		if frame.Request.Path != "/health" || frame.Request.Method != http.MethodGet {
			agentErr <- &fallbackTestError{"proxied operation was not GET /health"}
			return
		}
		agentErr <- websocket.JSON.Send(agent, WSTunnelFrame{
			Type: "response",
			ID:   frame.ID,
			Response: &TunnelResponse{
				ID:         frame.Request.ID,
				StatusCode: http.StatusOK,
				Headers:    map[string]string{"Content-Type": "application/json"},
				Body:       []byte(`{"ok":true,"transport":"websocket"}`),
			},
		})
	}()

	req, err := http.NewRequest(http.MethodGet, proxy.URL+"/d/device-ws-only/health", nil)
	if err != nil {
		t.Fatalf("build proxy request: %v", err)
	}
	req.Header.Set("X-Relay-Password", "pw")
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("proxy health: %v", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read proxy response: %v", err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", response.StatusCode, body)
	}
	if got := string(body); got != `{"ok":true,"transport":"websocket"}` {
		t.Fatalf("body = %q", got)
	}
	if err := <-agentErr; err != nil {
		t.Fatalf("websocket agent: %v", err)
	}
}

type fallbackTestError struct{ message string }

func (e *fallbackTestError) Error() string { return e.message }

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
