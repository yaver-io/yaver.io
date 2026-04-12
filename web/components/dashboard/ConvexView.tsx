"use client";

import { useState, useEffect } from "react";
import { agentClient } from "@/lib/agent-client";

type Tab = "tables" | "run" | "schema";

export default function ConvexView() {
  const [directory, setDirectory] = useState("");
  const [status, setStatus] = useState<{ url: string; running: boolean; error?: string; hint?: string } | null>(null);
  const [tab, setTab] = useState<Tab>("tables");

  useEffect(() => { refresh(); }, [directory]);

  async function refresh() {
    try { setStatus(await agentClient.convexStatus(directory || undefined)); } catch {}
  }

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center gap-2">
        <input
          value={directory}
          onChange={(e) => setDirectory(e.target.value)}
          placeholder="project directory (blank = agent cwd)"
          className="flex-1 min-w-[200px] rounded-lg border border-surface-700 bg-surface-900 px-3 py-2 text-sm font-mono text-surface-200 placeholder-surface-500 outline-none focus:border-indigo-500"
        />
        <button onClick={refresh} className="px-3 py-2 text-sm rounded-lg bg-surface-800 text-surface-200 hover:bg-surface-700">Refresh</button>
      </div>

      <StatusBar status={status} directory={directory} onChange={refresh} />

      <div className="flex gap-1 border-b border-surface-800">
        {(["tables", "run", "schema"] as Tab[]).map((t) => (
          <button
            key={t}
            onClick={() => setTab(t)}
            className={`px-3 py-2 text-xs uppercase font-semibold ${tab === t ? "text-indigo-400 border-b-2 border-indigo-400" : "text-surface-500 hover:text-surface-300"}`}
          >
            {t === "run" ? "Function Runner" : t}
          </button>
        ))}
      </div>

      {tab === "tables" && <TablesPanel directory={directory} />}
      {tab === "run" && <RunnerPanel directory={directory} />}
      {tab === "schema" && <SchemaPanel directory={directory} />}
    </div>
  );
}

function StatusBar({
  status,
  directory,
  onChange,
}: {
  status: { url: string; running: boolean; error?: string; hint?: string } | null;
  directory: string;
  onChange: () => void;
}) {
  async function installHelper() {
    if (!directory) { alert("Enter a project directory first"); return; }
    const r = await agentClient.convexInstallHelper(directory);
    alert(r.error ? `Error: ${r.error}` : `Wrote ${r.wrote}\n\n${r.next}`);
    onChange();
  }

  if (!status) {
    return <div className="text-sm text-surface-500">Checking…</div>;
  }
  const dotColor = status.running ? "bg-emerald-400" : "bg-red-400";
  return (
    <div className="flex flex-wrap items-center gap-3 rounded-lg border border-surface-800 bg-surface-900/50 p-3 text-sm">
      <div className={`w-2 h-2 rounded-full ${dotColor}`} />
      <span className="font-mono text-surface-300">{status.url}</span>
      <span className="text-xs text-surface-500">
        {status.running ? "online" : status.error || "offline"}
      </span>
      <div className="flex-1" />
      <button onClick={installHelper} className="px-3 py-1.5 text-xs rounded-lg bg-indigo-500/20 text-indigo-300 hover:bg-indigo-500/30">
        Install yaver_admin.ts
      </button>
      {status.hint && <div className="w-full text-xs text-surface-500 mt-1">{status.hint}</div>}
    </div>
  );
}

function TablesPanel({ directory }: { directory: string }) {
  const [tables, setTables] = useState<any[]>([]);
  const [rawHint, setRawHint] = useState<string>("");
  const [selected, setSelected] = useState<string | null>(null);
  const [docs, setDocs] = useState<any[]>([]);
  const [cursor, setCursor] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);

  useEffect(() => { load(); }, [directory]);

  async function load() {
    setLoading(true);
    try {
      const r = await agentClient.convexTables(directory || undefined);
      setTables(Array.isArray(r.tables) ? r.tables : []);
      setRawHint(r.hint || "");
    } catch {}
    setLoading(false);
  }

  async function openTable(name: string) {
    setSelected(name);
    setDocs([]);
    const r = await agentClient.convexBrowse(name, { directory: directory || undefined, limit: 50 });
    const pages = r.result ?? r;
    setDocs(pages?.page ?? []);
    setCursor(pages?.continueCursor ?? null);
  }

  async function loadMore() {
    if (!selected || !cursor) return;
    const r = await agentClient.convexBrowse(selected, { directory: directory || undefined, limit: 50, cursor });
    const pages = r.result ?? r;
    setDocs((d) => [...d, ...(pages?.page ?? [])]);
    setCursor(pages?.continueCursor ?? null);
  }

  return (
    <div className="grid md:grid-cols-3 gap-3">
      <div className="md:col-span-1 space-y-1">
        <h3 className="text-xs uppercase text-surface-500 font-semibold mb-2">Tables</h3>
        {loading && <div className="text-sm text-surface-500">Loading…</div>}
        {!loading && tables.length === 0 && (
          <div className="text-xs text-surface-500">
            {rawHint || "No tables found. Install yaver_admin.ts and deploy it."}
          </div>
        )}
        {tables.map((t: any) => (
          <button
            key={t.name || t.id}
            onClick={() => openTable(t.name)}
            className={`w-full text-left px-3 py-2 rounded-lg text-sm font-mono ${selected === t.name ? "bg-indigo-500/20 text-indigo-300" : "bg-surface-900/50 text-surface-300 hover:bg-surface-800"}`}
          >
            {t.name}
          </button>
        ))}
      </div>

      <div className="md:col-span-2 space-y-2">
        <h3 className="text-xs uppercase text-surface-500 font-semibold mb-2">
          {selected ? `${selected} (${docs.length})` : "Pick a table"}
        </h3>
        <div className="space-y-1 max-h-[500px] overflow-auto">
          {docs.map((doc, i) => (
            <pre key={doc._id || i} className="text-xs font-mono bg-surface-900/50 border border-surface-800 rounded-lg p-2 overflow-auto">
              {JSON.stringify(doc, null, 2)}
            </pre>
          ))}
        </div>
        {cursor && (
          <button onClick={loadMore} className="px-3 py-2 text-sm rounded-lg bg-surface-800 text-surface-200 hover:bg-surface-700">
            Load more
          </button>
        )}
      </div>
    </div>
  );
}

function RunnerPanel({ directory }: { directory: string }) {
  const [kind, setKind] = useState<"query" | "mutate" | "action">("query");
  const [fn, setFn] = useState("");
  const [args, setArgs] = useState("{}");
  const [result, setResult] = useState<string>("");
  const [running, setRunning] = useState(false);

  async function run() {
    setRunning(true);
    setResult("");
    try {
      const parsed = args.trim() ? JSON.parse(args) : {};
      const r = await agentClient.convexCall(kind, fn, parsed, directory || undefined);
      setResult(JSON.stringify(r, null, 2));
    } catch (e: any) {
      setResult(`Error: ${e.message}`);
    }
    setRunning(false);
  }

  return (
    <div className="space-y-3">
      <div className="flex flex-wrap gap-2">
        <select value={kind} onChange={(e) => setKind(e.target.value as any)} className="rounded-lg border border-surface-700 bg-surface-900 px-3 py-2 text-sm text-surface-200">
          <option value="query">query</option>
          <option value="mutate">mutation</option>
          <option value="action">action</option>
        </select>
        <input
          value={fn}
          onChange={(e) => setFn(e.target.value)}
          placeholder="module:function (e.g. messages:list)"
          className="flex-1 min-w-[200px] rounded-lg border border-surface-700 bg-surface-900 px-3 py-2 text-sm font-mono text-surface-200 placeholder-surface-500"
        />
        <button onClick={run} disabled={running || !fn} className="px-4 py-2 text-sm rounded-lg bg-indigo-500 text-white hover:bg-indigo-400 disabled:opacity-50">
          {running ? "Running…" : "Run"}
        </button>
      </div>
      <div>
        <div className="text-xs uppercase text-surface-500 font-semibold mb-1">Args (JSON)</div>
        <textarea
          value={args}
          onChange={(e) => setArgs(e.target.value)}
          rows={4}
          className="w-full rounded-lg border border-surface-700 bg-surface-900 px-3 py-2 text-xs font-mono text-surface-200 outline-none focus:border-indigo-500"
        />
      </div>
      {result && (
        <pre className="text-xs font-mono bg-surface-900/50 border border-surface-800 rounded-lg p-3 overflow-auto max-h-[400px]">
          {result}
        </pre>
      )}
    </div>
  );
}

function SchemaPanel({ directory }: { directory: string }) {
  const [schema, setSchema] = useState<string>("");
  const [error, setError] = useState<string>("");

  useEffect(() => {
    (async () => {
      const r = await agentClient.convexSchema(directory || undefined);
      if (r.error) setError(r.error);
      else setSchema(r.schema || "");
    })();
  }, [directory]);

  if (error) return <div className="text-sm text-red-400">Could not load schema: {error}</div>;
  if (!schema) return <div className="text-sm text-surface-500">Loading…</div>;
  return (
    <pre className="text-xs font-mono bg-surface-900/50 border border-surface-800 rounded-lg p-3 overflow-auto max-h-[600px]">
      {schema}
    </pre>
  );
}
