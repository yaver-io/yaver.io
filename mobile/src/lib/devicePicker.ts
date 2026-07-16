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

/** A managed row in a terminal state is gone, not asleep — it can never
 * become a remote box again, so it is not a picker candidate. Parked
 * machines that CAN wake arrive from /subscription and render in their
 * own "Sleeping machines" section. */
const TERMINAL_MACHINE_STATUS = new Set(["removed", "deleted", "destroyed", "terminated", "error"]);

/** Can this row ever serve as a remote box? Excludes only rows that are
 * structurally incapable of it — never rows that are merely offline or
 * merely unauthenticated. Those are real boxes with a real recovery
 * path, and hiding them is what made a rebooted Mac mini vanish from
 * the picker entirely instead of showing up as "needs sign-in". */
export function isPickableRemoteBox(device: Device): boolean {
  // Ghost row: no hardware id and no public key means we cannot address,
  // verify, or pair with it. Mirrors DeviceContext's own ghost guard.
  if (!device.hwid && !device.publicKey && !device.isGuest && !device.managed) return false;
  if (device.managed && device.machineStatus && TERMINAL_MACHINE_STATUS.has(device.machineStatus)) {
    return false;
  }
  return true;
}

/** Remote-box picker candidates.
 *
 * Ordering is liveness-first: pooled, then online, then boxes that need a
 * yaver sign-in, then offline — each group alphabetical so selector UIs
 * stay predictable across tabs.
 *
 * This deliberately INCLUDES `needsAuth` and offline devices. Both
 * consumers already render them honestly (`Down · last seen …`, and the
 * recoverDeviceAuth confirm sheet in TaskTargetWizard) — filtering them
 * out here made that handling unreachable and left a real, reachable box
 * with no way to be seen or signed in from the phone. Inclusion is a
 * presentation problem, not an eligibility one; the row says what state
 * it is in and the tap routes accordingly. */
export function eligibleRemoteBoxDevices(
  devices: Device[],
  connectedIds: Iterable<string>,
  activeDeviceId?: string | null,
): Device[] {
  const connectedSet = connectedIds instanceof Set ? connectedIds : new Set(connectedIds);
  const filtered = devices.filter(
    (d) => connectedSet.has(d.id) || activeDeviceId === d.id || isPickableRemoteBox(d),
  );
  const rank = (d: Device): number => {
    if (connectedSet.has(d.id)) return 0;
    if (d.online && !d.needsAuth) return 1;
    if (d.online && d.needsAuth) return 2;
    return 3;
  };
  return filtered.sort((a, b) => {
    const delta = rank(a) - rank(b);
    if (delta !== 0) return delta;
    return a.name.localeCompare(b.name);
  });
}
