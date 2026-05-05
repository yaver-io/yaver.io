"use client";

// VaultView — UI for the encrypted-on-host vault (desktop/agent/vault.go).
//
// All data stays on the user's machine. This component only ever holds
// a revealed value in React state for the lifetime of a single reveal;
// there is no localStorage, no sessionStorage, and no caching of the
// secret on the web worker. Closing the tab wipes every exposure.

import { useCallback, useEffect, useState } from "react";
import { agentClient, type VaultEntrySummary, type VaultEntry, type VaultCategory } from "@/lib/agent-client";
import { EmptyState, Button } from "@/components/ui";

const CATEGORIES: VaultCategory[] = ["api-key", "signing-key", "ssh-key", "git-credential", "custom"];

// Per-entry category badge — single neutral style. The category text itself
// is the differentiator; we don't decorate-color types. Filter pills are a
// separate decision (brand-soft when selected, neutral otherwise).
const CATEGORY_BADGE = "bg-surface-800/60 text-surface-300 border-surface-700/60";

interface VaultViewProps {
  /** True when the connected device is in needsAuth state. Lets us
   *  swap the raw "HTTP 503" / "HTTP 401" error for a re-auth CTA. */
  needsAuth?: boolean;
  /** Called when the user clicks "Reconnect" on the re-auth banner.
   *  Same handler the dashboard uses for the device cards. */
  onReconnect?: () => Promise<void>;
}

export default function VaultView({ needsAuth, onReconnect }: VaultViewProps = {}) {
  const [entries, setEntries] = useState<VaultEntrySummary[]>([]);
  const [loading, setLoading] = useState(true);
  const [err, setErr] = useState<string | null>(null);
  const [revealed, setRevealed] = useState<Record<string, string>>({});
  const [revealing, setRevealing] = useState<string | null>(null);
  const [filter, setFilter] = useState<Set<VaultCategory>>(new Set(CATEGORIES));
  const [q, setQ] = useState("");
  const [masks, setMasks] = useState<Record<string, string>>({});

  // Compose form
  const [draftName, setDraftName] = useState("");
  const [draftCategory, setDraftCategory] = useState<VaultCategory>("api-key");
  const [draftValue, setDraftValue] = useState("");
  const [draftNotes, setDraftNotes] = useState("");
  const [saving, setSaving] = useState(false);
  // When set, the add form is in "edit mode": name is locked, value
  // is pre-loaded, and Save becomes Update.
  const [editingName, setEditingName] = useState<string | null>(null);

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
      setEditingName(null);
      await load();
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    } finally {
      setSaving(false);
    }
  }

  async function beginEdit(name: string) {
    try {
      const entry = await agentClient.vaultGet(name);
      setEditingName(entry.name);
      setDraftName(entry.name);
      setDraftCategory(entry.category);
      setDraftValue(entry.value);
      setDraftNotes(entry.notes ?? "");
      // Scroll-to-top isn't available in a server-rendered context;
      // the form is at the top so it's already visible.
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    }
  }

  function cancelEdit() {
    setEditingName(null);
    setDraftName("");
    setDraftValue("");
    setDraftNotes("");
  }

  // .env import state
  const [importOpen, setImportOpen] = useState(false);
  const [importText, setImportText] = useState("");
  const [importCategory, setImportCategory] = useState<VaultCategory>("api-key");
  const [importing, setImporting] = useState(false);

  // parseEnv tolerates most .env flavours:
  //   - `#` comments (line and trailing)
  //   - leading `export `
  //   - `KEY=VAL`, `KEY="VAL"`, `KEY='VAL'`
  //   - blank lines
  // Returns {name, value} pairs in document order. Duplicates win-last.
  function parseEnv(text: string): { name: string; value: string }[] {
    const out = new Map<string, string>();
    for (const rawLine of text.split(/\r?\n/)) {
      let line = rawLine.trim();
      if (!line || line.startsWith("#")) continue;
      if (line.startsWith("export ")) line = line.slice("export ".length).trim();
      const eq = line.indexOf("=");
      if (eq < 1) continue; // no `=` or line starts with `=`
      let name = line.slice(0, eq).trim();
      let value = line.slice(eq + 1).trim();
      // Strip matching outer quotes.
      if (
        (value.startsWith('"') && value.endsWith('"') && value.length >= 2) ||
        (value.startsWith("'") && value.endsWith("'") && value.length >= 2)
      ) {
        value = value.slice(1, -1);
      }
      if (!/^[A-Za-z_][A-Za-z0-9_.-]*$/.test(name)) continue;
      out.set(name, value);
    }
    return Array.from(out.entries()).map(([name, value]) => ({ name, value }));
  }

  const importPreview = importText.trim() ? parseEnv(importText) : [];

  async function runImport() {
    if (importPreview.length === 0) return;
    setImporting(true);
    let ok = 0;
    let failed: string[] = [];
    for (const pair of importPreview) {
      try {
        await agentClient.vaultSet({
          name: pair.name,
          category: importCategory,
          value: pair.value,
          notes: "imported from .env",
        });
        ok += 1;
      } catch {
        failed.push(pair.name);
      }
    }
    setImporting(false);
    if (failed.length === 0) {
      setImportText("");
      setImportOpen(false);
    } else {
      setErr(`imported ${ok}/${importPreview.length} — failed: ${failed.slice(0, 5).join(", ")}${failed.length > 5 ? "…" : ""}`);
    }
    await load();
  }

  async function copyToClipboard(text: string) {
    try {
      await navigator.clipboard.writeText(text);
    } catch {
      // silent — some browsers block in non-secure contexts
    }
  }

  // Fetches the value, copies it, and throws the plaintext away —
  // never renders it. Useful when the user just wants to paste
  // into another app without leaving the secret on screen.
  async function quickCopy(name: string) {
    try {
      const entry: VaultEntry = await agentClient.vaultGet(name);
      await copyToClipboard(entry.value);
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    }
  }

  // "sk-xxxx…abcd" — enough to recognise which key it is without
  // revealing it. Used as an inline preview when the entry is hidden.
  async function toggleMask(name: string) {
    if (masks[name]) {
      setMasks((prev) => {
        const next = { ...prev };
        delete next[name];
        return next;
      });
      return;
    }
    try {
      const entry: VaultEntry = await agentClient.vaultGet(name);
      const v = entry.value;
      const hint =
        v.length <= 10
          ? v.slice(0, 1) + "…" + v.slice(-1)
          : v.slice(0, 4) + "…" + v.slice(-4);
      setMasks((prev) => ({ ...prev, [name]: hint }));
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
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

      {err && (() => {
        const looksLikeAuthFailure = needsAuth ||
          /HTTP 5(03|02)|HTTP 401|HTTP 403|needs auth|expired|unauthor/i.test(err);
        if (looksLikeAuthFailure) {
          return (
            <div className="flex items-start gap-3 rounded-lg border border-warning/40 bg-warning-soft/40 px-3 py-2.5 text-sm text-warning-softFg" role="alert">
              <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" className="mt-0.5 shrink-0 text-warning">
                <rect x="3" y="11" width="18" height="11" rx="2" ry="2" />
                <path d="M7 11V7a5 5 0 0 1 10 0v4" />
              </svg>
              <div className="flex-1 min-w-0">
                <p className="font-medium">Vault locked: agent session expired on this machine</p>
                <p className="mt-0.5 text-[12px] opacity-80">
                  Vault stays sealed until the host signs back in. Run <code className="rounded bg-surface-800 px-1 py-px font-mono text-[11px]">yaver auth</code> on the host, or click Reconnect.
                </p>
              </div>
              <div className="flex shrink-0 gap-1.5 self-start">
                {onReconnect && (
                  <Button variant="primary" size="sm" onClick={() => { void onReconnect(); }}>
                    Reconnect
                  </Button>
                )}
                <Button variant="ghost" size="sm" onClick={() => void load()} title="Retry the vault list call">
                  Retry
                </Button>
              </div>
            </div>
          );
        }
        return (
          <div className="flex items-center gap-2 rounded-lg border border-danger/40 bg-danger-soft/40 px-3 py-2 text-sm text-danger-softFg" role="alert">
            <span className="flex-1">{err}</span>
            <Button variant="ghost" size="sm" onClick={() => void load()}>
              Retry
            </Button>
          </div>
        );
      })()}

      <section className="rounded border border-surface-700 bg-surface-950/30 p-3">
        <div className="flex items-center gap-2">
          <h3 className="text-xs font-semibold uppercase tracking-wider text-surface-400">
            {editingName ? `Edit entry: ${editingName}` : "Add entry"}
          </h3>
          {!editingName && (
            <button
              type="button"
              className="ml-auto inline-flex items-center gap-1.5 rounded-md bg-info-soft text-info-softFg px-2.5 py-1 text-xs font-medium hover:bg-info/15 transition-colors"
              onClick={() => setImportOpen((v) => !v)}
            >
              <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={1.8} strokeLinecap="round" strokeLinejoin="round" className="h-3.5 w-3.5" aria-hidden>
                <path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4" />
                <polyline points="17 8 12 3 7 8" />
                <line x1="12" y1="3" x2="12" y2="15" />
              </svg>
              {importOpen ? "Close" : "Import from .env"}
            </button>
          )}
        </div>
        {importOpen && !editingName && (
          <div className="mt-2 rounded border border-surface-800 bg-surface-950 p-2">
            <p className="text-[11px] text-surface-500">
              Paste a <code className="rounded bg-surface-900 px-1">.env</code> file. KEY=VALUE lines are parsed; comments and blanks are skipped. Values land in your local vault — nothing leaves this machine.
            </p>
            <textarea
              className="mt-2 w-full rounded border border-surface-700 bg-surface-900 px-2 py-1.5 font-mono text-xs"
              rows={6}
              placeholder={`OPENAI_API_KEY=sk-...\nSTRIPE_SECRET=sk_live_...`}
              value={importText}
              onChange={(e) => setImportText(e.target.value)}
            />
            <div className="mt-2 flex items-center gap-2 text-xs">
              <select
                className="rounded border border-surface-700 bg-surface-900 px-2 py-1 text-xs"
                value={importCategory}
                onChange={(e) => setImportCategory(e.target.value as VaultCategory)}
              >
                {CATEGORIES.map((c) => (
                  <option key={c} value={c}>
                    {c}
                  </option>
                ))}
              </select>
              <span className="text-surface-500">
                {importPreview.length} entr{importPreview.length === 1 ? "y" : "ies"} parsed
              </span>
              <button
                type="button"
                className="ml-auto rounded bg-indigo-600 px-3 py-1 text-xs font-semibold disabled:opacity-40"
                disabled={importing || importPreview.length === 0}
                onClick={() => void runImport()}
              >
                {importing ? "Importing…" : "Import all"}
              </button>
            </div>
            {importPreview.length > 0 && (
              <ul className="mt-2 max-h-40 overflow-auto text-[11px] text-surface-400">
                {importPreview.map((p) => (
                  <li key={p.name} className="flex gap-2">
                    <code className="text-surface-200">{p.name}</code>
                    <span className="truncate">
                      =
                      {p.value.length > 20
                        ? `${p.value.slice(0, 8)}…${p.value.slice(-4)}`
                        : p.value}
                    </span>
                  </li>
                ))}
              </ul>
            )}
          </div>
        )}
        <div className="mt-2 grid gap-2 md:grid-cols-2">
          <input
            className="rounded border border-surface-700 bg-surface-900 px-2 py-1.5 font-mono text-sm disabled:opacity-60"
            placeholder="name (e.g. OPENAI_API_KEY)"
            value={draftName}
            onChange={(e) => setDraftName(e.target.value)}
            disabled={editingName !== null}
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
        <div className="mt-3 flex justify-end gap-2">
          {editingName && (
            <button
              type="button"
              className="rounded bg-surface-800 px-3 py-1.5 text-sm"
              onClick={cancelEdit}
            >
              Cancel
            </button>
          )}
          <Button
            variant="primary"
            size="md"
            disabled={saving || !draftName.trim() || !draftValue.trim()}
            onClick={() => void save()}
          >
            {saving ? (editingName ? "Updating…" : "Saving…") : editingName ? "Update" : "Save"}
          </Button>
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
                className={`rounded border px-2 py-0.5 text-[10px] transition-colors ${
                  filter.has(cat)
                    ? "border-brand/30 bg-brand-soft text-brand-softFg"
                    : "border-surface-800 bg-surface-900 text-surface-500 hover:border-surface-700 hover:text-surface-300"
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
          <EmptyState
            compact
            icon={
              <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={1.6} strokeLinecap="round" strokeLinejoin="round" aria-hidden>
                <rect x="3" y="11" width="18" height="11" rx="2" ry="2" />
                <path d="M7 11V7a5 5 0 0 1 10 0v4" />
              </svg>
            }
            title="No entries yet"
            description="Add your first secret to start using the vault."
          />
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
                  <span className={`rounded border px-1.5 py-0.5 text-[10px] ${CATEGORY_BADGE}`}>
                    {e.category}
                  </span>
                  <span className="ml-auto flex gap-1 text-xs">
                    <button
                      type="button"
                      className="rounded bg-surface-800 px-2 py-0.5 hover:bg-surface-700"
                      onClick={() => void quickCopy(e.name)}
                      title="Copy value without displaying it"
                    >
                      Copy
                    </button>
                    <button
                      type="button"
                      className="rounded bg-surface-800 px-2 py-0.5 hover:bg-surface-700"
                      onClick={() => void toggleMask(e.name)}
                      title="Show a masked preview (first+last 4 chars)"
                    >
                      {masks[e.name] ? "Hide" : "Preview"}
                    </button>
                    <button
                      type="button"
                      className="rounded bg-surface-800 px-2 py-0.5 hover:bg-surface-700"
                      onClick={() => void reveal(e.name)}
                      disabled={revealing === e.name}
                    >
                      {value ? "Hide" : revealing === e.name ? "…" : "Reveal"}
                    </button>
                    <button
                      type="button"
                      className="rounded bg-surface-800 px-2 py-0.5 hover:bg-surface-700"
                      onClick={() => void beginEdit(e.name)}
                      title="Edit value / notes (name stays fixed)"
                    >
                      Edit
                    </button>
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
                {!value && masks[e.name] && (
                  <code className="mt-1 inline-block rounded bg-black/30 px-1 text-xs text-surface-300">
                    {masks[e.name]}
                  </code>
                )}
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
