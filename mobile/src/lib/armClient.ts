// armClient — drives a GENERIC multi-DOF arm cell (Fairino / Elephant myCobot /
// PAROL6 / any line-protocol robot) over the YAVER MESH, addressed by device,
// with YOUR bearer + machine:"local" (LAN-first, relay fallback). DOF + joint
// limits are DATA returned by arm_describe — the UI renders N joint sliders from
// that, nothing is hardcoded. Mirrors robotClient's transport.

import AsyncStorage from "@react-native-async-storage/async-storage";
import { quicClient } from "./quic";

const ARM_DEVICE_KEY = "yaver.arm.deviceId";
const AGENT_PORT = 18080;

export type ArmTarget = { id: string; lanIps?: string[]; host?: string; port?: number };

export type JointSpec = {
  name: string;
  type?: "revolute" | "prismatic" | "continuous";
  min: number;
  max: number;
  home?: number;
  unit?: string;
  maxVel?: number;
};
export type ArmInfo = {
  model?: string;
  vendor?: string;
  dof: number;
  joints: JointSpec[];
  hasCartesian?: boolean;
  poseFrame?: string;
  payloadKg?: number;
  reachMm?: number;
  source?: "robot" | "config";
};
export type JointState = { name: string; position: number; unit?: string };
export type Pose = { x: number; y: number; z: number; roll: number; pitch: number; yaw: number };
export type ArmStatus = {
  ok: boolean;
  backend?: string;
  connected?: boolean;
  enabled?: boolean;
  estopped?: boolean;
  joints?: JointState[];
  pose?: Pose | null;
  cameraOk?: boolean;
  error?: string;
};
export type ArmVerdict = {
  mode: "agent" | "frames";
  moved?: boolean;
  confidence?: number;
  obstruction?: boolean;
  reason?: string;
  observed?: string;
};
export type ArmMoveResult = {
  ok: boolean;
  code?: string;
  error?: string;
  kind?: string;
  joints?: JointState[];
  pose?: Pose | null;
  verify?: ArmVerdict;
  frames?: { before?: string; after?: string };
  tookMs?: number;
};
export type ArmConfig = {
  driver?: string;
  addr?: string;
  port?: number;
  baud?: number;
  info: ArmInfo;
  readFromRobot?: boolean;
  defaultVelPct?: number;
  defaultAccPct?: number;
  camera?: string;
  label?: string;
};
export type ArmDriver = { driver: string; label: string; transport: string; defaultPort?: number; info?: ArmInfo; note?: string };
export type RobotModel = { vendor: string; model: string; driver: string; transport: string; payloadKg?: number; reachMm?: number; info: ArmInfo; note?: string };
export type Waypoint = {
  joints?: Record<string, number>;
  pose?: Pose;
  velPct?: number;
  accPct?: number;
  dwellMs?: number;
  verify?: string;
  expectation?: string;
  label?: string;
};
export type ArmProgram = { name: string; waypoints: Waypoint[]; createdAt?: number; updatedAt?: number };
export type ArmRunResult = { ok: boolean; program?: string; completed?: number; total?: number; error?: string; tookMs?: number };
export type VerifyMode = "agent" | "frames" | "off";

export async function getArmDeviceId(): Promise<string> {
  return (await AsyncStorage.getItem(ARM_DEVICE_KEY)) || "";
}
export async function setArmDeviceId(id: string): Promise<void> {
  await AsyncStorage.setItem(ARM_DEVICE_KEY, id.trim());
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

async function armOps<T = any>(target: ArmTarget | undefined, verb: string, payload: Record<string, unknown>, timeoutMs = 120000): Promise<T> {
  if (!target?.id) return { ok: false, error: "pick an arm device first" } as unknown as T;
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

export const armClient = {
  drivers: (t: ArmTarget) => armOps<{ drivers: ArmDriver[] }>(t, "arm_drivers", {}, 15000),
  models: (t: ArmTarget) => armOps<{ models: RobotModel[]; byVendor: Record<string, RobotModel[]> }>(t, "arm_models", {}, 15000),
  configGet: (t: ArmTarget) => armOps<{ config: ArmConfig; enabled: boolean }>(t, "arm_config_get", {}, 15000),
  configSet: (t: ArmTarget, config: ArmConfig) => armOps<{ config: ArmConfig; note?: string }>(t, "arm_config_set", config as any, 15000),
  describe: (t: ArmTarget) => armOps<{ info: ArmInfo }>(t, "arm_describe", {}, 20000),
  status: (t: ArmTarget) => armOps<{ status: ArmStatus; info: ArmInfo }>(t, "arm_status", {}, 20000),
  state: (t: ArmTarget) => armOps<{ joints: JointState[]; pose: Pose | null }>(t, "arm_state", {}, 15000),
  enable: (t: ArmTarget, on: boolean) => armOps<ArmMoveResult>(t, "arm_enable", { on }, 15000),
  jog: (t: ArmTarget, joint: string, delta: number, velPct: number, verify: VerifyMode) =>
    armOps<ArmMoveResult>(t, "arm_jog", { joint, delta, velPct, verify }),
  movej: (t: ArmTarget, targets: Record<string, number>, velPct: number, verify: VerifyMode) =>
    armOps<ArmMoveResult>(t, "arm_movej", { targets, velPct, verify }),
  movel: (t: ArmTarget, pose: Pose, velPct: number, verify: VerifyMode) =>
    armOps<ArmMoveResult>(t, "arm_movel", { pose, velPct, verify }),
  home: (t: ArmTarget, velPct: number, verify: VerifyMode) => armOps<ArmMoveResult>(t, "arm_home", { velPct, verify }),
  stop: (t: ArmTarget) => armOps<{ stopped: boolean }>(t, "arm_stop", {}, 10000),
  estop: (t: ArmTarget) => armOps<{ estopped: boolean }>(t, "arm_estop", {}, 10000),
  reset: (t: ArmTarget) => armOps<{ estopped: boolean }>(t, "arm_reset", {}, 10000),
  // learning mode
  freedrive: (t: ArmTarget, on: boolean) => armOps<ArmMoveResult>(t, "arm_freedrive", { on }, 15000),
  teachCapture: (t: ArmTarget, label?: string, velPct?: number, dwellMs?: number) =>
    armOps<{ waypoint: Waypoint }>(t, "arm_teach_capture", { label, velPct, dwellMs }, 15000),
  programSave: (t: ArmTarget, program: ArmProgram) =>
    armOps<{ saved: string; waypoints: number }>(t, "arm_program_save", { program }, 15000),
  programList: (t: ArmTarget) => armOps<{ programs: ArmProgram[] }>(t, "arm_program_list", {}, 15000),
  programGet: (t: ArmTarget, name: string) => armOps<ArmProgram>(t, "arm_program_get", { name }, 15000),
  programDelete: (t: ArmTarget, name: string) => armOps<{ deleted: string }>(t, "arm_program_delete", { name }, 15000),
  programRun: (t: ArmTarget, name: string, verify: VerifyMode) =>
    armOps<ArmRunResult>(t, "arm_program_run", { name, verify }, 600000),
  // camera (shared box eye)
  snapshot: (t: ArmTarget) => armOps<{ ok?: boolean; image?: string; error?: string }>(t, "arm_snapshot", {}, 30000),
  look: (t: ArmTarget, prompt?: string) => armOps<{ ok?: boolean; answer?: string; image?: string; error?: string }>(t, "arm_look", { prompt }, 95000),
};
