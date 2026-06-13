// circuitClient — drives Yaver's electrical-circuit cell over the YAVER MESH,
// addressed by device, with YOUR bearer + machine:"local" (LAN-first, relay
// fallback). Mirrors printerClient/armClient transport. Import a SPICE netlist,
// a KiCad export, or an EPLAN/harness connection list; simulate with the
// dependency-free built-in MNA engine (or ngspice if the box has it); run a
// generic ERC; and view the waveform PNG the host coding agent also sees via
// the circuit_plot MCP tool. Netlists stay on the box (vault), never on Convex.

import AsyncStorage from "@react-native-async-storage/async-storage";
import { quicClient } from "./quic";

const CIRCUIT_DEVICE_KEY = "yaver.circuit.deviceId";
const AGENT_PORT = 18080;

export type CircuitTarget = { id: string; lanIps?: string[]; host?: string; port?: number };

export type Net = { name: string; connCount: number; domainV?: number; isGround?: boolean };
export type ElementInfo = { name: string; kind: string; nodes: string[]; value?: number; display?: string };
export type CircuitInfo = {
  title?: string;
  nets?: Net[];
  elements?: ElementInfo[];
  nodeCount?: number;
  elementCount?: number;
  sources?: string[];
  hasGround?: boolean;
  simulatable?: boolean;
  source?: string;
};
export type EngineCap = { engine: string; available: boolean; analyses: string[]; elements: string[]; nonlinear?: boolean; note?: string };
export type CircuitConfig = { engine?: string; ngspicePath?: string; enabled?: boolean; info?: CircuitInfo };
export type Analysis = {
  type?: "op" | "tran" | "ac" | "dc" | string;
  tstop?: number;
  tstep?: number;
  fstart?: number;
  fstop?: number;
  points?: number;
  sweepSrc?: string;
  sweepStart?: number;
  sweepStop?: number;
  sweepStep?: number;
};
export type SimResult = {
  analysis: string;
  signals: string[];
  samples: number[][];
  nodeVoltages?: Record<string, number>;
  branchCurrents?: Record<string, number>;
  engine: string;
};
export type ERCFinding = { rule: string; severity: "error" | "warning" | "info" | string; net?: string; element?: string; message: string };
export type ERCReport = { findings?: ERCFinding[]; errors: number; warnings: number; ok: boolean };

export async function getCircuitDeviceId(): Promise<string> {
  return (await AsyncStorage.getItem(CIRCUIT_DEVICE_KEY)) || "";
}
export async function setCircuitDeviceId(id: string): Promise<void> {
  await AsyncStorage.setItem(CIRCUIT_DEVICE_KEY, id.trim());
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

async function circuitOps<T = any>(target: CircuitTarget | undefined, verb: string, payload: Record<string, unknown>, timeoutMs = 30000): Promise<T> {
  if (!target?.id) return { ok: false, error: "pick a circuit device first" } as unknown as T;
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

export const circuitClient = {
  engines: (t: CircuitTarget) => circuitOps<{ engines: EngineCap[]; active: string }>(t, "circuit_engines", {}, 15000),
  configGet: (t: CircuitTarget) => circuitOps<CircuitConfig>(t, "circuit_config_get", {}, 15000),
  configSet: (t: CircuitTarget, cfg: Partial<CircuitConfig>) => circuitOps<{ engine: string }>(t, "circuit_config_set", cfg as any, 15000),

  importNetlist: (t: CircuitTarget, text: string, format = "auto") => circuitOps<{ info: CircuitInfo }>(t, "circuit_import", { text, format }, 30000),
  exportNetlist: (t: CircuitTarget, format: "spice" | "json" = "spice") => circuitOps<{ format: string; spice?: string; netlist?: any }>(t, "circuit_export", { format }, 15000),
  describe: (t: CircuitTarget) => circuitOps<{ info: CircuitInfo }>(t, "circuit_describe", {}, 15000),

  simulate: (t: CircuitTarget, analysis: Analysis) => circuitOps<{ result: SimResult }>(t, "circuit_simulate", analysis as any, 60000),
  measure: (t: CircuitTarget) => circuitOps<{ nodeVoltages: Record<string, number>; branchCurrents: Record<string, number>; engine: string }>(t, "circuit_measure", {}, 30000),
  erc: (t: CircuitTarget) => circuitOps<{ report: ERCReport }>(t, "circuit_erc", {}, 20000),
  setDomain: (t: CircuitTarget, net: string, volts: number) => circuitOps<{ net: string; volts: number }>(t, "circuit_set_domain", { net, volts }, 12000),
  plot: (t: CircuitTarget, analysis: Analysis, signals?: string[]) =>
    circuitOps<{ image?: string; analysis?: string; signals?: string[]; engine?: string; ok?: boolean; error?: string }>(t, "circuit_plot", { ...analysis, signals }, 60000),
};
