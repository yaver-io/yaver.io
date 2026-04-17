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

import { useCallback, useEffect, useRef, useState } from "react";
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
  const [keys, setKeys] = useState<{ key: string; size?: number; contentType?: string; uploadedAt?: string }[]>([]);
  const [nextCursor, setNextCursor] = useState<string | undefined>(undefined);
  const [total, setTotal] = useState<number>(0);
  const [loadingMore, setLoadingMore] = useState(false);
  const [prefix, setPrefix] = useState("");
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [sharing, setSharing] = useState<{ key: string; url: string; expiresIn: number } | null>(null);
  const [ttlSec, setTtlSec] = useState<number>(300);
  const [err, setErr] = useState<string | null>(null);
  const [uploading, setUploading] = useState(false);
  const [uploadLabel, setUploadLabel] = useState("");
  const [uploadPct, setUploadPct] = useState(0);
  const [newBucket, setNewBucket] = useState("");
  const fileRef = useRef<HTMLInputElement>(null);

  const loadBuckets = useCallback(async () => {
    try {
      const data = await agentClient.blobsListBuckets();
      setBuckets(data.buckets || []);
      if ((data.buckets || []).length > 0 && !active) setActive(data.buckets[0]);
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    }
  }, [active]);

  useEffect(() => {
    void loadBuckets();
  }, [loadBuckets]);

  const loadKeys = useCallback(async (bucket: string) => {
    try {
      const data = await agentClient.blobsListKeys(bucket, { limit: 200 });
      setKeys(data.keys || []);
      setNextCursor(data.nextCursor);
      setTotal(data.total ?? (data.keys ?? []).length);
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    }
  }, []);

  async function loadMoreKeys() {
    if (!active || !nextCursor || loadingMore) return;
    setLoadingMore(true);
    try {
      const data = await agentClient.blobsListKeys(active, { limit: 200, after: nextCursor });
      setKeys((prev) => [...prev, ...(data.keys ?? [])]);
      setNextCursor(data.nextCursor);
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    } finally {
      setLoadingMore(false);
    }
  }

  useEffect(() => {
    if (!active) return;
    void loadKeys(active);
  }, [active, loadKeys]);

  async function onUpload(files: FileList | null) {
    if (!files || files.length === 0) return;
    const bucket = active ?? newBucket.trim();
    if (!bucket) {
      setErr("pick a bucket or type a new one before uploading");
      return;
    }
    setUploading(true);
    setUploadPct(0);
    try {
      const list = Array.from(files);
      for (let i = 0; i < list.length; i++) {
        const file = list[i];
        setUploadLabel(`${i + 1}/${list.length} ${file.name}`);
        await agentClient.blobsUpload(bucket, file.name, file, (loaded, total) => {
          if (total > 0) setUploadPct(Math.round((loaded / total) * 100));
        });
      }
      await loadBuckets();
      setActive(bucket);
      await loadKeys(bucket);
      if (fileRef.current) fileRef.current.value = "";
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    } finally {
      setUploading(false);
      setUploadLabel("");
      setUploadPct(0);
    }
  }

  async function remove(k: string) {
    if (!active) return;
    if (!window.confirm(`Delete ${active}/${k}?`)) return;
    try {
      await agentClient.blobsDelete(active, k);
      const data = await agentClient.blobsListKeys(active);
      setKeys(data.keys || []);
      setSelected((prev) => {
        const next = new Set(prev);
        next.delete(k);
        return next;
      });
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    }
  }

  async function removeSelected() {
    if (!active || selected.size === 0) return;
    if (!window.confirm(`Delete ${selected.size} selected key(s)?`)) return;
    const failures: string[] = [];
    await Promise.all(
      Array.from(selected).map(async (k) => {
        try {
          await agentClient.blobsDelete(active, k);
        } catch {
          failures.push(k);
        }
      }),
    );
    setSelected(new Set());
    const data = await agentClient.blobsListKeys(active, { limit: 200 });
    setKeys(data.keys || []);
    setNextCursor(data.nextCursor);
    if (failures.length > 0) {
      setErr(`failed to delete: ${failures.slice(0, 5).join(", ")}${failures.length > 5 ? "…" : ""}`);
    }
  }

  async function download(k: string) {
    if (!active) return;
    try {
      await agentClient.blobsDownload(active, k);
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    }
  }

  async function sign(k: string) {
    if (!active) return;
    try {
      const res = await agentClient.blobsSignUrl(active, k, ttlSec);
      setSharing({ key: k, url: res.url, expiresIn: res.expiresIn });
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    }
  }

  function toggleSelect(k: string) {
    setSelected((prev) => {
      const next = new Set(prev);
      if (next.has(k)) next.delete(k);
      else next.add(k);
      return next;
    });
  }

  const filteredKeys = prefix.trim()
    ? keys.filter((k) => k.key.toLowerCase().includes(prefix.toLowerCase()))
    : keys;

  return (
    <div className="flex min-h-0 flex-1 flex-col gap-2">
      {err && <div className="rounded border border-red-500/40 bg-red-950/30 px-3 py-2 text-sm text-red-200">{err}</div>}
      <div className="flex flex-wrap items-center gap-2 rounded border border-surface-700 bg-surface-950/30 p-2 text-xs">
        <input
          type="text"
          placeholder="new bucket name (optional)"
          className="rounded border border-surface-700 bg-surface-900 px-2 py-1 text-sm"
          value={newBucket}
          onChange={(e) => setNewBucket(e.target.value)}
        />
        <input
          ref={fileRef}
          type="file"
          multiple
          disabled={uploading}
          onChange={(e) => void onUpload(e.target.files)}
          className="text-surface-300"
        />
        {uploading && (
          <span className="flex flex-1 items-center gap-2 text-surface-300">
            <span className="truncate text-xs">{uploadLabel}</span>
            <span className="h-1 flex-1 min-w-[80px] overflow-hidden rounded bg-surface-800">
              <span
                className="block h-full bg-indigo-500 transition-all"
                style={{ width: `${uploadPct}%` }}
              />
            </span>
            <span className="text-xs tabular-nums">{uploadPct}%</span>
          </span>
        )}
        <span className="ml-auto text-surface-500">files land in <code className="rounded bg-surface-900 px-1">~/.yaver/blobs/&lt;bucket&gt;/</code> on your machine</span>
      </div>
      {buckets.length === 0 && !err && !uploading && (
        <div className="rounded border border-surface-700 bg-surface-950/30 p-3 text-sm text-surface-400">
          No buckets yet. Type a bucket name above and pick a file to create one.
        </div>
      )}
      {buckets.length > 0 && (
        <>
          <div className="flex flex-wrap items-center gap-2 text-xs">
            <select
              className="rounded border border-surface-700 bg-surface-900 px-2 py-1 text-sm"
              value={active ?? ""}
              onChange={(e) => {
                setActive(e.target.value);
                setSelected(new Set());
                setPrefix("");
              }}
            >
              {buckets.map((b) => (
                <option key={b} value={b}>
                  {b}
                </option>
              ))}
            </select>
            <input
              className="w-40 rounded border border-surface-700 bg-surface-900 px-2 py-1 text-sm"
              placeholder="filter keys…"
              value={prefix}
              onChange={(e) => setPrefix(e.target.value)}
            />
            <span className="text-surface-500">
              {filteredKeys.length}
              {total > keys.length ? ` / ${total}` : ""} key(s)
            </span>
            {selected.size > 0 && (
              <button
                type="button"
                className="ml-auto rounded bg-red-900/40 px-2 py-0.5 text-red-200 hover:bg-red-900/70"
                onClick={() => void removeSelected()}
              >
                Delete {selected.size}
              </button>
            )}
          </div>
          {sharing && (
            <div className="rounded border border-indigo-500/40 bg-indigo-950/30 p-3 text-sm">
              <p className="text-indigo-200">
                Signed URL for <code className="text-indigo-100">{sharing.key}</code> — expires in {sharing.expiresIn}s
              </p>
              <div className="mt-2 flex gap-2">
                <input
                  className="flex-1 rounded border border-surface-700 bg-surface-900 px-2 py-1 font-mono text-xs"
                  readOnly
                  value={sharing.url}
                  onFocus={(e) => e.currentTarget.select()}
                />
                <button
                  type="button"
                  className="rounded bg-indigo-600 px-3 py-1 text-xs font-semibold"
                  onClick={() => void navigator.clipboard.writeText(sharing.url).catch(() => {})}
                >
                  Copy
                </button>
                <button
                  type="button"
                  className="rounded bg-surface-800 px-3 py-1 text-xs"
                  onClick={() => setSharing(null)}
                >
                  Close
                </button>
              </div>
              <p className="mt-1 text-[11px] text-surface-500">
                Anyone with this link can read the blob until it expires. Never share a URL whose agent is on a private LAN if the recipient is outside that network.
              </p>
            </div>
          )}
          <div className="flex flex-wrap items-center gap-2 text-[11px] text-surface-500">
            <span>share TTL:</span>
            {[300, 3600, 86400].map((sec) => (
              <button
                key={sec}
                type="button"
                className={`rounded px-2 py-0.5 ${
                  ttlSec === sec ? "bg-indigo-900/60 text-indigo-100" : "bg-surface-900 hover:text-surface-200"
                }`}
                onClick={() => setTtlSec(sec)}
              >
                {sec === 300 ? "5 min" : sec === 3600 ? "1 hour" : "1 day"}
              </button>
            ))}
          </div>
          <ul className="min-h-0 flex-1 overflow-auto rounded border border-surface-700 text-sm">
            {filteredKeys.map((k) => (
              <li key={k.key} className="flex items-center gap-2 px-3 py-1 hover:bg-surface-800">
                <input
                  type="checkbox"
                  className="accent-indigo-500"
                  checked={selected.has(k.key)}
                  onChange={() => toggleSelect(k.key)}
                />
                <button
                  type="button"
                  className="flex-1 truncate text-left font-mono hover:text-indigo-300"
                  onClick={() => void download(k.key)}
                  title="Click to download"
                >
                  {k.key}
                </button>
                <span className="flex items-center gap-2 text-xs text-surface-500">
                  {k.contentType && <code className="text-surface-400">{k.contentType}</code>}
                  {typeof k.size === "number" && <span>{k.size}B</span>}
                  <button
                    type="button"
                    onClick={() => void sign(k.key)}
                    className="rounded bg-surface-800 px-2 py-0.5 hover:bg-surface-700"
                    title="Generate a signed share URL"
                  >
                    Share
                  </button>
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
            {nextCursor && (
              <li className="flex justify-center py-2">
                <button
                  type="button"
                  onClick={() => void loadMoreKeys()}
                  disabled={loadingMore}
                  className="rounded bg-indigo-900/40 px-3 py-1 text-xs text-indigo-200 hover:bg-indigo-900/60 disabled:opacity-40"
                >
                  {loadingMore ? "Loading…" : "Load more"}
                </button>
              </li>
            )}
          </ul>
        </>
      )}
    </div>
  );
}
