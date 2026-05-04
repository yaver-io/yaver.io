package main

// turn.go — Pion TURN/STUN server colocated with the QUIC + HTTP
// listeners. The whole reason it exists: a browser viewer behind
// CG-NAT can't reach a Linux dev box behind another CG-NAT directly.
// ICE will look for a TURN candidate; this is the one the agent
// hands the viewer.
//
// Auth uses the long-term-credential mechanism keyed off the same
// shared secret the relay already enforces (RELAY_PASSWORD by
// default). No new key material is distributed; if a self-hoster
// rotates RELAY_PASSWORD, TURN follows automatically.
//
// We bind UDP only (TCP/TLS-TURN is a future stretch). 3478 is the
// IANA-assigned port; production deployments should expose it the
// same way they expose 4433 for QUIC.

import (
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"strconv"

	"github.com/pion/logging"
	"github.com/pion/turn/v4"
)

// StartTURN runs the TURN/STUN server until ctx is cancelled, then
// closes the listener cleanly. publicIP is the address the relay
// reports as its TURN candidate — clients open allocations against
// it, so it must be the box's WAN-reachable address (not 127.0.0.1).
// realm is the long-term-credential realm; "yaver-relay" is the
// default and shows up in browser devtools.
//
// authSecret is the shared secret used to derive each session's
// short-lived TURN password (see Pion's GenerateLongTermCredentials
// for the algorithm). Defaults to RELAY_PASSWORD via env var, but
// can be overridden with TURN_AUTH_SECRET so an operator can rotate
// TURN creds independently from the relay HTTP password.
func StartTURN(ctx context.Context, publicIP, realm string, port int, authSecret string) error {
	if authSecret == "" {
		return fmt.Errorf("turn: authSecret cannot be empty")
	}
	if publicIP == "" {
		return fmt.Errorf("turn: publicIP cannot be empty (relay needs a WAN-reachable IP for TURN candidates)")
	}
	if port <= 0 || port > 65535 {
		return fmt.Errorf("turn: port %d out of range", port)
	}
	if realm == "" {
		realm = "yaver-relay"
	}

	udpListener, err := net.ListenPacket("udp4", "0.0.0.0:"+strconv.Itoa(port))
	if err != nil {
		return fmt.Errorf("turn: bind UDP %d: %w", port, err)
	}

	// Pion's leveled logger writes to stdout by default. We pipe its
	// output through the same writer the rest of the relay uses so a
	// single tail -f shows everything.
	logger := logging.NewDefaultLeveledLoggerForScope(
		"yaver-turn",
		logging.LogLevelInfo,
		os.Stderr,
	)

	server, err := turn.NewServer(turn.ServerConfig{
		Realm:       realm,
		AuthHandler: turn.NewLongTermAuthHandler(authSecret, logger),
		PacketConnConfigs: []turn.PacketConnConfig{
			{
				PacketConn: udpListener,
				RelayAddressGenerator: &turn.RelayAddressGeneratorStatic{
					RelayAddress: net.ParseIP(publicIP),
					Address:      "0.0.0.0",
				},
			},
		},
	})
	if err != nil {
		_ = udpListener.Close()
		return fmt.Errorf("turn: create server: %w", err)
	}
	log.Printf("  TURN server:      udp/%d (realm=%s, public-ip=%s)", port, realm, publicIP)

	<-ctx.Done()
	if err := server.Close(); err != nil {
		log.Printf("  TURN server close: %v", err)
	}
	return nil
}
