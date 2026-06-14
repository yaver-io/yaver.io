// CRUD client for the agent's external-MCP registry (GET/POST/DELETE
// /mcp/servers — see desktop/agent/mcp_external.go). Unlike relay servers (which
// live in on-phone AsyncStorage), these are stored SERVER-SIDE on the agent: the
// agent connects out to each registered MCP, merges its tools into tools/list,
// and forwards "<name>__<tool>" calls. So adding one here makes its tools usable
// from this app, web, and the LLM — for your own private MCPs or anyone's public
// ones.
//
// Transport mirrors yaverMcpDirect.ts: `${quicClient.baseUrl}/mcp/servers` with
// the existing bearer auth headers.

import { quicClient } from "./quic";

export interface McpServer {
  name: string;
  url: string;
  enabled: boolean;
  /** GET only — whether a bearer token is stored (the token itself is redacted). */
  hasAuth?: boolean;
  /** GET only — live tool count fetched from the server. */
  toolCount?: number;
}

export interface McpServerInput {
  name: string;
  url: string;
  auth_token?: string;
  enabled?: boolean;
}

async function request<T>(method: string, path: string, body?: unknown, signal?: AbortSignal): Promise<T> {
  const base = quicClient.baseUrl;
  if (!base) throw new Error("no agent selected — pick a device first");
  const res = await fetch(`${base}${path}`, {
    method,
    headers: {
      "Content-Type": "application/json",
      Accept: "application/json",
      ...quicClient.getAuthHeaders(),
    },
    body: body !== undefined ? JSON.stringify(body) : undefined,
    signal,
  });
  const text = await res.text().catch(() => "");
  if (!res.ok) {
    throw new Error(`HTTP ${res.status}${text ? ` — ${text.slice(0, 200)}` : ""}`);
  }
  return (text ? JSON.parse(text) : {}) as T;
}

/** List every registered external MCP server (tokens redacted). */
export async function listMcpServers(signal?: AbortSignal): Promise<McpServer[]> {
  const r = await request<{ servers?: McpServer[] }>("GET", "/mcp/servers", undefined, signal);
  return r.servers ?? [];
}

/** Add or update a server (upsert by name). */
export async function saveMcpServer(s: McpServerInput, signal?: AbortSignal): Promise<void> {
  await request<{ ok: boolean }>("POST", "/mcp/servers", s, signal);
}

/** Probe a server's reachability + tool count without persisting it. */
export async function testMcpServer(
  s: McpServerInput,
  signal?: AbortSignal,
): Promise<{ ok: boolean; toolCount?: number; error?: string }> {
  return request("POST", "/mcp/servers?test=1", s, signal);
}

/** Remove a server by name. */
export async function deleteMcpServer(name: string, signal?: AbortSignal): Promise<void> {
  await request<{ ok: boolean }>("DELETE", `/mcp/servers?name=${encodeURIComponent(name)}`, undefined, signal);
}
