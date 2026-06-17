// homeClient — drives the "single kumanda" home-control surface
// (docs/yaver-single-kumanda.md) over the YAVER MESH, addressed by the hub
// device, LAN-direct first with relay fallback. Mirrors appletvClient's
// transport exactly. The agent side lives in desktop/agent/ops_home.go +
// ops_home_activity.go (the home_* ops verbs).
//
// This is a deliberately separate surface from the coding-agent UI: the remote
// / appliance / camera features live under the "Home" hub, never the dev tabs.

import { quicClient } from "./quic";

const AGENT_PORT = 18080;

export type HomeTarget = { id: string; lanIps?: string[]; host?: string; port?: number };

export type HomeDevice = { id: string; name?: string; kind: string; address?: string };
export type HomeStep = { device: string; key: string; app?: string; onError?: string };
export type HomeActivity = { name: string; steps: HomeStep[] };

// Canonical logical keys the router understands (per-device subset).
export type HomeKey =
  | "up" | "down" | "left" | "right" | "ok" | "back" | "home" | "menu"
  | "play" | "pause" | "play_pause" | "stop" | "next" | "previous"
  | "vol_up" | "vol_down" | "mute" | "power" | "power_on" | "power_off"
  | "channel_up" | "channel_down" | "launch"
  | "0" | "1" | "2" | "3" | "4" | "5" | "6" | "7" | "8" | "9";

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

async function homeOps<T = any>(target: HomeTarget | undefined, verb: string, payload: Record<string, unknown>, timeoutMs = 20000): Promise<T> {
  if (!target?.id) return { ok: false, error: "pick a hub device first" } as unknown as T;
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

export const homeClient = {
  // devices
  listDevices: (t: HomeTarget) => homeOps<{ devices: HomeDevice[] }>(t, "home_device_list", {}, 15000),
  addDevice: (t: HomeTarget, d: HomeDevice) => homeOps<{ device: HomeDevice; updated: boolean }>(t, "home_device_add", d as any, 15000),
  removeDevice: (t: HomeTarget, id: string) => homeOps<{ removed: boolean }>(t, "home_device_remove", { id }, 15000),
  // control — the router
  key: (t: HomeTarget, device: string, key: HomeKey, app?: string) =>
    homeOps<{ sent?: string; ok?: boolean }>(t, "home_key", { device, key, app }, 15000),
  // activities
  listActivities: (t: HomeTarget) => homeOps<{ activities: HomeActivity[] }>(t, "home_activity_list", {}, 15000),
  createActivity: (t: HomeTarget, a: HomeActivity) => homeOps<{ activity: string; steps: number }>(t, "home_activity_create", a as any, 15000),
  removeActivity: (t: HomeTarget, name: string) => homeOps<{ removed: boolean }>(t, "home_activity_remove", { name }, 15000),
  runActivity: (t: HomeTarget, name: string) =>
    homeOps<{ activity: string; completed: boolean; steps: { device?: string; key?: string; verb?: string; ok: boolean; error?: string }[] }>(t, "home_activity_run", { name }, 30000),
  // cameras
  addCamera: (t: HomeTarget, args: { id: string; name?: string; url: string }) => homeOps<{ camera: string }>(t, "camera_add", args as any, 15000),
  listCameras: (t: HomeTarget) => homeOps<{ cameras: { id: string; name?: string }[] }>(t, "camera_list", {}, 15000),
  cameraSnapshot: (t: HomeTarget, id: string) => homeOps<{ image_b64: string; mime: string; bytes: number }>(t, "camera_snapshot", { id }, 25000),
  cameraMotion: (t: HomeTarget, id: string) => homeOps<{ motion: boolean; score: number; dark: boolean }>(t, "camera_motion", { id }, 25000),
  // air conditioners
  addAC: (t: HomeTarget, args: { id: string; name?: string; kind: string; host: string; devid?: string; localkey?: string; version?: string }) =>
    homeOps<{ ac: string }>(t, "ac_add", args as any, 15000),
  listACs: (t: HomeTarget) => homeOps<{ acs: { id: string; name?: string; kind: string }[] }>(t, "ac_list", {}, 15000),
  acSet: (t: HomeTarget, args: { id: string; power?: boolean; mode?: string; temp?: number; fan?: string; swing?: boolean }) =>
    homeOps<any>(t, "ac_set", args as any, 20000),
  acStatus: (t: HomeTarget, id: string) => homeOps<any>(t, "ac_status", { id }, 15000),
  // IR
  irScan: (t: HomeTarget) => homeOps<{ devices: { host: string; mac: string; type: string; name: string }[] }>(t, "ir_scan", {}, 20000),
  irLearn: (t: HomeTarget, device: string, key: string, host: string) => homeOps<any>(t, "ir_learn", { device, key, host }, 40000),
  irList: (t: HomeTarget, device: string) => homeOps<{ device: string; keys: string[] }>(t, "ir_list", { device }, 15000),
  // Android TV / Mi Box (remote v2 — no ADB)
  atv2PairBegin: (t: HomeTarget, host: string) => homeOps<any>(t, "atv2_pair_begin", { host }, 25000),
  atv2PairFinish: (t: HomeTarget, args: { host: string; code: string; id: string; name?: string }) => homeOps<any>(t, "atv2_pair_finish", args as any, 25000),
  atv2List: (t: HomeTarget) => homeOps<{ devices: { id: string; name?: string; host: string }[] }>(t, "atv2_list", {}, 15000),
  // generic — call any ops verb on the hub
  runVerb: (t: HomeTarget, verb: string, payload: Record<string, unknown> = {}) => homeOps<any>(t, verb, payload, 25000),
};
