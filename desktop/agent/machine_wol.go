package main

// Wake-on-LAN.
//
// Restored from the 2026-04-28 lean-stack cut (it lived in mcp_iot.go, which
// was dropped wholesale). It earns its place back because waking a sleeping
// box is the entry point for every remote surface: a watch, car or headset
// client is useless if the machine it drives is asleep, and unlike the rest
// of that IoT family this is core to Yaver's own job.
//
// Note the split with machine_wake (machine_lifecycle.go): that verb "wakes" a
// stopped BYO *cloud* machine by recreating it from a snapshot. This is the
// other half — a physical box asleep on a LAN, which no amount of cloud API
// can help with.
//
// Two things the original got wrong, both fixed here:
//   - It broadcast only to 255.255.255.255 on whatever interface the kernel
//     happened to pick. Multi-homed hosts (every machine on Tailscale, i.e.
//     all of ours) route that out the wrong link, and plenty of gear drops
//     the all-ones address outright — the packet silently never lands. We
//     send a subnet-directed broadcast out of every eligible interface.
//   - It sent only to port 9. Port 7 is equally standard and some firmware
//     listens on one but not the other; sending both costs 102 bytes.
//
// A magic packet is link-local: it cannot be routed to another subnet. A
// client on cellular therefore can never wake anything by itself — the call
// has to land on an agent already awake on the target's LAN. That relay is
// what makes "wake my desk box from the car" possible at all.

import (
	"fmt"
	"net"
	"strings"
)

// wolPorts are the two ports WoL implementations listen on. Both are
// standard; firmware disagrees about which.
var wolPorts = []int{7, 9}

// buildMagicPacket returns the 102-byte WoL payload: 6 bytes of 0xFF
// followed by the target MAC repeated 16 times.
func buildMagicPacket(hw net.HardwareAddr) []byte {
	packet := make([]byte, 0, 102)
	for i := 0; i < 6; i++ {
		packet = append(packet, 0xFF)
	}
	for i := 0; i < 16; i++ {
		packet = append(packet, hw...)
	}
	return packet
}

// broadcastAddrsForInterface returns the subnet-directed broadcast address of
// every IPv4 network the interface is on (e.g. 10.0.0.45/24 -> 10.0.0.255).
//
// Subnet-directed beats 255.255.255.255: the kernel routes it out the right
// link without guessing, and switches forward it where they drop the
// all-ones address.
func broadcastAddrsForInterface(iface net.Interface) []net.IP {
	if iface.Flags&net.FlagUp == 0 ||
		iface.Flags&net.FlagLoopback != 0 ||
		iface.Flags&net.FlagBroadcast == 0 {
		return nil
	}
	addrs, err := iface.Addrs()
	if err != nil {
		return nil
	}
	var out []net.IP
	for _, addr := range addrs {
		ipnet, ok := addr.(*net.IPNet)
		if !ok {
			continue
		}
		ip4 := ipnet.IP.To4()
		if ip4 == nil {
			continue
		}
		mask := net.IP(ipnet.Mask).To4()
		if mask == nil {
			continue
		}
		bcast := make(net.IP, 4)
		for i := 0; i < 4; i++ {
			bcast[i] = ip4[i] | ^mask[i]
		}
		out = append(out, bcast)
	}
	return out
}

// wakeOnLANResult reports which broadcast addresses actually accepted the
// packet. Callers surface `sent` so a user can tell "we shouted on the right
// wire" apart from "we had nowhere to shout".
type wakeOnLANResult struct {
	OK      bool     `json:"ok"`
	MAC     string   `json:"mac"`
	Sent    []string `json:"sent,omitempty"`
	Errors  []string `json:"errors,omitempty"`
	Message string   `json:"message,omitempty"`
}

// sendWakeOnLAN broadcasts a magic packet for `mac` out of every eligible
// interface, on both WoL ports.
//
// Best-effort by design: a host with several NICs will fail on some of them
// (no route, link down mid-send) while succeeding on the one that matters, so
// a partial failure is still a success. Only "not one packet left the box" is
// an error.
func sendWakeOnLAN(mac string) wakeOnLANResult {
	hw, err := net.ParseMAC(strings.TrimSpace(mac))
	if err != nil {
		return wakeOnLANResult{OK: false, MAC: mac, Message: "invalid MAC address: " + err.Error()}
	}
	if len(hw) != 6 {
		return wakeOnLANResult{OK: false, MAC: mac,
			Message: fmt.Sprintf("expected a 6-byte MAC, got %d bytes", len(hw))}
	}

	packet := buildMagicPacket(hw)
	res := wakeOnLANResult{MAC: hw.String()}

	ifaces, err := net.Interfaces()
	if err != nil {
		return wakeOnLANResult{OK: false, MAC: hw.String(), Message: "enumerate interfaces: " + err.Error()}
	}

	for _, iface := range ifaces {
		for _, bcast := range broadcastAddrsForInterface(iface) {
			for _, port := range wolPorts {
				target := &net.UDPAddr{IP: bcast, Port: port}
				conn, err := net.DialUDP("udp", nil, target)
				if err != nil {
					res.Errors = append(res.Errors, fmt.Sprintf("%s %s: %v", iface.Name, target, err))
					continue
				}
				_, err = conn.Write(packet)
				conn.Close()
				if err != nil {
					res.Errors = append(res.Errors, fmt.Sprintf("%s %s: %v", iface.Name, target, err))
					continue
				}
				res.Sent = append(res.Sent, fmt.Sprintf("%s:%d (%s)", bcast, port, iface.Name))
			}
		}
	}

	if len(res.Sent) == 0 {
		res.OK = false
		if res.Message == "" {
			res.Message = "no broadcast-capable interface accepted the packet — is this host on the target's LAN?"
		}
		return res
	}
	res.OK = true
	res.Message = fmt.Sprintf("magic packet sent on %d address(es)", len(res.Sent))
	return res
}

// localMACAddrs lists this host's own hardware addresses, so a machine can
// self-report what a peer must target to wake it.
//
// Without this the user has to hand-type a MAC, which is exactly the kind of
// thing nobody knows from a watch face. Skips loopback, down links, and
// virtual interfaces (docker/bridge/tailscale) — waking those is meaningless
// and they'd only pad the list.
func localMACAddrs() []string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	var out []string
	for _, iface := range ifaces {
		if iface.Flags&net.FlagLoopback != 0 || len(iface.HardwareAddr) != 6 {
			continue
		}
		if isVirtualInterface(iface.Name) {
			continue
		}
		// Only interfaces that could actually receive a broadcast.
		if iface.Flags&net.FlagBroadcast == 0 {
			continue
		}
		out = append(out, iface.HardwareAddr.String())
	}
	return out
}

// isVirtualInterface reports whether a NIC is software-only. WoL is a
// property of physical link hardware, so these can never be wake targets.
func isVirtualInterface(name string) bool {
	n := strings.ToLower(name)
	for _, prefix := range []string{
		"docker", "br-", "veth", "virbr", "tailscale", "utun", "tun", "tap",
		"lo", "awdl", "llw", "bridge", "vmnet", "zt",
	} {
		if strings.HasPrefix(n, prefix) {
			return true
		}
	}
	return false
}

// mcpWakeOnLAN is the MCP entry point. Replaces the dropped stub, which
// advertised the tool in the tool list while returning "feature_removed" —
// so a model calling it got a confident lie.
func mcpWakeOnLAN(mac string) interface{} {
	return sendWakeOnLAN(mac)
}
