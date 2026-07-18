/**
 * One source of truth for "what state is this device in, and can this browser
 * actually reach it right now?"
 *
 * Why this file exists: `deriveDeviceLifecycleState` + `lastSeenAgeMs` +
 * `hasRecentLiveSignal` used to exist as verbatim copies in BOTH
 * `app/dashboard/page.tsx` and `components/dashboard/DevicesView.tsx`. They
 * drifted, which is exactly how the dashboard ended up lying: a fix applied to
 * one copy left the other still promoting a dead box to "Ready to Connect".
 * Any new consumer imports from here — do not re-inline these.
 *
 * The second half of this file is the part the old code was missing entirely.
 * A Convex heartbeat proves the *agent* is alive and talking to Convex. It
 * proves nothing about whether *this browser* has a working path to the box
 * (relay tunnel up, LAN routable, token accepted). Treating the two as one
 * signal is what produced the `magara` bug: "Ready to Connect · Last agent
 * signal just now" on a box where every connect, update and shell attempt
 * failed with "Could not reach agent (direct, tunnel, or relay)".
 *
 * So: `deriveDeviceLifecycleState` answers "is the agent alive?" and
 * `deriveBrowserReach` answers "can we get to it from here?". Every CTA that
 * performs browser→agent I/O (connect, update, shell) must consult BOTH.
 */

import type { Device } from "@/lib/use-devices";

export type DeviceLifecycleState =
  | "offline"
  | "bootstrap"
  | "yaver-auth-expired"
  | "ready-to-connect"
  | "connected";

export function lastSeenAgeMs(lastSeen?: string): number | null {
  if (!lastSeen) return null;
  const ts = Date.parse(lastSeen);
  if (Number.isNaN(ts)) return null;
  return Math.max(0, Date.now() - ts);
}

export function formatAgeShort(ms: number | null): string | null {
  if (ms == null) return null;
  const sec = Math.floor(ms / 1000);
  if (sec < 60) return `${sec}s`;
  const min = Math.floor(sec / 60);
  if (min < 60) return `${min}m`;
  const hr = Math.floor(min / 60);
  if (hr < 24) return `${hr}h`;
  const day = Math.floor(hr / 24);
  return `${day}d`;
}

export function hasRecentLiveSignal(
  device: Pick<Device, "lastTunnelEvent" | "peerState" | "workspaceLive">,
  maxAgeMs = 360_000,
): boolean {
  if (device.workspaceLive) return true;
  if (device.peerState === "online") return true;
  return Boolean(
    device.lastTunnelEvent &&
    device.lastTunnelEvent.online &&
    device.lastTunnelEvent.at > 0 &&
    (Date.now() - device.lastTunnelEvent.at) < maxAgeMs,
  );
}

export type DeviceLifecycleInput = Pick<
  Device,
  "online" | "needsAuth" | "peerState" | "workspaceLive" | "probeState" | "lastTunnelEvent" | "probeInfo"
>;

export function deriveDeviceLifecycleState(device: DeviceLifecycleInput): DeviceLifecycleState {
  if (device.workspaceLive) return "connected";
  const lifecycleState = String(device.probeInfo?.lifecycle?.state || device.probeInfo?.lifecycleState || "");
  if (
    lifecycleState === "bootstrap" ||
    lifecycleState === "yaver-auth-expired" ||
    lifecycleState === "ready-to-connect"
  ) {
    return lifecycleState as DeviceLifecycleState;
  }
  if (device.needsAuth && (device.online || device.peerState === "online" || device.peerState === "stale" || hasRecentLiveSignal(device))) return "bootstrap";
  if (device.probeState === "auth-expired") return "yaver-auth-expired";
  // "ready-to-connect" must mean a *positive, recent* signal that the box can
  // actually be reached. A `stale` peerState is the bus saying "I saw this
  // machine, but no transport is healthy right now" — the opposite of ready.
  // Mirrors deriveMobileDeviceLifecycleState in mobile/src/lib/deviceStatus.ts,
  // which dropped `stale` for this same reason (it names magara in its comment).
  if (
    device.probeState === "ok" ||
    device.peerState === "online" ||
    device.online ||
    hasRecentLiveSignal(device)
  ) {
    return "ready-to-connect";
  }
  return "offline";
}

// ---------------------------------------------------------------------------
// Browser reachability — independent of the Convex heartbeat.
// ---------------------------------------------------------------------------

/**
 * How stale a classified probe failure may be and still count as evidence that
 * this browser can't reach the box. Probes back off exponentially (up to 2 min
 * in probe-backoff.ts), so a window shorter than that would flap back to
 * "reachable" purely because we stopped asking.
 */
export const BROWSER_FAILURE_WINDOW_MS = 90_000;

export interface LastFailureLike {
  reason: string;
  label: string;
  detail: string;
  at: number;
}

export interface BrowserReach {
  /** True when we have positive evidence this browser cannot reach the agent. */
  unreachable: boolean;
  /** Short label for badges/CTAs, e.g. "Unauthorized". Null when reachable. */
  label: string | null;
  /** Sentence-length explanation for tooltips. Null when reachable. */
  detail: string | null;
  /** Classified reason, e.g. "unauthorized" | "relay-stale". Null when reachable. */
  reason: string | null;
}

const REACHABLE: BrowserReach = { unreachable: false, label: null, detail: null, reason: null };

/**
 * Combine every signal we have about browser→agent reachability.
 *
 * `lastFailure` comes from the probe-backoff registry (recorded by the /info
 * and /projects probes). Callers must pass it in rather than reading the module
 * map directly, so this stays a pure function React can re-render off.
 */
export function deriveBrowserReach(
  device: Pick<Device, "probeState" | "probeError">,
  lastFailure: LastFailureLike | null | undefined,
  now: number = Date.now(),
): BrowserReach {
  const fresh = lastFailure && now - lastFailure.at < BROWSER_FAILURE_WINDOW_MS ? lastFailure : null;
  if (fresh) {
    return { unreachable: true, label: fresh.label, detail: fresh.detail, reason: fresh.reason };
  }
  if (device.probeState === "unreachable") {
    return {
      unreachable: true,
      label: "Browser can't reach",
      detail: device.probeError || "The last reachability probe of this device failed.",
      reason: "probe-unreachable",
    };
  }
  return REACHABLE;
}

/**
 * The honest one-line status for a device card, given both signals.
 *
 * The key case: lifecycle says ready/connected but the browser demonstrably
 * can't get there. That is NOT "Ready to Connect" — the agent is alive, we just
 * have no path to it. Saying "Ready" there sends the user into a connect that
 * can only fail.
 */
export function deviceStatusLabel(lifecycle: DeviceLifecycleState, reach: BrowserReach): string {
  const contradicted = reach.unreachable && (lifecycle === "ready-to-connect" || lifecycle === "connected");
  if (contradicted) {
    return reach.label ? `Alive · can't reach (${reach.label})` : "Alive · can't reach";
  }
  switch (lifecycle) {
    case "connected": return "Connected";
    case "bootstrap": return "Bootstrap";
    case "yaver-auth-expired": return "Yaver Auth Expired";
    case "ready-to-connect": return "Ready to Connect";
    default: return "Offline";
  }
}

/**
 * Can a browser→agent action (connect, update, open shell) plausibly succeed?
 * False means: offer it as a diagnostic ("Try Connect") or queue it, never as
 * a confident CTA.
 */
export function canBrowserActOnDevice(lifecycle: DeviceLifecycleState, reach: BrowserReach): boolean {
  if (reach.unreachable) return false;
  return lifecycle === "connected" || lifecycle === "ready-to-connect";
}
