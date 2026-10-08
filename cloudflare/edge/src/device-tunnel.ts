import { DurableObject } from "cloudflare:workers";
import type { TunnelFrame, TunnelRequest, TunnelResponse } from "./protocol";
import { constantTimeEqual, validDeviceId } from "./security";

interface Env {
  TUNNEL_REQUEST_TIMEOUT_MS?: string;
}

interface AgentAttachment {
  kind: "agent";
  deviceId: string;
  ownerUserId: string;
  registered: boolean;
  connectedAt?: number;
}

interface ClientAttachment {
  kind: "client";
  streamId: string;
  ownerUserId: string;
}

type SocketAttachment = AgentAttachment | ClientAttachment;

interface PendingRequest {
  resolve: (response: TunnelResponse) => void;
  reject: (error: Error) => void;
  timer: ReturnType<typeof setTimeout>;
}

interface PendingStream {
  resolve: (response: Response) => void;
  reject: (error: Error) => void;
  controller?: ReadableStreamDefaultController<Uint8Array>;
  timer: ReturnType<typeof setTimeout>;
}

const HOP_BY_HOP = new Set([
  "connection", "keep-alive", "proxy-authenticate", "proxy-authorization",
  "te", "trailer", "transfer-encoding", "upgrade",
]);

export class DeviceTunnel extends DurableObject<Env> {
  private readonly pending = new Map<string, PendingRequest>();
  private readonly streams = new Map<string, PendingStream>();

  async fetch(request: Request): Promise<Response> {
    const url = new URL(request.url);
    if (url.pathname === "/_agent") return this.acceptAgent(request);
    if (url.pathname === "/_proxy") return this.proxy(request);
    if (url.pathname === "/_presence") return this.presence(request);
    return new Response("Not found", { status: 404 });
  }

  private acceptAgent(request: Request): Response {
    if (request.headers.get("upgrade")?.toLowerCase() !== "websocket") {
      return new Response("WebSocket upgrade required", { status: 426 });
    }
    const deviceId = request.headers.get("x-yaver-device-id") || "";
    const ownerUserId = request.headers.get("x-yaver-owner-user-id") || "";
    if (!validDeviceId(deviceId) || !ownerUserId) return new Response("Unauthorized", { status: 401 });

    for (const existing of this.ctx.getWebSockets("agent")) {
      const attachment = existing.deserializeAttachment() as AgentAttachment | null;
      if (attachment && !constantTimeEqual(attachment.ownerUserId, ownerUserId)) {
        return new Response("Device unavailable", { status: 409 });
      }
      existing.close(4001, "superseded by same-owner reconnect");
    }

    const pair = new WebSocketPair();
    const [client, server] = Object.values(pair);
    const attachment: AgentAttachment = { kind: "agent", deviceId, ownerUserId, registered: false, connectedAt: Date.now() };
    server.serializeAttachment(attachment);
    this.ctx.acceptWebSocket(server, ["agent"]);
    return new Response(null, { status: 101, webSocket: client });
  }

  private presence(request: Request): Response {
    const ownerUserId = request.headers.get("x-yaver-device-owner-id") || "";
    const attachment = this.ctx.getWebSockets("agent")
      .map((socket) => socket.deserializeAttachment() as AgentAttachment | null)
      .find((candidate) => candidate?.registered && constantTimeEqual(candidate.ownerUserId, ownerUserId));
    const connectedAt = attachment?.connectedAt;
    return Response.json({
      online: Boolean(attachment),
      ...(connectedAt ? {
        connectedAt,
        uptimeSec: Math.max(0, Math.floor((Date.now() - connectedAt) / 1000)),
      } : {}),
    });
  }

  private async proxy(request: Request): Promise<Response> {
    const deviceId = request.headers.get("x-yaver-device-id") || "";
    const callerOwner = request.headers.get("x-yaver-device-owner-id") || "";
    const callerUser = request.headers.get("x-yaver-caller-user-id") || "";
    if (!validDeviceId(deviceId) || !callerOwner || !callerUser) return new Response("Unauthorized", { status: 401 });

    const agent = this.ctx.getWebSockets("agent").find((socket) => {
      const attachment = socket.deserializeAttachment() as AgentAttachment | null;
      return attachment?.registered && constantTimeEqual(attachment.ownerUserId, callerOwner);
    });
    if (!agent) {
      return Response.json({
        ok: false,
        code: "device_not_connected",
        reasonCode: "connectivity.relay.device_not_connected",
        error: "device not connected to relay",
      }, { status: 502 });
    }

    if (request.headers.get("upgrade")?.toLowerCase() === "websocket") {
      return this.proxyWebSocket(request, agent, callerOwner);
    }
    if (isStreamingRequest(request)) {
      return this.proxyStream(request, agent);
    }

    const id = crypto.randomUUID();
    const headers: Record<string, string> = {};
    request.headers.forEach((value, key) => {
      const lower = key.toLowerCase();
      if (!HOP_BY_HOP.has(lower) && !lower.startsWith("x-yaver-edge-")) headers[key] = value;
    });
    const bodyBytes = new Uint8Array(await request.arrayBuffer());
    const original = new URL(request.headers.get("x-yaver-original-url") || request.url);
    const tunnelRequest: TunnelRequest = {
      id,
      method: request.method,
      path: request.headers.get("x-yaver-forward-path") || "/",
      query: original.search.slice(1),
      headers,
      body: bytesToBase64(bodyBytes),
    };

    const timeoutMs = boundedTimeout(this.env.TUNNEL_REQUEST_TIMEOUT_MS);
    const response = await new Promise<TunnelResponse>((resolve, reject) => {
      const timer = setTimeout(() => {
        this.pending.delete(id);
        reject(new Error("agent tunnel request timed out"));
      }, timeoutMs);
      this.pending.set(id, { resolve, reject, timer });
      agent.send(JSON.stringify({ type: "request", id, request: tunnelRequest } satisfies TunnelFrame));
    }).catch((cause: Error) => ({
      id,
      statusCode: 502,
      headers: { "content-type": "application/json" },
      body: bytesToBase64(new TextEncoder().encode(JSON.stringify({ error: cause.message }))),
    }));

    const responseHeaders = new Headers();
    for (const [key, value] of Object.entries(response.headers || {})) {
      if (!HOP_BY_HOP.has(key.toLowerCase())) responseHeaders.set(key, value);
    }
    responseHeaders.set("cache-control", "no-store");
    const responseBody = base64ToBytes(response.body || "");
    const responseBuffer = new ArrayBuffer(responseBody.byteLength);
    new Uint8Array(responseBuffer).set(responseBody);
    return new Response(responseBuffer, {
      status: response.statusCode >= 100 && response.statusCode <= 599 ? response.statusCode : 502,
      headers: responseHeaders,
    });
  }

  private async proxyWebSocket(request: Request, agent: WebSocket, ownerUserId: string): Promise<Response> {
    const id = crypto.randomUUID();
    const tunnelRequest = await requestEnvelope(request, id);
    const pair = new WebSocketPair();
    const [client, server] = Object.values(pair);
    const attachment: ClientAttachment = { kind: "client", streamId: id, ownerUserId };
    server.serializeAttachment(attachment);
    this.ctx.acceptWebSocket(server, ["client"]);
    agent.send(JSON.stringify({ type: "ws_open", id, request: tunnelRequest } satisfies TunnelFrame));
    return new Response(null, { status: 101, webSocket: client });
  }

  private async proxyStream(request: Request, agent: WebSocket): Promise<Response> {
    const id = crypto.randomUUID();
    const tunnelRequest = await requestEnvelope(request, id);
    const timeoutMs = boundedTimeout(this.env.TUNNEL_REQUEST_TIMEOUT_MS);
    return new Promise<Response>((resolve, reject) => {
      const timer = setTimeout(() => {
        const pending = this.streams.get(id);
        this.streams.delete(id);
        if (pending?.controller) pending.controller.error(new Error("agent stream timed out"));
        else reject(new Error("agent stream timed out"));
      }, timeoutMs);
      this.streams.set(id, { resolve, reject, timer });
      agent.send(JSON.stringify({ type: "stream_request", id, request: tunnelRequest } satisfies TunnelFrame));
    }).catch((cause: Error) => Response.json({ error: cause.message }, { status: 502 }));
  }

  webSocketMessage(socket: WebSocket, message: string | ArrayBuffer): void {
    const attachment = socket.deserializeAttachment() as SocketAttachment | null;
    if (!attachment) return;
    if (attachment.kind === "client") {
      const agent = this.ctx.getWebSockets("agent").find((candidate) => {
        const candidateAttachment = candidate.deserializeAttachment() as AgentAttachment | null;
        return candidateAttachment?.registered && constantTimeEqual(candidateAttachment.ownerUserId, attachment.ownerUserId);
      });
      if (!agent) {
        socket.close(1011, "agent tunnel disconnected");
        return;
      }
      const bytes = typeof message === "string" ? new TextEncoder().encode(message) : new Uint8Array(message);
      agent.send(JSON.stringify({
        type: "ws_data",
        id: attachment.streamId,
        data: bytesToBase64(bytes),
        messageType: typeof message === "string" ? 1 : 2,
      } satisfies TunnelFrame));
      return;
    }
    if (typeof message !== "string") {
      socket.close(1003, "JSON frames required");
      return;
    }
    let frame: TunnelFrame;
    try {
      frame = JSON.parse(message) as TunnelFrame;
    } catch {
      socket.send(JSON.stringify({ type: "error", message: "invalid frame" } satisfies TunnelFrame));
      return;
    }

    if (!attachment.registered) {
      const registration = frame.type === "register" ? frame.register : undefined;
      if (!registration || registration.type !== "register" || registration.deviceId !== attachment.deviceId) {
        socket.send(JSON.stringify({ type: "error", ok: false, message: "invalid registration" } satisfies TunnelFrame));
        socket.close(4003, "invalid registration");
        return;
      }
      attachment.registered = true;
      socket.serializeAttachment(attachment);
      socket.send(JSON.stringify({ type: "registered", ok: true } satisfies TunnelFrame));
      return;
    }

    if (frame.type === "ping") {
      socket.send(JSON.stringify({ type: "pong", id: frame.id } satisfies TunnelFrame));
      return;
    }
    if ((frame.type === "response" || frame.type === "error") && frame.id) {
      const pending = this.pending.get(frame.id);
      if (!pending) return;
      clearTimeout(pending.timer);
      this.pending.delete(frame.id);
      if (frame.type === "response" && frame.response) pending.resolve(frame.response);
      else pending.reject(new Error(frame.message || "agent tunnel error"));
      return;
    }
    if (frame.id && frame.type.startsWith("stream_")) {
      const pending = this.streams.get(frame.id);
      if (!pending) return;
      if (frame.type === "stream_start") {
        clearTimeout(pending.timer);
        const headers = new Headers();
        for (const [key, value] of Object.entries(frame.headers || {})) {
          if (!HOP_BY_HOP.has(key.toLowerCase())) headers.set(key, value);
        }
        headers.set("cache-control", "no-store");
        const stream = new ReadableStream<Uint8Array>({
          start: (controller) => { pending.controller = controller; },
          cancel: () => { this.streams.delete(frame.id!); },
        });
        pending.resolve(new Response(stream, {
          status: frame.statusCode && frame.statusCode >= 100 && frame.statusCode <= 599 ? frame.statusCode : 200,
          headers,
        }));
        return;
      }
      if (frame.type === "stream_data" && pending.controller && frame.data) {
        pending.controller.enqueue(base64ToBytes(frame.data));
        return;
      }
      if (frame.type === "stream_end") {
        clearTimeout(pending.timer);
        pending.controller?.close();
        this.streams.delete(frame.id);
        return;
      }
      if (frame.type === "stream_error") {
        clearTimeout(pending.timer);
        const failure = new Error(frame.message || "agent stream error");
        if (pending.controller) pending.controller.error(failure);
        else pending.reject(failure);
        this.streams.delete(frame.id);
      }
    }
    if (frame.id && frame.type.startsWith("ws_")) {
      const client = this.ctx.getWebSockets("client").find((candidate) => {
        const clientAttachment = candidate.deserializeAttachment() as ClientAttachment | null;
        return clientAttachment?.kind === "client" && clientAttachment.streamId === frame.id;
      });
      if (!client) return;
      if (frame.type === "ws_data" && frame.data) {
        const bytes = base64ToBytes(frame.data);
        if (frame.messageType === 1) client.send(new TextDecoder().decode(bytes));
        else client.send(bytes);
        return;
      }
      if (frame.type === "ws_close") {
        client.close(1000, "agent websocket closed");
        return;
      }
      if (frame.type === "ws_error") client.close(1011, frame.message || "agent websocket error");
    }
  }

  webSocketClose(socket: WebSocket): void {
    const attachment = socket.deserializeAttachment() as SocketAttachment | null;
    if (attachment?.kind === "client") {
      const agent = this.ctx.getWebSockets("agent")[0];
      if (agent) agent.send(JSON.stringify({ type: "ws_close", id: attachment.streamId } satisfies TunnelFrame));
      return;
    }
    if (attachment?.kind !== "agent") return;
    for (const [id, pending] of this.pending) {
      clearTimeout(pending.timer);
      pending.reject(new Error("agent tunnel disconnected"));
      this.pending.delete(id);
    }
    for (const [id, pending] of this.streams) {
      clearTimeout(pending.timer);
      const failure = new Error("agent tunnel disconnected");
      if (pending.controller) pending.controller.error(failure);
      else pending.reject(failure);
      this.streams.delete(id);
    }
    for (const client of this.ctx.getWebSockets("client")) client.close(1011, "agent tunnel disconnected");
  }

  webSocketError(socket: WebSocket): void {
    this.webSocketClose(socket);
  }
}

async function requestEnvelope(request: Request, id: string): Promise<TunnelRequest> {
  const headers: Record<string, string> = {};
  request.headers.forEach((value, key) => {
    const lower = key.toLowerCase();
    if (!HOP_BY_HOP.has(lower) && !lower.startsWith("x-yaver-edge-")) headers[key] = value;
  });
  const original = new URL(request.headers.get("x-yaver-original-url") || request.url);
  return {
    id,
    method: request.method,
    path: request.headers.get("x-yaver-forward-path") || "/",
    query: original.search.slice(1),
    headers,
    body: bytesToBase64(new Uint8Array(await request.arrayBuffer())),
  };
}

function boundedTimeout(value?: string): number {
  const parsed = Number(value || "900000");
  return Number.isFinite(parsed) ? Math.max(1_000, Math.min(parsed, 900_000)) : 900_000;
}

function isStreamingRequest(request: Request): boolean {
  const path = request.headers.get("x-yaver-forward-path") || "";
  return request.method === "GET" && (
    request.headers.get("accept")?.includes("text/event-stream") === true ||
    path.includes("/output") || path.endsWith("/dev/events") || path.endsWith("/subscribe") ||
    path.endsWith("/blackbox/command-stream") || path.endsWith("/blackbox/stream") ||
    path.endsWith("/feedback/stream") || path.includes("/streams/")
  );
}

function bytesToBase64(bytes: Uint8Array): string {
  let binary = "";
  for (let offset = 0; offset < bytes.length; offset += 0x8000) {
    binary += String.fromCharCode(...bytes.subarray(offset, offset + 0x8000));
  }
  return btoa(binary);
}

function base64ToBytes(value: string): Uint8Array {
  const binary = atob(value);
  const bytes = new Uint8Array(binary.length);
  for (let index = 0; index < binary.length; index += 1) bytes[index] = binary.charCodeAt(index);
  return bytes;
}
