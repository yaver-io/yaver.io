"use client";

// RecycleBoxDialog — cloud-box lifecycle, provider-agnostic UI.
//   • Recycle (replace): create a fresh box, health-check, then
//     snapshot+delete the old one (zero-downtime; rollback keeps the
//     old box if the new one is unhealthy). Agent `recycle` verb.
//   • Remove (decommission): clean snapshot+delete, NO replacement.
//     Agent `cloud_destroy` verb.
//
// "Yaver-level connect": this dialog opens its own short-lived agent
// connection (relay → tunnel → direct, same path the rest of the
// dashboard uses) instead of leaning on whatever workspace happens to
// be open. The user never sees "AgentClient is not connected" — either
// it auto-connects and auto-resolves the exact resource, or it says
// plainly that the box can't be reached.
//
// The UI is provider-neutral on purpose ("cloud resource", not the
// IaaS name). Yaver's facade layer (agent ops_cloud.go) is the only
// place that knows the resource lives on a specific provider and how
// to delete it there. The Convex `provider` column records which one.
// Theme-aware (Tailwind, light+dark) — no hardcoded dark surface.

import { useEffect, useRef, useState } from "react";
import { AgentClient, agentClient } from "@/lib/agent-client";
import type { Device } from "@/lib/use-devices";

interface CloudResourceInfo {
  id: string;
  name: string;
  ip: string;
  status: string;
  type: string;
  location: string;
  created: string;
}

interface RecycleBoxDialogProps {
  device: Device;
  token: string;
  onClose: () => void;
}

type Mode = "recycle" | "remove";
type Phase = "form" | "preview" | "running" | "done";

function friendlyConnectError(name: string): string {
  return `Couldn't reach "${name}" to manage its cloud resource — the box looks offline or unreachable right now. It may already be gone, or this will work once it's back online.`;
}

export function RecycleBoxDialog({ device, token, onClose }: RecycleBoxDialogProps) {
  const deviceId = device.id;
  const deviceName = device.alias || device.name || device.id;

  const [mode, setMode] = useState<Mode>("recycle");
  const [resourceId, setResourceId] = useState("");
  const [newName, setNewName] = useState(`${deviceName || "box"}-new`);
  const [plan, setPlan] = useState("starter");
  const [region, setRegion] = useState("eu");
  const [phase, setPhase] = useState<Phase>("form");
  const [steps, setSteps] = useState<string[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  // Yaver-level connect state.
  const [connecting, setConnecting] = useState(true);
  const [connectError, setConnectError] = useState<string | null>(null);

  // Resource resolution — the user never has to recall an id.
  const [resolvedFrom, setResolvedFrom] = useState<string | null>(null);
  const [resolvedLabel, setResolvedLabel] = useState<string | null>(null);
  const [resources, setResources] = useState<CloudResourceInfo[] | null>(null);
  const [lookupBusy, setLookupBusy] = useState(false);
  const [lookupNote, setLookupNote] = useState<string | null>(null);
  const [showPicker, setShowPicker] = useState(false);

  // Short-lived client scoped to this dialog. We don't touch the shared
  // workspace agentClient — this connects, does the cloud op, and is
  // disconnected on unmount. Same connect recipe the rest of the
  // dashboard uses (relay candidates + tunnel URLs).
  const clientRef = useRef<AgentClient | null>(null);

  async function ensureClient(): Promise<AgentClient> {
    const existing = clientRef.current;
    if (existing && existing.isConnected) return existing;
    const client = existing ?? new AgentClient();
    client.setRelayServers(agentClient.configuredRelayServers.map((r) => ({ ...r })));
    const tunnelUrls = Array.from(
      new Set(
        [
          ...(Array.isArray(device.publicEndpoints) ? device.publicEndpoints : []),
          ...(device.tunnelUrl ? [device.tunnelUrl] : []),
        ]
          .map((u) => String(u || "").trim())
          .filter(Boolean),
      ),
    );
    await client.connect(device.host, device.port, token, device.id, { tunnelUrls });
    clientRef.current = client;
    return client;
  }

  async function callOps(verb: string, payload: Record<string, unknown>) {
    const client = await ensureClient();
    return client.callOps(verb, payload);
  }

  // Disconnect the throwaway client when the dialog closes.
  useEffect(() => {
    return () => {
      try {
        clientRef.current?.disconnect();
      } catch {
        /* noop */
      }
      clientRef.current = null;
    };
  }, []);

  // On open: connect, then auto-resolve THIS box's exact resource so
  // the user never types or recalls an id. Best-effort; failures fall
  // back to the manual picker but never to a raw client error.
  useEffect(() => {
    let cancelled = false;
    (async () => {
      setConnecting(true);
      setConnectError(null);
      try {
        await ensureClient();
        if (cancelled) return;

        // 1. Managed-box record → exact resource id for this device.
        try {
          const res = await callOps("cloud_status", {});
          if (!cancelled && res.ok !== false) {
            const machines: any[] = (res.initial as any)?.machines || [];
            const hit =
              machines.find((m) => m.deviceId && m.deviceId === deviceId) ||
              machines.find((m) => m.hostname && m.hostname === deviceName);
            const rid = hit?.cloudResourceId || hit?.hetznerServerId;
            if (rid) {
              setResourceId(String(rid));
              setResolvedFrom("this box's cloud record");
              setResolvedLabel(
                `${hit.hostname || deviceName}${hit.serverIp ? ` · ${hit.serverIp}` : ""}`,
              );
            }
          }
        } catch {
          /* fall through to the live list */
        }

        // 2. Live account list → auto-select the unambiguous match.
        try {
          const res = await callOps("cloud_list", {});
          if (!cancelled && res.ok !== false) {
            const list: CloudResourceInfo[] = (res.initial as any)?.servers || [];
            setResources(list);
            const match =
              list.find((s) => s.name && s.name === deviceName) ||
              list.find((s) => s.id === resourceId.trim());
            if (match) {
              setResourceId((cur) => (cur.trim() === "" ? match.id : cur));
              setResolvedFrom((cur) => cur ?? "your connected cloud account");
              setResolvedLabel(
                (cur) => cur ?? `${match.name} · ${match.ip} · ${match.status}`,
              );
            }
          }
        } catch {
          /* manual picker still available */
        }
      } catch {
        if (!cancelled) setConnectError(friendlyConnectError(deviceName));
      } finally {
        if (!cancelled) setConnecting(false);
      }
    })();
    return () => {
      cancelled = true;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [deviceId, deviceName]);

  // Manual refresh of the live resource list (rare — only when the
  // auto-resolved match is wrong or ambiguous).
  async function lookupResources() {
    setLookupBusy(true);
    setLookupNote(null);
    try {
      const res = await callOps("cloud_list", {});
      if (res.ok === false) {
        const code = (res as any).code || "";
        setLookupNote(
          code === "unknown_verb" || /unknown/i.test((res as any).error || "")
            ? "This agent is too old for the resource picker — update it. The box's own record is still auto-resolved above."
            : (res as any).error || "could not list cloud resources",
        );
        return;
      }
      const list: CloudResourceInfo[] = (res.initial as any)?.servers || [];
      setResources(list);
      if (list.length === 0) {
        setLookupNote("No cloud resources found on your connected account.");
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
      const res = await callOps("recycle", {
        targetDeviceId: deviceId,
        oldServerId: resourceId.trim(),
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
      setError(e?.message || friendlyConnectError(deviceName));
      setPhase("form");
    } finally {
      setBusy(false);
    }
  }

  async function runRemove() {
    setBusy(true);
    setError(null);
    try {
      const res = await callOps("cloud_destroy", {
        serverId: resourceId.trim(),
        confirm: true,
      });
      const r: any = res.initial || {};
      if (res.ok === false || r.error) {
        setError(r.error || (res as any).error || "remove failed");
        setPhase("form");
      } else {
        setSteps([`Snapshot taken, cloud resource ${resolvedLabel || resourceId.trim()} deleted`]);
        setPhase("done");
      }
    } catch (e: any) {
      setError(e?.message || friendlyConnectError(deviceName));
      setPhase("form");
    } finally {
      setBusy(false);
    }
  }

  function retryConnect() {
    setConnectError(null);
    setConnecting(true);
    // Re-run the resolve effect by toggling a fresh client.
    try {
      clientRef.current?.disconnect();
    } catch {
      /* noop */
    }
    clientRef.current = null;
    (async () => {
      try {
        await ensureClient();
        await lookupResources();
        setConnectError(null);
      } catch {
        setConnectError(friendlyConnectError(deviceName));
      } finally {
        setConnecting(false);
      }
    })();
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

  const ready = !connecting && !connectError;
  const resolved = resourceId.trim() !== "";

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

        {connecting ? (
          <div className="flex items-center gap-2 rounded-md bg-slate-100 px-3 py-2.5 text-xs text-slate-600 dark:bg-[rgba(12,12,16,0.9)] dark:text-surface-300">
            <span className="inline-block h-3 w-3 animate-spin rounded-full border-2 border-slate-400 border-t-transparent" />
            Connecting to {deviceName} and resolving its cloud resource…
          </div>
        ) : connectError ? (
          <div className="rounded-md border border-amber-300 bg-amber-50 px-3 py-2.5 text-xs text-amber-700 dark:border-amber-500/40 dark:bg-amber-500/10 dark:text-amber-400">
            <p className="m-0">{connectError}</p>
            <button
              type="button"
              onClick={retryConnect}
              className="mt-2 rounded-md border border-amber-400 px-2.5 py-1 text-[11px] font-medium text-amber-700 dark:border-amber-500/40 dark:text-amber-300"
            >
              Try again
            </button>
          </div>
        ) : phase !== "done" ? (
          <div className="grid gap-2.5">
            {resolved && !showPicker ? (
              <div className="rounded-md border border-emerald-300 bg-emerald-50 px-3 py-2.5 text-xs text-emerald-700 dark:border-emerald-500/40 dark:bg-emerald-500/10 dark:text-emerald-400">
                <p className="m-0 font-medium">
                  {mode === "recycle" ? "Will recycle" : "Will remove"}: {resolvedLabel || resourceId.trim()}
                </p>
                <p className="m-0 mt-0.5 text-[11px] opacity-80">
                  Resolved automatically from {resolvedFrom || "your connected cloud account"}.
                </p>
                <button
                  type="button"
                  onClick={() => { setShowPicker(true); if (!resources) void lookupResources(); }}
                  className="mt-1.5 text-[11px] font-medium underline underline-offset-2"
                >
                  Use a different resource
                </button>
              </div>
            ) : (
              <div className="grid gap-1.5">
                <label className="text-xs text-slate-600 dark:text-surface-400">
                  Cloud resource to {mode === "recycle" ? "recycle" : "remove"}
                  <input
                    value={resourceId}
                    onChange={(e) => { setResourceId(e.target.value); setResolvedFrom(null); setResolvedLabel(null); }}
                    placeholder={connecting ? "resolving…" : "pick below, or paste a resource id"}
                    disabled={phase === "preview"}
                    className={`mt-1 ${inputCls}`}
                  />
                </label>
                <div className="flex items-center gap-2">
                  <button
                    type="button"
                    onClick={() => void lookupResources()}
                    disabled={lookupBusy}
                    className="rounded-md border border-slate-300 px-2.5 py-1 text-[11px] font-medium text-slate-700 disabled:opacity-50 dark:border-surface-700 dark:text-surface-300"
                  >
                    {lookupBusy ? "Loading…" : resources ? "Refresh list" : "Show all my cloud resources"}
                  </button>
                  {resolved ? (
                    <button
                      type="button"
                      onClick={() => setShowPicker(false)}
                      className="text-[11px] font-medium text-slate-500 underline underline-offset-2 dark:text-surface-400"
                    >
                      back to resolved
                    </button>
                  ) : null}
                </div>
                {resources && resources.length > 0 ? (
                  <select
                    value={resourceId}
                    onChange={(e) => {
                      const s = resources.find((x) => x.id === e.target.value);
                      setResourceId(e.target.value);
                      setResolvedFrom(s ? "your connected cloud account" : null);
                      setResolvedLabel(s ? `${s.name} · ${s.ip} · ${s.status}` : null);
                    }}
                    className={inputCls}
                  >
                    <option value="">— pick the box to {mode === "recycle" ? "recycle" : "remove"} —</option>
                    {resources.map((s) => (
                      <option key={s.id} value={s.id}>
                        {s.name} · {s.ip} · {s.type} · {s.status}
                      </option>
                    ))}
                  </select>
                ) : null}
                {lookupNote ? (
                  <p className="text-[11px] text-amber-600 dark:text-amber-400">{lookupNote}</p>
                ) : null}
              </div>
            )}

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
          {ready && mode === "recycle" && phase === "form" ? (
            <button
              onClick={() => void runRecycle(false)}
              disabled={!resolved || newName.trim() === "" || busy}
              className="rounded-md border border-slate-300 bg-slate-900 px-3.5 py-2 text-sm font-semibold text-white disabled:opacity-50 dark:bg-surface-100 dark:text-surface-900"
            >
              {busy ? "Previewing…" : "Preview plan (dry-run)"}
            </button>
          ) : null}
          {ready && mode === "recycle" && phase === "preview" ? (
            <button
              onClick={() => void runRecycle(true)}
              disabled={busy}
              className="rounded-md border border-rose-500 bg-rose-600 px-3.5 py-2 text-sm font-semibold text-white disabled:opacity-50"
            >
              {busy ? "Recycling…" : "Confirm & recycle (destructive)"}
            </button>
          ) : null}
          {ready && mode === "remove" && phase === "form" ? (
            <button
              onClick={() => void runRemove()}
              disabled={!resolved || busy}
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
