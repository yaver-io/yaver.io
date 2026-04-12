"use client";

import { useState, useEffect } from "react";
import { agentClient } from "@/lib/agent-client";

type Tab = "tables" | "query" | "cloud";

export default function DataView() {
  const [directory, setDirectory] = useState("");
  const [status, setStatus] = useState<{ kind: string; url: string; running: boolean; error?: string; hint?: string; version?: string } | null>(null);
  const [tab, setTab] = useState<Tab>("tables");

  useEffect(() => { refresh(); }, [directory]);

  async function refresh() {
    try { setStatus(await agentClient.backendStatus(directory || undefined)); } catch {}
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

      <StatusBar status={status} />

      <div className="flex gap-1 border-b border-surface-800">
        {(["tables", "query", "cloud"] as Tab[]).map((t) => (
          <button
            key={t}
            onClick={() => setTab(t)}
            className={`px-3 py-2 text-xs uppercase font-semibold ${tab === t ? "text-indigo-400 border-b-2 border-indigo-400" : "text-surface-500 hover:text-surface-300"}`}
          >
            {t === "cloud" ? "Cloud Emulators" : t === "query" ? "Query Runner" : "Tables"}
          </button>
        ))}
      </div>

      {tab === "tables" && <TablesPanel directory={directory} status={status} />}
      {tab === "query" && <QueryPanel directory={directory} status={status} />}
      {tab === "cloud" && <CloudPanel directory={directory} />}
    </div>
  );
}

function StatusBar({ status }: { status: { kind: string; url: string; running: boolean; error?: string; hint?: string; version?: string } | null }) {
  if (!status) return <div className="text-sm text-surface-500">Checking…</div>;
  const dotColor = status.running ? "bg-emerald-400" : "bg-red-400";
  return (
    <div className="flex flex-wrap items-center gap-3 rounded-lg border border-surface-800 bg-surface-900/50 p-3 text-sm">
      <div className={`w-2 h-2 rounded-full ${dotColor}`} />
      <span className="text-xs uppercase font-semibold text-indigo-400">{status.kind || "unknown"}</span>
      <span className="font-mono text-surface-300 truncate max-w-[50%]">{status.url}</span>
      {status.version && <span className="text-xs text-surface-500">v{status.version}</span>}
      <span className="text-xs text-surface-500">{status.running ? "online" : (status.error || "offline")}</span>
      {status.hint && <div className="w-full text-xs text-amber-400">{status.hint}</div>}
    </div>
  );
}

function TablesPanel({ directory, status }: { directory: string; status: { kind: string; running: boolean } | null }) {
  const [tables, setTables] = useState<{ name: string; rowCount?: number; kind?: string }[]>([]);
  const [error, setError] = useState("");
  const [selected, setSelected] = useState<string | null>(null);
  const [rows, setRows] = useState<any[]>([]);
  const [cursor, setCursor] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);

  useEffect(() => { load(); setSelected(null); setRows([]); }, [directory, status?.kind]);

  async function load() {
    setLoading(true);
    setError("");
    try {
      const r = await agentClient.backendTables(directory || undefined);
      if (r.error) setError(r.error);
      setTables(r.tables || []);
    } catch (e: any) { setError(e.message); }
    setLoading(false);
  }

  async function openTable(name: string) {
    setSelected(name);
    setRows([]);
    const r = await agentClient.backendBrowse(name, { directory: directory || undefined, limit: 50 });
    setRows(r.rows || []);
    setCursor(r.nextCursor || null);
  }

  async function loadMore() {
    if (!selected || !cursor) return;
    const r = await agentClient.backendBrowse(selected, { directory: directory || undefined, limit: 50, cursor });
    setRows((d) => [...d, ...(r.rows || [])]);
    setCursor(r.nextCursor || null);
  }

  async function deleteRow(row: any) {
    const id = String(row.id ?? row._id ?? row["$id"] ?? "");
    if (!id || !selected) return;
    if (!confirm(`Delete row ${id}?`)) return;
    await agentClient.backendDelete(selected, id, directory || undefined);
    openTable(selected);
  }

  return (
    <div className="grid md:grid-cols-3 gap-3">
      <div className="md:col-span-1 space-y-1">
        <h3 className="text-xs uppercase text-surface-500 font-semibold mb-2">Tables / Collections</h3>
        {loading && <div className="text-sm text-surface-500">Loading…</div>}
        {error && <div className="text-xs text-red-400 p-2 rounded bg-red-900/20 border border-red-500/30">{error}</div>}
        {!loading && !error && tables.length === 0 && (
          <div className="text-xs text-surface-500">No tables found. Backend may be stopped or empty.</div>
        )}
        {tables.map((t) => (
          <button
            key={t.name}
            onClick={() => openTable(t.name)}
            className={`w-full flex items-center gap-2 text-left px-3 py-2 rounded-lg text-sm font-mono ${selected === t.name ? "bg-indigo-500/20 text-indigo-300" : "bg-surface-900/50 text-surface-300 hover:bg-surface-800"}`}
          >
            <span className="flex-1 truncate">{t.name}</span>
            {t.rowCount != null && <span className="text-xs text-surface-500">{t.rowCount}</span>}
          </button>
        ))}
      </div>

      <div className="md:col-span-2 space-y-2">
        <h3 className="text-xs uppercase text-surface-500 font-semibold mb-2">
          {selected ? `${selected} (${rows.length} loaded)` : "Pick a table"}
        </h3>
        <div className="space-y-1 max-h-[500px] overflow-auto">
          {rows.map((row, i) => {
            const id = String(row.id ?? row._id ?? row["$id"] ?? i);
            return (
              <div key={id + "-" + i} className="bg-surface-900/50 border border-surface-800 rounded-lg p-2 relative group">
                <pre className="text-xs font-mono overflow-auto">{JSON.stringify(row, null, 2)}</pre>
                <button
                  onClick={() => deleteRow(row)}
                  className="absolute top-2 right-2 opacity-0 group-hover:opacity-100 px-2 py-1 text-xs rounded bg-red-500/20 text-red-300 hover:bg-red-500/40"
                >
                  Delete
                </button>
              </div>
            );
          })}
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

function QueryPanel({ directory, status }: { directory: string; status: { kind: string } | null }) {
  const [query, setQuery] = useState("");
  const [argsJSON, setArgsJSON] = useState("{}");
  const [result, setResult] = useState("");
  const [running, setRunning] = useState(false);

  const kind = status?.kind || "";
  const placeholder =
    kind === "convex" ? "module:function (e.g. messages:list)" :
    kind === "pocketbase" || kind === "appwrite" ? "REST path (e.g. collections/users/records)" :
    "SQL (e.g. SELECT * FROM users LIMIT 10)";

  async function run() {
    setRunning(true);
    setResult("");
    try {
      const parsed = argsJSON.trim() ? JSON.parse(argsJSON) : {};
      const r = await agentClient.backendQuery(query, parsed, directory || undefined);
      setResult(JSON.stringify(r, null, 2));
    } catch (e: any) {
      setResult(`Error: ${e.message}`);
    }
    setRunning(false);
  }

  return (
    <div className="space-y-3">
      <div>
        <div className="text-xs uppercase text-surface-500 font-semibold mb-1">Query</div>
        <textarea
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          rows={4}
          placeholder={placeholder}
          className="w-full rounded-lg border border-surface-700 bg-surface-900 px-3 py-2 text-xs font-mono text-surface-200 outline-none focus:border-indigo-500"
        />
      </div>
      {(kind === "convex") && (
        <div>
          <div className="text-xs uppercase text-surface-500 font-semibold mb-1">Args (JSON)</div>
          <textarea
            value={argsJSON}
            onChange={(e) => setArgsJSON(e.target.value)}
            rows={3}
            className="w-full rounded-lg border border-surface-700 bg-surface-900 px-3 py-2 text-xs font-mono text-surface-200 outline-none focus:border-indigo-500"
          />
        </div>
      )}
      <button onClick={run} disabled={running || !query} className="px-4 py-2 text-sm rounded-lg bg-indigo-500 text-white hover:bg-indigo-400 disabled:opacity-50">
        {running ? "Running…" : "Run"}
      </button>
      {result && (
        <pre className="text-xs font-mono bg-surface-900/50 border border-surface-800 rounded-lg p-3 overflow-auto max-h-[400px]">{result}</pre>
      )}
    </div>
  );
}

function CloudPanel({ directory }: { directory: string }) {
  const [emus, setEmus] = useState<{ name: string; provider: string; running: boolean; port: number; health: string }[]>([]);
  const [config, setConfig] = useState<any>(null);
  const [provider, setProvider] = useState("aws");

  useEffect(() => { refresh(); }, [directory]);
  useEffect(() => { loadConfig(); }, [provider]);

  async function refresh() {
    try {
      const r = await agentClient.cloudEmuStatus(directory || undefined);
      setEmus(r.emulators || []);
    } catch {}
  }
  async function loadConfig() {
    try {
      const r = await agentClient.cloudEmuConfig(provider);
      setConfig(r.config);
    } catch {}
  }

  async function start(services: string[] = []) {
    await agentClient.cloudEmuStart(provider, services, directory || undefined);
    refresh();
  }
  async function stop(services: string[] = []) {
    await agentClient.cloudEmuStop(provider, services, directory || undefined);
    refresh();
  }

  return (
    <div className="space-y-4">
      <div className="flex items-center gap-2">
        <select value={provider} onChange={(e) => setProvider(e.target.value)} className="rounded-lg border border-surface-700 bg-surface-900 px-3 py-2 text-sm text-surface-200">
          <option value="aws">AWS (MinIO, DynamoDB, ElasticMQ)</option>
          <option value="azure">Azure (Azurite)</option>
          <option value="gcp">GCP (Firebase Emulators)</option>
        </select>
        <button onClick={() => start()} className="px-3 py-2 text-sm rounded-lg bg-emerald-500/20 text-emerald-300 hover:bg-emerald-500/30">Start all</button>
        <button onClick={() => stop()} className="px-3 py-2 text-sm rounded-lg bg-red-500/20 text-red-300 hover:bg-red-500/30">Stop all</button>
      </div>

      <div className="space-y-1">
        {emus.length === 0 && <div className="text-xs text-surface-500">No emulators running</div>}
        {emus.map((e) => (
          <div key={e.name + e.provider} className="flex items-center gap-3 bg-surface-900/50 border border-surface-800 rounded-lg p-3">
            <div className={`w-2 h-2 rounded-full ${e.running ? "bg-emerald-400" : "bg-surface-600"}`} />
            <span className="text-xs uppercase font-semibold text-indigo-400">{e.provider}</span>
            <span className="flex-1 text-sm font-mono">{e.name}</span>
            <span className="text-xs text-surface-500">:{e.port}</span>
            <span className="text-xs font-mono uppercase">{e.health}</span>
          </div>
        ))}
      </div>

      {config && (
        <div>
          <h3 className="text-xs uppercase text-surface-500 font-semibold mb-2">SDK Config for {provider}</h3>
          <pre className="text-xs font-mono bg-surface-900/50 border border-surface-800 rounded-lg p-3 overflow-auto">{JSON.stringify(config, null, 2)}</pre>
        </div>
      )}
    </div>
  );
}
