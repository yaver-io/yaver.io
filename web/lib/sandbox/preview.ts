"use client";

// preview.ts — builds the live app preview that runs in a sandboxed iframe.
//
// A generic renderer (RENDERER_SOURCE, written in TS) is compiled to IIFE JS by
// esbuild-wasm in the browser, then embedded in an iframe srcdoc together with
// the project's schema + app spec. The renderer talks to the parent via the
// data bridge (dataBridge.ts) to read/write the browser-local SQLite, so the
// preview is a real working app — list/add/delete against live rows — with no
// server. Custom source files can later be added to the esbuild input; v1
// compiles the generic renderer alone.

import type { PhoneAppSpec, PhoneSchema } from "@/lib/agent-client";
import { loadEsbuild } from "./cdn";

// The in-iframe app. Pure DOM (no framework) for a small, robust bundle.
// Reads window.__YAVER_APP__ and proxies all data ops to the parent window.
const RENDERER_SOURCE = String.raw`
type Req = { source: "yaver-sandbox-req"; id: string; op: string; table?: string; doc?: Record<string, unknown>; rowId?: unknown; limit?: number };
type Rep = { source: "yaver-sandbox-rep"; id: string; ok: boolean; data?: unknown; error?: string };

const pending = new Map<string, (r: Rep) => void>();
let seq = 0;

window.addEventListener("message", (e: MessageEvent) => {
  const d = e.data as Rep;
  if (!d || d.source !== "yaver-sandbox-rep") return;
  const fn = pending.get(d.id);
  if (fn) { pending.delete(d.id); fn(d); }
});

function call(op: string, extra: Partial<Req> = {}): Promise<unknown> {
  return new Promise((resolve, reject) => {
    const id = "r" + (++seq);
    pending.set(id, (r) => (r.ok ? resolve(r.data) : reject(new Error(r.error || "error"))));
    const msg: Req = { source: "yaver-sandbox-req", id, op, ...extra };
    parent.postMessage(msg, "*");
    setTimeout(() => { if (pending.has(id)) { pending.delete(id); reject(new Error("timeout")); } }, 8000);
  });
}

const app = (window as any).__YAVER_APP__ || { schema: { tables: [] }, app: {} };
const schema = app.schema || { tables: [] };
const appSpec = app.app || {};
const root = document.getElementById("root")!;

function tablesForNav(): string[] {
  const screens = appSpec.screens || [];
  const fromScreens = screens.map((s: any) => s.table).filter(Boolean);
  const set = new Set<string>(fromScreens.length ? fromScreens : schema.tables.map((t: any) => t.name));
  return Array.from(set);
}

function columnsFor(table: string): Array<{ name: string; type: string; primary?: boolean }> {
  const t = (schema.tables || []).find((x: any) => x.name === table);
  return t ? t.columns : [];
}

let active = tablesForNav()[0] || "";

async function render() {
  root.innerHTML = "";
  const nav = document.createElement("div");
  nav.className = "nav";
  for (const t of tablesForNav()) {
    const b = document.createElement("button");
    b.textContent = t;
    b.className = t === active ? "tab active" : "tab";
    b.onclick = () => { active = t; void render(); };
    nav.appendChild(b);
  }
  root.appendChild(nav);

  if (!active) {
    const empty = document.createElement("p");
    empty.className = "muted";
    empty.textContent = "No tables yet — define a schema to see the app.";
    root.appendChild(empty);
    return;
  }

  const cols = columnsFor(active);
  const screen = (appSpec.screens || []).find((s: any) => s.table === active);
  const title = document.createElement("h2");
  title.textContent = screen?.title || active;
  root.appendChild(title);

  // Add-row controls. A plain div + button (NOT a <form>) — the iframe sandbox
  // is allow-scripts only, which blocks form submission; a button onclick is
  // unaffected and keeps the sandbox maximally locked down.
  const formEl = document.createElement("div");
  formEl.className = "addform";
  const inputs: Record<string, HTMLInputElement> = {};
  for (const c of cols) {
    if (c.primary && (c as any).default) continue; // auto-generated id
    const wrap = document.createElement("label");
    wrap.textContent = c.name;
    const input = document.createElement("input");
    input.placeholder = c.type;
    input.onkeydown = (ev) => { if ((ev as KeyboardEvent).key === "Enter") void addRow(); };
    inputs[c.name] = input;
    wrap.appendChild(input);
    formEl.appendChild(wrap);
  }
  const submit = document.createElement("button");
  submit.type = "button";
  submit.textContent = "Add";
  async function addRow() {
    const doc: Record<string, unknown> = {};
    for (const [k, el] of Object.entries(inputs)) {
      if (el.value !== "") doc[k] = el.value;
    }
    try {
      await call("insert", { table: active, doc });
      await render();
    } catch (e) { alert("Insert failed: " + (e as Error).message); }
  }
  submit.onclick = () => void addRow();
  formEl.appendChild(submit);
  root.appendChild(formEl);

  // Rows table.
  let rows: Array<Record<string, unknown>> = [];
  try { rows = (await call("list", { table: active, limit: 200 })) as any[]; }
  catch (e) {
    const err = document.createElement("p");
    err.className = "muted";
    err.textContent = "Could not load rows: " + (e as Error).message;
    root.appendChild(err);
    return;
  }

  if (!rows.length) {
    const empty = document.createElement("p");
    empty.className = "muted";
    empty.textContent = screen?.emptyState || "No rows yet.";
    root.appendChild(empty);
    return;
  }

  const pk = (cols.find((c) => c.primary)?.name) || Object.keys(rows[0])[0];
  const table = document.createElement("table");
  const thead = document.createElement("thead");
  const htr = document.createElement("tr");
  for (const k of Object.keys(rows[0])) {
    const th = document.createElement("th");
    th.textContent = k;
    htr.appendChild(th);
  }
  htr.appendChild(document.createElement("th"));
  thead.appendChild(htr);
  table.appendChild(thead);
  const tbody = document.createElement("tbody");
  for (const r of rows) {
    const tr = document.createElement("tr");
    for (const v of Object.values(r)) {
      const td = document.createElement("td");
      td.textContent = v === null || v === undefined ? "—" : String(v);
      tr.appendChild(td);
    }
    const actTd = document.createElement("td");
    const del = document.createElement("button");
    del.textContent = "×";
    del.className = "del";
    del.onclick = async () => {
      try { await call("delete", { table: active, rowId: r[pk] }); await render(); }
      catch (e) { alert("Delete failed: " + (e as Error).message); }
    };
    actTd.appendChild(del);
    tr.appendChild(actTd);
    tbody.appendChild(tr);
  }
  table.appendChild(tbody);
  root.appendChild(table);
}

void render();
`;

const STYLES = `
  :root { color-scheme: light dark; }
  * { box-sizing: border-box; }
  body { font: 14px/1.5 system-ui, sans-serif; margin: 0; padding: 16px; }
  .nav { display: flex; gap: 8px; flex-wrap: wrap; margin-bottom: 16px; }
  .tab { border: 1px solid #8884; background: transparent; border-radius: 999px; padding: 4px 12px; cursor: pointer; }
  .tab.active { background: #6366f1; color: #fff; border-color: #6366f1; }
  h2 { margin: 0 0 12px; }
  .addform { display: flex; gap: 8px; flex-wrap: wrap; align-items: flex-end; margin-bottom: 16px; }
  .addform label { display: flex; flex-direction: column; font-size: 11px; opacity: .7; gap: 2px; }
  .addform input { padding: 6px 8px; border: 1px solid #8884; border-radius: 6px; background: transparent; color: inherit; }
  .addform button, .del { padding: 6px 12px; border-radius: 6px; border: 1px solid #6366f1; background: #6366f1; color: #fff; cursor: pointer; }
  .del { background: transparent; color: #ef4444; border-color: #ef444466; padding: 2px 8px; }
  table { width: 100%; border-collapse: collapse; font-size: 13px; }
  th, td { text-align: left; padding: 6px 10px; border-bottom: 1px solid #8882; }
  th { opacity: .6; font-weight: 600; }
  .muted { opacity: .6; }
`;

let cachedScript: string | null = null;

/** Compile the generic renderer with esbuild-wasm (cached for the session). */
async function buildPreviewScript(): Promise<string> {
  if (cachedScript) return cachedScript;
  const esbuild = await loadEsbuild();
  const { code } = await esbuild.transform(RENDERER_SOURCE, {
    loader: "ts",
    format: "iife",
    target: "es2018",
  });
  cachedScript = code;
  return code;
}

function escapeForScript(json: string): string {
  return json.replace(/</g, "\\u003c");
}

/** Build the full iframe srcdoc for a project's preview. */
export async function buildPreviewSrcdoc(schema: PhoneSchema, app: PhoneAppSpec): Promise<string> {
  const script = await buildPreviewScript();
  const data = escapeForScript(JSON.stringify({ schema, app }));
  return `<!doctype html><html><head><meta charset="utf-8"><style>${STYLES}</style></head>
<body><div id="root"></div>
<script>window.__YAVER_APP__ = ${data};</script>
<script>${script}</script>
</body></html>`;
}
