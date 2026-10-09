package main

// access_channel.go defines the deliberately tiny payload grammar accepted from
// an MQTT/Yaver access rendezvous transport. It is not a generic message bus:
// private data and arbitrary commands cannot be represented. Transport auth and
// per-tenant topic ACLs are still mandatory; this guard is defense in depth for
// malformed, replayed, expired, or spammed signals that pass the broker.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"regexp"
	"strings"
	"sync"
	"time"
)

const (
	accessTopicPrefix     = "access/v1/device/"
	accessSignalMaxBytes  = 2048
	accessSignalMaxFuture = 2 * time.Minute
	accessSignalMaxLife   = 10 * time.Minute
)

var accessIDPattern = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,128}$`)

type accessSignal struct {
	Version        int    `json:"version"`
	Kind           string `json:"kind"`
	RequestID      string `json:"requestId"`
	TargetDeviceID string `json:"targetDeviceId"`
	IssuerDeviceID string `json:"issuerDeviceId"`
	IssuedAt       int64  `json:"issuedAt"`
	ExpiresAt      int64  `json:"expiresAt"`
	Nonce          string `json:"nonce"`
	// Provider is an enum, never a URL, token, account name, or arbitrary
	// string. v1 intentionally supports only Yaver-account authorization.
	Provider string `json:"provider,omitempty"`
}

type accessSignalGuard struct {
	mu       sync.Mutex
	now      func() time.Time
	nonces   map[string]time.Time
	windows  map[string][]time.Time
	inflight map[string]time.Time
}

func newAccessSignalGuard() *accessSignalGuard {
	return &accessSignalGuard{
		now:      time.Now,
		nonces:   make(map[string]time.Time),
		windows:  make(map[string][]time.Time),
		inflight: make(map[string]time.Time),
	}
}

func isAccessTopic(topic string) bool { return strings.HasPrefix(topic, accessTopicPrefix) }

func canonicalAccessDeviceProof(deviceID string, timestamp int64, nonce string) string {
	return fmt.Sprintf("yaver-access-device-v1\n%s\n%d\n%s", deviceID, timestamp, nonce)
}

// startAccessRequestConsumer turns the tiny control-plane verbs into local,
// deterministic actions. `auth` starts the existing one-tap Yaver device-code
// self-nomination; the code itself stays on the established Convex approval
// path and never enters the Access Channel. Wake/access requests only prove
// presence. No arbitrary command can be represented here.
func startAccessRequestConsumer(ctx context.Context, b *Bus, cfg *Config, server *HTTPServer) {
	if b == nil || cfg == nil || strings.TrimSpace(cfg.DeviceID) == "" {
		return
	}
	prefix := accessTopicPrefix + cfg.DeviceID + "/requests"
	unsubscribe := b.Subscribe(prefix, func(evt BusEvent) {
		var request accessSignal
		if err := json.Unmarshal(evt.Payload, &request); err != nil {
			return
		}
		_, _, action, ok := accessTopicParts(evt.Topic)
		if !ok {
			return
		}
		if action == "auth" && request.Provider == "yaver" {
			base := strings.TrimRight(strings.TrimSpace(cfg.ConvexSiteURL), "/")
			if base == "" {
				base = defaultConvexSiteURL
			}
			beginSelfNomination(base, server)
		}
		responseKind := "events." + action
		if action == "wake" {
			responseKind = "events.presence"
		}
		now := time.Now()
		response := accessSignal{
			Version:        1,
			Kind:           responseKind,
			RequestID:      request.RequestID,
			TargetDeviceID: cfg.DeviceID,
			IssuerDeviceID: cfg.DeviceID,
			IssuedAt:       now.UnixMilli(),
			ExpiresAt:      now.Add(2 * time.Minute).UnixMilli(),
			Nonce:          newSigNonce(),
			Provider:       request.Provider,
		}
		go func() {
			if _, err := b.Publish(ctx, accessTopicPrefix+cfg.DeviceID+"/events/"+strings.TrimPrefix(responseKind, "events."), response, 0, 1); err != nil {
				log.Printf("[access] response publish failed: %v", err)
			}
		}()
	})
	go func() {
		<-ctx.Done()
		unsubscribe()
	}()
}

func accessTopicParts(topic string) (deviceID, direction, action string, ok bool) {
	parts := strings.Split(strings.TrimPrefix(topic, accessTopicPrefix), "/")
	if len(parts) != 3 || !accessIDPattern.MatchString(parts[0]) {
		return "", "", "", false
	}
	if parts[1] != "requests" && parts[1] != "events" {
		return "", "", "", false
	}
	allowed := map[string]bool{
		"requests/access": true,
		"requests/auth":   true,
		"requests/wake":   true,
		"events/presence": true,
		"events/auth":     true,
		"events/access":   true,
		"events/result":   true,
	}
	if !allowed[parts[1]+"/"+parts[2]] {
		return "", "", "", false
	}
	return parts[0], parts[1], parts[2], true
}

func decodeAccessSignal(raw json.RawMessage) (accessSignal, error) {
	if len(raw) == 0 || len(raw) > accessSignalMaxBytes {
		return accessSignal{}, fmt.Errorf("access signal payload must be 1..%d bytes", accessSignalMaxBytes)
	}
	var sig accessSignal
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&sig); err != nil {
		return accessSignal{}, fmt.Errorf("invalid access signal: %w", err)
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return accessSignal{}, errors.New("access signal must contain exactly one object")
	}
	return sig, nil
}

func (g *accessSignalGuard) Accept(evt BusEvent) error {
	target, direction, action, ok := accessTopicParts(evt.Topic)
	if !ok {
		return errors.New("invalid access topic")
	}
	sig, err := decodeAccessSignal(evt.Payload)
	if err != nil {
		return err
	}
	if sig.Version != 1 || sig.TargetDeviceID != target || !accessIDPattern.MatchString(sig.IssuerDeviceID) ||
		!accessIDPattern.MatchString(sig.RequestID) || !accessIDPattern.MatchString(sig.Nonce) {
		return errors.New("access signal identity fields are invalid")
	}
	wantKind := direction + "." + action
	if sig.Kind != wantKind {
		return fmt.Errorf("access signal kind %q does not match topic", sig.Kind)
	}
	if sig.Provider != "" && sig.Provider != "yaver" {
		return errors.New("unsupported access auth provider")
	}
	now := g.now()
	issued := time.UnixMilli(sig.IssuedAt)
	expires := time.UnixMilli(sig.ExpiresAt)
	if issued.After(now.Add(accessSignalMaxFuture)) || expires.Before(now) || !expires.After(issued) || expires.Sub(issued) > accessSignalMaxLife {
		return errors.New("access signal is expired or outside its allowed lifetime")
	}

	g.mu.Lock()
	defer g.mu.Unlock()
	for nonce, expiry := range g.nonces {
		if !expiry.After(now) {
			delete(g.nonces, nonce)
		}
	}
	for key, expiry := range g.inflight {
		if !expiry.After(now) {
			delete(g.inflight, key)
		}
	}
	if _, replay := g.nonces[sig.Nonce]; replay {
		return errors.New("access signal replay rejected")
	}

	rateKey := sig.IssuerDeviceID + "|" + sig.TargetDeviceID + "|" + sig.Kind
	window := g.windows[rateKey][:0]
	for _, at := range g.windows[rateKey] {
		if at.After(now.Add(-time.Minute)) {
			window = append(window, at)
		}
	}
	limit := 20
	if sig.Kind == "requests.auth" {
		limit = 1
		inflightKey := sig.TargetDeviceID + "|" + sig.Provider
		if expiry, exists := g.inflight[inflightKey]; exists && expiry.After(now) {
			return errors.New("an OAuth request is already active for this device")
		}
		g.inflight[inflightKey] = expires
	}
	if len(window) >= limit {
		return errors.New("access signal rate limit exceeded")
	}
	g.windows[rateKey] = append(window, now)
	g.nonces[sig.Nonce] = expires
	return nil
}
