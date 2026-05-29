"use client";

import { useEffect, useState } from "react";
import { agentClient } from "@/lib/agent-client";
import EnvironmentSwitcher from "./EnvironmentSwitcher";

// One screen per project — everything scoped to a single directory:
// env switcher, services, recent deploys, backend status, domains, data summary.
// Opened from the Projects list (per-project card) and from Home quick actions.
export default function ProjectDetailView({ directory, onClose }: { directory: string; onClose: () => void }) {
  const [status, setStatus] = useState<any>(null);
  const [deploys, setDeploys] = useState<any[]>([]);
  const [backups, setBackups] = useState<any[]>([]);
  const [domains, setDomains] = useState<any[]>([]);
  const [services, setServices] = useState<any[]>([]);
  const [tables, setTables] = useState<any[]>([]);
  const [actionMsg, setActionMsg] = useState<{ type: "ok" | "error"; text: string } | null>(null);

  function showMsg(type: "ok" | "error", text: string) {
    setActionMsg({ type, text });
    setTimeout(() => setActionMsg(null), 5000);
  }

  function cleanErr(e: unknown, fallback: string): string {
    const raw = typeof e === "string" ? e : (e as any)?.message;
    return typeof raw === "string" && raw.trim() && raw.length <= 160 ? raw.trim() : fallback;
  }

  useEffect(() => {
    (async () => {
      try {
        const [st, d, b, dom, t] = await Promise.all([
          agentClient.backendStatus(directory),
          agentClient.deployList(directory),
          agentClient.backupList(directory),
          agentClient.domainList(),
          agentClient.backendTables(directory).catch(() => ({ tables: [] })),
        ]);
        setStatus(st);
        setDeploys(d.deploys || []);
        setBackups(b.backups || []);
        setDomains(dom.domains || []);
        setTables(t.tables || []);
      } catch {}
      try {
        const r = await fetch(`${(agentClient as any).baseUrl}/console/containers?all=1`, { headers: (agentClient as any).authHeaders });
        const j = await r.json();
        // Filter to this project's containers via docker-compose project label.
        const slug = directory.split("/").pop();
        setServices((j.containers || []).filter((c: any) => c.project === slug || c.project === "yaver-services"));
      } catch {}
    })();
  }, [directory]);

  const slug = directory.split("/").pop() || directory;

  async function deploy() {
    try {
      const p = await agentClient.deployPreview(directory);
      if (!confirm(`Deploy ${slug}?\n\nBranch: ${p.branch || "?"}\n${p.dirty ? "⚠️ " + p.dirtyFiles?.length + " uncommitted\n" : ""}Active env: ${p.activeEnv}\n${p.warnings?.join("\n") || ""}`)) return;
      const r = await agentClient.deployRun(directory);
      if (r.error) showMsg("error", cleanErr(r.error, "Deploy failed. Check the agent logs and try again."));
      else showMsg("ok", r.status || "Deploy started.");
    } catch (e) {
      showMsg("error", cleanErr(e, "Couldn't deploy — the agent may be unreachable."));
    }
  }
  async function snapshot() {
    try {
      const r = await agentClient.backupCreate(directory);
      if (r.error) showMsg("error", cleanErr(r.error, "Snapshot failed. Please try again."));
      else showMsg("ok", "Snapshot created.");
    } catch (e) {
      showMsg("error", cleanErr(e, "Couldn't create snapshot — the agent may be unreachable."));
    }
  }

  return (
    <div className="space-y-5">
      <div className="flex items-center gap-3">
        <button onClick={onClose} className="text-sm text-indigo-400 hover:text-indigo-300">← Projects</button>
        <h2 className="text-xl font-semibold text-surface-100 flex-1 truncate font-mono">{slug}</h2>
      </div>
      <div className="text-xs text-surface-500 font-mono truncate">{directory}</div>

      <EnvironmentSwitcher directory={directory} />

      {/* Top-line status */}
      <div className="grid grid-cols-2 md:grid-cols-4 gap-2">
        <MiniCard label="Backend" value={status?.kind || "—"} sub={status?.running ? "running" : (status?.error || "offline")} tone={status?.running ? "ok" : "warn"} />
        <MiniCard label="Services" value={`${services.filter(s => s.state === "running").length}`} sub={`${services.length} total`} />
        <MiniCard label="Tables" value={`${tables.length}`} sub="in backend" />
        <MiniCard label="Domains" value={`${domains.length}`} sub="via Caddy" />
      </div>

      {/* Action row */}
      <div className="flex gap-2 flex-wrap">
        <button onClick={deploy} className="px-3 py-2 text-sm rounded-lg bg-indigo-500 text-white hover:bg-indigo-400">🚀 Deploy</button>
        <button onClick={snapshot} className="px-3 py-2 text-sm rounded-lg bg-surface-800 text-surface-200 hover:bg-surface-700">📸 Snapshot</button>
        <a href={`/dashboard/${encodeURIComponent(directory)}`} className="px-3 py-2 text-sm rounded-lg bg-surface-800 text-surface-200 hover:bg-surface-700">🗄️ Dashboard</a>
      </div>

      {actionMsg && (
        <div className={`text-sm rounded-lg border px-3 py-2 ${actionMsg.type === "ok" ? "border-emerald-500/30 bg-emerald-500/10 text-emerald-300" : "border-red-500/30 bg-red-500/10 text-red-300"}`}>
          {actionMsg.text}
        </div>
      )}

      <Section title="Services">
        {services.length === 0 && <Empty text="No services" />}
        {services.map((s) => (
          <Row key={s.id}>
            <Tag tone={s.state === "running" ? "ok" : "muted"}>{s.state}</Tag>
            <span className="font-mono">{s.name}</span>
            <span className="text-surface-500 text-xs flex-1 truncate">{s.image}</span>
          </Row>
        ))}
      </Section>

      <Section title="Recent deploys">
        {deploys.length === 0 && <Empty text="No deployments" />}
        {deploys.slice(0, 5).map((d) => (
          <Row key={d.id}>
            <Tag tone={d.status === "success" ? "ok" : "fail"}>{d.status}</Tag>
            <span className="font-mono text-xs">{(d.commit || "").slice(0, 8)}</span>
            <span className="flex-1 truncate text-xs">{d.message || "(no message)"}</span>
            <span className="text-surface-500 text-xs">{d.duration}</span>
          </Row>
        ))}
      </Section>

      <Section title="Backups">
        {backups.length === 0 && <Empty text="No backups" />}
        {backups.slice(0, 5).map((b) => (
          <Row key={b.id}>
            <span className="font-mono text-xs text-surface-400">{b.id}</span>
            <span className="text-xs text-surface-500">{b.backend}</span>
            <span className="text-xs flex-1 truncate font-mono text-surface-500">{b.path}</span>
            <button onClick={async () => {
              if (!confirm("Restore this backup? This overwrites current backend data.")) return;
              try {
                const r = await agentClient.backupRestore(b.id, directory);
                if ((r as any)?.error) showMsg("error", cleanErr((r as any).error, "Restore failed. Please try again."));
                else showMsg("ok", "Backup restored.");
              } catch (e) {
                showMsg("error", cleanErr(e, "Couldn't restore — the agent may be unreachable."));
              }
            }} className="text-xs text-emerald-400 hover:text-emerald-300">Restore</button>
          </Row>
        ))}
      </Section>

      <Section title="Domains">
        {domains.length === 0 && <Empty text="No domain attached. Add one in Ops → Domains." />}
        {domains.map((d) => (
          <Row key={d.id}>
            <span className="font-mono">{d.domain}</span>
            <span className="text-surface-500">→</span>
            <span className="font-mono text-xs text-surface-400 flex-1 truncate">{d.upstream}</span>
          </Row>
        ))}
      </Section>

      <Section title="Data overview">
        {tables.length === 0 && <Empty text="No tables yet (or helper not installed)." />}
        <div className="grid grid-cols-2 md:grid-cols-4 gap-2">
          {tables.slice(0, 12).map((t) => (
            <div key={t.name} className="bg-surface-900/50 border border-surface-800 rounded-lg p-2 text-xs">
              <div className="font-mono truncate">{t.name}</div>
              {t.rowCount != null && <div className="text-surface-500">{t.rowCount} rows</div>}
            </div>
          ))}
        </div>
      </Section>
    </div>
  );
}

function MiniCard({ label, value, sub, tone }: { label: string; value: string; sub: string; tone?: "ok" | "warn" }) {
  const border = tone === "ok" ? "border-emerald-500/30" : tone === "warn" ? "border-amber-500/40" : "border-surface-800";
  return (
    <div className={`bg-surface-900/50 border rounded-lg p-2 ${border}`}>
      <div className="text-[10px] uppercase text-surface-500 font-semibold">{label}</div>
      <div className="text-lg font-semibold text-surface-100 truncate">{value}</div>
      <div className="text-[10px] text-surface-500">{sub}</div>
    </div>
  );
}

function Section({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <div>
      <h3 className="text-xs uppercase text-surface-500 font-semibold mb-2">{title}</h3>
      <div className="space-y-1">{children}</div>
    </div>
  );
}
function Row({ children }: { children: React.ReactNode }) {
  return <div className="flex items-center gap-2 bg-surface-900/50 border border-surface-800 rounded-lg p-2 text-sm">{children}</div>;
}
function Tag({ tone, children }: { tone: "ok" | "fail" | "muted"; children: React.ReactNode }) {
  const cls = tone === "ok" ? "bg-emerald-500/20 text-emerald-300"
    : tone === "fail" ? "bg-red-500/20 text-red-300"
    : "bg-surface-800 text-surface-400";
  return <span className={`px-1.5 py-0.5 rounded text-[10px] uppercase font-semibold ${cls}`}>{children}</span>;
}
function Empty({ text = "—" }: { text?: string }) {
  return <div className="text-xs text-surface-500">{text}</div>;
}
