import { useCallback, useEffect, useRef, useState } from "react";
import {
  getManagedSubscription,
  startManagedCloudMachine,
  stopManagedCloudMachine,
  type ManagedCloudMachineSummary,
} from "./subscription";

// wakeMachine — shared model + helpers for the managed-cloud box
// lifecycle: waking a self-parked box back up from its snapshot, and
// parking it back down. A paused box has snapshot+deleted its server, so
// it reports machineStatus "paused"/"stopped" and has no live endpoint —
// that's why the runner reads DISCONNECTED. Resuming recreates the server
// from the latest snapshot (~1-2 min) and it re-registers over the free
// relay with its persisted token (no re-auth).
//
// This file is the SINGLE SOURCE OF TRUTH for the wake/park phase
// vocabulary. Every surface — the shared RemoteBoxBanner, the Connection
// screen, car-voice, and (mirrored by string) the watch / TV / CLI —
// renders the same ladder so "waking up" and "closing down" look and read
// the same everywhere.
//
// Wake is intentionally user-intent driven (tap Wake / act on a sleeping
// box), never auto-fired on passive app-open — that would defeat the
// whole point of auto-off (every glance would spin the box back up + bill).

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

// ---------------------------------------------------------------------------
// Lifecycle phase model — the canonical contract.
// ---------------------------------------------------------------------------

/**
 * A single ordered vocabulary for both directions of the box lifecycle.
 * Wake:  asleep → requested → resuming → booting → registering → online → ready
 * Park:  ready  → snapshotting → powering-down → parked
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
  machine: Pick<ManagedCloudMachineSummary, "status" | "provisionPhase" | "runnersAuthorized"> | null | undefined,
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

// ---------------------------------------------------------------------------
// Actions.
// ---------------------------------------------------------------------------

export interface WakeResult {
  ok: boolean;
  error?: string;
}

/**
 * wakeManagedDevice asks the control plane to resume a paused managed box
 * from its latest snapshot. Resolves when the resume request is ACCEPTED —
 * the box then boots + re-registers over the relay asynchronously, so
 * drive `useMachineLifecycle` to show the rest. Safe to call again while a
 * resume is already in flight.
 */
export async function wakeManagedDevice(
  token: string | null | undefined,
  machineId: string | null | undefined,
): Promise<WakeResult> {
  if (!token) return { ok: false, error: "Not signed in." };
  if (!machineId) return { ok: false, error: "No managed machine to wake." };
  try {
    await startManagedCloudMachine(token, machineId);
    return { ok: true };
  } catch (e: any) {
    return { ok: false, error: e?.message ? String(e.message) : "Wake request failed." };
  }
}

/**
 * parkManagedDevice asks the control plane to park (snapshot + power down)
 * a running managed box to stop the meter. Resolves when the request is
 * ACCEPTED — the box snapshots then deletes its server asynchronously.
 */
export async function parkManagedDevice(
  token: string | null | undefined,
  machineId: string | null | undefined,
): Promise<WakeResult> {
  if (!token) return { ok: false, error: "Not signed in." };
  if (!machineId) return { ok: false, error: "No managed machine to park." };
  try {
    await stopManagedCloudMachine(token, machineId);
    return { ok: true };
  } catch (e: any) {
    return { ok: false, error: e?.message ? String(e.message) : "Park request failed." };
  }
}

// ---------------------------------------------------------------------------
// useMachineLifecycle — the shared driver hook.
// ---------------------------------------------------------------------------

export interface MachineLifecycleState {
  /** Current derived phase. */
  phase: LifecyclePhase;
  meta: PhaseMeta;
  /** 0-100, monotonic within a run so the bar never jumps backwards. */
  percent: number;
  /** Which direction we're animating, or null at rest. */
  direction: "wake" | "park" | null;
  /** True while an action is in flight (drive spinners, disable buttons). */
  busy: boolean;
  /** Last action error, if any. */
  error: string | null;
  /** The freshest managed-machine summary we polled, if available. */
  machine: ManagedCloudMachineSummary | null;
  wake: () => Promise<void>;
  park: () => Promise<void>;
}

export interface UseMachineLifecycleOpts {
  token: string | null | undefined;
  /** The focused device (for machineId + machineStatus + managed flag). */
  device: (WakeableDevice & { id?: string; name?: string }) | null | undefined;
  /** Real reachability of this device (live transport / relay presence). */
  deviceReachable: boolean;
  /** Called each poll tick so the caller can refresh its device list and
   *  pick up the isOnline flip fast (accelerated during a run). */
  onTick?: () => void;
  /** Poll cadence while a run is in flight (ms). Default 3500. */
  pollMs?: number;
}

/**
 * useMachineLifecycle owns the wake/park run: it fires the action, then
 * accelerates a poll of `/subscription` (+ the caller's device refresh via
 * onTick) to advance the phase from resuming → booting → registering →
 * online → ready (or snapshotting → powering-down → parked), stopping the
 * poll once settled. Progress is monotonic so the bar only ever fills.
 */
export function useMachineLifecycle(opts: UseMachineLifecycleOpts): MachineLifecycleState {
  const { token, device, deviceReachable, onTick, pollMs = 3500 } = opts;
  const machineId = device?.machineId ?? null;

  const [direction, setDirection] = useState<"wake" | "park" | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [machine, setMachine] = useState<ManagedCloudMachineSummary | null>(null);
  const [optimistic, setOptimistic] = useState<LifecyclePhase | null>(null);
  const floorRef = useRef(0); // monotonic percent floor within a run

  const onTickRef = useRef(onTick);
  onTickRef.current = onTick;

  // Resting phase straight off the device status when no run is active.
  const restingPhase: LifecyclePhase = isDeviceAsleep(device) ? "asleep" : deviceReachable ? "ready" : "asleep";

  const serverPhase = machine ? deriveServerPhase(machine, deviceReachable) : restingPhase;

  // The optimistic phase wins only until the server catches up past it, so
  // a tap gives instant feedback but never masks real progress/regression.
  let phase: LifecyclePhase = serverPhase;
  if (optimistic) {
    const optPct = PHASE_META[optimistic].percent;
    const srvPct = PHASE_META[serverPhase].percent;
    if (serverPhase !== "error" && srvPct < optPct) phase = optimistic;
  }
  if (deviceReachable && direction === "wake" && machine?.runnersAuthorized !== false) phase = "ready";

  // Monotonic progress within a run.
  let percent = PHASE_META[phase].percent;
  if (direction) {
    percent = Math.max(floorRef.current, percent);
    if (percent > floorRef.current) floorRef.current = percent;
  }
  const meta = PHASE_META[phase];

  // Adopt an externally-initiated transition — if the box starts resuming
  // or parking from another surface (or another device), reflect it here
  // too so the wake/park progress is consistent across every tab.
  useEffect(() => {
    if (direction) return;
    const st = String(device?.machineStatus ?? "").toLowerCase();
    if (st === "resuming") {
      setDirection("wake");
      floorRef.current = PHASE_META.resuming.percent;
    } else if (st === "stopping" || st === "grace") {
      setDirection("park");
      floorRef.current = PHASE_META.snapshotting.percent;
    }
  }, [device?.machineStatus, direction]);

  // Poll while a run is in flight.
  useEffect(() => {
    if (!direction || !token) return;
    let cancelled = false;
    let timer: ReturnType<typeof setTimeout> | null = null;

    const tick = async () => {
      try {
        onTickRef.current?.();
        const sub = await getManagedSubscription(token);
        if (cancelled) return;
        const m = sub?.machines?.find((x) => x.id === machineId) ?? null;
        if (m) setMachine(m);
      } catch {
        /* transient — keep polling */
      }
      if (!cancelled) timer = setTimeout(tick, pollMs);
    };
    timer = setTimeout(tick, pollMs);
    return () => {
      cancelled = true;
      if (timer) clearTimeout(timer);
    };
  }, [direction, token, machineId, pollMs]);

  // Settle: once the phase is terminal for our direction, end the run.
  useEffect(() => {
    if (!direction) return;
    const settledWake = direction === "wake" && (phase === "ready" || phase === "error");
    const settledPark = direction === "park" && (phase === "parked" || phase === "asleep" || phase === "error");
    if (settledWake || settledPark) {
      // brief hold so the user sees 100% / Ready before it clears
      const t = setTimeout(() => {
        setDirection(null);
        setOptimistic(null);
        floorRef.current = 0;
      }, 1400);
      return () => clearTimeout(t);
    }
  }, [direction, phase]);

  const wake = useCallback(async () => {
    if (busy || !machineId) return;
    setBusy(true);
    setError(null);
    setDirection("wake");
    setOptimistic("requested");
    floorRef.current = PHASE_META.requested.percent;
    const res = await wakeManagedDevice(token, machineId);
    if (!res.ok) {
      setError(res.error ?? "Wake failed.");
      setDirection(null);
      setOptimistic(null);
      floorRef.current = 0;
    }
    setBusy(false);
  }, [busy, token, machineId]);

  const park = useCallback(async () => {
    if (busy || !machineId) return;
    setBusy(true);
    setError(null);
    setDirection("park");
    setOptimistic("snapshotting");
    floorRef.current = PHASE_META.snapshotting.percent;
    const res = await parkManagedDevice(token, machineId);
    if (!res.ok) {
      setError(res.error ?? "Park failed.");
      setDirection(null);
      setOptimistic(null);
      floorRef.current = 0;
    }
    setBusy(false);
  }, [busy, token, machineId]);

  return { phase, meta, percent, direction, busy, error, machine, wake, park };
}
