package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestStreamingWireRoundtrip proves the agent's writeStreamingResponse
// produces bytes the relay's reader (parallel implementation in
// relay/relay_stream_wire.go) accepts. We can't import the relay
// package, so we re-implement the reader here matching the wire spec
// exactly — if the formats drift, this test breaks before deploy.
func TestStreamingWireRoundtrip(t *testing.T) {
	const bodySize = 10 * 1024 * 1024 // 10 MiB — bigger than SFMG bundle

	// Build a synthetic upstream HTTP server that streams a 10 MB body
	// in 64 KiB chunks (just like the agent's local /dev/native-bundle
	// would).
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("X-Yaver-Bundle-Metadata", `{"size":10485760,"md5":"deadbeef","hermesBCVersion":96}`)
		w.WriteHeader(200)
		buf := make([]byte, 64*1024)
		for i := 0; i < bodySize/len(buf); i++ {
			for j := range buf {
				buf[j] = byte((i + j) & 0xFF)
			}
			w.Write(buf)
		}
	}))
	defer upstream.Close()

	resp, err := http.Get(upstream.URL)
	if err != nil {
		t.Fatalf("get upstream: %v", err)
	}
	defer resp.Body.Close()

	// Pipe so writer and reader can run concurrently — proves it
	// streams instead of fully buffering on either end.
	pr, pw := io.Pipe()
	writeErr := make(chan error, 1)
	go func() {
		defer pw.Close()
		writeErr <- writeStreamingResponse(pw, resp)
	}()

	// Replicate the relay's reader inline so we exercise the EXACT
	// same wire format. (Cross-package test would be cleaner but we
	// can't import package main from another package main.)
	first := make([]byte, 1)
	if _, err := io.ReadFull(pr, first); err != nil {
		t.Fatalf("read magic: %v", err)
	}
	if first[0] != streamWireMagic {
		t.Fatalf("magic byte = 0x%02X, want 0x%02X", first[0], streamWireMagic)
	}
	var ver [1]byte
	if _, err := io.ReadFull(pr, ver[:]); err != nil {
		t.Fatalf("read version: %v", err)
	}
	if ver[0] != streamWireVersion {
		t.Fatalf("version = %d, want %d", ver[0], streamWireVersion)
	}
	var statusBE [2]byte
	if _, err := io.ReadFull(pr, statusBE[:]); err != nil {
		t.Fatalf("read status: %v", err)
	}
	statusCode := int(binary.BigEndian.Uint16(statusBE[:]))
	if statusCode != 200 {
		t.Fatalf("status = %d, want 200", statusCode)
	}
	var hdrLenBE [4]byte
	if _, err := io.ReadFull(pr, hdrLenBE[:]); err != nil {
		t.Fatalf("read headers len: %v", err)
	}
	hdrLen := binary.BigEndian.Uint32(hdrLenBE[:])
	hdrBuf := make([]byte, hdrLen)
	if _, err := io.ReadFull(pr, hdrBuf); err != nil {
		t.Fatalf("read headers: %v", err)
	}
	var headers map[string]string
	if err := json.Unmarshal(hdrBuf, &headers); err != nil {
		t.Fatalf("parse headers: %v", err)
	}
	if headers["X-Yaver-Bundle-Metadata"] == "" {
		t.Errorf("missing X-Yaver-Bundle-Metadata header in streamed response")
	}

	// Body chunks
	var assembled bytes.Buffer
	chunkLenBE := make([]byte, 4)
	chunksReceived := 0
	for {
		if _, err := io.ReadFull(pr, chunkLenBE); err != nil {
			t.Fatalf("read chunk len at chunk #%d: %v", chunksReceived, err)
		}
		chunkLen := binary.BigEndian.Uint32(chunkLenBE)
		if chunkLen == 0 {
			break // EOF
		}
		if _, err := io.CopyN(&assembled, pr, int64(chunkLen)); err != nil {
			t.Fatalf("copy chunk #%d (len=%d): %v", chunksReceived, chunkLen, err)
		}
		chunksReceived++
	}

	if assembled.Len() != bodySize {
		t.Fatalf("body size = %d, want %d", assembled.Len(), bodySize)
	}
	if chunksReceived < 50 {
		// 10 MB / 64 KB = 160 chunks; we should see lots, proving
		// streaming actually happened in chunks rather than one big
		// blob.
		t.Errorf("only %d chunks received — looks like buffered, not streamed", chunksReceived)
	}

	// Drain the writer goroutine
	select {
	case err := <-writeErr:
		if err != nil {
			t.Fatalf("writer error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("writer goroutine didn't finish")
	}
}

// TestStreamingWireSmallResponse covers the JSON-status-only case
// (e.g. /dev/build-native returning a small JSON body) — should
// produce one chunk + EOF marker.
func TestStreamingWireSmallResponse(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		io.WriteString(w, `{"status":"ok","bundleUrl":"/dev/native-bundle"}`)
	}))
	defer upstream.Close()

	resp, err := http.Get(upstream.URL)
	if err != nil {
		t.Fatalf("get upstream: %v", err)
	}
	defer resp.Body.Close()

	var buf bytes.Buffer
	if err := writeStreamingResponse(&buf, resp); err != nil {
		t.Fatalf("write: %v", err)
	}

	// Format: [0xFE][0x01][2 status][4 hdr_len][hdr][4 chunk_len][chunk][4 0]
	out := buf.Bytes()
	if out[0] != streamWireMagic {
		t.Fatalf("magic = 0x%02X", out[0])
	}
	if out[1] != streamWireVersion {
		t.Fatalf("version = %d", out[1])
	}
	if status := binary.BigEndian.Uint16(out[2:4]); status != 200 {
		t.Errorf("status = %d", status)
	}
	hdrLen := binary.BigEndian.Uint32(out[4:8])
	if hdrLen < 10 {
		t.Errorf("headers len = %d, expected non-trivial JSON", hdrLen)
	}
	// Last 4 bytes must be the EOF marker (chunk_len=0).
	tail := out[len(out)-4:]
	if !bytes.Equal(tail, []byte{0, 0, 0, 0}) {
		t.Errorf("tail = %x, want 00000000 (EOF marker)", tail)
	}
}

// TestStreamingWireEmptyBody — a 204 No Content / 304 Not Modified-like
// response. Format must still produce a valid frame ending in EOF
// marker, no body chunks.
func TestStreamingWireEmptyBody(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(204)
	}))
	defer upstream.Close()

	resp, err := http.Get(upstream.URL)
	if err != nil {
		t.Fatalf("get upstream: %v", err)
	}
	defer resp.Body.Close()

	var buf bytes.Buffer
	if err := writeStreamingResponse(&buf, resp); err != nil {
		t.Fatalf("write: %v", err)
	}

	// Should end with the EOF marker — no body chunks before it.
	tail := buf.Bytes()[buf.Len()-4:]
	if !bytes.Equal(tail, []byte{0, 0, 0, 0}) {
		t.Errorf("tail = %x, want EOF marker", tail)
	}

	// Status check
	out := buf.Bytes()
	if status := binary.BigEndian.Uint16(out[2:4]); status != 204 {
		t.Errorf("status = %d, want 204", status)
	}
}

// TestStreamingFirstByteIsMagic guarantees the first byte is never
// '{' or any other JSON start character. The relay distinguishes
// formats by peeking the first byte; a collision would silently break
// backwards compat.
func TestStreamingFirstByteIsMagic(t *testing.T) {
	if streamWireMagic == '{' || streamWireMagic == '[' {
		t.Fatalf("magic byte 0x%02X collides with JSON start", streamWireMagic)
	}
	// 0xFE is invalid as the first byte of any UTF-8 / ASCII string
	// and not a JSON whitespace either.
	if streamWireMagic >= 0x20 && streamWireMagic <= 0x7E {
		t.Errorf("magic 0x%02X falls in printable ASCII range — ambiguous with random JSON", streamWireMagic)
	}
}
