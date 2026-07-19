// managedCloudFlow — finish setup for an already-purchased Yaver managed-cloud
// box from the mobile app.
//
// Store-policy boundary: mobile must not create, display, or open a checkout
// URL. Subscription checkout/cancellation stay on web. This helper sequences
// only the post-purchase setup verbs that are safe in the App Store / Play Store
// app: cloud_status, runner_auth_mirror, and runner_auth_setup.
//
// Step-by-step:
//
//   1. List currently-known managed-cloud boxes (cloud_status).
//   2. Pick the user's existing box, or report that no box is active yet.
//   3. Wait until the box's agentStatus flips to "online" (the
//      Convex device row gets registered by the provisioner script
//      installing yaver-cli + running `yaver serve`).
//   4. Mirror OAuth runner credentials for Claude/Codex, or bootstrap
//      OpenCode/GLM target-locally so the box is immediately selectable.
//   5. Done. Caller can switch the active device to the cloud box.
//
// Each step emits a progress event so the UI can render a checklist
// with spinners / checks. Any step's failure short-circuits the flow
// and surfaces an actionable error.

import { callMcpDirect } from "./yaverMcpDirect";

export type FlowStep =
  | "find_box"
  | "wait_for_box"
  | "wait_for_agent"
  | "mirror_runner"
  | "done";

export interface FlowProgress {
  step: FlowStep;
  message: string;
  /** Filled once we know which box is being configured. */
  newBox?: ManagedCloudBox;
  /** True when this is the final tick of a successful flow. */
  done?: boolean;
}

export interface ManagedCloudBox {
  /** Convex device id (also Yaver's deviceId). */
  deviceId: string;
  /** Display label — alias or hostname. */
  label: string;
  /** Provider id (e.g. Hetzner serverId). */
  cloudResourceId?: string;
  /** "provisioning" | "online" | "offline" | … */
  status: string;
}

interface OpsEnvelope<T> {
  ok?: boolean;
  code?: string;
  error?: string;
  initial?: T;
}

interface CloudStatusPayload {
  machines?: Array<{
    deviceId?: string;
    cloudResourceId?: string;
    alias?: string;
    name?: string;
    status?: string;
    agentStatus?: string;
  }>;
}

export interface ManagedCloudFlowOpts {
  /** Runner to configure on the new box. */
  runner?: "claude" | "codex" | "opencode";
  /** Max minutes to wait before giving up on the box coming online. */
  maxWaitMinutes?: number;
  /** Called for every step transition. */
  onProgress: (p: FlowProgress) => void;
  signal?: AbortSignal;
}

/**
 * Run the post-purchase setup sequence end-to-end. Returns the deviceId of the
 * configured box on success so the caller can re-target the device picker.
 */
export async function runManagedCloudFlow(opts: ManagedCloudFlowOpts): Promise<string> {
  const {
    runner = "opencode",
    maxWaitMinutes = 15,
    onProgress,
    signal,
  } = opts;

  abortGuard(signal);
  onProgress({ step: "find_box", message: "checking this account for managed cloud machines..." });
  const deadline = Date.now() + maxWaitMinutes * 60 * 1000;
  let newBox = pickManagedBox(await listManagedBoxes(signal));
  if (!newBox) {
    onProgress({ step: "wait_for_box", message: "waiting for an existing managed cloud machine to appear..." });
  }
  while (Date.now() < deadline) {
    if (newBox) break;
    abortGuard(signal);
    await sleep(6000, signal);
    newBox = pickManagedBox(await listManagedBoxes(signal));
  }
  if (!newBox) {
    throw new Error("no managed cloud machine is active for this account yet");
  }

  // Wait for its agent to come online.
  onProgress({
    step: "wait_for_agent",
    message: `found ${newBox.label}; waiting for the agent to come online...`,
    newBox,
  });
  while (Date.now() < deadline) {
    if (newBox.status === "online") break;
    abortGuard(signal);
    await sleep(6000, signal);
    const now = await listManagedBoxes(signal);
    const updated = now.find((b) => b.deviceId === newBox!.deviceId);
    if (updated && updated.status === "online") {
      newBox = updated;
      break;
    }
  }
  if (!newBox || newBox.status !== "online") {
    throw new Error("timed out waiting for agent to come online on the new box");
  }

  // Mirror OAuth runners; configure BYOK/config runners on the target box.
  onProgress({
    step: "mirror_runner",
    message: `configuring ${runner} on ${newBox.label}...`,
    newBox,
  });
  if (runner === "claude" || runner === "codex") {
    const mirror = await callMcpDirect<{ ok?: boolean; writtenTo?: string; error?: string }>(
      "runner_auth_mirror",
      { runner, targetDeviceId: newBox.deviceId },
      signal,
    );
    if (!mirror.ok || !mirror.result?.ok) {
      // Mirror failure is recoverable — the user can re-run from the
      // device picker. Surface the error but don't lose the box.
      throw new Error(`mirror failed (box is provisioned, just re-run mirror): ${mirror.result?.error || mirror.error}`);
    }
  } else {
    const setup = await callMcpDirect<{ ok?: boolean; error?: string }>(
      "runner_auth_setup",
      {
        device_id: newBox.deviceId,
        runner,
        install_if_missing: true,
        allow_install_only: true,
        setup_mcp: true,
      },
      signal,
    );
    if (!setup.ok || !setup.result?.ok) {
      throw new Error(`runner setup failed (box is provisioned, just re-run setup): ${setup.result?.error || setup.error}`);
    }
  }

  onProgress({
    step: "done",
    message: `ready - ${newBox.label} is configured for ${runner}. switch devices to start coding.`,
    newBox,
    done: true,
  });
  return newBox.deviceId;
}

function pickManagedBox(boxes: ManagedCloudBox[]): ManagedCloudBox | null {
  return boxes.find((b) => b.status === "online") ?? boxes[0] ?? null;
}

async function listManagedBoxes(signal?: AbortSignal): Promise<ManagedCloudBox[]> {
  const res = await callMcpDirect<OpsEnvelope<CloudStatusPayload>>(
    "ops",
    { verb: "cloud_status", machine: "local", payload: {} },
    signal,
  );
  if (!res.ok || !res.result || !res.result.ok) return [];
  const machines = res.result.initial?.machines ?? [];
  return machines.map((m) => ({
    deviceId: m.deviceId ?? "",
    label: m.alias ?? m.name ?? m.deviceId ?? "(unnamed)",
    cloudResourceId: m.cloudResourceId,
    status: m.agentStatus ?? m.status ?? "unknown",
  })).filter((b) => b.deviceId);
}

function sleep(ms: number, signal?: AbortSignal): Promise<void> {
  return new Promise((resolve, reject) => {
    const id = setTimeout(resolve, ms);
    if (signal) {
      signal.addEventListener("abort", () => {
        clearTimeout(id);
        reject(new Error("aborted"));
      }, { once: true });
    }
  });
}

function abortGuard(signal?: AbortSignal): void {
  if (signal?.aborted) throw new Error("aborted");
}
