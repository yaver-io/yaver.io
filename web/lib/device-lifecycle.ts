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
import { MAX_DELAY_MS as PROBE_MAX_BACKOFF_MS } from "@/lib/probe-backoff";

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
 * this browser can't reach the box.
 *
 * This MUST exceed probe-backoff's MAX_DELAY_MS, or the evidence expires before
 * the next probe can re-record it and the card flaps back to a confident
 * "reachable" on a box that has been failing for minutes. That is not
 * hypothetical: this constant was 90s against a 120s backoff cap, so from the
 * 6th consecutive failure onward `magara` cycled back to "Ready to Connect".
 * Derived from the cap rather than hand-tuned so the two cannot drift apart —
 * see the assertion in device-lifecycle.test.ts.
 */
export const BROWSER_FAILURE_WINDOW_MS = PROBE_MAX_BACKOFF_MS + 60_000;

export interface LastFailureLike {
  reason: string;
  label: string;
  detail: string;
  at: number;
}

/**
 * What we actually know about getting from THIS client to the agent.
 *
 * The critical distinction is `claimed` vs `reachable`. Convex telling us the
 * agent heartbeats is not evidence that we have a path to it — the agent's
 * heartbeat is outbound-only and survives NAT, a dead relay tunnel, and a
 * 15-minute staleness window (see docs/architecture/DEVICE_TRUTH.md §1).
 *
 * Absence of evidence is not evidence of reachability. An unprobed device is
 * `claimed`, never `reachable`, and must not get a confident CTA.
 */
export type BrowserReachState =
  | "reachable"    // we completed a request to this agent
  | "claimed"      // heartbeat says alive; we have not proven a path from here
  | "unreachable"  // we tried and failed
  | "offline";     // no recent heartbeat; nothing claims it is alive

export interface BrowserReach {
  state: BrowserReachState;
  /** True only for `unreachable` — kept as the single "do not promise" gate. */
  unreachable: boolean;
  /** True only for `reachable` — we have positive proof, not just a claim. */
  verified: boolean;
  /** Short label for badges/CTAs, e.g. "Unauthorized". Null when nothing to say. */
  label: string | null;
  /** Sentence-length explanation for tooltips. */
  detail: string | null;
  /** Classified reason, e.g. "unauthorized" | "relay-stale". */
  reason: string | null;
  /** When the evidence was gathered (ms epoch), or null if we have none. */
  checkedAt: number | null;
}

/**
 * Combine every signal we have about browser→agent reachability.
 *
 * `lastFailure` comes from the probe-backoff registry (recorded by the /info
 * and /projects probes). Callers must pass it in rather than reading the module
 * map directly, so this stays a pure function React can re-render off.
 */
export function deriveBrowserReach(
  device: Pick<Device, "probeState" | "probeError" | "online" | "peerState" | "workspaceLive" | "lastTunnelEvent">,
  lastFailure: LastFailureLike | null | undefined,
  now: number = Date.now(),
): BrowserReach {
  const fresh = lastFailure && now - lastFailure.at < BROWSER_FAILURE_WINDOW_MS ? lastFailure : null;
  if (fresh) {
    return {
      state: "unreachable",
      unreachable: true,
      verified: false,
      label: fresh.label,
      detail: fresh.detail,
      reason: fresh.reason,
      checkedAt: fresh.at,
    };
  }
  if (device.probeState === "unreachable") {
    return {
      state: "unreachable",
      unreachable: true,
      verified: false,
      label: "Can't reach",
      detail: device.probeError || "The last reachability probe of this device failed.",
      reason: "probe-unreachable",
      checkedAt: null,
    };
  }
  // Positive proof: a successful probe, or a live workspace connection we are
  // currently holding open. Both mean a request completed end to end.
  if (device.probeState === "ok" || device.workspaceLive) {
    return {
      state: "reachable",
      unreachable: false,
      verified: true,
      label: null,
      detail: null,
      reason: null,
      checkedAt: null,
    };
  }
  // Heartbeat-only. The agent says it is alive; we have not proven we can get
  // there. This is the honest resting state for a card nobody has probed.
  if (device.online || device.peerState === "online" || hasRecentLiveSignal(device)) {
    return {
      state: "claimed",
      unreachable: false,
      verified: false,
      label: "Not verified from here",
      detail: "This device reports in to Yaver, but this browser hasn't confirmed a working connection to it yet.",
      reason: "unverified",
      checkedAt: null,
    };
  }
  return {
    state: "offline",
    unreachable: false,
    verified: false,
    label: null,
    detail: null,
    reason: null,
    checkedAt: null,
  };
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
    // "Bootstrap" and "Yaver Auth Expired" are internal enum names. Say what the
    // user must DO instead — see docs/architecture/DEVICE_TRUTH.md §6.
    case "bootstrap": return "Needs pairing";
    case "yaver-auth-expired": return "Signed out";
    case "ready-to-connect":
      // The heart of the model: only claim readiness when something actually
      // proved a path. Unprobed devices say what we know (a recent check-in),
      // not what we hope (that connecting will work).
      return reach.verified ? "Ready to Connect" : "Reporting in · not verified";
    default: return "Offline";
  }
}

/**
 * Can a browser→agent action (connect, update, open shell) plausibly succeed?
 * False means: offer it as a diagnostic ("Try Connect") or queue it, never as
 * a confident CTA.
 *
 * NOTE this stays true for `claimed` — a heartbeating box is worth attempting,
 * and refusing to try would be its own kind of lie. What must change with
 * `claimed` is the CONFIDENCE of the CTA, not its availability. Use
 * `deviceCtaLabel` for that.
 */
export function canBrowserActOnDevice(lifecycle: DeviceLifecycleState, reach: BrowserReach): boolean {
  if (reach.unreachable) return false;
  return lifecycle === "connected" || lifecycle === "ready-to-connect";
}

export interface DeviceCta {
  label: string;
  /** Render the confident/primary style only when we have proof. */
  confident: boolean;
  title: string;
}

/**
 * The single place that decides what the primary button on a device says.
 *
 * "Open Workspace" is a promise that it will open. We may only make it when a
 * probe succeeded or a workspace is already live. Everything else is an attempt,
 * and is labelled as one.
 */
export function deviceCtaLabel(lifecycle: DeviceLifecycleState, reach: BrowserReach): DeviceCta {
  if (reach.unreachable) {
    return {
      label: "Try Connect",
      confident: false,
      title: reach.label
        ? `Last attempt from this browser failed (${reach.label}). Try anyway and show relay/direct diagnostics.`
        : "Probe this machine anyway and show relay/direct diagnostics.",
    };
  }
  if (lifecycle === "offline") {
    return {
      label: "Try Connect",
      confident: false,
      title: "No recent check-in from this machine. Probe it anyway and show diagnostics.",
    };
  }
  if (lifecycle === "bootstrap" || lifecycle === "yaver-auth-expired") {
    return {
      label: "Try Connect",
      confident: false,
      title: "This machine needs to be paired or signed in before a workspace can open.",
    };
  }
  if (reach.verified) {
    return {
      label: "Open Workspace",
      confident: true,
      title: "Connect to this machine and start working on it",
    };
  }
  return {
    label: "Connect",
    confident: false,
    title: "This machine reports in to Yaver, but this browser hasn't confirmed a connection yet. Connecting will verify it.",
  };
}
