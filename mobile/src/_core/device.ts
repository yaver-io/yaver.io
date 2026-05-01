// AUTO-SYNCED from shared/client-core/src/device.ts.
// DO NOT EDIT IN PLACE. Edit the source and re-run
// scripts/sync-client-core.sh. CI checks drift via `--check`.

/**
 * Device dedup, merging, freshness, and target-picking.
 *
 * Phase-2 extract of the Yaver-mobile logic that lives at
 * mobile/src/context/DeviceContext.tsx:193-413. Ported faithfully —
 * mobile's collapseAliasDevices has been battle-tested through
 * multiple Convex schema changes and the re-pair edge cases; this
 * file is byte-identical in behaviour, only renamed for the shared
 * RemoteDevice shape. The Feedback SDK used to carry its own copy
 * (sdk/feedback/react-native/src/deviceDedup.ts) that drifted on
 * `hwid` strong-identity and `runners`/`local` field preservation.
 *
 * Canonical device shape shared across every surface:
 *   - Mobile app:  mobile/src/context/DeviceContext.tsx::Device (adapts
 *                  into this shape at the Convex /devices/list fetch
 *                  site).
 *   - Feedback SDK: src/auth.ts::RemoteDevice re-exports this type.
 *   - Web / Desktop: same.
 *
 * The mobile app may have extra view-model fields it wants to keep on
 * its own Device interface (edgeProfile, sessionBinding, etc.) — those
 * stay as local extensions; the core operates only on the fields
 * declared here.
 */

import { HEARTBEAT_STALE_MS } from './constants';

export interface CoreDevice {
  /** Convex-issued device id. */
  deviceId: string;
  /** Display name — usually hostname. */
  name: string;
  /** OS family — "darwin", "linux", "windows". */
  platform: string;
  isOnline: boolean;
  needsAuth: boolean;
  runnerDown: boolean;
  /** Unix ms of the latest heartbeat the agent sent to Convex. */
  lastHeartbeat: number;
  isGuest: boolean;
  hostName?: string;
  hostEmail?: string;
  accessScope?: 'owner' | 'shared-scoped' | 'shared-legacy';
  /** Optional transport hint for a shared device when the host exposed exactly one box. */
  tunnelUrl?: string;
  /** Primary LAN IP (or tunnel host) the agent advertised. */
  quicHost: string;
  quicPort: number;
  /** HTTP port the agent listens on (usually 18080). */
  httpPort?: number;
  publicKey?: string;
  /** Stable hardware identifier — dedup key for re-pair events. */
  hwid?: string;
  /**
   * Every LAN IP the agent reported in its last heartbeat. Used by
   * Discovery.raceProbe to try all interfaces in parallel.
   */
  localIps?: string[];
}

// ── Normalisers ───────────────────────────────────────────────────────

function normName(name: string | undefined): string {
  return String(name || '').trim().toLowerCase().replace(/\.local$/i, '');
}

function normHost(host: string | undefined): string {
  return String(host || '').trim().toLowerCase().replace(/\.local$/i, '');
}

// ── Keys ──────────────────────────────────────────────────────────────

export function deviceIdentityKey(d: CoreDevice): string {
  // Stable cryptographic identity wins. Without hwid or publicKey a
  // non-guest row is a "ghost" — its identity is unstable across
  // renames and platform reads. We deliberately stop falling back
  // to (platform, name) here: that fallback collapsed unrelated
  // boxes that happened to share a hostname, and split a single
  // box across renames. Ghost rows are still addressable per
  // deviceId so the dashboard can list and warn about them.
  if (d.hwid) return `hwid:${d.hwid}`;
  if (d.publicKey) return `pub:${d.publicKey}`;
  if (d.isGuest) {
    const scope = d.hostEmail || d.hostName || 'guest';
    return `guest:${scope}:${d.deviceId || d.name}`;
  }
  if (d.deviceId) return `id:${d.deviceId}`;
  return `name:${d.name}`;
}

/**
 * True when the device has neither hwid nor publicKey AND is not a
 * guest. Reconnect targets must guard on this — a ghost row cannot
 * be reliably matched to a live agent across renames or restarts.
 */
export function isGhostDevice(d: CoreDevice): boolean {
  return !d.hwid && !d.publicKey && !d.isGuest;
}

export function deviceAliasKey(d: CoreDevice): string | null {
  if (d.isGuest) return null;
  const n = normName(d.name);
  const os = String(d.platform || '').trim().toLowerCase();
  if (!n || !os) return null;
  return `${os}:${n}`;
}

export function deviceEndpointKey(d: CoreDevice): string | null {
  if (d.isGuest) return null;
  const h = normHost(d.quicHost);
  if (!h) return null;
  return `${h}:${d.quicPort || 0}`;
}

// ── Merge rules ───────────────────────────────────────────────────────

export function mergeDeviceEntries(a: CoreDevice, b: CoreDevice): CoreDevice {
  const incomingWins =
    (!!a.needsAuth && !b.needsAuth) ||
    (b.lastHeartbeat || 0) > (a.lastHeartbeat || 0) ||
    (!!b.isOnline && !a.isOnline);
  const base = incomingWins ? b : a;
  const other = incomingWins ? a : b;
  return {
    ...other,
    ...base,
    quicHost: base.quicHost || other.quicHost,
    quicPort: base.quicPort || other.quicPort,
    httpPort: base.httpPort || other.httpPort,
    isOnline: base.isOnline || other.isOnline,
    runnerDown: base.runnerDown && other.runnerDown,
    publicKey: base.publicKey || other.publicKey,
    hwid: base.hwid || other.hwid,
    lastHeartbeat: Math.max(a.lastHeartbeat || 0, b.lastHeartbeat || 0),
    localIps: (() => {
      const set = new Set<string>();
      for (const ip of a.localIps || []) if (ip) set.add(ip);
      for (const ip of b.localIps || []) if (ip) set.add(ip);
      return set.size > 0 ? [...set] : undefined;
    })(),
  };
}

// When two rows share the same alias (hostname + OS) but differ on
// hwid / publicKey, prefer the authenticated + online row over a
// stale "needsAuth + offline" leftover. That leftover pattern is
// what re-pair / wipe-and-reinstall produces on Convex.
function pickActiveOverStaleNeedsAuth(
  a: CoreDevice,
  b: CoreDevice,
): CoreDevice | null {
  const aDead = a.needsAuth && !a.isOnline;
  const bDead = b.needsAuth && !b.isOnline;
  const aLive = !a.needsAuth && a.isOnline;
  const bLive = !b.needsAuth && b.isOnline;
  if (aDead && bLive) return b;
  if (bDead && aLive) return a;
  return null;
}

// ── Collapse (three-pass dedup) ───────────────────────────────────────

/**
 * Collapse duplicate Convex rows so each physical machine appears
 * exactly once. Three passes — identity key → alias key → endpoint key.
 * Safe on empty lists; idempotent on already-deduped input.
 */
export function collapseDevices(devices: CoreDevice[]): CoreDevice[] {
  if (!Array.isArray(devices) || devices.length === 0) return [];

  // Pass 1: identity key (hwid / publicKey / name+os).
  const byIdentity = new Map<string, CoreDevice>();
  for (const d of devices) {
    const k = deviceIdentityKey(d);
    const prev = byIdentity.get(k);
    byIdentity.set(k, prev ? mergeDeviceEntries(prev, d) : d);
  }

  // Pass 2: alias key (os + normalised hostname), with strong-identity
  // conflict resolution so two genuinely-different machines sharing a
  // hostname don't silently merge.
  const byAlias = new Map<string, CoreDevice>();
  for (const d of byIdentity.values()) {
    const k = deviceAliasKey(d);
    if (!k) {
      byAlias.set(`id:${d.deviceId}`, d);
      continue;
    }
    const prev = byAlias.get(k);
    if (!prev) {
      byAlias.set(k, d);
      continue;
    }
    const strongConflict =
      (!!prev.hwid && !!d.hwid && prev.hwid !== d.hwid) ||
      (!!prev.publicKey && !!d.publicKey && prev.publicKey !== d.publicKey);
    if (strongConflict) {
      const winner = pickActiveOverStaleNeedsAuth(prev, d);
      if (winner) {
        byAlias.set(k, winner);
        continue;
      }
    }
    byAlias.set(k, mergeDeviceEntries(prev, d));
  }

  // Pass 3: endpoint key (host:port) — last-chance dedup for rows
  // that share a LAN address but slipped through identity + alias.
  const byEndpoint = new Map<string, CoreDevice>();
  for (const d of byAlias.values()) {
    const k = deviceEndpointKey(d);
    if (!k) {
      byEndpoint.set(`id:${d.deviceId}`, d);
      continue;
    }
    const prev = byEndpoint.get(k);
    byEndpoint.set(k, prev ? mergeDeviceEntries(prev, d) : d);
  }

  return [...byEndpoint.values()];
}

// ── Freshness + target pick ───────────────────────────────────────────

/**
 * "Fresh" matches the mobile app: online + heartbeat < 90 s. Clients
 * read Convex's `isOnline` first (backend already applies its own 90 s
 * gate from the server clock), then use this helper when they need the
 * phone-side freshness opinion too — e.g. for auto-connect picks.
 */
export function isDeviceFresh(d: CoreDevice, now = Date.now()): boolean {
  if (!d.isOnline) return false;
  if (!d.lastHeartbeat) return true;
  return now - d.lastHeartbeat < HEARTBEAT_STALE_MS;
}

/**
 * Choose the best candidate for an auto-connect attempt. Preference:
 *   1. explicit `preferredDeviceId` that's still fresh
 *   2. fresh (online + recent heartbeat) + has a quicHost
 *   3. online + has a quicHost
 *   4. first with a quicHost
 */
export function pickTargetDevice(
  devices: CoreDevice[],
  preferredDeviceId?: string,
): CoreDevice | null {
  if (!devices.length) return null;
  if (preferredDeviceId) {
    const preferred = devices.find(
      (d) => d.deviceId === preferredDeviceId && d.quicHost,
    );
    if (preferred && isDeviceFresh(preferred)) return preferred;
    if (preferred) return preferred;
  }
  const fresh = devices.find((d) => isDeviceFresh(d) && d.quicHost);
  if (fresh) return fresh;
  const online = devices.find((d) => d.isOnline && d.quicHost);
  if (online) return online;
  return devices.find((d) => d.quicHost) || devices[0] || null;
}

// ── Probe candidate assembly ──────────────────────────────────────────

/**
 * Build the set of `/health` candidate URLs for a target device —
 * `quicHost` plus every LAN IP the agent reported in `localIps`,
 * uniqued and formatted. This is the thing that makes direct LAN
 * reloads "just work" on multi-homed hosts (en0 + utun tailscale +
 * docker0 etc.) — the mobile app races them in parallel via
 * Promise.any.
 */
export function buildProbeCandidates(
  target: CoreDevice,
  defaultHttpPort = 18080,
): string[] {
  const port = target.httpPort ?? target.quicPort ?? defaultHttpPort;
  const ips = new Set<string>();
  if (target.quicHost) ips.add(target.quicHost);
  for (const ip of target.localIps ?? []) {
    if (ip) ips.add(ip);
  }
  return [...ips].map((ip) => `http://${ip}:${port}`);
}

// ── Parallel /health race (Promise.any polyfill baked in) ─────────────

export interface ProbeResult {
  url: string;
  hostname?: string;
  version?: string;
  latency?: number;
}

/**
 * Race `/health` probes across N URLs. First 200 wins; everything else
 * is abandoned. Older Hermes doesn't have Promise.any, so we hand-roll
 * the same semantic.
 */
export async function raceHealthProbes(
  urls: string[],
  opts: { timeoutMs?: number; headers?: Record<string, string> } = {},
): Promise<ProbeResult | null> {
  if (!urls || urls.length === 0) return null;
  const timeoutMs = opts.timeoutMs ?? 2500;

  const probeOne = async (url: string): Promise<ProbeResult | null> => {
    const base = url.replace(/\/$/, '');
    const start = Date.now();
    try {
      const controller = new AbortController();
      const timer = setTimeout(() => controller.abort(), timeoutMs);
      const res = await fetch(`${base}/health`, {
        method: 'GET',
        headers: opts.headers,
        signal: controller.signal,
      });
      clearTimeout(timer);
      if (!res.ok) return null;
      const latency = Date.now() - start;
      let hostname: string | undefined;
      let version: string | undefined;
      try {
        const data = await res.json();
        hostname = data?.hostname ?? data?.name;
        version = data?.version;
      } catch {
        // /health may return plain text
      }
      return { url: base, hostname, version, latency };
    } catch {
      return null;
    }
  };

  return new Promise<ProbeResult | null>((resolve) => {
    let remaining = urls.length;
    let settled = false;
    for (const url of urls) {
      probeOne(url)
        .then((r) => {
          if (settled) return;
          if (r) {
            settled = true;
            resolve(r);
            return;
          }
          remaining -= 1;
          if (remaining <= 0 && !settled) {
            settled = true;
            resolve(null);
          }
        })
        .catch(() => {
          remaining -= 1;
          if (remaining <= 0 && !settled) {
            settled = true;
            resolve(null);
          }
        });
    }
  });
}
