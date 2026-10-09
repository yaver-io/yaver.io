import { bearerFromProtocols, verifyAccessJwt } from "./jwt";
import { parseSignal, type AccessClaims } from "./protocol";

export interface Env {
  ACCESS_ROOMS: DurableObjectNamespace;
  JWKS_URL: string;
  JWT_ISSUER: string;
}

type SocketAttachment = AccessClaims & { windowStartedAt: number; count: number; nonces: Record<string, number> };

function json(body: unknown, status: number): Response {
  return Response.json(body, { status, headers: { "cache-control": "no-store", "content-type": "application/json" } });
}

export default {
  async fetch(request: Request, env: Env): Promise<Response> {
    const url = new URL(request.url);
    if (request.method === "GET" && url.pathname === "/healthz") {
      return json({ ok: true, transport: "hibernating-websocket", storesMessages: false }, 200);
    }
    if (request.method !== "GET" || url.pathname !== "/v1/connect" || request.headers.get("Upgrade")?.toLowerCase() !== "websocket") {
      return json({ error: "not_found" }, 404);
    }
    try {
      const token = bearerFromProtocols(request.headers.get("Sec-WebSocket-Protocol"));
      const claims = await verifyAccessJwt(token, env.JWKS_URL, env.JWT_ISSUER);
      const room = env.ACCESS_ROOMS.get(env.ACCESS_ROOMS.idFromName(claims.tenant));
      const headers = new Headers({
        "x-yaver-role": claims.role,
        "x-yaver-tenant": claims.tenant,
        "x-yaver-broker": claims.broker,
        "x-yaver-actor": claims.actor,
        "x-yaver-sub": claims.sub,
        "x-yaver-jti": claims.jti,
        "x-yaver-exp": String(claims.exp),
      });
      if (claims.device) headers.set("x-yaver-device", claims.device);
      return room.fetch(new Request("https://access-room/connect", { headers }));
    } catch (error) {
      return json({ error: error instanceof Error ? error.message : "access_denied" }, 401);
    }
  },
} satisfies ExportedHandler<Env>;

export class AccessRoom implements DurableObject {
  constructor(private readonly ctx: DurableObjectState, private readonly env: Env) {}

  async fetch(request: Request): Promise<Response> {
    const role = request.headers.get("x-yaver-role");
    const claims: AccessClaims = {
      role: role === "device" ? "device" : "controller",
      tenant: request.headers.get("x-yaver-tenant") || "",
      broker: request.headers.get("x-yaver-broker") || "",
      actor: request.headers.get("x-yaver-actor") || "",
      device: request.headers.get("x-yaver-device") || undefined,
      sub: request.headers.get("x-yaver-sub") || "",
      jti: request.headers.get("x-yaver-jti") || "",
      exp: Number(request.headers.get("x-yaver-exp") || 0),
    };
    if (!claims.tenant || !claims.actor || claims.exp * 1000 <= Date.now()) return json({ error: "access_denied" }, 403);
    const pair = new WebSocketPair();
    const [client, server] = Object.values(pair);
    const attachment: SocketAttachment = { ...claims, windowStartedAt: Date.now(), count: 0, nonces: {} };
    server.serializeAttachment(attachment);
    this.ctx.acceptWebSocket(server);
    return new Response(null, { status: 101, webSocket: client, headers: { "Sec-WebSocket-Protocol": "yaver-access-v1" } });
  }

  async webSocketMessage(socket: WebSocket, message: string | ArrayBuffer): Promise<void> {
    if (typeof message !== "string") return this.deny(socket, "text_required");
    const attachment = socket.deserializeAttachment() as SocketAttachment;
    if (!attachment || attachment.exp * 1000 <= Date.now()) return this.deny(socket, "credential_expired");
    const now = Date.now();
    if (now - attachment.windowStartedAt >= 60_000) {
      attachment.windowStartedAt = now;
      attachment.count = 0;
      attachment.nonces = {};
    }
    if (++attachment.count > 20) return this.deny(socket, "rate_limited");
    let signal;
    try { signal = parseSignal(message, attachment, now); }
    catch (error) { return this.deny(socket, error instanceof Error ? error.message : "invalid_signal"); }
    for (const [nonce, expiry] of Object.entries(attachment.nonces)) if (expiry <= now) delete attachment.nonces[nonce];
    if (attachment.nonces[signal.nonce]) return this.deny(socket, "replay_rejected");
    attachment.nonces[signal.nonce] = signal.expiresAt;
    socket.serializeAttachment(attachment);

    if (signal.kind === "requests.auth") {
      const key = `auth:${signal.targetDeviceId}`;
      const activeUntil = await this.ctx.storage.get<number>(key);
      if (activeUntil && activeUntil > now) return this.deny(socket, "auth_request_active");
      await this.ctx.storage.put(key, signal.expiresAt);
    }
    for (const peer of this.ctx.getWebSockets()) {
      if (peer === socket || peer.readyState !== WebSocket.OPEN) continue;
      const target = peer.deserializeAttachment() as SocketAttachment;
      const deliver = attachment.role === "controller"
        ? target.role === "device" && target.device === signal.targetDeviceId
        : target.role === "controller";
      if (deliver) peer.send(message);
    }
  }

  webSocketClose(socket: WebSocket, code: number, reason: string): void {
    socket.close(code, reason);
  }

  private deny(socket: WebSocket, code: string): void {
    try { socket.send(JSON.stringify({ error: code })); } catch { /* closing */ }
    socket.close(1008, code.slice(0, 120));
  }
}
