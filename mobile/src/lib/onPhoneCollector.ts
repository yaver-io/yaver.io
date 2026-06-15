// onPhoneCollector.ts — store-and-forward for the on-phone WebView collector.
// Extracted observations are queued locally (survives app restart) and drained
// to the phone's own agent (127.0.0.1) collection store, auto-registering a
// source + vantage. Mirrors jobqueue.go's disk-backed model so a flaky link
// loses nothing. See docs/yaver-task-packages.md (mobile target).

import AsyncStorage from "@react-native-async-storage/async-storage";
import { getToken } from "./auth";

const QUEUE_KEY = "yaver.packages.onphone.queue";
const IDS_KEY = "yaver.packages.onphone.ids";

export type QueuedObservation = {
  pkg: string;
  dataset: string;
  fields: Record<string, unknown>;
  at: number;
};

async function readQueue(): Promise<QueuedObservation[]> {
  const raw = await AsyncStorage.getItem(QUEUE_KEY);
  if (!raw) return [];
  try {
    return JSON.parse(raw) as QueuedObservation[];
  } catch {
    return [];
  }
}
async function writeQueue(q: QueuedObservation[]): Promise<void> {
  await AsyncStorage.setItem(QUEUE_KEY, JSON.stringify(q));
}

export async function queueObservation(obs: QueuedObservation): Promise<void> {
  const q = await readQueue();
  q.push(obs);
  if (q.length > 500) q.splice(0, q.length - 500);
  await writeQueue(q);
}

export async function pendingCount(): Promise<number> {
  return (await readQueue()).length;
}

async function agentOps(host: string, verb: string, payload: any): Promise<any | null> {
  const token = await getToken();
  if (!token) return null;
  try {
    const res = await fetch(`http://${host}:18080/ops`, {
      method: "POST",
      headers: { Authorization: `Bearer ${token}`, "Content-Type": "application/json" },
      body: JSON.stringify({ verb, payload, machine: "local" }),
    });
    const data = await res.json().catch(() => ({}));
    if (data?.ok === false) return null;
    return data?.initial ?? data;
  } catch {
    return null;
  }
}

async function ensureIds(host: string, pkg: string): Promise<{ sourceId: string; vantageId: string } | null> {
  const raw = await AsyncStorage.getItem(IDS_KEY);
  const cache: Record<string, { sourceId: string; vantageId: string }> = raw ? JSON.parse(raw) : {};
  if (cache[pkg]) return cache[pkg];
  const src = await agentOps(host, "collection_source_register", {
    name: pkg,
    kind: "package",
    accessState: "public_allowed",
  });
  const van = await agentOps(host, "collection_vantage_register", { egressPolicy: "machine_native" });
  const sourceId = src?.source?.sourceId;
  const vantageId = van?.vantage?.vantageId;
  if (!sourceId || !vantageId) return null;
  cache[pkg] = { sourceId, vantageId };
  await AsyncStorage.setItem(IDS_KEY, JSON.stringify(cache));
  return cache[pkg];
}

// drainToAgent ships queued observations to the phone's own agent and returns
// how many shipped. Anything that fails stays queued for the next drain.
export async function drainToAgent(host = "127.0.0.1"): Promise<number> {
  const q = await readQueue();
  if (q.length === 0) return 0;
  const remaining: QueuedObservation[] = [];
  let shipped = 0;
  for (const obs of q) {
    const ids = await ensureIds(host, obs.pkg);
    if (!ids) {
      remaining.push(obs);
      continue;
    }
    const res = await agentOps(host, "collection_observe", {
      sourceId: ids.sourceId,
      vantageId: ids.vantageId,
      dataset: obs.dataset,
      fields: obs.fields,
    });
    if (res?.observation) shipped++;
    else remaining.push(obs);
  }
  await writeQueue(remaining);
  return shipped;
}
