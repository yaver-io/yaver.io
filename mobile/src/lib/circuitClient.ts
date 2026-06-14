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

// A named netlist slot on the sim node (design slots / S-2). "" / "default" is
// the legacy single slot; named slots let one box hold many designs.
export type CircuitDesignSummary = { design: string; title?: string; elements?: number; simulatable?: boolean; engine?: string; updatedAt?: number };
export type CircuitHealth = { ok?: boolean; design?: string; enabled?: boolean; elements?: number; simulatable?: boolean; engine?: string; engines?: EngineCap[]; designCount?: number };

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

// d() folds an optional design-slot id into a verb payload. Omitted/blank → the
// default slot (back-compat).
const d = (design: string | undefined, extra: Record<string, unknown> = {}) =>
  design && design.trim() ? { design: design.trim(), ...extra } : extra;

export const circuitClient = {
  engines: (t: CircuitTarget, design?: string) => circuitOps<{ engines: EngineCap[]; active: string }>(t, "circuit_engines", d(design), 15000),
  configGet: (t: CircuitTarget, design?: string) => circuitOps<CircuitConfig>(t, "circuit_config_get", d(design), 15000),
  configSet: (t: CircuitTarget, cfg: Partial<CircuitConfig>, design?: string) => circuitOps<{ engine: string }>(t, "circuit_config_set", d(design, cfg as any), 15000),

  importNetlist: (t: CircuitTarget, text: string, format = "auto", design?: string) => circuitOps<{ info: CircuitInfo }>(t, "circuit_import", d(design, { text, format }), 30000),
  exportNetlist: (t: CircuitTarget, format: "spice" | "json" = "spice", design?: string) => circuitOps<{ format: string; spice?: string; netlist?: any }>(t, "circuit_export", d(design, { format }), 15000),
  describe: (t: CircuitTarget, design?: string) => circuitOps<{ info: CircuitInfo }>(t, "circuit_describe", d(design), 15000),

  simulate: (t: CircuitTarget, analysis: Analysis, design?: string) => circuitOps<{ result: SimResult }>(t, "circuit_simulate", d(design, analysis as any), 60000),
  measure: (t: CircuitTarget, design?: string) => circuitOps<{ nodeVoltages: Record<string, number>; branchCurrents: Record<string, number>; engine: string }>(t, "circuit_measure", d(design), 30000),
  erc: (t: CircuitTarget, design?: string) => circuitOps<{ report: ERCReport }>(t, "circuit_erc", d(design), 20000),
  setDomain: (t: CircuitTarget, net: string, volts: number, design?: string) => circuitOps<{ net: string; volts: number }>(t, "circuit_set_domain", d(design, { net, volts }), 12000),
  plot: (t: CircuitTarget, analysis: Analysis, signals?: string[], design?: string) =>
    circuitOps<{ image?: string; analysis?: string; signals?: string[]; engine?: string; ok?: boolean; error?: string }>(t, "circuit_plot", d(design, { ...analysis, signals }), 60000),

  // service primitives (S-2/S-3): list/delete design slots + node health.
  designs: (t: CircuitTarget) => circuitOps<{ designs: CircuitDesignSummary[] }>(t, "circuit_designs", {}, 15000),
  designDelete: (t: CircuitTarget, design: string) => circuitOps<{ design: string; deleted: boolean }>(t, "circuit_design_delete", { design }, 12000),
  health: (t: CircuitTarget, design?: string) => circuitOps<CircuitHealth>(t, "circuit_health", d(design), 12000),
};
