# Yaver Wi-Fi Mesh Handoff

Owner handoff for continuing the Yaver Box Wi-Fi/AP/repeater/mesh work.

## Product direction

Keep Wi-Fi mesh optional. Do not make it a new primary product surface unless the
user explicitly asks. The normal mobile/app console should stay quiet:

- one existing `wifi` tab is enough;
- SoftAP/AP+STA controls are primary;
- box-to-box mesh is an advanced optional section inside Wi-Fi;
- avoid adding more top-level console tabs or broad UI surfaces.

The architecture is:

- phone/tablet/laptop access: normal SoftAP via `hostapd` + `dnsmasq`;
- upstream Wi-Fi sharing: AP+STA when hardware supports it;
- box-to-box backhaul: optional 802.11s with `iw` + `wpa_supplicant`;
- larger local LAN-like fabric: optional 802.11s + BATMAN-adv;
- secure identity/ACL/MagicDNS layer: existing Yaver Mesh WireGuard overlay on
  top, not replaced by Wi-Fi mesh.

## Implemented in this pass

### Agent Wi-Fi hotspot/repeater

Files:

- `desktop/agent/wifi_hotspot.go`
- `desktop/agent/wifi_hotspot_http.go`
- `desktop/agent/ops_wifi.go`
- `desktop/agent/wifi_hotspot_test.go`
- `WIFI_HOTSPOT_IMPLEMENTATION_GUIDE.md`

Implemented Linux lifecycle and API for Yaver-managed SoftAP/AP+STA:

- `GET /console/wifi/capabilities`
- `GET /console/wifi/status`
- `POST /console/wifi/start`
- `POST /console/wifi/stop`
- ops verbs: `wifi_capabilities`, `wifi_status`, `wifi_start`, `wifi_stop`

Uses `hostapd`, `dnsmasq`, `iw`, `ip`, and iptables NAT where requested.

### Agent optional Wi-Fi mesh

Files:

- `desktop/agent/wifi_mesh.go`
- `desktop/agent/wifi_mesh_test.go`
- `desktop/agent/wifi_hotspot_http.go`
- `desktop/agent/ops_wifi.go`
- `desktop/agent/httpserver.go`
- `docs/yaver-wifi-mesh-box-design.md`

Implemented optional Linux 802.11s/BATMAN wrapper:

- `GET /console/wifi-mesh/capabilities`
- `GET /console/wifi-mesh/status`
- `POST /console/wifi-mesh/start`
- `POST /console/wifi-mesh/stop`
- ops verbs: `wifi_mesh_capabilities`, `wifi_mesh_status`,
  `wifi_mesh_start`, `wifi_mesh_stop`

Backends:

- `80211s`: creates/uses a mesh point interface and runs `wpa_supplicant`
  `mode=5`; assigns the configured CIDR to the mesh interface.
- `batman`: same 802.11s substrate, then loads/uses BATMAN-adv through
  `batctl`, attaches the mesh interface to `bat0`, and assigns the configured
  CIDR to `bat0`.

This requires Linux and root privileges at start time.

### Mobile console

File:

- `mobile/app/(tabs)/console.tsx`

Current UI policy:

- keep a single `wifi` top-level tab;
- AP/repeater controls stay visible;
- mesh is behind an optional `Box mesh` expander inside the Wi-Fi tab;
- do not add a separate `mesh` top-level tab.

## Validation already run

From `desktop/agent`:

```sh
go test -run 'TestWiFiMesh|TestIWSupportsMeshPoint|TestChannelToFrequencyMHz|TestParseIWPhyCapabilities|TestComboTotalAtLeastTwo' .
go test -run '^$' .
```

Both passed.

From `mobile`, run again after any UI edits:

```sh
npx tsc --noEmit
```

## Not yet hardware validated

The code compiles and unit tests pass, but real RF lifecycle still needs a Linux
box lab test. Minimum hardware matrix:

- one Linux box with supported AP mode for SoftAP;
- one Linux box/chip that supports AP+STA valid interface combinations;
- two Linux/OpenWrt-style boxes with `iw`, `wpa_supplicant`, and 802.11s mesh
  point support;
- optional BATMAN path with `batman-adv` kernel module and `batctl`.

Test plan:

1. `GET /console/wifi/capabilities` and verify interface/bands/modes.
2. Start SoftAP, join from phone, verify DHCP and local agent reachability.
3. Start AP+STA on supported hardware, verify upstream and local AP both work.
4. On two boxes, start `wifi-mesh` backend `80211s` with different static IPs
   in `10.47.0.0/16`, then ping between boxes.
5. Repeat with backend `batman`, verify `bat0`, `batctl n`, and peer reachability.
6. Run existing `yaver mesh up` over the RF fabric and verify `.mesh` names/ACLs.

## Safety notes

- Do not commit or push without explicit user permission.
- Do not run destructive cleanup commands unless exact paths are inspected first.
- Do not commit credentials, customer IPs, relay hostnames, or secrets.
- Treat docs as stale context; grep code before relying on any route/function
  claim.
- Keep mesh optional and advanced. The main app should not become a networking
  control panel unless requested.

## Useful references

- Linux wireless 802.11s mesh setup:
  https://wireless.docs.kernel.org/en/latest/en/users/drivers/ath10k/mesh.html
- Linux kernel BATMAN-adv docs:
  https://docs.kernel.org/networking/batman-adv.html
- OpenWrt 802.11s docs:
  https://openwrt.org/docs/guide-user/network/wifi/mesh/802-11s
