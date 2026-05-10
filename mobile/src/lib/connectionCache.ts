import AsyncStorage from "@react-native-async-storage/async-storage";

// connectionCache — per-device snapshot of the last working connection.
//
// The published `device.publicEndpoints` and `device.relayServers` lists
// are authoritative when fresh, but they can rot: a Cloudflare tunnel
// recycles, the relay reassigns the public URL, the relay subdomain DNS
// loses its route. When that happens the agent is still healthy and
// listening — only the published candidates are stale. The mobile client
// then sits in CONNECTING for a minute, retries 5 times, gives up.
//
// We cache the exact transport coordinates that worked the last time and
// probe them first on the next attempt, parallel to (not instead of) the
// regular candidate race. If the cached path still answers /health, we
// skip the candidate race entirely and reconnect in one round-trip.
//
// `hadSuccess` flips true on the first successful connect for a device
// and never flips back. Reconnect policy reads it to switch from
// "give up after N attempts" (never-connected) to "retry forever with
// capped backoff" (previously-connected). The user explicitly does not
// want a known-reachable device to silently stop trying.

const CACHE_KEY_PREFIX = "@yaver/conn-cache/v1/";
const CACHE_TTL_MS = 7 * 24 * 60 * 60 * 1000; // 7 days

export type ConnectionMode = "direct" | "tunnel" | "relay";

export interface ConnectionCacheEntry {
  v: 1;
  deviceId: string;
  mode: ConnectionMode;
  /** Direct-mode coordinates. Set when mode === "direct". */
  host?: string;
  port?: number;
  /** Tunnel-mode coordinates. tunnelUrl is the full https://...
   *  endpoint; headers carry CF-Access-Client-Id/Secret etc. */
  tunnelUrl?: string;
  tunnelHeaders?: Record<string, string>;
  /** Relay-mode coordinates. relayUrl is the relay's base http URL
   *  (without /d/<deviceId>); the deviceId path is appended at probe
   *  time. relayPassword may be empty for password-less relays. */
  relayUrl?: string;
  relayPassword?: string;
  /** Last-successful-connect millis. Entries older than CACHE_TTL_MS
   *  are still tried (they may still work) but do not contribute to
   *  hadSuccess. */
  ts: number;
  /** True once the device has ever connected successfully. Drives the
   *  "indefinite retry vs. give up after N attempts" branch in the
   *  reconnect scheduler. */
  hadSuccess: boolean;
}

function key(deviceId: string): string {
  return CACHE_KEY_PREFIX + deviceId;
}

export async function loadConnectionCache(deviceId: string): Promise<ConnectionCacheEntry | null> {
  if (!deviceId) return null;
  try {
    const raw = await AsyncStorage.getItem(key(deviceId));
    if (!raw) return null;
    const parsed = JSON.parse(raw) as ConnectionCacheEntry;
    if (parsed?.v !== 1 || parsed.deviceId !== deviceId) return null;
    return parsed;
  } catch {
    return null;
  }
}

export async function persistConnectionCache(entry: ConnectionCacheEntry): Promise<void> {
  if (!entry?.deviceId) return;
  try {
    await AsyncStorage.setItem(key(entry.deviceId), JSON.stringify(entry));
  } catch {
    // AsyncStorage failures are non-fatal — losing the cache only
    // costs us one extra candidate-race round-trip on the next connect.
  }
}

export async function clearConnectionCache(deviceId: string): Promise<void> {
  if (!deviceId) return;
  try {
    await AsyncStorage.removeItem(key(deviceId));
  } catch {
    // intentionally swallow — see persistConnectionCache
  }
}

/** Returns the URL we should probe to validate the cached entry. For
 *  direct + tunnel, that's the bare base. For relay, /d/<deviceId>
 *  is appended because the relay proxies per-device under that path. */
export function probeBaseFor(entry: ConnectionCacheEntry): string | null {
  switch (entry.mode) {
    case "direct":
      if (!entry.host || !entry.port) return null;
      return `http://${entry.host}:${entry.port}`;
    case "tunnel":
      return entry.tunnelUrl || null;
    case "relay":
      if (!entry.relayUrl || !entry.deviceId) return null;
      return `${entry.relayUrl}/d/${entry.deviceId}`;
  }
}

/** Builds the headers needed to probe the cached entry. Caller still
 *  layers Authorization on top — we only own transport-specific bits
 *  (relay password, Cloudflare Access). */
export function probeHeadersFor(entry: ConnectionCacheEntry): Record<string, string> {
  if (entry.mode === "tunnel" && entry.tunnelHeaders) {
    return { ...entry.tunnelHeaders };
  }
  if (entry.mode === "relay" && entry.relayPassword) {
    return { "X-Relay-Password": entry.relayPassword };
  }
  return {};
}

export function isFresh(entry: ConnectionCacheEntry, nowMs: number = Date.now()): boolean {
  return nowMs - entry.ts < CACHE_TTL_MS;
}
