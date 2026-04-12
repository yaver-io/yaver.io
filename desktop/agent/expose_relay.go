package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"regexp"
	"sync"
	"time"

	"github.com/quic-go/quic-go"
)

// RelayExposeEntry represents a subdomain registered through the QUIC relay.
type RelayExposeEntry struct {
	Subdomain string    `json:"subdomain"`
	Port      int       `json:"port"`
	PublicURL string    `json:"publicUrl"`
	CreatedAt time.Time `json:"createdAt"`
}

// RelayExposeManager manages relay-based subdomain expose registrations.
// Unlike ExposeManager (which uses cloudflared/bore/SSH), this uses the Yaver QUIC relay.
type RelayExposeManager struct {
	mu       sync.Mutex
	entries  map[string]*RelayExposeEntry // subdomain -> entry
	conn     quic.Connection
	deviceID string
}

// NewRelayExposeManager creates a new RelayExposeManager.
func NewRelayExposeManager() *RelayExposeManager {
	return &RelayExposeManager{
		entries: make(map[string]*RelayExposeEntry),
	}
}

var relaySubdomainRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,30}[a-z0-9]$`)

func validateRelaySubdomain(s string) error {
	if len(s) < 3 || len(s) > 32 {
		return fmt.Errorf("subdomain must be 3-32 characters")
	}
	if !relaySubdomainRe.MatchString(s) {
		return fmt.Errorf("subdomain must be lowercase alphanumeric and hyphens, cannot start/end with hyphen")
	}
	return nil
}

// SetConn updates the QUIC connection and re-registers all active entries.
// Called after each successful relay registration.
func (m *RelayExposeManager) SetConn(conn quic.Connection, deviceID string) {
	m.mu.Lock()
	m.conn = conn
	m.deviceID = deviceID
	entries := make([]*RelayExposeEntry, 0, len(m.entries))
	for _, e := range m.entries {
		entries = append(entries, e)
	}
	m.mu.Unlock()

	// Re-register all entries on the new connection
	for _, e := range entries {
		go func(entry *RelayExposeEntry) {
			resp, err := sendRelayExposeRegister(conn, deviceID, entry.Subdomain, entry.Port)
			if err != nil {
				log.Printf("[EXPOSE] Re-register %s failed: %v", entry.Subdomain, err)
				return
			}
			if resp.OK {
				log.Printf("[EXPOSE] Re-registered %s → %s", entry.Subdomain, resp.PublicURL)
				m.mu.Lock()
				entry.PublicURL = resp.PublicURL
				m.mu.Unlock()
			} else {
				log.Printf("[EXPOSE] Re-register %s rejected: %s", entry.Subdomain, resp.Message)
			}
		}(e)
	}
}

// Register registers a new subdomain with the relay.
func (m *RelayExposeManager) Register(subdomain string, port int) (*RelayExposeEntry, error) {
	if err := validateRelaySubdomain(subdomain); err != nil {
		return nil, err
	}

	m.mu.Lock()
	conn := m.conn
	deviceID := m.deviceID
	m.mu.Unlock()

	if conn == nil {
		return nil, fmt.Errorf("not connected to relay — start agent with relay support first")
	}

	resp, err := sendRelayExposeRegister(conn, deviceID, subdomain, port)
	if err != nil {
		return nil, fmt.Errorf("relay error: %w", err)
	}
	if !resp.OK {
		return nil, fmt.Errorf("relay rejected: %s", resp.Message)
	}

	entry := &RelayExposeEntry{
		Subdomain: subdomain,
		Port:      port,
		PublicURL: resp.PublicURL,
		CreatedAt: time.Now(),
	}

	m.mu.Lock()
	m.entries[subdomain] = entry
	m.mu.Unlock()

	log.Printf("[EXPOSE] Registered %s → port %d (%s)", subdomain, port, resp.PublicURL)
	return entry, nil
}

// Unregister removes a subdomain registration.
func (m *RelayExposeManager) Unregister(subdomain string) error {
	m.mu.Lock()
	conn := m.conn
	deviceID := m.deviceID
	delete(m.entries, subdomain)
	m.mu.Unlock()

	if conn != nil {
		sendRelayExposeUnregister(conn, deviceID, subdomain)
	}
	log.Printf("[EXPOSE] Unregistered %s", subdomain)
	return nil
}

// UnregisterAll removes all subdomain registrations.
func (m *RelayExposeManager) UnregisterAll() {
	m.mu.Lock()
	conn := m.conn
	deviceID := m.deviceID
	subs := make([]string, 0, len(m.entries))
	for sub := range m.entries {
		subs = append(subs, sub)
	}
	m.entries = make(map[string]*RelayExposeEntry)
	m.mu.Unlock()

	if conn != nil {
		for _, sub := range subs {
			sendRelayExposeUnregister(conn, deviceID, sub)
		}
	}
}

// List returns a snapshot of all active relay expose entries.
func (m *RelayExposeManager) List() []*RelayExposeEntry {
	m.mu.Lock()
	defer m.mu.Unlock()
	list := make([]*RelayExposeEntry, 0, len(m.entries))
	for _, e := range m.entries {
		list = append(list, e)
	}
	return list
}

// --- QUIC protocol helpers ---

type relayExposeRegisterMsg struct {
	Type      string `json:"type"`
	DeviceID  string `json:"deviceId"`
	Subdomain string `json:"subdomain"`
	Port      int    `json:"port"`
}

type relayExposeRegisterResp struct {
	Type      string `json:"type"`
	OK        bool   `json:"ok"`
	PublicURL string `json:"publicUrl,omitempty"`
	Message   string `json:"message,omitempty"`
}

type relayExposeUnregisterMsg struct {
	Type      string `json:"type"`
	DeviceID  string `json:"deviceId"`
	Subdomain string `json:"subdomain"`
}

func sendRelayExposeRegister(conn quic.Connection, deviceID, subdomain string, port int) (*relayExposeRegisterResp, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	stream, err := conn.OpenStreamSync(ctx)
	if err != nil {
		return nil, fmt.Errorf("open stream: %w", err)
	}
	defer stream.Close()

	msg := relayExposeRegisterMsg{
		Type:      "expose_register",
		DeviceID:  deviceID,
		Subdomain: subdomain,
		Port:      port,
	}
	data, _ := json.Marshal(msg)
	if _, err := stream.Write(data); err != nil {
		return nil, fmt.Errorf("write: %w", err)
	}
	stream.Close()

	respData, err := io.ReadAll(io.LimitReader(stream, 1<<16))
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	var resp relayExposeRegisterResp
	if err := json.Unmarshal(respData, &resp); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}

	return &resp, nil
}

func sendRelayExposeUnregister(conn quic.Connection, deviceID, subdomain string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	stream, err := conn.OpenStreamSync(ctx)
	if err != nil {
		return
	}
	defer stream.Close()

	msg := relayExposeUnregisterMsg{
		Type:      "expose_unregister",
		DeviceID:  deviceID,
		Subdomain: subdomain,
	}
	data, _ := json.Marshal(msg)
	stream.Write(data)
}
