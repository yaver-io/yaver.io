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
const PRESETS = [
  {
    id: "remote-box",
    label: "Remote Box",
    description: "Machine-to-machine onboarding and remote agent access.",
    scopes: ["health", "todolist", "builds"],
  },
  {
    id: "sdk",
    label: "Feedback SDK",
    description: "Embed in an app and keep it narrowed to feedback endpoints.",
    scopes: ["feedback", "blackbox", "voice"],
  },
  {
    id: "automation",
    label: "Automation",
    description: "CI or scripts that need broader project automation access.",
    scopes: ["feedback", "blackbox", "voice", "builds", "health", "todolist"],
  },
] as const;

function formatExpiry(days: string) {
  const parsed = Number.parseInt(days, 10);
  if (!Number.isFinite(parsed) || parsed <= 0) return "Does not expire";
  const expiresAt = new Date(Date.now() + parsed * 24 * 60 * 60 * 1000);
  return expiresAt.toLocaleDateString(undefined, { dateStyle: "full" });
}

export default function APIKeysView() {
  const [keys, setKeys] = useState<APIKeyRecord[]>([]);
  const [loading, setLoading] = useState(true);
  const [err, setErr] = useState<string | null>(null);
  const [freshToken, setFreshToken] = useState<{ label: string; token: string } | null>(null);

  // New key form
  const [preset, setPreset] = useState<(typeof PRESETS)[number]["id"]>("remote-box");
  const [label, setLabel] = useState("");
  const [description, setDescription] = useState("");
  const [scopes, setScopes] = useState<string[]>(["health", "todolist", "builds"]);
  const [expiresDays, setExpiresDays] = useState<string>("365");
  const [allowedCIDRs, setAllowedCIDRs] = useState("");
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
      const normalizedCidrs = allowedCIDRs
        .split(/[\n,]/)
        .map((v) => v.trim())
        .filter(Boolean);
      const finalLabel = description.trim() ? `${label.trim()} — ${description.trim()}`.slice(0, 80) : label.trim();
      const out = await agentClient.apiKeyCreate({
        label: finalLabel,
        scopes,
        expiresInMs,
        allowedCIDRs: normalizedCidrs,
      });
      setFreshToken({ label: out.label, token: out.token });
      setLabel("");
      setDescription("");
      setAllowedCIDRs("");
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

  function applyPreset(nextPreset: (typeof PRESETS)[number]) {
    setPreset(nextPreset.id);
    setScopes([...nextPreset.scopes]);
  }

  return (
    <div className="flex h-full flex-col gap-4 overflow-y-auto p-4 text-surface-100">
      <header>
        <h2 className="text-lg font-semibold">Yaver Tokens</h2>
        <p className="text-xs text-surface-400">
          npm-style token generation for remote boxes, CI, and app SDKs. Use guest invites for people; use Yaver tokens for machine-to-machine access.
        </p>
      </header>

      <section className="rounded border border-indigo-500/30 bg-indigo-500/5 p-3">
        <div className="flex flex-wrap items-center gap-2">
          <span className="rounded border border-indigo-500/30 bg-indigo-500/10 px-2 py-0.5 text-[10px] font-semibold uppercase tracking-wider text-indigo-700 dark:text-indigo-300">
            Audit
          </span>
          <p className="text-sm text-surface-200">
            Yaver is not the same as npm here. npm granular tokens stop at package and org permissions; Yaver also has guest identity, invite codes, per-machine scope, and live runtime policy.
          </p>
        </div>
      </section>

      {err && (
        <div className="rounded border border-red-500/40 bg-red-950/30 px-3 py-2 text-sm text-red-700 dark:text-red-200" role="alert">
          {err}
        </div>
      )}

      {freshToken && (
        <div className="rounded border border-amber-500/40 bg-amber-950/30 p-3 text-sm">
          <p className="font-semibold text-amber-700 dark:text-amber-200">
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
        <div className="flex items-center justify-between gap-3">
          <h3 className="text-xs font-semibold uppercase tracking-wider text-surface-400">Generate token</h3>
          <div className="text-[11px] text-surface-500">Raw secret is shown once, then only the hash remains.</div>
        </div>

        <div className="mt-3 grid gap-2 lg:grid-cols-3">
          {PRESETS.map((option) => (
            <button
              key={option.id}
              type="button"
              onClick={() => applyPreset(option)}
              className={`border px-3 py-3 text-left text-sm ${
                preset === option.id
                  ? "border-indigo-500 bg-indigo-500/10 text-surface-50"
                  : "border-surface-700 bg-surface-900 text-surface-300 hover:border-surface-600"
              }`}
            >
              <div className="font-semibold">{option.label}</div>
              <div className="mt-1 text-xs text-surface-400">{option.description}</div>
            </button>
          ))}
        </div>

        <div className="mt-3 grid gap-2 md:grid-cols-2">
          <input
            className="rounded border border-surface-700 bg-surface-900 px-2 py-1.5 text-sm"
            placeholder="Token name"
            value={label}
            onChange={(e) => setLabel(e.target.value)}
            maxLength={80}
          />
          <input
            className="rounded border border-surface-700 bg-surface-900 px-2 py-1.5 text-sm"
            placeholder="Description"
            value={description}
            onChange={(e) => setDescription(e.target.value)}
            maxLength={120}
          />
          <input
            className="rounded border border-surface-700 bg-surface-900 px-2 py-1.5 text-sm"
            placeholder="Expiration in days"
            type="number"
            min="0"
            value={expiresDays}
            onChange={(e) => setExpiresDays(e.target.value)}
          />
          <textarea
            className="min-h-20 rounded border border-surface-700 bg-surface-900 px-2 py-1.5 text-sm font-mono"
            placeholder="Allowed IP ranges, one CIDR per line"
            value={allowedCIDRs}
            onChange={(e) => setAllowedCIDRs(e.target.value)}
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
                  ? "border-indigo-500 bg-indigo-900/40 text-indigo-700 dark:text-indigo-200"
                  : "border-surface-700 bg-surface-900 text-surface-400 hover:text-surface-200"
              }`}
            >
              {s}
            </button>
          ))}
        </div>
        <div className="mt-3 grid gap-2 border border-surface-800 bg-surface-900/50 p-3 text-xs text-surface-400 md:grid-cols-2">
          <div>
            <div className="text-[10px] font-semibold uppercase tracking-wider text-surface-500">Summary</div>
            <ul className="mt-2 space-y-1">
              <li>Name: <span className="text-surface-200">{label.trim() || "Untitled token"}</span></li>
              <li>Scopes: <span className="text-surface-200">{scopes.join(", ") || "none"}</span></li>
              <li>Expiration: <span className="text-surface-200">{formatExpiry(expiresDays)}</span></li>
            </ul>
          </div>
          <div>
            <div className="text-[10px] font-semibold uppercase tracking-wider text-surface-500">This token will</div>
            <ul className="mt-2 space-y-1">
              <li>Work for automation and remote boxes.</li>
              <li>Be limited to the selected scopes.</li>
              <li>{allowedCIDRs.trim() ? "Accept only the listed CIDR ranges." : "Accept from any IP range."}</li>
            </ul>
          </div>
        </div>
        <div className="mt-3 flex items-center justify-between gap-3">
          <span className="text-xs text-surface-500">
            Use guest invites for human access. Tokens are better for agents, remote boxes, and CI.
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
            Active tokens {keys.length > 0 && `(${keys.length})`}
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
                  <span className="rounded bg-red-900/40 px-1.5 py-0.5 text-[10px] text-red-700 dark:text-red-200">disabled</span>
                )}
                <span className="ml-auto flex gap-1 text-xs">
                  <button
                    type="button"
                    className="rounded bg-red-900/40 px-2 py-0.5 text-red-700 dark:text-red-200 hover:bg-red-900/70 disabled:opacity-40"
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
                {k.lastUsedAt && <span>last: {new Date(k.lastUsedAt).toLocaleString()}</span>}
                {k.createdAt && <span>created: {new Date(k.createdAt).toLocaleString()}</span>}
              </div>
            </li>
          ))}
        </ul>
      </section>
    </div>
  );
}
