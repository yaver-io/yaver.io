// Direct-MCP client — bypasses the on-phone runYaverAgent tool-use
// loop and POSTs a JSON-RPC tools/call straight at the currently-
// selected Yaver agent's `/mcp` endpoint. Used by glass-terminal's
// vibe-bar chips (⟳ reload, 📦 push, 📊 status, 🩺 doctor) where the
// action is deterministic, the args are fixed, and an LLM round-trip
// is wasted latency + budget.
//
// Transport: `${quicClient.baseUrl}/mcp` with the existing bearer auth
// headers. Wire format mirrors what `talcli mcp serve --http` speaks:
//
//   request:  { jsonrpc: "2.0", id: 1, method: "tools/call",
//               params: { name, arguments } }
//   response: { jsonrpc: "2.0", id: 1, result: <tool-output> }
//          or { jsonrpc: "2.0", id: 1, error:  { code, message } }
//
// The agent's tool dispatchers return a `content[]` array with text /
// json items; we surface the first json item's parsed payload, or the
// concatenated text otherwise. Callers that want the raw shape can
// reach for `callMcpDirectRaw`.

import { quicClient } from "./quic";

export interface McpDirectResult<T = unknown> {
  ok: boolean;
  result?: T;
  error?: string;
  /** Raw JSON-RPC envelope, exposed so glass-terminal can pretty-print. */
  raw?: unknown;
}

export interface McpRawEnvelope {
  jsonrpc: "2.0";
  id: number;
  result?: {
    content?: Array<
      | { type: "text"; text: string }
      | { type: "json"; json: unknown }
      | Record<string, unknown>
    >;
    isError?: boolean;
    [k: string]: unknown;
  };
  error?: { code: number; message: string; data?: unknown };
}

let nextRpcId = 1;

export async function callMcpDirectRaw(
  toolName: string,
  args: Record<string, unknown> = {},
  signal?: AbortSignal,
): Promise<McpRawEnvelope> {
  const base = quicClient.baseUrl;
  if (!base) throw new Error("no agent selected — pick a device first");
  const headers = {
    "Content-Type": "application/json",
    Accept: "application/json",
    ...quicClient.getAuthHeaders(),
  };
  const body = JSON.stringify({
    jsonrpc: "2.0",
    id: nextRpcId++,
    method: "tools/call",
    params: { name: toolName, arguments: args },
  });
  const res = await fetch(`${base}/mcp`, {
    method: "POST",
    headers,
    body,
    signal,
  });
  if (!res.ok) {
    const text = await res.text().catch(() => "");
    throw new Error(`MCP ${toolName} HTTP ${res.status}${text ? ` — ${text.slice(0, 200)}` : ""}`);
  }
  return (await res.json()) as McpRawEnvelope;
}

export async function callMcpDirect<T = unknown>(
  toolName: string,
  args: Record<string, unknown> = {},
  signal?: AbortSignal,
): Promise<McpDirectResult<T>> {
  try {
    const env = await callMcpDirectRaw(toolName, args, signal);
    if (env.error) {
      return { ok: false, error: env.error.message, raw: env };
    }
    // Tool handlers return `{ content: [...] }` where content is either
    // a single { type: "json", json: <result> } or text items.
    const content = env.result?.content ?? [];
    for (const item of content) {
      if (item && typeof item === "object") {
        // Some Go handlers stuff the payload directly into the result
        // map without wrapping it in a content[] item; guard for that
        // shape too.
        if ("json" in item) {
          return { ok: true, result: (item as { json: T }).json, raw: env };
        }
        if ("text" in item && typeof (item as { text: unknown }).text === "string") {
          const text = (item as { text: string }).text;
          // Try to JSON.parse — many tools return stringified JSON in a
          // text item rather than a typed json item.
          try {
            const parsed = JSON.parse(text);
            return { ok: true, result: parsed as T, raw: env };
          } catch {
            return { ok: true, result: text as unknown as T, raw: env };
          }
        }
      }
    }
    // Some tools just return the raw map under `result` with no content
    // wrapper. Hand it back as the result.
    return { ok: true, result: env.result as unknown as T, raw: env };
  } catch (e: unknown) {
    return {
      ok: false,
      error: e instanceof Error ? e.message : String(e),
    };
  }
}

// Pre-baked one-shot helpers for the glass-terminal vibe chips.
export interface MobileHermesReloadResult {
  ok: boolean;
  changeClass?: "js_only" | "native_rebuild_required" | "unknown";
  nativeChangesDetected?: boolean;
  nativeChanges?: Array<{ Path: string; Reason: string }>;
  error?: string;
}

export function callMobileHermesReload(
  opts: { targetDeviceId?: string; mode?: "dev" | "bundle"; signal?: AbortSignal } = {},
): Promise<McpDirectResult<MobileHermesReloadResult>> {
  const args: Record<string, unknown> = {};
  if (opts.targetDeviceId) args.target_device_id = opts.targetDeviceId;
  if (opts.mode) args.mode = opts.mode;
  return callMcpDirect<MobileHermesReloadResult>("mobile_hermes_reload", args, opts.signal);
}
