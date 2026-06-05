package main

// mesh.go — Yaver Mesh DERP relay. When two mesh peers can't establish a direct
// WireGuard path (symmetric NAT), each agent opens a persistent "mesh_relay"
// QUIC stream to the relay. The relay registers it by deviceId and forwards
// frames: a frame agent A sends with dst=B is written to B's stream tagged
// src=A. Pass-through only — payloads are encrypted WG packets the relay never
// inspects. Framing mirrors desktop/agent/mesh/derpframe.go (relay is a separate
// module, so the ~30 lines are duplicated rather than imported).

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"
	"sync"

	"github.com/quic-go/quic-go"
)

type meshStreamHandle struct {
	stream quic.Stream
	mu     sync.Mutex // serializes writes to this agent's stream
}

func (h *meshStreamHandle) writeFrame(srcDeviceID string, payload []byte) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	return encodeMeshFrame(h.stream, srcDeviceID, payload)
}

// handleMeshStream registers a device's persistent mesh frame stream and pumps
// inbound frames to their destination peers. br is positioned just past the
// newline-terminated control header.
func (s *RelayServer) handleMeshStream(stream quic.Stream, br *bufio.Reader, deviceID string) {
	handle := &meshStreamHandle{stream: stream}
	s.meshMu.Lock()
	// Replace any stale stream for this device.
	s.meshStreams[deviceID] = handle
	s.meshMu.Unlock()

	defer func() {
		s.meshMu.Lock()
		if s.meshStreams[deviceID] == handle {
			delete(s.meshStreams, deviceID)
		}
		s.meshMu.Unlock()
		_ = stream.Close()
	}()

	for {
		dst, payload, err := decodeMeshFrame(br)
		if err != nil {
			return // stream closed / error
		}
		if dst == "" {
			continue
		}
		s.meshMu.RLock()
		target := s.meshStreams[dst]
		s.meshMu.RUnlock()
		if target == nil {
			continue // peer not connected to this relay for mesh; drop
		}
		if err := target.writeFrame(deviceID, payload); err != nil {
			// A broken destination stream shouldn't kill the source loop.
			continue
		}
	}
}

// dropMeshStream removes a device's mesh stream on disconnect.
func (s *RelayServer) dropMeshStream(deviceID string) {
	s.meshMu.Lock()
	if h, ok := s.meshStreams[deviceID]; ok {
		delete(s.meshStreams, deviceID)
		_ = h.stream.Close()
	}
	s.meshMu.Unlock()
}

// --- framing (mirror of desktop/agent/mesh/derpframe.go) ---

const maxMeshFrame = 65535

func encodeMeshFrame(w io.Writer, id string, payload []byte) error {
	if len(id) > maxMeshFrame || len(payload) > maxMeshFrame {
		return fmt.Errorf("mesh frame too large")
	}
	var hdr [2]byte
	binary.BigEndian.PutUint16(hdr[:], uint16(len(id)))
	if _, err := w.Write(hdr[:]); err != nil {
		return err
	}
	if _, err := w.Write([]byte(id)); err != nil {
		return err
	}
	binary.BigEndian.PutUint16(hdr[:], uint16(len(payload)))
	if _, err := w.Write(hdr[:]); err != nil {
		return err
	}
	_, err := w.Write(payload)
	return err
}

func decodeMeshFrame(r io.Reader) (id string, payload []byte, err error) {
	var hdr [2]byte
	if _, err = io.ReadFull(r, hdr[:]); err != nil {
		return "", nil, err
	}
	idLen := binary.BigEndian.Uint16(hdr[:])
	idBuf := make([]byte, idLen)
	if _, err = io.ReadFull(r, idBuf); err != nil {
		return "", nil, err
	}
	if _, err = io.ReadFull(r, hdr[:]); err != nil {
		return "", nil, err
	}
	plLen := binary.BigEndian.Uint16(hdr[:])
	payload = make([]byte, plLen)
	if _, err = io.ReadFull(r, payload); err != nil {
		return "", nil, err
	}
	return string(idBuf), payload, nil
}
