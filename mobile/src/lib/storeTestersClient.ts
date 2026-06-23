// storeTestersClient — drives Yaver's app-store TESTER + BUILD management over
// the YAVER MESH (LAN-first, relay fallback, your bearer, machine:"local").
// Backed by the store_* MCP ops verbs (desktop/agent/ops_store.go →
// appstoreconnect.go / playpublish_api.go). apple = TestFlight beta testers/
// groups/builds; google = Play internal track Google-Groups + rollout.
// Mirrors circuitClient/printerClient transport. Credentials live in the box's
// vault, never on Convex.

import AsyncStorage from "@react-native-async-storage/async-storage";
import { quicClient } from "./quic";

const STORE_DEVICE_KEY = "yaver.storeTesters.deviceId";
const AGENT_PORT = 18080;

export type StoreTarget = { id: string; lanIps?: string[]; host?: string; port?: number };
export type Store = "apple" | "google";

export type ASCBetaGroup = { id: string; name: string; isInternal: boolean; publicLink?: string };
export type ASCBetaTester = { id: string; email: string; firstName?: string; lastName?: string; state?: string };
export type ASCBuild = { id: string; version: string; uploadedDate?: string; processingState?: string; expired?: boolean };
export type PlayRelease = { name?: string; versionCodes?: string[]; status?: string; userFraction?: number };

export async function getStoreDeviceId(): Promise<string> {
  return (await AsyncStorage.getItem(STORE_DEVICE_KEY)) || "";
}
export async function setStoreDeviceId(id: string): Promise<void> {
  await AsyncStorage.setItem(STORE_DEVICE_KEY, id.trim());
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

async function storeOps<T = any>(target: StoreTarget | undefined, verb: string, payload: Record<string, unknown>, timeoutMs = 30000): Promise<T> {
  if (!target?.id) return { ok: false, error: "pick a device first" } as unknown as T;
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

// common identity (store + project + app id) folded into every payload
type Ident = { store: Store; project?: string; bundleId?: string; packageName?: string; track?: string };
const id = (i: Ident, extra: Record<string, unknown> = {}) => ({ track: "internal", ...i, ...extra });

export const storeTestersClient = {
  credentialsStatus: (t: StoreTarget, project?: string) =>
    storeOps<{ apple?: any; google?: any }>(t, "store_credentials_status", { project }, 15000),

  groupList: (t: StoreTarget, i: Ident) =>
    storeOps<{ groups?: ASCBetaGroup[]; googleGroups?: string[] }>(t, "store_group_list", id(i), 20000),
  groupCreate: (t: StoreTarget, i: Ident, name: string, publicLink = false) =>
    storeOps<{ group: ASCBetaGroup }>(t, "store_group_create", id(i, { name, publicLink }), 20000),

  testerList: (t: StoreTarget, i: Ident) =>
    storeOps<{ testers?: ASCBetaTester[]; googleGroups?: string[]; note?: string }>(t, "store_tester_list", id(i), 20000),
  testerInvite: (t: StoreTarget, i: Ident, opts: { email?: string; firstName?: string; lastName?: string; group?: string; groupEmail?: string }) =>
    storeOps<{ tester?: ASCBetaTester; googleGroups?: string[] }>(t, "store_tester_invite", id(i, opts), 20000),
  testerRemove: (t: StoreTarget, i: Ident, opts: { email?: string; groupEmail?: string }) =>
    storeOps<{ removed?: string; googleGroups?: string[] }>(t, "store_tester_remove", id(i, opts), 20000),

  buildList: (t: StoreTarget, i: Ident) =>
    storeOps<{ builds?: ASCBuild[]; releases?: PlayRelease[] }>(t, "store_build_list", id(i), 20000),
  releasePromote: (t: StoreTarget, i: Ident, opts: { group?: string; status?: string; userFraction?: number } = {}) =>
    storeOps<{ assignedBuild?: ASCBuild; track_state?: any }>(t, "store_release_promote", id(i, opts), 30000),
};
