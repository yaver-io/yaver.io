// Command meshtest is a tiny harness for end-to-end testing of the Yaver Mesh
// DATA PLANE without the control plane (Convex/auth). It drives the mesh package
// directly: bring up a real TUN + wireguard-go device, configure the overlay IP
// + route, and peer a single counterpart. Two instances (in two network
// namespaces) form a tunnel you can ping across — the real-hardware proof that
// TUN creation + WireGuard handshake + netconfig + ICMP over the overlay all
// work. Build for linux and run inside a privileged container/VM.
//
//	meshtest keygen
//	  -> prints "<privB64> <pubB64>"
//	meshtest run <ifname> <selfIPv4> <listenPort> <privB64> <peerPubB64> <peerEndpoint> <peerIPv4>
//	  -> brings the device up and blocks (Ctrl-C to stop)
package main

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/yaver-io/agent/mesh"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: meshtest keygen | run <ifname> <selfIPv4> <listenPort> <privB64> <peerPubB64> <peerEndpoint> <peerIPv4>")
		os.Exit(2)
	}
	switch os.Args[1] {
	case "keygen":
		kp, err := mesh.GenerateKeyPair()
		if err != nil {
			fmt.Fprintln(os.Stderr, "keygen:", err)
			os.Exit(1)
		}
		fmt.Printf("%s %s\n", kp.PrivateKey, kp.PublicKey)
	case "run":
		if len(os.Args) != 9 {
			fmt.Fprintln(os.Stderr, "run needs: <ifname> <selfIPv4> <listenPort> <privB64> <peerPubB64> <peerEndpoint> <peerIPv4>")
			os.Exit(2)
		}
		ifname, selfIP, port := os.Args[2], os.Args[3], os.Args[4]
		priv, peerPub, peerEndpoint, peerIP := os.Args[5], os.Args[6], os.Args[7], os.Args[8]
		var listenPort int
		fmt.Sscanf(port, "%d", &listenPort)

		dev, err := mesh.NewDevice(ifname, priv, listenPort, 0)
		if err != nil {
			fmt.Fprintln(os.Stderr, "NewDevice:", err)
			os.Exit(1)
		}
		fmt.Printf("[%s] device up on %s, listen %d\n", ifname, dev.Name(), listenPort)
		if err := dev.ConfigureNetwork(selfIP, mesh.MeshSubnetCIDR); err != nil {
			fmt.Fprintln(os.Stderr, "ConfigureNetwork:", err)
			os.Exit(1)
		}
		fmt.Printf("[%s] overlay IP %s configured\n", ifname, selfIP)
		if err := dev.SetPeers([]mesh.Peer{{
			DeviceID:         "peer",
			PublicKey:        peerPub,
			Endpoint:         peerEndpoint,
			AllowedIPs:       []string{peerIP + "/32"},
			KeepaliveSeconds: 25,
		}}); err != nil {
			fmt.Fprintln(os.Stderr, "SetPeers:", err)
			os.Exit(1)
		}
		fmt.Printf("[%s] peer %s via %s (allowed %s/32) — ready\n", ifname, peerPub[:12]+"…", peerEndpoint, peerIP)

		sig := make(chan os.Signal, 1)
		signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
		<-sig
		_ = dev.Close()
	default:
		fmt.Fprintln(os.Stderr, "unknown mode:", os.Args[1])
		os.Exit(2)
	}
}
