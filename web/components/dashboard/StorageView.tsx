"use client";

// StorageView — unified browser for the user's own storage:
//
//  - Files: read-only project tree (/files/*)
//  - Shared: configured NAS/SMB/S3/Azure profiles (/shared-storage/*)
//  - Blobs: simple key-value bucket store (/blobs/*)
//
// All three live on the user's machine; Convex never sees any file
// bytes or keys. The view is deliberately thin — a deeper file
// manager with write/upload exists on mobile's files.tsx; here we
// give the owner a quick "what's on my box?" surface to confirm
// the agent is healthy and the profiles are mounted correctly.

import { useCallback, useEffect, useState } from "react";
import { agentClient } from "@/lib/agent-client";

type Tab = "files" | "shared" | "blobs";

type FileRoot = { id: string; name: string; path: string };
type FileEntry = { name: string; path: string; isDir?: boolean; size?: number };

type StorageProfile = { id: string; label?: string; type?: string; root?: string };

export default function StorageView() {
  const [tab, setTab] = useState<Tab>("files");
  return (
    <div className="flex h-full flex-col gap-3 overflow-hidden p-4 text-surface-100">
      <header className="flex items-center gap-3">
        <h2 className="text-lg font-semibold">Storage</h2>
        <nav className="ml-auto flex gap-1 text-xs">
          {(["files", "shared", "blobs"] as Tab[]).map((t) => (
            <button
              key={t}
              type="button"
              onClick={() => setTab(t)}
              className={`rounded px-2 py-1 ${
                tab === t ? "bg-indigo-900/60 text-indigo-100" : "bg-surface-900 text-surface-400 hover:text-surface-100"
              }`}
            >
              {t === "files" ? "Project files" : t === "shared" ? "Shared (NAS)" : "Blobs"}
            </button>
          ))}
        </nav>
      </header>
      {tab === "files" && <FilesTab />}
      {tab === "shared" && <SharedTab />}
      {tab === "blobs" && <BlobsTab />}
    </div>
  );
}

// ── Files ────────────────────────────────────────────────────────────

function FilesTab() {
  const [roots, setRoots] = useState<FileRoot[]>([]);
  const [activeRoot, setActiveRoot] = useState<string | null>(null);
  const [path, setPath] = useState("");
  const [entries, setEntries] = useState<FileEntry[]>([]);
  const [preview, setPreview] = useState<string | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);

  const loadRoots = useCallback(async () => {
    try {
      setErr(null);
      const data = await agentClient.filesRoots();
      const r = data.roots || [];
      setRoots(r);
      if (r.length > 0 && !activeRoot) setActiveRoot(r[0].id);
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    }
  }, [activeRoot]);

  const loadDir = useCallback(async () => {
    if (!activeRoot) return;
    setLoading(true);
    try {
      const data = await agentClient.filesList(activeRoot, path);
      const raw: unknown[] = data.entries || data.files || [];
      const normalized: FileEntry[] = raw
        .map((v): FileEntry | null => {
          if (v && typeof v === "object") {
            const o = v as Record<string, unknown>;
            const name = typeof o.name === "string" ? o.name : "";
            if (!name) return null;
            return {
              name,
              path: typeof o.path === "string" ? o.path : name,
              isDir: Boolean(o.isDir || o.type === "dir"),
              size: typeof o.size === "number" ? o.size : undefined,
            };
          }
          return null;
        })
        .filter((v): v is FileEntry => v !== null);
      setEntries(normalized);
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    } finally {
      setLoading(false);
    }
  }, [activeRoot, path]);

  useEffect(() => {
    void loadRoots();
  }, [loadRoots]);
  useEffect(() => {
    void loadDir();
  }, [loadDir]);

  async function open(entry: FileEntry) {
    if (entry.isDir) {
      setPath(entry.path);
      setPreview(null);
      return;
    }
    if (!activeRoot) return;
    try {
      const data = await agentClient.filesRead(activeRoot, entry.path);
      setPreview(typeof data?.content === "string" ? data.content : JSON.stringify(data, null, 2));
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    }
  }

  function up() {
    const segments = path.split("/").filter(Boolean);
    segments.pop();
    setPath(segments.join("/"));
    setPreview(null);
  }

  return (
    <div className="flex min-h-0 flex-1 flex-col gap-2">
      {err && <div className="rounded border border-red-500/40 bg-red-950/30 px-3 py-2 text-sm text-red-200">{err}</div>}
      <div className="flex items-center gap-2 text-xs text-surface-400">
        <select
          className="rounded border border-surface-700 bg-surface-900 px-2 py-1 text-sm"
          value={activeRoot ?? ""}
          onChange={(e) => {
            setActiveRoot(e.target.value);
            setPath("");
            setPreview(null);
          }}
        >
          {roots.length === 0 && <option value="">(no roots)</option>}
          {roots.map((r) => (
            <option key={r.id} value={r.id}>
              {r.name} — {r.path}
            </option>
          ))}
        </select>
        <button
          type="button"
          className="rounded bg-surface-800 px-2 py-1 disabled:opacity-40"
          onClick={up}
          disabled={!path}
        >
          ↑ up
        </button>
        <span className="truncate font-mono">/{path || ""}</span>
      </div>
      <div className="grid min-h-0 flex-1 grid-cols-1 gap-2 md:grid-cols-[minmax(0,280px)_minmax(0,1fr)]">
        <ul className="min-h-0 overflow-auto rounded border border-surface-700 text-sm">
          {loading && <li className="px-3 py-1 text-surface-500">Loading…</li>}
          {!loading && entries.length === 0 && <li className="px-3 py-1 text-surface-500">Empty.</li>}
          {entries.map((e) => (
            <li key={e.path}>
              <button
                type="button"
                className="flex w-full items-center justify-between px-3 py-1 text-left hover:bg-surface-800"
                onClick={() => void open(e)}
              >
                <span className="truncate">
                  {e.isDir ? "📁" : "📄"} {e.name}
                </span>
                {typeof e.size === "number" && !e.isDir && (
                  <span className="ml-2 text-xs text-surface-500">{e.size}B</span>
                )}
              </button>
            </li>
          ))}
        </ul>
        <pre className="min-h-0 overflow-auto rounded border border-surface-700 bg-black/40 p-3 font-mono text-xs">
          {preview ?? "(select a file to preview)"}
        </pre>
      </div>
    </div>
  );
}

// ── Shared storage (NAS / SMB / S3 / Azure) ─────────────────────────

function SharedTab() {
  const [profiles, setProfiles] = useState<StorageProfile[]>([]);
  const [err, setErr] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);
  const [activeId, setActiveId] = useState<string | null>(null);
  const [path, setPath] = useState("");
  const [entries, setEntries] = useState<FileEntry[]>([]);
  const [q, setQ] = useState("");

  const load = useCallback(async () => {
    try {
      setErr(null);
      const data = await agentClient.sharedStorageProfiles();
      const rows: unknown[] = Array.isArray(data?.profiles) ? data.profiles : Array.isArray(data) ? data : [];
      const normalized: StorageProfile[] = rows
        .map((v): StorageProfile | null => {
          if (v && typeof v === "object") {
            const o = v as Record<string, unknown>;
            const id = typeof o.id === "string" ? o.id : "";
            if (!id) return null;
            return {
              id,
              label: typeof o.label === "string" ? o.label : undefined,
              type: typeof o.type === "string" ? o.type : undefined,
              root: typeof o.root === "string" ? o.root : undefined,
            };
          }
          return null;
        })
        .filter((v): v is StorageProfile => v !== null);
      setProfiles(normalized);
      if (normalized.length > 0 && !activeId) setActiveId(normalized[0].id);
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    } finally {
      setLoading(false);
    }
  }, [activeId]);

  const loadDir = useCallback(async () => {
    if (!activeId) return;
    try {
      const data = await agentClient.sharedStorageList(activeId, path);
      const raw: unknown[] = Array.isArray(data?.entries) ? data.entries : [];
      const normalized: FileEntry[] = raw
        .map((v): FileEntry | null => {
          if (v && typeof v === "object") {
            const o = v as Record<string, unknown>;
            const name = typeof o.name === "string" ? o.name : "";
            if (!name) return null;
            return {
              name,
              path: typeof o.path === "string" ? o.path : name,
              isDir: Boolean(o.isDir || o.type === "dir"),
              size: typeof o.size === "number" ? o.size : undefined,
            };
          }
          return null;
        })
        .filter((v): v is FileEntry => v !== null);
      setEntries(normalized);
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    }
  }, [activeId, path]);

  useEffect(() => {
    void load();
  }, [load]);
  useEffect(() => {
    void loadDir();
  }, [loadDir]);

  async function search() {
    if (!q.trim() || !activeId) return;
    try {
      const out = await agentClient.sharedStorageSearch(q.trim(), { id: activeId, limit: 50 });
      const raw: unknown[] = Array.isArray(out?.matches) ? out.matches : Array.isArray(out?.results) ? out.results : [];
      const hits: FileEntry[] = raw
        .map((v): FileEntry | null => {
          if (v && typeof v === "object") {
            const o = v as Record<string, unknown>;
            const p = typeof o.path === "string" ? o.path : "";
            if (!p) return null;
            return { name: p.split("/").pop() || p, path: p, isDir: false };
          }
          return null;
        })
        .filter((v): v is FileEntry => v !== null);
      setEntries(hits);
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    }
  }

  return (
    <div className="flex min-h-0 flex-1 flex-col gap-2">
      {err && <div className="rounded border border-red-500/40 bg-red-950/30 px-3 py-2 text-sm text-red-200">{err}</div>}
      {loading && <p className="text-sm text-surface-400">Loading profiles…</p>}
      {!loading && profiles.length === 0 && (
        <div className="rounded border border-surface-700 bg-surface-950/30 p-3 text-sm text-surface-400">
          No shared-storage profiles. Add one with{" "}
          <code className="rounded bg-surface-900 px-1">yaver storage profile add</code> or via{" "}
          <code className="rounded bg-surface-900 px-1">/shared-storage/profiles</code> (SMB, NFS, S3, Azure).
        </div>
      )}
      {profiles.length > 0 && (
        <>
          <div className="flex flex-wrap items-center gap-2 text-xs text-surface-400">
            <select
              className="rounded border border-surface-700 bg-surface-900 px-2 py-1 text-sm"
              value={activeId ?? ""}
              onChange={(e) => {
                setActiveId(e.target.value);
                setPath("");
              }}
            >
              {profiles.map((p) => (
                <option key={p.id} value={p.id}>
                  {p.label || p.id} {p.type ? `(${p.type})` : ""}
                </option>
              ))}
            </select>
            <input
              className="flex-1 rounded border border-surface-700 bg-surface-900 px-2 py-1 text-sm"
              placeholder="search (full-text, limited to 50 hits)"
              value={q}
              onChange={(e) => setQ(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === "Enter") void search();
              }}
            />
            <button type="button" className="rounded bg-indigo-600 px-2 py-1 text-xs" onClick={() => void search()}>
              Search
            </button>
          </div>
          <ul className="min-h-0 flex-1 overflow-auto rounded border border-surface-700 text-sm">
            {entries.length === 0 && <li className="px-3 py-1 text-surface-500">Empty.</li>}
            {entries.map((e) => (
              <li key={e.path} className="flex items-center justify-between px-3 py-1 hover:bg-surface-800">
                <span className="truncate font-mono">
                  {e.isDir ? "📁" : "📄"} {e.path}
                </span>
                {typeof e.size === "number" && !e.isDir && (
                  <span className="text-xs text-surface-500">{e.size}B</span>
                )}
              </li>
            ))}
          </ul>
        </>
      )}
    </div>
  );
}

// ── Blobs ────────────────────────────────────────────────────────────

function BlobsTab() {
  const [buckets, setBuckets] = useState<string[]>([]);
  const [active, setActive] = useState<string | null>(null);
  const [keys, setKeys] = useState<{ key: string; size?: number; contentType?: string; updatedAt?: string }[]>([]);
  const [err, setErr] = useState<string | null>(null);

  useEffect(() => {
    (async () => {
      try {
        const data = await agentClient.blobsListBuckets();
        setBuckets(data.buckets || []);
        if ((data.buckets || []).length > 0) setActive(data.buckets[0]);
      } catch (e) {
        setErr(e instanceof Error ? e.message : String(e));
      }
    })();
  }, []);

  useEffect(() => {
    if (!active) return;
    (async () => {
      try {
        const data = await agentClient.blobsListKeys(active);
        setKeys(data.keys || []);
      } catch (e) {
        setErr(e instanceof Error ? e.message : String(e));
      }
    })();
  }, [active]);

  async function remove(k: string) {
    if (!active) return;
    if (!window.confirm(`Delete ${active}/${k}?`)) return;
    try {
      await agentClient.blobsDelete(active, k);
      const data = await agentClient.blobsListKeys(active);
      setKeys(data.keys || []);
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    }
  }

  return (
    <div className="flex min-h-0 flex-1 flex-col gap-2">
      {err && <div className="rounded border border-red-500/40 bg-red-950/30 px-3 py-2 text-sm text-red-200">{err}</div>}
      {buckets.length === 0 && !err && (
        <div className="rounded border border-surface-700 bg-surface-950/30 p-3 text-sm text-surface-400">
          No buckets yet. Create one by PUT-ing a first object via the blobs API or{" "}
          <code className="rounded bg-surface-900 px-1">yaver blob put</code>.
        </div>
      )}
      {buckets.length > 0 && (
        <>
          <div className="flex items-center gap-2 text-xs">
            <select
              className="rounded border border-surface-700 bg-surface-900 px-2 py-1 text-sm"
              value={active ?? ""}
              onChange={(e) => setActive(e.target.value)}
            >
              {buckets.map((b) => (
                <option key={b} value={b}>
                  {b}
                </option>
              ))}
            </select>
            <span className="text-surface-500">{keys.length} key(s)</span>
          </div>
          <ul className="min-h-0 flex-1 overflow-auto rounded border border-surface-700 text-sm">
            {keys.map((k) => (
              <li key={k.key} className="flex items-center justify-between px-3 py-1 hover:bg-surface-800">
                <span className="truncate font-mono">{k.key}</span>
                <span className="flex items-center gap-2 text-xs text-surface-500">
                  {k.contentType && <code className="text-surface-400">{k.contentType}</code>}
                  {typeof k.size === "number" && <span>{k.size}B</span>}
                  <button
                    type="button"
                    onClick={() => void remove(k.key)}
                    className="rounded bg-red-900/40 px-2 py-0.5 text-red-200 hover:bg-red-900/70"
                  >
                    Delete
                  </button>
                </span>
              </li>
            ))}
          </ul>
        </>
      )}
    </div>
  );
}
