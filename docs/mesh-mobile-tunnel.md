# Yaver Mesh — mobile on-device tunnel (Phase 7)

**Status: JS half SHIPPED; native half scaffolded, not yet activated.** The
RN-side shim (`src/lib/yaverMesh.ts`) and the "This phone → Connect to mesh"
card in `app/(tabs)/network.tsx` ship today — with no native module present they
no-op (`{ supported: false }`) and the card shows a "coming in a native update"
hint, so they are safe in every current build. The native half (the phone
actually *carrying* mesh traffic) is a native-toolchain + Apple-entitlement task
that can't be compiled or verified from the agent/CI environment.

**Scaffolded (tracked, NOT activated):**
- `mobile/native-mesh/ios/PacketTunnelProvider.swift` — NEPacketTunnelProvider.
- `mobile/native-mesh/ios/YaverMeshModule.swift` + `.m` — the `NativeModules.YaverMesh`
  RN bridge (ensureKeyPair/up/down/status; private key stays in the Keychain).
- `mobile/native-mesh/android/YaverMeshVpnService.kt` — Android VpnService.
- `mobile/plugins/withMeshTunnel.js` — Expo config plugin (entitlement, App
  Group, Android manifest service + permission, WireGuard deps). **Deliberately
  NOT in app.json `plugins`** — registering it is the activation step and must
  land together with the Apple entitlement + a native rebuild, or prebuild
  output changes in a build no one verified.

**Activation checklist (on the Mac, when ready):**
1. Grant the Network Extension capability on the App ID + regenerate the
   provisioning profile (Apple-gated).
2. Add `"./plugins/withMeshTunnel"` to `mobile/app.json` → `expo.plugins`.
3. Finish the iOS extension *target* creation in the plugin (the one PBX piece
   left as a `TODO(activation)` — use `@config-plugins/apple-target` or an
   xcode-mod; provider bundle id must equal `io.yaver.mobile.YaverMeshTunnel`).
4. `expo prebuild --clean` → restore force-tracked iOS overlays → pod install →
   native rebuild → verify on a real device (Simulator NE is unreliable).

Until this lands, the phone is a **mesh console only** (`app/(tabs)/network.tsx`):
it shows peers, edits ACLs, toggles exit-node/routes — but does not get its own
overlay IP. Desktop/server agents form the actual data plane.

## What it adds

The phone becomes a full mesh node with its own `100.96.x.x` overlay IP, so
`ssh`, HTTP, and any TCP/UDP to a peer's mesh IP work from the device, and the
phone can route through an exit node.

## Why it's a separate, hardware-bound task

1. **iOS Network Extension entitlement** — `com.apple.developer.networking.networkextension`
   with the `packet-tunnel-provider` value must be added to the App ID in the
   Apple Developer portal and to a new provisioning profile. Apple gates this;
   it is not something the codebase can grant itself.
2. **New Xcode target** — a Packet Tunnel Provider is a separate app extension
   target in `Yaver.xcodeproj`. `expo prebuild --clean` regenerates the iOS
   project, so the target must be (re)created by a config plugin, not hand-edited
   pbxproj (which would be wiped). See `mobile/ios/` force-tracked-overlay rules
   in CLAUDE.md.
3. **WireGuard mobile libraries** — iOS uses `WireGuardKit` (SwiftPM/pod);
   Android uses the `com.wireguard.android:tunnel` GoBackend (`tunnel.aar`).
   Both require a native rebuild (pod install / gradle), ~30–60 min cold.
4. **On-device verification** — a VPN tunnel can only be confirmed on a real
   device (Simulator NEPacketTunnelProvider is unreliable; Android VpnService
   needs a device).

## Control-plane reuse (no new backend)

The phone joins the mesh exactly like a desktop agent, via the existing Convex
control plane:

- Generate a Curve25519 keypair on device; **private key stays in the iOS
  Keychain / Android Keystore**, never synced (same contract as the agent vault).
- `POST {CONVEX_SITE_URL}/mesh/...` — the mobile app already holds the session
  token. We add a thin `mesh:joinMeshWeb` (token-hash) mutation mirroring the
  agent's `joinMesh` so the phone can register its pubkey + endpoints and get an
  overlay IP. (Agent path uses `ctx.auth`; mobile uses token-hash — same split
  as `listMeshPeersWeb`.)
- Peers come from `GET /mesh/peers` (already built). The tunnel configures one
  WireGuard peer per row, exactly like `buildMeshPeerSource` on the agent.
- Relay-as-DERP: the phone behind CGNAT will usually have no direct path; it can
  reuse the same relay mesh-stream protocol (`mesh_relay`) — see
  `desktop/agent/mesh/derp.go` + `relay/mesh.go`. On mobile this is optional for
  v1 (rely on STUN + the relay's TURN at :3478 first).

## iOS — Packet Tunnel Provider

Reference: `mobile/native-mesh/ios/PacketTunnelProvider.swift`.

Steps:
1. Add capability in the Apple Developer portal: App ID → Network Extensions →
   Packet Tunnel. Regenerate the provisioning profile (the TestFlight flow in
   CLAUDE.md will pick it up).
2. Add a config plugin (`mobile/plugins/withMeshTunnel.js`) that, on
   `expo prebuild`, (a) creates the `YaverMeshTunnel` app-extension target,
   (b) adds the `packet-tunnel-provider` entitlement to both the app and the
   extension, (c) adds an App Group (`group.io.yaver.mesh`) so the app and
   extension share config, (d) adds the `WireGuardKit` SwiftPM dependency.
3. Add `PacketTunnelProvider.swift` to the extension target.
4. From RN, start/stop via a small native module wrapping
   `NETunnelProviderManager` (load/save a `NETunnelProviderProtocol`, then
   `startVPNTunnel()`).

## Android — VpnService

Reference: `mobile/native-mesh/android/YaverMeshVpnService.kt`.

Steps:
1. Add `com.wireguard.android:tunnel` to `mobile/android/app/build.gradle` (or
   bundle `tunnel.aar`).
2. Add the service + `BIND_VPN_SERVICE` permission to `AndroidManifest.xml`
   (via an expo config plugin so prebuild doesn't wipe it).
3. Foreground service + persistent notification (Android requires it for VPNs).
4. RN bridge: `VpnService.prepare()` (consent dialog) → start the service with
   the WireGuard config string built from `/mesh/peers`.

## RN integration

- Extend `app/(tabs)/network.tsx`: when the on-device tunnel is available, show a
  big **Connect / Disconnect** toggle (Tailscale-style) for *this phone*, in
  addition to the existing peer/ACL management.
- A `NativeModules.YaverMesh` shim with `joinMesh()`, `up()`, `down()`,
  `status()` backed by the native providers; no-ops (returns
  `{ supported: false }`) on builds without the extension so the JS is safe to
  ship before the native side lands.

## Acceptance

- Phone gets a `100.96.x.x` IP visible in `yaver mesh status` on a peer.
- `ping`/`ssh` from a desktop peer to the phone's overlay IP works on the same
  LAN, then across the internet (direct), then via relay-DERP behind CGNAT.
- Toggling an exit node on the phone routes its traffic through that node.
