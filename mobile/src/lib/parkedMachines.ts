// parkedMachines — mobile infra client + staged-wake model for the
// "wakeable parked machine" feature on the Infra tab.
//
// A parked managed box (Yaver snapshotted + deleted its Hetzner server so it
// accrues no hourly cost) shows up in /subscription with status
// "paused"/"stopped"/"suspended". This module:
//   • classifies which machines are parked / waking / awake,
//   • drives a polished STAGED "waking up" ladder from the box's own status +
//     provisionPhase + provisionProgress (never a bare spinner), and
//   • wraps the wake call (POST /billing/yaver-cloud/start) + an accelerated
//     poll so the card advances resuming → booting → registering → online.
//
// The wake itself is owner-scoped + balance-gated server-side; this is purely
// the presentation + polling driver.

import { useCallback, useEffect, useRef, useState } from "react";
import {
  getManagedSubscription,
  startManagedCloudMachine,
  stopManagedCloudMachine,
  type ManagedCloudMachineSummary,
  type ManagedSubscriptionSummary,
} from "./subscription";

// A parked box is one whose server was deleted but can be recreated from its
// snapshot. All three read as "Parked" in the UI.
export function isParkedStatus(status?: string | null): boolean {
  // NOTE: "stopped" is intentionally excluded — a stopped managed box is NOT
  // resumable (resumeMachine only accepts paused/suspended), so showing it as a
  // wakeable "ASLEEP" row just produces a 409 on Wake. Only paused/suspended
  // are genuinely wakeable.
  return status === "paused" || status === "suspended";
}

// True while the box is actively coming back (server record recreated, OS +
// agent still catching up). Drives the staged card instead of the Parked card.
export function isWakingStatus(status?: string | null): boolean {
  return status === "resuming" || status === "provisioning";
}

// The four honest stages of a wake, in order. The active stage is derived from
// the box's real status/phase so the ladder reflects the server, not a timer.
export const WAKE_STAGES: { key: string; label: string }[] = [
  { key: "creating", label: "Creating server" },
  { key: "restoring", label: "Restoring snapshot" },
  { key: "booting", label: "Booting" },
  { key: "online", label: "Agent online" },
];

export type WakeTone = "parked" | "waking" | "online" | "error";

export interface WakeView {
  tone: WakeTone;
  /** Headline for the card, e.g. "Waking up…" / "Yaver-managed · Parked". */
  title: string;
  /** Index into WAKE_STAGES of the CURRENT stage (0..3), or -1 when parked. */
  stageIndex: number;
  /** 0-100 progress for the bar. */
  percent: number;
  /** True while a wake is in flight (disable the button, show the ladder). */
  inFlight: boolean;
  /** A short curated failure label when the box reported an error. */
  error: string | null;
}

// Map a box's provisionPhase to one of our 4 stages. The box emits a finer
// phase vocabulary (creating|booting|installing-docker|…|registering|ready);
// we fold it onto the coarse ladder the card renders.
function phaseToStageIndex(phase: string | null | undefined): number {
  switch (phase) {
    case "creating":
      return 0;
    case "restoring":
    case "pulling-image":
      return 1;
    case "booting":
    case "installing-docker":
    case "starting-agent":
      return 2;
    case "registering":
    case "authorizing-runners":
    case "ready":
      return 3;
    default:
      return -1;
  }
}

// deriveWakeView turns a machine summary (+ an optional local "just tapped"
// flag) into everything the card needs. Progress is deliberately monotone-ish
// per stage so the bar reads as forward motion even between server polls.
export function deriveWakeView(
  m: ManagedCloudMachineSummary,
  optimisticWaking: boolean,
): WakeView {
  const status = m.status ?? "";
  if (status === "error" || m.provisionPhase === "error") {
    return {
      tone: "error",
      title: "Wake failed",
      stageIndex: -1,
      percent: 0,
      inFlight: false,
      error: (m as any).provisionError ?? m.errorMessage ?? "The box could not be recreated.",
    };
  }

  if (status === "active") {
    // Runners still authorizing counts as "almost online" but the server is up.
    return {
      tone: "online",
      title: "Online",
      stageIndex: WAKE_STAGES.length - 1,
      percent: 100,
      inFlight: false,
      error: null,
    };
  }

  if (isWakingStatus(status) || optimisticWaking) {
    // Prefer the phase-derived stage; fall back to a status default so we never
    // sit at stage 0 with no motion.
    let stageIndex = phaseToStageIndex(m.provisionPhase);
    if (stageIndex < 0) stageIndex = status === "provisioning" ? 2 : 0;
    // Percent: use the box's reported progress when present, else a per-stage
    // floor so each stage shows a distinct, forward position.
    const stageFloor = [10, 35, 60, 88][stageIndex] ?? 10;
    const percent =
      typeof m.provisionProgress === "number"
        ? Math.max(stageFloor - 5, Math.min(97, m.provisionProgress))
        : stageFloor;
    return {
      tone: "waking",
      title: "Waking up…",
      stageIndex,
      percent,
      inFlight: true,
      error: null,
    };
  }

  // Parked (paused/stopped/suspended) — the resting state with a Wake button.
  return {
    tone: "parked",
    title: "Yaver-managed · Parked",
    stageIndex: -1,
    percent: 0,
    inFlight: false,
    error: null,
  };
}

// Human "3h ago" / "2m ago" from an epoch-ms timestamp.
export function timeAgo(ts?: number | null): string | null {
  if (!ts || ts <= 0) return null;
  const s = Math.max(0, Math.floor((Date.now() - ts) / 1000));
  if (s < 60) return "just now";
  const m = Math.floor(s / 60);
  if (m < 60) return `${m}m ago`;
  const h = Math.floor(m / 60);
  if (h < 24) return `${h}h ago`;
  const d = Math.floor(h / 24);
  return `${d}d ago`;
}

// One-line spec summary for the Parked card. Degrades gracefully — falls back to
// machineType/serverType when detailed specs aren't in the payload yet.
export function specSummary(m: ManagedCloudMachineSummary): string {
  const parts: string[] = [];
  if (m.specs?.vcpu) parts.push(`${m.specs.vcpu} vCPU`);
  if (m.specs?.ramGb) parts.push(`${m.specs.ramGb} GB RAM`);
  if (m.specs?.diskGb) parts.push(`${m.specs.diskGb} GB disk`);
  if (parts.length === 0) {
    if (m.serverType) parts.push(m.serverType);
    parts.push((m.machineType ?? "cpu").toUpperCase());
  }
  if (m.region) parts.push(m.region.toUpperCase());
  return parts.join(" · ");
}

export interface UseParkedMachinesResult {
  machines: ManagedCloudMachineSummary[];
  /** True only when this account may use managed cloud (owner / launch flag). */
  hasAccess: boolean;
  loading: boolean;
  /** machineId currently being woken (button spinner), or null. */
  wakingId: string | null;
  /** machineId currently being parked/slept (button spinner), or null. */
  parkingId: string | null;
  /** Last wake/park error keyed by machineId. */
  errors: Record<string, string>;
  wake: (machineId: string) => Promise<void>;
  /** Park (sleep) a running managed box: snapshot + delete, scale-to-zero. */
  park: (machineId: string) => Promise<void>;
  refresh: () => Promise<void>;
}

// useParkedMachines owns the /subscription poll + the wake action. It polls
// slowly at rest and accelerates while any box is waking so the staged ladder
// advances promptly, then relaxes once everything settles.
export function useParkedMachines(token: string | null | undefined): UseParkedMachinesResult {
  const [data, setData] = useState<ManagedSubscriptionSummary | null>(null);
  const [loading, setLoading] = useState(true);
  const [wakingId, setWakingId] = useState<string | null>(null);
  const [parkingId, setParkingId] = useState<string | null>(null);
  const [errors, setErrors] = useState<Record<string, string>>({});
  // machineIds we optimistically flipped to "waking" on tap, so the card shows
  // motion instantly before the server reports "resuming".
  const optimisticRef = useRef<Set<string>>(new Set());

  const refresh = useCallback(async () => {
    if (!token) return;
    const r = await getManagedSubscription(token);
    if (r) setData(r);
    setLoading(false);
  }, [token]);

  // Frontend defense mirroring the backend /subscription filter: only surface
  // machines the user can act on (wakeable parked or live), never terminal
  // (removed/stopped) or duplicate rows sharing a deviceId. Guards against
  // stale/cached payloads from an un-updated backend so the picker never shows
  // "tons of devices to wake".
  const machines = (data?.machines ?? [])
    .filter((m) => {
      // Only status is safe to key off here: the /subscription payload does NOT
      // carry lastSnapshotId/volumeId/baseImageId, so an un-wakeable check on
      // those would evaluate every parked row to `undefined → hide` and empty
      // the whole Sleeping list (the "no wakeable device" bug). The BACKEND
      // already drops removed/stopped/un-wakeable-paused/zombie rows in
      // /subscription, so the client only needs to belt-and-suspenders the
      // terminal statuses (which ARE in the payload) and dedupe by deviceId.
      const s = String((m as any).status || "");
      return s !== "removed" && s !== "stopped";
    })
    .filter((m, i, arr) => {
      const dev = (m as any).deviceId;
      if (!dev) return true;
      return arr.findIndex((x) => (x as any).deviceId === dev) === i;
    });
  const anyInFlight = machines.some(
    (m) => isWakingStatus(m.status) || optimisticRef.current.has(m.id),
  );

  useEffect(() => {
    void refresh();
    const iv = setInterval(() => void refresh(), anyInFlight ? 3500 : 9000);
    return () => clearInterval(iv);
  }, [refresh, anyInFlight]);

  // Clear the optimistic flag once the server catches up (resuming/active) so we
  // don't hold a stale "waking" after settle.
  useEffect(() => {
    for (const m of machines) {
      if (
        optimisticRef.current.has(m.id) &&
        (isWakingStatus(m.status) || m.status === "active" || m.status === "error")
      ) {
        optimisticRef.current.delete(m.id);
      }
    }
  }, [machines]);

  const wake = useCallback(
    async (machineId: string) => {
      if (!token || wakingId) return;
      setWakingId(machineId);
      setErrors((e) => {
        const next = { ...e };
        delete next[machineId];
        return next;
      });
      optimisticRef.current.add(machineId);
      try {
        await startManagedCloudMachine(token, machineId);
        await refresh();
      } catch (e: any) {
        optimisticRef.current.delete(machineId);
        setErrors((prev) => ({
          ...prev,
          [machineId]:
            e?.message ||
            "Yaver couldn't wake this box right now. Check your balance and connection, then try again.",
        }));
      } finally {
        setWakingId(null);
      }
    },
    [token, wakingId, refresh],
  );

  const park = useCallback(
    async (machineId: string) => {
      if (!token || parkingId) return;
      setParkingId(machineId);
      setErrors((e) => {
        const next = { ...e };
        delete next[machineId];
        return next;
      });
      try {
        await stopManagedCloudMachine(token, machineId);
        await refresh();
      } catch (e: any) {
        setErrors((prev) => ({
          ...prev,
          [machineId]:
            e?.message ||
            "Yaver couldn't park this box right now. Try again in a moment.",
        }));
      } finally {
        setParkingId(null);
      }
    },
    [token, parkingId, refresh],
  );

  return {
    machines,
    hasAccess: data?.cloudAccess === true || data?.cloudPreviewOwner === true,
    loading,
    wakingId,
    parkingId,
    errors,
    wake,
    park,
    refresh,
  };
}
