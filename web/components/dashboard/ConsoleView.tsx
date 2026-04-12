"use client";

import { useEffect, useRef, useState } from "react";
import { agentClient } from "@/lib/agent-client";
import TerminalView from "./TerminalView";

type Tab = "overview" | "machines" | "containers" | "terminal" | "catalog" | "images" | "multiregion";

export default function ConsoleView() {
  const [tab, setTab] = useState<Tab>("overview");
  return (
    <div className="space-y-4">
      <div className="flex gap-1 border-b border-surface-800">
        {(["overview", "machines", "containers", "terminal", "catalog", "images", "multiregion"] as Tab[]).map((t) => (
          <button key={t} onClick={() => setTab(t)}
            className={`px-3 py-2 text-xs uppercase font-semibold ${tab === t ? "text-indigo-400 border-b-2 border-indigo-400" : "text-surface-500 hover:text-surface-300"}`}>
            {t}
          </button>
        ))}
      </div>
      {tab === "overview" && <Overview />}
      {tab === "machines" && <Machines />}
      {tab === "containers" && <Containers />}
      {tab === "terminal" && <TerminalView />}
      {tab === "catalog" && <Catalog />}
      {tab === "images" && <Images />}
      {tab === "multiregion" && <MultiRegion />}
    </div>
  );
}

function Overview() {
  const [m, setM] = useState<any>(null);
  const [hist, setHist] = useState<number[]>([]);

  useEffect(() => {
    let ws: WebSocket | null = null;
    try {
      ws = new WebSocket(agentClient.metricsWsUrl());
      ws.onmessage = (e) => {
        const sample = JSON.parse(e.data);
        setM(sample);
        setHist((h) => [...h.slice(-59), sample.cpuPct || 0]);
      };
    } catch {}
    return () => { ws?.close(); };
  }, []);

  if (!m) return <div className="text-sm text-surface-500">Connecting to metrics stream…</div>;

  return (
    <div className="space-y-4">
      <div className="grid sm:grid-cols-2 lg:grid-cols-4 gap-2">
        <MetricCard label="CPU" value={`${m.cpuPct?.toFixed(1) ?? 0}%`} sub={`${m.cores} cores`} sparkline={hist} />
        <MetricCard label="RAM" value={`${m.ramPct?.toFixed(0) ?? 0}%`} sub={`${fmtBytes(m.ramUsed)} / ${fmtBytes(m.ramTotal)}`} />
        <MetricCard label="Disk" value={`${m.diskPct?.toFixed(0) ?? 0}%`} sub={`${fmtBytes(m.diskUsed)} / ${fmtBytes(m.diskTotal)}`} />
        <MetricCard label="Network" value={`↓ ${fmtBps(m.netRxBps)}`} sub={`↑ ${fmtBps(m.netTxBps)}`} />
      </div>
      <div className="text-xs text-surface-500">
        Host: <span className="font-mono text-surface-300">{m.hostname}</span> · {m.os} · uptime {fmtUptime(m.uptime)}
      </div>
    </div>
  );
}

function MetricCard({ label, value, sub, sparkline }: { label: string; value: string; sub: string; sparkline?: number[] }) {
  return (
    <div className="bg-surface-900/50 border border-surface-800 rounded-lg p-3">
      <div className="text-xs uppercase text-surface-500 font-semibold">{label}</div>
      <div className="text-2xl font-bold text-surface-200 mt-1">{value}</div>
      <div className="text-xs text-surface-500">{sub}</div>
      {sparkline && sparkline.length > 1 && (
        <svg viewBox="0 0 100 20" className="mt-2 w-full h-6">
          <polyline
            points={sparkline.map((v, i) => `${(i / (sparkline.length - 1)) * 100},${20 - (v / 100) * 20}`).join(" ")}
            fill="none" stroke="#818cf8" strokeWidth="0.5" />
        </svg>
      )}
    </div>
  );
}

function Containers() {
  const [list, setList] = useState<any[]>([]);
  const [all, setAll] = useState(false);
  const [error, setError] = useState("");
  const [selectedLogId, setSelectedLogId] = useState<string | null>(null);

  useEffect(() => { refresh(); }, [all]);
  async function refresh() {
    const r = await agentClient.consoleContainers(all);
    setList(r.containers || []);
    setError(r.error || "");
  }
  async function act(id: string, action: string) {
    const r = await agentClient.consoleContainerAction(id, action);
    if (r.error) alert(r.error);
    refresh();
  }
  async function prune() {
    if (!confirm("Prune unused images, containers, and volumes?")) return;
    const r = await agentClient.consolePrune();
    alert(JSON.stringify(r, null, 2));
    refresh();
  }

  return (
    <div className="space-y-3">
      <div className="flex items-center gap-2">
        <label className="text-xs text-surface-400 flex items-center gap-1">
          <input type="checkbox" checked={all} onChange={(e) => setAll(e.target.checked)} />
          Include stopped
        </label>
        <button onClick={refresh} className="px-3 py-1.5 text-xs rounded bg-surface-800 text-surface-200 hover:bg-surface-700">Refresh</button>
        <button onClick={prune} className="px-3 py-1.5 text-xs rounded bg-amber-500/20 text-amber-300 hover:bg-amber-500/30">Prune unused</button>
      </div>
      {error && <div className="text-xs text-red-400 p-2 rounded bg-red-900/20 border border-red-500/30">{error}</div>}
      <div className="overflow-auto border border-surface-800 rounded-lg">
        <table className="w-full text-xs">
          <thead className="bg-surface-900">
            <tr className="text-surface-500 uppercase">
              <th className="text-left p-2">Name</th>
              <th className="text-left p-2">Image</th>
              <th className="text-left p-2">State</th>
              <th className="text-left p-2">Ports</th>
              <th className="text-left p-2">Project</th>
              <th className="text-right p-2">Actions</th>
            </tr>
          </thead>
          <tbody>
            {list.map((c) => (
              <tr key={c.id} className="border-t border-surface-800">
                <td className="p-2 font-mono">{c.name}</td>
                <td className="p-2 font-mono text-surface-400">{c.image}</td>
                <td className="p-2">
                  <span className={`px-1.5 py-0.5 rounded text-[10px] ${c.state === "running" ? "bg-emerald-500/20 text-emerald-300" : "bg-surface-800 text-surface-400"}`}>{c.state}</span>
                </td>
                <td className="p-2 text-surface-400">
                  {(c.ports || []).filter((p: any) => p.public).map((p: any) => `${p.public}→${p.private}`).join(", ") || "—"}
                </td>
                <td className="p-2 text-surface-500">{c.project || "—"}</td>
                <td className="p-2 text-right space-x-1">
                  {c.state === "running" ? (
                    <>
                      <button onClick={() => act(c.id, "restart")} className="text-indigo-400 hover:text-indigo-300">↻</button>
                      <button onClick={() => act(c.id, "stop")} className="text-red-400 hover:text-red-300">⏹</button>
                    </>
                  ) : (
                    <button onClick={() => act(c.id, "start")} className="text-emerald-400 hover:text-emerald-300">▶</button>
                  )}
                  <button onClick={() => setSelectedLogId(c.id)} className="text-surface-400 hover:text-surface-200">logs</button>
                  <button onClick={() => confirm(`Remove ${c.name}?`) && act(c.id, "remove")} className="text-red-400 hover:text-red-300">✕</button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      {selectedLogId && <LogPanel id={selectedLogId} onClose={() => setSelectedLogId(null)} />}
    </div>
  );
}

function LogPanel({ id, onClose }: { id: string; onClose: () => void }) {
  const [lines, setLines] = useState<string[]>([]);
  const ref = useRef<HTMLPreElement>(null);
  useEffect(() => {
    let ws: WebSocket | null = null;
    try {
      ws = new WebSocket(agentClient.containerLogsWsUrl(id));
      ws.binaryType = "arraybuffer";
      ws.onmessage = (e) => {
        const text = typeof e.data === "string" ? e.data : new TextDecoder().decode(e.data);
        setLines((ls) => [...ls.slice(-999), ...text.split("\n").filter(Boolean)]);
      };
    } catch {}
    return () => { ws?.close(); };
  }, [id]);
  useEffect(() => { ref.current?.scrollTo({ top: ref.current.scrollHeight }); }, [lines]);
  return (
    <div className="fixed inset-0 bg-black/60 flex items-center justify-center z-50 p-4">
      <div className="bg-surface-950 border border-surface-700 rounded-xl w-full max-w-3xl max-h-[80vh] flex flex-col">
        <div className="flex items-center gap-2 p-3 border-b border-surface-800">
          <span className="text-sm font-mono flex-1">logs: {id}</span>
          <button onClick={onClose} className="text-xs text-surface-500 hover:text-surface-300">Close</button>
        </div>
        <pre ref={ref} className="flex-1 overflow-auto p-3 text-[10px] font-mono text-surface-300">
          {lines.join("\n")}
        </pre>
      </div>
    </div>
  );
}

function Catalog() {
  const [entries, setEntries] = useState<any[]>([]);
  const [categories, setCategories] = useState<Record<string, any[]>>({});
  const [active, setActive] = useState<any>(null);
  const [fieldValues, setFieldValues] = useState<Record<string, string>>({});
  const [directory, setDirectory] = useState("");
  const [installing, setInstalling] = useState(false);

  useEffect(() => { (async () => {
    const r = await agentClient.consoleCatalog();
    setEntries(r.entries || []);
    setCategories(r.categories || {});
  })(); }, []);

  async function install() {
    if (!active) return;
    setInstalling(true);
    const r = await agentClient.consoleCatalogInstall(active.id, fieldValues, directory || undefined);
    setInstalling(false);
    alert(r.error ? `Error: ${r.error}` : JSON.stringify(r, null, 2));
    if (!r.error) setActive(null);
  }

  return (
    <div className="space-y-4">
      <input value={directory} onChange={(e) => setDirectory(e.target.value)}
        placeholder="project directory (defaults to agent cwd)"
        className="w-full rounded-lg border border-surface-700 bg-surface-900 px-3 py-2 text-sm font-mono text-surface-200" />

      {Object.entries(categories).map(([cat, list]) => (
        <div key={cat}>
          <h3 className="text-xs uppercase text-indigo-400 font-semibold mb-2">{cat.replace("-", " ")}</h3>
          <div className="grid sm:grid-cols-2 lg:grid-cols-3 gap-2">
            {list.map((e: any) => (
              <button key={e.id} onClick={() => { setActive(e); setFieldValues(Object.fromEntries((e.fields || []).map((f: any) => [f.key, f.default || ""]))); }}
                className="text-left bg-surface-900/50 border border-surface-800 hover:border-indigo-500 rounded-lg p-3 transition">
                <div className="text-sm font-semibold text-surface-200">{e.name}</div>
                <div className="text-xs text-surface-500 line-clamp-2">{e.description}</div>
                <div className="text-[10px] text-surface-600 mt-1 font-mono">{e.image || e.notes}</div>
              </button>
            ))}
          </div>
        </div>
      ))}

      {active && (
        <div className="fixed inset-0 bg-black/60 flex items-center justify-center z-50 p-4">
          <div className="bg-surface-950 border border-surface-700 rounded-xl p-5 max-w-md w-full space-y-3">
            <h3 className="text-sm font-semibold">Install {active.name}</h3>
            <div className="text-xs text-surface-500">{active.description}</div>
            {(active.fields || []).map((f: any) => (
              <div key={f.key}>
                <label className="text-xs text-surface-400">{f.label || f.key}</label>
                <input
                  type={f.secret ? "password" : "text"}
                  value={fieldValues[f.key] || ""}
                  onChange={(e) => setFieldValues({ ...fieldValues, [f.key]: e.target.value })}
                  placeholder={f.default}
                  className="w-full rounded-lg border border-surface-700 bg-surface-900 px-3 py-2 text-sm font-mono text-surface-200"
                />
              </div>
            ))}
            <div className="flex gap-2 pt-2">
              <button onClick={install} disabled={installing} className="px-4 py-2 text-sm rounded-lg bg-indigo-500 text-white hover:bg-indigo-400 disabled:opacity-50">
                {installing ? "Installing…" : "Install & Start"}
              </button>
              <button onClick={() => setActive(null)} className="px-4 py-2 text-sm rounded-lg bg-surface-800 text-surface-200">Cancel</button>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}

function Images() {
  const [list, setList] = useState<any[]>([]);
  useEffect(() => { (async () => {
    const r = await agentClient.consoleImages();
    setList(r.images || []);
  })(); }, []);
  return (
    <div className="space-y-1">
      {list.map((i) => (
        <div key={i.id} className="flex items-center gap-3 bg-surface-900/50 border border-surface-800 rounded-lg p-2 text-xs">
          <span className="font-mono text-surface-200 flex-1 truncate">{(i.repoTags || ["(untagged)"]).join(", ")}</span>
          <span className="text-surface-500 font-mono">{fmtBytes(i.size)}</span>
          <span className="text-surface-600 font-mono">{i.id.slice(7, 19)}</span>
        </div>
      ))}
    </div>
  );
}

function MultiRegion() {
  const [name, setName] = useState("");
  const [regions, setRegions] = useState("nbg1,fsn1");
  const [domain, setDomain] = useState("");
  const [gitRepo, setGitRepo] = useState("");
  const [directory, setDirectory] = useState("");
  const [result, setResult] = useState<any>(null);
  const [running, setRunning] = useState(false);

  async function deploy() {
    if (!name || !regions) { alert("name + regions required"); return; }
    const regionList = regions.split(",").map((r) => r.trim()).filter(Boolean);
    if (regionList.length < 2) { alert("need at least 2 regions"); return; }
    if (!confirm(`Provision ${regionList.length} Hetzner VPSes in ${regionList.join(", ")} and bootstrap each? This creates real billable servers.`)) return;
    setRunning(true);
    const r = await agentClient.multiRegionOrchestrate(name, regionList, domain, gitRepo, directory || undefined);
    setRunning(false);
    setResult(r);
  }

  return (
    <div className="space-y-3">
      <div className="text-xs text-surface-500">
        Provisions N Hetzner VPSes across the chosen regions via the connected Hetzner account,
        SSHes in, installs Docker + Yaver agent, rsyncs your project (or git clones), starts services,
        and writes a Caddy round-robin config on the first healthy server.
      </div>
      <input value={name} onChange={(e) => setName(e.target.value)} placeholder="deployment name (e.g. myapp-ha)"
        className="w-full rounded border border-surface-700 bg-surface-900 px-3 py-2 text-sm" />
      <input value={regions} onChange={(e) => setRegions(e.target.value)} placeholder="regions (comma-separated, e.g. nbg1,fsn1,hel1)"
        className="w-full rounded border border-surface-700 bg-surface-900 px-3 py-2 text-sm font-mono" />
      <input value={domain} onChange={(e) => setDomain(e.target.value)} placeholder="domain (e.g. myapp.com — Caddy writes round-robin config)"
        className="w-full rounded border border-surface-700 bg-surface-900 px-3 py-2 text-sm font-mono" />
      <input value={gitRepo} onChange={(e) => setGitRepo(e.target.value)} placeholder="git clone URL (optional — rsync current project if blank)"
        className="w-full rounded border border-surface-700 bg-surface-900 px-3 py-2 text-sm font-mono" />
      <input value={directory} onChange={(e) => setDirectory(e.target.value)} placeholder="project directory (defaults to agent cwd)"
        className="w-full rounded border border-surface-700 bg-surface-900 px-3 py-2 text-sm font-mono" />
      <button onClick={deploy} disabled={running} className="px-4 py-2 text-sm rounded bg-indigo-500 text-white hover:bg-indigo-400 disabled:opacity-50">
        {running ? "Provisioning + bootstrapping…" : "🌍 Deploy multi-region"}
      </button>

      {result?.error && <div className="text-xs text-red-400 p-2 rounded bg-red-900/20 border border-red-500/30">{result.error}</div>}
      {result?.provision?.servers && (
        <div className="space-y-2">
          <h3 className="text-xs uppercase text-surface-500 font-semibold mt-4">Servers</h3>
          {result.provision.servers.map((srv: any, i: number) => (
            <div key={i} className="bg-surface-900/50 border border-surface-800 rounded-lg p-3 text-sm">
              <div className="flex items-center gap-2">
                <span className="font-mono text-indigo-300">{srv.resource}/{srv.id}</span>
                <span className="text-xs text-surface-500 flex-1">{srv.details?.ipv4}</span>
                <span className="text-xs text-emerald-400">{srv.details?.status || "ok"}</span>
              </div>
              {srv.notes && <div className="text-xs text-surface-500 mt-1">{srv.notes}</div>}
            </div>
          ))}
        </div>
      )}
      {result?.orchestrate?.servers && (
        <div className="space-y-2">
          <h3 className="text-xs uppercase text-surface-500 font-semibold mt-4">Bootstrap</h3>
          {result.orchestrate.servers.map((os: any, i: number) => (
            <div key={i} className="bg-surface-900/50 border border-surface-800 rounded-lg p-3 text-sm">
              <div className="flex items-center gap-2">
                <span className={`px-1.5 py-0.5 rounded text-[10px] uppercase ${os.status === "ready" ? "bg-emerald-500/20 text-emerald-300" : os.status === "failed" ? "bg-red-500/20 text-red-300" : "bg-amber-500/20 text-amber-300"}`}>{os.status}</span>
                <span className="font-mono flex-1">{os.ip} · {os.region} · {os.role}</span>
              </div>
              <ul className="text-[10px] text-surface-500 mt-2 space-y-0.5">
                {(os.steps || []).map((step: string, j: number) => <li key={j}>· {step}</li>)}
              </ul>
              {os.error && <div className="text-xs text-red-400 mt-1">{os.error}</div>}
            </div>
          ))}
        </div>
      )}
      {result?.orchestrate?.caddyConfig && (
        <div>
          <h3 className="text-xs uppercase text-surface-500 font-semibold mt-4">Caddy round-robin</h3>
          <pre className="bg-surface-900/50 border border-surface-800 rounded p-2 text-[10px] font-mono overflow-auto">{result.orchestrate.caddyConfig}</pre>
        </div>
      )}
    </div>
  );
}

function Machines() {
  const [list, setList] = useState<any[]>([]);
  const [error, setError] = useState("");

  useEffect(() => { refresh(); const i = setInterval(refresh, 10000); return () => clearInterval(i); }, []);

  async function refresh() {
    try {
      const r = await agentClient.consoleMachines();
      setList(r.machines || []);
    } catch (e: any) { setError(e.message); }
  }

  const providerIcon = (p: string) => {
    switch (p) {
      case "hetzner": return "🖥️";
      case "aws": return "☁️";
      case "gcp": return "🌩️";
      case "local-mac": return "🍎";
      case "yaver-cloud": return "⚡";
      default: return "💻";
    }
  };
  const providerColor = (p: string) => {
    switch (p) {
      case "hetzner": return "bg-red-500/20 text-red-300";
      case "aws": return "bg-amber-500/20 text-amber-300";
      case "gcp": return "bg-blue-500/20 text-blue-300";
      case "local-mac":
      case "local": return "bg-emerald-500/20 text-emerald-300";
      case "yaver-cloud": return "bg-indigo-500/20 text-indigo-300";
      default: return "bg-surface-800 text-surface-400";
    }
  };

  return (
    <div className="space-y-3">
      {error && <div className="text-xs text-red-400">{error}</div>}
      <div className="text-xs text-surface-500">
        Hybrid view: own hardware + cloud VPSes managed through one UI. Pick a machine in the device bar to re-target Containers/Terminal/etc.
      </div>
      <div className="grid sm:grid-cols-2 lg:grid-cols-3 gap-3">
        {list.map((m) => (
          <div key={m.deviceId} className={`bg-surface-900/50 border rounded-lg p-3 space-y-2 ${m.isLocal ? "border-indigo-500/40" : "border-surface-800"}`}>
            <div className="flex items-center gap-2">
              <span className="text-xl">{providerIcon(m.provider)}</span>
              <div className="flex-1 min-w-0">
                <div className="text-sm font-semibold text-surface-200 truncate">{m.name}</div>
                <div className="text-[10px] font-mono text-surface-500 truncate">{m.platform}</div>
              </div>
              <span className={`w-2 h-2 rounded-full ${m.isOnline ? "bg-emerald-400" : "bg-red-400"}`} />
            </div>
            <div className="flex flex-wrap gap-1 text-[10px]">
              <span className={`px-1.5 py-0.5 rounded uppercase ${providerColor(m.provider || "unknown")}`}>{m.provider || "unknown"}</span>
              {m.isLocal && <span className="px-1.5 py-0.5 rounded bg-indigo-500/20 text-indigo-300">this machine</span>}
              {m.arch && <span className="px-1.5 py-0.5 rounded bg-surface-800 text-surface-400">{m.arch}</span>}
              {m.cost && <span className="px-1.5 py-0.5 rounded bg-surface-800 text-surface-400">{m.cost}</span>}
            </div>
            {m.uptime > 0 && (
              <div className="text-[10px] text-surface-500">uptime: {Math.floor(m.uptime / 86400)}d {Math.floor((m.uptime % 86400) / 3600)}h</div>
            )}
            {m.quicHost && (
              <div className="text-[10px] text-surface-600 font-mono truncate">{m.quicHost}:{m.quicPort}</div>
            )}
          </div>
        ))}
      </div>
    </div>
  );
}

function fmtBytes(n: number | undefined): string {
  if (!n) return "0 B";
  const units = ["B", "KB", "MB", "GB", "TB"];
  let i = 0;
  while (n >= 1024 && i < units.length - 1) { n /= 1024; i++; }
  return `${n.toFixed(1)} ${units[i]}`;
}

function fmtBps(n: number | undefined): string {
  if (!n) return "0 B/s";
  return fmtBytes(n) + "/s";
}

function fmtUptime(secs: number | undefined): string {
  if (!secs) return "—";
  const d = Math.floor(secs / 86400);
  const h = Math.floor((secs % 86400) / 3600);
  return `${d}d ${h}h`;
}
