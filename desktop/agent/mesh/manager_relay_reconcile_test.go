package mesh

import (
	"encoding/base64"
	"net"
	"testing"
	"time"
)

type statusDevice struct {
	stats []PeerStat
	peers []Peer
}

func (d *statusDevice) Stats() ([]PeerStat, error) { return d.stats, nil }
func (d *statusDevice) SetPeers(p []Peer) error    { d.peers = p; return nil }

type observedRelay struct{ sent chan string }

func (r *observedRelay) SetReceiver(func(string, []byte))      {}
func (r *observedRelay) SendFrame(peer string, _ []byte) error { r.sent <- peer; return nil }

func TestReconcilePreservesWorkingRelayAndReleasesRoamedDirectPath(t *testing.T) {
	transport := &observedRelay{sent: make(chan string, 1)}
	relay := NewDERPManager(1, transport)
	defer relay.Close()
	endpoint, err := relay.EndpointFor("peer")
	if err != nil {
		t.Fatal(err)
	}
	key := base64.StdEncoding.EncodeToString(make([]byte, 32))
	hex, _ := keyB64ToHex(key)
	d := &statusDevice{stats: []PeerStat{{PublicKeyHex: hex, Endpoint: endpoint, LastHandshakeUnix: time.Now().Unix()}}}
	m := &Manager{derp: relay}
	peer := Peer{DeviceID: "peer", PublicKey: key, Endpoint: "192.0.2.1:1234", MeshIP: "100.96.0.2", Name: "test-peer"}
	if err := m.applyPeers(d, []Peer{peer}); err != nil {
		t.Fatal(err)
	}
	conn, err := net.Dial("udp", endpoint)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.Write([]byte{4, 0, 0, 0}); err != nil {
		t.Fatal(err)
	}
	select {
	case dst := <-transport.sent:
		if dst != "peer" {
			t.Fatal(dst)
		}
	case <-time.After(time.Second):
		t.Fatal("reconciliation discarded the working relay path")
	}
	if m.peerInfo[hex].MeshIP != "100.96.0.2" {
		t.Fatal("missing overlay identity")
	}
	// A real move to a direct endpoint must retire the no-longer-used shim.
	d.stats[0].Endpoint = "192.0.2.2:1234"
	if err := m.applyPeers(d, []Peer{peer}); err != nil {
		t.Fatal(err)
	}
	if relay.IsPeerEndpoint("peer", endpoint) {
		t.Fatal("unused relay shim retained after direct roam")
	}
}
