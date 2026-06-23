"use client";

// preview.ts — builds the live app preview that runs in a sandboxed iframe.
//
// A generic renderer (RENDERER_SOURCE, written in TS) is compiled to IIFE JS by
// esbuild-wasm in the browser, then embedded in an iframe srcdoc together with
// the project's schema + app spec. The renderer talks to the parent via the
// data bridge (dataBridge.ts) to read/write the browser-local SQLite, so the
// preview is a real working app — list/add/delete against live rows — with no
// server.
//
// TWO-MODE MODEL (the isolation contract — see
// docs/yaver-mini-figma-direct-manipulation.md §Isolation):
//   • RUN mode    — the app behaves normally. End-user drag affordances
//                   (swipe-delete / reorder) run IF the builder enabled them.
//                   ZERO design instrumentation is attached, so nothing can
//                   collide with the app's own gestures.
//   • DESIGN mode — a single "Design Glass" capture surface sits above the app
//                   and owns ALL input. The app's gestures never fire while
//                   editing, so the builder's select/drag can't collide with the
//                   app's drag. The glass is removed entirely in run mode.
// Because Yaver authors both the app and the overlay, the two are coordinated by
// one mode flag rather than two competing libraries.

import type { PhoneAppSpec, PhoneDesign, PhoneNodeUi, PhoneSchema } from "@/lib/agent-client";
import { loadEsbuild } from "./cdn";

/** Re-exported for host code that builds overrides. */
export type NodeUi = PhoneNodeUi;

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

function call(op: string, extra: Partial<Req> = {}): Promise<any> {
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
const readOnly = !!app.readOnly;
const designMode = !!app.designMode;
const design = app.design || {};
const ui: Record<string, any> = design.ui || {};
const layout: string[] | null = (design.layout && design.layout.length) ? design.layout : null;
let selectedId: string | null = app.selectedId || null;
const root = document.getElementById("root")!;

function nodeUi(id: string) { return ui[id] || {}; }

// Mark a rendered element as an addressable widget node and apply host overrides.
function tag(el: HTMLElement, id: string, kind: string) {
  el.setAttribute("data-ynode", id);
  el.setAttribute("data-ykind", kind);
  const u = nodeUi(id);
  if (u.hidden) el.style.display = "none";
  if (typeof u.marginTop === "number") el.style.marginTop = u.marginTop + "px";
}

// Post a design event to the host (namespaced — never collides with data ops).
function postDesign(op: string, extra: Record<string, unknown>) {
  parent.postMessage(Object.assign({ source: "yaver-design-evt", op }, extra), "*");
}
function serialRect(r: DOMRect | null) {
  return r ? { x: r.left, y: r.top, w: r.width, h: r.height } : undefined;
}
function rectOf(id: string): DOMRect | null {
  const el = document.querySelector('[data-ynode="' + id + '"]') as HTMLElement | null;
  return el ? el.getBoundingClientRect() : null;
}

function tablesForNav(): string[] {
  const screens = appSpec.screens || [];
  const fromScreens = screens.map((s: any) => s.table).filter(Boolean);
  const set = new Set<string>(fromScreens.length ? fromScreens : schema.tables.map((t: any) => t.name));
  return Array.from(set);
}
function columnsFor(table: string): Array<{ name: string; type: string; primary?: boolean; default?: string }> {
  const t = (schema.tables || []).find((x: any) => x.name === table);
  return t ? t.columns : [];
}

let active = tablesForNav()[0] || "";

async function render() {
  root.innerHTML = "";
  const blocks: Record<string, HTMLElement> = {};

  const nav = document.createElement("div");
  nav.className = "nav";
  tag(nav, "nav", "Nav");
  for (const t of tablesForNav()) {
    const b = document.createElement("button");
    b.textContent = t;
    b.className = t === active ? "tab active" : "tab";
    b.onclick = () => { if (designMode) return; active = t; void render(); };
    nav.appendChild(b);
  }
  blocks["nav"] = nav;

  if (!active) {
    root.appendChild(nav);
    const empty = document.createElement("p");
    empty.className = "muted";
    empty.textContent = "No tables yet — define a schema to see the app.";
    root.appendChild(empty);
    if (designMode) buildGlass();
    return;
  }

  const cols = columnsFor(active);
  const screen = (appSpec.screens || []).find((s: any) => s.table === active);

  const title = document.createElement("h2");
  title.textContent = nodeUi("title").title || (screen && screen.title) || active;
  tag(title, "title", "Header");
  blocks["title"] = title;

  // QuickAdd. A plain div + button (NOT a <form>) — the iframe sandbox is
  // allow-scripts only, which blocks form submission. Skipped in read-only mode.
  if (!readOnly) {
    const formEl = document.createElement("div");
    formEl.className = "addform";
    tag(formEl, "quickadd", "QuickAdd");
    const inputs: Record<string, HTMLInputElement> = {};
    for (const c of cols) {
      if (c.primary && c.default) continue; // auto-generated id
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
      for (const k of Object.keys(inputs)) { if (inputs[k].value !== "") doc[k] = inputs[k].value; }
      try { await call("insert", { table: active, doc }); await render(); }
      catch (e) { alert("Insert failed: " + (e as Error).message); }
    }
    submit.onclick = () => void addRow();
    formEl.appendChild(submit);
    blocks["quickadd"] = formEl;
  }

  const listWrap = document.createElement("div");
  listWrap.className = "listwrap";
  tag(listWrap, "list", "List");
  blocks["list"] = listWrap;

  // Append blocks in the builder-defined order, then any leftovers.
  const order = layout || ["nav", "title", "quickadd", "list"];
  const seen: Record<string, boolean> = {};
  for (const id of order) { if (blocks[id]) { root.appendChild(blocks[id]); seen[id] = true; } }
  for (const id of Object.keys(blocks)) { if (!seen[id]) root.appendChild(blocks[id]); }

  await renderRows(listWrap, cols, screen);

  if (designMode) buildGlass();
}

async function renderRows(listWrap: HTMLElement, cols: any[], screen: any) {
  let rows: Array<Record<string, unknown>> = [];
  try { rows = (await call("list", { table: active, limit: 200 })) as any[]; }
  catch (e) {
    const err = document.createElement("p");
    err.className = "muted";
    err.textContent = "Could not load rows: " + (e as Error).message;
    listWrap.appendChild(err);
    return;
  }
  if (!rows.length) {
    const empty = document.createElement("p");
    empty.className = "muted";
    empty.textContent = (screen && screen.emptyState) || "No rows yet.";
    listWrap.appendChild(empty);
    return;
  }

  const pk = (cols.find((c: any) => c.primary) || {}).name || Object.keys(rows[0])[0];

  // Kanban board: if the builder set list.board.groupBy and the column exists,
  // render columns of cards instead of a table. End users drag cards between
  // columns (run mode), persisting via update of the group-by column.
  const lu = nodeUi("list");
  const groupBy = lu.board && lu.board.groupBy ? lu.board.groupBy : null;
  if (groupBy && Object.keys(rows[0]).indexOf(groupBy) >= 0) {
    renderBoard(listWrap, rows, pk, groupBy);
    return;
  }

  const table = document.createElement("table");
  const thead = document.createElement("thead");
  const htr = document.createElement("tr");
  for (const k of Object.keys(rows[0])) { const th = document.createElement("th"); th.textContent = k; htr.appendChild(th); }
  if (!readOnly) htr.appendChild(document.createElement("th"));
  thead.appendChild(htr);
  table.appendChild(thead);

  const tbody = document.createElement("tbody");
  // End-user runtime affordances run ONLY in run mode, and ONLY if the builder
  // enabled them. In design mode the glass owns input, so these never attach.
  // (lu was declared above for the board check — reuse it.)
  const swipeOn = !readOnly && !designMode && !!lu.swipeDelete;
  for (const r of rows) {
    const tr = document.createElement("tr");
    for (const v of Object.values(r)) {
      const td = document.createElement("td");
      td.textContent = v === null || v === undefined ? "—" : String(v);
      tr.appendChild(td);
    }
    if (!readOnly) {
      const actTd = document.createElement("td");
      const del = document.createElement("button");
      del.textContent = "×";
      del.className = "del";
      del.onclick = async () => {
        if (designMode) return;
        try { await call("delete", { table: active, rowId: r[pk] }); await render(); }
        catch (e) { alert("Delete failed: " + (e as Error).message); }
      };
      actTd.appendChild(del);
      tr.appendChild(actTd);
    }
    if (swipeOn) {
      enableSwipeDelete(tr, async () => {
        try { await call("delete", { table: active, rowId: r[pk] }); await render(); } catch (e) { /* ignore */ }
      });
    }
    tbody.appendChild(tr);
  }
  table.appendChild(tbody);
  listWrap.appendChild(table);
}

// Kanban board renderer. Groups rows by a column into draggable columns.
function renderBoard(listWrap: HTMLElement, rows: Array<Record<string, unknown>>, pk: string, groupBy: string) {
  const groups: Record<string, Array<Record<string, unknown>>> = {};
  const orderVals: string[] = [];
  for (const r of rows) {
    const v = r[groupBy];
    const key = v === null || v === undefined || v === "" ? "—" : String(v);
    if (!groups[key]) { groups[key] = []; orderVals.push(key); }
    groups[key].push(r);
  }
  const board = document.createElement("div");
  board.className = "board";
  for (const key of orderVals) {
    const col = document.createElement("div");
    col.className = "bcol";
    col.setAttribute("data-col", key);
    const h = document.createElement("div");
    h.className = "bcolh";
    h.textContent = key + " (" + groups[key].length + ")";
    col.appendChild(h);
    for (const r of groups[key]) {
      const card = document.createElement("div");
      card.className = "bcard";
      card.setAttribute("data-row-id", String(r[pk]));
      const fields = Object.keys(r).filter((k) => k !== pk && k !== groupBy).slice(0, 3);
      const main = document.createElement("div");
      main.className = "bcardmain";
      const head = fields.length ? r[fields[0]] : r[pk];
      main.textContent = head === null || head === undefined ? String(r[pk]) : String(head);
      card.appendChild(main);
      for (let i = 1; i < fields.length; i++) {
        const s = document.createElement("div");
        s.className = "bcardsub";
        const v = r[fields[i]];
        s.textContent = fields[i] + ": " + (v === null || v === undefined ? "—" : String(v));
        card.appendChild(s);
      }
      if (!readOnly && !designMode) enableCardDrag(card, r[pk], groupBy);
      col.appendChild(card);
    }
    board.appendChild(col);
  }
  listWrap.appendChild(board);
}

// End-user kanban drag: pick a card, drop it on another column → update the
// group-by column to that column's value. Run mode only.
function enableCardDrag(card: HTMLElement, rowId: unknown, groupBy: string) {
  let dragging = false;
  card.style.touchAction = "none";
  card.addEventListener("pointerdown", (e) => {
    dragging = true;
    card.style.opacity = "0.6";
    try { card.setPointerCapture(e.pointerId); } catch (_) {}
  });
  card.addEventListener("pointerup", async (e) => {
    if (!dragging) return;
    dragging = false;
    card.style.opacity = "";
    try { card.releasePointerCapture(e.pointerId); } catch (_) {}
    const stack = document.elementsFromPoint(e.clientX, e.clientY) as HTMLElement[];
    let colEl: HTMLElement | null = null;
    for (const el of stack) { const c = el.closest ? (el.closest("[data-col]") as HTMLElement | null) : null; if (c) { colEl = c; break; } }
    if (!colEl) return;
    const target = colEl.getAttribute("data-col");
    if (!target || target === "—") return;
    const doc: Record<string, unknown> = {};
    doc[groupBy] = target;
    try { await call("update", { table: active, rowId, doc }); await render(); }
    catch (err) { alert("Move failed: " + (err as Error).message); }
  });
}

// End-user swipe-to-delete (runtime). Horizontal drag past threshold deletes.
// Attached only in run mode when the builder enabled it (see swipeOn).
function enableSwipeDelete(tr: HTMLElement, onDelete: () => Promise<void>) {
  let x0: number | null = null;
  tr.style.touchAction = "pan-y";
  tr.addEventListener("pointerdown", (e) => { x0 = e.clientX; try { tr.setPointerCapture(e.pointerId); } catch (_) {} });
  tr.addEventListener("pointermove", (e) => {
    if (x0 === null) return;
    const dx = e.clientX - x0;
    if (dx < 0) { tr.style.transform = "translateX(" + dx + "px)"; tr.style.opacity = String(Math.max(0.2, 1 + dx / 200)); }
  });
  tr.addEventListener("pointerup", (e) => {
    if (x0 === null) return;
    const dx = e.clientX - x0;
    tr.style.transform = "";
    tr.style.opacity = "";
    if (dx < -120) void onDelete();
    x0 = null;
  });
}

// ── Design Glass — the isolation layer. One capture surface above the app that
// exists ONLY in design mode; it owns all input so the app's own gestures never
// fire. Select = click; reorder = drag (insert before the block nearest pointer
// Y). All edits leave as namespaced "yaver-design-evt" messages; the host owns
// the inspector, persistence, and undo. ─────────────────────────────────────
let glassEl: HTMLElement | null = null;
function removeGlass() { if (glassEl && glassEl.parentNode) glassEl.parentNode.removeChild(glassEl); glassEl = null; }
function ynodeAt(x: number, y: number): HTMLElement | null {
  const stack = document.elementsFromPoint(x, y) as HTMLElement[];
  for (const el of stack) { const n = el.closest ? (el.closest("[data-ynode]") as HTMLElement | null) : null; if (n) return n; }
  return null;
}
function currentOrder(): string[] {
  return Array.prototype.slice.call(root.children)
    .map((el: any) => (el.getAttribute ? el.getAttribute("data-ynode") : null))
    .filter(Boolean) as string[];
}
function nearestBeforeId(y: number): string | null {
  for (const id of currentOrder()) {
    const r = rectOf(id);
    if (r && y < r.top + r.height / 2) return id;
  }
  return null; // drop at end
}
function buildGlass() {
  removeGlass();
  const glass = document.createElement("div");
  glass.id = "design-glass";
  document.body.appendChild(glass);
  glassEl = glass;
  const box = document.createElement("div");
  box.id = "design-selbox";
  glass.appendChild(box);
  function showBox(id: string | null) {
    const r = id ? rectOf(id) : null;
    if (!r) { box.style.display = "none"; return; }
    box.style.display = "block";
    box.style.left = r.left + "px";
    box.style.top = r.top + "px";
    box.style.width = r.width + "px";
    box.style.height = r.height + "px";
  }
  showBox(selectedId);

  let dragId: string | null = null;
  let downY = 0;
  let moved = false;
  glass.addEventListener("pointerdown", (e) => {
    const n = ynodeAt(e.clientX, e.clientY);
    if (!n) return;
    const id = n.getAttribute("data-ynode");
    selectedId = id;
    showBox(id);
    postDesign("select", { nodeId: id, kind: n.getAttribute("data-ykind"), rect: serialRect(rectOf(id!)) });
    dragId = id;
    downY = e.clientY;
    moved = false;
    try { glass.setPointerCapture(e.pointerId); } catch (_) {}
  });
  glass.addEventListener("pointermove", (e) => {
    if (dragId === null) return;
    if (Math.abs(e.clientY - downY) > 6) { moved = true; glass.style.cursor = "grabbing"; }
  });
  glass.addEventListener("pointerup", (e) => {
    glass.style.cursor = "";
    if (dragId !== null && moved) {
      const beforeId = nearestBeforeId(e.clientY);
      if (beforeId !== dragId) postDesign("moved", { nodeId: dragId, beforeId });
    }
    dragId = null;
  });
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
  tr { background: var(--rowbg, transparent); }
  /* Kanban board */
  .board { display: flex; gap: 12px; overflow-x: auto; padding-bottom: 8px; align-items: flex-start; }
  .bcol { min-width: 150px; flex: 1; background: #8881; border-radius: 8px; padding: 8px; }
  .bcolh { font-size: 11px; font-weight: 600; opacity: .65; margin-bottom: 8px; text-transform: uppercase; }
  .bcard { background: #ffffff14; border: 1px solid #8883; border-radius: 6px; padding: 8px; margin-bottom: 8px; cursor: grab; }
  .bcardmain { font-size: 13px; font-weight: 500; }
  .bcardsub { font-size: 11px; opacity: .6; margin-top: 2px; }
  /* Design Glass: the isolation capture layer (design mode only). */
  #design-glass { position: fixed; inset: 0; z-index: 2147483000; cursor: grab; background: transparent; }
  #design-selbox { position: fixed; display: none; border: 2px solid #6366f1; background: #6366f11f; pointer-events: none; box-sizing: border-box; border-radius: 4px; }
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

/** Build the full iframe srcdoc for a project's preview.
 *  - readOnly hides mutation controls (friend "USE the app" previews).
 *  - designMode turns on the Design Glass (select + drag-reorder reporting).
 *  - design carries persisted layout order + per-node overrides.
 *  - selectedId keeps the selection box across re-renders. */
export async function buildPreviewSrcdoc(
  schema: PhoneSchema,
  app: PhoneAppSpec,
  opts?: { readOnly?: boolean; designMode?: boolean; design?: PhoneDesign; selectedId?: string | null },
): Promise<string> {
  const script = await buildPreviewScript();
  const data = escapeForScript(
    JSON.stringify({
      schema,
      app,
      readOnly: !!opts?.readOnly,
      designMode: !!opts?.designMode,
      design: opts?.design || app.design || {},
      selectedId: opts?.selectedId || null,
    }),
  );
  return `<!doctype html><html><head><meta charset="utf-8"><style>${STYLES}</style></head>
<body><div id="root"></div>
<script>window.__YAVER_APP__ = ${data};</script>
<script>${script}</script>
</body></html>`;
}
