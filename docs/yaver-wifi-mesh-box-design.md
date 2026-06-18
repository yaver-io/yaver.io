# Yaver Wi-Fi Mesh for Fast Prototyping Boxes

This is the physical RF/backhaul layer for Yaver Box-style fast prototyping.
It is separate from **Yaver Mesh** (`yaver mesh up`), which is the secure
WireGuard overlay. The two compose:

```
phone / laptop / box UI
        │
Yaver app / agent HTTP / QUIC / Modbus / streams
        │
optional Yaver Mesh overlay (WireGuard ACLs, .mesh names, relay fallback)
        │
Wi-Fi substrate: AP, AP+STA repeater, 802.11s, BATMAN-adv, Ethernet, LTE
```

## Product goal

For a prototype lab, robot bench, factory corner, disaster demo, or Teknofest
case, a user should be able to drop two or more Yaver boxes into a space and get:

- local reachability even when there is no router or internet;
- multi-hop box-to-box backhaul when there are several boxes;
- a phone-friendly access point for iOS/Android clients;
- optional Yaver Mesh overlay on top for identity, ACLs, `.mesh` names, and
  relay fallback when the RF fabric is absent.

## The honest RF stack

### Mode A — SoftAP island

One box runs an AP with `hostapd` + `dnsmasq`; phones/laptops join it directly.
This is the most reliable mode for iOS/Android because mobile OSes join normal
APs, not 802.11s mesh points.

Use when:

- one Yaver box is the local hub;
- the phone must join directly;
- no multi-hop is needed.

Implemented as `/console/wifi/*` and `wifi_*` ops.

### Mode B — AP+STA repeater

One radio joins upstream Wi-Fi as STA and exposes a local AP. This is useful for
daily use and demos, but simultaneous AP+STA depends on the Wi-Fi chip's
`iw phy` valid interface combinations.

Use when:

- there is an upstream router/hotspot;
- the Yaver box should also expose a local Yaver SSID.

Implemented as `/console/wifi/*` and `wifi_*` ops. Hardware lab validation is
still required.

### Mode C — 802.11s mesh point

Linux boxes form an 802.11s mesh with `iw` + `wpa_supplicant` (`mode=5`). This
is the clean standard substrate for box-to-box RF backhaul.

Use when:

- all mesh participants are Linux/OpenWrt-style boxes;
- multi-hop local backhaul matters;
- phones are not expected to join the mesh directly.

Implemented as `/console/wifi-mesh/*` and `wifi_mesh_*` ops with backend
`80211s`.

### Mode D — 802.11s + BATMAN-adv

802.11s carries frames between nodes; BATMAN-adv creates a Layer-2 virtual
switch (`bat0`) across the mesh. This is the most practical "drop boxes in a
lab and everything is one LAN" mode.

Use when:

- there are 3+ boxes;
- topology changes;
- L2 discovery/broadcast protocols matter;
- users want one LAN-like fabric under Yaver.

Implemented as `/console/wifi-mesh/*` and `wifi_mesh_*` ops with backend
`batman`. Requires `batman-adv` kernel module and `batctl`.

### Mode E — router/OpenWrt managed mesh

If the Yaver Box is an OpenWrt router or GL.iNet-style device, let OpenWrt own
Wi-Fi and expose Yaver as the control/UI layer. Do not fight netifd/hostapd in
that environment.

Future wrapper target:

- read OpenWrt UCI status;
- write a Yaver profile through UCI;
- restart only the managed wireless network;
- keep Yaver's agent API identical to Linux-hosted mode.

## What was implemented in the agent

Files:

- `desktop/agent/wifi_mesh.go`
- `desktop/agent/wifi_hotspot_http.go`
- `desktop/agent/ops_wifi.go`
- `desktop/agent/wifi_mesh_test.go`

HTTP:

- `GET /console/wifi-mesh/capabilities`
- `GET /console/wifi-mesh/status`
- `POST /console/wifi-mesh/start`
- `POST /console/wifi-mesh/stop`

Ops:

- `wifi_mesh_capabilities`
- `wifi_mesh_status`
- `wifi_mesh_start`
- `wifi_mesh_stop`

Config shape:

```json
{
  "meshId": "yaver-lab",
  "passphrase": "shared-secret",
  "interface": "wlan0",
  "meshInterface": "mesh0",
  "backend": "batman",
  "channel": 6,
  "frequencyMhz": 2437,
  "ipAddress": "10.47.0.1/24",
  "countryCode": "US"
}
```

For backend `80211s`, the IP is assigned to `meshInterface`. For backend
`batman`, the mesh interface is attached to `bat0` and the IP is assigned to
`bat0`.

## Recommended Yaver Box prototype topology

Use two Wi-Fi radios when possible:

- radio 1: 802.11s/BATMAN backhaul between boxes;
- radio 2: SoftAP for phones/tablets/laptops.

Single-radio prototypes can work, but every chip has different valid interface
combinations, and throughput drops when one radio handles both backhaul and AP
clients.

Suggested lab defaults:

- mesh ID: `yaver-lab`;
- mesh CIDR: `10.47.0.0/16`;
- node IPs: static at first (`10.47.0.11/16`, `10.47.0.12/16`, ...);
- phone AP CIDR: `192.168.47.0/24`;
- overlay: run `yaver mesh up` over the RF fabric when identity/ACLs matter.

## Why not make phones join the mesh directly?

iOS and Android join normal Wi-Fi APs. They do not behave like generic Linux
802.11s mesh nodes. The phone-facing surface should be SoftAP. The box-facing
backhaul can be 802.11s/BATMAN. This is why the Yaver Box design should expose
both "AP for humans" and "mesh for boxes" as separate but composable controls.

## Safety and limits

- This is local RF infrastructure. It is not internet by itself.
- LoRa/Meshtastic remains the right long-range, low-bandwidth emergency mesh.
- Wi-Fi mesh is short range and power hungry compared with LoRa.
- Region/country code matters. Do not auto-pick illegal channels.
- Starting/stopping this layer requires root because it changes kernel network
  interfaces and may load kernel modules.

## Source notes

- Linux wireless docs show 802.11s mesh points being created with `iw` and
  managed by `wpa_supplicant`:
  https://wireless.docs.kernel.org/en/latest/en/users/drivers/ath10k/mesh.html
- Linux kernel BATMAN-adv docs describe it as a Layer-2 virtual switch across
  mesh nodes:
  https://docs.kernel.org/networking/batman-adv.html
- OpenWrt documents 802.11s mesh as the standards-based wireless mesh path:
  https://openwrt.org/docs/guide-user/network/wifi/mesh/802-11s
