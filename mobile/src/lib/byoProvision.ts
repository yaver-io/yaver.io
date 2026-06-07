// byoProvision.ts — spin up a vibe-ready box on the user's OWN Hetzner from
// the phone, with NO paired agent. The privacy-preserving split:
//
//   1. Convex mints the box's device credential + self-bootstrapping
//      cloud-init (POST /byo/provision-init) — NO Hetzner token involved.
//   2. The phone creates the Hetzner server itself with the user's token
//      (kept in the device keychain), baking in that cloud-init.
//   3. The phone reports the new server id/ip back (POST /byo/provision-complete).
//   4. The box self-installs yaver, auths as the user (baked session token),
//      registers as a device, and mirrors the runner → ready to vibe code in
//      a few minutes.
//
// The Hetzner token NEVER leaves the phone; Convex only does the device
// bookkeeping it's allowed to do (a session token hash), never the cloud
// provider secret.

import { getConvexSiteUrl } from "./auth";
import { HetznerClient, locationFor, serverTypeFor, type Plan, type Region } from "./hcloud";

export interface ByoProvisionProgress {
  step: "mint" | "create" | "complete" | "done";
  message: string;
  deviceId?: string;
}

export interface ByoProvisionResult {
  machineId: string;
  deviceId: string;
  serverId: string;
  ip: string;
}

interface MintResponse {
  ok?: boolean;
  error?: string;
  machineId?: string;
  deviceId?: string;
  serverName?: string;
  userData?: string;
}

export interface ProvisionByoOpts {
  /** Yaver user session bearer (for Convex). */
  token: string;
  /** BYO Hetzner API token (from the device keychain). */
  hetznerToken: string;
  machineType: "cpu" | "gpu";
  region: Region;
  plan: Plan;
  onProgress: (p: ByoProvisionProgress) => void;
  signal?: AbortSignal;
}

export async function provisionByoBox(opts: ProvisionByoOpts): Promise<ByoProvisionResult> {
  const { token, hetznerToken, machineType, region, plan, onProgress, signal } = opts;

  // 1. Mint the bootstrap (Convex). No Hetzner token sent.
  onProgress({ step: "mint", message: "preparing your box credentials…" });
  const mintRes = await fetch(`${getConvexSiteUrl()}/byo/provision-init`, {
    method: "POST",
    headers: { Authorization: `Bearer ${token}`, "Content-Type": "application/json" },
    body: JSON.stringify({ machineType, region }),
    signal,
  });
  const mint = (await mintRes.json().catch(() => ({}))) as MintResponse;
  if (!mintRes.ok || !mint.ok || !mint.machineId || !mint.userData || !mint.serverName) {
    throw new Error(mint.error || `provision-init failed (HTTP ${mintRes.status})`);
  }

  // 2. Create the Hetzner server directly from the phone, baking in cloud-init.
  onProgress({ step: "create", message: "creating the server on your Hetzner account…", deviceId: mint.deviceId });
  const client = new HetznerClient(hetznerToken);
  const created = await client.createServer(
    {
      name: mint.serverName,
      serverType: serverTypeFor(plan, region),
      location: locationFor(region),
      image: "ubuntu-24.04",
      userData: mint.userData,
      labels: { service: "yaver-byo", managed: "false" },
    },
    signal,
  );

  // 3. Report the id/ip so the row can manage it later. Best-effort: the box
  //    also self-registers, so a failure here doesn't lose the box.
  onProgress({ step: "complete", message: "registering the box…", deviceId: mint.deviceId });
  await fetch(`${getConvexSiteUrl()}/byo/provision-complete`, {
    method: "POST",
    headers: { Authorization: `Bearer ${token}`, "Content-Type": "application/json" },
    body: JSON.stringify({ machineId: mint.machineId, hetznerServerId: created.serverId, serverIp: created.ip }),
    signal,
  }).catch(() => {});

  onProgress({
    step: "done",
    message: `box created (${created.ip}). It self-installs yaver + signs in over ~3-5 min, then shows up in your devices.`,
    deviceId: mint.deviceId,
  });
  return { machineId: mint.machineId, deviceId: mint.deviceId ?? "", serverId: created.serverId, ip: created.ip };
}
