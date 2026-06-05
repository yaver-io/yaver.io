package main

// derp.go — meshtest "derp" mode: bring up a mesh node whose peer is reachable
// ONLY through the relay (no direct UDP path). Proves the relay-as-DERP fallback
// for symmetric NAT: the node dials the relay, registers, opens a persistent
// mesh_relay stream, and the mesh.Manager substitutes a loopback DERP shim for
// the peer (which has an empty direct endpoint) so WireGuard frames travel
// agent -> relay -> agent. Run two of these in two namespaces that can each
// reach the relay but NOT each other, then ping over the overlay.
//
//   meshtest derp <ifname> <selfIPv4> <privB64> <selfDeviceID> \
//                 <peerPubB64> <peerDeviceID> <peerIPv4> <relayAddr> <password>

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/quic-go/quic-go"
	"github.com/yaver-io/agent/mesh"
)

type relayRegisterMsg struct {
	Type     string `json:"type"`
	DeviceID string `json:"deviceId"`
	Token    string `json:"token"`
	Password string `json:"password,omitempty"`
}
type relayRegisterResp struct {
	OK      bool   `json:"ok"`
	Message string `json:"message,omitempty"`
}

// relayDerpTransport implements mesh.RelayTransport over a QUIC mesh_relay stream
// to the relay (mirrors desktop/agent/mesh_derp_transport.go, standalone).
type relayDerpTransport struct {
	relayAddr, deviceID, password string
	mu                            sync.Mutex
	writeMu                       sync.Mutex
	stream                        quic.Stream
	receiver                      func(string, []byte)
}

func (t *relayDerpTransport) SetReceiver(fn func(string, []byte)) {
	t.mu.Lock()
	t.receiver = fn
	t.mu.Unlock()
}

func (t *relayDerpTransport) SendFrame(dst string, payload []byte) error {
	t.mu.Lock()
	st := t.stream
	t.mu.Unlock()
	if st == nil {
		return fmt.Errorf("relay stream not connected")
	}
	t.writeMu.Lock()
	defer t.writeMu.Unlock()
	return mesh.EncodeDERPFrame(st, dst, payload)
}

func (t *relayDerpTransport) connect(ctx context.Context) error {
	conn, err := quic.DialAddr(ctx, t.relayAddr,
		&tls.Config{InsecureSkipVerify: true, NextProtos: []string{"yaver-relay"}},
		&quic.Config{MaxIdleTimeout: 120 * time.Second, KeepAlivePeriod: 20 * time.Second})
	if err != nil {
		return fmt.Errorf("dial relay: %w", err)
	}
	// Register on stream 0.
	reg, err := conn.OpenStreamSync(ctx)
	if err != nil {
		return err
	}
	// Token is opaque when the relay has no Convex (password-only auth) but must
	// be non-empty (relay/server.go register validation).
	data, _ := json.Marshal(relayRegisterMsg{Type: "register", DeviceID: t.deviceID, Token: "meshtest-token", Password: t.password})
	reg.Write(data)
	reg.Close()
	respData, _ := io.ReadAll(io.LimitReader(reg, 1<<16))
	var resp relayRegisterResp
	if err := json.Unmarshal(respData, &resp); err != nil || !resp.OK {
		return fmt.Errorf("register rejected: %s", resp.Message)
	}
	// Open the persistent mesh_relay stream + header.
	ms, err := conn.OpenStreamSync(ctx)
	if err != nil {
		return err
	}
	if _, err := ms.Write([]byte(fmt.Sprintf("{\"type\":\"mesh_relay\",\"deviceId\":%q}\n", t.deviceID))); err != nil {
		return err
	}
	t.mu.Lock()
	t.stream = ms
	t.mu.Unlock()
	go t.recvLoop(ms)
	fmt.Printf("[%s] mesh_relay stream attached to %s\n", t.deviceID, t.relayAddr)
	return nil
}

func (t *relayDerpTransport) recvLoop(st quic.Stream) {
	for {
		src, payload, err := mesh.DecodeDERPFrame(st)
		if err != nil {
			return
		}
		t.mu.Lock()
		r := t.receiver
		t.mu.Unlock()
		if r != nil {
			r(src, payload)
		}
	}
}

func runDerpMode(args []string) {
	if len(args) != 9 {
		fmt.Fprintln(os.Stderr, "derp needs: <ifname> <selfIPv4> <privB64> <selfDeviceID> <peerPubB64> <peerDeviceID> <peerIPv4> <relayAddr> <password>")
		os.Exit(2)
	}
	// args[0] (ifname) is accepted for signature symmetry; the manager always
	// uses the platform default TUN name (yaver-wg0 on linux), which is unique
	// per network namespace.
	selfIP, priv, selfID := args[1], args[2], args[3]
	peerPub, peerID, peerIP, relayAddr, password := args[4], args[5], args[6], args[7], args[8]

	transport := &relayDerpTransport{relayAddr: relayAddr, deviceID: selfID, password: password}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	if err := transport.connect(ctx); err != nil {
		cancel()
		fmt.Fprintln(os.Stderr, "relay connect:", err)
		os.Exit(1)
	}
	cancel()

	// PeerSource: one peer with NO direct endpoint -> the manager substitutes the
	// DERP shim endpoint, forcing traffic through the relay transport.
	source := func() (string, []mesh.Peer, error) {
		return selfIP, []mesh.Peer{{
			DeviceID:         peerID,
			PublicKey:        peerPub,
			Endpoint:         "", // no direct path -> DERP
			AllowedIPs:       []string{peerIP + "/32"},
			KeepaliveSeconds: 25,
		}}, nil
	}
	mgr := mesh.NewManager(priv, source)
	mgr.SetRelayTransport(transport)
	if err := mgr.Start(); err != nil {
		fmt.Fprintln(os.Stderr, "manager start:", err)
		os.Exit(1)
	}
	fmt.Printf("[%s] mesh up on %s, peer %s via relay-DERP — ready\n", selfID, selfIP, peerID)

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	<-sig
	_ = mgr.Stop()
}
