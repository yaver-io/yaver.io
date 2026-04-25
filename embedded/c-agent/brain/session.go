package brain

// Session is the brain side of a c-agent connection. One session
// owns one Conn and orchestrates the brain → device protocol:
// HELLO exchange, INVOKE → TOOL_RSP request/response, periodic
// HEARTBEATs, structured ERROR handling.
//
// v1 is deliberately synchronous — Invoke blocks until the
// matching TOOL_RSP arrives. Stream chunks for the same call are
// collected into Invoke's response. Other frames received while
// waiting (HEARTBEAT, EVENT) are routed to per-kind handlers if
// configured, otherwise logged + dropped.
//
// A real production brain wraps multiple sessions in a Manager
// that handles connection lifecycle, reconnect, etc. — that's
// upstream of this layer.

import (
	"errors"
	"fmt"
	"time"
)

// SessionConfig configures Session-level behaviour.
type SessionConfig struct {
	// AgentVersion identifies the brain's binary in HELLOs.
	// Free-form; "yvr-brain/0.1.0" is a reasonable default.
	AgentVersion string

	// MaxFrameBytes caps payload reads. Frames larger than this
	// are drained + dropped with ErrBufferTooSmall surfaced.
	// 0 → 16 MB (i.e. the wire's natural cap).
	MaxFrameBytes int

	// InvokeTimeout bounds how long Invoke waits for TOOL_RSP.
	// 0 → no timeout (block forever).
	InvokeTimeout time.Duration

	// OnEvent receives EVENT frames (asynchronous device pushes
	// like ERROR or unsolicited probe output). May be nil.
	OnEvent func(EventInfo)
}

// EventInfo is what OnEvent receives. Fields are populated based
// on the actual frame kind; callers check Kind to know which
// fields are meaningful.
type EventInfo struct {
	Kind    FrameType
	Header  FrameHeader
	Payload []byte
}

// Session holds per-connection state. NOT goroutine-safe — one
// caller drives it at a time. For concurrent use, wrap in a
// channel-based dispatcher.
type Session struct {
	conn          *Conn
	cfg           SessionConfig
	maxFrameBytes int

	// PeerHello is filled in after HandleHello succeeds. Carries
	// the device's role + agent_version so the brain knows what
	// it's talking to.
	PeerHello Hello

	streamCounter uint32 // monotonic for outgoing brain-initiated streams
}

// NewSession wraps a connected Conn with brain-side protocol
// machinery. Caller still owns conn.Close — Close on Session
// just stops dispatching, not the underlying socket (call
// Session.Close() to do both).
func NewSession(conn *Conn, cfg SessionConfig) *Session {
	max := cfg.MaxFrameBytes
	if max <= 0 {
		max = int(FrameMaxPayload)
	}
	if cfg.AgentVersion == "" {
		cfg.AgentVersion = "yvr-brain/0.0.1"
	}
	return &Session{
		conn:          conn,
		cfg:           cfg,
		maxFrameBytes: max,
		// Brain initiates streams on odd ids per architecture
		// doc §6 (initiator = odd, responder = even).
		streamCounter: 1,
	}
}

// HandleHello sends the brain's HELLO and waits for the device's.
// Both peers must HELLO before any other frame flows.
func (s *Session) HandleHello() error {
	hello := Hello{
		ProtocolVersion: ProtocolVersion,
		Role:            "brain",
		AgentVersion:    s.cfg.AgentVersion,
	}
	body := make([]byte, 256)
	n, err := hello.Encode(body)
	if err != nil {
		return fmt.Errorf("encode brain hello: %w", err)
	}
	if err := s.conn.SendFrame(FrameHeader{Type: FrameHello}, body[:n]); err != nil {
		return fmt.Errorf("send brain hello: %w", err)
	}

	// Read the device's HELLO. Other frames before HELLO are
	// protocol violations and abort the session.
	hdr, payload, err := s.conn.RecvFrame(s.maxFrameBytes)
	if err != nil {
		return fmt.Errorf("recv device hello: %w", err)
	}
	if hdr.Type != FrameHello {
		return fmt.Errorf("expected HELLO, got 0x%02x", uint8(hdr.Type))
	}
	peer, err := DecodeHello(payload)
	if err != nil {
		return fmt.Errorf("decode device hello: %w", err)
	}
	if peer.Role != "device" {
		return fmt.Errorf("unexpected peer role %q", peer.Role)
	}
	s.PeerHello = peer
	return nil
}

// Invoke runs one synchronous brain-initiated RPC against the
// device. Blocks until a matching TOOL_RSP arrives or
// InvokeTimeout fires. STREAM_CHUNK frames for the same stream
// id (currently used only for very long probes) accumulate into
// the response's Result field — caller decides how to interpret.
//
// Out-of-band frames (HEARTBEAT, EVENT) received during the wait
// route to OnEvent if configured; otherwise dropped.
//
// Errors:
//   - context.DeadlineExceeded if InvokeTimeout fires
//   - ErrBadFrame if the device sends a malformed response
//   - the underlying Conn error if the socket dies mid-call
func (s *Session) Invoke(req Invoke) (ToolRsp, error) {
	if len(req.ToolHash) == 0 {
		return ToolRsp{}, ErrInvalidArg
	}
	if req.ProtocolVersion == 0 {
		req.ProtocolVersion = ProtocolVersion
	}

	body := make([]byte, 4096)
	n, err := req.Encode(body)
	if err != nil {
		// Buffer too small — grow and retry.
		body = make([]byte, 1<<16)
		n, err = req.Encode(body)
		if err != nil {
			return ToolRsp{}, fmt.Errorf("encode invoke: %w", err)
		}
	}

	streamID := s.nextStreamID()
	hdr := FrameHeader{
		Type:     FrameInvoke,
		StreamID: streamID,
	}

	if s.cfg.InvokeTimeout > 0 {
		_ = s.conn.SetDeadline(time.Now().Add(s.cfg.InvokeTimeout))
		defer s.conn.SetDeadline(time.Time{})
	}

	if err := s.conn.SendFrame(hdr, body[:n]); err != nil {
		return ToolRsp{}, fmt.Errorf("send invoke: %w", err)
	}

	// Loop until we see TOOL_RSP for this stream.
	for {
		fhdr, fpayload, err := s.conn.RecvFrame(s.maxFrameBytes)
		if err != nil {
			return ToolRsp{}, err
		}
		switch fhdr.Type {
		case FrameToolRsp:
			if fhdr.StreamID != streamID {
				// Out-of-order tool_rsp is unusual but possible
				// if the device pipelines invokes — for v1 we
				// surface as bad frame. Caller can retry.
				return ToolRsp{}, fmt.Errorf("tool_rsp stream id mismatch: got %d, want %d",
					fhdr.StreamID, streamID)
			}
			rsp, err := DecodeToolRsp(fpayload)
			if err != nil {
				return ToolRsp{}, fmt.Errorf("decode tool_rsp: %w", err)
			}
			return rsp, nil
		case FrameStreamChunk:
			// Per-invoke streaming isn't wired into v1's
			// synchronous Invoke. Surface via OnEvent so
			// callers that subscribe can collect chunks; the
			// final TOOL_RSP eventually arrives.
			s.dispatchEvent(fhdr, fpayload)
		case FrameHeartbeat, FrameEvent, FrameError:
			s.dispatchEvent(fhdr, fpayload)
		default:
			// Unknown frame type during invoke — log via OnEvent.
			s.dispatchEvent(fhdr, fpayload)
		}
	}
}

// SendHeartbeat issues a HEARTBEAT frame with the current wall
// clock. Brain → device direction; device adopts the time as
// its monotonic-anchored clock.
func (s *Session) SendHeartbeat() error {
	hb := Heartbeat{
		ProtocolVersion: ProtocolVersion,
		NowMs:           uint64(time.Now().UnixMilli()),
	}
	body := make([]byte, 64)
	n, err := hb.Encode(body)
	if err != nil {
		return err
	}
	return s.conn.SendFrame(FrameHeader{Type: FrameHeartbeat}, body[:n])
}

// Close shuts down the underlying connection. Idempotent.
func (s *Session) Close() error {
	if s == nil || s.conn == nil {
		return nil
	}
	return s.conn.Close()
}

// nextStreamID returns the next outgoing stream id. Per protocol,
// brain-initiated streams are odd-numbered; we step by 2.
func (s *Session) nextStreamID() uint32 {
	id := s.streamCounter
	s.streamCounter += 2
	return id
}

func (s *Session) dispatchEvent(hdr FrameHeader, payload []byte) {
	if s.cfg.OnEvent == nil {
		return
	}
	cp := append([]byte(nil), payload...)
	s.cfg.OnEvent(EventInfo{
		Kind:    hdr.Type,
		Header:  hdr,
		Payload: cp,
	})
}

// ErrSessionClosed is returned when an operation is attempted on
// a closed or never-initialized session.
var ErrSessionClosed = errors.New("session closed")
