// packagesClient.ts — mobile client for Yaver Task Packages
// (docs/yaver-task-packages.md). Mirrors armClient: LAN-first ops calls to the
// agent's package_* verbs, relay fallback. Used by app/packages.tsx and the
// background collector.

import AsyncStorage from "@react-native-async-storage/async-storage";
import { quicClient } from "./quic";

const PKG_DEVICE_KEY = "yaver.packages.deviceId";
const AGENT_PORT = 18080;

export type PackageTarget = { id: string; lanIps?: string[]; host?: string; port?: number };

export type PackageRow = {
  name: string;
  kind: string;
  tier: string;
  version: number;
  engines?: string[];
  runtimes?: string[];
  vantage?: { geo?: string[]; residential?: boolean };
};

export type PackageRunResult = {
  package: string;
  status: string;
  fields?: Record<string, unknown>;
  sourcesOk?: number;
  sourcesBlocked?: number;
  mcpCalls?: Array<Record<string, unknown>>;
  notes?: string[];
  observationId?: string;
  country?: string;
};

export type PackageCheckResult = {
  package: string;
  status: string; // pass | warn | fail
  at?: number;
  findings?: Array<{ level: string; code: string; message: string }>;
};

export async function getPackagesDeviceId(): Promise<string> {
  return (await AsyncStorage.getItem(PKG_DEVICE_KEY)) || "";
}
export async function setPackagesDeviceId(id: string): Promise<void> {
  await AsyncStorage.setItem(PKG_DEVICE_KEY, id.trim());
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

async function packageOps<T = any>(
  target: PackageTarget | undefined,
  verb: string,
  payload: Record<string, unknown>,
  timeoutMs = 60000,
): Promise<T> {
  if (!target?.id) return { ok: false, error: "pick a device first" } as unknown as T;
  const body = JSON.stringify({ verb, payload, machine: "local" });
  const port = target.port || AGENT_PORT;
  const hosts = [...(target.lanIps || []), ...(target.host ? [target.host] : [])].filter(Boolean);
  for (const h of hosts) {
    const data = await lanAttempt(h as string, port, body, timeoutMs);
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

export const packagesClient = {
  list: (t: PackageTarget) => packageOps<{ packages: PackageRow[]; count: number }>(t, "package_list", {}, 15000),
  get: (t: PackageTarget, name: string) => packageOps<any>(t, "package_get", { name }, 15000),
  run: (t: PackageTarget, name: string, confirm = false) =>
    packageOps<{ run: PackageRunResult }>(t, "package_run", { name, confirm }, 120000),
  check: (t: PackageTarget, name: string) =>
    packageOps<{ check: PackageCheckResult }>(t, "package_check", { name }, 60000),
  status: (t: PackageTarget, name?: string) => packageOps<any>(t, "package_status", { name }, 15000),
};
