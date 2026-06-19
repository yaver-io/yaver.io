import AsyncStorage from "@react-native-async-storage/async-storage";
import { quicClient } from "./quic";

const TWIN_DEVICE_KEY = "yaver.twin.deviceId";
const AGENT_PORT = 18080;

export type TwinTarget = { id: string; lanIps?: string[]; host?: string; port?: number };

export type TwinSurface = "android-redroid" | "web-playwright" | "web-chromedp";
export type TwinMode = "manual" | "scripted" | "sdk-declared";

export type TwinStep = {
  action: string;
  url?: string;
  selector?: string;
  text?: string;
  key?: string;
  x?: number;
  y?: number;
  holdSec?: number;
  timeoutSec?: number;
  name?: string;
};

export type TwinStartArgs = {
  surface: TwinSurface;
  mode?: TwinMode;
  steps?: TwinStep[];
  record?: boolean;
  maxSec?: number;
  headful?: boolean;
  trace?: boolean;
  sshHost?: string;
  sshOpts?: string;
  workDir?: string;

  apk?: string;
  package?: string;
  activity?: string;
  hostWorkDir?: string;
  image?: string;
  keepSurface?: boolean;

  url?: string;
  browser?: "chromium" | "firefox" | "webkit";
  remoteDebuggingUrl?: string;
  viewportWidth?: number;
  viewportHeight?: number;
};

export type TwinJob = {
  ok?: boolean;
  error?: string;
  code?: string;
  id?: string;
  surface?: string;
  mode?: string;
  state?: "queued" | "running" | "completed" | "failed";
  phase?: string;
  log?: string[];
  durationSec?: number;
  artifacts?: {
    dir?: string;
    video?: string;
    trace?: string;
    frames?: string;
    logs?: string;
    crash?: string;
    screenshots?: string[];
    metadata?: Record<string, unknown>;
  };
};

export async function getTwinDeviceId(): Promise<string> {
  return (await AsyncStorage.getItem(TWIN_DEVICE_KEY)) || "";
}

export async function setTwinDeviceId(id: string): Promise<void> {
  await AsyncStorage.setItem(TWIN_DEVICE_KEY, id.trim());
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

async function twinOps<T = any>(
  target: TwinTarget | undefined,
  verb: string,
  payload: Record<string, unknown>,
  timeoutMs = 30000,
): Promise<T> {
  if (!target?.id) return { ok: false, error: "pick a remote dev machine first" } as unknown as T;
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

export const twinClient = {
  start: (t: TwinTarget, args: TwinStartArgs) => twinOps<TwinJob>(t, "twin_job_start", args as any, 30000),
  status: (t: TwinTarget, jobId?: string) => twinOps<TwinJob>(t, "twin_job_status", { jobId } as any, 15000),
};
