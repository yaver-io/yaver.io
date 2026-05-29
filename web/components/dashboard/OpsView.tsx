"use client";

import { useEffect, useState } from "react";
import { agentClient } from "@/lib/agent-client";
import EnvironmentSwitcher from "./EnvironmentSwitcher";

type Tab = "deploy" | "backups" | "domains" | "logs" | "errors" | "clone" | "cron" | "uptime";

export default function OpsView() {
  const [directory, setDirectory] = useState("");
  const [tab, setTab] = useState<Tab>("deploy");
  return (
    <div className="space-y-4">
      <input value={directory} onChange={(e) => setDirectory(e.target.value)}
        placeholder="project directory (defaults to agent cwd)"
        className="w-full rounded-lg border border-surface-700 bg-surface-900 px-3 py-2 text-sm font-mono text-surface-200" />
      <EnvironmentSwitcher directory={directory || undefined} />
      <div className="flex gap-1 border-b border-surface-800 overflow-auto">
        {(["deploy", "backups", "domains", "logs", "errors", "clone", "cron", "uptime"] as Tab[]).map((t) => (
          <button key={t} onClick={() => setTab(t)}
            className={`px-3 py-2 text-xs uppercase font-semibold whitespace-nowrap ${tab === t ? "text-indigo-400 border-b-2 border-indigo-400" : "text-surface-500 hover:text-surface-300"}`}>
            {t}
          </button>
        ))}
      </div>
      {tab === "deploy" && <Deploy directory={directory} />}
      {tab === "backups" && <Backups directory={directory} />}
      {tab === "domains" && <Domains />}
      {tab === "logs" && <LogSearch />}
      {tab === "errors" && <Errors />}
      {tab === "clone" && <Clone />}
      {tab === "cron" && <Cron directory={directory} />}
      {tab === "uptime" && <Uptime />}
    </div>
  );
}

function Deploy({ directory }: { directory: string }) {
  const [list, setList] = useState<any[]>([]);
  const [running, setRunning] = useState(false);
  const [cfg, setCfg] = useState<any>({ branch: "main", autoDeploy: false });
  const [preview, setPreview] = useState<any>(null);
  const [msg, setMsg] = useState<{ type: "ok" | "error"; text: string } | null>(null);

  useEffect(() => {
    refresh();
    (async () => {
      try { setCfg(await agentClient.deployConfigGet(directory || undefined)); }
      catch { /* keep defaults */ }
    })();
  }, [directory]);
  async function refresh() {
    try {
      const r = await agentClient.deployList(directory || undefined);
      setList(r.deploys || []);
    } catch {
      setMsg({ type: "error", text: "Couldn't load deploys — the agent may be unreachable." });
    }
  }
  async function openPreview() {
    setMsg(null);
    try {
      const p = await agentClient.deployPreview(directory || undefined);
      setPreview(p);
    } catch {
      setMsg({ type: "error", text: "Couldn't build the deploy preview — the agent may be unreachable." });
    }
  }
  async function confirmRun() {
    setPreview(null);
    setRunning(true);
    setMsg(null);
    try {
      await agentClient.deployRun(directory || undefined);
    } catch {
      setMsg({ type: "error", text: "Deploy failed to start. Check the agent logs and try again." });
    }
    setRunning(false);
    refresh();
  }
  async function rollback(id: string) {
    if (!confirm("Rollback to this deploy's commit?")) return;
    try {
      await agentClient.deployRollback(id, directory || undefined);
    } catch {
      setMsg({ type: "error", text: "Rollback failed. Please try again." });
    }
    refresh();
  }
  async function saveCfg() {
    setMsg(null);
    try {
      await agentClient.deployConfigSet(cfg, directory || undefined);
      setMsg({ type: "ok", text: "Config saved." });
    } catch {
      setMsg({ type: "error", text: "Couldn't save config — the agent may be unreachable." });
    }
  }

  return (
    <div className="space-y-4">
      <div className="bg-surface-900/50 border border-surface-800 rounded-lg p-3 space-y-2 text-sm">
        <div className="grid sm:grid-cols-3 gap-2">
          <input value={cfg.branch || ""} onChange={(e) => setCfg({ ...cfg, branch: e.target.value })} placeholder="branch" className="rounded border border-surface-700 bg-surface-900 px-2 py-1 font-mono text-xs" />
          <input value={cfg.healthcheck || ""} onChange={(e) => setCfg({ ...cfg, healthcheck: e.target.value })} placeholder="healthcheck URL" className="rounded border border-surface-700 bg-surface-900 px-2 py-1 font-mono text-xs" />
          <input value={cfg.buildCommand || ""} onChange={(e) => setCfg({ ...cfg, buildCommand: e.target.value })} placeholder="build command (optional)" className="rounded border border-surface-700 bg-surface-900 px-2 py-1 font-mono text-xs" />
          <input value={cfg.webhookSecret || ""} onChange={(e) => setCfg({ ...cfg, webhookSecret: e.target.value })} placeholder="webhook secret" className="rounded border border-surface-700 bg-surface-900 px-2 py-1 font-mono text-xs" />
          <label className="flex items-center gap-1 text-xs"><input type="checkbox" checked={!!cfg.autoDeploy} onChange={(e) => setCfg({ ...cfg, autoDeploy: e.target.checked })} /> auto-deploy on push</label>
          <button onClick={saveCfg} className="px-2 py-1 text-xs rounded bg-surface-800 hover:bg-surface-700">Save config</button>
        </div>
        <div className="text-[10px] text-surface-500">Webhook URL: <code>/deploy/webhook?project={encodeURIComponent(directory || "<dir>")}</code></div>
      </div>
      <div className="flex gap-2">
        <button onClick={openPreview} disabled={running} className="px-4 py-2 text-sm rounded-lg bg-indigo-500 text-white hover:bg-indigo-400 disabled:opacity-50">{running ? "Deploying…" : "🚀 Deploy now"}</button>
        <button onClick={refresh} className="px-3 py-2 text-sm rounded-lg bg-surface-800 text-surface-200 hover:bg-surface-700">Refresh</button>
      </div>
      {msg && <div className={`text-sm ${msg.type === "ok" ? "text-emerald-400" : "text-red-400"}`}>{msg.text}</div>}
      {preview && <PreviewModal preview={preview} onConfirm={confirmRun} onClose={() => setPreview(null)} />}
      <div className="space-y-1">
        {list.map((d) => (
          <div key={d.id} className="bg-surface-900/50 border border-surface-800 rounded-lg p-3 text-sm">
            <div className="flex items-center gap-2">
              <span className={`px-1.5 py-0.5 rounded text-[10px] uppercase ${d.status === "success" ? "bg-emerald-500/20 text-emerald-300" : d.status === "failed" ? "bg-red-500/20 text-red-300" : d.status === "rolled-back" ? "bg-amber-500/20 text-amber-300" : "bg-surface-800 text-surface-400"}`}>{d.status}</span>
              <span className="font-mono text-surface-400 text-xs">{d.commit?.slice(0, 8)}</span>
              <span className="flex-1 truncate">{d.message || "(no message)"}</span>
              <span className="text-xs text-surface-500">{d.duration}</span>
              {d.status === "success" && <button onClick={() => rollback(d.id)} className="text-xs text-amber-400 hover:text-amber-300">Rollback</button>}
            </div>
          </div>
        ))}
      </div>
    </div>
  );
}

function PreviewModal({ preview, onConfirm, onClose }: { preview: any; onConfirm: () => void; onClose: () => void }) {
  const p = preview;
  const hasBlockers = p.warnings?.length > 0 && p.warnings.some((w: string) =>
    w.includes("PRODUCTION") || w.includes("uncommitted"));
  return (
    <div className="fixed inset-0 bg-black/60 flex items-center justify-center z-50 p-4" onClick={onClose}>
      <div className="bg-surface-950 border border-surface-700 rounded-xl p-5 max-w-2xl w-full space-y-3 max-h-[90vh] overflow-auto" onClick={(e) => e.stopPropagation()}>
        <div className="flex items-center justify-between">
          <h3 className="text-base font-semibold">Pre-deploy check</h3>
          <button onClick={onClose} className="text-xs text-surface-500 hover:text-surface-300">close</button>
        </div>

        {p.warnings?.length > 0 && (
          <div className={`rounded-lg border p-3 text-xs space-y-1 ${hasBlockers ? "border-amber-500/40 bg-amber-500/10 text-amber-200" : "border-surface-700 bg-surface-900 text-surface-300"}`}>
            {p.warnings.map((w: string, i: number) => <div key={i}>⚠️ {w}</div>)}
          </div>
        )}

        <div className="space-y-2 text-sm">
          <Row label="Branch" value={p.branch || "(not a git repo)"} />
          {p.lastCommit && <Row label="Last commit" value={<span><code className="text-indigo-300">{p.lastCommit}</code> {p.lastMessage}</span>} />}
          <Row label="Ahead / behind" value={`${p.ahead || 0} ahead · ${p.behind || 0} behind`} />
          <Row label="Uncommitted" value={p.dirty ? `${p.dirtyFiles.length} file(s) — will NOT deploy` : "clean"} />
          <Row label="Active env" value={<span className={p.activeEnv === "production" ? "text-red-300" : ""}>{p.activeEnv}</span>} />
          <Row label="CI gate" value={p.ciConfigured ? `${p.ciSteps} step(s) · onFail=${p.ciOnFail}` : "not configured"} />
          <Row label="DB migrations" value={p.migrator ? `${p.migrator} (${p.migratorCmd})` : "none detected"} />
          <Row label="Healthcheck" value={p.healthcheck ? `${p.healthcheck}${p.healthInferred ? " (auto-inferred)" : ""}` : "none"} />
          {p.lastDeploy && (
            <Row label="Last deploy" value={<span><span className={p.lastDeploy.status === "success" ? "text-emerald-400" : "text-red-400"}>{p.lastDeploy.status}</span> · {p.lastDeploy.commit?.slice(0, 8)} · {p.lastDeploy.duration}</span>} />
          )}
        </div>

        {p.dirtyFiles?.length > 0 && (
          <details className="rounded bg-surface-900/50 border border-surface-800 p-2">
            <summary className="text-xs text-surface-400 cursor-pointer">Uncommitted files ({p.dirtyFiles.length})</summary>
            <pre className="mt-1 text-[10px] text-surface-500 font-mono max-h-32 overflow-auto">{p.dirtyFiles.join("\n")}</pre>
          </details>
        )}

        <div className="flex gap-2 pt-2">
          <button onClick={onConfirm}
            className={`flex-1 px-4 py-2 text-sm rounded-lg font-semibold ${hasBlockers ? "bg-amber-500 hover:bg-amber-400" : "bg-indigo-500 hover:bg-indigo-400"} text-white`}>
            {hasBlockers ? "⚠️ Deploy anyway" : "🚀 Deploy"}
          </button>
          <button onClick={onClose} className="px-4 py-2 text-sm rounded-lg bg-surface-800 text-surface-200">Cancel</button>
        </div>
      </div>
    </div>
  );
}

function Row({ label, value }: { label: string; value: React.ReactNode }) {
  return (
    <div className="flex gap-3 text-xs">
      <span className="w-32 text-surface-500">{label}</span>
      <span className="flex-1 text-surface-200 font-mono">{value}</span>
    </div>
  );
}

function Backups({ directory }: { directory: string }) {
  const [list, setList] = useState<any[]>([]);
  const [msg, setMsg] = useState<{ type: "ok" | "error"; text: string } | null>(null);
  useEffect(() => { refresh(); }, [directory]);
  async function refresh() {
    try {
      const r = await agentClient.backupList(directory || undefined);
      setList(r.backups || []);
    } catch {
      setMsg({ type: "error", text: "Couldn't load backups — the agent may be unreachable." });
    }
  }
  async function create() {
    setMsg(null);
    try {
      await agentClient.backupCreate(directory || undefined);
      setMsg({ type: "ok", text: "Snapshot created." });
    } catch {
      setMsg({ type: "error", text: "Snapshot failed. Please try again." });
    }
    refresh();
  }
  async function restore(id: string) {
    if (!confirm("Restore this backup? Current data will be overwritten.")) return;
    setMsg(null);
    try {
      await agentClient.backupRestore(id, directory || undefined);
      setMsg({ type: "ok", text: "Backup restored." });
    } catch {
      setMsg({ type: "error", text: "Restore failed. Please try again." });
    }
  }
  async function del(id: string) {
    if (!confirm("Delete this backup?")) return;
    try {
      await agentClient.backupDelete(id, directory || undefined);
    } catch {
      setMsg({ type: "error", text: "Couldn't delete the backup. Please try again." });
    }
    refresh();
  }
  async function toggleAuto() {
    setMsg(null);
    try {
      await agentClient.backupAuto(true, 24, directory || undefined);
      setMsg({ type: "ok", text: "Daily auto-backups enabled." });
    } catch {
      setMsg({ type: "error", text: "Couldn't enable auto-backups — the agent may be unreachable." });
    }
  }

  return (
    <div className="space-y-3">
      <div className="flex gap-2">
        <button onClick={create} className="px-3 py-2 text-sm rounded bg-indigo-500 text-white hover:bg-indigo-400">Snapshot now</button>
        <button onClick={toggleAuto} className="px-3 py-2 text-sm rounded bg-surface-800 text-surface-200 hover:bg-surface-700">Enable daily auto-backup</button>
      </div>
      {msg && <div className={`text-sm ${msg.type === "ok" ? "text-emerald-400" : "text-red-400"}`}>{msg.text}</div>}
      <div className="space-y-1">
        {list.map((b) => (
          <div key={b.id} className="bg-surface-900/50 border border-surface-800 rounded-lg p-3 text-sm flex items-center gap-3">
            <span className="font-mono text-xs text-surface-400">{b.id}</span>
            <span className="text-xs text-surface-500">{b.backend}</span>
            <span className="flex-1 truncate text-[10px] font-mono text-surface-600">{b.path}</span>
            <span className="text-xs text-surface-500">{fmtBytes(b.size)}</span>
            <button onClick={() => restore(b.id)} className="text-xs text-emerald-400 hover:text-emerald-300">Restore</button>
            <button onClick={() => del(b.id)} className="text-xs text-red-400 hover:text-red-300">Delete</button>
          </div>
        ))}
      </div>
    </div>
  );
}

function Domains() {
  const [list, setList] = useState<any[]>([]);
  const [domain, setDomain] = useState(""); const [upstream, setUpstream] = useState("");
  const [msg, setMsg] = useState<string | null>(null);
  useEffect(() => { refresh(); }, []);
  async function refresh() {
    try { const r = await agentClient.domainList(); setList(r.domains || []); }
    catch { setMsg("Couldn't load domains — the agent may be unreachable."); }
  }
  async function add() {
    setMsg(null);
    try {
      const r = await agentClient.domainAdd(domain, upstream);
      if (r.error) {
        const e = String(r.error);
        setMsg(e.trim() && e.length <= 160 ? e : "Couldn't add the domain. Check the values and try again.");
      } else { setDomain(""); setUpstream(""); refresh(); }
    } catch {
      setMsg("Couldn't add the domain — the agent may be unreachable.");
    }
  }
  async function remove(d: string) {
    if (!confirm(`Remove ${d}?`)) return;
    try { await agentClient.domainRemove(d); }
    catch { setMsg("Couldn't remove the domain. Please try again."); }
    refresh();
  }
  return (
    <div className="space-y-3">
      <div className="flex gap-2">
        <input value={domain} onChange={(e) => setDomain(e.target.value)} placeholder="app.example.com" className="flex-1 rounded border border-surface-700 bg-surface-900 px-2 py-1 text-sm font-mono" />
        <input value={upstream} onChange={(e) => setUpstream(e.target.value)} placeholder="localhost:3000" className="flex-1 rounded border border-surface-700 bg-surface-900 px-2 py-1 text-sm font-mono" />
        <button onClick={add} className="px-3 py-1 text-sm rounded bg-indigo-500 text-white hover:bg-indigo-400">Add</button>
      </div>
      {msg && <div className="text-sm text-red-400">{msg}</div>}
      <div className="text-xs text-surface-500">Domains are served by Caddy with automatic Let's Encrypt certs. Point your DNS at this machine's IP.</div>
      <div className="space-y-1">
        {list.map((r) => (
          <div key={r.id} className="bg-surface-900/50 border border-surface-800 rounded-lg p-3 text-sm flex items-center gap-3">
            <span className="font-mono">{r.domain}</span>
            <span className="text-surface-500">→</span>
            <span className="font-mono flex-1 text-surface-300">{r.upstream || r.static}</span>
            <span className={`text-[10px] ${r.enabled ? "text-emerald-400" : "text-surface-500"}`}>{r.enabled ? "ACTIVE" : "DISABLED"}</span>
            <button onClick={() => remove(r.domain)} className="text-xs text-red-400 hover:text-red-300">Remove</button>
          </div>
        ))}
      </div>
    </div>
  );
}

function LogSearch() {
  const [q, setQ] = useState(""); const [hits, setHits] = useState<any[]>([]);
  const [msg, setMsg] = useState<{ type: "ok" | "error"; text: string } | null>(null);
  async function search() {
    setMsg(null);
    try { const r = await agentClient.logSearch(q || "*"); setHits(r.hits || []); }
    catch { setMsg({ type: "error", text: "Search failed — the log indexer may not be running yet." }); }
  }
  async function startIndex() {
    setMsg(null);
    try {
      await agentClient.logIndexStart();
      setMsg({ type: "ok", text: "Log indexer started — give it a minute to capture logs." });
    } catch {
      setMsg({ type: "error", text: "Couldn't start the log indexer — the agent may be unreachable." });
    }
  }
  return (
    <div className="space-y-3">
      <div className="flex gap-2">
        <input value={q} onChange={(e) => setQ(e.target.value)} onKeyDown={(e) => e.key === "Enter" && search()} placeholder="FTS5 query (e.g. error OR timeout)" className="flex-1 rounded border border-surface-700 bg-surface-900 px-2 py-1 text-sm font-mono" />
        <button onClick={search} className="px-3 py-1 text-sm rounded bg-indigo-500 text-white hover:bg-indigo-400">Search</button>
        <button onClick={startIndex} className="px-3 py-1 text-sm rounded bg-surface-800 text-surface-200">Start indexer</button>
      </div>
      {msg && <div className={`text-sm ${msg.type === "ok" ? "text-emerald-400" : "text-red-400"}`}>{msg.text}</div>}
      <pre className="text-[10px] font-mono bg-surface-900/50 border border-surface-800 rounded-lg p-3 overflow-auto max-h-[500px]">
        {hits.map((h, i) => <div key={i}><span className="text-indigo-400">[{h.service}]</span> <span className="text-surface-500">{h.ts?.slice(11, 19)}</span> {h.line}</div>)}
      </pre>
    </div>
  );
}

function Errors() {
  const [groups, setGroups] = useState<any[]>([]);
  const [msg, setMsg] = useState<string | null>(null);
  useEffect(() => {
    (async () => {
      try { setGroups((await agentClient.errorGroups()).groups || []); }
      catch { setMsg("Couldn't load errors — the agent may be unreachable."); }
    })();
  }, []);
  async function resolve(fp: string, v: boolean) {
    try {
      await agentClient.errorResolve(fp, v);
      setGroups((await agentClient.errorGroups()).groups || []);
    } catch {
      setMsg("Couldn't update that error. Please try again.");
    }
  }
  return (
    <div className="space-y-3">
      <div className="text-xs text-surface-500">
        Ingest from your frontend app with:
        <pre className="mt-1 text-[10px] font-mono bg-surface-900 border border-surface-800 rounded p-2 overflow-auto">{`fetch('${window.location.origin}/errors/ingest', { method: 'POST', body: JSON.stringify({ message: err.message, stack: err.stack, url: location.href, userId: '...' }) })`}</pre>
      </div>
      {msg && <div className="text-sm text-red-400">{msg}</div>}
      <div className="space-y-1">
        {groups.length === 0 && !msg && <div className="text-xs text-surface-500">No errors yet 🎉</div>}
        {groups.map((g) => (
          <div key={g.fingerprint} className={`bg-surface-900/50 border rounded-lg p-3 text-sm ${g.resolved ? "border-surface-800 opacity-50" : "border-surface-800"}`}>
            <div className="flex items-center gap-2">
              <span className="px-1.5 py-0.5 rounded text-[10px] bg-red-500/20 text-red-300">{g.count}</span>
              <span className="flex-1 truncate font-mono">{g.message}</span>
              <span className="text-[10px] text-surface-500">{g.lastSeen?.slice(0, 19)}</span>
              <button onClick={() => resolve(g.fingerprint, !g.resolved)} className="text-xs text-indigo-400 hover:text-indigo-300">{g.resolved ? "Unresolve" : "Resolve"}</button>
            </div>
            {g.lastStack && <pre className="text-[10px] text-surface-500 mt-1 line-clamp-3">{g.lastStack}</pre>}
          </div>
        ))}
      </div>
    </div>
  );
}

function Clone() {
  const [source, setSource] = useState(""); const [target, setTarget] = useState("");
  const [result, setResult] = useState<any>(null);
  const [msg, setMsg] = useState<string | null>(null);
  async function run() {
    setMsg(null);
    try { setResult(await agentClient.envClone(source, target)); }
    catch { setMsg("Clone failed — the agent may be unreachable. Check the paths and try again."); }
  }
  return (
    <div className="space-y-3">
      <input value={source} onChange={(e) => setSource(e.target.value)} placeholder="source project dir (prod)" className="w-full rounded border border-surface-700 bg-surface-900 px-2 py-1 text-sm font-mono" />
      <input value={target} onChange={(e) => setTarget(e.target.value)} placeholder="target project dir (staging)" className="w-full rounded border border-surface-700 bg-surface-900 px-2 py-1 text-sm font-mono" />
      <button onClick={run} className="px-3 py-2 text-sm rounded bg-indigo-500 text-white hover:bg-indigo-400">Clone prod → staging</button>
      {msg && <div className="text-sm text-red-400">{msg}</div>}
      {result && <pre className="text-[10px] font-mono bg-surface-900/50 border border-surface-800 rounded p-2 overflow-auto">{JSON.stringify(result, null, 2)}</pre>}
    </div>
  );
}

function Cron({ directory }: { directory: string }) {
  const [name, setName] = useState(""); const [schedule, setSchedule] = useState("0 * * * *"); const [target, setTarget] = useState("");
  const [result, setResult] = useState<any>(null);
  const [msg, setMsg] = useState<string | null>(null);
  async function add() {
    setMsg(null);
    try { setResult(await agentClient.cronCreate(name, schedule, target, directory || undefined)); }
    catch { setMsg("Couldn't create the cron job — the agent may be unreachable."); }
  }
  return (
    <div className="space-y-3">
      <div className="text-xs text-surface-500">Writes to <code>pg_cron</code> on Postgres/Supabase. On Convex, returns a snippet to paste into <code>convex/crons.ts</code>.</div>
      <input value={name} onChange={(e) => setName(e.target.value)} placeholder="job name" className="w-full rounded border border-surface-700 bg-surface-900 px-2 py-1 text-sm font-mono" />
      <input value={schedule} onChange={(e) => setSchedule(e.target.value)} placeholder="cron expression (0 * * * *)" className="w-full rounded border border-surface-700 bg-surface-900 px-2 py-1 text-sm font-mono" />
      <input value={target} onChange={(e) => setTarget(e.target.value)} placeholder='SQL for pg_cron, or module.func for Convex' className="w-full rounded border border-surface-700 bg-surface-900 px-2 py-1 text-sm font-mono" />
      <button onClick={add} className="px-3 py-2 text-sm rounded bg-indigo-500 text-white hover:bg-indigo-400">Add cron</button>
      {msg && <div className="text-sm text-red-400">{msg}</div>}
      {result && <pre className="text-[10px] font-mono bg-surface-900/50 border border-surface-800 rounded p-2 overflow-auto">{JSON.stringify(result, null, 2)}</pre>}
    </div>
  );
}

function Uptime() {
  const [list, setList] = useState<any[]>([]);
  const [url, setUrl] = useState(""); const [name, setName] = useState("");
  const [msg, setMsg] = useState<string | null>(null);
  useEffect(() => { refresh(); const i = setInterval(refresh, 5000); return () => clearInterval(i); }, []);
  async function refresh() {
    try { setList((await agentClient.uptimeList()).monitors || []); }
    catch { setMsg("Couldn't load monitors — the agent may be unreachable."); }
  }
  async function add() {
    setMsg(null);
    try { await agentClient.uptimeAdd({ url, name, intervalSeconds: 60, alertOnDown: true }); setUrl(""); setName(""); refresh(); }
    catch { setMsg("Couldn't add the monitor. Check the URL and try again."); }
  }
  async function remove(id: string) {
    if (!confirm("Remove?")) return;
    try { await agentClient.uptimeRemove(id); }
    catch { setMsg("Couldn't remove the monitor. Please try again."); }
    refresh();
  }
  return (
    <div className="space-y-3">
      <div className="flex gap-2">
        <input value={url} onChange={(e) => setUrl(e.target.value)} placeholder="https://myapp.com/health" className="flex-1 rounded border border-surface-700 bg-surface-900 px-2 py-1 text-sm font-mono" />
        <input value={name} onChange={(e) => setName(e.target.value)} placeholder="label (optional)" className="rounded border border-surface-700 bg-surface-900 px-2 py-1 text-sm" />
        <button onClick={add} className="px-3 py-1 text-sm rounded bg-indigo-500 text-white hover:bg-indigo-400">Monitor</button>
      </div>
      {msg && <div className="text-sm text-red-400">{msg}</div>}
      <div className="text-xs text-surface-500">Monitors ping every 60s. When a target flips up→down you get a phone push via the Yaver notification system.</div>
      <div className="space-y-1">
        {list.map((m) => (
          <div key={m.id} className="bg-surface-900/50 border border-surface-800 rounded-lg p-3 text-sm flex items-center gap-3">
            <span className={`w-2 h-2 rounded-full ${m.status === "up" ? "bg-emerald-400" : m.status === "down" ? "bg-red-400" : "bg-surface-500"}`} />
            <span className="font-mono flex-1 truncate">{m.name || m.url}</span>
            <span className="text-xs text-surface-500">{m.lastLatencyMs}ms</span>
            <span className="text-xs text-surface-500">{m.lastCheck?.slice(11, 19)}</span>
            <button onClick={() => remove(m.id)} className="text-xs text-red-400 hover:text-red-300">×</button>
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
