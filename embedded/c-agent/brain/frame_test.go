package brain

import (
	"bytes"
	"testing"
)

// Test vectors copied verbatim from
// embedded/c-agent/tests/test_frame.c so a regression in either
// codec produces a parity failure.

func TestFrameHeader_RoundTrip(t *testing.T) {
	in := FrameHeader{
		Length:   0x123456,
		Type:     FrameHello,
		Flags:    FlagEndStream | FlagAck,
		StreamID: 0xDEADBEEF,
	}
	buf := make([]byte, FrameHeaderSize)
	if _, err := EncodeFrameHeader(in, buf); err != nil {
		t.Fatalf("encode failed: %v", err)
	}
	out, err := DecodeFrameHeader(buf)
	if err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	if out != in {
		t.Fatalf("round-trip mismatch: got %+v, want %+v", out, in)
	}
}

func TestFrameHeader_KnownBytes(t *testing.T) {
	// Same vector as test_frame.c::test_known_bytes.
	in := FrameHeader{
		Length:   0x000007,
		Type:     0x01,
		Flags:    0x02,
		StreamID: 0x00000003,
	}
	expected := []byte{
		0x00, 0x00, 0x07, // length
		0x01,                   // type
		0x02,                   // flags
		0x00, 0x00, 0x00, 0x03, // stream_id
	}
	buf := make([]byte, FrameHeaderSize)
	n, err := EncodeFrameHeader(in, buf)
	if err != nil {
		t.Fatalf("encode failed: %v", err)
	}
	if n != FrameHeaderSize {
		t.Fatalf("returned %d bytes, want %d", n, FrameHeaderSize)
	}
	if !bytes.Equal(buf, expected) {
		t.Fatalf("byte mismatch: got %x, want %x", buf, expected)
	}
}

func TestFrameHeader_MaxValues(t *testing.T) {
	in := FrameHeader{
		Length:   FrameMaxPayload,
		Type:     0xFF,
		Flags:    0xFF,
		StreamID: 0xFFFFFFFF,
	}
	buf := make([]byte, FrameHeaderSize)
	if _, err := EncodeFrameHeader(in, buf); err != nil {
		t.Fatalf("encode failed: %v", err)
	}
	out, err := DecodeFrameHeader(buf)
	if err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	if out != in {
		t.Fatalf("round-trip mismatch: got %+v", out)
	}
}

func TestFrameHeader_PayloadTooLarge(t *testing.T) {
	in := FrameHeader{Length: FrameMaxPayload + 1}
	buf := make([]byte, FrameHeaderSize)
	_, err := EncodeFrameHeader(in, buf)
	if err != ErrPayloadTooLarge {
		t.Fatalf("got %v, want ErrPayloadTooLarge", err)
	}
}

func TestFrameHeader_BufferTooSmall(t *testing.T) {
	buf := make([]byte, FrameHeaderSize-1)
	_, err := EncodeFrameHeader(FrameHeader{}, buf)
	if err != ErrBufferTooSmall {
		t.Fatalf("got %v, want ErrBufferTooSmall", err)
	}
}

func TestFrameHeader_Truncated(t *testing.T) {
	buf := make([]byte, FrameHeaderSize-1)
	_, err := DecodeFrameHeader(buf)
	if err != ErrTruncated {
		t.Fatalf("got %v, want ErrTruncated", err)
	}
}
