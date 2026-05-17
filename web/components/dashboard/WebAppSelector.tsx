"use client";

// WebAppSelector — dropdown populated from /workspace/apps, filtered
// to web kinds. Surfaces each app's stack, monorepo path,
// and missing env vars so the user knows what they're picking.

import type { WorkspaceAppView } from "@/lib/agent-client";

interface Props {
  apps: WorkspaceAppView[];
  selectedApp: string | null;
  activeApp: string | null; // the app whose dev server is currently running
  onSelect: (name: string) => void;
  loading?: boolean;
  error?: string | null;
}

export function WebAppSelector({ apps, selectedApp, activeApp, onSelect, loading, error }: Props) {
  if (loading) {
    return (
      <div className="rounded-md border border-surface-800 bg-surface-900/40 px-3 py-4 text-[11px] text-surface-500">
        Loading workspace…
      </div>
    );
  }

  if (error) {
    return (
      <div className="rounded-md border border-amber-500/30 bg-amber-500/5 px-3 py-3 text-[11px]">
        <p className="font-medium text-amber-300">No workspace manifest</p>
        <p className="mt-1 text-amber-200/70">{error}</p>
        <p className="mt-2 text-amber-200/50">
          Create <code className="rounded bg-amber-500/10 px-1 py-0.5">yaver.workspace.yaml</code> at
          the repo root, or run <code className="rounded bg-amber-500/10 px-1 py-0.5">yaver workspace init --scaffold</code> on
          the remote machine.
        </p>
      </div>
    );
  }

  if (apps.length === 0) {
    return (
      <div className="rounded-md border border-surface-800 bg-surface-900/40 px-3 py-3 text-[11px] text-surface-500">
        No web apps declared. Add an app with <code className="rounded bg-surface-800 px-1 py-0.5 text-surface-300">stack:
          nextjs</code> (or vite / flutter) to <code className="rounded bg-surface-800 px-1 py-0.5 text-surface-300">yaver.workspace.yaml</code>.
      </div>
    );
  }

  return (
    <div className="space-y-1">
      {apps.map((app) => {
        const isSelected = selectedApp === app.name;
        const isActive = activeApp === app.name;
        const missingEnv = (app.envMissing ?? []).length;
        return (
          <button
            key={app.name}
            onClick={() => onSelect(app.name)}
            disabled={!app.exists}
            className={`group flex w-full items-center justify-between gap-2 rounded-md border px-2.5 py-2 text-left transition-colors ${
              isSelected
                ? "border-indigo-500/40 bg-indigo-500/10"
                : "border-surface-800 bg-surface-900/40 hover:border-surface-700 hover:bg-surface-900"
            } ${!app.exists ? "opacity-50" : ""}`}
            title={app.absPath || app.path}
          >
            <div className="min-w-0 flex-1">
              <div className="flex items-center gap-2">
                <span className={`truncate text-xs font-medium ${isSelected ? "text-indigo-200" : "text-surface-100"}`}>
                  {app.name}
                </span>
                {isActive && (
                  <span className="flex items-center gap-1 text-[10px] text-emerald-300">
                    <span className="h-1.5 w-1.5 rounded-full bg-emerald-400" />
                    running
                  </span>
                )}
              </div>
              <div className="mt-0.5 flex items-center gap-2 text-[10px] text-surface-500">
                <span className="rounded bg-surface-800 px-1 py-px uppercase tracking-wide">
                  {app.stack || "?"}
                </span>
                <span className="truncate">{app.path}</span>
              </div>
              {missingEnv > 0 && (
                <div className="mt-1 text-[10px] text-amber-400">
                  ⚠ {missingEnv} env var{missingEnv === 1 ? "" : "s"} missing
                </div>
              )}
              {!app.exists && (
                <div className="mt-1 text-[10px] text-surface-500">not on disk</div>
              )}
            </div>
            {app.kind && (
              <span className={`shrink-0 rounded px-1.5 py-0.5 text-[9px] uppercase tracking-widest ${
                app.kind === "web"
                  ? "bg-emerald-500/10 text-emerald-300"
                  : "bg-surface-800 text-surface-400"
              }`}>
                {app.kind}
              </span>
            )}
          </button>
        );
      })}
    </div>
  );
}
