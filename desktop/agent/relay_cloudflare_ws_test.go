package main

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"

	gorillawebsocket "github.com/gorilla/websocket"
	"golang.org/x/net/websocket"
)

// TestCloudflareEdgeTunnelLive is an opt-in operation test against wrangler
// dev or the production edge. It deliberately dials the edge WebSocket
// directly: no QUIC address and no Yaver Free Relay process exists in this
// test, so a green result proves the Cloudflare-only fallback path.
func TestCloudflareEdgeTunnelLive(t *testing.T) {
	edgeURL := strings.TrimRight(os.Getenv("YAVER_EDGE_TEST_URL"), "/")
	token := os.Getenv("YAVER_EDGE_TEST_TOKEN")
	otherToken := os.Getenv("YAVER_EDGE_TEST_OTHER_TOKEN")
	deviceID := os.Getenv("YAVER_EDGE_TEST_DEVICE_ID")
	if os.Getenv("YAVER_EDGE_TEST_USE_CURRENT_CONFIG") == "1" {
		cfg, err := LoadConfig()
		if err != nil {
			t.Fatalf("load current Yaver config: %v", err)
		}
		token = cfg.AuthToken
		deviceID = cfg.DeviceID
	}
	if os.Getenv("YAVER_EDGE_TEST_PUBLIC_DNS") == "1" {
		dialer := cloudflareEdgeTestDialer()
		relayWebSocketDialerOverride = dialer
		t.Cleanup(func() { relayWebSocketDialerOverride = nil })
	}
	if edgeURL == "" || token == "" || deviceID == "" {
		t.Skip("set YAVER_EDGE_TEST_URL, YAVER_EDGE_TEST_TOKEN, and YAVER_EDGE_TEST_DEVICE_ID")
	}

	agent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/echo" {
			upgrader := gorillawebsocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
			conn, err := upgrader.Upgrade(w, r, nil)
			if err != nil {
				return
			}
			defer conn.Close()
			messageType, data, err := conn.ReadMessage()
			if err == nil {
				_ = conn.WriteMessage(messageType, append([]byte("echo:"), data...))
			}
			return
		}
		if r.URL.Path == "/events" {
			w.Header().Set("Content-Type", "text/event-stream")
			flusher, _ := w.(http.Flusher)
			_, _ = io.WriteString(w, "data: one\n\n")
			flusher.Flush()
			time.Sleep(25 * time.Millisecond)
			_, _ = io.WriteString(w, "data: two\n\n")
			flusher.Flush()
			return
		}
		if r.URL.Path != "/health" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"ok":true,"transport":"cloudflare-edge"}`)
	}))
	defer agent.Close()
	agentURL, err := url.Parse(agent.URL)
	if err != nil {
		t.Fatal(err)
	}

	wsURL := strings.Replace(edgeURL, "https://", "wss://", 1)
	wsURL = strings.Replace(wsURL, "http://", "ws://", 1) + "/agent/tunnel/ws"
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	tunnelErr := make(chan error, 1)
	if os.Getenv("YAVER_EDGE_TEST_USE_CURRENT_CONFIG") == "1" {
		// Exercise the production HTTP-only manager path: no QuicAddr exists and
		// no failed UDP attempt is needed before the edge becomes reachable.
		go runRelayWebSocketTunnel(ctx, RelayServerInfo{
			ID: hostedEdgeRelayID, HttpURL: edgeURL,
		}, agentURL.Host, deviceID, token, "", nil)
	} else {
		go func() {
			tunnelErr <- relayConnectAndServeWebSocket(ctx, wsURL, agentURL.Host, deviceID, token, "")
		}()
	}

	client := &http.Client{Timeout: 2 * time.Second}
	if os.Getenv("YAVER_EDGE_TEST_PUBLIC_DNS") == "1" {
		transport := &http.Transport{DialContext: cloudflareEdgeTestDialer().DialContext}
		client.Transport = transport
		t.Cleanup(transport.CloseIdleConnections)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		req, reqErr := http.NewRequest(http.MethodGet, edgeURL+"/d/"+url.PathEscape(deviceID)+"/health", nil)
		if reqErr != nil {
			t.Fatal(reqErr)
		}
		req.Header.Set("Authorization", "Bearer "+token)
		response, requestErr := client.Do(req)
		if requestErr == nil {
			body, _ := io.ReadAll(response.Body)
			response.Body.Close()
			if response.StatusCode == http.StatusOK {
				if got := string(body); got != `{"ok":true,"transport":"cloudflare-edge"}` {
					t.Fatalf("edge response = %q", got)
				}
				assertCloudflareEdgeSSE(t, client, edgeURL, deviceID, token)
				assertCloudflareEdgeWebSocket(t, edgeURL, deviceID, token)
				assertCloudflareEdgePresence(t, client, edgeURL, deviceID, token)
				if otherToken != "" {
					assertCloudflareEdgeTenantIsolation(t, edgeURL, wsURL, deviceID, otherToken)
				}
				return
			}
		}
		select {
		case tunnelFailure := <-tunnelErr:
			t.Fatalf("Cloudflare-only agent tunnel failed: %v", tunnelFailure)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("Cloudflare-only /d/{deviceId}/health did not become reachable")
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func assertCloudflareEdgePresence(t *testing.T, client *http.Client, edgeURL, deviceID, token string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, edgeURL+"/presence?ids="+url.QueryEscape(deviceID), nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	response, err := client.Do(req)
	if err != nil {
		t.Fatalf("Cloudflare presence request: %v", err)
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(response.Body)
	if response.StatusCode != http.StatusOK || !strings.Contains(string(body), `"online":true`) {
		t.Fatalf("Cloudflare presence status=%d body=%q", response.StatusCode, body)
	}
}

func assertCloudflareEdgeWebSocket(t *testing.T, edgeURL, deviceID, token string) {
	t.Helper()
	wsURL := strings.Replace(edgeURL, "https://", "wss://", 1)
	wsURL = strings.Replace(wsURL, "http://", "ws://", 1) + "/d/" + url.PathEscape(deviceID) + "/echo"
	dialer := gorillawebsocket.Dialer{HandshakeTimeout: 5 * time.Second}
	if os.Getenv("YAVER_EDGE_TEST_PUBLIC_DNS") == "1" {
		dialer.NetDialContext = cloudflareEdgeTestDialer().DialContext
	}
	headers := http.Header{"Authorization": []string{"Bearer " + token}}
	conn, response, err := dialer.Dial(wsURL, headers)
	if response != nil && response.Body != nil {
		defer response.Body.Close()
	}
	if err != nil {
		t.Fatalf("Cloudflare WebSocket dial: %v", err)
	}
	defer conn.Close()
	if err := conn.WriteMessage(gorillawebsocket.BinaryMessage, []byte("ping")); err != nil {
		t.Fatalf("Cloudflare WebSocket write: %v", err)
	}
	messageType, data, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("Cloudflare WebSocket read: %v", err)
	}
	if messageType != gorillawebsocket.BinaryMessage || string(data) != "echo:ping" {
		t.Fatalf("Cloudflare WebSocket message type=%d data=%q", messageType, data)
	}
}

func assertCloudflareEdgeSSE(t *testing.T, client *http.Client, edgeURL, deviceID, token string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, edgeURL+"/d/"+url.PathEscape(deviceID)+"/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "text/event-stream")
	response, err := client.Do(req)
	if err != nil {
		t.Fatalf("Cloudflare SSE request: %v", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("Cloudflare SSE body: %v", err)
	}
	if response.StatusCode != http.StatusOK || string(body) != "data: one\n\ndata: two\n\n" {
		t.Fatalf("Cloudflare SSE status=%d body=%q", response.StatusCode, body)
	}
}

// TestCloudflareEdgeRegisterCurrentDeviceLive performs the same registration
// an upgraded `yaver serve` will do after its control-plane origin moves. It is
// opt-in because it writes the current device's public routing identity to the
// remote Edge database. Private key material never leaves LoadOrGenerateKeys.
func TestCloudflareEdgeRegisterCurrentDeviceLive(t *testing.T) {
	if os.Getenv("YAVER_EDGE_BOOTSTRAP_CURRENT_DEVICE") != "1" {
		t.Skip("set YAVER_EDGE_BOOTSTRAP_CURRENT_DEVICE=1")
	}
	edgeURL := strings.TrimRight(os.Getenv("YAVER_EDGE_TEST_URL"), "/")
	if edgeURL == "" {
		t.Fatal("YAVER_EDGE_TEST_URL is required")
	}
	if os.Getenv("YAVER_EDGE_TEST_PUBLIC_DNS") == "1" {
		oldClient := httpClient
		transport := &http.Transport{DialContext: cloudflareEdgeTestDialer().DialContext}
		httpClient = &http.Client{Timeout: httpTimeout, Transport: transport}
		t.Cleanup(func() {
			transport.CloseIdleConnections()
			httpClient = oldClient
		})
	}
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("load current Yaver config: %v", err)
	}
	keys, err := LoadOrGenerateKeys()
	if err != nil {
		t.Fatalf("load device public key: %v", err)
	}
	hostname, _ := os.Hostname()
	platform := runtime.GOOS
	if platform == "darwin" {
		platform = "macos"
	}
	_, err = RegisterDevice(edgeURL, RegisterDeviceRequest{
		Token:         cfg.AuthToken,
		DeviceID:      cfg.DeviceID,
		Name:          hostname,
		Platform:      platform,
		PublicKey:     keys.PublicKeyBase64(),
		SignPublicKey: deviceSignPublicKey(),
		AgentVersion:  version,
	})
	if err != nil {
		t.Fatalf("register current device with Cloudflare Edge: %v", err)
	}
}

// TestCloudflareEdgeCurrentAgentLive proves the actual installed agent HTTP
// operation is reachable through the new HTTP-only manager path. It does not
// replace or restart the installed binary and never touches the legacy relay.
func TestCloudflareEdgeCurrentAgentLive(t *testing.T) {
	if os.Getenv("YAVER_EDGE_TEST_CURRENT_AGENT") != "1" {
		t.Skip("set YAVER_EDGE_TEST_CURRENT_AGENT=1")
	}
	edgeURL := strings.TrimRight(os.Getenv("YAVER_EDGE_TEST_URL"), "/")
	if edgeURL == "" {
		t.Fatal("YAVER_EDGE_TEST_URL is required")
	}
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if os.Getenv("YAVER_EDGE_TEST_PUBLIC_DNS") == "1" {
		relayWebSocketDialerOverride = cloudflareEdgeTestDialer()
		t.Cleanup(func() { relayWebSocketDialerOverride = nil })
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	go runRelayWebSocketTunnel(ctx, RelayServerInfo{
		ID: hostedEdgeRelayID, HttpURL: edgeURL,
	}, "127.0.0.1:18080", cfg.DeviceID, cfg.AuthToken, "", nil)

	client := &http.Client{Timeout: 3 * time.Second}
	if os.Getenv("YAVER_EDGE_TEST_PUBLIC_DNS") == "1" {
		transport := &http.Transport{DialContext: cloudflareEdgeTestDialer().DialContext}
		client.Transport = transport
		t.Cleanup(transport.CloseIdleConnections)
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		req, requestErr := http.NewRequest(http.MethodGet, edgeURL+"/d/"+url.PathEscape(cfg.DeviceID)+"/health", nil)
		if requestErr != nil {
			t.Fatal(requestErr)
		}
		req.Header.Set("Authorization", "Bearer "+cfg.AuthToken)
		response, requestErr := client.Do(req)
		if requestErr == nil {
			body, _ := io.ReadAll(response.Body)
			response.Body.Close()
			if response.StatusCode == http.StatusOK && strings.Contains(string(body), `"ok":true`) {
				assertCloudflareEdgePresence(t, client, edgeURL, cfg.DeviceID, cfg.AuthToken)
				return
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("installed agent /health did not become reachable through Cloudflare edge")
}

func cloudflareEdgeTestDialer() *net.Dialer {
	resolverDialer := &net.Dialer{Timeout: 3 * time.Second}
	resolver := &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return resolverDialer.DialContext(ctx, "udp", "1.1.1.1:53")
		},
	}
	return &net.Dialer{Timeout: 5 * time.Second, Resolver: resolver}
}

func assertCloudflareEdgeTenantIsolation(t *testing.T, edgeURL, wsURL, deviceID, otherToken string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, edgeURL+"/d/"+url.PathEscape(deviceID)+"/health", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+otherToken)
	response, err := (&http.Client{Timeout: 2 * time.Second}).Do(req)
	if err != nil {
		t.Fatalf("cross-tenant request: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("cross-tenant /d request status = %d, want 404", response.StatusCode)
	}

	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	if err := relayConnectAndServeWebSocket(ctx, wsURL, "127.0.0.1:1", deviceID, otherToken, ""); err == nil {
		t.Fatal("cross-tenant agent registration unexpectedly succeeded")
	}
}

// The hosted edge is invisible to the user: the agent presents only its Yaver
// session and device routing identity. A regression that drops either header
// makes an installed agent look healthy locally while no off-LAN surface can
// reach it.
func TestCloudflareWebSocketRegistrationCarriesYaverIdentityOnly(t *testing.T) {
	seen := make(chan *http.Request, 1)
	server := httptest.NewServer(websocket.Handler(func(conn *websocket.Conn) {
		seen <- conn.Request().Clone(conn.Request().Context())
		var registration relayWSTunnelFrame
		if err := websocket.JSON.Receive(conn, &registration); err != nil {
			return
		}
		_ = websocket.JSON.Send(conn, relayWSTunnelFrame{Type: "registered", OK: true})
		_ = conn.Close()
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/agent/tunnel/ws"
	err := relayConnectAndServeWebSocket(t.Context(), wsURL, "127.0.0.1:1", "device-edge-1", strings.Repeat("a", 64), "")
	if err == nil || !strings.Contains(err.Error(), "websocket relay closed") {
		t.Fatalf("relayConnectAndServeWebSocket() error = %v, want clean post-registration close", err)
	}

	select {
	case request := <-seen:
		if got := request.URL.Query().Get("deviceId"); got != "device-edge-1" {
			t.Fatalf("deviceId query = %q, want device-edge-1", got)
		}
		if got := request.Header.Get("Authorization"); got != "Bearer "+strings.Repeat("a", 64) {
			t.Fatalf("Authorization header missing or changed")
		}
		if got := request.Header.Get("X-Yaver-Device-ID"); got != "device-edge-1" {
			t.Fatalf("X-Yaver-Device-ID = %q", got)
		}
		for key := range request.Header {
			lower := strings.ToLower(key)
			if strings.Contains(lower, "cloudflare-api") || strings.Contains(lower, "tunnel-token") {
				t.Fatalf("Cloudflare management credential leaked through header %q", key)
			}
		}
	case <-time.After(2 * time.Second):
		t.Fatal("agent never reached websocket edge")
	}
}

func TestCloudflareWebSocketDeviceHintPreservesExistingQuery(t *testing.T) {
	raw := "wss://relay.example/agent/tunnel/ws?mode=compat"
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	query := parsed.Query()
	query.Set("deviceId", "device-edge-1")
	parsed.RawQuery = query.Encode()
	if parsed.Query().Get("mode") != "compat" || parsed.Query().Get("deviceId") != "device-edge-1" {
		t.Fatalf("query was not additive: %s", parsed.RawQuery)
	}
}
