import { DeviceTunnel } from "./device-tunnel";
import { deviceEntitlement, freeOwnedDeviceLimit } from "./entitlements";
import { cors, error, json } from "./http";
import { bearerToken, opaqueDeviceKey, validDeviceId } from "./security";
import { accessForDevice, ownerDevice, sessionForToken } from "./store";

export { DeviceTunnel };

interface Env {
  DB: D1Database;
  DEVICE_TUNNELS: DurableObjectNamespace<DeviceTunnel>;
  DEVICE_ID_HMAC_KEY: string;
  FREE_OWNED_DEVICE_LIMIT?: string;
  SESSION_DAYS?: string;
  TUNNEL_REQUEST_TIMEOUT_MS?: string;
}

export default {
  async fetch(request: Request, env: Env): Promise<Response> {
    if (request.method === "OPTIONS") {
      return cors(request, new Response(null, {
        status: 204,
        headers: {
          "access-control-allow-headers": "Authorization, Content-Type, X-Yaver-Rotate-Token, X-Relay-Password",
          "access-control-allow-methods": "GET, POST, PUT, PATCH, DELETE, OPTIONS",
          "access-control-max-age": "86400",
        },
      }));
    }
    const response = await route(request, env).catch((cause: unknown) => {
      console.error("edge request failed", cause instanceof Error ? cause.message : String(cause));
      return error("Internal server error", 500, "edge_internal");
    });
    return cors(request, response);
  },
} satisfies ExportedHandler<Env>;

async function route(request: Request, env: Env): Promise<Response> {
  const url = new URL(request.url);
  if (request.method === "GET" && (url.pathname === "/health" || url.pathname === "/healthz")) {
    return json({ ok: true, service: "yaver-edge", storesUserContent: false });
  }
  if (request.method === "GET" && url.pathname === "/auth/validate") return validateSession(request, env);
  if (request.method === "POST" && url.pathname === "/auth/refresh") return refreshSession(request, env);
  if (request.method === "POST" && url.pathname === "/devices/register") return registerDevice(request, env);
  if (request.method === "GET" && url.pathname === "/devices/list") return listDevices(request, env);
  if (request.method === "GET" && url.pathname === "/presence") return devicePresence(request, env);
  if (url.pathname === "/agent/tunnel/ws") return connectAgent(request, env);
  if (url.pathname.startsWith("/d/")) return proxyDevice(request, env);
  return error("Not found", 404);
}

async function authenticated(request: Request, env: Env) {
  const token = bearerToken(request);
  if (!token) return null;
  const session = await sessionForToken(env.DB, token);
  return session ? { token, session } : null;
}

async function validateSession(request: Request, env: Env): Promise<Response> {
  const auth = await authenticated(request, env);
  if (!auth) return error("Unauthorized", 401);
  return json({
    user: {
      userId: auth.session.userId,
      email: auth.session.email,
      fullName: auth.session.fullName,
      avatarUrl: auth.session.avatarUrl,
      isOwner: false,
      plan: auth.session.plan,
    },
    entitlement: deviceEntitlement(auth.session.plan, env.FREE_OWNED_DEVICE_LIMIT),
  });
}

async function refreshSession(request: Request, env: Env): Promise<Response> {
  const auth = await authenticated(request, env);
  if (!auth) return error("Session expired or invalid", 401);
  const url = new URL(request.url);
  const rotate = url.searchParams.get("rotate") === "1" || request.headers.get("x-yaver-rotate-token") === "1";
  const days = Math.max(1, Math.min(Number(env.SESSION_DAYS || "365"), 365));
  const expiresAt = Date.now() + days * 86_400_000;
  let token: string | undefined;
  let nextHash: string | undefined;
  if (rotate) {
    const bytes = crypto.getRandomValues(new Uint8Array(32));
    token = [...bytes].map((byte) => byte.toString(16).padStart(2, "0")).join("");
    const { sha256Hex } = await import("./security");
    nextHash = await sha256Hex(token);
  }
  const { sha256Hex } = await import("./security");
  const oldHash = await sha256Hex(auth.token);
  const result = await env.DB.prepare(`
    UPDATE sessions SET token_hash = ?1, expires_at = ?2, refreshed_at = ?3
     WHERE token_hash = ?4 AND user_id = ?5
  `).bind(nextHash || oldHash, expiresAt, Date.now(), oldHash, auth.session.userId).run();
  if (result.meta.changes !== 1) return error("Session expired or invalid", 401);
  return json({
    ok: true,
    expiresAt,
    ...(rotate ? { token, rotated: true } : { rotated: false }),
    userId: auth.session.userId,
    isOwner: false,
  });
}

async function registerDevice(request: Request, env: Env): Promise<Response> {
  const auth = await authenticated(request, env);
  if (!auth) return error("Unauthorized", 401);
  const body = await request.json<Record<string, unknown>>().catch(() => null);
  const deviceId = typeof body?.deviceId === "string" ? body.deviceId.trim() : "";
  const publicKey = typeof body?.publicKey === "string" ? body.publicKey.trim() : "";
  if (!validDeviceId(deviceId) || !publicKey) return error("deviceId and publicKey required", 400);
  const name = typeof body?.name === "string" && body.name.trim() ? body.name.trim().slice(0, 160) : deviceId;
  const platform = typeof body?.platform === "string" && body.platform.trim() ? body.platform.trim().slice(0, 64) : "unknown";
  const deviceClass = typeof body?.deviceClass === "string" ? body.deviceClass.trim().slice(0, 64) : null;
  const agentVersion = typeof body?.agentVersion === "string" ? body.agentVersion.trim().slice(0, 64) : null;
  const now = Date.now();
  const existing = await env.DB.prepare("SELECT owner_user_id AS ownerUserId, public_key AS publicKey FROM devices WHERE device_id = ?1")
    .bind(deviceId).first<{ ownerUserId: string; publicKey: string }>();
  if (existing && existing.ownerUserId !== auth.session.userId) return error("Device unavailable", 409);
  if (existing && existing.publicKey !== publicKey) return error("Device key mismatch", 409);
  const limit = freeOwnedDeviceLimit(env.FREE_OWNED_DEVICE_LIMIT);
  const result = await env.DB.prepare(`
    INSERT INTO devices (device_id, owner_user_id, name, platform, device_class, public_key, agent_version, created_at, updated_at, last_seen_at)
    SELECT ?1, ?2, ?3, ?4, ?5, ?6, ?7, ?8, ?8, ?8
     WHERE NOT EXISTS (
             SELECT 1 FROM devices
              WHERE device_id = ?1 AND (owner_user_id != ?2 OR public_key != ?6)
           )
       AND (?9 != 'free'
         OR EXISTS (SELECT 1 FROM devices WHERE device_id = ?1 AND owner_user_id = ?2)
         OR (SELECT COUNT(*) FROM devices WHERE owner_user_id = ?2) < ?10)
    ON CONFLICT(device_id) DO UPDATE SET name = excluded.name, platform = excluded.platform,
      device_class = excluded.device_class, agent_version = excluded.agent_version,
      updated_at = excluded.updated_at, last_seen_at = excluded.last_seen_at
      WHERE devices.owner_user_id = excluded.owner_user_id AND devices.public_key = excluded.public_key
  `).bind(deviceId, auth.session.userId, name, platform, deviceClass, publicKey, agentVersion, now, auth.session.plan, limit).run();
  if (result.meta.changes !== 1) {
    // Re-check after the guarded write so a concurrent claim can never be
    // mistaken for an entitlement denial or mutate another tenant's row.
    const claimed = await env.DB.prepare("SELECT owner_user_id AS ownerUserId, public_key AS publicKey FROM devices WHERE device_id = ?1")
      .bind(deviceId).first<{ ownerUserId: string; publicKey: string }>();
    if (claimed && claimed.ownerUserId !== auth.session.userId) return error("Device unavailable", 409);
    if (claimed && claimed.publicKey !== publicKey) return error("Device key mismatch", 409);
    return json({
      error: `Free accounts can register up to ${limit} owned devices`,
      code: "free_device_limit_reached",
      reasonCode: "entitlement.free_device_limit_reached",
      ...deviceEntitlement(auth.session.plan, env.FREE_OWNED_DEVICE_LIMIT),
      upgradeRequired: true,
      remedy: {
        label: "Open Relay Pro billing",
        method: "GET",
        url: "https://yaver.io/dashboard?tab=billing",
      },
    }, 403);
  }
  return json({ deviceId, entitlement: deviceEntitlement(auth.session.plan, env.FREE_OWNED_DEVICE_LIMIT) });
}

async function listDevices(request: Request, env: Env): Promise<Response> {
  const auth = await authenticated(request, env);
  if (!auth) return error("Unauthorized", 401);
  const rows = await env.DB.prepare(`
    SELECT d.device_id AS deviceId, d.name, d.platform, d.device_class AS deviceClass,
           d.public_key AS publicKey, d.agent_version AS agentVersion,
           d.last_seen_at AS lastSeenAt,
           CASE WHEN d.owner_user_id = ?1 THEN 1 ELSE 0 END AS isOwner
      FROM devices d
 LEFT JOIN device_access a ON a.device_id = d.device_id AND a.user_id = ?1
     WHERE d.owner_user_id = ?1 OR (a.user_id = ?1 AND (a.expires_at IS NULL OR a.expires_at > ?2))
     ORDER BY COALESCE(d.last_seen_at, 0) DESC, d.name ASC
  `).bind(auth.session.userId, Date.now()).all<Record<string, unknown>>();
  return json({
    devices: rows.results,
    entitlement: deviceEntitlement(auth.session.plan, env.FREE_OWNED_DEVICE_LIMIT),
  });
}

async function devicePresence(request: Request, env: Env): Promise<Response> {
  const auth = await authenticated(request, env);
  if (!auth) return error("Unauthorized", 401);
  const requested = [...new Set((new URL(request.url).searchParams.get("ids") || "")
    .split(",").map((value) => value.trim()).filter(validDeviceId))].slice(0, 100);
  if (requested.length === 0) return json({ devices: {} });
  const placeholders = requested.map((_, index) => `?${index + 3}`).join(", ");
  const rows = await env.DB.prepare(`
    SELECT d.device_id AS deviceId, d.owner_user_id AS ownerUserId
      FROM devices d
 LEFT JOIN device_access a ON a.device_id = d.device_id AND a.user_id = ?1
     WHERE (d.owner_user_id = ?1 OR (a.user_id = ?1 AND (a.expires_at IS NULL OR a.expires_at > ?2)))
       AND d.device_id IN (${placeholders})
  `).bind(auth.session.userId, Date.now(), ...requested).all<{ deviceId: string; ownerUserId: string }>();
  const entries = await Promise.all(rows.results.map(async (row) => {
    const object = await tunnelObject(env, row.deviceId);
    const response = await object.fetch(new Request("https://device.internal/_presence", {
      headers: { "x-yaver-device-owner-id": row.ownerUserId },
    }));
    const state = response.ok ? await response.json<Record<string, unknown>>() : { online: false };
    return [row.deviceId, state] as const;
  }));
  return json({ devices: Object.fromEntries(entries) });
}

async function connectAgent(request: Request, env: Env): Promise<Response> {
  if (request.headers.get("upgrade")?.toLowerCase() !== "websocket") return error("WebSocket upgrade required", 426);
  const auth = await authenticated(request, env);
  if (!auth) return error("Unauthorized", 401);
  const deviceId = new URL(request.url).searchParams.get("deviceId")?.trim() || request.headers.get("x-yaver-device-id") || "";
  if (!validDeviceId(deviceId)) return error("deviceId required", 400);
  const device = await ownerDevice(env.DB, auth.session.userId, deviceId);
  if (!device) return error("Device unavailable", 404);
  const object = await tunnelObject(env, deviceId);
  const headers = new Headers(request.headers);
  headers.set("x-yaver-device-id", deviceId);
  headers.set("x-yaver-owner-user-id", auth.session.userId);
  headers.delete("x-yaver-caller-user-id");
  return object.fetch(new Request("https://device.internal/_agent", { method: "GET", headers }));
}

async function proxyDevice(request: Request, env: Env): Promise<Response> {
  const auth = await authenticated(request, env);
  if (!auth) return error("Unauthorized", 401);
  const url = new URL(request.url);
  const match = url.pathname.match(/^\/d\/([^/]+)(\/.*)?$/);
  const deviceId = decodeURIComponent(match?.[1] || "");
  const forwardPath = match?.[2] || "/";
  if (!validDeviceId(deviceId)) return error("Not found", 404);
  const access = await accessForDevice(env.DB, auth.session.userId, deviceId);
  if (!access) return error("Device unavailable", 404);
  const object = await tunnelObject(env, deviceId);
  const headers = new Headers(request.headers);
  headers.set("x-yaver-device-id", deviceId);
  headers.set("x-yaver-device-owner-id", access.ownerUserId);
  headers.set("x-yaver-caller-user-id", auth.session.userId);
  headers.set("x-yaver-forward-path", forwardPath);
  headers.set("x-yaver-original-url", request.url);
  const init: RequestInit = { method: request.method, headers, redirect: "manual" };
  if (request.method !== "GET" && request.method !== "HEAD") init.body = request.body;
  return object.fetch(new Request("https://device.internal/_proxy", init));
}

async function tunnelObject(env: Env, deviceId: string): Promise<DurableObjectStub<DeviceTunnel>> {
  const key = await opaqueDeviceKey(deviceId, env.DEVICE_ID_HMAC_KEY);
  return env.DEVICE_TUNNELS.get(env.DEVICE_TUNNELS.idFromName(key));
}
