package main

// mesh_cmd.go — `yaver mesh …`, the CLI surface for Yaver Mesh, the OPTIONAL
// WireGuard overlay that lets Yaver stand in for Tailscale.
//
// OPT-IN CONTRACT: a user who never runs `yaver mesh up` has cfg.Mesh == nil.
// Nothing here touches the network at agent start, no TUN is created, and the
// beacon/relay/QUIC behavior is unchanged. Enabling the mesh is an explicit,
// reversible act.
//
// PHASE 0 (this file): control-plane only. `mesh up` generates a
// WireGuard-compatible keypair (private half stored in the vault, never synced),
// registers the PUBLIC key + reachable endpoints with Convex (mesh:joinMesh),
// and records the assigned overlay IP. The actual data plane — a wireguard-go
// userspace device + TUN interface — lands in Phase 1 under desktop/agent/mesh/.
// Until then `mesh status` clearly reports the data plane as not-yet-active.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/yaver-io/agent/mesh"
)

// MeshConfig is the persisted, opt-in state for the WireGuard overlay. Its mere
// presence (cfg.Mesh != nil) means the user has opted in at least once; Enabled
// gates whether the data plane should come up.
type MeshConfig struct {
	Enabled bool `json:"enabled"`
	// Disabled is the explicit opt-OUT for the default-on overlay. `yaver mesh
	// down` sets it so the agent does NOT auto-rejoin on the next `yaver serve`.
	// Distinct from !Enabled (which just means "not currently up"): default-on
	// treats a nil/!Disabled config as "bring mesh up", and only a true Disabled
	// (or the YAVER_MESH_DISABLE env) keeps it off.
	Disabled bool `json:"disabled,omitempty"`
	// PublicKey is the base64 WireGuard public key. The private half lives in
	// the vault (project "mesh", name "wg_private_key") and is NEVER persisted
	// here or synced to Convex.
	PublicKey string `json:"public_key,omitempty"`
	// MeshIPv4 is the overlay address assigned by the control plane on join.
	MeshIPv4 string `json:"mesh_ipv4,omitempty"`
	MeshIPv6 string `json:"mesh_ipv6,omitempty"`
	// AdvertisedRoutes / ExitNode are Phase 5 (subnet router / exit node).
	// This node ADVERTISES these to peers.
	AdvertisedRoutes []string `json:"advertised_routes,omitempty"`
	ExitNode         bool     `json:"exit_node,omitempty"`
	// UseExitNode is the deviceId of an exit node THIS node routes its default
	// traffic through. Empty = no exit node (the safe default — an advertised
	// 0.0.0.0/0 never captures a peer's traffic unless it explicitly opts in).
	UseExitNode  string `json:"use_exit_node,omitempty"`
	LastJoinedAt int64  `json:"last_joined_at,omitempty"`
}

// tailnetInteropRoutes is the Tailscale CGNAT range (100.64.0.0/10) split so it
// EXCLUDES the Yaver Mesh overlay subrange (100.96.0.0/12). 100.64/11 covers
// 100.64–100.95 and 100.112/12 covers 100.112–100.127; together they are
// 100.64/10 minus 100.96/12. Advertising these (not raw 100.64/10) lets mesh
// peers reach tailnet hosts through a dual-homed node without the route
// capturing overlay traffic or being rejected by the peer-side overlap filter.
var tailnetInteropRoutes = []string{"100.64.0.0/11", "100.112.0.0/12"}

const (
	meshVaultProject = "mesh"
	meshVaultKeyName = "wg_private_key"
	// meshListenPort is the default WireGuard UDP port the data plane will bind
	// in Phase 1. Advertised now so peers know where to send.
	meshListenPort = 51820
)

func runMesh(args []string) {
	if len(args) == 0 {
		printMeshUsage()
		return
	}
	switch args[0] {
	case "up", "enable", "join":
		runMeshUp(args[1:])
	case "down", "disable", "leave":
		runMeshDown(args[1:])
	case "status", "":
		runMeshStatus()
	case "ip":
		runMeshIP()
	case "key", "pubkey":
		runMeshKey()
	case "exit-node":
		runMeshExitNode(args[1:])
	case "route":
		runMeshRoute(args[1:])
	case "help", "-h", "--help":
		printMeshUsage()
	default:
		fmt.Fprintf(os.Stderr, "unknown mesh subcommand %q\n\n", args[0])
		printMeshUsage()
		os.Exit(1)
	}
}

func printMeshUsage() {
	fmt.Println("Yaver Mesh — optional WireGuard overlay (Tailscale alternative)")
	fmt.Println()
	fmt.Println("Usage:")
	fmt.Println("  yaver mesh up        Opt in: generate keys, register, assign overlay IP")
	fmt.Println("  yaver mesh down      Opt out: mark this node offline in the mesh")
	fmt.Println("  yaver mesh status    Show mesh state for this device")
	fmt.Println("  yaver mesh ip        Print this device's overlay IP")
	fmt.Println("  yaver mesh key       Print this device's WireGuard public key")
	fmt.Println()
	fmt.Println("  yaver mesh exit-node enable|disable   Advertise this node as an exit node")
	fmt.Println("  yaver mesh exit-node use <alias>      Route THIS node's traffic via an exit node")
	fmt.Println("  yaver mesh exit-node clear            Stop using an exit node")
	fmt.Println("  yaver mesh route advertise <cidr>     Advertise a subnet route (subnet router)")
	fmt.Println("  yaver mesh route advertise --tailscale  Bridge your tailnet — mesh peers reach Tailscale hosts via this node")
	fmt.Println("  yaver mesh route remove <cidr>        Withdraw a subnet route")
	fmt.Println()
	fmt.Println("The mesh is OFF until you run `yaver mesh up`. Disabling it changes")
	fmt.Println("nothing else about how Yaver connects (relay/LAN/QUIC are unaffected).")
}

// meshLoadOrCreateKeyPair returns the device's WireGuard keypair, generating and
// persisting the private half to the vault on first use. The public key is
// recomputed from the private key so we only ever store one secret.
func meshLoadOrCreateKeyPair() (mesh.KeyPair, error) {
	vs, err := openVaultE()
	if err != nil {
		return mesh.KeyPair{}, fmt.Errorf("open vault: %w", err)
	}
	if entry, err := vs.Get(meshVaultProject, meshVaultKeyName); err == nil && entry != nil && entry.Value != "" {
		pub, perr := mesh.PublicFromPrivate(entry.Value)
		if perr != nil {
			return mesh.KeyPair{}, fmt.Errorf("derive public key from stored private: %w", perr)
		}
		return mesh.KeyPair{PrivateKey: entry.Value, PublicKey: pub}, nil
	}

	kp, err := mesh.GenerateKeyPair()
	if err != nil {
		return mesh.KeyPair{}, err
	}
	if err := vs.Set(VaultEntry{
		Project: meshVaultProject,
		Name:    meshVaultKeyName,
		Value:   kp.PrivateKey,
	}); err != nil {
		return mesh.KeyPair{}, fmt.Errorf("store private key in vault: %w", err)
	}
	return kp, nil
}

// meshLocalEndpoints gathers the host:port UDP candidates this node advertises
// for WireGuard. Privacy-equivalent to the LAN IPs already published on the
// devices table — non-loopback, non-link-local unicast addresses only.
func meshLocalEndpoints() []string {
	var out []string
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return out
	}
	for _, a := range addrs {
		ipnet, ok := a.(*net.IPNet)
		if !ok {
			continue
		}
		ip := ipnet.IP
		if ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
			continue
		}
		// Privacy contract: RFC1918 LAN IPs must NEVER be published to Convex —
		// meshNodes.endpoints is shared cross-tenant to grant counterparties, so
		// a private address would leak the user's internal subnet layout. Only
		// globally-routable on-interface IPs (e.g. a cloud box's public IP) and
		// the STUN-discovered public endpoint go to the control plane. Same-LAN
		// direct paths are exchanged P2P/relay-brokered, not via Convex.
		if ip.IsPrivate() {
			continue
		}
		if v4 := ip.To4(); v4 != nil {
			out = append(out, fmt.Sprintf("%s:%d", v4.String(), meshListenPort))
		}
	}
	sort.Strings(out)

	// Phase 2: discover the public endpoint via STUN so peers behind NAT can
	// reach us directly. Best-effort + bounded — a failure just means we fall
	// back to LAN endpoints + the relay path. Listed last (LAN is preferred when
	// peers share a subnet); deduped against the LAN list.
	if pub, err := mesh.DiscoverPublicIP(2 * time.Second); err == nil && pub.IsValid() {
		pubEndpoint := fmt.Sprintf("%s:%d", pub.String(), meshListenPort)
		dup := false
		for _, e := range out {
			if e == pubEndpoint {
				dup = true
				break
			}
		}
		if !dup {
			out = append(out, pubEndpoint)
		}
	}
	return out
}

func runMeshUp(args []string) {
	// Prefer the running daemon — it owns the long-lived WireGuard TUN, so the
	// overlay survives this CLI process exiting. The daemon handler does the
	// keygen + control-plane join + data-plane bring-up in one shot.
	if res, err := localAgentRequest("POST", "/mesh/up", nil); err == nil {
		fmt.Println("✓ Yaver Mesh: joined")
		if ip, ok := res["meshIPv4"].(string); ok {
			fmt.Printf("  overlay IP : %s\n", meshOrDash(ip))
		}
		if pk, ok := res["publicKey"].(string); ok {
			fmt.Printf("  public key : %s\n", pk)
		}
		if warn, ok := res["dataPlaneWarning"].(string); ok && warn != "" {
			fmt.Printf("\n  ⚠ data plane not active: %s\n", warn)
			fmt.Println("    Run the agent with elevated privilege to create the TUN")
			fmt.Println("    (the control-plane registration above is already live).")
		} else {
			fmt.Println("\n  Data plane active — peers reachable over the overlay IP.")
		}
		return
	}
	// No daemon reachable: register the control plane directly so peers can see
	// this node, then point the user at `yaver serve` for the data plane.
	runMeshUpDirect(args)
}

// runMeshUpDirect registers this device with the mesh control plane WITHOUT a
// running daemon. It cannot create the TUN (that needs the long-lived serve
// process), so it explains the next step.
func runMeshUpDirect(_ []string) {
	cfg, err := LoadConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: load config: %v\n", err)
		os.Exit(1)
	}
	if cfg.AuthToken == "" || cfg.ConvexSiteURL == "" || cfg.DeviceID == "" {
		fmt.Fprintln(os.Stderr, "Error: not signed in. Run `yaver auth` first.")
		os.Exit(1)
	}

	kp, err := meshLoadOrCreateKeyPair()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	endpoints := meshLocalEndpoints()
	args := map[string]interface{}{
		"deviceId":    cfg.DeviceID,
		"wgPublicKey": kp.PublicKey,
		"endpoints":   endpoints,
	}
	if cfg.Mesh != nil {
		if len(cfg.Mesh.AdvertisedRoutes) > 0 {
			args["advertisedRoutes"] = cfg.Mesh.AdvertisedRoutes
		}
		if cfg.Mesh.ExitNode {
			args["isExitNode"] = true
		}
	}

	raw, err := meshConvexCall(cfg, "mutation", "mesh:joinMesh", args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: register with control plane: %v\n", err)
		os.Exit(1)
	}
	var assigned struct {
		MeshIPv4 string `json:"meshIPv4"`
		MeshIPv6 string `json:"meshIPv6"`
	}
	_ = json.Unmarshal(raw, &assigned)

	if cfg.Mesh == nil {
		cfg.Mesh = &MeshConfig{}
	}
	cfg.Mesh.Enabled = true
	cfg.Mesh.PublicKey = kp.PublicKey
	cfg.Mesh.MeshIPv4 = assigned.MeshIPv4
	cfg.Mesh.MeshIPv6 = assigned.MeshIPv6
	cfg.Mesh.LastJoinedAt = time.Now().Unix()
	if err := SaveConfig(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "Error: save config: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("✓ Yaver Mesh: joined")
	fmt.Printf("  overlay IP : %s\n", meshOrDash(assigned.MeshIPv4))
	fmt.Printf("  public key : %s\n", kp.PublicKey)
	if len(endpoints) > 0 {
		fmt.Printf("  endpoints  : %v\n", endpoints)
	}
	fmt.Println()
	fmt.Println("  Control plane registered. The WireGuard data plane runs inside the")
	fmt.Println("  agent daemon — start it with privilege to bring the overlay up:")
	fmt.Println("      sudo yaver serve     # or restart your running agent")
	fmt.Println("  Then `yaver mesh status` shows live peers.")
}

func runMeshDown(_ []string) {
	// Prefer the daemon so the live TUN is actually torn down.
	if _, err := localAgentRequest("POST", "/mesh/down", nil); err == nil {
		fmt.Println("✓ Yaver Mesh: left. The WireGuard private key is kept in the vault")
		fmt.Println("  so re-joining later reuses the same overlay IP.")
		return
	}
	cfg, err := LoadConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: load config: %v\n", err)
		os.Exit(1)
	}
	if cfg.Mesh == nil || !cfg.Mesh.Enabled {
		fmt.Println("Yaver Mesh is already off for this device.")
		return
	}
	if cfg.AuthToken != "" && cfg.ConvexSiteURL != "" && cfg.DeviceID != "" {
		if _, err := meshConvexCall(cfg, "mutation", "mesh:leaveMesh", map[string]interface{}{
			"deviceId": cfg.DeviceID,
		}); err != nil {
			// Non-fatal: we still flip local state off so the data plane stays down.
			fmt.Fprintf(os.Stderr, "warning: control-plane leave failed: %v\n", err)
		}
	}
	cfg.Mesh.Enabled = false
	cfg.Mesh.Disabled = true // explicit opt-out: don't auto-rejoin on next serve
	if err := SaveConfig(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "Error: save config: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("✓ Yaver Mesh: left. The WireGuard private key is kept in the vault")
	fmt.Println("  so re-joining later reuses the same overlay IP.")
}

func runMeshStatus() {
	// Prefer the daemon: it has the live data-plane snapshot (interface name,
	// per-peer WireGuard handshakes) the standalone CLI can't see.
	if res, err := localAgentRequest("GET", "/mesh/status", nil); err == nil {
		printMeshDaemonStatus(res)
		return
	}
	cfg, err := LoadConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: load config: %v\n", err)
		os.Exit(1)
	}
	if cfg.Mesh == nil {
		fmt.Println("Yaver Mesh: not configured (opt-in with `yaver mesh up`).")
		return
	}
	state := "disabled"
	if cfg.Mesh.Enabled {
		state = "enabled (control plane); data plane: Phase 1"
	}
	fmt.Println("Yaver Mesh status")
	fmt.Printf("  state      : %s\n", state)
	fmt.Printf("  overlay IP : %s\n", meshOrDash(cfg.Mesh.MeshIPv4))
	fmt.Printf("  public key : %s\n", meshOrDash(cfg.Mesh.PublicKey))
	if cfg.Mesh.ExitNode {
		fmt.Println("  exit node  : advertised")
	}
	if len(cfg.Mesh.AdvertisedRoutes) > 0 {
		fmt.Printf("  routes     : %v\n", cfg.Mesh.AdvertisedRoutes)
	}

	if !cfg.Mesh.Enabled || cfg.AuthToken == "" || cfg.ConvexSiteURL == "" {
		return
	}
	raw, err := meshConvexCall(cfg, "query", "mesh:listMeshPeers", map[string]interface{}{})
	if err != nil {
		fmt.Printf("  peers      : (control-plane query failed: %v)\n", err)
		return
	}
	var resp struct {
		Peers []struct {
			DeviceID    string `json:"deviceId"`
			MeshIPv4    string `json:"meshIPv4"`
			Online      bool   `json:"online"`
			AccessScope string `json:"accessScope"`
		} `json:"peers"`
	}
	_ = json.Unmarshal(raw, &resp)
	others := 0
	fmt.Println("  peers      :")
	for _, p := range resp.Peers {
		if p.DeviceID == cfg.DeviceID {
			continue
		}
		others++
		live := "offline"
		if p.Online {
			live = "online"
		}
		fmt.Printf("    - %s  %s  %s  (%s)\n", p.MeshIPv4, p.DeviceID, live, p.AccessScope)
	}
	if others == 0 {
		fmt.Println("    (none yet — bring another device up with `yaver mesh up`)")
	}
}

func runMeshIP() {
	cfg, err := LoadConfig()
	if err != nil || cfg.Mesh == nil || cfg.Mesh.MeshIPv4 == "" {
		fmt.Fprintln(os.Stderr, "no overlay IP assigned — run `yaver mesh up` first")
		os.Exit(1)
	}
	fmt.Println(cfg.Mesh.MeshIPv4)
}

func runMeshKey() {
	cfg, err := LoadConfig()
	if err != nil || cfg.Mesh == nil || cfg.Mesh.PublicKey == "" {
		fmt.Fprintln(os.Stderr, "no mesh key yet — run `yaver mesh up` first")
		os.Exit(1)
	}
	fmt.Println(cfg.Mesh.PublicKey)
}

// printMeshDaemonStatus renders the /mesh/status JSON, including the live
// data-plane snapshot (interface + per-peer handshakes) when the overlay is up.
func printMeshDaemonStatus(res map[string]interface{}) {
	fmt.Println("Yaver Mesh status")
	enabled, _ := res["enabled"].(bool)
	if !enabled {
		fmt.Println("  state      : disabled (opt-in with `yaver mesh up`)")
		return
	}
	ip, _ := res["meshIPv4"].(string)
	pk, _ := res["publicKey"].(string)
	fmt.Printf("  overlay IP : %s\n", meshOrDash(ip))
	fmt.Printf("  public key : %s\n", meshOrDash(pk))

	dp, ok := res["dataPlane"].(map[string]interface{})
	if !ok {
		fmt.Println("  data plane : not running (start the agent with privilege)")
		return
	}
	running, _ := dp["running"].(bool)
	if !running {
		fmt.Println("  data plane : not running")
		if le, _ := dp["lastError"].(string); le != "" {
			fmt.Printf("  last error : %s\n", le)
		}
		return
	}
	iface, _ := dp["ifaceName"].(string)
	fmt.Printf("  data plane : up on %s\n", meshOrDash(iface))
	peers, _ := dp["peers"].([]interface{})
	fmt.Println("  peers      :")
	if len(peers) == 0 {
		fmt.Println("    (none yet — bring another device up with `yaver mesh up`)")
		return
	}
	for _, p := range peers {
		pm, ok := p.(map[string]interface{})
		if !ok {
			continue
		}
		endpoint, _ := pm["Endpoint"].(string)
		hs, _ := pm["LastHandshakeUnix"].(float64)
		state := "no handshake"
		if hs > 0 {
			state = "handshook " + time.Unix(int64(hs), 0).Format("15:04:05")
		}
		fmt.Printf("    - %s  %s\n", meshOrDash(endpoint), state)
	}
}

func appendUnique(list []string, v string) []string {
	for _, x := range list {
		if x == v {
			return list
		}
	}
	return append(append([]string{}, list...), v)
}

// runMeshExitNode handles `yaver mesh exit-node enable|disable|use <alias>|clear`.
func runMeshExitNode(args []string) {
	if len(args) == 0 {
		fmt.Println("Usage: yaver mesh exit-node enable|disable|use <alias>|clear")
		return
	}
	cfg, err := LoadConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	if cfg.Mesh == nil {
		cfg.Mesh = &MeshConfig{}
	}
	switch args[0] {
	case "enable":
		cfg.Mesh.ExitNode = true
		fmt.Println("✓ Advertising this node as an exit node (default route).")
	case "disable":
		cfg.Mesh.ExitNode = false
		fmt.Println("✓ No longer advertising as an exit node.")
	case "use":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "Error: yaver mesh exit-node use <alias|deviceId>")
			os.Exit(1)
		}
		deviceID := meshResolvePeerDeviceID(cfg, args[1])
		if deviceID == "" {
			fmt.Fprintf(os.Stderr, "Error: no mesh peer matches %q\n", args[1])
			os.Exit(1)
		}
		cfg.Mesh.UseExitNode = deviceID
		fmt.Printf("✓ Routing this node's default traffic through %s.\n", args[1])
	case "clear":
		cfg.Mesh.UseExitNode = ""
		fmt.Println("✓ Stopped using an exit node.")
	default:
		fmt.Fprintf(os.Stderr, "unknown exit-node subcommand %q\n", args[0])
		os.Exit(1)
	}
	meshSaveAndReapply(cfg)
}

// runMeshRoute handles `yaver mesh route advertise|remove <cidr>`.
func runMeshRoute(args []string) {
	if len(args) < 2 {
		fmt.Println("Usage: yaver mesh route advertise|remove <cidr>")
		return
	}
	cfg, err := LoadConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	if cfg.Mesh == nil {
		cfg.Mesh = &MeshConfig{}
	}
	cidr := args[1]
	// Tailscale interop: `--tailscale` advertises the tailnet CGNAT range so
	// mesh peers reach tailnet hosts through this dual-homed node. We advertise
	// 100.64/10 MINUS the overlay subrange (100.96/12) as two constituent CIDRs
	// — advertising raw 100.64/10 would let this route capture overlay traffic
	// (100.96/12 ⊂ 100.64/10) and get rejected by the peer-side overlap filter.
	tailscaleRoute := cidr == "--tailscale" || cidr == "tailscale"
	routes := []string{cidr}
	if tailscaleRoute {
		routes = tailnetInteropRoutes
	}
	switch args[0] {
	case "advertise", "add":
		if tailscaleRoute && !localTailscaleUp() {
			fmt.Fprintln(os.Stderr, "Error: --tailscale requires a running tailnet on this node (no 100.64/10 interface found).")
			os.Exit(1)
		}
		for _, r := range routes {
			cfg.Mesh.AdvertisedRoutes = appendUnique(cfg.Mesh.AdvertisedRoutes, r)
		}
		if tailscaleRoute {
			fmt.Printf("✓ Advertising the tailnet (%s) — mesh peers can now reach your Tailscale hosts through this node.\n", strings.Join(routes, ", "))
		} else {
			fmt.Printf("✓ Advertising subnet route %s (this node is now a subnet router).\n", cidr)
		}
	case "remove", "rm":
		drop := map[string]bool{}
		for _, r := range routes {
			drop[r] = true
		}
		out := cfg.Mesh.AdvertisedRoutes[:0]
		for _, r := range cfg.Mesh.AdvertisedRoutes {
			if !drop[r] {
				out = append(out, r)
			}
		}
		cfg.Mesh.AdvertisedRoutes = out
		fmt.Printf("✓ Withdrew subnet route(s) %s.\n", strings.Join(routes, ", "))
	default:
		fmt.Fprintf(os.Stderr, "unknown route subcommand %q\n", args[0])
		os.Exit(1)
	}
	meshSaveAndReapply(cfg)
}

// meshResolvePeerDeviceID maps an alias/deviceId hint to a mesh peer's deviceId
// by querying the control plane.
func meshResolvePeerDeviceID(cfg *Config, hint string) string {
	raw, err := meshConvexCall(cfg, "query", "mesh:listMeshPeers", map[string]interface{}{})
	if err != nil {
		return ""
	}
	var resp struct {
		Peers []struct {
			DeviceID string `json:"deviceId"`
			Alias    string `json:"alias"`
		} `json:"peers"`
	}
	_ = json.Unmarshal(raw, &resp)
	for _, p := range resp.Peers {
		if p.DeviceID == hint || strings.EqualFold(p.Alias, hint) {
			return p.DeviceID
		}
	}
	return ""
}

// meshSaveAndReapply persists config and restarts the data plane (down+up via
// the daemon) so route/exit-node/forwarding changes take effect, re-registering
// the new advertisement with the control plane.
func meshSaveAndReapply(cfg *Config) {
	if err := SaveConfig(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "Error: save config: %v\n", err)
		os.Exit(1)
	}
	if !cfg.Mesh.Enabled {
		fmt.Println("  (mesh is off — `yaver mesh up` to apply)")
		return
	}
	// Restart the data plane so forwarding/NAT + re-registration apply cleanly.
	if _, err := localAgentRequest("POST", "/mesh/down", nil); err == nil {
		if _, err := localAgentRequest("POST", "/mesh/up", nil); err == nil {
			fmt.Println("  Applied — mesh restarted with new settings.")
			return
		}
	}
	fmt.Println("  Saved. Restart the mesh to apply: yaver mesh down && yaver mesh up")
}

func meshOrDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

// meshConvexCall invokes a Convex mutation or query and returns the raw `value`
// field. It mirrors convexSyncer.callMutation's transport (POST {path,args,
// format:json} with the bearer token) but, unlike that fire-and-forget helper,
// it RETURNS the result body — joinMesh needs the assigned overlay IP and
// listMeshPeers needs the peer list.
func meshConvexCall(cfg *Config, kind, path string, args map[string]interface{}) (json.RawMessage, error) {
	endpoint := cfg.ConvexSiteURL + "/api/" + kind
	body, _ := json.Marshal(map[string]interface{}{
		"path":   path,
		"args":   args,
		"format": "json",
	})
	req, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+cfg.AuthToken)
	client := &http.Client{Timeout: 15 * time.Second}
	res, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	var parsed struct {
		Status       string          `json:"status"`
		Value        json.RawMessage `json:"value"`
		ErrorMessage string          `json:"errorMessage"`
	}
	if err := json.NewDecoder(res.Body).Decode(&parsed); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	if res.StatusCode >= 400 || parsed.Status == "error" {
		msg := parsed.ErrorMessage
		if msg == "" {
			msg = fmt.Sprintf("HTTP %d", res.StatusCode)
		}
		return nil, fmt.Errorf("%s", msg)
	}
	return parsed.Value, nil
}
