package mesh

import (
	"bytes"
	"net"
	"sync"
	"testing"
	"time"
)

// fakeTransport routes frames between two DERPManagers in-memory, simulating the
// relay forwarding agent A's frames to agent B and vice-versa.
type fakeTransport struct {
	mu    sync.Mutex
	peers map[string]*DERPManager // deviceId -> that device's manager
	// selfID is the deviceId this transport's frames originate FROM.
	selfID string
}

func (f *fakeTransport) SetReceiver(func(string, []byte)) {} // delivers directly in this fake

func (f *fakeTransport) SendFrame(dst string, payload []byte) error {
	f.mu.Lock()
	mgr := f.peers[dst]
	f.mu.Unlock()
	if mgr == nil {
		return nil
	}
	// The destination manager receives a frame whose SOURCE is us.
	pkt := make([]byte, len(payload))
	copy(pkt, payload)
	mgr.DeliverFrame(f.selfID, pkt)
	return nil
}

// TestDERPShim_loopbackBridge verifies the full bridge: a fake "WireGuard"
// socket sends to peer B's shim endpoint; the frame crosses the transport to
// B's manager; B's shim injects it into B's "WireGuard" listen socket.
func TestDERPShim_loopbackBridge(t *testing.T) {
	// Stand up B's fake WireGuard listen socket on loopback.
	wgB, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatalf("wgB listen: %v", err)
	}
	defer wgB.Close()
	wgBPort := wgB.LocalAddr().(*net.UDPAddr).Port

	// A's fake WireGuard socket (so A's shim can deliver replies somewhere).
	wgA, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatalf("wgA listen: %v", err)
	}
	defer wgA.Close()
	wgAPort := wgA.LocalAddr().(*net.UDPAddr).Port

	mgrA := NewDERPManager(wgAPort, nil)
	mgrB := NewDERPManager(wgBPort, nil)
	defer mgrA.Close()
	defer mgrB.Close()

	transA := &fakeTransport{peers: map[string]*DERPManager{"B": mgrB}, selfID: "A"}
	transB := &fakeTransport{peers: map[string]*DERPManager{"A": mgrA}, selfID: "B"}
	mgrA.transport = transA
	mgrB.transport = transB

	// A creates a shim for peer B; B creates a shim for peer A.
	epB, err := mgrA.EndpointFor("B")
	if err != nil {
		t.Fatalf("EndpointFor B: %v", err)
	}
	if _, err := mgrB.EndpointFor("A"); err != nil {
		t.Fatalf("EndpointFor A: %v", err)
	}

	// Simulate A's WireGuard sending an encrypted packet to B: send to the shim
	// endpoint epB from A's wg socket.
	epBAddr, _ := net.ResolveUDPAddr("udp", epB)
	want := []byte("encrypted-wg-packet-for-B")
	if _, err := wgA.WriteToUDP(want, epBAddr); err != nil {
		t.Fatalf("send to shim: %v", err)
	}

	// It should arrive at B's WireGuard listen socket, injected by B's shim.
	_ = wgB.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 1500)
	n, src, err := wgB.ReadFromUDP(buf)
	if err != nil {
		t.Fatalf("wgB read: %v", err)
	}
	if !bytes.Equal(buf[:n], want) {
		t.Fatalf("payload mismatch: got %q want %q", buf[:n], want)
	}
	// The injected packet's source must be B's shim loopback (so WG attributes
	// it to peer A and roams its endpoint there).
	if src.IP.String() != "127.0.0.1" {
		t.Errorf("injected src should be loopback, got %v", src)
	}
}

func TestEncodeDecodeDERPFrame(t *testing.T) {
	var buf bytes.Buffer
	payload := []byte{0x01, 0x02, 0x03, 0xff}
	if err := EncodeDERPFrame(&buf, "device-xyz", payload); err != nil {
		t.Fatalf("encode: %v", err)
	}
	id, got, err := DecodeDERPFrame(&buf)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if id != "device-xyz" || !bytes.Equal(got, payload) {
		t.Fatalf("roundtrip mismatch: id=%q payload=%v", id, got)
	}
}

func TestEncodeDecodeDERPFrame_emptyPayload(t *testing.T) {
	var buf bytes.Buffer
	if err := EncodeDERPFrame(&buf, "d", nil); err != nil {
		t.Fatalf("encode: %v", err)
	}
	id, got, err := DecodeDERPFrame(&buf)
	if err != nil || id != "d" || len(got) != 0 {
		t.Fatalf("empty payload roundtrip failed: id=%q got=%v err=%v", id, got, err)
	}
}
