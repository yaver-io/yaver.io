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

export async function validateHetznerToken(token: string, fetchImpl: FetchLike = fetch): Promise<void> {
  await request(token, "/servers?per_page=1", {}, fetchImpl);
}

export async function listHetznerServers(token: string, fetchImpl: FetchLike = fetch): Promise<HetznerServer[]> {
  const body = await request(token, "/servers?per_page=50", {}, fetchImpl);
  return Array.isArray(body?.servers) ? body.servers.map(normalizeServer) : [];
}

export async function powerOnHetznerServer(
  token: string,
  serverId: number,
  fetchImpl: FetchLike = fetch,
  wait?: WaitLike,
): Promise<void> {
  const body = await request(token, `/servers/${encodeURIComponent(String(serverId))}/actions/poweron`, { method: "POST" }, fetchImpl);
  await waitForAction(token, body?.action, fetchImpl, wait);
}

export async function shutdownHetznerServer(
  token: string,
  serverId: number,
  fetchImpl: FetchLike = fetch,
  wait?: WaitLike,
): Promise<void> {
  const body = await request(token, `/servers/${encodeURIComponent(String(serverId))}/actions/shutdown`, { method: "POST" }, fetchImpl);
  await waitForAction(token, body?.action, fetchImpl, wait);
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
