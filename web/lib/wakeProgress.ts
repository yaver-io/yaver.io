/**
 * Mirror of the wake/park half of mobile/src/lib/wakeMachineCore.ts.
 *
 * Web cannot import from mobile/ — tsc and CI both accept a relative
 * cross-surface import, but Turbopack fails `next build` and takes the
 * Cloudflare deploy down with it (see 964f142a4). Same convention as
 * web/lib/agentStatus.ts: mirror, don't reach across the surface boundary.
 *
 * Scoped to the CLOUD wake on purpose. The web dashboard's only wake control
 * is ManagedPowerButton, which is managed-cloud only; mobile additionally
 * handles Wake-on-LAN, and porting that ladder here would be untested dead
 * code the moment it landed.
 *
 * Keep in sync with mobile when the phase vocabulary changes — the slugs come
 * from the control plane (backend/convex/cloudMachines.ts PROVISION_PHASES),
 * so a slug added there needs a home in BOTH mappers or it silently falls
 * through to the default rung.
 */

/**
 * Ordered vocabulary for both directions of the box lifecycle.
 * Wake: asleep -> resuming -> booting -> registering -> online -> ready
 * Park: ready  -> snapshotting -> powering-down -> parked
 */
export type LifecyclePhase =
  | "asleep"
  | "requested"
  | "resuming"
  | "booting"
  | "registering"
  | "online"
  | "ready"
  | "needs-auth"
  | "snapshotting"
  | "powering-down"
  | "parked"
  | "error";

export type LifecycleTone = "rest" | "progress" | "network" | "ok" | "warn" | "error";

export interface PhaseMeta {
  /** Full sentence for the primary status line. */
  label: string;
  /** One/two-word rung label. */
  short: string;
  /** 0-100 for the progress bar. */
  percent: number;
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
  // Terminal on purpose: the box is UP, and the only missing piece is a Yaver
  // session it cannot obtain by itself. Creeping a bar toward a flip that can
  // never come without the user is a lie. Sits at the same percent as
  // `registering` because that is honestly how far it got.
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
 * 160 GB disk. Drives two things: creep (so a 10-minute boot never looks like
 * a frozen 40%) and stall hints (after ~2x typical, say what is happening
 * instead of spinning silently).
 */
export const PHASE_TYPICAL_MS: Partial<Record<LifecyclePhase, number>> = {
  requested: 10_000,
  resuming: 60_000,
  booting: 480_000, // snapshot restore + OS boot — genuinely minutes
  registering: 90_000,
  online: 20_000,
  snapshotting: 300_000,
  "powering-down": 60_000,
};

/** Ordered rungs for the wake stepper (excludes the resting end). */
export const WAKE_STEPS: LifecyclePhase[] = ["resuming", "booting", "registering", "online", "ready"];
/** Ordered rungs for the park stepper. */
export const PARK_STEPS: LifecyclePhase[] = ["snapshotting", "powering-down", "parked"];

/** True while a phase represents active work (animate, block re-tap). */
export function isPhaseInFlight(p: LifecyclePhase): boolean {
  return (
    p === "requested" || p === "resuming" || p === "booting" || p === "registering" ||
    p === "online" || p === "snapshotting" || p === "powering-down"
  );
}

/** True for a resting / done phase where polling can stop. */
export function isPhaseSettled(p: LifecyclePhase): boolean {
  return p === "asleep" || p === "ready" || p === "parked" || p === "error" || p === "needs-auth";
}

/**
 * creepPercent — extra progress to add INSIDE the current phase, so the bar
 * keeps inching toward (but never reaches) the next rung while we wait.
 */
export function creepPercent(phase: LifecyclePhase, elapsedMs: number, steps: LifecyclePhase[]): number {
  const typical = PHASE_TYPICAL_MS[phase];
  if (!typical || !isPhaseInFlight(phase)) return 0;
  const idx = steps.indexOf(phase);
  const here = PHASE_META[phase].percent;
  const next = idx >= 0 && idx + 1 < steps.length ? PHASE_META[steps[idx + 1]].percent : here + 6;
  const gap = Math.max(0, next - here);
  // Asymptotic: fast at first, never spans more than ~85% of the gap, so the
  // bar cannot arrive at the next rung before the server says so.
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
 * stallHint — an HONEST explanation when a phase overruns. These are the
 * sentences the user needed while the bar sat silently at 85%.
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

/** The subset of a managed machine this module reasons about. */
export interface WakeMachineLike {
  status?: string | null;
  provisionPhase?: string | null;
  provisionPhaseAt?: number | null;
  lastWokeAt?: number | null;
  runnersAuthorized?: boolean | null;
  errorMessage?: string | null;
  /** Provider's own word for the server state ("initializing", "running"). */
  providerStatus?: string | null;
  providerStatusAt?: number | null;
}

/** Provider vocabulary → a sentence, so we never print a raw API enum. */
const PROVIDER_STATUS_LABEL: Record<string, string> = {
  initializing: "the provider is still initializing the server",
  starting: "the provider is powering the server on",
  running: "the server is powered on at the provider",
  off: "the server is powered off at the provider",
  stopping: "the provider is stopping the server",
  deleting: "the provider is deleting the server",
  migrating: "the provider is migrating the server",
  rebuilding: "the provider is rebuilding the server",
};

/**
 * providerLine — one sentence about what the PROVIDER sees, or null.
 *
 * Deliberately silent once the box is reachable: at that point our own signal
 * is strictly better, and "the server is powered on" next to "Connecting over
 * the relay" is noise. Also silent on stale data — a provider status from ten
 * minutes ago is worse than none, because it reads as current.
 */
export function providerLine(
  machine: WakeMachineLike | null | undefined,
  phase: LifecyclePhase,
  now: number,
): string | null {
  if (phase !== "resuming" && phase !== "booting") return null;
  const raw = String(machine?.providerStatus ?? "").toLowerCase();
  if (!raw) return null;
  const at = typeof machine?.providerStatusAt === "number" ? machine.providerStatusAt : null;
  if (at !== null && now - at > 120_000) return null;
  const label = PROVIDER_STATUS_LABEL[raw];
  return label ? `Provider: ${label}.` : null;
}

/**
 * deriveServerPhase maps the authoritative control-plane signals — status +
 * provisionPhase — plus whether the device is actually reachable onto a
 * lifecycle phase. `deviceReachable` is the real "back online" bit (relay
 * presence), which flips independently of and later than status=active.
 */
export function deriveServerPhase(
  machine: WakeMachineLike | null | undefined,
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

  if (status === "resuming" || status === "active" || status === "provisioning") {
    if (deviceReachable) {
      return machine?.runnersAuthorized === false ? "online" : "ready";
    }
    switch (provision) {
      // Up, but its session expired — it cannot register on its own no matter
      // how long we wait. Mapping this to "registering" parked the bar at 65%
      // "Connecting over the free relay…" forever, for a connection that was
      // never coming.
      case "awaiting-yaver-auth":
        return "needs-auth";
      case "starting-agent":
      case "registering":
      case "authorizing-runners":
        return "registering";
      case "ready":
        // Control plane says ready but the device isn't reachable yet — the
        // "record written, box still cold" window. Show registering, not a
        // false 100%.
        return "registering";
      // Wake-only steps: finding the snapshot, freeing the volume, restoring
      // onto a new server. All three run BEFORE any server boots, so they
      // belong on the "Restoring" rung.
      case "checking-snapshot":
      case "preparing-volume":
      case "restoring-snapshot":
        return "resuming";
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

export interface WakeView {
  phase: LifecyclePhase;
  meta: PhaseMeta;
  /** 0-100, including in-phase creep. */
  percent: number;
  /** Rungs to render for this direction. */
  steps: LifecyclePhase[];
  /** ms since the CURRENT phase began. */
  elapsedInPhaseMs: number;
  /** ms since the wake was requested, or null when unknown. */
  elapsedTotalMs: number | null;
  direction: "wake" | "park" | null;
  stallHint: string | null;
  /** The control plane's own sentence, when it has one. */
  error: string | null;
  /** What the provider reports, while that is the best signal we have. */
  provider: string | null;
}

/**
 * computeWakeView — everything the progress UI needs, as one pure function of
 * (machine, reachable, now). Pure so the caller can drive re-renders off a
 * plain interval tick without the derivation itself holding state.
 */
export function computeWakeView(
  machine: WakeMachineLike | null | undefined,
  deviceReachable: boolean,
  now: number,
): WakeView {
  const phase = deriveServerPhase(machine, deviceReachable);
  const meta = PHASE_META[phase];
  const steps = meta.kind === "park" ? PARK_STEPS : WAKE_STEPS;

  // Prefer provisionPhaseAt: it times THIS phase, which is what separates
  // "booting for 20s" from "booting for 9 minutes". lastWokeAt is the whole
  // wake and would suppress every stall hint until the run was long dead.
  const phaseAt = typeof machine?.provisionPhaseAt === "number" ? machine.provisionPhaseAt : null;
  const wokeAt = typeof machine?.lastWokeAt === "number" ? machine.lastWokeAt : null;
  const elapsedInPhaseMs = Math.max(0, now - (phaseAt ?? wokeAt ?? now));
  const elapsedTotalMs = wokeAt !== null ? Math.max(0, now - wokeAt) : null;

  const percent = Math.min(100, meta.percent + creepPercent(phase, elapsedInPhaseMs, steps));

  return {
    phase,
    meta,
    percent,
    steps,
    elapsedInPhaseMs,
    elapsedTotalMs,
    direction: meta.kind === "terminal" ? null : meta.kind,
    stallHint: stallHint(phase, elapsedInPhaseMs),
    error: machine?.errorMessage?.trim() ? machine.errorMessage.trim() : null,
    provider: providerLine(machine, phase, now),
  };
}

/** m:ss for a live elapsed clock. */
export function formatClock(ms: number): string {
  const total = Math.max(0, Math.floor(ms / 1000));
  const m = Math.floor(total / 60);
  const s = total % 60;
  return `${m}:${String(s).padStart(2, "0")}`;
}

/** "~N min" remaining in this phase, or null when it's nearly done / unknown. */
export function etaLabel(phase: LifecyclePhase, elapsedMs: number): string | null {
  const typical = PHASE_TYPICAL_MS[phase];
  if (!typical) return null;
  const remaining = Math.max(0, typical - elapsedMs);
  if (remaining <= 15_000) return null;
  return `~${Math.max(1, Math.round(remaining / 60_000))} min`;
}
