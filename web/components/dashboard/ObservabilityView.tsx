"use client";

import { useEffect, useRef, useState } from "react";
import { agentClient } from "@/lib/agent-client";

type Tab = "schema" | "storage" | "jobs" | "logs" | "cost";

export default function ObservabilityView() {
  const [directory, setDirectory] = useState("");
  const [tab, setTab] = useState<Tab>("schema");

  return (
    <div className="space-y-4">
      <input value={directory} onChange={(e) => setDirectory(e.target.value)}
        placeholder="project directory"
        className="w-full rounded-lg border border-surface-700 bg-surface-900 px-3 py-2 text-sm font-mono text-surface-200" />
      <div className="flex gap-1 border-b border-surface-800 overflow-auto">
        {(["schema", "storage", "jobs", "logs", "cost"] as Tab[]).map((t) => (
          <button key={t} onClick={() => setTab(t)}
            className={`px-3 py-2 text-xs uppercase font-semibold whitespace-nowrap ${tab === t ? "text-indigo-400 border-b-2 border-indigo-400" : "text-surface-500 hover:text-surface-300"}`}>
            {t}
          </button>
        ))}
      </div>
      {tab === "schema" && <Schema directory={directory} />}
      {tab === "storage" && <Storage directory={directory} />}
      {tab === "jobs" && <Jobs directory={directory} />}
      {tab === "logs" && <Logs directory={directory} />}
      {tab === "cost" && <Cost directory={directory} />}
    </div>
  );
}

function Schema({ directory }: { directory: string }) {
  const [data, setData] = useState<any>(null);
  useEffect(() => { (async () => setData(await agentClient.backendSchema(directory || undefined)))(); }, [directory]);
  if (!data) return <div className="text-sm text-surface-500">Loading…</div>;
  if (data.error) return <div className="text-xs text-red-400">{data.error}</div>;
  return (
    <div className="space-y-3">
      <div className="text-xs text-surface-500">Backend: {data.backend} · Source: {data.source}</div>
      <div className="grid md:grid-cols-2 gap-3">
        <div className="space-y-2">
          {(data.tables || []).map((t: any) => (
            <div key={t.name} className="bg-surface-900/50 border border-surface-800 rounded-lg p-3">
              <div className="text-sm font-semibold font-mono text-indigo-300">{t.name}</div>
              <div className="mt-1 space-y-0.5">
                {(t.columns || []).map((c: any, i: number) => (
                  <div key={i} className="text-xs font-mono text-surface-400 flex gap-2">
                    <span className="text-surface-200">{c.name}</span>
                    <span className="text-surface-500">{c.type}</span>
                    {c.primaryKey && <span className="text-amber-400">PK</span>}
                  </div>
                ))}
              </div>
            </div>
          ))}
        </div>
        <div>
          <h3 className="text-xs uppercase text-surface-500 font-semibold mb-2">Mermaid ERD</h3>
          <pre className="text-[10px] font-mono bg-surface-900/50 border border-surface-800 rounded-lg p-3 overflow-auto max-h-[600px]">{data.mermaid}</pre>
        </div>
      </div>
    </div>
  );
}

function Storage({ directory }: { directory: string }) {
  const [data, setData] = useState<any>(null);
  useEffect(() => { (async () => setData(await agentClient.storageList(undefined, directory || undefined)))(); }, [directory]);
  if (!data) return <div className="text-sm text-surface-500">Loading…</div>;
  return (
    <div className="space-y-2">
      <div className="text-xs text-surface-500">Source: {data.source}</div>
      {data.error && <div className="text-xs text-red-400">{data.error}</div>}
      {(data.files || []).map((f: any) => (
        <div key={f.id} className="flex items-center gap-3 bg-surface-900/50 border border-surface-800 rounded-lg p-2 text-xs">
          <span className="font-mono flex-1 truncate">{f.name}</span>
          <span className="text-surface-500 font-mono">{fmtBytes(f.size)}</span>
          <span className="text-surface-600 text-[10px]">{f.createdAt?.slice(0, 10)}</span>
        </div>
      ))}
    </div>
  );
}

function Jobs({ directory }: { directory: string }) {
  const [data, setData] = useState<any>(null);
  useEffect(() => { (async () => setData(await agentClient.jobsList(directory || undefined)))(); }, [directory]);
  if (!data) return <div className="text-sm text-surface-500">Loading…</div>;
  return (
    <div className="space-y-2">
      <div className="text-xs text-surface-500">Source: {data.source}</div>
      {(!data.jobs || data.jobs.length === 0) && <div className="text-xs text-surface-500">No scheduled jobs.</div>}
      {(data.jobs || []).map((j: any, i: number) => (
        <div key={i} className="bg-surface-900/50 border border-surface-800 rounded-lg p-2 text-xs">
          <div className="flex gap-2">
            <span className="font-mono text-indigo-300">{j.name}</span>
            <span className="text-surface-500">{j.kind}</span>
            {j.schedule && <span className="text-surface-400 font-mono">{j.schedule}</span>}
            {j.status && <span className="ml-auto text-surface-500">{j.status}</span>}
          </div>
          {j.target && <div className="text-[10px] font-mono text-surface-500 mt-1 truncate">{j.target}</div>}
          {j.nextRun && <div className="text-[10px] text-surface-600">next: {j.nextRun}</div>}
        </div>
      ))}
    </div>
  );
}

function Logs({ directory }: { directory: string }) {
  const [service, setService] = useState("postgres");
  const [lines, setLines] = useState<string[]>([]);
  const [connected, setConnected] = useState(false);
  const esRef = useRef<EventSource | null>(null);

  function start() {
    esRef.current?.close();
    setLines([]);
    const es = new EventSource(agentClient.logsSseUrl(service));
    es.onopen = () => setConnected(true);
    es.onerror = () => setConnected(false);
    es.onmessage = (e) => setLines((ls) => [...ls.slice(-999), e.data]);
    esRef.current = es;
  }

  useEffect(() => () => esRef.current?.close(), []);

  return (
    <div className="space-y-2">
      <div className="flex gap-2 items-center">
        <input value={service} onChange={(e) => setService(e.target.value)}
          placeholder="service name (e.g. postgres)"
          className="flex-1 rounded-lg border border-surface-700 bg-surface-900 px-3 py-2 text-sm font-mono text-surface-200" />
        <button onClick={start} className="px-3 py-2 text-sm rounded-lg bg-indigo-500 text-white hover:bg-indigo-400">Stream</button>
        <span className={`text-xs ${connected ? "text-emerald-400" : "text-surface-500"}`}>{connected ? "●" : "○"}</span>
      </div>
      <pre className="text-[10px] font-mono bg-surface-900/50 border border-surface-800 rounded-lg p-3 overflow-auto max-h-[500px]">{lines.join("\n")}</pre>
    </div>
  );
}

function Cost({ directory }: { directory: string }) {
  const [data, setData] = useState<any>(null);
  useEffect(() => { (async () => setData(await agentClient.switchCost(directory || undefined)))(); }, [directory]);
  if (!data) return <div className="text-sm text-surface-500">Loading…</div>;
  const ests = (data.estimates || []).sort((a: any, b: any) => a.monthly - b.monthly);
  return (
    <div className="space-y-3">
      <div className="text-xs text-surface-500">
        Project usage: DB {fmtBytes((data.usage?.dbSizeMb || 0) * 1024 * 1024)} · Storage {fmtBytes((data.usage?.storageMb || 0) * 1024 * 1024)}
      </div>
      <div className="space-y-1">
        {ests.map((e: any) => (
          <div key={e.target} className="flex items-center gap-3 bg-surface-900/50 border border-surface-800 rounded-lg p-2 text-sm">
            <span className={`px-1.5 py-0.5 rounded text-[9px] uppercase ${e.freeTierOk ? "bg-emerald-500/20 text-emerald-300" : "bg-amber-500/20 text-amber-300"}`}>{e.tier}</span>
            <span className="flex-1 font-mono text-surface-200">{e.label}</span>
            <span className="text-surface-300 font-mono">${e.monthly.toFixed(2)}/mo</span>
          </div>
        ))}
      </div>
    </div>
  );
}

function fmtBytes(n: number): string {
  if (!n) return "0 B";
  const units = ["B", "KB", "MB", "GB", "TB"];
  let i = 0;
  while (n >= 1024 && i < units.length - 1) { n /= 1024; i++; }
  return `${n.toFixed(1)} ${units[i]}`;
}
