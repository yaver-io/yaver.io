package main

import (
	"bytes"
	"net"
	"testing"
)

// The magic packet format is unforgiving and failure is silent: a malformed
// packet is simply ignored by the NIC, so nothing anywhere reports an error
// and the box just never wakes. Pin the bytes.
func TestBuildMagicPacket(t *testing.T) {
	hw, err := net.ParseMAC("AA:BB:CC:DD:EE:FF")
	if err != nil {
		t.Fatalf("ParseMAC: %v", err)
	}
	packet := buildMagicPacket(hw)

	if len(packet) != 102 {
		t.Fatalf("len = %d, want 102 (6 sync bytes + 16*6 MAC)", len(packet))
	}
	if !bytes.Equal(packet[:6], []byte{0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF}) {
		t.Errorf("sync stream = % X, want 6 x FF", packet[:6])
	}
	for i := 0; i < 16; i++ {
		got := packet[6+i*6 : 6+(i+1)*6]
		if !bytes.Equal(got, hw) {
			t.Fatalf("repetition %d = % X, want % X", i, got, hw)
		}
	}
}

func TestSendWakeOnLANRejectsBadMAC(t *testing.T) {
	for _, mac := range []string{"", "not-a-mac", "AA:BB:CC:DD:EE", "zz:bb:cc:dd:ee:ff"} {
		res := sendWakeOnLAN(mac)
		if res.OK {
			t.Errorf("sendWakeOnLAN(%q).OK = true, want false", mac)
		}
		if res.Message == "" {
			t.Errorf("sendWakeOnLAN(%q) gave no message to show the user", mac)
		}
	}
}

// A 20-octet InfiniBand address parses fine but cannot be woken; the packet
// layout assumes 6 octets and would be garbage.
func TestSendWakeOnLANRejectsNon6ByteMAC(t *testing.T) {
	const ib = "00:00:00:00:fe:80:00:00:00:00:00:00:02:00:5e:10:00:00:00:01"
	if _, err := net.ParseMAC(ib); err != nil {
		t.Skipf("this Go version rejects the address at parse time: %v", err)
	}
	res := sendWakeOnLAN(ib)
	if res.OK {
		t.Error("OK = true for a 20-byte address, want false")
	}
}

// Subnet-directed broadcast is the whole point of the rewrite: the old code
// used 255.255.255.255, which multi-homed hosts (anything on Tailscale — all
// of ours) route out the wrong link.
func TestBroadcastAddrsForInterface(t *testing.T) {
	_, ipnet, err := net.ParseCIDR("10.0.0.45/24")
	if err != nil {
		t.Fatalf("ParseCIDR: %v", err)
	}
	ip := net.ParseIP("10.0.0.45").To4()
	mask := net.IP(ipnet.Mask).To4()
	bcast := make(net.IP, 4)
	for i := 0; i < 4; i++ {
		bcast[i] = ip[i] | ^mask[i]
	}
	if got, want := bcast.String(), "10.0.0.255"; got != want {
		t.Errorf("broadcast for 10.0.0.45/24 = %s, want %s", got, want)
	}
}

// Loopback and down interfaces must never be offered as wake targets.
func TestBroadcastAddrsSkipsLoopback(t *testing.T) {
	lo := net.Interface{Name: "lo0", Flags: net.FlagUp | net.FlagLoopback}
	if got := broadcastAddrsForInterface(lo); got != nil {
		t.Errorf("loopback yielded %v, want nil", got)
	}
	down := net.Interface{Name: "en0", Flags: net.FlagBroadcast}
	if got := broadcastAddrsForInterface(down); got != nil {
		t.Errorf("down interface yielded %v, want nil", got)
	}
}

// WoL is a property of physical link hardware. Waking a docker bridge or a
// Tailscale tunnel is meaningless, and listing them would bury the real NIC.
func TestIsVirtualInterface(t *testing.T) {
	for _, name := range []string{"docker0", "br-abc123", "veth1234", "tailscale0", "utun3", "vmnet1", "lo0"} {
		if !isVirtualInterface(name) {
			t.Errorf("isVirtualInterface(%q) = false, want true", name)
		}
	}
	for _, name := range []string{"en0", "eth0", "enp3s0", "wlan0", "wlp2s0"} {
		if isVirtualInterface(name) {
			t.Errorf("isVirtualInterface(%q) = true, want false", name)
		}
	}
}

// localMACAddrs feeds "which MAC do I wake?" so nobody has to type one from a
// watch face. It must never return a virtual NIC's address.
func TestLocalMACAddrsAreRealNICs(t *testing.T) {
	for _, mac := range localMACAddrs() {
		hw, err := net.ParseMAC(mac)
		if err != nil {
			t.Errorf("localMACAddrs returned unparseable %q: %v", mac, err)
			continue
		}
		if len(hw) != 6 {
			t.Errorf("localMACAddrs returned %d-byte %q, want 6", len(hw), mac)
		}
	}
}
