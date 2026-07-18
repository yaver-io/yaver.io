package mesh

// device.go (Phase 1) — a thin wrapper over wireguard-go's userspace device +
// TUN interface. This is where the optional Yaver Mesh actually carries IP
// traffic. Creating the TUN and configuring addresses/routes requires elevated
// privilege; the manager surfaces a clear error when it can't.
//
// WireGuard's UAPI speaks HEX-encoded keys, while the rest of Yaver (and `wg`
// itself) stores base64. keyB64ToHex bridges the two so our base64 keys
// interoperate with the userspace device.

import (
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strings"

	"golang.zx2c4.com/wireguard/conn"
	"golang.zx2c4.com/wireguard/device"
	"golang.zx2c4.com/wireguard/tun"
)

const (
	// DefaultMTU is WireGuard's standard tunnel MTU.
	DefaultMTU = 1420
	// DefaultListenPort is the UDP port the userspace device binds.
	DefaultListenPort = 51820
	// MeshSubnetCIDR is Yaver's overlay address space. Must match the allocator
	// in backend/convex/mesh.ts.
	//
	// CORRECTION (2026-07-18): this constant used to be documented as
	// "deliberately OUTSIDE Tailscale's 100.64.0.0/10 so both can run side by
	// side". That claim is arithmetically FALSE and it shaped the design for a
	// long time, so the correction is recorded here rather than quietly edited:
	//
	//     Tailscale  100.64.0.0/10 = 100.64.0.0 .. 100.127.255.255
	//     Yaver Mesh 100.96.0.0/12 = 100.96.0.0 .. 100.111.255.255
	//
	// 100.96/12 is entirely CONTAINED in 100.64/10 (both are RFC 6598 CGNAT
	// space). Observed consequence on a real host running both: Tailscale
	// installs a single route for the whole /10, so our /12 cannot be claimed
	// without fighting it — verified in a routing table as
	// `100.64/10 ... utun4`.
	//
	// Two things follow, and neither is optional:
	//   1. Mesh must NOT assume it can coexist with Tailscale. Before claiming
	//      the overlay it must check whether another interface already owns a
	//      covering route, and defer if so (see SubnetRouteConflict).
	//   2. Enabling mesh by default is only safe BEHIND that check — a blind
	//      default-on would start a routing fight on every Tailscale user's
	//      machine at upgrade.
	//
	// Relocating the range would be the real fix but is fleet-breaking (every
	// node's allocated address changes, and it must stay in lockstep with the
	// Convex allocator), so it is deliberately NOT done here.
	MeshSubnetCIDR = "100.96.0.0/12"

	// TailscaleCGNATCIDR is the range Tailscale/headscale allocate from. Named
	// here so the containment relationship above is expressed in code and
	// checkable by a test, not left as prose that can drift back to the old
	// false claim.
	TailscaleCGNATCIDR = "100.64.0.0/10"
)

// Device wraps a live wireguard-go device bound to a TUN interface.
type Device struct {
	dev  *device.Device
	tun  tun.Device
	ftun *filterTUN
	name string
}

// SetMatcher swaps the inbound ACL matcher (nil = pass-through). Lock-free and
// safe to call while the device is running.
func (d *Device) SetMatcher(m *Matcher) {
	if d.ftun != nil {
		d.ftun.setMatcher(m)
	}
}

// keyB64ToHex converts a base64 WireGuard key (private or public) to the hex
// form UAPI expects.
func keyB64ToHex(b64 string) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(b64))
	if err != nil {
		return "", fmt.Errorf("decode key: %w", err)
	}
	if len(raw) != 32 {
		return "", fmt.Errorf("key must be 32 bytes, got %d", len(raw))
	}
	return hex.EncodeToString(raw), nil
}

// NewDevice creates the TUN interface, brings up a userspace WireGuard device
// keyed by privateKeyB64, and binds listenPort. The returned Device has no peers
// yet — call SetPeers. requestedName is a hint; the OS may assign a different
// name (e.g. macOS utunN), readable via Name().
func NewDevice(requestedName, privateKeyB64 string, listenPort, mtu int) (*Device, error) {
	if mtu == 0 {
		mtu = DefaultMTU
	}
	if listenPort == 0 {
		listenPort = DefaultListenPort
	}
	tdev, err := tun.CreateTUN(requestedName, mtu)
	if err != nil {
		return nil, fmt.Errorf("create TUN %q (need elevated privilege?): %w", requestedName, err)
	}
	name, err := tdev.Name()
	if err != nil {
		name = requestedName
	}
	// Wrap the TUN so ACLs can filter inbound packets. Starts as pass-through.
	ftun := newFilterTUN(tdev)
	logger := device.NewLogger(device.LogLevelError, fmt.Sprintf("yaver-mesh(%s) ", name))
	d := device.NewDevice(ftun, conn.NewDefaultBind(), logger)

	privHex, err := keyB64ToHex(privateKeyB64)
	if err != nil {
		d.Close()
		return nil, fmt.Errorf("private key: %w", err)
	}
	base := fmt.Sprintf("private_key=%s\nlisten_port=%d\n", privHex, listenPort)
	if err := d.IpcSet(base); err != nil {
		d.Close()
		return nil, fmt.Errorf("configure device: %w", err)
	}
	if err := d.Up(); err != nil {
		d.Close()
		return nil, fmt.Errorf("bring device up: %w", err)
	}
	return &Device{dev: d, tun: tdev, ftun: ftun, name: name}, nil
}

// Name returns the OS interface name (e.g. utun5, yaver-wg0).
func (d *Device) Name() string { return d.name }

// Close tears down the device and TUN interface.
func (d *Device) Close() error {
	if d.dev != nil {
		d.dev.Close()
	}
	return nil
}
