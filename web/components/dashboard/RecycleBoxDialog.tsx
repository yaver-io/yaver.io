"use client";

// RecycleBoxDialog — BYO box lifecycle. Two modes:
//   • Recycle (replace): create a fresh Hetzner box, health-check,
//     then snapshot+delete the old one (zero-downtime; rollback keeps
//     the old box if the new one is unhealthy). Agent `recycle` verb.
//   • Remove (decommission): clean snapshot+delete of the box, NO
//     replacement. Agent `cloud_destroy` verb.
// Thin trigger: every safety guard lives agent-side. Theme-aware
// (Tailwind, light+dark) — no hardcoded dark surface.

import { useEffect, useState } from "react";
import { agentClient } from "@/lib/agent-client";

interface HetznerServerInfo {
  id: string;
  name: string;
  ip: string;
  status: string;
  type: string;
  location: string;
  created: string;
}

interface RecycleBoxDialogProps {
  deviceId: string;
  deviceName: string;
  onClose: () => void;
}

type Mode = "recycle" | "remove";
type Phase = "form" | "preview" | "running" | "done";

export function RecycleBoxDialog({ deviceId, deviceName, onClose }: RecycleBoxDialogProps) {
  const [mode, setMode] = useState<Mode>("recycle");
  const [oldServerId, setOldServerId] = useState("");
  const [newName, setNewName] = useState(`${deviceName || "box"}-new`);
  const [plan, setPlan] = useState("starter");
  const [region, setRegion] = useState("eu");
  const [phase, setPhase] = useState<Phase>("form");
  const [steps, setSteps] = useState<string[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  // Server-id resolution — so the user never has to recall it.
  const [resolvedFrom, setResolvedFrom] = useState<string | null>(null);
  const [servers, setServers] = useState<HetznerServerInfo[] | null>(null);
  const [lookupBusy, setLookupBusy] = useState(false);
  const [lookupNote, setLookupNote] = useState<string | null>(null);

  // On open, try to resolve the exact Hetzner id of THIS box from the
  // managed-box record (cloud_status → /subscription). Pure prefill;
  // the field stays editable. Never overrides a value the user typed.
  useEffect(() => {
    let cancelled = false;
    (async () => {
      try {
        const res = await agentClient.callOps("cloud_status", {});
        if (cancelled || res.ok === false) return;
        const machines: any[] = (res.initial as any)?.machines || [];
        const hit =
          machines.find((m) => m.deviceId && m.deviceId === deviceId) ||
          machines.find((m) => m.hostname && m.hostname === deviceName);
        if (hit?.hetznerServerId) {
          setOldServerId((cur) => (cur.trim() === "" ? String(hit.hetznerServerId) : cur));
          setResolvedFrom(
            `managed box record${hit.hostname ? ` · ${hit.hostname}` : ""}${hit.serverIp ? ` · ${hit.serverIp}` : ""}`,
          );
        }
      } catch {
        /* best-effort prefill; manual entry / picker still available */
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [deviceId, deviceName]);

  // Live picker — list real account servers so the user picks the
  // exact box by name+IP instead of recalling a numeric id. Resolution
  // from the live account, not a fuzzy guess (user still selects).
  async function lookupServers() {
    setLookupBusy(true);
    setLookupNote(null);
    try {
      const res = await agentClient.callOps("cloud_list", {});
      if (res.ok === false) {
        const code = (res as any).code || "";
        setLookupNote(
          code === "unknown_verb" || /unknown/i.test((res as any).error || "")
            ? "This agent is too old for the server picker — update it. Managed boxes are still auto-resolved above."
            : (res as any).error || "could not list servers",
        );
        return;
      }
      const list: HetznerServerInfo[] = (res.initial as any)?.servers || [];
      setServers(list);
      if (list.length === 0) {
        setLookupNote("No servers on the connected Hetzner account.");
        return;
      }
      // Auto-select the unambiguous match (by name, then current id).
      const match =
        list.find((s) => s.name && s.name === deviceName) ||
        list.find((s) => s.id === oldServerId.trim());
      if (match && oldServerId.trim() === "") {
        setOldServerId(match.id);
        setResolvedFrom(`live account · ${match.name} · ${match.ip}`);
      }
    } catch (e: any) {
      setLookupNote(e?.message || String(e));
    } finally {
      setLookupBusy(false);
    }
  }

  async function runRecycle(confirm: boolean) {
    setBusy(true);
    setError(null);
    try {
      const res = await agentClient.callOps("recycle", {
        targetDeviceId: deviceId,
        oldServerId: oldServerId.trim(),
        newName: newName.trim(),
        plan,
        region,
        confirm,
      });
      const r: any = res.initial || {};
      setSteps(Array.isArray(r.steps) ? r.steps : []);
      if (res.ok === false || r.error) {
        setError(r.error || (res as any).error || "recycle failed");
        setPhase(confirm ? "done" : "form");
      } else {
        setPhase(confirm ? "done" : "preview");
      }
    } catch (e: any) {
      setError(e?.message || String(e));
      setPhase("form");
    } finally {
      setBusy(false);
    }
  }

  async function runRemove() {
    setBusy(true);
    setError(null);
    try {
      const res = await agentClient.callOps("cloud_destroy", {
        serverId: oldServerId.trim(),
        confirm: true,
      });
      const r: any = res.initial || {};
      if (res.ok === false || r.error) {
        setError(r.error || (res as any).error || "remove failed");
        setPhase("form");
      } else {
        setSteps([`snapshot taken, server ${oldServerId.trim()} deleted`]);
        setPhase("done");
      }
    } catch (e: any) {
      setError(e?.message || String(e));
      setPhase("form");
    } finally {
      setBusy(false);
    }
  }

  const inputCls =
    "w-full rounded-md border border-slate-300 bg-white px-2.5 py-1.5 text-sm text-slate-900 placeholder:text-slate-400 dark:border-surface-700 dark:bg-[rgba(12,12,16,0.9)] dark:text-surface-100";
  const tab = (m: Mode, label: string) => (
    <button
      onClick={() => { setMode(m); setPhase("form"); setError(null); setSteps([]); }}
      className={`flex-1 rounded-md px-3 py-1.5 text-xs font-semibold ${
        mode === m
          ? "bg-slate-900 text-white dark:bg-surface-100 dark:text-surface-900"
          : "bg-slate-100 text-slate-600 dark:bg-surface-800 dark:text-surface-400"
      }`}
    >
      {label}
    </button>
  );

  return (
    <div
      className="fixed inset-0 z-[1000] flex items-center justify-center bg-black/50 p-4"
      onClick={onClose}
    >
      <div
        onClick={(e) => e.stopPropagation()}
        className="max-h-[88vh] w-[460px] max-w-[92vw] overflow-auto rounded-xl border border-slate-200 bg-white p-5 text-slate-900 shadow-2xl dark:border-surface-800 dark:bg-surface-900 dark:text-surface-100"
      >
        <h3 className="m-0 mb-1 text-base font-semibold">
          {mode === "recycle" ? "Recycle" : "Remove"} box — {deviceName || deviceId.slice(0, 8)}
        </h3>
        <p className="mb-3 text-xs text-slate-500 dark:text-surface-400">
          {mode === "recycle"
            ? "Creates a fresh box, health-checks it, then snapshots & deletes the old one. The old box keeps serving until the new one is healthy — a failure rolls back with nothing destroyed."
            : "Snapshots then deletes this box. No replacement. The snapshot is your recovery point. The agent refuses to remove the device it runs on."}
        </p>

        <div className="mb-3 flex gap-1.5">
          {tab("recycle", "Recycle (replace)")}
          {tab("remove", "Remove (decommission)")}
        </div>

        {phase !== "done" ? (
          <div className="grid gap-2.5">
            <label className="text-xs text-slate-600 dark:text-surface-400">
              Old Hetzner server id (exact — resolved for you, not guessed)
              <input
                value={oldServerId}
                onChange={(e) => { setOldServerId(e.target.value); setResolvedFrom(null); }}
                placeholder="resolving… or pick / type the id"
                disabled={phase === "preview"}
                className={`mt-1 ${inputCls}`}
              />
            </label>
            {resolvedFrom ? (
              <p className="-mt-1 text-[11px] text-emerald-600 dark:text-emerald-400">
                ✓ resolved from {resolvedFrom} — confirm it's the right box
              </p>
            ) : null}
            {phase !== "preview" ? (
              <div className="-mt-0.5 grid gap-1.5">
                <button
                  type="button"
                  onClick={() => void lookupServers()}
                  disabled={lookupBusy}
                  className="justify-self-start rounded-md border border-slate-300 px-2.5 py-1 text-[11px] font-medium text-slate-700 disabled:opacity-50 dark:border-surface-700 dark:text-surface-300"
                >
                  {lookupBusy ? "Looking up…" : servers ? "Refresh server list" : "Look up my servers"}
                </button>
                {servers && servers.length > 0 ? (
                  <select
                    value={oldServerId}
                    onChange={(e) => {
                      const s = servers.find((x) => x.id === e.target.value);
                      setOldServerId(e.target.value);
                      setResolvedFrom(s ? `live account · ${s.name} · ${s.ip}` : null);
                    }}
                    className={inputCls}
                  >
                    <option value="">— pick the box to {mode === "recycle" ? "recycle" : "remove"} —</option>
                    {servers.map((s) => (
                      <option key={s.id} value={s.id}>
                        {s.name} · {s.ip} · {s.type} · {s.status} (id {s.id})
                      </option>
                    ))}
                  </select>
                ) : null}
                {lookupNote ? (
                  <p className="text-[11px] text-amber-600 dark:text-amber-400">{lookupNote}</p>
                ) : null}
              </div>
            ) : null}
            {mode === "recycle" ? (
              <>
                <label className="text-xs text-slate-600 dark:text-surface-400">
                  New box name
                  <input value={newName} onChange={(e) => setNewName(e.target.value)} disabled={phase === "preview"} className={`mt-1 ${inputCls}`} />
                </label>
                <div className="flex gap-2.5">
                  <label className="flex-1 text-xs text-slate-600 dark:text-surface-400">
                    Plan
                    <select value={plan} onChange={(e) => setPlan(e.target.value)} disabled={phase === "preview"} className={`mt-1 ${inputCls}`}>
                      <option value="starter">starter</option>
                      <option value="pro">pro</option>
                      <option value="scale">scale</option>
                    </select>
                  </label>
                  <label className="flex-1 text-xs text-slate-600 dark:text-surface-400">
                    Region
                    <select value={region} onChange={(e) => setRegion(e.target.value)} disabled={phase === "preview"} className={`mt-1 ${inputCls}`}>
                      <option value="eu">eu</option>
                      <option value="us">us</option>
                    </select>
                  </label>
                </div>
              </>
            ) : null}
          </div>
        ) : null}

        {steps.length > 0 ? (
          <pre className="mt-3.5 max-h-56 overflow-auto whitespace-pre-wrap rounded-lg bg-slate-100 p-3 text-xs text-slate-700 dark:bg-[rgba(12,12,16,0.9)] dark:text-surface-300">
            {steps.join("\n")}
          </pre>
        ) : null}
        {error ? <p className="mt-3 text-sm text-rose-600 dark:text-rose-400">{error}</p> : null}

        <div className="mt-4 flex justify-end gap-2.5">
          <button
            onClick={onClose}
            disabled={busy}
            className="rounded-md border border-slate-300 px-3.5 py-2 text-sm font-medium text-slate-700 disabled:opacity-50 dark:border-surface-700 dark:text-surface-300"
          >
            {phase === "done" ? "Close" : "Cancel"}
          </button>
          {mode === "recycle" && phase === "form" ? (
            <button
              onClick={() => void runRecycle(false)}
              disabled={oldServerId.trim() === "" || newName.trim() === "" || busy}
              className="rounded-md border border-slate-300 bg-slate-900 px-3.5 py-2 text-sm font-semibold text-white disabled:opacity-50 dark:bg-surface-100 dark:text-surface-900"
            >
              {busy ? "Previewing…" : "Preview plan (dry-run)"}
            </button>
          ) : null}
          {mode === "recycle" && phase === "preview" ? (
            <button
              onClick={() => void runRecycle(true)}
              disabled={busy}
              className="rounded-md border border-rose-500 bg-rose-600 px-3.5 py-2 text-sm font-semibold text-white disabled:opacity-50"
            >
              {busy ? "Recycling…" : "Confirm & recycle (destructive)"}
            </button>
          ) : null}
          {mode === "remove" && phase === "form" ? (
            <button
              onClick={() => void runRemove()}
              disabled={oldServerId.trim() === "" || busy}
              className="rounded-md border border-rose-500 bg-rose-600 px-3.5 py-2 text-sm font-semibold text-white disabled:opacity-50"
            >
              {busy ? "Removing…" : "Snapshot & remove (destructive)"}
            </button>
          ) : null}
        </div>
      </div>
    </div>
  );
}
