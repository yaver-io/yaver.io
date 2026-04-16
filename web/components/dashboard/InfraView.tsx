"use client";

import { useEffect, useState, type ReactNode } from "react";
import { agentClient, type InfraSummary } from "@/lib/agent-client";

function fmtBytes(n?: number) {
  if (!n) return "0 B";
  const units = ["B", "KB", "MB", "GB", "TB"];
  let value = n;
  let i = 0;
  while (value >= 1024 && i < units.length - 1) {
    value /= 1024;
    i++;
  }
  return `${value.toFixed(value >= 10 || i === 0 ? 0 : 1)} ${units[i]}`;
}

function fmtUptime(seconds?: number) {
  if (!seconds) return "0m";
  const days = Math.floor(seconds / 86400);
  const hours = Math.floor((seconds % 86400) / 3600);
  const mins = Math.floor((seconds % 3600) / 60);
  if (days > 0) return `${days}d ${hours}h`;
  if (hours > 0) return `${hours}h ${mins}m`;
  return `${mins}m`;
}

export default function InfraView() {
  const [summary, setSummary] = useState<InfraSummary | null>(null);
  const [loading, setLoading] = useState(true);
  const [busyService, setBusyService] = useState<string | null>(null);
  const [powerBusy, setPowerBusy] = useState<string | null>(null);
  const [sandboxBusy, setSandboxBusy] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);

  async function refresh() {
    setError(null);
    try {
      setSummary(await agentClient.infraSummary());
    } catch (e: any) {
      setError(e?.message || "Failed to load infra summary");
    } finally {
      setLoading(false);
    }
  }

  useEffect(() => {
    refresh();
    const iv = setInterval(refresh, 15000);
    return () => clearInterval(iv);
  }, []);

  async function serviceAction(name: string, action: "start" | "stop" | "restart") {
    setBusyService(`${name}:${action}`);
    try {
      await agentClient.infraServiceAction("dev", name, action);
      await refresh();
    } finally {
      setBusyService(null);
    }
  }

  async function powerAction(action: "agent_shutdown" | "host_reboot") {
    const ok = window.confirm(action === "host_reboot" ? "Reboot this machine?" : "Shut down the Yaver agent?");
    if (!ok) return;
    setPowerBusy(action);
    try {
      await agentClient.infraPower(action);
      if (action !== "agent_shutdown") await refresh();
    } finally {
      setPowerBusy(null);
    }
  }

  async function sandboxQuickstart(mode: "guests" | "host") {
    setSandboxBusy(mode);
    try {
      const res = await agentClient.sandboxQuickstart(mode, true);
      if (!res.ok) {
        alert(res.error || "Could not enable containerization");
        return;
      }
      if (res.message) alert(res.message);
      await refresh();
    } finally {
      setSandboxBusy(null);
    }
  }

  async function buildSandbox() {
    setSandboxBusy("build");
    try {
      await agentClient.buildSandboxImage();
      alert("Sandbox image build started.");
      await refresh();
    } finally {
      setSandboxBusy(null);
    }
  }

  if (loading) {
    return <div className="flex min-h-[30vh] items-center justify-center"><div className="h-6 w-6 animate-spin rounded-full border-2 border-surface-700 border-t-emerald-400" /></div>;
  }

  if (!summary) {
    return <div className="rounded-2xl border border-red-500/20 bg-red-500/5 p-4 text-sm text-red-300">{error || "Infra unavailable"}</div>;
  }

  return (
    <div className="space-y-6">
      <div className="flex flex-col gap-3 rounded-3xl border border-surface-800 bg-surface-900/70 p-5 md:flex-row md:items-end md:justify-between">
        <div>
          <div className="mb-2 flex items-center gap-2">
            <span className={`h-2 w-2 rounded-full ${summary.machine.isOnline ? "bg-emerald-400" : "bg-red-400"}`} />
            <span className="text-xs font-semibold uppercase tracking-[0.18em] text-surface-500">Managed Infra</span>
          </div>
          <h2 className="text-2xl font-semibold text-surface-100">{summary.machine.name}</h2>
          <p className="mt-1 text-sm text-surface-400">{summary.machine.platform} {summary.machine.arch ? `· ${summary.machine.arch}` : ""}</p>
        </div>
        <div className="flex flex-wrap gap-2">
          <button onClick={() => powerAction("agent_shutdown")} disabled={powerBusy !== null} className="rounded-xl border border-amber-500/30 bg-amber-500/10 px-3 py-2 text-sm text-amber-300 hover:bg-amber-500/20 disabled:opacity-50">Stop agent</button>
          <button onClick={() => powerAction("host_reboot")} disabled={powerBusy !== null || !summary.capabilities.hostReboot} className="rounded-xl border border-red-500/30 bg-red-500/10 px-3 py-2 text-sm text-red-300 hover:bg-red-500/20 disabled:opacity-50">Reboot host</button>
        </div>
      </div>

      <div className="grid gap-3 md:grid-cols-4">
        <Metric label="CPU" value={`${(summary.metrics?.cpuPct || 0).toFixed(1)}%`} sub={`${summary.metrics?.cores || 0} cores`} />
        <Metric label="Memory" value={`${(summary.metrics?.ramPct || 0).toFixed(0)}%`} sub={`${fmtBytes(summary.metrics?.ramUsed)} / ${fmtBytes(summary.metrics?.ramTotal)}`} />
        <Metric label="Disk" value={`${(summary.metrics?.diskPct || 0).toFixed(0)}%`} sub={`${fmtBytes(summary.metrics?.diskUsed)} / ${fmtBytes(summary.metrics?.diskTotal)}`} />
        <Metric label="Uptime" value={fmtUptime(summary.metrics?.uptime)} sub={summary.metrics?.hostname || summary.machine.deviceId} />
      </div>

      <Section title="Services" subtitle="Dev services from .yaver/services.yaml">
        <div className="space-y-3">
          {(summary.devServices || []).length === 0 && <p className="text-sm text-surface-500">No managed dev services configured on this machine.</p>}
          {(summary.devServices || []).map((svc) => (
            <div key={svc.name} className="flex flex-col gap-3 rounded-2xl border border-surface-800 bg-surface-950/60 p-4 md:flex-row md:items-center">
              <div className="min-w-0 flex-1">
                <div className="flex items-center gap-2">
                  <span className={`h-2 w-2 rounded-full ${svc.running ? "bg-emerald-400" : "bg-surface-600"}`} />
                  <span className="font-medium text-surface-100">{svc.name}</span>
                  <span className="rounded-full border border-surface-700 px-2 py-0.5 text-[11px] text-surface-400">{svc.health}</span>
                </div>
                <p className="mt-1 text-xs text-surface-500">{svc.image || "binary service"} {svc.port ? `· port ${svc.port}` : ""} {svc.memory ? `· ${svc.memory}` : ""}</p>
              </div>
              <div className="flex gap-2">
                <button onClick={() => serviceAction(svc.name, svc.running ? "restart" : "start")} disabled={busyService !== null} className="rounded-lg border border-indigo-500/30 bg-indigo-500/10 px-3 py-2 text-xs font-medium text-indigo-300 disabled:opacity-50">{svc.running ? "Restart" : "Start"}</button>
                <button onClick={() => serviceAction(svc.name, "stop")} disabled={busyService !== null || !svc.running} className="rounded-lg border border-surface-700 bg-surface-900 px-3 py-2 text-xs font-medium text-surface-300 disabled:opacity-50">Stop</button>
              </div>
            </div>
          ))}
        </div>
      </Section>

      <div className="grid gap-6 lg:grid-cols-2">
        <Section title="Containerization" subtitle="Whether remote Yaver tasks run on the host or in Docker">
          <div className="grid gap-3 md:grid-cols-2">
            <Metric
              label="Mode"
              value={
                summary.sandbox.enabledMode === "host"
                  ? "All tasks"
                  : summary.sandbox.enabledMode === "guests"
                    ? "Guests only"
                    : "Direct host"
              }
              sub={
                summary.sandbox.enabledMode === "host"
                  ? "all agent tasks isolated"
                  : summary.sandbox.enabledMode === "guests"
                    ? "shared infra isolated"
                    : "tasks run on host"
              }
            />
            <Metric
              label="Image"
              value={summary.sandbox.imageReady ? "Ready" : "Not built"}
              sub={summary.sandbox.imageName || "yaver-sandbox"}
            />
          </div>
          <div className="mt-3 rounded-2xl border border-surface-800 bg-surface-950/60 p-4 text-sm text-surface-400">
            Docker: <span className={summary.sandbox.docker ? "text-emerald-300" : "text-red-300"}>{summary.sandbox.docker ? "available" : "not found"}</span>
            {summary.sandbox.recommendedReason ? ` · ${summary.sandbox.recommendedReason}` : ""}
          </div>
          <div className="mt-3 flex flex-wrap gap-2">
            <button onClick={() => sandboxQuickstart("guests")} disabled={sandboxBusy !== null || !summary.sandbox.docker} className="rounded-xl border border-emerald-500/30 bg-emerald-500/10 px-3 py-2 text-sm text-emerald-300 disabled:opacity-50">Enable guest isolation</button>
            <button onClick={() => sandboxQuickstart("host")} disabled={sandboxBusy !== null || !summary.sandbox.docker} className="rounded-xl border border-indigo-500/30 bg-indigo-500/10 px-3 py-2 text-sm text-indigo-300 disabled:opacity-50">Containerize all tasks</button>
            {!summary.sandbox.imageReady && summary.sandbox.docker && (
              <button onClick={buildSandbox} disabled={sandboxBusy !== null} className="rounded-xl border border-surface-700 bg-surface-900 px-3 py-2 text-sm text-surface-300 disabled:opacity-50">Build image</button>
            )}
          </div>
        </Section>

        <Section title="Relay" subtitle="Configured and cached relay endpoints">
          <div className="space-y-3">
            {(summary.relays || []).length === 0 && <p className="text-sm text-surface-500">No relay endpoints configured.</p>}
            {(summary.relays || []).map((relay) => (
              <div key={`${relay.source}:${relay.id}`} className="rounded-2xl border border-surface-800 bg-surface-950/60 p-4">
                <div className="flex items-center justify-between gap-3">
                  <div className="min-w-0">
                    <div className="font-medium text-surface-100">{relay.label || relay.id}</div>
                    <div className="truncate text-xs text-surface-500">{relay.httpUrl || relay.quicAddr}</div>
                  </div>
                  <span className="rounded-full border border-surface-700 px-2 py-0.5 text-[11px] text-surface-400">{relay.source}</span>
                </div>
              </div>
            ))}
          </div>
        </Section>

        <Section title="Sharing" subtitle="Host-side guest access posture">
          <div className="grid gap-3 md:grid-cols-2">
            <Metric label="Accepted guests" value={`${summary.sharing.acceptedGuests}`} sub="active shared access" />
            <Metric label="Pending invites" value={`${summary.sharing.pendingGuests}`} sub="awaiting acceptance" />
          </div>
          <div className="mt-3 rounded-2xl border border-surface-800 bg-surface-950/60 p-4 text-sm text-surface-400">
            Mobile guest controls live under the existing Guest Access screen. MCP now sees this same infra posture through `infra_summary`.
          </div>
        </Section>
      </div>

      <Section title="Network" subtitle="Local interfaces visible to the agent">
        <div className="space-y-3">
          {(summary.network || []).slice(0, 8).map((iface) => (
            <div key={iface.name} className="rounded-2xl border border-surface-800 bg-surface-950/60 p-4">
              <div className="flex items-center justify-between gap-3">
                <div className="font-medium text-surface-100">{iface.name}</div>
                <div className="text-xs text-surface-500">{iface.flags}</div>
              </div>
              <div className="mt-2 text-xs text-surface-500">{(iface.addresses || []).join(" · ") || "no addresses"}</div>
            </div>
          ))}
        </div>
      </Section>
    </div>
  );
}

function Section({ title, subtitle, children }: { title: string; subtitle: string; children: ReactNode }) {
  return (
    <section className="rounded-3xl border border-surface-800 bg-surface-900/50 p-5">
      <div className="mb-4">
        <h3 className="text-lg font-semibold text-surface-100">{title}</h3>
        <p className="text-sm text-surface-500">{subtitle}</p>
      </div>
      {children}
    </section>
  );
}

function Metric({ label, value, sub }: { label: string; value: string; sub: string }) {
  return (
    <div className="rounded-2xl border border-surface-800 bg-surface-900/70 p-4">
      <div className="text-xs font-semibold uppercase tracking-[0.16em] text-surface-500">{label}</div>
      <div className="mt-2 text-2xl font-semibold text-surface-100">{value}</div>
      <div className="mt-1 text-xs text-surface-500">{sub}</div>
    </div>
  );
}
