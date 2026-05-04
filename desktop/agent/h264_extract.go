package main

// h264_extract.go — pure-Go extraction of H.264 NAL units from
// platform-native screen capture output. Two input formats:
//
//  1. Raw Annex-B (start-code delimited NALUs): emitted by
//     `adb exec-out screenrecord --output-format=h264 -`. Streamed
//     splitting via AnnexBReader.
//
//  2. Fragmented MP4: emitted by
//     `xcrun simctl io booted recordVideo --codec=h264 -`. Phase 4
//     work — MP4ToAnnexB returns ErrUnsupportedFormat for now so the
//     iOS pipeline falls back to the existing JPEG-DC path until the
//     parser lands.
//
// The extractor is streaming on purpose: callers pump NAL units
// directly into Pion's TrackLocalStaticSample.WriteSample one frame
// at a time without buffering whole videos.
//
// This file deliberately has zero external dependencies. The whole
// reason it exists is to keep `npm install -g yaver-cli` enough to
// stream — no apt-get ffmpeg, no GStreamer.

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

// ErrUnsupportedFormat is returned by parsers for input shapes that
// have not been wired up yet (chiefly fragmented MP4). Callers should
// treat it as a soft failure and fall back to a different transport
// rather than crashing the agent.
var ErrUnsupportedFormat = errors.New("h264 extract: input format not supported (yet)")

// NALUnit is a single H.264 NAL unit *without* the Annex-B start
// code. Type is the lower 5 bits of the first byte — the standard
// H.264 NAL unit type identifier (1=non-IDR slice, 5=IDR slice,
// 6=SEI, 7=SPS, 8=PPS, 9=Access Unit Delimiter, etc.).
type NALUnit struct {
	Data []byte
	Type uint8
}

// IsKeyframe returns true for NAL units that mark the start of a
// decodable sequence: IDR slices and parameter sets. A receiver that
// just connected (or that just rotated) needs one of these before it
// can paint anything.
func (n NALUnit) IsKeyframe() bool {
	switch n.Type {
	case 5, 7, 8: // IDR, SPS, PPS
		return true
	}
	return false
}

// AnnexBReader splits an io.Reader stream on H.264 start codes
// (0x000001 or 0x00000001) and emits NAL units.
//
// The reader holds at most the in-flight NALU plus a 4-byte
// lookahead for the next start code, so a 4 GB capture costs O(1)
// memory. Suitable for piping `adb screenrecord` stdout directly
// into Pion.
type AnnexBReader struct {
	br      *bufio.Reader
	pending []byte // start-code bytes already consumed; logically belong to the next NAL
	eof     bool
}

// NewAnnexBReader wraps r in a NAL splitter. The supplied reader is
// not closed by AnnexBReader; the caller owns its lifecycle.
func NewAnnexBReader(r io.Reader) *AnnexBReader {
	return &AnnexBReader{br: bufio.NewReaderSize(r, 64*1024)}
}

// Next returns the next NAL unit, or io.EOF when the stream is fully
// drained. Honors ctx for cancellation. Returns short reads as
// io.ErrUnexpectedEOF only if the input ends mid-NAL with non-empty
// content, in which case the partial NAL is also returned so the
// caller can decide whether to log + continue or abort.
func (r *AnnexBReader) Next(ctx context.Context) (NALUnit, error) {
	if err := ctx.Err(); err != nil {
		return NALUnit{}, err
	}
	if r.eof {
		return NALUnit{}, io.EOF
	}
	if err := r.skipStartCode(); err != nil {
		return NALUnit{}, err
	}
	var nal []byte
	for {
		if err := ctx.Err(); err != nil {
			return NALUnit{}, err
		}
		b, err := r.br.ReadByte()
		if err == io.EOF {
			r.eof = true
			if len(nal) == 0 {
				return NALUnit{}, io.EOF
			}
			return mkNAL(nal), nil
		}
		if err != nil {
			return NALUnit{}, err
		}
		nal = append(nal, b)
		// Detect the trailing 3-byte start-code pattern 00 00 01.
		// When present it marks the BEGINNING of the next NAL — the
		// current NAL is everything before it.
		if n := len(nal); n >= 3 && nal[n-3] == 0 && nal[n-2] == 0 && nal[n-1] == 1 {
			cut := n - 3
			pendLen := 3
			// 4-byte start code (00 00 00 01) — peel off the leading 0
			// from the payload too.
			if cut > 0 && nal[cut-1] == 0 {
				cut--
				pendLen = 4
			}
			payload := append([]byte(nil), nal[:cut]...)
			// Stash the start code so skipStartCode for the next NAL
			// knows it has already been consumed from the underlying
			// reader.
			if pendLen == 4 {
				r.pending = []byte{0, 0, 0, 1}
			} else {
				r.pending = []byte{0, 0, 1}
			}
			if len(payload) > 0 {
				return mkNAL(payload), nil
			}
			// Empty NAL between two start codes (rare but legal —
			// some encoders emit an extra start code at sequence
			// boundaries). Consume and keep going.
			nal = nal[:0]
		}
	}
}

// skipStartCode discards a 3- or 4-byte start code from the front of
// the stream. If a previous Next() already consumed the start code
// (recorded in r.pending), this is a no-op. Returns io.EOF only when
// the underlying reader is empty.
func (r *AnnexBReader) skipStartCode() error {
	if len(r.pending) > 0 {
		r.pending = nil
		return nil
	}
	peek, err := r.br.Peek(4)
	if err != nil && len(peek) == 0 {
		return err
	}
	if len(peek) >= 4 && peek[0] == 0 && peek[1] == 0 && peek[2] == 0 && peek[3] == 1 {
		_, _ = r.br.Discard(4)
		return nil
	}
	if len(peek) >= 3 && peek[0] == 0 && peek[1] == 0 && peek[2] == 1 {
		_, _ = r.br.Discard(3)
		return nil
	}
	// No start code at the front of the stream. Some emitters drop
	// it and just open with the NAL payload; treat the bytes up to
	// the next start code as one NAL. This keeps the reader robust
	// without needing the caller to special-case malformed inputs.
	return nil
}

func mkNAL(b []byte) NALUnit {
	if len(b) == 0 {
		return NALUnit{}
	}
	return NALUnit{Data: b, Type: b[0] & 0x1f}
}

// MP4AnnexBReader streams a fragmented MP4 (the format
// `xcrun simctl io recordVideo --codec=h264 -` writes to stdout) and
// emits H.264 NAL units. The first NALs returned are the SPS and PPS
// extracted from the avcC config record, so a fresh decoder can come
// up immediately; subsequent calls drain whatever the running mdat
// boxes hold.
//
// The parser is intentionally small. We don't validate every box,
// don't decode timestamps, don't surface track metadata. We care
// about exactly two things: (a) the SPS/PPS in moov→trak→mdia→
// minf→stbl→stsd→avc1→avcC, and (b) each mdat box's AVCC-formatted
// NAL units (4-byte BE length + payload, no start code). Everything
// else gets skipped at the top level.
type MP4AnnexBReader struct {
	r *bufio.Reader

	sps              [][]byte
	pps              [][]byte
	nalUnitLen       int    // bytes per AVCC length prefix; defaults to 4
	parameterSetsSent bool

	queue []NALUnit
	eof   bool
}

// MP4ToAnnexB wraps r in a fragmented-MP4 → Annex-B NAL stream.
// Returns a reader whose Next() implements the same interface as
// AnnexBReader so callers can treat the two interchangeably.
func MP4ToAnnexB(r io.Reader) (*MP4AnnexBReader, error) {
	if r == nil {
		return nil, fmt.Errorf("nil reader")
	}
	return &MP4AnnexBReader{
		r:          bufio.NewReaderSize(r, 64*1024),
		nalUnitLen: 4,
	}, nil
}

// Next returns the next NAL unit in the stream. Parameter sets
// (SPS/PPS) are emitted once, before any frame data, so a decoder
// that just connected can come up. After that, mdat NALs flow in
// arrival order.
func (m *MP4AnnexBReader) Next(ctx context.Context) (NALUnit, error) {
	for {
		if err := ctx.Err(); err != nil {
			return NALUnit{}, err
		}
		if len(m.queue) > 0 {
			n := m.queue[0]
			m.queue = m.queue[1:]
			return n, nil
		}
		if m.eof {
			return NALUnit{}, io.EOF
		}

		size, boxType, err := readBoxHeader(m.r)
		if errors.Is(err, io.EOF) {
			m.eof = true
			if len(m.queue) == 0 {
				return NALUnit{}, io.EOF
			}
			continue
		}
		if err != nil {
			return NALUnit{}, err
		}

		switch boxType {
		case "moov":
			if err := m.parseMoov(size); err != nil {
				return NALUnit{}, err
			}
			m.flushParameterSets()
		case "mdat":
			if err := m.parseMdat(size); err != nil {
				return NALUnit{}, err
			}
		default:
			// ftyp / moof / free / skip / styp / sidx / and any
			// other top-level box we don't care about — discard
			// the body in chunks so memory stays flat.
			if err := skipBytes(m.r, size); err != nil {
				return NALUnit{}, err
			}
		}
	}
}

// flushParameterSets queues SPS/PPS NALs at the front of the output
// queue (only once per stream). Called immediately after parsing
// moov so the very first Next() that has bytes returns SPS, then
// PPS, then the first picture data.
func (m *MP4AnnexBReader) flushParameterSets() {
	if m.parameterSetsSent {
		return
	}
	if len(m.sps) == 0 && len(m.pps) == 0 {
		return
	}
	for _, s := range m.sps {
		if len(s) == 0 {
			continue
		}
		m.queue = append(m.queue, NALUnit{Data: append([]byte(nil), s...), Type: s[0] & 0x1f})
	}
	for _, p := range m.pps {
		if len(p) == 0 {
			continue
		}
		m.queue = append(m.queue, NALUnit{Data: append([]byte(nil), p...), Type: p[0] & 0x1f})
	}
	m.parameterSetsSent = true
}

// parseMoov reads the entire movie box into memory (typical size
// ≪50 KB) and walks its child container boxes looking for avcC.
func (m *MP4AnnexBReader) parseMoov(size uint64) error {
	body, err := readN(m.r, size)
	if err != nil {
		return err
	}
	return m.walkContainer(body)
}

// walkContainer iterates child boxes inside a known container box.
// The MP4 spec defines which boxes nest; we hardcode the path we
// care about (moov → trak → mdia → minf → stbl → stsd → avc1/avc3
// → avcC). Everything else is ignored.
func (m *MP4AnnexBReader) walkContainer(buf []byte) error {
	for pos := 0; pos+8 <= len(buf); {
		size := uint64(binary.BigEndian.Uint32(buf[pos : pos+4]))
		boxType := string(buf[pos+4 : pos+8])
		bodyStart := pos + 8
		var bodyEnd int
		switch {
		case size == 1:
			if pos+16 > len(buf) {
				return fmt.Errorf("truncated 64-bit box %q", boxType)
			}
			large := binary.BigEndian.Uint64(buf[pos+8 : pos+16])
			if large > uint64(len(buf)-pos) {
				return fmt.Errorf("box %q size %d overruns parent", boxType, large)
			}
			bodyStart = pos + 16
			bodyEnd = pos + int(large)
		case size == 0:
			bodyEnd = len(buf)
		default:
			if int(size) < 8 || pos+int(size) > len(buf) {
				return fmt.Errorf("box %q size %d invalid (parent has %d bytes left)", boxType, size, len(buf)-pos)
			}
			bodyEnd = pos + int(size)
		}
		body := buf[bodyStart:bodyEnd]

		switch boxType {
		case "trak", "mdia", "minf", "stbl":
			if err := m.walkContainer(body); err != nil {
				return err
			}
		case "stsd":
			// Sample Description Box: 1-byte version + 3-byte flags
			// + 4-byte entry_count + entries. Each entry is itself a
			// box, so once we skip the 8-byte header we treat the
			// remainder as a container.
			if len(body) >= 8 {
				if err := m.walkContainer(body[8:]); err != nil {
					return err
				}
			}
		case "avc1", "avc3":
			// Visual Sample Entry has a fixed 78-byte header before
			// nested boxes start. Inside is avcC plus optional
			// extras (btrt, pasp, etc.) — we only care about avcC.
			const sampleEntryHeaderSize = 78
			if len(body) > sampleEntryHeaderSize {
				if err := m.walkContainer(body[sampleEntryHeaderSize:]); err != nil {
					return err
				}
			}
		case "avcC":
			if err := m.parseAvcC(body); err != nil {
				return err
			}
		}
		pos = bodyEnd
	}
	return nil
}

// parseAvcC decodes the AVCDecoderConfigurationRecord (ISO/IEC
// 14496-15 §5.2.4) and stores SPS + PPS for emission ahead of the
// first frame. Layout (offset : meaning):
//
//	0  : configurationVersion (1)
//	1  : AVCProfileIndication
//	2  : profile_compatibility
//	3  : AVCLevelIndication
//	4  : 0b111111 lengthSizeMinusOne (last 2 bits)  → byte width of mdat NAL length prefixes
//	5  : 0b111   numOfSequenceParameterSets (last 5 bits)
//	6+ : repeated [u16 sps_len BE][sps_len bytes]
//	     1-byte numOfPictureParameterSets
//	     repeated [u16 pps_len BE][pps_len bytes]
func (m *MP4AnnexBReader) parseAvcC(body []byte) error {
	if len(body) < 7 {
		return fmt.Errorf("avcC too short (%d bytes)", len(body))
	}
	m.nalUnitLen = int(body[4]&0x03) + 1
	if m.nalUnitLen != 1 && m.nalUnitLen != 2 && m.nalUnitLen != 4 {
		return fmt.Errorf("avcC bad lengthSizeMinusOne field (%d)", body[4]&0x03)
	}
	pos := 5
	numSPS := int(body[pos] & 0x1f)
	pos++
	for i := 0; i < numSPS; i++ {
		if pos+2 > len(body) {
			return fmt.Errorf("avcC truncated sps header")
		}
		spsLen := int(binary.BigEndian.Uint16(body[pos : pos+2]))
		pos += 2
		if pos+spsLen > len(body) {
			return fmt.Errorf("avcC truncated sps body")
		}
		m.sps = append(m.sps, append([]byte(nil), body[pos:pos+spsLen]...))
		pos += spsLen
	}
	if pos >= len(body) {
		return nil
	}
	numPPS := int(body[pos])
	pos++
	for i := 0; i < numPPS; i++ {
		if pos+2 > len(body) {
			return fmt.Errorf("avcC truncated pps header")
		}
		ppsLen := int(binary.BigEndian.Uint16(body[pos : pos+2]))
		pos += 2
		if pos+ppsLen > len(body) {
			return fmt.Errorf("avcC truncated pps body")
		}
		m.pps = append(m.pps, append([]byte(nil), body[pos:pos+ppsLen]...))
		pos += ppsLen
	}
	return nil
}

// parseMdat walks an mdat body's AVCC-formatted NAL units and queues
// them. Each entry is `[lengthSizeMinusOne+1 bytes BE size][N bytes
// of NAL]`. Streamed: we read piece by piece so the whole mdat never
// has to fit in memory (xcrun emits ~100 KB per fragment, which is
// fine, but a ringbuffer-style stream eventually grows large).
func (m *MP4AnnexBReader) parseMdat(size uint64) error {
	if m.nalUnitLen == 0 {
		// avcC wasn't seen yet — fall back to the standard 4-byte
		// length field and hope the next moov fixes the SPS gap.
		m.nalUnitLen = 4
	}
	remaining := size
	for remaining > 0 {
		if remaining < uint64(m.nalUnitLen) {
			return skipBytes(m.r, remaining)
		}
		lenBuf := make([]byte, m.nalUnitLen)
		if _, err := io.ReadFull(m.r, lenBuf); err != nil {
			return err
		}
		var nalLen uint64
		switch m.nalUnitLen {
		case 1:
			nalLen = uint64(lenBuf[0])
		case 2:
			nalLen = uint64(binary.BigEndian.Uint16(lenBuf))
		case 4:
			nalLen = uint64(binary.BigEndian.Uint32(lenBuf))
		}
		remaining -= uint64(m.nalUnitLen)
		if nalLen > remaining {
			return fmt.Errorf("mdat NAL length %d exceeds remaining %d", nalLen, remaining)
		}
		if nalLen == 0 {
			continue
		}
		nal := make([]byte, nalLen)
		if _, err := io.ReadFull(m.r, nal); err != nil {
			return err
		}
		remaining -= nalLen
		m.queue = append(m.queue, NALUnit{Data: nal, Type: nal[0] & 0x1f})
	}
	return nil
}

// readBoxHeader reads the next ISO BMFF box header from r. Returns
// the BODY size (i.e. payload bytes only, excluding the 8 or 16
// header bytes already consumed) so callers can directly limit reads
// or skips. Boxes with size==0 (run-to-EOF) are returned with body
// size = math.MaxUint64 so the caller can use io.Copy to drain.
func readBoxHeader(r *bufio.Reader) (uint64, string, error) {
	hdr := make([]byte, 8)
	if _, err := io.ReadFull(r, hdr); err != nil {
		return 0, "", err
	}
	size := uint64(binary.BigEndian.Uint32(hdr[0:4]))
	boxType := string(hdr[4:8])
	switch size {
	case 0:
		// Box extends to end of stream — common for the outermost
		// mdat in an unfragmented MP4 (irrelevant here, but
		// supported for robustness).
		return ^uint64(0), boxType, nil
	case 1:
		// 64-bit large size follows.
		large := make([]byte, 8)
		if _, err := io.ReadFull(r, large); err != nil {
			return 0, boxType, err
		}
		full := binary.BigEndian.Uint64(large)
		if full < 16 {
			return 0, boxType, fmt.Errorf("box %q advertises 64-bit size %d < 16", boxType, full)
		}
		return full - 16, boxType, nil
	default:
		if size < 8 {
			return 0, boxType, fmt.Errorf("box %q advertises 32-bit size %d < 8", boxType, size)
		}
		return size - 8, boxType, nil
	}
}

// readN reads exactly n bytes from r. Used for moov bodies which
// must be parsed in full to find avcC.
func readN(r *bufio.Reader, n uint64) ([]byte, error) {
	if n == ^uint64(0) {
		return nil, fmt.Errorf("refusing to load run-to-EOF box into memory")
	}
	if n > 32*1024*1024 {
		return nil, fmt.Errorf("refusing to load %d-byte box (>32 MB) into memory", n)
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(r, buf); err != nil {
		return nil, err
	}
	return buf, nil
}

// skipBytes drops exactly n bytes from r without buffering them.
// Special-cases the run-to-EOF marker by draining to discard.
func skipBytes(r *bufio.Reader, n uint64) error {
	if n == ^uint64(0) {
		_, err := io.Copy(io.Discard, r)
		return err
	}
	const chunk = 32 * 1024
	buf := make([]byte, chunk)
	for n > 0 {
		take := uint64(chunk)
		if n < take {
			take = n
		}
		if _, err := io.ReadFull(r, buf[:take]); err != nil {
			return err
		}
		n -= take
	}
	return nil
}
