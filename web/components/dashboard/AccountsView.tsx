"use client";

import { useEffect, useState } from "react";
import { agentClient } from "@/lib/agent-client";

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

  useEffect(() => { refresh(); }, []);

  async function refresh() {
    try {
      const r = await agentClient.accountsList();
      setProviders(r.providers || []);
      setAccounts(r.accounts || []);
    } catch {}
  }

  function openConnect(p: Provider) {
    setActive(p);
    setFields(Object.fromEntries(p.fields.map((f) => [f, ""])));
    setLabel("");
  }

  async function save() {
    if (!active) return;
    setSaving(true);
    const r = await agentClient.accountConnect(active.id, label, fields);
    setSaving(false);
    if (r.error) { alert(r.error); return; }
    setActive(null);
    refresh();
  }

  async function disconnect(id: string) {
    if (!confirm(`Disconnect ${id}?`)) return;
    await agentClient.accountDisconnect(id);
    refresh();
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
