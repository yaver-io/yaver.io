/**
 * In-memory negative cache for direct-connect legs that have been proven
 * unroutable from the phone's current network.
 *
 * Why this exists: audit §2 (2026-07-19). The phone was racing nine dead
 * addresses on every attempt — including Docker bridges and stale iPhone
 * hotspot ranges — because an INSTANT `Network request failed` fell through
 * every branch of the classifier and looked identical to a transient failure.
 * Nothing negative-cached the leg, so the same impossible candidates were
 * re-raced forever at full rate. That trips relay rate limiters (audit §3)
 * and starves the one leg that would actually succeed of iOS's ~6-per-host
 * socket budget.
 *
 * Contract:
 *   - Pure module, no React Native imports, so it can run under `npx tsx` as
 *     a unit test.
 *   - Keyed on (networkIdentity, candidateKey). networkIdentity is opaque —
 *     the caller decides what a "network" means (typically NetInfo type +
 *     ssid/carrier hash). candidateKey is a compact "path=ip:port" string.
 *   - Entries expire after DEFAULT_TTL_MS. The lifetime is deliberately
 *     short: the phone's routing table can change under us at any moment
 *     (Wi-Fi → cellular, VPN up, tailnet reconnect), and this cache must
 *     never keep a leg from being retried after a real network change.
 *   - Calling forgetNetwork(oldId) whenever the phone's network identity
 *     changes drops every entry from the prior network in one call. That is
 *     the fast path — the TTL is just a safety net for when the caller
 *     forgets to notify us.
 *
 * NON-goals: this is NOT a policy on whether to attach the session bearer,
 * and NOT a source of truth for tailnet membership. It is a local memory of
 * "this leg failed unroutably in this network". Higher-level modules
 * (transportPolicy, isCredentialSafeBase) may consume it, but they own the
 * policy decision, not this file.
 */

const DEFAULT_TTL_MS = 5 * 60 * 1000; // 5 minutes

/**
 * Tunnel-dependent legs (Tailscale / Yaver Mesh) expire much faster, and can be
 * dropped outright by forgetTunnelLegs().
 *
 * THE BUG THIS FIXES (2026-07-20). The header above promises this cache will
 * "never keep a leg from being retried after a real network change" and names
 * "VPN up" as such a change. It cannot keep that promise, because the identity
 * it is keyed on — deriveNetworkIdentity() in quic.ts — sees only Wi-Fi SSID
 * and cellular carrier. Bringing Tailscale up changes NEITHER: the phone stays
 * on the same SSID. So setNetworkIdentity() early-returns on an unchanged id,
 * nothing is wiped, and every 100.x leg that failed while the tunnel was down
 * stays marked unroutable — while the tailnet is now genuinely up. Observed
 * exactly that: phone and mac mini both on the tailnet, `mobiles-mac-mini`
 * green in the Tailscale app, and the log still reading
 *   lan-tailscale 100.89.155.25:18080 failed — unroutable
 * on repeat, forcing every request onto a flaky relay.
 *
 * A LAN address is a property of the network you are on; a tunnel address is a
 * property of a daemon that can come up at any instant without the network
 * changing at all. They must not share a cache lifetime.
 */
const TUNNEL_TTL_MS = 45 * 1000;

/** Path-family prefixes whose reachability depends on a VPN/tunnel daemon. */
const TUNNEL_PATH_PREFIXES = ["lan-tailscale", "lan-mesh", "tailscale", "mesh"];

/** True when this candidate's reachability depends on a tunnel being up. */
export function isTunnelPath(path: string): boolean {
  const p = (path || "").toLowerCase();
  return TUNNEL_PATH_PREFIXES.some((prefix) => p.startsWith(prefix));
}

/** TTL to use for a leg on this path family. */
export function ttlForPath(path: string): number {
  return isTunnelPath(path) ? TUNNEL_TTL_MS : DEFAULT_TTL_MS;
}

type Entry = { expiresAt: number };

type Store = Map<string, Map<string, Entry>>; // networkId → candidateKey → Entry

let currentNetworkId = "unknown";
const store: Store = new Map();

function keyForCandidate(path: string, ip: string, port: number): string {
  return `${path}=${ip}:${port}`;
}

/**
 * Tell the cache which network the phone is on. On change, everything cached
 * against the OLD network is dropped — legs that were unroutable via one
 * uplink may well be routable via the next one. Passing the same id twice is
 * a no-op.
 */
export function setNetworkIdentity(id: string | null | undefined, now: number = Date.now()): void {
  const next = String(id || "unknown");
  if (next === currentNetworkId) return;
  // Wipe everything from the old network entirely. Not just expire — drop.
  store.delete(currentNetworkId);
  currentNetworkId = next;
  // Also GC any expired entries left over from previous flips.
  gc(now);
}

/**
 * Get the network identity the cache currently believes we are on. Exposed
 * for observability and for the tailnet-membership gate: tailnet candidates
 * that turned out to be unroutable on this network mean "not on tailnet
 * right now", and callers can consult observedTailnetUp() below.
 */
export function currentNetwork(): string {
  return currentNetworkId;
}

/**
 * Record that a candidate failed unroutably on the current network. TTL is
 * an override for tests; production callers should use the default.
 */
export function rememberUnroutable(
  path: string,
  ip: string,
  port: number,
  now: number = Date.now(),
  ttlMs: number = DEFAULT_TTL_MS,
): void {
  let inner = store.get(currentNetworkId);
  if (!inner) {
    inner = new Map();
    store.set(currentNetworkId, inner);
  }
  inner.set(keyForCandidate(path, ip, port), { expiresAt: now + ttlMs });
}

/**
 * True when this candidate has been negative-cached for the current network
 * and the entry has not expired yet.
 */
export function isKnownUnroutable(
  path: string,
  ip: string,
  port: number,
  now: number = Date.now(),
): boolean {
  const inner = store.get(currentNetworkId);
  if (!inner) return false;
  const entry = inner.get(keyForCandidate(path, ip, port));
  if (!entry) return false;
  if (entry.expiresAt <= now) {
    inner.delete(keyForCandidate(path, ip, port));
    return false;
  }
  return true;
}

/**
 * Record that a candidate SUCCEEDED on the current network. Positive proof:
 * whatever path label won can be trusted here, so we forget any prior
 * negative for that path family. Used by observedTailnetUp() below.
 */
const knownPathsUp: Map<string, Set<string>> = new Map();
export function rememberReachable(path: string): void {
  let s = knownPathsUp.get(currentNetworkId);
  if (!s) {
    s = new Set();
    knownPathsUp.set(currentNetworkId, s);
  }
  s.add(path);
}

/**
 * observedTailnetUp: has any lan-tailscale / lan-mesh candidate reached its
 * agent on this network? Replaces the pre-2026-07-19 heuristic
 * `policy.allowTailnet` (a user preference the phone couldn't verify) with
 * observed membership. Returns:
 *   - true  → tailnet legs are known good on this network; race them.
 *   - false → tailnet legs are known unroutable (all candidates negative-cached
 *             and none confirmed) OR no evidence yet — the caller should
 *             probe once and update, but not race the whole set at full rate.
 *
 * Deliberately conservative: absence of evidence is not treated as tailnet-up.
 * The old default let 100.x candidates race indefinitely on cellular; the new
 * default keeps the ladder honest by requiring proof.
 */
export function observedTailnetUp(): boolean {
  const paths = knownPathsUp.get(currentNetworkId);
  if (!paths) return false;
  return paths.has("lan-tailscale") || paths.has("lan-mesh");
}

/**
 * Reset the cache — for tests only. Named `_forTest` so a codebase grep for
 * production callers finds nothing.
 */
/**
 * Drop every negative entry for tunnel-dependent legs, on every network.
 *
 * The TTL above bounds the damage; this removes it. Call whenever the user has
 * plausibly just changed tunnel state and is asking us to try again — app
 * foreground, an explicit Retry/Connect tap, or a NetInfo change event (even
 * one that leaves the identity unchanged, which is precisely the Tailscale
 * case). Cheap: a handful of map deletes over an in-memory store.
 *
 * Returns how many entries were dropped so the caller can log it — a silent
 * cache clear is indistinguishable from a no-op when you are debugging why a
 * leg is not being retried.
 */
export function forgetTunnelLegs(): number {
  let dropped = 0;
  for (const inner of store.values()) {
    for (const key of [...inner.keys()]) {
      // key is "path=ip:port" — see keyForCandidate.
      const path = key.split("=")[0] ?? "";
      if (isTunnelPath(path)) {
        inner.delete(key);
        dropped++;
      }
    }
  }
  return dropped;
}

export function _resetForTest(): void {
  store.clear();
  knownPathsUp.clear();
  currentNetworkId = "unknown";
}

function gc(now: number): void {
  for (const [netId, inner] of store) {
    for (const [k, entry] of inner) {
      if (entry.expiresAt <= now) inner.delete(k);
    }
    if (inner.size === 0) store.delete(netId);
  }
}
