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
