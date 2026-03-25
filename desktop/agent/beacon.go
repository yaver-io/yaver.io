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
	}
	data, err := json.Marshal(payload)
	if err != nil {
		log.Printf("[beacon] Failed to marshal payload: %v", err)
		return
	}

	addr := &net.UDPAddr{
		IP:   net.IPv4bcast, // 255.255.255.255
		Port: beaconPort,
	}

	log.Printf("[beacon] Broadcasting on UDP port %d every %s (id=%s, th=%s)", beaconPort, beaconInterval, shortID, fp)

	ticker := time.NewTicker(beaconInterval)
	defer ticker.Stop()

	var conn *net.UDPConn
	lastErr := ""
	consecutiveErrors := 0

	dial := func() *net.UDPConn {
		c, err := net.DialUDP("udp4", nil, addr)
		if err != nil {
			if err.Error() != lastErr {
				log.Printf("[beacon] UDP socket error: %v (will retry silently)", err)
				lastErr = err.Error()
			}
			return nil
		}
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
			if _, err := conn.Write(data); err != nil {
				consecutiveErrors++
				if consecutiveErrors == 1 {
					log.Printf("[beacon] Send error: %v (suppressing further)", err)
				}
				// Rebind after 5 consecutive failures (network likely changed)
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
