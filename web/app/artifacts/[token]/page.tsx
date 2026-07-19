"use client";

import { useEffect, useState } from "react";
import { useParams } from "next/navigation";
import { getPublicProjectArtifact, type ProjectArtifact } from "@/lib/task-placement";

function fmtBytes(bytes?: number | null): string {
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

function fmtDate(ts?: number | null): string {
  return ts ? new Date(ts).toLocaleString() : "";
}

export default function PublicProjectArtifactPage() {
  const params = useParams<{ token: string }>();
  const token = String(params?.token || "");
  const [artifact, setArtifact] = useState<ProjectArtifact | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;
    setLoading(true);
    setError(null);
    getPublicProjectArtifact(token)
      .then((row) => {
        if (!cancelled) setArtifact(row);
      })
      .catch((err) => {
        if (!cancelled) setError(err instanceof Error ? err.message : String(err));
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [token]);

  return (
    <main className="min-h-screen bg-surface-950 px-4 py-10 text-surface-100">
      <div className="mx-auto max-w-xl">
        <a href="/" className="text-xl font-bold tracking-tight text-surface-50">
          yaver<span className="font-normal text-surface-500">.io</span>
        </a>
        <section className="mt-8 rounded border border-surface-800 bg-surface-900/60 p-5">
          {loading ? (
            <p className="text-sm text-surface-500">Loading artifact...</p>
          ) : error || !artifact ? (
            <div>
              <h1 className="text-lg font-semibold text-surface-100">Artifact unavailable</h1>
              <p className="mt-2 text-sm text-surface-500">
                {error || "This link is invalid, expired, or no longer public."}
              </p>
            </div>
          ) : (
            <div>
              <div className="flex flex-wrap items-center gap-2">
                <span className="rounded-full border border-surface-700 bg-surface-950 px-2 py-0.5 text-[10px] font-semibold uppercase tracking-wide text-surface-400">
                  {artifact.kind}
                </span>
                <span className="rounded-full border border-surface-700 bg-surface-950 px-2 py-0.5 text-[10px] font-semibold uppercase tracking-wide text-surface-400">
                  {artifact.projectSlug}
                </span>
              </div>
              <h1 className="mt-3 text-2xl font-semibold text-surface-50">{artifact.title}</h1>
              {artifact.description ? (
                <p className="mt-2 text-sm leading-6 text-surface-400">{artifact.description}</p>
              ) : null}
              <p className="mt-3 text-xs text-surface-500">
                {artifact.provider} · {fmtBytes(artifact.sizeBytes)} · saved {fmtDate(artifact.createdAt)}
                {artifact.shareUrlExpiresAt ? ` · link expires ${fmtDate(artifact.shareUrlExpiresAt)}` : ""}
              </p>
              {artifact.url ? (
                <a
                  href={artifact.url}
                  className="mt-5 inline-flex rounded-md border border-emerald-500/30 bg-emerald-500/10 px-3 py-2 text-sm font-semibold text-emerald-700 dark:text-emerald-200"
                >
                  Open artifact
                </a>
              ) : (
                <p className="mt-5 text-sm text-surface-500">No downloadable URL is attached to this artifact.</p>
              )}
            </div>
          )}
        </section>
      </div>
    </main>
  );
}
