package mesh

// derpframe.go — wire framing for WireGuard-over-relay (DERP) frames carried on
// the agent↔relay QUIC mesh stream. Each frame: a destination/source deviceId
// (length-prefixed) plus the opaque WG payload (length-prefixed). The relay
// reads the dst to route; the receiving agent reads the src to attribute the
// frame to the right peer shim.

import (
	"encoding/binary"
	"fmt"
	"io"
)

const maxDERPFrame = 65535

// EncodeDERPFrame writes a length-prefixed frame: u16 idLen, id bytes, u16
// payloadLen, payload bytes. id is the dst deviceId (agent→relay) or src
// deviceId (relay→agent).
func EncodeDERPFrame(w io.Writer, id string, payload []byte) error {
	if len(id) > maxDERPFrame || len(payload) > maxDERPFrame {
		return fmt.Errorf("derp frame too large")
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

// DecodeDERPFrame reads one frame written by EncodeDERPFrame.
func DecodeDERPFrame(r io.Reader) (id string, payload []byte, err error) {
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
