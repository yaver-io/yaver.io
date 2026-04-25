package brain

import (
	"bytes"
	"testing"
)

// Vectors copied from embedded/c-agent/tests/test_cbor.c — same
// inputs, expected to produce same bytes. RFC 8949 Appendix A.

func encUint(t *testing.T, v uint64) []byte {
	t.Helper()
	w := NewCBORWriter(make([]byte, 16))
	w.WriteUint(v)
	if w.Err != nil {
		t.Fatalf("encode failed: %v", w.Err)
	}
	return w.Bytes()
}

func encInt(t *testing.T, v int64) []byte {
	t.Helper()
	w := NewCBORWriter(make([]byte, 16))
	w.WriteInt(v)
	if w.Err != nil {
		t.Fatalf("encode failed: %v", w.Err)
	}
	return w.Bytes()
}

func TestCBOR_UintVectors(t *testing.T) {
	cases := []struct {
		v   uint64
		exp []byte
	}{
		{0, []byte{0x00}},
		{1, []byte{0x01}},
		{10, []byte{0x0a}},
		{23, []byte{0x17}},
		{24, []byte{0x18, 0x18}},
		{25, []byte{0x18, 0x19}},
		{100, []byte{0x18, 0x64}},
		{1000, []byte{0x19, 0x03, 0xe8}},
		{1000000, []byte{0x1a, 0x00, 0x0f, 0x42, 0x40}},
		{1000000000000, []byte{0x1b, 0x00, 0x00, 0x00, 0xe8, 0xd4, 0xa5, 0x10, 0x00}},
		{0xFFFFFFFFFFFFFFFF, []byte{0x1b, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff}},
	}
	for _, c := range cases {
		got := encUint(t, c.v)
		if !bytes.Equal(got, c.exp) {
			t.Fatalf("uint(%d): got %x, want %x", c.v, got, c.exp)
		}
	}
}

func TestCBOR_IntVectors(t *testing.T) {
	cases := []struct {
		v   int64
		exp []byte
	}{
		{-1, []byte{0x20}},
		{-10, []byte{0x29}},
		{-100, []byte{0x38, 0x63}},
		{-1000, []byte{0x39, 0x03, 0xe7}},
		{1, []byte{0x01}},
		{-9223372036854775808, []byte{0x3b, 0x7f, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff}},
	}
	for _, c := range cases {
		got := encInt(t, c.v)
		if !bytes.Equal(got, c.exp) {
			t.Fatalf("int(%d): got %x, want %x", c.v, got, c.exp)
		}
	}
}

func TestCBOR_TextVectors(t *testing.T) {
	cases := []struct {
		s   string
		exp []byte
	}{
		{"", []byte{0x60}},
		{"a", []byte{0x61, 0x61}},
		{"IETF", []byte{0x64, 'I', 'E', 'T', 'F'}},
		{"\"\\", []byte{0x62, 0x22, 0x5c}},
	}
	for _, c := range cases {
		w := NewCBORWriter(make([]byte, 16))
		w.WriteText(c.s)
		if w.Err != nil {
			t.Fatalf("encode failed: %v", w.Err)
		}
		if !bytes.Equal(w.Bytes(), c.exp) {
			t.Fatalf("text(%q): got %x, want %x", c.s, w.Bytes(), c.exp)
		}
	}
}

func TestCBOR_BytesVectors(t *testing.T) {
	w := NewCBORWriter(make([]byte, 16))
	w.WriteBytes(nil)
	if !bytes.Equal(w.Bytes(), []byte{0x40}) {
		t.Fatalf("bytes(nil): got %x", w.Bytes())
	}

	w = NewCBORWriter(make([]byte, 16))
	w.WriteBytes([]byte{0x01, 0x02, 0x03, 0x04})
	expected := []byte{0x44, 0x01, 0x02, 0x03, 0x04}
	if !bytes.Equal(w.Bytes(), expected) {
		t.Fatalf("bytes: got %x, want %x", w.Bytes(), expected)
	}
}

func TestCBOR_ArrayMap(t *testing.T) {
	// {"a":1, "b":[2,3]} from RFC 8949.
	w := NewCBORWriter(make([]byte, 32))
	w.BeginMap(2)
	w.WriteText("a")
	w.WriteUint(1)
	w.WriteText("b")
	w.BeginArray(2)
	w.WriteUint(2)
	w.WriteUint(3)
	if w.Err != nil {
		t.Fatalf("encode failed: %v", w.Err)
	}
	expected := []byte{
		0xa2,
		0x61, 0x61, 0x01,
		0x61, 0x62, 0x82, 0x02, 0x03,
	}
	if !bytes.Equal(w.Bytes(), expected) {
		t.Fatalf("got %x, want %x", w.Bytes(), expected)
	}
}

func TestCBOR_Simple(t *testing.T) {
	cases := []struct {
		fn  func(*CBORWriter)
		exp byte
	}{
		{func(w *CBORWriter) { w.WriteBool(false) }, 0xF4},
		{func(w *CBORWriter) { w.WriteBool(true) }, 0xF5},
		{func(w *CBORWriter) { w.WriteNull() }, 0xF6},
	}
	for _, c := range cases {
		w := NewCBORWriter(make([]byte, 4))
		c.fn(w)
		if w.Err != nil || w.Len() != 1 || w.Bytes()[0] != c.exp {
			t.Fatalf("simple: got %x, want %x", w.Bytes(), c.exp)
		}
	}
}

func TestCBOR_Decode(t *testing.T) {
	// Encode {"v":1, "name":"yvr-c-agent", "now_ms":1700000000000}
	// then decode and verify every field matches.
	w := NewCBORWriter(make([]byte, 64))
	w.BeginMap(3)
	w.WriteText("v")
	w.WriteUint(1)
	w.WriteText("name")
	w.WriteText("yvr-c-agent")
	w.WriteText("now_ms")
	w.WriteUint(1700000000000)
	if w.Err != nil {
		t.Fatalf("encode failed: %v", w.Err)
	}

	r := NewCBORReader(w.Bytes())
	kv, err := r.ReadMapBegin()
	if err != nil || kv != 3 {
		t.Fatalf("ReadMapBegin: kv=%d err=%v", kv, err)
	}
	k, _ := r.ReadText()
	if k != "v" {
		t.Fatalf("k1=%q", k)
	}
	v1, _ := r.ReadUint()
	if v1 != 1 {
		t.Fatalf("v1=%d", v1)
	}
	k, _ = r.ReadText()
	if k != "name" {
		t.Fatalf("k2=%q", k)
	}
	s, _ := r.ReadText()
	if s != "yvr-c-agent" {
		t.Fatalf("name=%q", s)
	}
	k, _ = r.ReadText()
	if k != "now_ms" {
		t.Fatalf("k3=%q", k)
	}
	v3, _ := r.ReadUint()
	if v3 != 1700000000000 {
		t.Fatalf("now_ms=%d", v3)
	}
}

func TestCBOR_RejectIndefinite(t *testing.T) {
	// Indefinite-length byte string (0x5f) — must be rejected.
	r := NewCBORReader([]byte{0x5f, 0xff})
	if _, err := r.ReadBytes(); err != ErrBadFrame {
		t.Fatalf("got %v, want ErrBadFrame", err)
	}
}

func TestCBOR_RejectTagged(t *testing.T) {
	r := NewCBORReader([]byte{0xc0, 0x60})
	if _, err := r.Peek(); err != ErrBadFrame {
		t.Fatalf("got %v, want ErrBadFrame", err)
	}
}

func TestCBOR_Skip(t *testing.T) {
	// {"keep":42, "drop":[1,[2,3]], "tail":"x"}
	in := []byte{
		0xa3,
		0x64, 'k', 'e', 'e', 'p', 0x18, 0x2a,
		0x64, 'd', 'r', 'o', 'p', 0x82, 0x01, 0x82, 0x02, 0x03,
		0x64, 't', 'a', 'i', 'l', 0x61, 'x',
	}
	r := NewCBORReader(in)
	kv, _ := r.ReadMapBegin()
	if kv != 3 {
		t.Fatalf("kv=%d", kv)
	}
	if k, _ := r.ReadText(); k != "keep" {
		t.Fatalf("k1=%q", k)
	}
	if v, _ := r.ReadUint(); v != 42 {
		t.Fatalf("v1=%d", v)
	}
	if k, _ := r.ReadText(); k != "drop" {
		t.Fatalf("k2=%q", k)
	}
	if err := r.Skip(); err != nil {
		t.Fatalf("skip failed: %v", err)
	}
	if k, _ := r.ReadText(); k != "tail" {
		t.Fatalf("k3=%q", k)
	}
	if v, _ := r.ReadText(); v != "x" {
		t.Fatalf("v3=%q", v)
	}
	if r.Pos() != len(in) {
		t.Fatalf("pos=%d, want %d", r.Pos(), len(in))
	}
}
