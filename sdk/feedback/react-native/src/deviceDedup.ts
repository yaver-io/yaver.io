/**
 * Device deduplication + online-signal merging, ported from the Yaver
 * mobile app's DeviceContext.collapseAliasDevices.
 *
 * Convex stores one row per pair (device, re-install). After a re-pair
 * or hostname change the list can contain 2-3 rows for the same
 * physical machine, with different hwid/publicKey values. The picker
 * then shows duplicates and the user can't tell which one is live.
 *
 * This file collapses rows in three passes:
 *   1. Identity key (hwid → publicKey → "host:os:name" → id → name)
 *   2. Alias key (os + normalized-hostname) — catches re-pairs that
 *      mint a new hwid
 *   3. Endpoint key (host:port)
 *
 * When collapsing, `mergeDeviceEntries` prefers: authenticated over
 * needsAuth, online over offline, freshest lastHeartbeat.
 */

import type { RemoteDevice } from './auth';

function normalizedName(name: string | undefined): string {
  return String(name || '').trim().toLowerCase().replace(/\.local$/i, '');
}

function normalizedHost(host: string | undefined): string {
  return String(host || '').trim().toLowerCase().replace(/\.local$/i, '');
}

function identityKey(d: RemoteDevice): string {
  if (d.hwid) return `hwid:${d.hwid}`;
  if (d.publicKey) return `pub:${d.publicKey}`;
  if (d.isGuest) {
    const scope = d.hostEmail || d.hostName || 'guest';
    return `guest:${scope}:${d.deviceId || d.name}`;
  }
  const n = normalizedName(d.name);
  const os = String(d.platform || '').trim().toLowerCase();
  if (n && os) return `host:${os}:${n}`;
  if (d.deviceId) return `id:${d.deviceId}`;
  return `name:${d.name}`;
}

function aliasKey(d: RemoteDevice): string | null {
  if (d.isGuest) return null;
  const n = normalizedName(d.name);
  const os = String(d.platform || '').trim().toLowerCase();
  if (!n || !os) return null;
  return `${os}:${n}`;
}

function endpointKey(d: RemoteDevice): string | null {
  if (d.isGuest) return null;
  const h = normalizedHost(d.quicHost);
  if (!h) return null;
  return `${h}:${d.quicPort || 0}`;
}

function mergeEntries(existing: RemoteDevice, incoming: RemoteDevice): RemoteDevice {
  const incomingWins =
    (!!existing.needsAuth && !incoming.needsAuth) ||
    (incoming.lastHeartbeat || 0) > (existing.lastHeartbeat || 0) ||
    (!!incoming.isOnline && !existing.isOnline);
  const base = incomingWins ? incoming : existing;
  const other = incomingWins ? existing : incoming;
  return {
    ...other,
    ...base,
    quicHost: base.quicHost || other.quicHost,
    quicPort: base.quicPort || other.quicPort,
    isOnline: base.isOnline || other.isOnline,
    runnerDown: base.runnerDown && other.runnerDown,
    publicKey: base.publicKey || other.publicKey,
    lastHeartbeat: Math.max(existing.lastHeartbeat || 0, incoming.lastHeartbeat || 0),
  };
}

// When two rows share the same alias key (hostname + OS) but differ on
// hwid/publicKey, pick the active one over the stale needs-auth leftover.
function pickActiveOverStaleNeedsAuth(a: RemoteDevice, b: RemoteDevice): RemoteDevice | null {
  const aDead = a.needsAuth && !a.isOnline;
  const bDead = b.needsAuth && !b.isOnline;
  const aLive = !a.needsAuth && a.isOnline;
  const bLive = !b.needsAuth && b.isOnline;
  if (aDead && bLive) return b;
  if (bDead && aLive) return a;
  return null;
}

/**
 * Collapse a Convex device list so each physical machine appears once.
 * Safe on an empty list; idempotent on an already-deduped list.
 */
export function collapseRemoteDevices(devices: RemoteDevice[]): RemoteDevice[] {
  if (!Array.isArray(devices) || devices.length === 0) return [];

  const byIdentity = new Map<string, RemoteDevice>();
  for (const d of devices) {
    const k = identityKey(d);
    const prev = byIdentity.get(k);
    byIdentity.set(k, prev ? mergeEntries(prev, d) : d);
  }

  const byAlias = new Map<string, RemoteDevice>();
  for (const d of byIdentity.values()) {
    const k = aliasKey(d);
    if (!k) {
      byAlias.set(`id:${d.deviceId}`, d);
      continue;
    }
    const prev = byAlias.get(k);
    if (!prev) {
      byAlias.set(k, d);
      continue;
    }
    const strongIdentityConflict =
      !!prev.publicKey && !!d.publicKey && prev.publicKey !== d.publicKey;
    if (strongIdentityConflict) {
      const winner = pickActiveOverStaleNeedsAuth(prev, d);
      if (winner) {
        byAlias.set(k, winner);
        continue;
      }
    }
    byAlias.set(k, mergeEntries(prev, d));
  }

  const byEndpoint = new Map<string, RemoteDevice>();
  for (const d of byAlias.values()) {
    const k = endpointKey(d);
    if (!k) {
      byEndpoint.set(`id:${d.deviceId}`, d);
      continue;
    }
    const prev = byEndpoint.get(k);
    byEndpoint.set(k, prev ? mergeEntries(prev, d) : d);
  }

  return [...byEndpoint.values()];
}

/**
 * Threshold for "online" based on heartbeat age, in milliseconds.
 * Matches the mobile app (HEARTBEAT_STALE_MS = 90 s). The SDK used
 * 60 s, which flashed yellow on single missed beats.
 */
export const HEARTBEAT_STALE_MS = 90_000;

/**
 * Returns a freshness flag consistent with the mobile app. A device is
 * "fresh" when it was online per Convex AND its heartbeat is within
 * `HEARTBEAT_STALE_MS`.
 */
export function isDeviceFresh(d: RemoteDevice): boolean {
  if (!d.isOnline) return false;
  if (!d.lastHeartbeat) return true;
  return Date.now() - d.lastHeartbeat < HEARTBEAT_STALE_MS;
}

/**
 * Pick the best candidate for an auto-connect attempt. Preference:
 *   1. matches the preferred deviceId when supplied + still fresh
 *   2. fresh (online + recent heartbeat) + has a quicHost
 *   3. online + has a quicHost
 *   4. first with a quicHost
 */
export function pickTargetDevice(
  devices: RemoteDevice[],
  preferredDeviceId?: string,
): RemoteDevice | null {
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
