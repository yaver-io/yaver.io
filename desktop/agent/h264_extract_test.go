package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"
)

func TestAnnexBReader_SplitsStandardStream(t *testing.T) {
	// Three NALs: SPS (7), PPS (8), IDR slice (5). 4-byte start codes
	// throughout. This is the canonical shape adb screenrecord emits
	// on the wire: parameter sets followed by encoded picture data.
	data := []byte{
		0, 0, 0, 1, 0x67, 0x42, 0x00, 0x1f,
		0, 0, 0, 1, 0x68, 0xce, 0x3c, 0x80,
		0, 0, 0, 1, 0x65, 0xb8, 0x01, 0x02,
	}
	r := NewAnnexBReader(bytes.NewReader(data))
	var nals []NALUnit
	for {
		n, err := r.Next(context.Background())
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		nals = append(nals, n)
	}
	if got, want := len(nals), 3; got != want {
		t.Fatalf("nal count: got %d want %d", got, want)
	}
	if nals[0].Type != 7 {
		t.Errorf("nals[0].Type=%d want 7 (SPS)", nals[0].Type)
	}
	if nals[1].Type != 8 {
		t.Errorf("nals[1].Type=%d want 8 (PPS)", nals[1].Type)
	}
	if nals[2].Type != 5 {
		t.Errorf("nals[2].Type=%d want 5 (IDR)", nals[2].Type)
	}
	if !nals[2].IsKeyframe() {
		t.Error("IDR slice should be flagged as a keyframe")
	}
	if !nals[0].IsKeyframe() || !nals[1].IsKeyframe() {
		t.Error("SPS/PPS should be flagged as keyframes")
	}
	// Type 1 (non-IDR slice) is the canonical non-keyframe.
	non := NALUnit{Data: []byte{0x01, 0x00}, Type: 1}
	if non.IsKeyframe() {
		t.Error("type 1 NAL should not be a keyframe")
	}
}

func TestAnnexBReader_HandlesMixedStartCodeLengths(t *testing.T) {
	// 3-byte and 4-byte start codes intermixed. screenrecord emits
	// both depending on Android version.
	data := []byte{
		0, 0, 1, 0x09, 0xf0, // 3-byte start, AUD (type 9)
		0, 0, 0, 1, 0x67, 0x42, // 4-byte start, SPS (type 7)
		0, 0, 1, 0x65, 0x88, // 3-byte start, IDR (type 5)
	}
	r := NewAnnexBReader(bytes.NewReader(data))
	var types []uint8
	for {
		n, err := r.Next(context.Background())
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		types = append(types, n.Type)
	}
	want := []uint8{9, 7, 5}
	if len(types) != len(want) {
		t.Fatalf("types=%v want %v", types, want)
	}
	for i := range want {
		if types[i] != want[i] {
			t.Errorf("types[%d]=%d want %d", i, types[i], want[i])
		}
	}
}

func TestAnnexBReader_HandlesEmptyStream(t *testing.T) {
	r := NewAnnexBReader(bytes.NewReader(nil))
	_, err := r.Next(context.Background())
	if !errors.Is(err, io.EOF) {
		t.Errorf("got %v, want io.EOF", err)
	}
}

func TestAnnexBReader_NoStartCodeAtStart(t *testing.T) {
	// Some emitters (or stream resumption after a relay reconnect)
	// drop the leading start code and open with a raw NAL payload.
	// The reader should still produce a usable first NAL whose Type
	// comes from the first byte.
	data := []byte{
		0x67, 0x42, 0x00, // raw SPS
		0, 0, 0, 1, 0x65, 0xb8, // start code + IDR
	}
	r := NewAnnexBReader(bytes.NewReader(data))
	n1, err := r.Next(context.Background())
	if err != nil {
		t.Fatalf("first Next: %v", err)
	}
	if n1.Type != 7 {
		t.Errorf("first NAL Type=%d want 7", n1.Type)
	}
	n2, err := r.Next(context.Background())
	if err != nil {
		t.Fatalf("second Next: %v", err)
	}
	if n2.Type != 5 {
		t.Errorf("second NAL Type=%d want 5", n2.Type)
	}
	_, err = r.Next(context.Background())
	if !errors.Is(err, io.EOF) {
		t.Errorf("third Next: got %v want io.EOF", err)
	}
}

func TestAnnexBReader_TrailingPartialNAL(t *testing.T) {
	// EOF mid-NAL — the partial bytes should still come out so the
	// caller can decide whether to log + skip or abort. Common when
	// the capture process gets SIGINT'd.
	data := []byte{
		0, 0, 0, 1, 0x67, 0x42, 0x00, 0x1f, // complete SPS
		0, 0, 0, 1, 0x65, 0xb8, // truncated IDR
	}
	r := NewAnnexBReader(bytes.NewReader(data))
	first, err := r.Next(context.Background())
	if err != nil {
		t.Fatalf("first NAL: %v", err)
	}
	if first.Type != 7 {
		t.Errorf("first NAL Type=%d want 7", first.Type)
	}
	second, err := r.Next(context.Background())
	if err != nil {
		t.Fatalf("second NAL: %v", err)
	}
	if second.Type != 5 {
		t.Errorf("second NAL Type=%d want 5", second.Type)
	}
	_, err = r.Next(context.Background())
	if !errors.Is(err, io.EOF) {
		t.Errorf("after EOF: got %v want io.EOF", err)
	}
}

func TestAnnexBReader_RespectsContextCancel(t *testing.T) {
	// Already-cancelled ctx must short-circuit Next() without
	// touching the underlying reader. We give it a real (but unused)
	// byte buffer so any accidental read would surface as a passing
	// stream rather than a hang.
	r := NewAnnexBReader(bytes.NewReader([]byte{0, 0, 0, 1, 0x67, 0x42}))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := r.Next(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("got %v, want context.Canceled", err)
	}
}

func TestMP4ToAnnexB_RejectsNilReader(t *testing.T) {
	if _, err := MP4ToAnnexB(nil); err == nil {
		t.Error("nil reader should fail")
	}
}

func TestMP4ToAnnexB_EmitsParameterSetsThenFrameNALs(t *testing.T) {
	// Synthetic fragmented MP4 with the boxes we actually parse:
	// ftyp (skipped), moov→…→avcC (SPS+PPS extraction), then mdat
	// with two AVCC-formatted NAL units. Verifies the public
	// contract: parameter sets first, then mdat NALs in order.
	sps := []byte{0x67, 0x42, 0x00, 0x1f, 0xab}
	pps := []byte{0x68, 0xce, 0x3c, 0x80}
	idr := []byte{0x65, 0xb8, 0x01, 0x02, 0x03}
	non := []byte{0x41, 0x9b, 0x00, 0x10}

	stream := buildSyntheticFragmentedMP4(t, sps, pps, [][]byte{idr, non})

	r, err := MP4ToAnnexB(bytes.NewReader(stream))
	if err != nil {
		t.Fatalf("MP4ToAnnexB: %v", err)
	}

	want := []NALUnit{
		{Data: sps, Type: 7},
		{Data: pps, Type: 8},
		{Data: idr, Type: 5},
		{Data: non, Type: 1},
	}
	for i, w := range want {
		got, err := r.Next(context.Background())
		if err != nil {
			t.Fatalf("Next[%d]: %v", i, err)
		}
		if got.Type != w.Type {
			t.Errorf("nal[%d].Type=%d want %d", i, got.Type, w.Type)
		}
		if !bytes.Equal(got.Data, w.Data) {
			t.Errorf("nal[%d].Data=% x want % x", i, got.Data, w.Data)
		}
	}
	// Stream is drained — next call should be EOF.
	if _, err := r.Next(context.Background()); !errors.Is(err, io.EOF) {
		t.Errorf("after drain: got %v, want io.EOF", err)
	}
}

func TestMP4ToAnnexB_HandlesMissingPPS(t *testing.T) {
	// avcC with SPS but zero PPS — legal per spec, the only NALs
	// emitted before frame data should be the SPS.
	sps := []byte{0x67, 0x42, 0x00, 0x1f}
	stream := buildSyntheticFragmentedMP4(t, sps, nil, [][]byte{{0x65, 0xaa}})
	r, err := MP4ToAnnexB(bytes.NewReader(stream))
	if err != nil {
		t.Fatal(err)
	}
	first, err := r.Next(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if first.Type != 7 {
		t.Errorf("first NAL=%d want SPS(7)", first.Type)
	}
	second, err := r.Next(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if second.Type != 5 {
		t.Errorf("second NAL=%d want IDR(5) (no PPS in this stream)", second.Type)
	}
}

func TestMP4ToAnnexB_RejectsTooDeepBoxBeforeMdat(t *testing.T) {
	// avcC absent → mdat parse must still proceed using the default
	// 4-byte length prefix. xcrun never produces a stream like this
	// in practice (its first moov always carries avcC) but the
	// parser shouldn't crash if it does.
	stream := buildSyntheticFragmentedMP4(t, nil, nil, [][]byte{{0x65, 0xaa, 0xbb}})
	r, err := MP4ToAnnexB(bytes.NewReader(stream))
	if err != nil {
		t.Fatal(err)
	}
	got, err := r.Next(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got.Type != 5 {
		t.Errorf("got NAL type %d, want 5", got.Type)
	}
}

func TestMP4ToAnnexB_TruncatedMdatLength(t *testing.T) {
	// Build a valid stream then truncate the mdat body so the
	// length prefix promises more bytes than exist. Reader should
	// return an error rather than hang or return garbage.
	stream := buildSyntheticFragmentedMP4(t, nil, nil, [][]byte{{0x65, 0xaa, 0xbb, 0xcc}})
	// Cut off the last 2 bytes of the NAL payload.
	truncated := stream[:len(stream)-2]
	r, err := MP4ToAnnexB(bytes.NewReader(truncated))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Next(context.Background()); err == nil {
		t.Error("truncated mdat should error")
	}
}

// buildSyntheticFragmentedMP4 emits a tiny ISO BMFF stream that
// exercises every code path in MP4AnnexBReader without reaching for
// a binary fixture file. Layout:
//
//	ftyp        — empty body, parser skips
//	moov
//	  trak
//	    mdia
//	      minf
//	        stbl
//	          stsd  (1 entry: avc1 → avcC with SPS+PPS)
//	mdat        — AVCC-formatted [4-byte BE length][NAL] for each frame
//
// SPS == nil omits SPS entries; PPS == nil omits the PPS section
// entirely (lengthSizeMinusOne=3 still gets written so mdat parses).
// frames is the list of raw NALs to write into mdat in order.
func buildSyntheticFragmentedMP4(t *testing.T, sps, pps []byte, frames [][]byte) []byte {
	t.Helper()

	box := func(name string, body []byte) []byte {
		if len(name) != 4 {
			t.Fatalf("box name %q must be 4 bytes", name)
		}
		out := make([]byte, 8+len(body))
		size := uint32(len(out))
		out[0] = byte(size >> 24)
		out[1] = byte(size >> 16)
		out[2] = byte(size >> 8)
		out[3] = byte(size)
		copy(out[4:8], name)
		copy(out[8:], body)
		return out
	}

	// avcC body: configurationVersion + profile + compat + level +
	// (lengthSizeMinusOne=3 → 4-byte mdat length prefixes) + numSPS
	// + [u16 spslen + sps] + numPPS + [u16 ppslen + pps].
	avcC := []byte{0x01, 0x42, 0x00, 0x1f, 0xff}
	if sps == nil {
		avcC = append(avcC, 0xe0) // numSPS = 0
	} else {
		avcC = append(avcC, 0xe1) // numSPS = 1
		avcC = append(avcC, byte(len(sps)>>8), byte(len(sps)))
		avcC = append(avcC, sps...)
	}
	if pps == nil {
		avcC = append(avcC, 0x00) // numPPS = 0
	} else {
		avcC = append(avcC, 0x01) // numPPS = 1
		avcC = append(avcC, byte(len(pps)>>8), byte(len(pps)))
		avcC = append(avcC, pps...)
	}

	// Visual sample entry: 78 fixed bytes, then nested avcC.
	sampleEntryHeader := make([]byte, 78)
	avc1 := append(sampleEntryHeader, box("avcC", avcC)...)

	// stsd: 4-byte version+flags + 4-byte entry_count + entries.
	stsd := append([]byte{0, 0, 0, 0, 0, 0, 0, 1}, box("avc1", avc1)...)

	stbl := box("stsd", stsd)
	minf := box("stbl", stbl)
	mdia := box("minf", minf)
	trak := box("mdia", mdia)
	moov := box("moov", box("trak", trak))

	// mdat: [4-byte BE length][NAL] for each frame.
	var mdatBody []byte
	for _, f := range frames {
		ln := uint32(len(f))
		mdatBody = append(mdatBody,
			byte(ln>>24), byte(ln>>16), byte(ln>>8), byte(ln))
		mdatBody = append(mdatBody, f...)
	}
	mdat := box("mdat", mdatBody)

	// Skipped boxes prove the top-level loop drains them: a one-byte
	// `free` box (size=8, empty body) before the moov, plus a nonsense
	// `stub` box after moov that the parser must ignore without
	// breaking the mdat that follows.
	free := box("free", nil)
	stub := box("uuid", []byte{0xde, 0xad, 0xbe, 0xef})
	ftyp := box("ftyp", []byte("isom\x00\x00\x00\x00mp42"))

	var stream []byte
	stream = append(stream, ftyp...)
	stream = append(stream, free...)
	stream = append(stream, moov...)
	stream = append(stream, stub...)
	stream = append(stream, mdat...)
	return stream
}
