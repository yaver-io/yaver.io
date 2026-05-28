// managedCloudFlow — orchestrate the "buy a Yaver managed-cloud box,
// open it, mirror my runner creds" multi-step flow from the mobile app.
//
// Glasses users without a desktop have no other way to reach the
// backend pieces of this — the existing ops verbs all exist
// (cloud_checkout, cloud_status, runner_auth_mirror) but spread across
// surfaces. This helper sequences them so a single button-tap on
// glasses fires the whole thing.
//
// Step-by-step:
//
//   1. List currently-known managed-cloud boxes (cloud_status).
//   2. Call ops cloud_checkout → returns a LemonSqueezy URL. The
//      caller opens it via Linking.openURL; the user pays in their
//      iPhone's Safari (XREAL mirrors the screen so they SEE the
//      payment form in the glasses).
//   3. Poll cloud_status every 6 s. When a new machineId appears
//      that wasn't in step 1, that's the new box.
//   4. Wait until the box's agentStatus flips to "online" (the
//      Convex device row gets registered by the provisioner script
//      installing yaver-cli + running `yaver serve`).
//   5. Call runner_auth_mirror with sourceDeviceId=THIS device,
//      targetDeviceId=the new box. The agent on the new box receives
//      the credentials.json byte-for-byte and the user has Claude
//      Code / Codex ready to go.
//   6. Done. Caller can switch the active device to the new box.
//
// Each step emits a progress event so the UI can render a checklist
// with spinners / checks. Any step's failure short-circuits the flow
// and surfaces an actionable error.

import { callMcpDirect } from "./yaverMcpDirect";

export type FlowStep =
  | "checkout"
  | "wait_for_box"
  | "wait_for_agent"
  | "mirror_runner"
  | "done";

export interface FlowProgress {
  step: FlowStep;
  message: string;
  /** Set when step=checkout — the URL the UI should open. */
  checkoutUrl?: string;
  /** Filled once we know which box is the new one. */
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

interface CloudCheckoutPayload {
  url?: string;
  checkoutUrl?: string;
}

export interface ManagedCloudFlowOpts {
  /** Machine type to buy. "cpu" (RN/Hermes + web + deploy) or "gpu". */
  machineType?: "cpu" | "gpu";
  /** Region — "eu" (default) or "us". */
  region?: "eu" | "us";
  /** Runner whose credentials to mirror to the new box. */
  runner?: "claude" | "codex";
  /** Max minutes to wait before giving up on the box appearing. */
  maxWaitMinutes?: number;
  /** Called for every step transition. */
  onProgress: (p: FlowProgress) => void;
  signal?: AbortSignal;
}

/**
 * Run the buy → provision → mirror sequence end-to-end. Returns the
 * deviceId of the new box on success so the caller can re-target the
 * device picker.
 */
export async function runManagedCloudFlow(opts: ManagedCloudFlowOpts): Promise<string> {
  const {
    machineType = "cpu",
    region = "eu",
    runner = "claude",
    maxWaitMinutes = 15,
    onProgress,
    signal,
  } = opts;

  abortGuard(signal);
  // 1. Snapshot the existing box set so we can identify the new one.
  const before = await listManagedBoxes(signal);
  const beforeIds = new Set(before.map((b) => b.deviceId));

  // 2. Get a checkout URL from Convex (via the ops grand-tool).
  onProgress({ step: "checkout", message: "asking Convex for a checkout URL…" });
  abortGuard(signal);
  const checkoutRes = await callMcpDirect<OpsEnvelope<CloudCheckoutPayload>>(
    "ops",
    { verb: "cloud_checkout", machine: "local", payload: { machineType, region } },
    signal,
  );
  if (!checkoutRes.ok || !checkoutRes.result) {
    throw new Error(`checkout failed: ${checkoutRes.error ?? "unknown"}`);
  }
  if (!checkoutRes.result.ok) {
    throw new Error(`checkout failed: ${checkoutRes.result.error ?? checkoutRes.result.code ?? "unknown"}`);
  }
  const payload = checkoutRes.result.initial ?? {};
  const url = payload.url ?? payload.checkoutUrl;
  if (!url) throw new Error("checkout response missing url field");
  onProgress({ step: "checkout", message: "open this URL to complete payment", checkoutUrl: url });

  // 3. Poll cloud_status until a new box appears.
  onProgress({ step: "wait_for_box", message: "waiting for box to provision (typically 2-4 min)…" });
  const deadline = Date.now() + maxWaitMinutes * 60 * 1000;
  let newBox: ManagedCloudBox | null = null;
  while (Date.now() < deadline) {
    abortGuard(signal);
    await sleep(6000, signal);
    const now = await listManagedBoxes(signal);
    const fresh = now.find((b) => !beforeIds.has(b.deviceId));
    if (fresh) { newBox = fresh; break; }
  }
  if (!newBox) throw new Error("timed out waiting for the new box to appear in cloud_status");

  // 4. Wait for its agent to come online.
  onProgress({
    step: "wait_for_agent",
    message: `box ${newBox.label} provisioned; waiting for the agent to come online…`,
    newBox,
  });
  while (Date.now() < deadline) {
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

  // 5. Mirror runner creds to the new box.
  onProgress({
    step: "mirror_runner",
    message: `pushing ${runner} subscription token to ${newBox.label}…`,
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

  // 6. Done.
  onProgress({
    step: "done",
    message: `ready — ${newBox.label} is signed in with your ${runner} token. switch devices to start coding.`,
    newBox,
    done: true,
  });
  return newBox.deviceId;
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
