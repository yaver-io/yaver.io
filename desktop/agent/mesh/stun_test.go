package mesh

import (
	"encoding/binary"
	"net/netip"
	"testing"
)

// buildXorMappedResponse fabricates a STUN binding success response carrying an
// XOR-MAPPED-ADDRESS for the given public endpoint + transaction ID.
func buildXorMappedResponse(txID [12]byte, ip [4]byte, port uint16) []byte {
	// Attribute value: reserved(1) family(1)=0x01 xport(2) xaddr(4) = 8 bytes.
	val := make([]byte, 8)
	val[0] = 0
	val[1] = 0x01
	binary.BigEndian.PutUint16(val[2:4], port^uint16(stunMagicCookie>>16))
	var addr [4]byte
	binary.BigEndian.PutUint32(addr[:], binary.BigEndian.Uint32(ip[:])^stunMagicCookie)
	copy(val[4:8], addr[:])

	attr := make([]byte, 4+len(val))
	binary.BigEndian.PutUint16(attr[0:2], 0x0020) // XOR-MAPPED-ADDRESS
	binary.BigEndian.PutUint16(attr[2:4], uint16(len(val)))
	copy(attr[4:], val)

	msg := make([]byte, 20+len(attr))
	binary.BigEndian.PutUint16(msg[0:2], 0x0101) // Binding Success Response
	binary.BigEndian.PutUint16(msg[2:4], uint16(len(attr)))
	binary.BigEndian.PutUint32(msg[4:8], stunMagicCookie)
	copy(msg[8:20], txID[:])
	copy(msg[20:], attr)
	return msg
}

func TestParseBindingResponse_xorMapped(t *testing.T) {
	txID := [12]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12}
	want := netip.AddrPortFrom(netip.AddrFrom4([4]byte{203, 0, 113, 7}), 51820)
	resp := buildXorMappedResponse(txID, [4]byte{203, 0, 113, 7}, 51820)

	got, err := parseBindingResponse(resp, txID)
	if err != nil {
		t.Fatalf("parseBindingResponse: %v", err)
	}
	if got != want {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestParseBindingResponse_rejectsWrongTxID(t *testing.T) {
	good := [12]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12}
	resp := buildXorMappedResponse(good, [4]byte{198, 51, 100, 1}, 1234)
	wrong := [12]byte{9, 9, 9}
	if _, err := parseBindingResponse(resp, wrong); err == nil {
		t.Error("expected transaction-ID mismatch error")
	}
}

func TestParseBindingResponse_rejectsBadCookie(t *testing.T) {
	txID := [12]byte{1}
	resp := buildXorMappedResponse(txID, [4]byte{198, 51, 100, 1}, 1234)
	binary.BigEndian.PutUint32(resp[4:8], 0xDEADBEEF) // corrupt magic cookie
	if _, err := parseBindingResponse(resp, txID); err == nil {
		t.Error("expected magic-cookie mismatch error")
	}
}

func TestBuildBindingRequest_shape(t *testing.T) {
	req, txID := buildBindingRequest()
	if len(req) != 20 {
		t.Fatalf("binding request must be 20 bytes, got %d", len(req))
	}
	if binary.BigEndian.Uint16(req[0:2]) != 0x0001 {
		t.Error("type must be Binding Request 0x0001")
	}
	if binary.BigEndian.Uint32(req[4:8]) != stunMagicCookie {
		t.Error("magic cookie missing")
	}
	var embedded [12]byte
	copy(embedded[:], req[8:20])
	if embedded != txID {
		t.Error("transaction ID not embedded in request")
	}
}
