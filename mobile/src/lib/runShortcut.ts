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
import { describeStep, type Shortcut, type ShortcutStep } from "./shortcuts";

export type StepPhase = "running" | "ok" | "fail";

export interface RunShortcutHooks {
  /** Connect this phone to a dev machine. Wired by the screen to
   *  DeviceContext.selectDevice (which handles transport + relay). */
  connectDevice: (deviceId: string) => Promise<void>;
  /** Bring the Projects tab forward so its openAppBus subscriber is
   *  mounted to receive the open request. Wired to expo-router. */
  openProjectsTab: () => void;
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
      const ok = await clientFor(step.deviceId).reloadDevServer({ mode: step.mode || "bundle" });
      if (!ok) throw new Error("dev server unreachable — start one first");
      return;
    }
    default:
      throw new Error(`unknown step "${(step as ShortcutStep).kind}"`);
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
