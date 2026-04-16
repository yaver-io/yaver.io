"use client";

import { useEffect, useState } from "react";
import { agentClient } from "@/lib/agent-client";

type Tab = "ci" | "alerts" | "metrics" | "encryption" | "rotate" | "studios";

export default function ExtrasView() {
  const [directory, setDirectory] = useState("");
  const [tab, setTab] = useState<Tab>("ci");
  return (
    <div className="space-y-4">
      <input value={directory} onChange={(e) => setDirectory(e.target.value)}
        placeholder="project directory (defaults to agent cwd)"
        className="w-full rounded-lg border border-surface-700 bg-surface-900 px-3 py-2 text-sm font-mono text-surface-200" />
      <div className="flex gap-1 border-b border-surface-800 overflow-auto">
        {(["ci", "alerts", "metrics", "encryption", "rotate", "studios"] as Tab[]).map((t) => (
          <button key={t} onClick={() => setTab(t)}
            className={`px-3 py-2 text-xs uppercase font-semibold whitespace-nowrap ${tab === t ? "text-indigo-400 border-b-2 border-indigo-400" : "text-surface-500 hover:text-surface-300"}`}>
            {t}
          </button>
        ))}
      </div>
      {tab === "ci" && <CIPanel dir={directory} />}
      {tab === "alerts" && <AlertsPanel />}
      {tab === "metrics" && <MetricsPanel />}
      {tab === "encryption" && <EncryptionPanel dir={directory} />}
      {tab === "rotate" && <RotatePanel />}
      {tab === "studios" && <StudiosPanel />}
    </div>
  );
}

function CIPanel({ dir }: { dir: string }) {
  const [runs, setRuns] = useState<any[]>([]);
  const [cfg, setCfg] = useState<any>({ image: "node:20", steps: [{ name: "test", run: "npm test" }], onFail: "block-deploy" });
  const [cfgOpen, setCfgOpen] = useState(false);
  const [running, setRunning] = useState(false);

  useEffect(() => { refresh(); loadCfg(); }, [dir]);
  async function refresh() { const r = await agentClient.ciList(dir || undefined); setRuns(r.runs || []); }
  async function loadCfg() { const r = await agentClient.ciConfigGet(dir || undefined); if (r && !r.error) setCfg(r); }
  async function saveCfg() { await agentClient.ciConfigSet(cfg, dir || undefined); setCfgOpen(false); }
  async function run() {
    setRunning(true);
    const r = await agentClient.ciRun(dir || undefined);
    setRunning(false);
    alert(r.error || `CI ${r.status} (${r.steps?.length || 0} steps)`);
    refresh();
  }

  return (
    <div className="space-y-3">
      <div className="flex gap-2">
        <button onClick={run} disabled={running} className="px-4 py-2 text-sm rounded bg-indigo-500 text-white hover:bg-indigo-400 disabled:opacity-50">{running ? "Running…" : "▶ Run CI"}</button>
        <button onClick={() => setCfgOpen(true)} className="px-3 py-2 text-sm rounded bg-surface-800 text-surface-200 hover:bg-surface-700">Edit ci.yaml</button>
      </div>
      <div className="text-xs text-surface-500">CI runs before every deploy. On failure: {cfg.onFail || "block-deploy"}. Image: {cfg.image || "node:20"}</div>
      <div className="space-y-1">
        {runs.length === 0 && <div className="text-xs text-surface-500">No runs yet.</div>}
        {runs.map((r) => (
          <div key={r.id} className="bg-surface-900/50 border border-surface-800 rounded-lg p-3 text-sm">
            <div className="flex items-center gap-2">
              <span className={`px-1.5 py-0.5 rounded text-[10px] uppercase ${r.status === "passed" ? "bg-emerald-500/20 text-emerald-300" : "bg-red-500/20 text-red-300"}`}>{r.status}</span>
              <span className="flex-1 text-xs text-surface-500">{r.trigger}</span>
              <span className="text-xs text-surface-500">{r.startedAt?.slice(0, 19)}</span>
            </div>
            <div className="mt-1 space-y-0.5">
              {(r.steps || []).map((s: any, i: number) => (
                <div key={i} className="text-[11px] flex gap-2">
                  <span className={s.status === "passed" ? "text-emerald-400" : "text-red-400"}>{s.status === "passed" ? "✓" : "✗"}</span>
                  <span className="text-surface-300">{s.name}</span>
                  <span className="text-surface-500">{s.duration}</span>
                </div>
              ))}
            </div>
          </div>
        ))}
      </div>

      {cfgOpen && (
        <div className="fixed inset-0 bg-black/60 flex items-center justify-center z-50 p-4">
          <div className="bg-surface-950 border border-surface-700 rounded-xl p-5 max-w-2xl w-full space-y-3">
            <h3 className="text-sm font-semibold">.yaver/ci.yaml</h3>
            <input value={cfg.image || ""} onChange={(e) => setCfg({ ...cfg, image: e.target.value })} placeholder="Docker image (node:20, python:3.12, ...)" className="w-full rounded border border-surface-700 bg-surface-900 px-2 py-1 text-sm font-mono" />
            <select value={cfg.onFail || "block-deploy"} onChange={(e) => setCfg({ ...cfg, onFail: e.target.value })} className="w-full rounded border border-surface-700 bg-surface-900 px-2 py-1 text-sm">
              <option value="block-deploy">On fail: block deploy</option>
              <option value="warn">On fail: warn only</option>
            </select>
            <div className="space-y-1">
              <div className="text-xs text-surface-500">Steps:</div>
              {(cfg.steps || []).map((s: any, i: number) => (
                <div key={i} className="flex gap-2">
                  <input value={s.name} onChange={(e) => { const steps = [...cfg.steps]; steps[i].name = e.target.value; setCfg({ ...cfg, steps }); }} placeholder="name" className="rounded border border-surface-700 bg-surface-900 px-2 py-1 text-xs font-mono w-32" />
                  <input value={s.run} onChange={(e) => { const steps = [...cfg.steps]; steps[i].run = e.target.value; setCfg({ ...cfg, steps }); }} placeholder="shell command" className="flex-1 rounded border border-surface-700 bg-surface-900 px-2 py-1 text-xs font-mono" />
                  <button onClick={() => { const steps = cfg.steps.filter((_: any, j: number) => j !== i); setCfg({ ...cfg, steps }); }} className="text-red-400 hover:text-red-300">×</button>
                </div>
              ))}
              <button onClick={() => setCfg({ ...cfg, steps: [...(cfg.steps || []), { name: "", run: "" }] })} className="text-xs text-indigo-400 hover:text-indigo-300">+ step</button>
            </div>
            <div className="flex gap-2 pt-2">
              <button onClick={saveCfg} className="px-4 py-2 text-sm rounded bg-indigo-500 text-white hover:bg-indigo-400">Save</button>
              <button onClick={() => setCfgOpen(false)} className="px-4 py-2 text-sm rounded bg-surface-800 text-surface-200">Cancel</button>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}

function AlertsPanel() {
  const [list, setList] = useState<any[]>([]);
  const [metric, setMetric] = useState("cpu");
  const [threshold, setThreshold] = useState(80);
  const [durationSecs, setDurationSecs] = useState(300);

  useEffect(() => { refresh(); }, []);
  async function refresh() { const r = await agentClient.alertList(); setList(r.alerts || []); }
  async function add() {
    await agentClient.alertAdd({ metric, threshold, durationSecs, label: `${metric} ≥ ${threshold}%` });
    refresh();
  }
  async function rem(id: string) { await agentClient.alertRemove(id); refresh(); }
  return (
    <div className="space-y-3">
      <div className="text-xs text-surface-500">Alerts fire when a metric stays sustained above threshold for the chosen window. Routed to mobile push via Yaver notifications.</div>
      <div className="flex gap-2 items-center">
        <select value={metric} onChange={(e) => setMetric(e.target.value)} className="rounded border border-surface-700 bg-surface-900 px-2 py-1 text-sm">
          <option value="cpu">CPU %</option>
          <option value="ram">RAM %</option>
          <option value="disk">Disk %</option>
        </select>
        <input type="number" value={threshold} onChange={(e) => setThreshold(+e.target.value)} min={1} max={100} className="w-20 rounded border border-surface-700 bg-surface-900 px-2 py-1 text-sm" />
        <span className="text-xs text-surface-500">≥</span>
        <input type="number" value={durationSecs} onChange={(e) => setDurationSecs(+e.target.value)} className="w-24 rounded border border-surface-700 bg-surface-900 px-2 py-1 text-sm" />
        <span className="text-xs text-surface-500">sec</span>
        <button onClick={add} className="px-3 py-1 text-sm rounded bg-indigo-500 text-white hover:bg-indigo-400">+ Alert</button>
      </div>
      <div className="space-y-1">
        {list.map((a) => (
          <div key={a.id} className="flex items-center gap-3 bg-surface-900/50 border border-surface-800 rounded-lg p-2 text-sm">
            <span className="tag bg-amber-500/20 text-amber-300 px-1.5 py-0.5 rounded text-[10px]">{a.metric}</span>
            <span className="flex-1 font-mono">{a.label || `${a.metric} ≥ ${a.threshold}%`}</span>
            <span className="text-xs text-surface-500">{a.durationSecs}s</span>
            {a.lastFiredAt && <span className="text-xs text-red-400">fired {a.lastFiredAt.slice(0, 16)}</span>}
            <button onClick={() => rem(a.id)} className="text-xs text-red-400 hover:text-red-300">×</button>
          </div>
        ))}
      </div>
    </div>
  );
}

function MetricsPanel() {
  const [samples, setSamples] = useState<any[]>([]);
  const [window, setWindow] = useState("1h");
  useEffect(() => { (async () => setSamples((await agentClient.metricsHistory(window)).samples || []))(); }, [window]);

  const cpuPoints = samples.slice(0, 200).reverse().map((s, i, arr) => ({ x: (i / Math.max(1, arr.length - 1)) * 100, y: 100 - (s.cpuPct || 0) }));
  const ramPoints = samples.slice(0, 200).reverse().map((s, i, arr) => ({ x: (i / Math.max(1, arr.length - 1)) * 100, y: 100 - (s.ramPct || 0) }));

  return (
    <div className="space-y-4">
      <div className="flex gap-2">
        {["15m", "1h", "6h", "24h", "7d"].map((w) => (
          <button key={w} onClick={() => setWindow(w)}
            className={`px-2 py-1 text-xs rounded ${window === w ? "bg-indigo-500/30 text-indigo-300" : "bg-surface-800 text-surface-400"}`}>{w}</button>
        ))}
      </div>
      <MetricChart title="CPU %" points={cpuPoints} color="#818cf8" samples={samples.map(s => s.cpuPct)} />
      <MetricChart title="RAM %" points={ramPoints} color="#34d399" samples={samples.map(s => s.ramPct)} />
      <div className="text-xs text-surface-500">{samples.length} samples · 7-day retention</div>
    </div>
  );
}

function MetricChart({ title, points, color, samples }: { title: string; points: { x: number; y: number }[]; color: string; samples: number[] }) {
  const latest = samples[0] || 0;
  const avg = samples.length > 0 ? samples.reduce((a, b) => a + b, 0) / samples.length : 0;
  const max = samples.length > 0 ? Math.max(...samples) : 0;
  return (
    <div className="bg-surface-900/50 border border-surface-800 rounded-lg p-3">
      <div className="flex items-center gap-3 text-xs text-surface-400 mb-2">
        <span className="font-semibold uppercase text-surface-500">{title}</span>
        <span>now: <span className="text-surface-200 font-mono">{latest.toFixed(1)}%</span></span>
        <span>avg: <span className="font-mono">{avg.toFixed(1)}%</span></span>
        <span>max: <span className="font-mono">{max.toFixed(1)}%</span></span>
      </div>
      <svg viewBox="0 0 100 100" className="w-full h-24" preserveAspectRatio="none">
        <polyline points={points.map(p => `${p.x},${p.y}`).join(" ")} fill="none" stroke={color} strokeWidth="0.5" />
      </svg>
    </div>
  );
}

function EncryptionPanel({ dir }: { dir: string }) {
  const [enabled, setEnabled] = useState(false);
  useEffect(() => { (async () => setEnabled((await agentClient.backupEncryptionGet(dir || undefined)).enabled))(); }, [dir]);
  async function toggle() {
    const next = !enabled;
    await agentClient.backupEncryptionSet(next, dir || undefined);
    setEnabled(next);
  }
  return (
    <div className="space-y-3">
      <div className="rounded-lg border border-surface-800 bg-surface-900/50 p-4 space-y-2">
        <div className="flex items-center gap-3">
          <span className={`w-3 h-3 rounded-full ${enabled ? "bg-emerald-400" : "bg-surface-600"}`} />
          <span className="font-semibold text-sm flex-1">Backup encryption at rest</span>
          <button onClick={toggle} className={`px-3 py-1 text-xs rounded ${enabled ? "bg-red-500/20 text-red-300" : "bg-emerald-500/20 text-emerald-300"}`}>
            {enabled ? "Disable" : "Enable"}
          </button>
        </div>
        <div className="text-xs text-surface-500">
          When enabled, new backups get an <code>.enc</code> extension; plaintext is removed.
          Uses AES-GCM with <code>~/.yaver/master.key</code> (same key that protects your
          cloud provider credentials). Restore auto-decrypts to a temp file and cleans up after replay.
          <br/><br/>
          <strong className="text-amber-400">Back up <code>~/.yaver/master.key</code> off-machine</strong> —
          losing it means you can't restore encrypted backups.
        </div>
      </div>
    </div>
  );
}

function RotatePanel() {
  const [provider, setProvider] = useState("stripe");
  const [opts, setOpts] = useState<Record<string, string>>({});
  const [result, setResult] = useState<any>(null);
  async function rotate() {
    const r = await agentClient.providerRotate(provider, opts);
    setResult(r);
  }
  const fieldsByProvider: Record<string, { key: string; placeholder: string; secret?: boolean }[]> = {
    stripe: [
      { key: "apiKey", placeholder: "sk_live_... (or use connected account)", secret: true },
      { key: "webhookId", placeholder: "we_... (existing webhook to replace)" },
      { key: "url", placeholder: "https://myapp.com/api/webhook" },
      { key: "events", placeholder: "charge.succeeded,invoice.paid" },
      { key: "deleteId", placeholder: "we_old (optional — delete old webhook)" },
    ],
    aws: [{ key: "username", placeholder: "IAM user name" }],
    cloudflare: [{ key: "tokenId", placeholder: "token ID from the API Tokens page" }],
    hetzner: [],
    vercel: [],
    github: [],
  };
  const fields = fieldsByProvider[provider] || [];
  return (
    <div className="space-y-3">
      <div className="text-xs text-surface-500">Rotate a credential at the upstream provider. Uses your connected account token. Falls back to manual steps when the provider lacks an API.</div>
      <select value={provider} onChange={(e) => { setProvider(e.target.value); setOpts({}); setResult(null); }}
        className="rounded border border-surface-700 bg-surface-900 px-3 py-2 text-sm">
        <option value="stripe">Stripe — webhook signing secret</option>
        <option value="aws">AWS IAM — access key</option>
        <option value="cloudflare">Cloudflare — API token</option>
        <option value="hetzner">Hetzner — API token (manual)</option>
        <option value="vercel">Vercel — token (manual)</option>
        <option value="github">GitHub — PAT (manual)</option>
      </select>
      {fields.map((f) => (
        <input key={f.key} type={f.secret ? "password" : "text"}
          placeholder={f.placeholder} value={opts[f.key] || ""}
          onChange={(e) => setOpts({ ...opts, [f.key]: e.target.value })}
          className="w-full rounded border border-surface-700 bg-surface-900 px-3 py-2 text-sm font-mono" />
      ))}
      <button onClick={rotate} className="px-4 py-2 text-sm rounded bg-indigo-500 text-white hover:bg-indigo-400">🔑 Rotate</button>
      {result && (
        <div className="bg-surface-900/50 border border-surface-800 rounded-lg p-3 text-xs font-mono">
          {result.newSecret && <div className="text-emerald-400">new secret: <code>{result.newSecret}</code></div>}
          {result.error && <div className="text-red-400">{result.error}</div>}
          {result.manualSteps && result.manualSteps.map((s: string, i: number) => <div key={i} className="text-surface-300">{s}</div>)}
        </div>
      )}
    </div>
  );
}

function StudiosPanel() {
  const [list, setList] = useState<any[]>([]);
  const [active, setActive] = useState<string | null>(null);
  const [proxyUrl, setProxyUrl] = useState<string>("");
  useEffect(() => { (async () => setList((await agentClient.studioList()).studios || []))(); }, []);
  useEffect(() => {
    let cancelled = false;
    if (!active) {
      setProxyUrl("");
      return;
    }
    void (async () => {
      try {
        const url = await agentClient.studioProxyUrl(active);
        if (!cancelled) setProxyUrl(url);
      } catch {
        if (!cancelled) setProxyUrl("");
      }
    })();
    return () => { cancelled = true; };
  }, [active]);
  if (active) {
    const studio = list.find(s => s.id === active);
    return (
      <div className="space-y-3">
        <div className="flex items-center gap-2">
          <button onClick={() => setActive(null)} className="text-sm text-indigo-400">← Back</button>
          <span className="text-sm font-semibold">{studio?.label}</span>
          <span className="text-xs text-surface-500 font-mono">{studio?.url}</span>
        </div>
        {proxyUrl ? (
          <iframe src={proxyUrl}
            className="w-full h-[70vh] rounded-lg border border-surface-800" />
        ) : (
          <div className="w-full h-[70vh] rounded-lg border border-surface-800 bg-surface-950/40 flex items-center justify-center text-sm text-surface-500">
            Opening studio…
          </div>
        )}
      </div>
    );
  }
  return (
    <div className="space-y-3">
      <div className="text-xs text-surface-500">
        Local database + email + storage dashboards, tunneled through the agent so they work from
        any browser/phone. Click to embed.
      </div>
      <div className="grid sm:grid-cols-2 gap-2">
        {list.map((s) => (
          <button key={s.id} onClick={() => s.running && setActive(s.id)}
            disabled={!s.running}
            className={`text-left bg-surface-900/50 border rounded-lg p-3 transition ${s.running ? "border-surface-800 hover:border-indigo-500" : "border-surface-800 opacity-50"}`}>
            <div className="flex items-center gap-2">
              <span className={`w-2 h-2 rounded-full ${s.running ? "bg-emerald-400" : "bg-red-400"}`} />
              <span className="text-sm font-semibold">{s.label}</span>
            </div>
            <div className="text-[10px] font-mono text-surface-500 truncate mt-1">{s.url}</div>
            {!s.running && <div className="text-[10px] text-surface-500 mt-1">service not running</div>}
          </button>
        ))}
      </div>
    </div>
  );
}
