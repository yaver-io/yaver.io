// managedCloudFlow — finish setup for an already-purchased Yaver managed-cloud
// box from the mobile app.
//
// Store-policy boundary: mobile must not create, display, or open a checkout
// URL. Purchase/top-up flows stay on web and CLI. This helper sequences only
// the post-purchase setup verbs that are safe in the App Store / Play Store
// app: cloud_status and runner_auth_mirror.
//
// Step-by-step:
//
//   1. List currently-known managed-cloud boxes (cloud_status).
//   2. Pick the user's existing box, or report that no box is active yet.
//   3. Wait until the box's agentStatus flips to "online" (the
//      Convex device row gets registered by the provisioner script
//      installing yaver-cli + running `yaver serve`).
//   4. Call runner_auth_mirror with sourceDeviceId=THIS device,
//      targetDeviceId=the new box. The agent on the new box receives
//      the credentials.json byte-for-byte and the user has Claude
//      Code / Codex ready to go.
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
  /** Runner whose credentials to mirror to the new box. */
  runner?: "claude" | "codex";
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
    runner = "claude",
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

  // Mirror runner creds to the new box.
  onProgress({
    step: "mirror_runner",
    message: `pushing ${runner} runner token to ${newBox.label}...`,
    newBox,
  });
  const mirror = await callMcpDirect<{ ok?: boolean; writtenTo?: string }>(
    "runner_auth_mirror",
    { runner, targetDeviceId: newBox.deviceId },
    signal,
  );
  if (!mirror.ok) {
    // Mirror failure is recoverable — the user can re-run from the
    // device picker. Surface the error but don't lose the box.
    throw new Error(`mirror failed (box is provisioned, just re-run mirror): ${mirror.error}`);
  }

  onProgress({
    step: "done",
    message: `ready - ${newBox.label} is signed in with your ${runner} token. switch devices to start coding.`,
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
