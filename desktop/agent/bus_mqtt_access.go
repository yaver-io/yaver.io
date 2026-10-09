package main

// bus_mqtt_access.go is a platform-neutral MQTT rendezvous transport. It uses
// normal HTTPS Yaver account auth to discover an owner-registered broker,
// enrolls only this already-owned device, then exchanges the Yaver bearer for a
// five-minute MQTT JWT. The bearer never reaches MQTT or disk.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
)

type accessBrokerRow struct {
	BrokerID      string  `json:"brokerId"`
	Endpoint      string  `json:"endpoint"`
	Transport     string  `json:"transport"`
	CAFingerprint *string `json:"caFingerprint"`
	Enabled       bool    `json:"enabled"`
}

type accessTokenResponse struct {
	OK            bool    `json:"ok"`
	Endpoint      string  `json:"endpoint"`
	BrokerID      string  `json:"brokerId"`
	ClientID      string  `json:"clientId"`
	Username      string  `json:"username"`
	Password      string  `json:"password"`
	Token         string  `json:"token"`
	Transport     string  `json:"transport"`
	TopicRoot     string  `json:"topicRoot"`
	CAFingerprint *string `json:"caFingerprint"`
	ExpiresAt     int64   `json:"expiresAt"`
}

type mqttAccessTransport struct {
	cfg    *Config
	b      *Bus
	client mqtt.Client
	mu     sync.RWMutex
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

func NewMQTTAccessTransport(cfg *Config, b *Bus) *mqttAccessTransport {
	return &mqttAccessTransport{cfg: cfg, b: b}
}

func (t *mqttAccessTransport) Name() string { return "mqtt-access" }

func (t *mqttAccessTransport) Start(parent context.Context) {
	ctx, cancel := context.WithCancel(parent)
	t.cancel = cancel
	t.wg.Add(1)
	go t.run(ctx)
}

func (t *mqttAccessTransport) Close() error {
	if t.cancel != nil {
		t.cancel()
	}
	t.wg.Wait()
	t.mu.Lock()
	if t.client != nil && t.client.IsConnected() {
		t.client.Disconnect(250)
	}
	t.client = nil
	t.mu.Unlock()
	return nil
}

func (t *mqttAccessTransport) Publish(ctx context.Context, evt BusEvent) error {
	if !isAccessTopic(evt.Topic) {
		return nil
	}
	deviceID, direction, action, ok := accessTopicParts(evt.Topic)
	if !ok || direction != "events" || deviceID != t.cfg.DeviceID {
		return fmt.Errorf("mqtt access publish denied for topic %q", evt.Topic)
	}
	t.mu.RLock()
	client := t.client
	t.mu.RUnlock()
	if client == nil || !client.IsConnectionOpen() {
		return fmt.Errorf("mqtt access broker is not connected")
	}
	// The active topic root is held in the client store below; deriving it from
	// the token response prevents caller-controlled tenant prefixes.
	topicRoot := mqttTopicRoot(client)
	if topicRoot == "" {
		return fmt.Errorf("mqtt access topic root unavailable")
	}
	wireTopic := topicRoot + "/device/" + deviceID + "/events/" + action
	tok := client.Publish(wireTopic, evt.QoS, false, evt.Payload)
	if !tok.WaitTimeout(10 * time.Second) {
		return fmt.Errorf("mqtt access publish timed out")
	}
	return tok.Error()
}

// Paho does not expose arbitrary connection metadata. Keep the topic root in a
// process-local map keyed by client; it contains only a random tenant id.
var mqttAccessRoots sync.Map

func mqttTopicRoot(client mqtt.Client) string {
	root, _ := mqttAccessRoots.Load(client)
	value, _ := root.(string)
	return value
}

func (t *mqttAccessTransport) run(ctx context.Context) {
	defer t.wg.Done()
	backoff := 5 * time.Second
	for ctx.Err() == nil {
		grant, err := t.fetchGrant(ctx)
		if err != nil {
			// No broker is a normal state for accounts that did not opt in.
			if !strings.Contains(err.Error(), "no enabled access broker") {
				log.Printf("[mqtt-access] unavailable: %v", err)
			}
			if !waitContext(ctx, backoff) {
				return
			}
			if backoff < 5*time.Minute {
				backoff *= 2
			}
			continue
		}
		client, err := t.connect(ctx, grant)
		if err != nil {
			log.Printf("[mqtt-access] connect failed: %v", err)
			if !waitContext(ctx, backoff) {
				return
			}
			continue
		}
		backoff = 5 * time.Second
		t.mu.Lock()
		old := t.client
		t.client = client
		t.mu.Unlock()
		if old != nil {
			mqttAccessRoots.Delete(old)
			old.Disconnect(100)
		}
		// Renew before the five-minute JWT expires. Reconnect creates a new
		// clean session, so revoked grants stop working within one token life.
		renewIn := time.Until(time.UnixMilli(grant.ExpiresAt).Add(-45 * time.Second))
		if renewIn < 30*time.Second {
			renewIn = 30 * time.Second
		}
		if !waitContext(ctx, renewIn) {
			return
		}
	}
}

func waitContext(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func (t *mqttAccessTransport) fetchGrant(ctx context.Context) (accessTokenResponse, error) {
	base := strings.TrimRight(strings.TrimSpace(t.cfg.ConvexSiteURL), "/")
	if base == "" {
		base = defaultConvexSiteURL
	}
	var listed struct {
		Brokers []accessBrokerRow `json:"brokers"`
	}
	if err := t.accountJSON(ctx, http.MethodGet, base+"/access-channel/brokers", nil, &listed); err != nil {
		return accessTokenResponse{}, err
	}
	var broker accessBrokerRow
	for _, row := range listed.Brokers {
		if row.Enabled && row.Transport == "mqtt" {
			broker = row
			break
		}
	}
	if broker.BrokerID == "" {
		return accessTokenResponse{}, fmt.Errorf("no enabled access broker")
	}
	if err := t.accountJSON(ctx, http.MethodPost, base+"/access-channel/enroll", map[string]string{
		"brokerId": broker.BrokerID,
		"deviceId": t.cfg.DeviceID,
	}, nil); err != nil {
		return accessTokenResponse{}, fmt.Errorf("enroll device: %w", err)
	}
	var grant accessTokenResponse
	if err := t.accountJSON(ctx, http.MethodPost, base+"/access-channel/token", map[string]string{
		"brokerId": broker.BrokerID,
		"deviceId": t.cfg.DeviceID,
		"role":     "device",
	}, &grant); err != nil {
		return accessTokenResponse{}, fmt.Errorf("mint MQTT credential: %w", err)
	}
	if !grant.OK || grant.Endpoint == "" || grant.Password == "" || grant.TopicRoot == "" {
		return accessTokenResponse{}, fmt.Errorf("incomplete MQTT grant")
	}
	return grant, nil
}

func accessAccountJSON(ctx context.Context, cfg *Config, method, url string, body interface{}, out interface{}) error {
	return accessJSON(ctx, cfg.AuthToken, method, url, body, out)
}

func accessPublicJSON(ctx context.Context, method, url string, body interface{}, out interface{}) error {
	return accessJSON(ctx, "", method, url, body, out)
}

func accessJSON(ctx context.Context, bearer, method, url string, body interface{}, out interface{}) error {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(raw)
	}
	reqCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, method, url, reader)
	if err != nil {
		return err
	}
	if strings.TrimSpace(bearer) != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(msg)))
	}
	if out == nil {
		io.Copy(io.Discard, resp.Body)
		return nil
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(out)
}

func (t *mqttAccessTransport) accountJSON(ctx context.Context, method, url string, body interface{}, out interface{}) error {
	return accessAccountJSON(ctx, t.cfg, method, url, body, out)
}

func (t *mqttAccessTransport) connect(ctx context.Context, grant accessTokenResponse) (mqtt.Client, error) {
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12}
	if grant.CAFingerprint != nil && strings.TrimSpace(*grant.CAFingerprint) != "" {
		want, err := parseCertFingerprint(*grant.CAFingerprint)
		if err != nil {
			return nil, err
		}
		tlsConfig.VerifyConnection = func(state tls.ConnectionState) error {
			for _, chain := range state.VerifiedChains {
				for _, cert := range chain {
					sum := sha256.Sum256(cert.Raw)
					if bytes.Equal(sum[:], want) {
						return nil
					}
				}
			}
			return fmt.Errorf("access broker certificate chain does not match registered fingerprint")
		}
	}
	opts := mqtt.NewClientOptions().
		AddBroker(grant.Endpoint).
		SetClientID(grant.ClientID).
		SetUsername(grant.Username).
		SetPassword(grant.Password).
		SetTLSConfig(tlsConfig).
		SetCleanSession(true).
		SetAutoReconnect(true).
		SetConnectRetry(true).
		SetConnectRetryInterval(5 * time.Second).
		SetKeepAlive(30 * time.Second).
		SetPingTimeout(10 * time.Second)
	client := mqtt.NewClient(opts)
	mqttAccessRoots.Store(client, grant.TopicRoot)
	connectToken := client.Connect()
	if !connectToken.WaitTimeout(20 * time.Second) {
		mqttAccessRoots.Delete(client)
		return nil, fmt.Errorf("MQTT connect timed out")
	}
	if err := connectToken.Error(); err != nil {
		mqttAccessRoots.Delete(client)
		return nil, err
	}
	topic := grant.TopicRoot + "/device/" + t.cfg.DeviceID + "/requests/#"
	subToken := client.Subscribe(topic, 1, func(_ mqtt.Client, msg mqtt.Message) {
		t.receive(grant.TopicRoot, msg.Topic(), msg.Payload())
	})
	if !subToken.WaitTimeout(10*time.Second) || subToken.Error() != nil {
		client.Disconnect(100)
		mqttAccessRoots.Delete(client)
		if subToken.Error() != nil {
			return nil, subToken.Error()
		}
		return nil, fmt.Errorf("MQTT subscribe timed out")
	}
	log.Printf("[mqtt-access] connected to owner broker; credential expires in %s", time.Until(time.UnixMilli(grant.ExpiresAt)).Round(time.Second))
	return client, nil
}

func (t *mqttAccessTransport) receive(root, topic string, payload []byte) {
	prefix := root + "/device/" + t.cfg.DeviceID + "/requests/"
	if !strings.HasPrefix(topic, prefix) {
		return
	}
	action := strings.TrimPrefix(topic, prefix)
	if strings.Contains(action, "/") || action == "" {
		return
	}
	var signal accessSignal
	if err := json.Unmarshal(payload, &signal); err != nil {
		return
	}
	t.b.ReceiveAccess(BusEvent{
		ID:          signal.RequestID,
		Topic:       accessTopicPrefix + t.cfg.DeviceID + "/requests/" + action,
		Publisher:   signal.IssuerDeviceID,
		PublishedAt: signal.IssuedAt,
		QoS:         1,
		Payload:     append(json.RawMessage(nil), payload...),
	})
}

func parseCertFingerprint(value string) ([]byte, error) {
	clean := strings.ReplaceAll(strings.TrimSpace(value), ":", "")
	raw, err := hex.DecodeString(clean)
	if err != nil || len(raw) != sha256.Size {
		return nil, fmt.Errorf("invalid SHA-256 broker certificate fingerprint")
	}
	return raw, nil
}
