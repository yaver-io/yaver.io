package mesh

import (
	"strings"
	"testing"
)

func TestRenderPeers_uapiShape(t *testing.T) {
	kp, _ := GenerateKeyPair()
	cfg, err := renderPeers([]Peer{{
		PublicKey:        kp.PublicKey,
		Endpoint:         "198.51.100.7:51820",
		AllowedIPs:       []string{"100.96.0.5/32", "10.0.0.0/24"},
		KeepaliveSeconds: 25,
	}})
	if err != nil {
		t.Fatalf("renderPeers: %v", err)
	}
	for _, want := range []string{
		"replace_peers=true",
		"public_key=", // hex, not base64
		"endpoint=198.51.100.7:51820",
		"persistent_keepalive_interval=25",
		"replace_allowed_ips=true",
		"allowed_ip=100.96.0.5/32",
		"allowed_ip=10.0.0.0/24",
	} {
		if !strings.Contains(cfg, want) {
			t.Errorf("rendered config missing %q\n%s", want, cfg)
		}
	}
	// The base64 public key must NOT appear verbatim — UAPI is hex.
	if strings.Contains(cfg, kp.PublicKey) {
		t.Errorf("base64 key leaked into UAPI config (should be hex)")
	}
}

func TestRenderPeers_rejectsBadKey(t *testing.T) {
	if _, err := renderPeers([]Peer{{PublicKey: "!!!not-base64"}}); err == nil {
		t.Error("expected error on invalid peer key")
	}
}

func TestRenderPeers_omitsEmptyEndpoint(t *testing.T) {
	kp, _ := GenerateKeyPair()
	cfg, err := renderPeers([]Peer{{PublicKey: kp.PublicKey, AllowedIPs: []string{"100.96.0.9/32"}}})
	if err != nil {
		t.Fatalf("renderPeers: %v", err)
	}
	if strings.Contains(cfg, "endpoint=") {
		t.Errorf("endpoint line should be omitted when empty\n%s", cfg)
	}
}

func TestParsePeerStats(t *testing.T) {
	uapi := strings.Join([]string{
		"private_key=abcd",
		"listen_port=51820",
		"public_key=aabbcc",
		"endpoint=192.0.2.1:51820",
		"last_handshake_time_sec=1714000000",
		"rx_bytes=2048",
		"tx_bytes=4096",
		"public_key=ddeeff",
		"last_handshake_time_sec=0",
		"",
	}, "\n")
	stats := parsePeerStats(uapi)
	if len(stats) != 2 {
		t.Fatalf("expected 2 peers, got %d", len(stats))
	}
	if stats[0].PublicKeyHex != "aabbcc" || stats[0].Endpoint != "192.0.2.1:51820" {
		t.Errorf("peer 0 mis-parsed: %+v", stats[0])
	}
	if stats[0].LastHandshakeUnix != 1714000000 || stats[0].RxBytes != 2048 || stats[0].TxBytes != 4096 {
		t.Errorf("peer 0 counters mis-parsed: %+v", stats[0])
	}
	if stats[1].PublicKeyHex != "ddeeff" || stats[1].LastHandshakeUnix != 0 {
		t.Errorf("peer 1 mis-parsed: %+v", stats[1])
	}
}
