// dataCollectionClient — drives Yaver's general data-collection cell over the
// YAVER MESH, addressed by device, with YOUR bearer + machine:"local" (LAN-first,
// relay fallback). Mirrors circuitClient/armClient transport. Read this runtime's
// egress identity, lend/borrow egress between your own machines (peer-egress),
// inspect per-vantage source health + blocks, and view the cross-vantage diff.
// Collected data stays on the box (local store), never on Convex.

import AsyncStorage from "@react-native-async-storage/async-storage";
import { quicClient } from "./quic";

const DATA_COLLECTION_DEVICE_KEY = "yaver.dataCollection.deviceId";
const AGENT_PORT = 18080;

export type DataCollectionTarget = { id: string; lanIps?: string[]; host?: string; port?: number };

export type Egress = {
  ip?: string;
  country?: string;
  region?: string;
  city?: string;
  asn?: string;
  org?: string;
  stable?: boolean;
  stableKnown?: boolean;
  viaProxy?: boolean;
  source?: string;
};
export type EgressProxyPolicy = { enabled?: boolean; allowPrivateTargets?: boolean; allowedPorts?: number[] };
export type EgressProxyStatus = { policy?: EgressProxyPolicy; effectivePorts?: number[]; egress?: Egress; recentAudit?: any[] };
export type HealthRow = {
  sourceId: string;
  vantageId: string;
  state: string;
  geoBlockCount24h?: number;
  ipBlockCount24h?: number;
  rateLimitCount24h?: number;
  lastRows?: number;
};
export type CompareRow = {
  vantageId: string;
  egressIp?: string;
  egressGeo?: string;
  egressCountry?: string;
  egressPolicy?: string;
  state?: string;
  values?: Record<string, unknown>;
};
export type VantageCompare = { sourceId: string; dataset?: string; fields?: string[]; vantages?: CompareRow[] };
export type EgressViaPeer = { bridge_id?: string; proxy_url?: string; peer_device?: string; peer_label?: string; peer_egress?: Egress };
export type CollectionPlanRequest = {
  source: string;
  action?: string;
  jurisdiction?: string;
  preferredRegion?: string;
  runtime?: "auto" | "yaver_managed_cloud" | "self_hosted" | "mobile_user_present" | "external_mcp_only" | string;
  needsDurable?: boolean;
  needsBrowser?: boolean;
  needsAndroid?: boolean;
  userPresentRequired?: boolean;
  adapter?: string;
  dataset?: string;
};
export type CollectionPlan = {
  ok?: boolean;
  status: "ready" | "warn" | "blocked" | "manual_required" | "no_runtime" | string;
  source: string;
  action: string;
  jurisdiction?: string;
  policy?: { decision: "allow" | "warn" | "block" | string; reason?: string; category?: string };
  runtime?: string;
  collectorType?: string;
  egressPolicy?: string;
  preferredRegion?: string;
  machine?: { deviceId?: string; name?: string; provider?: string; geoRegion?: string; isLocal?: boolean; isOnline?: boolean };
  viaPeer?: string;
  adapter?: string;
  accessState?: string;
  nextActions?: string[];
  reason?: string;
};
export type CollectionPlanResult = { plan: CollectionPlan; sourceId?: string; vantageId?: string; source?: any; vantage?: any };

export async function getDataCollectionDeviceId(): Promise<string> {
  return (await AsyncStorage.getItem(DATA_COLLECTION_DEVICE_KEY)) || "";
}
export async function setDataCollectionDeviceId(id: string): Promise<void> {
  await AsyncStorage.setItem(DATA_COLLECTION_DEVICE_KEY, id.trim());
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

async function collectionOps<T = any>(
  target: DataCollectionTarget | undefined,
  verb: string,
  payload: Record<string, unknown>,
  timeoutMs = 30000,
): Promise<T> {
  if (!target?.id) return { ok: false, error: "pick a data-collection device first" } as unknown as T;
  const body = JSON.stringify({ verb, payload, machine: "local" });
  const port = target.port || AGENT_PORT;
  const hosts = [...(target.lanIps || []), ...(target.host ? [target.host] : [])].filter(Boolean);
  for (const h of hosts) {
    const data = await lanAttempt(h as string, port, body, timeoutMs);
    if (data) return unwrapOps<T>(data);
  }
  const data = await quicClient.callOpsOnDevice(target.id, verb, payload, timeoutMs);
  return unwrapOps<T>(data);
}

// unwrapOps flattens an ops result. A verb that returns a structured payload
// (e.g. a collection_plan verdict) carries it in `initial` — preserve that even
// when ok===false, because a "blocked" plan is a valid verdict, not a transport
// error. Callers still detect failure via the merged ok/error fields.
function unwrapOps<T>(data: any): T {
  const initial = (data as any)?.initial;
  if (initial && typeof initial === "object") {
    if (data?.ok === false) return { ...initial, ok: false, code: data?.code, error: data?.error } as unknown as T;
    return initial as T;
  }
  if (data?.ok === false || data?.error) return { ok: false, code: data?.code, error: data?.error } as unknown as T;
  return data as T;
}

export const dataCollectionClient = {
  runtimeEgress: (t: DataCollectionTarget, refresh = false) => collectionOps<{ egress: Egress }>(t, "runtime_egress", { refresh }, 15000),
  egressProxyStatus: (t: DataCollectionTarget) => collectionOps<EgressProxyStatus>(t, "egress_proxy_status", {}, 15000),
  egressProxySet: (t: DataCollectionTarget, patch: Partial<EgressProxyPolicy>) =>
    collectionOps<{ policy: EgressProxyPolicy }>(t, "egress_proxy_set", patch as Record<string, unknown>, 15000),
  egressViaPeerStart: (t: DataCollectionTarget, device: string) => collectionOps<EgressViaPeer>(t, "egress_via_peer_start", { device }, 30000),
  egressViaPeerStop: (t: DataCollectionTarget, bridgeId: string) => collectionOps<any>(t, "egress_via_peer_stop", { bridge_id: bridgeId }, 15000),
  sourceHealth: (t: DataCollectionTarget, sourceId?: string) => collectionOps<{ health: HealthRow[] }>(t, "collection_source_health", sourceId ? { sourceId } : {}, 15000),
  blockList: (t: DataCollectionTarget, sourceId?: string) => collectionOps<{ blocked: HealthRow[]; count: number }>(t, "block_list", sourceId ? { sourceId } : {}, 15000),
  vantageCompare: (t: DataCollectionTarget, sourceId: string, dataset?: string) =>
    collectionOps<VantageCompare>(t, "collection_vantage_compare", dataset ? { sourceId, dataset } : { sourceId }, 20000),
  datasetQuery: (t: DataCollectionTarget, payload: Record<string, unknown>) => collectionOps<{ observations: any[]; count: number }>(t, "collection_dataset_query", payload, 20000),
  plan: (t: DataCollectionTarget, payload: CollectionPlanRequest) =>
    collectionOps<CollectionPlanResult>(t, "collection_plan", payload as Record<string, unknown>, 15000),
  planApply: (t: DataCollectionTarget, payload: CollectionPlanRequest) =>
    collectionOps<CollectionPlanResult>(t, "collection_plan_apply", payload as Record<string, unknown>, 15000),
};
