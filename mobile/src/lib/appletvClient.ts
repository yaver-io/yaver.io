// appletvClient — drives an Apple TV (control + now-playing) and the home
// capture-card video source over the YAVER MESH, addressed by device, with YOUR
// bearer + machine:"local" (LAN-first, relay fallback). Mirrors armClient's
// transport exactly. The agent side lives in desktop/agent/ops_appletv.go.
//
// Control + metadata is the robust, always-legal path. The capture-card video
// (captureFrameUrl / captureStreamUrl on quicClient) streams the user's OWN
// non-protected HDMI sources only — an HDCP-protected input is reported, never
// streamed.

import AsyncStorage from "@react-native-async-storage/async-storage";
import { quicClient } from "./quic";

const ATV_DEVICE_KEY = "yaver.appletv.deviceId";
const AGENT_PORT = 18080;

export type AppleTVTarget = { id: string; lanIps?: string[]; host?: string; port?: number };

export type ScannedATV = {
  identifier: string;
  name: string;
  address: string;
  model?: string;
  services?: string[];
};
export type PairedATV = {
  identifier: string;
  name: string;
  address: string;
  default?: boolean;
  protocols?: string[];
};
export type NowPlaying = {
  title?: string | null;
  artist?: string | null;
  album?: string | null;
  app?: string | null;
  state?: string;
  position?: number | null;
  total?: number | null;
  artwork_b64?: string;
  mimetype?: string;
  error?: string;
};
export type CaptureStatus = {
  running: boolean;
  device?: string;
  fps?: number;
  width?: number;
  height?: number;
  hasFrame?: boolean;
  hdcpBlocked?: boolean;
  note?: string;
  error?: string;
  ffmpeg?: boolean;
};
export type RemoteKey =
  | "up" | "down" | "left" | "right" | "select" | "menu" | "home"
  | "play" | "pause" | "stop" | "next" | "previous" | "play_pause"
  | "volume_up" | "volume_down";

export async function getAppleTVDeviceId(): Promise<string> {
  return (await AsyncStorage.getItem(ATV_DEVICE_KEY)) || "";
}
export async function setAppleTVDeviceId(id: string): Promise<void> {
  await AsyncStorage.setItem(ATV_DEVICE_KEY, id.trim());
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

async function atvOps<T = any>(target: AppleTVTarget | undefined, verb: string, payload: Record<string, unknown>, timeoutMs = 30000): Promise<T> {
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

export const appletvClient = {
  // discovery + pairing
  scan: (t: AppleTVTarget) => atvOps<{ devices: ScannedATV[] }>(t, "appletv_scan", {}, 15000),
  list: (t: AppleTVTarget) => atvOps<{ devices: PairedATV[] }>(t, "appletv_list", {}, 15000),
  pairBegin: (t: AppleTVTarget, identifier: string, protocol?: string) =>
    atvOps<{ session: string; device_provides_pin: boolean }>(t, "appletv_pair_begin", { identifier, protocol }, 30000),
  pairFinish: (t: AppleTVTarget, args: { session: string; pin?: number; identifier: string; name: string; address: string }) =>
    atvOps<{ paired: string; name: string }>(t, "appletv_pair_finish", args as any, 30000),
  // control
  key: (t: AppleTVTarget, key: RemoteKey, device?: string) => atvOps<{ ok: boolean }>(t, "appletv_remote_key", { key, device }, 15000),
  transport: (t: AppleTVTarget, action: RemoteKey, device?: string) => atvOps<{ ok: boolean }>(t, "appletv_transport", { action, device }, 15000),
  power: (t: AppleTVTarget, state: "on" | "off", device?: string) => atvOps<{ ok: boolean }>(t, "appletv_power", { state, device }, 20000),
  seek: (t: AppleTVTarget, seconds: number, device?: string) => atvOps<{ ok: boolean }>(t, "appletv_seek", { seconds, device }, 15000),
  launchApp: (t: AppleTVTarget, bundleId: string, device?: string) => atvOps<{ ok: boolean }>(t, "appletv_launch_app", { bundle_id: bundleId, device }, 15000),
  nowPlaying: (t: AppleTVTarget, device?: string) => atvOps<NowPlaying>(t, "appletv_now_playing", { device }, 15000),
  // capture card (home A/V source)
  captureDevices: (t: AppleTVTarget) => atvOps<{ devices: { path: string; name: string }[]; ffmpeg: boolean }>(t, "capture_devices", {}, 15000),
  captureStart: (t: AppleTVTarget, args: { device?: string; fps?: number; width?: number; height?: number }) =>
    atvOps<CaptureStatus>(t, "capture_start", args as any, 20000),
  captureStop: (t: AppleTVTarget) => atvOps<CaptureStatus>(t, "capture_stop", {}, 15000),
  captureStatus: (t: AppleTVTarget) => atvOps<CaptureStatus>(t, "capture_status", {}, 15000),
};
