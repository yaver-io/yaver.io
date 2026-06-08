"use client";

import { useCallback, useEffect, useState } from "react";
import { agentClient } from "@/lib/agent-client";

// ByoCloudPanel — manage boxes on the user's OWN Hetzner account from the
// web (parity with the mobile CloudProvidersSection): spin up, STOP (save
// cost), START from snapshot, DELETE, and BAKE a fast-boot golden image.
// Everything goes through the connected agent's /ops verbs (vault token
// stays on the agent — never in the browser/Convex). Real mutates need
// YAVER_CLOUD_STOPSTART_LIVE=1 on the agent; otherwise a dry-run plan is
// returned. Renders only when Hetzner is connected on the agent.

type Server = { id?: string; ID?: string; name?: string; Name?: string; status?: string; Status?: string; ip?: string; IP?: string };
type Snap = { id?: string; ID?: string; description?: string; Description?: string; estMonthlyEur?: number; EstMonthlyEUR?: number };

const HOURLY: Record<string, string> = { starter: "€0.007/hr", pro: "€0.013/hr", scale: "€0.026/hr" };

export default function ByoCloudPanel() {
  const [connected, setConnected] = useState<boolean | null>(null);
  const [servers, setServers] = useState<Server[] | null>(null);
  const [snaps, setSnaps] = useState<Snap[] | null>(null);
  const [busy, setBusy] = useState<string | null>(null);
  const [msg, setMsg] = useState<string | null>(null);
  const [plan, setPlan] = useState("starter");
  const [region, setRegion] = useState("eu");
  const [repoUrl, setRepoUrl] = useState("");

  const loadAccounts = useCallback(async () => {
    if (!agentClient.isConnected) { setConnected(false); return; }
    try {
      const r = await agentClient.accountsList();
      const hz = (r.accounts || []).find((a: any) => a.provider === "hetzner");
      setConnected(hz?.connected === true);
    } catch { setConnected(false); }
  }, []);

  const loadServers = useCallback(async () => {
    setBusy("servers");
    try {
      const r = await agentClient.callOps("cloud_list", {});
      setServers(Array.isArray(r.initial?.servers) ? r.initial.servers : []);
      const s = await agentClient.callOps("cloud_snapshots", {});
      setSnaps(Array.isArray(s.initial?.snapshots) ? s.initial.snapshots : []);
    } catch (e: any) {
      setMsg(e?.message || "Couldn't list servers");
    } finally { setBusy(null); }
  }, []);

  useEffect(() => { void loadAccounts(); }, [loadAccounts]);

  if (connected !== true) return null;

  const money = (n?: number) => `€${Number(n ?? 0).toFixed(2)}`;
  const did = (r: any, ok: string) => {
    if (r?.initial?.dryRun) setMsg(`${r.initial.plan || "Dry run"} — set YAVER_CLOUD_STOPSTART_LIVE=1 on the machine for real action.`);
    else setMsg(ok);
  };

  const act = async (key: string, fn: () => Promise<any>, ok: string) => {
    setBusy(key); setMsg(null);
    try { did(await fn(), ok); await loadServers(); }
    catch (e: any) { setMsg(e?.message || "Action failed"); }
    finally { setBusy(null); }
  };

  const id = (s: Server) => String(s.id ?? s.ID ?? "");
  const nm = (s: Server) => String(s.name ?? s.Name ?? id(s));

  return (
    <div className="rounded-xl border border-sky-500/20 bg-sky-500/5 p-4 space-y-3">
      <div className="flex items-center justify-between">
        <div>
          <h3 className="text-sm font-semibold text-surface-200">Your Hetzner boxes</h3>
          <p className="text-xs text-surface-500">Run on your own account — you pay Hetzner directly. Stop to halt billing; start from a snapshot anytime.</p>
        </div>
        <button onClick={() => void loadServers()} disabled={!!busy}
          className="rounded-md border border-sky-500/40 px-3 py-1.5 text-xs font-semibold text-sky-700 dark:text-sky-300 disabled:opacity-50">
          {busy === "servers" ? "…" : servers === null ? "Load" : "Refresh"}
        </button>
      </div>

      {/* Spin up */}
      <div className="flex flex-wrap items-end gap-2 rounded-lg border border-surface-800 p-3">
        <label className="text-[11px] text-surface-400">Plan
          <select value={plan} onChange={(e) => setPlan(e.target.value)} className="mt-1 block rounded border border-surface-700 bg-surface-900 px-2 py-1 text-xs">
            <option value="starter">starter (~{HOURLY.starter})</option>
            <option value="pro">pro (~{HOURLY.pro})</option>
            <option value="scale">scale (~{HOURLY.scale})</option>
          </select>
        </label>
        <label className="text-[11px] text-surface-400">Region
          <select value={region} onChange={(e) => setRegion(e.target.value)} className="mt-1 block rounded border border-surface-700 bg-surface-900 px-2 py-1 text-xs">
            <option value="eu">eu</option><option value="us">us</option>
          </select>
        </label>
        <input value={repoUrl} onChange={(e) => setRepoUrl(e.target.value)} placeholder="Git repo to clone (optional)"
          className="flex-1 min-w-[180px] rounded border border-surface-700 bg-surface-900 px-2 py-1 text-xs font-mono" />
        <button disabled={!!busy}
          onClick={() => act("spinup", () => agentClient.callOps("cloud_provision", { plan, region, repoUrl: repoUrl.trim() || undefined, confirm: true }), "Box spinning up — it self-installs + appears as a device to claim.")}
          className="rounded-md border border-emerald-500/50 bg-emerald-500/10 px-3 py-1.5 text-xs font-semibold text-emerald-700 dark:text-emerald-300 disabled:opacity-50">
          {busy === "spinup" ? "…" : "Spin up"}
        </button>
      </div>

      {/* Running servers */}
      {servers === null ? (
        <p className="text-xs text-surface-500">Tap Load to list servers on your account.</p>
      ) : servers.length === 0 ? (
        <p className="text-xs text-surface-500">No running servers.</p>
      ) : (
        <div className="space-y-1">
          {servers.map((s) => (
            <div key={id(s)} className="flex items-center gap-2 text-xs">
              <span className="flex-1 font-mono text-surface-400 truncate">{nm(s)} · {String(s.status ?? s.Status ?? "?")} · {String(s.ip ?? s.IP ?? "")}</span>
              <button disabled={!!busy} onClick={() => act(`bake:${id(s)}`, () => agentClient.callOps("cloud_bake", { serverId: id(s), confirm: true }), "Baked golden image — new boxes boot fast.")}
                className="text-sky-700 dark:text-sky-300 font-semibold disabled:opacity-50">{busy === `bake:${id(s)}` ? "…" : "Bake"}</button>
              <button disabled={!!busy} onClick={() => act(`stop:${id(s)}`, () => agentClient.callOps("cloud_stop", { serverId: id(s), confirm: true }), "Stopped — snapshot kept, billing halted.")}
                className="text-amber-400 font-semibold disabled:opacity-50">{busy === `stop:${id(s)}` ? "…" : "Stop"}</button>
              <button disabled={!!busy} onClick={() => { if (confirm(`Delete ${nm(s)} permanently?`)) void act(`rm:${id(s)}`, () => agentClient.callOps("cloud_destroy", { serverId: id(s), confirm: true }), "Deleted."); }}
                className="text-red-400 font-semibold disabled:opacity-50">{busy === `rm:${id(s)}` ? "…" : "Delete"}</button>
            </div>
          ))}
        </div>
      )}

      {/* Stopped boxes (snapshots) — restart */}
      {snaps && snaps.length > 0 ? (
        <div className="space-y-1 border-t border-surface-800 pt-2">
          <p className="text-xs font-semibold text-surface-300">Stopped boxes (snapshots)</p>
          {snaps.map((s) => {
            const sid = String(s.id ?? s.ID ?? "");
            const desc = String(s.description ?? s.Description ?? sid);
            const name = (desc.replace(/^yaver-stop-/, "yaver-") || `yaver-${sid}`).slice(0, 40);
            return (
              <div key={sid} className="flex items-center gap-2 text-xs">
                <span className="flex-1 font-mono text-surface-400 truncate">{desc} · {money(s.estMonthlyEur ?? s.EstMonthlyEUR)}/mo</span>
                <button disabled={!!busy} onClick={() => act(`start:${sid}`, () => agentClient.callOps("cloud_start", { snapshotImageId: sid, name, plan, region, confirm: true }), "Box starting from snapshot.")}
                  className="text-emerald-400 font-semibold disabled:opacity-50">{busy === `start:${sid}` ? "…" : "Start"}</button>
              </div>
            );
          })}
        </div>
      ) : null}

      {msg ? <p className="text-xs text-surface-400">{msg}</p> : null}
    </div>
  );
}
