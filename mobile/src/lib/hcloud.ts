// hcloud.ts — phone-DIRECT Hetzner Cloud client. The app talks to
// api.hetzner.cloud itself (token from the device keychain), so a user can
// WIRE Hetzner and manage / provision boxes with NO paired agent. This is the
// chicken-and-egg fix: a fresh install can spin up its first box.
//
// Split for testability (mirrors codingBackend.ts / boxInit.ts): the request
// builders, response parsers, and cost helpers are PURE + RN-free (tsx-tested);
// HetznerClient is the thin fetch shell. The Hetzner token is passed in by the
// caller (read from SecureStore) and is the ONLY secret involved — it never
// transits Convex or any relay, matching the BYO on-device-only posture.

export const HCLOUD_API = "https://api.hetzner.cloud/v1";

export interface HetznerServer {
  id: string;
  name: string;
  status: string; // running | off | initializing | starting | stopping | ...
  ip: string;
  type: string; // server_type name, e.g. "cpx21"
  location: string;
  created: string; // ISO timestamp
}

export interface HttpReq {
  url: string;
  method: string;
  headers: Record<string, string>;
  body?: string;
}

// ── cost / uptime ─────────────────────────────────────────────────────────
// Approx Hetzner running cost (EUR/mo, incl. IPv4) by server_type. Rough — for
// at-a-glance "what am I burning", NOT billing. A running box bills full price
// even powered-off; only DELETE halts it. Unknown type → null.
export const TYPE_EUR_MO: Record<string, number> = {
  cx11: 4.15, cx21: 5.83, cx31: 10.59, cx41: 19.9, cx51: 35.79,
  cx22: 4.59, cx32: 7.59, cx42: 17.49, cx52: 33.69,
  cpx11: 4.79, cpx21: 8.49, cpx31: 15.49, cpx41: 29.99, cpx51: 65.99,
  cax11: 3.99, cax21: 7.49, cax31: 14.99, cax41: 29.99,
};

export function monthlyEur(type?: string | null): number | null {
  if (!type) return null;
  return TYPE_EUR_MO[String(type).toLowerCase()] ?? null;
}

export function uptimeLabel(created?: string | null, nowMs: number = Date.now()): string {
  if (!created) return "";
  const t = Date.parse(String(created));
  if (Number.isNaN(t)) return "";
  const ms = nowMs - t;
  const days = Math.floor(ms / 86400000);
  if (days >= 1) return `up ${days}d`;
  const hrs = Math.floor(ms / 3600000);
  return `up ${Math.max(1, hrs)}h`;
}

// ── SKU mapping ───────────────────────────────────────────────────────────
// Plan → Hetzner server_type, per region. ARM (cax*) is EU-only and cheapest;
// AMD (cpx*) is the cross-region choice (ash/hil + EU). We pick arm in EU, amd
// in US so a plan resolves to a real, orderable type in the chosen location.
export type Plan = "starter" | "pro" | "scale";
export type Region = "eu" | "us";

const SKU: Record<Region, Record<Plan, string>> = {
  eu: { starter: "cax21", pro: "cax31", scale: "cax41" },
  us: { starter: "cpx21", pro: "cpx31", scale: "cpx41" },
};
const LOCATION: Record<Region, string> = { eu: "hel1", us: "ash" };

export function serverTypeFor(plan: Plan, region: Region): string {
  return SKU[region][plan];
}
export function locationFor(region: Region): string {
  return LOCATION[region];
}

// ── request builders (pure) ───────────────────────────────────────────────
function authHeaders(token: string, withJson = false): Record<string, string> {
  const h: Record<string, string> = { Authorization: `Bearer ${token}` };
  if (withJson) h["Content-Type"] = "application/json";
  return h;
}

export function listServersReq(token: string, page = 1): HttpReq {
  return { url: `${HCLOUD_API}/servers?per_page=50&page=${page}`, method: "GET", headers: authHeaders(token) };
}

export interface CreateServerOpts {
  name: string;
  serverType: string;
  location: string;
  image?: string; // default ubuntu-24.04
  userData?: string; // cloud-init
  sshKeys?: string[]; // key names/ids on the account
  labels?: Record<string, string>;
}

export function createServerReq(token: string, o: CreateServerOpts): HttpReq {
  const body: Record<string, unknown> = {
    name: o.name,
    server_type: o.serverType,
    image: o.image ?? "ubuntu-24.04",
    location: o.location,
  };
  if (o.userData) body.user_data = o.userData;
  if (o.sshKeys && o.sshKeys.length) body.ssh_keys = o.sshKeys;
  if (o.labels) body.labels = o.labels;
  return { url: `${HCLOUD_API}/servers`, method: "POST", headers: authHeaders(token, true), body: JSON.stringify(body) };
}

export function snapshotReq(token: string, serverId: string, description: string): HttpReq {
  return {
    url: `${HCLOUD_API}/servers/${serverId}/actions/create_image`,
    method: "POST",
    headers: authHeaders(token, true),
    body: JSON.stringify({ type: "snapshot", description }),
  };
}

export function deleteServerReq(token: string, serverId: string): HttpReq {
  return { url: `${HCLOUD_API}/servers/${serverId}`, method: "DELETE", headers: authHeaders(token) };
}

// ── response parsers (pure) ───────────────────────────────────────────────
export function parseServers(body: any): HetznerServer[] {
  const arr = Array.isArray(body?.servers) ? body.servers : [];
  return arr.map((s: any) => ({
    id: String(s?.id ?? ""),
    name: String(s?.name ?? ""),
    status: String(s?.status ?? "?"),
    ip: String(s?.public_net?.ipv4?.ip ?? ""),
    type: String(s?.server_type?.name ?? ""),
    location: String(s?.datacenter?.location?.name ?? ""),
    created: String(s?.created ?? ""),
  })).filter((s: HetznerServer) => s.id);
}

export function parseCreate(body: any): { serverId: string; ip: string } {
  const id = body?.server?.id;
  if (!id) throw new Error("create returned no server id");
  return { serverId: String(id), ip: String(body?.server?.public_net?.ipv4?.ip ?? "") };
}

export function parseSnapshot(body: any): { imageId: string } {
  const id = body?.image?.id;
  if (!id) throw new Error("snapshot returned no image id");
  return { imageId: String(id) };
}

/** Hetzner error envelope → message string, or null if none. */
export function parseError(body: any): string | null {
  const e = body?.error;
  if (!e) return null;
  return `${e.code ?? "error"}: ${e.message ?? "unknown"}`;
}

/** True if the token looks structurally like a Hetzner token (64 hex/alnum).
 *  Cheap client-side guard before a network round-trip; the real check is a
 *  listServers call. */
export function looksLikeToken(token: string): boolean {
  return /^[A-Za-z0-9]{40,80}$/.test(token.trim());
}

// ── fetch client (I/O) ────────────────────────────────────────────────────
async function doFetch<T>(req: HttpReq, parse: (body: any) => T, signal?: AbortSignal): Promise<T> {
  const res = await fetch(req.url, { method: req.method, headers: req.headers, body: req.body, signal });
  const body = await res.json().catch(() => ({}));
  if (!res.ok) {
    throw new Error(parseError(body) || `Hetzner HTTP ${res.status}`);
  }
  return parse(body);
}

export class HetznerClient {
  constructor(private token: string) {}

  /** All servers on the account (paginated). Doubles as token validation —
   *  a bad token throws (401). */
  async listServers(signal?: AbortSignal): Promise<HetznerServer[]> {
    const out: HetznerServer[] = [];
    let page = 1;
    // Hard page cap so a malformed response can't loop forever.
    for (let i = 0; i < 20; i++) {
      const res = await fetch(listServersReq(this.token, page).url, {
        headers: authHeaders(this.token),
        signal,
      });
      const body = await res.json().catch(() => ({}));
      if (!res.ok) throw new Error(parseError(body) || `Hetzner HTTP ${res.status}`);
      out.push(...parseServers(body));
      const next = body?.meta?.pagination?.next_page;
      if (!next) break;
      page = next;
    }
    return out;
  }

  async createServer(o: CreateServerOpts, signal?: AbortSignal): Promise<{ serverId: string; ip: string }> {
    return doFetch(createServerReq(this.token, o), parseCreate, signal);
  }

  async snapshot(serverId: string, description: string, signal?: AbortSignal): Promise<{ imageId: string }> {
    return doFetch(snapshotReq(this.token, serverId, description), parseSnapshot, signal);
  }

  async deleteServer(serverId: string, signal?: AbortSignal): Promise<void> {
    const res = await fetch(deleteServerReq(this.token, serverId).url, {
      method: "DELETE",
      headers: authHeaders(this.token),
      signal,
    });
    if (!res.ok) {
      const body = await res.json().catch(() => ({}));
      throw new Error(parseError(body) || `Hetzner HTTP ${res.status}`);
    }
  }

  /** Stop = snapshot (recover-safe) THEN delete, so billing fully halts (a
   *  powered-off Hetzner server still bills). Fail-closed: a failed snapshot
   *  aborts before delete, so data is never lost. */
  async stop(serverId: string, description: string, signal?: AbortSignal): Promise<{ imageId: string }> {
    const snap = await this.snapshot(serverId, description, signal);
    await this.deleteServer(serverId, signal);
    return snap;
  }
}
