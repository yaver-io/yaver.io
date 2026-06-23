"use client";

// agentDataBridge.ts — the agent-relay twin of dataBridge.ts. The preview iframe
// posts the SAME yaver-sandbox-req data ops; this bridge proxies them to the
// connected agent over HTTP (browse/insert/update/delete) instead of local
// sql.js. Lets the exact same renderer + Design Glass run against an
// agent-hosted phone project over the relay.

import { agentClient } from "@/lib/agent-client";

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

function isRequest(data: unknown): data is BridgeRequest {
  return !!data && typeof data === "object" && (data as { source?: unknown }).source === REQ;
}

export function attachAgentBridge(slug: string, opts: { onMutate?: () => void } = {}): () => void {
  async function handle(req: BridgeRequest): Promise<{ data?: unknown; error?: string }> {
    try {
      switch (req.op) {
        case "list": {
          if (!req.table) return { error: "missing table" };
          const r = await agentClient.browsePhoneTable(slug, req.table, "", req.limit ?? 200);
          return { data: r.rows };
        }
        case "insert":
          if (!req.table || !req.doc) return { error: "missing table/doc" };
          await agentClient.insertPhoneRow(slug, req.table, req.doc);
          opts.onMutate?.();
          return { data: { ok: true } };
        case "update":
          if (!req.table) return { error: "missing table" };
          await agentClient.updatePhoneRow(slug, req.table, String(req.rowId), req.doc ?? {});
          opts.onMutate?.();
          return { data: { ok: true } };
        case "delete":
          if (!req.table) return { error: "missing table" };
          await agentClient.deletePhoneRow(slug, req.table, String(req.rowId));
          opts.onMutate?.();
          return { data: { ok: true } };
        default:
          return { error: "unsupported op" };
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
