"use client";

// remoteBridge.ts — parent side of the preview data channel, backed by a REMOTE
// Yaver Serverless `/data/<slug>` API instead of local sql.js. This is what
// turns the generic renderer (preview.ts) into a "USE the deployed app" runtime:
// a friend opens a shared app and the same iframe renderer reads/writes through
// the host's /data API using a scoped read-only token. The renderer is byte-for-
// byte the same as in local preview — only the bridge differs (dataBridge.ts =
// local sql.js; this = remote HTTP).

import type { PhoneAppSpec, PhoneSchema } from "@/lib/agent-client";

const REQ = "yaver-sandbox-req";
const REP = "yaver-sandbox-rep";

interface BridgeRequest {
  source: typeof REQ;
  id: string;
  op: "describe" | "tables" | "list" | "insert" | "update" | "delete";
  table?: string;
  doc?: Record<string, unknown>;
  rowId?: unknown;
  limit?: number;
}

function isRequest(d: unknown): d is BridgeRequest {
  return !!d && typeof d === "object" && (d as { source?: unknown }).source === REQ;
}

export interface RemoteBridgeConfig {
  /** Origin serving the data API, e.g. https://cloud.yaver.io */
  dataBase: string;
  slug: string;
  /** Scoped pp_ token (read-only for friend preview). */
  dataToken?: string;
  schema?: PhoneSchema | null;
  app?: PhoneAppSpec | null;
}

export function attachRemoteBridge(cfg: RemoteBridgeConfig): () => void {
  const base = cfg.dataBase.replace(/\/$/, "");
  const slug = encodeURIComponent(cfg.slug);
  const headers = (json = false): Record<string, string> => {
    const h: Record<string, string> = {};
    if (cfg.dataToken) h.Authorization = `Bearer ${cfg.dataToken}`;
    if (json) h["Content-Type"] = "application/json";
    return h;
  };

  async function handle(req: BridgeRequest): Promise<{ data?: unknown; error?: string }> {
    try {
      switch (req.op) {
        case "describe":
          return { data: { schema: cfg.schema ?? { tables: [] }, app: cfg.app ?? {} } };
        case "tables":
          return { data: (cfg.schema?.tables ?? []).map((t) => ({ name: t.name })) };
        case "list": {
          if (!req.table) return { error: "missing table" };
          const r = await fetch(`${base}/data/${slug}/${encodeURIComponent(req.table)}?limit=${req.limit ?? 200}`, {
            headers: headers(),
          });
          if (!r.ok) return { error: `load failed (${r.status})` };
          const b = (await r.json()) as { rows?: unknown[] } | unknown[];
          return { data: Array.isArray(b) ? b : (b.rows ?? []) };
        }
        case "insert": {
          if (!req.table) return { error: "missing table" };
          const r = await fetch(`${base}/data/${slug}/${encodeURIComponent(req.table)}`, {
            method: "POST",
            headers: headers(true),
            body: JSON.stringify(req.doc ?? {}),
          });
          if (!r.ok) return { error: r.status === 403 ? "read-only preview — sign in as the owner to edit" : `insert failed (${r.status})` };
          return { data: { ok: true } };
        }
        case "update": {
          if (!req.table) return { error: "missing table" };
          const r = await fetch(`${base}/data/${slug}/${encodeURIComponent(req.table)}/${encodeURIComponent(String(req.rowId))}`, {
            method: "PATCH",
            headers: headers(true),
            body: JSON.stringify(req.doc ?? {}),
          });
          if (!r.ok) return { error: r.status === 403 ? "read-only preview" : `update failed (${r.status})` };
          return { data: { ok: true } };
        }
        case "delete": {
          if (!req.table) return { error: "missing table" };
          const r = await fetch(`${base}/data/${slug}/${encodeURIComponent(req.table)}/${encodeURIComponent(String(req.rowId))}`, {
            method: "DELETE",
            headers: headers(),
          });
          if (!r.ok) return { error: r.status === 403 ? "read-only preview" : `delete failed (${r.status})` };
          return { data: { ok: true } };
        }
        default:
          return { error: "unknown op" };
      }
    } catch (e) {
      return { error: e instanceof Error ? e.message : String(e) };
    }
  }

  const listener = (event: MessageEvent) => {
    if (!isRequest(event.data)) return;
    const req = event.data;
    const target = event.source as Window | null;
    void handle(req).then((res) => {
      target?.postMessage({ source: REP, id: req.id, ok: !res.error, ...res }, "*");
    });
  };
  window.addEventListener("message", listener);
  return () => window.removeEventListener("message", listener);
}
