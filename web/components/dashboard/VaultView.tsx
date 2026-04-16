"use client";

// VaultView — UI for the encrypted-on-host vault (desktop/agent/vault.go).
//
// All data stays on the user's machine. This component only ever holds
// a revealed value in React state for the lifetime of a single reveal;
// there is no localStorage, no sessionStorage, and no caching of the
// secret on the web worker. Closing the tab wipes every exposure.

import { useCallback, useEffect, useState } from "react";
import { agentClient, type VaultEntrySummary, type VaultEntry, type VaultCategory } from "@/lib/agent-client";

const CATEGORIES: VaultCategory[] = ["api-key", "signing-key", "ssh-key", "git-credential", "custom"];

function categoryColor(c: VaultCategory): string {
  switch (c) {
    case "api-key":
      return "bg-indigo-900/40 text-indigo-200 border-indigo-700";
    case "signing-key":
      return "bg-amber-900/40 text-amber-200 border-amber-700";
    case "ssh-key":
      return "bg-emerald-900/40 text-emerald-200 border-emerald-700";
    case "git-credential":
      return "bg-sky-900/40 text-sky-200 border-sky-700";
    default:
      return "bg-surface-800 text-surface-300 border-surface-700";
  }
}

export default function VaultView() {
  const [entries, setEntries] = useState<VaultEntrySummary[]>([]);
  const [loading, setLoading] = useState(true);
  const [err, setErr] = useState<string | null>(null);
  const [revealed, setRevealed] = useState<Record<string, string>>({});
  const [revealing, setRevealing] = useState<string | null>(null);
  const [filter, setFilter] = useState<Set<VaultCategory>>(new Set(CATEGORIES));
  const [q, setQ] = useState("");

  // Compose form
  const [draftName, setDraftName] = useState("");
  const [draftCategory, setDraftCategory] = useState<VaultCategory>("api-key");
  const [draftValue, setDraftValue] = useState("");
  const [draftNotes, setDraftNotes] = useState("");
  const [saving, setSaving] = useState(false);

  const load = useCallback(async () => {
    try {
      setErr(null);
      const rows = await agentClient.vaultList();
      setEntries(rows.sort((a, b) => a.name.localeCompare(b.name)));
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  async function reveal(name: string) {
    if (revealed[name]) {
      // Hide again — the reveal toggle also clears state to reduce
      // window-of-exposure time.
      setRevealed((prev) => {
        const next = { ...prev };
        delete next[name];
        return next;
      });
      return;
    }
    setRevealing(name);
    try {
      const entry: VaultEntry = await agentClient.vaultGet(name);
      setRevealed((prev) => ({ ...prev, [name]: entry.value }));
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    } finally {
      setRevealing(null);
    }
  }

  async function remove(name: string) {
    if (!window.confirm(`Delete vault entry "${name}"? This cannot be undone.`)) return;
    try {
      await agentClient.vaultDelete(name);
      setRevealed((prev) => {
        const next = { ...prev };
        delete next[name];
        return next;
      });
      await load();
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    }
  }

  async function save() {
    if (!draftName.trim() || !draftValue.trim()) return;
    setSaving(true);
    try {
      await agentClient.vaultSet({
        name: draftName.trim(),
        category: draftCategory,
        value: draftValue,
        notes: draftNotes.trim() || undefined,
      });
      setDraftName("");
      setDraftValue("");
      setDraftNotes("");
      await load();
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    } finally {
      setSaving(false);
    }
  }

  async function copyToClipboard(text: string) {
    try {
      await navigator.clipboard.writeText(text);
    } catch {
      // silent — some browsers block in non-secure contexts
    }
  }

  return (
    <div className="flex h-full flex-col gap-4 overflow-y-auto p-4 text-surface-100">
      <header>
        <h2 className="text-lg font-semibold">Vault</h2>
        <p className="text-xs text-surface-400">
          Encrypted at rest on your machine (<code className="rounded bg-surface-900 px-1">~/.yaver/vault.enc</code>, AES-GCM + Argon2id). Nothing touches Convex.
        </p>
      </header>

      {err && (
        <div className="rounded border border-red-500/40 bg-red-950/30 px-3 py-2 text-sm text-red-200" role="alert">
          {err}
        </div>
      )}

      <section className="rounded border border-surface-700 bg-surface-950/30 p-3">
        <h3 className="text-xs font-semibold uppercase tracking-wider text-surface-400">Add entry</h3>
        <div className="mt-2 grid gap-2 md:grid-cols-2">
          <input
            className="rounded border border-surface-700 bg-surface-900 px-2 py-1.5 font-mono text-sm"
            placeholder="name (e.g. OPENAI_API_KEY)"
            value={draftName}
            onChange={(e) => setDraftName(e.target.value)}
          />
          <select
            className="rounded border border-surface-700 bg-surface-900 px-2 py-1.5 text-sm"
            value={draftCategory}
            onChange={(e) => setDraftCategory(e.target.value as VaultCategory)}
          >
            {CATEGORIES.map((c) => (
              <option key={c} value={c}>
                {c}
              </option>
            ))}
          </select>
          <input
            className="rounded border border-surface-700 bg-surface-900 px-2 py-1.5 font-mono text-sm md:col-span-2"
            placeholder="value (not echoed)"
            type="password"
            value={draftValue}
            onChange={(e) => setDraftValue(e.target.value)}
            autoComplete="new-password"
          />
          <input
            className="rounded border border-surface-700 bg-surface-900 px-2 py-1.5 text-sm md:col-span-2"
            placeholder="notes (optional)"
            value={draftNotes}
            onChange={(e) => setDraftNotes(e.target.value)}
          />
        </div>
        <div className="mt-3 flex justify-end">
          <button
            type="button"
            className="rounded bg-indigo-600 px-4 py-1.5 text-sm font-semibold disabled:opacity-40"
            disabled={saving || !draftName.trim() || !draftValue.trim()}
            onClick={() => void save()}
          >
            {saving ? "Saving…" : "Save"}
          </button>
        </div>
      </section>

      <section className="rounded border border-surface-700">
        <div className="flex flex-wrap items-center gap-2 border-b border-surface-700 px-3 py-2">
          <h3 className="text-xs font-semibold uppercase tracking-wider text-surface-400">
            Entries {entries.length > 0 && `(${entries.length})`}
          </h3>
          <input
            className="ml-2 rounded border border-surface-700 bg-surface-900 px-2 py-1 text-xs"
            placeholder="filter by name…"
            value={q}
            onChange={(e) => setQ(e.target.value)}
          />
          <div className="flex flex-wrap gap-1">
            {CATEGORIES.map((cat) => (
              <button
                key={cat}
                type="button"
                className={`rounded border px-2 py-0.5 text-[10px] ${
                  filter.has(cat) ? categoryColor(cat) : "border-surface-800 bg-surface-900 text-surface-500"
                }`}
                onClick={() => {
                  setFilter((prev) => {
                    const next = new Set(prev);
                    if (next.has(cat)) next.delete(cat);
                    else next.add(cat);
                    if (next.size === 0) return new Set(CATEGORIES);
                    return next;
                  });
                }}
                title={filter.has(cat) ? `Hide ${cat}` : `Show ${cat}`}
              >
                {cat}
              </button>
            ))}
          </div>
          <button
            type="button"
            className="ml-auto text-xs text-surface-400 hover:text-surface-100"
            onClick={() => void load()}
          >
            Refresh
          </button>
        </div>
        {loading && <p className="p-3 text-sm text-surface-400">Loading…</p>}
        {!loading && entries.length === 0 && (
          <p className="p-3 text-sm text-surface-500">No entries yet.</p>
        )}
        <ul className="divide-y divide-surface-800">
          {entries
            .filter((e) => filter.has(e.category))
            .filter((e) =>
              q.trim() === ""
                ? true
                : e.name.toLowerCase().includes(q.trim().toLowerCase()) ||
                  (e.notes ?? "").toLowerCase().includes(q.trim().toLowerCase()),
            )
            .map((e) => {
            const value = revealed[e.name];
            return (
              <li key={e.name} className="flex flex-col gap-1 px-3 py-2">
                <div className="flex items-center gap-2">
                  <span className="font-mono text-sm">{e.name}</span>
                  <span className={`rounded border px-1.5 py-0.5 text-[10px] ${categoryColor(e.category)}`}>
                    {e.category}
                  </span>
                  <span className="ml-auto flex gap-1 text-xs">
                    <button
                      type="button"
                      className="rounded bg-surface-800 px-2 py-0.5 hover:bg-surface-700"
                      onClick={() => void reveal(e.name)}
                      disabled={revealing === e.name}
                    >
                      {value ? "Hide" : revealing === e.name ? "…" : "Reveal"}
                    </button>
                    {value && (
                      <button
                        type="button"
                        className="rounded bg-surface-800 px-2 py-0.5 hover:bg-surface-700"
                        onClick={() => void copyToClipboard(value)}
                      >
                        Copy
                      </button>
                    )}
                    <button
                      type="button"
                      className="rounded bg-red-900/40 px-2 py-0.5 text-red-200 hover:bg-red-900/70"
                      onClick={() => void remove(e.name)}
                    >
                      Delete
                    </button>
                  </span>
                </div>
                {e.notes && <p className="text-xs text-surface-400">{e.notes}</p>}
                {value && (
                  <pre className="mt-1 overflow-x-auto rounded bg-black/40 p-2 font-mono text-xs">
                    {value}
                  </pre>
                )}
              </li>
            );
          })}
        </ul>
      </section>
    </div>
  );
}
