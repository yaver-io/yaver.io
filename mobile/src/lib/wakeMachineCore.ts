/**
 * How a given box gets woken. The two are not interchangeable:
 *   cloud — a managed BYO machine, recreated from its snapshot by the
 *           control plane (agent verb `machine_wake`).
 *   lan   — a physical box asleep on a LAN, woken by a magic packet. No
 *           snapshot exists and no cloud API can help.
 */
export type WakeKind = "cloud" | "lan";

/** Shape we need off a Device to reason about sleep state. */
export interface WakeableDevice {
  managed?: boolean;
  machineId?: string;
  machineStatus?: string;
  /** Reachable right now. Only meaningful for LAN boxes. */
  isOnline?: boolean;
  /**
   * Set when this box can be woken by a magic packet: its MAC, plus the
   * device id of an agent that is awake on the same LAN and can broadcast it
   * (`POST /wake`). Both halves are required — a magic packet is link-local,
   * so knowing the MAC is useless without someone on that wire to shout it.
   */
  wakeOnLan?: { mac?: string; viaDeviceId?: string } | null;
}

/** True when a box has everything needed for a magic packet to land. */
export function canWakeOnLan(d: WakeableDevice | null | undefined): boolean {
  const w = d?.wakeOnLan;
  return !!(w && String(w.mac ?? "").trim() && String(w.viaDeviceId ?? "").trim());
}

/**
 * wakeKindFor reports how a device would be woken, or null if it can't be.
 * Managed boxes go through the control plane; everything else needs a LAN
 * peer to broadcast for it.
 */
export function wakeKindFor(d: WakeableDevice | null | undefined): WakeKind | null {
  if (!d) return null;
  if (d.managed) return "cloud";
  if (canWakeOnLan(d)) return "lan";
  return null;
}

/**
 * isDeviceAsleep reports whether a device is at rest AND we have a way to
 * wake it. Two cases:
 *   - a managed box that auto-off'd (self-parked): managed + a non-running
 *     lifecycle status.
 *   - a self-hosted box that is simply offline but has a known MAC and an
 *     awake LAN peer to broadcast for it.
 *
 * The second case used to be excluded ("a self-hosted box that's merely
 * offline is NOT asleep — we can't wake it"), which was true only while
 * Wake-on-LAN was a dead stub. It isn't any more, so an offline physical box
 * with a reachable waker is now genuinely wakeable.
 *
 * Still deliberately false for an offline box with no waker: showing a Wake
 * button that cannot work is worse than showing none.
 */
export function isDeviceAsleep(d: WakeableDevice | null | undefined): boolean {
  if (!d) return false;
  if (d.managed) {
    const st = String(d.machineStatus ?? "").toLowerCase();
    return st === "paused" || st === "stopped" || st === "off" || st === "suspended";
  }
  return d.isOnline === false && canWakeOnLan(d);
}

/**
 * A single ordered vocabulary for both directions of the box lifecycle.
 * Wake:  asleep -> requested -> resuming -> booting -> registering -> online -> ready
 * Park:  ready  -> snapshotting -> powering-down -> parked
 */
export type LifecyclePhase =
  | "asleep" // parked, at rest — the resting state a Wake starts from
  | "requested" // user tapped Wake; request in flight (optimistic, client-only)
  | "resuming" // control plane accepted; recreating the server from snapshot
  | "booting" // server exists; OS booting, agent not up yet
  | "registering" // agent starting + registering over the free relay
  | "online" // reachable over the relay, but runners not yet authorized
  | "ready" // fully usable — reachable + runners signed in
  | "needs-auth" // box is UP but its agent session expired — blocked on the user
  | "snapshotting" // parking: taking the snapshot before delete
  | "powering-down" // parking: deleting the server (snapshot kept)
  | "parked" // parking done — at rest, meter stopped
  | "error";

export type LifecycleTone = "rest" | "progress" | "network" | "ok" | "warn" | "error";

export interface PhaseMeta {
  /** Full sentence for the primary status line. */
  label: string;
  /** One/two-word chip label. */
  short: string;
  /** 0-100 for the progress bar. */
  percent: number;
  /** Which direction this phase belongs to. */
  kind: "wake" | "park" | "terminal";
  tone: LifecycleTone;
}

export const PHASE_META: Record<LifecyclePhase, PhaseMeta> = {
  asleep: { label: "Asleep — parked to save cost", short: "Asleep", percent: 0, kind: "wake", tone: "rest" },
  requested: { label: "Waking your box…", short: "Waking", percent: 8, kind: "wake", tone: "progress" },
  resuming: { label: "Recreating from the latest snapshot…", short: "Restoring", percent: 22, kind: "wake", tone: "progress" },
  booting: { label: "Booting the machine…", short: "Booting", percent: 40, kind: "wake", tone: "progress" },
  registering: { label: "Connecting over the free relay…", short: "Connecting", percent: 65, kind: "wake", tone: "network" },
  online: { label: "Network connected — finishing up…", short: "Online", percent: 86, kind: "wake", tone: "network" },
  ready: { label: "Ready", short: "Ready", percent: 100, kind: "terminal", tone: "ok" },
  // The box is UP — server created, OS booted, agent process running. The only
  // thing missing is a Yaver session it can't obtain by itself. Terminal on
  // purpose: nothing will change until the user acts, so creeping a bar or
  // polling for a flip that can never come is a lie. Sits at the same percent
  // as `registering` because that is honestly how far it got.
  "needs-auth": {
    label: "Sign this machine in to finish waking",
    short: "Sign-in needed",
    percent: 65,
    kind: "terminal",
    tone: "warn",
  },
  snapshotting: { label: "Saving a snapshot…", short: "Snapshotting", percent: 35, kind: "park", tone: "progress" },
  "powering-down": { label: "Powering down — snapshot kept…", short: "Powering down", percent: 78, kind: "park", tone: "progress" },
  parked: { label: "Parked — meter stopped", short: "Parked", percent: 100, kind: "terminal", tone: "rest" },
  error: { label: "Something went wrong", short: "Error", percent: 100, kind: "terminal", tone: "error" },
};

/**
 * How long each phase TYPICALLY takes, measured against a real cx43 with a
 * 160 GB disk: recreating the server is a quick API call, but restoring the
 * snapshot + booting is the long pole (~8-10 min), and the agent then needs a
 * moment to start and dial the relay. Used for two things:
 *   1. creep — move the bar continuously inside a phase so a 10-minute boot
 *      never looks like a frozen 52%,
 *   2. stall hints — after ~2x typical, say what's actually happening instead
 *      of silently spinning.
 */
export const PHASE_TYPICAL_MS: Partial<Record<LifecyclePhase, number>> = {
  requested: 10_000,
  resuming: 60_000,
  booting: 480_000, // snapshot restore + OS boot — genuinely minutes
  registering: 90_000,
  online: 20_000,
  snapshotting: 300_000, // provider finalizes the image before we delete
  "powering-down": 60_000,
};

/**
 * LAN wake is a different order of magnitude and needs its own timings.
 * A sleeping Mac or desktop resumes in seconds — there's no snapshot to
 * restore and no server to create. Reusing the cloud numbers would make the
 * bar crawl at 40% for eight minutes while the box has been up the whole
 * time, and would suppress every stall hint long past the point the wake had
 * actually failed (a dropped packet gives no error — the box simply never
 * appears, so the timeout IS the signal).
 */
export const PHASE_TYPICAL_MS_LAN: Partial<Record<LifecyclePhase, number>> = {
  requested: 3_000, // POST /wake to the LAN peer — a single round trip
  booting: 25_000, // resume from sleep + network link up
  registering: 20_000, // agent already installed; it just re-dials
  online: 10_000,
};

/** Typical duration for a phase, honouring the wake kind and this box's pace. */
export function phaseTypicalMs(
  phase: LifecyclePhase,
  kind: WakeKind = "cloud",
  /** Scales the built-in estimates to a box's MEASURED pace (see expectedWakeMs). */
  scale = 1,
): number | undefined {
  const base = kind === "lan" ? PHASE_TYPICAL_MS_LAN[phase] : PHASE_TYPICAL_MS[phase];
  if (!base) return undefined;
  return base * (scale > 0 ? scale : 1);
}

/** Sum of the default cloud per-phase estimates — the fallback total. */
const DEFAULT_WAKE_TOTAL_MS =
  (PHASE_TYPICAL_MS.resuming ?? 0) +
  (PHASE_TYPICAL_MS.booting ?? 0) +
  (PHASE_TYPICAL_MS.registering ?? 0) +
  (PHASE_TYPICAL_MS.online ?? 0);

/** The subset of a managed machine the timing/rest helpers reason about. */
export interface WakeTimingLike {
  lastWakeDurationMs?: number | null;
  lastWakeOutcome?: string | null;
  lastParkedAt?: number | null;
  snapshotSizeGb?: number | null;
  snapshotCreatedAt?: number | null;
  hasVolume?: boolean | null;
}

/**
 * expectedWakeMs — how long THIS box's wake should take.
 *
 * Prefers the measured duration of its last successful wake over the constants
 * above, which were measured on one cx43 with a 160 GB disk and are wrong for
 * every box that isn't that one — a volume-backed box wakes in ~1-2 min and was
 * being promised eight. Implausible measurements (<20s, >30min) are ignored so
 * one freak run can't poison every future wake.
 */
export function expectedWakeMs(machine: WakeTimingLike | null | undefined): number {
  const measured = machine?.lastWakeDurationMs;
  if (typeof measured === "number" && measured >= 20_000 && measured <= 1_800_000) {
    return measured;
  }
  if (machine?.hasVolume) return 150_000;
  return DEFAULT_WAKE_TOTAL_MS;
}

/** This box's pace vs the built-in estimates, clamped against freak measurements. */
export function wakeScaleFor(machine: WakeTimingLike | null | undefined): number {
  return Math.min(3, Math.max(0.2, expectedWakeMs(machine) / DEFAULT_WAKE_TOTAL_MS));
}

/** Human "~4 min" / "~45s" for a duration. */
export function formatDuration(ms: number): string {
  if (ms < 1000) return "0s";
  if (ms < 90_000) return `${Math.round(ms / 1000)}s`;
  return `${Math.max(1, Math.round(ms / 60_000))} min`;
}

export interface RestSummary {
  storage: string | null;
  warning: string | null;
  eta: string;
}

/**
 * describeRest — what a PARKED box should say about itself.
 *
 * A parked box rendered the same "data kept, meter stopped" line regardless of
 * history, so one that woke, sat signed-out for ten minutes and re-parked
 * itself looked exactly like one that had slept peacefully all week — with the
 * user having just watched that wake apparently do nothing.
 */
export function describeRest(
  machine: WakeTimingLike | null | undefined,
  now: number,
): RestSummary {
  const sizeGb = machine?.snapshotSizeGb;
  const hasVolume = !!machine?.hasVolume;
  let storage: string | null = null;
  if (hasVolume) {
    storage = "Data kept on its volume — nothing to restore, so it wakes fast.";
  } else if (typeof sizeGb === "number" && sizeGb > 0) {
    const at = machine?.snapshotCreatedAt ?? machine?.lastParkedAt ?? null;
    const age = at ? ` taken ${formatDuration(Math.max(0, now - at))} ago` : "";
    storage = `Snapshot kept — ${sizeGb} GB${age}.`;
  }

  let warning: string | null = null;
  switch (String(machine?.lastWakeOutcome ?? "")) {
    case "needs-auth":
      warning =
        "Its last wake got the box running but stopped there — the Yaver session had expired, so it parked again. Waking now will hit the same wall until it's signed in.";
      break;
    case "abandoned":
      warning =
        "Its last wake never became reachable and it parked again to stop the meter. Worth another try.";
      break;
    case "error":
      warning = "Its last wake failed outright.";
      break;
  }

  return { storage, warning, eta: formatDuration(expectedWakeMs(machine)) };
}

/**
 * creepPercent — extra progress to add INSIDE the current phase, so the bar
 * keeps inching toward (but never reaches) the next step while we wait. Pure.
 */
export function creepPercent(
  phase: LifecyclePhase,
  elapsedMs: number,
  steps: LifecyclePhase[],
  kind: WakeKind = "cloud",
  scale = 1,
): number {
  const typical = phaseTypicalMs(phase, kind, scale);
  if (!typical || !isPhaseInFlight(phase)) return 0;
  const idx = steps.indexOf(phase);
  const here = PHASE_META[phase].percent;
  const next = idx >= 0 && idx + 1 < steps.length ? PHASE_META[steps[idx + 1]].percent : here + 6;
  const gap = Math.max(0, next - here);
  // Asymptotic: fast at first, never spans more than ~85% of the gap, so the
  // bar can't lie by arriving at the next step before the server says so.
  const ratio = 1 - Math.exp(-elapsedMs / typical);
  return gap * 0.85 * ratio;
}

/** True when a phase has run well past its typical duration. */
export function isPhaseStalled(phase: LifecyclePhase, elapsedMs: number, kind: WakeKind = "cloud", scale = 1): boolean {
  const typical = phaseTypicalMs(phase, kind, scale);
  if (!typical || !isPhaseInFlight(phase)) return false;
  return elapsedMs > typical * 2;
}

/**
 * stallHint — an HONEST explanation of what's happening when a phase overruns.
 * These are the sentences the user needed when the bar sat silently at 80%.
 */
export function stallHint(phase: LifecyclePhase, elapsedMs: number, kind: WakeKind = "cloud", scale = 1): string | null {
  if (!isPhaseStalled(phase, elapsedMs, kind, scale)) return null;

  // A magic packet is fire-and-forget: nothing ever reports that it was
  // dropped, ignored, or sent to a MAC that no longer exists. The box simply
  // never shows up, so this timeout is the ONLY diagnosis the user gets —
  // the cloud hints ("restoring your snapshot") would be actively misleading
  // here, since none of that is happening.
  if (kind === "lan") {
    switch (phase) {
      case "requested":
        return "Still asking the box on that network to send the wake packet. If it just went offline too, nothing there can shout for you.";
      case "booting":
        return "Packet sent, but the machine hasn't come back yet. Wake-on-LAN is often disabled in firmware by default, and most machines ignore it over Wi-Fi — it usually needs wired Ethernet.";
      case "registering":
        return "The machine is awake but its agent hasn't reconnected yet. It retries on its own; give it a moment.";
      case "online":
        return "Almost there — waiting for the runners to finish signing in.";
      default:
        return null;
    }
  }

  switch (phase) {
    case "resuming":
      return "Still recreating the server from your snapshot. Large disks take a few minutes.";
    case "booting":
      return "The machine is rebooting and restoring your snapshot — this can take up to ~10 minutes on a big disk.";
    case "registering":
      return "The box is up but hasn't finished connecting to the relay yet. It retries automatically; give it a minute.";
    case "online":
      return "Almost there — waiting for the runners to finish signing in.";
    case "snapshotting":
      return "Saving your snapshot. We won't delete the server until the snapshot is safely stored.";
    case "powering-down":
      return "Snapshot is safe — removing the server so billing stops.";
    default:
      return null;
  }
}

/** Ordered steps for the wake stepper (excludes the resting/terminal ends). */
export const WAKE_STEPS: LifecyclePhase[] = ["resuming", "booting", "registering", "online", "ready"];
/**
 * LAN wake skips `resuming`: there is no snapshot to recreate from. Showing
 * a "Restoring" step that can never happen would strand the stepper on a
 * phase the box never enters.
 */
export const WAKE_STEPS_LAN: LifecyclePhase[] = ["booting", "registering", "online", "ready"];

/** The step ladder for a wake of the given kind. */
export function wakeStepsFor(kind: WakeKind = "cloud"): LifecyclePhase[] {
  return kind === "lan" ? WAKE_STEPS_LAN : WAKE_STEPS;
}
/** Ordered steps for the park stepper. */
export const PARK_STEPS: LifecyclePhase[] = ["snapshotting", "powering-down", "parked"];

/** True while a phase represents active work (spin the bar, block re-tap). */
export function isPhaseInFlight(p: LifecyclePhase): boolean {
  return p === "requested" || p === "resuming" || p === "booting" || p === "registering" || p === "online" ||
    p === "snapshotting" || p === "powering-down";
}

/** True for a resting / done phase where polling can stop. */
export function isPhaseSettled(p: LifecyclePhase): boolean {
  return p === "asleep" || p === "ready" || p === "parked" || p === "error" || p === "needs-auth";
}

/**
 * deriveServerPhase maps the authoritative server signals — the managed
 * machine's `status`/`provisionPhase` plus whether the device is actually
 * reachable — onto a lifecycle phase. `deviceReachable` is the real
 * "back online" bit (relay presence / live transport), which flips
 * independently of and later than machine.status=active.
 */
export function deriveServerPhase(
  machine: { status?: string | null; provisionPhase?: string | null; runnersAuthorized?: boolean | null } | null | undefined,
  deviceReachable: boolean,
  kind: WakeKind = "cloud",
): LifecyclePhase {
  // A LAN box has no control-plane record at all — no status, no
  // provisionPhase, nobody to ask. Reachability is the entire signal: it is
  // either back or it isn't. Any caller-supplied optimistic phase carries the
  // in-between, which is why this only answers the two settled ends.
  if (kind === "lan") {
    if (deviceReachable) {
      return machine?.runnersAuthorized === false ? "online" : "ready";
    }
    return "asleep";
  }

  const status = String(machine?.status ?? "").toLowerCase();
  const provision = String(machine?.provisionPhase ?? "").toLowerCase();

  if (status === "error") return "error";

  // Parking direction — the box is on its way down.
  if (status === "stopping" || status === "grace") {
    if (provision === "powering-down" || provision === "deleting") return "powering-down";
    return "snapshotting";
  }
  if (status === "paused" || status === "suspended" || status === "stopped" || status === "off") {
    return "asleep";
  }

  // Wake direction.
  if (status === "resuming") {
    if (deviceReachable) {
      return machine?.runnersAuthorized === false ? "online" : "ready";
    }
    switch (provision) {
      // The box is up but its agent session expired; it cannot register on
      // its own no matter how long we wait. Mapping this to "registering"
      // parked the bar at 65% "Connecting over the free relay…" forever,
      // because that connection was never coming.
      case "awaiting-yaver-auth":
        return "needs-auth";
      case "registering":
      case "authorizing-runners":
      case "ready":
        return "registering";
      // Wake-only steps: finding the snapshot, freeing the volume, restoring
      // onto a new server. All three happen BEFORE any server is booting, so
      // they belong on the "Restoring" rung — the default below would have
      // shown "Booting the machine…" for a machine that does not exist yet.
      case "checking-snapshot":
      case "preparing-volume":
      case "restoring-snapshot":
        return "resuming";
      case "creating":
      case "booting":
      case "installing-docker":
      case "pulling-image":
      case "starting-agent":
      default:
        return "booting";
    }
  }

  if (status === "active" || status === "provisioning") {
    if (deviceReachable) {
      return machine?.runnersAuthorized === false ? "online" : "ready";
    }
    // Not reachable yet — map the box-authored provision phase to boot vs
    // register. Resume drives these once the backend stops pinning "ready".
    switch (provision) {
      case "awaiting-yaver-auth":
        return "needs-auth";
      case "starting-agent":
      case "registering":
      case "authorizing-runners":
        return "registering";
      case "ready":
        // Backend still says ready but the device isn't reachable yet —
        // that's the "created record, still cold" window. Show registering,
        // not a false 100%.
        return "registering";
      case "creating":
      case "booting":
      case "installing-docker":
      case "pulling-image":
      default:
        return "booting";
    }
  }

  return "asleep";
}
