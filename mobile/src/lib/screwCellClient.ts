// screwCellClient — reads Yaver's screw-cell shop-floor analytics over the
// YAVER MESH (LAN-first via /ops, relay fallback), the same agent verbs the
// firmware pushes to (screw_cell_record via cell_runner.py --yaver) and the
// coding agent reads via the screw_cell_analytics MCP tool. Runs live in the
// agent's vault ("screw-cell"/"runs"), never on Convex. Mirrors circuitClient's
// transport exactly.

import AsyncStorage from "@react-native-async-storage/async-storage";
import { quicClient } from "./quic";

const SCREW_DEVICE_KEY = "yaver.screwcell.deviceId";
const AGENT_PORT = 18080;

export type ScrewTarget = { id: string; lanIps?: string[]; host?: string; port?: number };

export type ScrewTotals = { runs: number; screws: number; passed: number; failed: number; failRate: number };
export type ScrewTrendPoint = { date: string; screws: number; failRate: number };
export type ScrewLabelRow = { label: string; runs: number; screws: number; passed: number; failRate: number };
export type ScrewFlaggedOrder = {
  ficheno: string;
  productId?: string;
  blocks: number;
  flaggedBlocks: number;
  screws: number;
  failed: number;
  failRate: number;
  lastAt?: number;
};
export type ScrewRecentRun = {
  id: string;
  label?: string;
  ficheno?: string;
  screws: number;
  passed: number;
  failRate: number;
  createdAt?: number;
};
export type ScrewAnalytics = {
  window?: { days: number };
  totals: ScrewTotals;
  trend: ScrewTrendPoint[];
  byLabel: ScrewLabelRow[];
  flaggedOrders: ScrewFlaggedOrder[];
  recent: ScrewRecentRun[];
};
export type ScrewRunSummary = {
  id: string;
  label?: string;
  ficheno?: string;
  screws: number;
  passed: number;
  flagged: boolean;
  failRate: number;
  host?: string;
  createdAt?: number;
};
export type ScrewByOrder = {
  ficheno: string;
  blocks: number;
  blocksFlagged: number;
  screws: number;
  passed: number;
  failed: number;
  failRate: number;
  runs: ScrewRunSummary[];
};

export async function getScrewDeviceId(): Promise<string> {
  return (await AsyncStorage.getItem(SCREW_DEVICE_KEY)) || "";
}
export async function setScrewDeviceId(id: string): Promise<void> {
  await AsyncStorage.setItem(SCREW_DEVICE_KEY, id.trim());
}

async function lanAttempt(host: string, port: number, body: string, timeoutMs: number): Promise<any | null> {
  const ctrl = new AbortController();
  const timer = setTimeout(() => ctrl.abort(), Math.min(timeoutMs, 8000));
  try {
    const res = await fetch(`http://${host}:${port}/ops`, {
      method: "POST",
      headers: { ...quicClient.getAuthHeaders(), "Content-Type": "application/json" },
      body,
      signal: ctrl.signal,
    });
    const data = await res.json().catch(() => ({}));
    if (res.ok || data?.error) return data;
    return null;
  } catch {
    return null;
  } finally {
    clearTimeout(timer);
  }
}

async function screwOps<T = any>(target: ScrewTarget | undefined, verb: string, payload: Record<string, unknown>, timeoutMs = 30000): Promise<T> {
  if (!target?.id) return { ok: false, error: "pick a screw-cell device first" } as unknown as T;
  const body = JSON.stringify({ verb, payload, machine: "local" });
  const port = target.port || AGENT_PORT;
  const hosts = [...(target.lanIps || []), ...(target.host ? [target.host] : [])].filter(Boolean);
  for (const h of hosts) {
    const data = await lanAttempt(h, port, body, timeoutMs);
    if (data) {
      if (data?.ok === false || (data?.error && data?.initial === undefined)) {
        return { ok: false, code: data?.code, error: data?.error } as unknown as T;
      }
      return ((data as any)?.initial ?? data) as T;
    }
  }
  const data = await quicClient.callOpsOnDevice(target.id, verb, payload, timeoutMs);
  if (data?.ok === false) return { ok: false, code: (data as any)?.code, error: data?.error } as unknown as T;
  return ((data as any)?.initial ?? data) as T;
}

export const screwCellClient = {
  analytics: (t: ScrewTarget, days = 30) => screwOps<ScrewAnalytics>(t, "screw_cell_analytics", { days }, 20000),
  runs: (t: ScrewTarget, limit?: number) => screwOps<{ runs: ScrewRunSummary[] }>(t, "screw_cell_runs", limit ? { limit } : {}, 15000),
  byOrder: (t: ScrewTarget, ficheno: string) => screwOps<ScrewByOrder>(t, "screw_cell_by_order", { ficheno }, 15000),
  record: (t: ScrewTarget, run: { label?: string; ficheno?: string; product?: string; screws: number; passed: number; results?: any[] }) =>
    screwOps<{ id: string; flagged: boolean; failRate: number }>(t, "screw_cell_record", run as any, 15000),
};
