// printerClient — drives a 3D printer cell (Bambu Lab P1/P1S/A1/X1) over the
// YAVER MESH, addressed by device, with YOUR bearer + machine:"local" (LAN-first,
// relay fallback). Mirrors armClient/robotClient transport. Discovery is
// credential-free; everything else needs the box's saved printer config (the
// access code lives encrypted in the box vault, never on the phone).
//
// Also exposes the remote-CAD verbs (cad_*) since the printer screen closes the
// loop: write OpenSCAD on a dev box → render → preview on the phone → slice →
// upload → print.

import AsyncStorage from "@react-native-async-storage/async-storage";
import { quicClient } from "./quic";

const PRINTER_DEVICE_KEY = "yaver.printer.deviceId";
const AGENT_PORT = 18080;

export type PrinterTarget = { id: string; lanIps?: string[]; host?: string; port?: number };

export type TempPair = { cur: number; target: number };
export type PrinterStatus = {
  online: boolean;
  state: "idle" | "printing" | "paused" | "finished" | "failed" | "prepare" | "unknown" | string;
  stage?: string;
  nozzle: TempPair;
  bed: TempPair;
  chamber?: TempPair;
  progress: number;
  layerNum?: number;
  totalLayers?: number;
  remainingMin?: number;
  speedLevel?: number;
  fanSpeed?: number;
  subtaskName?: string;
  lightOn?: boolean | null;
  nozzleDiameter?: number;
  errors?: string[];
  updatedAt?: number;
};
export type PrinterInfo = {
  vendor: string;
  model: string;
  modelKey?: string;
  serial: string;
  name?: string;
  firmware?: string;
  ip?: string;
  hasCamera?: boolean;
  hasAMS?: boolean;
};
export type Discovered = {
  ip: string;
  serial: string;
  model: string;
  modelKey?: string;
  name?: string;
  firmware?: string;
  signalDb?: number;
  connect?: string;
  bind?: string;
};
export type PrinterConfig = {
  driver?: string;
  addr?: string;
  accessCode?: string; // write-only from the UI; read-back is redacted
  serial?: string;
  model?: string;
  name?: string;
  mqttPort?: number;
  cameraPort?: number;
  ftpPort?: number;
  cameraOverride?: string;
  label?: string;
};

// CAD
export type CadRender = {
  dir?: string;
  scadPath?: string;
  pngPath?: string;
  preview?: string; // data:image/png URL
  stlPath?: string;
  stlBytes?: number;
};
export type CadTools = { openscad: boolean; openscadPath?: string; slicer: boolean; slicerPath?: string; hints?: Record<string, string> };

export async function getPrinterDeviceId(): Promise<string> {
  return (await AsyncStorage.getItem(PRINTER_DEVICE_KEY)) || "";
}
export async function setPrinterDeviceId(id: string): Promise<void> {
  await AsyncStorage.setItem(PRINTER_DEVICE_KEY, id.trim());
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

async function printerOps<T = any>(target: PrinterTarget | undefined, verb: string, payload: Record<string, unknown>, timeoutMs = 30000): Promise<T> {
  if (!target?.id) return { ok: false, error: "pick a printer device first" } as unknown as T;
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

export const printerClient = {
  // discovery + config
  discover: (t: PrinterTarget, seconds = 6) => printerOps<{ printers: Discovered[]; count: number }>(t, "printer_discover", { seconds }, (seconds + 4) * 1000),
  drivers: (t: PrinterTarget) => printerOps<{ drivers: any[]; bambuModels: Record<string, string> }>(t, "printer_drivers", {}, 15000),
  configGet: (t: PrinterTarget) => printerOps<{ config: PrinterConfig; enabled: boolean }>(t, "printer_config_get", {}, 15000),
  configSet: (t: PrinterTarget, config: PrinterConfig) => printerOps<{ config: PrinterConfig; enabled: boolean }>(t, "printer_config_set", config as any, 15000),

  // telemetry
  info: (t: PrinterTarget) => printerOps<{ info: PrinterInfo }>(t, "printer_info", {}, 12000),
  status: (t: PrinterTarget) => printerOps<{ status: PrinterStatus }>(t, "printer_status", {}, 20000),
  snapshot: (t: PrinterTarget) => printerOps<{ ok?: boolean; image?: string; error?: string }>(t, "printer_snapshot", {}, 30000),

  // control
  light: (t: PrinterTarget, on: boolean) => printerOps<{ lightOn: boolean }>(t, "printer_light", { on }, 12000),
  pause: (t: PrinterTarget) => printerOps<{ ok: boolean }>(t, "printer_pause", {}, 12000),
  resume: (t: PrinterTarget) => printerOps<{ ok: boolean }>(t, "printer_resume", {}, 12000),
  stop: (t: PrinterTarget) => printerOps<{ ok: boolean }>(t, "printer_stop", {}, 12000),
  setTemp: (t: PrinterTarget, which: "nozzle" | "bed" | "chamber", celsius: number) => printerOps<{ which: string; target: number }>(t, "printer_set_temp", { which, celsius }, 12000),
  gcode: (t: PrinterTarget, line: string) => printerOps<{ sent: string }>(t, "printer_gcode", { line, confirm: true }, 12000),

  // print pipeline (DESTRUCTIVE — confirm gated by the agent)
  upload: (t: PrinterTarget, localPath: string, remoteName?: string) => printerOps<{ remoteFile: string }>(t, "printer_upload", { localPath, remoteName }, 180000),
  print: (t: PrinterTarget, remoteFile: string, opts?: { plate?: number; useAMS?: boolean; bedLevel?: boolean }) =>
    printerOps<{ started?: string; ok?: boolean; code?: string; error?: string }>(t, "printer_print", { remoteFile, confirm: true, ...opts }, 30000),

  // remote CAD (OpenSCAD on the box)
  cadTools: (t: PrinterTarget) => printerOps<CadTools>(t, "cad_tools", {}, 12000),
  cadRender: (t: PrinterTarget, scad: string, name?: string, params?: Record<string, string>) => printerOps<CadRender>(t, "cad_render", { scad, name, params }, 240000),
  cadPreview: (t: PrinterTarget, scad: string, params?: Record<string, string>) => printerOps<{ preview?: string; pngPath?: string }>(t, "cad_preview", { scad, params }, 90000),
  cadSlice: (t: PrinterTarget, modelPath: string, opts?: { slicer?: string; profile?: string }) => printerOps<{ outputPath?: string; bytes?: number; slicer?: string; error?: string }>(t, "cad_slice", { modelPath, ...opts }, 300000),
  cadGet: (t: PrinterTarget, path: string) => printerOps<{ name?: string; bytes?: number; base64?: string; mime?: string }>(t, "cad_get", { path }, 120000),
};
