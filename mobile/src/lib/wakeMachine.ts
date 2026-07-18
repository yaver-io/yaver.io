import { useCallback, useEffect, useRef, useState } from "react";
import {
  getManagedSubscription,
  startManagedCloudMachine,
  stopManagedCloudMachine,
  type ManagedCloudMachineSummary,
} from "./subscription";
import { agentFetch } from "./agentRequest";
import { probeMobileDeviceStatus } from "./deviceStatus";
export {
  canWakeOnLan,
  deriveServerPhase,
  isDeviceAsleep,
  isPhaseInFlight,
  isPhaseSettled,
  PARK_STEPS,
  PHASE_META,
  PHASE_TYPICAL_MS,
  PHASE_TYPICAL_MS_LAN,
  phaseTypicalMs,
  WAKE_STEPS,
  WAKE_STEPS_LAN,
  wakeKindFor,
  wakeStepsFor,
  type LifecyclePhase,
  type LifecycleTone,
  type PhaseMeta,
  type WakeableDevice,
  type WakeKind,
} from "./wakeMachineCore";
import {
  creepPercent,
  wakeScaleFor,
  deriveServerPhase,
  isDeviceAsleep,
  PARK_STEPS,
  PHASE_META,
  stallHint,
  wakeKindFor,
  wakeStepsFor,
  WAKE_STEPS,
  type LifecyclePhase,
  type PhaseMeta,
  type WakeableDevice,
  type WakeKind,
} from "./wakeMachineCore";

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
 * wakePhysicalDevice wakes a self-hosted box that is asleep on a LAN, by
 * asking an agent that is already awake on that same LAN to broadcast a
 * magic packet for it (`POST /wake` on the peer).
 *
 * The indirection is not a design choice — a magic packet is link-local and
 * cannot be routed. A watch on cellular, a car head unit, a headset or the
 * web dashboard therefore has no way to wake anything on its own; the packet
 * has to originate on the sleeping box's own wire. `viaDeviceId` is whoever
 * is standing on it.
 *
 * Resolves when the packet has been SENT, which is as much as anyone can
 * ever know: Wake-on-LAN is fire-and-forget, with no acknowledgement of any
 * kind. Whether the box actually wakes is only observable by it reappearing,
 * so drive `useMachineLifecycle` with kind "lan" to show the rest.
 */
export async function wakePhysicalDevice(
  token: string | null | undefined,
  target: { mac?: string; viaDeviceId?: string } | null | undefined,
): Promise<WakeResult> {
  if (!token) return { ok: false, error: "Not signed in." };
  const mac = String(target?.mac ?? "").trim();
  const via = String(target?.viaDeviceId ?? "").trim();
  if (!mac) return { ok: false, error: "This machine has no known MAC address to wake." };
  if (!via) {
    return {
      ok: false,
      error: "Nothing is awake on that network to send the wake packet.",
    };
  }
  try {
    const res = await agentFetch(
      { id: via },
      token,
      "/wake",
      {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ mac }),
      },
      15000,
    );
    // The agent answers 200 with ok:false when it couldn't broadcast (it
    // isn't on the target's LAN, the MAC is malformed). That's a real answer
    // worth showing verbatim, not a transport failure.
    const body = (await res.json().catch(() => null)) as
      | { ok?: boolean; message?: string }
      | null;
    if (!res.ok) {
      return { ok: false, error: `Wake request failed (HTTP ${res.status}).` };
    }
    if (body?.ok === false) {
      return { ok: false, error: body?.message || "The wake packet could not be sent." };
    }
    return { ok: true };
  } catch (e: any) {
    // Reaching the waker is itself over the network — and it may have gone
    // to sleep too, which is worth saying plainly.
    return {
      ok: false,
      error: e?.message
        ? `Couldn't reach the machine that would send the wake packet: ${String(e.message)}`
        : "Couldn't reach the machine that would send the wake packet.",
    };
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
  /** An honest explanation when the current phase is overrunning (e.g. a long
   *  snapshot-restore boot, or the box not yet on the relay). Null when normal. */
  stallHint: string | null;
  /**
   * Why the box is stuck, straight from whoever knows. The control plane
   * already writes an exact sentence into `machine.errorMessage` ("The box is
   * awake but its Yaver agent session expired. Sign this machine in from your
   * phone to finish wake.") and every surface used to throw it away, leaving
   * the user staring at a bar that said "Connecting…" forever.
   */
  blockedReason: string | null;
  /**
   * Live detail probed off the box itself. A managed machine that never
   * registered has deviceId=null, so the normal device-health path can't see
   * it at all — but its agent is up and answers /health on serverIp with its
   * real lifecycle state, version and recovery mode. This is that answer.
   */
  probe: {
    lifecycleState?: string | null;
    authExpired?: boolean;
    version?: string | null;
    hostname?: string | null;
    reachable: boolean;
  } | null;
  /** How long we've been in the current phase (ms). */
  elapsedInPhaseMs: number;
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
  // How this box gets woken decides the ladder, the timings, the hints and
  // the action. Default to cloud so a device we can't wake at all still
  // renders its resting state exactly as before.
  const kind: WakeKind = wakeKindFor(device) ?? "cloud";

  const [direction, setDirection] = useState<"wake" | "park" | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [machine, setMachine] = useState<ManagedCloudMachineSummary | null>(null);
  const [optimistic, setOptimistic] = useState<LifecyclePhase | null>(null);
  const [probe, setProbe] = useState<MachineLifecycleState["probe"]>(null);
  const floorRef = useRef(0); // monotonic percent floor within a run
  // When the current phase began — for in-phase creep + stall hints.
  const phaseStartRef = useRef<{ phase: LifecyclePhase; at: number }>({ phase: "asleep", at: Date.now() });
  // Re-render on a slow tick so the creep advances (and a stall hint appears)
  // even between server polls; otherwise a long boot would look frozen.
  const [, setTick] = useState(0);

  const onTickRef = useRef(onTick);
  onTickRef.current = onTick;

  // Resting phase straight off the device status when no run is active.
  const restingPhase: LifecyclePhase = isDeviceAsleep(device) ? "asleep" : deviceReachable ? "ready" : "asleep";

  const serverPhase =
    kind === "lan"
      ? deriveServerPhase(null, deviceReachable, "lan")
      : machine
        ? deriveServerPhase(machine, deviceReachable)
        : restingPhase;

  // The optimistic phase wins only until the server catches up past it, so
  // a tap gives instant feedback but never masks real progress/regression.
  let phase: LifecyclePhase = serverPhase;
  if (optimistic) {
    const optPct = PHASE_META[optimistic].percent;
    const srvPct = PHASE_META[serverPhase].percent;
    if (serverPhase !== "error" && srvPct < optPct) phase = optimistic;
  }
  if (deviceReachable && direction === "wake" && machine?.runnersAuthorized !== false) phase = "ready";

  // Time spent in the CURRENT phase — drives the in-phase creep (so a ~10 min
  // snapshot-restore boot never looks like a frozen bar) and the stall hints
  // (so a long "connecting over the relay" says what's actually happening
  // instead of silently sitting at 80%).
  if (phaseStartRef.current.phase !== phase) {
    phaseStartRef.current = { phase, at: Date.now() };
  }
  const elapsedInPhaseMs = Date.now() - phaseStartRef.current.at;

  // Monotonic progress within a run, plus a continuous creep inside the phase.
  const steps = direction === "park" ? PARK_STEPS : wakeStepsFor(kind);
  // Pace the bar and the stall hints to THIS box's measured wake, not to the
  // constants — those were timed on one cx43 with a 160 GB disk, so a
  // volume-backed box that wakes in ~2 min was being crawled through an
  // 8-minute animation and told "~7 min left".
  const paceScale = kind === "lan" ? 1 : wakeScaleFor(machine);
  const creep = direction ? creepPercent(phase, elapsedInPhaseMs, steps, kind, paceScale) : 0;
  let percent = PHASE_META[phase].percent + creep;
  if (direction) {
    percent = Math.max(floorRef.current, percent);
    if (percent > floorRef.current) floorRef.current = percent;
  }
  percent = Math.min(100, percent);
  const meta = PHASE_META[phase];
  const hint = direction ? stallHint(phase, elapsedInPhaseMs, kind, paceScale) : null;

  // A managed box that is UP but never registered (deviceId null) is
  // invisible to every device-health path — yet its agent is running and
  // answers /health on serverIp unauthenticated, with the real lifecycle
  // state, version and recovery mode. Ask it directly; that answer is the
  // difference between "Connecting…" forever and telling the user their
  // session expired.
  const serverIp = machine?.serverIp ?? null;
  const shouldProbe = kind === "cloud" && !!serverIp && !deviceReachable;
  useEffect(() => {
    if (!shouldProbe || !serverIp) {
      setProbe(null);
      return;
    }
    let cancelled = false;
    const run = async () => {
      try {
        const r = await probeMobileDeviceStatus(
          { id: machineId ?? serverIp, host: serverIp, port: 18080 },
          token ?? null,
          4000,
        );
        if (cancelled) return;
        setProbe({
          lifecycleState: r.lifecycleState ?? null,
          authExpired: r.authExpired,
          version: (r.info?.version as string) ?? null,
          hostname: (r.info?.hostname as string) ?? null,
          reachable: r.reachable,
        });
      } catch {
        if (!cancelled) setProbe(null);
      }
    };
    void run();
    const iv = setInterval(run, 8000);
    return () => {
      cancelled = true;
      clearInterval(iv);
    };
  }, [shouldProbe, serverIp, machineId, token]);

  // Keep the bar alive while a run is in flight: a 1s tick re-renders so the
  // in-phase creep advances and the stall hint appears on time, independent of
  // the (slower) server poll.
  useEffect(() => {
    if (!direction) return;
    const iv = setInterval(() => setTick((t) => t + 1), 1000);
    return () => clearInterval(iv);
  }, [direction]);

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
        // A LAN box isn't in the managed-machine list, so there is nothing to
        // look up — onTick's device refresh (the isOnline flip) is the only
        // signal that matters. Asking anyway would burn a request per tick to
        // always find nothing.
        if (kind === "lan") return;
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
  }, [direction, token, machineId, pollMs, kind]);

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
    // A LAN box has no machineId — it isn't a managed machine at all — so
    // only the cloud path can require one.
    if (busy) return;
    if (kind === "cloud" && !machineId) return;
    setBusy(true);
    setError(null);
    setDirection("wake");
    setOptimistic("requested");
    floorRef.current = PHASE_META.requested.percent;
    const res =
      kind === "lan"
        ? await wakePhysicalDevice(token, device?.wakeOnLan ?? null)
        : await wakeManagedDevice(token, machineId);
    if (!res.ok) {
      setError(res.error ?? "Wake failed.");
      setDirection(null);
      setOptimistic(null);
      floorRef.current = 0;
    }
    setBusy(false);
  }, [busy, token, machineId, kind, device?.wakeOnLan]);

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

  // Why the box is stuck. Prefer the control plane's own sentence — it is
  // written for the user and names the fix — then fall back to what the box
  // says about itself when it never registered and nobody else can see it.
  const blockedReason =
    phase === "needs-auth" || phase === "error"
      ? (machine?.errorMessage ??
        (probe?.authExpired
          ? "This machine's Yaver session expired, so it can't finish connecting on its own. Sign it in to finish waking."
          : null))
      : null;

  return {
    phase,
    meta,
    percent,
    direction,
    busy,
    error,
    stallHint: hint,
    blockedReason,
    probe,
    elapsedInPhaseMs,
    machine,
    wake,
    park,
  };
}
