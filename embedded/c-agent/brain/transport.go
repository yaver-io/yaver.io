package brain

// TCP transport for the brain side. Same shape as the C side
// (transports/tcp.{h,c}) but using Go's net.Conn instead of raw
// POSIX sockets. The wire is identical — a Go brain talking to a
// C device sees byte-identical frames.

import (
	"fmt"
	"io"
	"net"
	"time"
)

// Conn is one framed TCP connection. Wraps net.Conn with frame
// send/recv helpers; the underlying connection is exposed for
// callers that need to manage deadlines or select across many.
type Conn struct {
	nc net.Conn
}

// Dial opens an outbound TCP connection to host:port with a
// connect timeout. timeout=0 means use Go's default.
func Dial(host string, port int, timeout time.Duration) (*Conn, error) {
	addr := fmt.Sprintf("%s:%d", host, port)
	nc, err := net.DialTimeout("tcp", addr, timeout)
	if err != nil {
		return nil, err
	}
	return &Conn{nc: nc}, nil
}

// Wrap wraps an existing net.Conn (e.g. one returned by
// listener.Accept) into a Conn so the same SendFrame/RecvFrame
// helpers apply on both client and server sides.
func Wrap(nc net.Conn) *Conn {
	return &Conn{nc: nc}
}

// Close shuts down the connection. Idempotent.
func (c *Conn) Close() error {
	if c == nil || c.nc == nil {
		return nil
	}
	return c.nc.Close()
}

// NetConn returns the underlying net.Conn for callers that need
// to set deadlines, select across many, etc. Returns nil for a
// nil receiver.
func (c *Conn) NetConn() net.Conn {
	if c == nil {
		return nil
	}
	return c.nc
}

// SendFrame writes one framed message: the 9-byte header
// followed by `payload`. The header's Length field is overwritten
// to match len(payload) so callers don't have to keep them in
// sync (matches the C side's behaviour).
func (c *Conn) SendFrame(hdr FrameHeader, payload []byte) error {
	if c == nil || c.nc == nil {
		return ErrInvalidArg
	}
	if uint64(len(payload)) > uint64(FrameMaxPayload) {
		return ErrPayloadTooLarge
	}
	hdr.Length = uint32(len(payload))

	var hb [FrameHeaderSize]byte
	if _, err := EncodeFrameHeader(hdr, hb[:]); err != nil {
		return err
	}
	if _, err := c.nc.Write(hb[:]); err != nil {
		return err
	}
	if len(payload) > 0 {
		if _, err := c.nc.Write(payload); err != nil {
			return err
		}
	}
	return nil
}

// RecvFrame reads one framed message into a caller-supplied
// payload cap. If the incoming payload exceeds payloadCap, the
// excess bytes are drained from the socket and ErrBufferTooSmall
// is returned alongside the partial payload — same behaviour as
// the C side, so the caller can resize and reset cleanly.
func (c *Conn) RecvFrame(payloadCap int) (FrameHeader, []byte, error) {
	if c == nil || c.nc == nil {
		return FrameHeader{}, nil, ErrInvalidArg
	}

	var hb [FrameHeaderSize]byte
	if _, err := io.ReadFull(c.nc, hb[:]); err != nil {
		return FrameHeader{}, nil, err
	}
	hdr, err := DecodeFrameHeader(hb[:])
	if err != nil {
		return hdr, nil, err
	}

	want := int(hdr.Length)

	if want > payloadCap {
		// Read what fits, drain the rest, signal truncation.
		var partial []byte
		if payloadCap > 0 {
			partial = make([]byte, payloadCap)
			if _, err := io.ReadFull(c.nc, partial); err != nil {
				return hdr, partial, err
			}
		}
		if drainErr := drainBytes(c.nc, want-payloadCap); drainErr != nil {
			return hdr, partial, drainErr
		}
		return hdr, partial, ErrBufferTooSmall
	}

	if want == 0 {
		return hdr, nil, nil
	}
	payload := make([]byte, want)
	if _, err := io.ReadFull(c.nc, payload); err != nil {
		return hdr, payload, err
	}
	return hdr, payload, nil
}

func drainBytes(r io.Reader, n int) error {
	if n <= 0 {
		return nil
	}
	scratch := make([]byte, 1024)
	for n > 0 {
		take := len(scratch)
		if take > n {
			take = n
		}
		if _, err := io.ReadFull(r, scratch[:take]); err != nil {
			return err
		}
		n -= take
	}
	return nil
}

// SetDeadline pushes a per-connection IO deadline to the
// underlying socket. Useful when the brain wants to bound a
// session that's gone quiet.
func (c *Conn) SetDeadline(t time.Time) error {
	return c.nc.SetDeadline(t)
}
