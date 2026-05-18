"use client";

// RecycleBoxDialog — cloud-box lifecycle, provider-agnostic UI.
//   • Recycle (replace): create a fresh box, health-check, then
//     snapshot+delete the old one (zero-downtime; rollback keeps the
//     old box if the new one is unhealthy). Agent `recycle` verb.
//   • Remove (decommission): clean snapshot+delete, NO replacement.
//     Agent `cloud_destroy` verb.
//
// CONTROL-AGENT MODEL. You can't reliably decommission a box by running
// the teardown *on that box* — its agent may be old, offline, or it's
// the very box being deleted (self-destruct). The provider delete is an
// API call that needs the user's cloud credential, which lives in ONE
// agent's local vault (whichever machine ran `accounts connect`) — not
// on every box. So this dialog never connects to the target. It scans
// the user's other online devices, finds the one that (a) is new enough
// to have the cloud verbs and (b) actually holds the cloud account, and
// runs everything there, targeting the box by resource id. The target
// box can be powered off, wiped, or unreachable — irrelevant.
//
// The UI is provider-neutral ("cloud resource", not the IaaS name).
// Yaver's facade layer (agent ops_cloud.go) is the only place that
// knows the provider and how to delete there. Theme-aware (light+dark).

import { useEffect, useRef, useState } from "react";
import { AgentClient, agentClient } from "@/lib/agent-client";
import type { Device } from "@/lib/use-devices";

// Local, dependency-free version ordering (avoids a circular import
// with DevicesView). Only used to rank control-agent candidates —
// newest agent first — so loose parsing is fine.
function cmpVer(a: string, b: string): number {
  const pa = a.split(".").map((n) => parseInt(n, 10) || 0);
  const pb = b.split(".").map((n) => parseInt(n, 10) || 0);
  for (let i = 0; i < Math.max(pa.length, pb.length); i++) {
    const d = (pa[i] || 0) - (pb[i] || 0);
    if (d !== 0) return d < 0 ? -1 : 1;
  }
  return 0;
}

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
  device: Device; // the box being recycled/removed (the target)
  devices: Device[]; // all of the user's devices, to pick a control agent
  primaryDeviceId: string | null;
  token: string;
  onClose: () => void;
}

type Mode = "recycle" | "remove";
type Phase = "form" | "preview" | "running" | "done";

// Why a candidate couldn't be the control agent — drives a precise,
// provider-neutral message instead of a raw client error.
type SkipReason = "unreachable" | "too-old" | "no-account";

function ipsOf(device: Device): string[] {
  const raw = [device.host, ...(device.publicEndpoints || []), device.tunnelUrl || ""];
  const ips = new Set<string>();
  for (const s of raw) {
    for (const m of String(s || "").matchAll(/\b\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}\b/g)) {
      ips.add(m[0]);
    }
  }
  return Array.from(ips);
}

// Match the target device to a row from the live cloud account. Name
// first (user-set, usually identical), then public IP (strong).
function matchResource(servers: CloudResourceInfo[], target: Device): CloudResourceInfo | undefined {
  const names = [target.name, target.alias].filter(Boolean).map((s) => String(s));
  const byName = servers.find((s) => s.name && names.includes(s.name));
  if (byName) return byName;
  const ips = new Set(ipsOf(target));
  return servers.find((s) => s.ip && ips.has(s.ip));
}

export function RecycleBoxDialog({ device, devices, primaryDeviceId, token, onClose }: RecycleBoxDialogProps) {
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

  // Control-agent connect state.
  const [connecting, setConnecting] = useState(true);
  const [connectError, setConnectError] = useState<string | null>(null);
  const [controlName, setControlName] = useState<string | null>(null);

  // Resource resolution — the user never has to recall an id.
  const [resolvedFrom, setResolvedFrom] = useState<string | null>(null);
  const [resolvedLabel, setResolvedLabel] = useState<string | null>(null);
  const [resources, setResources] = useState<CloudResourceInfo[] | null>(null);
  const [lookupBusy, setLookupBusy] = useState(false);
  const [lookupNote, setLookupNote] = useState<string | null>(null);
  const [showPicker, setShowPicker] = useState(false);

  // The connected control client (NOT the target box). Held for the
  // dialog's lifetime, disconnected on unmount.
  const clientRef = useRef<AgentClient | null>(null);

  // Devices that could act as the control agent, best first: never the
  // target, never a guest, must be online. Primary first, then newest
  // agent version (more likely to have the cloud verbs).
  function controlCandidates(): Device[] {
    return devices
      .filter((d) => d.id !== deviceId && !d.isGuest && d.online)
      .sort((a, b) => {
        if (a.id === primaryDeviceId) return -1;
        if (b.id === primaryDeviceId) return 1;
        const av = String(a.agentVersion || "0").replace(/^v/i, "");
        const bv = String(b.agentVersion || "0").replace(/^v/i, "");
        return -cmpVer(av, bv);
      });
  }

  function connectTo(d: Device): Promise<AgentClient> {
    const client = new AgentClient();
    client.setRelayServers(agentClient.configuredRelayServers.map((r) => ({ ...r })));
    const tunnelUrls = Array.from(
      new Set(
        [...(Array.isArray(d.publicEndpoints) ? d.publicEndpoints : []), ...(d.tunnelUrl ? [d.tunnelUrl] : [])]
          .map((u) => String(u || "").trim())
          .filter(Boolean),
      ),
    );
    return client.connect(d.host, d.port, token, d.id, { tunnelUrls }).then(() => client);
  }

  function classifyOpsError(res: any): SkipReason {
    const code = String(res?.code || "");
    const err = String(res?.error || "");
    if (code === "no_account" || /not connected|connect first|no account/i.test(err)) return "no-account";
    if (code === "unknown_verb" || /unknown/i.test(err)) return "too-old";
    return "too-old";
  }

  function noControlMessage(reasons: Set<SkipReason>, sawAny: boolean): string {
    if (!sawAny) {
      return "Removing a box runs from one of your other devices — not the box itself. None of your other devices is online right now. Bring one online (your primary is best), then try again.";
    }
    if (reasons.has("no-account")) {
      return "None of your online devices has your cloud account connected, so Yaver can't reach the provider to delete this box. Connect it once (Vault → Accounts, on your primary device) and removing or recycling any box works from anywhere.";
    }
    if (reasons.has("too-old")) {
      return "Your other devices' agents are too old to manage cloud resources. Update one — your primary is easiest (`yaver primary auth` then it self-updates) — and try again.";
    }
    return "Couldn't reach any device able to manage cloud resources. Check that another of your devices is online and try again.";
  }

  async function callOps(verb: string, payload: Record<string, unknown>) {
    const client = clientRef.current;
    if (!client) throw new Error("no control agent");
    return client.callOps(verb, payload);
  }

  // Disconnect the control client when the dialog closes.
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

  // On open: find a control agent (probe each candidate with cloud_list
  // — the first that returns ok both proves it has the cloud account
  // AND hands back the resource list to resolve the target), then
  // auto-resolve this box's exact resource. Provider-neutral throughout.
  async function discoverAndResolve(cancelledRef: { v: boolean }) {
    setConnecting(true);
    setConnectError(null);
    setControlName(null);

    const candidates = controlCandidates();
    const reasons = new Set<SkipReason>();
    let chosen: AgentClient | null = null;
    let chosenDevice: Device | null = null;
    let servers: CloudResourceInfo[] = [];

    for (const cand of candidates) {
      if (cancelledRef.v) return;
      let client: AgentClient | null = null;
      try {
        client = await connectTo(cand);
        const res = await client.callOps("cloud_list", {});
        if (res.ok === false) {
          reasons.add(classifyOpsError(res));
          client.disconnect();
          continue;
        }
        chosen = client;
        chosenDevice = cand;
        servers = ((res.initial as any)?.servers || []) as CloudResourceInfo[];
        break;
      } catch {
        reasons.add("unreachable");
        try {
          client?.disconnect();
        } catch {
          /* noop */
        }
      }
    }

    if (cancelledRef.v) {
      try {
        chosen?.disconnect();
      } catch {
        /* noop */
      }
      return;
    }

    if (!chosen || !chosenDevice) {
      setConnectError(noControlMessage(reasons, candidates.length > 0));
      setConnecting(false);
      return;
    }

    clientRef.current = chosen;
    setControlName(chosenDevice.alias || chosenDevice.name || "another device");
    setResources(servers);

    // Resolve the exact resource for this box. Managed record first
    // (cloud_status), then live-account match by name/IP.
    try {
      const st = await chosen.callOps("cloud_status", {});
      if (!cancelledRef.v && st.ok !== false) {
        const machines: any[] = (st.initial as any)?.machines || [];
        const hit =
          machines.find((m) => m.deviceId && m.deviceId === deviceId) ||
          machines.find((m) => m.hostname && m.hostname === deviceName);
        const rid = hit?.cloudResourceId || hit?.hetznerServerId;
        if (rid) {
          setResourceId(String(rid));
          setResolvedFrom("this box's cloud record");
          setResolvedLabel(`${hit.hostname || deviceName}${hit.serverIp ? ` · ${hit.serverIp}` : ""}`);
        }
      }
    } catch {
      /* fall through to live-list match */
    }

    if (!cancelledRef.v) {
      const match = matchResource(servers, device);
      if (match) {
        setResourceId((cur) => (cur.trim() === "" ? match.id : cur));
        setResolvedFrom((cur) => cur ?? "your connected cloud account");
        setResolvedLabel((cur) => cur ?? `${match.name} · ${match.ip} · ${match.status}`);
      }
      setConnecting(false);
    }
  }

  useEffect(() => {
    const cancelledRef = { v: false };
    void discoverAndResolve(cancelledRef);
    return () => {
      cancelledRef.v = true;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [deviceId]);

  // Manual refresh of the live resource list (rare — ambiguous match).
  async function lookupResources() {
    setLookupBusy(true);
    setLookupNote(null);
    try {
      const res = await callOps("cloud_list", {});
      if (res.ok === false) {
        setLookupNote((res as any).error || "could not list cloud resources");
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
      // Runs on the control agent; targetDeviceId is the box being
      // retired. The agent's self-destruct guard passes because the
      // control agent is never the target.
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
      setError(e?.message || "recycle failed");
      setPhase("form");
    } finally {
      setBusy(false);
    }
  }

  async function runRemove() {
    setBusy(true);
    setError(null);
    try {
      // targetDeviceId is sent for the agent-side self-destruct guard
      // (defense-in-depth); older agents ignore the extra field.
      const res = await callOps("cloud_destroy", {
        serverId: resourceId.trim(),
        targetDeviceId: deviceId,
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
      setError(e?.message || "remove failed");
      setPhase("form");
    } finally {
      setBusy(false);
    }
  }

  function retryConnect() {
    try {
      clientRef.current?.disconnect();
    } catch {
      /* noop */
    }
    clientRef.current = null;
    const cancelledRef = { v: false };
    void discoverAndResolve(cancelledRef);
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
            : "Snapshots then deletes this box. No replacement. The snapshot is your recovery point. Runs from another of your devices, so it works even if this box is already offline."}
        </p>

        <div className="mb-3 flex gap-1.5">
          {tab("recycle", "Recycle (replace)")}
          {tab("remove", "Remove (decommission)")}
        </div>

        {connecting ? (
          <div className="flex items-center gap-2 rounded-md bg-slate-100 px-3 py-2.5 text-xs text-slate-600 dark:bg-[rgba(12,12,16,0.9)] dark:text-surface-300">
            <span className="inline-block h-3 w-3 animate-spin rounded-full border-2 border-slate-400 border-t-transparent" />
            Finding a device that can manage this box, and resolving its cloud resource…
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
                  Resolved from {resolvedFrom || "your connected cloud account"}
                  {controlName ? ` · runs via ${controlName}` : ""}.
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
