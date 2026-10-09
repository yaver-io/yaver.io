package main

// bus_websocket_access.go is the default hosted Access Channel transport.
// It carries only the strict accessSignal grammar over a hibernating
// Cloudflare WebSocket. The normal Yaver bearer is exchanged over HTTPS for a
// five-minute capability JWT and never reaches the WebSocket service.

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

type websocketAccessTransport struct {
	cfg     *Config
	b       *Bus
	mu      sync.RWMutex
	writeMu sync.Mutex
	conn    *websocket.Conn
	cancel  context.CancelFunc
	wg      sync.WaitGroup
}

func NewWebSocketAccessTransport(cfg *Config, b *Bus) *websocketAccessTransport {
	return &websocketAccessTransport{cfg: cfg, b: b}
}

func (t *websocketAccessTransport) Name() string { return "websocket-access" }

func (t *websocketAccessTransport) Start(parent context.Context) {
	ctx, cancel := context.WithCancel(parent)
	t.cancel = cancel
	t.wg.Add(1)
	go t.run(ctx)
}

func (t *websocketAccessTransport) Close() error {
	if t.cancel != nil {
		t.cancel()
	}
	t.mu.Lock()
	if t.conn != nil {
		_ = t.conn.Close()
		t.conn = nil
	}
	t.mu.Unlock()
	t.wg.Wait()
	return nil
}

func (t *websocketAccessTransport) Publish(ctx context.Context, evt BusEvent) error {
	if !isAccessTopic(evt.Topic) {
		return nil
	}
	deviceID, direction, _, ok := accessTopicParts(evt.Topic)
	if !ok || direction != "events" || deviceID != t.cfg.DeviceID {
		return fmt.Errorf("hosted access publish denied for topic %q", evt.Topic)
	}
	t.mu.RLock()
	conn := t.conn
	t.mu.RUnlock()
	if conn == nil {
		return fmt.Errorf("hosted access channel is not connected")
	}
	t.writeMu.Lock()
	defer t.writeMu.Unlock()
	deadline := time.Now().Add(10 * time.Second)
	if end, ok := ctx.Deadline(); ok && end.Before(deadline) {
		deadline = end
	}
	_ = conn.SetWriteDeadline(deadline)
	return conn.WriteMessage(websocket.TextMessage, evt.Payload)
}

func (t *websocketAccessTransport) run(ctx context.Context) {
	defer t.wg.Done()
	backoff := 5 * time.Second
	for ctx.Err() == nil {
		grant, err := t.fetchGrant(ctx)
		if err != nil {
			log.Printf("[access] hosted channel unavailable: %v", err)
			if !waitContext(ctx, backoff) {
				return
			}
			if backoff < 5*time.Minute {
				backoff *= 2
			}
			continue
		}
		if err := t.connectAndRead(ctx, grant); err != nil && ctx.Err() == nil {
			log.Printf("[access] hosted channel disconnected: %v", err)
		}
		if !waitContext(ctx, backoff) {
			return
		}
		if backoff < time.Minute {
			backoff *= 2
		}
	}
}

func (t *websocketAccessTransport) fetchGrant(ctx context.Context) (accessTokenResponse, error) {
	base := strings.TrimRight(strings.TrimSpace(t.cfg.ConvexSiteURL), "/")
	if base == "" {
		base = defaultConvexSiteURL
	}
	// Normal reconnects authenticate with the installation-generated Ed25519
	// key. This keeps a remote box reachable after its account bearer expires.
	// Only first enrollment needs the owner's live Yaver session.
	if signingKey, err := LoadOrGenerateSigningKey(); err == nil {
		timestamp := time.Now().UnixMilli()
		nonce := newSigNonce()
		message := canonicalAccessDeviceProof(t.cfg.DeviceID, timestamp, nonce)
		var signed accessTokenResponse
		err = accessPublicJSON(ctx, http.MethodPost, base+"/access-channel/device-token", map[string]interface{}{
			"deviceId":  t.cfg.DeviceID,
			"timestamp": timestamp,
			"nonce":     nonce,
			"signature": signingKey.Sign([]byte(message)),
		}, &signed)
		if err == nil && signed.OK && signed.Transport == "websocket" && signed.Endpoint != "" && signed.Token != "" {
			return signed, nil
		}
	}
	if strings.TrimSpace(t.cfg.AuthToken) == "" {
		return accessTokenResponse{}, fmt.Errorf("device is not enrolled and no owner session is available")
	}
	var listed struct {
		Brokers []accessBrokerRow `json:"brokers"`
	}
	if err := accessAccountJSON(ctx, t.cfg, http.MethodGet, base+"/access-channel/brokers", nil, &listed); err != nil {
		return accessTokenResponse{}, err
	}
	var broker accessBrokerRow
	for _, row := range listed.Brokers {
		if row.Enabled && row.Transport == "websocket" {
			broker = row
			break
		}
	}
	if broker.BrokerID == "" {
		if err := accessAccountJSON(ctx, t.cfg, http.MethodPost, base+"/access-channel/hosted", map[string]string{}, nil); err != nil {
			return accessTokenResponse{}, err
		}
		if err := accessAccountJSON(ctx, t.cfg, http.MethodGet, base+"/access-channel/brokers", nil, &listed); err != nil {
			return accessTokenResponse{}, err
		}
		for _, row := range listed.Brokers {
			if row.Enabled && row.Transport == "websocket" {
				broker = row
				break
			}
		}
	}
	if broker.BrokerID == "" {
		return accessTokenResponse{}, fmt.Errorf("hosted access channel was not registered")
	}
	if err := accessAccountJSON(ctx, t.cfg, http.MethodPost, base+"/access-channel/enroll", map[string]string{
		"brokerId": broker.BrokerID, "deviceId": t.cfg.DeviceID,
	}, nil); err != nil {
		return accessTokenResponse{}, fmt.Errorf("enroll device: %w", err)
	}
	var grant accessTokenResponse
	if err := accessAccountJSON(ctx, t.cfg, http.MethodPost, base+"/access-channel/token", map[string]string{
		"brokerId": broker.BrokerID, "deviceId": t.cfg.DeviceID, "role": "device",
	}, &grant); err != nil {
		return accessTokenResponse{}, fmt.Errorf("mint access credential: %w", err)
	}
	if !grant.OK || grant.Transport != "websocket" || grant.Endpoint == "" || grant.Token == "" {
		return accessTokenResponse{}, fmt.Errorf("incomplete hosted access grant")
	}
	return grant, nil
}

func (t *websocketAccessTransport) connectAndRead(parent context.Context, grant accessTokenResponse) error {
	deadline := time.UnixMilli(grant.ExpiresAt).Add(-45 * time.Second)
	ctx, cancel := context.WithDeadline(parent, deadline)
	defer cancel()
	dialer := websocket.Dialer{HandshakeTimeout: 20 * time.Second, Subprotocols: []string{"yaver-access-v1", "auth." + grant.Token}}
	conn, resp, err := dialer.DialContext(ctx, grant.Endpoint, nil)
	if err != nil {
		if resp != nil {
			return fmt.Errorf("websocket HTTP %d: %w", resp.StatusCode, err)
		}
		return err
	}
	if conn.Subprotocol() != "yaver-access-v1" {
		_ = conn.Close()
		return fmt.Errorf("access channel did not negotiate the expected protocol")
	}
	t.mu.Lock()
	old := t.conn
	t.conn = conn
	t.mu.Unlock()
	if old != nil {
		_ = old.Close()
	}
	defer func() {
		t.mu.Lock()
		if t.conn == conn {
			t.conn = nil
		}
		t.mu.Unlock()
		_ = conn.Close()
	}()
	log.Printf("[access] connected to hosted channel; credential expires in %s", time.Until(time.UnixMilli(grant.ExpiresAt)).Round(time.Second))
	go func() { <-ctx.Done(); _ = conn.Close() }()
	for {
		kind, raw, err := conn.ReadMessage()
		if err != nil {
			return err
		}
		if kind != websocket.TextMessage || len(raw) > accessSignalMaxBytes {
			continue
		}
		var signal accessSignal
		if err := json.Unmarshal(raw, &signal); err != nil {
			continue
		}
		action := strings.TrimPrefix(signal.Kind, "requests.")
		if action == signal.Kind {
			continue
		}
		t.b.ReceiveAccess(BusEvent{ID: signal.RequestID, Topic: accessTopicPrefix + t.cfg.DeviceID + "/requests/" + action,
			Publisher: signal.IssuerDeviceID, PublishedAt: signal.IssuedAt, QoS: 1, Payload: append([]byte(nil), raw...)})
	}
}
