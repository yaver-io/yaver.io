"use client";

import { useEffect, useState } from "react";
import { agentClient } from "@/lib/agent-client";
import ByoCloudPanel from "@/components/dashboard/ByoCloudPanel";

type Provider = {
  id: string;
  label: string;
  authType: string;
  fields: string[];
  signupURL: string;
  tokenURL: string;
  notes?: string;
};

type Account = {
  provider: string;
  label?: string;
  connected: boolean;
  connectedAt?: string;
  lastUsedAt?: string;
  hasSecret?: boolean;
  hint?: string;
};

export default function AccountsView() {
  const [providers, setProviders] = useState<Provider[]>([]);
  const [accounts, setAccounts] = useState<Account[]>([]);
  const [active, setActive] = useState<Provider | null>(null);
  const [fields, setFields] = useState<Record<string, string>>({});
  const [label, setLabel] = useState("");
  const [saving, setSaving] = useState(false);
  const [connectError, setConnectError] = useState<string | null>(null);
  const [connectedMachine, setConnectedMachine] = useState<string>("");
  const [machineDeleteConfirm, setMachineDeleteConfirm] = useState("");
  const [removingMachine, setRemovingMachine] = useState(false);
  const [machineRemoveMessage, setMachineRemoveMessage] = useState<string | null>(null);
  // Streaming uninstall progress: each entry is one step the remote
  // agent emits via /streams/<machine-remove:...>. Rendered live until
  // the machine_remove_result event arrives or the connection drops
  // (the agent process has exited).
  const [machineRemoveSteps, setMachineRemoveSteps] = useState<
    { step: string; status: string; detail: string; error?: string }[]
  >([]);

  useEffect(() => { refresh(); }, []);

  async function refresh() {
    try {
      const r = await agentClient.accountsList();
      setProviders(r.providers || []);
      setAccounts(r.accounts || []);
    } catch {}
    if (agentClient.isConnected) {
      try {
        const info = await agentClient.getInfo();
        setConnectedMachine(info.hostname || "");
      } catch {
        setConnectedMachine("");
      }
    } else {
      setConnectedMachine("");
    }
  }

  function openConnect(p: Provider) {
    setActive(p);
    setFields(Object.fromEntries(p.fields.map((f) => [f, ""])));
    setLabel("");
    setConnectError(null);
  }

  async function save() {
    if (!active) return;
    setSaving(true);
    setConnectError(null);
    try {
      const r = await agentClient.accountConnect(active.id, label, fields);
      if (r.error) {
        const e = String(r.error);
        setConnectError(e.trim() && e.length <= 160 ? e : "Couldn't connect this account. Check the values and try again.");
        return;
      }
      setActive(null);
      // Hygiene: never keep the pasted token (or any secret field) in
      // React state after it's been sent to the agent.
      setFields({});
      setLabel("");
      refresh();
    } catch {
      setConnectError("Couldn't connect this account — the agent may be unreachable.");
    } finally {
      setSaving(false);
    }
  }

  async function disconnect(id: string) {
    if (!confirm(`Disconnect ${id}?`)) return;
    await agentClient.accountDisconnect(id);
    refresh();
  }

  async function removeMachine() {
    if (machineDeleteConfirm !== "delete my machine") return;
    setRemovingMachine(true);
    setMachineRemoveMessage(null);
    setMachineRemoveSteps([]);
    try {
      const res = await agentClient.machineRemove(machineDeleteConfirm);
      if (!res?.ok) {
        throw new Error(res?.error || "Failed to remove machine");
      }
      setMachineDeleteConfirm("");
      // The agent returns a stream name we can subscribe to for live
      // step-by-step progress. Old agents (pre-1.99.163) won't include
      // it; in that case we keep the old "scheduled" toast behavior.
      const streamName: string | undefined = res?.stream;
      if (!streamName) {
        const manualSteps = Array.isArray(res?.manualSteps) ? ` Manual cleanup if needed: ${res.manualSteps.join(" | ")}` : "";
        setMachineRemoveMessage(`Removal started for ${connectedMachine || "this machine"}.${manualSteps}`);
        agentClient.disconnect();
        return;
      }
      // Track the high-water mark so we can recognize a successful
      // uninstall even if the agent process exits before its final
      // machine_remove_result event flushes through SSE. config_dir=ok
      // is the last destructive step — past that the box is clean.
      let configDirCleared = false;
      let resolved = false;
      const finish = (status: "ok" | "error", message: string) => {
        if (resolved) return;
        resolved = true;
        setMachineRemoveMessage(message);
        cleanup();
        agentClient.disconnect();
      };
      const cleanup = agentClient.streamLog(
        streamName,
        (evt: any) => {
          if (evt?.type === "machine_remove_step") {
            if (evt.step === "config_dir" && evt.status === "ok") configDirCleared = true;
            setMachineRemoveSteps((prev) => {
              // Collapse consecutive ok events for the same step so the
              // UI is one row per phase. Errors and running events
              // always append.
              if (prev.length > 0) {
                const last = prev[prev.length - 1];
                if (last.step === evt.step && last.status === "ok" && evt.status === "ok") {
                  const next = prev.slice(0, -1);
                  next.push({ step: evt.step, status: evt.status, detail: evt.detail || last.detail, error: evt.error });
                  return next;
                }
              }
              return [...prev, { step: evt.step, status: evt.status, detail: evt.detail || "", error: evt.error }];
            });
          } else if (evt?.type === "machine_remove_result") {
            finish(
              evt.status === "error" ? "error" : "ok",
              evt.status === "error"
                ? `Removal failed: ${evt.error || "see step list above"}`
                : `${connectedMachine || "Machine"} entirely removed.`,
            );
          }
        },
        () => {
          // Agent process exited (stream closed). Treat as success if
          // we already saw config_dir=ok; otherwise surface partial-
          // state so the user knows to refresh and verify.
          if (configDirCleared) {
            finish("ok", `${connectedMachine || "Machine"} entirely removed.`);
          } else {
            finish("error", "Connection dropped before the agent reported completion. Refresh to verify the device list.");
          }
        },
      );
    } catch (error) {
      const message = error instanceof Error ? error.message : "Failed to remove machine";
      setMachineRemoveMessage(message);
    } finally {
      setRemovingMachine(false);
    }
  }

  const byProvider: Record<string, Account> = {};
  for (const a of accounts) byProvider[a.provider] = a;

  return (
    <div className="space-y-4">
      <p className="text-sm text-surface-500">
        Cloud provider credentials are encrypted at rest (AES-GCM) at <code className="text-surface-400">~/.yaver/secrets/</code>.
        The key is stored at <code className="text-surface-400">~/.yaver/master.key</code> — back it up if you want to restore on another machine.
      </p>

      <div className="grid sm:grid-cols-2 lg:grid-cols-3 gap-2">
        {providers.map((p) => {
          const acct = byProvider[p.id];
          return (
            <div key={p.id} className="bg-surface-900/50 border border-surface-800 rounded-lg p-3 space-y-2">
              <div className="flex items-center gap-2">
                <span className={`w-2 h-2 rounded-full ${acct?.connected ? "bg-emerald-400" : "bg-surface-600"}`} />
                <span className="text-sm font-semibold text-surface-200 flex-1">{p.label}</span>
                <span className="text-[10px] uppercase text-surface-500">{p.authType}</span>
              </div>
              {acct?.connected ? (
                <div className="flex items-center gap-2">
                  <span className="text-xs text-emerald-400 flex-1">connected {acct.connectedAt?.slice(0, 10)}</span>
                  <button onClick={() => disconnect(p.id)} className="text-xs text-red-400 hover:text-red-300">Disconnect</button>
                </div>
              ) : (
                <div className="flex items-center gap-2">
                  <button onClick={() => openConnect(p)} className="px-2 py-1 text-xs rounded bg-indigo-500/20 text-indigo-300 hover:bg-indigo-500/30">Connect</button>
                  {p.tokenURL && <a href={p.tokenURL.startsWith("http") ? p.tokenURL : undefined} target="_blank" rel="noreferrer" className="text-xs text-surface-500 hover:text-surface-300 truncate">get token</a>}
                </div>
              )}
              {p.notes && <div className="text-[10px] text-surface-500">{p.notes}</div>}
            </div>
          );
        })}
      </div>

      {/* Manage boxes on the user's own Hetzner (stop / start / delete /
          bake / spin up) — renders only when Hetzner is connected. */}
      <ByoCloudPanel />

      <div className="rounded-xl border border-red-500/20 bg-red-500/5 p-4">
        <div className="text-xs font-medium uppercase tracking-wider text-red-300">Danger Zone</div>
        <p className="mt-2 text-sm text-surface-400">
          Permanently remove Yaver from the connected host machine. This unregisters the device, removes auto-start, wipes <code className="text-surface-300">~/.yaver</code>, and stops the agent. Your repositories are not deleted.
        </p>
        <p className="mt-3 text-xs text-surface-500">
          Type <span className="font-mono text-surface-300">delete my machine</span> to confirm{connectedMachine ? ` on ${connectedMachine}` : ""}.
        </p>
        <input
          type="text"
          value={machineDeleteConfirm}
          onChange={(e) => setMachineDeleteConfirm(e.target.value)}
          placeholder="delete my machine"
          disabled={removingMachine || !agentClient.isConnected}
          className="mt-3 w-full rounded-lg border border-surface-700 bg-surface-900 px-3 py-2 text-sm text-surface-200 outline-none focus:border-red-500/50 disabled:opacity-50"
        />
        {machineRemoveSteps.length > 0 ? (
          <div className="mt-3 rounded-lg border border-surface-800 bg-surface-950/60 p-3 max-h-64 overflow-auto font-mono text-xs space-y-1">
            {machineRemoveSteps.map((s, i) => (
              <div key={i} className="flex gap-2">
                <span className={
                  s.status === "ok" ? "text-emerald-400 w-3" :
                  s.status === "error" ? "text-red-400 w-3" :
                  s.status === "skipped" ? "text-surface-500 w-3" :
                  "text-amber-300 w-3"
                }>
                  {s.status === "ok" ? "✓" : s.status === "error" ? "✗" : s.status === "skipped" ? "—" : "›"}
                </span>
                <span className="text-surface-400 min-w-[7rem]">{s.step}</span>
                <span className={s.status === "error" ? "text-red-300 flex-1" : "text-surface-300 flex-1"}>
                  {s.error ? s.error : s.detail}
                </span>
              </div>
            ))}
          </div>
        ) : null}
        {machineRemoveMessage ? (
          <p className="mt-3 text-xs text-surface-400">{machineRemoveMessage}</p>
        ) : null}
        <button
          onClick={removeMachine}
          disabled={machineDeleteConfirm !== "delete my machine" || removingMachine || !agentClient.isConnected}
          className="mt-3 rounded-lg border border-red-500/30 px-4 py-2 text-sm font-medium text-red-300 transition-colors hover:bg-red-500/10 disabled:opacity-30"
        >
          {removingMachine ? "Removing..." : agentClient.isConnected ? "Remove Yaver From This Host" : "Connect a machine first"}
        </button>
      </div>

      {active && (
        <div className="fixed inset-0 bg-black/60 flex items-center justify-center z-50 p-4">
          <div className="bg-surface-950 border border-surface-700 rounded-xl p-5 max-w-md w-full space-y-3">
            <h3 className="text-sm font-semibold text-surface-200">Connect {active.label}</h3>
            <input
              value={label}
              onChange={(e) => setLabel(e.target.value)}
              placeholder="label (optional, e.g. 'work account')"
              className="w-full rounded-lg border border-surface-700 bg-surface-900 px-3 py-2 text-sm text-surface-200 outline-none focus:border-indigo-500"
            />
            {active.fields.map((f) => (
              <input
                key={f}
                type={f.toLowerCase().includes("secret") || f === "token" ? "password" : "text"}
                value={fields[f] || ""}
                onChange={(e) => setFields({ ...fields, [f]: e.target.value })}
                placeholder={f}
                className="w-full rounded-lg border border-surface-700 bg-surface-900 px-3 py-2 text-sm font-mono text-surface-200 outline-none focus:border-indigo-500"
              />
            ))}
            {connectError && (
              <div className="text-sm text-red-400">{connectError}</div>
            )}
            <div className="flex gap-2 pt-2">
              <button onClick={save} disabled={saving} className="px-4 py-2 text-sm rounded-lg bg-indigo-500 text-white hover:bg-indigo-400 disabled:opacity-50">
                {saving ? "Saving…" : "Save"}
              </button>
              <button onClick={() => setActive(null)} className="px-4 py-2 text-sm rounded-lg bg-surface-800 text-surface-200 hover:bg-surface-700">Cancel</button>
            </div>
            {active.tokenURL && !active.tokenURL.startsWith("http") && (
              <div className="text-xs text-surface-500">{active.tokenURL}</div>
            )}
          </div>
        </div>
      )}
    </div>
  );
}
