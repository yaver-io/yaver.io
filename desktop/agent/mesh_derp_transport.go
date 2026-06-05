package main

// mesh_derp_transport.go — agent side of the relay-as-DERP fallback. Rides the
// agent's EXISTING registered relay QUIC connection (same one expose uses): on
// connect it opens a single persistent "mesh_relay" stream, writes a header,
// and then pumps WireGuard frames to/from the relay. Implements
// mesh.RelayTransport so the mesh.Manager can bridge symmetric-NAT peers through
// per-peer loopback shims.
//
// Frames dropped before the relay stream is up are harmless — WireGuard retries
// its handshakes, so the path heals as soon as the relay connection lands.

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/quic-go/quic-go"
	"github.com/yaver-io/agent/mesh"
)

// globalMeshDERP is the process-wide DERP transport, shared between the mesh
// manager (which calls SendFrame/SetReceiver) and the relay connection loop
// (which calls attach with the live connection). Mirrors globalConvexSync.
var globalMeshDERP *meshRelayTransport

type meshRelayTransport struct {
	mu       sync.Mutex
	writeMu  sync.Mutex
	stream   quic.Stream
	receiver func(srcDeviceID string, payload []byte)
}

func ensureGlobalMeshDERP() *meshRelayTransport {
	if globalMeshDERP == nil {
		globalMeshDERP = &meshRelayTransport{}
	}
	return globalMeshDERP
}

// SetReceiver records the inbound-frame callback (wired to DERPManager.DeliverFrame).
func (t *meshRelayTransport) SetReceiver(fn func(string, []byte)) {
	t.mu.Lock()
	t.receiver = fn
	t.mu.Unlock()
}

// SendFrame forwards an encrypted WG packet to dstDeviceID over the relay mesh
// stream. Returns an error (frame dropped) when the relay isn't connected yet.
func (t *meshRelayTransport) SendFrame(dstDeviceID string, payload []byte) error {
	t.mu.Lock()
	st := t.stream
	t.mu.Unlock()
	if st == nil {
		return fmt.Errorf("mesh relay stream not connected")
	}
	t.writeMu.Lock()
	defer t.writeMu.Unlock()
	return mesh.EncodeDERPFrame(st, dstDeviceID, payload)
}

// attach is called when the agent (re)registers with the relay. It opens the
// mesh_relay stream on the live connection and starts the receive loop.
func (t *meshRelayTransport) attach(conn quic.Connection, deviceID string) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	stream, err := conn.OpenStreamSync(ctx)
	if err != nil {
		return
	}
	// Newline-terminated header so the relay's handleControlMsg routes this to
	// the persistent mesh path rather than the one-shot expose path.
	header := fmt.Sprintf("{\"type\":\"mesh_relay\",\"deviceId\":%q}\n", deviceID)
	if _, err := stream.Write([]byte(header)); err != nil {
		_ = stream.Close()
		return
	}
	t.mu.Lock()
	old := t.stream
	t.stream = stream
	t.mu.Unlock()
	if old != nil {
		_ = old.Close()
	}
	go t.recvLoop(stream)
}

func (t *meshRelayTransport) recvLoop(st quic.Stream) {
	for {
		src, payload, err := mesh.DecodeDERPFrame(st)
		if err != nil {
			t.mu.Lock()
			if t.stream == st {
				t.stream = nil
			}
			t.mu.Unlock()
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
