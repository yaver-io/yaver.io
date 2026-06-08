// studioClient — drives the Yaver store-asset Studio over the YAVER MESH,
// addressed by device, with your bearer + machine:"local" (LAN-first, relay
// fallback). Mirrors armClient's transport. Today it exposes the offline
// permission-justification path (analyze an app's manifest → Play Console prose
// + demo-video shot-list); the capture/record verbs land next (the agent-side
// capture layer lives in desktop/agent/studio/).
//
// Works against any device that holds the app's repo — the owner's own box
// (on-prem, free) or a Yaver-managed-cloud farm box.

import AsyncStorage from "@react-native-async-storage/async-storage";
import { quicClient } from "./quic";

const STUDIO_DEVICE_KEY = "yaver.studio.deviceId";
const AGENT_PORT = 18080;

export type StudioTarget = { id: string; lanIps?: string[]; host?: string; port?: number };

export type PermissionProse = {
  ok?: boolean;
  error?: string;
  code?: string;
  permission?: string;
  platform?: string;
  fgsType?: string;
  service?: string;
  subtype?: string;
  trigger?: string;
  declared?: boolean;
  taskOther?: string;
  description?: string;
  shotList?: string[];
  warnings?: string[];
  markdown?: string;
};

export async function getStudioDeviceId(): Promise<string> {
  return (await AsyncStorage.getItem(STUDIO_DEVICE_KEY)) || "";
}
export async function setStudioDeviceId(id: string): Promise<void> {
  await AsyncStorage.setItem(STUDIO_DEVICE_KEY, id);
}

async function lanAttempt(host: string, port: number, body: string, timeoutMs: number): Promise<any | null> {
  try {
    const ctrl = new AbortController();
    const t = setTimeout(() => ctrl.abort(), timeoutMs);
    const res = await fetch(`http://${host}:${port}/ops`, {
      method: "POST",
      headers: { ...quicClient.getAuthHeaders(), "Content-Type": "application/json" },
      body,
      signal: ctrl.signal,
    });
    clearTimeout(t);
    if (!res.ok) return null;
    return await res.json();
  } catch {
    return null;
  }
}

async function studioOps<T = any>(
  target: StudioTarget | undefined,
  verb: string,
  payload: Record<string, unknown>,
  timeoutMs = 60000,
): Promise<T> {
  if (!target?.id) return { ok: false, error: "pick a device that has your app's repo first" } as unknown as T;
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

export type StudioJob = {
  ok?: boolean;
  error?: string;
  id?: string;
  kind?: string;
  permission?: string;
  state?: "queued" | "running" | "completed" | "failed";
  phase?: string;
  log?: string[];
  durationSec?: number;
  artifacts?: {
    mp4?: string;
    captionedMp4?: string;
    justification?: string;
    captionCount?: number;
    dir?: string;
  };
};

export type StartJobArgs = {
  permission: string;
  apk: string; // app artifact built for the surface arch
  hostWorkDir: string; // dir on the surface host for the redroid /data mount
  path?: string;
  package?: string;
  activity?: string;
  startAction?: string;
  sshHost?: string; // empty = managed-cloud farm box (agent is local); else on-prem
  app?: string;
  what?: string;
  maxSec?: number;
};

export const studioClient = {
  /**
   * Generate the Play Console permission-justification prose + demo-video
   * shot-list for an app permission (offline, fast — no device).
   */
  permissionProse: (
    t: StudioTarget,
    args: { permission: string; path?: string; manifest?: string; app?: string; what?: string },
  ) => studioOps<PermissionProse>(t, "studio_permission_prose", args as any, 30000),

  /** Start an async capture job (records the permission demo video). Returns a job to poll. */
  startPermissionJob: (t: StudioTarget, args: StartJobArgs) =>
    studioOps<StudioJob>(t, "studio_job_start", args as any, 30000),

  /** Poll a job's live status (state/phase/log/artifacts). Omit jobId to list all. */
  jobStatus: (t: StudioTarget, jobId?: string) =>
    studioOps<StudioJob>(t, "studio_job_status", { jobId } as any, 15000),
};
