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
