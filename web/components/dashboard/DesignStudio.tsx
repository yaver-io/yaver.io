"use client";

// DesignStudio — the shared mini-figma panel (preview + Design Glass + inspector
// + undo/redo + AI). It is backend-agnostic: BrowserSandbox drives it against the
// browser-local sandbox; PhoneProjectsView drives it against an agent-hosted
// project over the relay. Both flow edits through one applyDesign + history.

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import type { PhoneAppSpec, PhoneDesign, PhoneDesignPatch, PhoneSchema } from "@/lib/agent-client";
import { buildPreviewSrcdoc, type NodeUi } from "@/lib/sandbox/preview";
import { attachDesignBridge, type DesignEvent } from "@/lib/sandbox/designBridge";
import { widgetDef } from "@/lib/sandbox/widgets";

export const DEFAULT_ORDER = ["nav", "title", "quickadd", "list"];

export function reorderLayout(order: string[], nodeId: string, beforeId: string | null): string[] {
  const arr = order.filter((id) => id !== nodeId);
  if (beforeId === null) return [...arr, nodeId];
  const idx = arr.indexOf(beforeId);
  if (idx < 0) return [...arr, nodeId];
  arr.splice(idx, 0, nodeId);
  return arr;
}

/** A data/design source for the studio (local sandbox or agent relay). */
export interface DesignBackend {
  loadSchemaApp: () => Promise<{ schema: PhoneSchema; app: PhoneAppSpec } | null>;
  attachData: (onMutate: () => void) => () => void;
  loadDesign: () => Promise<PhoneDesign>;
  saveDesign: (design: PhoneDesign) => Promise<void>;
}

function err(e: unknown, fallback: string): string {
  const raw = e instanceof Error ? e.message : typeof e === "string" ? e : "";
  return raw.trim() && raw.trim().length <= 200 ? raw.trim() : fallback;
}

function LivePreview({
  backend,
  onMutate,
  designMode,
  design,
  selectedId,
  onEvent,
}: {
  backend: DesignBackend;
  onMutate: () => void;
  designMode: boolean;
  design: PhoneDesign;
  selectedId: string | null;
  onEvent: (e: DesignEvent) => void;
}) {
  const [srcDoc, setSrcDoc] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const iframeRef = useRef<HTMLIFrameElement | null>(null);

  // selectedId is read on (re)build to restore the selection box, but it must
  // NOT be a rebuild trigger: selecting fires on pointer-down, and reloading the
  // iframe mid-gesture would kill an in-progress drag-reorder. The glass draws
  // the box locally on click, so a ref keeps the latest value without rebuilding.
  const selectedRef = useRef(selectedId);
  selectedRef.current = selectedId;

  useEffect(() => {
    let cancelled = false;
    (async () => {
      try {
        const sa = await backend.loadSchemaApp();
        if (!sa) throw new Error("project not found");
        const doc = await buildPreviewSrcdoc(sa.schema, sa.app, { designMode, design, selectedId: selectedRef.current });
        if (!cancelled) setSrcDoc(doc);
      } catch (e) {
        if (!cancelled) setError(err(e, "Preview failed to build."));
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [backend, designMode, design]);

  useEffect(() => backend.attachData(onMutate), [backend, onMutate]);

  useEffect(() => {
    if (!designMode) return;
    return attachDesignBridge(onEvent);
  }, [designMode, onEvent]);

  if (error) {
    return <div className="rounded border border-red-500/30 bg-red-500/10 p-3 text-xs text-red-300">{error}</div>;
  }
  if (!srcDoc) {
    return <div className="rounded border border-surface-800 bg-surface-950 p-4 text-sm text-surface-500">Compiling preview…</div>;
  }
  return (
    <iframe
      ref={iframeRef}
      title="App preview"
      sandbox="allow-scripts"
      srcDoc={srcDoc}
      className="h-[480px] w-full rounded border border-surface-800 bg-white dark:bg-surface-950"
    />
  );
}

function Inspector({
  sel,
  design,
  order,
  columns,
  onPatch,
  onMove,
}: {
  sel: { nodeId: string; kind: string } | null;
  design: PhoneDesign;
  order: string[];
  columns: string[];
  onPatch: (nodeId: string, patch: Partial<NodeUi>) => void;
  onMove: (nodeId: string, dir: -1 | 1) => void;
}) {
  if (!sel) {
    return (
      <div className="rounded border border-dashed border-surface-800 bg-surface-950 p-4 text-xs text-surface-500">
        Click a widget in the preview to edit it — drag it to reorder, or use the controls here. No chat, no tokens.
      </div>
    );
  }
  const def = widgetDef(sel.kind);
  const u = (design.ui ?? {})[sel.nodeId] ?? {};
  const pos = order.indexOf(sel.nodeId);
  const ctl = def.controls;
  return (
    <div className="flex flex-col gap-3 rounded border border-surface-800 bg-surface-900 p-3 text-sm">
      <div>
        <div className="font-medium text-surface-100">{def.label}</div>
        <div className="text-[11px] text-surface-500">{def.description}</div>
      </div>

      {def.design.movable ? (
        <div className="flex items-center gap-2 text-xs text-surface-400">
          Order
          <button
            disabled={pos <= 0}
            onClick={() => onMove(sel.nodeId, -1)}
            className="rounded border border-surface-700 px-2 py-0.5 text-surface-200 disabled:opacity-40 hover:bg-surface-800"
          >
            ↑
          </button>
          <button
            disabled={pos < 0 || pos >= order.length - 1}
            onClick={() => onMove(sel.nodeId, 1)}
            className="rounded border border-surface-700 px-2 py-0.5 text-surface-200 disabled:opacity-40 hover:bg-surface-800"
          >
            ↓
          </button>
        </div>
      ) : null}

      {ctl.includes("title") ? (
        <label className="flex flex-col gap-1 text-xs text-surface-400">
          Title
          <input
            value={u.title ?? ""}
            onChange={(e) => onPatch(sel.nodeId, { title: e.target.value || undefined })}
            placeholder="(default)"
            className="rounded border border-surface-700 bg-surface-950 px-2 py-1.5 text-sm text-surface-100"
          />
        </label>
      ) : null}

      {ctl.includes("spacing") ? (
        <label className="flex flex-col gap-1 text-xs text-surface-400">
          Space above: {u.marginTop ?? 0}px
          <input
            type="range"
            min={0}
            max={64}
            value={u.marginTop ?? 0}
            onChange={(e) => onPatch(sel.nodeId, { marginTop: Number(e.target.value) || undefined })}
          />
        </label>
      ) : null}

      {ctl.includes("hidden") ? (
        <label className="flex items-center gap-2 text-xs text-surface-300">
          <input
            type="checkbox"
            checked={!!u.hidden}
            onChange={(e) => onPatch(sel.nodeId, { hidden: e.target.checked || undefined })}
          />
          Hide this widget
        </label>
      ) : null}

      {ctl.includes("board") ? (
        <label className="flex flex-col gap-1 text-xs text-surface-400">
          Show as kanban board grouped by
          <select
            value={u.board?.groupBy ?? ""}
            onChange={(e) => onPatch(sel.nodeId, { board: e.target.value ? { groupBy: e.target.value } : undefined })}
            className="rounded border border-surface-700 bg-surface-950 px-2 py-1.5 text-sm text-surface-100"
          >
            <option value="">— off (table view) —</option>
            {columns.map((cn) => (
              <option key={cn} value={cn}>
                {cn}
              </option>
            ))}
          </select>
        </label>
      ) : null}

      {ctl.includes("reorderable") || ctl.includes("swipeDelete") ? (
        <div className="flex flex-col gap-1.5 border-t border-surface-800 pt-2">
          <div className="text-[11px] uppercase tracking-wide text-surface-500">End-user drag (yaver draggable mode)</div>
          {ctl.includes("reorderable") ? (
            <label className="flex items-center gap-2 text-xs text-surface-300">
              <input
                type="checkbox"
                checked={!!u.reorderable}
                onChange={(e) => onPatch(sel.nodeId, { reorderable: e.target.checked || undefined })}
              />
              Let users drag to reorder
            </label>
          ) : null}
          {ctl.includes("swipeDelete") ? (
            <label className="flex items-center gap-2 text-xs text-surface-300">
              <input
                type="checkbox"
                checked={!!u.swipeDelete}
                onChange={(e) => onPatch(sel.nodeId, { swipeDelete: e.target.checked || undefined })}
              />
              Let users swipe a row to delete
            </label>
          ) : null}
        </div>
      ) : null}
    </div>
  );
}

/** The full design studio panel. Render inside a "Preview" area. */
export function DesignStudioPanel({
  backend,
  columns,
  aiDraft,
  onDataMutate,
}: {
  backend: DesignBackend;
  columns: string[];
  /** Optional NL→patch drafter (AI). Omit to hide the AI input. ctx carries the
   * selected widget so a prompt like "change this" scopes to it. */
  aiDraft?: (text: string, ctx?: { nodeId: string; kind: string }) => Promise<PhoneDesignPatch[]>;
  /** Called after a preview data mutation so the host can refresh its browser. */
  onDataMutate?: () => void;
}) {
  const [designMode, setDesignMode] = useState(false);
  const [design, setDesign] = useState<PhoneDesign>({});
  const [selNode, setSelNode] = useState<{ nodeId: string; kind: string } | null>(null);
  const [undoStack, setUndoStack] = useState<PhoneDesign[]>([]);
  const [redoStack, setRedoStack] = useState<PhoneDesign[]>([]);
  const [designPrompt, setDesignPrompt] = useState("");
  const [busy, setBusy] = useState(false);
  const [notice, setNotice] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;
    setSelNode(null);
    setUndoStack([]);
    setRedoStack([]);
    backend
      .loadDesign()
      .then((d) => { if (!cancelled) setDesign(d ?? {}); })
      .catch(() => { if (!cancelled) setDesign({}); });
    return () => { cancelled = true; };
  }, [backend]);

  const persist = useCallback((next: PhoneDesign) => { void backend.saveDesign(next).catch(() => {}); }, [backend]);

  const applyDesign = useCallback(
    (next: PhoneDesign) => {
      setDesign((prev) => {
        setUndoStack((s) => [...s, prev]);
        return next;
      });
      setRedoStack([]);
      persist(next);
    },
    [persist],
  );

  const patchNode = useCallback(
    (nodeId: string, patch: Partial<NodeUi>) => {
      setDesign((prev) => {
        const ui = { ...(prev.ui ?? {}) };
        ui[nodeId] = { ...(ui[nodeId] ?? {}), ...patch };
        const next: PhoneDesign = { ...prev, ui };
        setUndoStack((s) => [...s, prev]);
        persist(next);
        return next;
      });
      setRedoStack([]);
    },
    [persist],
  );

  const effectiveOrder = useMemo(() => (design.layout?.length ? design.layout : DEFAULT_ORDER), [design.layout]);

  const moveNode = useCallback(
    (nodeId: string, dir: -1 | 1) => {
      const order = [...effectiveOrder];
      const i = order.indexOf(nodeId);
      const j = i + dir;
      if (i < 0 || j < 0 || j >= order.length) return;
      [order[i], order[j]] = [order[j], order[i]];
      applyDesign({ ...design, layout: order });
    },
    [effectiveOrder, design, applyDesign],
  );

  const onDesignEvent = useCallback(
    (e: DesignEvent) => {
      if (e.op === "select") {
        setSelNode({ nodeId: e.nodeId, kind: e.kind });
        return;
      }
      const order = design.layout?.length ? design.layout : DEFAULT_ORDER;
      applyDesign({ ...design, layout: reorderLayout(order, e.nodeId, e.beforeId) });
    },
    [design, applyDesign],
  );

  const applyDesignPatches = useCallback(
    (patches: PhoneDesignPatch[]) => {
      let next = design;
      for (const p of patches) {
        if (p.op === "set") {
          const ui = { ...(next.ui ?? {}) };
          ui[p.nodeId] = { ...(ui[p.nodeId] ?? {}), ...p.props };
          next = { ...next, ui };
        } else if (p.op === "move") {
          const order = next.layout?.length ? next.layout : DEFAULT_ORDER;
          next = { ...next, layout: reorderLayout(order, p.nodeId, p.beforeId) };
        } else if (p.op === "enable") {
          const ui = { ...(next.ui ?? {}) };
          const flag = p.affordance === "reorder" ? { reorderable: true } : { swipeDelete: true };
          ui[p.nodeId] = { ...(ui[p.nodeId] ?? {}), ...flag };
          next = { ...next, ui };
        }
      }
      applyDesign(next);
    },
    [design, applyDesign],
  );

  const undo = useCallback(() => {
    setUndoStack((s) => {
      if (!s.length) return s;
      const prev = s[s.length - 1];
      setRedoStack((r) => [...r, design]);
      setDesign(prev);
      persist(prev);
      return s.slice(0, -1);
    });
  }, [design, persist]);

  const redo = useCallback(() => {
    setRedoStack((r) => {
      if (!r.length) return r;
      const next = r[r.length - 1];
      setUndoStack((s) => [...s, design]);
      setDesign(next);
      persist(next);
      return r.slice(0, -1);
    });
  }, [design, persist]);

  const runAI = useCallback(async () => {
    const text = designPrompt.trim();
    if (!text || !aiDraft) return;
    setBusy(true);
    setNotice(null);
    try {
      const ctx = selNode ? { nodeId: selNode.nodeId, kind: selNode.kind } : undefined;
      const patches = await aiDraft(text, ctx);
      applyDesignPatches(patches);
      setDesignPrompt("");
    } catch (e) {
      setNotice(err(e, "Couldn't apply that layout change."));
    } finally {
      setBusy(false);
    }
  }, [designPrompt, aiDraft, applyDesignPatches, selNode]);

  const onMutate = useCallback(() => { onDataMutate?.(); }, [onDataMutate]);

  return (
    <div className="flex flex-col gap-2">
      <div className="flex flex-wrap items-center gap-3">
        <label className="flex items-center gap-2 text-xs text-surface-300">
          <input
            type="checkbox"
            checked={designMode}
            onChange={(e) => {
              setDesignMode(e.target.checked);
              setSelNode(null);
            }}
          />
          Design mode — select, drag &amp; edit widgets
        </label>
        {designMode ? (
          <>
            <span className="text-[11px] text-surface-500">Click to select, drag to reorder. Edits persist &amp; ship on deploy.</span>
            <div className="ml-auto flex items-center gap-1">
              <button
                disabled={!undoStack.length}
                onClick={undo}
                className="rounded border border-surface-700 px-2 py-0.5 text-xs text-surface-200 disabled:opacity-40 hover:bg-surface-800"
              >
                Undo
              </button>
              <button
                disabled={!redoStack.length}
                onClick={redo}
                className="rounded border border-surface-700 px-2 py-0.5 text-xs text-surface-200 disabled:opacity-40 hover:bg-surface-800"
              >
                Redo
              </button>
            </div>
          </>
        ) : null}
      </div>

      {notice ? <div className="rounded border border-red-500/30 bg-red-500/10 p-2 text-xs text-red-300">{notice}</div> : null}

      {designMode && aiDraft ? (
        <div className="flex items-center gap-2">
          {selNode ? (
            <span className="shrink-0 rounded-full bg-indigo-500/15 px-2 py-1 text-[11px] text-indigo-300">
              Editing: {widgetDef(selNode.kind).label}
            </span>
          ) : null}
          <input
            value={designPrompt}
            onChange={(e) => setDesignPrompt(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "Enter") void runAI();
            }}
            placeholder={
              selNode
                ? `Change the selected ${widgetDef(selNode.kind).label.toLowerCase()} — e.g. “add space above”, “let users reorder”`
                : "Ask AI to tweak the layout — e.g. “move quick-add below the list, let users reorder”"
            }
            className="flex-1 rounded border border-surface-700 bg-surface-950 px-3 py-1.5 text-xs text-surface-100"
          />
          <button
            disabled={busy || !designPrompt.trim()}
            onClick={() => void runAI()}
            className="rounded bg-indigo-600 px-3 py-1.5 text-xs font-medium text-white disabled:opacity-50 hover:bg-indigo-500"
          >
            {busy ? "Applying…" : "Apply"}
          </button>
        </div>
      ) : null}

      <div className={designMode ? "grid grid-cols-1 gap-3 lg:grid-cols-[1fr_260px]" : ""}>
        <LivePreview
          backend={backend}
          onMutate={onMutate}
          designMode={designMode}
          design={design}
          selectedId={selNode?.nodeId ?? null}
          onEvent={onDesignEvent}
        />
        {designMode ? (
          <Inspector sel={selNode} design={design} order={effectiveOrder} columns={columns} onPatch={patchNode} onMove={moveNode} />
        ) : null}
      </div>
    </div>
  );
}
