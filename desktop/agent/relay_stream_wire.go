package main

// relay_stream_wire.go — agent-side wire format for streaming
// /dev/native-bundle (and any large response) through the relay
// without buffering the entire body on either end.
//
// Why we need this:
// The legacy protocol marshalled the entire HTTP response into a JSON
// envelope (TunnelResponse) and shipped it to the relay in a single
// QUIC stream write. The relay then io.ReadAll'd the envelope before
// touching the client connection. For an 8.5 MB Hermes bundle the
// iPhone observed ZERO bytes for several seconds while the agent was
// still serializing the envelope, tripped iOS' Data stall detector
// (0x67002), and cancelled the URLSession task with NSURLError -999.
// On real-world relay hops (3G, slow Wi-Fi, etc.) this gets even
// worse. The product depends on relay working — Yaver users self-host
// agents at home and connect through the public relay.
//
// Wire format (one frame per HTTP response on a single QUIC stream):
//
//   [0xFE]                     1 byte  magic — distinguishes from
//                                       legacy JSON envelopes which
//                                       always start with '{' (0x7B).
//   [version=0x01]             1 byte
//   [status_code BE]           2 bytes uint16 — 100..599
//   [headers_len BE]           4 bytes uint32 — length of JSON-marshalled
//                                       map[string]string headers
//   [headers_json]             N bytes
//   ┌── repeated body chunks
//   │ [chunk_len BE]           4 bytes uint32 — when 0, EOF
//   │ [chunk_bytes]            chunk_len bytes
//   └──
//   [0x00000000]               4 bytes EOF terminator
//
// Endianness: big-endian throughout, matching network byte order.
// Maximum chunk size: 16 MiB (sanity cap, real chunks are 64 KiB).
// Maximum headers JSON: 1 MiB.
// No total-size cap — the protocol streams arbitrarily large responses.

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

const (
	streamWireMagic       byte   = 0xFE
	streamWireVersion     byte   = 0x01
	streamWireChunkSize          = 64 * 1024 // 64 KiB per body chunk
	streamWireMaxChunkLen uint32 = 16 << 20  // 16 MiB sanity cap on a single chunk
	streamWireMaxHdrLen   uint32 = 1 << 20   // 1 MiB sanity cap on headers JSON
)

// writeStreamingResponse serializes an HTTP response onto an io.Writer
// (a quic.Stream in production) using the streaming wire format. It
// reads resp.Body in 64 KiB chunks and writes each chunk to the stream
// before reading the next, so the relay receives bytes incrementally
// and can flush them to the HTTP client as they arrive. resp.Body is
// fully consumed (caller must close it).
func writeStreamingResponse(w io.Writer, resp *http.Response) error {
	// Header bytes: magic + version + status_code + headers_len + headers
	headers := make(map[string]string)
	for k, v := range resp.Header {
		if len(v) > 0 {
			headers[k] = v[0]
		}
	}
	headersJSON, err := json.Marshal(headers)
	if err != nil {
		return fmt.Errorf("stream wire: marshal headers: %w", err)
	}
	if uint32(len(headersJSON)) > streamWireMaxHdrLen {
		return fmt.Errorf("stream wire: headers too large: %d > %d", len(headersJSON), streamWireMaxHdrLen)
	}

	// Single combined first write so all the framing arrives in one
	// QUIC packet — most efficient and avoids tiny initial writes.
	prefix := make([]byte, 0, 2+2+4+len(headersJSON))
	prefix = append(prefix, streamWireMagic, streamWireVersion)
	statusBE := make([]byte, 2)
	binary.BigEndian.PutUint16(statusBE, uint16(resp.StatusCode))
	prefix = append(prefix, statusBE...)
	hdrLenBE := make([]byte, 4)
	binary.BigEndian.PutUint32(hdrLenBE, uint32(len(headersJSON)))
	prefix = append(prefix, hdrLenBE...)
	prefix = append(prefix, headersJSON...)
	if _, err := w.Write(prefix); err != nil {
		return fmt.Errorf("stream wire: write prefix: %w", err)
	}

	// Stream body — read 64 KiB at a time, frame each non-empty chunk.
	buf := make([]byte, streamWireChunkSize)
	chunkLenBE := make([]byte, 4)
	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			binary.BigEndian.PutUint32(chunkLenBE, uint32(n))
			if _, err := w.Write(chunkLenBE); err != nil {
				return fmt.Errorf("stream wire: write chunk len: %w", err)
			}
			if _, err := w.Write(buf[:n]); err != nil {
				return fmt.Errorf("stream wire: write chunk body: %w", err)
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return fmt.Errorf("stream wire: read body: %w", readErr)
		}
	}

	// EOF marker — chunk_len = 0
	if _, err := w.Write([]byte{0, 0, 0, 0}); err != nil {
		return fmt.Errorf("stream wire: write eof: %w", err)
	}
	return nil
}
