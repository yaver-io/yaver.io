"use client";

// ToolPanelView — the generic, schema-driven tool surface. Instead of a
// hand-written View.tsx per feature, this lists every `ops` verb the connected
// agent published via /ops/verbs and renders each one's JSON-Schema payload as
// a native form (SchemaForm). Submitting calls the same agentClient.callOps
// pipe the bespoke panels use. External / connected-project MCP tools that the
// agent merges show up here too, since their inputSchema is JSON Schema all the
// same.
//
// This is the long-tail surface: bespoke panels (Circuit, Remote Desktop,
// Stores) stay as-is; everything else gets a form for free.

import { useEffect, useMemo, useState } from "react";
import { agentClient } from "@/lib/agent-client";
import SchemaForm, { type JSONSchema } from "./SchemaForm";

type Verb = {
  name: string;
  description?: string;
  streaming?: boolean;
  allowGuest?: boolean;
  payload?: JSONSchema;
};

function uiHint(v: Verb): Record<string, any> {
  return (v.payload && (v.payload as any)["x-yaver-ui"]) || {};
}

// A verb shows on web unless it declares surfaces that exclude "web".
function showsOnWeb(v: Verb): boolean {
  const s = uiHint(v).surfaces;
  if (!Array.isArray(s) || s.length === 0) return true;
  return s.includes("web");
}

function groupOf(v: Verb): string {
  const g = uiHint(v).group;
  if (typeof g === "string" && g) return g;
  const idx = v.name.indexOf("_");
  return idx > 0 ? v.name.slice(0, idx) : v.name;
}

function titleOf(v: Verb): string {
  return uiHint(v).title || v.name;
}

export default function ToolPanelView() {
  const [verbs, setVerbs] = useState<Verb[]>([]);
  const [loading, setLoading] = useState(true);
  const [loadErr, setLoadErr] = useState<string | null>(null);
  const [query, setQuery] = useState("");
  const [selected, setSelected] = useState<string | null>(null);

  const [busy, setBusy] = useState(false);
  const [result, setResult] = useState<{ ok: boolean; text: string } | null>(null);

  useEffect(() => {
    let alive = true;
    (async () => {
      setLoading(true);
      setLoadErr(null);
      try {
        const list = await agentClient.getOpsVerbs();
        if (!alive) return;
        setVerbs(list.filter(showsOnWeb));
      } catch (e: any) {
        if (alive) setLoadErr(e?.message || String(e));
      } finally {
        if (alive) setLoading(false);
      }
    })();
    return () => {
      alive = false;
    };
  }, []);

  const filtered = useMemo(() => {
    const q = query.trim().toLowerCase();
    if (!q) return verbs;
    return verbs.filter(
      (v) =>
        v.name.toLowerCase().includes(q) ||
        (v.description || "").toLowerCase().includes(q) ||
        groupOf(v).toLowerCase().includes(q),
    );
  }, [verbs, query]);

  const groups = useMemo(() => {
    const m = new Map<string, Verb[]>();
    for (const v of filtered) {
      const g = groupOf(v);
      if (!m.has(g)) m.set(g, []);
      m.get(g)!.push(v);
    }
    return [...m.entries()].sort((a, b) => a[0].localeCompare(b[0]));
  }, [filtered]);

  const active = useMemo(() => verbs.find((v) => v.name === selected) || null, [verbs, selected]);

  async function run(payload: any) {
    if (!active) return;
    if (uiHint(active).confirm) {
      if (!window.confirm(`Run ${active.name}? This action is marked destructive.`)) return;
    }
    setBusy(true);
    setResult(null);
    try {
      const res = await agentClient.callOps(active.name, payload);
      const ok = res.ok !== false;
      setResult({
        ok,
        text: JSON.stringify(res.initial !== undefined ? res.initial : res, null, 2),
      });
    } catch (e: any) {
      setResult({ ok: false, text: e?.message || String(e) });
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="space-y-4">
      <div>
        <h2 className="text-lg font-semibold text-surface-100">Tools</h2>
        <p className="text-xs text-surface-500">
          Every capability the connected agent exposes, rendered from its schema. Bespoke panels
          (Circuit, Remote Desktop, Stores) live in their own tabs.
        </p>
      </div>

      {loading ? (
        <p className="text-sm text-surface-500">Loading verbs…</p>
      ) : loadErr ? (
        <p className="text-sm text-red-400">Couldn’t load verbs: {loadErr}</p>
      ) : (
        <div className="grid grid-cols-1 gap-4 md:grid-cols-[280px_1fr]">
          {/* Left: searchable, grouped verb list */}
          <div className="space-y-2">
            <input
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              placeholder={`Search ${verbs.length} tools…`}
              className="w-full rounded-lg border border-surface-700 bg-surface-900 px-3 py-2 text-sm text-surface-200 focus:border-indigo-500 focus:outline-none"
            />
            <div className="max-h-[70vh] overflow-y-auto rounded-lg border border-surface-800">
              {groups.length === 0 && <p className="p-3 text-xs text-surface-600">No matches.</p>}
              {groups.map(([g, vs]) => (
                <div key={g}>
                  <div className="sticky top-0 bg-surface-950/90 px-3 py-1 text-[10px] font-semibold uppercase tracking-wide text-surface-500 backdrop-blur">
                    {g} <span className="text-surface-700">({vs.length})</span>
                  </div>
                  {vs.map((v) => (
                    <button
                      key={v.name}
                      onClick={() => {
                        setSelected(v.name);
                        setResult(null);
                      }}
                      className={`flex w-full items-center gap-2 px-3 py-1.5 text-left text-xs ${
                        selected === v.name
                          ? "bg-indigo-600/15 text-indigo-300"
                          : "text-surface-300 hover:bg-surface-900"
                      }`}
                    >
                      <span className="truncate font-mono">{titleOf(v)}</span>
                      {v.streaming && <span className="shrink-0 text-[9px] text-amber-500">stream</span>}
                      {v.allowGuest === false && (
                        <span className="ml-auto shrink-0 text-[9px] text-surface-600">owner</span>
                      )}
                    </button>
                  ))}
                </div>
              ))}
            </div>
          </div>

          {/* Right: selected verb form + result */}
          <div className="rounded-lg border border-surface-800 bg-surface-950/40 p-4">
            {!active ? (
              <p className="text-sm text-surface-500">Pick a tool on the left to see its form.</p>
            ) : (
              <div className="space-y-4">
                <div>
                  <div className="flex items-center gap-2">
                    <h3 className="font-mono text-sm font-semibold text-surface-100">{active.name}</h3>
                    {active.streaming && (
                      <span className="rounded bg-amber-500/15 px-1.5 py-0.5 text-[10px] text-amber-400">
                        streaming
                      </span>
                    )}
                    {active.allowGuest === false && (
                      <span className="rounded bg-surface-800 px-1.5 py-0.5 text-[10px] text-surface-400">
                        owner-only
                      </span>
                    )}
                  </div>
                  {active.description && (
                    <p className="mt-1 text-xs text-surface-500">{active.description}</p>
                  )}
                </div>

                <SchemaForm
                  key={active.name}
                  schema={active.payload}
                  submitting={busy}
                  submitLabel={uiHint(active).confirm ? "Run (destructive)" : "Run"}
                  onSubmit={run}
                />

                {active.streaming && (
                  <p className="text-[11px] text-surface-600">
                    This verb streams — the result below is the initial response (may include a
                    streamId). Live stream tailing lands in v2.
                  </p>
                )}

                {result && (
                  <div className="space-y-1">
                    <div
                      className={`text-xs font-semibold ${result.ok ? "text-emerald-400" : "text-red-400"}`}
                    >
                      {result.ok ? "OK" : "Error"}
                    </div>
                    <pre className="max-h-[40vh] overflow-auto rounded-lg border border-surface-800 bg-surface-950 p-3 text-[11px] text-surface-300">
                      {result.text}
                    </pre>
                  </div>
                )}
              </div>
            )}
          </div>
        </div>
      )}
    </div>
  );
}
