/**
 * Per-device probe backoff.
 *
 * Without this, useDeviceRuntimeInfo and useDeviceProjects re-fire on every
 * React re-render that bumps the dependency list (and the Convex live query
 * for devices updates roughly every heartbeat, so re-renders happen often).
 * When the URL is dead — e.g. a relay 502 because the agent's QUIC tunnel
 * isn't established — that's dozens of identical failed fetches per minute,
 * which is exactly what the user saw in DevTools.
 *
 * State is keyed by `{deviceId}::{kind}` so /info and /projects back off
 * independently (one can succeed while the other doesn't). The module-level
 * map survives component remounts so a rapidly-rerendering parent can't
 * defeat the backoff.
 */

type BackoffKind = "info" | "projects" | "health" | string;

interface BackoffState {
  failures: number;
  nextAttemptAfter: number;
  lastError?: string;
}

const backoffByDeviceKind = new Map<string, BackoffState>();

/**
 * Last classified failure observed for any browser probe on this device, of
 * any kind. The card-list-item reads this so its lifecycle label ("Ready to
 * Connect") can be downgraded when the browser side is demonstrably failing,
 * even when the failure came from a different surface (the expanded details
 * panel's runtime-info or projects probe, not just the user-initiated Ping).
 *
 * Stored separately from BackoffState because the card needs the human-readable
 * label/reason, not the next-attempt timestamp.
 */
interface LastFailureRecord {
  reason: string;
  label: string;
  detail: string;
  at: number;
}
const lastFailureByDevice = new Map<string, LastFailureRecord>();
type FailureSubscriber = () => void;
const failureSubscribers = new Set<FailureSubscriber>();

function notifyFailureSubscribers(): void {
  for (const fn of failureSubscribers) {
    try { fn(); } catch {}
  }
}

export function recordLastFailure(
  deviceId: string,
  failure: { reason: string; label: string; detail: string },
): void {
  lastFailureByDevice.set(deviceId, { ...failure, at: Date.now() });
  notifyFailureSubscribers();
}

export function clearLastFailure(deviceId: string): void {
  if (lastFailureByDevice.delete(deviceId)) {
    notifyFailureSubscribers();
  }
}

export function getLastFailure(deviceId: string): LastFailureRecord | null {
  return lastFailureByDevice.get(deviceId) ?? null;
}

export function subscribeLastFailure(fn: FailureSubscriber): () => void {
  failureSubscribers.add(fn);
  return () => { failureSubscribers.delete(fn); };
}


const BASE_DELAY_MS = 4_000;
const MAX_DELAY_MS = 120_000;

function key(deviceId: string, kind: BackoffKind): string {
  return `${deviceId}::${kind}`;
}

/**
 * Returns the timestamp before which we should NOT re-attempt this
 * (deviceId, kind) probe. 0 means "go ahead, no backoff active".
 */
export function probeNextAttemptAfter(deviceId: string, kind: BackoffKind): number {
  return backoffByDeviceKind.get(key(deviceId, kind))?.nextAttemptAfter ?? 0;
}

/**
 * Should we attempt this probe right now?
 */
export function probeAllowed(deviceId: string, kind: BackoffKind, now = Date.now()): boolean {
  return now >= probeNextAttemptAfter(deviceId, kind);
}

/**
 * Record a successful probe — clears backoff so the next attempt fires
 * immediately.
 */
export function probeSucceeded(deviceId: string, kind: BackoffKind): void {
  backoffByDeviceKind.delete(key(deviceId, kind));
}

/**
 * Record a failed probe. Increments the failure counter and schedules the
 * next attempt with exponential delay (capped at 2 minutes). Returns the
 * new state so callers can show "retrying in Xs" hints if they want.
 */
export function probeFailed(deviceId: string, kind: BackoffKind, errMsg?: string): BackoffState {
  const k = key(deviceId, kind);
  const prev = backoffByDeviceKind.get(k);
  const failures = (prev?.failures ?? 0) + 1;
  const delay = Math.min(MAX_DELAY_MS, BASE_DELAY_MS * Math.pow(2, failures - 1));
  const state: BackoffState = {
    failures,
    nextAttemptAfter: Date.now() + delay,
    lastError: errMsg,
  };
  backoffByDeviceKind.set(k, state);
  return state;
}

/**
 * Wipe all backoff for one device (e.g. on user-initiated retry via Ping).
 */
export function probeReset(deviceId: string, kind?: BackoffKind): void {
  if (kind) {
    backoffByDeviceKind.delete(key(deviceId, kind));
    return;
  }
  for (const k of Array.from(backoffByDeviceKind.keys())) {
    if (k.startsWith(`${deviceId}::`)) backoffByDeviceKind.delete(k);
  }
}

/**
 * Read current state (for surfacing "retrying in Xs" UI). Returns null if
 * no backoff is active.
 */
export function probeBackoffState(deviceId: string, kind: BackoffKind): BackoffState | null {
  return backoffByDeviceKind.get(key(deviceId, kind)) ?? null;
}

/**
 * Seconds until next attempt, rounded up. Returns 0 if no backoff active or
 * already elapsed.
 */
export function probeBackoffSecondsRemaining(deviceId: string, kind: BackoffKind): number {
  const after = probeNextAttemptAfter(deviceId, kind);
  if (!after) return 0;
  const ms = after - Date.now();
  return ms > 0 ? Math.ceil(ms / 1000) : 0;
}
