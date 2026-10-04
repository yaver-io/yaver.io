/**
 * Minimal typed Hetzner Cloud client for native Yaver surfaces.
 *
 * The caller supplies the credential for the duration of one request. This
 * module never persists it, logs it, places it in a URL, or returns it. Native
 * storage is handled by hetznerDirect.ts; Convex and the Yaver relay are not in
 * this data path.
 */

const HCLOUD_API = "https://api.hetzner.cloud/v1";

export type HetznerServer = {
  id: number;
  name: string;
  status: string;
  ip: string | null;
  type: string | null;
  location: string | null;
  created: string | null;
};

export type HetznerPowerTarget = "running" | "off";

export type HetznerActionLog = {
  id: number;
  command: string;
  status: string;
  started: string | null;
  finished: string | null;
  progress: number | null;
  errorCode: string | null;
};

type FetchLike = (input: string, init?: RequestInit) => Promise<Response>;
type WaitLike = (milliseconds: number) => Promise<void>;

export class HetznerDirectError extends Error {
  readonly status: number;
  readonly code: string;

  constructor(status: number, code: string) {
    super(status === 401 || status === 403
      ? "Hetzner rejected this API token. Check its permissions and try again."
      : `Hetzner request failed (${status}${code ? `, ${code}` : ""}).`);
    this.name = "HetznerDirectError";
    this.status = status;
    this.code = code;
  }
}

async function request(
  token: string,
  path: string,
  init: RequestInit = {},
  fetchImpl: FetchLike = fetch,
): Promise<any> {
  const credential = token.trim();
  if (!credential) throw new HetznerDirectError(401, "missing_token");
  const response = await fetchImpl(`${HCLOUD_API}${path}`, {
    ...init,
    headers: {
      Accept: "application/json",
      Authorization: `Bearer ${credential}`,
      ...(init.body ? { "Content-Type": "application/json" } : {}),
      ...(init.headers || {}),
    },
  });
  const body = await response.json().catch(() => ({}));
  if (!response.ok) {
    // Return only Hetzner's stable error code. Provider messages may contain
    // user-supplied resource names, so they are intentionally not surfaced.
    throw new HetznerDirectError(response.status, String(body?.error?.code || "unknown"));
  }
  return body;
}

function normalizeServer(server: any): HetznerServer {
  return {
    id: Number(server?.id),
    name: String(server?.name || server?.id || "server"),
    status: String(server?.status || "unknown"),
    ip: server?.public_net?.ipv4?.ip ? String(server.public_net.ipv4.ip) : null,
    type: server?.server_type?.name ? String(server.server_type.name) : null,
    location: server?.datacenter?.location?.name ? String(server.datacenter.location.name) : null,
    created: server?.created ? String(server.created) : null,
  };
}

export async function getHetznerServer(
  token: string,
  serverId: number,
  fetchImpl: FetchLike = fetch,
): Promise<HetznerServer> {
  const body = await request(token, `/servers/${encodeURIComponent(String(serverId))}`, {}, fetchImpl);
  const server = normalizeServer(body?.server);
  if (!Number.isSafeInteger(server.id) || server.id <= 0) {
    throw new HetznerDirectError(502, "invalid_server");
  }
  return server;
}

export async function validateHetznerToken(token: string, fetchImpl: FetchLike = fetch): Promise<void> {
  await request(token, "/servers?per_page=1", {}, fetchImpl);
}

export async function listHetznerServers(token: string, fetchImpl: FetchLike = fetch): Promise<HetznerServer[]> {
  const body = await request(token, "/servers?per_page=50", {}, fetchImpl);
  return Array.isArray(body?.servers) ? body.servers.map(normalizeServer) : [];
}

export async function listHetznerServerActions(
  token: string,
  serverId: number,
  fetchImpl: FetchLike = fetch,
): Promise<HetznerActionLog[]> {
  const body = await request(
    token,
    `/servers/${encodeURIComponent(String(serverId))}/actions?sort=started:desc&per_page=20`,
    {},
    fetchImpl,
  );
  return Array.isArray(body?.actions) ? body.actions.map((action: any) => ({
    id: Number(action?.id),
    command: String(action?.command || "unknown"),
    status: String(action?.status || "unknown"),
    started: action?.started ? String(action.started) : null,
    finished: action?.finished ? String(action.finished) : null,
    progress: Number.isFinite(Number(action?.progress)) ? Number(action.progress) : null,
    // Provider error messages can contain user-controlled resource data. Only
    // the stable code is safe to expose in the local UI.
    errorCode: action?.error?.code ? String(action.error.code) : null,
  })).filter((action: HetznerActionLog) => Number.isSafeInteger(action.id) && action.id > 0) : [];
}

export async function powerOnHetznerServer(
  token: string,
  serverId: number,
  fetchImpl: FetchLike = fetch,
  wait?: WaitLike,
): Promise<HetznerServer> {
  const before = await getHetznerServer(token, serverId, fetchImpl);
  if (before.status === "running") return before;
  const body = await request(token, `/servers/${encodeURIComponent(String(serverId))}/actions/poweron`, { method: "POST" }, fetchImpl);
  await waitForAction(token, body?.action, fetchImpl, wait);
  return waitForServerState(token, before, "running", fetchImpl, wait);
}

export async function shutdownHetznerServer(
  token: string,
  serverId: number,
  fetchImpl: FetchLike = fetch,
  wait?: WaitLike,
): Promise<HetznerServer> {
  const before = await getHetznerServer(token, serverId, fetchImpl);
  if (before.status === "off") return before;
  const body = await request(token, `/servers/${encodeURIComponent(String(serverId))}/actions/shutdown`, { method: "POST" }, fetchImpl);
  await waitForAction(token, body?.action, fetchImpl, wait);
  return waitForServerState(token, before, "off", fetchImpl, wait);
}

export async function renameHetznerServer(
  token: string,
  serverId: number,
  name: string,
  fetchImpl: FetchLike = fetch,
): Promise<HetznerServer> {
  const nextName = name.trim();
  if (!nextName || nextName.length > 64 || /[\x00-\x20\x7f]/.test(nextName)) {
    throw new HetznerDirectError(400, "invalid_server_name");
  }
  const before = await getHetznerServer(token, serverId, fetchImpl);
  const body = await request(token, `/servers/${encodeURIComponent(String(serverId))}`, {
    method: "PUT",
    body: JSON.stringify({ name: nextName }),
  }, fetchImpl);
  const renamed = normalizeServer(body?.server);
  if (renamed.id !== before.id || renamed.ip !== before.ip || renamed.name !== nextName) {
    throw new HetznerDirectError(409, "server_identity_changed");
  }
  return renamed;
}

async function waitForServerState(
  token: string,
  before: HetznerServer,
  target: HetznerPowerTarget,
  fetchImpl: FetchLike,
  wait: WaitLike | undefined,
): Promise<HetznerServer> {
  const pause = wait ?? ((milliseconds: number) => new Promise<void>((resolve) => setTimeout(resolve, milliseconds)));
  for (let attempt = 0; attempt < 45; attempt += 1) {
    const current = await getHetznerServer(token, before.id, fetchImpl);
    if (current.id !== before.id || current.ip !== before.ip) {
      // Power transitions must never replace a server or its Primary IPv4.
      // Do not silently report completion if the provider response violates
      // that identity-preservation contract.
      throw new HetznerDirectError(409, "server_identity_changed");
    }
    if (current.status === target) return current;
    if (current.status === "deleting" || current.status === "rebuilding") {
      throw new HetznerDirectError(409, "unexpected_destructive_state");
    }
    await pause(2_000);
  }
  throw new HetznerDirectError(504, "server_state_timeout");
}

async function waitForAction(
  token: string,
  initialAction: any,
  fetchImpl: FetchLike,
  wait: WaitLike | undefined,
): Promise<void> {
  const pause = wait ?? ((milliseconds: number) => new Promise<void>((resolve) => setTimeout(resolve, milliseconds)));
  const actionId = Number(initialAction?.id);
  let status = String(initialAction?.status || "");
  if (!Number.isSafeInteger(actionId) || actionId <= 0 || !status) {
    throw new HetznerDirectError(502, "invalid_action");
  }
  for (let attempt = 0; attempt < 45; attempt += 1) {
    if (status === "success") return;
    if (status === "error") {
      throw new HetznerDirectError(409, String(initialAction?.error?.code || "action_failed"));
    }
    if (status !== "running") throw new HetznerDirectError(502, "invalid_action_status");
    await pause(2_000);
    const body = await request(token, `/actions/${encodeURIComponent(String(actionId))}`, {}, fetchImpl);
    initialAction = body?.action;
    status = String(initialAction?.status || "");
  }
  throw new HetznerDirectError(504, "action_timeout");
}
