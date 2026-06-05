package main

// mesh_agent.go — the bridge between the agent (package main) and the pure
// data-plane package (desktop/agent/mesh). It implements mesh.PeerSource by
// querying the Convex control plane (mesh:listMeshPeers) and mapping the
// visible meshNodes into WireGuard peers, and it constructs/holds the manager.

import (
	"encoding/json"
	"fmt"
	"net"
	"net/netip"
	"strings"
	"time"

	"github.com/yaver-io/agent/mesh"
)

// meshPeerRow mirrors the shape returned by backend/convex/mesh.ts listMeshPeers.
type meshPeerRow struct {
	DeviceID         string   `json:"deviceId"`
	OwnerUserID      string   `json:"ownerUserId"`
	Alias            string   `json:"alias"`
	WgPublicKey      string   `json:"wgPublicKey"`
	MeshIPv4         string   `json:"meshIPv4"`
	Endpoints        []string `json:"endpoints"`
	AdvertisedRoutes []string `json:"advertisedRoutes"`
	IsExitNode       bool     `json:"isExitNode"`
	Online           bool     `json:"online"`
	AccessScope      string   `json:"accessScope"`
	// Desired state set by the console (Tailscale-style intent).
	WantEnabled     *bool    `json:"wantEnabled"`
	WantExitNode    bool     `json:"wantExitNode"`
	WantUseExitNode string   `json:"wantUseExitNode"`
	WantRoutes      []string `json:"wantRoutes"`
}

// fetchMeshPeers calls the control plane and returns the raw rows.
func fetchMeshPeers(cfg *Config) ([]meshPeerRow, error) {
	raw, err := meshConvexCall(cfg, "query", "mesh:listMeshPeers", map[string]interface{}{})
	if err != nil {
		return nil, err
	}
	var resp struct {
		Peers []meshPeerRow `json:"peers"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, fmt.Errorf("decode peers: %w", err)
	}
	return resp.Peers, nil
}

// buildMeshPeerSource returns a mesh.PeerSource closure. It reloads config each
// call so a rotated auth token is picked up, finds this device's own row to
// learn the overlay self-IP, and maps every OTHER visible node into a peer.
func buildMeshPeerSource(deviceID string) mesh.PeerSource {
	return func() (string, []mesh.Peer, error) {
		cfg, err := LoadConfig()
		if err != nil {
			return "", nil, err
		}
		rows, err := fetchMeshPeers(cfg)
		if err != nil {
			return "", nil, err
		}
		useExitNode := ""
		if cfg.Mesh != nil {
			useExitNode = cfg.Mesh.UseExitNode
		}
		localIPs := localIPv4s()
		var selfIP string
		var peers []mesh.Peer
		for _, r := range rows {
			if r.DeviceID == deviceID {
				selfIP = r.MeshIPv4
				continue
			}
			if r.WgPublicKey == "" || r.MeshIPv4 == "" {
				continue // node hasn't finished joining
			}
			endpoint := pickPeerEndpoint(localIPs, r.Endpoints)
			// At minimum the peer's /32 overlay IP. Advertised SUBNET routes
			// extend reachability automatically (subnet-router behavior). A
			// DEFAULT route (0.0.0.0/0, ::/0) is only honored when this node has
			// explicitly chosen this peer as its exit node — otherwise an exit
			// node would silently capture every peer's traffic.
			allowed := []string{r.MeshIPv4 + "/32"}
			for _, route := range r.AdvertisedRoutes {
				if isDefaultRoute(route) && r.DeviceID != useExitNode {
					continue
				}
				allowed = append(allowed, route)
			}
			peers = append(peers, mesh.Peer{
				DeviceID:         r.DeviceID,
				PublicKey:        r.WgPublicKey,
				Endpoint:         endpoint,
				AllowedIPs:       allowed,
				KeepaliveSeconds: 25,
			})
		}
		return selfIP, peers, nil
	}
}

// isDefaultRoute reports whether a CIDR is an IPv4/IPv6 default route.
func isDefaultRoute(cidr string) bool {
	c := strings.TrimSpace(cidr)
	return c == "0.0.0.0/0" || c == "::/0"
}

// localIPv4s returns this host's non-loopback IPv4 addresses, used to decide
// whether a peer's LAN endpoint is reachable from here.
func localIPv4s() []net.IP {
	var out []net.IP
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return out
	}
	for _, a := range addrs {
		if ipnet, ok := a.(*net.IPNet); ok {
			if v4 := ipnet.IP.To4(); v4 != nil && !ipnet.IP.IsLoopback() {
				out = append(out, v4)
			}
		}
	}
	return out
}

// pickPeerEndpoint selects the single WireGuard endpoint to dial for a peer.
// WireGuard only accepts one endpoint, so we choose the most likely-reachable:
//  1. a peer endpoint sharing a /24 with one of our local IPs (same LAN), else
//  2. a public (non-RFC1918) endpoint (across-internet via STUN-discovered IP),
//     else
//  3. the first endpoint as a last resort.
//
// WireGuard's roaming then self-corrects the endpoint once any packet arrives.
func pickPeerEndpoint(localIPs []net.IP, endpoints []string) string {
	if len(endpoints) == 0 {
		return ""
	}
	var public string
	for _, ep := range endpoints {
		host, _, err := net.SplitHostPort(ep)
		if err != nil {
			continue
		}
		ip := net.ParseIP(host)
		if ip == nil {
			continue
		}
		if ip.IsPrivate() {
			for _, local := range localIPs {
				if sameIPv24(local, ip) {
					return ep // same-LAN: best path
				}
			}
		} else if public == "" {
			public = ep
		}
	}
	if public != "" {
		return public
	}
	return endpoints[0]
}

// sameIPv24 reports whether two IPv4 addresses share a /24 prefix.
func sameIPv24(a, b net.IP) bool {
	a4, b4 := a.To4(), b.To4()
	if a4 == nil || b4 == nil {
		return false
	}
	return a4[0] == b4[0] && a4[1] == b4[1] && a4[2] == b4[2]
}

// buildMeshNameSource returns a mesh.NameSource closure for MagicDNS: it maps
// each visible node's alias -> overlay IP (including this device's own alias),
// so `<alias>.mesh` resolves across the tailnet.
func buildMeshNameSource() mesh.NameSource {
	return func() (map[string]netip.Addr, error) {
		cfg, err := LoadConfig()
		if err != nil {
			return nil, err
		}
		rows, err := fetchMeshPeers(cfg)
		if err != nil {
			return nil, err
		}
		out := map[string]netip.Addr{}
		for _, r := range rows {
			if r.Alias == "" || r.MeshIPv4 == "" {
				continue
			}
			addr, perr := netip.ParseAddr(r.MeshIPv4)
			if perr != nil {
				continue
			}
			out[meshDNSLabel(r.Alias)] = addr
		}
		return out, nil
	}
}

// meshDNSLabel normalizes an alias into a DNS label (lower-case, spaces/dots to
// hyphens) so multi-word device names still resolve.
func meshDNSLabel(alias string) string {
	l := strings.ToLower(strings.TrimSpace(alias))
	l = strings.ReplaceAll(l, " ", "-")
	l = strings.ReplaceAll(l, ".", "-")
	return l
}

// ensureMeshManager constructs the manager on first use, keyed by the device's
// vault-stored WireGuard private key. Caller holds s.meshMu.
func (s *HTTPServer) ensureMeshManagerLocked(deviceID string) (*mesh.Manager, error) {
	if s.meshMgr != nil {
		return s.meshMgr, nil
	}
	kp, err := meshLoadOrCreateKeyPair()
	if err != nil {
		return nil, err
	}
	mgr := mesh.NewManager(kp.PrivateKey, buildMeshPeerSource(deviceID))
	mgr.SetNameSource(buildMeshNameSource())
	mgr.SetACLSource(buildMeshACLSource())
	// Relay-as-DERP fallback for symmetric-NAT peers (no direct path). The
	// transport rides the agent's relay connection, attached in relayConnectAndServe.
	mgr.SetRelayTransport(ensureGlobalMeshDERP())
	// Enable forwarding/NAT when this node advertises routes or is an exit node.
	if cfg, _ := LoadConfig(); cfg != nil && cfg.Mesh != nil {
		mgr.SetForwarding(cfg.Mesh.ExitNode || len(cfg.Mesh.AdvertisedRoutes) > 0)
	}
	s.meshMgr = mgr
	return s.meshMgr, nil
}

// startMeshDesiredLoop launches (once) the convergence loop that pulls this
// node's DESIRED config from the console and applies it — the Tailscale model
// where the control plane holds intent and the node reconciles to it. Started
// from /mesh/up and the serve-time restore.
func (s *HTTPServer) startMeshDesiredLoop(deviceID string) {
	s.meshMu.Lock()
	if s.meshDesiredStarted {
		s.meshMu.Unlock()
		return
	}
	s.meshDesiredStarted = true
	s.meshMu.Unlock()
	go func() {
		defer func() { _ = recover() }()
		t := time.NewTicker(30 * time.Second)
		defer t.Stop()
		for range t.C {
			s.meshConvergeDesired(deviceID)
		}
	}()
}

// meshConvergeDesired applies any console-set desired config that differs from
// the local agent state (exit-node advertisement, exit node to use, subnet
// routes, or a remote disable). When something changes it updates config and
// restarts the data plane so forwarding/routing re-applies, then re-registers.
func (s *HTTPServer) meshConvergeDesired(deviceID string) {
	cfg, err := LoadConfig()
	if err != nil || cfg.Mesh == nil || !cfg.Mesh.Enabled {
		return
	}
	rows, err := fetchMeshPeers(cfg)
	if err != nil {
		return
	}
	var self *meshPeerRow
	for i := range rows {
		if rows[i].DeviceID == deviceID {
			self = &rows[i]
			break
		}
	}
	if self == nil {
		return
	}

	// Remote disable: the console asked this node to leave the mesh.
	if self.WantEnabled != nil && !*self.WantEnabled {
		s.meshMu.Lock()
		if s.meshMgr != nil {
			_ = s.meshMgr.Stop()
			s.meshMgr = nil
		}
		s.meshMu.Unlock()
		_, _ = meshConvexCall(cfg, "mutation", "mesh:leaveMesh", map[string]interface{}{"deviceId": deviceID})
		cfg.Mesh.Enabled = false
		_ = SaveConfig(cfg)
		return
	}

	changed := false
	if self.WantExitNode != cfg.Mesh.ExitNode {
		cfg.Mesh.ExitNode = self.WantExitNode
		changed = true
	}
	if self.WantUseExitNode != cfg.Mesh.UseExitNode {
		cfg.Mesh.UseExitNode = self.WantUseExitNode
		changed = true
	}
	if self.WantRoutes != nil && !equalStringSets(self.WantRoutes, cfg.Mesh.AdvertisedRoutes) {
		cfg.Mesh.AdvertisedRoutes = self.WantRoutes
		changed = true
	}
	if !changed {
		return
	}
	if err := SaveConfig(cfg); err != nil {
		return
	}
	// Restart the data plane so forwarding/NAT + AllowedIPs re-apply, then
	// re-register the new advertisement with the control plane.
	s.meshMu.Lock()
	if s.meshMgr != nil {
		_ = s.meshMgr.Stop()
		s.meshMgr = nil
	}
	mgr, eerr := s.ensureMeshManagerLocked(deviceID)
	s.meshMu.Unlock()
	if eerr == nil {
		_ = mgr.Start()
	}
	_, _ = meshRegisterJoin(cfg, cfg.Mesh.PublicKey, meshLocalEndpoints())
}

// equalStringSets reports set equality ignoring order/dupes.
func equalStringSets(a, b []string) bool {
	ma := map[string]bool{}
	for _, x := range a {
		ma[x] = true
	}
	mb := map[string]bool{}
	for _, x := range b {
		mb[x] = true
	}
	if len(ma) != len(mb) {
		return false
	}
	for k := range ma {
		if !mb[k] {
			return false
		}
	}
	return true
}
