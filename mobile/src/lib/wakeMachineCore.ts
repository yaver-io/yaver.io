/** Shape we need off a Device to reason about sleep state. */
export interface WakeableDevice {
  managed?: boolean;
  machineId?: string;
  machineStatus?: string;
}

/**
 * isDeviceAsleep reports whether a device is a managed box that auto-off'd
 * (self-parked) — managed + a non-running lifecycle status. A self-hosted
 * box that's merely offline is NOT "asleep" (we can't wake it), so this is
 * gated on `managed`.
 */
export function isDeviceAsleep(d: WakeableDevice | null | undefined): boolean {
  if (!d?.managed) return false;
  const st = String(d.machineStatus ?? "").toLowerCase();
  return st === "paused" || st === "stopped" || st === "off" || st === "suspended";
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
  booting: { label: "Booting the machine…", short: "Booting", percent: 52, kind: "wake", tone: "progress" },
  registering: { label: "Connecting over the free relay…", short: "Connecting", percent: 80, kind: "wake", tone: "network" },
  online: { label: "Network connected — finishing up…", short: "Online", percent: 94, kind: "wake", tone: "network" },
  ready: { label: "Ready", short: "Ready", percent: 100, kind: "terminal", tone: "ok" },
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
 * creepPercent — extra progress to add INSIDE the current phase, so the bar
 * keeps inching toward (but never reaches) the next step while we wait. Pure.
 */
export function creepPercent(phase: LifecyclePhase, elapsedMs: number, steps: LifecyclePhase[]): number {
  const typical = PHASE_TYPICAL_MS[phase];
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
export function isPhaseStalled(phase: LifecyclePhase, elapsedMs: number): boolean {
  const typical = PHASE_TYPICAL_MS[phase];
  if (!typical || !isPhaseInFlight(phase)) return false;
  return elapsedMs > typical * 2;
}

/**
 * stallHint — an HONEST explanation of what's happening when a phase overruns.
 * These are the sentences the user needed when the bar sat silently at 80%.
 */
export function stallHint(phase: LifecyclePhase, elapsedMs: number): string | null {
  if (!isPhaseStalled(phase, elapsedMs)) return null;
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
/** Ordered steps for the park stepper. */
export const PARK_STEPS: LifecyclePhase[] = ["snapshotting", "powering-down", "parked"];

/** True while a phase represents active work (spin the bar, block re-tap). */
export function isPhaseInFlight(p: LifecyclePhase): boolean {
  return p === "requested" || p === "resuming" || p === "booting" || p === "registering" || p === "online" ||
    p === "snapshotting" || p === "powering-down";
}

/** True for a resting / done phase where polling can stop. */
export function isPhaseSettled(p: LifecyclePhase): boolean {
  return p === "asleep" || p === "ready" || p === "parked" || p === "error";
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
): LifecyclePhase {
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
  if (status === "resuming") return "resuming";

  if (status === "active" || status === "provisioning") {
    if (deviceReachable) {
      return machine?.runnersAuthorized === false ? "online" : "ready";
    }
    // Not reachable yet — map the box-authored provision phase to boot vs
    // register. Resume drives these once the backend stops pinning "ready".
    switch (provision) {
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
