// Package brain mirrors the c-agent wire layer in Go. The frame
// header below is byte-identical to what core/src/frame.c emits;
// every field on the wire matches the same byte offset and
// encoding rules.

package brain

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// FrameType is the 8-bit type code carried in every frame header.
// Values are stable; new types append.
type FrameType uint8

const (
	FrameHello        FrameType = 0x01
	FrameAuth         FrameType = 0x02
	FrameAuthRsp      FrameType = 0x03
	FrameAttest       FrameType = 0x04
	FrameInvoke       FrameType = 0x05
	FrameToolRsp      FrameType = 0x06
	FrameNeed         FrameType = 0x07
	FrameModule       FrameType = 0x08
	FrameStreamChunk  FrameType = 0x09
	FrameEvent        FrameType = 0x0A
	FrameHeartbeat    FrameType = 0x0B
	FrameApprovalReq  FrameType = 0x0C
	FrameApprovalRsp  FrameType = 0x0D
	FrameError        FrameType = 0x0E
	FrameWindowUpdate FrameType = 0x0F
	FrameKill         FrameType = 0x10
)

// FrameFlag is the 8-bit flag bitfield. Bits are ORable.
type FrameFlag uint8

const (
	FlagEndStream  FrameFlag = 0x01
	FlagAck        FrameFlag = 0x02
	FlagCompressed FrameFlag = 0x04
)

const (
	// FrameHeaderSize is the fixed wire size of every frame
	// header — 9 bytes, big-endian.
	FrameHeaderSize = 9

	// FrameMaxPayload is the largest payload a frame can carry,
	// imposed by the 24-bit length field.
	FrameMaxPayload uint32 = 0x00FFFFFF
)

// FrameHeader is the 9-byte frame header. Layout on the wire:
//
//	+---------+--------+--------+----------------+
//	| length  | type   | flags  |   stream_id    |
//	| 24 bits | 8 bits | 8 bits |   32 bits      |
//	+---------+--------+--------+----------------+
type FrameHeader struct {
	Length   uint32
	Type     FrameType
	Flags    FrameFlag
	StreamID uint32
}

// Errors returned by the codec. The values match the C side's
// yvr_status_t error codes by intent — they're translated to
// numeric codes by the wire layer above this codec.
var (
	ErrInvalidArg      = errors.New("invalid argument")
	ErrBufferTooSmall  = errors.New("buffer too small")
	ErrPayloadTooLarge = errors.New("payload too large")
	ErrTruncated       = errors.New("truncated")
	ErrBadFrame        = errors.New("bad frame")
)

// EncodeFrameHeader writes the header into the first
// FrameHeaderSize bytes of out. Returns the number of bytes
// written (always FrameHeaderSize on success).
func EncodeFrameHeader(h FrameHeader, out []byte) (int, error) {
	if len(out) < FrameHeaderSize {
		return 0, ErrBufferTooSmall
	}
	if h.Length > FrameMaxPayload {
		return 0, ErrPayloadTooLarge
	}

	// 24-bit big-endian length.
	out[0] = byte((h.Length >> 16) & 0xFF)
	out[1] = byte((h.Length >> 8) & 0xFF)
	out[2] = byte(h.Length & 0xFF)
	out[3] = byte(h.Type)
	out[4] = byte(h.Flags)
	binary.BigEndian.PutUint32(out[5:9], h.StreamID)

	return FrameHeaderSize, nil
}

// DecodeFrameHeader reads a header from in. The buffer may carry
// payload bytes after the header; only the first FrameHeaderSize
// bytes are consumed.
func DecodeFrameHeader(in []byte) (FrameHeader, error) {
	if len(in) < FrameHeaderSize {
		return FrameHeader{}, ErrTruncated
	}
	length := uint32(in[0])<<16 | uint32(in[1])<<8 | uint32(in[2])
	return FrameHeader{
		Length:   length,
		Type:     FrameType(in[3]),
		Flags:    FrameFlag(in[4]),
		StreamID: binary.BigEndian.Uint32(in[5:9]),
	}, nil
}

// String renders a FrameHeader for log output. Only used for
// diagnostics; not part of the wire contract.
func (h FrameHeader) String() string {
	return fmt.Sprintf("FrameHeader{type=0x%02x flags=0x%02x sid=%d len=%d}",
		uint8(h.Type), uint8(h.Flags), h.StreamID, h.Length)
}
