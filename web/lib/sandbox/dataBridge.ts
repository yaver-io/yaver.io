"use client";

// dataBridge.ts — parent side of the preview's data channel.
//
// The preview app runs in a sandboxed iframe (opaque origin) and cannot touch
// IndexedDB or sql.js directly. It posts data requests to the parent window;
// this bridge executes them against the browser-local project (localProjects)
// and posts results back. Mutations persist to IndexedDB, so the live app and
// the data browser stay in sync. Protocol is intentionally tiny and versioned
// by the `source` tag so it can't collide with other postMessage traffic.

import {
  browseLocalTable,
  deleteLocalRow,
  getLocalProject,
  insertLocalRow,
  listLocalTables,
  updateLocalRow,
} from "./localProjects";

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

export interface BridgeOptions {
  /** Called after any mutation so the host can refresh the data browser. */
  onMutate?: () => void;
}

/**
 * Attach the data bridge for `slug`. Returns a detach function. Pass the
 * preview iframe's contentWindow so replies only go to it.
 */
export function attachSandboxBridge(slug: string, opts: BridgeOptions = {}): () => void {
  async function handle(req: BridgeRequest): Promise<{ data?: unknown; error?: string }> {
    try {
      switch (req.op) {
        case "describe": {
          const proj = await getLocalProject(slug);
          return { data: { schema: proj?.schema ?? { tables: [] }, app: null } };
        }
        case "tables":
          return { data: await listLocalTables(slug) };
        case "list":
          if (!req.table) return { error: "missing table" };
          return { data: (await browseLocalTable(slug, req.table, req.limit ?? 200)).rows };
        case "insert":
          if (!req.table || !req.doc) return { error: "missing table/doc" };
          await insertLocalRow(slug, req.table, req.doc);
          opts.onMutate?.();
          return { data: { ok: true } };
        case "update":
          if (!req.table || !req.doc) return { error: "missing table/doc" };
          await updateLocalRow(slug, req.table, req.rowId, req.doc);
          opts.onMutate?.();
          return { data: { ok: true } };
        case "delete":
          if (!req.table) return { error: "missing table" };
          await deleteLocalRow(slug, req.table, req.rowId);
          opts.onMutate?.();
          return { data: { ok: true } };
        default:
          return { error: `unknown op` };
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
