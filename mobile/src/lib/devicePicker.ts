import type { Device } from "../context/DeviceContext";

/** Distance between two semver-like strings. Returns 0 when equal, a
 * positive integer when `current` is older, -1 when we can't decide
 * (different major series, malformed strings, etc.).
 *
 * Yaver versions today are 1.99.<patch> on every channel, so the diff is
 * almost always patch-only. Major + minor must match exactly; patch
 * difference is the count returned. */
export function versionPatchDistance(current: string, latest: string): number {
  const c = current.trim();
  const l = latest.trim();
  if (!c || !l) return -1;
  if (c === l) return 0;
  const parse = (s: string): [number, number, number] | null => {
    const m = /^(\d+)\.(\d+)\.(\d+)/.exec(s);
    if (!m) return null;
    return [Number(m[1]), Number(m[2]), Number(m[3])];
  };
  const cv = parse(c);
  const lv = parse(l);
  if (!cv || !lv) return -1;
  if (cv[0] !== lv[0] || cv[1] !== lv[1]) return -1;
  return Math.max(0, lv[2] - cv[2]);
}

/** Remote-box picker candidates: machines that are already pooled or
 * at least currently live and authed. Sort pooled first, then by name
 * so selector UIs stay predictable across tabs. */
export function eligibleRemoteBoxDevices(
  devices: Device[],
  connectedIds: Iterable<string>,
  activeDeviceId?: string | null,
): Device[] {
  const connectedSet = connectedIds instanceof Set ? connectedIds : new Set(connectedIds);
  const filtered = devices.filter((d) =>
    !d.needsAuth && (connectedSet.has(d.id) || activeDeviceId === d.id || d.online),
  );
  return filtered.sort((a, b) => {
    const aLive = connectedSet.has(a.id) ? 0 : 1;
    const bLive = connectedSet.has(b.id) ? 0 : 1;
    if (aLive !== bLive) return aLive - bLive;
    return a.name.localeCompare(b.name);
  });
}
