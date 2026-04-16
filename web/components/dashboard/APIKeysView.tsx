"use client";

// APIKeysView — UI over GET/POST/DELETE /apikeys (apikeys.go).
//
// SDK tokens are created in Convex and registered in a local on-disk
// registry (~/.yaver/apikeys/registry.json) for labels, usage counts
// and disable/enable without deleting the underlying token. The raw
// token is returned exactly once on creation — after that only the
// hash is queryable.

import { useCallback, useEffect, useState } from "react";
import { agentClient, type APIKeyRecord } from "@/lib/agent-client";

const SCOPES = ["feedback", "blackbox", "voice", "builds", "testapp", "health", "todolist"];

export default function APIKeysView() {
  const [keys, setKeys] = useState<APIKeyRecord[]>([]);
  const [loading, setLoading] = useState(true);
  const [err, setErr] = useState<string | null>(null);
  const [freshToken, setFreshToken] = useState<{ label: string; token: string } | null>(null);

  // New key form
  const [label, setLabel] = useState("");
  const [scopes, setScopes] = useState<string[]>(["feedback"]);
  const [expiresDays, setExpiresDays] = useState<string>("365");
  const [creating, setCreating] = useState(false);

  const load = useCallback(async () => {
    try {
      setErr(null);
      const rows = await agentClient.apiKeyList();
      rows.sort((a, b) => a.label.localeCompare(b.label));
      setKeys(rows);
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  async function createKey() {
    if (!label.trim()) return;
    setCreating(true);
    try {
      const days = Number.parseInt(expiresDays, 10);
      const expiresInMs = Number.isFinite(days) && days > 0 ? days * 24 * 60 * 60 * 1000 : undefined;
      const out = await agentClient.apiKeyCreate({ label: label.trim(), scopes, expiresInMs });
      setFreshToken({ label: out.label, token: out.token });
      setLabel("");
      await load();
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    } finally {
      setCreating(false);
    }
  }

  async function disable(record: APIKeyRecord) {
    if (!window.confirm(`Disable API key "${record.label}"? It can be re-enabled via CLI.`)) return;
    try {
      await agentClient.apiKeyDisable(record.label || record.tokenHash.slice(0, 8));
      await load();
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    }
  }

  async function copy(text: string) {
    try {
      await navigator.clipboard.writeText(text);
    } catch {
      // ignore
    }
  }

  function toggleScope(s: string) {
    setScopes((prev) => (prev.includes(s) ? prev.filter((x) => x !== s) : [...prev, s]));
  }

  return (
    <div className="flex h-full flex-col gap-4 overflow-y-auto p-4 text-surface-100">
      <header>
        <h2 className="text-lg font-semibold">API Keys</h2>
        <p className="text-xs text-surface-400">
          Labeled SDK tokens with scope restriction, IP binding (via CLI), and usage tracking. Used by the Feedback SDK and third-party apps.
        </p>
      </header>

      {err && (
        <div className="rounded border border-red-500/40 bg-red-950/30 px-3 py-2 text-sm text-red-200" role="alert">
          {err}
        </div>
      )}

      {freshToken && (
        <div className="rounded border border-amber-500/40 bg-amber-950/30 p-3 text-sm">
          <p className="font-semibold text-amber-200">
            Copy {freshToken.label} — it will not be shown again.
          </p>
          <pre className="mt-2 overflow-x-auto rounded bg-black/40 p-2 font-mono text-xs">{freshToken.token}</pre>
          <div className="mt-2 flex gap-2">
            <button
              type="button"
              className="rounded bg-indigo-600 px-3 py-1 text-xs font-semibold"
              onClick={() => void copy(freshToken.token)}
            >
              Copy
            </button>
            <button
              type="button"
              className="rounded bg-surface-800 px-3 py-1 text-xs"
              onClick={() => setFreshToken(null)}
            >
              Dismiss
            </button>
          </div>
        </div>
      )}

      <section className="rounded border border-surface-700 bg-surface-950/30 p-3">
        <h3 className="text-xs font-semibold uppercase tracking-wider text-surface-400">Create key</h3>
        <div className="mt-2 grid gap-2 md:grid-cols-2">
          <input
            className="rounded border border-surface-700 bg-surface-900 px-2 py-1.5 text-sm"
            placeholder="label (e.g. BentoApp prod)"
            value={label}
            onChange={(e) => setLabel(e.target.value)}
          />
          <input
            className="rounded border border-surface-700 bg-surface-900 px-2 py-1.5 text-sm"
            placeholder="expires in days (default 365)"
            type="number"
            min="1"
            value={expiresDays}
            onChange={(e) => setExpiresDays(e.target.value)}
          />
        </div>
        <div className="mt-2 flex flex-wrap gap-1.5">
          {SCOPES.map((s) => (
            <button
              key={s}
              type="button"
              onClick={() => toggleScope(s)}
              className={`rounded border px-2 py-0.5 text-xs ${
                scopes.includes(s)
                  ? "border-indigo-500 bg-indigo-900/40 text-indigo-200"
                  : "border-surface-700 bg-surface-900 text-surface-400 hover:text-surface-200"
              }`}
            >
              {s}
            </button>
          ))}
        </div>
        <div className="mt-3 flex items-center justify-between">
          <span className="text-xs text-surface-500">
            Scopes narrow what the key can hit. All requests are still signed with this agent's fingerprint.
          </span>
          <button
            type="button"
            className="rounded bg-indigo-600 px-4 py-1.5 text-sm font-semibold disabled:opacity-40"
            disabled={creating || !label.trim() || scopes.length === 0}
            onClick={() => void createKey()}
          >
            {creating ? "Creating…" : "Create"}
          </button>
        </div>
      </section>

      <section className="rounded border border-surface-700">
        <div className="flex items-center justify-between border-b border-surface-700 px-3 py-2">
          <h3 className="text-xs font-semibold uppercase tracking-wider text-surface-400">
            Keys {keys.length > 0 && `(${keys.length})`}
          </h3>
          <button type="button" className="text-xs text-surface-400 hover:text-surface-100" onClick={() => void load()}>
            Refresh
          </button>
        </div>
        {loading && <p className="p-3 text-sm text-surface-400">Loading…</p>}
        {!loading && keys.length === 0 && <p className="p-3 text-sm text-surface-500">No keys yet.</p>}
        <ul className="divide-y divide-surface-800">
          {keys.map((k) => (
            <li key={k.tokenHash} className="flex flex-col gap-1 px-3 py-2">
              <div className="flex items-center gap-2">
                <span className="font-mono text-sm">{k.label || "(unlabeled)"}</span>
                <span className="rounded bg-surface-800 px-1.5 py-0.5 font-mono text-[10px] text-surface-400">
                  {k.tokenHash.slice(0, 8)}
                </span>
                {k.disabled && (
                  <span className="rounded bg-red-900/40 px-1.5 py-0.5 text-[10px] text-red-200">disabled</span>
                )}
                <span className="ml-auto flex gap-1 text-xs">
                  <button
                    type="button"
                    className="rounded bg-red-900/40 px-2 py-0.5 text-red-200 hover:bg-red-900/70 disabled:opacity-40"
                    disabled={k.disabled}
                    onClick={() => void disable(k)}
                  >
                    Disable
                  </button>
                </span>
              </div>
              <div className="flex flex-wrap gap-2 text-[11px] text-surface-400">
                {k.scopes && k.scopes.length > 0 && (
                  <span>
                    scopes: <code>{k.scopes.join(", ")}</code>
                  </span>
                )}
                <span>usage: {k.usageCount ?? 0}</span>
                {k.lastUsedAt && <span>last: {k.lastUsedAt}</span>}
                {k.createdAt && <span>created: {k.createdAt}</span>}
              </div>
            </li>
          ))}
        </ul>
      </section>
    </div>
  );
}
