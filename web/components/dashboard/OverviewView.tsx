"use client";

import { useEffect, useState } from "react";
import { agentClient } from "@/lib/agent-client";

type Summary = {
  machines: { total: number; online: number; offline: number };
  projects: { total: number; deployed: number; local: number };
  services: { running: number; stopped: number };
  alerts: { active: number; summary: string };
  cost: { monthlyUsd: number; breakdown: { provider: string; monthly: number }[] };
  uptime: { up: number; down: number; pct: number };
  recentActivity: { timestamp: string; icon: string; title: string; detail?: string }[];
};

export default function OverviewView({
  user,
  onNavigate,
}: {
  user?: { name?: string; email?: string };
  // Switch the dashboard's active tab. Wired by the parent so the Quick
  // Action buttons actually move the user where they point instead of
  // popping an alert telling them to navigate by hand.
  onNavigate?: (tab: string) => void;
}) {
  const [s, setS] = useState<Summary | null>(null);
  const [error, setError] = useState("");
  const [pipelineMsg, setPipelineMsg] = useState<string | null>(null);
  const [notConnected, setNotConnected] = useState(false);

  useEffect(() => {
    refresh();
    const i = setInterval(refresh, 15000);
    return () => clearInterval(i);
  }, []);

  async function refresh() {
    try {
      setS(await agentClient.overviewSummary());
      setError("");
      setNotConnected(false);
    } catch (e: any) {
      const msg = String(e?.message || "");
      // assertConnected() throws this when no agent is picked yet. Showing
      // it as a blood-red error on Home is wrong — Home should be the
      // landing page even when you haven't connected to a device.
      if (msg.includes("not connected")) {
        setNotConnected(true);
        setError("");
      } else {
        setError(msg);
        setNotConnected(false);
      }
    }
  }

  const greeting = new Date().getHours() < 12 ? "Good morning" : new Date().getHours() < 18 ? "Good afternoon" : "Good evening";
  const name = user?.name || user?.email?.split("@")[0] || "there";

  if (notConnected) {
    return (
      <div className="space-y-6">
        <div>
          <h1 className="text-2xl font-semibold text-surface-100">{greeting}, {name}</h1>
          <p className="text-sm text-surface-500">Pick a device on the left to open your workspace.</p>
        </div>
        <div className="rounded-lg border border-surface-800 bg-surface-900/40 p-6">
          <h2 className="text-sm font-semibold text-surface-200 mb-1">Not connected to an agent yet</h2>
          <p className="text-xs text-surface-500">
            Your devices are listed in the sidebar. Click one to open the workspace —
            tasks, hot reload, git, vibing, and deploy surfaces all live inside an
            active agent connection.
          </p>
          <p className="mt-3 text-xs text-surface-600">
            If nothing lights up: install Yaver on a machine and run{" "}
            <code className="rounded bg-surface-800 px-1 py-0.5 text-surface-300">yaver auth &amp;&amp; yaver serve</code>.
          </p>
        </div>
      </div>
    );
  }

  if (error) return (
    <div className="p-4 text-sm text-red-700 dark:text-red-300 bg-red-900/20 border border-red-500/30 rounded">
      <div>Couldn&apos;t load your overview. Try again in a moment.</div>
      <div className="mt-1 text-xs text-red-400/70 break-words">{error}</div>
    </div>
  );
  if (!s) return <div className="p-4 text-sm text-surface-500">Loading…</div>;

  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-semibold text-surface-100">{greeting}, {name}</h1>
        <p className="text-sm text-surface-500">Your machines at a glance</p>
      </div>

      <div className="grid grid-cols-2 md:grid-cols-3 lg:grid-cols-6 gap-3">
        <Card label="Machines" value={s.machines.total} sub={`${s.machines.online} online · ${s.machines.offline} off`} tone={s.machines.online > 0 ? "ok" : "warn"} />
        <Card label="Projects" value={s.projects.total} sub={`${s.projects.deployed} deployed · ${s.projects.local} local`} tone="info" />
        <Card label="Services" value={s.services.running} sub={`${s.services.stopped} stopped`} tone="ok" />
        <Card label="Alerts" value={s.alerts.active} sub={s.alerts.summary} tone={s.alerts.active > 0 ? "warn" : "ok"} />
        <Card label="Cost" value={`$${s.cost.monthlyUsd.toFixed(2)}`} sub="per month" tone="info" />
        <Card label="Uptime" value={`${s.uptime.pct.toFixed(1)}%`} sub={`${s.uptime.up} monitors`} tone={s.uptime.down === 0 ? "ok" : "warn"} />
      </div>

      <section>
        <h2 className="text-xs uppercase text-surface-500 font-semibold mb-2">Quick Actions</h2>
        <div className="flex flex-wrap gap-2">
          {onNavigate ? (
            <>
              <QuickBtn label="🚀 Deploy" onClick={() => onNavigate("ops")} />
              <QuickBtn label="+ New Project" onClick={() => onNavigate("projects")} />
              <QuickBtn label="📟 Terminal" onClick={() => onNavigate("exec")} />
            </>
          ) : null}
          <QuickBtn label="📊 Run Pipeline" onClick={async () => {
            setPipelineMsg(null);
            try {
              const res = await fetch(`${(agentClient as any).baseUrl}/ci/run`, { method: "POST", headers: (agentClient as any).authHeaders });
              if (!res.ok) {
                setPipelineMsg("Couldn't start the pipeline. Connect to a machine with a CI config and try again.");
                return;
              }
              const j = await res.json().catch(() => ({} as any));
              setPipelineMsg(j.error || `CI ${j.status ?? "started"} (${j.steps?.length || 0} steps)`);
            } catch {
              setPipelineMsg("Couldn't reach the agent to start the pipeline.");
            }
          }} />
        </div>
        {pipelineMsg ? <div className="mt-2 text-xs text-surface-400">{pipelineMsg}</div> : null}
      </section>

      <section>
        <h2 className="text-xs uppercase text-surface-500 font-semibold mb-2">Recent Activity</h2>
        <div className="space-y-1">
          {s.recentActivity.length === 0 && <div className="text-xs text-surface-500">No activity yet.</div>}
          {s.recentActivity.map((a, i) => (
            <div key={i} className="flex items-center gap-3 bg-surface-900/50 border border-surface-800 rounded-lg p-2 text-sm">
              <span>{a.icon}</span>
              <span className="flex-1 truncate">{a.title}</span>
              <span className="text-xs text-surface-500">{new Date(a.timestamp).toLocaleTimeString()}</span>
            </div>
          ))}
        </div>
      </section>

      {s.cost.breakdown.length > 0 && (
        <section>
          <h2 className="text-xs uppercase text-surface-500 font-semibold mb-2">Cost Breakdown</h2>
          <div className="space-y-1">
            {s.cost.breakdown.map((c, i) => (
              <div key={i} className="flex items-center gap-3 bg-surface-900/50 border border-surface-800 rounded-lg p-2 text-sm">
                <span className="flex-1">{c.provider}</span>
                <span className="font-mono">${c.monthly.toFixed(2)}/mo</span>
              </div>
            ))}
          </div>
        </section>
      )}
    </div>
  );
}

function Card({ label, value, sub, tone }: { label: string; value: number | string; sub: string; tone: "ok" | "warn" | "info" }) {
  const border = tone === "ok" ? "border-emerald-500/30" : tone === "warn" ? "border-amber-500/40" : "border-surface-800";
  return (
    <div className={`bg-surface-900/50 border rounded-lg p-3 ${border}`}>
      <div className="text-xs uppercase text-surface-500 font-semibold">{label}</div>
      <div className="text-2xl font-bold text-surface-100 mt-1">{value}</div>
      <div className="text-xs text-surface-500">{sub}</div>
    </div>
  );
}

function QuickBtn({ label, onClick }: { label: string; onClick: () => void }) {
  return (
    <button onClick={onClick}
      className="px-3 py-2 text-sm rounded-lg bg-indigo-500/20 text-indigo-700 dark:text-indigo-300 hover:bg-indigo-500/30 border border-indigo-500/30">
      {label}
    </button>
  );
}
