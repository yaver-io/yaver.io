"use client";

import { useCallback, useEffect, useMemo, useState } from "react";
import {
  cleanupExpiredProjectArtifacts,
  createProjectArtifact,
  createProjectArtifactUploadUrl,
  getProjectArtifactUsage,
  hideProjectArtifact,
  listProjectArtifacts,
  type ProjectArtifact,
  type ProjectArtifactUsage,
  type ProjectArtifactVisibility,
} from "@/lib/task-placement";
import { getManagedSubscription } from "@/lib/subscription";

const KIND_OPTIONS = ["apk", "hermes", "web-preview", "screenshot", "bundle", "other"];

export function planIncludesYaverArtifactStorage(plan?: string | null): boolean {
  const value = String(plan || "").trim();
  return value === "cloud-workspace" || value === "cloud-agent" || value.startsWith("yaver-cloud");
}

export function fmtProjectArtifactBytes(bytes?: number | null): string {
  const value = Math.max(0, Number(bytes || 0));
  if (value < 1024) return `${value} B`;
  const units = ["KB", "MB", "GB", "TB"];
  let n = value / 1024;
  let i = 0;
  while (n >= 1024 && i < units.length - 1) {
    n /= 1024;
    i += 1;
  }
  return `${n >= 10 ? n.toFixed(0) : n.toFixed(1)} ${units[i]}`;
}

export function projectArtifactMeteredLabel(usage?: Pick<ProjectArtifactUsage["project"], "storageBytes" | "reservedUploadBytes" | "totalMeteredBytes"> | null): string {
  const reservedBytes = usage?.reservedUploadBytes ?? 0;
  const meteredBytes = usage?.totalMeteredBytes ?? usage?.storageBytes;
  return `${fmtProjectArtifactBytes(meteredBytes)} metered${reservedBytes > 0 ? ` · ${fmtProjectArtifactBytes(reservedBytes)} pending upload` : ""}`;
}

export function ownerArtifactStorageLabel(usage?: Pick<ProjectArtifactUsage["owner"], "storageBytes" | "reservedUploadBytes" | "totalMeteredBytes" | "remainingBytes"> | null): string {
  const reservedBytes = usage?.reservedUploadBytes ?? 0;
  return `${fmtProjectArtifactBytes(usage?.remainingBytes)} remaining${reservedBytes > 0 ? ` · ${fmtProjectArtifactBytes(reservedBytes)} reserved` : ""}`;
}

function fmtDate(ts?: number | null): string {
  if (!ts) return "";
  return new Date(ts).toLocaleString(undefined, {
    month: "short",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
  });
}

function artifactLink(artifact: ProjectArtifact): string | null {
  return artifact.url || null;
}

function visibilityLabel(value: ProjectArtifactVisibility): string {
  switch (value) {
    case "public-link":
      return "Public link";
    case "project":
      return "Project";
    default:
      return "Private";
  }
}

export default function ProjectArtifactsView({ token }: { token: string | null | undefined }) {
  const [projectSlug, setProjectSlug] = useState("");
  const [artifacts, setArtifacts] = useState<ProjectArtifact[]>([]);
  const [usage, setUsage] = useState<ProjectArtifactUsage | null>(null);
  const [loading, setLoading] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [message, setMessage] = useState<string | null>(null);
  const [storageEntitled, setStorageEntitled] = useState(false);
  const [title, setTitle] = useState("");
  const [kind, setKind] = useState("apk");
  const [visibility, setVisibility] = useState<ProjectArtifactVisibility>("public-link");
  const [url, setUrl] = useState("");
  const [file, setFile] = useState<File | null>(null);

  const normalizedSlug = useMemo(() => projectSlug.trim(), [projectSlug]);

  const load = useCallback(async () => {
    if (!token || !normalizedSlug) return;
    setLoading(true);
    setError(null);
    try {
      const [rows, usageRow] = await Promise.all([
        listProjectArtifacts(token, { projectSlug: normalizedSlug, limit: 80 }),
        getProjectArtifactUsage(token, { projectSlug: normalizedSlug }),
      ]);
      setArtifacts(rows);
      setUsage(usageRow);
    } catch (err) {
      setArtifacts([]);
      setUsage(null);
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setLoading(false);
    }
  }, [normalizedSlug, token]);

  useEffect(() => {
    void load();
  }, [load]);

  useEffect(() => {
    let cancelled = false;
    setStorageEntitled(false);
    if (!token) return;
    getManagedSubscription(token).then((summary) => {
      if (cancelled) return;
      const sub = summary?.subscription;
      setStorageEntitled(sub?.status === "active" && planIncludesYaverArtifactStorage(sub.plan));
    });
    return () => {
      cancelled = true;
    };
  }, [token]);

  async function addExternalLink() {
    if (!token || !normalizedSlug) return;
    setBusy(true);
    setError(null);
    setMessage(null);
    try {
      await createProjectArtifact(token, {
        projectSlug: normalizedSlug,
        title: title.trim() || url.trim().split("/").pop() || "Artifact",
        kind,
        provider: "external",
        url: url.trim(),
        visibility,
      });
      setTitle("");
      setUrl("");
      setMessage("Artifact link saved.");
      await load();
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  }

  async function uploadArtifact() {
    if (!token || !normalizedSlug || !file) return;
    if (!storageEntitled) {
      setError("Yaver artifact storage is included with Cloud Workspace. Save an external HTTPS artifact link instead.");
      return;
    }
    setBusy(true);
    setError(null);
    setMessage(null);
    try {
      const { uploadUrl, uploadIntentId } = await createProjectArtifactUploadUrl(token, {
        projectSlug: normalizedSlug,
        sizeBytes: file.size,
      });
      const uploadRes = await fetch(uploadUrl, {
        method: "POST",
        headers: { "Content-Type": file.type || "application/octet-stream" },
        body: file,
      });
      const uploadBody = await uploadRes.json().catch(() => ({}));
      if (!uploadRes.ok || !uploadBody?.storageId) {
        throw new Error(uploadBody?.error || `upload failed (${uploadRes.status})`);
      }
      await createProjectArtifact(token, {
        projectSlug: normalizedSlug,
        title: title.trim() || file.name,
        kind,
        provider: "convex",
        storageId: uploadBody.storageId,
        uploadIntentId,
        contentType: file.type || "application/octet-stream",
        sizeBytes: file.size,
        visibility,
      });
      setTitle("");
      setFile(null);
      setMessage("Artifact uploaded.");
      await load();
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  }

  async function hideArtifact(artifact: ProjectArtifact) {
    if (!token) return;
    setBusy(true);
    setError(null);
    setMessage(null);
    try {
      await hideProjectArtifact(token, artifact.id);
      setMessage("Artifact hidden.");
      await load();
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  }

  async function copyPublicLink(artifact: ProjectArtifact) {
    if (!artifact.shareToken || typeof window === "undefined") return;
    const link = `${window.location.origin}/artifacts/${encodeURIComponent(artifact.shareToken)}`;
    try {
      await window.navigator.clipboard.writeText(link);
      setMessage("Public artifact link copied.");
    } catch {
      setMessage(link);
    }
  }

  async function cleanupExpired() {
    if (!token || !normalizedSlug) return;
    setBusy(true);
    setError(null);
    setMessage(null);
    try {
      const result = await cleanupExpiredProjectArtifacts(token, { projectSlug: normalizedSlug, limit: 100 });
      setMessage(`Cleaned ${result.expired} expired artifact${result.expired === 1 ? "" : "s"}.`);
      await load();
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  }

  if (!token) {
    return <div className="p-6 text-sm text-surface-500">Sign in to manage project artifacts.</div>;
  }

  const ownerUsage = usage?.owner;
  const projectUsage = usage?.project;

  return (
    <div className="flex h-full min-h-0 flex-col gap-4 overflow-y-auto p-4 text-surface-100">
      <header className="flex flex-wrap items-start justify-between gap-3">
        <div>
          <h2 className="text-lg font-semibold">Project Artifacts</h2>
          <p className="mt-1 text-xs leading-5 text-surface-500">
            Save APKs, Hermes bundles, web previews, and shareable outputs for a project. External HTTPS links work on every plan; Yaver-hosted artifact storage is included with Cloud Workspace.
          </p>
        </div>
        <button
          type="button"
          onClick={() => void load()}
          disabled={loading || !normalizedSlug}
          className="rounded-md border border-surface-700 bg-surface-900 px-3 py-1.5 text-xs font-semibold text-surface-200 disabled:opacity-50"
        >
          {loading ? "Refreshing..." : "Refresh"}
        </button>
      </header>

      <section className="rounded border border-surface-800 bg-surface-950/40 p-3">
        <label className="block text-xs font-semibold text-surface-300" htmlFor="artifact-project-slug">
          Project slug
        </label>
        <div className="mt-2 flex flex-col gap-2 sm:flex-row">
          <input
            id="artifact-project-slug"
            value={projectSlug}
            onChange={(event) => setProjectSlug(event.target.value)}
            placeholder="my-app"
            className="min-w-0 flex-1 rounded-md border border-surface-700 bg-surface-900 px-3 py-2 text-sm text-surface-100 outline-none focus:border-brand/60"
          />
          <button
            type="button"
            onClick={() => void load()}
            disabled={loading || !normalizedSlug}
            className="rounded-md border border-surface-700 bg-surface-900 px-3 py-2 text-xs font-semibold text-surface-200 disabled:opacity-50"
          >
            Load
          </button>
        </div>
      </section>

      {error ? (
        <div className="rounded border border-rose-500/40 bg-rose-500/10 px-3 py-2 text-sm text-rose-700 dark:text-rose-200">
          {error}
        </div>
      ) : null}
      {message ? (
        <div className="rounded border border-emerald-500/30 bg-emerald-500/10 px-3 py-2 text-sm text-emerald-700 dark:text-emerald-200">
          {message}
        </div>
      ) : null}

      <section className="grid gap-3 md:grid-cols-3">
        <div className="rounded border border-surface-800 bg-surface-950/40 p-3">
          <p className="text-xs font-semibold uppercase tracking-wide text-surface-500">Project files</p>
          <p className="mt-2 text-2xl font-semibold text-surface-100">{projectUsage?.activeCount ?? 0}</p>
          <p className="mt-1 text-xs text-surface-500">{projectArtifactMeteredLabel(projectUsage)}</p>
        </div>
        <div className="rounded border border-surface-800 bg-surface-950/40 p-3">
          <p className="text-xs font-semibold uppercase tracking-wide text-surface-500">Owner storage</p>
          <p className="mt-2 text-2xl font-semibold text-surface-100">{fmtProjectArtifactBytes(ownerUsage?.totalMeteredBytes ?? ownerUsage?.storageBytes)}</p>
          <p className="mt-1 text-xs text-surface-500">{ownerArtifactStorageLabel(ownerUsage)}</p>
        </div>
        <div className="rounded border border-surface-800 bg-surface-950/40 p-3">
          <p className="text-xs font-semibold uppercase tracking-wide text-surface-500">Public links</p>
          <p className="mt-2 text-2xl font-semibold text-surface-100">{projectUsage?.publicLinkCount ?? 0}</p>
          <p className="mt-1 text-xs text-surface-500">{ownerUsage?.overQuota ? "Storage quota exceeded" : "Stored + pending bytes checked"}</p>
        </div>
      </section>

      <section className="rounded border border-surface-800 bg-surface-950/40 p-3">
        <h3 className="text-sm font-semibold text-surface-100">Add artifact</h3>
        {!storageEntitled ? (
          <p className="mt-2 rounded border border-amber-500/30 bg-amber-500/10 px-3 py-2 text-xs leading-5 text-amber-800 dark:text-amber-200">
            Uploading files to Yaver storage requires Cloud Workspace. Free and Relay Pro can save external HTTPS artifact links.
          </p>
        ) : null}
        <div className="mt-3 grid gap-3 md:grid-cols-2">
          <input
            value={title}
            onChange={(event) => setTitle(event.target.value)}
            placeholder="Title"
            className="rounded-md border border-surface-700 bg-surface-900 px-3 py-2 text-sm text-surface-100 outline-none focus:border-brand/60"
          />
          <div className="grid grid-cols-2 gap-2">
            <select
              value={kind}
              onChange={(event) => setKind(event.target.value)}
              className="rounded-md border border-surface-700 bg-surface-900 px-3 py-2 text-sm text-surface-100 outline-none focus:border-brand/60"
            >
              {KIND_OPTIONS.map((option) => (
                <option key={option} value={option}>{option}</option>
              ))}
            </select>
            <select
              value={visibility}
              onChange={(event) => setVisibility(event.target.value as ProjectArtifactVisibility)}
              className="rounded-md border border-surface-700 bg-surface-900 px-3 py-2 text-sm text-surface-100 outline-none focus:border-brand/60"
            >
              <option value="public-link">public link</option>
              <option value="project">project</option>
              <option value="private">private</option>
            </select>
          </div>
          <input
            value={url}
            onChange={(event) => setUrl(event.target.value)}
            placeholder="https://..."
            className="rounded-md border border-surface-700 bg-surface-900 px-3 py-2 text-sm text-surface-100 outline-none focus:border-brand/60"
          />
          <button
            type="button"
            onClick={() => void addExternalLink()}
            disabled={busy || !normalizedSlug || !url.trim()}
            className="rounded-md border border-sky-500/30 bg-sky-500/10 px-3 py-2 text-xs font-semibold text-sky-700 disabled:opacity-50 dark:text-sky-200"
          >
            Save Link
          </button>
          <input
            type="file"
            disabled={!storageEntitled}
            onChange={(event) => setFile(event.target.files?.[0] ?? null)}
            className="rounded-md border border-surface-700 bg-surface-900 px-3 py-2 text-sm text-surface-300 file:mr-3 file:rounded file:border-0 file:bg-surface-800 file:px-2 file:py-1 file:text-xs file:font-semibold file:text-surface-200"
          />
          <button
            type="button"
            onClick={() => void uploadArtifact()}
            disabled={busy || !normalizedSlug || !file || !storageEntitled}
            className="rounded-md border border-emerald-500/30 bg-emerald-500/10 px-3 py-2 text-xs font-semibold text-emerald-700 disabled:opacity-50 dark:text-emerald-200"
          >
            {storageEntitled ? "Upload File" : "Cloud Workspace Only"}
          </button>
        </div>
      </section>

      <section className="rounded border border-surface-800 bg-surface-950/40 p-3">
        <div className="mb-3 flex flex-wrap items-center justify-between gap-2">
          <h3 className="text-sm font-semibold text-surface-100">Artifacts</h3>
          <button
            type="button"
            onClick={() => void cleanupExpired()}
            disabled={busy || !normalizedSlug}
            className="rounded-md border border-surface-700 bg-surface-900 px-2.5 py-1 text-xs font-semibold text-surface-300 disabled:opacity-50"
          >
            Clean Expired
          </button>
        </div>
        {artifacts.length > 0 ? (
          <div className="space-y-2">
            {artifacts.map((artifact) => {
              const link = artifactLink(artifact);
              return (
                <div key={artifact.id} className="rounded border border-surface-800 bg-surface-900/60 p-3">
                  <div className="flex flex-wrap items-start justify-between gap-3">
                    <div className="min-w-0">
                      <div className="flex flex-wrap items-center gap-2">
                        <p className="truncate text-sm font-semibold text-surface-100">{artifact.title}</p>
                        <span className="rounded-full border border-surface-700 bg-surface-950 px-2 py-0.5 text-[10px] font-semibold uppercase tracking-wide text-surface-400">
                          {artifact.kind}
                        </span>
                        <span className="rounded-full border border-surface-700 bg-surface-950 px-2 py-0.5 text-[10px] font-semibold uppercase tracking-wide text-surface-400">
                          {visibilityLabel(artifact.visibility)}
                        </span>
                      </div>
                      <p className="mt-1 text-[11px] text-surface-500">
                        {artifact.provider} · {fmtProjectArtifactBytes(artifact.sizeBytes)} · {fmtDate(artifact.createdAt)}
                        {artifact.expiresAt ? ` · expires ${fmtDate(artifact.expiresAt)}` : ""}
                      </p>
                    </div>
                    <div className="flex shrink-0 flex-wrap gap-2">
                      {link ? (
                        <a
                          href={link}
                          target="_blank"
                          rel="noreferrer"
                          className="rounded-md border border-indigo-500/30 bg-indigo-500/10 px-2.5 py-1 text-xs font-semibold text-indigo-700 dark:text-indigo-200"
                        >
                          Open
                        </a>
                      ) : null}
                      {artifact.shareToken ? (
                        <button
                          type="button"
                          onClick={() => void copyPublicLink(artifact)}
                          className="rounded-md border border-sky-500/30 bg-sky-500/10 px-2.5 py-1 text-xs font-semibold text-sky-700 dark:text-sky-200"
                        >
                          Copy Link
                        </button>
                      ) : null}
                      <button
                        type="button"
                        onClick={() => void hideArtifact(artifact)}
                        disabled={busy}
                        className="rounded-md border border-surface-700 bg-surface-950 px-2.5 py-1 text-xs font-semibold text-surface-400 disabled:opacity-50"
                      >
                        Hide
                      </button>
                    </div>
                  </div>
                </div>
              );
            })}
          </div>
        ) : (
          <p className="text-sm text-surface-500">
            {normalizedSlug ? "No artifacts for this project yet." : "Enter a project slug to load artifacts."}
          </p>
        )}
      </section>
    </div>
  );
}
