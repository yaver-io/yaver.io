package main

// relay_stream_wire.go — relay-side reader for the streaming wire
// format used by `desktop/agent/relay_stream_wire.go`.
//
// The relay reads the first byte off the QUIC stream:
//   - 0xFE → streaming wire format (this file's readStreamingResponse)
//   - '{'  → legacy JSON envelope (existing TunnelResponse path in
//             handleProxy). Backwards compatible with old agents.
//
// As streaming chunks arrive on the QUIC stream, they are written
// straight to the http.ResponseWriter and immediately Flush()'d so
// the HTTP client (iPhone, browser, Mac CLI) receives bytes within
// ~100ms instead of waiting for the entire body to be buffered. This
// is what fixes iOS' Data stall (0x67002) → NSURLError -999 cascade
// on 8 MB+ Hermes bundle downloads.
//
// Wire format (must match desktop/agent/relay_stream_wire.go):
//
//   [0xFE]                     1 byte  magic — already consumed by caller
//   [version=0x01]             1 byte
//   [status_code BE]           2 bytes uint16
//   [headers_len BE]           4 bytes uint32
//   [headers_json]             N bytes
//   ┌── repeated body chunks
//   │ [chunk_len BE]           4 bytes uint32 — when 0, EOF
//   │ [chunk_bytes]            chunk_len bytes
//   └──
//   [0x00000000]               4 bytes EOF terminator

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

const (
	streamWireMagic       byte   = 0xFE
	streamWireMaxChunkLen uint32 = 16 << 20 // 16 MiB cap on a single chunk
	streamWireMaxHdrLen   uint32 = 1 << 20  // 1 MiB cap on headers JSON
)

// readStreamingResponse drains a streaming response from `stream` and
// forwards it to `w` chunk by chunk. The 0xFE magic byte must already
// have been consumed by the caller (so the caller can detect format
// before deciding which reader to invoke).
//
// Returns nil on clean completion (EOF marker received). Returns an
// error if the wire format is invalid, headers can't be parsed, or
// any I/O fails. The caller should NOT have already written status/
// headers to w when this is called — readStreamingResponse owns the
// response-writing lifecycle.
func readStreamingResponse(w http.ResponseWriter, stream io.Reader) error {
	flusher, _ := w.(http.Flusher)

	// Version
	var ver [1]byte
	if _, err := io.ReadFull(stream, ver[:]); err != nil {
		return fmt.Errorf("stream wire: read version: %w", err)
	}
	if ver[0] != 0x01 {
		return fmt.Errorf("stream wire: unsupported version %d", ver[0])
	}

	// Status code
	var sb [2]byte
	if _, err := io.ReadFull(stream, sb[:]); err != nil {
		return fmt.Errorf("stream wire: read status: %w", err)
	}
	statusCode := int(binary.BigEndian.Uint16(sb[:]))
	if statusCode < 100 || statusCode > 599 {
		return fmt.Errorf("stream wire: invalid status code %d", statusCode)
	}

	// Headers length + JSON
	var hl [4]byte
	if _, err := io.ReadFull(stream, hl[:]); err != nil {
		return fmt.Errorf("stream wire: read headers len: %w", err)
	}
	headersLen := binary.BigEndian.Uint32(hl[:])
	if headersLen > streamWireMaxHdrLen {
		return fmt.Errorf("stream wire: headers too large: %d > %d", headersLen, streamWireMaxHdrLen)
	}
	hdrBuf := make([]byte, headersLen)
	if _, err := io.ReadFull(stream, hdrBuf); err != nil {
		return fmt.Errorf("stream wire: read headers: %w", err)
	}
	var headers map[string]string
	if err := json.Unmarshal(hdrBuf, &headers); err != nil {
		return fmt.Errorf("stream wire: parse headers: %w", err)
	}

	// Write status + headers to the HTTP client immediately, so
	// iOS / browsers see something flowing while we wait on body
	// chunks from the agent.
	for k, v := range headers {
		w.Header().Set(k, v)
	}
	w.WriteHeader(statusCode)
	if flusher != nil {
		flusher.Flush()
	}

	// Body chunks until EOF marker
	chunkLenBuf := make([]byte, 4)
	for {
		if _, err := io.ReadFull(stream, chunkLenBuf); err != nil {
			if err == io.EOF || err == io.ErrUnexpectedEOF {
				// Agent disconnected without sending EOF marker —
				// treat as truncation. Body sent so far is what
				// the client got. Surface a log line; can't change
				// the HTTP status now (already written).
				return fmt.Errorf("stream wire: unexpected eof reading chunk len: %w", err)
			}
			return fmt.Errorf("stream wire: read chunk len: %w", err)
		}
		chunkLen := binary.BigEndian.Uint32(chunkLenBuf)
		if chunkLen == 0 {
			// Clean EOF.
			return nil
		}
		if chunkLen > streamWireMaxChunkLen {
			return fmt.Errorf("stream wire: chunk too large: %d > %d", chunkLen, streamWireMaxChunkLen)
		}
		// io.CopyN streams the chunk bytes from the QUIC stream
		// straight into the HTTP response body. Flush after each
		// chunk so the client never sees a multi-second pause.
		if _, err := io.CopyN(w, stream, int64(chunkLen)); err != nil {
			return fmt.Errorf("stream wire: copy chunk: %w", err)
		}
		if flusher != nil {
			flusher.Flush()
		}
	}
}
