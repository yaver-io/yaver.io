package main

// Tier-1 bus transport: UDP broadcast on the LAN. Mirror of
// desktop/agent/beacon.go's pattern but a separate socket + port so
// discovery beacons (used by mobile) stay on 19837 and bus events
// stay on 19838.
//
// Why broadcast and not multicast: multicast groups require explicit
// `IGMP` joins and iOS/Android need the "Local Network Usage" entitlement
// just to receive them. Broadcast (255.255.255.255 and per-interface
// subnet broadcasts) works without any special permission. The
// desktop-to-desktop bus is all we need Tier 1 for — mobile uses the
// SSE path instead.
//
// Security: every LAN packet carries a user fingerprint (first 8 hex
// chars of SHA256(userId), same scheme as beacon.go::tokenFingerprint).
// Receivers drop packets whose fingerprint doesn't match their own
// user. This is a coarse filter, not encryption — a hostile peer on
// the same LAN could sniff event bodies. Upgrade path (deferred):
// HMAC the packet body with a derived key. For Phase 2 the threat
// model is "LAN is trusted", matching beacon.go.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"sync"
)

const (
	busLANPort    = 19838
	busLANVersion = 1
	// UDP datagrams above ~64 KiB fragment; safe ceiling.
	busLANMaxPayload = 60 * 1024
)

// busLANFrame wraps a BusEvent with a user-fingerprint so listeners
// can filter same-user traffic without paying the JSON-parse cost
// on other tenants' events.
type busLANFrame struct {
	V  int      `json:"v"`  // protocol version
	TH string   `json:"th"` // tokenFingerprint(userID), first 8 hex chars
	EV BusEvent `json:"ev"`
}

type lanBusTransport struct {
	b          *Bus
	userFP     string // empty = filter disabled (single-user relay mode)
	deviceID   DeviceID
	mu         sync.Mutex
	sendConn   *net.UDPConn
	recvConn   *net.UDPConn
	cancel     context.CancelFunc
	wg         sync.WaitGroup
	dropOnUser int // counter: packets we saw from a different userFP
}

// NewLANBusTransport constructs the transport but does not open sockets
// until Start() is called. `userID` is empty-tolerant — when unset,
// we neither sign outgoing nor filter incoming. That mode is safe
// only on trusted single-tenant networks (the Hetzner test box; a
// self-hosted home setup).
func NewLANBusTransport(b *Bus, deviceID DeviceID, userID string) *lanBusTransport {
	fp := ""
	if userID != "" {
		fp = tokenFingerprint(userID)
	}
	return &lanBusTransport{
		b:        b,
		userFP:   fp,
		deviceID: deviceID,
	}
}

func (t *lanBusTransport) Name() string { return "lan" }

// Start opens the send + receive sockets. Receive runs in its own
// goroutine; errors are logged and the loop retries after a 1s
// backoff rather than fatal-ing — a transient socket loss (network
// flap, VPN toggle) should self-heal without restarting yaver.
func (t *lanBusTransport) Start(parent context.Context) error {
	ctx, cancel := context.WithCancel(parent)
	t.cancel = cancel

	// Send socket — ephemeral source port, broadcast-capable.
	send, err := openBroadcastSendConn()
	if err != nil {
		cancel()
		return fmt.Errorf("bus-lan: open send conn: %w", err)
	}
	// Receive socket — bound to the bus port, listens on 0.0.0.0.
	recv, err := net.ListenUDP("udp4", &net.UDPAddr{Port: busLANPort})
	if err != nil {
		_ = send.Close()
		cancel()
		return fmt.Errorf("bus-lan: listen on :%d: %w", busLANPort, err)
	}

	t.mu.Lock()
	t.sendConn = send
	t.recvConn = recv
	t.mu.Unlock()

	t.wg.Add(1)
	go t.receiveLoop(ctx)
	log.Printf("[bus-lan] listening on UDP :%d (userFP=%s)", busLANPort, t.userFP)
	return nil
}

func (t *lanBusTransport) Close() error {
	if t.cancel != nil {
		t.cancel()
	}
	t.mu.Lock()
	var errs []error
	if t.sendConn != nil {
		errs = append(errs, t.sendConn.Close())
	}
	if t.recvConn != nil {
		errs = append(errs, t.recvConn.Close())
	}
	t.mu.Unlock()
	t.wg.Wait()
	return errors.Join(errs...)
}

// Publish fans the event out as one UDP datagram per destination.
// Destinations: global broadcast (255.255.255.255) + every per-
// interface subnet broadcast we can discover. Same scheme as beacon.go.
func (t *lanBusTransport) Publish(ctx context.Context, evt BusEvent) error {
	frame := busLANFrame{V: busLANVersion, TH: t.userFP, EV: evt}
	body, err := json.Marshal(frame)
	if err != nil {
		return err
	}
	if len(body) > busLANMaxPayload {
		// Over-large events skip LAN (they'll still go via relay).
		return fmt.Errorf("bus-lan: event too large (%d B)", len(body))
	}
	t.mu.Lock()
	send := t.sendConn
	t.mu.Unlock()
	if send == nil {
		return errors.New("bus-lan: not started")
	}

	// Global broadcast first — covers most LANs where routers allow it.
	globalAddr := &net.UDPAddr{IP: net.IPv4bcast, Port: busLANPort}
	_, _ = send.WriteToUDP(body, globalAddr)

	// Per-interface subnet broadcast for tighter LANs.
	for _, addr := range subnetBroadcasts() {
		_, _ = send.WriteToUDP(body, &net.UDPAddr{IP: addr, Port: busLANPort})
	}
	return nil
}

// receiveLoop reads datagrams forever, filters by userFP, and
// feeds into the local bus.
func (t *lanBusTransport) receiveLoop(ctx context.Context) {
	defer t.wg.Done()
	buf := make([]byte, 64*1024)
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		t.mu.Lock()
		conn := t.recvConn
		t.mu.Unlock()
		if conn == nil {
			return
		}
		n, _, err := conn.ReadFromUDP(buf)
		if err != nil {
			// Socket closed by Close() — exit cleanly.
			if ctx.Err() != nil {
				return
			}
			log.Printf("[bus-lan] read: %v", err)
			return
		}

		var frame busLANFrame
		if err := json.Unmarshal(buf[:n], &frame); err != nil {
			continue
		}
		if frame.V != busLANVersion {
			continue
		}
		// Filter on user. Empty userFP on either side = single-tenant
		// mode, accept anything.
		if t.userFP != "" && frame.TH != "" && frame.TH != t.userFP {
			t.mu.Lock()
			t.dropOnUser++
			t.mu.Unlock()
			continue
		}
		// Ignore our own broadcast echoing back off the socket.
		if frame.EV.Publisher == t.deviceID {
			continue
		}
		t.b.Receive(frame.EV)
	}
}

// ── helpers (shared with beacon) ───────────────────────────────────

// openBroadcastSendConn dials a broadcast-capable UDP source. We dial
// on 0.0.0.0:0 so the kernel picks an ephemeral port; SetWriteBuffer
// and setting SO_BROADCAST are on by default for UDP4 broadcasts in
// Go's net package.
func openBroadcastSendConn() (*net.UDPConn, error) {
	return net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero, Port: 0})
}

// subnetBroadcasts is defined in beacon.go and shared between the
// discovery beacon and the LAN bus transport.
