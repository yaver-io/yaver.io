// Client-side shortcut chain runner. Executes a shortcut's steps in
// order on the phone, reusing the same primitives the Tasks/Reload/
// Projects tabs already use:
//   select-device  → DeviceContext.selectDevice (connect + focus)   [injected]
//   open-project   → Projects tab + openAppBus (existing load flow)
//   start-dev      → QuicClient.switchProject(slug, startDev=true)
//   hermes-reload  → QuicClient.reloadDevServer({mode})
//
// Device-dependent steps route through connectionManager.clientFor(id)
// so a multi-device chain hits the box the step names — never whichever
// client happened to be focused. Stops on the first failing step with a
// visible error (no silent retry), per the project's failure policy.

import { connectionManager } from "./connectionManager";
import { quicClient } from "./quic";
import { openAppBus } from "./openAppBus";
import { openRobotBus } from "./openRobotBus";
import { describeStep, type Shortcut, type ShortcutStep } from "./shortcuts";
import { robotClient, setRobotDeviceId, type RobotTarget, type VerifyMode } from "./robotClient";

export type StepPhase = "running" | "ok" | "fail";

export interface RunShortcutHooks {
  /** Connect this phone to a dev machine. Wired by the screen to
   *  DeviceContext.selectDevice (which handles transport + relay). */
  connectDevice: (deviceId: string) => Promise<void>;
  /** Bring the Projects tab forward so its openAppBus subscriber is
   *  mounted to receive the open request. Wired to expo-router. */
  openProjectsTab: () => void;
  /** Bring the Robot tab forward after selecting a robot device. */
  openRobotTab?: () => void;
  /** Preset the target device's agent runner + model when a step carries
   *  one. Wired to DeviceContext.setPrimaryRunnerForDevice; optional since
   *  runner can be "off". */
  setAgent?: (deviceId: string, runner: string, model?: string) => Promise<void> | void;
  /** Per-step progress for the running UI. */
  onProgress?: (stepIndex: number, phase: StepPhase, message: string) => void;
}

function clientFor(deviceId?: string) {
  return deviceId ? connectionManager.clientFor(deviceId) : quicClient;
}

async function runStep(step: ShortcutStep, hooks: RunShortcutHooks): Promise<void> {
  switch (step.kind) {
    case "select-device":
      if (!step.deviceId) throw new Error("no device set on this step");
      await hooks.connectDevice(step.deviceId);
      return;
    case "open-project":
      if (!step.projectSlug) throw new Error("no project set on this step");
      hooks.openProjectsTab();
      openAppBus.publish(step.projectSlug);
      return;
    case "start-dev":
      if (!step.projectSlug) throw new Error("no project set on this step");
      await clientFor(step.deviceId).switchProject(step.projectSlug, true);
      return;
    case "hermes-reload": {
      // Preset the device's agent + model first (when the shortcut carries
      // one and it isn't "off") so a task launched after the reload uses
      // the shortcut's chosen runner. Hermes mode is always the full bundle.
      if (step.runner && step.deviceId && hooks.setAgent) {
        await hooks.setAgent(step.deviceId, step.runner, step.model);
      }
      const ok = await clientFor(step.deviceId).reloadDevServer({ mode: step.mode || "bundle" });
      if (!ok) throw new Error("dev server unreachable — start one first");
      return;
    }
    case "open-robot":
      if (!step.deviceId) throw new Error("no robot device set on this step");
      await setRobotDeviceId(step.deviceId);
      hooks.openRobotTab?.();
      openRobotBus.publish(step.deviceId);
      return;
    case "robot-action":
      await runRobotStep(step);
      return;
    default:
      throw new Error(`unknown step "${(step as ShortcutStep).kind}"`);
  }
}

function robotTarget(step: ShortcutStep): RobotTarget {
  if (!step.deviceId) throw new Error("no robot device set on this step");
  return { id: step.deviceId };
}

function verifyMode(step: ShortcutStep): VerifyMode {
  return step.verify || "frames";
}

function numberOr(value: number | undefined, fallback: number): number {
  return typeof value === "number" && Number.isFinite(value) ? value : fallback;
}

async function runRobotStep(step: ShortcutStep): Promise<void> {
  const target = robotTarget(step);
  const action = step.robotAction || "status";
  let res: any;

  switch (action) {
    case "status":
      res = await robotClient.status(target);
      break;
    case "home":
      res = await robotClient.home(target, verifyMode(step), "robot homed");
      break;
    case "jog":
      res = await robotClient.jog(
        target,
        step.axis || "X",
        numberOr(step.distanceMm, 10),
        numberOr(step.feed, step.axis === "Z" ? 600 : 3000),
        verifyMode(step),
        `robot jogged ${step.axis || "X"} ${numberOr(step.distanceMm, 10)}mm`,
      );
      break;
    case "tool":
      res = await robotClient.tool(target, step.toolOn !== false);
      break;
    case "screw":
      res = await robotClient.screw(target, {
        ...(typeof step.x === "number" ? { x: step.x } : {}),
        ...(typeof step.y === "number" ? { y: step.y } : {}),
        ...(typeof step.targetTorqueNmm === "number" ? { targetTorqueNmm: step.targetTorqueNmm } : {}),
        verify: verifyMode(step),
      });
      break;
    case "program-run":
      if (!step.programName?.trim()) throw new Error("no robot program set on this step");
      res = await robotClient.programRun(target, step.programName.trim(), verifyMode(step));
      break;
    case "estop":
      res = await robotClient.estop(target);
      break;
    case "reset":
      res = await robotClient.reset(target);
      break;
    default:
      throw new Error(`unknown robot action "${action}"`);
  }

  if (res?.ok === false) {
    throw new Error(res.error || res.code || `${action} failed`);
  }
}

/** Run every step in order. Resolves when the whole chain succeeds;
 *  rejects (after marking the failing step) on the first error. */
export async function runShortcut(shortcut: Shortcut, hooks: RunShortcutHooks): Promise<void> {
  for (let i = 0; i < shortcut.steps.length; i++) {
    const step = shortcut.steps[i];
    const label = describeStep(step);
    hooks.onProgress?.(i, "running", label);
    try {
      await runStep(step, hooks);
      hooks.onProgress?.(i, "ok", label);
    } catch (e) {
      const msg = e instanceof Error ? e.message : String(e);
      hooks.onProgress?.(i, "fail", `${label}: ${msg}`);
      throw new Error(`Step ${i + 1} — ${label}: ${msg}`);
    }
  }
}
