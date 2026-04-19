package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"time"
)

const (
	beaconPort     = 19837
	beaconInterval = 3 * time.Second
	beaconVersion  = 1
)

// beaconPayload is the JSON broadcast on the LAN.
type beaconPayload struct {
	Version          int    `json:"v"`
	DeviceID         string `json:"id"`            // first 8 chars
	Port             int    `json:"p"`
	Name             string `json:"n"`
	TokenFingerprint string `json:"th"`            // first 8 chars of SHA256(userId)
	VoiceCapable     bool   `json:"vc,omitempty"`  // true if voice transcription is available
	TLSFingerprint   string `json:"tf,omitempty"`  // SHA256 of TLS cert
	TLSPort          int    `json:"tp,omitempty"`  // HTTPS port
	HardwareID       string `json:"hw,omitempty"`  // stable hardware identifier (P2P only, never sent to Convex)
	// Bootstrap mode fields — only set while the box is running
	// `yaver serve` with no auth token.
	NeedsAuth        bool   `json:"na,omitempty"`  // true = waiting for a token via /auth/pair/submit
	BootstrapPasskey string `json:"pk,omitempty"`  // 6-char pairing passkey (LAN-trust model; empty if suppressed)
	DevicePublicKey  string `json:"dpk,omitempty"` // X25519 public key (base64) for encrypted pairing
}

// tokenFingerprint returns the first 8 hex chars of SHA256(userId).
// Both CLI and mobile compute the same value for the same user,
// letting the mobile filter beacons to same-user devices.
func tokenFingerprint(userID string) string {
	h := sha256.Sum256([]byte(userID))
	return fmt.Sprintf("%x", h[:4]) // 4 bytes = 8 hex chars
}

// startBeacon broadcasts a UDP beacon every 3 seconds so mobile apps
// on the same network can discover this agent instantly.
// It returns when ctx is cancelled.
func startBeacon(ctx context.Context, deviceID string, httpPort int, hostname string, userID string) {
	fp := tokenFingerprint(userID)
	shortID := deviceID
	if len(shortID) > 8 {
		shortID = shortID[:8]
	}

	payload := beaconPayload{
		Version:          beaconVersion,
		DeviceID:         shortID,
		Port:             httpPort,
		Name:             hostname,
		TokenFingerprint: fp,
		HardwareID:       HardwareID(),
	}
	data, err := json.Marshal(payload)
	if err != nil {
		log.Printf("[beacon] Failed to marshal payload: %v", err)
		return
	}

	// A listening UDP socket lets us send to multiple destinations
	// (global broadcast plus every per-interface subnet broadcast).
	// Many routers drop 255.255.255.255 but still forward subnet-directed
	// broadcasts (e.g. 192.168.1.255) so we fan out to both.
	globalAddr := &net.UDPAddr{IP: net.IPv4bcast, Port: beaconPort}

	log.Printf("[beacon] Broadcasting on UDP port %d every %s (id=%s, th=%s)", beaconPort, beaconInterval, shortID, fp)

	ticker := time.NewTicker(beaconInterval)
	defer ticker.Stop()

	var conn *net.UDPConn
	lastErr := ""
	consecutiveErrors := 0

	dial := func() *net.UDPConn {
		c, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero, Port: 0})
		if err != nil {
			if err.Error() != lastErr {
				log.Printf("[beacon] UDP socket error: %v (will retry silently)", err)
				lastErr = err.Error()
			}
			return nil
		}
		// Enable SO_BROADCAST via net package semantics (ListenUDP on ipv4 allows broadcast dst).
		if lastErr != "" {
			log.Printf("[beacon] UDP socket recovered")
			lastErr = ""
			consecutiveErrors = 0
		}
		return c
	}

	conn = dial()

	for {
		select {
		case <-ctx.Done():
			if conn != nil {
				conn.Close()
			}
			log.Println("[beacon] Stopped.")
			return
		case <-ticker.C:
			if conn == nil {
				conn = dial()
				if conn == nil {
					continue
				}
			}
			sendErr := sendBeacon(conn, data, globalAddr)
			if sendErr != nil {
				consecutiveErrors++
				if consecutiveErrors == 1 {
					log.Printf("[beacon] Send error: %v (suppressing further)", sendErr)
				}
				if consecutiveErrors >= 5 {
					conn.Close()
					conn = nil
					conn = dial()
					consecutiveErrors = 0
				}
			} else {
				if consecutiveErrors > 0 {
					log.Printf("[beacon] Send recovered after %d errors", consecutiveErrors)
				}
				consecutiveErrors = 0
				lastErr = ""
			}
		}
	}
}

// sendBeacon writes the payload to 255.255.255.255 plus every per-interface
// subnet-directed broadcast address (192.168.1.255, 10.0.0.255, …). Returning
// nil means at least one send succeeded; an error means every destination
// failed so the caller can rebuild the socket.
func sendBeacon(conn *net.UDPConn, data []byte, global *net.UDPAddr) error {
	var firstErr error
	anyOK := false
	if _, err := conn.WriteToUDP(data, global); err != nil {
		firstErr = err
	} else {
		anyOK = true
	}
	for _, b := range subnetBroadcasts() {
		addr := &net.UDPAddr{IP: b, Port: beaconPort}
		if _, err := conn.WriteToUDP(data, addr); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		anyOK = true
	}
	if anyOK {
		return nil
	}
	return firstErr
}

// subnetBroadcasts returns the broadcast address for every active, non-loopback
// IPv4 interface. Cached per tick; cheap to recompute.
func subnetBroadcasts() []net.IP {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	var out []net.IP
	for _, ifi := range ifaces {
		if ifi.Flags&net.FlagUp == 0 || ifi.Flags&net.FlagLoopback != 0 {
			continue
		}
		if ifi.Flags&net.FlagBroadcast == 0 {
			continue
		}
		addrs, err := ifi.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			ipNet, ok := a.(*net.IPNet)
			if !ok {
				continue
			}
			ip := ipNet.IP.To4()
			if ip == nil || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
				continue
			}
			mask := ipNet.Mask
			if len(mask) != 4 {
				continue
			}
			bcast := make(net.IP, 4)
			for i := 0; i < 4; i++ {
				bcast[i] = ip[i] | ^mask[i]
			}
			// Skip /32 interfaces (Tailscale, VPN) — bcast == ip, pointless.
			if bcast.Equal(ip) {
				continue
			}
			out = append(out, bcast)
		}
	}
	return out
}
