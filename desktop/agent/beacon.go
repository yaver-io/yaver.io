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

	conn, err := net.DialUDP("udp4", nil, addr)
	if err != nil {
		log.Printf("[beacon] Failed to create UDP socket: %v", err)
		return
	}
	defer conn.Close()

	log.Printf("[beacon] Broadcasting on UDP port %d every %s (id=%s, th=%s)", beaconPort, beaconInterval, shortID, fp)

	ticker := time.NewTicker(beaconInterval)
	defer ticker.Stop()

	// Send immediately on start
	if _, err := conn.Write(data); err != nil {
		log.Printf("[beacon] Send error: %v", err)
	}

	for {
		select {
		case <-ctx.Done():
			log.Println("[beacon] Stopped.")
			return
		case <-ticker.C:
			if _, err := conn.Write(data); err != nil {
				log.Printf("[beacon] Send error: %v", err)
			}
		}
	}
}
