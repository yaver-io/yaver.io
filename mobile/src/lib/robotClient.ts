// robotClient — drives a robot cell over the YAVER MESH, addressed by device
// (docs/yaver-robot-fleet-mesh-design.md). It reaches the chosen device's agent
// with YOUR bearer + machine:"local" — NOT the gateway proxy (which forwards the
// gateway's own token and gets rejected as a different user). It tries the
// device's LAN address first (same-WiFi, fast), then a relay fallback. One app,
// many robots: each is just a device you pick.

import AsyncStorage from "@react-native-async-storage/async-storage";
import { quicClient } from "./quic";

const ROBOT_DEVICE_KEY = "yaver.robot.deviceId";
const AGENT_PORT = 18080;

export type RobotTarget = { id: string; lanIps?: string[]; host?: string; port?: number };

export type RobotPosition = { x: number; y: number; z: number; homed: boolean };
export type RobotVerdict = {
  mode: "agent" | "frames";
  moved?: boolean;
  confidence?: number;
  obstruction?: boolean;
  expectation?: string;
  reason?: string;
  observed?: string;
};
export type RobotFrames = { before?: string; after?: string };
export type RobotCrossCheck = {
  expectedDelta?: Record<string, number>;
  observedDelta?: Record<string, number>;
  agree: boolean;
};
export type RobotMoveResponse = {
  ok: boolean;
  code?: string;
  error?: string;
  action?: Record<string, unknown>;
  position?: RobotPosition;
  verify?: RobotVerdict;
  frames?: RobotFrames;
  encoderCrossCheck?: RobotCrossCheck;
  tookMs?: number;
};
export type RobotModule = "motion" | "tool" | "rotate" | "gpio" | "camera";
export type RobotStatus = {
  ok: boolean;
  backend?: string;
  connected?: boolean;
  position?: RobotPosition;
  tool?: string;
  estopped?: boolean;
  cameraOk?: boolean;
  error?: string;
  code?: string;
  // profile (added by the agent's robot_status)
  profile?: string;
  modules?: RobotModule[];
  label?: string;
  // screwdriver calibration + torque sensor
  companion?: boolean;
  targetTorqueNmm?: number;
  zEngage?: number;
  zSafe?: number;
};
export type VerifyMode = "agent" | "frames" | "off";

export type RobotConfig = {
  profile: string;
  modules?: RobotModule[];
  serial?: string;
  toolMode?: string;
  toolPin?: number;
  ePerTurn?: number;
  camera?: string;
  strictEncoder?: boolean;
  label?: string;
  // screwdriver calibration + torque (terminal blocks)
  zSafe?: number;
  zEngage?: number;
  maxPlunge?: number;
  targetTorqueNmm?: number;
  companion?: string;
  // Fuju / external-driver machine setup
  stepsPerMm?: { x?: number; y?: number; z?: number };
  envelope?: { Xmin: number; Xmax: number; Ymin: number; Ymax: number; Zmin: number; Zmax: number };
};
export type SenseReading = { currentmA?: number; forceG?: number; torqueNmm?: number; raw?: string };
export type RobotScrewResult = {
  ok: boolean;
  code?: string;
  error?: string;
  seated?: boolean;
  targetTorqueNmm?: number;
  measuredTorqueNmm?: number;
  finalZ?: number;
  steps?: number;
  position?: RobotPosition;
  frames?: RobotFrames;
  tookMs?: number;
};
export type ProfileOption = { kind: string; label: string; modules: RobotModule[]; desc: string };

// A klemens (terminal-block) layout to fasten — grid (jig in the work area) or
// linear (single rail indexes a strip).
export type ArrayParams = {
  name?: string;
  mode: "grid" | "linear";
  // grid
  cols?: number;
  rows?: number;
  pitchX?: number;
  pitchY?: number;
  originX?: number;
  originY?: number;
  serpentine?: boolean;
  // linear
  axis?: "X" | "Y";
  count?: number;
  pitch?: number;
  origin?: number;
  // common
  targetTorqueNmm?: number;
  home?: boolean;
  captureOrigin?: boolean;
};

// Printable klemens jig (matches the grid array). Yaver generates the design;
// you render + print it on your own printer.
export type JigParams = {
  cols?: number;
  rows?: number;
  pitchX?: number;
  pitchY?: number;
  klemensW?: number;
  klemensL?: number;
  pocketDepth?: number;
  wall?: number;
  plateH?: number;
  clearance?: number;
};

// A taught step — mirrors robot.Step.
export type RobotStep = {
  type: "home" | "move" | "jog" | "tool" | "dwell" | "screw" | "rotate";
  axis?: string;
  dist?: number;
  x?: number;
  y?: number;
  z?: number;
  feed?: number;
  on?: boolean;
  ms?: number;
  turns?: number;
  rpm?: number;
  ccw?: boolean;
  torque?: number;
  zEngage?: number;
  zSafe?: number;
  label?: string;
};
export type RobotProgram = { name: string; steps: RobotStep[]; createdAt?: number; updatedAt?: number };
export type RobotRunResult = {
  ok: boolean;
  program?: string;
  completed?: number;
  total?: number;
  error?: string;
  tookMs?: number;
  steps?: Array<{ index: number; ok: boolean; code?: string; error?: string }>;
};
// moduleEnabled: an older agent (pre-profiles) reports no `modules` — treat that
// as "everything on" so existing rigs keep all controls until they're upgraded.
export function moduleEnabled(status: RobotStatus | undefined, m: RobotModule): boolean {
  if (!status?.modules) return true;
  return status.modules.includes(m);
}

export async function getRobotDeviceId(): Promise<string> {
  return (await AsyncStorage.getItem(ROBOT_DEVICE_KEY)) || "";
}
export async function setRobotDeviceId(deviceId: string): Promise<void> {
  await AsyncStorage.setItem(ROBOT_DEVICE_KEY, deviceId.trim());
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
    // Reached the agent. ok → success; a JSON error (e.g. "different user") is a
    // real answer, not a transport failure — return it (don't keep probing).
    if (res.ok || data?.error) return data;
    return null;
  } catch {
    return null; // unreachable on this host — try the next
  } finally {
    clearTimeout(timer);
  }
}

// robotOps reaches the device's agent with the USER's bearer + machine:"local":
// LAN address(es) first, then a relay fallback. Unwraps the verb result (nested
// in `initial`).
async function robotOps<T = any>(target: RobotTarget | undefined, verb: string, payload: Record<string, unknown>, timeoutMs = 120000): Promise<T> {
  if (!target?.id) return { ok: false, error: "pick a robot device first" } as unknown as T;
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
  // Relay fallback (remote / off-LAN) — also user-bearer + machine:"local".
  const data = await quicClient.callOpsOnDevice(target.id, verb, payload, timeoutMs);
  if (data?.ok === false) return { ok: false, code: (data as any)?.code, error: data?.error } as unknown as T;
  return ((data as any)?.initial ?? data) as T;
}

export const robotClient = {
  status: (t: RobotTarget) => robotOps<RobotStatus>(t, "robot_status", {}, 20000),
  home: (t: RobotTarget, verify: VerifyMode, expectation?: string) =>
    robotOps<RobotMoveResponse>(t, "robot_home", { verify, expectation }),
  jog: (t: RobotTarget, axis: "X" | "Y" | "Z", dist: number, feed: number, verify: VerifyMode, expectation?: string) =>
    robotOps<RobotMoveResponse>(t, "robot_jog", { axis, dist, feed, verify, expectation }),
  move: (t: RobotTarget, target: { x?: number; y?: number; z?: number }, feed: number, verify: VerifyMode, expectation?: string) =>
    robotOps<RobotMoveResponse>(t, "robot_move", { ...target, feed, verify, expectation }),
  tool: (t: RobotTarget, on: boolean) => robotOps<RobotMoveResponse>(t, "robot_tool", { on }),
  verify: (t: RobotTarget, expectation: string) => robotOps<RobotMoveResponse>(t, "robot_verify", { expectation }),
  estop: (t: RobotTarget) => robotOps<{ ok: boolean; estopped?: boolean }>(t, "robot_estop", {}, 15000),
  reset: (t: RobotTarget) => robotOps<{ ok: boolean }>(t, "robot_reset", {}, 15000),
  snapshot: (t: RobotTarget) => robotOps<{ ok?: boolean; image?: string; error?: string }>(t, "robot_snapshot", {}, 30000),
  // Push a JPEG frame into the box's "external" camera buffer. Used when the box
  // is a phone capturing its OWN camera (no /dev/video0 on Android) — typically
  // targeted at 127.0.0.1 (the co-located agent). image = base64 or data: URL.
  cameraPush: (t: RobotTarget, image: string) =>
    robotOps<{ ok?: boolean; bytes?: number; ageMs?: number; error?: string }>(t, "robot_camera_push", { image }, 15000),
  // Ask the box's OWN vision model about the current frame (on-device brain).
  // For host-side reasoning instead, the desktop MCP tool `robot_camera` returns
  // the frame as a viewable image to your Claude Code / Codex.
  look: (t: RobotTarget, prompt?: string) =>
    robotOps<{ ok?: boolean; answer?: string; image?: string; visionError?: string; error?: string }>(t, "robot_look", { prompt }, 95000),

  // --- screwdriver motor / GPIO ---
  rotate: (t: RobotTarget, turns: number, rpm: number, ccw: boolean) =>
    robotOps<RobotMoveResponse>(t, "robot_screw_rotate", { turns, rpm, ccw }, 60000),
  gpio: (t: RobotTarget, pin: number, value: number) =>
    robotOps<RobotMoveResponse>(t, "robot_gpio", { pin, value }, 20000),
  gcode: (t: RobotTarget, line: string) => robotOps<RobotMoveResponse>(t, "robot_gcode", { line }, 30000),
  // machine power (M80/M81, needs PSU-control wiring) + release steppers (M84)
  power: (t: RobotTarget, on: boolean) => robotOps<RobotMoveResponse>(t, "robot_power", { on }, 15000),
  motorsOff: (t: RobotTarget) => robotOps<RobotMoveResponse>(t, "robot_motors_off", {}, 15000),

  // --- torque + drive-home (terminal blocks) ---
  torque: (t: RobotTarget) => robotOps<SenseReading>(t, "robot_torque", {}, 10000),
  screw: (t: RobotTarget, opts?: { x?: number; y?: number; targetTorqueNmm?: number; verify?: VerifyMode }) =>
    robotOps<RobotScrewResult>(t, "robot_screw", { ...(opts || {}) }, 120000),
  // düz (slotted) slot-find: spin while creeping Z down to catch the yuva, then
  // drive to torque. seekMm/seekFeed/seekDwellMs/pecks default server-side.
  screwHome: (t: RobotTarget, opts?: { x?: number; y?: number; targetTorqueNmm?: number; seekMm?: number; seekFeed?: number; seekDwellMs?: number; pecks?: number; verify?: VerifyMode }) =>
    robotOps<RobotScrewResult>(t, "robot_screw_home", { ...(opts || {}) }, 120000),

  // --- profiles / config (vault-backed) ---
  profiles: (t: RobotTarget) => robotOps<{ profiles: ProfileOption[] }>(t, "robot_profiles", {}, 15000),
  configGet: (t: RobotTarget) => robotOps<{ config: RobotConfig; modules: RobotModule[] }>(t, "robot_config_get", {}, 15000),
  configSet: (t: RobotTarget, config: RobotConfig) =>
    robotOps<{ ok?: boolean; config?: RobotConfig; modules?: RobotModule[]; error?: string }>(t, "robot_config_set", config as any, 15000),

  // --- teach-and-repeat ---
  programSave: (t: RobotTarget, program: RobotProgram) =>
    robotOps<{ ok?: boolean; saved?: string; steps?: number; error?: string }>(t, "robot_program_save", { program }, 15000),
  programList: (t: RobotTarget) => robotOps<{ programs: RobotProgram[] }>(t, "robot_program_list", {}, 15000),
  programGet: (t: RobotTarget, name: string) => robotOps<RobotProgram>(t, "robot_program_get", { name }, 15000),
  programDelete: (t: RobotTarget, name: string) =>
    robotOps<{ ok?: boolean; deleted?: string; error?: string }>(t, "robot_program_delete", { name }, 15000),
  programRun: (t: RobotTarget, name: string, verify: VerifyMode) =>
    robotOps<RobotRunResult>(t, "robot_program_run", { name, verify }, 600000),

  // klemens array → fastening program (grid jig or linear rail)
  arrayBuild: (t: RobotTarget, params: ArrayParams) =>
    robotOps<{ ok?: boolean; saved?: string; steps?: number; program?: RobotProgram; error?: string }>(t, "robot_array_build", params as any, 20000),
  // printable jig (OpenSCAD) matching the grid — render + print on your own printer
  jigScad: (t: RobotTarget, params: JigParams) =>
    robotOps<{ ok?: boolean; scad?: string; filename?: string; error?: string }>(t, "robot_jig_scad", params as any, 15000),

  // --- optional Talos backup (off unless configured on the edge) ---
  backup: (t: RobotTarget) => robotOps<{ ok?: boolean; backedUp?: boolean; programs?: number; error?: string; code?: string }>(t, "robot_backup", {}, 30000),
};
