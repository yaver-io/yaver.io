package brain

// CBOR encoder + decoder, deterministic CTAP2 subset.
//
// Mirrors core/src/cbor.c byte-for-byte. Same major types, same
// shortest-encoding rules, same indefinite-length rejection. The
// goal is that for any input value, the Go encoder produces the
// same bytes that the C encoder produces — that's what makes
// brain ↔ device parity testable.
//
// Subset:
//   - uint (major 0)
//   - negint (major 1)
//   - bytes (major 2, definite-length only)
//   - text  (major 3, definite-length only)
//   - array (major 4, definite-length only)
//   - map   (major 5, definite-length only; CALLER orders keys)
//   - bool  (major 7, simples 20/21)
//   - null  (major 7, simple 22)
//
// Out: tagged values, indefinite-length items, floats, bignums.

import (
	"encoding/binary"
)

// CBOR major types.
const (
	cborMtUint   = 0
	cborMtNegInt = 1
	cborMtBytes  = 2
	cborMtText   = 3
	cborMtArray  = 4
	cborMtMap    = 5
	cborMtTag    = 6
	cborMtSimple = 7
)

// Initial-byte additional info encoding lengths.
const (
	cborInfoInlineMax = 23
	cborInfoU8        = 24
	cborInfoU16       = 25
	cborInfoU32       = 26
	cborInfoU64       = 27
)

// CBORWriter is a sticky-error encoder over a caller-provided
// buffer. Same shape as yvr_cbor_w_t in C: once Err is set, all
// subsequent writes no-op.
type CBORWriter struct {
	buf []byte
	pos int
	Err error
}

// NewCBORWriter wraps buf for write-side use.
func NewCBORWriter(buf []byte) *CBORWriter {
	return &CBORWriter{buf: buf}
}

// Bytes returns the encoded bytes so far. Slice aliases the
// caller's buffer — copy if mutation is a concern.
func (w *CBORWriter) Bytes() []byte {
	return w.buf[:w.pos]
}

// Len returns the number of bytes written so far.
func (w *CBORWriter) Len() int {
	return w.pos
}

func (w *CBORWriter) putByte(b byte) {
	if w.Err != nil {
		return
	}
	if w.pos+1 > len(w.buf) {
		w.Err = ErrBufferTooSmall
		return
	}
	w.buf[w.pos] = b
	w.pos++
}

func (w *CBORWriter) put(b []byte) {
	if w.Err != nil {
		return
	}
	if w.pos+len(b) > len(w.buf) {
		w.Err = ErrBufferTooSmall
		return
	}
	copy(w.buf[w.pos:], b)
	w.pos += len(b)
}

// emitHead writes the head byte for a CBOR item with major type
// mt and unsigned argument v in shortest form.
func (w *CBORWriter) emitHead(mt byte, v uint64) {
	if w.Err != nil {
		return
	}
	mtTop := byte((mt & 0x07) << 5)

	switch {
	case v <= cborInfoInlineMax:
		w.putByte(mtTop | byte(v))
	case v <= 0xFF:
		w.put([]byte{mtTop | cborInfoU8, byte(v)})
	case v <= 0xFFFF:
		var b [3]byte
		b[0] = mtTop | cborInfoU16
		binary.BigEndian.PutUint16(b[1:], uint16(v))
		w.put(b[:])
	case v <= 0xFFFFFFFF:
		var b [5]byte
		b[0] = mtTop | cborInfoU32
		binary.BigEndian.PutUint32(b[1:], uint32(v))
		w.put(b[:])
	default:
		var b [9]byte
		b[0] = mtTop | cborInfoU64
		binary.BigEndian.PutUint64(b[1:], v)
		w.put(b[:])
	}
}

// WriteUint writes major type 0 (unsigned int).
func (w *CBORWriter) WriteUint(v uint64) {
	w.emitHead(cborMtUint, v)
}

// WriteInt writes either major 0 (positive) or major 1 (negative)
// per the CBOR convention. Math is identical to the C side:
// negative values encode as -(v+1) under major 1.
func (w *CBORWriter) WriteInt(v int64) {
	if v >= 0 {
		w.emitHead(cborMtUint, uint64(v))
	} else {
		// -(v+1) where v can be MinInt64. -(MinInt64+1) = MaxInt64.
		w.emitHead(cborMtNegInt, uint64(-(v + 1)))
	}
}

// WriteBytes writes a CBOR byte string (major 2).
func (w *CBORWriter) WriteBytes(b []byte) {
	if b == nil && len(b) == 0 {
		// Match C side: NULL+0 is allowed and becomes 0x40.
	}
	w.emitHead(cborMtBytes, uint64(len(b)))
	w.put(b)
}

// WriteText writes a CBOR text string (major 3). UTF-8 not
// validated — same as C.
func (w *CBORWriter) WriteText(s string) {
	w.emitHead(cborMtText, uint64(len(s)))
	if w.Err != nil {
		return
	}
	if w.pos+len(s) > len(w.buf) {
		w.Err = ErrBufferTooSmall
		return
	}
	copy(w.buf[w.pos:], s)
	w.pos += len(s)
}

// BeginArray writes the array head with the given count. The
// caller writes n more items afterwards.
func (w *CBORWriter) BeginArray(n int) {
	w.emitHead(cborMtArray, uint64(n))
}

// BeginMap writes the map head with the given key-value pair count.
// The caller writes 2*n more items (alternating keys + values) in
// CTAP2-deterministic order.
func (w *CBORWriter) BeginMap(n int) {
	w.emitHead(cborMtMap, uint64(n))
}

// WriteBool writes a CBOR boolean (major 7, simple 20/21).
func (w *CBORWriter) WriteBool(v bool) {
	if v {
		w.putByte(0xF5)
	} else {
		w.putByte(0xF4)
	}
}

// WriteNull writes the null simple (major 7, simple 22).
func (w *CBORWriter) WriteNull() {
	w.putByte(0xF6)
}

// ── Decoder ────────────────────────────────────────────────────

// CBORKind classifies the next item to be read.
type CBORKind int

const (
	CBORKindNone  CBORKind = 0
	CBORKindUint  CBORKind = 1
	CBORKindInt   CBORKind = 2 // negative integer
	CBORKindBytes CBORKind = 3
	CBORKindText  CBORKind = 4
	CBORKindArray CBORKind = 5
	CBORKindMap   CBORKind = 6
	CBORKindBool  CBORKind = 7
	CBORKindNull  CBORKind = 8
)

// CBORReader is a pull-style reader over a caller-provided buffer.
// Pointers returned by ReadBytes / ReadText alias the input —
// caller copies if the input might be reused.
type CBORReader struct {
	buf []byte
	pos int
	Err error
}

// NewCBORReader wraps buf for read-side use.
func NewCBORReader(buf []byte) *CBORReader {
	return &CBORReader{buf: buf}
}

// Pos returns the next byte offset to read from. Useful for
// computing how much of buf was consumed.
func (r *CBORReader) Pos() int { return r.pos }

func (r *CBORReader) takeByte() (byte, error) {
	if r.Err != nil {
		return 0, r.Err
	}
	if r.pos+1 > len(r.buf) {
		r.Err = ErrTruncated
		return 0, r.Err
	}
	b := r.buf[r.pos]
	r.pos++
	return b, nil
}

func (r *CBORReader) take(n int) ([]byte, error) {
	if r.Err != nil {
		return nil, r.Err
	}
	if r.pos+n > len(r.buf) {
		r.Err = ErrTruncated
		return nil, r.Err
	}
	b := r.buf[r.pos : r.pos+n]
	r.pos += n
	return b, nil
}

// readHead consumes one CBOR item's head bytes and returns its
// major type + unsigned argument. Rejects indefinite-length and
// reserved info widths.
func (r *CBORReader) readHead() (mt byte, v uint64, err error) {
	b, err := r.takeByte()
	if err != nil {
		return 0, 0, err
	}
	mt = byte(b>>5) & 0x07
	info := byte(b & 0x1F)

	switch {
	case info <= cborInfoInlineMax:
		v = uint64(info)
	case info == cborInfoU8:
		var bb byte
		bb, err = r.takeByte()
		if err != nil {
			return 0, 0, err
		}
		v = uint64(bb)
	case info == cborInfoU16:
		var bb []byte
		bb, err = r.take(2)
		if err != nil {
			return 0, 0, err
		}
		v = uint64(binary.BigEndian.Uint16(bb))
	case info == cborInfoU32:
		var bb []byte
		bb, err = r.take(4)
		if err != nil {
			return 0, 0, err
		}
		v = uint64(binary.BigEndian.Uint32(bb))
	case info == cborInfoU64:
		var bb []byte
		bb, err = r.take(8)
		if err != nil {
			return 0, 0, err
		}
		v = binary.BigEndian.Uint64(bb)
	default:
		// info 28..30 reserved, 31 indefinite — both rejected.
		r.Err = ErrBadFrame
		return 0, 0, ErrBadFrame
	}
	return mt, v, nil
}

// Peek reports the kind of the next item without consuming it.
func (r *CBORReader) Peek() (CBORKind, error) {
	if r.Err != nil {
		return CBORKindNone, r.Err
	}
	if r.pos >= len(r.buf) {
		return CBORKindNone, ErrTruncated
	}
	b := r.buf[r.pos]
	mt := byte(b>>5) & 0x07
	if mt == cborMtSimple {
		switch b {
		case 0xF4, 0xF5:
			return CBORKindBool, nil
		case 0xF6:
			return CBORKindNull, nil
		default:
			return CBORKindNone, ErrBadFrame
		}
	}
	if mt == cborMtTag {
		return CBORKindNone, ErrBadFrame
	}
	switch mt {
	case cborMtUint:
		return CBORKindUint, nil
	case cborMtNegInt:
		return CBORKindInt, nil
	case cborMtBytes:
		return CBORKindBytes, nil
	case cborMtText:
		return CBORKindText, nil
	case cborMtArray:
		return CBORKindArray, nil
	case cborMtMap:
		return CBORKindMap, nil
	}
	return CBORKindNone, ErrBadFrame
}

// ReadUint reads a major-0 item.
func (r *CBORReader) ReadUint() (uint64, error) {
	mt, v, err := r.readHead()
	if err != nil {
		return 0, err
	}
	if mt != cborMtUint {
		r.Err = ErrBadFrame
		return 0, ErrBadFrame
	}
	return v, nil
}

// ReadInt reads either a major-0 or major-1 item, returning a
// signed result. Rejects values that don't fit in int64.
func (r *CBORReader) ReadInt() (int64, error) {
	mt, v, err := r.readHead()
	if err != nil {
		return 0, err
	}
	switch mt {
	case cborMtUint:
		if v > 1<<63-1 {
			r.Err = ErrBadFrame
			return 0, ErrBadFrame
		}
		return int64(v), nil
	case cborMtNegInt:
		if v > 1<<63-1 {
			r.Err = ErrBadFrame
			return 0, ErrBadFrame
		}
		return -1 - int64(v), nil
	}
	r.Err = ErrBadFrame
	return 0, ErrBadFrame
}

func (r *CBORReader) readString(expectedMt byte) ([]byte, error) {
	mt, v, err := r.readHead()
	if err != nil {
		return nil, err
	}
	if mt != expectedMt {
		r.Err = ErrBadFrame
		return nil, ErrBadFrame
	}
	if v > uint64(len(r.buf)) {
		r.Err = ErrBadFrame
		return nil, ErrBadFrame
	}
	return r.take(int(v))
}

// ReadBytes reads a major-2 byte string. Returned slice aliases
// the input buffer.
func (r *CBORReader) ReadBytes() ([]byte, error) {
	return r.readString(cborMtBytes)
}

// ReadText reads a major-3 text string. Returned string is a copy
// (Go strings are immutable, the bytes are aliased internally).
func (r *CBORReader) ReadText() (string, error) {
	b, err := r.readString(cborMtText)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// ReadArrayBegin reads a major-4 array head, returning element count.
func (r *CBORReader) ReadArrayBegin() (int, error) {
	mt, v, err := r.readHead()
	if err != nil {
		return 0, err
	}
	if mt != cborMtArray {
		r.Err = ErrBadFrame
		return 0, ErrBadFrame
	}
	if v > uint64(int(^uint(0)>>1)) {
		r.Err = ErrBadFrame
		return 0, ErrBadFrame
	}
	return int(v), nil
}

// ReadMapBegin reads a major-5 map head, returning kv-pair count.
func (r *CBORReader) ReadMapBegin() (int, error) {
	mt, v, err := r.readHead()
	if err != nil {
		return 0, err
	}
	if mt != cborMtMap {
		r.Err = ErrBadFrame
		return 0, ErrBadFrame
	}
	if v > uint64(int(^uint(0)>>1)) {
		r.Err = ErrBadFrame
		return 0, ErrBadFrame
	}
	return int(v), nil
}

// ReadBool reads major-7 simples 20 / 21.
func (r *CBORReader) ReadBool() (bool, error) {
	b, err := r.takeByte()
	if err != nil {
		return false, err
	}
	switch b {
	case 0xF4:
		return false, nil
	case 0xF5:
		return true, nil
	}
	r.Err = ErrBadFrame
	return false, ErrBadFrame
}

// ReadNull reads major-7 simple 22.
func (r *CBORReader) ReadNull() error {
	b, err := r.takeByte()
	if err != nil {
		return err
	}
	if b != 0xF6 {
		r.Err = ErrBadFrame
		return ErrBadFrame
	}
	return nil
}

// Skip consumes exactly one item (recursive for arrays and maps).
// Used by body decoders to ignore unknown-but-well-formed fields.
func (r *CBORReader) Skip() error {
	k, err := r.Peek()
	if err != nil {
		return err
	}
	switch k {
	case CBORKindUint:
		_, err = r.ReadUint()
	case CBORKindInt:
		_, err = r.ReadInt()
	case CBORKindBytes:
		_, err = r.ReadBytes()
	case CBORKindText:
		_, err = r.ReadText()
	case CBORKindBool:
		_, err = r.ReadBool()
	case CBORKindNull:
		err = r.ReadNull()
	case CBORKindArray:
		var n int
		if n, err = r.ReadArrayBegin(); err != nil {
			return err
		}
		for i := 0; i < n; i++ {
			if err = r.Skip(); err != nil {
				return err
			}
		}
	case CBORKindMap:
		var n int
		if n, err = r.ReadMapBegin(); err != nil {
			return err
		}
		for i := 0; i < 2*n; i++ {
			if err = r.Skip(); err != nil {
				return err
			}
		}
	default:
		r.Err = ErrBadFrame
		return ErrBadFrame
	}
	return err
}
