// meshControl.ts — drive a *remote* box's Yaver Mesh state from the phone.
//
// The mesh home (app/(tabs)/mesh.tsx) lists every machine in the account and
// lets the user flip mesh on/off per box (and "enable on all").
//
//   POST /mesh/up        — ensure keypair, register control plane, bring up the
//                          data plane. Body: none. (desktop/agent/mesh_http.go)
//   POST /mesh/down      — tear down + mark offline.
//   GET  /mesh/status    — { enabled, meshIPv4 }.
//   POST /agent/self-heal — { Apply, AllowSelfUpdate, Quiet } downloads+stages
//                          the latest signed binary.
//
// Transport: these requests go through the box's LIVE QuicClient
// (connectionManager.clientFor) whenever one is connected — so they ride the
// transport connect() already resolved (direct LAN first, relay last). The
// old path always built a relay-only URL, which 502'd ("device not connected
// to relay") for a box reachable on the same Wi-Fi but not parked on the relay.
// When no client is connected we fall back to the relay/host URL.
//
// Enabling bundles a best-effort agent self-update FIRST (stage only — the
// running process keeps the old code until the next `yaver serve` restart, so
// we never risk killing a remote box mid-flow), then brings mesh up.

import { agentFetch, type AgentDeviceRef } from "./agentRequest";

export type MeshDeviceStatus = {
  enabled: boolean;
  meshIPv4?: string;
};

export type MeshEnableResult = {
  meshIPv4?: string;
  /** Non-fatal: control plane joined but the data plane couldn't come up
   *  (e.g. "elevated privilege required" on a desktop without sudo). */
  dataPlaneWarning?: string;
  /** Latest release the self-heal step staged, when it pulled a newer binary. */
  stagedVersion?: string;
};

/** The two back-to-back remote calls an enable runs through. Surfaced to the UI
 *  so the user sees "what's happening" instead of a silent ~55s spinner. */
export type MeshEnablePhase = "updating" | "bringing-up";

/** User-facing label for a phase (and the generic fallback when none is set). */
export function meshEnablePhaseLabel(phase?: MeshEnablePhase): string {
  switch (phase) {
    case "updating":
      return "Updating agent…";
    case "bringing-up":
      return "Bringing mesh up…";
    default:
      return "Enabling…";
  }
}

type DeviceRef = AgentDeviceRef;

const STATUS_TIMEOUT_MS = 5000;
const SELF_HEAL_TIMEOUT_MS = 30000;
const MESH_UP_TIMEOUT_MS = 25000;

/** Probe a box's live mesh state. Returns null when unreachable so the caller
 *  can fall back to the Convex control-plane view (mesh.peers). */
export async function meshStatusForDevice(
  device: DeviceRef,
  token: string | null,
): Promise<MeshDeviceStatus | null> {
  try {
    const res = await agentFetch(device, token, "/mesh/status", { method: "GET" }, STATUS_TIMEOUT_MS);
    if (!res.ok) return null;
    const j = await res.json();
    return {
      enabled: j?.enabled === true,
      meshIPv4: typeof j?.meshIPv4 === "string" && j.meshIPv4 ? j.meshIPv4 : undefined,
    };
  } catch {
    return null;
  }
}

/** Stage the latest agent binary on the box (best-effort), then bring mesh up.
 *  Throws "unreachable" when there's no route to the agent. */
export async function enableMeshOnDevice(
  device: DeviceRef,
  token: string | null,
  onPhase?: (phase: MeshEnablePhase) => void,
): Promise<MeshEnableResult> {
  // 1. Best-effort agent self-update — stage only, no restart. Never blocks the
  //    mesh bring-up: a failed/slow update still leaves mesh enable to proceed.
  onPhase?.("updating");
  const stagedVersion = await stageAgentUpdate(device, token);

  // 2. Bring mesh up. The agent ensures its keypair, registers with the control
  //    plane, and starts the data plane. A dataPlaneWarning is non-fatal.
  onPhase?.("bringing-up");
  const res = await agentFetch(
    device,
    token,
    "/mesh/up",
    { method: "POST", headers: { "Content-Type": "application/json" }, body: "{}" },
    MESH_UP_TIMEOUT_MS,
  );
  if (!res.ok) {
    const body = await res.text().catch(() => "");
    throw new Error(`mesh up: HTTP ${res.status}${body ? ` — ${body.slice(0, 120)}` : ""}`);
  }
  const j = await res.json().catch(() => ({}));
  return {
    meshIPv4: typeof j?.meshIPv4 === "string" ? j.meshIPv4 : undefined,
    dataPlaneWarning: typeof j?.dataPlaneWarning === "string" ? j.dataPlaneWarning : undefined,
    stagedVersion,
  };
}

/** Tear mesh down on a box (keeps the vault keypair so re-enabling reuses the
 *  same overlay IP). */
export async function disableMeshOnDevice(
  device: DeviceRef,
  token: string | null,
): Promise<void> {
  const res = await agentFetch(
    device,
    token,
    "/mesh/down",
    { method: "POST", headers: { "Content-Type": "application/json" }, body: "{}" },
    MESH_UP_TIMEOUT_MS,
  );
  if (!res.ok) throw new Error(`mesh down: HTTP ${res.status}`);
}

/** POST /agent/self-heal with AllowSelfUpdate. Returns the staged release tag
 *  when a newer binary was actually pulled, else undefined. Swallows every
 *  error — a box that can't update still gets mesh enabled. */
async function stageAgentUpdate(device: DeviceRef, token: string | null): Promise<string | undefined> {
  try {
    const res = await agentFetch(
      device,
      token,
      "/agent/self-heal",
      {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        // Field names mirror Go's SelfHealOptions (Apply/AllowSelfUpdate/Quiet).
        body: JSON.stringify({ Apply: true, AllowSelfUpdate: true, Quiet: true }),
      },
      SELF_HEAL_TIMEOUT_MS,
    );
    if (!res.ok) return undefined;
    const rep = await res.json().catch(() => ({}));
    // Report "staged" only when self-heal both saw a newer release and applied
    // something — otherwise the box was already current.
    const pulled = rep?.needsSelfPull === true && Array.isArray(rep?.applied) && rep.applied.length > 0;
    return pulled && typeof rep?.latestRelease === "string" ? rep.latestRelease : undefined;
  } catch {
    return undefined;
  }
}
