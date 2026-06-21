"use client";

// BrowserSandbox — the browser-local sandbox surface inside the Phone tab.
// Everything here runs on the user's own machine: SQLite-in-WASM (sql.js)
// persisted to IndexedDB, a live esbuild+iframe app preview, and one-tap deploy
// to Yaver Serverless via the same .yaver.tgz bundle the agent uses. No Go
// binary, no cloud required until the user deploys. Used when no agent is
// connected; the agent-relay PhoneProjectsView handles the connected case.

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import type { PhoneAppSpec, PhoneProject, PhoneSchema } from "@/lib/agent-client";
import { useAuth } from "@/lib/use-auth";
import { getYaverCloudBaseUrl } from "@/lib/yaver-cloud";
import {
  browseLocalTable,
  createLocalProject,
  deleteLocalProject,
  deleteLocalRow,
  exportLocalBundle,
  getLocalAppAndSchema,
  getLocalProject,
  insertLocalRow,
  listLocalProjects,
  listLocalTables,
  listLocalTemplates,
} from "@/lib/sandbox/localProjects";
import { createServerlessShare, deployLocalProjectToCloud } from "@/lib/sandbox/deploy";
import { draftProjectFromPrompt } from "@/lib/sandbox/aiDraft";
import { gatewayConfigured } from "@/lib/sandbox/gateway";
import { buildPreviewSrcdoc } from "@/lib/sandbox/preview";
import { attachSandboxBridge } from "@/lib/sandbox/dataBridge";

function formatBytes(n: number): string {
  if (!n) return "0 B";
  if (n < 1024) return `${n} B`;
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`;
  return `${(n / 1024 / 1024).toFixed(1)} MB`;
}

function clean(e: unknown, fallback: string): string {
  const raw = e instanceof Error ? e.message : typeof e === "string" ? e : "";
  return raw.trim() && raw.trim().length <= 200 ? raw.trim() : fallback;
}

// ── Live preview (esbuild bundle in a sandboxed iframe + data bridge) ─────────
function LivePreview({ slug, onMutate }: { slug: string; onMutate: () => void }) {
  const [srcDoc, setSrcDoc] = useState<string | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const iframeRef = useRef<HTMLIFrameElement | null>(null);

  useEffect(() => {
    let detach: (() => void) | null = null;
    let cancelled = false;
    (async () => {
      try {
        const sa = await getLocalAppAndSchema(slug);
        if (!sa) throw new Error("project not found");
        const doc = await buildPreviewSrcdoc(sa.schema, sa.app);
        if (cancelled) return;
        detach = attachSandboxBridge(slug, { onMutate });
        setSrcDoc(doc);
      } catch (e) {
        if (!cancelled) setErr(clean(e, "Preview failed to build."));
      }
    })();
    return () => {
      cancelled = true;
      detach?.();
    };
  }, [slug, onMutate]);

  if (err) {
    return <div className="rounded border border-red-500/30 bg-red-500/10 p-3 text-xs text-red-300">{err}</div>;
  }
  if (!srcDoc) {
    return <div className="rounded border border-surface-800 bg-surface-950 p-4 text-sm text-surface-500">Compiling preview…</div>;
  }
  return (
    <iframe
      ref={iframeRef}
      title="App preview"
      sandbox="allow-scripts"
      srcDoc={srcDoc}
      className="h-[480px] w-full rounded border border-surface-800 bg-white dark:bg-surface-950"
    />
  );
}

export default function BrowserSandbox() {
  const { token } = useAuth();
  const [projects, setProjects] = useState<PhoneProject[]>([]);
  const [loading, setLoading] = useState(true);
  const [notice, setNotice] = useState<{ type: "ok" | "error"; text: string } | null>(null);

  const templates = useMemo(() => listLocalTemplates(), []);
  const [showForm, setShowForm] = useState(false);
  const [name, setName] = useState("");
  const [templateId, setTemplateId] = useState("crud");
  const [prompt, setPrompt] = useState("");
  const [creating, setCreating] = useState(false);

  const [selected, setSelected] = useState<PhoneProject | null>(null);
  const [tables, setTables] = useState<Array<{ name: string; rowCount?: number }>>([]);
  const [activeTable, setActiveTable] = useState<string | null>(null);
  const [rows, setRows] = useState<Array<Record<string, unknown>>>([]);
  const [insertJSON, setInsertJSON] = useState("{}");
  const [showPreview, setShowPreview] = useState(false);
  const [deploying, setDeploying] = useState(false);
  const [lastDeploy, setLastDeploy] = useState<{ url: string } | null>(null);
  const [sharing, setSharing] = useState(false);
  const [share, setShare] = useState<{ code: string; link: string } | null>(null);

  const showNotice = useCallback((type: "ok" | "error", text: string) => {
    setNotice({ type, text });
    setTimeout(() => setNotice((n) => (n?.text === text ? null : n)), 6000);
  }, []);

  const load = useCallback(async () => {
    try {
      setProjects(await listLocalProjects());
    } catch (e) {
      showNotice("error", clean(e, "Could not read local projects."));
    } finally {
      setLoading(false);
    }
  }, [showNotice]);

  useEffect(() => {
    void load();
  }, [load]);

  const refreshTable = useCallback(async (slug: string, table: string) => {
    const r = await browseLocalTable(slug, table, 200);
    setRows(r.rows);
  }, []);

  const loadDetail = useCallback(
    async (slug: string) => {
      setShowPreview(false);
      setLastDeploy(null);
      setShare(null);
      // Fetch directly (not from the `projects` closure, which may be stale
      // right after create()).
      const proj = await getLocalProject(slug);
      setSelected(proj);
      const ts = await listLocalTables(slug);
      setTables(ts);
      if (ts.length) {
        setActiveTable(ts[0].name);
        await refreshTable(slug, ts[0].name);
      } else {
        setActiveTable(null);
        setRows([]);
      }
    },
    [refreshTable],
  );

  async function create() {
    const projectName = name.trim() || prompt.trim().slice(0, 40);
    if (!projectName && !prompt.trim()) return;
    setCreating(true);
    try {
      let proj: PhoneProject;
      if (prompt.trim() && gatewayConfigured() && token) {
        const spec = await draftProjectFromPrompt(prompt.trim(), token);
        proj = await createLocalProject({ ...spec, name: name.trim() || spec.name });
      } else {
        proj = await createLocalProject({ name: projectName || "New App", template: templateId });
      }
      setName("");
      setPrompt("");
      setShowForm(false);
      await load();
      await loadDetail(proj.slug);
    } catch (e) {
      showNotice("error", clean(e, "Couldn't create the project."));
    } finally {
      setCreating(false);
    }
  }

  async function doDelete(slug: string) {
    if (!window.confirm(`Delete "${slug}" from this browser? This removes its SQLite data.`)) return;
    await deleteLocalProject(slug);
    if (selected?.slug === slug) {
      setSelected(null);
      setTables([]);
      setRows([]);
    }
    await load();
  }

  async function switchTable(table: string) {
    if (!selected) return;
    setActiveTable(table);
    await refreshTable(selected.slug, table);
  }

  async function doInsert() {
    if (!selected || !activeTable) return;
    try {
      const doc = JSON.parse(insertJSON || "{}");
      if (!doc || typeof doc !== "object") throw new Error("JSON must be an object");
      await insertLocalRow(selected.slug, activeTable, doc);
      setInsertJSON("{}");
      await switchTable(activeTable);
    } catch (e) {
      showNotice("error", clean(e, "Insert failed — check the JSON."));
    }
  }

  async function doDeleteRow(id: unknown) {
    if (!selected || !activeTable || id === undefined) return;
    await deleteLocalRow(selected.slug, activeTable, id);
    await switchTable(activeTable);
  }

  async function doDownload(slug: string) {
    try {
      const bytes = await exportLocalBundle(slug, true);
      const blob = new Blob([bytes.slice().buffer], { type: "application/gzip" });
      const url = URL.createObjectURL(blob);
      const a = document.createElement("a");
      a.href = url;
      a.download = `${slug}.yaver.tgz`;
      document.body.appendChild(a);
      a.click();
      a.remove();
      URL.revokeObjectURL(url);
    } catch (e) {
      showNotice("error", clean(e, "Export failed."));
    }
  }

  async function doDeploy() {
    if (!selected) return;
    const baseUrl = getYaverCloudBaseUrl();
    if (!baseUrl) {
      showNotice("error", "No Yaver Cloud URL configured for this build.");
      return;
    }
    setDeploying(true);
    setLastDeploy(null);
    try {
      const res = await deployLocalProjectToCloud({ baseUrl, token: token ?? undefined, slug: selected.slug });
      if (res.ok) {
        setLastDeploy({ url: res.browseUrl ?? baseUrl });
        showNotice("ok", "Deployed to Yaver Serverless.");
      } else {
        showNotice("error", res.error ?? "Deploy failed.");
      }
    } catch (e) {
      showNotice("error", clean(e, "Deploy failed — target may be unreachable."));
    } finally {
      setDeploying(false);
    }
  }

  async function doShare() {
    if (!selected) return;
    const baseUrl = getYaverCloudBaseUrl();
    if (!baseUrl) {
      showNotice("error", "No Yaver Cloud URL configured for this build.");
      return;
    }
    setSharing(true);
    try {
      const res = await createServerlessShare({ baseUrl, token: token ?? undefined, slug: selected.slug });
      if (res.ok && res.code && res.link) {
        setShare({ code: res.code, link: res.link });
        showNotice("ok", "Share link ready — friends can open the app, no account needed.");
      } else {
        showNotice("error", res.error ?? "Could not create share. Deploy first, then share.");
      }
    } catch (e) {
      showNotice("error", clean(e, "Share failed."));
    } finally {
      setSharing(false);
    }
  }

  const onPreviewMutate = useCallback(() => {
    if (selected && activeTable) void refreshTable(selected.slug, activeTable);
  }, [selected, activeTable, refreshTable]);

  return (
    <div className="flex flex-col gap-6">
      <div>
        <h1 className="text-xl font-semibold text-surface-100">Browser Sandbox</h1>
        <p className="mt-1 text-sm text-surface-400">
          Build a SQLite-backed app entirely in this browser — no agent, no cloud.
          Edit data, preview the live app, then deploy the same project to Yaver
          Serverless in one tap. Projects are stored locally in this browser; use
          “Download .tgz” to back them up or move them.
        </p>
      </div>

      {notice ? (
        <div
          className={`rounded border p-3 text-sm ${
            notice.type === "ok"
              ? "border-emerald-500/30 bg-emerald-500/10 text-emerald-200"
              : "border-red-500/30 bg-red-500/10 text-red-300"
          }`}
        >
          {notice.text}
        </div>
      ) : null}

      <div className="flex items-center gap-3">
        {!showForm ? (
          <button
            onClick={() => setShowForm(true)}
            className="rounded bg-indigo-600 px-4 py-2 text-sm font-medium text-white hover:bg-indigo-500"
          >
            + New app
          </button>
        ) : null}
        <button
          onClick={() => void load()}
          className="rounded border border-surface-700 px-3 py-1.5 text-sm text-surface-300 hover:bg-surface-800"
        >
          Refresh
        </button>
      </div>

      {showForm ? (
        <div className="rounded border border-surface-800 bg-surface-900 p-4">
          <label className="text-xs uppercase tracking-wide text-surface-400">App name</label>
          <input
            value={name}
            onChange={(e) => setName(e.target.value)}
            placeholder="My app"
            className="mt-1 w-full rounded border border-surface-700 bg-surface-950 px-3 py-2 text-sm text-surface-100"
          />
          <label className="mt-4 block text-xs uppercase tracking-wide text-surface-400">Template</label>
          <div className="mt-2 grid grid-cols-2 gap-2">
            {templates.map((t) => (
              <button
                key={t.id}
                onClick={() => setTemplateId(t.id)}
                className={`rounded border p-3 text-left text-sm transition ${
                  templateId === t.id
                    ? "border-indigo-500 bg-indigo-500/10"
                    : "border-surface-800 bg-surface-950 hover:border-surface-600"
                }`}
              >
                <div className="font-medium text-surface-100">{t.label}</div>
                <div className="mt-0.5 text-xs text-surface-400">{t.description}</div>
              </button>
            ))}
          </div>
          <label className="mt-4 block text-xs uppercase tracking-wide text-surface-400">
            Or describe the app {gatewayConfigured() ? "(AI drafts the schema)" : "(AI drafting unavailable)"}
          </label>
          <textarea
            value={prompt}
            onChange={(e) => setPrompt(e.target.value)}
            disabled={!gatewayConfigured() || !token}
            placeholder={gatewayConfigured() ? "A habit tracker with daily check-ins and a streak count…" : "Sign in and configure the AI gateway to use this."}
            className="mt-1 min-h-20 w-full rounded border border-surface-700 bg-surface-950 px-3 py-2 text-sm text-surface-100 disabled:opacity-50"
          />
          <div className="mt-4 flex justify-end gap-2">
            <button
              onClick={() => setShowForm(false)}
              className="rounded border border-surface-700 px-3 py-1.5 text-sm text-surface-300 hover:bg-surface-800"
            >
              Cancel
            </button>
            <button
              disabled={creating || (!name.trim() && !prompt.trim())}
              onClick={create}
              className="rounded bg-indigo-600 px-4 py-1.5 text-sm font-medium text-white disabled:opacity-50 hover:bg-indigo-500"
            >
              {creating ? "Creating…" : "Create"}
            </button>
          </div>
        </div>
      ) : null}

      <div className="grid grid-cols-1 gap-3 lg:grid-cols-3">
        <div className="lg:col-span-1">
          <div className="mb-2 text-xs uppercase tracking-wide text-surface-500">Apps in this browser</div>
          {loading ? (
            <div className="text-sm text-surface-500">Loading…</div>
          ) : projects.length === 0 ? (
            <div className="rounded border border-surface-800 bg-surface-950 p-4 text-sm text-surface-400">
              No apps yet. Click “New app” to build one locally.
            </div>
          ) : (
            <div className="flex flex-col gap-2">
              {projects.map((p) => (
                <button
                  key={p.slug}
                  onClick={() => void loadDetail(p.slug)}
                  className={`rounded border p-3 text-left transition ${
                    selected?.slug === p.slug
                      ? "border-indigo-500 bg-indigo-500/10"
                      : "border-surface-800 bg-surface-950 hover:border-surface-600"
                  }`}
                >
                  <div className="text-sm font-medium text-surface-100">{p.name}</div>
                  <div className="mt-0.5 text-xs text-surface-400">
                    {p.slug}
                    {p.template ? ` · ${p.template}` : ""}
                  </div>
                  {p.stats ? (
                    <div className="mt-1 text-[11px] text-surface-500">
                      {p.stats.tableCount} tables · {p.stats.rowCount} rows · {formatBytes(p.stats.dbBytes)}
                    </div>
                  ) : null}
                </button>
              ))}
            </div>
          )}
        </div>

        <div className="lg:col-span-2">
          {!selected ? (
            <div className="rounded border border-dashed border-surface-800 bg-surface-950 p-6 text-sm text-surface-500">
              Pick an app to browse data, preview it live, deploy to serverless, or download a .tgz.
            </div>
          ) : (
            <div className="flex flex-col gap-4">
              <div className="flex items-center justify-between">
                <div>
                  <div className="text-lg font-semibold text-surface-100">{selected.name}</div>
                  <div className="text-xs text-surface-500">{selected.slug}</div>
                </div>
                <div className="flex flex-wrap gap-2">
                  <button
                    onClick={() => setShowPreview((v) => !v)}
                    className="rounded border border-indigo-500 px-3 py-1.5 text-sm text-indigo-300 hover:bg-indigo-500/10"
                  >
                    {showPreview ? "Hide preview" : "Preview app"}
                  </button>
                  <button
                    disabled={deploying}
                    onClick={() => void doDeploy()}
                    className="rounded bg-indigo-600 px-3 py-1.5 text-sm font-medium text-white hover:bg-indigo-500 disabled:opacity-50"
                  >
                    {deploying ? "Deploying…" : "Deploy to serverless"}
                  </button>
                  <button
                    onClick={() => void doDownload(selected.slug)}
                    className="rounded border border-surface-700 px-3 py-1.5 text-sm text-surface-200 hover:bg-surface-800"
                  >
                    Download .tgz
                  </button>
                  <button
                    onClick={() => void doDelete(selected.slug)}
                    className="rounded border border-red-500/50 px-3 py-1.5 text-sm text-red-300 hover:bg-red-500/10"
                  >
                    Delete
                  </button>
                </div>
              </div>

              {lastDeploy ? (
                <div className="flex flex-col gap-2">
                  <a
                    href={lastDeploy.url}
                    target="_blank"
                    rel="noreferrer"
                    className="block rounded border border-emerald-500/40 bg-emerald-500/10 p-3 text-xs text-emerald-200 hover:bg-emerald-500/15"
                  >
                    ✓ Deployed — <span className="underline">{lastDeploy.url}</span>
                  </a>
                  <div className="flex items-center gap-2">
                    <button
                      disabled={sharing}
                      onClick={() => void doShare()}
                      className="rounded border border-indigo-500 px-3 py-1.5 text-sm text-indigo-300 hover:bg-indigo-500/10 disabled:opacity-50"
                    >
                      {sharing ? "Creating link…" : "Share with a friend"}
                    </button>
                    <span className="text-xs text-surface-500">Friends open the app — no account, no install.</span>
                  </div>
                  {share ? (
                    <div className="rounded border border-indigo-500/30 bg-indigo-500/10 p-3 text-xs text-indigo-100">
                      <div>
                        Code: <span className="font-mono font-semibold">{share.code}</span>
                      </div>
                      <a href={share.link} target="_blank" rel="noreferrer" className="mt-1 block break-all underline">
                        {share.link}
                      </a>
                    </div>
                  ) : null}
                </div>
              ) : null}

              {showPreview ? <LivePreview slug={selected.slug} onMutate={onPreviewMutate} /> : null}

              <div>
                <div className="mb-2 text-xs uppercase tracking-wide text-surface-500">Tables</div>
                <div className="flex flex-wrap gap-2">
                  {tables.length === 0 ? (
                    <div className="text-sm text-surface-500">No tables yet.</div>
                  ) : (
                    tables.map((t) => (
                      <button
                        key={t.name}
                        onClick={() => void switchTable(t.name)}
                        className={`rounded-full border px-3 py-1 text-xs transition ${
                          activeTable === t.name
                            ? "border-indigo-400 bg-indigo-500 text-white"
                            : "border-surface-700 bg-surface-950 text-surface-300 hover:border-surface-600"
                        }`}
                      >
                        {t.name}
                        {typeof t.rowCount === "number" ? <span className="ml-1 opacity-70">({t.rowCount})</span> : null}
                      </button>
                    ))
                  )}
                </div>
              </div>

              {activeTable ? (
                <div className="flex flex-col gap-2">
                  <div className="flex items-start gap-2">
                    <input
                      value={insertJSON}
                      onChange={(e) => setInsertJSON(e.target.value)}
                      placeholder='{"title":"hello"}'
                      className="flex-1 rounded border border-surface-700 bg-surface-950 px-3 py-2 font-mono text-xs text-surface-100"
                    />
                    <button
                      onClick={doInsert}
                      className="rounded bg-indigo-600 px-3 py-2 text-xs font-medium text-white hover:bg-indigo-500"
                    >
                      Insert
                    </button>
                  </div>
                  <div className="overflow-auto rounded border border-surface-800 bg-surface-950">
                    {rows.length === 0 ? (
                      <div className="p-4 text-sm text-surface-500">No rows.</div>
                    ) : (
                      <table className="w-full text-xs">
                        <thead className="bg-surface-900 text-surface-400">
                          <tr>
                            {Object.keys(rows[0]).map((k) => (
                              <th key={k} className="px-3 py-2 text-left font-medium">
                                {k}
                              </th>
                            ))}
                            <th />
                          </tr>
                        </thead>
                        <tbody>
                          {rows.map((r, i) => (
                            <tr key={i} className="border-t border-surface-800">
                              {Object.entries(r).map(([k, v]) => (
                                <td key={k} className="px-3 py-2 text-surface-200">
                                  {v === null || v === undefined ? "—" : typeof v === "object" ? JSON.stringify(v) : String(v)}
                                </td>
                              ))}
                              <td className="px-2 py-2 text-right">
                                <button
                                  onClick={() => void doDeleteRow(r.id ?? Object.values(r)[0])}
                                  className="text-xs text-red-400 hover:text-red-300"
                                >
                                  ×
                                </button>
                              </td>
                            </tr>
                          ))}
                        </tbody>
                      </table>
                    )}
                  </div>
                </div>
              ) : null}
            </div>
          )}
        </div>
      </div>
    </div>
  );
}
