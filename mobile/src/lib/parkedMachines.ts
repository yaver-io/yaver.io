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
// The ONE wake ladder. parkedMachines used to hold a second, less correct
// opinion of the same machine; it now derives from this one so the two cannot
// drift apart again. See deriveWakeView.
import { deriveServerPhase, PHASE_META } from "./wakeMachineCore";
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

// The wake ladder lives in wakeMachineCore — the React-free module — so it can
// be unit-tested without React Native, and so there is exactly ONE answer to
// "how far along is this wake?". This module used to hold a second, less correct
// copy; re-exporting keeps every existing import working.
export {
  WAKE_STAGES,
  deriveWakeView,
  type WakeTone,
  type WakeView,
} from "./wakeMachineCore";

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
  /** The most recent wake/park failure, NOT keyed by machine.
   *
   *  `errors` can only be rendered on a machine's own row, so a failure whose
   *  row then leaves the list (filtered out by status, or dropped by the
   *  /subscription payload) takes its explanation with it — the user taps Wake,
   *  the row disappears, and nothing is ever said. Keep the last failure here
   *  too so the picker can state it even with an empty list. */
  lastFailure: { machineId: string; message: string } | null;
  /** A machine that just finished waking, held briefly so a successful wake has
   *  a visible ending. Without it, "success" looks identical to the bug: the row
   *  silently vanishes from Sleeping the moment the box is usable. */
  justWoke: ManagedCloudMachineSummary | null;
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
  const [lastFailure, setLastFailure] = useState<{ machineId: string; message: string } | null>(null);
  const [justWoke, setJustWoke] = useState<ManagedCloudMachineSummary | null>(null);
  // machineIds we optimistically flipped to "waking" on tap, so the card shows
  // motion instantly before the server reports "resuming".
  const optimisticRef = useRef<Set<string>>(new Set());
  // machineIds THIS session asked to wake — survives the whole run, so we can
  // still announce the box when it finally lands on "active" (minutes later).
  const wakeIntentRef = useRef<Set<string>>(new Set());

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

  // Watch for a box crossing into "usable" and hold it briefly. A wake that
  // works ends with the row leaving this list (it is a device now, not a
  // sleeping machine) — which, with no announcement, is exactly what a wake
  // that FAILED used to look like. Give success its own visible ending.
  //
  // Intent is tracked in its OWN ref (not the optimistic flag, which the effect
  // above clears the moment the server reports any progress — it would be gone
  // before the box ever reached "active").
  useEffect(() => {
    for (const m of machines) {
      if (m.status !== "active") continue;
      if (!wakeIntentRef.current.has(m.id)) continue;
      wakeIntentRef.current.delete(m.id);
      setJustWoke(m);
      setTimeout(() => setJustWoke((cur) => (cur?.id === m.id ? null : cur)), 8000);
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
      wakeIntentRef.current.add(machineId);
      setLastFailure(null);
      try {
        await startManagedCloudMachine(token, machineId);
        await refresh();
      } catch (e: any) {
        optimisticRef.current.delete(machineId);
        wakeIntentRef.current.delete(machineId);
        const message =
          e?.message ||
          "Yaver couldn't wake this box right now. Check your balance and connection, then try again.";
        setErrors((prev) => ({ ...prev, [machineId]: message }));
        setLastFailure({ machineId, message });
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
        const message =
          e?.message || "Yaver couldn't park this box right now. Try again in a moment.";
        setErrors((prev) => ({ ...prev, [machineId]: message }));
        setLastFailure({ machineId, message });
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
    lastFailure,
    justWoke,
    wake,
    park,
    refresh,
  };
}
