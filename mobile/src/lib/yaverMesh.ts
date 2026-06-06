// yaverMesh.ts — RN shim over the on-device Yaver Mesh tunnel (Phase 7).
//
// This is the SAFE-TO-SHIP half of the on-device tunnel. It reads the optional
// `NativeModules.YaverMesh` native module (the NEPacketTunnelProvider bridge on
// iOS / VpnService bridge on Android). Until that native extension is built and
// wired in (Apple NetworkExtension entitlement + `mobile/plugins/withMeshTunnel.js`
// + native rebuild — see docs/mesh-mobile-tunnel.md), the module is absent and
// EVERY call here resolves to a `{ supported: false }` no-op. So this file —
// and the Connect/Disconnect UI built on it — ships today without the native
// side and lights up automatically once the extension lands.
//
// The phone joins the mesh through the existing Convex control plane exactly
// like a desktop agent: it registers its pubkey via POST /mesh/join, pulls
// peers from GET /mesh/peers, and hands the resulting wg-quick config to the
// native provider. The private key never leaves the iOS Keychain / Android
// Keystore (same contract as the desktop vault) — it is generated and held on
// the native side, so it is intentionally absent from this JS layer.

import { NativeModules, Platform } from "react-native";

export type MeshTunnelState = "disconnected" | "connecting" | "connected" | "error";

export type MeshTunnelStatus = {
  supported: boolean;
  state: MeshTunnelState;
  meshIPv4?: string;
  error?: string;
};

type NativeYaverMesh = {
  // Generate (once) the device keypair in the Keychain/Keystore and return the
  // base64 PUBLIC key — the private half never crosses the bridge.
  ensureKeyPair?: () => Promise<string>;
  // Bring the tunnel up with a wg-quick-style config string.
  up?: (wgQuickConfig: string) => Promise<void>;
  // Push a fresh config to a live tunnel without dropping it (peers/ACLs changed).
  reconfigure?: (wgQuickConfig: string) => Promise<void>;
  down?: () => Promise<void>;
  status?: () => Promise<{ state: MeshTunnelState; meshIPv4?: string; error?: string }>;
};

function native(): NativeYaverMesh | undefined {
  return (NativeModules as { YaverMesh?: NativeYaverMesh }).YaverMesh;
}

/**
 * Stable mesh device id for this phone, derived from its public key. The key
 * lives in the Keychain/Keystore and is generated once, so this is constant
 * across joins and unique per install — no separate device registration.
 */
export function meshDeviceIdFromPubKey(publicKey: string): string {
  const slug = publicKey.replace(/[^A-Za-z0-9]/g, "").slice(0, 20).toLowerCase();
  return `phone-${Platform.OS}-${slug}`;
}

/** True only on a build that bundled the native tunnel extension. */
export function isMeshTunnelSupported(): boolean {
  const m = native();
  return !!m && typeof m.up === "function";
}

const UNSUPPORTED: MeshTunnelStatus = { supported: false, state: "disconnected" };

/**
 * Generate-or-load this device's mesh public key from the secure enclave.
 * Returns null when the native module is absent (pre-extension builds).
 */
export async function ensureMeshKeyPair(): Promise<string | null> {
  const m = native();
  if (!m?.ensureKeyPair) return null;
  try {
    return await m.ensureKeyPair();
  } catch {
    return null;
  }
}

/**
 * Register this phone as a mesh node and bring the tunnel up. Joins via the
 * Convex control plane (POST /mesh/join with the device pubkey), pulls peers
 * (GET /mesh/peers), builds the wg-quick config, and hands it to the native
 * provider. No-op `{ supported: false }` when the extension isn't present.
 */
export async function meshTunnelUp(opts: {
  convexSiteUrl: string;
  token: string;
}): Promise<MeshTunnelStatus> {
  const m = native();
  if (!m?.up || !m.ensureKeyPair) return UNSUPPORTED;
  try {
    const publicKey = await m.ensureKeyPair();
    const headers = { Authorization: `Bearer ${opts.token}`, "Content-Type": "application/json" };

    // The phone isn't a fleet device row, so its mesh identity is derived
    // deterministically from its (Keychain-stable) public key — same id every
    // join, unique per install, no extra registration round-trip.
    const deviceId = meshDeviceIdFromPubKey(publicKey);

    // 1. Join: register pubkey + (best-effort) endpoints, receive overlay IP.
    const joinRes = await fetch(`${opts.convexSiteUrl}/mesh/join`, {
      method: "POST",
      headers,
      body: JSON.stringify({ deviceId, wgPublicKey: publicKey, platform: Platform.OS }),
    });
    if (!joinRes.ok) throw new Error(`mesh join: HTTP ${joinRes.status}`);
    const join = (await joinRes.json()) as { meshIPv4?: string };

    // 2. Peers: one [Peer] block per visible mesh node.
    const peersRes = await fetch(`${opts.convexSiteUrl}/mesh/peers`, { headers });
    if (!peersRes.ok) throw new Error(`mesh peers: HTTP ${peersRes.status}`);
    const peers = ((await peersRes.json()).peers ?? []) as Array<{
      wgPublicKey?: string;
      meshIPv4?: string;
      endpoints?: string[];
      advertisedRoutes?: string[];
    }>;

    const cfg = buildWgQuickConfig(join.meshIPv4, peers);
    await m.up(cfg);
    return { supported: true, state: "connected", meshIPv4: join.meshIPv4 };
  } catch (e) {
    return { supported: true, state: "error", error: e instanceof Error ? e.message : String(e) };
  }
}

export async function meshTunnelDown(): Promise<MeshTunnelStatus> {
  const m = native();
  if (!m?.down) return UNSUPPORTED;
  try {
    await m.down();
    return { supported: true, state: "disconnected" };
  } catch (e) {
    return { supported: true, state: "error", error: e instanceof Error ? e.message : String(e) };
  }
}

export async function meshTunnelStatus(): Promise<MeshTunnelStatus> {
  const m = native();
  if (!m?.status) return UNSUPPORTED;
  try {
    const s = await m.status();
    return { supported: true, state: s.state, meshIPv4: s.meshIPv4, error: s.error };
  } catch (e) {
    return { supported: true, state: "error", error: e instanceof Error ? e.message : String(e) };
  }
}

/**
 * Render a wg-quick config string the native WireGuard adapter understands.
 * Mirrors `buildMeshPeerSource` on the desktop agent: self /32 [Interface] +
 * one [Peer] per node (its overlay /32 plus any advertised routes as AllowedIPs).
 * The private key is injected natively (it lives in the Keychain/Keystore), so
 * the [Interface] PrivateKey is a placeholder the native side replaces.
 */
export function buildWgQuickConfig(
  selfMeshIPv4: string | undefined,
  peers: Array<{ wgPublicKey?: string; meshIPv4?: string; endpoints?: string[]; advertisedRoutes?: string[] }>
): string {
  const lines: string[] = ["[Interface]"];
  if (selfMeshIPv4) lines.push(`Address = ${selfMeshIPv4}/12`);
  lines.push("PrivateKey = __KEYCHAIN__"); // native side substitutes the real key
  for (const p of peers) {
    if (!p.wgPublicKey || !p.meshIPv4) continue;
    lines.push("", "[Peer]", `PublicKey = ${p.wgPublicKey}`);
    const allowed = [`${p.meshIPv4}/32`, ...(p.advertisedRoutes ?? [])];
    lines.push(`AllowedIPs = ${allowed.join(", ")}`);
    const ep = (p.endpoints ?? [])[0];
    if (ep) lines.push(`Endpoint = ${ep}`);
    lines.push("PersistentKeepalive = 25");
  }
  return lines.join("\n");
}
