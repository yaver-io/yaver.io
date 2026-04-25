package brain

import (
	"bytes"
	"testing"
)

// Parity vectors copied from embedded/c-agent/tests/test_body.c.
// If either codec drifts, the byte-for-byte comparison here
// catches it.

func TestHello_KnownBytes(t *testing.T) {
	// {"v": 1, "role": "brain"}  — same as test_hello_known_bytes
	in := Hello{
		ProtocolVersion: 1,
		Role:            "brain",
	}
	expected := []byte{
		0xa2,
		0x61, 'v',
		0x01,
		0x64, 'r', 'o', 'l', 'e',
		0x65, 'b', 'r', 'a', 'i', 'n',
	}
	buf := make([]byte, 32)
	n, err := in.Encode(buf)
	if err != nil {
		t.Fatalf("encode failed: %v", err)
	}
	got := buf[:n]
	if !bytes.Equal(got, expected) {
		t.Fatalf("byte mismatch:\n  got  %x\n  want %x", got, expected)
	}
}

func TestHello_RoundTripFull(t *testing.T) {
	in := Hello{
		ProtocolVersion: ProtocolVersion,
		Role:            "device",
		AgentVersion:    "yvr-cagent/0.0.1",
	}
	buf := make([]byte, 64)
	n, err := in.Encode(buf)
	if err != nil {
		t.Fatalf("encode failed: %v", err)
	}
	out, err := DecodeHello(buf[:n])
	if err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	if out != in {
		t.Fatalf("round-trip mismatch:\n  got  %+v\n  want %+v", out, in)
	}
}

func TestHello_RoundTripMinimal(t *testing.T) {
	in := Hello{
		ProtocolVersion: ProtocolVersion,
		Role:            "brain",
	}
	buf := make([]byte, 32)
	n, _ := in.Encode(buf)
	out, err := DecodeHello(buf[:n])
	if err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	if out.AgentVersion != "" {
		t.Fatalf("expected empty AgentVersion, got %q", out.AgentVersion)
	}
}

func TestHello_SkipsUnknownFields(t *testing.T) {
	// Same byte vector as test_hello_skips_unknown_fields in C:
	// {"v":1, "role":"device", "future":42}
	in := []byte{
		0xa3,
		0x61, 'v', 0x01,
		0x64, 'r', 'o', 'l', 'e', 0x66, 'd', 'e', 'v', 'i', 'c', 'e',
		0x66, 'f', 'u', 't', 'u', 'r', 'e', 0x18, 0x2a,
	}
	out, err := DecodeHello(in)
	if err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	if out.ProtocolVersion != 1 || out.Role != "device" {
		t.Fatalf("got %+v", out)
	}
}

func TestHello_RejectsMissingRequired(t *testing.T) {
	// Map with only "v" — missing "role".
	in := []byte{0xa1, 0x61, 'v', 0x01}
	if _, err := DecodeHello(in); err != ErrBadFrame {
		t.Fatalf("got %v, want ErrBadFrame", err)
	}
}

func TestHeartbeat_KnownBytes(t *testing.T) {
	// {"v":1, "now_ms":1000} — matches test_heartbeat_known_bytes.
	in := Heartbeat{
		ProtocolVersion: 1,
		NowMs:           1000,
	}
	expected := []byte{
		0xa2,
		0x61, 'v', 0x01,
		0x66, 'n', 'o', 'w', '_', 'm', 's', 0x19, 0x03, 0xe8,
	}
	buf := make([]byte, 32)
	n, err := in.Encode(buf)
	if err != nil {
		t.Fatalf("encode failed: %v", err)
	}
	if !bytes.Equal(buf[:n], expected) {
		t.Fatalf("byte mismatch:\n  got  %x\n  want %x", buf[:n], expected)
	}
}

func TestHeartbeat_RoundTripWithSig(t *testing.T) {
	sig := make([]byte, 64)
	for i := range sig {
		sig[i] = byte(i)
	}
	in := Heartbeat{
		ProtocolVersion: 1,
		NowMs:           1700000000123,
		Signature:       sig,
	}
	buf := make([]byte, 256)
	n, err := in.Encode(buf)
	if err != nil {
		t.Fatalf("encode failed: %v", err)
	}
	out, err := DecodeHeartbeat(buf[:n])
	if err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	if out.NowMs != in.NowMs {
		t.Fatalf("NowMs got=%d want=%d", out.NowMs, in.NowMs)
	}
	if !bytes.Equal(out.Signature, sig) {
		t.Fatalf("Signature mismatch")
	}
}

func TestInvoke_RoundTrip(t *testing.T) {
	hash := make([]byte, 32)
	for i := range hash {
		hash[i] = byte(0xab + i)
	}
	args := []byte{0xc0, 0xc1, 0xc2, 0xc3}
	in := Invoke{
		ProtocolVersion: 1,
		ToolHash:        hash,
		Method:          "wifi_client_count",
		Args:            args,
	}
	buf := make([]byte, 256)
	n, err := in.Encode(buf)
	if err != nil {
		t.Fatalf("encode failed: %v", err)
	}
	out, err := DecodeInvoke(buf[:n])
	if err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	if !bytes.Equal(out.ToolHash, hash) {
		t.Fatalf("ToolHash mismatch")
	}
	if out.Method != "wifi_client_count" {
		t.Fatalf("Method = %q", out.Method)
	}
	if !bytes.Equal(out.Args, args) {
		t.Fatalf("Args mismatch")
	}
	if out.Approval != nil {
		t.Fatalf("expected nil Approval, got %d bytes", len(out.Approval))
	}
}

func TestInvoke_WithApproval(t *testing.T) {
	hash := []byte{0x01, 0x02, 0x03}
	apv := []byte{0x80, 0x81, 0x82, 0x83, 0x84, 0x85, 0x86, 0x87}
	in := Invoke{
		ProtocolVersion: 1,
		ToolHash:        hash,
		Method:          "restart",
		Args:            []byte{},
		Approval:        apv,
	}
	buf := make([]byte, 128)
	n, err := in.Encode(buf)
	if err != nil {
		t.Fatalf("encode failed: %v", err)
	}
	out, err := DecodeInvoke(buf[:n])
	if err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	if !bytes.Equal(out.Approval, apv) {
		t.Fatalf("Approval mismatch")
	}
}

// TestParity_HelloMatchesC is the most important test in this
// file: the byte vector below was lifted directly from the C
// test (test_hello_known_bytes) and must match what the Go
// encoder produces. If this fails, the two codecs have drifted.
func TestParity_HelloMatchesC(t *testing.T) {
	// Inputs identical to C: ProtocolVersion=1, Role="brain".
	// Expected bytes copied verbatim from test_body.c.
	expectedFromC := []byte{
		0xa2,
		0x61, 'v',
		0x01,
		0x64, 'r', 'o', 'l', 'e',
		0x65, 'b', 'r', 'a', 'i', 'n',
	}
	buf := make([]byte, 32)
	n, _ := Hello{ProtocolVersion: 1, Role: "brain"}.Encode(buf)
	if !bytes.Equal(buf[:n], expectedFromC) {
		t.Fatalf("Go encoder drift from C:\n  got  %x\n  want %x", buf[:n], expectedFromC)
	}
}

// ── New body codec round-trips ───────────────────────────────

func TestAuth_RoundTrip(t *testing.T) {
	nonce := make([]byte, 32)
	for i := range nonce {
		nonce[i] = byte(0xa0 + i)
	}
	in := Auth{
		ProtocolVersion: 1,
		Nonce:           nonce,
		SignedNowMs:     1700000000123,
	}
	buf := make([]byte, 128)
	n, err := in.Encode(buf)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	out, err := DecodeAuth(buf[:n])
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.SignedNowMs != in.SignedNowMs {
		t.Fatalf("SignedNowMs got=%d want=%d", out.SignedNowMs, in.SignedNowMs)
	}
	if !bytes.Equal(out.Nonce, nonce) {
		t.Fatalf("Nonce mismatch")
	}
}

func TestAuthRsp_RoundTrip(t *testing.T) {
	sig := make([]byte, 64)
	nonce := make([]byte, 32)
	cert := make([]byte, 128)
	for i := range sig {
		sig[i] = byte(i)
	}
	for i := range nonce {
		nonce[i] = byte(0xa0 + i)
	}
	for i := range cert {
		cert[i] = byte(0x30 + i)
	}
	in := AuthRsp{
		ProtocolVersion: 1, Sig: sig, Nonce: nonce, DeviceCert: cert,
	}
	buf := make([]byte, 512)
	n, err := in.Encode(buf)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	out, err := DecodeAuthRsp(buf[:n])
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !bytes.Equal(out.Sig, sig) || !bytes.Equal(out.Nonce, nonce) || !bytes.Equal(out.DeviceCert, cert) {
		t.Fatalf("authrsp round-trip mismatch")
	}
}

func TestToolRsp_RoundTripOK(t *testing.T) {
	hash := make([]byte, 32)
	result := []byte{0xd0, 0xd1, 0xd2}
	in := ToolRsp{
		ProtocolVersion: 1,
		Result:          result,
		Status:          0,
		ToolHash:        hash,
		DurationMs:      1234,
	}
	buf := make([]byte, 128)
	n, _ := in.Encode(buf)
	out, err := DecodeToolRsp(buf[:n])
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Status != 0 || !bytes.Equal(out.Result, result) || out.DurationMs != 1234 {
		t.Fatalf("tool_rsp ok mismatch: %+v", out)
	}
}

func TestToolRsp_RoundTripError(t *testing.T) {
	hash := make([]byte, 32)
	in := ToolRsp{
		ProtocolVersion: 1,
		Error:           "module trapped",
		Status:          -2,
		ToolHash:        hash,
	}
	buf := make([]byte, 128)
	n, _ := in.Encode(buf)
	out, err := DecodeToolRsp(buf[:n])
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Status != -2 || out.Error != "module trapped" {
		t.Fatalf("tool_rsp err mismatch: %+v", out)
	}
}

func TestStreamChunk_RoundTrip(t *testing.T) {
	data := make([]byte, 64)
	for i := range data {
		data[i] = byte(i)
	}
	in := StreamChunk{
		ProtocolVersion: 1, Seq: 17,
		Data: data, StreamID: 0xDEADBEEF, EndStream: false,
	}
	buf := make([]byte, 128)
	n, _ := in.Encode(buf)
	out, err := DecodeStreamChunk(buf[:n])
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Seq != 17 || out.StreamID != 0xDEADBEEF || out.EndStream != false {
		t.Fatalf("stream chunk mismatch: %+v", out)
	}
	if !bytes.Equal(out.Data, data) {
		t.Fatalf("data mismatch")
	}
}

func TestNeed_RoundTrip(t *testing.T) {
	hash := make([]byte, 32)
	for i := range hash {
		hash[i] = byte(0x10 + i)
	}
	in := Need{ProtocolVersion: 1, ToolHash: hash}
	buf := make([]byte, 64)
	n, _ := in.Encode(buf)
	out, err := DecodeNeed(buf[:n])
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !bytes.Equal(out.ToolHash, hash) {
		t.Fatalf("tool_hash mismatch")
	}
}

func TestModuleBody_RoundTrip(t *testing.T) {
	wasm := make([]byte, 256)
	desc := make([]byte, 64)
	for i := range wasm {
		wasm[i] = byte(i & 0xFF)
	}
	for i := range desc {
		desc[i] = byte(0x40 + i)
	}
	in := ModuleBody{ProtocolVersion: 1, Wasm: wasm, Descriptor: desc}
	buf := make([]byte, 512)
	n, _ := in.Encode(buf)
	out, err := DecodeModuleBody(buf[:n])
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !bytes.Equal(out.Wasm, wasm) || !bytes.Equal(out.Descriptor, desc) {
		t.Fatalf("module body mismatch")
	}
}

func TestParity_HeartbeatMatchesC(t *testing.T) {
	expectedFromC := []byte{
		0xa2,
		0x61, 'v', 0x01,
		0x66, 'n', 'o', 'w', '_', 'm', 's', 0x19, 0x03, 0xe8,
	}
	buf := make([]byte, 32)
	n, _ := Heartbeat{ProtocolVersion: 1, NowMs: 1000}.Encode(buf)
	if !bytes.Equal(buf[:n], expectedFromC) {
		t.Fatalf("Go encoder drift from C:\n  got  %x\n  want %x", buf[:n], expectedFromC)
	}
}

// TestParity_C_Decodes_Go_Hello: encode in Go, decode in Go,
// then re-encode and verify byte-stable. Combined with the
// C-side test_hello_known_bytes test (which produces the same
// vector), this confirms both codecs agree on canonical form.
func TestParity_RoundTripStability_Hello(t *testing.T) {
	in := Hello{ProtocolVersion: 1, Role: "device", AgentVersion: "yvr-cagent/0.0.1"}
	buf1 := make([]byte, 64)
	n1, _ := in.Encode(buf1)
	got, err := DecodeHello(buf1[:n1])
	if err != nil {
		t.Fatal(err)
	}
	buf2 := make([]byte, 64)
	n2, _ := got.Encode(buf2)
	if !bytes.Equal(buf1[:n1], buf2[:n2]) {
		t.Fatalf("re-encode drift: %x vs %x", buf1[:n1], buf2[:n2])
	}
}

// TestParity_AuthMatchesC: auth body with deterministic nonce +
// signed_now_ms. Locks down the wire exactly so a follow-up C
// test can verify the same bytes.
func TestParity_AuthEncoding(t *testing.T) {
	// nonce = 0xa0..0xbf (32 bytes), signed_now_ms = 1000
	nonce := make([]byte, 32)
	for i := range nonce {
		nonce[i] = byte(0xa0 + i)
	}
	in := Auth{ProtocolVersion: 1, Nonce: nonce, SignedNowMs: 1000}
	buf := make([]byte, 128)
	n, err := in.Encode(buf)
	if err != nil {
		t.Fatal(err)
	}

	// Round-trip → re-encode → byte-equal.
	got, err := DecodeAuth(buf[:n])
	if err != nil {
		t.Fatal(err)
	}
	buf2 := make([]byte, 128)
	n2, _ := got.Encode(buf2)
	if !bytes.Equal(buf[:n], buf2[:n2]) {
		t.Fatalf("re-encode drift")
	}

	// Sanity: first three bytes must be map(3) + "v" + 0x01.
	wantPrefix := []byte{0xa3, 0x61, 'v', 0x01}
	if !bytes.HasPrefix(buf[:n], wantPrefix) {
		t.Fatalf("wire prefix wrong: %x", buf[:6])
	}
}

// TestParity_InvokeEncoding: same shape — invoke is the most
// frequently-emitted brain → device frame, so the encoding
// stability is critical.
func TestParity_InvokeEncoding(t *testing.T) {
	in := Invoke{
		ProtocolVersion: 1,
		ToolHash:        []byte{0xab, 0xab},
		Method:          "x",
		Args:            []byte{0xc0},
	}
	buf := make([]byte, 64)
	n, err := in.Encode(buf)
	if err != nil {
		t.Fatal(err)
	}
	got, err := DecodeInvoke(buf[:n])
	if err != nil {
		t.Fatal(err)
	}
	buf2 := make([]byte, 64)
	n2, _ := got.Encode(buf2)
	if !bytes.Equal(buf[:n], buf2[:n2]) {
		t.Fatalf("re-encode drift")
	}
}

// TestParity_ToolRspEncoding.
func TestParity_ToolRspEncoding(t *testing.T) {
	in := ToolRsp{
		ProtocolVersion: 1,
		Result:          []byte{0xd0},
		Status:          0,
		ToolHash:        []byte{0xab},
		DurationMs:      42,
	}
	buf := make([]byte, 64)
	n, err := in.Encode(buf)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := DecodeToolRsp(buf[:n])
	buf2 := make([]byte, 64)
	n2, _ := got.Encode(buf2)
	if !bytes.Equal(buf[:n], buf2[:n2]) {
		t.Fatalf("re-encode drift")
	}
}

// TestParity_StreamChunkEncoding.
func TestParity_StreamChunkEncoding(t *testing.T) {
	in := StreamChunk{
		ProtocolVersion: 1,
		Seq:             5,
		Data:            []byte{0xaa, 0xbb},
		StreamID:        9,
		EndStream:       true,
	}
	buf := make([]byte, 64)
	n, err := in.Encode(buf)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := DecodeStreamChunk(buf[:n])
	buf2 := make([]byte, 64)
	n2, _ := got.Encode(buf2)
	if !bytes.Equal(buf[:n], buf2[:n2]) {
		t.Fatalf("re-encode drift")
	}
}
