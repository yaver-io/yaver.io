"use client";

// CIRunnerView — configure a Yaver box as a GitHub/GitLab self-hosted CI runner
// from the web dashboard, over the relay. Register a repo → its existing
// workflows (runs-on: [self-hosted, yaver]) run on the box for $0 GitHub
// minutes. Shows the savings ledger ("GitHub would have billed $X, you saved
// $Y") and scaffolds deploy workflows (npm / TestFlight / Play internal). Thin
// trigger over the ci_runner_* / ci_workflow_* ops verbs — the agent owns all
// the logic. See docs/yaver-managed-cloud-ci-absorption.md.
import { useCallback, useEffect, useRef, useState } from "react";
import { AgentClient, agentClient } from "@/lib/agent-client";
import type { Device } from "@/lib/use-devices";
import { HIDE_PAID_UI } from "@/lib/launchFlags";

type Registration = {
  key: string;
  provider: string;
  target: string;
  scope: string;
  labels: string[];
  isolation: string;
  where: string;
  maxConcurrent: number;
  live?: boolean;
};
type Savings = { runs: number; chargedCents: number; wouldHaveCostUpstreamCents: number; savedCents: number };
type WorkflowTarget = { target: string; file: string; runsOn: string; secrets: string[]; description: string };

const dollars = (cents: number) => `$${((cents || 0) / 100).toFixed(2)}`;

export default function CIRunnerView({ devices, token }: { devices: Device[]; token: string | null }) {
  const [deviceId, setDeviceId] = useState("");
  const [regs, setRegs] = useState<Registration[]>([]);
  const [savings, setSavings] = useState<Savings | null>(null);
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState<string | null>(null);

  // register form
  const [provider, setProvider] = useState<"github" | "gitlab">("github");
  const [target, setTarget] = useState("");
  const [scope, setScope] = useState<"repo" | "org">("repo");
  const [isolation, setIsolation] = useState<"container" | "host">("container");
  const [where, setWhere] = useState<"self-hosted" | "operator-fleet" | "yaver-cloud">("self-hosted");

  // workflow scaffold
  const [wfTargets, setWfTargets] = useState<WorkflowTarget[]>([]);
  const [wfTarget, setWfTarget] = useState("test");
  const [wfPreview, setWfPreview] = useState<{ path: string; content: string; secrets: string[] } | null>(null);

  const clientRef = useRef<AgentClient | null>(null);
  const connectedTo = useRef("");

  const ensureClient = useCallback(
    async (id: string): Promise<AgentClient | null> => {
      const device = devices.find((d) => d.id === id);
      if (!device || !token) return null;
      if (clientRef.current && connectedTo.current === id) return clientRef.current;
      try {
        clientRef.current?.disconnect();
      } catch {}
      clientRef.current = null;
      connectedTo.current = "";
      const client = new AgentClient();
      client.setRelayServers(agentClient.configuredRelayServers.map((r) => ({ ...r })));
      const tunnelUrls = Array.from(
        new Set([...(Array.isArray(device.publicEndpoints) ? device.publicEndpoints : []), ...(device.tunnelUrl ? [device.tunnelUrl] : [])]),
      );
      await client.connect(device.host, device.port, token, device.id, { tunnelUrls });
      clientRef.current = client;
      connectedTo.current = id;
      return client;
    },
    [devices, token],
  );

  const callCI = useCallback(
    async (verb: string, payload: Record<string, unknown> = {}): Promise<any> => {
      try {
        const client = await ensureClient(deviceId);
        if (!client) return { ok: false, error: "not connected" };
        const res = await client.callOps(verb, payload);
        if (res?.ok === false) return { ok: false, code: res.code, error: res.error };
        return (res as any)?.initial ?? res;
      } catch (e: any) {
        setMsg(e?.message || "connection failed");
        return { ok: false, error: e?.message || "failed" };
      }
    },
    [deviceId, ensureClient],
  );

  const refresh = useCallback(async () => {
    if (!deviceId) return;
    const s = await callCI("ci_runner_status");
    if (Array.isArray(s?.registrations)) setRegs(s.registrations);
    if (s?.savings) setSavings(s.savings);
  }, [deviceId, callCI]);

  useEffect(() => {
    if (!deviceId) return;
    (async () => {
      await refresh();
      const t = await callCI("ci_workflow_targets");
      if (Array.isArray(t?.targets)) setWfTargets(t.targets);
    })();
  }, [deviceId]); // eslint-disable-line

  const register = async () => {
    if (!target.trim()) {
      setMsg("enter owner/repo (or org / project id)");
      return;
    }
    setBusy(true);
    setMsg(null);
    const r = await callCI("ci_runner_register", { provider, target: target.trim(), scope, isolation, where });
    setBusy(false);
    if (r?.ok === false) {
      setMsg(r.error || "register failed");
      return;
    }
    setMsg(`registered — set workflow to runs-on: ${JSON.stringify(r?.runsOn || ["self-hosted", "yaver"])}`);
    setTarget("");
    refresh();
  };

  const remove = async (key: string) => {
    setBusy(true);
    await callCI("ci_runner_remove", { key });
    setBusy(false);
    refresh();
  };

  const previewWorkflow = async (write: boolean) => {
    setBusy(true);
    setMsg(null);
    const r = await callCI("ci_workflow_scaffold", { target: wfTarget, write, workDir: "." });
    setBusy(false);
    if (r?.ok === false) {
      setMsg(r.error || "scaffold failed");
      if (r) setWfPreview({ path: r.path, content: r.content, secrets: r.secrets || [] });
      return;
    }
    setWfPreview({ path: r.path, content: r.content, secrets: r.secrets || [] });
    if (write) setMsg(`wrote ${r.path} — commit it, set the secrets, push the tag.`);
  };

  return (
    <div className="space-y-6">
      <header>
        <h1 className="text-xl font-semibold">Self-hosted CI</h1>
        <p className="text-sm text-neutral-400">
          Run your GitHub/GitLab workflows on your own box for $0 minutes. Register a repo, set{" "}
          <code className="text-neutral-300">runs-on: [self-hosted, yaver]</code>, and the build runs here.
        </p>
      </header>

      {/* device picker */}
      <label className="block">
        <span className="text-sm text-neutral-400">Runner box</span>
        <select
          className="mt-1 w-full rounded-md border border-neutral-700 bg-neutral-900 p-2 text-sm"
          value={deviceId}
          onChange={(e) => setDeviceId(e.target.value)}
        >
          <option value="">Select a device…</option>
          {devices.map((d) => (
            <option key={d.id} value={d.id}>
              {d.name || d.id} {d.online ? "● online" : "○ offline"}
            </option>
          ))}
        </select>
      </label>

      {deviceId && (
        <>
          {/* savings ledger */}
          {savings && savings.runs > 0 && (
            <div className="rounded-lg border border-emerald-800 bg-emerald-950/40 p-4">
              <div className="text-sm text-emerald-300">
                {savings.runs} CI run{savings.runs === 1 ? "" : "s"} on this box · GitHub would have billed{" "}
                <b>{dollars(savings.wouldHaveCostUpstreamCents)}</b> · you paid <b>{dollars(savings.chargedCents)}</b> ·{" "}
                <span className="text-emerald-200">saved {dollars(savings.savedCents)}</span>
              </div>
            </div>
          )}

          {/* register */}
          <section className="rounded-lg border border-neutral-800 p-4 space-y-3">
            <h2 className="text-sm font-medium">Register a repo</h2>
            <div className="grid grid-cols-2 gap-3 sm:grid-cols-4">
              <select className="rounded-md border border-neutral-700 bg-neutral-900 p-2 text-sm" value={provider} onChange={(e) => setProvider(e.target.value as any)}>
                <option value="github">GitHub</option>
                <option value="gitlab">GitLab</option>
              </select>
              <input
                className="col-span-2 rounded-md border border-neutral-700 bg-neutral-900 p-2 text-sm sm:col-span-1"
                placeholder="owner/repo"
                value={target}
                onChange={(e) => setTarget(e.target.value)}
              />
              <select className="rounded-md border border-neutral-700 bg-neutral-900 p-2 text-sm" value={scope} onChange={(e) => setScope(e.target.value as any)}>
                <option value="repo">repo</option>
                <option value="org">org</option>
              </select>
              <select className="rounded-md border border-neutral-700 bg-neutral-900 p-2 text-sm" value={isolation} onChange={(e) => setIsolation(e.target.value as any)}>
                <option value="container">container (safe)</option>
                <option value="host">host (trusted)</option>
              </select>
            </div>
            <div className="flex items-center gap-3">
              <select className="rounded-md border border-neutral-700 bg-neutral-900 p-2 text-sm" value={where} onChange={(e) => setWhere(e.target.value as any)}>
                <option value="self-hosted">your box (free)</option>
                <option value="operator-fleet">operator fleet (free)</option>
                {/* HN-LAUNCH-HIDE-PAID: hide metered Yaver Cloud runner option at launch */}
                {!HIDE_PAID_UI && <option value="yaver-cloud">Yaver Cloud (metered)</option>}
              </select>
              <button disabled={busy} onClick={register} className="rounded-md bg-emerald-700 px-4 py-2 text-sm font-medium disabled:opacity-50">
                Register runner
              </button>
            </div>
            <p className="text-xs text-neutral-500">Private repos only by default. The forge token is minted per-job from this box&apos;s git creds (run “Connect git” first if missing).</p>
          </section>

          {/* registrations */}
          <section className="space-y-2">
            <h2 className="text-sm font-medium">Registered runners</h2>
            {regs.length === 0 && <p className="text-sm text-neutral-500">None yet.</p>}
            {regs.map((r) => (
              <div key={r.key} className="flex items-center justify-between rounded-md border border-neutral-800 p-3 text-sm">
                <div>
                  <span className="font-mono">{r.key}</span>{" "}
                  <span className={r.live ? "text-emerald-400" : "text-neutral-500"}>{r.live ? "● live" : "○ idle"}</span>
                  <div className="text-xs text-neutral-500">
                    {r.isolation} · {r.where} · runs-on {JSON.stringify(r.labels?.slice(0, 4))}
                  </div>
                </div>
                <button disabled={busy} onClick={() => remove(r.key)} className="rounded-md border border-neutral-700 px-3 py-1 text-xs hover:bg-neutral-800 disabled:opacity-50">
                  Remove
                </button>
              </div>
            ))}
          </section>

          {/* workflow scaffold */}
          <section className="rounded-lg border border-neutral-800 p-4 space-y-3">
            <h2 className="text-sm font-medium">Scaffold a deploy workflow</h2>
            <div className="flex flex-wrap items-center gap-3">
              <select className="rounded-md border border-neutral-700 bg-neutral-900 p-2 text-sm" value={wfTarget} onChange={(e) => setWfTarget(e.target.value)}>
                {(wfTargets.length ? wfTargets.map((t) => t.target) : ["test", "npm", "testflight", "play-internal"]).map((t) => (
                  <option key={t} value={t}>
                    {t}
                  </option>
                ))}
              </select>
              <button disabled={busy} onClick={() => previewWorkflow(false)} className="rounded-md border border-neutral-700 px-3 py-2 text-sm hover:bg-neutral-800 disabled:opacity-50">
                Preview
              </button>
              <button disabled={busy} onClick={() => previewWorkflow(true)} className="rounded-md bg-neutral-700 px-3 py-2 text-sm disabled:opacity-50">
                Write to repo
              </button>
            </div>
            {wfTargets.find((t) => t.target === wfTarget)?.description && (
              <p className="text-xs text-neutral-400">{wfTargets.find((t) => t.target === wfTarget)?.description}</p>
            )}
            {wfPreview && (
              <div className="space-y-2">
                <div className="text-xs text-neutral-400">{wfPreview.path}</div>
                <pre className="max-h-72 overflow-auto rounded-md border border-neutral-800 bg-neutral-950 p-3 text-xs text-neutral-300">{wfPreview.content}</pre>
                {wfPreview.secrets.length > 0 && (
                  <p className="text-xs text-amber-400">Set these GitHub Actions secrets: {wfPreview.secrets.join(", ")}</p>
                )}
              </div>
            )}
          </section>
        </>
      )}

      {msg && <p className="text-sm text-neutral-400">{msg}</p>}
    </div>
  );
}
